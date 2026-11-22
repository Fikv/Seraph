package orch

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"seraph/internal/agent"
	"seraph/internal/store"
)

type fakeDriver struct{ sleep string }

func (fakeDriver) Name() string   { return "fake" }
func (fakeDriver) Binary() string { return "sh" }
func (f fakeDriver) Args(s agent.Spec) []string {
	dirs, _ := json.Marshal(s.AddDirs)
	script := `echo '{"type":"system","subtype":"init","session_id":"fake-1"}'
echo '{"type":"rate_limit_event","rate_limit_info":{"unifiedWindows":{"five_hour":{"utilization":0.37,"resetsAt":1795297200},"seven_day":{"utilization":0.05,"resetsAt":1795597200}}}}'
sleep ` + f.sleep + `
echo "$SERAPH_TEST_NOTE" > work.txt
echo '{"type":"assistant","message":{"content":[{"type":"text","text":"dirs ` + strings.ReplaceAll(string(dirs), `"`, `\"`) + ` approval ` + string(s.Approval) + ` explain ` + fmt.Sprint(strings.Contains(s.SystemPrompt, "answering the user")) + `"}]}}'
echo '{"type":"result","is_error":false,"result":"ok","total_cost_usd":0.01}'`
	return []string{"-c", script}
}
func (fakeDriver) Parse(line []byte) []agent.Event { return agent.Claude{}.Parse(line) }

func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"commit", "-q", "--allow-empty", "-m", "init"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	return dir
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestWorkerSpawnInProject(t *testing.T) {
	t.Setenv("SERAPH_HOME", t.TempDir())
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@t")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@t")
	t.Setenv("SERAPH_TEST_NOTE", "done")
	agent.Register("fake", fakeDriver{sleep: "0.1"})
	st, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	m, err := New(st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	notes, repo := t.TempDir(), gitRepo(t)
	st.SaveProject(store.Project{ID: "p1", Name: "Demo", NotesDir: notes, Repos: []string{repo}})
	id, err := m.Spawn(SpawnOpts{Task: "quick fix", Agent: "fake", Approval: agent.ApprovalSafe, Repo: repo, ProjectID: "p1"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "worker done", func() bool { s, _ := m.Get(id); return s.Status == store.StatusDone })
	m.Wait()
	lim, ok := m.Limits("fake")
	if !ok || len(lim.Windows) != 2 || lim.Windows[0].Window != "five_hour" || lim.Windows[0].Utilization != 0.37 {
		t.Fatalf("limits = %+v ok=%v", lim, ok)
	}
	evs, _ := m.Log(id)
	var sawDirs, sawCommit bool
	for _, ev := range evs {
		if ev.Kind == agent.KindLimits {
			t.Fatal("limit events must not show up in the transcript")
		}
		sawDirs = sawDirs || strings.Contains(ev.Text, notes)
		sawCommit = sawCommit || strings.HasPrefix(ev.Text, "committed ")
	}
	if !sawDirs || !sawCommit {
		t.Fatalf("notes dir passed=%v auto-commit=%v: %+v", sawDirs, sawCommit, evs)
	}
}

func TestTodosStartMissionsWithinLimit(t *testing.T) {
	agent.Register("team", newTeam(`[{"id":"t1","title":"only","task":"x","files":["a/**"]}]`))
	m, st, _, _ := missionEnv(t)
	for i, title := range []string{"first", "second"} {
		st.SaveTodo(store.Todo{ID: "t" + string(rune('1'+i)), ProjectID: "p1", Title: title, Status: store.TodoQueued, Priority: 3 - i})
	}
	m.ScheduleOnce()
	ms1, ok, _ := st.MissionForTodo("t1")
	if t2, _, _ := st.Todo("t2"); !ok || t2.Status != store.TodoQueued {
		t.Fatalf("max_parallel=1 must start only the top todo's mission: ok=%v t2=%s", ok, t2.Status)
	}
	waitMission(t, m, ms1.ID, "spec gate", waiting(store.PhaseSpec))
	m.ScheduleOnce()
	if t2, _, _ := st.Todo("t2"); t2.Status != store.TodoQueued {
		t.Fatal("a mission waiting at a gate still counts against max_parallel")
	}
	if err := m.CancelMission(ms1.ID); err != nil {
		t.Fatal(err)
	}
	if t1, _, _ := st.Todo("t1"); t1.Status != store.TodoOpen {
		t.Fatalf("cancelled mission must reopen its todo, got %s", t1.Status)
	}
	m.ScheduleOnce()
	if _, ok, _ := st.MissionForTodo("t2"); !ok {
		t.Fatal("the next todo must start once a slot frees up")
	}
	if err := m.SetTodoStatus("t1", store.TodoDone); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Dispatch("t1", Defaults{Agent: "team"}); err == nil {
		t.Fatal("a done todo must not be dispatched again")
	}
	m.StopAll()
}

func TestProjectRepoPicksCwd(t *testing.T) {
	p := store.Project{Repos: []string{"/a/one", "/a/two"}}
	if got := ProjectRepo(p, "/a/two/sub"); got != "/a/two" {
		t.Fatalf("got %s", got)
	}
	if got := ProjectRepo(p, "/elsewhere"); got != "/a/one" {
		t.Fatalf("got %s", got)
	}
}

func TestAskIsReadOnly(t *testing.T) {
	t.Setenv("SERAPH_HOME", t.TempDir())
	for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		t.Setenv(k, "t")
	}
	for _, k := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(k, "t@t")
	}
	t.Setenv("SERAPH_TEST_NOTE", "first")
	agent.Register("fake", fakeDriver{sleep: "0.1"})
	st, _ := store.Open()
	defer st.Close()
	m, _ := New(st, nil)
	defer m.Close()
	repo := gitRepo(t)
	st.SaveProject(store.Project{ID: "p", Name: "P", Repos: []string{repo}, MaxParallel: 1})
	id, err := m.Spawn(SpawnOpts{Task: "do it", Agent: "fake", Approval: agent.ApprovalSafe, Repo: repo, ProjectID: "p"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "worker done", func() bool { x, _ := m.Get(id); return x.Status == store.StatusDone && !m.Running(id) })
	s, _ := m.Get(id)
	head := func() string {
		out, _ := exec.Command("git", "-C", s.Dir, "rev-parse", "HEAD").Output()
		return strings.TrimSpace(string(out))
	}
	before := head()

	t.Setenv("SERAPH_TEST_NOTE", "changed-during-ask")
	if err := m.Ask(id, "why this?", "work.txt", "@@ -0,0 +1 @@\n+first"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "ask to finish", func() bool { return !m.Running(id) })
	m.Wait()
	if head() != before {
		t.Fatal("ask must not commit")
	}
	if s2, _ := m.Get(id); s2.Status != store.StatusDone {
		t.Fatalf("status should be restored to done, got %s", s2.Status)
	}
	evs, _ := m.Log(id)
	var sawQuestion, sawMode bool
	for _, ev := range evs {
		sawQuestion = sawQuestion || (ev.Kind == agent.KindUser && strings.Contains(ev.Text, "why this?") && strings.Contains(ev.Text, "```diff"))
		sawMode = sawMode || strings.Contains(ev.Text, "approval ask explain true")
	}
	if !sawQuestion || !sawMode {
		t.Fatalf("question=%v askMode=%v", sawQuestion, sawMode)
	}
	if err := m.Ask("nope", "", "", ""); err == nil {
		t.Fatal("unknown session must fail")
	}
}
