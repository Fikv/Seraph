package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type MissionPhase string

const (
	PhaseSpec     MissionPhase = "spec"
	PhaseAnalysis MissionPhase = "analysis"
	PhasePlan     MissionPhase = "plan"
	PhaseBuild    MissionPhase = "build"
	PhaseTest     MissionPhase = "test"
	PhaseAccept   MissionPhase = "accept"
	PhaseDone     MissionPhase = "done"
)

var MissionPhases = []MissionPhase{PhaseSpec, PhaseAnalysis, PhasePlan, PhaseBuild, PhaseTest, PhaseAccept, PhaseDone}

type MissionState string

const (
	MissionRunning   MissionState = "running"
	MissionWaiting   MissionState = "waiting"
	MissionBlocked   MissionState = "blocked"
	MissionDone      MissionState = "done"
	MissionCancelled MissionState = "cancelled"
)

type Mission struct {
	ID        string
	ProjectID string
	TodoID    string
	Title     string
	Goal      string
	Repo      string
	Base      string
	Branch    string
	Dir       string
	Artifacts string
	Phase     MissionPhase
	State     MissionState
	Gate      MissionPhase
	Error     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (m Mission) Active() bool {
	return m.State == MissionRunning || m.State == MissionWaiting || m.State == MissionBlocked
}

type Role string

const (
	RoleOrchestrator Role = "orchestrator"
	RolePO           Role = "po"
	RoleAnalyst      Role = "analyst"
	RoleLead         Role = "lead"
	RoleDev          Role = "dev"
	RoleReviewer     Role = "reviewer"
	RoleTester       Role = "tester"
	RoleAcceptance   Role = "acceptance"
)

var Roles = []Role{RolePO, RoleAnalyst, RoleLead, RoleDev, RoleReviewer, RoleTester, RoleAcceptance}

type NodeStatus string

const (
	NodePending NodeStatus = "pending"
	NodeWorking NodeStatus = "working"
	NodeDone    NodeStatus = "done"
	NodeMerged  NodeStatus = "merged"
	NodeFailed  NodeStatus = "failed"
	NodeSkipped NodeStatus = "skipped"
)

type Node struct {
	ID        string
	MissionID string
	ParentID  string
	Role      Role
	Title     string
	Task      string
	Files     []string
	Deps      []string
	Status    NodeStatus
	SessionID string
	Artifact  string
	Verdict   string
	Round     int
	Position  int
	Note      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

const missionCols = `id, project_id, todo_id, title, goal, repo, base, branch, dir, artifacts, phase, state, gate, error, created_at, updated_at`

func scanMission(r scanner) (Mission, error) {
	var m Mission
	var phase, state, gate string
	var c, u int64
	err := r.Scan(&m.ID, &m.ProjectID, &m.TodoID, &m.Title, &m.Goal, &m.Repo, &m.Base, &m.Branch, &m.Dir, &m.Artifacts,
		&phase, &state, &gate, &m.Error, &c, &u)
	m.Phase, m.State, m.Gate = MissionPhase(phase), MissionState(state), MissionPhase(gate)
	m.CreatedAt, m.UpdatedAt = fromMS(c), fromMS(u)
	return m, err
}

func (s *Store) SaveMission(m Mission) error {
	if strings.TrimSpace(m.Goal) == "" {
		return errors.New("mission goal is empty")
	}
	now := time.Now()
	if m.CreatedAt.IsZero() {
		m.CreatedAt = now
	}
	m.UpdatedAt = now
	_, err := s.db.Exec(`INSERT INTO missions (`+missionCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET project_id=excluded.project_id, todo_id=excluded.todo_id, title=excluded.title,
			goal=excluded.goal, repo=excluded.repo, base=excluded.base, branch=excluded.branch, dir=excluded.dir,
			artifacts=excluded.artifacts, phase=excluded.phase, state=excluded.state, gate=excluded.gate,
			error=excluded.error, updated_at=excluded.updated_at`,
		m.ID, m.ProjectID, m.TodoID, m.Title, m.Goal, m.Repo, m.Base, m.Branch, m.Dir, m.Artifacts,
		string(m.Phase), string(m.State), string(m.Gate), m.Error, ms(m.CreatedAt), ms(m.UpdatedAt))
	return err
}

func (s *Store) Mission(id string) (Mission, bool, error) {
	m, err := scanMission(s.db.QueryRow(`SELECT `+missionCols+` FROM missions WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Mission{}, false, nil
	}
	return m, err == nil, err
}

func (s *Store) MissionForTodo(todoID string) (Mission, bool, error) {
	m, err := scanMission(s.db.QueryRow(`SELECT `+missionCols+` FROM missions WHERE todo_id = ? ORDER BY created_at DESC LIMIT 1`, todoID))
	if errors.Is(err, sql.ErrNoRows) {
		return Mission{}, false, nil
	}
	return m, err == nil, err
}

func (s *Store) Missions() ([]Mission, error) {
	rows, err := s.db.Query(`SELECT ` + missionCols + ` FROM missions ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Mission
	for rows.Next() {
		m, err := scanMission(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) DeleteMission(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE sessions SET mission_id = '', node_id = '' WHERE mission_id = ?`, id); err != nil {
		tx.Rollback()
		return err
	}
	if _, err := tx.Exec(`DELETE FROM missions WHERE id = ?`, id); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

const nodeCols = `id, mission_id, parent_id, role, title, task, files, deps, status, session_id, artifact, verdict, round, position, note, created_at, updated_at`

func scanNode(r scanner) (Node, error) {
	var n Node
	var role, status, files, deps string
	var c, u int64
	err := r.Scan(&n.ID, &n.MissionID, &n.ParentID, &role, &n.Title, &n.Task, &files, &deps, &status, &n.SessionID,
		&n.Artifact, &n.Verdict, &n.Round, &n.Position, &n.Note, &c, &u)
	n.Role, n.Status = Role(role), NodeStatus(status)
	_ = json.Unmarshal([]byte(files), &n.Files)
	_ = json.Unmarshal([]byte(deps), &n.Deps)
	n.CreatedAt, n.UpdatedAt = fromMS(c), fromMS(u)
	return n, err
}

func jsonList(v []string) string {
	if v == nil {
		v = []string{}
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func (s *Store) SaveNode(n Node) error {
	now := time.Now()
	if n.CreatedAt.IsZero() {
		n.CreatedAt = now
	}
	n.UpdatedAt = now
	_, err := s.db.Exec(`INSERT INTO mission_nodes (`+nodeCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET parent_id=excluded.parent_id, role=excluded.role, title=excluded.title,
			task=excluded.task, files=excluded.files, deps=excluded.deps, status=excluded.status,
			session_id=excluded.session_id, artifact=excluded.artifact, verdict=excluded.verdict,
			round=excluded.round, position=excluded.position, note=excluded.note, updated_at=excluded.updated_at`,
		n.ID, n.MissionID, n.ParentID, string(n.Role), n.Title, n.Task, jsonList(n.Files), jsonList(n.Deps),
		string(n.Status), n.SessionID, n.Artifact, n.Verdict, n.Round, n.Position, n.Note, ms(n.CreatedAt), ms(n.UpdatedAt))
	return err
}

func (s *Store) Nodes(missionID string) ([]Node, error) {
	rows, err := s.db.Query(`SELECT `+nodeCols+` FROM mission_nodes WHERE mission_id = ? ORDER BY position, created_at`, missionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) DeleteNode(id string) error {
	_, err := s.db.Exec(`DELETE FROM mission_nodes WHERE id = ?`, id)
	return err
}
