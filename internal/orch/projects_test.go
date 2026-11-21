package orch

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
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
sleep ` + f.sleep + `
echo "$SERAPH_TEST_NOTE" > work.txt
echo '{"type":"assistant","message":{"content":[{"type":"text","text":"dirs ` + strings.ReplaceAll(string(dirs), `"`, `\"`) + `"}]}}'
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

func TestDispatchAndSchedule(t *testing.T) {
	t.Setenv("SERAPH_HOME", t.TempDir())
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@t")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@t")
	t.Setenv("SERAPH_TEST_NOTE", "done")
	agent.Register("fake", fakeDriver{sleep: "0.3"})
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

	notes := t.TempDir()
	repo := gitRepo(t)
	p := store.Project{ID: "p1", Name: "Demo", NotesDir: notes, DefaultAgent: "fake", MaxParallel: 1, Repos: []string{repo}}
	if err := st.SaveProject(p); err != nil {
		t.Fatal(err)
	}
	for i, title := range []string{"first", "second", "third"} {
		st.SaveTodo(store.Todo{ID: "t" + string(rune('1'+i)), ProjectID: "p1", Title: title, Status: store.TodoQueued, Priority: 3 - i})
	}
	d := Defaults{Agent: "fake", Approval: agent.ApprovalSafe}

	m.SetDefaults(d)
	m.ScheduleOnce()
	t1, _, _ := st.Todo("t1")
	t2, _, _ := st.Todo("t2")
	if t1.Status != store.TodoDoing || t1.SessionID == "" || t2.Status != store.TodoQueued {
		t.Fatalf("max_parallel=1 must start only the top todo: t1=%s t2=%s", t1.Status, t2.Status)
	}
	sess, _ := m.Get(t1.SessionID)
	if sess.ProjectID != "p1" || sess.TodoID != "t1" || !sess.Worktree {
		t.Fatalf("session = %+v", sess)
	}

	waitFor(t, "t1 in review", func() bool { x, _, _ := st.Todo("t1"); return x.Status == store.TodoReview })
	evs, _ := m.Log(t1.SessionID)
	var sawDirs, sawCommit bool
	for _, ev := range evs {
		sawDirs = sawDirs || strings.Contains(ev.Text, notes)
		sawCommit = sawCommit || strings.HasPrefix(ev.Text, "committed ")
	}
	if !sawDirs || !sawCommit {
		t.Fatalf("notes dir passed=%v auto-commit=%v: %+v", sawDirs, sawCommit, evs)
	}

	m.ScheduleOnce()
	waitFor(t, "t2 dispatched", func() bool { x, _, _ := st.Todo("t2"); return x.Status != store.TodoQueued })

	if err := m.SetTodoStatus("t1", store.TodoDone); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Dispatch("t1", d); err == nil {
		t.Fatal("a done todo must not be dispatched again")
	}

	waitFor(t, "t2 in review", func() bool { x, _, _ := st.Todo("t2"); return x.Status == store.TodoReview })
	t2, _, _ = st.Todo("t2")
	if err := m.Remove(t2.SessionID); err != nil {
		t.Fatal(err)
	}
	t2, _, _ = st.Todo("t2")
	if t2.Status != store.TodoOpen || t2.SessionID != "" {
		t.Fatalf("released worker must reopen its todo: %+v", t2)
	}

	if _, err := os.Stat(filepath.Join(notes)); err != nil {
		t.Fatal(err)
	}
	m.Wait()
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
