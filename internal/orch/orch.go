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
	"seraph/internal/bus"
	"seraph/internal/mcp"
	"seraph/internal/store"
	"seraph/internal/team"
	"seraph/internal/workspace"
)

const workerPrompt = `You are a Seraph worker, one of several coding agents working in parallel.
You run in an isolated git worktree on branch %s; other workers handle other tasks on their own branches.
Complete the task fully and stay within its scope. Run the project's relevant checks when you can.
Do not run git add, git commit or git push: Seraph commits your changes on this branch when you finish.
If the task asks you to push, merge or publish, do everything else and say so: Seraph's release step (an independent validator, then merge into the user's branch and push) does that part after you.
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
	Title     string
	Task      string
	Agent     string
	Model     string
	Approval  agent.Approval
	Repo      string
	Dir       string
	ProjectID string
	TodoID    string
	MissionID string
	NodeID    string
	FromRef   string
	Shared    bool
	Branch    string
	Base      string
	Role      string
	ID        string
	ParentID  string
	TeamID    string
	AgentName string
}

const (
	heartbeatEvery = 5 * time.Second
	staleAfter     = 20 * time.Second
	publishEvery   = 150 * time.Millisecond
	persistEvery   = 700 * time.Millisecond
)

type Manager struct {
	st        *store.Store
	bus       *bus.Bus
	pid       int
	mu        sync.Mutex
	sessions  map[string]*store.Session
	logs      map[string][]agent.Event
	versions  map[string]int
	cancels   map[string]context.CancelFunc
	localLog  map[string]bool
	logCursor map[string]int64
	lastPub   map[string]time.Time
	lastSave  map[string]time.Time
	wg        sync.WaitGroup
	done      chan struct{}
	closeOnce sync.Once
	sched     scheduler
	tmu       sync.Mutex
	mmu       sync.Mutex
	halting   bool
}

func New(st *store.Store, b *bus.Bus) (*Manager, error) {
	m := &Manager{
		st:        st,
		bus:       b,
		pid:       os.Getpid(),
		sessions:  map[string]*store.Session{},
		logs:      map[string][]agent.Event{},
		versions:  map[string]int{},
		cancels:   map[string]context.CancelFunc{},
		localLog:  map[string]bool{},
		logCursor: map[string]int64{},
		lastPub:   map[string]time.Time{},
		lastSave:  map[string]time.Time{},
		done:      make(chan struct{}),
	}
	if err := m.reload(); err != nil {
		return nil, err
	}
	go m.heartbeatLoop()
	return m, nil
}

func (m *Manager) Store() *store.Store { return m.st }

func (m *Manager) Bus() *bus.Bus { return m.bus }

func (m *Manager) Close() {
	m.closeOnce.Do(func() { close(m.done) })
}

func stale(s store.Session) bool {
	return (s.Status == store.StatusWorking || s.Status == store.StatusQueued) && time.Since(s.HeartbeatAt) > staleAfter
}

func (m *Manager) reload() error {
	list, err := m.st.List()
	if err != nil {
		return err
	}
	var fixed []store.Session
	m.mu.Lock()
	seen := map[string]bool{}
	for i := range list {
		s := list[i]
		seen[s.ID] = true
		if _, local := m.cancels[s.ID]; local {
			continue
		}
		if stale(s) {
			s.Status = store.StatusStopped
			s.Error = "interrupted: the seraph process running this worker exited"
			fixed = append(fixed, s)
		}
		cur, ok := m.sessions[s.ID]
		if !ok || cur.UpdatedAt != s.UpdatedAt || cur.Status != s.Status || cur.HeartbeatAt.IsZero() != s.HeartbeatAt.IsZero() {
			cp := s
			m.sessions[s.ID] = &cp
			m.versions[s.ID]++
		}
	}
	for id := range m.sessions {
		if _, local := m.cancels[id]; !seen[id] && !local {
			delete(m.sessions, id)
			delete(m.logs, id)
			delete(m.logCursor, id)
			delete(m.localLog, id)
		}
	}
	m.mu.Unlock()
	for _, s := range fixed {
		_ = m.st.Save(s)
	}
	return nil
}

func (m *Manager) Refresh() { _ = m.reload() }

func (m *Manager) heartbeatLoop() {
	t := time.NewTicker(heartbeatEvery)
	defer t.Stop()
	for {
		select {
		case <-m.done:
			return
		case <-t.C:
		}
		m.mu.Lock()
		ids := make([]string, 0, len(m.cancels))
		for id := range m.cancels {
			ids = append(ids, id)
		}
		m.mu.Unlock()
		stop, err := m.st.Heartbeat(ids, m.pid, time.Now())
		if err != nil {
			continue
		}
		for _, id := range stop {
			_ = m.st.RequestStop(id, false)
			m.Stop(id)
		}
	}
}

func (m *Manager) publish(id string, force bool) {
	if m.bus == nil {
		return
	}
	m.mu.Lock()
	if !force && time.Since(m.lastPub[id]) < publishEvery {
		m.mu.Unlock()
		return
	}
	m.lastPub[id] = time.Now()
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	m.bus.Publish(ctx, id)
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

func (m *Manager) LogVersion(id string) int {
	m.mu.Lock()
	local := m.localLog[id]
	cursor := m.logCursor[id]
	m.mu.Unlock()
	if !local {
		evs, last, err := m.st.ReadLogSince(id, cursor)
		m.mu.Lock()
		if err == nil && len(evs) > 0 && m.logCursor[id] == cursor {
			m.logs[id] = append(m.logs[id], evs...)
			m.logCursor[id] = last
			m.versions[id]++
		}
		m.mu.Unlock()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.versions[id]
}

func (m *Manager) Log(id string) ([]agent.Event, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.localLog[id] {
		evs, last, err := m.st.ReadLogSince(id, m.logCursor[id])
		if err == nil && len(evs) > 0 {
			m.logs[id] = append(m.logs[id], evs...)
			m.logCursor[id] = last
			m.versions[id]++
		}
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

func (m *Manager) RunningElsewhere(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, local := m.cancels[id]; local {
		return false
	}
	s, ok := m.sessions[id]
	return ok && (s.Status == store.StatusWorking || s.Status == store.StatusQueued) && !stale(*s)
}

func (m *Manager) Active(id string) bool { return m.Running(id) || m.RunningElsewhere(id) }

func (m *Manager) LocalRunning() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.cancels)
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
		ID:        firstNonEmpty(o.ID, store.NewID()),
		Title:     o.Title,
		Task:      o.Task,
		Agent:     o.Agent,
		Model:     o.Model,
		Approval:  string(o.Approval),
		Repo:      o.Repo,
		Dir:       o.Dir,
		ProjectID: o.ProjectID,
		TodoID:    o.TodoID,
		MissionID: o.MissionID,
		NodeID:    o.NodeID,
		Role:      o.Role,
		ParentID:  o.ParentID,
		TeamID:    o.TeamID,
		AgentName: o.AgentName,
		Status:    store.StatusQueued,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if s.Title == "" {
		s.Title = titleFrom(o.Task)
	}
	switch {
	case o.Shared:
		s.Branch, s.Base = o.Branch, o.Base
	case o.Repo != "":
		ref := firstNonEmpty(o.FromRef, "HEAD")
		s.Base = firstNonEmpty(o.FromRef, workspace.CurrentBranch(o.Repo))
		dir, branch, err := workspace.CreateFrom(store.Home(), o.Repo, s.ID, ref)
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
	m.localLog[s.ID] = true
	m.mu.Unlock()

	m.start(s.ID, o.Task, "", false)
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
	if m.RunningElsewhere(id) {
		return errors.New("the worker is running in another seraph process; stop it first or wait")
	}
	m.Log(id)
	m.mu.Lock()
	m.localLog[id] = true
	m.mu.Unlock()
	if _, err := os.Stat(s.Dir); err != nil {
		return fmt.Errorf("workspace is gone: %s", s.Dir)
	}
	m.start(id, prompt, resume, false)
	return nil
}

const explainPrompt = `You are the Seraph worker that made the changes on branch %s, now answering the user's questions about them.
Explain what changed and why, referring to concrete files and lines. Read files if you need to.
Do not modify, create or delete any files, and do not run git commands that change anything.`

func (m *Manager) Ask(id, question, file string, hunk string) error {
	m.mu.Lock()
	s, ok := m.sessions[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("no session %s", id)
	}
	if _, running := m.cancels[id]; running {
		m.mu.Unlock()
		return errors.New("the worker is still running; wait for it to finish before asking")
	}
	resume, dir := s.AgentSessionID, s.Dir
	m.mu.Unlock()
	if m.RunningElsewhere(id) {
		return errors.New("the worker is running in another seraph process")
	}
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("the worktree is gone (%s), so the agent cannot be resumed there", dir)
	}
	question = strings.TrimSpace(question)
	if question == "" {
		question = "Explain what happened here and why you made this change."
	}
	prompt := question
	if hunk != "" {
		prompt += "\n\nThis is about " + file + ":\n```diff\n" + hunk + "\n```"
	} else if file != "" {
		prompt += "\n\nThis is about the changes in " + file + "."
	}
	m.Log(id)
	m.mu.Lock()
	m.localLog[id] = true
	m.mu.Unlock()
	m.start(id, prompt, resume, true)
	return nil
}

func (m *Manager) Stop(id string) {
	m.mu.Lock()
	cancel := m.cancels[id]
	m.mu.Unlock()
	if cancel != nil {
		cancel()
		return
	}
	if m.RunningElsewhere(id) {
		_ = m.st.RequestStop(id, true)
		m.publish(id, true)
	}
}

func (m *Manager) StopAll() {
	m.mu.Lock()
	m.halting = true
	for _, c := range m.cancels {
		c()
	}
	m.mu.Unlock()
	m.wg.Wait()
}

func (m *Manager) Wait() { m.wg.Wait() }

func (m *Manager) Remove(id string) error {
	if m.RunningElsewhere(id) {
		return errors.New("the worker is running in another seraph process; stop it first")
	}
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
	delete(m.localLog, id)
	delete(m.logCursor, id)
	m.mu.Unlock()
	if s.TodoID != "" {
		if t, ok, _ := m.st.Todo(s.TodoID); ok && t.SessionID == id {
			if t.Status == store.TodoDoing || t.Status == store.TodoReview {
				t.Status = store.TodoOpen
			}
			t.SessionID, t.Note = "", "worker "+id+" was released"
			_ = m.st.SaveTodo(t)
		}
	}
	m.publish(id, true)
	return nil
}

func (m *Manager) start(id, prompt, resume string, ask bool) {
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	s := m.sessions[id]
	m.cancels[id] = cancel
	prevStatus, prevErr := s.Status, s.Error
	s.Status = store.StatusWorking
	s.Error = ""
	s.Turns++
	s.UpdatedAt = time.Now()
	s.OwnerPID = m.pid
	s.HeartbeatAt = time.Now()
	snapshot := *s
	m.mu.Unlock()
	_ = m.st.RequestStop(id, false)
	_ = m.st.Save(snapshot)
	m.publish(id, true)
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
	if snapshot.Role == roleValidatorSession {
		spec.SystemPrompt = roleRules + "\n" + roleIdentity[store.RoleValidator]
	}
	if snapshot.TeamID != "" {
		sys, dirs, tools := m.teamSystem(snapshot)
		spec.SystemPrompt = sys
		spec.AddDirs = append(spec.AddDirs, dirs...)
		if tools {
			if exe, err := os.Executable(); err == nil {
				spec.Team = []string{exe, "team", id}
				spec.AllowTools = append(spec.AllowTools, team.ToolPrefix)
			}
		}
	}
	if snapshot.NodeID != "" {
		sys, dirs := m.nodeSystem(snapshot)
		spec.SystemPrompt = sys
		spec.AddDirs = append(spec.AddDirs, dirs...)
	}
	if ask {
		spec.Approval = agent.ApprovalAsk
		spec.SystemPrompt = fmt.Sprintf(explainPrompt, branchOr(snapshot.Branch))
	}
	if dirs, extra := m.projectContext(snapshot.ProjectID); extra != "" {
		spec.AddDirs = append(spec.AddDirs, dirs...)
		spec.SystemPrompt += "\n" + extra
	}
	if cfg, err := mcp.Load(m.st); err == nil {
		spec.ApproveMCPs = cfg.AnyCursor()
	}
	switch spec.Approval {
	case agent.ApprovalSafe, agent.ApprovalTest, agent.ApprovalConfirm, agent.ApprovalYolo:
		for _, n := range mcp.Trusted(m.st) {
			spec.AllowTools = append(spec.AllowTools, mcp.ToolPattern(n))
		}
	}
	if spec.Approval == agent.ApprovalConfirm {
		if exe, err := os.Executable(); err == nil {
			spec.Permit = []string{exe, "permit", id}
		}
	}

	checkoutLead := snapshot.Role == roleOrchestrator && !snapshot.Worktree && snapshot.Branch != "" && spec.Approval != agent.ApprovalReadOnly && !ask
	cleanAtStart := false
	if checkoutLead {
		dirty, err := workspace.Dirty(snapshot.Dir)
		cleanAtStart = err == nil && dirty == ""
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
		writes := snapshot.Worktree || (snapshot.MissionID != "" && snapshot.Branch != "" && spec.Approval != agent.ApprovalReadOnly)
		if err == nil && !resultErr && writes && !ask {
			m.commit(id, snapshot, prompt)
		}
		if err == nil && !resultErr && checkoutLead {
			switch cur := workspace.CurrentBranch(snapshot.Dir); {
			case cur != snapshot.Branch:
				m.record(id, agent.Event{Kind: agent.KindError, Text: "your checkout is now on " + cur + ", not " + snapshot.Branch + ": Seraph did not commit", Time: time.Now()})
			case cleanAtStart:
				m.commit(id, snapshot, prompt)
			default:
				if d, _ := workspace.Dirty(snapshot.Dir); d != "" {
					m.record(id, agent.Event{Kind: agent.KindSystem, Text: "not committed: your checkout had uncommitted changes before this turn, so the changes stay in the working tree for you to review", Time: time.Now()})
				}
			}
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
		if ask && err == nil && !resultErr && prevStatus != store.StatusWorking && prevStatus != store.StatusQueued {
			s.Status, s.Error = prevStatus, prevErr
		}
		s.UpdatedAt = time.Now()
		snapshot := *s
		m.mu.Unlock()
		_ = m.st.Save(snapshot)
		_ = m.st.ExpirePermissions(id)
		if err != nil && !errors.Is(err, context.Canceled) {
			m.record(id, agent.Event{Kind: agent.KindError, Text: err.Error(), Time: time.Now()})
		}
		if snapshot.TodoID != "" && !ask {
			m.todoSessionEnded(snapshot.TodoID, id, snapshot.Status, snapshot.Error)
		}
		m.publish(id, true)
		if snapshot.MissionID != "" && !ask {
			m.nodeSessionEnded(snapshot)
		}
		if snapshot.Role == roleValidatorSession {
			m.validationEnded(snapshot)
		}
		if snapshot.TeamID != "" && !ask {
			m.teamTurnEnded(snapshot)
		}
	}()
}

func (m *Manager) commit(id string, s store.Session, prompt string) {
	subject, body := strings.TrimPrefix(s.Title, "Orchestrator: "), s.Task
	if s.Turns > 1 {
		subject, body = titleFrom(prompt), prompt
	}
	if strings.HasPrefix(strings.TrimSpace(prompt), "Seraph:") {
		subject, body = "Integrate team results", ""
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
	if ev.Kind == agent.KindLimits {
		m.saveLimits(id, ev.Limits)
		return
	}
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
	s.HeartbeatAt = time.Now()
	persist := ev.Kind == agent.KindInit || ev.Kind == agent.KindResult ||
		(ev.Kind != agent.KindThinking && time.Since(m.lastSave[id]) > persistEvery)
	if persist {
		m.lastSave[id] = time.Now()
	}
	snapshot := *s
	m.mu.Unlock()

	if !(ev.Kind == agent.KindThinking && ev.Delta) {
		_, _ = m.st.AppendLog(id, ev)
	}
	if persist {
		_ = m.st.Save(snapshot)
	}
	m.publish(id, ev.Kind == agent.KindResult)
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

func limitsKey(agentName string) string { return "limits:" + agentName }

func (m *Manager) saveLimits(sessionID string, ls []agent.Limit) {
	m.mu.Lock()
	s := m.sessions[sessionID]
	m.mu.Unlock()
	name := "claude"
	if s != nil {
		name = s.Agent
	}
	data, err := json.Marshal(agent.Limits{Agent: name, Windows: ls, Captured: time.Now()})
	if err == nil {
		_ = m.st.SetSetting(limitsKey(name), string(data))
	}
}

func (m *Manager) Limits(agentName string) (agent.Limits, bool) {
	raw := m.st.Setting(limitsKey(agentName))
	if raw == "" {
		return agent.Limits{}, false
	}
	var l agent.Limits
	if json.Unmarshal([]byte(raw), &l) != nil {
		return agent.Limits{}, false
	}
	return l, true
}

func (m *Manager) PendingPermissions() []store.Permission {
	ps, err := m.st.PendingPermissions()
	if err != nil {
		return nil
	}
	var out []store.Permission
	for _, p := range ps {
		if s, ok := m.Get(p.SessionID); ok && (m.Active(p.SessionID) || s.Status == store.StatusWorking) {
			out = append(out, p)
			continue
		}
		_ = m.st.ExpirePermissions(p.SessionID)
	}
	return out
}

func (m *Manager) DecidePermission(id string, allow, always bool, message string) error {
	p, ok, err := m.st.Permission(id)
	if err != nil || !ok {
		return fmt.Errorf("no permission request %s", id)
	}
	if allow && always {
		prefix := ""
		if p.Tool == "Bash" {
			prefix = store.CommandPrefix(p.Command())
		}
		if err := m.st.AddPermitRule(p.SessionID, p.Tool, prefix); err != nil {
			return err
		}
	}
	status := store.PermDeny
	if allow {
		status = store.PermAllow
	}
	decided, err := m.st.DecidePermission(id, status, message)
	if err == nil && !decided {
		return errors.New("this request was already answered or expired")
	}
	m.publish(p.SessionID, true)
	return err
}

func (m *Manager) SetApproval(id string, a agent.Approval) error {
	m.mu.Lock()
	s, ok := m.sessions[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("no session %s", id)
	}
	if _, running := m.cancels[id]; running {
		m.mu.Unlock()
		return errors.New("the worker is running: change its mode when it stops")
	}
	if a == agent.ApprovalYolo && !s.Worktree {
		m.mu.Unlock()
		return errors.New("yolo needs a worktree; this worker shares a directory")
	}
	s.Approval = string(a)
	s.UpdatedAt = time.Now()
	snapshot := *s
	m.mu.Unlock()
	err := m.st.Save(snapshot)
	m.publish(id, true)
	return err
}
