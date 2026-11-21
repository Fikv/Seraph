package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"seraph/internal/store"
)

const (
	TransportStdio = "stdio"
	TransportHTTP  = "http"
	TransportSSE   = "sse"
)

type Server struct {
	Name        string            `json:"name"`
	Transport   string            `json:"transport"`
	Command     string            `json:"command,omitempty"`
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	URL         string            `json:"url,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	Registry    string            `json:"registry,omitempty"`
	Version     string            `json:"version,omitempty"`
	Source      string            `json:"source,omitempty"`
	Description string            `json:"description,omitempty"`
	Secrets     []string          `json:"secrets,omitempty"`
	Claude      bool              `json:"claude"`
	Cursor      bool              `json:"cursor"`
	Tools       []string          `json:"tools,omitempty"`
	Status      string            `json:"status,omitempty"`
	Error       string            `json:"error,omitempty"`
	CheckedAt   time.Time         `json:"checked_at,omitempty"`
}

const (
	StatusOK    = "ok"
	StatusError = "error"
	StatusAuth  = "auth"
)

type Config struct {
	Servers []Server `json:"servers"`
	st      *store.Store
}

func legacyPath() string { return filepath.Join(store.Home(), "mcp.json") }

func Load(st *store.Store) (*Config, error) {
	c := &Config{st: st}
	rows, err := st.DB().Query(`SELECT data FROM mcp_servers ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var s Server
		if json.Unmarshal([]byte(data), &s) == nil && s.Name != "" {
			c.Servers = append(c.Servers, s)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(c.Servers) == 0 {
		if err := c.importLegacy(); err != nil {
			return nil, err
		}
	}
	return c, nil
}

func (c *Config) importLegacy() error {
	data, err := os.ReadFile(legacyPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var legacy struct {
		Servers []Server `json:"servers"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return fmt.Errorf("%s: %w", legacyPath(), err)
	}
	c.Servers = legacy.Servers
	if err := c.Save(); err != nil {
		return err
	}
	return os.Rename(legacyPath(), legacyPath()+".imported")
}

func (c *Config) Save() error {
	if c.st == nil {
		return errors.New("mcp config has no store")
	}
	sort.Slice(c.Servers, func(i, j int) bool { return c.Servers[i].Name < c.Servers[j].Name })
	tx, err := c.st.DB().Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM mcp_servers`); err != nil {
		return err
	}
	for _, s := range c.Servers {
		data, err := json.Marshal(s)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO mcp_servers (name, data) VALUES (?, ?)`, s.Name, string(data)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (c *Config) Get(name string) (*Server, bool) {
	for i := range c.Servers {
		if c.Servers[i].Name == name {
			return &c.Servers[i], true
		}
	}
	return nil, false
}

func (c *Config) Put(s Server) {
	if cur, ok := c.Get(s.Name); ok {
		*cur = s
		return
	}
	c.Servers = append(c.Servers, s)
}

func (c *Config) Delete(name string) {
	out := c.Servers[:0]
	for _, s := range c.Servers {
		if s.Name != name {
			out = append(out, s)
		}
	}
	c.Servers = out
}

func (c *Config) AnyCursor() bool {
	for _, s := range c.Servers {
		if s.Cursor {
			return true
		}
	}
	return false
}

var nameSanitizer = regexp.MustCompile(`[^a-z0-9_-]+`)

func ShortName(registryName string) string {
	n := registryName
	if i := strings.LastIndex(n, "/"); i >= 0 {
		n = n[i+1:]
	}
	n = strings.TrimSuffix(strings.TrimPrefix(strings.ToLower(n), "mcp-server-"), "-mcp-server")
	n = strings.Trim(nameSanitizer.ReplaceAllString(n, "-"), "-")
	if n == "" {
		n = "server"
	}
	return n
}

func (c *Config) UniqueName(base string) string {
	name := base
	for i := 2; ; i++ {
		if _, taken := c.Get(name); !taken {
			return name
		}
		name = fmt.Sprintf("%s-%d", base, i)
	}
}

func (s Server) ClaudeJSON() map[string]any {
	switch s.Transport {
	case TransportHTTP, TransportSSE:
		m := map[string]any{"type": s.Transport, "url": s.URL}
		if len(s.Headers) > 0 {
			m["headers"] = s.Headers
		}
		return m
	}
	m := map[string]any{"type": "stdio", "command": s.Command, "args": nonNil(s.Args)}
	if len(s.Env) > 0 {
		m["env"] = s.Env
	}
	return m
}

func (s Server) CursorJSON() map[string]any {
	switch s.Transport {
	case TransportHTTP, TransportSSE:
		m := map[string]any{"url": s.URL}
		if len(s.Headers) > 0 {
			m["headers"] = s.Headers
		}
		return m
	}
	m := map[string]any{"command": s.Command, "args": nonNil(s.Args)}
	if len(s.Env) > 0 {
		m["env"] = s.Env
	}
	return m
}

func (s Server) CommandLine() string {
	if s.Transport != TransportStdio {
		return s.Transport + " " + s.URL
	}
	parts := append([]string{s.Command}, s.Args...)
	for i, p := range parts {
		if strings.ContainsAny(p, " \"'") {
			parts[i] = fmt.Sprintf("%q", p)
		}
	}
	return strings.Join(parts, " ")
}

func nonNil(a []string) []string {
	if a == nil {
		return []string{}
	}
	return a
}
