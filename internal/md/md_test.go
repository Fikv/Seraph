package md

import (
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestPrepare(t *testing.T) {
	src := "---\ntags: [seraph, modul]\npath: x\n---\n# Title\n\nSee [[pkg agent|agent]] and [[01-Architektura#Warstwy]] and [[Seraph]].\n\n![[logo.png]]\n\n> [!warning] Careful\n> body\n\n==important==\n"
	d := Prepare(src)
	if !reflect.DeepEqual(d.Tags, []string{"seraph", "modul"}) {
		t.Fatalf("tags = %v", d.Tags)
	}
	if !reflect.DeepEqual(d.Links, []string{"logo.png", "pkg agent", "01-Architektura", "Seraph"}) {
		t.Fatalf("links = %v", d.Links)
	}
	for _, want := range []string{"**agent**", "**01-Architektura › Warstwy**", "**Seraph**", "⧉ *logo.png*", "> **WARNING · Careful**", "**important**"} {
		if !strings.Contains(d.Body, want) {
			t.Errorf("body missing %q:\n%s", want, d.Body)
		}
	}
	if strings.Contains(d.Body, "tags:") {
		t.Error("frontmatter not stripped")
	}
}

func TestRenderFitsWidth(t *testing.T) {
	src := "# Heading\n\nA paragraph " + strings.Repeat("with many words ", 30) + "\n\n- item one\n- [ ] task\n\n```go\nfunc main() {}\n```\n\n| a | b |\n|---|---|\n| 1 | 2 |\n"
	for _, w := range []int{30, 60, 100} {
		out := Render(src, w)
		if !strings.Contains(ansi.Strip(out), "HEADING") || !strings.Contains(ansi.Strip(out), "func main()") {
			t.Fatalf("render @%d lost content:\n%s", w, ansi.Strip(out))
		}
		for i, l := range strings.Split(out, "\n") {
			if ansi.StringWidth(l) > w {
				t.Fatalf("render @%d line %d is %d wide: %q", w, i, ansi.StringWidth(l), ansi.Strip(l))
			}
		}
	}
	if Render(src, 60) != Render(src, 60) {
		t.Fatal("cache must be stable")
	}
}
