package usage

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/zJay26/claude-usage/internal/model"
	"github.com/zJay26/claude-usage/internal/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func openTest(t *testing.T) (*Scanner, *store.Store, string) {
	t.Helper()
	root := t.TempDir()
	st, e := store.Open(filepath.Join(root, "state", "usage.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { st.Close() })
	return &Scanner{Store: st}, st, filepath.Join(root, "home")
}
func appendRecords(t *testing.T, home, name string, records ...any) string {
	t.Helper()
	p := filepath.Join(home, "projects", "project", name)
	if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		t.Fatal(e)
	}
	f, e := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	for _, r := range records {
		if e = json.NewEncoder(f).Encode(r); e != nil {
			t.Fatal(e)
		}
	}
	return p
}
func message(id string, usage map[string]any) map[string]any {
	return map[string]any{"type": "assistant", "sessionId": "session", "uuid": "record-" + id, "requestId": "request-" + id, "timestamp": "2026-09-22T23:59:01Z", "cwd": "/fixture/project", "entrypoint": "cli", "message": map[string]any{"id": "msg-" + id, "model": "claude-opus-5-5", "usage": usage}}
}
func counters(input, read, write, output int64) map[string]any {
	return map[string]any{"input_tokens": input, "cache_read_input_tokens": read, "cache_creation_input_tokens": write, "output_tokens": output, "cache_creation": map[string]any{"ephemeral_5m_input_tokens": write}, "speed": "standard"}
}
func scan(t *testing.T, s *Scanner, homes ...string) ScanResult {
	t.Helper()
	r, e := s.Scan(context.Background(), homes, false)
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func total(t *testing.T, st *store.Store) model.Summary {
	t.Helper()
	v, e := st.Summary(context.Background(), model.Filter{})
	if e != nil {
		t.Fatal(e)
	}
	return v
}

func TestGlobalDedupeCacheAccountingAndSourceUnion(t *testing.T) {
	s, st, home := openTest(t)
	other := filepath.Join(t.TempDir(), "mirror")
	u := counters(2, 100, 40, 20)
	u["output_tokens_details"] = map[string]any{"thinking_tokens": 8}
	r := message("a", u)
	appendRecords(t, home, "a.jsonl", r, r)
	delete(r, "requestId")
	appendRecords(t, other, "copy.jsonl", r)
	got := scan(t, s, home, other)
	if got.EventsInserted != 1 || got.Duplicates != 2 {
		t.Fatalf("dedupe: %+v", got)
	}
	v := total(t, st)
	if v.Usage.Total != 162 || v.Usage.Input != 142 || v.Usage.CacheWrite5m != 40 || v.Usage.ReasoningOutput != 8 {
		t.Fatalf("usage: %+v", v.Usage)
	}
	for _, roots := range [][]string{{home}, {other}, {home, other}} {
		v, e := st.Summary(context.Background(), model.Filter{Homes: roots})
		if e != nil || v.Usage.Total != 162 {
			t.Fatalf("union: %+v %v", v, e)
		}
	}
	events, e := st.Events(context.Background(), store.EventQuery{})
	if e != nil || len(events) != 1 || len(events[0].Sources) != 2 {
		t.Fatalf("sources: %+v %v", events, e)
	}
	revision := st.Revision()
	if second := scan(t, s, home, other); second.EventsInserted != 0 {
		t.Fatal(second)
	}
	if st.Revision() != revision {
		t.Fatal("unchanged scan changed revision")
	}
}

func TestIterationsReplaceTopLevelAndPriceByModel(t *testing.T) {
	s, st, home := openTest(t)
	u := counters(9000, 0, 0, 9000)
	a := counters(10, 0, 3, 2)
	a["type"] = "compaction"
	b := counters(7, 10, 0, 4)
	b["type"] = "advisor_message"
	b["model"] = "claude-sonnet-5"
	u["iterations"] = []any{a, b}
	appendRecords(t, home, "a.jsonl", message("iterations", u))
	scan(t, s, home)
	v := total(t, st)
	if v.Usage.Total != 36 || v.EventCount != 2 {
		t.Fatal(v)
	}
	rows, e := st.Breakdown(context.Background(), model.Filter{}, "model", 20)
	if e != nil || len(rows) != 2 {
		t.Fatalf("models: %+v %v", rows, e)
	}
}

func TestPartialUsageUpdatedWithoutCountingRequestTwice(t *testing.T) {
	s, st, home := openTest(t)
	r := message("update", counters(10, 0, 0, 1))
	appendRecords(t, home, "a.jsonl", r)
	scan(t, s, home)
	r["message"].(map[string]any)["usage"] = counters(10, 0, 0, 9)
	r["message"].(map[string]any)["stop_reason"] = "end_turn"
	appendRecords(t, home, "a.jsonl", r)
	res := scan(t, s, home)
	if res.Corrections != 1 || res.EventsInserted != 0 || total(t, st).Usage.Total != 19 {
		t.Fatalf("update: %+v", res)
	}
	n, _ := st.RequestCount(context.Background())
	if n != 1 {
		t.Fatal(n)
	}
}

func TestRequestIdentityAndLateIterations(t *testing.T) {
	s, st, home := openTest(t)
	u := counters(10, 100, 5, 20)
	r := message("a", u)
	r["message"].(map[string]any)["stop_reason"] = "end_turn"
	appendRecords(t, home, "a.jsonl", r)
	scan(t, s, home)
	// The same provider request can be replayed under a different message ID.
	r["message"].(map[string]any)["id"] = "different-message-id"
	appendRecords(t, home, "mirror.jsonl", r)
	if got := scan(t, s, home); got.EventsInserted != 0 || total(t, st).Usage.Total != 135 {
		t.Fatal(got)
	}
	// Later accounting includes previously omitted compaction cost.
	u["iterations"] = []any{counters(10, 100, 5, 20), map[string]any{"type": "compaction", "input_tokens": 8, "output_tokens": 2}}
	appendRecords(t, home, "mirror.jsonl", r)
	if got := scan(t, s, home); got.Corrections != 1 || total(t, st).Usage.Total != 145 {
		t.Fatal(got, total(t, st))
	}
	delete(r, "requestId")
	appendRecords(t, home, "mirror.jsonl", r)
	if got := scan(t, s, home); got.EventsInserted != 0 || total(t, st).Usage.Total != 145 {
		t.Fatal(got)
	}
	n, _ := st.RequestCount(context.Background())
	if n != 1 {
		t.Fatal(n)
	}
}

func TestLateRequestIDBridgesTwoMessages(t *testing.T) {
	s, st, home := openTest(t)
	a, b := message("a", counters(2, 0, 0, 3)), message("b", counters(2, 0, 0, 3))
	delete(b, "requestId")
	appendRecords(t, home, "a.jsonl", a, b)
	scan(t, s, home)
	b["requestId"] = a["requestId"]
	appendRecords(t, home, "a.jsonl", b)
	scan(t, s, home)
	n, _ := st.RequestCount(context.Background())
	if n != 1 || total(t, st).Usage.Total != 5 {
		t.Fatal(n, total(t, st))
	}
}

func TestRewriteDoesNotBlockHealthySource(t *testing.T) {
	s, st, home := openTest(t)
	other := filepath.Join(t.TempDir(), "other")
	broken := appendRecords(t, home, "a.jsonl", message("a", counters(2, 0, 0, 3)))
	scan(t, s, home)
	if err := os.WriteFile(broken, []byte{}, 0600); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"b", "c"} {
		appendRecords(t, other, "b.jsonl", message(id, counters(2, 0, 0, 3)))
		_, err := s.Scan(context.Background(), []string{home, other}, false)
		var rebuild *RebuildRequiredError
		if !errors.As(err, &rebuild) {
			t.Fatal(err)
		}
	}
	if got := total(t, st).Usage.Total; got != 15 {
		t.Fatalf("healthy root stalled: %d", got)
	}
}

