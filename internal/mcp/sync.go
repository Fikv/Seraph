package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	Name   string
	Target string
}

func Externals(c *Config) []External {
	var out []External
	for name := range claudeServers() {
		if _, ours := c.Get(name); !ours {
			out = append(out, External{Name: name, Target: "claude"})
		}
	}
	if d, err := readCursor(); err == nil {
		for name := range d["mcpServers"].(map[string]any) {
			if _, ours := c.Get(name); !ours {
				out = append(out, External{Name: name, Target: "cursor"})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Target+out[i].Name < out[j].Target+out[j].Name })
	return out
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
