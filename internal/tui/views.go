package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"

	"seraph/internal/agent"
	"seraph/internal/store"
	t "seraph/internal/theme"
)

func (m *Model) View() string {
	if m.w == 0 || m.h == 0 {
		return ""
	}
	m.zoneIDs = m.zoneIDs[:0]
	return zone.Scan(m.screen())
}

func (m *Model) screen() string {
	switch m.scr {
	case scrSplash:
		return m.splashView()
	case scrDetail:
		return m.detailView()
	case scrNew:
		return m.overlay(m.composeView("☩ SUMMON A WORKER", "summon"))
	case scrPlanGoal:
		return m.overlay(m.composeView("✦ DIVINE A PLAN", "plan"))
	case scrPlanning:
		return m.overlay(m.planningView())
	case scrPlanReview:
		return m.overlay(m.reviewView())
	case scrConfirm:
		return m.overlay(m.confirmView())
	case scrMenu:
		return m.overlay(m.menuView())
	case scrMCP:
		return m.mcpView()
	}
	return m.boardView()
}

func (m *Model) overlay(box string) string {
	return lipgloss.Place(m.w, m.h, lipgloss.Center, lipgloss.Center, box)
}

func (m *Model) header() string {
	left := sGoldB.Render("✧ SERAPH") + "  " + sDim.Render(m.repoText)
	ls := m.lanes()
	counts := sCyan.Render(fmt.Sprintf("⟡ %d", len(ls[laneFlight]))) + "  " +
		sRose.Render(fmt.Sprintf("☩ %d", len(ls[laneNeeds]))) + "  " +
		sGold.Render(fmt.Sprintf("✦ %d", len(ls[laneAscended])))
	right := agentLabel(m.opt.Agent) + sDim.Render(" · ") + approvalLabel(string(m.opt.Approval)) + sDim.Render("   ") + counts
	gap := max(m.w-lipgloss.Width(left)-lipgloss.Width(right)-1, 1)
	return left + strings.Repeat(" ", gap) + right + "\n" + rule(m.w)
}

func (m *Model) flashLine() string {
	if m.flash == "" || time.Since(m.flashAt) > 6*time.Second {
		return ""
	}
	if m.flashErr {
		return sRose.Render("✗ " + trunc(m.flash, m.w-3))
	}
	return sGold.Render("✦ ") + sIvory.Render(trunc(m.flash, m.w-3))
}

func hints(width int, items ...string) string {
	line := strings.Join(items, sFaint.Render("  ·  "))
	if lipgloss.Width(line) > width {
		return lipgloss.NewStyle().MaxWidth(width).Render(line)
	}
	return line
}

func (m *Model) boardView() string {
	bodyH := max(m.h-5, 4)
	ls := m.lanes()

	var body string
	if len(ls[0])+len(ls[1])+len(ls[2]) == 0 {
		body = m.emptyView(bodyH)
	} else {
		gap := 2
		lw := (m.w - 2*gap) / 3
		cols := make([]string, 0, 5)
		for l := lane(0); l < laneCount; l++ {
			if l > 0 {
				cols = append(cols, strings.Repeat(" ", gap))
			}
			cols = append(cols, m.laneView(l, ls[l], lw, bodyH))
		}
		body = lipgloss.JoinHorizontal(lipgloss.Top, cols...)
	}

	keys := hints(m.w,
		m.chip("m", "☰", "menu"), m.chip("n", "n", "summon"), m.chip("p", "p", "plan"), m.chip("enter", "⏎", "open"), m.chip("c", "c", "mcp"),
		m.chip("r", "r", "resume"), m.chip("x", "x", "stop"), m.chip("d", "d", "release"),
		m.chip("a", "a", "agent"), m.chip("y", "y", "mode"), m.chip("q", "q", "quit"))
	return m.header() + "\n" + lipgloss.NewStyle().Height(bodyH).MaxHeight(bodyH).Render(body) + "\n" + m.flashLine() + "\n" + keys
}

