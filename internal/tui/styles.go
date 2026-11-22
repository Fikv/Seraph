package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"

	"seraph/internal/store"
	t "seraph/internal/theme"
)

var (
	sIvory    = t.FG(t.Ivory)
	sTitle    = t.FG(t.Ivory).Bold(true)
	sGold     = t.FG(t.Gold)
	sGoldB    = t.FG(t.Gold).Bold(true)
	sCyan     = t.FG(t.Cyan)
	sLavender = t.FG(t.Lavender)
	sRose     = t.FG(t.Rose)
	sMint     = t.FG(t.Mint)
	sDim      = t.FG(t.Dim)
	sFaint    = t.FG(t.Faint)
	sThink    = t.FG(t.Dim).Italic(true)
)

type lane int

const (
	laneFlight lane = iota
	laneNeeds
	laneAscended
	laneCount
)

var laneNames = [laneCount]string{"⟡ IN FLIGHT", "☩ NEEDS YOU", "✦ ASCENDED"}
var laneColors = [laneCount]t.RGB{t.Cyan, t.Rose, t.Gold}

func laneOf(s store.Session) lane {
	switch s.Status {
	case store.StatusWorking, store.StatusQueued:
		return laneFlight
	case store.StatusDone:
		return laneAscended
	default:
		return laneNeeds
	}
}

var sparkle = []string{"✧", "✦", "⟡", "✦"}

func agentGlyph(name string) string {
	switch name {
	case "claude":
		return sGold.Render("✻")
	case "cursor":
		return sCyan.Render("◆")
	}
	return sLavender.Render("◇")
}

func agentLabel(name string) string {
	switch name {
	case "claude":
		return sGoldB.Render("✻ claude")
	case "cursor":
		return t.FG(t.Cyan).Bold(true).Render("◆ cursor")
	}
	return name
}

func statusPill(st store.Status, frame int) string {
	switch st {
	case store.StatusWorking, store.StatusQueued:
		return sCyan.Render(sparkle[(frame/3)%len(sparkle)] + " ascending")
	case store.StatusDone:
		return sGold.Render("✦ ascended")
	case store.StatusFailed:
		return sRose.Render("✗ fallen")
	case store.StatusStopped:
		return sLavender.Render("◇ paused")
	}
	return string(st)
}

func approvalLabel(a string) string {
	switch a {
	case "yolo":
		return sRose.Render("yolo")
	case "confirm":
		return sGold.Render("confirm")
	}
	return sMint.Render("safe")
}

func clean(s string) string {
	s = ansi.Strip(s)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if j := strings.LastIndexByte(l, '\r'); j >= 0 {
			l = l[j+1:]
		}
		l = strings.ReplaceAll(l, "\t", "    ")
		lines[i] = strings.Map(func(r rune) rune {
			if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
				return -1
			}
			return r
		}, l)
	}
	return strings.Join(lines, "\n")
}

func trunc(s string, w int) string {
	if w <= 1 {
		return ""
	}
	s = strings.Join(strings.Fields(clean(s)), " ")
	return runewidth.Truncate(s, w, "…")
}

func key(k, desc string) string {
	return sGold.Render(k) + " " + sDim.Render(desc)
}

func rule(width int) string {
	if width <= 0 {
		return ""
	}
	return sFaint.Render(strings.Repeat("─", width))
}

func modal(title string, body string, width int) string {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(t.DeepGold.Color()).
		Padding(1, 2).
		Width(width).
		Render(sGoldB.Render(title) + "\n\n" + body)
}

func splitWidths(total, list int) (int, int) {
	detail := total - list - 3
	if detail < 24 {
		return total, 0
	}
	return list, detail
}

func joinPanels(list, detail string, detailW, h int) string {
	if detailW == 0 {
		return list
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, list, divider(h), detail)
}

func fitLines(lines []string, focus, limit int) []string {
	if limit <= 0 || len(lines) <= limit {
		return lines
	}
	start := min(max(focus-limit/2, 0), len(lines)-limit)
	out := append([]string(nil), lines[start:start+limit]...)
	if start > 0 {
		out[0] = sFaint.Render("  ⋯")
	}
	if start+limit < len(lines) {
		out[len(out)-1] = sFaint.Render("  ⋯")
	}
	return out
}

func (m *Model) formLimit() int { return m.h - 7 }

func physicalLine(lines []string, k int) int {
	if k <= 0 {
		return 0
	}
	k = min(k, len(lines))
	return strings.Count(strings.Join(lines[:k], "\n"), "\n") + 1
}

func (m *Model) fitForm(lines []string, focusAt int) string {
	phys := strings.Split(strings.Join(lines, "\n"), "\n")
	return strings.Join(fitLines(phys, physicalLine(lines, focusAt), m.formLimit()), "\n")
}
