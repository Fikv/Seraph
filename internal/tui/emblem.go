package tui

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	t "seraph/internal/theme"
)

const (
	emblemW     = 21
	emblemH     = 3
	emblemBuild = 1100 * time.Millisecond
	emblemLap   = 4200 * time.Millisecond
	glitchEvery = 7 * time.Second
	glitchFor   = 160 * time.Millisecond
)

var (
	glitchGlyphs = []string{"▖", "▘", "▝", "▗", "▚", "▞", "▙", "▟"}
	sEmblemWord  = t.FG(t.Ivory).Bold(true)
	sEmblemBlade = t.FG(t.Gold)
	sOrbitFront  = t.FG(t.Lavender)
	sOrbitBack   = t.FG(t.Faint)
	sEmblemStar  = t.FG(t.Gold).Bold(true)
	sEmblemTrail = t.FG(t.Dim)
	sGlitch      = t.FG(t.Cyan)
)

type emblemCell struct {
	r     string
	style lipgloss.Style
	word  bool
}

type orbitPoint struct {
	x, y  int
	front bool
	glyph string
}

var (
	emblemBase  [emblemH][emblemW]emblemCell
	emblemOrbit []orbitPoint
	wordLeft    = 2
	wordRight   = 18
)

func behindWord(x, y int) bool { return y == 1 && x >= wordLeft && x <= wordRight }

func init() {
	for y := range emblemBase {
		for x := range emblemBase[y] {
			emblemBase[y][x] = emblemCell{r: " "}
		}
	}
	put := func(y, x int, s string, style lipgloss.Style, word bool) {
		for i, r := range []rune(s) {
			if x+i >= 0 && x+i < emblemW && r != ' ' {
				emblemBase[y][x+i] = emblemCell{r: string(r), style: style, word: word}
			}
		}
	}
	put(1, 2, "╺━", sEmblemBlade, false)
	put(1, 5, "S E R A P H", sEmblemWord, true)
	put(1, 17, "━╸", sEmblemBlade, false)

	const l, r = 1, emblemW - 2
	add := func(x, y int, glyph string, front bool) {
		emblemOrbit = append(emblemOrbit, orbitPoint{x: x, y: y, glyph: glyph, front: front})
	}
	add(l, 0, "╭", false)
	for x := l + 1; x < r; x++ {
		add(x, 0, "─", false)
	}
	add(r, 0, "╮", false)
	add(r, 1, "│", true)
	add(r, 2, "╯", true)
	for x := r - 1; x > l; x-- {
		add(x, 2, "─", true)
	}
	add(l, 2, "╰", true)
	add(l, 1, "│", true)
}

func hash32(a, b int) uint32 {
	h := uint32(a)*73856093 ^ uint32(b)*19349663
	h ^= h >> 13
	return h * 2654435761
}

