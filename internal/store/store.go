package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/zJay26/claude-usage/internal/model"
	_ "modernc.org/sqlite"
)

const (
	schemaVersion               = 1
	historicalRebuildReasonKey  = "historical_rebuild_required"
	pricingAggregatableEventSQL = `e.input_tokens>=0 AND e.cached_input_tokens>=0 AND e.cache_write_input_tokens>=0
		AND e.output_tokens>=0 AND e.reasoning_output_tokens>=0 AND e.total_tokens>=0
		AND e.cached_input_tokens+e.cache_write_input_tokens<=e.input_tokens
		AND e.reasoning_output_tokens<=e.output_tokens
		AND (e.total_tokens=0 OR e.input_tokens+e.output_tokens=0
			OR e.total_tokens=e.input_tokens+e.output_tokens)`
	pricingClassSQL = `CASE
		WHEN e.input_tokens=0 AND e.output_tokens=0 AND e.total_tokens>0 THEN 1
		WHEN e.total_tokens=0 AND e.input_tokens+e.output_tokens>0 THEN 2
		ELSE 0 END`
	actionableWarningSQL = `1=1`
)

type Store struct {
	// db is the single writer used by ingestion and migrations. readDB is a
	// small read-only pool so dashboard queries do not queue behind a scan.
	db       *sql.DB
	readDB   *sql.DB
	path     string
	machine  model.Machine
	tx       *sql.Tx
	location *time.Location
}

type FileCursor struct {
	Path, ClaudeHome, PrefixHash string
	Size, ModifiedNanos, Offset  int64
}

type Status struct {
	Machine            model.Machine `json:"machine"`
	DatabasePath       string        `json:"database_path"`
	AccountingMode     string        `json:"accounting_mode"`
	LastScan           *time.Time    `json:"last_scan,omitempty"`
	EventCount         int64         `json:"event_count"`
	SessionCount       int64         `json:"session_count"`
	WarningCount       int64         `json:"warning_count"`
	AccountingTimezone string        `json:"accounting_timezone"`
	DataRevision       uint64        `json:"data_revision"`
	ClaudeHomes        []HomeStatus  `json:"claude_homes"`
}

type HomeStatus struct {
	Path         string     `json:"path"`
	LastScan     *time.Time `json:"last_scan,omitempty"`
	StateDB      string     `json:"state_db,omitempty"`
	FilesScanned int64      `json:"files_scanned"`
	Warning      string     `json:"warning,omitempty"`
}

type SessionRow struct {
	Modes model.ModeUsage `json:"modes"`
	model.SessionInfo
	Usage      model.TokenUsage `json:"usage"`
	EventCount int64            `json:"event_count"`
	Confidence string           `json:"confidence,omitempty"`
	LastUsage  time.Time        `json:"last_usage,omitempty"`
}

type EventQuery struct {
	model.Filter
	Limit  int
	Offset int
}

type DimensionValues struct {
	Homes    []string `json:"homes"`
	Models   []string `json:"models"`
	Sources  []string `json:"sources"`
	Projects []string `json:"projects"`
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", sqliteURI(path, "_txlock=immediate"))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
	} {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("%s: %w", pragma, err)
		}
	}
	s := &Store{db: db, path: path}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.initTimezone(ctx); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.ensureMachine(ctx); err != nil {
		db.Close()
		return nil, err
	}
	readDB, err := sql.Open("sqlite", sqliteURI(path,
		"mode=ro&_pragma=busy_timeout(5000)&_pragma=query_only(1)"))
	if err != nil {
		db.Close()
		return nil, err
	}
	readDB.SetMaxOpenConns(2)
	readDB.SetMaxIdleConns(2)
	if err := readDB.PingContext(ctx); err != nil {
		readDB.Close()
		db.Close()
		return nil, fmt.Errorf("打开只读查询池: %w", err)
	}
	s.readDB = readDB
	_ = os.Chmod(path, 0o600)
	return s, nil
}

func (s *Store) Close() error {
	var readErr error
	if s.readDB != nil {
		readErr = s.readDB.Close()
	}
	return errors.Join(readErr, s.db.Close())
}
func (s *Store) DBPath() string         { return s.path }
func (s *Store) Machine() model.Machine { return s.machine }

func (s *Store) reader() database {
	if s.tx != nil {
		return s.tx
	}
	if s.readDB != nil {
		return s.readDB
	}
	return s.db
}