func TestRestartResumesCursorAndWaitsForTail(t *testing.T) {
	s, st, home := openTest(t)
	file := appendRecords(t, home, "restart.jsonl", message("a", counters(10, 0, 0, 2)))
	scan(t, s, home)
	partial, _ := json.Marshal(message("b", counters(20, 0, 0, 3)))
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	f.Write(partial[:len(partial)/2])
	f.Close()
	scan(t, s, home)
	dbPath := st.DBPath()
	st.Close()
	reopened, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	s = &Scanner{Store: reopened}
	f, err = os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	f.Write(partial[len(partial)/2:])
	f.WriteString("\n")
	f.Close()
	got := scan(t, s, home)
	if got.Records != 1 || got.EventsInserted != 1 || total(t, reopened).Usage.Total != 35 {
		t.Fatal(got, total(t, reopened))
	}
	if got = scan(t, s, home); got.EventsInserted != 0 || got.Records != 0 {
		t.Fatal(got)
	}
}

func TestInvalidIterationListKeepsTopLevelWithDiagnostic(t *testing.T) {
	s, st, home := openTest(t)
	u := counters(10, 0, 0, 2)
	u["iterations"] = []any{map[string]any{"type": "message"}}
	appendRecords(t, home, "invalid-iterations.jsonl", message("a", u))
	got := scan(t, s, home)
	if got.Warnings != 1 || total(t, st).Usage.Total != 12 || !total(t, st).CoverageIncomplete {
		t.Fatal(got, total(t, st))
	}
}

