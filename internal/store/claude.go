package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/zJay26/claude-usage/internal/model"
)

func migrateLedger(ctx context.Context, tx *sql.Tx) error {
	for _, statement := range []string{
		`ALTER TABLE usage_events ADD COLUMN hour_start INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE usage_events ADD COLUMN source_homes TEXT NOT NULL DEFAULT '[]'`,
		`ALTER TABLE usage_events ADD COLUMN iteration_type TEXT NOT NULL DEFAULT 'message'`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil && !isDuplicateColumn(err) {
			return err
		}
	}
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS claude_requests (request_key TEXT PRIMARY KEY, request_id TEXT NOT NULL, events_json TEXT NOT NULL, complete INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE IF NOT EXISTS request_sources (request_key TEXT NOT NULL, home TEXT NOT NULL, path TEXT NOT NULL, PRIMARY KEY(request_key,home,path))`,
		`CREATE TABLE IF NOT EXISTS request_identities (identity TEXT PRIMARY KEY, request_key TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_requests_id ON claude_requests(request_id)`,
		`CREATE INDEX IF NOT EXISTS idx_sources_home ON request_sources(home,request_key)`,
		`CREATE INDEX IF NOT EXISTS idx_events_request ON usage_events(turn_id)`,
		`INSERT OR IGNORE INTO meta VALUES('data_revision','1')`,
		`INSERT OR IGNORE INTO meta VALUES('product','claude-usage')`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	for _, table := range []string{"usage_events", "sessions"} {
		for _, op := range []string{"INSERT", "UPDATE", "DELETE"} {
			if _, err := tx.ExecContext(ctx, `CREATE TRIGGER IF NOT EXISTS revision_`+table+`_`+op+` AFTER `+op+` ON `+table+` BEGIN UPDATE meta SET value=CAST(value AS INTEGER)+1 WHERE key='data_revision'; END`); err != nil {
				return err
			}
		}
	}
	return nil
}

func isDuplicateColumn(err error) bool {
	return err != nil && strings.Contains(err.Error(), "duplicate column")
}

// PutRequest is called inside Ingest. The global message identity deliberately
// does not include a file, session, OS, or home: those can all contain copies.
// Only whitelisted metadata and counters are serialized here, never message content.
func (s *Store) PutRequest(ctx context.Context, key, requestID, home, path string, events []model.UsageEvent, complete bool) (inserted int64, corrected, conflict bool, err error) {
	identity := key
	// Either provider request identity or message identity can join a mirror.
	// Keep aliases so a later record without requestId still resolves correctly.
	var byMessage, byRequest string
	_ = s.writer().QueryRowContext(ctx, `SELECT request_key FROM request_identities WHERE identity=?`, identity).Scan(&byMessage)
	if byMessage == "" {
		_ = s.writer().QueryRowContext(ctx, `SELECT request_key FROM claude_requests WHERE request_key=?`, identity).Scan(&byMessage)
	}
	if byMessage != "" && requestID != "" {
		var existingRequest string
		if err = s.writer().QueryRowContext(ctx, `SELECT request_id FROM claude_requests WHERE request_key=?`, byMessage).Scan(&existingRequest); err != nil {
			return
		}
		if existingRequest != "" && existingRequest != requestID {
			conflict = true
			err = s.AddWarning(ctx, "request_conflict", identity, "同一消息出现不同请求身份，保留原记录 / Message identity conflicts with provider request identity")
			return
		}
	}
	if requestID != "" {
		_ = s.writer().QueryRowContext(ctx, `SELECT request_key FROM claude_requests WHERE request_id=? ORDER BY rowid LIMIT 1`, requestID).Scan(&byRequest)
	}
	if byMessage != "" {
		key = byMessage
	}
	if byRequest != "" {
		key = byRequest
	}
	if byRequest != "" && byMessage != "" && byRequest != byMessage {
		// A newly supplied requestId links two previously independent messages.
		// Collapse the duplicate ledger rows, retain all source associations, and
		// make the ambiguity visible; never bill the same request twice.
		for _, q := range []string{
			`INSERT OR IGNORE INTO request_sources SELECT ?,home,path FROM request_sources WHERE request_key=?`,
			`UPDATE request_identities SET request_key=? WHERE request_key=?`,
		} {
			if _, err = s.writer().ExecContext(ctx, q, key, byMessage); err != nil {
				return
			}
		}
		for _, q := range []string{`DELETE FROM usage_events WHERE turn_id=?`, `DELETE FROM request_sources WHERE request_key=?`, `DELETE FROM claude_requests WHERE request_key=?`} {
			if _, err = s.writer().ExecContext(ctx, q, byMessage); err != nil {
				return
			}
		}
		if err = s.AddWarning(ctx, "identity_linked", key, "后补请求身份合并了先前独立的消息 / Late request identity joined previously separate messages"); err != nil {
			return
		}
		corrected = true
	}
	for i := range events {
		events[i].TurnID = key
		events[i].ID = key + ":" + strconv.Itoa(i)
	}
	if _, err = s.writer().ExecContext(ctx, `INSERT INTO request_identities VALUES(?,?) ON CONFLICT(identity) DO UPDATE SET request_key=excluded.request_key`, identity, key); err != nil {
		return
	}
	var raw, oldRequest string
	var oldComplete bool
	err = s.writer().QueryRowContext(ctx, `SELECT request_id,events_json,complete FROM claude_requests WHERE request_key=?`, key).Scan(&oldRequest, &raw, &oldComplete)
	fresh := err == sql.ErrNoRows
	if err != nil && !fresh {
		return 0, false, false, err
	}
	var old []model.UsageEvent
	if !fresh {
		if err = json.Unmarshal([]byte(raw), &old); err != nil {
			return
		}
	}
	_, err = s.writer().ExecContext(ctx, `INSERT OR IGNORE INTO request_sources VALUES(?,?,?)`, key, home, path)
	if err != nil {
		return
	}
	changed := fresh
	if !fresh {
		if oldRequest != "" && requestID != "" && oldRequest != requestID {
			conflict = true
		} else {
			equal := len(old) == len(events)
			classificationConflict := false
			var before, after model.TokenUsage
			for _, e := range old {
				before = before.Add(e.Usage)
			}
			for i, e := range events {
				if len(old) == len(events) {
					// A plain message is also the fallback for missing iteration
					// metadata. Preserve a specific classification when a stale
					// top-level copy arrives after the detailed usage snapshot.
					if genericIterationType(e.IterationType) && !genericIterationType(old[i].IterationType) {
						e.IterationType = old[i].IterationType
						events[i].IterationType = e.IterationType
					} else if !genericIterationType(old[i].IterationType) && old[i].IterationType != e.IterationType {
						classificationConflict = true
					}
				}
				after = after.Add(e.Usage)
				if i >= len(old) || !reflect.DeepEqual(e.Usage, old[i].Usage) || e.Model != old[i].Model || e.ServiceMode.ServiceMode != old[i].ServiceMode.ServiceMode || e.IterationType != old[i].IterationType || (old[i].SessionID == "" && e.SessionID != "") {
					equal = false
				}
			}
			if classificationConflict {
				conflict = true
			} else if !equal {
				// A later final snapshot can replace a partial snapshot; a stale
				// mirror must never downgrade finalized or more complete counters.
				monotonic := after.Input >= before.Input && after.Output >= before.Output && after.CachedInput >= before.CachedInput && after.CacheWriteInput >= before.CacheWriteInput && len(events) >= len(old)
				if monotonic && (!oldComplete || canEnrich(old, events)) {
					changed = true
					corrected = true
				} else if !(before.Input >= after.Input && before.Output >= after.Output && !complete) {
					conflict = true
				}
			}
		}
	}
	if conflict {
		if err = s.AddWarning(ctx, "request_conflict", key, "同一请求出现不一致的身份或用量，保留已确认记录 / Conflicting request; retained confirmed usage"); err != nil {
			return
		}
	}
	if changed {
		if !fresh {
			for i := range events {
				if len(old) > 0 {
					events[i].Timestamp = old[0].Timestamp
					if old[0].SessionID != "" {
						events[i].SessionID = old[0].SessionID
					}
					events[i].ProjectPath = old[0].ProjectPath
				}
			}
			if _, err = s.writer().ExecContext(ctx, `DELETE FROM usage_events WHERE turn_id=?`, key); err != nil {
				return
			}
		}
		for _, e := range events {
			if _, err = s.InsertEvent(ctx, e, path); err != nil {
				return
			}
			if _, err = s.writer().ExecContext(ctx, `UPDATE usage_events SET iteration_type=? WHERE id=?`, e.IterationType, e.ID); err != nil {
				return
			}
		}
		encoded, e := json.Marshal(events)
		if e != nil {
			err = e
			return
		}
		// Metadata enrichment must never turn a finalized request back into a
		// partial one just because a mirror omitted stop_reason.
		_, err = s.writer().ExecContext(ctx, `INSERT INTO claude_requests VALUES(?,?,?,?) ON CONFLICT(request_key) DO UPDATE SET request_id=CASE WHEN excluded.request_id<>'' THEN excluded.request_id ELSE claude_requests.request_id END,events_json=excluded.events_json,complete=excluded.complete`, key, requestID, string(encoded), complete || oldComplete)
		if fresh {
			inserted = int64(len(events))
		}
	} else if complete && !oldComplete && !conflict {
		_, err = s.writer().ExecContext(ctx, `UPDATE claude_requests SET complete=1 WHERE request_key=?`, key)
	}
	if err == nil && !conflict && requestID != "" {
		_, err = s.writer().ExecContext(ctx, `UPDATE claude_requests SET request_id=? WHERE request_key=? AND request_id=''`, requestID, key)
	}
	if err != nil {
		return
	}
	// Do not bump the data revision for unchanged duplicate scans.
	_, err = s.writer().ExecContext(ctx, `UPDATE usage_events SET source_homes=(SELECT json_group_array(home) FROM (SELECT DISTINCT home FROM request_sources WHERE request_key=? ORDER BY home)) WHERE turn_id=? AND source_homes<>(SELECT json_group_array(home) FROM (SELECT DISTINCT home FROM request_sources WHERE request_key=? ORDER BY home))`, key, key, key)
	return
}

func canEnrich(old, next []model.UsageEvent) bool {
	if len(next) > len(old) {
		// Later iteration accounting adds compaction or advisor costs to a
		// finalized top-level message. Require its existing message to survive.
		for _, a := range old {
			found := false
			for _, b := range next {
				if a.Model == b.Model && a.Usage == b.Usage {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		return true
	}
	if len(old) != len(next) {
		return false
	}
	for i, a := range old {
		b := next[i]
		if a.Model != b.Model || (!genericIterationType(a.IterationType) && a.IterationType != b.IterationType) || (a.ServiceMode.ServiceMode != model.ModeUnknown && a.ServiceMode.ServiceMode != b.ServiceMode.ServiceMode) {
			return false
		}
		u, v := a.Usage, b.Usage
		if u.Input != v.Input || u.CachedInput != v.CachedInput || u.CacheWriteInput != v.CacheWriteInput || u.Output != v.Output || u.Total != v.Total || u.CacheWrite5m > v.CacheWrite5m || u.CacheWrite1h > v.CacheWrite1h || u.ReasoningOutput > v.ReasoningOutput {
			return false
		}
	}
	return true
}

func genericIterationType(value string) bool {
	return value == "" || value == "message"
}

func (s *Store) RequestCount(ctx context.Context) (int64, error) {
	var n int64
	err := s.reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM claude_requests`).Scan(&n)
	return n, err
}

func (s *Store) PreserveRebuildReason(ctx context.Context, detail string) error {
	_, err := s.writer().ExecContext(ctx, `INSERT INTO meta VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, historicalRebuildReasonKey, detail)
	return err
}

func (s *Store) LastRequestTime(ctx context.Context) (time.Time, error) {
	var at int64
	err := s.reader().QueryRowContext(ctx, `SELECT COALESCE(MAX(usage_at),0) FROM usage_events`).Scan(&at)
	return time.Unix(at, 0), err
}
