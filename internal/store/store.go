package store

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"seraph/internal/agent"
)

type Status string

const (
	StatusQueued  Status = "queued"
	StatusWorking Status = "working"
	StatusDone    Status = "done"
	StatusFailed  Status = "failed"
	StatusStopped Status = "stopped"
)

type Session struct {
	ID             string    `json:"id"`
	Title          string    `json:"title"`
	Task           string    `json:"task"`
	Agent          string    `json:"agent"`
	Model          string    `json:"model,omitempty"`
	Approval       string    `json:"approval"`
	Repo           string    `json:"repo,omitempty"`
	Dir            string    `json:"dir"`
	Branch         string    `json:"branch,omitempty"`
	Base           string    `json:"base,omitempty"`
	Worktree       bool      `json:"worktree"`
	Status         Status    `json:"status"`
	AgentSessionID string    `json:"agent_session_id,omitempty"`
	Cost           float64   `json:"cost,omitempty"`
	Turns          int       `json:"turns"`
	LastLine       string    `json:"last_line,omitempty"`
	Error          string    `json:"error,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func Home() string {
	if h := os.Getenv("SERAPH_HOME"); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".seraph"
	}
	return filepath.Join(home, ".seraph")
}

func NewID() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

type Store struct {
	dir string
	mu  sync.Mutex
}

func Open() (*Store, error) {
	dir := filepath.Join(Home(), "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

func (s *Store) path(id, ext string) string { return filepath.Join(s.dir, id+ext) }

func (s *Store) Save(sess Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path(sess.ID, ".json.tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path(sess.ID, ".json"))
}

func (s *Store) List() ([]Session, error) {
	matches, err := filepath.Glob(filepath.Join(s.dir, "*.json"))
	if err != nil {
		return nil, err
	}
	out := make([]Session, 0, len(matches))
	for _, m := range matches {
		data, err := os.ReadFile(m)
		if err != nil {
			continue
		}
		var sess Session
		if json.Unmarshal(data, &sess) == nil && sess.ID != "" {
			out = append(out, sess)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (s *Store) AppendLog(id string, ev agent.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.OpenFile(s.path(id, ".log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	_, err = f.Write(append(data, '\n'))
	return err
}

func (s *Store) ReadLog(id string) ([]agent.Event, error) {
	f, err := os.Open(s.path(id, ".log"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []agent.Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for sc.Scan() {
		var ev agent.Event
		if json.Unmarshal(sc.Bytes(), &ev) == nil {
			out = append(out, ev)
		}
	}
	return out, sc.Err()
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = os.Remove(s.path(id, ".log"))
	err := os.Remove(s.path(id, ".json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
