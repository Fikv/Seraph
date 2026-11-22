package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"seraph/internal/agent"
	"seraph/internal/md"
	"seraph/internal/orch"
	"seraph/internal/store"
	t "seraph/internal/theme"
)

type missionMode int

const (
	mmBrowse missionMode = iota
	mmNew
	mmRevise
	mmRoles
	mmArtifact
)

type treeRow struct {
	node  *store.Node
	depth int
	last  []bool
}

type missionsState struct {
	list     []store.Mission
	sel      int
	nodes    []store.Node
	rows     []treeRow
	nodeSel  int
	focus    int
	mode     missionMode
	input    textinput.Model
	roleSel  int
	vp       viewport.Model
	artTitle string
	artPath  string
	armed    string
	loadedAt time.Time
}

func (m *Model) openMissions(focusID string) tea.Cmd {
	in := textinput.New()
	in.Prompt = "✎ "
	in.PromptStyle = sGold
	in.TextStyle = sIvory
	in.PlaceholderStyle = sFaint
	in.CharLimit = 4000
	m.missions = &missionsState{input: in}
	m.reloadMissions(true)
	for i, ms := range m.missions.list {
		if ms.ID == focusID {
			m.missions.sel = i
			m.missions.focus = 1
		}
	}
	m.reloadMissions(true)
	m.scr = scrMissions
	return nil
}

func (m *Model) reloadMissions(force bool) {
	s := m.missions
	if s == nil || (!force && time.Since(s.loadedAt) < 400*time.Millisecond) {
		return
	}
	s.loadedAt = time.Now()
	all, _ := m.mgr.Missions()
	prevID := ""
	if s.sel < len(s.list) {
		prevID = s.list[s.sel].ID
	}
	s.list = s.list[:0]
	for _, ms := range all {
		if m.project != nil && ms.ProjectID != m.project.ID {
			continue
		}
		s.list = append(s.list, ms)
	}
	for i, ms := range s.list {
		if ms.ID == prevID {
			s.sel = i
		}
	}
	s.sel = min(s.sel, max(len(s.list)-1, 0))
	s.nodes, s.rows = nil, nil
	if ms, ok := m.currentMission(); ok {
		_, nodes, _ := m.mgr.Mission(ms.ID)
		s.nodes = nodes
		s.rows = buildTree(nodes)
	}
	s.nodeSel = min(s.nodeSel, max(len(s.rows)-1, 0))
}

func (m *Model) currentMission() (store.Mission, bool) {
	s := m.missions
	if s == nil || s.sel >= len(s.list) {
		return store.Mission{}, false
	}
	return s.list[s.sel], true
}

func (m *Model) currentNode() (*store.Node, bool) {
	s := m.missions
	if s == nil || s.nodeSel >= len(s.rows) {
		return nil, false
	}
	return s.rows[s.nodeSel].node, true
}

func buildTree(nodes []store.Node) []treeRow {
	kids := map[string][]*store.Node{}
	for i := range nodes {
		kids[nodes[i].ParentID] = append(kids[nodes[i].ParentID], &nodes[i])
	}
	var rows []treeRow
	var walk func(parent string, depth int, last []bool)
	walk = func(parent string, depth int, last []bool) {
		list := kids[parent]
		for i, n := range list {
			l := append(append([]bool(nil), last...), i == len(list)-1)
			rows = append(rows, treeRow{node: n, depth: depth, last: l})
			walk(n.ID, depth+1, l)
		}
	}
	walk("", 0, nil)
	return rows
}

var roleStyles = map[store.Role]lipgloss.Style{
	store.RolePO:         t.FG(t.Gold).Bold(true),
	store.RoleAnalyst:    t.FG(t.Cyan),
	store.RoleLead:       t.FG(t.Lavender).Bold(true),
	store.RoleDev:        t.FG(t.Ivory).Bold(true),
	store.RoleReviewer:   t.FG(t.Rose),
	store.RoleTester:     t.FG(t.Mint),
	store.RoleAcceptance: t.FG(t.Gold),
}

var roleGlyph = map[store.Role]string{
	store.RolePO:         "♔",
	store.RoleAnalyst:    "◎",
	store.RoleLead:       "♖",
	store.RoleDev:        "⌘",
	store.RoleReviewer:   "◉",
	store.RoleTester:     "⚗",
	store.RoleAcceptance: "♔",
}