func (s *Store) migrate(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			session_id TEXT PRIMARY KEY,
			rollout_path TEXT NOT NULL DEFAULT '',
			claude_home TEXT NOT NULL DEFAULT '',
			title TEXT NOT NULL DEFAULT '',
			project_path TEXT NOT NULL DEFAULT '',
			model TEXT NOT NULL DEFAULT '',
			source TEXT NOT NULL DEFAULT '',
			thread_source TEXT NOT NULL DEFAULT '',
			agent_type TEXT NOT NULL DEFAULT 'main',
			cli_version TEXT NOT NULL DEFAULT '',
			tokens_used INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL DEFAULT 0,
			archived INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS usage_events (
			id TEXT PRIMARY KEY,
			usage_at INTEGER NOT NULL DEFAULT 0,
			local_date TEXT NOT NULL DEFAULT '',
			local_hour TEXT NOT NULL DEFAULT '',
			segment INTEGER NOT NULL DEFAULT 0,
			observed_at INTEGER NOT NULL,
			machine_id TEXT NOT NULL,
			session_id TEXT NOT NULL DEFAULT '',
			turn_id TEXT NOT NULL DEFAULT '',
			model TEXT NOT NULL DEFAULT '',
			source TEXT NOT NULL DEFAULT '',
			agent_type TEXT NOT NULL DEFAULT 'main',
			project_path TEXT NOT NULL DEFAULT '',
			thread_title TEXT NOT NULL DEFAULT '',
			input_tokens INTEGER NOT NULL DEFAULT 0,
			cached_input_tokens INTEGER NOT NULL DEFAULT 0,
			cache_write_input_tokens INTEGER NOT NULL DEFAULT 0,
			cache_write_5m INTEGER NOT NULL DEFAULT 0,
			cache_write_1h INTEGER NOT NULL DEFAULT 0,
			output_tokens INTEGER NOT NULL DEFAULT 0,
			reasoning_output_tokens INTEGER NOT NULL DEFAULT 0,
			total_tokens INTEGER NOT NULL DEFAULT 0,
			provenance TEXT NOT NULL,
			confidence TEXT NOT NULL,
			claude_home TEXT NOT NULL DEFAULT '',
			origin_path TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_events_usage_at ON usage_events(usage_at)`,
		`CREATE INDEX IF NOT EXISTS idx_events_session ON usage_events(session_id)`,
		`CREATE INDEX IF NOT EXISTS idx_events_provenance ON usage_events(provenance)`,
		`CREATE INDEX IF NOT EXISTS idx_events_provenance_local_date ON usage_events(provenance,local_date)`,
		`CREATE INDEX IF NOT EXISTS idx_events_provenance_usage_at ON usage_events(provenance,usage_at)`,
		`CREATE INDEX IF NOT EXISTS idx_events_provenance_session_usage ON usage_events(provenance,session_id,usage_at)`,
		`CREATE TABLE IF NOT EXISTS file_cursors (path TEXT PRIMARY KEY, claude_home TEXT NOT NULL, size INTEGER NOT NULL DEFAULT 0, modified_nanos INTEGER NOT NULL DEFAULT 0, offset INTEGER NOT NULL DEFAULT 0, prefix_hash TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE IF NOT EXISTS warnings (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			created_at INTEGER NOT NULL,
			first_seen INTEGER NOT NULL DEFAULT 0,
			occurrences INTEGER NOT NULL DEFAULT 1,
			kind TEXT NOT NULL,
			path TEXT NOT NULL DEFAULT '',
			detail TEXT NOT NULL,
			fingerprint TEXT NOT NULL UNIQUE
		)`,
		`CREATE TABLE IF NOT EXISTS scan_state (
			claude_home TEXT PRIMARY KEY,
			last_scan INTEGER NOT NULL DEFAULT 0,
			state_db TEXT NOT NULL DEFAULT '',
			files_scanned INTEGER NOT NULL DEFAULT 0,
			warning TEXT NOT NULL DEFAULT ''
		)`,
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for index, stmt := range stmts {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
		if index == 0 {
			var rawVersion string
			versionErr := tx.QueryRowContext(ctx,
				`SELECT value FROM meta WHERE key='schema_version'`).Scan(&rawVersion)
			if versionErr == nil {
				version, parseErr := strconv.Atoi(rawVersion)
				if parseErr != nil {
					return fmt.Errorf("无效 Claude Usage schema_version %q", rawVersion)
				}
				if version > schemaVersion {
					return fmt.Errorf("数据库 schema v%d 来自更高版本；当前程序仅支持到 v%d",
						version, schemaVersion)
				}
			} else if !errors.Is(versionErr, sql.ErrNoRows) {
				return versionErr
			}
		}
	}
	if err := migrateServiceModes(ctx, tx); err != nil {
		return err
	}
	if err := migrateRelationships(ctx, tx); err != nil {
		return err
	}
	if err := migrateLedger(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_warnings_kind_path ON warnings(kind,path)`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES('schema_version',?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, strconv.Itoa(schemaVersion)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES('accounting_mode','jsonl_only')
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ensureMachine(ctx context.Context) error {
	var raw string
	err := s.writer().QueryRowContext(ctx, `SELECT value FROM meta WHERE key='machine'`).Scan(&raw)
	if err == nil {
		if err := json.Unmarshal([]byte(raw), &s.machine); err == nil && s.machine.ID != "" {
			return nil
		}
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "unknown-host"
	}
	s.machine = model.Machine{
		ID:       newUUID(),
		Label:    hostname + " · " + runtime.GOOS,
		Hostname: hostname,
		OS:       runtime.GOOS,
		Arch:     runtime.GOARCH,
	}
	data, _ := json.Marshal(s.machine)
	_, err = s.writer().ExecContext(ctx, `INSERT INTO meta(key,value) VALUES('machine',?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, string(data))
	return err
}

func (s *Store) UpsertSession(ctx context.Context, in model.SessionInfo) error {
	if in.SessionID == "" {
		return nil
	}
	_, err := s.writer().ExecContext(ctx, `INSERT INTO sessions(
		session_id,rollout_path,claude_home,title,project_path,model,source,thread_source,
		agent_type,cli_version,tokens_used,created_at,updated_at,archived)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(session_id) DO UPDATE SET
			rollout_path=CASE WHEN excluded.rollout_path<>'' THEN excluded.rollout_path ELSE sessions.rollout_path END,
			claude_home=CASE WHEN excluded.claude_home<>'' THEN excluded.claude_home ELSE sessions.claude_home END,
			title=CASE WHEN excluded.title<>'' THEN excluded.title ELSE sessions.title END,
			project_path=CASE WHEN excluded.project_path<>'' THEN excluded.project_path ELSE sessions.project_path END,
			model=CASE WHEN excluded.model<>'' THEN excluded.model ELSE sessions.model END,
			source=CASE WHEN excluded.source<>'' THEN excluded.source ELSE sessions.source END,
			thread_source=CASE WHEN excluded.thread_source<>'' THEN excluded.thread_source ELSE sessions.thread_source END,
			agent_type=CASE WHEN excluded.agent_type<>'' THEN excluded.agent_type ELSE sessions.agent_type END,
			cli_version=CASE WHEN excluded.cli_version<>'' THEN excluded.cli_version ELSE sessions.cli_version END,
			tokens_used=CASE WHEN excluded.tokens_used>0 THEN excluded.tokens_used ELSE sessions.tokens_used END,
			created_at=CASE WHEN excluded.created_at>0 THEN excluded.created_at ELSE sessions.created_at END,
			updated_at=CASE WHEN excluded.updated_at>sessions.updated_at THEN excluded.updated_at ELSE sessions.updated_at END,
			archived=excluded.archived
		WHERE (excluded.rollout_path<>'' AND excluded.rollout_path<>sessions.rollout_path)
			OR (excluded.claude_home<>'' AND excluded.claude_home<>sessions.claude_home)
			OR (excluded.title<>'' AND excluded.title<>sessions.title)
			OR (excluded.project_path<>'' AND excluded.project_path<>sessions.project_path)
			OR (excluded.model<>'' AND excluded.model<>sessions.model)
			OR (excluded.source<>'' AND excluded.source<>sessions.source)
			OR (excluded.thread_source<>'' AND excluded.thread_source<>sessions.thread_source)
			OR (excluded.agent_type<>'' AND excluded.agent_type<>sessions.agent_type)
			OR (excluded.cli_version<>'' AND excluded.cli_version<>sessions.cli_version)
			OR (excluded.tokens_used>0 AND excluded.tokens_used<>sessions.tokens_used)
			OR (excluded.created_at>0 AND excluded.created_at<>sessions.created_at)
			OR excluded.updated_at>sessions.updated_at
			OR excluded.archived<>sessions.archived`,
		in.SessionID, in.RolloutPath, in.ClaudeHome, in.Title, in.ProjectPath, in.Model,
		in.Source, in.ThreadSource, defaultAgent(in.AgentType), in.CLIValue, in.TokensUsed,
		unixOrZero(in.CreatedAt), unixOrZero(in.UpdatedAt), boolInt(in.Archived))
	if err != nil {
		return err
	}
	return s.PutRelationship(ctx, in.SessionID, in.ParentSessionID, in.ForkedFromID)
}

func (s *Store) Session(ctx context.Context, id string) (model.SessionInfo, error) {
	var out model.SessionInfo
	var created, updated int64
	var archived int
	err := s.writer().QueryRowContext(ctx, `SELECT session_id,rollout_path,claude_home,title,
		project_path,model,source,thread_source,agent_type,cli_version,tokens_used,
		created_at,updated_at,archived FROM sessions WHERE session_id=?`, id).Scan(
		&out.SessionID, &out.RolloutPath, &out.ClaudeHome, &out.Title, &out.ProjectPath,
		&out.Model, &out.Source, &out.ThreadSource, &out.AgentType, &out.CLIValue,
		&out.TokensUsed, &created, &updated, &archived)
	out.CreatedAt = timeFromUnix(created)
	out.UpdatedAt = timeFromUnix(updated)
	out.Archived = archived != 0
	return out, err
}

func (s *Store) InsertEvent(ctx context.Context, event model.UsageEvent, originPath string) (bool, error) {
	if event.ID == "" {
		return false, errors.New("usage event id is empty")
	}
	if event.ObservedAt.IsZero() {
		event.ObservedAt = time.Now()
	}
	if event.MachineID == "" {
		event.MachineID = s.machine.ID
	}
	event.ServiceMode = event.ServiceMode.Normalized()
	event.Usage = event.Usage.Compatible()
	if !event.Timestamp.IsZero() {
		local := event.Timestamp.In(s.Location())
		if event.LocalDate == "" {
			event.LocalDate = local.Format("2006-01-02")
		}
		if event.LocalHour == "" {
			event.LocalHour = local.Format("2006-01-02T15")
		}
	}
	result, err := s.writer().ExecContext(ctx, `INSERT OR IGNORE INTO usage_events(
		id,usage_at,local_date,local_hour,segment,observed_at,machine_id,session_id,turn_id,model,source,agent_type,
		project_path,thread_title,input_tokens,cached_input_tokens,cache_write_input_tokens,cache_write_5m,cache_write_1h,
		output_tokens,reasoning_output_tokens,total_tokens,provenance,confidence,claude_home,origin_path,service_mode,service_tier,mode_source,hour_start)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		event.ID, unixOrZero(event.Timestamp), event.LocalDate, event.LocalHour, event.Segment,
		unixOrZero(event.ObservedAt), event.MachineID,
		event.SessionID, event.TurnID, event.Model, event.Source, defaultAgent(event.AgentType),
		event.ProjectPath, event.ThreadTitle, event.Usage.Input, event.Usage.CachedInput,
		event.Usage.CacheWriteInput, event.Usage.CacheWrite5m, event.Usage.CacheWrite1h, event.Usage.Output, event.Usage.ReasoningOutput,
		event.Usage.Total, event.Provenance, event.Confidence, event.ClaudeHome, originPath, event.ServiceMode.ServiceMode, event.ServiceTier, event.ModeSource, unixOrZero(hourStart(event.Timestamp.In(s.Location()))))
	if err != nil {
		return false, err
	}
	n, _ := result.RowsAffected()

	return n > 0, nil
}

func (s *Store) GetCursor(ctx context.Context, path string) (FileCursor, bool, error) {
	var out FileCursor
	err := s.writer().QueryRowContext(ctx, "SELECT path,claude_home,size,modified_nanos,offset,prefix_hash FROM file_cursors WHERE path=?", path).Scan(&out.Path, &out.ClaudeHome, &out.Size, &out.ModifiedNanos, &out.Offset, &out.PrefixHash)
	if errors.Is(err, sql.ErrNoRows) {
		return FileCursor{Path: path}, false, nil
	}
	return out, err == nil, err
}
func (s *Store) PutCursor(ctx context.Context, in FileCursor) error {
	_, err := s.writer().ExecContext(ctx, "INSERT INTO file_cursors(path,claude_home,size,modified_nanos,offset,prefix_hash) VALUES(?,?,?,?,?,?) ON CONFLICT(path) DO UPDATE SET claude_home=excluded.claude_home,size=excluded.size,modified_nanos=excluded.modified_nanos,offset=excluded.offset,prefix_hash=excluded.prefix_hash", in.Path, in.ClaudeHome, in.Size, in.ModifiedNanos, in.Offset, in.PrefixHash)
	return err
}

func (s *Store) ResetHistorical(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, stmt := range []string{
		`DELETE FROM usage_events`,
		`DELETE FROM request_sources`,
		`DELETE FROM request_identities`,
		`DELETE FROM claude_requests`,
		`DELETE FROM file_cursors`,
		`DELETE FROM sessions`,
		`DELETE FROM scan_state`,
		`DELETE FROM warnings`,
		`DELETE FROM meta WHERE key IN ('historical_rebuild_required')`,
	} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

// HistoricalRebuildReason reports a pending parser migration without changing
// or discarding the existing derived ledger.
func (s *Store) HistoricalRebuildReason(ctx context.Context) (string, bool, error) {
	var reason string
	err := s.writer().QueryRowContext(ctx, `SELECT value FROM meta WHERE key=?`, historicalRebuildReasonKey).Scan(&reason)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return reason, true, nil
}

func (s *Store) AddWarning(ctx context.Context, kind, path, detail string) error {
	fp := hashString(kind + "\x00" + path + "\x00" + detail)
	now := time.Now().Unix()
	_, err := s.writer().ExecContext(ctx, `INSERT INTO warnings(
		created_at,first_seen,occurrences,kind,path,detail,fingerprint) VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(kind,path) DO UPDATE SET created_at=excluded.created_at,
		detail=excluded.detail,fingerprint=excluded.fingerprint,
		occurrences=warnings.occurrences+1`,
		now, now, 1, kind, path, detail, fp)
	return err
}

func (s *Store) Warnings(ctx context.Context, limit int) ([]model.Warning, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.reader().QueryContext(ctx, `SELECT id,created_at,first_seen,occurrences,kind,path,detail
		FROM warnings WHERE `+actionableWarningSQL+` ORDER BY created_at DESC,id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Warning
	for rows.Next() {
		var item model.Warning
		var created, first int64
		if err := rows.Scan(&item.ID, &created, &first, &item.Occurrences,
			&item.Kind, &item.Path, &item.Detail); err != nil {
			return nil, err
		}
		item.CreatedAt = timeFromUnix(created)
		item.FirstSeen = timeFromUnix(first)
		out = append(out, item)
	}
	return out, rows.Err()
}

// ClearResolvedFileWarnings removes file-change warnings after a later scan
// has successfully reconciled the same path. It never clears parser, timestamp,
// or malformed-record warnings that may still describe an accounting gap.
func (s *Store) ClearResolvedFileWarnings(ctx context.Context, path string) error {
	_, err := s.writer().ExecContext(ctx, `DELETE FROM warnings
		WHERE path=? AND kind IN ('rollout_rewritten','rollout_truncated')`, path)
	return err
}

func (s *Store) UpdateScanState(ctx context.Context, home, stateDB string, files int64, warning string) error {
	_, err := s.writer().ExecContext(ctx, `INSERT INTO scan_state(
		claude_home,last_scan,state_db,files_scanned,warning) VALUES(?,?,?,?,?)
		ON CONFLICT(claude_home) DO UPDATE SET last_scan=excluded.last_scan,
		state_db=excluded.state_db,files_scanned=excluded.files_scanned,warning=excluded.warning`,
		home, time.Now().Unix(), stateDB, files, warning)
	return err
}

func (s *Store) Summary(ctx context.Context, filter model.Filter) (model.Summary, error) {
	where, args := canonicalWhere(filter, "e")
	if requiresAttribution(filter) {
		where, args = attributionWhere(filter, "e", false)
	}
	query := `SELECT COALESCE(SUM(e.input_tokens),0),COALESCE(SUM(e.cached_input_tokens),0),
		COALESCE(SUM(e.cache_write_input_tokens),0),COALESCE(SUM(e.cache_write_5m),0),COALESCE(SUM(e.cache_write_1h),0),COALESCE(SUM(e.output_tokens),0),
		COALESCE(SUM(e.reasoning_output_tokens),0),COALESCE(SUM(e.total_tokens),0),
		COUNT(*),COUNT(DISTINCT NULLIF(e.session_id,'')),COALESCE(MIN(NULLIF(e.usage_at,0)),0),
		COALESCE(MAX(e.usage_at),0),` + modeUsageSQL + ` FROM usage_events e WHERE ` + where
	var out model.Summary
	var first, last int64
	err := s.reader().QueryRowContext(ctx, query, args...).Scan(append([]any{
		&out.Usage.Input, &out.Usage.CachedInput, &out.Usage.CacheWriteInput, &out.Usage.CacheWrite5m, &out.Usage.CacheWrite1h,
		&out.Usage.Output, &out.Usage.ReasoningOutput, &out.Usage.Total,
		&out.EventCount, &out.SessionCount, &first, &last}, modeUsageDest(&out.Modes)...)...)
	if err != nil {
		return out, err
	}
	out.FirstEvent = timeFromUnix(first)
	out.LastEvent = timeFromUnix(last)
	out.GrandTotal = out.Usage.Total
	out.Modes.Complete(out.Usage)
	var warnings int64
	_ = s.reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM warnings WHERE `+actionableWarningSQL).Scan(&warnings)
	out.CoverageIncomplete = warnings > 0
	return out, nil
}

func (s *Store) Timeseries(ctx context.Context, filter model.Filter, bucket string) ([]model.Point, error) {
	where, args := canonicalWhere(filter, "e")
	if requiresAttribution(filter) {
		where, args = attributionWhere(filter, "e", false)
	}
	bucketColumn := "e.local_date"
	if bucket == "hour" {
		bucketColumn = "e.hour_start"
	}
	rows, err := s.reader().QueryContext(ctx, `SELECT `+bucketColumn+`,
		COALESCE(SUM(e.input_tokens),0),COALESCE(SUM(e.cached_input_tokens),0),
		COALESCE(SUM(e.cache_write_input_tokens),0),COALESCE(SUM(e.cache_write_5m),0),COALESCE(SUM(e.cache_write_1h),0),COALESCE(SUM(e.output_tokens),0),
		COALESCE(SUM(e.reasoning_output_tokens),0),COALESCE(SUM(e.total_tokens),0),`+modeUsageSQL+`
		FROM usage_events e WHERE `+where+` AND e.usage_at>0 AND `+bucketColumn+`<>''
		GROUP BY `+bucketColumn+` ORDER BY `+bucketColumn, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Point
	for rows.Next() {
		var key string
		var usage model.TokenUsage
		var modes model.ModeUsage
		if err := rows.Scan(append([]any{&key, &usage.Input, &usage.CachedInput,
			&usage.CacheWriteInput, &usage.CacheWrite5m, &usage.CacheWrite1h, &usage.Output, &usage.ReasoningOutput,
			&usage.Total}, modeUsageDest(&modes)...)...); err != nil {
			return nil, err
		}
		layout := "2006-01-02"
		if bucket == "hour" {
			layout = "2006-01-02T15"
		}
		parsed, _ := time.ParseInLocation(layout, key, time.UTC)
		if bucket == "hour" {
			unix, _ := strconv.ParseInt(key, 10, 64)
			parsed = time.Unix(unix, 0).UTC()
			key = parsed.In(s.Location()).Format("2006-01-02T15")
		}
		modes.Complete(usage)
		out = append(out, model.Point{Time: parsed, Date: key, Usage: usage, Modes: modes})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) Breakdown(ctx context.Context, filter model.Filter, dimension string, limit int) ([]model.BreakdownItem, error) {
	columns := map[string]string{
		"model":      "e.model",
		"source":     "e.source",
		"agent_type": "e.agent_type",
		"project":    "e.project_path",
		"thread":     "e.thread_title",
		"confidence": "e.confidence",
		"provenance": "e.provenance",
	}
	column, ok := columns[dimension]
	if !ok {
		return nil, fmt.Errorf("不支持的分解维度 %q", dimension)
	}
	if limit <= 0 || limit > 500 {
		limit = 25
	}
	where, args := canonicalWithFallbackWhere(filter, "e")
	if requiresAttribution(filter) || dimension == "project" || dimension == "thread" {
		where, args = attributionWhere(filter, "e", true)
	}
	args = append(args, limit)
	query := fmt.Sprintf(`SELECT COALESCE(NULLIF(%s,''),'未知') AS item,
		COALESCE(SUM(e.input_tokens),0),COALESCE(SUM(e.cached_input_tokens),0),
		COALESCE(SUM(e.cache_write_input_tokens),0),COALESCE(SUM(e.cache_write_5m),0),COALESCE(SUM(e.cache_write_1h),0),COALESCE(SUM(e.output_tokens),0),
		COALESCE(SUM(e.reasoning_output_tokens),0),COALESCE(SUM(e.total_tokens),0),
		COUNT(*),COUNT(DISTINCT NULLIF(e.session_id,'')),`+modeUsageSQL+`
		FROM usage_events e WHERE %s GROUP BY item ORDER BY SUM(e.total_tokens) DESC LIMIT ?`, column, where)
	rows, err := s.reader().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.BreakdownItem
	for rows.Next() {
		var item model.BreakdownItem
		if err := rows.Scan(append([]any{&item.Key, &item.Usage.Input, &item.Usage.CachedInput,
			&item.Usage.CacheWriteInput, &item.Usage.CacheWrite5m, &item.Usage.CacheWrite1h, &item.Usage.Output, &item.Usage.ReasoningOutput,
			&item.Usage.Total, &item.Events, &item.Sessions}, modeUsageDest(&item.Modes)...)...); err != nil {
			return nil, err
		}
		item.Modes.Complete(item.Usage)
		out = append(out, item)
	}
	return out, rows.Err()
}

// Dimensions returns only distinct filter values. The dashboard uses this
// lightweight query lazily instead of running three full token breakdowns at
// startup merely to populate select controls.
func (s *Store) Dimensions(ctx context.Context) (DimensionValues, error) {
	rows, err := s.reader().QueryContext(ctx, `
		SELECT 'model' AS kind,model AS value FROM usage_events
			WHERE provenance=? AND model<>'' GROUP BY model
		UNION ALL
		SELECT 'source' AS kind,source AS value FROM usage_events
			WHERE provenance=? AND source<>'' GROUP BY source
		UNION ALL
		SELECT 'project' AS kind,project_path AS value FROM usage_events
			WHERE provenance=? AND project_path<>'' GROUP BY project_path
		UNION ALL SELECT 'home' AS kind,home AS value FROM request_sources GROUP BY home
		ORDER BY kind,value COLLATE NOCASE`,
		model.ProvenanceSessionJSONL, model.ProvenanceSessionJSONL, model.ProvenanceSessionJSONL)
	if err != nil {
		return DimensionValues{}, err
	}
	defer rows.Close()
	var out DimensionValues
	for rows.Next() {
		var kind, value string
		if err := rows.Scan(&kind, &value); err != nil {
			return DimensionValues{}, err
		}
		switch kind {
		case "home":
			out.Homes = append(out.Homes, value)
		case "model":
			if len(out.Models) < 500 {
				out.Models = append(out.Models, value)
			}
		case "source":
			if len(out.Sources) < 500 {
				out.Sources = append(out.Sources, value)
			}
		case "project":
			if len(out.Projects) < 500 {
				out.Projects = append(out.Projects, value)
			}
		}
	}
	return out, rows.Err()
}

func (s *Store) Sessions(ctx context.Context, filter model.Filter, limit, offset int) ([]SessionRow, error) {
	if limit != -1 && (limit <= 0 || limit > 500) {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	where, args := attributionWhere(filter, "e", true)
	query := sessionRowsSQL(where)
	baseArgs := append([]any{}, args...)
	args = append(args, limit, offset)
	args = append(args, baseArgs...)
	rows, err := s.reader().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SessionRow
	for rows.Next() {
		var item SessionRow
		var created, updated, last int64
		var archived int
		if err := rows.Scan(append([]any{&item.SessionID, &item.RolloutPath, &item.ClaudeHome,
			&item.Title, &item.ProjectPath, &item.Model, &item.Source,
			&item.ThreadSource, &item.AgentType, &item.CLIValue, &item.TokensUsed,
			&created, &updated, &archived, &item.Usage.Input, &item.Usage.CachedInput,
			&item.Usage.CacheWriteInput, &item.Usage.CacheWrite5m, &item.Usage.CacheWrite1h, &item.Usage.Output, &item.Usage.ReasoningOutput,
			&item.Usage.Total, &item.EventCount, &item.Confidence, &last}, modeUsageDest(&item.Modes)...)...); err != nil {
			return nil, err
		}
		item.Modes.Complete(item.Usage)
		item.CreatedAt = timeFromUnix(created)
		item.UpdatedAt = timeFromUnix(updated)
		item.LastUsage = timeFromUnix(last)
		item.Archived = archived != 0
		out = append(out, item)
	}
	return out, rows.Err()
}

// WalkSessionPricingAggregates emits the pricing view for a bounded set of
// sessions in one query. It preserves model boundaries so sessions that switch
// models receive the same estimate as their underlying events.
func (s *Store) WalkSessionPricingAggregates(ctx context.Context, filter model.Filter, ids []string, fn func(model.UsageEvent) error) error {
	selected := map[string]bool{}
	for _, id := range ids {
		selected[id] = true
	}
	if len(ids) == 0 {
		return nil
	}
	return s.WalkPricingEvents(ctx, filter, func(e model.UsageEvent) error {
		if selected[e.SessionID] {
			return fn(e)
		}
		return nil
	})
}

func (s *Store) Events(ctx context.Context, query EventQuery) ([]model.UsageEvent, error) {
	return s.queryEvents(ctx, query, false)
}

// WalkEvents traverses the canonical export view in one stable SQLite read
// snapshot. A total ordering avoids duplicate or skipped rows when timestamps
// are equal, while avoiding the repeated work of OFFSET pagination.
func (s *Store) WalkEvents(ctx context.Context, filter model.Filter, fn func(model.UsageEvent) error) error {
	where, args := canonicalWithFallbackWhere(filter, "e")
	rows, err := s.reader().QueryContext(ctx, `SELECT id,usage_at,local_date,local_hour,observed_at,machine_id,
		session_id,turn_id,model,source,agent_type,project_path,thread_title,
		input_tokens,cached_input_tokens,cache_write_input_tokens,cache_write_5m,cache_write_1h,output_tokens,
		reasoning_output_tokens,total_tokens,provenance,confidence,claude_home,service_mode,service_tier,mode_source,source_homes,iteration_type
		FROM usage_events e WHERE `+where+` ORDER BY e.usage_at DESC,e.observed_at DESC,e.id DESC`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		item, scanErr := scanUsageEvent(rows)
		if scanErr != nil {
			return scanErr
		}
		if err := fn(item); err != nil {
			return err
		}
	}
	return rows.Err()
}

// PricingEvents applies the same canonical source and attribution rules as the
// aggregate APIs, while still exposing normalized events to the estimator.
func (s *Store) PricingEvents(ctx context.Context, query EventQuery) ([]model.UsageEvent, error) {
	return s.queryEvents(ctx, query, true)
}

// WalkPricingEvents streams the canonical pricing view through one SQL query.
// Cost estimation does not require event ordering, so this avoids repeatedly
// sorting and rescanning the same canonical view for OFFSET pages.
func (s *Store) WalkPricingEvents(ctx context.Context, filter model.Filter, fn func(model.UsageEvent) error) error {
	where, args := canonicalWithFallbackWhere(filter, "e")
	if requiresAttribution(filter) {
		where, args = attributionWhere(filter, "e", true)
	}
	rows, err := s.reader().QueryContext(ctx, `SELECT id,usage_at,local_date,local_hour,observed_at,machine_id,
		session_id,turn_id,model,source,agent_type,project_path,thread_title,
		input_tokens,cached_input_tokens,cache_write_input_tokens,cache_write_5m,cache_write_1h,output_tokens,
		reasoning_output_tokens,total_tokens,provenance,confidence,claude_home,service_mode,service_tier,mode_source,source_homes,iteration_type
		FROM usage_events e WHERE `+where, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		item, scanErr := scanUsageEvent(rows)
		if scanErr != nil {
			return scanErr
		}
		if err := fn(item); err != nil {
			return err
		}
	}
	return rows.Err()
}

// WalkPricingAggregates emits one valid aggregate per local day and model.
// Fast rows retain event boundaries so fractional multipliers round identically.
// Rows that are unsafe to aggregate remain individual so pricing diagnostics
// are byte-for-byte equivalent to evaluating raw events.
func (s *Store) WalkPricingAggregates(ctx context.Context, filter model.Filter, fn func(model.UsageEvent) error) error {
	return s.WalkPricingEvents(ctx, filter, fn)
}

func (s *Store) queryEvents(ctx context.Context, query EventQuery, pricingView bool) ([]model.UsageEvent, error) {
	if query.Limit <= 0 || query.Limit > 10000 {
		query.Limit = 1000
	}
	if query.Offset < 0 {
		query.Offset = 0
	}
	where, args := canonicalWithFallbackWhere(query.Filter, "e")
	if pricingView && requiresAttribution(query.Filter) {
		where, args = attributionWhere(query.Filter, "e", true)
	}
	args = append(args, query.Limit, query.Offset)
	rows, err := s.reader().QueryContext(ctx, `SELECT id,usage_at,local_date,local_hour,observed_at,machine_id,
		session_id,turn_id,model,source,agent_type,project_path,thread_title,
		input_tokens,cached_input_tokens,cache_write_input_tokens,cache_write_5m,cache_write_1h,output_tokens,
		reasoning_output_tokens,total_tokens,provenance,confidence,claude_home,service_mode,service_tier,mode_source,source_homes,iteration_type
		FROM usage_events e WHERE `+where+` ORDER BY e.usage_at DESC,e.observed_at DESC,e.id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.UsageEvent
	for rows.Next() {
		item, scanErr := scanUsageEvent(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

type usageEventScanner interface {
	Scan(dest ...any) error
}

func scanUsageEvent(scanner usageEventScanner) (model.UsageEvent, error) {
	var item model.UsageEvent
	var usageAt, observedAt int64
	var sourceJSON string
	if err := scanner.Scan(&item.ID, &usageAt, &item.LocalDate, &item.LocalHour, &observedAt, &item.MachineID,
		&item.SessionID, &item.TurnID, &item.Model, &item.Source, &item.AgentType,
		&item.ProjectPath, &item.ThreadTitle, &item.Usage.Input,
		&item.Usage.CachedInput, &item.Usage.CacheWriteInput, &item.Usage.CacheWrite5m, &item.Usage.CacheWrite1h, &item.Usage.Output,
		&item.Usage.ReasoningOutput, &item.Usage.Total, &item.Provenance,
		&item.Confidence, &item.ClaudeHome, &item.ServiceMode.ServiceMode, &item.ServiceTier, &item.ModeSource, &sourceJSON, &item.IterationType); err != nil {
		return model.UsageEvent{}, err
	}
	_ = json.Unmarshal([]byte(sourceJSON), &item.Sources)
	item.ServiceMode = item.ServiceMode.Normalized()
	item.Timestamp = timeFromUnix(usageAt)
	item.ObservedAt = timeFromUnix(observedAt)
	return item, nil
}

func (s *Store) Status(ctx context.Context) (Status, error) {
	revision, err := s.DataRevision(ctx)
	if err != nil {
		return Status{}, err
	}
	out := Status{Machine: s.machine, DatabasePath: s.path, AccountingMode: "jsonl_only", DataRevision: revision, AccountingTimezone: s.Location().String()}
	reader := s.reader()
	if err := reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_events`).Scan(&out.EventCount); err != nil {
		return out, err
	}
	if err := reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions`).Scan(&out.SessionCount); err != nil {
		return out, err
	}
	if err := reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM warnings WHERE `+actionableWarningSQL).Scan(&out.WarningCount); err != nil {
		return out, err
	}
	var scan int64
	_ = reader.QueryRowContext(ctx, `SELECT COALESCE(MAX(last_scan),0) FROM scan_state`).Scan(&scan)
	if scan > 0 {
		value := timeFromUnix(scan)
		out.LastScan = &value
	}
	rows, err := reader.QueryContext(ctx, `SELECT claude_home,last_scan,state_db,files_scanned,warning
		FROM scan_state ORDER BY claude_home`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var item HomeStatus
		var last int64
		if err := rows.Scan(&item.Path, &last, &item.StateDB, &item.FilesScanned, &item.Warning); err != nil {
			return out, err
		}
		if last > 0 {
			value := timeFromUnix(last)
			item.LastScan = &value
		}
		out.ClaudeHomes = append(out.ClaudeHomes, item)
	}
	return out, rows.Err()
}

func (s *Store) Vacuum(ctx context.Context) error {
	_, err := s.writer().ExecContext(ctx, `PRAGMA optimize`)
	return err
}

func canonicalWhere(filter model.Filter, alias string) (string, []any) {
	if alias == "" {
		alias = "e"
	}
	parts := []string{alias + ".provenance='session_jsonl'"}
	var args []any
	if len(filter.Homes) > 0 {
		placeholders := []string{}
		for _, home := range filter.Homes {
			placeholders = append(placeholders, "?")
			args = append(args, home)
		}
		parts = append(parts, "EXISTS(SELECT 1 FROM request_sources rs WHERE rs.request_key="+alias+".turn_id AND rs.home IN ("+strings.Join(placeholders, ",")+"))")
	}
	if !filter.Since.IsZero() || !filter.Until.IsZero() {
		// Zero is the sentinel for missing timestamps, not usage at the Unix
		// epoch. Such records cannot be attributed to a requested time range.
		parts = append(parts, alias+".usage_at<>0")
	}
	if filter.SinceDate != "" {
		parts = append(parts, alias+".local_date>=?")
		args = append(args, filter.SinceDate)
	} else if !filter.Since.IsZero() {
		parts = append(parts, alias+".usage_at>=?")
		args = append(args, filter.Since.Unix())
	}
	if filter.UntilDate != "" {
		parts = append(parts, alias+".local_date<?")
		args = append(args, filter.UntilDate)
	} else if !filter.Until.IsZero() {
		parts = append(parts, alias+".usage_at<?")
		args = append(args, filter.Until.Unix())
	}
	for column, value := range map[string]string{
		"model": filter.Model, "source": filter.Source, "agent_type": filter.AgentType,
		"project_path": filter.Project, "session_id": filter.SessionID,
		"confidence": filter.Confidence,
	} {
		if value == "" {
			continue
		}
		parts = append(parts, alias+"."+column+"=?")
		args = append(args, value)
	}
	switch filter.Mode {
	case "regular":
		parts = append(parts, alias+".service_mode<>'fast'")
	case "fast":
		parts = append(parts, alias+".service_mode='fast'")
	case "unknown":
		parts = append(parts, alias+".service_mode='unknown'")
	}
	if filter.Search != "" {
		predicate, values := sessionSearchSQL(alias, filter.Search)
		parts = append(parts, predicate)
		args = append(args, values...)
	}
	return strings.Join(parts, " AND "), args
}

func canonicalWithFallbackWhere(filter model.Filter, alias string) (string, []any) {
	return canonicalWhere(filter, alias)
}

func requiresAttribution(filter model.Filter) bool {
	return filter.Project != "" || filter.SessionID != ""
}

func attributionWhere(filter model.Filter, alias string, includeFallback bool) (string, []any) {
	return canonicalWhere(filter, alias)
}

// ReadStateThreads reads only metadata from a Claude state database. It probes
// columns first, so older/newer internal schemas fall back without a crash.
func unixOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

func sqliteURI(path, rawQuery string) string {
	absolute, err := filepath.Abs(path)
	if err == nil {
		path = absolute
	}
	normalized := filepath.ToSlash(path)
	if runtime.GOOS == "windows" && strings.HasPrefix(normalized, "//") {
		parts := strings.SplitN(strings.TrimPrefix(normalized, "//"), "/", 2)
		value := &url.URL{Scheme: "file", Host: parts[0], RawQuery: rawQuery}
		if len(parts) == 2 {
			value.Path = "/" + parts[1]
		}
		return value.String()
	}
	if runtime.GOOS == "windows" && len(normalized) >= 2 && normalized[1] == ':' {
		normalized = "/" + normalized
	}
	value := &url.URL{Scheme: "file", Path: normalized, RawQuery: rawQuery}
	return value.String()
}

func timeFromUnix(value int64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	return time.Unix(value, 0).UTC()
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func defaultAgent(value string) string {
	if value == "" {
		return "main"
	}
	return value
}

func newUUID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("%d-%d", time.Now().UnixNano(), os.Getpid())
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16])
}

func hashString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
