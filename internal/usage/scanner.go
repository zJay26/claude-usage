package usage

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zJay26/claude-usage/internal/model"
	"github.com/zJay26/claude-usage/internal/pricing"
	"github.com/zJay26/claude-usage/internal/sources"
	"github.com/zJay26/claude-usage/internal/store"
)

type Scanner struct {
	Store             *store.Store
	MaxRelevantRecord int
	Now               func() time.Time
	mu                sync.Mutex
	busy              atomic.Bool
}
type ScanResult struct {
	Homes          int      `json:"homes"`
	Files          int64    `json:"files"`
	Records        int64    `json:"records"`
	EventsInserted int64    `json:"events_inserted"`
	Corrections    int64    `json:"classification_corrections"`
	Duplicates     int64    `json:"duplicates"`
	Warnings       int64    `json:"warnings"`
	Unattributed   int64    `json:"unattributed_sessions"`
	StateDatabases []string `json:"state_databases,omitempty"`
	ElapsedMillis  int64    `json:"elapsed_ms"`
}
type HomeDiscovery struct {
	Home, StateDB string
	Sessions      []model.SessionInfo
	Paths         []string
	Fallback      bool
	Warning       string
}
type RebuildRequiredError struct{ Kind, Path, Detail string }

func (e *RebuildRequiredError) Error() string {
	return fmt.Sprintf("%s: %s (%s)", e.Kind, e.Detail, e.Path)
}
func (s *Scanner) Busy() bool { return s.busy.Load() }

func (s *Scanner) Scan(ctx context.Context, homes []string, rebuild bool) (result ScanResult, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.busy.Store(true)
	defer s.busy.Store(false)
	start := time.Now()
	defer func() { result.ElapsedMillis = time.Since(start).Milliseconds() }()
	if s.Store == nil {
		return result, errors.New("scanner store is nil")
	}
	if s.Now == nil {
		s.Now = time.Now
	}
	if s.MaxRelevantRecord <= 0 {
		s.MaxRelevantRecord = 8 << 20
	}
	result.Homes = len(homes)
	var firstErr error
	if rebuild {
		if err = s.Store.ResetHistorical(ctx); err != nil {
			return
		}
	} else if why, required, e := s.Store.HistoricalRebuildReason(ctx); e != nil {
		return result, e
	} else if required && os.Getenv("CLAUDE_USAGE_SCAN_WORKER") == "" {
		// Keep reporting pending approval while independent healthy files can
		// still append usage. Never discard history to recover one broken root.
		firstErr = &RebuildRequiredError{Kind: "historical_rebuild_required", Detail: why}
	}
	seen := map[string]bool{}
	for _, raw := range homes {
		if err = ctx.Err(); err != nil {
			return
		}
		home, e := canonicalPath(raw)
		if e != nil {
			result.Warnings++
			continue
		}
		if sources.IsWSLPath(home) && os.Getenv("CLAUDE_USAGE_SCAN_WORKER") == "" {
			part, e := s.scanWorker(ctx, home)
			sources.RecordResult(home, e, part.Files)
			result.Files += part.Files
			result.Records += part.Records
			result.EventsInserted += part.EventsInserted
			result.Corrections += part.Corrections
			result.Duplicates += part.Duplicates
			result.Warnings += part.Warnings
			if e != nil {
				result.Warnings++
				_ = s.Store.UpdateScanState(ctx, home, "", 0, e.Error())
				if firstErr == nil {
					firstErr = e
				}
			}
			continue
		}
		d := DiscoverHome(ctx, home)
		var sourceErr error
		if d.Warning != "" {
			sourceErr = errors.New(d.Warning)
		}
		var count int64
		for _, file := range d.Paths {
			if seen[pathKey(file)] {
				continue
			}
			seen[pathKey(file)] = true
			count++
			result.Files++
			var part ScanResult
			e = s.Store.Ingest(ctx, func(tx *store.Store) error { return s.scanFile(ctx, tx, home, file, &part) })
			if e != nil {
				sourceErr = e
				result.Warnings++
				var changed *RebuildRequiredError
				if errors.As(e, &changed) {
					_ = s.Store.PreserveRebuildReason(ctx, changed.Error())
					_ = s.Store.AddWarning(ctx, changed.Kind, file, changed.Detail)
					if firstErr == nil {
						firstErr = e
					}
				} else {
					_ = s.Store.AddWarning(ctx, "read_error", file, e.Error())
				}
				continue
			}
			result.Records += part.Records
			result.EventsInserted += part.EventsInserted
			result.Corrections += part.Corrections
			result.Duplicates += part.Duplicates
			result.Warnings += part.Warnings
		}
		if d.Warning != "" {
			result.Warnings++
		}
		sources.RecordResult(home, sourceErr, count)
		warning := ""
		if sourceErr != nil {
			warning = sourceErr.Error()
		}
		if e = s.Store.UpdateScanState(ctx, home, "", count, warning); e != nil && firstErr == nil {
			firstErr = e
		}
	}
	return result, firstErr
}