func (m *Model) nodeStatus(n *store.Node) string {
	switch n.Status {
	case store.NodeWorking:
		return sCyan.Render(sparkle[(m.frame/2)%len(sparkle)])
	case store.NodeDone:
		return sMint.Render("✓")
	case store.NodeMerged:
		return sGold.Render("✦")
	case store.NodeFailed:
		return sRose.Render("✗")
	case store.NodeSkipped:
		return sFaint.Render("–")
	}
	return sFaint.Render("○")
}

func verdictBadge(v string) string {
	switch v {
	case "":
		return ""
	case "APPROVE", "PASS", "ACCEPT":
		return " " + sMint.Render(strings.ToLower(v))
	case "CHANGES", "FAIL", "REJECT":
		return " " + sRose.Render(strings.ToLower(v))
	}
	return " " + sGold.Render(strings.ToLower(v))
}

func phaseLine(ms store.Mission, w int) string {
	var parts []string
	reached := true
	for _, p := range store.MissionPhases {
		label := string(p)
		switch {
		case p == ms.Phase && ms.State == store.MissionDone:
			parts = append(parts, sGoldB.Render("✦ "+label))
		case p == ms.Phase:
			st := sCyan.Bold(true)
			switch ms.State {
			case store.MissionWaiting:
				st = sGoldB
			case store.MissionBlocked, store.MissionCancelled:
				st = sRose.Bold(true)
			}
			parts = append(parts, st.Render("◆ "+label))
			reached = false
		case reached:
			parts = append(parts, sDim.Render("✓ "+label))
		default:
			parts = append(parts, sFaint.Render("○ "+label))
		}
	}
	line := strings.Join(parts, sFaint.Render(" › "))
	if lipgloss.Width(line) > w {
		line = ansi.Truncate(line, w, "…")
	}
	return line
}

func missionStateLabel(ms store.Mission) string {
	switch ms.State {
	case store.MissionWaiting:
		return sGoldB.Render("⏸ your call: " + gateName(ms.Gate))
	case store.MissionBlocked:
		return sRose.Bold(true).Render("☩ needs you")
	case store.MissionDone:
		return sGold.Render("✦ accepted")
	case store.MissionCancelled:
		return sFaint.Render("cancelled")
	}
	return sCyan.Render("⟡ " + string(ms.Phase))
}

func gateName(g store.MissionPhase) string {
	switch g {
	case store.PhaseSpec:
		return "specification"
	case store.PhasePlan:
		return "plan"
	case store.PhaseAccept:
		return "acceptance"
	}
	return string(g)
}

func (m *Model) nodeModel(n *store.Node) string {
	s, ok := m.mgr.Get(n.SessionID)
	if !ok {
		return ""
	}
	name := m.modelName(s.Agent, s.Model)
	out := agentGlyph(s.Agent) + " " + sFaint.Render(name)
	if s.Cost > 0 {
		out += sFaint.Render(" " + money(s.Cost))
	}
	return out
}

