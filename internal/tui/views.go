package tui

import (
	"fmt"
	"seraph/internal/workspace"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone"

	"seraph/internal/agent"
	"seraph/internal/md"
	"seraph/internal/store"
	t "seraph/internal/theme"
)

func (m *Model) View() string {
	if m.w == 0 || m.h == 0 {
		return ""
	}
	if m.skipRender && m.lastView != "" {
		m.skipRender = false
		return m.lastView
	}
	m.skipRender = false
	m.zoneIDs = m.zoneIDs[:0]
	screen := m.screen()
	if p, ok := m.currentPermission(); ok {
		screen = m.overlay(m.permissionView(p))
	}
	m.lastView = zone.Scan(screen)
	return m.lastView
}

func (m *Model) screen() string {
	switch m.scr {
	case scrSplash:
		return m.splashView()
	case scrDetail:
		return m.detailView()
	case scrNew:
		if m.composeTeam {
			return m.overlay(m.composeView("⟁ NEW ORCHESTRATOR · a lead that builds and runs its own team", "start"))
		}
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
	case scrProjects:
		return m.projectsView()
	case scrNotes:
		return m.notesView()
	case scrModels:
		return m.overlay(m.modelPickerView())
	case scrMissions:
		return m.missionsView()
	case scrRelease:
		return m.releaseView()
	case scrTeam:
		return m.teamView()
	case scrDiff:
		return m.diffView()
	case scrTodos:
		return m.todosView()
	}
	return m.boardView()
}

func (m *Model) overlay(box string) string {
	return lipgloss.Place(m.w, m.h, lipgloss.Center, lipgloss.Center, box)
}

func (m *Model) header() string {
	left := spinTitle() + "  " + m.projectLabel() + "  " + sDim.Render(m.repoText)

	ls := m.lanes()
	counts := sCyan.Render(fmt.Sprintf("⟡ %d", len(ls[laneFlight]))) + "  " +
		sRose.Render(fmt.Sprintf("☩ %d", len(ls[laneNeeds]))) + "  " +
		sGold.Render(fmt.Sprintf("✦ %d", len(ls[laneAscended])))
	if active, waiting := m.activeMissions(); active > 0 {
		mc := sLavender.Render(fmt.Sprintf("⟁ %d", active))
		if waiting > 0 {
			mc += sGold.Render(fmt.Sprintf(" ⏸%d", waiting))
		}
		counts = mc + "  " + counts
	}
	right := m.modelLabel() + sDim.Render(" · ") + approvalLabel(string(m.opt.Approval)) + sDim.Render("   ") + counts
	if lipgloss.Width(right) > m.w/2 {
		right = counts
	}
	if room := m.w - lipgloss.Width(right) - 2; lipgloss.Width(left) > room {
		left = ansi.Truncate(left, max(room, 0), "…")
	}
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
	top := m.header()
	bodyH := max(m.h-5, 4)
	if m.cornerEmblem() {
		top = m.emblemHeader()
		bodyH = max(m.h-m.logoLines()-4, 4)
	}
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
		m.chip("m", "☰", "menu"), m.chip("N", "N", "orchestrator"), m.chip("n", "n", "worker"), m.chip("p", "p", "plan"), m.chip("enter", "⏎", "open"), m.chip("g", "g", "project"), m.chip("t", "t", "todo"), m.chip("o", "o", "notes"), m.chip("v", "v", "diff"), m.chip("c", "c", "mcp"),
		m.chip("r", "r", "resume"), m.chip("x", "x", "stop"), m.chip("d", "d", "release"),
		m.chip("a", "a", "agent"), m.chip("y", "y", "mode"), m.chip("q", "q", "quit"))
	return top + "\n" + lipgloss.NewStyle().Height(bodyH).MaxHeight(bodyH).Render(body) + "\n" + m.flashLine() + "\n" + keys
}

