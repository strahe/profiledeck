package migrations

import (
	"context"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(upSystemState, downSystemState)
}

func upSystemState(ctx context.Context, db *bun.DB) error {
	return execStatements(ctx, db, []string{
		`CREATE TABLE IF NOT EXISTS system_state (
			key TEXT PRIMARY KEY NOT NULL,
			value_json TEXT NOT NULL CHECK (json_valid(value_json)),
			created_at_unix_ms INTEGER NOT NULL,
			updated_at_unix_ms INTEGER NOT NULL
		)`,
	})
}

func downSystemState(ctx context.Context, db *bun.DB) error {
	return execStatements(ctx, db, []string{
		`DROP TABLE IF EXISTS system_state`,
	})
}
