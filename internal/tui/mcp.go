package tui

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"seraph/internal/mcp"
	t "seraph/internal/theme"
)

type mcpState struct {
	cfg       *mcp.Config
	ext       []mcp.External
	tab       int
	sel       int
	search    textinput.Model
	lastQuery string
	searched  bool
	results   []mcp.Entry
	next      string
	resSel    int
	loading   bool
	busy      map[string]string
	extProbe  map[string]mcp.Server
	removing  string
	form      *mcpForm
}

type mcpForm struct {
	entry  mcp.Entry
	opts   []mcp.Option
	opt    int
	name   textinput.Model
	inputs []textinput.Model
	focus  int
	claude bool
	cursor bool
	err    string
	width  int
}

type mcpSearchMsg struct {
	entries []mcp.Entry
	next    string
	more    bool
	err     error
}

type mcpDoneMsg struct {
	server mcp.Server
	action string
	err    error
}

func newSearchInput() textinput.Model {
	in := textinput.New()
	in.Prompt = "⌕ "
	in.PromptStyle = sGold
	in.TextStyle = sIvory
	in.PlaceholderStyle = sFaint
	in.Placeholder = "search the MCP registry (min. 3 letters, enter)"
	in.CharLimit = 80
	return in
}

func (m *Model) openMCP() tea.Cmd {
	cfg, err := mcp.Load(m.mgr.Store())
	if err != nil {
		m.setFlash(err.Error(), true)
		return nil
	}
	if m.mcp == nil {
		m.mcp = &mcpState{search: newSearchInput(), busy: map[string]string{}}
	}
	m.mcp.cfg = cfg
	m.mcp.ext = mcp.Externals(cfg)
	m.mcp.removing = ""
	m.mcp.sel = min(m.mcp.sel, max(len(cfg.Servers)-1, 0))
	m.scr = scrMCP
	if m.mcp.tab == 1 {
		return m.mcp.search.Focus()
	}
	return nil
}

func (m *Model) mcpSave() {
	if err := m.mcp.cfg.Save(); err != nil {
		m.setFlash("save: "+err.Error(), true)
	}
	m.mcp.ext = mcp.Externals(m.mcp.cfg)
}

func searchCmd(query, cursor string, more bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		entries, next, err := mcp.Search(ctx, query, cursor, 30)
		return mcpSearchMsg{entries: entries, next: next, more: more, err: err}
	}
}

func runCmd(s mcp.Server, action string) tea.Cmd {
	return func() tea.Msg {
		timeout := 45 * time.Second
		if action == "install" {
			timeout = 4 * time.Minute
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		var err error
		switch action {
		case "install":
			mcp.Check(ctx, &s)
			err = mcp.Apply(ctx, s)
		case "test", "probe-ext":
			mcp.Check(ctx, &s)
		case "apply":
			err = mcp.Apply(ctx, s)
		case "remove":
			err = mcp.Remove(ctx, s.Name)
		}
		return mcpDoneMsg{server: s, action: action, err: err}
	}
}

func (m *Model) mcpMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	st := m.mcp
	if st == nil {
		return m, nil
	}
	switch msg := msg.(type) {
	case mcpSearchMsg:
		st.loading = false
		st.searched = true
		if msg.err != nil {
			m.setFlash("registry: "+msg.err.Error(), true)
			return m, nil
		}
		if msg.more {
			st.results = append(st.results, msg.entries...)
		} else {
			st.results, st.resSel = msg.entries, 0
		}
		st.next = msg.next
	case mcpDoneMsg:
		delete(st.busy, msg.server.Name)
		switch msg.action {
		case "remove":
			if msg.err != nil {
				m.setFlash("remove: "+msg.err.Error(), true)
				return m, nil
			}
			st.cfg.Delete(msg.server.Name)
			st.sel = min(st.sel, max(len(st.cfg.Servers)-1, 0))
			m.setFlash("removed "+msg.server.Name, false)
		case "probe-ext":
			if st.extProbe == nil {
				st.extProbe = map[string]mcp.Server{}
			}
			st.extProbe[msg.server.Name] = msg.server
			switch msg.server.Status {
			case mcp.StatusOK:
				m.setFlash(fmt.Sprintf("%s answers · %d tools", msg.server.Name, len(msg.server.Tools)), false)
			case mcp.StatusAuth:
				m.setFlash(msg.server.Name+" needs login (in Claude: /mcp)", true)
			default:
				m.setFlash(msg.server.Name+": "+msg.server.Error, true)
			}
			return m, nil
		default:
			st.cfg.Put(msg.server)
			switch {
			case msg.err != nil:
				m.setFlash(msg.action+": "+msg.err.Error(), true)
			case msg.server.Status == mcp.StatusOK:
				m.setFlash(fmt.Sprintf("%s ready · %d tools", msg.server.Name, len(msg.server.Tools)), false)
			case msg.server.Status == mcp.StatusAuth:
				m.setFlash(msg.server.Name+" needs login", true)
			case msg.server.Status == mcp.StatusError:
				m.setFlash(msg.server.Name+": "+msg.server.Error, true)
			}
		}
		m.mcpSave()
	}
	return m, nil
}

