package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func entry(t *testing.T, raw string) Entry {
	t.Helper()
	var e Entry
	if err := json.Unmarshal([]byte(raw), &e); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestOptionsAndBuild(t *testing.T) {
	e := entry(t, `{
		"name":"io.github.acme/brave-search","version":"1.2.3","description":"search",
		"packages":[
			{"registryType":"npm","identifier":"@acme/brave","version":"1.2.3","transport":{"type":"stdio"},
			 "environmentVariables":[{"name":"BRAVE_API_KEY","isRequired":true,"isSecret":true},{"name":"OPTIONAL","default":"x"}],
			 "packageArguments":[{"type":"positional","valueHint":"root","isRequired":true},{"type":"named","name":"--mode","default":"fast"}]},
			{"registryType":"pypi","identifier":"acme-brave","version":"1.2.3","transport":{"type":"stdio"}},
			{"registryType":"oci","identifier":"ghcr.io/acme/brave","version":"1.2.3","transport":{"type":"stdio"},
			 "environmentVariables":[{"name":"BRAVE_API_KEY","isRequired":true}]},
			{"registryType":"nuget","identifier":"Acme.Brave","version":"1.2.3","transport":{"type":"stdio"}},
			{"registryType":"npm","identifier":"@acme/brave-http","version":"1.2.3","transport":{"type":"streamable-http"}}
		],
		"remotes":[{"type":"streamable-http","url":"https://{tenant}.acme.dev/mcp","headers":[{"name":"Authorization","isRequired":true,"isSecret":true}]}]
	}`)
	opts := Options(e)
	if len(opts) != 4 {
		t.Fatalf("want 4 options (npm, pypi, oci, remote), got %d: %+v", len(opts), opts)
	}

	npm, err := opts[0].Build("brave", map[string]string{"BRAVE_API_KEY": "k", "root": "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"-y", "@acme/brave@1.2.3", "--mode", "fast", "/tmp"}; npm.Command != "npx" || !reflect.DeepEqual(npm.Args, want) {
		t.Fatalf("npm = %s %v", npm.Command, npm.Args)
	}
	if npm.Env["BRAVE_API_KEY"] != "k" || npm.Env["OPTIONAL"] != "x" || !reflect.DeepEqual(npm.Secrets, []string{"BRAVE_API_KEY"}) {
		t.Fatalf("npm env = %v secrets %v", npm.Env, npm.Secrets)
	}
	if _, err := opts[0].Build("brave", map[string]string{"root": "/tmp"}); err == nil {
		t.Fatal("missing required env must fail")
	}

	py, _ := opts[1].Build("brave", nil)
	if py.Command != "uvx" || py.Args[0] != "acme-brave==1.2.3" {
		t.Fatalf("pypi = %s %v", py.Command, py.Args)
	}

	oci, _ := opts[2].Build("brave", map[string]string{"BRAVE_API_KEY": "k"})
	if want := []string{"run", "-i", "--rm", "-e", "BRAVE_API_KEY", "ghcr.io/acme/brave:1.2.3"}; !reflect.DeepEqual(oci.Args, want) {
		t.Fatalf("oci args = %v", oci.Args)
	}

	remote, err := opts[3].Build("brave", map[string]string{"tenant": "me", "Authorization": "Bearer t"})
	if err != nil {
		t.Fatal(err)
	}
	if remote.Transport != TransportHTTP || remote.URL != "https://me.acme.dev/mcp" || remote.Headers["Authorization"] != "Bearer t" {
		t.Fatalf("remote = %+v", remote)
	}
	if got := remote.ClaudeJSON()["type"]; got != "http" {
		t.Fatalf("claude type = %v", got)
	}
}

func TestShortName(t *testing.T) {
	cases := map[string]string{
		"io.github.acme/mcp-server-fetch": "fetch",
		"com.pulsemcp/Remote Filesystem":  "remote-filesystem",
		"ac.inference.sh/mcp":             "mcp",
	}
	for in, want := range cases {
		if got := ShortName(in); got != want {
			t.Errorf("ShortName(%q) = %q, want %q", in, got, want)
		}
	}
	c := &Config{Servers: []Server{{Name: "fetch"}, {Name: "fetch-2"}}}
	if got := c.UniqueName("fetch"); got != "fetch-3" {
		t.Errorf("UniqueName = %q", got)
	}
}

func TestCursorConfigKeepsForeignEntries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	p := filepath.Join(home, ".cursor", "mcp.json")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte(`{"mcpServers":{"mine":{"command":"x"}},"other":1}`), 0o644)

	s := Server{Name: "seraph-one", Transport: TransportStdio, Command: "npx", Args: []string{"-y", "pkg"}}
	if err := cursorSet(s.Name, s.CursorJSON()); err != nil {
		t.Fatal(err)
	}
	d, _ := readCursor()
	servers := d["mcpServers"].(map[string]any)
	if _, ok := servers["mine"]; !ok || servers["seraph-one"] == nil || d["other"] == nil {
		t.Fatalf("config = %v", d)
	}
	if err := cursorSet(s.Name, nil); err != nil {
		t.Fatal(err)
	}
	d, _ = readCursor()
	if _, ok := d["mcpServers"].(map[string]any)["seraph-one"]; ok {
		t.Fatal("entry not removed")
	}
	if ext := Externals(&Config{}); len(ext) != 1 || ext[0].Name != "mine" {
		t.Fatalf("externals = %v", ext)
	}
}

const fakeServer = `
import json, sys
for line in sys.stdin:
    msg = json.loads(line)
    if "id" not in msg:
        continue
    if msg["method"] == "initialize":
        res = {"protocolVersion": "2025-06-18", "capabilities": {"tools": {}}, "serverInfo": {"name": "fake", "version": "1"}}
    elif msg["method"] == "tools/list":
        res = {"tools": [{"name": "zeta"}, {"name": "alpha"}]}
    else:
        res = {}
    print(json.dumps({"jsonrpc": "2.0", "id": msg["id"], "result": res}), flush=True)
`

func TestProbeStdio(t *testing.T) {
	script := filepath.Join(t.TempDir(), "server.py")
	os.WriteFile(script, []byte(fakeServer), 0o644)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s := Server{Name: "fake", Transport: TransportStdio, Command: "python3", Args: []string{"-I", script}}
	Check(ctx, &s)
	if s.Status != StatusOK || !reflect.DeepEqual(s.Tools, []string{"alpha", "zeta"}) {
		t.Fatalf("status=%s err=%s tools=%v", s.Status, s.Error, s.Tools)
	}

	bad := Server{Name: "bad", Transport: TransportStdio, Command: "python3", Args: []string{"-I", "-c", "import sys; sys.stderr.write('boom'); sys.exit(1)"}}
	Check(ctx, &bad)
	if bad.Status != StatusError || !strings.Contains(bad.Error, "boom") {
		t.Fatalf("bad status=%s err=%s", bad.Status, bad.Error)
	}
}
