package orch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"seraph/internal/agent"
	"seraph/internal/store"
	"seraph/internal/workspace"
)

const workerPrompt = `You are a Seraph worker, one of several coding agents working in parallel.
You run in an isolated git worktree on branch %s; other workers handle other tasks on their own branches.
Complete the task fully and stay within its scope. Run the project's relevant checks when you can.
Do not run git add, git commit or git push: Seraph commits your changes on this branch when you finish.
End with a short summary of what changed.`

const plannerPrompt = `You are the Seraph orchestrator. Split the goal below into 1-5 independent tasks that separate coding agents can do in parallel, each in its own git worktree branched from the current HEAD.
Explore the repository briefly (read-only) so the tasks fit the code. Avoid tasks that edit the same files. Every task must be self-contained: the worker sees only its own task text.
Do not modify any files.
Reply with ONLY a JSON array, no prose and no code fences:
[{"title": "short title", "task": "complete instructions for the worker"}]

Goal:
%s`

type PlannedTask struct {
	Title string `json:"title"`
	Task  string `json:"task"`
	Agent string `json:"-"`
	Skip  bool   `json:"-"`
}

type SpawnOpts struct {
	Title    string
	Task     string
	Agent    string
	Model    string
	Approval agent.Approval
	Repo     string
	Dir      string
}

type Manager struct {
	st       *store.Store
	mu       sync.Mutex
	sessions map[string]*store.Session
	logs     map[string][]agent.Event
	versions map[string]int
	cancels  map[string]context.CancelFunc
	wg       sync.WaitGroup
}

func New(st *store.Store) (*Manager, error) {
	list, err := st.List()
	if err != nil {
		return nil, err
	}
	m := &Manager{
		st:       st,
		sessions: map[string]*store.Session{},
		logs:     map[string][]agent.Event{},
		versions: map[string]int{},
		cancels:  map[string]context.CancelFunc{},
	}
	for i := range list {
		s := list[i]
		if s.Status == store.StatusWorking || s.Status == store.StatusQueued {
			s.Status = store.StatusStopped
			s.Error = "interrupted: seraph exited while the agent was running"
			_ = st.Save(s)
		}
		m.sessions[s.ID] = &s
	}
	return m, nil
}

func (m *Manager) Sessions() []store.Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]store.Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

func (m *Manager) Get(id string) (store.Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return store.Session{}, false
	}
	return *s, true
}

func (m *Manager) Log(id string) ([]agent.Event, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.logs[id]; !ok {
		evs, _ := m.st.ReadLog(id)
		if evs == nil {
			evs = []agent.Event{}
		}
		m.logs[id] = evs
	}
	evs := m.logs[id]
	return append([]agent.Event(nil), evs...), m.versions[id]
}

func (m *Manager) Running(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.cancels[id]
	return ok
}

func (m *Manager) Spawn(o SpawnOpts) (string, error) {
	if _, err := agent.Get(o.Agent); err != nil {
		return "", err
	}
	if strings.TrimSpace(o.Task) == "" {
		return "", errors.New("task is empty")
	}
	if o.Approval == "" {
		o.Approval = agent.ApprovalSafe
	}
	now := time.Now()
	s := store.Session{
		ID:        store.NewID(),
		Title:     o.Title,
		Task:      o.Task,
		Agent:     o.Agent,
		Model:     o.Model,
		Approval:  string(o.Approval),
		Repo:      o.Repo,
		Dir:       o.Dir,
		Status:    store.StatusQueued,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if s.Title == "" {
		s.Title = titleFrom(o.Task)
	}
	if o.Repo != "" {
		s.Base = workspace.CurrentBranch(o.Repo)
		dir, branch, err := workspace.Create(store.Home(), o.Repo, s.ID)
		if err != nil {
			return "", err
		}
		s.Dir, s.Branch, s.Worktree = dir, branch, true
	}
	if s.Dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		s.Dir = wd
	}
	if err := m.st.Save(s); err != nil {
		return "", err
	}
	m.mu.Lock()
	m.sessions[s.ID] = &s
	m.logs[s.ID] = []agent.Event{}
	m.mu.Unlock()

	m.start(s.ID, o.Task, "")
	return s.ID, nil
}

