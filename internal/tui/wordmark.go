package tui

import (
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

var wordmarkLetters = map[rune][3]string{
	'S': {"╭───╴", "╰───╮", "╶───╯"},
	'E': {"╭───╴", "├──  ", "╰───╴"},
	'R': {"╭───╮", "├──┬╯", "╵  ╰╴"},
	'A': {"╭───╮", "├───┤", "╵   ╵"},
	'P': {"╭───╮", "├───╯", "╵    "},
	'H': {"╷   ╷", "├───┤", "╵   ╵"},
}

const (
	wordmarkText = "SERAPH"
	wordmarkGap  = "  "
	spinStep     = 130 * time.Millisecond
)

var spinEpoch = time.Now()

func spinNow(reverse bool) string {
	return spinStar(int(time.Since(spinEpoch)/spinStep)*spinEvery, reverse)
}

func wordmarkRows() [3]string {
	var rows [3]string
	for i, r := range wordmarkText {
		glyph := wordmarkLetters[r]
		for row := range rows {
			if i > 0 {
				rows[row] += wordmarkGap
			}
			rows[row] += glyph[row]
		}
	}
	return rows
}

func wordmarkWidth() int { return lipgloss.Width(wordmarkRows()[0]) + 2*len("  ·   ✦   ") }

func (m *Model) bigBanner() bool { return m.h >= 28 && m.w >= wordmarkWidth()+4 }

func (m *Model) banner() string {
	rows := wordmarkRows()
	left := [3]string{"          ", sFaint.Render("  ·   ") + sGoldB.Render(spinNow(false)) + "   ", "          "}
	right := [3]string{"          ", "   " + sGoldB.Render(spinNow(true)) + sFaint.Render("   ·  "), "          "}
	var lines []string
	for i, r := range rows {
		lines = append(lines, left[i]+sGoldB.Render(r)+right[i])
	}
	block := strings.Join(lines, "\n")
	return lipgloss.PlaceHorizontal(m.w, lipgloss.Center, block)
}

func spinTitle() string {
	return sGoldB.Render(spinNow(false) + " SERAPH " + spinNow(true))
}
