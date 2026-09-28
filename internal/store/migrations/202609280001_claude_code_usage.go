package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() { Migrations.MustRegister(upClaudeCodeUsage, downClaudeCodeUsage) }
func upClaudeCodeUsage(ctx context.Context, db *bun.DB) error {
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := addUsageColumnIfMissing(ctx, tx, "usage_facts", "token_status", "INTEGER NOT NULL DEFAULT 0 CHECK (token_status BETWEEN 0 AND 3)"); err != nil {
			return err
		}
		if err := addUsageColumnIfMissing(ctx, tx, "usage_facts", "cache_write_5m_tokens", "INTEGER CHECK (cache_write_5m_tokens IS NULL OR cache_write_5m_tokens >= 0)"); err != nil {
			return err
		}
		if err := addUsageColumnIfMissing(ctx, tx, "usage_facts", "cache_write_1h_tokens", "INTEGER CHECK (cache_write_1h_tokens IS NULL OR cache_write_1h_tokens >= 0)"); err != nil {
			return err
		}
		if err := addUsageColumnIfMissing(ctx, tx, "usage_facts", "pricing_eligible", "INTEGER NOT NULL DEFAULT 0 CHECK (pricing_eligible IN (0, 1))"); err != nil {
			return err
		}
		if err := addUsageColumnIfMissing(ctx, tx, "usage_facts", "price_snapshot_json", "TEXT NOT NULL DEFAULT '{}' CHECK (CASE WHEN json_valid(price_snapshot_json) THEN json_type(price_snapshot_json) = 'object' AND length(CAST(price_snapshot_json AS BLOB)) <= 1024 ELSE 0 END)"); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS claude_code_usage_import_files (
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
			checkpoint_revision INTEGER NOT NULL DEFAULT 0 CHECK (checkpoint_revision >= 0),
			processed_bytes INTEGER NOT NULL DEFAULT 0 CHECK (processed_bytes >= 0 AND processed_bytes <= size_bytes),
			metadata_digest BLOB NOT NULL DEFAULT X'0000000000000000000000000000000000000000000000000000000000000000' CHECK (typeof(metadata_digest) = 'blob' AND length(metadata_digest) = 32),
			file_identity_digest BLOB NOT NULL DEFAULT X'0000000000000000000000000000000000000000000000000000000000000000' CHECK (typeof(file_identity_digest) = 'blob' AND length(file_identity_digest) = 32),
			boundary_digest BLOB NOT NULL DEFAULT X'0000000000000000000000000000000000000000000000000000000000000000' CHECK (typeof(boundary_digest) = 'blob' AND length(boundary_digest) = 32),
			checkpoint_event_digest BLOB NOT NULL DEFAULT X'0000000000000000000000000000000000000000000000000000000000000000' CHECK (typeof(checkpoint_event_digest) = 'blob' AND length(checkpoint_event_digest) = 32),
			parser_state_json TEXT NOT NULL DEFAULT '{}' CHECK (CASE WHEN json_valid(parser_state_json) THEN json_type(parser_state_json) = 'object' AND length(CAST(parser_state_json AS BLOB)) <= 1048576 ELSE 0 END),
			updated_at_unix_ms INTEGER NOT NULL CHECK (updated_at_unix_ms >= 0),
			PRIMARY KEY (source_id, file_key),
			FOREIGN KEY (source_id) REFERENCES usage_sources(id)
				ON UPDATE RESTRICT ON DELETE CASCADE
		) STRICT, WITHOUT ROWID`)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `SELECT file_key, checkpoint_revision, processed_bytes, metadata_digest,
			file_identity_digest, boundary_digest, checkpoint_event_digest, parser_state_json
			FROM claude_code_usage_import_files LIMIT 0`)
		return err
	})
}

func downClaudeCodeUsage(ctx context.Context, db *bun.DB) error {
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS claude_code_usage_import_files`); err != nil {
			return err
		}
		if err := dropUsageColumnIfExists(ctx, tx, "usage_facts", "price_snapshot_json"); err != nil {
			return err
		}
		if err := dropUsageColumnIfExists(ctx, tx, "usage_facts", "pricing_eligible"); err != nil {
			return err
		}
		if err := dropUsageColumnIfExists(ctx, tx, "usage_facts", "cache_write_1h_tokens"); err != nil {
			return err
		}
		if err := dropUsageColumnIfExists(ctx, tx, "usage_facts", "cache_write_5m_tokens"); err != nil {
			return err
		}
		if err := dropUsageColumnIfExists(ctx, tx, "usage_facts", "token_status"); err != nil {
			return err
		}
		return nil
	})
}
