package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

const (
	ReqPending = "pending"
	ReqDone    = "done"
	ReqError   = "error"
)

type TeamRequest struct {
	ID          string
	TeamID      string
	FromSession string
	Op          string
	Args        map[string]any
	Status      string
	Result      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type TeamMessage struct {
	ID          string
	TeamID      string
	FromSession string
	ToSession   string
	Text        string
	CreatedAt   time.Time
	DeliveredAt time.Time
}

const reqCols = `id, team_id, from_session, op, args, status, result, created_at, updated_at`

func scanReq(r scanner) (TeamRequest, error) {
	var x TeamRequest
	var args string
	var c, u int64
	err := r.Scan(&x.ID, &x.TeamID, &x.FromSession, &x.Op, &args, &x.Status, &x.Result, &c, &u)
	_ = json.Unmarshal([]byte(args), &x.Args)
	x.CreatedAt, x.UpdatedAt = fromMS(c), fromMS(u)
	return x, err
}

func (s *Store) AddTeamRequest(x TeamRequest) error {
	now := time.Now()
	args, _ := json.Marshal(x.Args)
	if x.Status == "" {
		x.Status = ReqPending
	}
	_, err := s.db.Exec(`INSERT INTO team_requests (`+reqCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		x.ID, x.TeamID, x.FromSession, x.Op, string(args), x.Status, x.Result, ms(now), ms(now))
	return err
}

func (s *Store) TeamRequest(id string) (TeamRequest, bool, error) {
	x, err := scanReq(s.db.QueryRow(`SELECT `+reqCols+` FROM team_requests WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return TeamRequest{}, false, nil
	}
	return x, err == nil, err
}

func (s *Store) PendingTeamRequests() ([]TeamRequest, error) {
	rows, err := s.db.Query(`SELECT `+reqCols+` FROM team_requests WHERE status = ? ORDER BY created_at`, ReqPending)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TeamRequest
	for rows.Next() {
		x, err := scanReq(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) FinishTeamRequest(id, status, result string) error {
	_, err := s.db.Exec(`UPDATE team_requests SET status = ?, result = ?, updated_at = ? WHERE id = ?`,
		status, result, time.Now().UnixMilli(), id)
	return err
}

func (s *Store) AddTeamMessage(x TeamMessage) error {
	_, err := s.db.Exec(`INSERT INTO team_messages (id, team_id, from_session, to_session, text, created_at, delivered_at) VALUES (?, ?, ?, ?, ?, ?, 0)`,
		x.ID, x.TeamID, x.FromSession, x.ToSession, x.Text, time.Now().UnixMilli())
	return err
}

func (s *Store) UndeliveredMessages(to string) ([]TeamMessage, error) {
	rows, err := s.db.Query(`SELECT id, team_id, from_session, to_session, text, created_at, delivered_at FROM team_messages
		WHERE to_session = ? AND delivered_at = 0 ORDER BY created_at`, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TeamMessage
	for rows.Next() {
		var x TeamMessage
		var c, d int64
		if err := rows.Scan(&x.ID, &x.TeamID, &x.FromSession, &x.ToSession, &x.Text, &c, &d); err != nil {
			return nil, err
		}
		x.CreatedAt, x.DeliveredAt = fromMS(c), fromMS(d)
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) MarkDelivered(ids []string) error {
	for _, id := range ids {
		if _, err := s.db.Exec(`UPDATE team_messages SET delivered_at = ? WHERE id = ?`, time.Now().UnixMilli(), id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) TeamSessions(teamID string) ([]Session, error) {
	rows, err := s.db.Query(`SELECT `+sessionCols+` FROM sessions WHERE team_id = ? ORDER BY created_at`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		x, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) MarkReported(id string, on bool) error {
	var err error
	if on {
		_, err = s.db.Exec(`INSERT OR IGNORE INTO team_reported (session_id, at) VALUES (?, ?)`, id, time.Now().UnixMilli())
	} else {
		_, err = s.db.Exec(`DELETE FROM team_reported WHERE session_id = ?`, id)
	}
	return err
}

func (s *Store) Reported(id string) bool {
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM team_reported WHERE session_id = ?`, id).Scan(&n)
	return n > 0
}