func (s *Scanner) scanFile(ctx context.Context, tx *store.Store, home, path string, result *ScanResult) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	c, exists, err := tx.GetCursor(ctx, path)
	if err != nil {
		return err
	}
	if exists && c.Offset > info.Size() {
		return &RebuildRequiredError{Kind: "source_truncated", Path: path, Detail: "日志已截断；现有用量保留，需明确重建"}
	}
	if exists && c.Size == info.Size() && c.ModifiedNanos == info.ModTime().UnixNano() {
		return nil
	}
	if exists && c.Offset > 0 {
		h, e := hashFilePrefix(path, c.Offset)
		if e != nil {
			return e
		}
		if h != c.PrefixHash {
			return &RebuildRequiredError{Kind: "source_rewritten", Path: path, Detail: "已读取的日志发生改写；现有用量保留，需明确重建"}
		}
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err = f.Seek(c.Offset, io.SeekStart); err != nil {
		return err
	}
	c.Path, c.ClaudeHome = path, home
	r := bufio.NewReaderSize(io.LimitReader(f, info.Size()-c.Offset), 64<<10)
	for {
		if err = ctx.Err(); err != nil {
			return err
		}
		data, n, complete, e := readProjectedRecord(r, s.MaxRelevantRecord)
		if !complete {
			break
		}
		c.Offset += n
		result.Records++
		if e != nil {
			result.Warnings++
			if err = tx.AddWarning(ctx, "invalid_record", path, fmt.Sprintf("Invalid JSON metadata at byte %d", c.Offset-n)); err != nil {
				return err
			}
			continue
		}
		var record transcriptRecord
		if json.Unmarshal(data, &record) != nil {
			result.Warnings++
			if err = tx.AddWarning(ctx, "invalid_record", path, fmt.Sprintf("Invalid usage fields at byte %d", c.Offset-n)); err != nil {
				return err
			}
			continue
		}
		if err = s.record(ctx, tx, record, home, path, result); err != nil {
			return err
		}
	}
	c.Size, c.ModifiedNanos = info.Size(), info.ModTime().UnixNano()
	c.PrefixHash, err = hashFilePrefix(path, c.Offset)
	if err != nil {
		return err
	}
	return tx.PutCursor(ctx, c)
}

type cacheCreation struct {
	Five int64 `json:"ephemeral_5m_input_tokens"`
	Hour int64 `json:"ephemeral_1h_input_tokens"`
}
type rawUsage struct {
	present bool
	Input   int64          `json:"input_tokens"`
	Output  int64          `json:"output_tokens"`
	Read    int64          `json:"cache_read_input_tokens"`
	Write   int64          `json:"cache_creation_input_tokens"`
	Cache   *cacheCreation `json:"cache_creation"`
	Details struct {
		Thinking int64 `json:"thinking_tokens"`
	} `json:"output_tokens_details"`
	Iterations []rawUsage `json:"iterations"`
	Type       string     `json:"type"`
	Model      string     `json:"model"`
	Speed      string     `json:"speed"`
	Tier       string     `json:"service_tier"`
}

func (u *rawUsage) UnmarshalJSON(data []byte) error {
	type fields rawUsage
	var decoded fields
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var counts struct {
		Input  *int64 `json:"input_tokens"`
		Output *int64 `json:"output_tokens"`
		Read   *int64 `json:"cache_read_input_tokens"`
		Write  *int64 `json:"cache_creation_input_tokens"`
	}
	if err := json.Unmarshal(data, &counts); err != nil {
		return err
	}
	*u = rawUsage(decoded)
	u.present = counts.Input != nil || counts.Output != nil || counts.Read != nil || counts.Write != nil
	return nil
}

