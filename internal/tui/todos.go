package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"seraph/internal/agent"
	"seraph/internal/orch"
	"seraph/internal/store"
	t "seraph/internal/theme"
)

var todoColNames = []string{"TODO", "QUEUED", "DOING", "REVIEW", "DONE"}
var todoColGlyphs = []string{"◇", "⋯", "⟡", "☩", "✦"}
var todoColColors = []t.RGB{t.Ivory, t.Lavender, t.Cyan, t.Rose, t.Gold}

type todoForm struct {
	editing  string
	original store.Todo
	title    textinput.Model
	body     textarea.Model
	repo     int
	agent    string
	priority int
	focus    int
	err      string
}

const (
	tfTitle = iota
	tfBody
	tfRepo
	tfAgent
	tfPriority
	tfSave
)

func (m *Model) defaults() orch.Defaults {
	return orch.Defaults{Agent: m.opt.Agent, Model: m.opt.Model, Approval: m.opt.Approval}
}

func (m *Model) openTodos() tea.Cmd {
	if m.project == nil {
		m.loadProjects()
		m.openProjects()
		m.setFlash("pick a project first: the todo board belongs to a project", true)
		return nil
	}
	m.todoRemoving = ""
	m.reloadTodos()
	m.scr = scrTodos
	return nil
}

func (m *Model) reloadTodos() {
	if m.project == nil {
		m.todos = nil
		return
	}
	todos, err := m.mgr.Store().Todos(m.project.ID)
	if err != nil {
		m.setFlash("todos: "+err.Error(), true)
		return
	}
	m.todos = todos
	m.todosAt = time.Now()
	cols := m.todoColumns()
	for c := range cols {
		m.todoIdx[c] = min(m.todoIdx[c], max(len(cols[c])-1, 0))
	}
	if m.todoSelID != "" {
		m.selectTodo(m.todoSelID)
	}
}

func (m *Model) rememberTodo() {
	if td, ok := m.selectedTodo(); ok {
		m.todoSelID = td.ID
	} else {
		m.todoSelID = ""
	}
}

func colOf(s store.TodoStatus) int {
	for i, st := range store.TodoStatuses {
		if st == s {
			return i
		}
	}
	return 0
}

func (m *Model) todoColumns() [5][]store.Todo {
	var cols [5][]store.Todo
	for _, td := range m.todos {
		c := colOf(td.Status)
		cols[c] = append(cols[c], td)
	}
	return cols
}

func (m *Model) selectedTodo() (store.Todo, bool) {
	cols := m.todoColumns()
	col := cols[m.todoCol]
	if len(col) == 0 {
		return store.Todo{}, false
	}
	return col[min(m.todoIdx[m.todoCol], len(col)-1)], true
}

func (m *Model) selectTodo(id string) {
	cols := m.todoColumns()
	for c := range cols {
		for i, td := range cols[c] {
			if td.ID == id {
				m.todoCol, m.todoIdx[c] = c, i
				m.todoSelID = id
			}
		}
	}
}

