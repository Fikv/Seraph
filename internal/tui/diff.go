package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"seraph/internal/store"
	"seraph/internal/workspace"
)

const askHunkLimit = 160

type diffState struct {
	sessionID string
	session   store.Session
	res       workspace.Diff
	err       string
	fileSel   int
	focus     int
	scroll    int
	hunk      int
	ask       textinput.Model
	asking    bool
	askWhole  bool
	back      screen
}

type diffRow struct {
	text string
	hunk int
}

func (m *Model) openDiff(sessionID string, back screen) tea.Cmd {
	s, ok := m.mgr.Get(sessionID)
	if !ok {
		m.setFlash("no session "+sessionID, true)
		return nil
	}
	in := textinput.New()
	in.Prompt = "? "
	in.PromptStyle = sGold
	in.TextStyle = sIvory
	in.PlaceholderStyle = sFaint
	in.CharLimit = 2000
	d := &diffState{sessionID: sessionID, session: s, ask: in, back: back}
	m.diff = d
	m.reloadDiff()
	m.scr = scrDiff
	return nil
}

func (m *Model) reloadDiff() {
	d := m.diff
	s := d.session
	var res workspace.Diff
	var err error
	switch {
	case s.Worktree:
		res, err = workspace.BranchDiff(s.Repo, s.Dir, s.Branch, s.Base)
	default:
		res, err = workspace.BranchDiff(s.Dir, s.Dir, "HEAD", "HEAD")
	}
	d.res, d.err = res, ""
	if err != nil {
		d.err = err.Error()
	}
	d.fileSel = min(d.fileSel, max(len(res.Files)-1, 0))
	d.hunk, d.scroll = 0, 0
}

func (m *Model) currentFile() (workspace.FileDiff, bool) {
	d := m.diff
	if d == nil || len(d.res.Files) == 0 {
		return workspace.FileDiff{}, false
	}
	return d.res.Files[d.fileSel], true
}

func fileGlyph(status string) string {
	switch status {
	case "added":
		return sMint.Render("+")
	case "deleted":
		return sRose.Render("−")
	case "renamed":
		return sLavender.Render("→")
	}
	return sGold.Render("~")
}

func (m *Model) diffRows(f workspace.FileDiff, w int) []diffRow {
	d := m.diff
	var rows []diffRow
	if f.Binary {
		return []diffRow{{text: sDim.Render("binary file"), hunk: -1}}
	}
	if f.Status == "renamed" && len(f.Hunks) == 0 {
		return []diffRow{{text: sLavender.Render("renamed from " + f.OldPath), hunk: -1}}
	}
	num := func(n int) string {
		if n == 0 {
			return "     "
		}
		return fmt.Sprintf("%5d", n)
	}
	for hi, h := range f.Hunks {
		marker := "  "
		hs := sLavender
		if hi == d.hunk {
			marker, hs = sGold.Render("▶ "), sGoldB
		}
		rows = append(rows, diffRow{text: m.mark(fmt.Sprintf("hunk:%d", hi), marker+hs.Render(trunc(h.Header, w-2))), hunk: hi})
		bar := " "
		if hi == d.hunk {
			bar = sGold.Render("┃")
		}
		for _, l := range h.Lines {
			text := strings.ReplaceAll(clean(l.Text), "\n", " ")
			var style lipglossStyle
			sign := " "
			switch l.Kind {
			case '+':
				style, sign = sMint, "+"
			case '-':
				style, sign = sRose, "-"
			case '\\':
				style, sign = sFaint, "\\"
			default:
				style = sDim
			}
			gutter := sFaint.Render(num(l.Old) + " " + num(l.New) + " ")
			line := bar + gutter + style.Render(sign+text)
			rows = append(rows, diffRow{text: ansi.Truncate(line, w, "…"), hunk: hi})
		}
	}
	return rows
}

type lipglossStyle = lipgloss.Style

func (m *Model) diffLayout() (listW, diffW, bodyH int) {
	bodyH = max(m.h-9, 4)
	listW = min(max(m.w/4, 26), 44)
	diffW = m.w - listW - 3
	if diffW < 40 {
		return m.w, 0, bodyH
	}
	return listW, diffW, bodyH
}