func (m *Model) emptyView(h int) string {
	msg := lipgloss.JoinVertical(lipgloss.Center,
		sGold.Render("✧  the heavens are quiet  ✧"),
		"",
		sDim.Render("press ")+sGold.Render("n")+sDim.Render(" to summon a worker or ")+sGold.Render("p")+sDim.Render(" to divine a plan"),
	)
	return lipgloss.Place(m.w, h, lipgloss.Center, lipgloss.Center, msg)
}

func (m *Model) laneView(l lane, items []store.Session, w, h int) string {
	active := l == m.lane
	color := laneColors[l]
	name := laneNames[l]
	head := m.mark(fmt.Sprintf("lane:%d", l), t.FG(color).Bold(true).Render(name)+sDim.Render(fmt.Sprintf("  %d", len(items))))
	underline := t.FG(t.Lerp(color, t.Void, 0.6)).Render(strings.Repeat("━", w))
	if active {
		underline = t.FG(color).Render(strings.Repeat("━", w))
	}
	lines := []string{head, underline}

	const cardH = 5
	maxCards := max((h-3)/cardH, 1)
	sel := m.idx[l]
	off := 0
	if sel >= maxCards {
		off = sel - maxCards + 1
	}
	end := min(off+maxCards, len(items))
	for i := off; i < end; i++ {
		lines = append(lines, m.card(items[i], w, active && i == sel, color))
	}
	if more := len(items) - end; more > 0 {
		lines = append(lines, sDim.Render(fmt.Sprintf("  ⋯ %d more", more)))
	}
	if len(items) == 0 {
		lines = append(lines, "", sFaint.Render("  "+emptyLane(l)))
	}
	return lipgloss.NewStyle().Width(w).Render(strings.Join(lines, "\n"))
}

func emptyLane(l lane) string {
	switch l {
	case laneFlight:
		return "no one in flight"
	case laneNeeds:
		return "nothing awaits you"
	}
	return "none ascended yet"
}

func (m *Model) card(s store.Session, w int, selected bool, laneColor t.RGB) string {
	inner := max(w-4, 4)
	l1 := agentGlyph(s.Agent) + " " + sTitle.Render(trunc(s.Title, inner-2))

	var l2 string
	switch s.Status {
	case store.StatusWorking, store.StatusQueued:
		line := s.LastLine
		if line == "" {
			line = "warming its wings…"
		}
		l2 = sCyan.Render(sparkle[(m.frame/3)%len(sparkle)]) + " " + sDim.Render(trunc(line, inner-2))
	case store.StatusFailed:
		line := s.Error
		if line == "" {
			line = s.LastLine
		}
		l2 = sRose.Render("✗ " + trunc(line, inner-2))
	case store.StatusStopped:
		l2 = sLavender.Render("◇ ") + sDim.Render(trunc(orDefault(s.LastLine, "paused"), inner-2))
	default:
		l2 = sGold.Render("✦ ") + sIvory.Render(trunc(orDefault(s.LastLine, "complete"), inner-2))
	}

	meta := s.ID
	if s.Branch != "" {
		meta += " · " + s.Branch
	} else {
		meta += " · shared dir"
	}
	if s.Cost > 0 {
		meta += " · " + money(s.Cost)
	}
	if s.Approval == string(agent.ApprovalYolo) {
		meta += " · yolo"
	}
	l3 := sFaint.Render(trunc(meta, inner))

	border := lipgloss.RoundedBorder()
	bc := t.Lerp(laneColor, t.Void, 0.55)
	switch {
	case selected:
		border = lipgloss.ThickBorder()
		bc = t.Gold
	case m.hover == "card:"+s.ID:
		bc = t.Lavender
	}
	return m.mark("card:"+s.ID, lipgloss.NewStyle().
		Border(border).
		BorderForeground(bc.Color()).
		Padding(0, 1).
		Width(w-2).
		Render(l1+"\n"+l2+"\n"+l3))
}

func orDefault(s, d string) string {
	if strings.TrimSpace(s) == "" {
		return d
	}
	return s
}

