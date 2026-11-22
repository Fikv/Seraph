package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"

	t "seraph/internal/theme"
)

var (
	chipCap      = lipgloss.NewStyle().Foreground(t.Ivory.Color()).Background(t.Faint.Color()).Bold(true).Padding(0, 1)
	chipCapHover = lipgloss.NewStyle().Foreground(t.Void.Color()).Background(t.Gold.Color()).Bold(true).Padding(0, 1)
	keycapBorder = lipgloss.Border{
		Top: "─", Bottom: "━", Left: "│", Right: "┃",
		TopLeft: "┌", TopRight: "┒", BottomLeft: "┕", BottomRight: "┛",
	}
)

func (m *Model) mark(id, s string) string {
	m.zoneIDs = append(m.zoneIDs, id)
	return zone.Mark(id, s)
}

func (m *Model) chip(k, cap, label string) string {
	id := "key:" + k
	cs, ls := chipCap, sDim
	if m.hover == id {
		cs, ls = chipCapHover, sGold
	}
	s := cs.Render(cap)
	if label != "" {
		s += " " + ls.Render(label)
	}
	return m.mark(id, s)
}

func keyMsg(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "alt+enter":
		return tea.KeyMsg{Type: tea.KeyEnter, Alt: true}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	case "ctrl+x":
		return tea.KeyMsg{Type: tea.KeyCtrlX}
	case "ctrl+r":
		return tea.KeyMsg{Type: tea.KeyCtrlR}
	case "ctrl+y":
		return tea.KeyMsg{Type: tea.KeyCtrlY}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func (m *Model) zoneAt(msg tea.MouseMsg) string {
	for i := len(m.zoneIDs) - 1; i >= 0; i-- {
		if z := zone.Get(m.zoneIDs[i]); z != nil && z.InBounds(msg) {
			return m.zoneIDs[i]
		}
	}
	return ""
}

func (m *Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.scr == scrSplash {
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			return m.handleKey(keyMsg("enter"))
		}
		return m, nil
	}
	id := m.zoneAt(msg)
	switch {
	case msg.Action == tea.MouseActionMotion:
		m.hover = id
		return m, nil
	case msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown:
		return m.wheel(msg)
	case msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft:
		return m, nil
	}

	kind, arg, _ := strings.Cut(id, ":")
	switch kind {
	case "key":
		return m.handleKey(keyMsg(arg))
	case "lane":
		if n, err := strconv.Atoi(arg); err == nil {
			m.lane = lane(n)
		}
	case "card":
		return m.clickCard(arg)
	case "plan":
		if i, err := strconv.Atoi(arg); err == nil && i < len(m.plan) {
			if i == m.planSel {
				m.plan[i].Skip = !m.plan[i].Skip
			}
			m.planSel = i
		}
	case "menu":
		m.scr = scrBoard
		return m.handleKey(keyMsg(arg))
	case "mcptab", "mcp", "mcpres", "form", "formopt":
		if m.mcp != nil {
			return m.mcpClick(kind, arg)
		}
	case "proj", "pform":
		n, _ := strconv.Atoi(arg)
		return m.projectClick(kind, n)
	case "todo", "tcol", "tform":
		return m.todoClick(kind, arg)
	case "note":
		if m.notes != nil {
			return m.notesClick(arg)
		}
	case "notelink":
		if m.notes != nil {
			var i int
			fmt.Sscanf(arg, "%d", &i)
			if i < len(m.notes.links) {
				m.followLink(m.notes.links[i])
			}
		}
	case "model":
		return m.modelClick(arg)
	case "dfile", "hunk":
		if m.diff != nil {
			return m.diffClick(kind, arg)
		}
	}
	return m, nil
}

func (m *Model) wheel(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	dir := "down"
	if msg.Button == tea.MouseButtonWheelUp {
		dir = "up"
	}
	switch m.scr {
	case scrDetail:
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd
	case scrNotes:
		if m.notes != nil && (m.notes.focus == 1 || m.zoneAt(msg) == "notesprev") {
			var cmd tea.Cmd
			m.notes.vp, cmd = m.notes.vp.Update(msg)
			return m, cmd
		}
		return m.handleKey(keyMsg(dir))
	case scrDiff:
		if m.diff != nil && !strings.HasPrefix(m.zoneAt(msg), "dfile:") {
			if dir == "up" {
				m.diff.scroll = max(m.diff.scroll-3, 0)
			} else {
				m.diff.scroll += 3
			}
			return m, nil
		}
		return m.handleKey(keyMsg(dir))
	case scrBoard, scrPlanReview, scrMCP, scrProjects, scrTodos:
		return m.handleKey(keyMsg(dir))
	}
	return m, nil
}