func (m *Model) mcpKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	st := m.mcp
	key := k.String()
	if st.form != nil {
		return m.mcpFormKey(k)
	}
	if key == "esc" && !(st.tab == 1 && st.search.Value() != "") {
		st.search.Blur()
		m.scr = scrBoard
		return m, nil
	}
	if key == "tab" || key == "shift+tab" {
		return m, m.mcpSetTab(1 - st.tab)
	}
	if st.tab == 0 {
		return m.mcpInstalledKey(key)
	}
	return m.mcpBrowseKey(k)
}

func (m *Model) mcpSetTab(tab int) tea.Cmd {
	st := m.mcp
	st.tab = tab
	st.removing = ""
	if tab == 0 {
		st.search.Blur()
		return nil
	}
	cmds := []tea.Cmd{st.search.Focus()}
	if !st.searched && !st.loading {
		st.loading = true
		cmds = append(cmds, searchCmd("", "", false))
	}
	return tea.Batch(cmds...)
}

func (m *Model) mcpInstalledKey(key string) (tea.Model, tea.Cmd) {
	st := m.mcp
	n := len(st.cfg.Servers)
	total := n + len(st.ext)
	if key != "d" {
		st.removing = ""
	}
	switch key {
	case "up", "k":
		if st.sel > 0 {
			st.sel--
		}
	case "down", "j":
		if st.sel < total-1 {
			st.sel++
		}
	case "/", "b":
		return m, m.mcpSetTab(1)
	}
	if st.sel >= n && st.sel < total {
		return m.mcpExternalKey(key, st.ext[st.sel-n])
	}
	if n == 0 {
		return m, nil
	}
	s := st.cfg.Servers[st.sel]
	if _, busy := st.busy[s.Name]; busy {
		return m, nil
	}
	switch key {
	case "a":
		m.toggleTrust(s.Name)
		return m, nil
	case "t", "enter":
		st.busy[s.Name] = "testing"
		return m, runCmd(s, "test")
	case "1":
		s.Claude = !s.Claude
		st.cfg.Put(s)
		st.busy[s.Name] = "syncing"
		return m, runCmd(s, "apply")
	case "2":
		s.Cursor = !s.Cursor
		st.cfg.Put(s)
		st.busy[s.Name] = "syncing"
		return m, runCmd(s, "apply")
	case "d":
		if st.removing != s.Name {
			st.removing = s.Name
			m.setFlash("press d again to remove "+s.Name+" from Seraph, Claude and Cursor", true)
			return m, nil
		}
		st.removing = ""
		st.busy[s.Name] = "removing"
		return m, runCmd(s, "remove")
	}
	return m, nil
}

func (m *Model) mcpBrowseKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	st := m.mcp
	switch k.String() {
	case "esc":
		st.search.SetValue("")
		return m, nil
	case "up":
		if st.resSel > 0 {
			st.resSel--
		}
		return m, nil
	case "down":
		if st.resSel < len(st.results)-1 {
			st.resSel++
		}
		return m, nil
	case "ctrl+n", "pgdown":
		if st.next != "" && !st.loading {
			st.loading = true
			return m, searchCmd(st.lastQuery, st.next, true)
		}
		return m, nil
	case "enter":
		q := strings.TrimSpace(st.search.Value())
		if q != st.lastQuery || !st.searched {
			if q != "" && len([]rune(q)) < 3 {
				m.setFlash("type at least 3 letters to search", true)
				return m, nil
			}
			st.lastQuery = q
			st.loading = true
			return m, searchCmd(q, "", false)
		}
		if len(st.results) > 0 {
			return m, m.openForm(st.results[st.resSel])
		}
		return m, nil
	}
	var cmd tea.Cmd
	st.search, cmd = st.search.Update(k)
	return m, cmd
}