func (m *Model) detailView() string {
	s, ok := m.mgr.Get(m.openID)
	if !ok {
		return m.boardView()
	}
	info1 := agentLabel(s.Agent) + "  " + sTitle.Render(trunc(s.Title, m.w/2)) + "  " + statusPill(s.Status, m.frame)
	where := sDim.Render("dir ") + sDim.Render(s.Dir)
	if s.Branch != "" {
		where = sDim.Render("branch ") + sLavender.Render(s.Branch) + sDim.Render(" off "+orDefault(s.Base, "HEAD")) + sDim.Render("  ·  ") + sMint.Render(orDefault(m.wsSummary, "…")) + sDim.Render("  ·  ") + sFaint.Render(s.Dir)
	}
	if s.Cost > 0 {
		where += sDim.Render("  ·  ") + sGold.Render(fmt.Sprintf("$%.3f", s.Cost))
	}
	info3 := m.flashLine()
	if info3 == "" && s.Error != "" && s.Status != store.StatusWorking {
		info3 = sRose.Render("✗ " + trunc(s.Error, m.w-3))
	}

	keys := hints(m.w, m.chip("enter", "⏎", "send"), m.chip("esc", "esc", "back"), key("↑↓ wheel", "scroll"),
		m.chip("ctrl+x", "ctrl+x", "stop"), m.chip("ctrl+r", "ctrl+r", "resume"))
	return strings.Join([]string{
		m.header(),
		lipgloss.NewStyle().MaxWidth(m.w).Render(info1),
		lipgloss.NewStyle().MaxWidth(m.w).Render(where),
		info3,
		m.vp.View(),
		rule(m.w),
		m.fi.View(),
		keys,
	}, "\n")
}

func renderTranscript(evs []agent.Event, agentName string, width int) string {
	w := max(width-2, 10)
	wrap := func(st lipgloss.Style, s string) string {
		return st.Width(w).Render(strings.TrimRight(s, "\n"))
	}
	var b strings.Builder
	var prev agent.Kind
	for _, ev := range evs {
		ev.Text = clean(ev.Text)
		switch ev.Kind {
		case agent.KindUser:
			b.WriteString("\n" + sLavender.Bold(true).Render("☩ you") + "\n")
			b.WriteString(wrap(sIvory, ev.Text) + "\n")
		case agent.KindText:
			if prev != agent.KindText {
				b.WriteString("\n" + sGoldB.Render("✧ "+agentName) + "\n")
			}
			b.WriteString(wrap(sIvory, ev.Text) + "\n")
		case agent.KindThinking:
			text := strings.TrimSpace(ev.Text)
			if r := []rune(text); len(r) > 600 {
				text = "…" + string(r[len(r)-600:])
			}
			if text != "" {
				b.WriteString(wrap(sThink, "∴ "+text) + "\n")
			}
		case agent.KindToolStart:
			name := sCyan.Render("  ⟢ " + ev.Tool)
			b.WriteString(name + " " + sDim.Render(trunc(ev.Text, max(w-lipgloss.Width(name)-1, 4))) + "\n")
		case agent.KindToolEnd:
			if ev.IsError {
				b.WriteString(sRose.Render("    ✗ "+trunc(orDefault(ev.Text, "tool failed"), w-6)) + "\n")
			}
		case agent.KindResult:
			if ev.IsError {
				b.WriteString(wrap(sRose, "✗ "+orDefault(ev.Text, "the agent reported an error")) + "\n")
			} else {
				line := "✦ complete"
				if ev.Cost > 0 {
					line += fmt.Sprintf(" · $%.4f", ev.Cost)
				}
				b.WriteString("\n" + sGold.Render(line) + "\n")
			}
		case agent.KindError:
			b.WriteString(wrap(sRose, "✗ "+ev.Text) + "\n")
		case agent.KindSystem:
			b.WriteString(sMint.Render("  ⎇ "+trunc(ev.Text, w-4)) + "\n")
		}
		if ev.Kind != agent.KindInit {
			prev = ev.Kind
		}
	}
	return strings.TrimLeft(b.String(), "\n")
}

func (m *Model) targetLine() string {
	if m.opt.Repo == "" {
		return sRose.Render("no git repo: workers share " + m.opt.Dir)
	}
	return sDim.Render("each worker gets its own worktree off ") + sLavender.Render(m.repoText)
}

