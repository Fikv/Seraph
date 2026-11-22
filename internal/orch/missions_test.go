package orch

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"seraph/internal/agent"
	"seraph/internal/store"
)

type teamDriver struct {
	mu     *sync.Mutex
	counts map[string]int
	plan   string
}

func newTeam(plan string) teamDriver {
	return teamDriver{mu: &sync.Mutex{}, counts: map[string]int{}, plan: plan}
}

func (teamDriver) Name() string   { return "team" }
func (teamDriver) Binary() string { return "sh" }

func (d teamDriver) bump(key string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.counts[key]++
	return d.counts[key]
}

var ownedRe = regexp.MustCompile(`Paths you own: ([^\s,]+)`)
var taskRe = regexp.MustCompile(`Task: (.+)`)

func (d teamDriver) Args(s agent.Spec) []string {
	sys, p := s.SystemPrompt, s.Prompt
	var reply, shell string
	switch {
	case strings.Contains(sys, "Product Owner doing acceptance"):
		reply = "## Acceptance criteria\n| AC1 | yes | a/ and b/ |\nVERDICT: ACCEPT"
	case strings.Contains(sys, "Product Owner"):
		n := d.bump("po")
		reply = fmt.Sprintf("# Feature\n## Goal\ngoal v%d\n## Acceptance criteria\n1. AC1 works\n## Out of scope\n- none\n## Analysis questions\n- How is config loaded?\n", n)
	case strings.Contains(sys, "Analyst"):
		reply = "## Answer\nfrom config.go\n## Relevant code\n- config.go:1"
	case strings.Contains(sys, "Tech Lead"):
		if d.bump("lead") == 1 && strings.HasPrefix(d.plan, "BAD|") {
			reply = "## Tasks\nI forgot the list"
		} else {
			reply = "## Approach\nsplit\n## Tasks\n...\n```json\n" + strings.TrimPrefix(d.plan, "BAD|") + "\n```"
		}
	case strings.Contains(sys, "Code Reviewer"):
		title := taskRe.FindStringSubmatch(p)[1]
		if d.bump("review:"+title) == 1 {
			reply = "## Findings\n- [blocking] a.txt:1 missing newline\nVERDICT: CHANGES"
		} else {
			reply = "## Findings\nnone\nVERDICT: APPROVE"
		}
	case strings.Contains(sys, "Tester"):
		shell = "mkdir -p tests && echo test >> tests/ac1_test.txt\n"
		if d.bump("tester") == 1 {
			reply = "## Failures\nAC1 fails\nVERDICT: FAIL"
		} else {
			reply = "## Test run\nok\nVERDICT: PASS"
		}
	case strings.Contains(sys, "Developer") && strings.Contains(p, "tester reported failures"):
		shell = "echo fixed > fix.txt\n"
		reply = "## Done\nfixed"
	case strings.Contains(sys, "Developer") && strings.Contains(p, "reviewer with fresh eyes"):
		shell = "for f in */*.txt; do echo again >> \"$f\"; done\n"
		reply = "fixed review findings"
	case strings.Contains(sys, "Developer"):
		dir := strings.TrimSuffix(globPrefix(ownedRe.FindStringSubmatch(p)[1]), "/")
		title := taskRe.FindStringSubmatch(p)[1]
		shell = fmt.Sprintf("mkdir -p %s && echo %q > %s/%s.txt && echo %q > %s/shared.txt\n", dir, title, dir, slug(title), title, dir)
		reply = "## Done\n" + title
	default:
		reply = "unknown role"
	}
	init, _ := json.Marshal(map[string]any{"type": "system", "subtype": "init", "session_id": "team-" + s.Dir})
	res, _ := json.Marshal(map[string]any{"type": "result", "is_error": false, "result": reply})
	q := func(b []byte) string { return "'" + strings.ReplaceAll(string(b), "'", `'\''`) + "'" }
	return []string{"-c", "printf '%s\\n' " + q(init) + "\n" + shell + "printf '%s\\n' " + q(res)}
}

func (teamDriver) Parse(line []byte) []agent.Event { return agent.Claude{}.Parse(line) }

