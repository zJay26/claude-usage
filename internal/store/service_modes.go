package store

import (
	"context"
	"database/sql"
	"strings"

	"github.com/zJay26/claude-usage/internal/model"
)

var modeUsageSQL = func() string {
	var columns []string
	for _, mode := range []string{"fast", "unknown"} {
		for _, column := range []string{"input_tokens", "cached_input_tokens", "cache_write_input_tokens", "cache_write_5m", "cache_write_1h", "output_tokens", "reasoning_output_tokens", "total_tokens"} {
			columns = append(columns, "COALESCE(SUM(CASE WHEN e.service_mode='"+mode+"' THEN e."+column+" ELSE 0 END),0)")
		}
	}
	return strings.Join(columns, ",")
}()

func modeUsageDest(m *model.ModeUsage) []any {
	return []any{&m.Fast.Input, &m.Fast.CachedInput, &m.Fast.CacheWriteInput, &m.Fast.CacheWrite5m, &m.Fast.CacheWrite1h, &m.Fast.Output, &m.Fast.ReasoningOutput, &m.Fast.Total,
		&m.Unknown.Input, &m.Unknown.CachedInput, &m.Unknown.CacheWriteInput, &m.Unknown.CacheWrite5m, &m.Unknown.CacheWrite1h, &m.Unknown.Output, &m.Unknown.ReasoningOutput, &m.Unknown.Total}
}

func migrateServiceModes(ctx context.Context, tx *sql.Tx) error {
	for _, stmt := range []string{
		`ALTER TABLE usage_events ADD COLUMN service_mode TEXT NOT NULL DEFAULT 'unknown'`,
		`ALTER TABLE usage_events ADD COLUMN service_tier TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE usage_events ADD COLUMN mode_source TEXT NOT NULL DEFAULT 'unavailable'`,
	} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil && !isDuplicateColumn(err) {
			return err
		}
	}
	return nil
}