func TestIncompleteTailAndLargeContent(t *testing.T) {
	s, st, home := openTest(t)
	r := message("large", counters(3, 4, 5, 6))
	r["message"].(map[string]any)["content"] = []any{map[string]any{"type": "text", "text": strings.Repeat("x", 10<<20)}}
	raw, _ := json.Marshal(r)
	p := appendRecords(t, home, "a.jsonl")
	if e := os.WriteFile(p, raw, 0600); e != nil {
		t.Fatal(e)
	}
	scan(t, s, home)
	if total(t, st).Usage.Total != 0 {
		t.Fatal("consumed partial tail")
	}
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0600)
	_, _ = f.WriteString("\n")
	f.Close()
	scan(t, s, home)
	if total(t, st).Usage.Total != 18 {
		t.Fatal("large assistant usage was lost")
	}
}

func TestRewriteRequiresApprovalAndDeletedFilesPreserveHistory(t *testing.T) {
	s, st, home := openTest(t)
	r := message("rewrite", counters(5, 0, 0, 5))
	p := appendRecords(t, home, "a.jsonl", r)
	scan(t, s, home)
	os.Remove(p)
	scan(t, s, home)
	if total(t, st).Usage.Total != 10 {
		t.Fatal("deleted source erased history")
	}
	p = appendRecords(t, home, "a.jsonl", message("short", counters(1, 0, 0, 1)))
	at := time.Now().Add(time.Second)
	os.Chtimes(p, at, at)
	_, e := s.Scan(context.Background(), []string{home}, false)
	var required *RebuildRequiredError
	if !errors.As(e, &required) {
		t.Fatalf("expected rebuild boundary: %v", e)
	}
	if total(t, st).Usage.Total != 10 {
		t.Fatal("history changed without approval")
	}
	if _, e = s.Scan(context.Background(), []string{home}, true); e != nil {
		t.Fatal(e)
	}
	if total(t, st).Usage.Total != 2 {
		t.Fatal("approved rebuild failed")
	}
}

func TestClaudeOnlySubagentRelationsAndBadRows(t *testing.T) {
	s, st, home := openTest(t)
	deep := message("deep", counters(1, 0, 0, 2))
	deep["message"].(map[string]any)["model"] = "deepseek-flash"
	appendRecords(t, home, "root.jsonl", deep, message("main", counters(1, 0, 0, 2)))
	child := message("child", counters(3, 0, 0, 4))
	child["parentUuid"] = "not-a-session"
	appendRecords(t, home, "session/subagents/agent-worker.jsonl", child)
	bad := message("negative", counters(-1, 0, 0, 2))
	appendRecords(t, home, "bad.jsonl", bad)
	result := scan(t, s, home)
	if total(t, st).Usage.Total != 10 || result.Warnings != 1 {
		t.Fatalf("Claude filter: %+v", result)
	}
	rels, e := st.SessionRelationships(context.Background())
	if e != nil || rels["session/agent:worker"].ParentSessionID != "session" {
		t.Fatalf("relation: %+v %v", rels, e)
	}
}

