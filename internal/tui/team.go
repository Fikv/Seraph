package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"seraph/internal/md"
	"seraph/internal/store"
	"seraph/internal/team"
	"seraph/internal/workspace"
)

type teamRow struct {
	s      store.Session
	depth  int
	last   []bool
	merged bool
}

type teamState struct {
	teamID   string
	rows     []teamRow
	sel      int
	back     screen
	backID   string
	notes    bool
	vp       viewport.Model
	loadedAt time.Time
}

func (m *Model) openTeam(teamID string, back screen) tea.Cmd {
	m.team = &teamState{teamID: teamID, back: back, backID: m.openID}
	m.reloadTeam(true)
	for i, r := range m.team.rows {
		if r.s.ID == m.openID {
			m.team.sel = i
		}
	}
	m.scr = scrTeam
	return nil
}

func (m *Model) reloadTeam(force bool) {
	t := m.team
	if t == nil || (!force && time.Since(t.loadedAt) < 500*time.Millisecond) {
		return
	}
	t.loadedAt = time.Now()
	all := m.mgr.TeamMembers(t.teamID)
	kids := map[string][]store.Session{}
	for _, s := range all {
		if live, ok := m.mgr.Get(s.ID); ok {
			s = live
		}
		kids[s.ParentID] = append(kids[s.ParentID], s)
	}
	t.rows = t.rows[:0]
	var walk func(parent string, depth int, last []bool)
	walk = func(parent string, depth int, last []bool) {
		list := kids[parent]
		for i, s := range list {
			if parent == "" && s.ID != t.teamID {
				continue
			}
			l := append(append([]bool(nil), last...), i == len(list)-1)
			row := teamRow{s: s, depth: depth, last: l}
			if s.Branch != "" && s.Base != "" && s.Repo != "" && s.Role != "orchestrator" {
				row.merged = workspace.IsAncestor(s.Repo, s.Branch, s.Base)
			}
			t.rows = append(t.rows, row)
			walk(s.ID, depth+1, l)
		}
	}
	walk("", 0, nil)
	t.sel = min(t.sel, max(len(t.rows)-1, 0))
}

func (m *Model) teamOf(id string) string {
	if s, ok := m.mgr.Get(id); ok {
		return s.TeamID
	}
	return ""
}

func sessionGlyph(s store.Session, frame int) string {
	switch s.Status {
	case store.StatusWorking, store.StatusQueued:
		return sCyan.Render(sparkle[(frame/2)%len(sparkle)])
	case store.StatusDone:
		return sMint.Render("✓")
	case store.StatusFailed:
		return sRose.Render("✗")
	case store.StatusStopped:
		return sLavender.Render("◇")
	}
	return sFaint.Render("○")
}