func missionEnv(t *testing.T) (*Manager, *store.Store, string, string) {
	t.Helper()
	t.Setenv("SERAPH_HOME", t.TempDir())
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@t")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@t")
	st, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	m, err := New(st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	m.SetDefaults(Defaults{Agent: "team", Approval: agent.ApprovalSafe})
	notes, repo := t.TempDir(), gitRepo(t)
	if err := st.SaveProject(store.Project{ID: "p1", Name: "Demo", NotesDir: notes, MaxParallel: 1, Repos: []string{repo}}); err != nil {
		t.Fatal(err)
	}
	return m, st, notes, repo
}

func waitMission(t *testing.T, m *Manager, id, what string, cond func(store.Mission, []store.Node) bool) (store.Mission, []store.Node) {
	t.Helper()
	var ms store.Mission
	var nodes []store.Node
	waitFor(t, what, func() bool {
		ms, nodes, _ = m.Mission(id)
		return cond(ms, nodes)
	})
	return ms, nodes
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return string(out)
}

func count(nodes []store.Node, r store.Role) int {
	n := 0
	for _, x := range nodes {
		if x.Role == r {
			n++
		}
	}
	return n
}

func waiting(gate store.MissionPhase) func(store.Mission, []store.Node) bool {
	return func(ms store.Mission, _ []store.Node) bool {
		return ms.State == store.MissionWaiting && ms.Gate == gate
	}
}