func newFieldInput(f mcp.Field) textinput.Model {
	in := textinput.New()
	in.Prompt = "  "
	in.TextStyle = sIvory
	in.PlaceholderStyle = sFaint
	in.CharLimit = 2000
	in.Placeholder = f.Default
	if in.Placeholder == "" {
		in.Placeholder = map[bool]string{true: "required", false: "optional"}[f.Required]
	}
	if f.Secret {
		in.EchoMode = textinput.EchoPassword
		in.EchoCharacter = '•'
	}
	return in
}

func (m *Model) openForm(e mcp.Entry) tea.Cmd {
	opts := mcp.Options(e)
	if len(opts) == 0 {
		m.setFlash("no installable package for "+e.Name, true)
		return nil
	}
	f := &mcpForm{entry: e, opts: opts, claude: true, cursor: true}
	f.name = textinput.New()
	f.name.Prompt = "  "
	f.name.TextStyle = sIvory
	f.name.CharLimit = 40
	f.name.SetValue(m.mcp.cfg.UniqueName(mcp.ShortName(e.Name)))
	f.width = m.modalWidth() - 10
	f.name.Width = f.width
	f.setOption(0)
	m.mcp.form = f
	return f.focusCurrent()
}

func (f *mcpForm) setOption(i int) {
	f.opt = i
	f.inputs = nil
	for _, field := range f.opts[i].Fields {
		in := newFieldInput(field)
		in.Width = f.width
		f.inputs = append(f.inputs, in)
	}
	f.focus = min(f.focus, f.slots()-1)
}

func (f *mcpForm) slots() int { return 1 + len(f.inputs) + 3 }

func (f *mcpForm) slotName(i int) string {
	switch {
	case i == 0:
		return "name"
	case i <= len(f.inputs):
		return "input"
	case i == len(f.inputs)+1:
		return "claude"
	case i == len(f.inputs)+2:
		return "cursor"
	}
	return "install"
}

func (f *mcpForm) focusCurrent() tea.Cmd {
	f.name.Blur()
	for i := range f.inputs {
		f.inputs[i].Blur()
	}
	switch f.slotName(f.focus) {
	case "name":
		return f.name.Focus()
	case "input":
		return f.inputs[f.focus-1].Focus()
	}
	return nil
}

func (m *Model) mcpFormKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := m.mcp.form
	slot := f.slotName(f.focus)
	switch k.String() {
	case "esc":
		m.mcp.form = nil
		return m, m.mcp.search.Focus()
	case "tab", "down":
		f.focus = (f.focus + 1) % f.slots()
		return m, f.focusCurrent()
	case "shift+tab", "up":
		f.focus = (f.focus + f.slots() - 1) % f.slots()
		return m, f.focusCurrent()
	case "ctrl+left", "ctrl+right", "ctrl+o":
		if len(f.opts) > 1 {
			f.setOption((f.opt + 1) % len(f.opts))
			return m, f.focusCurrent()
		}
		return m, nil
	case " ":
		if slot == "claude" || slot == "cursor" {
			return m.formToggle(slot)
		}
	case "enter":
		switch slot {
		case "claude", "cursor":
			return m.formToggle(slot)
		case "install":
			return m.formInstall()
		}
		f.focus = (f.focus + 1) % f.slots()
		return m, f.focusCurrent()
	}
	var cmd tea.Cmd
	switch slot {
	case "name":
		f.name, cmd = f.name.Update(k)
	case "input":
		f.inputs[f.focus-1], cmd = f.inputs[f.focus-1].Update(k)
	}
	return m, cmd
}

func (m *Model) formToggle(slot string) (tea.Model, tea.Cmd) {
	f := m.mcp.form
	if slot == "claude" {
		f.claude = !f.claude
	} else {
		f.cursor = !f.cursor
	}
	return m, nil
}

func (m *Model) formInstall() (tea.Model, tea.Cmd) {
	st, f := m.mcp, m.mcp.form
	name := mcp.ShortName(strings.TrimSpace(f.name.Value()))
	if _, taken := st.cfg.Get(name); taken {
		f.err = "name " + name + " is already used"
		return m, nil
	}
	values := map[string]string{}
	for i, field := range f.opts[f.opt].Fields {
		values[field.Name] = f.inputs[i].Value()
	}
	s, err := f.opts[f.opt].Build(name, values)
	if err != nil {
		f.err = err.Error()
		return m, nil
	}
	s.Claude, s.Cursor = f.claude, f.cursor
	st.cfg.Put(s)
	st.busy[s.Name] = "installing"
	st.form = nil
	st.tab = 0
	st.search.Blur()
	sort.SliceStable(st.cfg.Servers, func(i, j int) bool { return st.cfg.Servers[i].Name < st.cfg.Servers[j].Name })
	for i, srv := range st.cfg.Servers {
		if srv.Name == s.Name {
			st.sel = i
		}
	}
	m.setFlash("installing "+s.Name+": downloading and testing…", false)
	return m, runCmd(s, "install")
}

