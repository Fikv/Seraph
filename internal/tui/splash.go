package tui

import (
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"seraph/internal/agent"
	"seraph/internal/logo"
	"seraph/internal/store"
	t "seraph/internal/theme"
)

const (
	revealDuration = 1100 * time.Millisecond
	revealEnd      = 1.25
	titleStart     = 550 * time.Millisecond
	tagStep        = 14 * time.Millisecond
	lineStep       = 45 * time.Millisecond
	bobPeriod      = 3200 * time.Millisecond
	bobPad         = 2
	splashTag      = "orchestrate the choir"
	panelWidth     = 58
)

func (m *Model) splashElapsed() time.Duration {
	if m.splashDone {
		return time.Hour
	}
	el := time.Since(m.splashStart)
	if el >= m.splashTotal() {
		m.splashDone = true
		return time.Hour
	}
	return el
}

func titleDone() time.Duration {
	return titleStart + emblemBuild
}

func (m *Model) tagDone() time.Duration {
	return titleDone() + time.Duration(len([]rune(m.tagLine())))*tagStep
}

func (m *Model) splashTotal() time.Duration {
	return m.tagDone() + time.Duration(len(m.panelLines()))*lineStep + 100*time.Millisecond
}

func (m *Model) tagLine() string { return splashTag + " · v" + m.opt.Version }

func revealAt(el time.Duration) float64 {
	x := math.Min(float64(el)/float64(revealDuration), 1)
	eased := 1 - math.Pow(1-x, 3)
	return eased * revealEnd
}

func bobAt(since time.Duration) int {
	phase := float64(since%bobPeriod) / float64(bobPeriod) * 2 * math.Pi
	return int(math.Round(1 + math.Sin(phase)))
}

func (m *Model) tagAt(el time.Duration) string {
	r := []rune(m.tagLine())
	n := 0
	if el > titleDone() {
		n = min(int((el-titleDone())/tagStep), len(r))
	}
	return sDim.Render(string(r[:n]))
}

func (m *Model) panelLines() []string {
	row := func(k, v string) string { return sFaint.Render(fmt.Sprintf("%-10s", k)) + v }
	divider := sFaint.Render(strings.Repeat("─", panelWidth))

	repo := sIvory.Render(m.repoText)
	if m.spawnRepo() == "" {
		repo = sRose.Render("no git repo") + sDim.Render(" · workers share the dir")
	}
	var agents []string
	for _, name := range agent.Names() {
		v, probed := m.versions[name]
		switch {
		case !probed:
			agents = append(agents, agentGlyph(name)+sDim.Render(" "+name+" …"))
		case v == "":
			agents = append(agents, sRose.Render("✗ "+name))
		default:
			agents = append(agents, agentGlyph(name)+sIvory.Render(" "+name)+sDim.Render(" "+trunc(v, 12)))
		}
	}
	ls := m.lanes()
	sessions := sCyan.Render(fmt.Sprintf("⟡ %d", len(ls[laneFlight]))) + sFaint.Render(" in flight   ") +
		sRose.Render(fmt.Sprintf("☩ %d", len(ls[laneNeeds]))) + sFaint.Render(" need you   ") +
		sGold.Render(fmt.Sprintf("✦ %d", len(ls[laneAscended]))) + sFaint.Render(" ascended")

	lines := []string{
		divider,
		row("project", m.splashProject()),
		row("repo", repo),
		row("agents", strings.Join(agents, "   ")),
		row("model", m.modelLabel()+sDim.Render(" · ")+approvalLabel(string(m.opt.Approval))),
		row("limits", m.claudeLimits(6, true)),
		row("sessions", sessions),
		row("storage", m.storageLine()),
		row("state", sDim.Render(trunc(tildeHome(store.Home()), panelWidth-10))),
		divider,
	}

	recent := m.mgr.Sessions()
	if len(recent) > 0 {
		lines = append(lines, sFaint.Render("recent"))
		for _, s := range recent[:min(len(recent), 4)] {
			glyph := map[lane]string{laneFlight: sCyan.Render("⟡"), laneNeeds: sRose.Render("☩"), laneAscended: sGold.Render("✦")}[laneOf(s)]
			age := sFaint.Render(fmt.Sprintf("%6s", ago(s.UpdatedAt)))
			lines = append(lines, "  "+glyph+" "+agentGlyph(s.Agent)+" "+sIvory.Render(fmt.Sprintf("%-*s", panelWidth-14, trunc(s.Title, panelWidth-14)))+age)
		}
	} else {
		lines = append(lines, sFaint.Render("recent"), "  "+sDim.Render("no workers yet: the choir awaits its first voice"))
	}
	lines = append(lines,
		divider,
		key("n", "summon")+"   "+key("p", "plan")+"   "+key("t", "todo")+"   "+key("g", "project")+"   "+key("a", "agent")+"   "+key("y", "mode")+"   "+key("q", "quit"),
	)
	return lines
}