func (m *Model) treeView(ms store.Mission, w, h int) string {
	s := m.missions
	head := sGoldB.Render("☩ "+trunc(ms.Title, max(w-30, 10))) + "  " + missionStateLabel(ms)
	lines := []string{head, phaseLine(ms, w), ""}
	orchLine := sLavender.Bold(true).Render("✧ Orchestrator") + sFaint.Render(" · Seraph · "+ms.Branch)
	lines = append(lines, trunc(orchLine, w))
	selLine := 0
	for i, r := range s.rows {
		n := r.node
		var prefix strings.Builder
		prefix.WriteString(" ")
		for d, last := range r.last {
			switch {
			case d == len(r.last)-1 && last:
				prefix.WriteString("└─ ")
			case d == len(r.last)-1:
				prefix.WriteString("├─ ")
			case last:
				prefix.WriteString("   ")
			default:
				prefix.WriteString("│  ")
			}
		}
		style := roleStyles[n.Role]
		role := orch.RoleNames[n.Role]
		if n.Role == store.RoleDev && n.ParentID != "" {
			role = "Fixer"
		}
		cursor := " "
		if i == s.nodeSel && s.focus == 1 {
			cursor = sGold.Render("›")
			selLine = len(lines)
		}
		titleStyle := sIvory
		if m.hover == fmt.Sprintf("mnode:%d", i) {
			titleStyle = sLavender
		}
		left := cursor + sFaint.Render(prefix.String()) + m.nodeStatus(n) + " " + style.Render(roleGlyph[n.Role]+" "+role) + " " +
			titleStyle.Render(trunc(n.Title, max(w/2-lipgloss.Width(prefix.String())-14, 8)))
		if n.Round > 0 && (n.Role == store.RoleDev || n.Role == store.RoleReviewer) {
			left += sFaint.Render(fmt.Sprintf(" r%d", n.Round+1))
		}
		left += verdictBadge(n.Verdict)
		if len(n.Files) > 0 && n.Role == store.RoleDev {
			left += sFaint.Render("  [" + trunc(strings.Join(n.Files, " "), 24) + "]")
		}
		right := m.nodeModel(n)
		if lipgloss.Width(right) > w/3 {
			right = ansi.Truncate(right, w/3, "…")
		}
		room := w
		if right != "" {
			room = w - lipgloss.Width(right) - 2
		}
		if lipgloss.Width(left) > room {
			left = ansi.Truncate(left, max(room, 0), "…")
		}
		line := left
		if right != "" {
			line = left + strings.Repeat(" ", max(w-lipgloss.Width(left)-lipgloss.Width(right), 2)) + right
		}
		lines = append(lines, m.mark(fmt.Sprintf("mnode:%d", i), line))
	}
	if len(s.rows) == 0 {
		lines = append(lines, sFaint.Render("   the team is being assembled…"))
	}
	gate := m.gatePanel(ms, w)
	room := max(h-len(gate)-1, 4)
	lines = fitLines(lines, selLine, room)
	for len(lines) < room {
		lines = append(lines, "")
	}
	return strings.Join(append(append(lines, sFaint.Render(strings.Repeat("─", w))), gate...), "\n")
}

func (m *Model) gatePanel(ms store.Mission, w int) []string {
	var out []string
	switch ms.State {
	case store.MissionWaiting:
		out = append(out, sGoldB.Render("⏸ "+strings.ToUpper(gateName(ms.Gate))+" READY FOR YOUR DECISION"))
		if ms.Gate == store.PhasePlan {
			for _, n := range m.missions.nodes {
				if n.Role == store.RoleLead && n.Note != "" {
					for _, warn := range strings.Split(n.Note, "\n") {
						out = append(out, sRose.Render(trunc("⚠ "+warn, w)))
					}
				}
			}
		}
		out = append(out, hints(w, m.chip("y", "y", "approve"), m.chip("e", "e", "request changes"), m.chip("a", "a", "read "+gateArtifact(ms.Gate)), m.chip("x", "x", "cancel")))
	case store.MissionBlocked:
		out = append(out, sRose.Render(trunc("☩ "+ms.Error, w)))
		out = append(out, hints(w, m.chip("r", "r", "retry"), m.chip("enter", "⏎", "open worker"), m.chip("x", "x", "cancel")))
	case store.MissionDone:
		out = append(out, sGold.Render(trunc("✦ accepted · merge when ready: git merge "+ms.Branch+" (into "+ms.Base+")", w)))
	case store.MissionCancelled:
		out = append(out, sFaint.Render("cancelled · branch "+ms.Branch+" kept"))
	default:
		out = append(out, sCyan.Render(trunc("⟡ the team is working · artifacts: "+tildeHome(ms.Artifacts), w)))
	}
	return out
}

func gateArtifact(g store.MissionPhase) string {
	switch g {
	case store.PhaseSpec:
		return "spec"
	case store.PhasePlan:
		return "plan"
	}
	return "verdict"
}