func TestMissionFullTeam(t *testing.T) {
	plan := `[{"id":"t1","title":"api","task":"build api","files":["a/**"]},
{"id":"t2","title":"ui","task":"build ui","files":["b/**"]},
{"id":"t3","title":"api docs","task":"document api","files":["a/docs/**"],"depends_on":["t1"]}]`
	agent.Register("team", newTeam(plan))
	m, st, notes, repo := missionEnv(t)
	st.SaveTodo(store.Todo{ID: "td1", ProjectID: "p1", Title: "Add feature", Body: "details", Status: store.TodoQueued})

	m.ScheduleOnce()
	td, _, _ := st.Todo("td1")
	ms, ok, _ := st.MissionForTodo("td1")
	if !ok || td.Status != store.TodoDoing {
		t.Fatalf("todo must start a mission: todo=%+v mission=%v", td, ok)
	}
	if !strings.HasPrefix(ms.Artifacts, filepath.Join(notes, "Seraph Missions")) {
		t.Fatalf("artifacts must live in the project notes: %s", ms.Artifacts)
	}

	ms, _ = waitMission(t, m, ms.ID, "spec gate", waiting(store.PhaseSpec))
	if !strings.Contains(readFile(filepath.Join(ms.Artifacts, "spec.md"), 0), "goal v1") {
		_, ns, _ := m.Mission(ms.ID)
		ents, _ := os.ReadDir(ms.Artifacts)
		for _, n := range ns {
			evs, _ := m.Log(n.SessionID)
			t.Logf("node %+v events %+v", n, evs)
		}
		t.Fatalf("spec.md missing: %v %v", ents, readFile(filepath.Join(ms.Artifacts, "spec.md"), 0))
	}
	if err := m.Revise(ms.ID, "add AC2"); err != nil {
		t.Fatal(err)
	}
	ms, _ = waitMission(t, m, ms.ID, "revised spec gate", waiting(store.PhaseSpec))
	if !strings.Contains(readFile(filepath.Join(ms.Artifacts, "spec.md"), 0), "goal v2") {
		t.Fatal("revision must rewrite spec.md")
	}
	if err := m.Approve(ms.ID); err != nil {
		t.Fatal(err)
	}

	ms, nodes := waitMission(t, m, ms.ID, "plan gate", waiting(store.PhasePlan))
	if count(nodes, store.RoleAnalyst) != 1 || count(nodes, store.RoleDev) != 3 {
		t.Fatalf("nodes = %+v", nodes)
	}
	var lead store.Node
	for _, n := range nodes {
		if n.Role == store.RoleLead {
			lead = n
		}
	}
	if lead.Note != "" {
		t.Fatalf("disjoint plan must have no warnings: %q", lead.Note)
	}
	if err := m.Approve(ms.ID); err != nil {
		t.Fatal(err)
	}

	ms, nodes = waitMission(t, m, ms.ID, "acceptance gate", waiting(store.PhaseAccept))
	if count(nodes, store.RoleReviewer) != 6 || count(nodes, store.RoleTester) != 2 {
		t.Fatalf("every task must be reviewed twice and tests run twice: reviewers=%d testers=%d", count(nodes, store.RoleReviewer), count(nodes, store.RoleTester))
	}
	td, _, _ = st.Todo("td1")
	if td.Status != store.TodoReview {
		t.Fatalf("todo must wait in review, got %s", td.Status)
	}
	for _, f := range []string{"a/api.txt", "b/ui.txt", "a/docs/api-docs.txt", "fix.txt", "tests/ac1_test.txt"} {
		if _, err := os.Stat(filepath.Join(ms.Dir, f)); err != nil {
			t.Errorf("mission branch lacks %s", f)
		}
	}
	if !strings.Contains(readFile(filepath.Join(ms.Dir, "a/api.txt"), 0), "again") {
		t.Error("review fixes must be merged")
	}
	log := gitOut(t, ms.Dir, "log", "--oneline", "--merges")
	if strings.Count(log, "Merge ") != 3 {
		t.Errorf("want 3 task merges:\n%s", log)
	}
	if gitOut(t, repo, "rev-parse", "--abbrev-ref", "HEAD") != "main\n" {
		t.Error("the user's checkout must not move")
	}
	for _, f := range []string{"goal.md", "spec.md", "plan.md", "tests-1.md", "tests-2.md", "acceptance-1.md", "review-api-r1.md", "review-api-r2.md"} {
		if _, err := os.Stat(filepath.Join(ms.Artifacts, f)); err != nil {
			t.Errorf("artifact %s missing", f)
		}
	}
	for _, n := range nodes {
		s, _ := m.Get(n.SessionID)
		if s.MissionID != ms.ID || s.NodeID != n.ID {
			t.Errorf("session of %s not linked", n.Title)
		}
		if readOnlyRole(n.Role) && s.Approval != string(agent.ApprovalReadOnly) {
			t.Errorf("%s must run read-only, got %s", n.Role, s.Approval)
		}
		if !readOnlyRole(n.Role) && s.Approval != string(agent.ApprovalTest) {
			t.Errorf("%s must run in test mode, got %s", n.Role, s.Approval)
		}
	}

	if err := m.Approve(ms.ID); err != nil {
		t.Fatal(err)
	}
	ms, _, _ = m.Mission(ms.ID)
	td, _, _ = st.Todo("td1")
	if ms.State != store.MissionDone || td.Status != store.TodoDone {
		t.Fatalf("mission=%s todo=%s", ms.State, td.Status)
	}
}

func TestMissionConflictAndRetry(t *testing.T) {
	plan := `[{"id":"t1","title":"one","task":"x","files":["a/**"]},{"id":"t2","title":"two","task":"y","files":["a/**"]}]`
	agent.Register("team", newTeam(plan))
	m, _, _, repo := missionEnv(t)
	os.WriteFile(filepath.Join(repo, "seed.txt"), []byte("x"), 0o644)
	gitOut(t, repo, "add", ".")
	gitOut(t, repo, "commit", "-qm", "seed")

	ms, err := m.StartMission(MissionOpts{Goal: "conflicting work", Repo: repo})
	if err != nil {
		t.Fatal(err)
	}
	waitMission(t, m, ms.ID, "spec", waiting(store.PhaseSpec))
	m.Approve(ms.ID)
	_, nodes := waitMission(t, m, ms.ID, "plan", waiting(store.PhasePlan))
	for _, n := range nodes {
		if n.Role == store.RoleLead && !strings.Contains(n.Note, "both own") {
			t.Fatalf("overlapping plan must warn, note=%q", n.Note)
		}
	}
	m.Approve(ms.ID)

	ms, nodes = waitMission(t, m, ms.ID, "build to settle", func(ms store.Mission, _ []store.Node) bool {
		return ms.State == store.MissionBlocked || ms.Phase != store.PhaseBuild
	})
	if ms.State != store.MissionBlocked || !strings.Contains(ms.Error, "conflict in a/shared.txt") {
		t.Fatalf("parallel writers of one file must conflict and block: %s %q", ms.State, ms.Error)
	}
	if err := m.RetryNode(ms.ID, ""); err != nil {
		t.Fatal(err)
	}
	ms, _ = waitMission(t, m, ms.ID, "acceptance after retry", waiting(store.PhaseAccept))
	if err := m.Revise(ms.ID, "rename the files"); err != nil {
		t.Fatal(err)
	}
	ms, nodes = waitMission(t, m, ms.ID, "acceptance again", waiting(store.PhaseAccept))
	if count(nodes, store.RoleAcceptance) != 2 {
		t.Fatalf("acceptance changes must go through tests and acceptance again: %d", count(nodes, store.RoleAcceptance))
	}
	if err := m.RemoveMission(ms.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ms.Dir); !os.IsNotExist(err) {
		t.Fatal("mission worktree must be removed")
	}
}