func emblem(el time.Duration) []string {
	var g [emblemH][emblemW]emblemCell
	tick := int(el / (70 * time.Millisecond))

	for y := 0; y < emblemH; y++ {
		for x := 0; x < emblemW; x++ {
			c := emblemBase[y][x]
			if c.r == " " {
				g[y][x] = c
				continue
			}
			reveal := time.Duration(x)*14*time.Millisecond + time.Duration(hash32(x, y)%260)*time.Millisecond
			switch {
			case el < reveal-180*time.Millisecond:
				g[y][x] = emblemCell{r: " "}
			case el < reveal:
				g[y][x] = emblemCell{r: glitchGlyphs[hash32(x+tick, y)%uint32(len(glitchGlyphs))], style: sGlitch}
			default:
				g[y][x] = c
			}
		}
	}

	if el >= emblemBuild {
		window := int(el / glitchEvery)
		if el%glitchEvery < glitchFor && window > 0 {
			for k := 0; k < 3; k++ {
				h := hash32(window, k)
				y, x := 1, int(h>>8)%emblemW
				if g[y][x].word {
					g[y][x] = emblemCell{r: glitchGlyphs[(h>>16)%uint32(len(glitchGlyphs))], style: sGlitch}
				}
			}
		}
	}

	samples := len(emblemOrbit)
	drawn := int(float64(samples) * math.Min(math.Max(float64(el-350*time.Millisecond)/float64(650*time.Millisecond), 0), 1))
	for i := 0; i < drawn; i++ {
		p := emblemOrbit[i]
		if p.y < 0 || p.y >= emblemH || p.x < 0 || p.x >= emblemW || g[p.y][p.x].r != " " || behindWord(p.x, p.y) {
			continue
		}
		style := sOrbitBack
		if p.front {
			style = sOrbitFront
		}
		g[p.y][p.x] = emblemCell{r: p.glyph, style: style}
	}

	if el >= emblemBuild {
		phase := float64((el-emblemBuild)%emblemLap) / float64(emblemLap)
		head := int(phase * float64(samples))
		for k := 3; k >= 1; k-- {
			p := emblemOrbit[(head-k+samples)%samples]
			if p.y >= 0 && p.y < emblemH && p.x >= 0 && p.x < emblemW && !behindWord(p.x, p.y) {
				glyph := "·"
				if k == 1 {
					glyph = "∙"
				}
				g[p.y][p.x] = emblemCell{r: glyph, style: sEmblemTrail}
			}
		}
		p := emblemOrbit[head]
		if p.y >= 0 && p.y < emblemH && p.x >= 0 && p.x < emblemW && (p.front || !behindWord(p.x, p.y)) {
			g[p.y][p.x] = emblemCell{r: spinStar(int(el/(33*time.Millisecond)), false), style: sEmblemStar}
		}
	}

	lines := make([]string, emblemH)
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

func emblemPlain(el time.Duration) string {
	var lines []string
	for _, l := range emblem(el) {
		lines = append(lines, ansi.Strip(l))
	}
	return strings.Join(lines, "\n")
}

func (m *Model) cornerEmblem() bool {
	w, h := logoSize(m.logoStyle)
	return m.h >= 22+h && m.w >= w+46
}

func (m *Model) logoLines() int {
	_, h := logoSize(m.logoStyle)
	return h
}

func (m *Model) emblemHeader() string {
	if m.emblemStart.IsZero() {
		m.emblemStart = time.Now()
	}
	art := logoArt(m.logoStyle, time.Since(m.emblemStart))
	artW, artH := logoSize(m.logoStyle)
	ls := m.lanes()
	cost, workers := 0.0, 0
	for l := range ls {
		for _, s := range ls[l] {
			cost += s.Cost
			workers++
		}
	}
	counts := sCyan.Render(fmt.Sprintf("⟡ %d in flight", len(ls[laneFlight]))) + sFaint.Render("  ·  ") +
		sRose.Render(fmt.Sprintf("☩ %d need you", len(ls[laneNeeds]))) + sFaint.Render("  ·  ") +
		sGold.Render(fmt.Sprintf("✦ %d ascended", len(ls[laneAscended])))
	tag := sFaint.Render("ORCHESTRATE THE CHOIR") + sFaint.Render("  ·  v"+m.opt.Version)
	spent := sFaint.Render(fmt.Sprintf("%d %s · %s spent", workers, map[bool]string{true: "worker", false: "workers"}[workers == 1], money(cost)))
	project := m.projectLabel() + "  " + sDim.Render(m.repoText)
	agentLine := agentLabel(m.opt.Agent) + sDim.Render(" · ") + approvalLabel(string(m.opt.Approval))
	var right []string
	switch {
	case artH >= 5:
		right = []string{tag, project, agentLine, counts, spent}
	case artH == 4:
		right = []string{tag, project, agentLine, counts + sFaint.Render("  ·  ") + spent}
	default:
		right = []string{tag, project + sFaint.Render("   ") + agentLine, counts + sFaint.Render("  ·  ") + spent}
	}
	for len(right) < artH {
		right = append(right, "")
	}
	avail := m.w - artW - 2
	var lines []string
	for i := 0; i < artH; i++ {
		r := right[i]
		if lipgloss.Width(r) > avail {
			r = ansi.Truncate(r, avail, "…")
		}
		gap := avail - lipgloss.Width(r)
		fill := strings.Repeat(" ", max(gap, 0))
		if i == 0 && gap > 4 {
			fill = " " + sFaint.Render(strings.Repeat("─", gap-2)) + " "
		}
		lines = append(lines, art[i]+"  "+fill+r)
	}
	return strings.Join(lines, "\n") + "\n" + rule(m.w)
}