func (m *Model) todosKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.todoForm != nil {
		return m.todoFormKey(k)
	}
	key := k.String()
	if key != "d" {
		m.todoRemoving = ""
	}
	cols := m.todoColumns()
	switch key {
	case "esc", "t":
		m.scr = scrBoard
		return m, nil
	case "left", "h":
		m.todoCol = (m.todoCol + 4) % 5
		m.rememberTodo()
		return m, nil
	case "right", "l", "tab":
		m.todoCol = (m.todoCol + 1) % 5
		m.rememberTodo()
		return m, nil
	case "up", "k":
		if m.todoIdx[m.todoCol] > 0 {
			m.todoIdx[m.todoCol]--
		}
		m.rememberTodo()
		return m, nil
	case "down", "j":
		if m.todoIdx[m.todoCol] < len(cols[m.todoCol])-1 {
			m.todoIdx[m.todoCol]++
		}
		m.rememberTodo()
		return m, nil
	case "n":
		return m, m.openTodoForm(nil)
	case "o":
		if m.project != nil {
			m.openNotes(*m.project)
		}
		return m, nil
	case "U":
		n := 0
		for _, td := range cols[0] {
			td.Status = store.TodoQueued
			if m.mgr.Store().SaveTodo(td) == nil {
				n++
			}
		}
		m.reloadTodos()
		m.setFlash(fmt.Sprintf("queued %d todo(s): the scheduler processes them %d at a time", n, m.project.MaxParallel), false)
		return m, nil
	}
	td, ok := m.selectedTodo()
	if !ok {
		return m, nil
	}
	var err error
	msg := ""
	switch key {
	case "enter", "p":
		if td.Status == store.TodoDoing || td.Status == store.TodoReview {
			if td.SessionID != "" {
				if _, exists := m.mgr.Get(td.SessionID); exists {
					m.backTo = scrTodos
					m.openDetail(td.SessionID)
					return m, textinput.Blink
				}
			}
			return m, nil
		}
		if td.Status == store.TodoDone {
			return m, m.openTodoForm(&td)
		}
		var id string
		id, err = m.mgr.Dispatch(td.ID, m.defaults())
		msg = "processing " + td.Title + " → worker " + id
	case "e":
		return m, m.openTodoForm(&td)
	case "u":
		switch td.Status {
		case store.TodoOpen:
			err, msg = m.mgr.SetTodoStatus(td.ID, store.TodoQueued), "queued "+td.Title
		case store.TodoQueued:
			err, msg = m.mgr.SetTodoStatus(td.ID, store.TodoOpen), "unqueued "+td.Title
		}
	case "a":
		if td.Status == store.TodoReview || td.Status == store.TodoOpen || td.Status == store.TodoQueued {
			err, msg = m.mgr.SetTodoStatus(td.ID, store.TodoDone), "done: "+td.Title
		}
	case "r":
		switch td.Status {
		case store.TodoReview, store.TodoDoing:
			err, msg = m.mgr.RetryTodo(td.ID, "", m.defaults()), "retrying "+td.Title
		case store.TodoDone:
			err, msg = m.mgr.SetTodoStatus(td.ID, store.TodoOpen), "reopened "+td.Title
		}
	case "<", "shift+left":
		if c := colOf(td.Status); c > 0 && td.Status != store.TodoDoing {
			target := store.TodoStatuses[c-1]
			if target == store.TodoDoing {
				target = store.TodoQueued
			}
			err, msg = m.mgr.SetTodoStatus(td.ID, target), "moved to "+string(target)
		}
	case ">", "shift+right":
		if c := colOf(td.Status); c < 4 && td.Status != store.TodoDoing {
			target := store.TodoStatuses[c+1]
			if target == store.TodoDoing {
				target = store.TodoReview
			}
			err, msg = m.mgr.SetTodoStatus(td.ID, target), "moved to "+string(target)
		}
	case "+", "=", "-":
		if key == "-" {
			td.Priority = max(td.Priority-1, 0)
		} else {
			td.Priority = min(td.Priority+1, 3)
		}
		err = m.mgr.Store().SaveTodo(td)
	case "x":
		if td.SessionID != "" && m.mgr.Active(td.SessionID) {
			m.mgr.Stop(td.SessionID)
			msg = "stopping the worker for " + td.Title
		}
	case "d":
		if td.Status == store.TodoDoing && m.mgr.Active(td.SessionID) {
			m.setFlash("the worker is still running: x to stop it first", true)
			return m, nil
		}
		if m.todoRemoving != td.ID {
			m.todoRemoving = td.ID
			m.setFlash("press d again to delete \""+trunc(td.Title, 40)+"\" (its worker session stays)", true)
			return m, nil
		}
		m.todoRemoving = ""
		err, msg = m.mgr.Store().DeleteTodo(td.ID), "deleted "+td.Title
	}
	if err != nil {
		m.setFlash(err.Error(), true)
	} else if msg != "" {
		m.setFlash(msg, false)
	}
	m.reloadTodos()
	m.selectTodo(td.ID)
	return m, nil
}

