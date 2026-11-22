package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"seraph/internal/agent"
	"seraph/internal/obsidian"
	"seraph/internal/orch"
	"seraph/internal/store"
	"seraph/internal/workspace"
)

type projectForm struct {
	editing  string
	inputs   []textinput.Model
	focus    int
	agent    string
	err      string
	original store.Project
}

const (
	pfName = iota
	pfDesc
	pfRepos
	pfNotes
	pfParallel
	pfAgent
	pfSave
)

var projectFieldLabels = []string{"name", "description", "repositories (comma separated)", "Obsidian notes folder", "max parallel workers"}

func (m *Model) loadProjects() {
	list, err := m.mgr.Store().Projects()
	if err != nil {
		m.setFlash("projects: "+err.Error(), true)
		return
	}
	m.projects = list
	if m.project != nil {
		found := false
		for i := range list {
			if list[i].ID == m.project.ID {
				p := list[i]
				m.project, found = &p, true
			}
		}
		if !found {
			m.project = nil
		}
	}
	m.projSel = min(m.projSel, max(len(list)-1, 0))
}

func (m *Model) loadCurrentProject() {
	id := m.mgr.Store().Setting(store.CurrentProjectSetting)
	if id == "" {
		return
	}
	if p, ok, _ := m.mgr.Store().Project(id); ok {
		m.project = &p
	}
}

func (m *Model) useProject(p *store.Project) {
	m.project = p
	id := ""
	if p != nil {
		id = p.ID
	}
	_ = m.mgr.Store().SetSetting(store.CurrentProjectSetting, id)
	m.todos = nil
	m.todosAt = time.Time{}
	m.lane, m.idx = 0, [laneCount]int{}
	m.repoText = m.repoLabel()
	if p != nil {
		m.setFlash("project "+p.Name, false)
	} else {
		m.setFlash("all projects", false)
	}
	m.refreshStats()
}

func (m *Model) openProjects() {
	m.loadProjects()
	for i, p := range m.projects {
		if m.project != nil && p.ID == m.project.ID {
			m.projSel = i
		}
	}
	m.projRemoving = ""
	m.scr = scrProjects
}

func (m *Model) projectsKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.projForm != nil {
		return m.projectFormKey(k)
	}
	key := k.String()
	if key != "d" {
		m.projRemoving = ""
	}
	n := len(m.projects)
	switch key {
	case "esc", "g":
		m.scr = scrBoard
		return m, nil
	case "up", "k":
		if m.projSel > 0 {
			m.projSel--
		}
		return m, nil
	case "down", "j":
		if m.projSel < n-1 {
			m.projSel++
		}
		return m, nil
	case "n":
		return m, m.openProjectForm(nil)
	case "*", "0":
		m.useProject(nil)
		m.scr = scrBoard
		return m, nil
	}
	if n == 0 {
		return m, nil
	}
	p := m.projects[m.projSel]
	switch key {
	case "enter":
		m.useProject(&p)
		m.scr = scrBoard
	case "e":
		return m, m.openProjectForm(&p)
	case "t":
		m.useProject(&p)
		return m, m.openTodos()
	case "o":
		m.openNotes(p)
	case "d":
		if m.projRemoving != p.ID {
			m.projRemoving = p.ID
			m.setFlash("press d again to delete project "+p.Name+" and its todos (sessions and branches stay)", true)
			return m, nil
		}
		m.projRemoving = ""
		if err := m.mgr.Store().DeleteProject(p.ID); err != nil {
			m.setFlash(err.Error(), true)
			return m, nil
		}
		if m.project != nil && m.project.ID == p.ID {
			m.project = nil
		}
		m.loadProjects()
		m.setFlash("deleted project "+p.Name, false)
	}
	return m, nil
}

func (m *Model) openNotes(p store.Project) {
	if p.NotesDir == "" {
		m.setFlash("project "+p.Name+" has no notes folder: press e to set one", true)
		return
	}
	if err := obsidian.Open(p.NotesDir); err != nil {
		m.setFlash("obsidian: "+err.Error(), true)
		return
	}
	m.setFlash("opening "+p.Name+" notes in Obsidian", false)
}

func newFormInput(width int, placeholder string) textinput.Model {
	in := textinput.New()
	in.Prompt = "  "
	in.TextStyle = sIvory
	in.PlaceholderStyle = sFaint
	in.Placeholder = placeholder
	in.CharLimit = 1000
	in.Width = width
	return in
}