func (m *Model) mcpClick(kind, arg string) (tea.Model, tea.Cmd) {
	st := m.mcp
	n, _ := strconv.Atoi(arg)
	switch kind {
	case "mcptab":
		return m, m.mcpSetTab(n)
	case "mcp":
		if n < len(st.cfg.Servers)+len(st.ext) {
			st.sel = n
		}
	case "mcpres":
		if n < len(st.results) {
			if n == st.resSel {
				return m, m.openForm(st.results[n])
			}
			st.resSel = n
		}
	case "form":
		if st.form == nil {
			return m, nil
		}
		st.form.focus = n
		switch slot := st.form.slotName(n); slot {
		case "claude", "cursor":
			return m.formToggle(slot)
		case "install":
			return m.formInstall()
		}
		return m, st.form.focusCurrent()
	case "formopt":
		if st.form != nil && n < len(st.form.opts) {
			st.form.setOption(n)
			return m, st.form.focusCurrent()
		}
	}
	return m, nil
}

func statusBadge(s mcp.Server, busy string, frame int) string {
	if busy != "" {
		return sCyan.Render(sparkle[(frame/3)%len(sparkle)] + " " + busy)
	}
	switch s.Status {
	case mcp.StatusOK:
		return sMint.Render(fmt.Sprintf("✓ %d tools", len(s.Tools)))
	case mcp.StatusAuth:
		return sLavender.Render("◇ needs login")
	case mcp.StatusError:
		return sRose.Render("✗ error")
	}
	return sFaint.Render("· untested")
}

func targetBadges(claude, cursor bool) string {
	c := sFaint.Render("✻")
	if claude {
		c = sGold.Render("✻")
	}
	u := sFaint.Render("◆")
	if cursor {
		u = sCyan.Render("◆")
	}
	return c + " " + u
}

func (m *Model) mcpView() string {
	st := m.mcp
	bodyH := max(m.h-7, 4)
	tabs := []string{fmt.Sprintf("✦ Installed (%d)", len(st.cfg.Servers)), "⌕ Browse registry"}
	var tabLine []string
	for i, label := range tabs {
		style := sDim
		if i == st.tab {
			style = sGoldB.Underline(true)
		} else if m.hover == fmt.Sprintf("mcptab:%d", i) {
			style = sGold
		}
		tabLine = append(tabLine, m.mark(fmt.Sprintf("mcptab:%d", i), style.Render(label)))
	}
	head := sGoldB.Render("MCP") + "   " + strings.Join(tabLine, "    ")

	var body string
	var keys string
	if st.tab == 0 {
		body = m.mcpInstalledView(bodyH)
		keys = hints(m.w, m.chip("t", "t", "test"), m.chip("a", "a", "trust"), m.chip("1", "1", "claude"), m.chip("2", "2", "cursor"),
			m.chip("d", "d", "remove"), m.chip("tab", "tab", "browse"), m.chip("esc", "esc", "back"))
	} else {
		body = m.mcpBrowseView(bodyH)
		keys = hints(m.w, m.chip("enter", "⏎", "search / install"), m.chip("ctrl+n", "ctrl+n", "more"),
			m.chip("tab", "tab", "installed"), m.chip("esc", "esc", "clear / back"))
	}
	screen := m.header() + "\n" + head + "\n" + rule(m.w) + "\n" +
		lipgloss.NewStyle().Height(bodyH).MaxHeight(bodyH).Render(body) + "\n" + m.flashLine() + "\n" + keys
	if st.form != nil {
		return m.overlay(m.formView())
	}
	return screen
}