func (m *Model) missionList(w, h int) string {
	s := m.missions
	rows := []string{sFaint.Render(fmt.Sprintf("%d missions · n new", len(s.list)))}
	selRow := 0
	for i, ms := range s.list {
		cursor, style := "  ", sIvory
		if i == s.sel {
			cursor, style = sGold.Render("› "), sGoldB
			if s.focus == 1 {
				style = sGold
			}
			selRow = len(rows)
		} else if m.hover == fmt.Sprintf("mission:%d", i) {
			style = sLavender
		}
		glyph := map[store.MissionState]string{
			store.MissionRunning: sCyan.Render("⟡"), store.MissionWaiting: sGold.Render("⏸"),
			store.MissionBlocked: sRose.Render("☩"), store.MissionDone: sGold.Render("✦"), store.MissionCancelled: sFaint.Render("–"),
		}[ms.State]
		line := cursor + glyph + " " + style.Render(trunc(ms.Title, w-14)) + " " + sFaint.Render(string(ms.Phase))
		rows = append(rows, m.mark(fmt.Sprintf("mission:%d", i), line))
	}
	if len(s.list) == 0 {
		rows = append(rows, "", sDim.Render("  no missions yet"), "", sFaint.Render("  n  start one: a team of"), sFaint.Render("     PO → analysts → lead →"), sFaint.Render("     devs ⇄ review → tests →"), sFaint.Render("     acceptance"))
	}
	return lipgloss.NewStyle().Width(w).Render(strings.Join(fitLines(rows, selRow, h), "\n"))
}

func (m *Model) missionsLayout() (listW, treeW, bodyH int) {
	bodyH = max(m.h-7, 6)
	listW = min(max(m.w/4, 26), 40)
	treeW = m.w - listW - 3
	if treeW < 50 {
		return 0, m.w, bodyH
	}
	return listW, treeW, bodyH
}

func (m *Model) missionsView() string {
	s := m.missions
	m.reloadMissions(false)
	listW, treeW, bodyH := m.missionsLayout()
	var right string
	if ms, ok := m.currentMission(); ok {
		right = m.treeView(ms, treeW, bodyH)
	} else {
		right = sDim.Render("no mission selected")
	}
	body := right
	if listW > 0 {
		body = joinPanels(m.missionList(listW, bodyH), right, treeW, bodyH)
	}
	title := sGoldB.Render("⟁ MISSIONS")
	if m.project != nil {
		title += sFaint.Render(" · ") + sLavender.Render(m.project.Name)
	}
	keys := hints(m.w, m.chip("n", "n", "new"), m.chip("enter", "⏎", "open"), m.chip("a", "a", "artifact"),
		m.chip("v", "v", "diff"), m.chip("R", "R", "roles"), m.chip("d", "d", "remove"), key("tab ↑↓", "move"), m.chip("esc", "esc", "back"))
	screen := m.header() + "\n" + title + "\n" + rule(m.w) + "\n" +
		lipgloss.NewStyle().Height(bodyH).MaxHeight(bodyH).Render(body) + "\n" + m.flashLine() + "\n" + keys
	switch s.mode {
	case mmNew:
		return m.overlay(m.newMissionView())
	case mmRevise:
		return m.overlay(m.reviseView())
	case mmRoles:
		return m.overlay(m.rolesView())
	case mmArtifact:
		return m.artifactView()
	}
	return screen
}

func (m *Model) newMissionView() string {
	w := m.modalWidth() - 4
	repo := m.spawnRepo()
	where := sIvory.Render(tildeHome(repo))
	if repo == "" {
		where = sRose.Render("no git repository: run seraph in one or add one to the project")
	}
	m.ta.SetWidth(w)
	m.ta.SetHeight(max(min(m.h-18, 8), 3))
	body := sDim.Render("repo ") + where + "\n\n" + m.ta.View() + "\n\n" +
		sFaint.Render(trunc("PO writes the spec → you approve → analysts → tech lead plans → you approve → developers ⇄ reviewers → tester → acceptance → you approve", w)) + "\n\n" +
		hints(w, m.chip("enter", "⏎", "start"), key("alt+⏎", "newline"), m.chip("R", "ctrl+r", "roles"), m.chip("esc", "esc", "cancel"))
	return modal("⟁ NEW MISSION", body, m.modalWidth())
}

func (m *Model) reviseView() string {
	ms, _ := m.currentMission()
	w := m.modalWidth() - 4
	s := m.missions
	s.input.Width = w - 4
	body := sDim.Render("what should change in the "+gateName(ms.Gate)+"?") + "\n\n" + s.input.View() + "\n\n" +
		hints(w, m.chip("enter", "⏎", "send"), m.chip("esc", "esc", "cancel"))
	return modal("✎ REQUEST CHANGES", body, m.modalWidth())
}

