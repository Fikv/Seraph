package obsidian

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecentAndURI(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.MkdirAll(filepath.Join(dir, ".obsidian"), 0o755)
	files := []string{"a.md", "sub/b.md", ".obsidian/workspace.md", "image.png"}
	for i, f := range files {
		p := filepath.Join(dir, f)
		os.WriteFile(p, []byte("x"), 0o644)
		ts := time.Now().Add(time.Duration(i) * time.Minute)
		os.Chtimes(p, ts, ts)
	}
	notes := Recent(dir, 10)
	if len(notes) != 2 || notes[0].Rel != filepath.Join("sub", "b") || notes[1].Rel != "a" {
		t.Fatalf("notes = %+v", notes)
	}
	if got := URI(`C:\Users\me\My Vault\Note.md`); got != "obsidian://open?path=C%3A%5CUsers%5Cme%5CMy%20Vault%5CNote.md" {
		t.Fatalf("uri = %s", got)
	}
}

func TestListSkipsGeneratedAndVendor(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"a.md", "docs/b.md", "gen/c.md", "node_modules/x/d.md"} {
		os.MkdirAll(filepath.Join(dir, filepath.Dir(f)), 0o755)
		os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644)
	}
	os.WriteFile(filepath.Join(dir, "gen", ".graphify_obsidian_manifest.json"), []byte("{}"), 0o644)
	notes := List(dir, 100)
	if len(notes) != 2 || notes[0].Rel != "a" || notes[1].Rel != filepath.Join("docs", "b") {
		t.Fatalf("notes = %+v", notes)
	}
}