func (m *Model) emptyView(h int) string {
	wrap := lipgloss.NewStyle().Width(max(min(m.w-4, 90), 20)).Align(lipgloss.Center)
	msg := lipgloss.JoinVertical(lipgloss.Center,
		sGold.Render(spinNow(false)+"  the heavens are quiet  "+spinNow(true)),
		"",
		wrap.Render(sDim.Render("press ")+sGold.Render("N")+sDim.Render(" for an orchestrator that plans, delegates to a team and lands the work on your branch")),
		wrap.Render(sDim.Render("or ")+sGold.Render("n")+sDim.Render(" for a single worker in its own worktree · ")+sGold.Render("p")+sDim.Render(" to divine a plan")),
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
	spin := 0
	if s.Status == store.StatusWorking || s.Status == store.StatusQueued {
		spin = (m.frame/3)%len(sparkle) + 1
	}
	ck := fmt.Sprintf("%s|%s|%s|%s|%d|%s|%s|%s|%s|%.4f|%s|%s|%s|%d|%t|%t|%v|%d", m.stats.teams[s.ID], m.stats.waitingFor[s.ID], m.stats.badges[s.ID], s.ID, s.UpdatedAt.UnixNano(), s.Status, s.Title, s.LastLine, s.Error,
		s.Cost, s.Branch, s.Approval, s.Agent, w, selected, m.hover == "card:"+s.ID, laneColor, spin)
	if box, ok := m.cardMemo[ck]; ok {
		return m.mark("card:"+s.ID, box)
	}
	box := m.renderCard(s, w, selected, laneColor)
	if m.cardMemo == nil || len(m.cardMemo) > 256 {
		m.cardMemo = map[string]string{}
	}
	m.cardMemo[ck] = box
	return m.mark("card:"+s.ID, box)
}

func (m *Model) renderCard(s store.Session, w int, selected bool, laneColor t.RGB) string {
	inner := max(w-4, 4)
	l1 := agentGlyph(s.Agent) + " " + sTitle.Render(trunc(s.Title, inner-2))

	var l2 string
	switch {
	case m.stats.waitingFor[s.ID] != "":
		l2 = sGoldB.Render("⚿ ") + sGold.Render(trunc("asks: "+m.stats.waitingFor[s.ID], inner-2))
	case s.Status == store.StatusWorking || s.Status == store.StatusQueued:
		line := s.LastLine
		if line == "" {
			line = "warming its wings…"
		}
		l2 = sCyan.Render(sparkle[(m.frame/3)%len(sparkle)]) + " " + sDim.Render(trunc(line, inner-2))
	case s.Status == store.StatusFailed:
		line := s.Error
		if line == "" {
			line = s.LastLine
		}
		l2 = sRose.Render("✗ " + trunc(line, inner-2))
	case s.Status == store.StatusStopped:
		l2 = sLavender.Render("◇ ") + sDim.Render(trunc(orDefault(s.LastLine, "paused"), inner-2))
	default:
		l2 = sGold.Render("✦ ") + sIvory.Render(trunc(orDefault(s.LastLine, "complete"), inner-2))
	}

	meta := s.ID
	if b := m.stats.badges[s.ID]; b != "" {
		meta += " · " + b
	}
	if t := m.stats.teams[s.ID]; t != "" {
		meta += " · " + t
	}
	if s.Branch != "" {
		meta += " · " + s.Branch
	} else {
		meta += " · shared dir"
	}
	if s.Cost > 0 {
		meta += " · " + money(s.Cost)
	}
	if s.Approval == string(agent.ApprovalYolo) || s.Approval == string(agent.ApprovalConfirm) {
		meta += " · " + s.Approval
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
	return lipgloss.NewStyle().
		Border(border).
		BorderForeground(bc.Color()).
		Padding(0, 1).
		Width(w - 2).
		Render(l1 + "\n" + l2 + "\n" + l3)
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
	info1 := agentLabel(s.Agent) + "  " + sTitle.Render(trunc(s.Title, m.w/2)) + "  " + statusPill(s.Status, m.frame) + "  " +
		m.mark("key:ctrl+y", approvalLabel(orDefault(s.Approval, "safe")))
	if m.chatView {
		info1 += "  " + sFaint.Render("☰ conversation only")
	}

	where := sDim.Render("dir ") + sDim.Render(s.Dir)
	if s.Branch != "" {
		where = sDim.Render("branch ") + sLavender.Render(s.Branch) + sDim.Render(" off "+orDefault(s.Base, "HEAD")) + sDim.Render("  ·  ") + sMint.Render(orDefault(m.wsSummary, "…")) + sDim.Render("  ·  ") + sFaint.Render(s.Dir)
	}
	if s.Cost > 0 {
		where += sDim.Render("  ·  ") + sGold.Render(fmt.Sprintf("$%.3f", s.Cost))
	}
	if tl := m.teamLine(s); tl != "" {
		where = tl + sDim.Render("  ·  ") + where
	}
	if s.Role == "orchestrator" && !s.Worktree {
		where = m.teamLine(s) + sDim.Render("  ·  ") + sDim.Render("in your checkout on ") + sGoldB.Render(s.Branch) + sDim.Render(" (work lands there)")
	}
	info3 := m.flashLine()
	if info3 == "" && s.Error != "" && s.Status != store.StatusWorking {
		info3 = sRose.Render("✗ " + trunc(s.Error, m.w-3))
	}
	if info3 == "" && s.TeamID == "" && s.Role == "" && s.Turns >= 5 {
		info3 = sFaint.Render(trunc("long conversation with one worker? N starts an orchestrator that delegates to a team, keeps TEAM.md and lands on your branch", m.w))
	}

	selLabel := "select text"
	if m.selectMode {
		selLabel = "mouse back"
	}
	viewLabel := "hide thinking"
	if m.chatView {
		viewLabel = "show thinking"
	}
	keys := hints(m.w, m.chip("enter", "⏎", "send"), m.chip("esc", "esc", "back"), m.chip("tab", "tab", viewLabel), m.chip("ctrl+t", "ctrl+t", selLabel), m.chip("ctrl+o", "ctrl+o", "copy answer"),
		m.chip("ctrl+y", "ctrl+y", "mode"), m.chip("ctrl+g", "ctrl+g", "diff"), key("↑↓ wheel", "scroll"), m.chip("ctrl+x", "ctrl+x", "stop"), m.chip("ctrl+r", "ctrl+r", "resume"))
	if m.selectMode {
		keys = hints(m.w, sGold.Render("✂ SELECT MODE"), sDim.Render("drag to select, ctrl+shift+c copies"), m.chip("ctrl+t", "ctrl+t", "mouse back"))
	}
	divider := rule(m.w)
	switch {
	case m.stats.waitingFor[s.ID] != "":
		divider = sGoldB.Render("⚿ waiting for your approval: ") + sIvory.Render(trunc(m.stats.waitingFor[s.ID], max(m.w-30, 5)))
	case (s.Status == store.StatusWorking || s.Status == store.StatusQueued) && m.mgr.Active(s.ID):
		divider = workingBar(m.w, time.Since(spinEpoch))
	}
	return strings.Join([]string{
		m.header(),
		lipgloss.NewStyle().MaxWidth(m.w).Render(info1),
		lipgloss.NewStyle().MaxWidth(m.w).Render(where),
		info3,
		m.vp.View(),
		divider,
		m.fi.View(),
		keys,
	}, "\n")
}

type chatTurn struct {
	user    string
	blocks  []string
	result  string
	errs    []string
	hidden  int
	done    bool
	isError bool
	cost    float64
}

func chatTurns(evs []agent.Event) []chatTurn {
	var turns []chatTurn
	cur := -1
	lastWasText := false
	ensure := func() {
		if cur < 0 {
			turns = append(turns, chatTurn{})
			cur = 0
		}
	}
	for _, ev := range evs {
		text := clean(ev.Text)
		switch ev.Kind {
		case agent.KindInit:
			continue
		case agent.KindUser:
			turns = append(turns, chatTurn{user: text})
			cur = len(turns) - 1
			lastWasText = false
			continue
		}
		ensure()
		t := &turns[cur]
		switch ev.Kind {
		case agent.KindText:
			if lastWasText && len(t.blocks) > 0 {
				t.blocks[len(t.blocks)-1] += "\n" + text
			} else {
				t.blocks = append(t.blocks, text)
			}
			lastWasText = true
			continue
		case agent.KindResult:
			t.done, t.isError, t.cost = true, ev.IsError, ev.Cost
			t.result = strings.TrimSpace(text)
		case agent.KindError:
			t.errs = append(t.errs, text)
		case agent.KindToolEnd:
			if ev.IsError {
				t.hidden++
			}
		case agent.KindThinking, agent.KindToolStart, agent.KindSystem:
			t.hidden++
		}
		lastWasText = false
	}
	return turns
}

func (t chatTurn) answer() (string, int) {
	if t.result != "" && !t.isError {
		return t.result, len(t.blocks)
	}
	if n := len(t.blocks); n > 0 {
		return t.blocks[n-1], n - 1
	}
	return "", 0
}

func renderChat(evs []agent.Event, agentName string, width int, live bool) string {
	w := max(width-2, 10)
	var b strings.Builder
	turns := chatTurns(evs)
	for i, t := range turns {
		running := live && i == len(turns)-1 && !t.done
		if t.user != "" {
			b.WriteString("\n" + sLavender.Bold(true).Render("☩ you") + "\n")
			b.WriteString(md.Render(t.user, w) + "\n")
		}
		answer, skipped := t.answer()
		if running {
			answer, skipped = "", len(t.blocks)
		}
		hidden := t.hidden + skipped
		if hidden > 0 {
			label := fmt.Sprintf("∴ %d step", hidden)
			if hidden != 1 {
				label += "s"
			}
			b.WriteString(sFaint.Render("  "+label+" hidden · tab shows everything") + "\n")
		}
		switch {
		case answer != "":
			b.WriteString("\n" + sGoldB.Render("✧ "+agentName) + "\n")
			b.WriteString(md.Render(answer, w) + "\n")
		case running && len(t.errs) == 0:
			b.WriteString(sDim.Render("  ✧ working…") + "\n")
		}
		for _, e := range t.errs {
			b.WriteString(sRose.Width(w).Render("✗ "+e) + "\n")
		}
		if t.done && t.isError && t.result != "" {
			b.WriteString(sRose.Width(w).Render("✗ "+t.result) + "\n")
		}
	}
	return strings.TrimLeft(b.String(), "\n")
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
			b.WriteString(md.Render(ev.Text, w) + "\n")
		case agent.KindText:
			if prev != agent.KindText {
				b.WriteString("\n" + sGoldB.Render("✧ "+agentName) + "\n")
			}
			b.WriteString(md.Render(ev.Text, w) + "\n")
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
	repo := m.spawnRepo()
	if repo == "" {
		return sRose.Render("no git repo: workers share " + m.opt.Dir)
	}
	if m.composeTeam {
		branch := workspace.CurrentBranch(repo)
		line := sDim.Render("works in your checkout ") + sIvory.Render(tildeHome(repo)) + sDim.Render(" · lands on ") + sGoldB.Render(branch)
		if d, _ := workspace.Dirty(repo); d != "" {
			line += sGold.Render("  ⚠ uncommitted changes: Seraph will not commit over them")
		}
		return line
	}
	return sDim.Render("each worker gets its own worktree off ") + sLavender.Render(m.repoText)
}

func (m *Model) composeView(title, submit string) string {
	opts := sDim.Render("model ") + m.chip("tab", "tab", "") + " " + m.mark("key:tab", m.modelLabel()) +
		sDim.Render("     mode ") + m.chip("ctrl+y", "ctrl+y", "") + " " + m.mark("key:ctrl+y", approvalLabel(string(m.opt.Approval)))
	kind := sGoldB.Render("◉ worker") + sDim.Render(" one agent, own worktree   ") + sFaint.Render("○ orchestrator")
	if m.composeTeam {
		kind = sFaint.Render("○ worker   ") + sGoldB.Render("◉ orchestrator") + sDim.Render(" plans, delegates to subagents, remembers (TEAM.md)")
	}
	limits := m.limitsLine(m.modalWidth() - 4)
	if m.scr == scrNew {
		toggle := m.chip("ctrl+o", "ctrl+o", "") + " " + m.mark("key:ctrl+o", kind)
		if m.h < 26 {
			limits = toggle
		} else {
			opts = toggle + "\n" + opts
		}
	}
	body := strings.Join([]string{
		m.ta.View(),
		"",
		opts,
		lipgloss.NewStyle().MaxWidth(m.modalWidth() - 4).Render(m.targetLine()),
		lipgloss.NewStyle().MaxWidth(m.modalWidth() - 4).Render(limits),
		"",
		hints(m.modalWidth()-4, m.chip("enter", "⏎", submit), m.chip("alt+enter", "alt+⏎", "newline"), m.chip("esc", "esc", "cancel")),
	}, "\n")
	return modal(title, body, m.modalWidth())
}

func (m *Model) planningView() string {
	el := time.Since(m.planStart).Round(time.Second)
	spin := sparkle[(m.frame/3)%len(sparkle)]
	body := sGoldB.Render(spin+" the choir deliberates "+spin) + "\n\n" +
		sDim.Render("planner ") + m.modelLabel() + sDim.Render(fmt.Sprintf(" · read-only · %s", el)) + "\n\n" +
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
		hints(w, m.chip("enter", "⏎", "summon all"), m.chip("t", "t", "to todo board"), m.chip(" ", "space", "toggle"), m.chip("tab", "tab", "agent"),
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
