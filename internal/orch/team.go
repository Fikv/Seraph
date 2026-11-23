package orch

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"seraph/internal/agent"
	"seraph/internal/store"
	"seraph/internal/team"
)

const roleOrchestrator = "orchestrator"
const roleSubagent = "subagent"

type TeamOpts struct {
	Goal      string
	Repo      string
	Dir       string
	ProjectID string
	Agent     string
	Model     string
	Approval  agent.Approval
}

func (m *Manager) StartTeam(o TeamOpts) (string, error) {
	if strings.TrimSpace(o.Goal) == "" {
		return "", errors.New("tell the orchestrator what you want")
	}
	if o.Repo == "" {
		return "", errors.New("an orchestrator needs a git repository")
	}
	id := store.NewID()
	dir := m.teamDir(id, o.Goal, o.ProjectID)
	if err := os.MkdirAll(filepath.Join(dir, "results"), 0o755); err != nil {
		return "", err
	}
	_ = os.WriteFile(filepath.Join(dir, team.NotesFile), []byte("# Team notebook\n\n## Goal\n\n"+strings.TrimSpace(o.Goal)+"\n"), 0o644)
	if err := m.st.SetSetting(team.DirSetting+id, dir); err != nil {
		return "", err
	}
	return m.Spawn(SpawnOpts{
		ID: id, Title: "Orchestrator: " + titleFrom(o.Goal), Task: o.Goal, Agent: firstNonEmpty(o.Agent, "claude"), Model: o.Model,
		Approval: o.Approval, Repo: o.Repo, Dir: o.Dir, ProjectID: o.ProjectID, Role: roleOrchestrator, TeamID: id,
	})
}

func (m *Manager) teamDir(id, goal, projectID string) string {
	name := time.Now().Format("2006-01-02") + " " + slug(titleFrom(goal)) + " " + id
	if projectID != "" {
		if p, ok, _ := m.st.Project(projectID); ok && p.NotesDir != "" {
			return filepath.Join(p.NotesDir, "Seraph Teams", name)
		}
	}
	return filepath.Join(store.Home(), "teams", name)
}

func (m *Manager) TeamMembers(teamID string) []store.Session {
	all, _ := m.st.TeamSessions(teamID)
	return all
}

func (m *Manager) teamSystem(s store.Session) (string, []string, bool) {
	dir := team.Dir(m.st, s.TeamID)
	dirs := []string{}
	if dir != "" {
		dirs = append(dirs, dir)
	}
	claude := s.Agent == "claude"
	if s.Role == roleOrchestrator {
		return fmt.Sprintf(orchestratorPrompt, branchOr(s.Branch), dir), dirs, claude
	}
	parent := "the orchestrator"
	if p, ok := m.Get(s.ParentID); ok {
		parent = team.DisplayName(p)
	}
	tools := ""
	if claude {
		extra := ""
		if team.Depth(m.st, s) < team.MaxDepth {
			extra = ", spawn_agent(name, role, task, paths[], readonly, model), wait_agents(agents[]), merge_agent(agent), stop_agent(agent) (you may create helpers of your own; keep it to what the task needs)"
		}
		tools = fmt.Sprintf(subagentToolsNote, extra) + "\n"
	}
	role := strings.TrimSpace(strings.SplitN(s.Title, ":", 2)[0])
	sys := fmt.Sprintf(subagentPrompt, team.DisplayName(s), strings.ToLower(role), parent, branchOr(s.Branch),
		team.Tree(m.st, s.TeamID, s.ID), dir, tools)
	return sys + "\n" + fmt.Sprintf(workerPrompt, branchOr(s.Branch)), dirs, claude
}

func (m *Manager) StartTeamLoop() {
	m.sched.mu.Lock()
	if m.sched.teamLoop {
		m.sched.mu.Unlock()
		return
	}
	m.sched.teamLoop = true
	m.sched.mu.Unlock()
	go func() {
		t := time.NewTicker(300 * time.Millisecond)
		defer t.Stop()
		for {
			m.TeamOnce()
			select {
			case <-m.done:
				return
			case <-t.C:
			}
		}
	}()
}

