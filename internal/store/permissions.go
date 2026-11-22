package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type PermStatus string

const (
	PermPending PermStatus = "pending"
	PermAllow   PermStatus = "allow"
	PermDeny    PermStatus = "deny"
	PermExpired PermStatus = "expired"
)

type Permission struct {
	ID        string
	SessionID string
	Tool      string
	Input     map[string]any
	Status    PermStatus
	Message   string
	CreatedAt time.Time
	DecidedAt time.Time
}

func (p Permission) Command() string {
	if c, ok := p.Input["command"].(string); ok {
		return c
	}
	return ""
}

func (p Permission) Summary() string {
	for _, k := range []string{"command", "file_path", "path", "url", "pattern"} {
		if v, ok := p.Input[k].(string); ok && v != "" {
			return v
		}
	}
	b, _ := json.Marshal(p.Input)
	return string(b)
}

const permCols = `id, session_id, tool, input, status, message, created_at, decided_at`

func scanPerm(r scanner) (Permission, error) {
	var p Permission
	var input, status string
	var c, d int64
	err := r.Scan(&p.ID, &p.SessionID, &p.Tool, &input, &status, &p.Message, &c, &d)
	_ = json.Unmarshal([]byte(input), &p.Input)
	p.Status, p.CreatedAt, p.DecidedAt = PermStatus(status), fromMS(c), fromMS(d)
	return p, err
}

func (s *Store) AddPermission(p Permission) error {
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now()
	}
	if p.Status == "" {
		p.Status = PermPending
	}
	in, _ := json.Marshal(p.Input)
	_, err := s.db.Exec(`INSERT INTO permissions (`+permCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.SessionID, p.Tool, string(in), string(p.Status), p.Message, ms(p.CreatedAt), ms(p.DecidedAt))
	return err
}

func (s *Store) Permission(id string) (Permission, bool, error) {
	p, err := scanPerm(s.db.QueryRow(`SELECT `+permCols+` FROM permissions WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Permission{}, false, nil
	}
	return p, err == nil, err
}

func (s *Store) PendingPermissions() ([]Permission, error) {
	rows, err := s.db.Query(`SELECT `+permCols+` FROM permissions WHERE status = ? ORDER BY created_at`, string(PermPending))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Permission
	for rows.Next() {
		p, err := scanPerm(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) DecidePermission(id string, status PermStatus, message string) (bool, error) {
	res, err := s.db.Exec(`UPDATE permissions SET status = ?, message = ?, decided_at = ? WHERE id = ? AND status = ?`,
		string(status), message, time.Now().UnixMilli(), id, string(PermPending))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (s *Store) ExpirePermissions(sessionID string) error {
	_, err := s.db.Exec(`UPDATE permissions SET status = ?, message = 'the worker stopped', decided_at = ? WHERE session_id = ? AND status = ?`,
		string(PermExpired), time.Now().UnixMilli(), sessionID, string(PermPending))
	return err
}

func (s *Store) AddPermitRule(sessionID, tool, prefix string) error {
	_, err := s.db.Exec(`INSERT OR IGNORE INTO permit_rules (session_id, tool, prefix, created_at) VALUES (?, ?, ?, ?)`,
		sessionID, tool, prefix, time.Now().UnixMilli())
	return err
}

func (s *Store) PermitRules(sessionID string) (map[string][]string, error) {
	rows, err := s.db.Query(`SELECT tool, prefix FROM permit_rules WHERE session_id = ?`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var tool, prefix string
		if err := rows.Scan(&tool, &prefix); err != nil {
			return nil, err
		}
		out[tool] = append(out[tool], prefix)
	}
	return out, rows.Err()
}

var shellOps = []string{"&&", "||", ";", "|", "`", "$(", ">", "<", "\n", "&"}

func CommandPrefix(cmd string) string {
	f := strings.Fields(cmd)
	if len(f) > 2 {
		f = f[:2]
	}
	return strings.Join(f, " ")
}

func RuleAllows(rules map[string][]string, tool string, input map[string]any) bool {
	prefixes, ok := rules[tool]
	if !ok {
		return false
	}
	if tool != "Bash" {
		for _, p := range prefixes {
			if p == "" {
				return true
			}
		}
		return false
	}
	cmd, _ := input["command"].(string)
	cmd = strings.TrimSpace(cmd)
	for _, op := range shellOps {
		if strings.Contains(cmd, op) {
			return false
		}
	}
	for _, p := range prefixes {
		if p != "" && (cmd == p || strings.HasPrefix(cmd, p+" ")) {
			return true
		}
	}
	return false
}