func (m *Model) mcpInstalledView(h int) string {
	st := m.mcp
	listW, detailW := splitWidths(m.w, min(max(m.w*2/5, 36), 60))
	var rows []string
	if len(st.cfg.Servers) == 0 {
		rows = append(rows, "", sDim.Render("  no MCP servers yet"), "", sDim.Render("  press ")+sGold.Render("tab")+sDim.Render(" to browse the registry"))
	}
	for i, s := range st.cfg.Servers {
		cursor := "  "
		nameStyle := sIvory
		if i == st.sel {
			cursor, nameStyle = sGold.Render("› "), sGoldB
		} else if m.hover == fmt.Sprintf("mcp:%d", i) {
			nameStyle = sLavender
		}
		name := trunc(s.Name, listW-26)
		if mcp.IsTrusted(m.mgr.Store(), s.Name) {
			name = trunc(s.Name, listW-29) + " ⚡"
		}
		line := cursor + nameStyle.Render(fmt.Sprintf("%-*s", listW-26, name)) + " " +
			targetBadges(s.Claude, s.Cursor) + "  " + statusBadge(s, st.busy[s.Name], m.frame)
		rows = append(rows, m.mark(fmt.Sprintf("mcp:%d", i), line))
	}
	selRow := 0
	if st.sel < len(st.cfg.Servers) {
		selRow = st.sel
	}
	if len(st.ext) > 0 {
		rows = append(rows, "", sLavender.Render(fmt.Sprintf("  ◇ ADDED OUTSIDE SERAPH · %d", len(st.ext))))
		for j, e := range st.ext {
			i := len(st.cfg.Servers) + j
			cursor, nameStyle := "  ", sIvory
			if i == st.sel {
				cursor, nameStyle = sGold.Render("› "), sGoldB
				selRow = len(rows)
			} else if m.hover == fmt.Sprintf("mcp:%d", i) {
				nameStyle = sLavender
			}
			status := sFaint.Render("· untested")
			if p, ok := st.extProbe[e.Name]; ok {
				status = statusBadge(p, st.busy[e.Name], m.frame)
			} else if b := st.busy[e.Name]; b != "" {
				status = statusBadge(e.Server, b, m.frame)
			}
			where := targetBadges(e.Has("claude"), e.Has("cursor"))
			if e.Scope == "local" {
				where += sRose.Render(" local")
			}
			name := trunc(e.Name, max(listW-30, 8))
			if mcp.IsTrusted(m.mgr.Store(), e.Name) {
				name = trunc(e.Name, max(listW-33, 5)) + " ⚡"
			}
			line := cursor + nameStyle.Render(fmt.Sprintf("%-*s", max(listW-30, 8), name)) + " " + where + "  " + status
			rows = append(rows, m.mark(fmt.Sprintf("mcp:%d", i), line))
		}
	}
	rows = fitLines(rows, selRow, h)
	list := lipgloss.NewStyle().Width(listW).Render(strings.Join(rows, "\n"))

	detail := ""
	switch {
	case detailW == 0:
	case st.sel < len(st.cfg.Servers) && len(st.cfg.Servers) > 0:
		detail = m.serverDetail(st.cfg.Servers[st.sel], detailW, h)
	case st.sel-len(st.cfg.Servers) < len(st.ext):
		detail = m.externalDetail(st.ext[st.sel-len(st.cfg.Servers)], detailW, h)
	}
	return joinPanels(list, detail, detailW, h)
}

func (m *Model) serverDetail(s mcp.Server, w, h int) string {
	wrap := lipgloss.NewStyle().Width(w)
	row := func(k, v string) string { return sFaint.Render(fmt.Sprintf("%-10s", k)) + v }
	lines := []string{
		sGoldB.Render(s.Name) + "  " + statusBadge(s, m.mcp.busy[s.Name], m.frame),
	}
	if s.Description != "" {
		lines = append(lines, wrap.Render(sDim.Render(trunc(s.Description, w*2))))
	}
	lines = append(lines, "", row("source", sIvory.Render(trunc(firstNonEmpty(s.Registry, s.Source), w-12))+sFaint.Render(" "+s.Version)))
	lines = append(lines, row("run", sIvory.Render(trunc(s.CommandLine(), w-12))))
	secret := map[string]bool{}
	for _, n := range s.Secrets {
		secret[n] = true
	}
	var envKeys []string
	for k := range s.Env {
		envKeys = append(envKeys, k)
	}
	sort.Strings(envKeys)
	for _, k := range envKeys {
		v := s.Env[k]
		if secret[k] {
			v = "••••••"
		}
		lines = append(lines, row("env", sLavender.Render(k)+sFaint.Render("=")+sDim.Render(trunc(v, w-14-len(k)))))
	}
	for k := range s.Headers {
		lines = append(lines, row("header", sLavender.Render(k)+sFaint.Render(": ")+sDim.Render("••••••")))
	}
	claude := sFaint.Render("[ ]")
	if s.Claude {
		claude = sGold.Render("[✓]")
	}
	cursor := sFaint.Render("[ ]")
	if s.Cursor {
		cursor = sCyan.Render("[✓]")
	}
	lines = append(lines, row("enabled", m.mark("key:1", claude+" "+agentLabel("claude"))+"   "+m.mark("key:2", cursor+" "+agentLabel("cursor"))))
	lines = append(lines, m.mark("key:a", m.trustLine(s.Name, w)))
	if s.Error != "" {
		lines = append(lines, "", wrap.Render(sRose.Render("✗ "+trunc(s.Error, w*3))))
	}
	if len(s.Tools) > 0 {
		lines = append(lines, "", sFaint.Render(fmt.Sprintf("tools (%d)", len(s.Tools))))
		lines = append(lines, wrap.Render(sDim.Render(strings.Join(s.Tools, "  ·  "))))
	}
	if !s.CheckedAt.IsZero() {
		lines = append(lines, sFaint.Render("checked "+ago(s.CheckedAt)+" ago"))
	}
	out := strings.Join(lines, "\n")
	return lipgloss.NewStyle().Width(w).MaxHeight(h).Render(out)
}

