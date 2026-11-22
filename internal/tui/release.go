package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"seraph/internal/md"
	"seraph/internal/orch"
	"seraph/internal/store"
	"seraph/internal/workspace"
)

type releaseState struct {
	kind      string
	id        string
	target    orch.ReleaseTarget
	targetErr string
	back      screen
	armed     string
	checkErr  string
	ahead     int
	checkedAt time.Time
	report    bool
	vp        viewport.Model
}

func (m *Model) openRelease(kind, id string, back screen) tea.Cmd {
	m.release = &releaseState{kind: kind, id: id, back: back}
	m.refreshRelease(true)
	m.scr = scrRelease
	return nil
}

func (m *Model) refreshRelease(force bool) {
	r := m.release
	if r == nil || (!force && time.Since(r.checkedAt) < 2*time.Second) {
		return
	}
	r.checkedAt = time.Now()
	tg, err := m.mgr.ReleaseTarget(r.kind, r.id)
	r.targetErr = ""
	if err != nil {
		r.targetErr = err.Error()
		if rel, ok := m.mgr.Release(r.kind, r.id); ok {
			tg = orch.ReleaseTarget{Kind: r.kind, ID: r.id, Title: rel.Title, Repo: rel.Repo, Branch: rel.Branch, Base: rel.Base}
		}
	}
	r.target = tg
	r.checkErr = ""
	if tg.Repo != "" {
		if err := workspace.MergeCheck(tg.Repo, tg.Base); err != nil {
			r.checkErr = err.Error()
		}
		r.ahead = workspace.Ahead(tg.Repo, tg.Base, tg.Branch)
	}
}

func releaseBadge(rel store.Release, ok bool) string {
	switch {
	case !ok:
		return ""
	case !rel.PushedAt.IsZero():
		return "↑ pushed"
	case !rel.MergedAt.IsZero():
		return "⇢ merged"
	case rel.Status == store.ReleasePass:
		return "✓ validated"
	case rel.Status == store.ReleaseFail:
		return "✗ validation failed"
	case rel.Status == store.ReleaseValidating:
		return "⟡ validating"
	case rel.Status == store.ReleaseError:
		return "✗ validator error"
	}
	return ""
}

func stepMark(state int) string {
	switch state {
	case 1:
		return sMint.Render("✓")
	case 2:
		return sRose.Render("✗")
	case 3:
		return sCyan.Render("⟡")
	}
	return sFaint.Render("○")
}

func (m *Model) releaseView() string {
	r := m.release
	m.refreshRelease(false)
	w := m.modalWidth() - 4
	rel, has := m.mgr.Release(r.kind, r.id)
	if r.report && has {
		return m.releaseReportView(rel)
	}
	tg := r.target
	var lines []string
	if tg.Repo != "" {
		lines = append(lines,
			sDim.Render("repo   ")+sIvory.Render(tildeHome(tg.Repo)),
			sDim.Render("branch ")+sLavender.Render(tg.Branch)+sDim.Render(" → ")+sGoldB.Render(tg.Base)+
				sFaint.Render(fmt.Sprintf("  · %d commit(s) ahead", r.ahead)), "")
	}
	if r.targetErr != "" && !has {
		lines = append(lines, sRose.Render(trunc("✗ "+r.targetErr, w)), "")
	}

	vState, vText := 0, sDim.Render("not validated yet: an independent agent checks the request, build, tests and diff")
	if has {
		switch rel.Status {
		case store.ReleaseValidating:
			vState, vText = 3, sCyan.Render("validating… "+sparkle[(m.frame/3)%len(sparkle)])
		case store.ReleasePass:
			vState, vText = 1, sMint.Render("PASS")+sFaint.Render(" · "+agoText(rel.UpdatedAt))
		case store.ReleaseFail:
			vState, vText = 2, sRose.Render("FAIL")+sFaint.Render(" · "+agoText(rel.UpdatedAt))
		case store.ReleaseError:
			vState, vText = 2, sRose.Render(trunc(rel.Note, w-20))
		}
		if s, ok := m.mgr.Get(rel.SessionID); ok {
			vText += sFaint.Render(" · ") + agentGlyph(s.Agent) + " " + sFaint.Render(m.modelName(s.Agent, s.Model))
		}
	}
	lines = append(lines, stepMark(vState)+" "+sGoldB.Render("1 VALIDATE")+"   "+vText)
	if has && rel.Status != store.ReleaseValidating && rel.Report != "" {
		lines = append(lines, "   "+sFaint.Render(trunc(summaryLine(rel.Report), w-3)))
	}

	mState, mText := 0, sDim.Render("merge into "+tg.Base+" in your checkout")
	switch {
	case has && !rel.MergedAt.IsZero():
		mState, mText = 1, sMint.Render("merged "+rel.MergeSHA)+sFaint.Render(" · "+agoText(rel.MergedAt))
	case r.checkErr != "":
		mText = sRose.Render(trunc("⚠ "+r.checkErr, w-14))
	case has && rel.Status == store.ReleasePass:
		mText = sIvory.Render("ready: ") + sDim.Render("checkout on "+tg.Base+" and clean")
	}
	lines = append(lines, stepMark(mState)+" "+sGoldB.Render("2 MERGE   ")+" "+mText)

	pState, pText := 0, sDim.Render("push "+tg.Base+" to "+orDefault(workspace.Remote(tg.Repo), "(no remote)"))
	if has && !rel.PushedAt.IsZero() {
		pState, pText = 1, sMint.Render("pushed")+sFaint.Render(" · "+agoText(rel.PushedAt))
	}
	lines = append(lines, stepMark(pState)+" "+sGoldB.Render("3 PUSH    ")+" "+pText)
	if has && rel.Note != "" && rel.Status != store.ReleaseError {
		lines = append(lines, "", sGold.Render(trunc("⚠ "+rel.Note, w)))
	}
	if r.armed == "P" {
		lines = append(lines, "", sRose.Bold(true).Render(trunc("press P again to push "+tg.Base+" to "+workspace.Remote(tg.Repo), w)))
	}

	chips := []string{m.chip("enter", "⏎", "validate")}
	if has && rel.Report != "" {
		chips = append(chips, m.chip("a", "a", "report"))
	}
	if has && rel.Status == store.ReleaseFail && r.kind == store.TargetSession {
		chips = append(chips, m.chip("f", "f", "send findings"))
	}
	if has && rel.Status == store.ReleasePass && rel.MergedAt.IsZero() {
		chips = append(chips, m.chip("m", "m", "merge"))
	}
	if has && !rel.MergedAt.IsZero() && rel.PushedAt.IsZero() {
		chips = append(chips, m.chip("P", "P", "push"))
	}
	chips = append(chips, m.chip("esc", "esc", "back"))
	body := strings.Join(lines, "\n") + "\n\n" + hints(w, chips...)
	return m.overlay(modal("⇪ RELEASE · "+trunc(tg.Title, w-14), body, m.modalWidth()))
}