func (m *Model) openProjectForm(p *store.Project) tea.Cmd {
	w := m.modalWidth() - 10
	f := &projectForm{agent: m.opt.Agent}
	placeholders := []string{"e.g. DIKO", "what the project is about (agents see this)", "/home/you/code/app, /home/you/code/api", "/mnt/c/Users/you/Vault/Project", "2"}
	for _, ph := range placeholders {
		f.inputs = append(f.inputs, newFormInput(w, ph))
	}
	if p != nil {
		f.editing, f.original = p.ID, *p
		f.inputs[pfName].SetValue(p.Name)
		f.inputs[pfDesc].SetValue(p.Description)
		f.inputs[pfRepos].SetValue(strings.Join(p.Repos, ", "))
		f.inputs[pfNotes].SetValue(p.NotesDir)
		f.inputs[pfParallel].SetValue(strconv.Itoa(p.MaxParallel))
		if p.DefaultAgent != "" {
			f.agent = p.DefaultAgent
		}
	} else {
		if m.opt.Repo != "" {
			f.inputs[pfRepos].SetValue(m.opt.Repo)
		}
		f.inputs[pfParallel].SetValue("2")
	}
	m.projForm = f
	return f.focusCurrent()
}

func (f *projectForm) focusCurrent() tea.Cmd {
	var cmd tea.Cmd
	for i := range f.inputs {
		if i == f.focus {
			cmd = f.inputs[i].Focus()
		} else {
			f.inputs[i].Blur()
		}
	}
	return cmd
}

func (m *Model) projectFormKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := m.projForm
	switch k.String() {
	case "esc":
		m.projForm = nil
		return m, nil
	case "tab", "down":
		f.focus = (f.focus + 1) % (pfSave + 1)
		return m, f.focusCurrent()
	case "shift+tab", "up":
		f.focus = (f.focus + pfSave) % (pfSave + 1)
		return m, f.focusCurrent()
	case "enter":
		switch f.focus {
		case pfAgent:
			f.agent = agent.Next(f.agent)
			return m, nil
		case pfSave:
			return m.saveProjectForm()
		}
		f.focus++
		return m, f.focusCurrent()
	case " ":
		if f.focus == pfAgent {
			f.agent = agent.Next(f.agent)
			return m, nil
		}
	case "ctrl+s":
		return m.saveProjectForm()
	}
	if f.focus < len(f.inputs) {
		var cmd tea.Cmd
		f.inputs[f.focus], cmd = f.inputs[f.focus].Update(k)
		return m, cmd
	}
	return m, nil
}

func expandPath(p string) string {
	p = strings.TrimSpace(p)
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[2:])
		}
	}
	if p != "" && !filepath.IsAbs(p) {
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
	}
	return p
}

func (m *Model) saveProjectForm() (tea.Model, tea.Cmd) {
	f := m.projForm
	p := f.original
	if f.editing == "" {
		p.ID = store.NewID()
	}
	p.Name = strings.TrimSpace(f.inputs[pfName].Value())
	p.Description = strings.TrimSpace(f.inputs[pfDesc].Value())
	p.DefaultAgent = f.agent
	p.Repos = nil
	for _, r := range strings.Split(f.inputs[pfRepos].Value(), ",") {
		r = expandPath(r)
		if r == "" {
			continue
		}
		root, err := workspace.RepoRoot(r)
		if err != nil {
			f.err = r + " is not a git repository"
			return m, nil
		}
		p.Repos = append(p.Repos, root)
	}
	p.NotesDir = expandPath(f.inputs[pfNotes].Value())
	if p.NotesDir != "" {
		if st, err := os.Stat(p.NotesDir); err != nil || !st.IsDir() {
			f.err = "notes folder " + p.NotesDir + " does not exist"
			return m, nil
		}
	}
	n, err := strconv.Atoi(strings.TrimSpace(f.inputs[pfParallel].Value()))
	if err != nil || n < 1 || n > 16 {
		f.err = "max parallel workers must be 1-16"
		return m, nil
	}
	p.MaxParallel = n
	if err := m.mgr.Store().SaveProject(p); err != nil {
		f.err = err.Error()
		return m, nil
	}
	m.projForm = nil
	m.loadProjects()
	for i := range m.projects {
		if m.projects[i].ID == p.ID {
			m.projSel = i
		}
	}
	if f.editing == "" || (m.project != nil && m.project.ID == p.ID) {
		saved := p
		m.useProject(&saved)
	}
	m.setFlash("saved project "+p.Name, false)
	return m, nil
}