func (m *Model) mcpBrowseView(h int) string {
	st := m.mcp
	listW, detailW := splitWidths(m.w, min(max(m.w/2, 40), 72))
	lines := []string{st.search.View(), ""}
	switch {
	case st.loading && len(st.results) == 0:
		lines = append(lines, sCyan.Render("  "+sparkle[(m.frame/3)%len(sparkle)]+" asking the registry…"))
	case len(st.results) == 0 && st.searched:
		lines = append(lines, sDim.Render("  nothing found"))
	}
	per := 3
	visible := max((h-3)/per, 1)
	off := 0
	if st.resSel >= visible {
		off = st.resSel - visible + 1
	}
	end := min(off+visible, len(st.results))
	for i := off; i < end; i++ {
		e := st.results[i]
		cursor, nameStyle := "  ", sIvory.Bold(true)
		if i == st.resSel {
			cursor, nameStyle = sGold.Render("› "), sGoldB
		} else if m.hover == fmt.Sprintf("mcpres:%d", i) {
			nameStyle = sLavender.Bold(true)
		}
		var kinds []string
		for _, o := range mcp.Options(e) {
			kinds = append(kinds, o.Label)
		}
		item := cursor + nameStyle.Render(trunc(e.Display(), listW-20)) + sFaint.Render(" "+trunc(e.Version, 12)) + "\n" +
			"    " + sDim.Render(trunc(e.Description, listW-6)) + "\n" +
			"    " + sFaint.Render(trunc(strings.Join(kinds, " · "), listW-6))
		lines = append(lines, m.mark(fmt.Sprintf("mcpres:%d", i), item))
	}
	if st.next != "" && end == len(st.results) && len(st.results) > 0 {
		label := "  ⋯ more results"
		if st.loading {
			label = "  " + sparkle[(m.frame/3)%len(sparkle)] + " loading…"
		}
		lines = append(lines, m.mark("key:ctrl+n", sGold.Render(label)))
	}
	list := lipgloss.NewStyle().Width(listW).Render(strings.Join(lines, "\n"))
	detail := ""
	if len(st.results) > 0 && detailW > 0 {
		detail = m.entryDetail(st.results[st.resSel], detailW, h)
	}
	return joinPanels(list, detail, detailW, h)
}

func (m *Model) entryDetail(e mcp.Entry, w, h int) string {
	wrap := lipgloss.NewStyle().Width(w)
	lines := []string{sGoldB.Render(trunc(e.Display(), w)), sFaint.Render(trunc(e.Name+" · "+e.Version, w)), ""}
	if e.Description != "" {
		lines = append(lines, wrap.Render(sIvory.Render(clean(e.Description))), "")
	}
	lines = append(lines, sFaint.Render("install options"))
	for _, o := range mcp.Options(e) {
		lines = append(lines, "  "+sGold.Render("✦ ")+sIvory.Render(o.Label)+sFaint.Render("  "+trunc(o.Server.CommandLine(), w-len(o.Label)-8)))
		var req []string
		for _, f := range o.Fields {
			if f.Required {
				req = append(req, f.Name)
			}
		}
		if len(req) > 0 {
			lines = append(lines, "    "+sDim.Render(trunc("needs: "+strings.Join(req, ", "), w-4)))
		}
	}
	if link := firstNonEmpty(e.Repository.URL, e.WebsiteURL); link != "" {
		lines = append(lines, "", sFaint.Render(trunc(link, w)))
	}
	lines = append(lines, "", sDim.Render("press ")+sGold.Render("⏎")+sDim.Render(" (or click again) to install"))
	return lipgloss.NewStyle().Width(w).MaxHeight(h).Render(strings.Join(lines, "\n"))
}

