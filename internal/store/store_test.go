package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/zJay26/claude-usage/internal/model"
	"github.com/zJay26/claude-usage/internal/pricing"
)

func BenchmarkEventExportTraversal(b *testing.B) {
	path := os.Getenv("CLAUDE_USAGE_BENCH_DB")
	if path == "" {
		b.Skip("set CLAUDE_USAGE_BENCH_DB to a disposable database copy")
	}
	st, err := Open(path)
	if err != nil {
		b.Fatal(err)
	}
	defer st.Close()
	b.Run("offset-pages", func(b *testing.B) {
		for range b.N {
			count := 0
			for offset := 0; ; offset += 5000 {
				items, err := st.Events(context.Background(), EventQuery{Limit: 5000, Offset: offset})
				if err != nil {
					b.Fatal(err)
				}
				count += len(items)
				if len(items) < 5000 {
					break
				}
			}
			if count == 0 {
				b.Fatal("no export events")
			}
		}
	})
	b.Run("single-snapshot", func(b *testing.B) {
		for range b.N {
			count := 0
			if err := st.WalkEvents(context.Background(), model.Filter{}, func(model.UsageEvent) error {
				count++
				return nil
			}); err != nil {
				b.Fatal(err)
			}
			if count == 0 {
				b.Fatal("no export events")
			}
		}
	})
}

func BenchmarkPricingEventTraversal(b *testing.B) {
	path := os.Getenv("CLAUDE_USAGE_BENCH_DB")
	if path == "" {
		b.Skip("set CLAUDE_USAGE_BENCH_DB to a disposable database copy")
	}
	st, err := Open(path)
	if err != nil {
		b.Fatal(err)
	}
	defer st.Close()
	for range b.N {
		count := 0
		if err := st.WalkPricingEvents(context.Background(), model.Filter{}, func(model.UsageEvent) error {
			count++
			return nil
		}); err != nil {
			b.Fatal(err)
		}
		if count == 0 {
			b.Fatal("no pricing events")
		}
	}
}

func BenchmarkPricingAggregateTraversal(b *testing.B) {
	path := os.Getenv("CLAUDE_USAGE_BENCH_DB")
	if path == "" {
		b.Skip("set CLAUDE_USAGE_BENCH_DB to a disposable database copy")
	}
	st, err := Open(path)
	if err != nil {
		b.Fatal(err)
	}
	defer st.Close()
	for range b.N {
		count := 0
		if err := st.WalkPricingAggregates(context.Background(), model.Filter{}, func(model.UsageEvent) error {
			count++
			return nil
		}); err != nil {
			b.Fatal(err)
		}
		if count == 0 {
			b.Fatal("no pricing aggregates")
		}
	}
}

