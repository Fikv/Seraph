package orch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"seraph/internal/agent"
	"seraph/internal/store"
	"seraph/internal/team"
)

type crewDriver struct{}

func (crewDriver) Name() string   { return "crew" }
func (crewDriver) Binary() string { return "sh" }

var crewName = regexp.MustCompile(`You are (\S+),`)
var crewPaths = regexp.MustCompile(`Paths you own: (\S+?)/`)

func (crewDriver) Args(s agent.Spec) []string {
	reply, shell := "orchestrator turn done", ""
	if strings.Contains(s.SystemPrompt, "ORCHESTRATOR") {
		if d := os.Getenv("SERAPH_CREW_ORCH_SLEEP"); d != "" {
			shell = "sleep " + d + "\n"
		}
		if f := os.Getenv("SERAPH_CREW_ORCH_WRITE"); f != "" {
			shell += "echo lead >> " + f + "\n"
		}
	}
	if mm := crewName.FindStringSubmatch(s.SystemPrompt); mm != nil {
		name := mm[1]
		dir := "misc"
		if p := crewPaths.FindStringSubmatch(s.Prompt); p != nil {
			dir = p[1]
		}
		if !strings.Contains(s.Prompt, "read-only") {
			shell = fmt.Sprintf("mkdir -p %s && echo %s >> %s/%s.txt\n", dir, name, dir, name)
		}
		reply = "## Done\n" + name + " finished its part"
	}
	sys, _ := json.Marshal(map[string]any{"type": "system", "subtype": "init", "session_id": "crew"})
	res, _ := json.Marshal(map[string]any{"type": "result", "is_error": false, "result": reply})
	q := func(b []byte) string { return "'" + strings.ReplaceAll(string(b), "'", `'\''`) + "'" }
	return []string{"-c", "printf '%s\\n' " + q(sys) + "\n" + shell + "sleep 0.2\nprintf '%s\\n' " + q(res)}
}
func (crewDriver) Parse(line []byte) []agent.Event { return agent.Claude{}.Parse(line) }

