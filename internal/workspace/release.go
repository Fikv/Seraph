package workspace

import (
	"errors"
	"fmt"
	"strings"
)

func Dirty(dir string) (string, error) {
	return git(dir, "status", "--porcelain")
}

func DirtyTracked(dir string) (string, error) {
	return git(dir, "status", "--porcelain", "--untracked-files=no")
}

func Head(repo, ref string) (string, error) {
	return git(repo, "rev-parse", ref)
}

func Stash(dir, msg string) error {
	_, err := git(dir, "stash", "push", "--include-untracked", "-m", msg)
	return err
}

func Ahead(repo, base, branch string) int {
	out, err := git(repo, "rev-list", "--count", base+".."+branch)
	if err != nil {
		return 0
	}
	n := 0
	fmt.Sscanf(out, "%d", &n)
	return n
}

func MergeCheck(repo, base string) error {
	cur := CurrentBranch(repo)
	if cur != base {
		return fmt.Errorf("your checkout %s is on %q, not %q: switch to %s first", repo, cur, base, base)
	}
	if st, err := DirtyTracked(repo); err != nil {
		return err
	} else if st != "" {
		n := len(strings.Split(st, "\n"))
		return fmt.Errorf("your checkout %s has %d uncommitted change(s): commit or stash them first", repo, n)
	}
	return nil
}

func MergeInto(repo, base, branch, msg string) (string, error) {
	if err := MergeCheck(repo, base); err != nil {
		return "", err
	}
	if err := Merge(repo, branch, msg); err != nil {
		return "", err
	}
	return git(repo, "rev-parse", "--short", "HEAD")
}

func Remote(repo string) string {
	out, err := git(repo, "remote")
	if err != nil || out == "" {
		return ""
	}
	for _, r := range strings.Split(out, "\n") {
		if r == "origin" {
			return r
		}
	}
	return strings.Split(out, "\n")[0]
}

func Push(repo, branch string) (string, error) {
	remote := Remote(repo)
	if remote == "" {
		return "", errors.New("the repository has no remote to push to")
	}
	out, err := git(repo, "push", remote, branch)
	if err != nil {
		return out, err
	}
	return out, nil
}