func (m *Model) formView() string {
	f := m.mcp.form
	w := m.modalWidth() - 4
	var lines []string
	if f.entry.Description != "" {
		lines = append(lines, lipgloss.NewStyle().Width(w).Render(sDim.Render(trunc(f.entry.Description, w*2))), "")
	}
	if len(f.opts) > 1 {
		var opts []string
		for i, o := range f.opts {
			style := sDim
			if i == f.opt {
				style = sGoldB
			}
			opts = append(opts, m.mark(fmt.Sprintf("formopt:%d", i), style.Render(o.Label)))
		}
		lines = append(lines, sFaint.Render("via  ")+strings.Join(opts, sFaint.Render("  ·  "))+sFaint.Render("   ctrl+o"))
	} else {
		lines = append(lines, sFaint.Render("via  ")+sGoldB.Render(f.opts[0].Label))
	}
	lines = append(lines, "")

	label := func(slot int, text string) string {
		style := sFaint
		if f.focus == slot {
			style = sGold
		}
		return m.mark(fmt.Sprintf("form:%d", slot), style.Render(text))
	}
	type block struct{ lines []string }
	var blocks []block
	blocks = append(blocks, block{[]string{label(0, "name"), f.name.View()}})
	for i, field := range f.opts[f.opt].Fields {
		tag := field.Kind
		if field.Required {
			tag += " · required"
		}
		if field.Secret {
			tag += " · secret"
		}
		head := label(i+1, field.Name) + sFaint.Render("  "+tag)
		b := []string{head}
		if field.Description != "" {
			b = append(b, sFaint.Render("  "+trunc(field.Description, w-2)))
		}
		b = append(b, f.inputs[i].View())
		blocks = append(blocks, block{b})
	}
	budget := max(m.h-22, 6)
	start := 0
	for {
		used := 0
		for _, b := range blocks[start:min(f.focus+1, len(blocks))] {
			used += len(b.lines)
		}
		if used <= budget || start >= min(f.focus, len(blocks)-1) {
			break
		}
		start++
	}
	used := 0
	shown := 0
	for _, b := range blocks[start:] {
		if used+len(b.lines) > budget && shown > 0 {
			break
		}
		lines = append(lines, b.lines...)
		used += len(b.lines)
		shown++
	}
	if hidden := len(blocks) - start - shown; hidden > 0 || start > 0 {
		lines = append(lines, sFaint.Render(fmt.Sprintf("  ⋯ %d more field(s): tab to scroll", hidden+start)))
	}

	nIn := len(f.inputs)
	toggle := func(slot int, on bool, text string, color t.RGB) string {
		box := sFaint.Render("[ ]")
		if on {
			box = t.FG(color).Render("[✓]")
		}
		s := box + " " + text
		if f.focus == slot {
			s = sGold.Render("› ") + s
		} else {
			s = "  " + s
		}
		return m.mark(fmt.Sprintf("form:%d", slot), s)
	}
	lines = append(lines, "", sFaint.Render("enable for")+"  "+toggle(nIn+1, f.claude, agentLabel("claude"), t.Gold)+"   "+toggle(nIn+2, f.cursor, agentLabel("cursor"), t.Cyan))

	install := chipCap.Render("⏎ install")
	if f.focus == nIn+3 || m.hover == fmt.Sprintf("form:%d", nIn+3) {
		install = chipCapHover.Render("⏎ install")
	}
	lines = append(lines, "", m.mark(fmt.Sprintf("form:%d", nIn+3), install)+"   "+m.chip("esc", "esc", "cancel")+sFaint.Render("   tab/↑↓ move · space toggle"))
	if f.err != "" {
		lines = append(lines, "", sRose.Render("✗ "+trunc(f.err, w)))
	}
	focusAt := 0
	for i, l := range lines {
		if strings.Contains(l, "› ") || (f.focus <= nIn && strings.Contains(l, sGold.Render(f.fieldLabel()))) {
			focusAt = i
		}
	}
	if f.focus == nIn+3 {
		focusAt = len(lines) - 1
	}
	return modal("✦ INSTALL "+strings.ToUpper(trunc(f.entry.Display(), 40)), m.fitForm(lines, focusAt), m.modalWidth())
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

func divider(h int) string {
	return sFaint.Render(strings.TrimSuffix(strings.Repeat(" │ \n", max(h, 1)), "\n"))
}

func (f *mcpForm) fieldLabel() string {
	if f.focus == 0 {
		return "name"
	}
	if f.focus-1 < len(f.opts[f.opt].Fields) {
		return f.opts[f.opt].Fields[f.focus-1].Name
	}
	return ""
}

func (m *Model) mcpExternalKey(key string, e mcp.External) (tea.Model, tea.Cmd) {
	st := m.mcp
	if _, busy := st.busy[e.Name]; busy {
		return m, nil
	}
	switch key {
	case "a":
		m.toggleTrust(e.Name)
	case "t", "enter":
		st.busy[e.Name] = "testing"
		return m, runCmd(e.Server, "probe-ext")
	case "i":
		s := mcp.Import(st.cfg, e)
		if p, ok := st.extProbe[e.Name]; ok {
			s.Status, s.Tools, s.Error, s.CheckedAt = p.Status, p.Tools, p.Error, p.CheckedAt
			st.cfg.Put(s)
		}
		m.mcpSave()
		st.ext = mcp.Externals(st.cfg)
		for i, x := range st.cfg.Servers {
			if x.Name == s.Name {
				st.sel = i
			}
		}
		hint := "press 2 to share it with Cursor"
		if s.Cursor && !s.Claude {
			hint = "press 1 to share it with Claude Code"
		} else if s.Cursor && s.Claude {
			hint = "it is already in both agents"
		}
		if e.Scope == "local" {
			hint = "press 1 to add it to Claude's user scope so workers see it"
		}
		m.setFlash("imported "+s.Name+" into Seraph · "+hint, false)
	case "d":
		m.setFlash(e.Name+" lives in "+e.Where()+": import it (i) to manage or remove it from Seraph", true)
	}
	return m, nil
}

func (m *Model) externalDetail(e mcp.External, w, h int) string {
	wrap := lipgloss.NewStyle().Width(w)
	row := func(k, v string) string { return sFaint.Render(fmt.Sprintf("%-10s", k)) + v }
	s := e.Server
	status := sFaint.Render("untested")
	if p, ok := m.mcp.extProbe[e.Name]; ok {
		status = statusBadge(p, m.mcp.busy[e.Name], m.frame)
		s.Tools, s.Error = p.Tools, p.Error
	}
	lines := []string{
		sGoldB.Render(e.Name) + "  " + sLavender.Render("◇ added outside Seraph") + "  " + status,
		"",
		row("found in", wrap.Width(w-10).Render(sIvory.Render(e.Where()))),
		row("run", sIvory.Render(trunc(s.CommandLine(), w-12))),
	}
	var keys []string
	for k := range s.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		lines = append(lines, row("env", sLavender.Render(k)+sFaint.Render("=")+sDim.Render("••••••")))
	}
	for k := range s.Headers {
		lines = append(lines, row("header", sLavender.Render(k)+sFaint.Render(": ")+sDim.Render("••••••")))
	}
	if e.Scope == "local" {
		lines = append(lines, "", wrap.Render(sRose.Render("⚠ Claude loads local-scope servers only in that exact directory. Seraph workers run in worktrees under ~/.seraph, so they do not see it. Import it and press 1 to add it to Claude's user scope.")))
	} else {
		lines = append(lines, "", wrap.Render(sDim.Render("Workers already get it: Claude and Cursor read their own config. Import it to test, document and share it with the other agent from Seraph.")))
	}
	if len(s.Tools) > 0 {
		lines = append(lines, "", sFaint.Render(fmt.Sprintf("tools (%d)", len(s.Tools))))
		lines = append(lines, wrap.Render(sDim.Render(strings.Join(s.Tools, " · "))))
	}
	if s.Error != "" {
		lines = append(lines, "", wrap.Render(sRose.Render("✗ "+s.Error)))
	}
	lines = append(lines, "", m.trustLine(e.Name, w), "", hints(w, m.chip("t", "t", "test"), m.chip("i", "i", "import into Seraph"), m.chip("a", "a", "trust")))
	return lipgloss.NewStyle().Width(w).MaxHeight(h).Render(strings.Join(lines, "\n"))
}

func (m *Model) toggleTrust(name string) {
	on := !mcp.IsTrusted(m.mgr.Store(), name)
	if err := mcp.SetTrusted(m.mgr.Store(), name, on); err != nil {
		m.setFlash(err.Error(), true)
		return
	}
	if on {
		m.setFlash("⚡ "+name+" trusted: new and resumed workers use its tools without asking (not read-only roles)", false)
	} else {
		m.setFlash(name+": workers ask again (confirm mode) or are refused (safe mode)", false)
	}
}

func (m *Model) trustLine(name string, w int) string {
	if mcp.IsTrusted(m.mgr.Store(), name) {
		return lipgloss.NewStyle().Width(w).Render(sGold.Render("⚡ trusted: ") + sDim.Render("workers use its tools without asking ("+mcp.ToolPattern(name)+")"))
	}
	return lipgloss.NewStyle().Width(w).Render(sFaint.Render("workers: safe mode refuses its tools, confirm mode asks you · a trusts it"))
}