func TestConflictingFinalCopiesDoNotSilentlyReplaceUsage(t *testing.T) {
	s, st, home := openTest(t)
	r := message("conflict", counters(10, 0, 0, 2))
	r["message"].(map[string]any)["stop_reason"] = "end_turn"
	appendRecords(t, home, "a.jsonl", r)
	scan(t, s, home)
	r["message"].(map[string]any)["usage"] = counters(20, 0, 0, 2)
	appendRecords(t, home, "a.jsonl", r)
	res := scan(t, s, home)
	if res.Warnings != 1 || total(t, st).Usage.Total != 12 {
		t.Fatalf("conflict: %+v", res)
	}
}

func TestMalformedLineDoesNotPreventFollowingUsage(t *testing.T) {
	s, st, home := openTest(t)
	p := appendRecords(t, home, "a.jsonl")
	os.WriteFile(p, []byte("{broken}\n"), 0600)
	appendRecords(t, home, "a.jsonl", message("valid", counters(1, 2, 3, 4)))
	res := scan(t, s, home)
	if res.Warnings != 1 || total(t, st).Usage.Total != 10 {
		t.Fatal(res)
	}
}

func TestMissingSessionRetainsUsageAndDeduplicatesMirrors(t *testing.T) {
	s, st, home := openTest(t)
	other := filepath.Join(t.TempDir(), "mirror")
	missing := message("missing-session", counters(10, 0, 0, 5))
	delete(missing, "sessionId")
	appendRecords(t, home, "a.jsonl", message("known", counters(10, 0, 0, 5)), missing)
	got := scan(t, s, home)
	if got.EventsInserted != 2 || got.Warnings != 1 || total(t, st).Usage.Total != 30 {
		t.Fatalf("missing session lost valid request: scan=%+v summary=%+v", got, total(t, st))
	}
	warnings, err := st.Warnings(context.Background(), 10)
	if err != nil || len(warnings) != 1 || warnings[0].Kind != "missing_session" {
		t.Fatalf("missing session diagnostic: %+v %v", warnings, err)
	}
	appendRecords(t, other, "copy.jsonl", missing)
	got = scan(t, s, home, other)
	if got.EventsInserted != 0 || got.Duplicates != 1 || total(t, st).Usage.Total != 30 {
		t.Fatalf("mirror counted twice: %+v", got)
	}
	filtered, err := st.Summary(context.Background(), model.Filter{Homes: []string{other}})
	if err != nil || filtered.Usage.Total != 15 {
		t.Fatalf("missing source association: %+v %v", filtered, err)
	}
	if got = scan(t, s, home, other); got.Records != 0 || got.EventsInserted != 0 || got.Warnings != 0 {
		t.Fatalf("repeat scan was not idle: %+v", got)
	}
}

func TestMissingSessionStillUsesSubagentDirectory(t *testing.T) {
	s, st, home := openTest(t)
	r := message("child-no-session", counters(10, 0, 0, 5))
	delete(r, "sessionId")
	appendRecords(t, home, "parent/subagents/agent-worker.jsonl", r)
	scan(t, s, home)
	events, err := st.Events(context.Background(), store.EventQuery{})
	if err != nil || len(events) != 1 || events[0].SessionID != "parent/agent:worker" || events[0].AgentType != "subagent" {
		t.Fatalf("subagent usage: %+v %v", events, err)
	}
	rels, err := st.SessionRelationships(context.Background())
	if err != nil || rels["parent/agent:worker"].ParentSessionID != "parent" {
		t.Fatalf("subagent relation: %+v %v", rels, err)
	}
}

func TestMissingSessionStillValidatesRequestIdentity(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		message, request, uuid bool
		tokens, warnings       int64
	}{
		{"message-only", true, false, false, 15, 1},
		{"request-only", false, true, false, 15, 1},
		{"record-only", false, false, true, 15, 2},
		{"no-identity", false, false, false, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, st, home := openTest(t)
			r := message("identity", counters(10, 0, 0, 5))
			delete(r, "sessionId")
			if !tc.message {
				delete(r["message"].(map[string]any), "id")
			}
			if !tc.request {
				delete(r, "requestId")
			}
			if !tc.uuid {
				delete(r, "uuid")
			}
			appendRecords(t, home, "a.jsonl", r)
			got := scan(t, s, home)
			if got.Warnings != tc.warnings || total(t, st).Usage.Total != tc.tokens {
				t.Fatalf("identity validation: scan=%+v summary=%+v", got, total(t, st))
			}
		})
	}
}