func (m *Manager) TeamOnce() {
	if m.stopping() {
		return
	}
	reqs, err := m.st.PendingTeamRequests()
	if err != nil {
		return
	}
	for _, r := range reqs {
		var res string
		var err error
		switch r.Op {
		case "spawn":
			res, err = m.teamSpawn(r)
		case "deliver":
			to, _ := r.Args["to"].(string)
			if !m.Active(to) {
				m.deliverMessages(to)
			}
			res = "ok"
		case "mission":
			res, err = m.teamMission(r)
		default:
			err = fmt.Errorf("unknown request %s", r.Op)
		}
		if err != nil {
			_ = m.st.FinishTeamRequest(r.ID, store.ReqError, err.Error())
			continue
		}
		_ = m.st.FinishTeamRequest(r.ID, store.ReqDone, res)
	}
}

var modelAliases = map[string]string{"haiku": "claude-haiku-5-5", "sonnet": "claude-sonnet-5-5", "opus": "claude-opus-5-5", "fable": "claude-fable-5-2"}

func (m *Manager) teamSpawn(r store.TeamRequest) (string, error) {
	caller, ok := m.Get(r.FromSession)
	if !ok {
		return "", errors.New("unknown caller")
	}
	if d := team.Depth(m.st, caller); d >= team.MaxDepth {
		return "", fmt.Errorf("the team tree is limited to %d levels below the orchestrator", team.MaxDepth)
	}
	str := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := r.Args[k].(string); ok && strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		}
		return ""
	}
	role := strings.ToLower(firstNonEmpty(str("role", "kind", "type"), "agent"))
	name := strings.ToLower(strings.Join(strings.Fields(str("name", "agent_name", "agentName", "id")), "-"))
	if name == "parent" || name == "orchestrator" {
		name = ""
	}
	taken := map[string]bool{}
	for _, x := range m.TeamMembers(caller.TeamID) {
		taken[strings.ToLower(x.AgentName)] = true
	}
	if name != "" && taken[name] {
		return "", fmt.Errorf("an agent named %s already exists; pick another name or message it", name)
	}
	if name == "" {
		base := strings.Join(strings.Fields(role), "-")
		for i := 1; ; i++ {
			name = fmt.Sprintf("%s-%d", base, i)
			if !taken[name] {
				break
			}
		}
	}
	task := str("task", "brief", "prompt", "instructions", "description", "objective")
	if task == "" {
		return "", errors.New(`the task is empty: pass the full brief in "task"`)
	}
	var paths []string
	rawPaths := r.Args["paths"]
	if rawPaths == nil {
		rawPaths = r.Args["owned_paths"]
	}
	if one, ok := rawPaths.(string); ok && strings.TrimSpace(one) != "" {
		rawPaths = []any{one}
	}
	if xs, ok := rawPaths.([]any); ok {
		for _, x := range xs {
			if s, ok := x.(string); ok && strings.TrimSpace(s) != "" {
				paths = append(paths, strings.TrimSpace(s))
			}
		}
	}
	approval := agent.Approval(caller.Approval)
	if ro, _ := r.Args["readonly"].(bool); ro {
		approval = agent.ApprovalReadOnly
	}
	model := caller.Model
	if v := str("model"); v != "" {
		model = v
		if full, ok := modelAliases[strings.ToLower(v)]; ok {
			model = full
		}
	}
	brief := task
	if len(paths) > 0 {
		brief += "\n\nPaths you own: " + strings.Join(paths, ", ") + "\nChange only these paths."
	}
	if approval == agent.ApprovalReadOnly {
		brief += "\n\nYou are read-only: do not change files; report your findings."
	}
	id, err := m.Spawn(SpawnOpts{
		Title: strings.ToUpper(role[:1]) + role[1:] + ": " + name, Task: brief, Agent: caller.Agent, Model: model, Approval: approval,
		Repo: caller.Repo, FromRef: caller.Branch, ProjectID: caller.ProjectID,
		Role: roleSubagent, ParentID: caller.ID, TeamID: caller.TeamID, AgentName: name,
	})
	if err != nil {
		return "", err
	}
	s, _ := m.Get(id)
	_ = team.AppendNote(m.st, caller.TeamID, team.DisplayName(caller), fmt.Sprintf("Spawned **%s** (%s) as %s: %s", name, id, role, oneLineText(task, 300)))
	return fmt.Sprintf("spawned %s (%s) on branch %s off %s with %s; it is working now. Call wait_agents to collect its report.", name, id, s.Branch, caller.Branch, firstNonEmpty(model, "the default model")), nil
}

