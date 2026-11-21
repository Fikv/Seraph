package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
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
	if a == "yolo" {
		return sRose.Render("yolo")
	}
	return sMint.Render("safe")
}

func trunc(s string, w int) string {
	if w <= 1 {
		return ""
	}
	s = strings.Join(strings.Fields(s), " ")
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