func (m *Model) clickCard(id string) (tea.Model, tea.Cmd) {
	if m.scr != scrBoard {
		return m, nil
	}
	ls := m.lanes()
	for l := lane(0); l < laneCount; l++ {
		for i, s := range ls[l] {
			if s.ID != id {
				continue
			}
			double := m.lastClick == id && time.Since(m.lastClickAt) < 450*time.Millisecond
			already := m.lane == l && m.idx[l] == i
			m.lane, m.idx[l] = l, i
			m.lastClick, m.lastClickAt = id, time.Now()
			if double || already {
				return m.handleKey(keyMsg("enter"))
			}
			return m, nil
		}
	}
	return m, nil
}

type menuItem struct {
	key, cap, label string
	enabled         bool
}

func (m *Model) menuItems() [][]menuItem {
	sel, hasSel := m.selected()
	running := hasSel && m.mgr.Active(sel.ID)
	return [][]menuItem{
		{
			{"n", "N", "summon", true},
			{"p", "P", "plan", m.opt.Repo != ""},
			{"enter", "⏎", "open", hasSel},
			{"r", "R", "resume", hasSel && !running},
		},
		{
			{"x", "X", "stop", running},
			{"d", "D", "release", hasSel},
			{"a", "A", "model", true},
			{"y", "Y", "mode: " + string(m.opt.Approval), true},
		},
		{
			{"g", "G", "projects", true},
			{"o", "O", "notes", true},
			{"v", "V", "diff", hasSel},
			{"t", "T", "todo board", m.project != nil},
			{"c", "C", "MCP servers", true},
			{"s", "S", "start screen", true},
			{"esc", "ESC", "close", true},
			{"q", "Q", "quit", true},
		},
	}
}

func (m *Model) keycap(it menuItem) string {
	id := "menu:" + it.key
	if it.key == "esc" {
		id = "key:esc"
	}
	border, capStyle, labelStyle := t.Dim, sIvory.Bold(true), sDim
	switch {
	case !it.enabled:
		border, capStyle, labelStyle = t.Faint, sFaint, sFaint
	case m.hover == id:
		border, capStyle, labelStyle = t.Gold, sGoldB, sGold
	}
	tile := lipgloss.NewStyle().
		Border(keycapBorder).
		BorderForeground(border.Color()).
		Width(16).
		Align(lipgloss.Center).
		Render(capStyle.Render(it.cap) + "\n" + labelStyle.Render(trunc(it.label, 15)))
	if !it.enabled {
		return tile
	}
	return m.mark(id, tile)
}

func (m *Model) menuView() string {
	var items []menuItem
	for _, row := range m.menuItems() {
		items = append(items, row...)
	}
	perRow := 2
	switch {
	case m.w >= 19*4+5:
		perRow = 4
	case m.w >= 19*3+5:
		perRow = 3
	}
	rowsNeeded := (len(items) + perRow - 1) / perRow
	compact := rowsNeeded*4+8 > m.h
	if compact {
		perRow = max(1, min(3, (m.w-8)/20))
	}
	var rows []string
	for i := 0; i < len(items); i += perRow {
		var tiles []string
		for j, it := range items[i:min(i+perRow, len(items))] {
			if j > 0 {
				tiles = append(tiles, " ")
			}
			if compact {
				tiles = append(tiles, m.compactKey(it))
			} else {
				tiles = append(tiles, m.keycap(it))
			}
		}
		rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, tiles...))
	}
	width := perRow*19 - 1
	if compact {
		width = perRow*20 - 1
	}
	body := lipgloss.JoinVertical(lipgloss.Left, rows...)
	if !compact {
		body += "\n\n" + sFaint.Render(trunc("click a key or press it on the keyboard", width))
	}
	return modal("☰ MENU", body, width+4)
}

func (m *Model) compactKey(it menuItem) string {
	id := "menu:" + it.key
	if it.key == "esc" {
		id = "key:esc"
	}
	cs, ls := chipCap, sDim
	switch {
	case !it.enabled:
		cs, ls = chipCap.Foreground(t.Dim.Color()), sFaint
	case m.hover == id:
		cs, ls = chipCapHover, sGold
	}
	s := lipgloss.NewStyle().Width(19).Render(cs.Render(it.cap) + " " + ls.Render(trunc(it.label, 13)))
	if !it.enabled {
		return s
	}
	return m.mark(id, s)
}

func (m *Model) menuKey(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "esc", "m":
		m.scr = scrBoard
		return m, nil
	}
	for _, row := range m.menuItems() {
		for _, it := range row {
			if it.key == k && it.enabled {
				m.scr = scrBoard
				return m.handleKey(keyMsg(k))
			}
		}
	}
	return m, nil
}

func (m *Model) restartSplash() {
	m.scr = scrSplash
	m.splashStart = time.Now()
	m.splashDone = false
}
