package tui

import (
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	t "seraph/internal/theme"
)

const (
	LogoLine         = "line"
	LogoOrbit        = "orbit"
	LogoStyleSetting = "logo_style"

	lineW     = 33
	lineH     = 4
	lineBuild = 950 * time.Millisecond
	lineLap   = 3600 * time.Millisecond
)

var (
	lineFont = map[rune][3]string{
		'S': {"╭──╴", "╰──╮", "╶──╯"},
		'E': {"╭──╴", "├─  ", "╰──╴"},
		'R': {"╭──╮", "├─┬╯", "╵ ╰╴"},
		'A': {"╭──╮", "├──┤", "╵  ╵"},
		'P': {"╭──╮", "├──╯", "╵   "},
		'H': {"╷  ╷", "├──┤", "╵  ╵"},
	}
	sLineWord  = t.FG(t.Gold).Bold(true)
	sLineTrack = t.FG(t.Lavender)
	lineBase   [lineH][lineW]emblemCell
)

func init() {
	for y := range lineBase {
		for x := range lineBase[y] {
			lineBase[y][x] = emblemCell{r: " "}
		}
	}
	for row := 0; row < 3; row++ {
		var parts []string
		for _, r := range "SERAPH" {
			parts = append(parts, lineFont[r][row])
		}
		for i, r := range []rune(strings.Join(parts, " ")) {
			if r != ' ' {
				lineBase[row][2+i] = emblemCell{r: string(r), style: sLineWord, word: true}
			}
		}
	}
}

func logoStyleFor(setting string) string {
	if v := os.Getenv("SERAPH_LOGO"); v == LogoOrbit || v == LogoLine {
		return v
	}
	if setting == LogoLine {
		return LogoLine
	}
	return LogoOrbit
}

func logoSize(style string) (int, int) {
	if style == LogoOrbit {
		return emblemW, emblemH
	}
	return lineW, lineH
}

func logoArt(style string, el time.Duration) []string {
	if style == LogoOrbit {
		return emblem(el)
	}
	return lineMark(el)
}

func lineMark(el time.Duration) []string {
	var g [lineH][lineW]emblemCell
	tick := int(el / (70 * time.Millisecond))
	for y := 0; y < lineH; y++ {
		for x := 0; x < lineW; x++ {
			c := lineBase[y][x]
			if !c.word {
				g[y][x] = emblemCell{r: " "}
				continue
			}
			reveal := time.Duration(x)*16*time.Millisecond + time.Duration(hash32(x, y)%220)*time.Millisecond
			switch {
			case el < reveal-170*time.Millisecond:
				g[y][x] = emblemCell{r: " "}
			case el < reveal:
				g[y][x] = emblemCell{r: glitchGlyphs[hash32(x+tick, y)%uint32(len(glitchGlyphs))], style: sGlitch}
			default:
				g[y][x] = c
			}
		}
	}

	if el >= lineBuild {
		if window := int(el / glitchEvery); window > 0 && el%glitchEvery < glitchFor {
			for k := 0; k < 2; k++ {
				h := hash32(window, k+7)
				y, x := int(h%3), 2+int(h>>8)%29
				if g[y][x].word {
					g[y][x] = emblemCell{r: glitchGlyphs[(h>>16)%uint32(len(glitchGlyphs))], style: sGlitch}
				}
			}
		}
		spin := int(el / (33 * time.Millisecond))
		g[0][0] = emblemCell{r: spinStar(spin, false), style: sEmblemStar}
		g[0][lineW-1] = emblemCell{r: spinStar(spin, true), style: sEmblemStar}
	}

	drawn := int(float64(lineW) * min(max(float64(el-300*time.Millisecond)/float64(600*time.Millisecond), 0), 1))
	for x := 0; x < drawn; x++ {
		r := "─"
		switch x {
		case 0:
			r = "╶"
		case lineW - 1:
			r = "╴"
		}
		g[3][x] = emblemCell{r: r, style: sLineTrack}
	}

	if el >= lineBuild {
		pos := int(float64((el-lineBuild)%lineLap) / float64(lineLap) * float64(lineW+6))
		for k := 1; k <= 3; k++ {
			if x := pos - k; x >= 0 && x < lineW {
				glyph := "·"
				if k == 1 {
					glyph = "∙"
				}
				g[3][x] = emblemCell{r: glyph, style: sEmblemTrail}
			}
		}
		if pos >= 0 && pos < lineW {
			g[3][pos] = emblemCell{r: spinStar(int(el/(33*time.Millisecond)), false), style: sEmblemStar}
		}
	}

	lines := make([]string, lineH)
	for y := range g {
		var b strings.Builder
		for x := range g[y] {
			if g[y][x].r == " " {
				b.WriteByte(' ')
				continue
			}
			b.WriteString(g[y][x].style.Render(g[y][x].r))
		}
		lines[y] = b.String()
	}
	return lines
}

func lineMarkPlain(el time.Duration) string {
	var out []string
	for _, l := range lineMark(el) {
		out = append(out, ansi.Strip(l))
	}
	return strings.Join(out, "\n")
}
