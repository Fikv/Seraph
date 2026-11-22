package orch

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"seraph/internal/agent"
	"seraph/internal/store"
)

func TestReleaseFlow(t *testing.T) {
	agent.Register("team", newTeam(`[]`))
	agent.Register("fake", fakeDriver{sleep: "0.05"})
	m, _, _, repo := missionEnv(t)
	bare := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", "--bare", bare).CombinedOutput(); err != nil {
		t.Fatal(string(out))
	}
	gitOut(t, repo, "remote", "add", "origin", bare)
	gitOut(t, repo, "push", "-q", "origin", "main")
	os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("v1"), 0o644)
	gitOut(t, repo, "add", ".")
	gitOut(t, repo, "commit", "-qm", "tracked")

	t.Setenv("SERAPH_TEST_NOTE", "first")
	id, err := m.Spawn(SpawnOpts{Task: "write README and push it to GitHub", Agent: "fake", Approval: agent.ApprovalSafe, Repo: repo, ProjectID: "p1"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "worker", func() bool { s, _ := m.Get(id); return s.Status == store.StatusDone && !m.Active(id) })

	if _, err := m.Merge(store.TargetSession, id); err == nil {
		t.Fatal("merge without validation must fail")
	}
	t.Setenv("SERAPH_TEST_VERDICT", "FAIL")
	r, err := m.Validate(store.TargetSession, id)
	if err != nil {
		t.Fatal(err)
	}
	vs, _ := m.Get(r.SessionID)
	if vs.Role != roleValidatorSession || vs.Approval != string(agent.ApprovalCheck) || vs.Dir != func() string { s, _ := m.Get(id); return s.Dir }() {
		t.Fatalf("validator session = %+v", vs)
	}
	waitFor(t, "fail", func() bool { r, _ = m.Release(store.TargetSession, id); return r.Status == store.ReleaseFail })
	evs, _ := m.Log(r.SessionID)
	if !strings.Contains(evs[0].Text, "write README and push it to GitHub") || !strings.Contains(evs[0].Text, "work.txt") {
		t.Fatal("the validator must see the request and the diff")
	}
	t.Setenv("SERAPH_TEST_NOTE", "second")
	if err := m.SendFindings(store.TargetSession, id); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "worker fix", func() bool { s, _ := m.Get(id); return !m.Active(id) && s.Turns == 2 && s.Status == store.StatusDone })
	evs, _ = m.Log(id)
	sawReport := false
	for _, ev := range evs {
		sawReport = sawReport || (ev.Kind == agent.KindUser && strings.Contains(ev.Text, "validator checked your work"))
	}
	if !sawReport {
		t.Fatal("findings must reach the worker")
	}

	t.Setenv("SERAPH_TEST_VERDICT", "PASS")
	t.Setenv("SERAPH_TEST_VALIDATOR_WRITES", "1")
	m.Validate(store.TargetSession, id)
	waitFor(t, "pass", func() bool { r, _ = m.Release(store.TargetSession, id); return r.Status == store.ReleasePass })
	if !strings.Contains(r.Note, "stashed") {
		t.Fatalf("validator edits must be stashed and reported: %q", r.Note)
	}
	s, _ := m.Get(id)
	if _, err := os.Stat(filepath.Join(s.Dir, "validator-was-here.txt")); !os.IsNotExist(err) {
		t.Fatal("validator edits must not stay in the worktree")
	}
	t.Setenv("SERAPH_TEST_VALIDATOR_WRITES", "")

	t.Setenv("SERAPH_TEST_NOTE", "third")
	m.Followup(id, "one more change")
	waitFor(t, "worker 3", func() bool { s, _ := m.Get(id); return !m.Active(id) && s.Turns == 3 })
	if _, err := m.Merge(store.TargetSession, id); err == nil || !strings.Contains(err.Error(), "changed after validation") {
		t.Fatalf("a branch that moved after PASS must be validated again: %v", err)
	}
	m.Validate(store.TargetSession, id)
	waitFor(t, "pass 2", func() bool { r, _ = m.Release(store.TargetSession, id); return r.Status == store.ReleasePass })

	os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("local edit"), 0o644)
	if _, err := m.Merge(store.TargetSession, id); err == nil || !strings.Contains(err.Error(), "uncommitted") {
		t.Fatalf("a dirty checkout must block the merge: %v", err)
	}
	gitOut(t, repo, "checkout", "--", "tracked.txt")
	if _, err := m.Push(store.TargetSession, id); err == nil {
		t.Fatal("push before merge must fail")
	}
	r, err = m.Merge(store.TargetSession, id)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(readFile(filepath.Join(repo, "work.txt"), 0)); got != "third" {
		t.Fatalf("the user's checkout must have the work: %q", got)
	}
	r, err = m.Push(store.TargetSession, id)
	if err != nil {
		t.Fatal(err, r.PushOut)
	}
	if gitOut(t, bare, "rev-parse", "main") != gitOut(t, repo, "rev-parse", "main") {
		t.Fatal("origin/main must match the merged main")
	}
	if _, err := m.Push(store.TargetSession, id); err == nil {
		t.Fatal("double push must be refused")
	}
	m.StopAll()
}

func TestRunTargetFollowsProject(t *testing.T) {
	m, st, _, repo := missionEnv(t)
	_ = m
	other := gitRepo(t)
	if r, p := RunTarget(st, other); r != other || p != "" {
		t.Fatalf("no current project: %s %s", r, p)
	}
	st.SetSetting(store.CurrentProjectSetting, "p1")
	if r, p := RunTarget(st, other); r != repo || p != "p1" {
		t.Fatalf("current project must win over the cwd repo: %s %s", r, p)
	}
	if r, p := RunTarget(st, repo); r != repo || p != "p1" {
		t.Fatalf("cwd inside the project: %s %s", r, p)
	}
}