func (m *Model) rolesView() string {
	s := m.missions
	w := m.modalWidth() - 4
	var rows []string
	for i, r := range store.Roles {
		cfg := m.mgr.Role(r)
		_, custom := m.mgr.RoleSetting(r)
		cursor, style := "  ", sIvory
		if i == s.roleSel {
			cursor, style = sGold.Render("› "), sGoldB
		} else if m.hover == fmt.Sprintf("role:%d", i) {
			style = sLavender
		}
		provider := "Anthropic"
		if cfg.Agent == "cursor" {
			provider = agent.ProviderOf(cfg.Model)
			if cfg.Model == "" {
				provider = "Cursor"
			}
		}
		src := sFaint.Render("default")
		if custom {
			src = sGold.Render("set")
		}
		line := cursor + roleStyles[r].Render(fmt.Sprintf("%-2s", roleGlyph[r])) + style.Render(fmt.Sprintf("%-14s", orch.RoleNames[r])) +
			agentGlyph(cfg.Agent) + " " + sIvory.Render(trunc(m.modelName(cfg.Agent, cfg.Model), 22)) + " " + providerBadge(provider) +
			sFaint.Render("  "+string(cfg.Approval)+"  ") + src
		rows = append(rows, m.mark(fmt.Sprintf("role:%d", i), ansi.Truncate(line, w, "…")))
	}
	body := sDim.Render("which model plays each role (default = your board model)") + "\n\n" + strings.Join(rows, "\n") + "\n\n" +
		sFaint.Render(trunc("read-only: PO, analyst, lead, reviewer, acceptance · test mode: edits + test/build commands only", w)) + "\n\n" +
		hints(w, m.chip("enter", "⏎", "pick model"), m.chip("backspace", "⌫", "reset"), key("↑↓", "move"), m.chip("esc", "esc", "close"))
	return modal("♖ TEAM ROLES", body, m.modalWidth())
}

func (m *Model) openArtifact(title, path string) {
	s := m.missions
	data, err := os.ReadFile(path)
	content := ""
	if err != nil {
		content = sRose.Render("✗ " + err.Error())
	} else {
		content = md.Render(string(data), m.w-2)
	}
	s.vp = viewport.New(m.w, max(m.h-6, 3))
	s.vp.SetContent(content)
	s.artTitle, s.artPath, s.mode = title, path, mmArtifact
}

func (m *Model) artifactView() string {
	s := m.missions
	s.vp.Width, s.vp.Height = m.w, max(m.h-6, 3)
	head := sGoldB.Render("◈ "+trunc(s.artTitle, m.w/2)) + sFaint.Render("  "+trunc(tildeHome(s.artPath), m.w/2-4))
	if s.vp.TotalLineCount() > s.vp.Height {
		head += sFaint.Render(fmt.Sprintf("  %d%%", int(s.vp.ScrollPercent()*100)))
	}
	keys := hints(m.w, key("↑↓ pgup pgdn", "scroll"), m.chip("esc", "esc", "back"))
	return m.header() + "\n" + trunc(head, m.w) + "\n" + rule(m.w) + "\n" + s.vp.View() + "\n" + keys
}

func (m *Model) artifactFor(ms store.Mission, n *store.Node) (string, string) {
	if n != nil && n.Artifact != "" {
		return orch.RoleNames[n.Role] + ": " + n.Title, n.Artifact
	}
	want := map[store.MissionPhase]store.Role{store.PhaseSpec: store.RolePO, store.PhasePlan: store.RoleLead, store.PhaseAccept: store.RoleAcceptance}[ms.Gate]
	var best *store.Node
	for i := range m.missions.nodes {
		x := &m.missions.nodes[i]
		if x.Artifact != "" && (want == "" || x.Role == want) && (best == nil || x.Position > best.Position) {
			best = x
		}
	}
	if best != nil {
		return orch.RoleNames[best.Role] + ": " + best.Title, best.Artifact
	}
	return "Goal", filepath.Join(ms.Artifacts, "goal.md")
}

