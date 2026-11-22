package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"seraph/internal/store"
)

func TestExternalsAndImport(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SERAPH_HOME", t.TempDir())
	os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{
		"mcpServers": {
			"penpot": {"type": "http", "url": "http://localhost:9001/mcp/stream?userToken=SECRET123&x=1"},
			"shared": {"type": "stdio", "command": "npx", "args": ["-y", "shared"], "env": {"API_KEY": "k"}},
			"mine": {"type": "stdio", "command": "uvx", "args": ["mine"]}
		},
		"projects": {"/home/u/projekty/diko": {"mcpServers": {"medusa": {"type": "sse", "url": "https://x/sse"}}}}
	}`), 0o600)
	os.MkdirAll(filepath.Join(home, ".cursor"), 0o755)
	os.WriteFile(filepath.Join(home, ".cursor", "mcp.json"), []byte(`{"mcpServers": {
		"apex": {"url": "https://apex.dev/mcp", "headers": {"Authorization": "Bearer z"}},
		"shared": {"command": "npx", "args": ["-y", "shared"]}
	}}`), 0o600)
	st, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	c, _ := Load(st)
	c.Put(Server{Name: "mine", Transport: TransportStdio, Command: "uvx", Claude: true})

	ext := Externals(c)
	byName := map[string]External{}
	for _, e := range ext {
		byName[e.Name] = e
	}
	if len(ext) != 4 || byName["mine"].Name != "" {
		t.Fatalf("externals = %+v", ext)
	}
	if p := byName["penpot"]; p.Server.Transport != TransportHTTP || !p.Has("claude") || p.Has("cursor") || !p.ReachesWorkers() {
		t.Fatalf("penpot = %+v", p)
	}
	if s := byName["shared"]; !s.Has("claude") || !s.Has("cursor") {
		t.Fatalf("a server in both tools is one entry: %+v", s)
	}
	if m := byName["medusa"]; m.Scope != "local" || m.ReachesWorkers() || !strings.Contains(m.Where(), "local to /home/u/projekty/diko") || m.Server.Transport != TransportSSE {
		t.Fatalf("medusa = %+v", m)
	}
	if ext[len(ext)-1].Name != "medusa" {
		t.Fatal("local-scope servers come last")
	}
	if cl := byName["penpot"].Server.CommandLine(); strings.Contains(cl, "SECRET123") || !strings.Contains(cl, "userToken=") || !strings.Contains(cl, "x=1") {
		t.Fatalf("tokens in URLs must be masked: %s", cl)
	}

	s := Import(c, byName["penpot"])
	if !s.Claude || s.Cursor || !strings.Contains(s.Source, "Claude Code (user scope)") {
		t.Fatalf("imported = %+v", s)
	}
	if lm := Import(c, byName["medusa"]); lm.Claude {
		t.Fatal("a local-scope server does not reach workers until synced to user scope")
	}
	if a := Import(c, byName["apex"]); !a.Cursor || len(a.Secrets) != 1 {
		t.Fatalf("apex = %+v", a)
	}
	if n := len(Externals(c)); n != 1 {
		t.Fatalf("imported servers stop being external, left %d", n)
	}
}

func TestMaskURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://x/mcp":                        "https://x/mcp",
		"https://x/mcp?api_key=abc&page=2":     "https://x/mcp?api_key=••••&page=2",
		"https://user:pw@x/mcp":                "https://••••@x/mcp",
		"http://localhost:9001/s?userToken=ey": "http://localhost:9001/s?userToken=••••",
	} {
		if got := MaskURL(in); got != want {
			t.Errorf("MaskURL(%q) = %q, want %q", in, got, want)
		}
	}
}
