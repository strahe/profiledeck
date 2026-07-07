package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(upUsage, downUsage)
}

func upUsage(ctx context.Context, db *bun.DB) error {
	return execStatements(ctx, db, []string{
		`CREATE TABLE IF NOT EXISTS usage_events (
			id TEXT PRIMARY KEY,
			provider_id TEXT NOT NULL,
			source TEXT NOT NULL,
			source_key TEXT NOT NULL,
			session_id TEXT NOT NULL DEFAULT '',
			model TEXT NOT NULL DEFAULT '',
			occurred_at_unix_ms INTEGER NOT NULL DEFAULT 0,
			input_tokens INTEGER NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
			cached_input_tokens INTEGER NOT NULL DEFAULT 0 CHECK (cached_input_tokens >= 0),
			output_tokens INTEGER NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
			total_tokens INTEGER NOT NULL DEFAULT 0 CHECK (total_tokens >= 0),
			estimated_cost_micros INTEGER CHECK (estimated_cost_micros IS NULL OR estimated_cost_micros >= 0),
			cost_status TEXT NOT NULL CHECK (cost_status IN ('estimated', 'unknown')),
			metadata_json TEXT NOT NULL DEFAULT '{}',
			created_at_unix_ms INTEGER NOT NULL,
			updated_at_unix_ms INTEGER NOT NULL,
			CHECK (
				(cost_status = 'estimated' AND estimated_cost_micros IS NOT NULL)
				OR (cost_status = 'unknown' AND estimated_cost_micros IS NULL)
			)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_usage_events_provider_id ON usage_events(provider_id)`,
		`CREATE INDEX IF NOT EXISTS idx_usage_events_source ON usage_events(source)`,
		`CREATE INDEX IF NOT EXISTS idx_usage_events_source_key ON usage_events(source_key)`,
		`CREATE INDEX IF NOT EXISTS idx_usage_events_model ON usage_events(model)`,
		`CREATE INDEX IF NOT EXISTS idx_usage_events_occurred_at ON usage_events(occurred_at_unix_ms)`,
		`CREATE INDEX IF NOT EXISTS idx_usage_events_cost_status ON usage_events(cost_status)`,
		`CREATE TABLE IF NOT EXISTS usage_import_cursors (
			provider_id TEXT NOT NULL,
			source TEXT NOT NULL,
			source_key TEXT NOT NULL,
			modified_unix_ms INTEGER NOT NULL DEFAULT 0,
			size_bytes INTEGER NOT NULL DEFAULT 0 CHECK (size_bytes >= 0),
			imported_events INTEGER NOT NULL DEFAULT 0 CHECK (imported_events >= 0),
			invalid_lines INTEGER NOT NULL DEFAULT 0 CHECK (invalid_lines >= 0),
			unsupported_lines INTEGER NOT NULL DEFAULT 0 CHECK (unsupported_lines >= 0),
			metadata_json TEXT NOT NULL DEFAULT '{}',
			created_at_unix_ms INTEGER NOT NULL,
			updated_at_unix_ms INTEGER NOT NULL,
			PRIMARY KEY (provider_id, source, source_key)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_usage_import_cursors_source ON usage_import_cursors(source)`,
	})
}

func downUsage(ctx context.Context, db *bun.DB) error {
	return execStatements(ctx, db, []string{
		`DROP INDEX IF EXISTS idx_usage_import_cursors_source`,
		`DROP TABLE IF EXISTS usage_import_cursors`,
		`DROP INDEX IF EXISTS idx_usage_events_cost_status`,
		`DROP INDEX IF EXISTS idx_usage_events_occurred_at`,
		`DROP INDEX IF EXISTS idx_usage_events_model`,
		`DROP INDEX IF EXISTS idx_usage_events_source_key`,
		`DROP INDEX IF EXISTS idx_usage_events_source`,
		`DROP INDEX IF EXISTS idx_usage_events_provider_id`,
		`DROP TABLE IF EXISTS usage_events`,
	})
}
