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
		`CREATE TABLE IF NOT EXISTS usage_sources (
			id INTEGER PRIMARY KEY,
			provider_id TEXT NOT NULL,
			source_key TEXT NOT NULL,
			identity_revision INTEGER NOT NULL CHECK (identity_revision > 0),
			sync_generation INTEGER NOT NULL DEFAULT 0 CHECK (sync_generation >= 0),
			last_completed_at_unix_ms INTEGER NOT NULL DEFAULT 0 CHECK (last_completed_at_unix_ms >= 0),
			tracked_units INTEGER NOT NULL DEFAULT 0 CHECK (tracked_units >= 0),
			invalid_records INTEGER NOT NULL DEFAULT 0 CHECK (invalid_records >= 0),
			unsupported_records INTEGER NOT NULL DEFAULT 0 CHECK (unsupported_records >= 0)
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_usage_sources_provider_source
			ON usage_sources(provider_id, source_key)`,
		`CREATE TABLE IF NOT EXISTS usage_sessions (
			id INTEGER PRIMARY KEY,
			source_id INTEGER NOT NULL,
			session_key TEXT NOT NULL CHECK (length(session_key) BETWEEN 1 AND 256),
			FOREIGN KEY (source_id) REFERENCES usage_sources(id) ON UPDATE RESTRICT ON DELETE RESTRICT
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_usage_sessions_source_session
			ON usage_sessions(source_id, session_key)`,
		`CREATE TABLE IF NOT EXISTS usage_models (
			id INTEGER PRIMARY KEY,
			source_id INTEGER NOT NULL,
			model_key TEXT NOT NULL CHECK (
				length(model_key) BETWEEN 1 AND 200
				AND model_key NOT GLOB '*[^A-Za-z0-9._:/@-]*'
			),
			FOREIGN KEY (source_id) REFERENCES usage_sources(id) ON UPDATE RESTRICT ON DELETE RESTRICT
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_usage_models_source_model
			ON usage_models(source_id, model_key)`,
		`CREATE TABLE IF NOT EXISTS usage_facts (
			id INTEGER PRIMARY KEY,
			event_key BLOB NOT NULL CHECK (
				typeof(event_key) = 'blob' AND length(event_key) = 32 AND event_key <> zeroblob(32)
			),
			source_id INTEGER NOT NULL,
			session_id INTEGER,
			model_id INTEGER NOT NULL,
			occurred_at_unix_ms INTEGER NOT NULL DEFAULT 0 CHECK (occurred_at_unix_ms >= 0),
			input_tokens INTEGER NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
			cached_input_tokens INTEGER NOT NULL DEFAULT 0 CHECK (
				cached_input_tokens >= 0 AND cached_input_tokens <= input_tokens
			),
			output_tokens INTEGER NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
			total_tokens INTEGER NOT NULL DEFAULT 0 CHECK (total_tokens >= 0),
			estimated_cost_micros INTEGER CHECK (estimated_cost_micros IS NULL OR estimated_cost_micros >= 0),
			cost_status INTEGER NOT NULL CHECK (cost_status IN (0, 1, 2)),
			FOREIGN KEY (source_id) REFERENCES usage_sources(id) ON UPDATE RESTRICT ON DELETE RESTRICT,
			FOREIGN KEY (session_id) REFERENCES usage_sessions(id) ON UPDATE RESTRICT ON DELETE RESTRICT,
			FOREIGN KEY (model_id) REFERENCES usage_models(id) ON UPDATE RESTRICT ON DELETE RESTRICT,
			CHECK (
				(cost_status IN (1, 2) AND estimated_cost_micros IS NOT NULL)
				OR (cost_status = 0 AND estimated_cost_micros IS NULL)
			)
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_usage_facts_event_key ON usage_facts(event_key)`,
		`CREATE INDEX IF NOT EXISTS idx_usage_facts_source_time
			ON usage_facts(source_id, occurred_at_unix_ms)`,
		`CREATE INDEX IF NOT EXISTS idx_usage_facts_source_cost_model_id
			ON usage_facts(source_id, cost_status, model_id, id)`,
		`CREATE TABLE IF NOT EXISTS codex_usage_import_files (
			source_id INTEGER NOT NULL,
			file_key BLOB NOT NULL CHECK (
				typeof(file_key) = 'blob' AND length(file_key) = 32 AND file_key <> zeroblob(32)
			),
			modified_unix_ms INTEGER NOT NULL DEFAULT 0 CHECK (modified_unix_ms >= 0),
			size_bytes INTEGER NOT NULL DEFAULT 0 CHECK (size_bytes >= 0),
			imported_facts INTEGER NOT NULL DEFAULT 0 CHECK (imported_facts >= 0),
			invalid_lines INTEGER NOT NULL DEFAULT 0 CHECK (invalid_lines >= 0),
			unsupported_lines INTEGER NOT NULL DEFAULT 0 CHECK (unsupported_lines >= 0),
			parser_revision INTEGER NOT NULL CHECK (parser_revision > 0),
			identity_revision INTEGER NOT NULL CHECK (identity_revision > 0),
			event_digest BLOB NOT NULL CHECK (
				typeof(event_digest) = 'blob' AND length(event_digest) = 32 AND event_digest <> zeroblob(32)
			),
			updated_at_unix_ms INTEGER NOT NULL CHECK (updated_at_unix_ms >= 0),
			PRIMARY KEY (source_id, file_key),
			FOREIGN KEY (source_id) REFERENCES usage_sources(id) ON UPDATE RESTRICT ON DELETE RESTRICT
		) WITHOUT ROWID`,
	})
}

func downUsage(ctx context.Context, db *bun.DB) error {
	return execStatements(ctx, db, []string{
		`DROP TABLE IF EXISTS codex_usage_import_files`,
		`DROP INDEX IF EXISTS idx_usage_facts_source_cost_model_id`,
		`DROP INDEX IF EXISTS idx_usage_facts_source_time`,
		`DROP INDEX IF EXISTS idx_usage_facts_event_key`,
		`DROP TABLE IF EXISTS usage_facts`,
		`DROP INDEX IF EXISTS idx_usage_models_source_model`,
		`DROP TABLE IF EXISTS usage_models`,
		`DROP INDEX IF EXISTS idx_usage_sessions_source_session`,
		`DROP TABLE IF EXISTS usage_sessions`,
		`DROP INDEX IF EXISTS idx_usage_sources_provider_source`,
		`DROP TABLE IF EXISTS usage_sources`,
	})
}
