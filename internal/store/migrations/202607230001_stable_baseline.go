package migrations

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(upStableBaseline, downStableBaseline)
}

func upStableBaseline(ctx context.Context, db *bun.DB) error {
	return execStatements(ctx, db, []string{
		`CREATE TABLE IF NOT EXISTS providers (
			id TEXT PRIMARY KEY NOT NULL,
			name TEXT NOT NULL,
			adapter_id TEXT NOT NULL,
			metadata_json TEXT NOT NULL DEFAULT '{}'
				CHECK (CASE WHEN json_valid(metadata_json) THEN json_type(metadata_json) = 'object' ELSE 0 END),
			created_at_unix_ms INTEGER NOT NULL,
			updated_at_unix_ms INTEGER NOT NULL
		) STRICT`,
		`CREATE INDEX IF NOT EXISTS idx_providers_adapter_id ON providers(adapter_id)`,
		`CREATE TABLE IF NOT EXISTS profiles (
			id TEXT PRIMARY KEY NOT NULL,
			name TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			metadata_json TEXT NOT NULL DEFAULT '{}'
				CHECK (CASE WHEN json_valid(metadata_json) THEN json_type(metadata_json) = 'object' ELSE 0 END),
			created_at_unix_ms INTEGER NOT NULL,
			updated_at_unix_ms INTEGER NOT NULL
		) STRICT`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY NOT NULL,
			value_json TEXT NOT NULL CHECK (json_valid(value_json)),
			updated_at_unix_ms INTEGER NOT NULL
		) STRICT`,
		`CREATE TABLE IF NOT EXISTS provider_settings (
			provider_id TEXT PRIMARY KEY NOT NULL,
			schema_version INTEGER NOT NULL CHECK (schema_version > 0),
			settings_json TEXT NOT NULL DEFAULT '{}'
				CHECK (CASE WHEN json_valid(settings_json) THEN json_type(settings_json) = 'object' ELSE 0 END),
			updated_at_unix_ms INTEGER NOT NULL,
			FOREIGN KEY (provider_id) REFERENCES providers(id)
				ON UPDATE RESTRICT ON DELETE CASCADE
		) STRICT`,
		`CREATE TABLE IF NOT EXISTS provider_profile_settings (
			profile_id TEXT NOT NULL,
			provider_id TEXT NOT NULL,
			schema_version INTEGER NOT NULL CHECK (schema_version > 0),
			settings_json TEXT NOT NULL DEFAULT '{}'
				CHECK (CASE WHEN json_valid(settings_json) THEN json_type(settings_json) = 'object' ELSE 0 END),
			updated_at_unix_ms INTEGER NOT NULL,
			PRIMARY KEY (profile_id, provider_id),
			FOREIGN KEY (profile_id) REFERENCES profiles(id)
				ON UPDATE RESTRICT ON DELETE CASCADE,
			FOREIGN KEY (provider_id) REFERENCES providers(id)
				ON UPDATE RESTRICT ON DELETE CASCADE
		) STRICT`,
		`CREATE INDEX IF NOT EXISTS idx_provider_profile_settings_provider_id
			ON provider_profile_settings(provider_id)`,
		`CREATE TABLE IF NOT EXISTS provider_active_states (
			provider_id TEXT PRIMARY KEY NOT NULL,
			profile_id TEXT NOT NULL,
			revision INTEGER NOT NULL CHECK (revision > 0),
			updated_at_unix_ms INTEGER NOT NULL,
			FOREIGN KEY (provider_id) REFERENCES providers(id)
				ON UPDATE RESTRICT ON DELETE CASCADE,
			FOREIGN KEY (profile_id) REFERENCES profiles(id)
				ON UPDATE RESTRICT ON DELETE RESTRICT
		) STRICT`,
		`CREATE INDEX IF NOT EXISTS idx_provider_active_states_profile_id
			ON provider_active_states(profile_id)`,
		`CREATE TABLE IF NOT EXISTS operations (
			id TEXT PRIMARY KEY NOT NULL,
			provider_id TEXT NOT NULL,
			operation_type TEXT NOT NULL
				CHECK (operation_type IN ('switch', 'recovery', 'import', 'maintenance')),
			status TEXT NOT NULL CHECK (status IN ('pending', 'failed', 'applied')),
			source_operation_id TEXT,
			metadata_schema_version INTEGER NOT NULL CHECK (metadata_schema_version > 0),
			metadata_json TEXT NOT NULL DEFAULT '{}'
				CHECK (CASE WHEN json_valid(metadata_json) THEN json_type(metadata_json) = 'object' ELSE 0 END),
			error_code TEXT NOT NULL DEFAULT '',
			error_message TEXT NOT NULL DEFAULT '',
			resolution_kind TEXT NOT NULL DEFAULT '',
			resolved_at_unix_ms INTEGER NOT NULL DEFAULT 0 CHECK (resolved_at_unix_ms >= 0),
			created_at_unix_ms INTEGER NOT NULL,
			updated_at_unix_ms INTEGER NOT NULL,
			UNIQUE (provider_id, id),
			CHECK (source_operation_id IS NULL OR source_operation_id <> id),
			CHECK (
				(operation_type = 'recovery' AND source_operation_id IS NOT NULL)
				OR (operation_type <> 'recovery' AND source_operation_id IS NULL)
			),
			FOREIGN KEY (provider_id) REFERENCES providers(id)
				ON UPDATE RESTRICT ON DELETE RESTRICT,
			FOREIGN KEY (provider_id, source_operation_id) REFERENCES operations(provider_id, id)
				ON UPDATE RESTRICT ON DELETE CASCADE
		) STRICT`,
		`CREATE INDEX IF NOT EXISTS idx_operations_provider_status_updated
			ON operations(provider_id, status, updated_at_unix_ms)`,
		`CREATE INDEX IF NOT EXISTS idx_operations_operation_type ON operations(operation_type)`,
		`CREATE INDEX IF NOT EXISTS idx_operations_source_operation_id ON operations(source_operation_id)`,
		`CREATE TABLE IF NOT EXISTS operation_profiles (
			operation_id TEXT NOT NULL,
			profile_id TEXT NOT NULL,
			PRIMARY KEY (operation_id, profile_id),
			FOREIGN KEY (operation_id) REFERENCES operations(id)
				ON UPDATE RESTRICT ON DELETE CASCADE,
			FOREIGN KEY (profile_id) REFERENCES profiles(id)
				ON UPDATE RESTRICT ON DELETE RESTRICT
		) STRICT`,
		`CREATE INDEX IF NOT EXISTS idx_operation_profiles_profile_id ON operation_profiles(profile_id)`,
		`CREATE TABLE IF NOT EXISTS provider_credentials (
			id TEXT PRIMARY KEY NOT NULL,
			provider_id TEXT NOT NULL,
			credential_kind TEXT NOT NULL,
			payload_json TEXT NOT NULL
				CHECK (CASE WHEN json_valid(payload_json) THEN json_type(payload_json) = 'object' ELSE 0 END),
			payload_sha256 TEXT NOT NULL
				CHECK (length(payload_sha256) = 64 AND payload_sha256 NOT GLOB '*[^0-9a-f]*'),
			metadata_json TEXT NOT NULL DEFAULT '{}'
				CHECK (CASE WHEN json_valid(metadata_json) THEN json_type(metadata_json) = 'object' ELSE 0 END),
			created_at_unix_ms INTEGER NOT NULL,
			updated_at_unix_ms INTEGER NOT NULL,
			UNIQUE (provider_id, id),
			FOREIGN KEY (provider_id) REFERENCES providers(id)
				ON UPDATE RESTRICT ON DELETE CASCADE
		) STRICT`,
		`CREATE INDEX IF NOT EXISTS idx_provider_credentials_provider_id
			ON provider_credentials(provider_id)`,
		`CREATE INDEX IF NOT EXISTS idx_provider_credentials_kind ON provider_credentials(credential_kind)`,
		`CREATE TABLE IF NOT EXISTS provider_config_sets (
			provider_id TEXT NOT NULL,
			id TEXT NOT NULL,
			config_kind TEXT NOT NULL,
			name TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			payload_text TEXT NOT NULL,
			payload_sha256 TEXT NOT NULL
				CHECK (length(payload_sha256) = 64 AND payload_sha256 NOT GLOB '*[^0-9a-f]*'),
			metadata_json TEXT NOT NULL DEFAULT '{}'
				CHECK (CASE WHEN json_valid(metadata_json) THEN json_type(metadata_json) = 'object' ELSE 0 END),
			created_at_unix_ms INTEGER NOT NULL,
			updated_at_unix_ms INTEGER NOT NULL,
			PRIMARY KEY (provider_id, id),
			FOREIGN KEY (provider_id) REFERENCES providers(id)
				ON UPDATE RESTRICT ON DELETE CASCADE
		) STRICT`,
		`CREATE INDEX IF NOT EXISTS idx_provider_config_sets_kind ON provider_config_sets(config_kind)`,
		`CREATE TABLE IF NOT EXISTS profile_credential_bindings (
			profile_id TEXT NOT NULL,
			provider_id TEXT NOT NULL,
			slot_id TEXT NOT NULL,
			credential_id TEXT NOT NULL,
			created_at_unix_ms INTEGER NOT NULL,
			updated_at_unix_ms INTEGER NOT NULL,
			PRIMARY KEY (profile_id, provider_id, slot_id),
			FOREIGN KEY (profile_id) REFERENCES profiles(id)
				ON UPDATE RESTRICT ON DELETE CASCADE,
			FOREIGN KEY (provider_id) REFERENCES providers(id)
				ON UPDATE RESTRICT ON DELETE CASCADE,
			FOREIGN KEY (provider_id, credential_id) REFERENCES provider_credentials(provider_id, id)
				ON UPDATE RESTRICT ON DELETE RESTRICT
		) STRICT`,
		`CREATE INDEX IF NOT EXISTS idx_profile_credential_bindings_provider_id
			ON profile_credential_bindings(provider_id)`,
		`CREATE INDEX IF NOT EXISTS idx_profile_credential_bindings_credential_id
			ON profile_credential_bindings(credential_id)`,
		`CREATE TABLE IF NOT EXISTS profile_config_set_bindings (
			profile_id TEXT NOT NULL,
			provider_id TEXT NOT NULL,
			slot_id TEXT NOT NULL,
			config_set_id TEXT NOT NULL,
			created_at_unix_ms INTEGER NOT NULL,
			updated_at_unix_ms INTEGER NOT NULL,
			PRIMARY KEY (profile_id, provider_id, slot_id),
			FOREIGN KEY (profile_id) REFERENCES profiles(id)
				ON UPDATE RESTRICT ON DELETE CASCADE,
			FOREIGN KEY (provider_id) REFERENCES providers(id)
				ON UPDATE RESTRICT ON DELETE CASCADE,
			FOREIGN KEY (provider_id, config_set_id) REFERENCES provider_config_sets(provider_id, id)
				ON UPDATE RESTRICT ON DELETE RESTRICT
		) STRICT`,
		`CREATE INDEX IF NOT EXISTS idx_profile_config_set_bindings_provider_id
			ON profile_config_set_bindings(provider_id)`,
		`CREATE INDEX IF NOT EXISTS idx_profile_config_set_bindings_config_set_id
			ON profile_config_set_bindings(config_set_id)`,
		`CREATE TABLE IF NOT EXISTS profile_targets (
			profile_id TEXT NOT NULL,
			provider_id TEXT NOT NULL,
			target_id TEXT NOT NULL,
			path TEXT NOT NULL,
			path_key TEXT NOT NULL,
			format TEXT NOT NULL,
			strategy TEXT NOT NULL,
			value_json TEXT NOT NULL
				CHECK (CASE WHEN json_valid(value_json) THEN json_type(value_json) = 'object' ELSE 0 END),
			enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
			metadata_json TEXT NOT NULL DEFAULT '{}'
				CHECK (CASE WHEN json_valid(metadata_json) THEN json_type(metadata_json) = 'object' ELSE 0 END),
			created_at_unix_ms INTEGER NOT NULL,
			updated_at_unix_ms INTEGER NOT NULL,
			PRIMARY KEY (profile_id, provider_id, target_id),
			FOREIGN KEY (profile_id) REFERENCES profiles(id)
				ON UPDATE RESTRICT ON DELETE CASCADE,
			FOREIGN KEY (provider_id) REFERENCES providers(id)
				ON UPDATE RESTRICT ON DELETE CASCADE
		) STRICT`,
		`CREATE INDEX IF NOT EXISTS idx_profile_targets_profile_id ON profile_targets(profile_id)`,
		`CREATE INDEX IF NOT EXISTS idx_profile_targets_provider_id ON profile_targets(provider_id)`,
		`CREATE INDEX IF NOT EXISTS idx_profile_targets_enabled ON profile_targets(enabled)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_profile_targets_unique_path
			ON profile_targets(profile_id, provider_id, path_key)`,
		`CREATE INDEX IF NOT EXISTS idx_profile_targets_path_key ON profile_targets(path_key)`,
		`CREATE TRIGGER IF NOT EXISTS trg_profile_targets_path_owner_insert
		BEFORE INSERT ON profile_targets
		WHEN EXISTS (
			SELECT 1 FROM profile_targets
			WHERE path_key = NEW.path_key
				AND (provider_id <> NEW.provider_id OR target_id <> NEW.target_id)
		)
		BEGIN
			SELECT RAISE(ABORT, 'profile target path is owned by another provider target');
		END`,
		`CREATE TRIGGER IF NOT EXISTS trg_profile_targets_path_owner_update
		BEFORE UPDATE OF path, path_key, provider_id, target_id ON profile_targets
		WHEN EXISTS (
			SELECT 1 FROM profile_targets
			WHERE path_key = NEW.path_key
				AND NOT (
					profile_id = OLD.profile_id
					AND provider_id = OLD.provider_id
					AND target_id = OLD.target_id
				)
				AND (provider_id <> NEW.provider_id OR target_id <> NEW.target_id)
		)
		BEGIN
			SELECT RAISE(ABORT, 'profile target path is owned by another provider target');
		END`,
		`CREATE TABLE IF NOT EXISTS usage_sources (
			id INTEGER PRIMARY KEY,
			provider_id TEXT NOT NULL,
			source_key TEXT NOT NULL,
			identity_revision INTEGER NOT NULL CHECK (identity_revision > 0),
			sync_generation INTEGER NOT NULL DEFAULT 0 CHECK (sync_generation >= 0),
			last_completed_at_unix_ms INTEGER NOT NULL DEFAULT 0 CHECK (last_completed_at_unix_ms >= 0),
			tracked_units INTEGER NOT NULL DEFAULT 0 CHECK (tracked_units >= 0),
			invalid_records INTEGER NOT NULL DEFAULT 0 CHECK (invalid_records >= 0),
			unsupported_records INTEGER NOT NULL DEFAULT 0 CHECK (unsupported_records >= 0),
			FOREIGN KEY (provider_id) REFERENCES providers(id)
				ON UPDATE RESTRICT ON DELETE CASCADE
		) STRICT`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_usage_sources_provider_source
			ON usage_sources(provider_id, source_key)`,
		`CREATE TABLE IF NOT EXISTS usage_sessions (
			id INTEGER PRIMARY KEY,
			source_id INTEGER NOT NULL,
			session_key TEXT NOT NULL CHECK (length(session_key) BETWEEN 1 AND 256),
			UNIQUE (source_id, id),
			FOREIGN KEY (source_id) REFERENCES usage_sources(id)
				ON UPDATE RESTRICT ON DELETE CASCADE
		) STRICT`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_usage_sessions_source_session
			ON usage_sessions(source_id, session_key)`,
		`CREATE TABLE IF NOT EXISTS usage_models (
			id INTEGER PRIMARY KEY,
			source_id INTEGER NOT NULL,
			model_key TEXT NOT NULL CHECK (
				length(model_key) BETWEEN 1 AND 200
				AND model_key NOT GLOB '*[^A-Za-z0-9._:/@-]*'
			),
			UNIQUE (source_id, id),
			FOREIGN KEY (source_id) REFERENCES usage_sources(id)
				ON UPDATE RESTRICT ON DELETE CASCADE
		) STRICT`,
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
			FOREIGN KEY (source_id) REFERENCES usage_sources(id)
				ON UPDATE RESTRICT ON DELETE CASCADE,
			FOREIGN KEY (source_id, session_id) REFERENCES usage_sessions(source_id, id)
				ON UPDATE RESTRICT ON DELETE RESTRICT,
			FOREIGN KEY (source_id, model_id) REFERENCES usage_models(source_id, id)
				ON UPDATE RESTRICT ON DELETE RESTRICT,
			CHECK (
				(cost_status IN (1, 2) AND estimated_cost_micros IS NOT NULL)
				OR (cost_status = 0 AND estimated_cost_micros IS NULL)
			)
		) STRICT`,
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
			FOREIGN KEY (source_id) REFERENCES usage_sources(id)
				ON UPDATE RESTRICT ON DELETE CASCADE
		) STRICT, WITHOUT ROWID`,
		`CREATE TABLE IF NOT EXISTS system_state (
			key TEXT PRIMARY KEY NOT NULL,
			value_json TEXT NOT NULL CHECK (json_valid(value_json)),
			created_at_unix_ms INTEGER NOT NULL,
			updated_at_unix_ms INTEGER NOT NULL
		) STRICT`,
	})
}

func downStableBaseline(ctx context.Context, db *bun.DB) error {
	return execStatements(ctx, db, []string{
		`DROP TABLE IF EXISTS system_state`,
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
		`DROP TRIGGER IF EXISTS trg_profile_targets_path_owner_update`,
		`DROP TRIGGER IF EXISTS trg_profile_targets_path_owner_insert`,
		`DROP INDEX IF EXISTS idx_profile_targets_path_key`,
		`DROP INDEX IF EXISTS idx_profile_targets_unique_path`,
		`DROP INDEX IF EXISTS idx_profile_targets_enabled`,
		`DROP INDEX IF EXISTS idx_profile_targets_provider_id`,
		`DROP INDEX IF EXISTS idx_profile_targets_profile_id`,
		`DROP TABLE IF EXISTS profile_targets`,
		`DROP INDEX IF EXISTS idx_profile_config_set_bindings_config_set_id`,
		`DROP INDEX IF EXISTS idx_profile_config_set_bindings_provider_id`,
		`DROP TABLE IF EXISTS profile_config_set_bindings`,
		`DROP INDEX IF EXISTS idx_profile_credential_bindings_credential_id`,
		`DROP INDEX IF EXISTS idx_profile_credential_bindings_provider_id`,
		`DROP TABLE IF EXISTS profile_credential_bindings`,
		`DROP INDEX IF EXISTS idx_provider_config_sets_kind`,
		`DROP TABLE IF EXISTS provider_config_sets`,
		`DROP INDEX IF EXISTS idx_provider_credentials_kind`,
		`DROP INDEX IF EXISTS idx_provider_credentials_provider_id`,
		`DROP TABLE IF EXISTS provider_credentials`,
		`DROP INDEX IF EXISTS idx_operation_profiles_profile_id`,
		`DROP TABLE IF EXISTS operation_profiles`,
		`DROP INDEX IF EXISTS idx_operations_source_operation_id`,
		`DROP INDEX IF EXISTS idx_operations_operation_type`,
		`DROP INDEX IF EXISTS idx_operations_provider_status_updated`,
		`DROP TABLE IF EXISTS operations`,
		`DROP INDEX IF EXISTS idx_provider_active_states_profile_id`,
		`DROP TABLE IF EXISTS provider_active_states`,
		`DROP INDEX IF EXISTS idx_provider_profile_settings_provider_id`,
		`DROP TABLE IF EXISTS provider_profile_settings`,
		`DROP TABLE IF EXISTS provider_settings`,
		`DROP TABLE IF EXISTS settings`,
		`DROP TABLE IF EXISTS profiles`,
		`DROP INDEX IF EXISTS idx_providers_adapter_id`,
		`DROP TABLE IF EXISTS providers`,
	})
}

func execStatements(ctx context.Context, db *bun.DB, statements []string) error {
	// Bun records the marker after this callback returns. Keep the baseline
	// atomic so an interrupted first initialization can be replayed safely.
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		for index, statement := range statements {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("statement %d: %w", index+1, err)
			}
		}
		return nil
	})
}