func (m *Model) jumpToHunk(i int) {
	d := m.diff
	f, ok := m.currentFile()
	if !ok || len(f.Hunks) == 0 {
		return
	}
	d.hunk = min(max(i, 0), len(f.Hunks)-1)
	_, diffW, _ := m.diffLayout()
	for ri, r := range m.diffRows(f, max(diffW, m.w)) {
		if r.hunk == d.hunk {
			d.scroll = ri
			return
		}
	}
}

func (m *Model) diffKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	d := m.diff
	key := k.String()
	if d.asking {
		switch key {
		case "esc":
			d.asking = false
			d.ask.Blur()
			return m, nil
		case "enter":
			return m.sendAsk()
		}
		var cmd tea.Cmd
		d.ask, cmd = d.ask.Update(k)
		return m, cmd
	}
	f, _ := m.currentFile()
	_, _, bodyH := m.diffLayout()
	page := max(bodyH-3, 1)
	switch key {
	case "esc", "q":
		m.scr = d.back
		switch m.scr {
		case scrTodos:
			m.reloadTodos()
		case scrDetail:
			m.fi.Focus()
			m.refreshDetail(true)
		}
		return m, nil
	case "tab":
		d.focus = 1 - d.focus
	case "r":
		m.reloadDiff()
		m.setFlash("diff reloaded", false)
	case "?", "e":
		if len(f.Hunks) == 0 {
			return m.startAsk(true)
		}
		return m.startAsk(false)
	case "a":
		return m.startAsk(true)
	case "n", "]":
		m.jumpToHunk(d.hunk + 1)
	case "N", "[":
		m.jumpToHunk(d.hunk - 1)
	case "pgdown", "ctrl+d", " ":
		d.scroll += page
	case "pgup", "ctrl+u":
		d.scroll = max(d.scroll-page, 0)
	case "home":
		d.scroll = 0
	case "up", "k":
		if d.focus == 0 {
			if d.fileSel > 0 {
				d.fileSel--
				d.scroll, d.hunk = 0, 0
			}
		} else {
			d.scroll = max(d.scroll-1, 0)
		}
	case "down", "j":
		if d.focus == 0 {
			if d.fileSel < len(d.res.Files)-1 {
				d.fileSel++
				d.scroll, d.hunk = 0, 0
			}
		} else {
			d.scroll++
		}
	case "left", "h":
		d.focus = 0
	case "right", "l", "enter":
		d.focus = 1
	}
	return m, nil
}

func (m *Model) startAsk(whole bool) (tea.Model, tea.Cmd) {
	d := m.diff
	if m.mgr.Active(d.sessionID) {
		m.setFlash("the worker is still running: wait for it to finish", true)
		return m, nil
	}
	d.asking, d.askWhole = true, whole
	d.ask.SetValue("")
	if whole {
		d.ask.Placeholder = "ask the agent about this file (enter = explain what changed and why)"
	} else {
		d.ask.Placeholder = "ask the agent about this hunk (enter = explain what happened here)"
	}
	return m, d.ask.Focus()
}

func (m *Model) sendAsk() (tea.Model, tea.Cmd) {
	d := m.diff
	f, ok := m.currentFile()
	file, patch := "", ""
	if ok {
		file = f.Path
		if !d.askWhole && d.hunk < len(f.Hunks) {
			patch = f.Hunks[d.hunk].Patch()
			if lines := strings.Split(patch, "\n"); len(lines) > askHunkLimit {
				patch = strings.Join(lines[:askHunkLimit], "\n") + "\n… (hunk truncated)"
			}
		}
	}
	if err := m.mgr.Ask(d.sessionID, d.ask.Value(), file, patch); err != nil {
		m.setFlash(err.Error(), true)
		return m, nil
	}
	d.asking = false
	d.ask.Blur()
	m.backTo = scrDiff
	m.openDetail(d.sessionID)
	m.setFlash("asking the agent (read-only, nothing gets committed) · esc returns to the diff", false)
	return m, textinput.Blink
}

func (m *Model) diffClick(kind, arg string) (tea.Model, tea.Cmd) {
	d := m.diff
	var n int
	fmt.Sscanf(arg, "%d", &n)
	switch kind {
	case "dfile":
		if n < len(d.res.Files) {
			if n == d.fileSel {
				d.focus = 1
			} else {
				d.fileSel, d.scroll, d.hunk, d.focus = n, 0, 0, 0
			}
		}
	case "hunk":
		if n == d.hunk {
			return m.startAsk(false)
		}
		m.jumpToHunk(n)
		d.focus = 1
	}
	return m, nil
}

