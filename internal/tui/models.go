package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"seraph/internal/agent"
	t "seraph/internal/theme"
)

const (
	DefaultAgentSetting = "default_agent"
	DefaultModelSetting = "default_model"
	cursorModelsKey     = "models:cursor"
)

type catalogMsg struct {
	models []agent.Model
	err    error
}

type modelPicker struct {
	filter textinput.Model
	view   []int
	sel    int
	back   screen
	onPick func(agent.Model)
}

var providerColors = map[string]t.RGB{
	"Anthropic":   t.Gold,
	"OpenAI":      t.Mint,
	"Google":      t.Cyan,
	"xAI":         t.Ivory,
	"Cursor":      t.Lavender,
	"Meta":        t.Cyan,
	"DeepSeek":    t.Cyan,
	"Z.ai":        t.Lavender,
	"Moonshot AI": t.Rose,
	"Alibaba":     t.Gold,
}

func providerBadge(p string) string {
	c, ok := providerColors[p]
	if !ok {
		return sFaint.Render(p)
	}
	return t.FG(c).Render(p)
}

func (m *Model) catalog() []agent.Model {
	out := agent.ClaudeModels()
	return append(out, m.cursorModels...)
}

func (m *Model) loadCatalog() tea.Cmd {
	if len(m.cursorModels) > 0 || m.catalogLoading {
		return nil
	}
	c := m.cache()
	if raw, ok := c.CacheGet(context.Background(), cursorModelsKey); ok {
		var ms []agent.Model
		if json.Unmarshal([]byte(raw), &ms) == nil && len(ms) > 0 {
			m.cursorModels = ms
			return nil
		}
	}
	m.catalogLoading = true
	return func() tea.Msg {
		ms, err := agent.CursorModels(context.Background())
		if err == nil && len(ms) > 0 {
			if raw, jerr := json.Marshal(ms); jerr == nil {
				c.CacheSet(context.Background(), cursorModelsKey, string(raw), time.Hour)
			}
		}
		return catalogMsg{models: ms, err: err}
	}
}

func (m *Model) modelName(agentName, id string) string {
	key := agentName + "\x00" + id
	if v, ok := m.nameMemo[key]; ok {
		return v
	}
	v := agent.ModelName(agentName, id, m.catalog())
	if m.nameMemo == nil {
		m.nameMemo = map[string]string{}
	}
	m.nameMemo[key] = v
	return v
}

func (m *Model) modelLabel() string {
	name := m.modelName(m.opt.Agent, m.opt.Model)
	provider := "Anthropic"
	if m.opt.Agent == "cursor" {
		provider = agent.ProviderOf(m.opt.Model)
		if m.opt.Model == "" {
			provider = "Cursor"
		}
	}
	return agentLabel(m.opt.Agent) + sDim.Render(" · ") + sIvory.Render(trunc(name, 28)) + " " + providerBadge(provider)
}

func (m *Model) openModelPicker(back screen, onPick func(agent.Model)) tea.Cmd {
	in := textinput.New()
	in.Prompt = "⌕ "
	in.PromptStyle = sGold
	in.TextStyle = sIvory
	in.PlaceholderStyle = sFaint
	in.Placeholder = "filter by name, id or provider"
	in.CharLimit = 60
	in.Width = max(m.modalWidth()-10, 20)
	m.picker = &modelPicker{filter: in, back: back, onPick: onPick}
	m.applyModelFilter()
	for i, idx := range m.picker.view {
		md := m.catalog()[idx]
		if md.Agent == m.opt.Agent && md.ID == m.opt.Model {
			m.picker.sel = i
		}
	}
	m.scr = scrModels
	return tea.Batch(m.picker.filter.Focus(), m.loadCatalog())
}

func (m *Model) applyModelFilter() {
	p := m.picker
	q := strings.ToLower(strings.TrimSpace(p.filter.Value()))
	p.view = p.view[:0]
	for i, md := range m.catalog() {
		hay := strings.ToLower(md.Name + " " + md.ID + " " + md.Provider + " " + md.Agent)
		ok := true
		for _, word := range strings.Fields(q) {
			if !strings.Contains(hay, word) {
				ok = false
				break
			}
		}
		if ok {
			p.view = append(p.view, i)
		}
	}
	p.sel = min(p.sel, max(len(p.view)-1, 0))
}

func (m *Model) pickModel(md agent.Model) {
	p := m.picker
	m.picker = nil
	m.scr = p.back
	if p.onPick != nil {
		p.onPick(md)
	}
}

func (m *Model) setDefaultModel(md agent.Model) {
	m.opt.Agent, m.opt.Model = md.Agent, md.ID
	_ = m.mgr.Store().SetSetting(DefaultAgentSetting, md.Agent)
	_ = m.mgr.Store().SetSetting(DefaultModelSetting, md.ID)
	if md.ID == "" {
		_ = m.mgr.Store().SetSetting(DefaultModelSetting, "")
	}
	m.mgr.SetDefaults(m.defaults())
	m.setFlash("model: "+md.Name+" ("+md.Provider+", via "+md.Agent+")", false)
}

func (m *Model) modelsKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := m.picker
	switch k.String() {
	case "esc":
		if p.filter.Value() != "" {
			p.filter.SetValue("")
			m.applyModelFilter()
			return m, nil
		}
		m.picker = nil
		m.scr = p.back
		return m, nil
	case "up", "ctrl+p":
		p.sel = max(p.sel-1, 0)
		return m, nil
	case "down", "ctrl+n":
		p.sel = min(p.sel+1, max(len(p.view)-1, 0))
		return m, nil
	case "pgup":
		p.sel = max(p.sel-10, 0)
		return m, nil
	case "pgdown":
		p.sel = min(p.sel+10, max(len(p.view)-1, 0))
		return m, nil
	case "enter":
		if len(p.view) > 0 {
			m.pickModel(m.catalog()[p.view[p.sel]])
		}
		return m, nil
	}
	var cmd tea.Cmd
	p.filter, cmd = p.filter.Update(k)
	m.applyModelFilter()
	return m, cmd
}

