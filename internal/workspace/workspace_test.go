package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestWorktreeLifecycle(t *testing.T) {
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@t")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@t")
	repo := t.TempDir()
	run(t, repo, "git", "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a"), 0o644)
	run(t, repo, "git", "add", ".")
	run(t, repo, "git", "commit", "-qm", "init")

	dir, branch, err := Create(t.TempDir(), repo, "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if branch != "seraph/abc123" {
		t.Fatalf("branch = %q", branch)
	}
	if sha, err := CommitAll(dir, "nothing"); err != nil || sha != "" {
		t.Fatalf("empty commit: sha=%q err=%v", sha, err)
	}
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0o644)
	if got := Summary(dir, "main"); got != "1 uncommitted" {
		t.Fatalf("summary before commit = %q", got)
	}
	sha, err := CommitAll(dir, "add b\n\nbody")
	if err != nil || sha == "" {
		t.Fatalf("commit: sha=%q err=%v", sha, err)
	}
	if msg := run(t, dir, "git", "log", "-1", "--format=%B"); msg != "add b\n\nbody" {
		t.Fatalf("message = %q", msg)
	}
	if got := Summary(dir, "main"); got != "1 commits ahead of main" {
		t.Fatalf("summary after commit = %q", got)
	}
	if err := Remove(repo, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("worktree dir still exists")
	}
	if out := run(t, repo, "git", "branch", "--list", branch); out == "" {
		t.Fatal("branch was deleted")
	}
}