func (m *Model) projectsView() string {
	bodyH := max(m.h-7, 4)
	listW, detailW := splitWidths(m.w, min(max(m.w*2/5, 34), 56))
	var rows []string
	allLabel := "  ✦ all projects"
	if m.project == nil {
		allLabel = sGoldB.Render("  ✦ all projects") + sFaint.Render("  current")
	} else {
		allLabel = sDim.Render(allLabel)
	}
	rows = append(rows, m.mark("key:*", allLabel), "")
	if len(m.projects) == 0 {
		rows = append(rows, sDim.Render("  no projects yet: press ")+sGold.Render("n"))
	}
	for i, p := range m.projects {
		cursor, style := "  ", sIvory
		if i == m.projSel {
			cursor, style = sGold.Render("› "), sGoldB
		} else if m.hover == fmt.Sprintf("proj:%d", i) {
			style = sLavender
		}
		current := ""
		if m.project != nil && m.project.ID == p.ID {
			current = sFaint.Render("  current")
		}
		line := cursor + style.Render("◈ "+trunc(p.Name, listW-22)) + sFaint.Render(fmt.Sprintf("  %d repo", len(p.Repos))) + current
		rows = append(rows, m.mark(fmt.Sprintf("proj:%d", i), line))
	}
	list := lipgloss.NewStyle().Width(listW).Render(strings.Join(rows, "\n"))
	detail := ""
	if len(m.projects) > 0 && detailW > 0 {
		detail = m.projectDetail(m.projects[m.projSel], detailW, bodyH)
	}
	body := joinPanels(list, detail, detailW, bodyH)
	keys := hints(m.w, m.chip("enter", "⏎", "use"), m.chip("t", "t", "todo board"), m.chip("n", "n", "new"), m.chip("e", "e", "edit"),
		m.chip("o", "o", "obsidian"), m.chip("*", "*", "all"), m.chip("d", "d", "delete"), m.chip("esc", "esc", "back"))
	screen := m.header() + "\n" + sGoldB.Render("◈ PROJECTS") + "\n" + rule(m.w) + "\n" +
		lipgloss.NewStyle().Height(bodyH).MaxHeight(bodyH).Render(body) + "\n" + m.flashLine() + "\n" + keys
	if m.projForm != nil {
		return m.overlay(m.projectFormView())
	}
	return screen
}

func (m *Model) projectDetail(p store.Project, w, h int) string {
	row := func(k, v string) string { return sFaint.Render(fmt.Sprintf("%-9s", k)) + v }
	lines := []string{sGoldB.Render("◈ " + trunc(p.Name, w-2))}
	if p.Description != "" {
		lines = append(lines, lipgloss.NewStyle().Width(w).Render(sDim.Render(trunc(p.Description, w*2))))
	}
	lines = append(lines, "", row("agent", agentLabel(firstNonEmpty(p.DefaultAgent, m.opt.Agent)))+sFaint.Render(fmt.Sprintf("   max %d parallel", p.MaxParallel)))
	lines = append(lines, "", sFaint.Render("repositories"))
	if len(p.Repos) == 0 {
		lines = append(lines, sRose.Render("  none: workers need at least one repo"))
	}
	for _, r := range p.Repos {
		state := sMint.Render("⎇ " + workspace.CurrentBranch(r))
		if _, err := os.Stat(r); err != nil {
			state = sRose.Render("✗ missing")
		}
		lines = append(lines, "  "+sIvory.Render(trunc(tildeHome(r), w-24))+"  "+state)
	}
	lines = append(lines, "", sFaint.Render("obsidian notes"))
	if p.NotesDir == "" {
		lines = append(lines, sDim.Render("  not linked: press e to add a notes folder"))
	} else {
		lines = append(lines, "  "+m.mark("key:o", sLavender.Render(trunc(p.NotesDir, w-4))))
		for _, n := range m.recentNotes(p) {
			lines = append(lines, "    "+sDim.Render(trunc(n.Rel, w-14))+sFaint.Render("  "+ago(n.ModTime)))
		}
	}
	counts := map[store.TodoStatus]int{}
	if todos, err := m.mgr.Store().Todos(p.ID); err == nil {
		for _, t := range todos {
			counts[t.Status]++
		}
	}
	sessions := 0
	for _, s := range m.mgr.Sessions() {
		if s.ProjectID == p.ID {
			sessions++
		}
	}
	lines = append(lines, "", row("todo", fmt.Sprintf("%s  %s  %s  %s  %s",
		sIvory.Render(fmt.Sprintf("%d todo", counts[store.TodoOpen])), sLavender.Render(fmt.Sprintf("%d queued", counts[store.TodoQueued])),
		sCyan.Render(fmt.Sprintf("%d doing", counts[store.TodoDoing])), sRose.Render(fmt.Sprintf("%d review", counts[store.TodoReview])),
		sGold.Render(fmt.Sprintf("%d done", counts[store.TodoDone])))))
	lines = append(lines, row("workers", sDim.Render(fmt.Sprintf("%d sessions", sessions))))
	return lipgloss.NewStyle().Width(w).MaxHeight(h).Render(strings.Join(lines, "\n"))
}