func (m *Model) openTodoForm(td *store.Todo) tea.Cmd {
	w := m.modalWidth() - 10
	f := &todoForm{agent: "", priority: 1}
	f.title = newFormInput(w, "what should be done")
	f.body = textarea.New()
	f.body.ShowLineNumbers = false
	f.body.CharLimit = 16000
	f.body.SetWidth(w + 2)
	f.body.SetHeight(5)
	if m.h < 30 {
		f.body.SetHeight(2)
	}
	f.body.Prompt = "  "
	f.body.Placeholder = "details, acceptance criteria, links to notes…"
	f.body.FocusedStyle.CursorLine = lipgloss.NewStyle()
	f.body.FocusedStyle.Text = sIvory
	f.body.FocusedStyle.Placeholder = sFaint
	f.body.BlurredStyle = f.body.FocusedStyle
	if td != nil {
		f.editing, f.original = td.ID, *td
		f.title.SetValue(td.Title)
		f.body.SetValue(td.Body)
		f.agent, f.priority = td.Agent, td.Priority
		for i, r := range m.project.Repos {
			if r == td.Repo {
				f.repo = i + 1
			}
		}
	}
	m.todoForm = f
	return f.focusCurrent()
}

func (f *todoForm) focusCurrent() tea.Cmd {
	f.title.Blur()
	f.body.Blur()
	switch f.focus {
	case tfTitle:
		return f.title.Focus()
	case tfBody:
		return f.body.Focus()
	}
	return nil
}

func nextAgentOrDefault(a string) string {
	names := append([]string{""}, agent.Names()...)
	for i, n := range names {
		if n == a {
			return names[(i+1)%len(names)]
		}
	}
	return ""
}

func (m *Model) todoFormKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := m.todoForm
	switch k.String() {
	case "esc":
		m.todoForm = nil
		return m, nil
	case "tab":
		f.focus = (f.focus + 1) % (tfSave + 1)
		return m, f.focusCurrent()
	case "shift+tab":
		f.focus = (f.focus + tfSave) % (tfSave + 1)
		return m, f.focusCurrent()
	case "ctrl+s":
		return m.saveTodoForm()
	}
	switch f.focus {
	case tfTitle:
		if k.String() == "enter" {
			f.focus = tfBody
			return m, f.focusCurrent()
		}
		var cmd tea.Cmd
		f.title, cmd = f.title.Update(k)
		return m, cmd
	case tfBody:
		var cmd tea.Cmd
		f.body, cmd = f.body.Update(k)
		return m, cmd
	}
	switch k.String() {
	case "enter", " ", "right", "left":
		back := k.String() == "left"
		switch f.focus {
		case tfRepo:
			n := len(m.project.Repos) + 1
			if back {
				f.repo = (f.repo + n - 1) % n
			} else {
				f.repo = (f.repo + 1) % n
			}
		case tfAgent:
			f.agent = nextAgentOrDefault(f.agent)
		case tfPriority:
			if back {
				f.priority = (f.priority + 3) % 4
			} else {
				f.priority = (f.priority + 1) % 4
			}
		case tfSave:
			return m.saveTodoForm()
		}
	case "down":
		f.focus = min(f.focus+1, tfSave)
	case "up":
		f.focus = max(f.focus-1, tfTitle)
		return m, f.focusCurrent()
	}
	return m, nil
}

func (m *Model) saveTodoForm() (tea.Model, tea.Cmd) {
	f := m.todoForm
	td := f.original
	if f.editing == "" {
		td.ID = store.NewID()
		td.ProjectID = m.project.ID
		td.Status = store.TodoOpen
	}
	td.Title = strings.TrimSpace(f.title.Value())
	td.Body = strings.TrimSpace(f.body.Value())
	td.Agent, td.Priority = f.agent, f.priority
	td.Repo = ""
	if f.repo > 0 && f.repo <= len(m.project.Repos) {
		td.Repo = m.project.Repos[f.repo-1]
	}
	if td.Title == "" {
		f.err = "title is required"
		return m, nil
	}
	if err := m.mgr.Store().SaveTodo(td); err != nil {
		f.err = err.Error()
		return m, nil
	}
	m.todoForm = nil
	m.reloadTodos()
	m.selectTodo(td.ID)
	m.setFlash("saved "+td.Title, false)
	return m, nil
}

func priorityMark(p int) string {
	switch p {
	case 3:
		return sRose.Render("▲▲▲")
	case 2:
		return sGold.Render("▲▲")
	case 1:
		return sDim.Render("▲")
	}
	return ""
}

