package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(upGrokBuildUsageImport, downGrokBuildUsageImport)
}

func upGrokBuildUsageImport(ctx context.Context, db *bun.DB) error {
	return execStatements(ctx, db, []string{
		`CREATE TABLE IF NOT EXISTS grok_build_usage_import_files (
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
			FOREIGN KEY (source_id) REFERENCES usage_sources(id)
				ON UPDATE RESTRICT ON DELETE CASCADE
		) STRICT, WITHOUT ROWID`,
	})
}

func downGrokBuildUsageImport(ctx context.Context, db *bun.DB) error {
	return execStatements(ctx, db, []string{
		`DROP TABLE IF EXISTS grok_build_usage_import_files`,
	})
}