func (m *Model) modelClick(arg string) (tea.Model, tea.Cmd) {
	p := m.picker
	if p == nil {
		return m, nil
	}
	var i int
	fmt.Sscanf(arg, "%d", &i)
	if i < 0 || i >= len(p.view) {
		return m, nil
	}
	if i == p.sel {
		m.pickModel(m.catalog()[p.view[i]])
		return m, nil
	}
	p.sel = i
	return m, nil
}

func (m *Model) modelPickerView() string {
	p := m.picker
	w := m.modalWidth() - 4
	cat := m.catalog()
	var rows []string
	selRow := 0
	lastAgent := ""
	for vi, idx := range p.view {
		md := cat[idx]
		if md.Agent != lastAgent {
			head := sGoldB.Render("✻ CLAUDE CODE")
			if md.Agent == "cursor" {
				head = t.FG(t.Cyan).Bold(true).Render("◆ CURSOR")
			}
			rows = append(rows, "", head)
			lastAgent = md.Agent
		}
		cur := "  "
		if md.Agent == m.opt.Agent && md.ID == m.opt.Model {
			cur = sMint.Render("✓ ")
		}
		cursor, style := "  ", sIvory
		if vi == p.sel {
			cursor, style = sGold.Render("› "), sGoldB
			selRow = len(rows)
		} else if m.hover == fmt.Sprintf("model:%d", vi) {
			style = sLavender
		}
		nameW := min(max(w-40, 14), 34)
		id := md.ID
		if id == "" {
			id = "(no --model)"
		}
		line := cursor + cur + style.Render(fmt.Sprintf("%-*s", nameW, trunc(md.Name, nameW))) + " " +
			lipgloss.NewStyle().Width(12).Render(providerBadge(md.Provider)) + " " + sFaint.Render(trunc(id, max(w-nameW-19, 6)))
		rows = append(rows, m.mark(fmt.Sprintf("model:%d", vi), line))
	}
	if len(p.view) == 0 {
		rows = append(rows, "", sDim.Render("  nothing matches"))
	}
	if m.catalogLoading {
		rows = append(rows, "", sCyan.Render("  "+sparkle[(m.frame/3)%len(sparkle)]+" loading Cursor models…"))
	}
	if m.catalogErr != "" {
		rows = append(rows, "", sRose.Render("  ✗ cursor: "+trunc(m.catalogErr, w-14)))
	}
	limit := max(m.h-16, 6)
	rows = fitLines(rows, selRow, limit)
	body := p.filter.View() + "\n" + strings.Join(rows, "\n") + "\n\n" +
		m.limitsLine(w) + "\n\n" +
		hints(w, m.chip("enter", "⏎", "use"), key("↑↓ pgup pgdn", "move"), key("type", "filter"), m.chip("esc", "esc", "back"))
	return modal(fmt.Sprintf("⟡ MODEL · %d available", len(cat)), body, m.modalWidth())
}

func limitBar(util float64, cells int) string {
	util = math.Min(math.Max(util, 0), 1)
	filled := int(math.Round(util * float64(cells)))
	style := sMint
	switch {
	case util >= 0.85:
		style = sRose
	case util >= 0.6:
		style = sGold
	}
	return style.Render(strings.Repeat("▰", filled)) + sFaint.Render(strings.Repeat("▱", cells-filled))
}

func untilText(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}
	d := time.Until(ts)
	switch {
	case d <= 0:
		return "reset"
	case d < time.Hour:
		return fmt.Sprintf("↻%dm", int(d.Minutes())+1)
	case d < 48*time.Hour:
		return fmt.Sprintf("↻%dh", int(d.Hours()+0.5))
	}
	return fmt.Sprintf("↻%dd", int(d.Hours()/24+0.5))
}

func (m *Model) claudeLimits(cells int, withReset bool) string {
	l, ok := m.stats.limits, m.stats.hasLimits
	if !ok || len(l.Windows) == 0 {
		return sFaint.Render("limits: no data yet (updates when a Claude worker runs)")
	}
	var parts []string
	for _, w := range l.Windows {
		s := sDim.Render(agent.WindowLabel(w.Window)+" ") + limitBar(w.Utilization, cells) + " " + sIvory.Render(fmt.Sprintf("%d%%", int(math.Round(w.Utilization*100))))
		if withReset {
			if u := untilText(w.ResetsAt); u != "" {
				s += sFaint.Render(" " + u)
			}
		}
		parts = append(parts, s)
	}
	out := strings.Join(parts, sFaint.Render("  ·  "))
	if age := time.Since(l.Captured); age > 15*time.Minute {
		out += sFaint.Render("  (" + ago(l.Captured) + " ago)")
	}
	return out
}

func (m *Model) limitsLine(w int) string {
	line := agentGlyph("claude") + " " + m.claudeLimits(8, true) + sFaint.Render("     ") +
		agentGlyph("cursor") + " " + sDim.Render(m.cursorPlanText())
	if lipgloss.Width(line) > w {
		line = agentGlyph("claude") + " " + m.claudeLimits(6, false)
	}
	return line
}

func (m *Model) cursorPlanText() string {
	if m.cursorPlan == "" {
		return "usage not exposed by the CLI"
	}
	return m.cursorPlan + " · usage not exposed by the CLI"
}