func (m *Manager) Followup(id, prompt string) error {
	m.mu.Lock()
	s, ok := m.sessions[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("no session %s", id)
	}
	if _, running := m.cancels[id]; running {
		m.mu.Unlock()
		return errors.New("the worker is still running; stop it first or wait")
	}
	resume := s.AgentSessionID
	m.mu.Unlock()
	if _, err := os.Stat(s.Dir); err != nil {
		return fmt.Errorf("workspace is gone: %s", s.Dir)
	}
	m.start(id, prompt, resume)
	return nil
}

func (m *Manager) Stop(id string) {
	m.mu.Lock()
	cancel := m.cancels[id]
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (m *Manager) StopAll() {
	m.mu.Lock()
	for _, c := range m.cancels {
		c()
	}
	m.mu.Unlock()
	m.wg.Wait()
}

func (m *Manager) Wait() { m.wg.Wait() }

func (m *Manager) Remove(id string) error {
	m.Stop(id)
	m.mu.Lock()
	s, ok := m.sessions[id]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("no session %s", id)
	}
	for i := 0; i < 50 && m.Running(id); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if s.Worktree && s.Repo != "" {
		if err := workspace.Remove(s.Repo, s.Dir); err != nil {
			return err
		}
	}
	if err := m.st.Delete(id); err != nil {
		return err
	}
	m.mu.Lock()
	delete(m.sessions, id)
	delete(m.logs, id)
	delete(m.versions, id)
	m.mu.Unlock()
	return nil
}

func (m *Manager) start(id, prompt, resume string) {
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	s := m.sessions[id]
	m.cancels[id] = cancel
	s.Status = store.StatusWorking
	s.Error = ""
	s.Turns++
	s.UpdatedAt = time.Now()
	snapshot := *s
	m.mu.Unlock()
	_ = m.st.Save(snapshot)
	m.record(id, agent.Event{Kind: agent.KindUser, Text: prompt, Time: time.Now()})

	d, _ := agent.Get(snapshot.Agent)
	spec := agent.Spec{
		Prompt:       prompt,
		Dir:          snapshot.Dir,
		Model:        snapshot.Model,
		ResumeID:     resume,
		Approval:     agent.Approval(snapshot.Approval),
		SystemPrompt: fmt.Sprintf(workerPrompt, branchOr(snapshot.Branch)),
	}

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer cancel()
		var resultErr bool
		err := agent.Run(ctx, d, spec, func(ev agent.Event) {
			if ev.Kind == agent.KindResult && ev.IsError {
				resultErr = true
			}
			m.record(id, ev)
		})
		if err == nil && !resultErr && snapshot.Worktree {
			m.commit(id, snapshot, prompt)
		}

		m.mu.Lock()
		delete(m.cancels, id)
		s := m.sessions[id]
		if s == nil {
			m.mu.Unlock()
			return
		}
		switch {
		case errors.Is(err, context.Canceled):
			s.Status = store.StatusStopped
		case err != nil:
			s.Status = store.StatusFailed
			s.Error = err.Error()
		case resultErr:
			s.Status = store.StatusFailed
			if s.Error == "" {
				s.Error = "the agent reported an error"
			}
		default:
			s.Status = store.StatusDone
		}
		s.UpdatedAt = time.Now()
		snapshot := *s
		m.mu.Unlock()
		_ = m.st.Save(snapshot)
		if err != nil && !errors.Is(err, context.Canceled) {
			m.record(id, agent.Event{Kind: agent.KindError, Text: err.Error(), Time: time.Now()})
		}
	}()
}