func (m *Model) teamView() string {
	t := m.team
	m.reloadTeam(false)
	if t.notes {
		t.vp.Width, t.vp.Height = m.w, max(m.h-6, 3)
		head := sGoldB.Render("◈ TEAM NOTEBOOK") + sFaint.Render("  "+trunc(tildeHome(filepath.Join(team.Dir(m.mgr.Store(), t.teamID), team.NotesFile)), m.w-22))
		return m.header() + "\n" + trunc(head, m.w) + "\n" + rule(m.w) + "\n" + t.vp.View() + "\n" + hints(m.w, key("↑↓ pgup pgdn", "scroll"), m.chip("esc", "esc", "back"))
	}
	bodyH := max(m.h-8, 4)
	total, running := m.mgr.TeamStats(t.teamID)
	var root store.Session
	if len(t.rows) > 0 {
		root = t.rows[0].s
	}
	title := sGoldB.Render("⟁ TEAM") + sFaint.Render(" · ") + sIvory.Render(trunc(strings.TrimPrefix(root.Title, "Orchestrator: "), m.w/2)) +
		sFaint.Render(fmt.Sprintf("  ·  %d agents · %d working", total, running))
	var lines []string
	selLine := 0
	w := m.w
	for i, r := range t.rows {
		s := r.s
		var prefix strings.Builder
		for d, last := range r.last {
			if d == 0 {
				continue
			}
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
		cursor, style := " ", sIvory
		if i == t.sel {
			cursor, style = sGold.Render("›"), sGoldB
			selLine = len(lines)
		} else if m.hover == fmt.Sprintf("tnode:%d", i) {
			style = sLavender
		}
		name, role := team.DisplayName(s), strings.TrimSpace(strings.SplitN(s.Title, ":", 2)[0])
		if s.Role == "orchestrator" {
			name, role = "✧ orchestrator", ""
		}
		left := cursor + " " + sFaint.Render(prefix.String()) + sessionGlyph(s, m.frame) + " " + style.Render(name)
		if role != "" {
			left += sFaint.Render(" · " + strings.ToLower(role))
		}
		if s.Approval == "readonly" {
			left += sFaint.Render(" · read-only")
		}
		if m.stats.waitingFor[s.ID] != "" {
			left += sGold.Render(" ⚿ asks")
		}
		right := agentGlyph(s.Agent) + " " + sFaint.Render(m.modelName(s.Agent, s.Model))
		if s.Cost > 0 {
			right += sFaint.Render(" " + money(s.Cost))
		}
		gap := w - lipgloss.Width(left) - lipgloss.Width(right) - 1
		line := left + strings.Repeat(" ", max(gap, 1)) + right
		if lipgloss.Width(line) > w {
			line = ansi.Truncate(line, w, "…")
		}
		lines = append(lines, m.mark(fmt.Sprintf("tnode:%d", i), line))
		indent := strings.Repeat(" ", 4+lipgloss.Width(prefix.String()))
		var activity string
		switch {
		case s.Status == store.StatusWorking || s.Status == store.StatusQueued:
			activity = sCyan.Render("⟡ ") + sDim.Render(trunc(firstNonEmptyStr(s.LastLine, "starting…"), w-len(indent)-3))
		case s.Status == store.StatusFailed:
			activity = sRose.Render(trunc("✗ "+firstNonEmptyStr(s.Error, "failed"), w-len(indent)))
		case s.Role == "orchestrator":
			activity = sFaint.Render(trunc(s.Branch+" · the team's integration branch", w-len(indent)))
		case r.merged:
			activity = sGold.Render("✦ merged ") + sFaint.Render(trunc("into "+s.Base, w-len(indent)-9))
		case s.Branch != "":
			activity = sDim.Render(trunc("finished · not merged yet ("+s.Branch+")", w-len(indent)))
		}
		if activity != "" {
			lines = append(lines, indent+activity)
		}
	}
	if len(t.rows) == 0 {
		lines = append(lines, sDim.Render("  the team is being assembled…"))
	}
	lines = fitLines(lines, selLine, bodyH)
	for len(lines) < bodyH {
		lines = append(lines, "")
	}
	keys := hints(m.w, m.chip("enter", "⏎", "open agent"), m.chip("n", "n", "notebook"), m.chip("v", "v", "diff"), m.chip("w", "w", "release"),
		m.chip("x", "x", "stop"), key("↑↓", "move"), m.chip("esc", "esc", "back"))
	return m.header() + "\n" + trunc(title, m.w) + "\n" + rule(m.w) + "\n" + strings.Join(lines, "\n") + "\n" + m.flashLine() + "\n" + keys
}

func firstNonEmptyStr(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func (m *Model) teamKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	t := m.team
	key := k.String()
	if t.notes {
		if key == "esc" || key == "q" || key == "n" {
			t.notes = false
			return m, nil
		}
		var cmd tea.Cmd
		t.vp, cmd = t.vp.Update(k)
		return m, cmd
	}
	var cur store.Session
	if t.sel < len(t.rows) {
		cur = t.rows[t.sel].s
	}
	switch key {
	case "esc", "q":
		back, backID := t.back, t.backID
		m.team = nil
		if back == scrDetail && backID != "" {
			m.openDetail(backID)
			return m, textinput.Blink
		}
		m.scr = scrBoard
	case "up", "k":
		t.sel = max(t.sel-1, 0)
	case "down", "j":
		t.sel = min(t.sel+1, max(len(t.rows)-1, 0))
	case "enter":
		if cur.ID != "" {
			m.backTo = scrTeam
			m.openDetail(cur.ID)
			return m, textinput.Blink
		}
	case "n":
		data, err := os.ReadFile(filepath.Join(team.Dir(m.mgr.Store(), t.teamID), team.NotesFile))
		content := md.Render(string(data), m.w-2)
		if err != nil {
			content = sRose.Render("✗ " + err.Error())
		}
		t.vp = viewport.New(m.w, max(m.h-6, 3))
		t.vp.SetContent(content)
		t.vp.GotoBottom()
		t.notes = true
	case "v":
		if cur.ID != "" {
			return m, m.openDiff(cur.ID, scrTeam)
		}
	case "w":
		if cur.ID != "" {
			return m, m.openRelease(store.TargetSession, cur.ID, scrTeam)
		}
	case "x":
		if cur.ID != "" && m.mgr.Active(cur.ID) {
			m.mgr.Stop(cur.ID)
			m.setFlash("stopping "+team.DisplayName(cur), false)
		}
	}
	return m, nil
}

func (m *Model) teamClick(arg string) (tea.Model, tea.Cmd) {
	t := m.team
	if t == nil {
		return m, nil
	}
	var i int
	fmt.Sscanf(arg, "%d", &i)
	if i < 0 || i >= len(t.rows) {
		return m, nil
	}
	if i == t.sel {
		return m.teamKey(keyMsg("enter"))
	}
	t.sel = i
	return m, nil
}

func (m *Model) teamLine(s store.Session) string {
	if s.TeamID == "" {
		return ""
	}
	total, running := m.mgr.TeamStats(s.TeamID)
	label := "⟁ team"
	if s.Role == "orchestrator" {
		label = "⟁ orchestrator"
	} else if s.AgentName != "" {
		label = "⟁ " + s.AgentName
	}
	return sLavender.Render(label) + sFaint.Render(fmt.Sprintf(" · %d agents · %d working · ", total, running)) + m.chip("ctrl+l", "ctrl+l", "team tree")
}