func (m *Model) diffView() string {
	d := m.diff
	s := d.session
	listW, diffW, bodyH := m.diffLayout()

	adds, dels := 0, 0
	for _, f := range d.res.Files {
		adds += f.Add
		dels += f.Del
	}
	info1 := sGoldB.Render("⎇ DIFF") + "  " + agentLabel(s.Agent) + "  " + sTitle.Render(trunc(s.Title, m.w/2))
	where := sLavender.Render(orDefault(s.Branch, "working tree")) + sFaint.Render(" vs ") + sDim.Render(orDefault(s.Base, "HEAD"))
	if d.res.From != "" {
		where += sFaint.Render(" @" + d.res.From[:min(7, len(d.res.From))])
	}
	where += sFaint.Render("  ·  ") + sIvory.Render(fmt.Sprintf("%d files ", len(d.res.Files))) + sMint.Render(fmt.Sprintf("+%d ", adds)) + sRose.Render(fmt.Sprintf("-%d", dels))
	if !d.res.Live && s.Worktree {
		where += sFaint.Render("  ·  worktree released, showing the branch")
	}
	var commits []string
	for _, c := range d.res.Commits {
		commits = append(commits, sGold.Render(c.SHA)+" "+sDim.Render(c.Subject))
	}
	info3 := sFaint.Render("no commits yet")
	if len(commits) > 0 {
		info3 = strings.Join(commits, sFaint.Render("  ·  "))
	}
	if len(d.res.Untracked) > 0 {
		info3 += sFaint.Render(fmt.Sprintf("  ·  %d untracked", len(d.res.Untracked)))
	}

	var rows []string
	if d.err != "" {
		rows = append(rows, sRose.Render(trunc("✗ "+d.err, listW)))
	}
	if len(d.res.Files) == 0 && d.err == "" {
		rows = append(rows, sDim.Render("no changes"))
	}
	off := 0
	if d.fileSel >= bodyH {
		off = d.fileSel - bodyH + 1
	}
	for i := off; i < len(d.res.Files) && len(rows) < bodyH; i++ {
		f := d.res.Files[i]
		cursor, style := "  ", sIvory
		if i == d.fileSel {
			cursor, style = sGold.Render("› "), sGoldB
			if d.focus == 1 {
				style = sGold
			}
		} else if m.hover == fmt.Sprintf("dfile:%d", i) {
			style = sLavender
		}
		counts := sMint.Render(fmt.Sprintf("+%d", f.Add)) + sRose.Render(fmt.Sprintf(" -%d", f.Del))
		name := trunc(f.Path, listW-lipgloss.Width(counts)-6)
		line := cursor + fileGlyph(f.Status) + " " + style.Render(name)
		pad := max(listW-lipgloss.Width(line)-lipgloss.Width(counts), 1)
		rows = append(rows, m.mark(fmt.Sprintf("dfile:%d", i), line+strings.Repeat(" ", pad)+counts))
	}
	list := lipgloss.NewStyle().Width(listW).Render(strings.Join(rows, "\n"))

	view := ""
	if diffW > 0 {
		f, ok := m.currentFile()
		var lines []string
		if ok {
			drows := m.diffRows(f, diffW)
			d.scroll = min(d.scroll, max(len(drows)-bodyH, 0))
			for _, r := range drows[d.scroll:min(d.scroll+bodyH, len(drows))] {
				lines = append(lines, r.text)
			}
		}
		view = lipgloss.NewStyle().Width(diffW).Render(strings.Join(lines, "\n"))
	}
	body := joinPanels(list, view, diffW, bodyH)

	bottom := hints(m.w, m.chip("?", "?", "ask about hunk"), m.chip("a", "a", "ask about file"), m.chip("n", "n", "next hunk"),
		m.chip("N", "N", "prev"), m.chip("tab", "tab", "files/diff"), m.chip("r", "r", "reload"), m.chip("esc", "esc", "back"))
	if d.asking {
		bottom = d.ask.View()
	}
	return strings.Join([]string{
		m.header(),
		lipgloss.NewStyle().MaxWidth(m.w).Render(info1),
		lipgloss.NewStyle().MaxWidth(m.w).Render(where),
		lipgloss.NewStyle().MaxWidth(m.w).Render(info3),
		rule(m.w),
		lipgloss.NewStyle().Height(bodyH).MaxHeight(bodyH).Render(body),
		m.flashLine(),
		bottom,
	}, "\n")
}