type transcriptRecord struct {
	Type       string `json:"type"`
	UUID       string `json:"uuid"`
	Timestamp  string `json:"timestamp"`
	SessionID  string `json:"sessionId"`
	RequestID  string `json:"requestId"`
	CWD        string `json:"cwd"`
	Version    string `json:"version"`
	Entrypoint string `json:"entrypoint"`
	AgentID    string `json:"agentId"`
	Sidechain  bool   `json:"isSidechain"`
	Parent     string `json:"parentSessionId"`
	Fork       string `json:"forkedFromId"`
	Title      string `json:"customTitle"`
	AgentName  string `json:"agentName"`
	Message    struct {
		ID    string    `json:"id"`
		Model string    `json:"model"`
		Usage *rawUsage `json:"usage"`
		Stop  *string   `json:"stop_reason"`
	} `json:"message"`
}

func (u rawUsage) tokens() (model.TokenUsage, error) {
	var out model.TokenUsage
	for _, n := range []int64{u.Input, u.Read, u.Write, u.Output, u.Details.Thinking} {
		if n < 0 {
			return out, errors.New("negative token count")
		}
	}
	if u.Read > math.MaxInt64-u.Input || u.Write > math.MaxInt64-u.Input-u.Read || u.Output > math.MaxInt64-u.Input-u.Read-u.Write {
		return out, errors.New("token overflow")
	}
	out.Input = u.Input + u.Read + u.Write
	out.CachedInput = u.Read
	out.CacheWriteInput = u.Write
	out.Output = u.Output
	out.ReasoningOutput = u.Details.Thinking
	out.Total = out.Input + out.Output
	if u.Cache != nil {
		out.CacheWrite5m = u.Cache.Five
		out.CacheWrite1h = u.Cache.Hour
	}
	if out.CacheWrite5m < 0 || out.CacheWrite1h < 0 || out.CacheWrite5m > out.CacheWriteInput || out.CacheWrite1h > out.CacheWriteInput-out.CacheWrite5m || out.ReasoningOutput > out.Output {
		return out, errors.New("inconsistent token categories")
	}
	return out, nil
}