func (m *Model) todoCard(td store.Todo, w int, selected bool, color t.RGB) string {
	inner := max(w-4, 4)
	ag := td.Agent
	if ag == "" {
		ag = firstNonEmpty(m.project.DefaultAgent, m.opt.Agent)
	}
	l1 := agentGlyph(ag) + " " + sTitle.Render(trunc(td.Title, inner-6)) + " " + priorityMark(td.Priority)
	var l2 string
	switch {
	case td.SessionID != "":
		s, ok := m.mgr.Get(td.SessionID)
		if ok {
			l2 = statusPill(s.Status, m.frame) + sFaint.Render(" "+td.SessionID+" ") + sDim.Render(trunc(orDefault(s.LastLine, ""), inner-24))
		} else {
			l2 = sFaint.Render("worker " + td.SessionID + " released")
		}
	case td.Note != "":
		l2 = sDim.Render(trunc(td.Note, inner))
	case td.Body != "":
		l2 = sDim.Render(trunc(td.Body, inner))
	default:
		l2 = sFaint.Render("no details")
	}
	repo := "project repo"
	if td.Repo != "" {
		repo = filepath.Base(td.Repo)
	}
	l3 := sFaint.Render(trunc(td.ID+" · "+repo+" · "+ago(td.UpdatedAt), inner))
	border := lipgloss.RoundedBorder()
	bc := t.Lerp(color, t.Void, 0.55)
	switch {
	case selected:
		border, bc = lipgloss.ThickBorder(), t.Gold
	case m.hover == "todo:"+td.ID:
		bc = t.Lavender
	}
	return m.mark("todo:"+td.ID, lipgloss.NewStyle().Border(border).BorderForeground(bc.Color()).Padding(0, 1).Width(w-2).Render(l1+"\n"+l2+"\n"+l3))
}

func (m *Model) todosView() string {
	if m.project == nil {
		return m.boardView()
	}
	if time.Since(m.todosAt) > time.Second {
		m.reloadTodos()
	}
	bodyH := max(m.h-7, 4)
	cols := m.todoColumns()
	gap := 1
	visible := min(max((m.w+gap)/(18+gap), 1), 5)
	first := min(max(m.todoCol-visible/2, 0), 5-visible)
	cw := (m.w - (visible-1)*gap) / visible
	var views []string
	for c := first; c < first+visible; c++ {
		if c > first {
			views = append(views, strings.Repeat(" ", gap))
		}
		views = append(views, m.todoColumnView(c, cols[c], cw, bodyH))
	}
	head := sLavender.Bold(true).Render("◈ "+m.project.Name) + sFaint.Render("  todo board · ") +
		sDim.Render(fmt.Sprintf("max %d parallel · scheduler on", m.project.MaxParallel))
	head = lipgloss.NewStyle().MaxWidth(m.w).Render(head)
	keys := hints(m.w, m.chip("n", "n", "new"), m.chip("enter", "⏎", "process/open"), m.chip("u", "u", "queue"),
		m.chip("U", "U", "queue all"), m.chip("a", "a", "accept"), m.chip("r", "r", "retry"), m.chip("e", "e", "edit"),
		m.chip("<", "<", ""), m.chip(">", ">", "move"), m.chip("+", "+", "prio"), m.chip("o", "o", "notes"),
		m.chip("d", "d", "delete"), m.chip("esc", "esc", "back"))
	screen := m.header() + "\n" + head + "\n" + rule(m.w) + "\n" +
		lipgloss.NewStyle().Height(bodyH).MaxHeight(bodyH).Render(lipgloss.JoinHorizontal(lipgloss.Top, views...)) + "\n" + m.flashLine() + "\n" + keys
	if m.todoForm != nil {
		return m.overlay(m.todoFormView())
	}
	return screen
}

func (m *Model) todoColumnView(c int, items []store.Todo, w, h int) string {
	active := c == m.todoCol
	color := todoColColors[c]
	head := m.mark(fmt.Sprintf("tcol:%d", c), t.FG(color).Bold(true).Render(todoColGlyphs[c]+" "+todoColNames[c])+sDim.Render(fmt.Sprintf("  %d", len(items))))
	underline := t.FG(t.Lerp(color, t.Void, 0.6)).Render(strings.Repeat("━", w))
	if active {
		underline = t.FG(color).Render(strings.Repeat("━", w))
	}
	lines := []string{head, underline}
	const cardH = 5
	maxCards := max((h-3)/cardH, 1)
	sel := m.todoIdx[c]
	off := 0
	if sel >= maxCards {
		off = sel - maxCards + 1
	}
	end := min(off+maxCards, len(items))
	for i := off; i < end; i++ {
		lines = append(lines, m.todoCard(items[i], w, active && i == sel, color))
	}
	if more := len(items) - end; more > 0 {
		lines = append(lines, sDim.Render(fmt.Sprintf("  ⋯ %d more", more)))
	}
	if len(items) == 0 && c == 0 {
		lines = append(lines, "", sFaint.Render("  n to add a todo"))
	}
	return lipgloss.NewStyle().Width(w).Render(strings.Join(lines, "\n"))
}

