package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Project struct {
	ID           string
	Name         string
	Description  string
	NotesDir     string
	DefaultAgent string
	MaxParallel  int
	Repos        []string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type TodoStatus string

const (
	TodoOpen   TodoStatus = "todo"
	TodoQueued TodoStatus = "queued"
	TodoDoing  TodoStatus = "doing"
	TodoReview TodoStatus = "review"
	TodoDone   TodoStatus = "done"
)

var TodoStatuses = []TodoStatus{TodoOpen, TodoQueued, TodoDoing, TodoReview, TodoDone}

type Todo struct {
	ID        string
	ProjectID string
	Title     string
	Body      string
	Status    TodoStatus
	Priority  int
	Repo      string
	Agent     string
	Model     string
	SessionID string
	Note      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

const CurrentProjectSetting = "current_project"

func (s *Store) SaveProject(p Project) error {
	if strings.TrimSpace(p.Name) == "" {
		return errors.New("project name is empty")
	}
	if p.MaxParallel <= 0 {
		p.MaxParallel = 2
	}
	now := time.Now()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`INSERT INTO projects (id, name, description, notes_dir, default_agent, max_parallel, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, description=excluded.description, notes_dir=excluded.notes_dir,
			default_agent=excluded.default_agent, max_parallel=excluded.max_parallel, updated_at=excluded.updated_at`,
		p.ID, p.Name, p.Description, p.NotesDir, p.DefaultAgent, p.MaxParallel, ms(p.CreatedAt), ms(p.UpdatedAt))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return fmt.Errorf("a project named %q already exists", p.Name)
		}
		return err
	}
	if _, err := tx.Exec(`DELETE FROM project_repos WHERE project_id = ?`, p.ID); err != nil {
		return err
	}
	for i, r := range p.Repos {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO project_repos (project_id, path, position) VALUES (?, ?, ?)`, p.ID, r, i); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) Projects() ([]Project, error) {
	rows, err := s.db.Query(`SELECT id, name, description, notes_dir, default_agent, max_parallel, created_at, updated_at
		FROM projects ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	var out []Project
	for rows.Next() {
		var p Project
		var c, u int64
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.NotesDir, &p.DefaultAgent, &p.MaxParallel, &c, &u); err != nil {
			rows.Close()
			return nil, err
		}
		p.CreatedAt, p.UpdatedAt = fromMS(c), fromMS(u)
		out = append(out, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		repos, err := s.projectRepos(out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Repos = repos
	}
	return out, nil
}

func (s *Store) projectRepos(id string) ([]string, error) {
	rows, err := s.db.Query(`SELECT path FROM project_repos WHERE project_id = ? ORDER BY position, path`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) Project(idOrName string) (Project, bool, error) {
	list, err := s.Projects()
	if err != nil {
		return Project{}, false, err
	}
	for _, p := range list {
		if p.ID == idOrName || strings.EqualFold(p.Name, idOrName) {
			return p, true, nil
		}
	}
	return Project{}, false, nil
}

func (s *Store) DeleteProject(id string) error {
	wasCurrent := s.Setting(CurrentProjectSetting) == id
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE sessions SET project_id = '', todo_id = '' WHERE project_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE missions SET project_id = '', todo_id = '' WHERE project_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM projects WHERE id = ?`, id); err != nil {
		return err
	}
	if wasCurrent {
		if _, err := tx.Exec(`DELETE FROM settings WHERE key = ?`, CurrentProjectSetting); err != nil {
			return err
		}
	}
	return tx.Commit()
}

const todoCols = `id, project_id, title, body, status, priority, repo, agent, session_id, note, created_at, updated_at, model`

func scanTodo(r scanner) (Todo, error) {
	var t Todo
	var status string
	var c, u int64
	err := r.Scan(&t.ID, &t.ProjectID, &t.Title, &t.Body, &status, &t.Priority, &t.Repo, &t.Agent, &t.SessionID, &t.Note, &c, &u, &t.Model)
	t.Status, t.CreatedAt, t.UpdatedAt = TodoStatus(status), fromMS(c), fromMS(u)
	return t, err
}

func (s *Store) SaveTodo(t Todo) error {
	if strings.TrimSpace(t.Title) == "" {
		return errors.New("todo title is empty")
	}
	if t.Status == "" {
		t.Status = TodoOpen
	}
	now := time.Now()
	if t.CreatedAt.IsZero() {
		t.CreatedAt = now
	}
	t.UpdatedAt = now
	_, err := s.db.Exec(`INSERT INTO todos (`+todoCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET title=excluded.title, body=excluded.body, status=excluded.status,
			priority=excluded.priority, repo=excluded.repo, agent=excluded.agent, session_id=excluded.session_id,
			note=excluded.note, updated_at=excluded.updated_at, model=excluded.model`,
		t.ID, t.ProjectID, t.Title, t.Body, string(t.Status), t.Priority, t.Repo, t.Agent, t.SessionID, t.Note, ms(t.CreatedAt), ms(t.UpdatedAt), t.Model)
	return err
}

func (s *Store) Todo(id string) (Todo, bool, error) {
	t, err := scanTodo(s.db.QueryRow(`SELECT `+todoCols+` FROM todos WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Todo{}, false, nil
	}
	return t, err == nil, err
}

func (s *Store) Todos(projectID string) ([]Todo, error) {
	rows, err := s.db.Query(`SELECT `+todoCols+` FROM todos WHERE project_id = ?
		ORDER BY priority DESC, created_at`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Todo
	for rows.Next() {
		t, err := scanTodo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) DeleteTodo(id string) error {
	_, err := s.db.Exec(`DELETE FROM todos WHERE id = ?`, id)
	return err
}

func (s *Store) ClaimTodo(id string) (bool, error) {
	res, err := s.db.Exec(`UPDATE todos SET status = ?, updated_at = ? WHERE id = ? AND status IN (?, ?)`,
		string(TodoDoing), time.Now().UnixMilli(), id, string(TodoQueued), string(TodoOpen))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}
