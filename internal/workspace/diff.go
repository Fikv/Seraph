package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

type Line struct {
	Kind byte
	Text string
	Old  int
	New  int
}

type Hunk struct {
	Header   string
	OldStart int
	NewStart int
	Lines    []Line
}

type FileDiff struct {
	Path    string
	OldPath string
	Status  string
	Binary  bool
	Hunks   []Hunk
	Add     int
	Del     int
}

type Commit struct {
	SHA     string
	Subject string
}

type Diff struct {
	Files     []FileDiff
	Commits   []Commit
	Untracked []string
	From      string
	Live      bool
}

func BranchDiff(repo, dir, branch, base string) (Diff, error) {
	var d Diff
	src, head := repo, branch
	if dir != "" {
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			src, head, d.Live = dir, "HEAD", true
		}
	}
	if src == "" || head == "" {
		return d, fmt.Errorf("nothing to diff: no worktree and no branch")
	}
	from := ""
	if base != "" {
		if mb, err := git(src, "merge-base", base, head); err == nil {
			from = mb
		}
	}
	if from == "" {
		if first, err := git(src, "rev-list", "--max-parents=0", head); err == nil {
			from = strings.Split(first, "\n")[0]
		}
	}
	d.From = from
	args := []string{"diff", "--no-color", "--no-ext-diff", "-M", from}
	if !d.Live {
		args = append(args, head)
	}
	out, err := gitRaw(src, args...)
	if err != nil {
		return d, err
	}
	d.Files = ParseDiff(out)
	if log, err := git(src, "log", "--format=%h%x09%s", from+".."+head); err == nil && log != "" {
		for _, l := range strings.Split(log, "\n") {
			sha, subject, _ := strings.Cut(l, "\t")
			d.Commits = append(d.Commits, Commit{SHA: sha, Subject: subject})
		}
	}
	if d.Live {
		if u, err := git(src, "ls-files", "--others", "--exclude-standard"); err == nil && u != "" {
			d.Untracked = strings.Split(u, "\n")
		}
	}
	return d, nil
}

func ParseDiff(text string) []FileDiff {
	var files []FileDiff
	var cur *FileDiff
	var hunk *Hunk
	oldNo, newNo := 0, 0
	flushHunk := func() {
		if cur != nil && hunk != nil {
			cur.Hunks = append(cur.Hunks, *hunk)
		}
		hunk = nil
	}
	flushFile := func() {
		flushHunk()
		if cur != nil {
			files = append(files, *cur)
		}
		cur = nil
	}
	for _, line := range strings.Split(text, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			flushFile()
			cur = &FileDiff{Status: "modified"}
			parts := strings.SplitN(strings.TrimPrefix(line, "diff --git "), " b/", 2)
			cur.OldPath = strings.TrimPrefix(parts[0], "a/")
			if len(parts) == 2 {
				cur.Path = parts[1]
			} else {
				cur.Path = cur.OldPath
			}
		case cur == nil:
			continue
		case hunk == nil && strings.HasPrefix(line, "new file mode"):
			cur.Status = "added"
		case hunk == nil && strings.HasPrefix(line, "deleted file mode"):
			cur.Status = "deleted"
		case hunk == nil && strings.HasPrefix(line, "rename from "):
			cur.Status, cur.OldPath = "renamed", strings.TrimPrefix(line, "rename from ")
		case hunk == nil && strings.HasPrefix(line, "rename to "):
			cur.Path = strings.TrimPrefix(line, "rename to ")
		case hunk == nil && strings.HasPrefix(line, "Binary files "):
			cur.Binary = true
		case hunk == nil && strings.HasPrefix(line, "--- "):
		case hunk == nil && strings.HasPrefix(line, "+++ "):
		case strings.HasPrefix(line, "@@"):
			flushHunk()
			hunk = &Hunk{Header: line}
			oldNo, newNo = parseRange(line)
			hunk.OldStart, hunk.NewStart = oldNo, newNo
		case hunk != nil && len(line) > 0:
			l := Line{Kind: line[0], Text: line[1:]}
			switch line[0] {
			case '+':
				l.New = newNo
				newNo++
				cur.Add++
			case '-':
				l.Old = oldNo
				oldNo++
				cur.Del++
			case ' ':
				l.Old, l.New = oldNo, newNo
				oldNo++
				newNo++
			case '\\':
			default:
				continue
			}
			hunk.Lines = append(hunk.Lines, l)
		}
	}
	flushFile()
	return files
}

func parseRange(header string) (int, int) {
	var oldStart, newStart int
	for _, f := range strings.Fields(header) {
		switch {
		case strings.HasPrefix(f, "-") && oldStart == 0:
			oldStart = atoiPrefix(f[1:])
		case strings.HasPrefix(f, "+") && newStart == 0:
			newStart = atoiPrefix(f[1:])
		}
	}
	return oldStart, newStart
}

func atoiPrefix(s string) int {
	if i := strings.IndexByte(s, ','); i >= 0 {
		s = s[:i]
	}
	n, _ := strconv.Atoi(s)
	return n
}

func (h Hunk) Patch() string {
	var b strings.Builder
	b.WriteString(h.Header + "\n")
	for _, l := range h.Lines {
		b.WriteByte(l.Kind)
		b.WriteString(l.Text + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func gitRaw(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}