func (m *Model) missionsKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := m.missions
	key := k.String()
	switch s.mode {
	case mmNew:
		return m.newMissionKey(k)
	case mmRevise:
		return m.reviseKey(k)
	case mmRoles:
		return m.rolesKey(key)
	case mmArtifact:
		if key == "esc" || key == "q" {
			s.mode = mmBrowse
			return m, nil
		}
		var cmd tea.Cmd
		s.vp, cmd = s.vp.Update(k)
		return m, cmd
	}
	if key != s.armed {
		s.armed = ""
	}
	ms, hasMission := m.currentMission()
	switch key {
	case "esc", "q":
		m.scr = scrBoard
		m.missions = nil
		return m, nil
	case "tab", "right", "left", "l", "h":
		if len(s.rows) > 0 {
			s.focus = 1 - s.focus
		}
	case "up", "k":
		if s.focus == 1 {
			s.nodeSel = max(s.nodeSel-1, 0)
		} else if s.sel > 0 {
			s.sel--
			s.nodeSel = 0
			m.reloadMissions(true)
		}
	case "down", "j":
		if s.focus == 1 {
			s.nodeSel = min(s.nodeSel+1, max(len(s.rows)-1, 0))
		} else if s.sel < len(s.list)-1 {
			s.sel++
			s.nodeSel = 0
			m.reloadMissions(true)
		}
	case "[":
		if s.sel > 0 {
			s.sel--
			m.reloadMissions(true)
		}
	case "]":
		if s.sel < len(s.list)-1 {
			s.sel++
			m.reloadMissions(true)
		}
	case "enter":
		if s.focus == 0 && len(s.rows) > 0 {
			s.focus = 1
			return m, nil
		}
		if n, ok := m.currentNode(); ok && n.SessionID != "" {
			if _, exists := m.mgr.Get(n.SessionID); exists {
				m.backTo = scrMissions
				m.openDetail(n.SessionID)
				return m, textinput.Blink
			}
		}
	case "n":
		if m.spawnRepo() == "" {
			m.setFlash("a mission needs a git repository: run seraph inside one or add one to the project", true)
			return m, nil
		}
		s.mode = mmNew
		m.ta.Reset()
		m.ta.Placeholder = "describe the goal: what the user should get, not how to build it…"
		return m, m.ta.Focus()
	case "R":
		s.mode, s.roleSel = mmRoles, 0
	case "a":
		if hasMission {
			var n *store.Node
			if s.focus == 1 {
				n, _ = m.currentNode()
			}
			title, path := m.artifactFor(ms, n)
			m.openArtifact(title, path)
		}
	case "v":
		if n, ok := m.currentNode(); ok && n.SessionID != "" {
			return m, m.openDiff(n.SessionID, scrMissions)
		}
	case "y":
		if hasMission {
			if err := m.mgr.Approve(ms.ID); err != nil {
				m.setFlash(err.Error(), true)
			} else {
				m.setFlash("approved "+gateName(ms.Gate)+": the team continues", false)
			}
			m.reloadMissions(true)
		}
	case "e":
		if hasMission && ms.State == store.MissionWaiting {
			s.mode = mmRevise
			s.input.SetValue("")
			s.input.Placeholder = "e.g. AC3 must also cover the offline case"
			return m, s.input.Focus()
		}
	case "r":
		if hasMission && ms.State == store.MissionBlocked {
			nodeID := ""
			if n, ok := m.currentNode(); ok && n.Status == store.NodeFailed {
				nodeID = n.ID
			}
			if err := m.mgr.RetryNode(ms.ID, nodeID); err != nil {
				m.setFlash(err.Error(), true)
			} else {
				m.setFlash("retrying", false)
			}
			m.reloadMissions(true)
		}
	case "x":
		if hasMission && ms.Active() {
			if s.armed != "x" {
				s.armed = "x"
				m.setFlash("press x again to cancel the mission and stop its workers", true)
				return m, nil
			}
			_ = m.mgr.CancelMission(ms.ID)
			m.setFlash("mission cancelled; branch "+ms.Branch+" kept", false)
			m.reloadMissions(true)
		}
	case "d":
		if hasMission {
			if s.armed != "d" {
				s.armed = "d"
				m.setFlash("press d again to remove the mission, its worktrees and records (branches stay)", true)
				return m, nil
			}
			if err := m.mgr.RemoveMission(ms.ID); err != nil {
				m.setFlash(err.Error(), true)
			} else {
				m.setFlash("mission removed", false)
			}
			s.armed = ""
			m.reloadMissions(true)
		}
	}
	return m, nil
}