func (m *Model) recentNotes(p store.Project) []obsidian.Note {
	if m.notesCache == nil {
		m.notesCache = map[string]notesEntry{}
	}
	if e, ok := m.notesCache[p.NotesDir]; ok && time.Since(e.at) < 10*time.Second {
		return e.notes
	}
	notes := obsidian.Recent(p.NotesDir, 5)
	m.notesCache[p.NotesDir] = notesEntry{notes: notes, at: time.Now()}
	return notes
}

type notesEntry struct {
	notes []obsidian.Note
	at    time.Time
}

func (m *Model) projectFormView() string {
	f := m.projForm
	title := "◈ NEW PROJECT"
	if f.editing != "" {
		title = "◈ EDIT PROJECT"
	}
	var lines []string
	focusAt := 0
	for i, label := range projectFieldLabels {
		style := sFaint
		if f.focus == i {
			style = sGold
			focusAt = len(lines) + 1
		}
		lines = append(lines, m.mark(fmt.Sprintf("pform:%d", i), style.Render(label)), f.inputs[i].View())
	}
	agentLine := sFaint.Render("default agent  ") + m.mark(fmt.Sprintf("pform:%d", pfAgent), agentLabel(f.agent)) + sFaint.Render("  space/⏎ to change")
	if f.focus == pfAgent {
		agentLine = sGold.Render("› ") + agentLine
	}
	if f.focus == pfAgent {
		focusAt = len(lines) + 1
	}
	lines = append(lines, "", agentLine, "")
	if f.focus == pfSave {
		focusAt = len(lines)
	}
	save := chipCap.Render("⏎ save")
	if f.focus == pfSave || m.hover == fmt.Sprintf("pform:%d", pfSave) {
		save = chipCapHover.Render("⏎ save")
	}
	lines = append(lines, hints(m.modalWidth()-6, m.mark(fmt.Sprintf("pform:%d", pfSave), save)+"   "+m.chip("esc", "esc", "cancel")+sFaint.Render("   tab/↑↓ move · ctrl+s save")))
	if f.err != "" {
		lines = append(lines, "", sRose.Render("✗ "+trunc(f.err, m.modalWidth()-8)))
	}
	return modal(title, m.fitForm(lines, focusAt), m.modalWidth())
}

func (m *Model) projectClick(kind string, n int) (tea.Model, tea.Cmd) {
	switch kind {
	case "proj":
		if n < len(m.projects) {
			if n == m.projSel {
				return m.handleKey(keyMsg("enter"))
			}
			m.projSel = n
		}
	case "pform":
		if m.projForm == nil {
			return m, nil
		}
		m.projForm.focus = n
		switch n {
		case pfAgent:
			m.projForm.agent = agent.Next(m.projForm.agent)
		case pfSave:
			return m.saveProjectForm()
		}
		return m, m.projForm.focusCurrent()
	}
	return m, nil
}

func (m *Model) spawnRepo() string {
	if m.project != nil {
		if r := orch.ProjectRepo(*m.project, m.opt.Dir); r != "" {
			return r
		}
	}
	return m.opt.Repo
}

func (m *Model) projectID() string {
	if m.project == nil {
		return ""
	}
	return m.project.ID
}

func (m *Model) projectLabel() string {
	if m.project == nil {
		return m.mark("key:g", sFaint.Render("◈ all"))
	}
	return m.mark("key:g", sLavender.Bold(true).Render("◈ "+trunc(m.project.Name, 24)))
}
