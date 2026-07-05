package migrations

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(upInitialSchema, downInitialSchema)
}

func upInitialSchema(ctx context.Context, db *bun.DB) error {
	return execStatements(ctx, db, []string{
		`CREATE TABLE IF NOT EXISTS providers (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			adapter_id TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
			metadata_json TEXT NOT NULL DEFAULT '{}',
			created_at_unix_ms INTEGER NOT NULL,
			updated_at_unix_ms INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_providers_adapter_id ON providers(adapter_id)`,
		`CREATE INDEX IF NOT EXISTS idx_providers_enabled ON providers(enabled)`,
		`CREATE TABLE IF NOT EXISTS profiles (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			metadata_json TEXT NOT NULL DEFAULT '{}',
			created_at_unix_ms INTEGER NOT NULL,
			updated_at_unix_ms INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value_json TEXT NOT NULL,
			updated_at_unix_ms INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS active_states (
			scope_type TEXT NOT NULL,
			scope_id TEXT NOT NULL,
			profile_id TEXT NOT NULL DEFAULT '',
			operation_id TEXT NOT NULL DEFAULT '',
			updated_at_unix_ms INTEGER NOT NULL,
			PRIMARY KEY (scope_type, scope_id)
		)`,
		`CREATE TABLE IF NOT EXISTS operations (
			id TEXT PRIMARY KEY,
			operation_type TEXT NOT NULL CHECK (operation_type IN ('switch', 'rollback', 'import', 'maintenance')),
			status TEXT NOT NULL CHECK (status IN ('pending', 'failed', 'applied')),
			profile_id TEXT NOT NULL DEFAULT '',
			metadata_json TEXT NOT NULL DEFAULT '{}',
			error_code TEXT NOT NULL DEFAULT '',
			error_message TEXT NOT NULL DEFAULT '',
			created_at_unix_ms INTEGER NOT NULL,
			updated_at_unix_ms INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_operations_status ON operations(status)`,
		`CREATE INDEX IF NOT EXISTS idx_operations_operation_type ON operations(operation_type)`,
	})
}

func downInitialSchema(ctx context.Context, db *bun.DB) error {
	return execStatements(ctx, db, []string{
		`DROP INDEX IF EXISTS idx_operations_operation_type`,
		`DROP INDEX IF EXISTS idx_operations_status`,
		`DROP TABLE IF EXISTS operations`,
		`DROP TABLE IF EXISTS active_states`,
		`DROP TABLE IF EXISTS settings`,
		`DROP TABLE IF EXISTS profiles`,
		`DROP INDEX IF EXISTS idx_providers_enabled`,
		`DROP INDEX IF EXISTS idx_providers_adapter_id`,
		`DROP TABLE IF EXISTS providers`,
	})
}

func execStatements(ctx context.Context, db *bun.DB, statements []string) error {
	for i, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("statement %d: %w", i+1, err)
		}
	}
	return nil
}