func BenchmarkDashboardQueries(b *testing.B) {
	path := os.Getenv("CLAUDE_USAGE_BENCH_DB")
	if path == "" {
		b.Skip("set CLAUDE_USAGE_BENCH_DB to a disposable database copy")
	}
	st, err := Open(path)
	if err != nil {
		b.Fatal(err)
	}
	defer st.Close()
	filter := model.Filter{SinceDate: "2026-07-28", UntilDate: "2026-08-04"}
	b.Run("summary-7d", func(b *testing.B) {
		for range b.N {
			if _, err := st.Summary(context.Background(), filter); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("timeseries-all", func(b *testing.B) {
		for range b.N {
			if _, err := st.Timeseries(context.Background(), model.Filter{}, "day"); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("dimensions", func(b *testing.B) {
		for range b.N {
			if _, err := st.Dimensions(context.Background()); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("breakdown-model-7d", func(b *testing.B) {
		for range b.N {
			if _, err := st.Breakdown(context.Background(), filter, "model", 100); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("breakdown-thread-7d", func(b *testing.B) {
		for range b.N {
			if _, err := st.Breakdown(context.Background(), filter, "thread", 100); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("sessions-7d", func(b *testing.B) {
		for range b.N {
			if _, err := st.Sessions(context.Background(), filter, 100, 0); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func TestHourlyTimeseriesUsesInclusiveSinceAndExclusiveUntil(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	base := time.Date(2026, 8, 10, 10, 59, 59, 0, st.Location())
	events := []model.UsageEvent{
		{ID: "before", Timestamp: base, SessionID: "hourly", Usage: model.TokenUsage{Input: 7, Output: 3, Total: 10}},
		{ID: "start", Timestamp: base.Add(time.Second), SessionID: "hourly", Usage: model.TokenUsage{Input: 12, CachedInput: 4, Output: 8, ReasoningOutput: 3, Total: 20}},
		{ID: "inside", Timestamp: base.Add(time.Hour), SessionID: "hourly", Usage: model.TokenUsage{Input: 20, CachedInput: 5, Output: 10, ReasoningOutput: 2, Total: 30}},
		{ID: "until", Timestamp: base.Add(time.Hour + time.Second), SessionID: "hourly", Usage: model.TokenUsage{Input: 30, Output: 10, Total: 40}},
	}
	for _, event := range events {
		event.Provenance = model.ProvenanceSessionJSONL
		event.Confidence = model.ConfidenceExact
		if _, err := st.InsertEvent(ctx, event, event.ID+".jsonl"); err != nil {
			t.Fatal(err)
		}
	}

	since := time.Date(2026, 8, 10, 11, 0, 0, 0, st.Location())
	until := since.Add(time.Hour)
	filter := model.Filter{Since: since, Until: until}
	points, err := st.Timeseries(ctx, filter, "hour")
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 1 {
		t.Fatalf("expected one complete hour, got %#v", points)
	}
	point := points[0]
	if point.Date != "2026-08-10T11" {
		t.Fatalf("unexpected local-hour key %q", point.Date)
	}
	if point.Usage.Total != 50 || point.Usage.Input != 32 || point.Usage.Output != 18 ||
		point.Usage.CachedInput != 9 || point.Usage.ReasoningOutput != 5 {
		t.Fatalf("unexpected hourly usage: %#v", point.Usage)
	}

	summary, err := st.Summary(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	if summary.GrandTotal != 50 {
		t.Fatalf("summary and hourly bucket drifted: %#v", summary)
	}
}

func TestReadPoolRemainsAvailableWhileWriterConnectionIsBusy(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.InsertEvent(ctx, model.UsageEvent{
		ID: "committed", Timestamp: time.Now(), Usage: model.TokenUsage{Input: 8, Output: 2, Total: 10},
		Provenance: model.ProvenanceSessionJSONL, Confidence: model.ConfidenceExact,
	}, "fixture.jsonl"); err != nil {
		t.Fatal(err)
	}
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE meta SET value=value WHERE key='machine'`); err != nil {
		t.Fatal(err)
	}
	readCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	summary, err := st.Summary(readCtx, model.Filter{})
	if err != nil {
		t.Fatalf("read query waited behind writer: %v", err)
	}
	if summary.GrandTotal != 10 {
		t.Fatalf("unexpected committed view: %+v", summary)
	}
}

func TestPricingAggregatesMatchRawEventsAndDimensions(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	base := time.Date(2026, 7, 30, 1, 0, 0, 0, time.UTC)
	events := []model.UsageEvent{
		{ID: "a", Timestamp: base, SessionID: "s1", Model: "claude-opus-5-5", Source: "desktop", ProjectPath: "/p1", Usage: model.TokenUsage{Input: 80, CachedInput: 20, Output: 20, ReasoningOutput: 5, Total: 100}},
		{ID: "b", Timestamp: base.Add(time.Hour), SessionID: "s2", Model: "claude-opus-5-5", Source: "cli", ProjectPath: "/p2", Usage: model.TokenUsage{Input: 40, CachedInput: 10, Output: 10, Total: 50}},
		{ID: "missing", Timestamp: base.Add(time.Hour), SessionID: "s3", Model: "claude-opus-5-5", Source: "desktop", ProjectPath: "/p1", Usage: model.TokenUsage{Total: 12}},
		{ID: "zero-total", Timestamp: base.Add(time.Hour), SessionID: "s3", Model: "claude-opus-5-5", Source: "desktop", ProjectPath: "/p1", Usage: model.TokenUsage{Input: 3, Output: 2}},
		{ID: "invalid", Timestamp: base.Add(25 * time.Hour), SessionID: "s3", Model: "claude-opus-5-5", Source: "desktop", ProjectPath: "/p1", Usage: model.TokenUsage{Input: 8, CachedInput: 9, Output: 4, Total: 12}},
		{ID: "internal", Timestamp: base.Add(24 * time.Hour), SessionID: "s4", Model: "internal", Source: "desktop", ProjectPath: "/p1", Usage: model.TokenUsage{Input: 8, Output: 2, Total: 10}},
		// Individually invalid totals whose opposite errors cancel after SUM. They
		// must remain separate or aggregate pricing would incorrectly accept both.
		{ID: "mismatch-high", Timestamp: base.Add(2 * time.Hour), SessionID: "s5", Model: "claude-opus-5-5", Source: "desktop", ProjectPath: "/p1", Usage: model.TokenUsage{Input: 10, Total: 12}},
		{ID: "mismatch-low", Timestamp: base.Add(3 * time.Hour), SessionID: "s5", Model: "claude-opus-5-5", Source: "desktop", ProjectPath: "/p1", Usage: model.TokenUsage{Input: 10, Total: 8}},
	}
	for _, event := range events {
		event.Provenance = model.ProvenanceSessionJSONL
		event.Confidence = model.ConfidenceExact
		if _, err := st.InsertEvent(ctx, event, event.ID+".jsonl"); err != nil {
			t.Fatal(err)
		}
	}
	raw, _ := pricing.NewBuilder(nil)
	if err := st.WalkPricingEvents(ctx, model.Filter{}, raw.Add); err != nil {
		t.Fatal(err)
	}
	aggregated, _ := pricing.NewBuilder(nil)
	if err := st.WalkPricingAggregates(ctx, model.Filter{}, aggregated.Add); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(raw.Report(), aggregated.Report()) {
		t.Fatalf("aggregate pricing drifted\nraw=%#v\naggregated=%#v", raw.Report(), aggregated.Report())
	}
	rawSessions := map[string]*pricing.Builder{}
	for _, sessionID := range []string{"s1", "s2", "s3", "s4", "s5"} {
		rawSessions[sessionID], _ = pricing.NewBuilder(nil)
	}
	if err := st.WalkPricingEvents(ctx, model.Filter{}, func(event model.UsageEvent) error {
		return rawSessions[event.SessionID].Add(event)
	}); err != nil {
		t.Fatal(err)
	}
	aggregatedSessions := map[string]*pricing.Builder{}
	for sessionID := range rawSessions {
		aggregatedSessions[sessionID], _ = pricing.NewBuilder(nil)
	}
	if err := st.WalkSessionPricingAggregates(ctx, model.Filter{}, []string{"s1", "s2", "s3", "s4", "s5"}, func(event model.UsageEvent) error {
		return aggregatedSessions[event.SessionID].Add(event)
	}); err != nil {
		t.Fatal(err)
	}
	for sessionID, rawBuilder := range rawSessions {
		if !reflect.DeepEqual(rawBuilder.Report().Summary, aggregatedSessions[sessionID].Report().Summary) {
			t.Fatalf("session %s pricing drifted\nraw=%#v\naggregated=%#v", sessionID, rawBuilder.Report().Summary, aggregatedSessions[sessionID].Report().Summary)
		}
	}
	dimensions, err := st.Dimensions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(dimensions.Models, []string{"claude-opus-5-5", "internal"}) ||
		!reflect.DeepEqual(dimensions.Sources, []string{"cli", "desktop"}) ||
		!reflect.DeepEqual(dimensions.Projects, []string{"/p1", "/p2"}) {
		t.Fatalf("unexpected dimensions: %#v", dimensions)
	}
	sessions, err := st.Sessions(ctx, model.Filter{Search: "p2"}, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].SessionID != "s2" {
		t.Fatalf("session search mismatch: %#v", sessions)
	}
}

func TestWalkEventsUsesStableOrderAndPropagatesCallbackErrors(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	at := time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC)
	for _, event := range []model.UsageEvent{
		{ID: "b", Timestamp: at, ObservedAt: at, Model: "claude-opus-5-5"},
		{ID: "a", Timestamp: at, ObservedAt: at, Model: "claude-opus-5-5"},
		{ID: "ignored", Timestamp: at.Add(time.Hour), ObservedAt: at.Add(time.Hour), Model: "internal"},
		{ID: "c", Timestamp: at, ObservedAt: at, Model: "claude-opus-5-5"},
	} {
		event.Provenance = model.ProvenanceSessionJSONL
		event.Confidence = model.ConfidenceExact
		event.Usage = model.TokenUsage{Input: 8, Output: 2, Total: 10}
		if _, err := st.InsertEvent(ctx, event, event.ID+".jsonl"); err != nil {
			t.Fatal(err)
		}
	}
	var ids []string
	if err := st.WalkEvents(ctx, model.Filter{Model: "claude-opus-5-5"}, func(event model.UsageEvent) error {
		ids = append(ids, event.ID)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ids, []string{"c", "b", "a"}) {
		t.Fatalf("unstable event order: %v", ids)
	}
	sentinel := errors.New("stop export")
	if err := st.WalkEvents(ctx, model.Filter{}, func(model.UsageEvent) error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatalf("callback error was not propagated: %v", err)
	}
}

func TestSessionMetadataAdvancesRevisionOnlyWhenChanged(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	info := model.SessionInfo{SessionID: "session", Title: "before", UpdatedAt: time.Unix(10, 0)}
	before := st.Revision()
	if err := st.UpsertSession(ctx, info); err != nil {
		t.Fatal(err)
	}
	afterInsert := st.Revision()
	if afterInsert <= before {
		t.Fatalf("session insert did not advance revision: before=%d after=%d", before, afterInsert)
	}
	if err := st.UpsertSession(ctx, info); err != nil {
		t.Fatal(err)
	}
	if got := st.Revision(); got != afterInsert {
		t.Fatalf("no-op session upsert advanced revision: before=%d after=%d", afterInsert, got)
	}
	info.Title = "after"
	if err := st.UpsertSession(ctx, info); err != nil {
		t.Fatal(err)
	}
	if got := st.Revision(); got <= afterInsert {
		t.Fatalf("session metadata update did not advance revision: before=%d after=%d", afterInsert, got)
	}
}

func TestWarningsAreGroupedByKindAndPath(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, detail := range []string{"第一次", "第二次", "第三次"} {
		if err := st.AddWarning(ctx, "jsonl_record", "same.jsonl", detail); err != nil {
			t.Fatal(err)
		}
	}
	warnings, err := st.Warnings(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || warnings[0].Occurrences != 3 || warnings[0].Detail != "第三次" {
		t.Fatalf("warning grouping mismatch: %+v", warnings)
	}
	status, err := st.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.WarningCount != 1 || status.AccountingMode != "jsonl_only" {
		t.Fatalf("unexpected status: %+v", status)
	}
}

func TestCanonicalViewsUseOnlyJSONL(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	at := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	for _, event := range []model.UsageEvent{
		{ID: "json", Timestamp: at, SessionID: "session", Usage: model.TokenUsage{Input: 80, Output: 20, Total: 100}, Provenance: model.ProvenanceSessionJSONL, Confidence: model.ConfidenceExact},
		{ID: "legacy-otel", Timestamp: at, Usage: model.TokenUsage{Input: 120, Output: 30, Total: 150}, Provenance: "otel", Confidence: model.ConfidenceExact},
		{ID: "legacy-state", Usage: model.TokenUsage{Total: 90}, Provenance: "state_fallback", Confidence: model.ConfidenceAggregateOnly},
	} {
		if _, err := st.InsertEvent(ctx, event, event.ID); err != nil {
			t.Fatal(err)
		}
	}
	summary, err := st.Summary(ctx, model.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Usage.Total != 100 || summary.GrandTotal != 100 || summary.Unattributed.Total != 0 {
		t.Fatalf("non-JSONL provenance affected totals: %+v", summary)
	}
}

func TestTimeseriesKeepsIngestionLocalDateAfterTimezoneChange(t *testing.T) {
	t.Setenv("CLAUDE_USAGE_TIMEZONE", "Asia/Shanghai")
	previousLocal := time.Local
	time.Local = time.FixedZone("UTC+8", 8*60*60)
	t.Cleanup(func() { time.Local = previousLocal })
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	at := time.Date(2026, 7, 30, 16, 1, 0, 0, time.UTC)
	if _, err := st.InsertEvent(ctx, model.UsageEvent{
		ID: "event", Timestamp: at, Usage: model.TokenUsage{Input: 8, Output: 2, Total: 10},
		Provenance: model.ProvenanceSessionJSONL, Confidence: model.ConfidenceExact,
	}, "fixture.jsonl"); err != nil {
		t.Fatal(err)
	}
	time.Local = time.FixedZone("UTC-7", -7*60*60)
	points, err := st.Timeseries(ctx, model.Filter{SinceDate: "2026-07-31", UntilDate: "2026-08-01"}, "day")
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 1 || points[0].Date != "2026-07-31" || points[0].Usage.Total != 10 {
		t.Fatalf("stored local day drifted with query timezone: %+v", points)
	}
}
