package store

import (
	"database/sql"
	"errors"
	"time"
)

type ReleaseStatus string

const (
	ReleaseValidating ReleaseStatus = "validating"
	ReleasePass       ReleaseStatus = "pass"
	ReleaseFail       ReleaseStatus = "fail"
	ReleaseError      ReleaseStatus = "error"
)

const (
	TargetSession = "session"
	TargetMission = "mission"
)

type Release struct {
	ID        string
	Kind      string
	TargetID  string
	Title     string
	Repo      string
	Branch    string
	Base      string
	Head      string
	SessionID string
	Status    ReleaseStatus
	Report    string
	Note      string
	MergedAt  time.Time
	MergeSHA  string
	PushedAt  time.Time
	PushOut   string
	CreatedAt time.Time
	UpdatedAt time.Time
}

const releaseCols = `id, kind, target_id, title, repo, branch, base, head, session_id, status, report, note,
	merged_at, merge_sha, pushed_at, push_out, created_at, updated_at`

func scanRelease(r scanner) (Release, error) {
	var x Release
	var status string
	var merged, pushed, c, u int64
	err := r.Scan(&x.ID, &x.Kind, &x.TargetID, &x.Title, &x.Repo, &x.Branch, &x.Base, &x.Head, &x.SessionID, &status,
		&x.Report, &x.Note, &merged, &x.MergeSHA, &pushed, &x.PushOut, &c, &u)
	x.Status = ReleaseStatus(status)
	x.MergedAt, x.PushedAt, x.CreatedAt, x.UpdatedAt = fromMS(merged), fromMS(pushed), fromMS(c), fromMS(u)
	return x, err
}

func (s *Store) SaveRelease(x Release) error {
	now := time.Now()
	if x.CreatedAt.IsZero() {
		x.CreatedAt = now
	}
	x.UpdatedAt = now
	_, err := s.db.Exec(`INSERT INTO releases (`+releaseCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET title=excluded.title, head=excluded.head, session_id=excluded.session_id,
			status=excluded.status, report=excluded.report, note=excluded.note, merged_at=excluded.merged_at,
			merge_sha=excluded.merge_sha, pushed_at=excluded.pushed_at, push_out=excluded.push_out, updated_at=excluded.updated_at`,
		x.ID, x.Kind, x.TargetID, x.Title, x.Repo, x.Branch, x.Base, x.Head, x.SessionID, string(x.Status), x.Report, x.Note,
		ms(x.MergedAt), x.MergeSHA, ms(x.PushedAt), x.PushOut, ms(x.CreatedAt), ms(x.UpdatedAt))
	return err
}

func (s *Store) LatestRelease(kind, targetID string) (Release, bool, error) {
	x, err := scanRelease(s.db.QueryRow(`SELECT `+releaseCols+` FROM releases WHERE kind = ? AND target_id = ?
		ORDER BY created_at DESC LIMIT 1`, kind, targetID))
	if errors.Is(err, sql.ErrNoRows) {
		return Release{}, false, nil
	}
	return x, err == nil, err
}

func (s *Store) ReleaseBySession(sessionID string) (Release, bool, error) {
	x, err := scanRelease(s.db.QueryRow(`SELECT `+releaseCols+` FROM releases WHERE session_id = ? LIMIT 1`, sessionID))
	if errors.Is(err, sql.ErrNoRows) {
		return Release{}, false, nil
	}
	return x, err == nil, err
}

func (s *Store) Releases(status ReleaseStatus) ([]Release, error) {
	q := `SELECT ` + releaseCols + ` FROM releases ORDER BY created_at`
	args := []any{}
	if status != "" {
		q = `SELECT ` + releaseCols + ` FROM releases WHERE status = ? ORDER BY created_at`
		args = append(args, string(status))
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Release
	for rows.Next() {
		x, err := scanRelease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
