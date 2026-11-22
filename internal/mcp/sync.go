package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

func cursorConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cursor", "mcp.json")
}

func claudeConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude.json")
}

func Apply(ctx context.Context, s Server) error {
	var errs []error
	if s.Claude {
		errs = append(errs, claudeAdd(ctx, s))
	} else {
		errs = append(errs, claudeRemove(ctx, s.Name))
	}
	if s.Cursor {
		errs = append(errs, cursorSet(s.Name, s.CursorJSON()))
	} else {
		errs = append(errs, cursorSet(s.Name, nil))
	}
	return errors.Join(errs...)
}

func Remove(ctx context.Context, name string) error {
	return errors.Join(claudeRemove(ctx, name), cursorSet(name, nil))
}

func claudeAdd(ctx context.Context, s Server) error {
	_ = claudeRemove(ctx, s.Name)
	spec, err := json.Marshal(s.ClaudeJSON())
	if err != nil {
		return err
	}
	out, err := exec.CommandContext(ctx, "claude", "mcp", "add-json", "-s", "user", s.Name, string(spec)).CombinedOutput()
	if err != nil {
		return fmt.Errorf("claude mcp add-json: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func claudeRemove(ctx context.Context, name string) error {
	if !claudeHas(name) {
		return nil
	}
	out, err := exec.CommandContext(ctx, "claude", "mcp", "remove", "-s", "user", name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("claude mcp remove: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func claudeServers() map[string]json.RawMessage {
	data, err := os.ReadFile(claudeConfigPath())
	if err != nil {
		return nil
	}
	var d struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	_ = json.Unmarshal(data, &d)
	return d.MCPServers
}

func claudeHas(name string) bool {
	_, ok := claudeServers()[name]
	return ok
}

func readCursor() (map[string]any, error) {
	data, err := os.ReadFile(cursorConfigPath())
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{"mcpServers": map[string]any{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var d map[string]any
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("%s: %w", cursorConfigPath(), err)
	}
	if _, ok := d["mcpServers"].(map[string]any); !ok {
		d["mcpServers"] = map[string]any{}
	}
	return d, nil
}

func cursorSet(name string, spec map[string]any) error {
	d, err := readCursor()
	if err != nil {
		return err
	}
	servers := d["mcpServers"].(map[string]any)
	if spec == nil {
		if _, ok := servers[name]; !ok {
			return nil
		}
		delete(servers, name)
	} else {
		servers[name] = spec
	}
	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	p := cursorConfigPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".seraph.tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

type External struct {
	Name    string
	Targets []string
	Scope   string
	Dir     string
	Server  Server
}

func (e External) Has(target string) bool {
	for _, t := range e.Targets {
		if t == target {
			return true
		}
	}
	return false
}

func (e External) Where() string {
	var parts []string
	for _, t := range e.Targets {
		switch {
		case t == "claude" && e.Scope == "local":
			parts = append(parts, "Claude Code (local to "+e.Dir+")")
		case t == "claude":
			parts = append(parts, "Claude Code (user scope)")
		case t == "cursor":
			parts = append(parts, "Cursor (~/.cursor/mcp.json)")
		}
	}
	return strings.Join(parts, " + ")
}

func (e External) ReachesWorkers() bool {
	return e.Scope != "local"
}

func ParseSpec(name string, raw json.RawMessage) (Server, bool) {
	var d struct {
		Type    string            `json:"type"`
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env"`
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
	}
	if json.Unmarshal(raw, &d) != nil {
		return Server{}, false
	}
	s := Server{Name: name, Command: d.Command, Args: d.Args, Env: d.Env, URL: d.URL, Headers: d.Headers}
	switch {
	case d.Type == TransportSSE:
		s.Transport = TransportSSE
	case d.URL != "" || d.Type == "http" || d.Type == "streamable-http":
		s.Transport = TransportHTTP
	case d.Command != "":
		s.Transport = TransportStdio
	default:
		return Server{}, false
	}
	for k := range d.Env {
		s.Secrets = append(s.Secrets, k)
	}
	sort.Strings(s.Secrets)
	return s, true
}

func claudeLocalServers() map[string]map[string]json.RawMessage {
	data, err := os.ReadFile(claudeConfigPath())
	if err != nil {
		return nil
	}
	var d struct {
		Projects map[string]struct {
			MCPServers map[string]json.RawMessage `json:"mcpServers"`
		} `json:"projects"`
	}
	_ = json.Unmarshal(data, &d)
	out := map[string]map[string]json.RawMessage{}
	for dir, p := range d.Projects {
		if len(p.MCPServers) > 0 {
			out[dir] = p.MCPServers
		}
	}
	return out
}

func Externals(c *Config) []External {
	byName := map[string]*External{}
	var order []string
	add := func(name, target string, raw json.RawMessage) {
		if _, ours := c.Get(name); ours {
			return
		}
		srv, ok := ParseSpec(name, raw)
		if !ok {
			return
		}
		if e, seen := byName[name]; seen {
			e.Targets = append(e.Targets, target)
			return
		}
		byName[name] = &External{Name: name, Targets: []string{target}, Scope: "user", Server: srv}
		order = append(order, name)
	}
	for name, raw := range claudeServers() {
		add(name, "claude", raw)
	}
	if d, err := readCursor(); err == nil {
		for name, v := range d["mcpServers"].(map[string]any) {
			raw, _ := json.Marshal(v)
			add(name, "cursor", raw)
		}
	}
	var out []External
	for _, n := range order {
		out = append(out, *byName[n])
	}
	for dir, servers := range claudeLocalServers() {
		for name, raw := range servers {
			if _, ours := c.Get(name); ours {
				continue
			}
			if srv, ok := ParseSpec(name, raw); ok {
				out = append(out, External{Name: name, Targets: []string{"claude"}, Scope: "local", Dir: dir, Server: srv})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].Scope == "local") != (out[j].Scope == "local") {
			return out[j].Scope == "local"
		}
		return out[i].Name+out[i].Dir < out[j].Name+out[j].Dir
	})
	return out
}

func Import(c *Config, e External) Server {
	s := e.Server
	s.Name = c.UniqueName(e.Name)
	s.Claude = e.Has("claude") && e.Scope != "local"
	s.Cursor = e.Has("cursor")
	s.Source = "imported from " + e.Where()
	for k := range s.Headers {
		s.Secrets = append(s.Secrets, k)
	}
	c.Put(s)
	return s
}

var secretParam = regexp.MustCompile(`(?i)(token|key|secret|auth|pass|sig|session|credential)`)

func MaskURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	changed := false
	if u.User != nil {
		u.User = url.User("MASKED")
		changed = true
	}
	if u.RawQuery != "" {
		q := u.Query()
		for k := range q {
			if secretParam.MatchString(k) {
				q.Set(k, "MASKED")
				changed = true
			}
		}
		u.RawQuery = q.Encode()
	}
	if !changed {
		return raw
	}
	return strings.ReplaceAll(u.String(), "MASKED", "••••")
}

func Check(ctx context.Context, s *Server) {
	tools, err := Probe(ctx, *s)
	s.CheckedAt = timeNow()
	switch {
	case errors.Is(err, ErrAuth):
		s.Status, s.Error, s.Tools = StatusAuth, "needs login: run `claude mcp login "+s.Name+"` or `cursor-agent mcp login "+s.Name+"`", nil
	case err != nil:
		s.Status, s.Error, s.Tools = StatusError, err.Error(), nil
	default:
		s.Status, s.Error, s.Tools = StatusOK, "", tools
	}
}

var timeNow = time.Now
