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
		`CREATE TABLE IF NOT EXISTS provider_profile_settings (
			profile_id TEXT NOT NULL,
			provider_id TEXT NOT NULL,
			quota_refresh_interval_seconds INTEGER NOT NULL DEFAULT 0 CHECK (quota_refresh_interval_seconds IN (0, 300, 600, 1800, 3600)),
			auth_keepalive_enabled INTEGER NOT NULL DEFAULT 0 CHECK (auth_keepalive_enabled IN (0, 1)),
			updated_at_unix_ms INTEGER NOT NULL,
			PRIMARY KEY (profile_id, provider_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_provider_profile_settings_provider_id ON provider_profile_settings(provider_id)`,
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
			operation_type TEXT NOT NULL CHECK (operation_type IN ('switch', 'recovery', 'import', 'maintenance')),
			status TEXT NOT NULL CHECK (status IN ('pending', 'failed', 'applied')),
			profile_id TEXT NOT NULL DEFAULT '',
			metadata_json TEXT NOT NULL DEFAULT '{}',
			error_code TEXT NOT NULL DEFAULT '',
			error_message TEXT NOT NULL DEFAULT '',
			resolution_kind TEXT NOT NULL DEFAULT '',
			resolved_at_unix_ms INTEGER NOT NULL DEFAULT 0,
			created_at_unix_ms INTEGER NOT NULL,
			updated_at_unix_ms INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_operations_status ON operations(status)`,
		`CREATE INDEX IF NOT EXISTS idx_operations_operation_type ON operations(operation_type)`,
		`CREATE TABLE IF NOT EXISTS provider_credentials (
			id TEXT PRIMARY KEY,
			provider_id TEXT NOT NULL,
			credential_kind TEXT NOT NULL,
			payload_json TEXT NOT NULL,
			payload_sha256 TEXT NOT NULL,
			metadata_json TEXT NOT NULL DEFAULT '{}',
			created_at_unix_ms INTEGER NOT NULL,
			updated_at_unix_ms INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_provider_credentials_provider_id ON provider_credentials(provider_id)`,
		`CREATE INDEX IF NOT EXISTS idx_provider_credentials_kind ON provider_credentials(credential_kind)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_provider_credentials_provider_id_id ON provider_credentials(provider_id, id)`,
		`CREATE TABLE IF NOT EXISTS provider_config_sets (
			id TEXT PRIMARY KEY,
			provider_id TEXT NOT NULL,
			config_kind TEXT NOT NULL,
			name TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			payload_text TEXT NOT NULL,
			payload_sha256 TEXT NOT NULL,
			metadata_json TEXT NOT NULL DEFAULT '{}',
			created_at_unix_ms INTEGER NOT NULL,
			updated_at_unix_ms INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_provider_config_sets_provider_id ON provider_config_sets(provider_id)`,
		`CREATE INDEX IF NOT EXISTS idx_provider_config_sets_kind ON provider_config_sets(config_kind)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_provider_config_sets_provider_id_id ON provider_config_sets(provider_id, id)`,
		`CREATE TABLE IF NOT EXISTS profile_credential_bindings (
			profile_id TEXT NOT NULL,
			provider_id TEXT NOT NULL,
			slot_id TEXT NOT NULL,
			credential_id TEXT NOT NULL,
			created_at_unix_ms INTEGER NOT NULL,
			updated_at_unix_ms INTEGER NOT NULL,
			PRIMARY KEY (profile_id, provider_id, slot_id),
			FOREIGN KEY (profile_id) REFERENCES profiles(id) ON UPDATE RESTRICT ON DELETE RESTRICT,
			FOREIGN KEY (provider_id) REFERENCES providers(id) ON UPDATE RESTRICT ON DELETE RESTRICT,
			FOREIGN KEY (provider_id, credential_id) REFERENCES provider_credentials(provider_id, id) ON UPDATE RESTRICT ON DELETE RESTRICT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_profile_credential_bindings_provider_id ON profile_credential_bindings(provider_id)`,
		`CREATE INDEX IF NOT EXISTS idx_profile_credential_bindings_credential_id ON profile_credential_bindings(credential_id)`,
		`CREATE TABLE IF NOT EXISTS profile_config_set_bindings (
			profile_id TEXT NOT NULL,
			provider_id TEXT NOT NULL,
			slot_id TEXT NOT NULL,
			config_set_id TEXT NOT NULL,
			created_at_unix_ms INTEGER NOT NULL,
			updated_at_unix_ms INTEGER NOT NULL,
			PRIMARY KEY (profile_id, provider_id, slot_id),
			FOREIGN KEY (profile_id) REFERENCES profiles(id) ON UPDATE RESTRICT ON DELETE RESTRICT,
			FOREIGN KEY (provider_id) REFERENCES providers(id) ON UPDATE RESTRICT ON DELETE RESTRICT,
			FOREIGN KEY (provider_id, config_set_id) REFERENCES provider_config_sets(provider_id, id) ON UPDATE RESTRICT ON DELETE RESTRICT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_profile_config_set_bindings_provider_id ON profile_config_set_bindings(provider_id)`,
		`CREATE INDEX IF NOT EXISTS idx_profile_config_set_bindings_config_set_id ON profile_config_set_bindings(config_set_id)`,
	})
}

func downInitialSchema(ctx context.Context, db *bun.DB) error {
	return execStatements(ctx, db, []string{
		`DROP INDEX IF EXISTS idx_profile_config_set_bindings_config_set_id`,
		`DROP INDEX IF EXISTS idx_profile_config_set_bindings_provider_id`,
		`DROP TABLE IF EXISTS profile_config_set_bindings`,
		`DROP INDEX IF EXISTS idx_profile_credential_bindings_credential_id`,
		`DROP INDEX IF EXISTS idx_profile_credential_bindings_provider_id`,
		`DROP TABLE IF EXISTS profile_credential_bindings`,
		`DROP INDEX IF EXISTS idx_provider_config_sets_provider_id_id`,
		`DROP INDEX IF EXISTS idx_provider_config_sets_kind`,
		`DROP INDEX IF EXISTS idx_provider_config_sets_provider_id`,
		`DROP TABLE IF EXISTS provider_config_sets`,
		`DROP INDEX IF EXISTS idx_provider_credentials_provider_id_id`,
		`DROP INDEX IF EXISTS idx_provider_credentials_kind`,
		`DROP INDEX IF EXISTS idx_provider_credentials_provider_id`,
		`DROP TABLE IF EXISTS provider_credentials`,
		`DROP INDEX IF EXISTS idx_operations_operation_type`,
		`DROP INDEX IF EXISTS idx_operations_status`,
		`DROP TABLE IF EXISTS operations`,
		`DROP TABLE IF EXISTS active_states`,
		`DROP TABLE IF EXISTS settings`,
		`DROP INDEX IF EXISTS idx_provider_profile_settings_provider_id`,
		`DROP TABLE IF EXISTS provider_profile_settings`,
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
