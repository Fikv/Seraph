package orch

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"seraph/internal/agent"
	"seraph/internal/store"
	"seraph/internal/workspace"
)

const projectPrompt = `
This worker belongs to the Seraph project %q.%s%s
Stay within the project's conventions.`

const notesPrompt = `
The project's notes (an Obsidian vault folder) are at %s and you can read them: check the notes relevant to your task before you start (architecture, decisions, progress log). Only edit notes when the task asks for it.`

type Defaults struct {
	Agent    string
	Model    string
	Approval agent.Approval
}

type scheduler struct {
	mu       sync.Mutex
	defaults Defaults
	running  bool
}

func (m *Manager) projectContext(projectID string) (dirs []string, prompt string) {
	if projectID == "" {
		return nil, ""
	}
	p, ok, err := m.st.Project(projectID)
	if err != nil || !ok {
		return nil, ""
	}
	desc := ""
	if strings.TrimSpace(p.Description) != "" {
		desc = " " + strings.TrimSpace(p.Description)
	}
	notes := ""
	if p.NotesDir != "" {
		if st, err := os.Stat(p.NotesDir); err == nil && st.IsDir() {
			dirs = append(dirs, p.NotesDir)
			notes = fmt.Sprintf(notesPrompt, p.NotesDir)
		}
	}
	return dirs, fmt.Sprintf(projectPrompt, p.Name, desc, notes)
}

func ProjectRepo(p store.Project, cwd string) string {
	for _, r := range p.Repos {
		if cwd != "" && (cwd == r || strings.HasPrefix(cwd, r+string(os.PathSeparator))) {
			return r
		}
	}
	if len(p.Repos) > 0 {
		return p.Repos[0]
	}
	return ""
}

func (m *Manager) Dispatch(todoID string, d Defaults) (string, error) {
	t, ok, err := m.st.Todo(todoID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("no todo %s", todoID)
	}
	p, ok, err := m.st.Project(t.ProjectID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("todo %s has no project", todoID)
	}
	repo := t.Repo
	if repo == "" {
		repo = ProjectRepo(p, "")
	}
	if repo == "" {
		return "", fmt.Errorf("project %s has no repositories: add one first", p.Name)
	}
	if _, err := workspace.RepoRoot(repo); err != nil {
		return "", fmt.Errorf("%s is not a git repository", repo)
	}
	claimed, err := m.st.ClaimTodo(t.ID)
	if err != nil {
		return "", err
	}
	if !claimed {
		return "", errors.New("this todo is already being processed")
	}
	ag := firstNonEmpty(t.Agent, p.DefaultAgent, d.Agent, "claude")
	model := ""
	if ag == d.Agent {
		model = d.Model
	}
	task := t.Title
	if b := strings.TrimSpace(t.Body); b != "" {
		task += "\n\n" + b
	}
	id, err := m.Spawn(SpawnOpts{
		Title: t.Title, Task: task, Agent: ag, Model: model, Approval: d.Approval,
		Repo: repo, ProjectID: p.ID, TodoID: t.ID,
	})
	if err != nil {
		t.Status, t.Note = TodoStatusBefore(t), "dispatch failed: "+err.Error()
		_ = m.st.SaveTodo(t)
		m.publish("todo:"+t.ID, true)
		return "", err
	}
	t, _, _ = m.st.Todo(todoID)
	t.Status, t.SessionID, t.Note = store.TodoDoing, id, ""
	if err := m.st.SaveTodo(t); err != nil {
		return id, err
	}
	m.publish("todo:"+t.ID, true)
	return id, nil
}

func TodoStatusBefore(t store.Todo) store.TodoStatus {
	if t.Status == store.TodoQueued {
		return store.TodoQueued
	}
	return store.TodoOpen
}