func oneLineText(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func (m *Manager) teamMission(r store.TeamRequest) (string, error) {
	caller, ok := m.Get(r.FromSession)
	if !ok {
		return "", errors.New("unknown caller")
	}
	goal, _ := r.Args["goal"].(string)
	title, _ := r.Args["title"].(string)
	ms, err := m.StartMission(MissionOpts{Goal: goal, Title: title, Repo: caller.Repo, ProjectID: caller.ProjectID, FromRef: caller.Branch})
	if err != nil {
		return "", err
	}
	_ = team.AppendNote(m.st, caller.TeamID, team.DisplayName(caller), fmt.Sprintf("Started mission **%s** (%s): %s", ms.Title, ms.ID, oneLineText(goal, 300)))
	return fmt.Sprintf("mission %s started on branch %s (off %s). The human approves its spec, plan and acceptance in Seraph (missions screen). Check it with mission_status; when it is accepted, merge_agent mission:%s.", ms.ID, ms.Branch, caller.Branch, ms.ID), nil
}

func (m *Manager) deliverMessages(id string) bool {
	msgs, err := m.st.UndeliveredMessages(id)
	if err != nil || len(msgs) == 0 {
		return false
	}
	var b strings.Builder
	var ids []string
	for _, x := range msgs {
		from := x.FromSession
		if s, ok := m.Get(x.FromSession); ok {
			from = team.DisplayName(s)
		}
		fmt.Fprintf(&b, "From %s:\n%s\n\n", from, strings.TrimSpace(x.Text))
		ids = append(ids, x.ID)
	}
	if err := m.Followup(id, fmt.Sprintf(deliverPrompt, strings.TrimSpace(b.String()))); err != nil {
		return false
	}
	_ = m.st.MarkDelivered(ids)
	return true
}

func (m *Manager) teamTurnEnded(s store.Session) {
	if m.stopping() {
		return
	}
	if s.Role == roleSubagent {
		if dir := team.Dir(m.st, s.TeamID); dir != "" {
			_ = os.MkdirAll(filepath.Join(dir, "results"), 0o755)
			_ = os.WriteFile(filepath.Join(dir, "results", s.AgentName+".md"), []byte(team.Report(m.st, s)+"\n"), 0o644)
		}
	}
	if s.Status == store.StatusDone && m.deliverMessages(s.ID) {
		return
	}
	if s.ParentID != "" {
		m.maybeWake(s.ParentID)
	}
	m.maybeWake(s.ID)
}

func (m *Manager) maybeWake(parentID string) {
	m.tmu.Lock()
	defer m.tmu.Unlock()
	p, ok := m.Get(parentID)
	if !ok || m.Active(parentID) || p.Status == store.StatusStopped {
		return
	}
	var finished []store.Session
	for _, x := range m.TeamMembers(p.TeamID) {
		if x.ParentID != parentID {
			continue
		}
		if x.Status == store.StatusWorking || x.Status == store.StatusQueued || m.Active(x.ID) {
			return
		}
		if !m.st.Reported(x.ID) {
			finished = append(finished, x)
		}
	}
	if len(finished) == 0 {
		return
	}
	var b strings.Builder
	for _, x := range finished {
		fmt.Fprintf(&b, "## %s (%s) · %s\n%s\n\n", team.DisplayName(x), x.ID, x.Status, clipText(team.ResultText(m.st, x.ID), 2500))
	}
	if err := m.Followup(parentID, fmt.Sprintf(wakePrompt, strings.TrimSpace(b.String()))); err != nil {
		return
	}
	for _, x := range finished {
		_ = m.st.MarkReported(x.ID, true)
	}
}

func clipText(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "\n[… read_result for the full report]"
	}
	return s
}

func (m *Manager) TeamStats(teamID string) (total, running int) {
	for _, x := range m.TeamMembers(teamID) {
		if x.ID == teamID {
			continue
		}
		total++
		if x.Status == store.StatusWorking || x.Status == store.StatusQueued {
			running++
		}
	}
	return
}