func TestLateSessionMetadataEnrichesAnExistingRequest(t *testing.T) {
	s, st, home := openTest(t)
	r := message("late-session", counters(10, 0, 0, 5))
	delete(r, "sessionId")
	r["message"].(map[string]any)["stop_reason"] = "end_turn"
	appendRecords(t, home, "a.jsonl", r)
	scan(t, s, home)
	r["sessionId"] = "resolved-session"
	appendRecords(t, home, "a.jsonl", r)
	got := scan(t, s, home)
	events, err := st.Events(context.Background(), store.EventQuery{})
	if err != nil || got.Corrections != 1 || got.EventsInserted != 0 || len(events) != 1 || events[0].SessionID != "resolved-session" || total(t, st).Usage.Total != 15 {
		t.Fatalf("late session attribution: scan=%+v events=%+v err=%v", got, events, err)
	}
}

func TestLateIterationClassificationAndStaleCopies(t *testing.T) {
	for _, complete := range []bool{false, true} {
		t.Run(map[bool]string{false: "partial", true: "final"}[complete], func(t *testing.T) {
			s, st, home := openTest(t)
			r := message("late-type", counters(10, 0, 0, 5))
			if complete {
				r["message"].(map[string]any)["stop_reason"] = "end_turn"
			}
			appendRecords(t, home, "a.jsonl", r)
			scan(t, s, home)
			u := r["message"].(map[string]any)["usage"].(map[string]any)
			part := counters(10, 0, 0, 5)
			part["type"] = "compaction"
			u["iterations"] = []any{part}
			appendRecords(t, home, "a.jsonl", r)
			got := scan(t, s, home)
			events, err := st.Events(context.Background(), store.EventQuery{})
			if err != nil || got.Corrections != 1 || got.EventsInserted != 0 || got.Warnings != 0 || len(events) != 1 || events[0].IterationType != "compaction" || total(t, st).Usage.Total != 15 {
				t.Fatalf("late classification: scan=%+v events=%+v err=%v", got, events, err)
			}
			delete(u, "iterations")
			appendRecords(t, home, "stale.jsonl", r)
			got = scan(t, s, home)
			events, err = st.Events(context.Background(), store.EventQuery{})
			if err != nil || got.Corrections != 0 || got.Warnings != 0 || events[0].IterationType != "compaction" {
				t.Fatalf("stale copy downgraded classification: scan=%+v events=%+v err=%v", got, events, err)
			}
			part["type"] = "advisor_message"
			u["iterations"] = []any{part}
			appendRecords(t, home, "a.jsonl", r)
			got = scan(t, s, home)
			events, err = st.Events(context.Background(), store.EventQuery{})
			if err != nil || got.Warnings != 1 || events[0].IterationType != "compaction" || total(t, st).Usage.Total != 15 {
				t.Fatalf("conflicting classification must be diagnosed: scan=%+v events=%+v err=%v", got, events, err)
			}
		})
	}
}

func TestMetadataEnrichmentDoesNotReopenFinalUsage(t *testing.T) {
	for _, field := range []string{"iteration_type", "cache_lifetime"} {
		t.Run(field, func(t *testing.T) {
			s, st, home := openTest(t)
			u := counters(0, 0, 10, 5)
			delete(u, "cache_creation")
			r := message("final-enrichment", u)
			r["message"].(map[string]any)["stop_reason"] = "end_turn"
			appendRecords(t, home, "a.jsonl", r)
			scan(t, s, home)
			// A metadata-only mirror need not carry the original stop_reason.
			delete(r["message"].(map[string]any), "stop_reason")
			if field == "iteration_type" {
				u["type"] = "compaction"
			} else {
				u["cache_creation"] = map[string]any{"ephemeral_5m_input_tokens": int64(10)}
			}
			appendRecords(t, home, "a.jsonl", r)
			got := scan(t, s, home)
			if got.Corrections != 1 || total(t, st).Usage.Total != 15 {
				t.Fatalf("classification enrichment failed: %+v", got)
			}
			u["input_tokens"] = int64(100)
			appendRecords(t, home, "a.jsonl", r)
			got = scan(t, s, home)
			if got.Warnings != 1 || total(t, st).Usage.Total != 15 {
				t.Fatalf("metadata enrichment reopened final counters: scan=%+v summary=%+v", got, total(t, st))
			}
		})
	}
}