func summaryLine(report string) string {
	n, first := 0, ""
	for _, l := range strings.Split(report, "\n") {
		l = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(l), "-*+0123456789.) "))
		if strings.HasPrefix(l, "[blocking]") {
			if n == 0 {
				first = l
			}
			n++
		}
	}
	switch n {
	case 0:
		return "no blocking problems reported"
	case 1:
		return first
	}
	return fmt.Sprintf("%d blocking problems · first: %s", n, first)
}

func agoText(ts time.Time) string {
	if a := ago(ts); a != "now" {
		return a + " ago"
	}
	return "just now"
}

func (m *Model) releaseReportView(rel store.Release) string {
	r := m.release
	r.vp.Width, r.vp.Height = m.w, max(m.h-6, 3)
	head := sGoldB.Render("◈ VALIDATOR REPORT") + sFaint.Render(" · "+trunc(rel.Title, m.w/2))
	keys := hints(m.w, key("↑↓ pgup pgdn", "scroll"), m.chip("esc", "esc", "back"))
	return m.header() + "\n" + trunc(head, m.w) + "\n" + rule(m.w) + "\n" + r.vp.View() + "\n" + keys
}

func (m *Model) releaseKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	r := m.release
	key := k.String()
	if r.report {
		if key == "esc" || key == "q" || key == "a" {
			r.report = false
			return m, nil
		}
		var cmd tea.Cmd
		r.vp, cmd = r.vp.Update(k)
		return m, cmd
	}
	if key != r.armed {
		r.armed = ""
	}
	var err error
	switch key {
	case "esc", "q":
		m.scr = r.back
		m.release = nil
		if m.scr == scrMissions && m.missions != nil {
			m.reloadMissions(true)
		}
		return m, nil
	case "enter", "V":
		if _, err = m.mgr.Validate(r.kind, r.id); err == nil {
			m.setFlash("validator started: it checks the request, build, tests and diff", false)
		}
	case "a":
		if rel, ok := m.mgr.Release(r.kind, r.id); ok && rel.Report != "" {
			r.vp = viewport.New(m.w, max(m.h-6, 3))
			r.vp.SetContent(md.Render(rel.Report, m.w-2))
			r.report = true
		}
	case "f":
		if err = m.mgr.SendFindings(r.kind, r.id); err == nil {
			m.setFlash("findings sent to the worker; validate again when it finishes", false)
			m.scr, m.release = r.back, nil
			return m, nil
		}
	case "m":
		var rel store.Release
		if rel, err = m.mgr.Merge(r.kind, r.id); err == nil {
			m.setFlash("merged into "+rel.Base+" ("+rel.MergeSHA+")", false)
		}
	case "P":
		if r.armed != "P" {
			r.armed = "P"
			return m, nil
		}
		var rel store.Release
		if rel, err = m.mgr.Push(r.kind, r.id); err == nil {
			m.setFlash("pushed "+rel.Base, false)
		}
		r.armed = ""
	}
	if err != nil {
		m.setFlash(err.Error(), true)
	}
	m.refreshRelease(true)
	m.refreshStats()
	return m, nil
}