func ago(ts time.Time) string {
	d := time.Since(ts)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

func tildeHome(p string) string {
	home, err := os.UserHomeDir()
	if err == nil && home != "" && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

func (m *Model) panel(el time.Duration) []string {
	out := logoArt(m.logoStyle, el-titleStart)
	out = append(out, m.tagAt(el), "")
	for i, line := range m.panelLines() {
		if el < m.tagDone()+time.Duration(i)*lineStep {
			out = append(out, "")
			continue
		}
		out = append(out, line)
	}
	out = append(out, "")
	hint := ""
	if m.splashDone && (time.Since(m.splashStart)/(600*time.Millisecond))%2 == 0 {
		hint = sGoldB.Render("press any key to start")
	}
	out = append(out, hint)
	for i, line := range out {
		if pad := panelWidth + 4 - ansi.StringWidth(line); pad > 0 {
			out[i] = line + strings.Repeat(" ", pad)
		}
	}
	return out
}

func starfield(w, h, frame int) []string {
	glyphs := []string{"·", "·", "·", "⋆", "✧"}
	lines := make([]string, h)
	for y := 0; y < h; y++ {
		var b strings.Builder
		for x := 0; x < w; x++ {
			hv := uint32(x*73856093) ^ uint32(y*19349663)
			hv *= 2654435761
			if hv%61 != 0 {
				b.WriteByte(' ')
				continue
			}
			g := glyphs[(hv>>8)%uint32(len(glyphs))]
			style := sFaint
			if (hv>>12)%7 == 0 && (frame/24+int(hv>>16))%5 == 0 {
				g, style = "✦", t.FG(t.Dim)
			}
			b.WriteString(style.Render(g))
		}
		lines[y] = b.String()
	}
	return lines
}

func place(bg []string, block string, x, y, w int) {
	for i, line := range strings.Split(block, "\n") {
		row := y + i
		if row < 0 || row >= len(bg) {
			continue
		}
		lw := ansi.StringWidth(line)
		if x+lw > w {
			line = ansi.Truncate(line, w-x, "")
			lw = ansi.StringWidth(line)
		}
		bg[row] = ansi.Cut(bg[row], 0, x) + "\x1b[0m" + line + "\x1b[0m" + ansi.Cut(bg[row], x+lw, w)
	}
}

func (m *Model) splashView() string {
	el := m.splashElapsed()
	since := time.Since(m.splashStart)
	canvas := starfield(m.w, m.h, m.frame)

	panel := m.panel(el)
	wide := m.w >= 110
	artCols := min(44, m.w-4)
	artRows := m.h - 4
	if !wide {
		artRows = m.h - len(panel) - 3
	}
	art := logo.Prepare(artCols, min(artRows, 22))

	dy := 0
	if m.splashDone {
		dy = bobAt(since)
	}

	if wide {
		px := max(4, m.w/10)
		py := max(1, (m.h-len(panel))/2)
		place(canvas, strings.Join(panel, "\n"), px, py, m.w)
		if art != nil {
			rows := art.Rows(bobPad)
			ax := m.w/2 + (m.w/2-art.Cols())/2
			ay := max(0, (m.h-rows)/2)
			place(canvas, art.Frame(revealAt(el), dy, bobPad), ax, ay, m.w)
		}
		return strings.Join(canvas, "\n")
	}

	total := len(panel)
	rows := 0
	if art != nil {
		rows = art.Rows(bobPad)
		total += rows + 1
	}
	y := max(0, (m.h-total)/2)
	if art != nil {
		place(canvas, art.Frame(revealAt(el), dy, bobPad), (m.w-art.Cols())/2, y, m.w)
		y += rows + 1
	}
	place(canvas, strings.Join(panel, "\n"), max(1, (m.w-panelWidth-10)/2), y, m.w)
	return strings.Join(canvas, "\n")
}

func (m *Model) storageLine() string {
	redis := sFaint.Render("redis off")
	switch m.opt.Storage {
	case "on":
		redis = sMint.Render("redis ✓ live")
	case "unreachable":
		redis = sRose.Render("redis ✗ unreachable")
	}
	return sIvory.Render("sqlite") + sFaint.Render(" · ") + redis
}

func (m *Model) splashProject() string {
	if m.project == nil {
		return sDim.Render("all projects") + sFaint.Render(" · g to pick one")
	}
	open := 0
	if todos, err := m.mgr.Store().Todos(m.project.ID); err == nil {
		for _, td := range todos {
			if td.Status != store.TodoDone {
				open++
			}
		}
	}
	notes := ""
	if m.project.NotesDir != "" {
		notes = sFaint.Render(" · ") + sLavender.Render("obsidian ✓")
	}
	return sLavender.Bold(true).Render("◈ "+trunc(m.project.Name, 20)) + sFaint.Render(fmt.Sprintf(" · %d open todo", open)) + notes
}

var spinFrames = []string{"✦", "✶", "✕", "✶"}

const spinEvery = 4

func spinStar(frame int, reverse bool) string {
	i := (frame / spinEvery) % len(spinFrames)
	if reverse {
		i = (len(spinFrames) - i) % len(spinFrames)
	}
	return spinFrames[i]
}
