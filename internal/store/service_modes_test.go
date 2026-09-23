package store

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/zJay26/claude-usage/internal/model"
	"github.com/zJay26/claude-usage/internal/pricing"
)

func TestServiceModeAggregatesFiltersAndPricing(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, mode := range []string{"default", "priority", ""} {
		e := model.UsageEvent{ID: mode + "id", SessionID: "mixed", TurnID: mode, Model: "claude-opus-5-5", Timestamp: time.Now(), Usage: model.TokenUsage{Input: 100, Output: 20, Total: 120}, Provenance: model.ProvenanceSessionJSONL, ServiceMode: model.ModeFromTier(mode, "claude_usage")}
		if _, err := st.InsertEvent(ctx, e, ""); err != nil {
			t.Fatal(err)
		}
	}
	for _, tt := range []struct {
		mode                          string
		total, regular, fast, unknown int64
	}{{"", 360, 240, 120, 120}, {"regular", 240, 240, 0, 120}, {"fast", 120, 0, 120, 0}, {"unknown", 120, 120, 0, 120}} {
		f := model.Filter{Mode: tt.mode}
		s, err := st.Summary(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		if s.Usage.Total != tt.total || s.Modes.Regular.Total != tt.regular || s.Modes.Fast.Total != tt.fast || s.Modes.Unknown.Total != tt.unknown {
			t.Fatalf("%s: %+v", tt.mode, s)
		}
		points, err := st.Timeseries(ctx, f, "hour")
		if err != nil || len(points) != 1 || points[0].Modes != s.Modes {
			t.Fatalf("points %+v %v", points, err)
		}
		items, err := st.Breakdown(ctx, f, "model", 10)
		if err != nil || len(items) != 1 || items[0].Modes != s.Modes {
			t.Fatalf("breakdown %+v %v", items, err)
		}
		sessions, err := st.Sessions(ctx, f, 10, 0)
		if err != nil || len(sessions) != 1 || sessions[0].Modes != s.Modes {
			t.Fatalf("sessions %+v %v", sessions, err)
		}
		raw, _ := pricing.NewBuilderForBasis(nil, pricing.FastWeightedBasis)
		agg, _ := pricing.NewBuilderForBasis(nil, pricing.FastWeightedBasis)
		sess, _ := pricing.NewBuilderForBasis(nil, pricing.FastWeightedBasis)
		if err := st.WalkPricingEvents(ctx, f, raw.Add); err != nil {
			t.Fatal(err)
		}
		if err := st.WalkPricingAggregates(ctx, f, agg.Add); err != nil {
			t.Fatal(err)
		}
		if err := st.WalkSessionPricingAggregates(ctx, f, []string{"mixed"}, sess.Add); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(raw.Report().Summary, agg.Report().Summary) || !reflect.DeepEqual(raw.Report().Summary, sess.Report().Summary) {
			t.Fatalf("pricing aggregation lost mode: raw=%+v agg=%+v sess=%+v", raw.Report().Summary, agg.Report().Summary, sess.Report().Summary)
		}
	}
}

func TestFastPricingKeepsEventBoundaries(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, id := range []string{"a", "b", "c"} {
		_, err := st.InsertEvent(ctx, model.UsageEvent{ID: id, SessionID: "s", Timestamp: time.Now(), Model: "claude-local", Usage: model.TokenUsage{Input: 1, Total: 1}, ServiceMode: model.ModeFromTier("fast", "test"), Provenance: model.ProvenanceSessionJSONL}, "")
		if err != nil {
			t.Fatal(err)
		}
	}
	overrides := map[string]pricing.Override{"claude-local": {AliasOf: "claude-opus-5-5"}}
	for _, walk := range []func(context.Context, model.Filter, func(model.UsageEvent) error) error{st.WalkPricingEvents, st.WalkPricingAggregates, func(ctx context.Context, f model.Filter, fn func(model.UsageEvent) error) error {
		return st.WalkSessionPricingAggregates(ctx, f, []string{"s"}, fn)
	}} {
		b, err := pricing.NewBuilderForBasis(overrides, pricing.FastWeightedBasis)
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		if err := walk(ctx, model.Filter{}, func(event model.UsageEvent) error {
			count++
			return b.Add(event)
		}); err != nil {
			t.Fatal(err)
		}
		if count != 3 || b.Report().Summary.USD != "0.000024000" {
			t.Fatalf("event boundaries changed by aggregation: count=%d %+v", count, b.Report().Summary)
		}
	}
}
