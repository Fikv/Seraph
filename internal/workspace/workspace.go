package workspace

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	s := strings.TrimSpace(string(out))
	if err != nil {
		return s, fmt.Errorf("git %s: %s", strings.Join(args, " "), s)
	}
	return s, nil
}

func RepoRoot(dir string) (string, error) {
	return git(dir, "rev-parse", "--show-toplevel")
}

func CurrentBranch(repo string) string {
	b, err := git(repo, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return ""
	}
	return b
}

func Create(home, repo, id string) (dir, branch string, err error) {
	sum := sha1.Sum([]byte(repo))
	base := filepath.Join(home, "worktrees", filepath.Base(repo)+"-"+hex.EncodeToString(sum[:])[:8])
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", "", err
	}
	dir = filepath.Join(base, id)
	branch = "seraph/" + id
	if _, err := git(repo, "worktree", "add", "-b", branch, dir, "HEAD"); err != nil {
		return "", "", err
	}
	return dir, branch, nil
}

func Remove(repo, dir string) error {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		_, _ = git(repo, "worktree", "prune")
		return nil
	}
	_, err := git(repo, "worktree", "remove", "--force", dir)
	return err
}

func CommitAll(dir, msg string) (string, error) {
	if _, err := git(dir, "add", "-A"); err != nil {
		return "", err
	}
	if _, err := git(dir, "diff", "--cached", "--quiet"); err == nil {
		return "", nil
	}
	if _, err := git(dir, "commit", "-q", "-m", msg); err != nil {
		return "", err
	}
	return git(dir, "rev-parse", "--short", "HEAD")
}

func Summary(dir, base string) string {
	var parts []string
	if st, err := git(dir, "status", "--short"); err == nil && st != "" {
		n := len(strings.Split(st, "\n"))
		parts = append(parts, fmt.Sprintf("%d uncommitted", n))
	}
	if base != "" {
		if c, err := git(dir, "rev-list", "--count", base+"..HEAD"); err == nil && c != "0" {
			parts = append(parts, c+" commits ahead of "+base)
		}
	}
	if len(parts) == 0 {
		return "clean"
	}
	return strings.Join(parts, " · ")
}
