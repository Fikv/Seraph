package store

import (
	"bufio"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

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
	ProjectID      string    `json:"project_id,omitempty"`
	TodoID         string    `json:"todo_id,omitempty"`
	OwnerPID       int       `json:"-"`
	HeartbeatAt    time.Time `json:"-"`
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

func DBPath() string { return filepath.Join(Home(), "seraph.db") }

func NewID() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

type Store struct {
	db *sql.DB
}

var migrations = []string{
	`CREATE TABLE sessions (
		id TEXT PRIMARY KEY,
		title TEXT NOT NULL,
		task TEXT NOT NULL,
		agent TEXT NOT NULL,
		model TEXT NOT NULL DEFAULT '',
		approval TEXT NOT NULL DEFAULT 'safe',
		repo TEXT NOT NULL DEFAULT '',
		dir TEXT NOT NULL DEFAULT '',
		branch TEXT NOT NULL DEFAULT '',
		base TEXT NOT NULL DEFAULT '',
		worktree INTEGER NOT NULL DEFAULT 0,
		status TEXT NOT NULL,
		agent_session_id TEXT NOT NULL DEFAULT '',
		cost REAL NOT NULL DEFAULT 0,
		turns INTEGER NOT NULL DEFAULT 0,
		last_line TEXT NOT NULL DEFAULT '',
		error TEXT NOT NULL DEFAULT '',
		owner_pid INTEGER NOT NULL DEFAULT 0,
		heartbeat_at INTEGER NOT NULL DEFAULT 0,
		stop_requested INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);
	CREATE INDEX sessions_created ON sessions(created_at DESC);
	CREATE TABLE events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
		kind TEXT NOT NULL,
		text TEXT NOT NULL DEFAULT '',
		tool TEXT NOT NULL DEFAULT '',
		agent_session_id TEXT NOT NULL DEFAULT '',
		cost REAL NOT NULL DEFAULT 0,
		is_error INTEGER NOT NULL DEFAULT 0,
		t INTEGER NOT NULL
	);
	CREATE INDEX events_session ON events(session_id, id);
	CREATE TABLE mcp_servers (name TEXT PRIMARY KEY, data TEXT NOT NULL);
	CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
	CREATE TABLE cache (key TEXT PRIMARY KEY, value TEXT NOT NULL, expires_at INTEGER NOT NULL);`,
	`CREATE TABLE projects (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL UNIQUE COLLATE NOCASE,
		description TEXT NOT NULL DEFAULT '',
		notes_dir TEXT NOT NULL DEFAULT '',
		default_agent TEXT NOT NULL DEFAULT '',
		max_parallel INTEGER NOT NULL DEFAULT 2,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);
	CREATE TABLE project_repos (
		project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
		path TEXT NOT NULL,
		position INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (project_id, path)
	);
	CREATE TABLE todos (
		id TEXT PRIMARY KEY,
		project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
		title TEXT NOT NULL,
		body TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL,
		priority INTEGER NOT NULL DEFAULT 0,
		repo TEXT NOT NULL DEFAULT '',
		agent TEXT NOT NULL DEFAULT '',
		session_id TEXT NOT NULL DEFAULT '',
		note TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);
	CREATE INDEX todos_project ON todos(project_id, status, priority DESC, created_at);
	ALTER TABLE sessions ADD COLUMN project_id TEXT NOT NULL DEFAULT '';
	ALTER TABLE sessions ADD COLUMN todo_id TEXT NOT NULL DEFAULT '';`,
	`ALTER TABLE todos ADD COLUMN model TEXT NOT NULL DEFAULT '';`,
}

func Open() (*Store, error) {
	if err := os.MkdirAll(Home(), 0o755); err != nil {
		return nil, err
	}
	dsn := "file:" + DBPath() + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate %s: %w", DBPath(), err)
	}
	if err := s.importLegacy(); err != nil {
		db.Close()
		return nil, fmt.Errorf("import legacy sessions: %w", err)
	}
	_ = os.Chmod(DBPath(), 0o600)
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) migrate() error {
	var version int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	for i := version; i < len(migrations); i++ {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return err
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, i+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func ms(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func fromMS(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.UnixMilli(v)
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

const sessionCols = `id, title, task, agent, model, approval, repo, dir, branch, base, worktree, status,
	agent_session_id, cost, turns, last_line, error, owner_pid, heartbeat_at, created_at, updated_at, project_id, todo_id`

func (s *Store) Save(sess Session) error {
	_, err := s.db.Exec(`INSERT INTO sessions (`+sessionCols+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			title=excluded.title, task=excluded.task, agent=excluded.agent, model=excluded.model,
			approval=excluded.approval, repo=excluded.repo, dir=excluded.dir, branch=excluded.branch,
			base=excluded.base, worktree=excluded.worktree, status=excluded.status,
			agent_session_id=excluded.agent_session_id, cost=excluded.cost, turns=excluded.turns,
			last_line=excluded.last_line, error=excluded.error, owner_pid=excluded.owner_pid,
			heartbeat_at=excluded.heartbeat_at, updated_at=excluded.updated_at,
			project_id=excluded.project_id, todo_id=excluded.todo_id`,
		sess.ID, sess.Title, sess.Task, sess.Agent, sess.Model, sess.Approval, sess.Repo, sess.Dir,
		sess.Branch, sess.Base, b2i(sess.Worktree), string(sess.Status), sess.AgentSessionID, sess.Cost,
		sess.Turns, sess.LastLine, sess.Error, sess.OwnerPID, ms(sess.HeartbeatAt), ms(sess.CreatedAt), ms(sess.UpdatedAt),
		sess.ProjectID, sess.TodoID)
	return err
}

type scanner interface{ Scan(dest ...any) error }

func scanSession(r scanner) (Session, error) {
	var sess Session
	var status string
	var worktree int
	var hb, created, updated int64
	err := r.Scan(&sess.ID, &sess.Title, &sess.Task, &sess.Agent, &sess.Model, &sess.Approval, &sess.Repo,
		&sess.Dir, &sess.Branch, &sess.Base, &worktree, &status, &sess.AgentSessionID, &sess.Cost, &sess.Turns,
		&sess.LastLine, &sess.Error, &sess.OwnerPID, &hb, &created, &updated, &sess.ProjectID, &sess.TodoID)
	sess.Status = Status(status)
	sess.Worktree = worktree != 0
	sess.HeartbeatAt, sess.CreatedAt, sess.UpdatedAt = fromMS(hb), fromMS(created), fromMS(updated)
	return sess, err
}

func (s *Store) Get(id string) (Session, bool, error) {
	sess, err := scanSession(s.db.QueryRow(`SELECT `+sessionCols+` FROM sessions WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, false, nil
	}
	return sess, err == nil, err
}

func (s *Store) List() ([]Session, error) {
	rows, err := s.db.Query(`SELECT ` + sessionCols + ` FROM sessions ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		sess, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sess)
	}
	return out, rows.Err()
}

func (s *Store) Delete(id string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE id = ?`, id)
	return err
}

func (s *Store) AppendLog(id string, ev agent.Event) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO events (session_id, kind, text, tool, agent_session_id, cost, is_error, t)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, string(ev.Kind), ev.Text, ev.Tool, ev.SessionID, ev.Cost, b2i(ev.IsError), ms(ev.Time))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) ReadLog(id string) ([]agent.Event, error) {
	evs, _, err := s.ReadLogSince(id, 0)
	return evs, err
}

func (s *Store) ReadLogSince(id string, after int64) ([]agent.Event, int64, error) {
	rows, err := s.db.Query(`SELECT id, kind, text, tool, agent_session_id, cost, is_error, t
		FROM events WHERE session_id = ? AND id > ? ORDER BY id`, id, after)
	if err != nil {
		return nil, after, err
	}
	defer rows.Close()
	last := after
	var out []agent.Event
	for rows.Next() {
		var ev agent.Event
		var kind string
		var isErr int
		var t int64
		if err := rows.Scan(&last, &kind, &ev.Text, &ev.Tool, &ev.SessionID, &ev.Cost, &isErr, &t); err != nil {
			return nil, after, err
		}
		ev.Kind, ev.IsError, ev.Time = agent.Kind(kind), isErr != 0, fromMS(t)
		out = append(out, ev)
	}
	return out, last, rows.Err()
}

func (s *Store) Heartbeat(ids []string, pid int, now time.Time) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := []any{pid, ms(now)}
	for _, id := range ids {
		args = append(args, id)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET owner_pid = ?, heartbeat_at = ? WHERE id IN (`+ph+`)`, args...); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT id FROM sessions WHERE stop_requested = 1 AND id IN (`+ph+`)`, args[2:]...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var stop []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		stop = append(stop, id)
	}
	return stop, rows.Err()
}

func (s *Store) RequestStop(id string, on bool) error {
	_, err := s.db.Exec(`UPDATE sessions SET stop_requested = ? WHERE id = ?`, b2i(on), id)
	return err
}

func (s *Store) Setting(key string) string {
	var v string
	_ = s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	return v
}

func (s *Store) SetSetting(key, value string) error {
	if value == "" {
		_, err := s.db.Exec(`DELETE FROM settings WHERE key = ?`, key)
		return err
	}
	_, err := s.db.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

func (s *Store) CacheGet(_ context.Context, key string) (string, bool) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM cache WHERE key = ? AND expires_at > ?`, key, time.Now().UnixMilli()).Scan(&v)
	return v, err == nil
}

func (s *Store) CacheSet(_ context.Context, key, value string, ttl time.Duration) {
	_, _ = s.db.Exec(`INSERT INTO cache (key, value, expires_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, expires_at = excluded.expires_at`,
		key, value, time.Now().Add(ttl).UnixMilli())
	_, _ = s.db.Exec(`DELETE FROM cache WHERE expires_at <= ?`, time.Now().UnixMilli())
}

type Stats struct {
	Sessions, Events, MCPServers int
	SizeBytes                    int64
}

func (s *Store) Stats() Stats {
	var st Stats
	_ = s.db.QueryRow(`SELECT count(*) FROM sessions`).Scan(&st.Sessions)
	_ = s.db.QueryRow(`SELECT count(*) FROM events`).Scan(&st.Events)
	_ = s.db.QueryRow(`SELECT count(*) FROM mcp_servers`).Scan(&st.MCPServers)
	if fi, err := os.Stat(DBPath()); err == nil {
		st.SizeBytes = fi.Size()
	}
	return st
}

func (s *Store) importLegacy() error {
	dir := filepath.Join(Home(), "sessions")
	matches, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	if len(matches) == 0 {
		return nil
	}
	for _, m := range matches {
		data, err := os.ReadFile(m)
		if err != nil {
			continue
		}
		var sess Session
		if json.Unmarshal(data, &sess) != nil || sess.ID == "" {
			continue
		}
		if _, exists, _ := s.Get(sess.ID); exists {
			continue
		}
		if err := s.Save(sess); err != nil {
			return err
		}
		f, err := os.Open(strings.TrimSuffix(m, ".json") + ".log")
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
		for sc.Scan() {
			var ev agent.Event
			if json.Unmarshal(sc.Bytes(), &ev) == nil {
				if _, err := s.AppendLog(sess.ID, ev); err != nil {
					f.Close()
					return err
				}
			}
		}
		f.Close()
	}
	return os.Rename(dir, dir+".imported-"+time.Now().Format("20060102-150405"))
}