func (s *Scanner) record(ctx context.Context, tx *store.Store, r transcriptRecord, home, path string, result *ScanResult) error {
	session := r.SessionID
	parent := r.Parent
	agent := "main"
	if strings.Contains(filepath.ToSlash(path), "/subagents/") {
		parent = filepath.Base(filepath.Dir(filepath.Dir(path)))
		id := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), "agent-"), ".jsonl")
		session = parent + "/agent:" + id
		agent = "subagent"
	} else if r.Sidechain {
		agent = "subagent"
		if session != "" && r.AgentID != "" {
			session += "/agent:" + r.AgentID
		}
	}
	if r.Type == "custom-title" || r.Type == "agent-name" {
		return tx.UpsertSession(ctx, model.SessionInfo{SessionID: session, Title: firstNonEmpty(r.Title, r.AgentName), ParentSessionID: parent, AgentType: agent})
	}
	if r.Type != "assistant" || r.Message.Usage == nil || !pricing.IsClaudeModel(r.Message.Model) {
		return nil
	}
	at, err := time.Parse(time.RFC3339Nano, r.Timestamp)
	if err != nil {
		result.Warnings++
		return tx.AddWarning(ctx, "invalid_timestamp", path, "Usage record has no valid RFC3339 timestamp")
	}
	identity := firstNonEmpty(r.Message.ID, r.RequestID, r.UUID)
	if identity == "" {
		result.Warnings++
		return tx.AddWarning(ctx, "missing_identity", path, "用量缺少消息和请求身份，无法安全入账 / Missing request identity")
	}
	if r.Message.ID == "" && r.RequestID == "" {
		result.Warnings++
		if err = tx.AddWarning(ctx, "weak_identity", path, "仅有记录 UUID，跨副本去重受限 / Only record UUID available"); err != nil {
			return err
		}
	}
	key := "claude:" + shortHash(identity)
	u := r.Message.Usage
	parts := u.Iterations
	for _, part := range parts {
		_, invalid := part.tokens()
		if !part.present || invalid != nil {
			result.Warnings++
			if err = tx.AddWarning(ctx, "invalid_iterations", path, "无效迭代用量，仅保留可验证的顶层计数 / Invalid iterations; retained verifiable top-level counters"); err != nil {
				return err
			}
			parts = nil
			break
		}
	}
	if len(parts) == 0 {
		parts = []rawUsage{*u}
	}
	events := make([]model.UsageEvent, 0, len(parts))
	mode := model.ServiceMode{ServiceMode: model.ModeUnknown, ModeSource: "claude_usage", ServiceTier: u.Tier}
	if u.Speed == "fast" {
		mode.ServiceMode = model.ModeFast
	} else if u.Speed == "standard" {
		mode.ServiceMode = model.ModeStandard
	}
	for i, p := range parts {
		if len(parts) == 1 && p.Details.Thinking == 0 {
			p.Details = u.Details
		}
		tokens, e := p.tokens()
		if e != nil {
			result.Warnings++
			return tx.AddWarning(ctx, "invalid_usage", path, e.Error())
		}
		if tokens.Total == 0 {
			continue
		}
		m := firstNonEmpty(p.Model, r.Message.Model)
		if !pricing.IsClaudeModel(m) {
			continue
		}
		partMode := mode
		if p.Speed == "fast" {
			partMode.ServiceMode = model.ModeFast
		} else if p.Speed == "standard" {
			partMode.ServiceMode = model.ModeStandard
		}
		if p.Tier != "" {
			partMode.ServiceTier = p.Tier
		}
		events = append(events, model.UsageEvent{ID: fmt.Sprintf("%s:%d", key, i), TurnID: key, SessionID: session, Timestamp: at, ObservedAt: s.Now(), Model: pricing.NormalizeModel(m), Source: firstNonEmpty(r.Entrypoint, "claude-code"), AgentType: agent, ProjectPath: r.CWD, ClaudeHome: home, Usage: tokens, ServiceMode: partMode, Provenance: model.ProvenanceSessionJSONL, Confidence: model.ConfidenceExact, IterationType: firstNonEmpty(p.Type, "message")})
	}
	if len(events) == 0 {
		return nil
	}
	// Session attribution is optional; request identity is what makes usage
	// safe to account for and deduplicate. Do not invent a parent or session
	// from an arbitrary transcript-copy filename.
	if session == "" {
		result.Warnings++
		if err = tx.AddWarning(ctx, "missing_session", path, "用量缺少会话身份，已按请求入账 / Usage retained without session attribution"); err != nil {
			return err
		}
	}
	if err = tx.UpsertSession(ctx, model.SessionInfo{SessionID: session, ParentSessionID: parent, ForkedFromID: r.Fork, ProjectPath: r.CWD, Model: pricing.NormalizeModel(r.Message.Model), Source: firstNonEmpty(r.Entrypoint, "claude-code"), AgentType: agent, ClaudeHome: home, RolloutPath: path, CLIValue: r.Version, CreatedAt: at, UpdatedAt: at}); err != nil {
		return err
	}
	inserted, corrected, conflict, err := tx.PutRequest(ctx, key, r.RequestID, home, path, events, r.Message.Stop != nil && *r.Message.Stop != "")
	if err != nil {
		return err
	}
	result.EventsInserted += inserted
	if corrected {
		result.Corrections++
	} else if inserted == 0 {
		result.Duplicates++
	}
	if conflict {
		result.Warnings++
	}
	return nil
}

func DiscoverHome(ctx context.Context, home string) HomeDiscovery {
	d := HomeDiscovery{Home: home}
	root := filepath.Join(home, "projects")
	if _, e := os.Stat(root); e != nil {
		if !os.IsNotExist(e) {
			d.Warning = e.Error()
		}
		return d
	}
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		if err != nil {
			d.Warning = err.Error()
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if !entry.IsDir() && (strings.HasSuffix(entry.Name(), ".jsonl") || strings.Contains(entry.Name(), ".jsonl.superseded-")) {
			d.Paths = append(d.Paths, path)
		}
		return nil
	})
	sort.Strings(d.Paths)
	return d
}
func walkRollouts(home string) []string { return DiscoverHome(context.Background(), home).Paths }
func hashFilePrefix(path string, n int64) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.CopyN(h, f, n); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func shortHash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func canonicalPath(p string) (string, error) {
	p = strings.TrimPrefix(p, `\\?\`)
	return filepath.Abs(filepath.Clean(p))
}
func pathKey(p string) string {
	p = filepath.Clean(p)
	if runtime.GOOS == "windows" {
		p = strings.ToLower(p)
	}
	return p
}
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