func TestPlanHelpers(t *testing.T) {
	qs := analysisQuestions("# T\n## Analysis questions\n- How?\n1. Where?\n- none\n## Next\n- not a question")
	if len(qs) != 2 || qs[1] != "Where?" {
		t.Fatalf("questions = %q", qs)
	}
	if analysisQuestions("## Analysis questions\n- none\n") != nil {
		t.Fatal("none means no analysts")
	}
	if parseVerdict("blah\nVERDICT: changes\n**VERDICT:** APPROVE") != "APPROVE" {
		t.Fatal("last verdict wins")
	}
	haiku := "## Tasks\n```json\n[{\"id\": \"t1\", \"title\": \"rev\", \"task\": \"print \\\"h\\u00e9llo\\U0001F600\\\" and a\\xffb\ttab\", \"files\": [\"main.go\"], \"depends_on\": []}]\n```"
	if ts, err := parseLeadTasks(haiku); err != nil || len(ts) != 1 || !strings.Contains(ts[0].Task, `\U0001F600`) || !strings.Contains(ts[0].Task, "héllo") {
		t.Fatalf("invalid escapes must be repaired: %v %+v", err, ts)
	}
	if _, err := parseLeadTasks("no json here"); err == nil {
		t.Fatal("missing task list must fail")
	}
	devs := []store.Node{{ID: "a", Title: "A", Files: []string{"src/api/**"}}, {ID: "b", Title: "B", Files: []string{"src/**"}}, {ID: "c", Title: "C", Files: []string{"web/x.ts"}, Deps: nil}}
	if w := PlanWarnings(devs); len(w) != 1 || !strings.Contains(w[0], `"A" and "B"`) {
		t.Fatalf("warnings = %q", w)
	}
	devs[1].Deps = []string{"a"}
	if w := PlanWarnings(devs); len(w) != 0 {
		t.Fatalf("dependent tasks may share paths: %q", w)
	}
}

func TestLeadGetsOneFormatRetry(t *testing.T) {
	agent.Register("team", newTeam(`BAD|[{"id":"t1","title":"only","task":"x","files":["a/**"]}]`))
	m, _, _, repo := missionEnv(t)
	ms, err := m.StartMission(MissionOpts{Goal: "g", Repo: repo})
	if err != nil {
		t.Fatal(err)
	}
	waitMission(t, m, ms.ID, "spec", waiting(store.PhaseSpec))
	m.Approve(ms.ID)
	_, nodes := waitMission(t, m, ms.ID, "plan after format retry", waiting(store.PhasePlan))
	if count(nodes, store.RoleDev) != 1 {
		t.Fatalf("dev tasks = %d", count(nodes, store.RoleDev))
	}
	evs, _ := m.Log(nodes[len(nodes)-2].SessionID)
	saw := false
	for _, ev := range evs {
		saw = saw || (ev.Kind == agent.KindUser && strings.Contains(ev.Text, "could not read your task list"))
	}
	if !saw {
		t.Fatal("the lead must be asked to fix the format before the mission blocks")
	}
	m.StopAll()
}
