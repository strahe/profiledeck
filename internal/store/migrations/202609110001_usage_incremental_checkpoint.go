package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/uptrace/bun"
)

const zeroUsageDigestSQL = "X'0000000000000000000000000000000000000000000000000000000000000000'"

func init() {
	Migrations.MustRegister(upUsageIncrementalCheckpoint, downUsageIncrementalCheckpoint)
}

func upUsageIncrementalCheckpoint(ctx context.Context, db *bun.DB) error {
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return applyUsageIncrementalCheckpoint(ctx, tx)
	})
}

func applyUsageIncrementalCheckpoint(ctx context.Context, db bun.IDB) error {
	completedGenerationExists, err := usageColumnExists(ctx, db, "usage_sources", "completed_generation")
	if err != nil {
		return err
	}
	if !completedGenerationExists {
		if err := addUsageColumnIfMissing(ctx, db, "usage_sources", "completed_generation",
			"INTEGER NOT NULL DEFAULT 0 CHECK (completed_generation >= 0 AND completed_generation <= sync_generation)"); err != nil {
			return err
		}
		if _, err := db.ExecContext(ctx, `
			UPDATE usage_sources
			SET completed_generation = sync_generation
		`); err != nil {
			return err
		}
	}

	for _, table := range []string{"codex_usage_import_files", "grok_build_usage_import_files"} {
		columns := []struct {
			name       string
			definition string
		}{
			{"checkpoint_revision", "INTEGER NOT NULL DEFAULT 0 CHECK (checkpoint_revision >= 0)"},
			{"processed_bytes", "INTEGER NOT NULL DEFAULT 0 CHECK (processed_bytes >= 0 AND processed_bytes <= size_bytes)"},
			{"metadata_digest", "BLOB NOT NULL DEFAULT " + zeroUsageDigestSQL + " CHECK (typeof(metadata_digest) = 'blob' AND length(metadata_digest) = 32)"},
			{"file_identity_digest", "BLOB NOT NULL DEFAULT " + zeroUsageDigestSQL + " CHECK (typeof(file_identity_digest) = 'blob' AND length(file_identity_digest) = 32)"},
			{"boundary_digest", "BLOB NOT NULL DEFAULT " + zeroUsageDigestSQL + " CHECK (typeof(boundary_digest) = 'blob' AND length(boundary_digest) = 32)"},
			{"checkpoint_event_digest", "BLOB NOT NULL DEFAULT " + zeroUsageDigestSQL + " CHECK (typeof(checkpoint_event_digest) = 'blob' AND length(checkpoint_event_digest) = 32)"},
			{"parser_state_json", "TEXT NOT NULL DEFAULT '{}' CHECK (CASE WHEN json_valid(parser_state_json) THEN json_type(parser_state_json) = 'object' AND length(CAST(parser_state_json AS BLOB)) <= 1048576 ELSE 0 END)"},
		}
		for _, column := range columns {
			if err := addUsageColumnIfMissing(ctx, db, table, column.name, column.definition); err != nil {
				return err
			}
		}
	}

	_, err = db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS usage_import_observations (
			source_id INTEGER NOT NULL,
			file_key BLOB NOT NULL CHECK (
				typeof(file_key) = 'blob' AND length(file_key) = 32 AND file_key <> zeroblob(32)
			),
			metadata_digest BLOB NOT NULL CHECK (
				typeof(metadata_digest) = 'blob' AND length(metadata_digest) = 32 AND metadata_digest <> zeroblob(32)
			),
			status TEXT NOT NULL CHECK (status IN ('history_changed', 'unavailable', 'fact_conflict')),
			updated_at_unix_ms INTEGER NOT NULL CHECK (updated_at_unix_ms >= 0),
			PRIMARY KEY (source_id, file_key),
			FOREIGN KEY (source_id) REFERENCES usage_sources(id)
				ON UPDATE RESTRICT ON DELETE CASCADE
		) STRICT, WITHOUT ROWID`)
	return err
}

func downUsageIncrementalCheckpoint(ctx context.Context, db *bun.DB) error {
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS usage_import_observations`); err != nil {
			return err
		}
		for _, table := range []string{"grok_build_usage_import_files", "codex_usage_import_files"} {
			for _, column := range []string{
				"parser_state_json",
				"checkpoint_event_digest",
				"boundary_digest",
				"file_identity_digest",
				"metadata_digest",
				"processed_bytes",
				"checkpoint_revision",
			} {
				if err := dropUsageColumnIfExists(ctx, tx, table, column); err != nil {
					return err
				}
			}
		}
		return dropUsageColumnIfExists(ctx, tx, "usage_sources", "completed_generation")
	})
}

func addUsageColumnIfMissing(
	ctx context.Context,
	db bun.IDB,
	table string,
	column string,
	definition string,
) error {
	exists, err := usageColumnExists(ctx, db, table, column)
	if err != nil || exists {
		return err
	}
	_, err = db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, definition))
	return err
}

func dropUsageColumnIfExists(ctx context.Context, db bun.IDB, table, column string) error {
	exists, err := usageColumnExists(ctx, db, table, column)
	if err != nil || !exists {
		return err
	}
	_, err = db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s", table, column))
	return err
}

func usageColumnExists(ctx context.Context, db bun.IDB, table, column string) (bool, error) {
	rows, err := db.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}