func (m *Manager) commit(id string, s store.Session, prompt string) {
	subject, body := s.Title, s.Task
	if s.Turns > 1 {
		subject, body = titleFrom(prompt), prompt
	}
	msg := subject
	if b := strings.TrimSpace(body); b != "" && b != strings.TrimSpace(subject) {
		msg += "\n\n" + b
	}
	sha, err := workspace.CommitAll(s.Dir, msg)
	switch {
	case err != nil:
		m.record(id, agent.Event{Kind: agent.KindError, Text: "commit failed: " + err.Error(), Time: time.Now()})
	case sha != "":
		m.record(id, agent.Event{Kind: agent.KindSystem, Text: "committed " + sha + " on " + s.Branch, Time: time.Now()})
	}
}

func (m *Manager) record(id string, ev agent.Event) {
	m.mu.Lock()
	s := m.sessions[id]
	if s == nil {
		m.mu.Unlock()
		return
	}
	logs := m.logs[id]
	if ev.Kind == agent.KindThinking && ev.Delta && len(logs) > 0 && logs[len(logs)-1].Kind == agent.KindThinking && logs[len(logs)-1].Delta {
		logs[len(logs)-1].Text += ev.Text
	} else {
		logs = append(logs, ev)
	}
	m.logs[id] = logs
	m.versions[id]++

	if ev.SessionID != "" {
		s.AgentSessionID = ev.SessionID
	}
	s.Cost += ev.Cost
	switch ev.Kind {
	case agent.KindText:
		s.LastLine = lastNonEmpty(ev.Text)
	case agent.KindToolStart:
		s.LastLine = ev.Tool + " " + ev.Text
	case agent.KindResult:
		if ev.IsError {
			s.Error = firstLine(ev.Text)
		}
	}
	s.UpdatedAt = time.Now()
	persist := ev.Kind == agent.KindInit || ev.Kind == agent.KindResult
	snapshot := *s
	m.mu.Unlock()

	if !(ev.Kind == agent.KindThinking && ev.Delta) {
		_ = m.st.AppendLog(id, ev)
	}
	if persist {
		_ = m.st.Save(snapshot)
	}
}

func (m *Manager) Plan(ctx context.Context, goal, agentName, model, repo string) ([]PlannedTask, error) {
	d, err := agent.Get(agentName)
	if err != nil {
		return nil, err
	}
	var text, result strings.Builder
	runErr := agent.Run(ctx, d, agent.Spec{
		Prompt:   fmt.Sprintf(plannerPrompt, goal),
		Dir:      repo,
		Model:    model,
		Approval: agent.ApprovalReadOnly,
	}, func(ev agent.Event) {
		switch ev.Kind {
		case agent.KindText:
			text.WriteString(ev.Text + "\n")
		case agent.KindResult:
			result.WriteString(ev.Text)
		}
	})
	if runErr != nil {
		return nil, runErr
	}
	out := result.String()
	tasks, err := parsePlan(out)
	if err != nil {
		tasks, err = parsePlan(text.String())
	}
	if err != nil {
		return nil, fmt.Errorf("planner did not return a task list: %w", err)
	}
	for i := range tasks {
		tasks[i].Agent = agentName
	}
	return tasks, nil
}

func parsePlan(s string) ([]PlannedTask, error) {
	start := strings.Index(s, "[")
	end := strings.LastIndex(s, "]")
	if start < 0 || end <= start {
		return nil, errors.New("no JSON array in reply")
	}
	var tasks []PlannedTask
	if err := json.Unmarshal([]byte(s[start:end+1]), &tasks); err != nil {
		return nil, err
	}
	var out []PlannedTask
	for _, t := range tasks {
		if strings.TrimSpace(t.Task) != "" {
			if t.Title == "" {
				t.Title = titleFrom(t.Task)
			}
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("empty task list")
	}
	return out, nil
}

func titleFrom(task string) string {
	t := firstLine(task)
	r := []rune(t)
	if len(r) > 60 {
		return string(r[:59]) + "…"
	}
	return t
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

func lastNonEmpty(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if l == "" || strings.HasPrefix(l, "```") || strings.Trim(l, "-*_=#") == "" {
			continue
		}
		return l
	}
	return ""
}

func branchOr(b string) string {
	if b == "" {
		return "(no worktree: shared directory)"
	}
	return b
}