func TestOrchestratorTeam(t *testing.T) {
	agent.Register("crew", crewDriver{})
	agent.Register("team", newTeam(`[{"id":"t1","title":"x","task":"x","files":["m/**"]}]`))
	m, st, notes, repo := missionEnv(t)
	m.StartTeamLoop()
	t.Setenv("SERAPH_CREW_ORCH_SLEEP", "4")

	oid, err := m.StartTeam(TeamOpts{Goal: "Build the api and the ui", Repo: repo, ProjectID: "p1", Agent: "crew", Approval: agent.ApprovalSafe})
	if err != nil {
		t.Fatal(err)
	}
	idle := func(id string) func() bool {
		return func() bool {
			s, _ := m.Get(id)
			return !m.Active(id) && s.Status != store.StatusWorking && s.Status != store.StatusQueued
		}
	}
	orch, _ := m.Get(oid)
	if orch.TeamID != oid || orch.Role != roleOrchestrator || !strings.HasPrefix(team.Dir(st, oid), filepath.Join(notes, "Seraph Teams")) {
		t.Fatalf("orchestrator = %+v dir=%s", orch, team.Dir(st, oid))
	}
	call := func(as, tool string, args map[string]any) (string, error) {
		return team.Server{Store: st, SessionID: as}.Call(tool, args)
	}
	for _, spec := range []map[string]any{
		{"name": "api", "role": "developer", "task": "build the api", "paths": []any{"a/**"}},
		{"name": "ui", "role": "developer", "task": "build the ui", "paths": []any{"b/**"}, "model": "haiku"},
		{"role": "analyst", "brief": "map the code", "readonly": true},
	} {
		if out, err := call(oid, "spawn_agent", spec); err != nil || !strings.Contains(out, "spawned") {
			t.Fatalf("spawn %v: %v %s", spec["name"], err, out)
		}
	}
	if _, err := call(oid, "spawn_agent", map[string]any{"name": "api", "task": "again"}); err == nil {
		t.Fatal("names are unique in a team")
	}
	if _, err := call(oid, "merge_agent", map[string]any{}); err == nil || !strings.Contains(err.Error(), "say which agent") {
		t.Fatalf("an empty agent reference must not resolve to anyone: %v", err)
	}
	if _, err := call(oid, "write_note", map[string]any{"note": "aliases work"}); err != nil {
		t.Fatalf("argument aliases: %v", err)
	}
	out, err := call(oid, "wait_agents", map[string]any{"timeout_s": 20})
	if err != nil || !strings.Contains(out, "api finished its part") || !strings.Contains(out, "ui finished its part") || !strings.Contains(out, "analyst-1") {
		t.Fatalf("wait = %v %s", err, out)
	}
	members := map[string]store.Session{}
	for _, x := range m.TeamMembers(oid) {
		members[x.AgentName] = x
	}
	members["scout"] = members["analyst-1"]
	if members["ui"].Model != "claude-haiku-5-5" || members["scout"].Approval != string(agent.ApprovalReadOnly) || members["api"].ParentID != oid {
		t.Fatalf("members = %+v", members)
	}
	if members["api"].Base != orch.Branch {
		t.Fatalf("children branch from the orchestrator: base %s, want %s", members["api"].Base, orch.Branch)
	}
	tree, _ := call(members["api"].ID, "list_agents", nil)
	if !strings.Contains(tree, "orchestrator") || !strings.Contains(tree, "  - api") || !strings.Contains(tree, "← you") {
		t.Fatalf("tree = %s", tree)
	}
	for _, n := range []string{"api", "ui"} {
		if out, err := call(oid, "merge_agent", map[string]any{"agent": n}); err != nil || !strings.Contains(out, "merged") {
			t.Fatalf("merge %s: %v %s", n, err, out)
		}
	}
	for _, f := range []string{"a/api.txt", "b/ui.txt"} {
		if _, err := os.Stat(filepath.Join(orch.Dir, f)); err != nil {
			t.Fatalf("orchestrator branch lacks %s", f)
		}
	}
	if _, err := call(members["api"].ID, "merge_agent", map[string]any{"agent": "ui"}); err == nil {
		t.Fatal("only a parent merges its own children")
	}
	t.Setenv("SERAPH_CREW_ORCH_SLEEP", "")
	waitFor(t, "orchestrator first turn", idle(oid))

	if out, err := call(members["api"].ID, "spawn_agent", map[string]any{"name": "helper", "task": "help", "paths": []any{"a/x/**"}}); err != nil {
		t.Fatalf("depth 2 must be allowed: %v %s", err, out)
	}
	waitFor(t, "helper", func() bool {
		for _, x := range m.TeamMembers(oid) {
			if x.AgentName == "helper" {
				return idle(x.ID)()
			}
		}
		return false
	})
	var helper store.Session
	for _, x := range m.TeamMembers(oid) {
		if x.AgentName == "helper" {
			helper = x
		}
	}
	if helper.ParentID != members["api"].ID {
		t.Fatalf("helper parent = %s", helper.ParentID)
	}
	if _, err := call(helper.ID, "spawn_agent", map[string]any{"name": "deep", "task": "x"}); err == nil {
		t.Fatal("depth 3 must be refused")
	}
	waitFor(t, "api woken by its helper", func() bool {
		evs, _ := m.Log(members["api"].ID)
		for _, ev := range evs {
			if ev.Kind == agent.KindUser && strings.Contains(ev.Text, "your subagents finished") && strings.Contains(ev.Text, "helper finished its part") {
				return true
			}
		}
		return false
	})

	waitFor(t, "orchestrator idle", idle(oid))
	before, _ := m.Get(oid)
	if _, err := call(oid, "spawn_agent", map[string]any{"name": "late", "task": "late work", "paths": []any{"c/**"}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "orchestrator woken with the late report", func() bool {
		s, _ := m.Get(oid)
		if s.Turns <= before.Turns {
			return false
		}
		evs, _ := m.Log(oid)
		for _, ev := range evs {
			if ev.Kind == agent.KindUser && strings.Contains(ev.Text, "late finished its part") {
				return true
			}
		}
		return false
	})
	waitFor(t, "orchestrator idle again", idle(oid))
	if !st.Reported(members["api"].ID) {
		t.Fatal("reports read through wait_agents count as reported")
	}

	if out, err := call(members["ui"].ID, "message", map[string]any{"to": "orchestrator", "text": "the ui needs an /items endpoint"}); err != nil || !strings.Contains(out, "queued") {
		t.Fatalf("message: %v %s", err, out)
	}
	waitFor(t, "message delivered", func() bool {
		evs, _ := m.Log(oid)
		for _, ev := range evs {
			if ev.Kind == agent.KindUser && strings.Contains(ev.Text, "From ui:") && strings.Contains(ev.Text, "/items") {
				return true
			}
		}
		return false
	})
	waitFor(t, "orchestrator idle after message", idle(oid))

	call(oid, "write_note", map[string]any{"text": "API returns JSON only."})
	notesText, _ := call(members["scout"].ID, "read_notes", nil)
	for _, want := range []string{"Build the api and the ui", "Spawned **api**", "API returns JSON only.", "Merged api", "aliases work", "Spawned **analyst-1**"} {
		if !strings.Contains(notesText, want) {
			t.Errorf("notes lack %q:\n%s", want, notesText)
		}
	}
	if _, err := os.Stat(filepath.Join(team.Dir(st, oid), "results", "api.md")); err != nil {
		t.Error("every child's report is saved in the team folder")
	}

	m.SetDefaults(Defaults{Agent: "team", Approval: agent.ApprovalSafe})
	out, err = call(oid, "start_mission", map[string]any{"goal": "a full feature", "title": "Feature"})
	if err != nil || !strings.Contains(out, "mission") {
		t.Fatalf("start_mission: %v %s", err, out)
	}
	ms, _ := m.Missions()
	if len(ms) != 1 || ms[0].Base != orch.Branch {
		t.Fatalf("the mission branches from the team branch: %+v", ms)
	}
	if st, _ := call(oid, "mission_status", map[string]any{"id": ms[0].ID}); !strings.Contains(st, "phase") {
		t.Fatalf("mission_status = %s", st)
	}
	if _, err := call(members["api"].ID, "start_mission", map[string]any{"goal": "x"}); err == nil {
		t.Fatal("only the orchestrator starts missions")
	}
	m.StopAll()
}

func TestOrchestratorLandsInCheckout(t *testing.T) {
	agent.Register("crew", crewDriver{})
	m, _, _, repo := missionEnv(t)
	m.StartTeamLoop()
	head := func() string { return strings.TrimSpace(gitOut(t, repo, "rev-parse", "HEAD")) }
	before := head()
	t.Setenv("SERAPH_CREW_ORCH_WRITE", "lead.txt")
	t.Setenv("SERAPH_CREW_ORCH_SLEEP", "1")
	oid, err := m.StartTeam(TeamOpts{Goal: "write lead.txt", Repo: repo, ProjectID: "p1", Agent: "crew", Approval: agent.ApprovalSafe})
	if err != nil {
		t.Fatal(err)
	}
	o, _ := m.Get(oid)
	if o.Worktree || o.Dir != repo || o.Branch != "main" {
		t.Fatalf("the lead works in the user's checkout on main: %+v", o)
	}
	if _, err := m.StartTeam(TeamOpts{Goal: "second", Repo: repo, Agent: "crew", Approval: agent.ApprovalSafe}); err == nil || !strings.Contains(err.Error(), "already working in your checkout") {
		t.Fatalf("one agent per checkout: %v", err)
	}
	if _, err := (team.Server{Store: m.Store(), SessionID: oid}).Call("spawn_agent", map[string]any{"name": "kid", "task": "x", "paths": []any{"k/**"}}); err != nil {
		t.Fatal(err)
	}
	idle := func(id string) func() bool {
		return func() bool {
			s, _ := m.Get(id)
			return !m.Active(id) && s.Status != store.StatusWorking && s.Status != store.StatusQueued
		}
	}
	waitFor(t, "lead turn", idle(oid))
	t.Setenv("SERAPH_CREW_ORCH_SLEEP", "")
	if head() == before || gitOut(t, repo, "rev-parse", "--abbrev-ref", "HEAD") != "main\n" {
		t.Fatal("the lead's work must be committed on main in the user's checkout")
	}
	if gitOut(t, repo, "status", "--porcelain") != "" {
		t.Fatal("the checkout must be clean after the commit")
	}
	if subj := gitOut(t, repo, "log", "-5", "--format=%s"); strings.Contains(subj, "Orchestrator:") || !strings.Contains(subj, "write lead.txt") {
		t.Fatalf("commits on the user's branch carry the request, not the role: %q", subj)
	}
	var kid store.Session
	for _, x := range m.TeamMembers(oid) {
		if x.AgentName == "kid" {
			kid = x
		}
	}
	waitFor(t, "kid", idle(kid.ID))
	if kid.Base != "main" || !kid.Worktree {
		t.Fatalf("subagents keep their own worktree off main: %+v", kid)
	}
	waitFor(t, "lead woken by kid", idle(oid))
	if out, err := (team.Server{Store: m.Store(), SessionID: oid}).Call("merge_agent", map[string]any{"agent": "kid"}); err != nil {
		t.Fatalf("merge into checkout: %v %s", err, out)
	}
	if _, err := os.Stat(filepath.Join(repo, "k", "kid.txt")); err != nil {
		t.Fatal("the child's work must land on main in the checkout")
	}

	os.WriteFile(filepath.Join(repo, "mine.txt"), []byte("user work in progress"), 0o644)
	beforeDirty := head()
	if err := m.Followup(oid, "one more"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "lead turn 3", idle(oid))
	if head() != beforeDirty {
		t.Fatal("with the user's uncommitted changes present, Seraph must not commit")
	}
	if !strings.Contains(gitOut(t, repo, "status", "--porcelain"), "mine.txt") {
		t.Fatal("the user's file must stay untouched and uncommitted")
	}
	evs, _ := m.Log(oid)
	saw := false
	for _, ev := range evs {
		saw = saw || strings.Contains(ev.Text, "not committed: your checkout had uncommitted changes")
	}
	if !saw {
		t.Fatal("the transcript must say why nothing was committed")
	}
	m.StopAll()
}