func (m *Model) composeView(title, submit string) string {
	opts := sDim.Render("agent ") + m.chip("tab", "tab", "") + " " + m.mark("key:tab", agentLabel(m.opt.Agent)) +
		sDim.Render("     mode ") + m.chip("ctrl+y", "ctrl+y", "") + " " + m.mark("key:ctrl+y", approvalLabel(string(m.opt.Approval)))
	body := strings.Join([]string{
		m.ta.View(),
		"",
		opts,
		m.targetLine(),
		"",
		hints(m.modalWidth()-4, m.chip("enter", "⏎", submit), m.chip("alt+enter", "alt+⏎", "newline"), m.chip("esc", "esc", "cancel")),
	}, "\n")
	return modal(title, body, m.modalWidth())
}

func (m *Model) planningView() string {
	el := time.Since(m.planStart).Round(time.Second)
	spin := sparkle[(m.frame/3)%len(sparkle)]
	body := sGoldB.Render(spin+" the choir deliberates "+spin) + "\n\n" +
		sDim.Render("planner ") + agentLabel(m.opt.Agent) + sDim.Render(fmt.Sprintf(" · read-only · %s", el)) + "\n\n" +
		sIvory.Render(trunc(m.planGoal, m.modalWidth()-6)) + "\n\n" +
		hints(m.modalWidth()-4, m.chip("esc", "esc", "cancel"))
	return modal("✦ DIVINING", body, m.modalWidth())
}

func (m *Model) reviewView() string {
	w := m.modalWidth() - 4
	var rows []string
	for i, tk := range m.plan {
		cursor := "  "
		if i == m.planSel {
			cursor = sGold.Render("› ")
		}
		box := sGold.Render("[✦]")
		title := sTitle.Render(trunc(tk.Title, w-12))
		if tk.Skip {
			box = sFaint.Render("[ ]")
			title = sFaint.Render(trunc(tk.Title, w-12))
		}
		desc := strings.Join(strings.Fields(tk.Task), " ")
		item := cursor + box + " " + agentGlyph(tk.Agent) + " " + title + "\n" + sDim.Width(w-6).MaxHeight(2).Render(desc)
		rows = append(rows, strings.Split(m.mark(fmt.Sprintf("plan:%d", i), item), "\n")...)
		rows = append(rows, "")
	}
	maxRows := max(m.h-14, 6)
	if len(rows) > maxRows {
		start := min(max(m.planSel*3-maxRows/2, 0), len(rows)-maxRows)
		rows = rows[start : start+maxRows]
	}
	body := strings.Join(rows, "\n") + "\n" +
		sDim.Render("mode ") + approvalLabel(string(m.opt.Approval)) + "\n\n" +
		hints(w, m.chip("enter", "⏎", "summon all"), m.chip(" ", "space", "toggle"), m.chip("tab", "tab", "agent"),
			m.chip("y", "y", "mode"), m.chip("esc", "esc", "discard"))
	return modal(fmt.Sprintf("✦ THE PLAN · %d TASKS", len(m.plan)), body, m.modalWidth())
}

func (m *Model) confirmView() string {
	s, _ := m.mgr.Get(m.confirmID)
	body := sIvory.Render("Release "+s.ID+" · "+trunc(s.Title, 50)+"?") + "\n\n"
	if s.Worktree {
		body += sDim.Render("Removes the worktree "+s.Dir+".\nBranch ") + sLavender.Render(s.Branch) + sDim.Render(" is kept, so committed work stays.") + "\n"
		body += sRose.Render("Uncommitted changes in the worktree will be lost.") + "\n"
	} else {
		body += sDim.Render("Removes the session record and log. Files are untouched.") + "\n"
	}
	body += "\n" + hints(m.modalWidth()-4, m.chip("y", "y", "release"), m.chip("n", "n", "keep"))
	return modal("☩ RELEASE WORKER", body, min(m.modalWidth(), 72))
}

func money(c float64) string {
	if c < 1 {
		return fmt.Sprintf("$%.3f", c)
	}
	return fmt.Sprintf("$%.2f", c)
}
