package migrations

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(upUsagePartialCost, downUsagePartialCost)
}

func upUsagePartialCost(ctx context.Context, db *bun.DB) error {
	return execUsageCostStatusStatements(ctx, db, []string{
		`ALTER TABLE usage_events RENAME TO usage_events_cost_status_old`,
		createUsageEventsTableWithPartialCost,
		`INSERT INTO usage_events (
			id, provider_id, source, source_key, session_id, model, occurred_at_unix_ms,
			input_tokens, cached_input_tokens, output_tokens, total_tokens,
			estimated_cost_micros, cost_status, metadata_json, created_at_unix_ms, updated_at_unix_ms
		)
		SELECT
			id, provider_id, source, source_key, session_id, model, occurred_at_unix_ms,
			input_tokens, cached_input_tokens, output_tokens, total_tokens,
			estimated_cost_micros, cost_status, metadata_json, created_at_unix_ms, updated_at_unix_ms
		FROM usage_events_cost_status_old`,
		`DROP TABLE usage_events_cost_status_old`,
		`CREATE INDEX idx_usage_events_provider_id ON usage_events(provider_id)`,
		`CREATE INDEX idx_usage_events_source ON usage_events(source)`,
		`CREATE INDEX idx_usage_events_source_key ON usage_events(source_key)`,
		`CREATE INDEX idx_usage_events_model ON usage_events(model)`,
		`CREATE INDEX idx_usage_events_occurred_at ON usage_events(occurred_at_unix_ms)`,
		`CREATE INDEX idx_usage_events_cost_status ON usage_events(cost_status)`,
		`CREATE INDEX idx_usage_events_provider_cost_model_id ON usage_events(provider_id, cost_status, model, id)`,
	})
}

func downUsagePartialCost(ctx context.Context, db *bun.DB) error {
	return execUsageCostStatusStatements(ctx, db, []string{
		`ALTER TABLE usage_events RENAME TO usage_events_cost_status_new`,
		createUsageEventsTableWithoutPartialCost,
		`INSERT INTO usage_events (
			id, provider_id, source, source_key, session_id, model, occurred_at_unix_ms,
			input_tokens, cached_input_tokens, output_tokens, total_tokens,
			estimated_cost_micros, cost_status, metadata_json, created_at_unix_ms, updated_at_unix_ms
		)
		SELECT
			id, provider_id, source, source_key, session_id, model, occurred_at_unix_ms,
			input_tokens, cached_input_tokens, output_tokens, total_tokens,
			CASE WHEN cost_status = 'partial' THEN NULL ELSE estimated_cost_micros END,
			CASE WHEN cost_status = 'partial' THEN 'unknown' ELSE cost_status END,
			metadata_json, created_at_unix_ms, updated_at_unix_ms
		FROM usage_events_cost_status_new`,
		`DROP TABLE usage_events_cost_status_new`,
		`CREATE INDEX idx_usage_events_provider_id ON usage_events(provider_id)`,
		`CREATE INDEX idx_usage_events_source ON usage_events(source)`,
		`CREATE INDEX idx_usage_events_source_key ON usage_events(source_key)`,
		`CREATE INDEX idx_usage_events_model ON usage_events(model)`,
		`CREATE INDEX idx_usage_events_occurred_at ON usage_events(occurred_at_unix_ms)`,
		`CREATE INDEX idx_usage_events_cost_status ON usage_events(cost_status)`,
	})
}

const createUsageEventsTableWithPartialCost = `CREATE TABLE usage_events (
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
	cost_status TEXT NOT NULL CHECK (cost_status IN ('estimated', 'partial', 'unknown')),
	metadata_json TEXT NOT NULL DEFAULT '{}',
	created_at_unix_ms INTEGER NOT NULL,
	updated_at_unix_ms INTEGER NOT NULL,
	CHECK (
		(cost_status IN ('estimated', 'partial') AND estimated_cost_micros IS NOT NULL)
		OR (cost_status = 'unknown' AND estimated_cost_micros IS NULL)
	)
)`

const createUsageEventsTableWithoutPartialCost = `CREATE TABLE usage_events (
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
)`

func execUsageCostStatusStatements(ctx context.Context, db *bun.DB, statements []string) error {
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		for index, statement := range statements {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("statement %d: %w", index+1, err)
			}
		}
		return nil
	})
}