func (m *Model) newMissionKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := m.missions
	switch k.String() {
	case "esc":
		s.mode = mmBrowse
		m.ta.Blur()
		return m, nil
	case "ctrl+r":
		m.ta.Blur()
		s.mode, s.roleSel = mmRoles, 0
		return m, nil
	case "alt+enter", "ctrl+j":
		m.ta.InsertString("\n")
		return m, nil
	case "enter":
		goal := strings.TrimSpace(m.ta.Value())
		if goal == "" {
			return m, nil
		}
		projectID := ""
		if m.project != nil {
			projectID = m.project.ID
		}
		m.mgr.SetDefaults(m.defaults())
		ms, err := m.mgr.StartMission(orch.MissionOpts{Goal: goal, Repo: m.spawnRepo(), ProjectID: projectID})
		if err != nil {
			m.setFlash(err.Error(), true)
			return m, nil
		}
		m.ta.Reset()
		m.ta.Blur()
		s.mode, s.focus = mmBrowse, 1
		m.reloadMissions(true)
		for i, x := range s.list {
			if x.ID == ms.ID {
				s.sel = i
			}
		}
		m.reloadMissions(true)
		m.setFlash("mission "+ms.ID+" started: the Product Owner is writing the spec", false)
		return m, nil
	}
	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(k)
	return m, cmd
}

func (m *Model) reviseKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := m.missions
	switch k.String() {
	case "esc":
		s.mode = mmBrowse
		s.input.Blur()
		return m, nil
	case "enter":
		ms, ok := m.currentMission()
		if !ok {
			return m, nil
		}
		if err := m.mgr.Revise(ms.ID, s.input.Value()); err != nil {
			m.setFlash(err.Error(), true)
			return m, nil
		}
		s.mode = mmBrowse
		s.input.Blur()
		m.setFlash("sent back to the "+map[store.MissionPhase]string{store.PhaseSpec: "Product Owner", store.PhasePlan: "Tech Lead", store.PhaseAccept: "developers"}[ms.Gate], false)
		m.reloadMissions(true)
		return m, nil
	}
	var cmd tea.Cmd
	s.input, cmd = s.input.Update(k)
	return m, cmd
}

func (m *Model) rolesKey(key string) (tea.Model, tea.Cmd) {
	s := m.missions
	switch key {
	case "esc", "q":
		s.mode = mmBrowse
	case "up", "k":
		s.roleSel = max(s.roleSel-1, 0)
	case "down", "j":
		s.roleSel = min(s.roleSel+1, len(store.Roles)-1)
	case "backspace", "delete":
		r := store.Roles[s.roleSel]
		_ = m.mgr.SetRole(r, nil)
		m.setFlash(orch.RoleNames[r]+" uses the board model again", false)
	case "enter":
		r := store.Roles[s.roleSel]
		return m, m.openModelPicker(scrMissions, func(md agent.Model) {
			cfg, _ := m.mgr.RoleSetting(r)
			cfg.Agent, cfg.Model = md.Agent, md.ID
			_ = m.mgr.SetRole(r, &cfg)
			m.setFlash(orch.RoleNames[r]+" → "+md.Name+" ("+md.Provider+")", false)
		})
	}
	return m, nil
}

func (m *Model) missionClick(kind, arg string) (tea.Model, tea.Cmd) {
	s := m.missions
	if s == nil {
		return m, nil
	}
	i, err := strconv.Atoi(arg)
	if err != nil {
		return m, nil
	}
	switch kind {
	case "mission":
		if i < len(s.list) {
			if i == s.sel && s.focus == 0 && len(s.rows) > 0 {
				s.focus = 1
			}
			if i != s.sel {
				s.sel, s.nodeSel = i, 0
				m.reloadMissions(true)
			}
		}
	case "mnode":
		if i < len(s.rows) {
			if i == s.nodeSel && s.focus == 1 {
				return m.missionsKey(keyMsg("enter"))
			}
			s.nodeSel, s.focus = i, 1
		}
	case "role":
		if i < len(store.Roles) {
			if i == s.roleSel {
				return m.rolesKey("enter")
			}
			s.roleSel = i
		}
	}
	return m, nil
}

func (m *Model) activeMissions() (active, waiting int) {
	all, _ := m.mgr.Missions()
	for _, ms := range all {
		if m.project != nil && ms.ProjectID != m.project.ID {
			continue
		}
		if ms.Active() {
			active++
		}
		if ms.State == store.MissionWaiting || ms.State == store.MissionBlocked {
			waiting++
		}
	}
	return
}