func (m *Manager) RetryTodo(todoID, prompt string, d Defaults) error {
	t, ok, err := m.st.Todo(todoID)
	if err != nil || !ok {
		return fmt.Errorf("no todo %s", todoID)
	}
	if strings.TrimSpace(prompt) == "" {
		prompt = continuePrompt
	}
	if t.SessionID != "" {
		if _, exists := m.Get(t.SessionID); exists {
			if m.Active(t.SessionID) {
				return errors.New("the worker for this todo is still running")
			}
			if err := m.Followup(t.SessionID, prompt); err != nil {
				return err
			}
			t.Status, t.Note = store.TodoDoing, ""
			if err := m.st.SaveTodo(t); err != nil {
				return err
			}
			m.publish("todo:"+t.ID, true)
			return nil
		}
	}
	t.Status, t.SessionID = store.TodoOpen, ""
	if err := m.st.SaveTodo(t); err != nil {
		return err
	}
	_, err = m.Dispatch(t.ID, d)
	return err
}

const continuePrompt = "Continue where you left off and finish the task."

func (m *Manager) SetTodoStatus(todoID string, status store.TodoStatus) error {
	t, ok, err := m.st.Todo(todoID)
	if err != nil || !ok {
		return fmt.Errorf("no todo %s", todoID)
	}
	if t.Status == store.TodoDoing && t.SessionID != "" && m.Active(t.SessionID) && status != store.TodoDoing {
		return errors.New("the worker for this todo is still running: stop it first")
	}
	if status == store.TodoDoing {
		return errors.New("use process to start a todo")
	}
	t.Status = status
	if status == store.TodoOpen || status == store.TodoQueued {
		t.Note = ""
	}
	if err := m.st.SaveTodo(t); err != nil {
		return err
	}
	m.publish("todo:"+t.ID, true)
	return nil
}

func (m *Manager) todoSessionEnded(todoID, sessionID string, status store.Status, errText string) {
	t, ok, err := m.st.Todo(todoID)
	if err != nil || !ok || t.SessionID != sessionID || t.Status != store.TodoDoing {
		return
	}
	t.Status = store.TodoReview
	switch status {
	case store.StatusDone:
		t.Note = "worker finished: review the branch"
	case store.StatusStopped:
		t.Note = "worker stopped"
	default:
		t.Note = "worker failed: " + firstLine(errText)
	}
	_ = m.st.SaveTodo(t)
	m.publish("todo:"+t.ID, true)
}

func (m *Manager) SetDefaults(d Defaults) {
	m.sched.mu.Lock()
	m.sched.defaults = d
	m.sched.mu.Unlock()
}

func (m *Manager) StartScheduler(d Defaults) {
	m.SetDefaults(d)
	m.sched.mu.Lock()
	if m.sched.running {
		m.sched.mu.Unlock()
		return
	}
	m.sched.running = true
	m.sched.mu.Unlock()
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			m.ScheduleOnce()
			select {
			case <-m.done:
				return
			case <-t.C:
			}
		}
	}()
}

func (m *Manager) ScheduleOnce() []error {
	m.sched.mu.Lock()
	d := m.sched.defaults
	m.sched.mu.Unlock()
	projects, err := m.st.Projects()
	if err != nil {
		return []error{err}
	}
	var errs []error
	for _, p := range projects {
		todos, err := m.st.Todos(p.ID)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		running := 0
		for _, t := range todos {
			if t.Status != store.TodoDoing {
				continue
			}
			s, exists := m.Get(t.SessionID)
			switch {
			case t.SessionID != "" && exists && m.Active(t.SessionID):
				running++
			case t.SessionID != "" && exists && s.Status != store.StatusWorking && s.Status != store.StatusQueued:
				m.todoSessionEnded(t.ID, t.SessionID, s.Status, s.Error)
			case !exists && time.Since(t.UpdatedAt) > staleAfter:
				t.Status, t.SessionID, t.Note = store.TodoOpen, "", "worker session disappeared"
				_ = m.st.SaveTodo(t)
			}
		}
		for _, t := range todos {
			if running >= p.MaxParallel {
				break
			}
			if t.Status != store.TodoQueued {
				continue
			}
			if _, err := m.Dispatch(t.ID, d); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", t.Title, err))
				continue
			}
			running++
		}
	}
	return errs
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
