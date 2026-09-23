package store

import (
	"context"
	"database/sql"
	"strings"

	"github.com/zJay26/claude-usage/internal/model"
)

func migrateRelationships(ctx context.Context, tx *sql.Tx) error {
	for _, stmt := range []string{
		`ALTER TABLE sessions ADD COLUMN parent_session_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sessions ADD COLUMN forked_from_id TEXT NOT NULL DEFAULT ''`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_parent ON sessions(parent_session_id)`,
	} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return err
		}
	}
	return nil
}

func (s *Store) PutRelationship(ctx context.Context, id, parent, fork string) error {
	if id == "" || (parent == "" && fork == "") {
		return nil
	}
	_, err := s.writer().ExecContext(ctx, `UPDATE sessions SET
		parent_session_id=CASE WHEN ?<>'' THEN ? ELSE parent_session_id END,
		forked_from_id=CASE WHEN ?<>'' THEN ? ELSE forked_from_id END
		WHERE session_id=? AND ((?<>'' AND parent_session_id<>?) OR (?<>'' AND forked_from_id<>?))`, parent, parent, fork, fork, id, parent, parent, fork, fork)
	return err
}

func (s *Store) SessionRelationships(ctx context.Context) (map[string]model.SessionInfo, error) {
	rows, err := s.reader().QueryContext(ctx, `SELECT session_id,title,parent_session_id,forked_from_id FROM sessions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]model.SessionInfo{}
	for rows.Next() {
		var item model.SessionInfo
		if err := rows.Scan(&item.SessionID, &item.Title, &item.ParentSessionID, &item.ForkedFromID); err != nil {
			return nil, err
		}
		out[item.SessionID] = item
	}
	return out, rows.Err()
}
