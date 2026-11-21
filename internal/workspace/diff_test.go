package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseDiff(t *testing.T) {
	raw := `diff --git a/a.go b/a.go
index 1..2 100644
--- a/a.go
+++ b/a.go
@@ -1,3 +1,4 @@ package a
 package a
-func old() {}
+func new() {}
+func extra() {}
 // end
@@ -10,2 +11,2 @@
-x
+y
\ No newline at end of file
diff --git a/img.png b/img.png
new file mode 100644
Binary files /dev/null and b/img.png differ
diff --git a/old name.txt b/new name.txt
similarity index 90%
rename from old name.txt
rename to new name.txt
`
	files := ParseDiff(raw)
	if len(files) != 3 {
		t.Fatalf("files = %d", len(files))
	}
	a := files[0]
	if a.Path != "a.go" || a.Add != 3 || a.Del != 2 || len(a.Hunks) != 2 {
		t.Fatalf("a.go = %+v", a)
	}
	h := a.Hunks[0]
	if h.OldStart != 1 || h.NewStart != 1 || h.Lines[2].Kind != '+' || h.Lines[2].New != 2 || h.Lines[1].Old != 2 || h.Lines[4].Old != 3 || h.Lines[4].New != 4 {
		t.Fatalf("hunk numbering = %+v", h.Lines)
	}
	if !strings.HasPrefix(h.Patch(), "@@ -1,3 +1,4 @@") || !strings.Contains(h.Patch(), "+func extra() {}") {
		t.Fatalf("patch = %s", h.Patch())
	}
	if files[1].Status != "added" || !files[1].Binary {
		t.Fatalf("binary = %+v", files[1])
	}
	if files[2].Status != "renamed" || files[2].OldPath != "old name.txt" || files[2].Path != "new name.txt" {
		t.Fatalf("rename = %+v", files[2])
	}
}

func TestBranchDiffLiveAndReleased(t *testing.T) {
	for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		t.Setenv(k, "t")
	}
	for _, k := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(k, "t@t")
	}
	repo := t.TempDir()
	run(t, repo, "git", "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(repo, "keep.txt"), []byte("one\ntwo\n"), 0o644)
	os.WriteFile(filepath.Join(repo, "gone.txt"), []byte("bye\n"), 0o644)
	run(t, repo, "git", "add", ".")
	run(t, repo, "git", "commit", "-qm", "init")

	dir, branch, err := Create(t.TempDir(), repo, "w1")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("one\nTWO\n"), 0o644)
	os.Remove(filepath.Join(dir, "gone.txt"))
	os.WriteFile(filepath.Join(dir, "new.txt"), []byte("hi\n"), 0o644)
	if _, err := CommitAll(dir, "worker change"); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("one\nTWO\nthree\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "scratch.txt"), []byte("x\n"), 0o644)

	live, err := BranchDiff(repo, dir, branch, "main")
	if err != nil {
		t.Fatal(err)
	}
	status := map[string]string{}
	for _, f := range live.Files {
		status[f.Path] = f.Status
	}
	if !live.Live || status["keep.txt"] != "modified" || status["gone.txt"] != "deleted" || status["new.txt"] != "added" {
		t.Fatalf("live diff = %+v", status)
	}
	if len(live.Commits) != 1 || live.Commits[0].Subject != "worker change" || len(live.Untracked) != 1 || live.Untracked[0] != "scratch.txt" {
		t.Fatalf("commits=%+v untracked=%v", live.Commits, live.Untracked)
	}
	var keep FileDiff
	for _, f := range live.Files {
		if f.Path == "keep.txt" {
			keep = f
		}
	}
	if keep.Add != 2 || keep.Del != 1 {
		t.Fatalf("uncommitted lines must be included: %+v", keep)
	}

	if err := Remove(repo, dir); err != nil {
		t.Fatal(err)
	}
	released, err := BranchDiff(repo, dir, branch, "main")
	if err != nil {
		t.Fatal(err)
	}
	if released.Live || len(released.Files) != 3 || len(released.Commits) != 1 {
		t.Fatalf("released diff = live:%v files:%d commits:%d", released.Live, len(released.Files), len(released.Commits))
	}
}