func (m *Model) todoFormView() string {
	f := m.todoForm
	title := "◇ NEW TODO · " + strings.ToUpper(trunc(m.project.Name, 30))
	if f.editing != "" {
		title = "◇ EDIT TODO"
	}
	label := func(slot int, text string) string {
		style := sFaint
		if f.focus == slot {
			style = sGold
		}
		return m.mark(fmt.Sprintf("tform:%d", slot), style.Render(text))
	}
	choice := func(slot int, text string) string {
		prefix := "  "
		if f.focus == slot {
			prefix = sGold.Render("› ")
		}
		return prefix + text
	}
	repo := sDim.Render("project default (" + filepath.Base(orch.ProjectRepo(*m.project, m.opt.Dir)) + ")")
	if f.repo > 0 && f.repo <= len(m.project.Repos) {
		repo = sIvory.Render(tildeHome(m.project.Repos[f.repo-1]))
	}
	ag := sDim.Render("project default")
	if f.agent != "" {
		ag = agentLabel(f.agent)
	}
	prio := []string{"none", "low", "medium", "high"}[f.priority]
	lines := []string{
		label(tfTitle, "title"), f.title.View(),
		label(tfBody, "details"), f.body.View(), "",
		choice(tfRepo, label(tfRepo, "repo      ")+repo),
		choice(tfAgent, label(tfAgent, "agent     ")+ag),
		choice(tfPriority, label(tfPriority, "priority  ")+sIvory.Render(prio)+" "+priorityMark(f.priority)),
		"",
	}
	save := chipCap.Render("⏎ save")
	if f.focus == tfSave || m.hover == fmt.Sprintf("tform:%d", tfSave) {
		save = chipCapHover.Render("⏎ save")
	}
	lines = append(lines, hints(m.modalWidth()-6, m.mark(fmt.Sprintf("tform:%d", tfSave), save)+"   "+m.chip("esc", "esc", "cancel")+
		sFaint.Render("   tab move · ←→/space change · ctrl+s save")))
	if f.err != "" {
		lines = append(lines, "", sRose.Render("✗ "+trunc(f.err, m.modalWidth()-8)))
	}
	focusAt := map[int]int{tfTitle: 1, tfBody: 3, tfRepo: 5, tfAgent: 6, tfPriority: 7, tfSave: 9}[f.focus]
	return modal(title, m.fitForm(lines, focusAt), m.modalWidth())
}

func (m *Model) todoClick(kind, arg string) (tea.Model, tea.Cmd) {
	switch kind {
	case "tcol":
		var c int
		fmt.Sscanf(arg, "%d", &c)
		m.todoCol = c
		m.rememberTodo()
	case "todo":
		cols := m.todoColumns()
		for c := range cols {
			for i, td := range cols[c] {
				if td.ID != arg {
					continue
				}
				double := m.lastClick == arg && time.Since(m.lastClickAt) < 450*time.Millisecond
				already := m.todoCol == c && m.todoIdx[c] == i
				m.todoCol, m.todoIdx[c] = c, i
				m.todoSelID = arg
				m.lastClick, m.lastClickAt = arg, time.Now()
				if double || already {
					return m.handleKey(keyMsg("enter"))
				}
				return m, nil
			}
		}
	case "tform":
		if m.todoForm == nil {
			return m, nil
		}
		var slot int
		fmt.Sscanf(arg, "%d", &slot)
		m.todoForm.focus = slot
		if slot == tfSave {
			return m.saveTodoForm()
		}
		if slot >= tfRepo {
			return m.todoFormKey(keyMsg(" "))
		}
		return m, m.todoForm.focusCurrent()
	}
	return m, nil
}
