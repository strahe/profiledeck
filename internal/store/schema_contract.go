package store

type tableSpec struct {
	name    string
	columns []columnSpec
	checks  []string
	strict  bool
}

type columnSpec struct {
	name           string
	columnType     string
	notNull        bool
	primaryKey     bool
	requireDefault bool
	defaultValue   string
}

type indexSpec struct {
	name    string
	table   string
	columns []string
	unique  bool
}

type triggerSpec struct {
	name   string
	table  string
	checks []string
}

var stableBaselineTableSpecs = []tableSpec{
	{
		name:   "providers",
		strict: true,
		columns: []columnSpec{
			{name: "id", columnType: "TEXT", primaryKey: true},
			{name: "name", columnType: "TEXT", notNull: true},
			{name: "adapter_id", columnType: "TEXT", notNull: true},
			{name: "metadata_json", columnType: "TEXT", notNull: true, requireDefault: true, defaultValue: "'{}'"},
			{name: "created_at_unix_ms", columnType: "INTEGER", notNull: true},
			{name: "updated_at_unix_ms", columnType: "INTEGER", notNull: true},
		},
		checks: []string{
			"CHECK (CASE WHEN json_valid(metadata_json) THEN json_type(metadata_json) = 'object' ELSE 0 END)",
		},
	},
	{
		name:   "profiles",
		strict: true,
		columns: []columnSpec{
			{name: "id", columnType: "TEXT", primaryKey: true},
			{name: "name", columnType: "TEXT", notNull: true},
			{name: "description", columnType: "TEXT", notNull: true, requireDefault: true, defaultValue: "''"},
			{name: "metadata_json", columnType: "TEXT", notNull: true, requireDefault: true, defaultValue: "'{}'"},
			{name: "created_at_unix_ms", columnType: "INTEGER", notNull: true},
			{name: "updated_at_unix_ms", columnType: "INTEGER", notNull: true},
		},
		checks: []string{
			"CHECK (CASE WHEN json_valid(metadata_json) THEN json_type(metadata_json) = 'object' ELSE 0 END)",
		},
	},
	{
		name:   "settings",
		strict: true,
		columns: []columnSpec{
			{name: "key", columnType: "TEXT", primaryKey: true},
			{name: "value_json", columnType: "TEXT", notNull: true},
			{name: "updated_at_unix_ms", columnType: "INTEGER", notNull: true},
		},
		checks: []string{"CHECK (json_valid(value_json))"},
	},
	{
		name:   "provider_settings",
		strict: true,
		columns: []columnSpec{
			{name: "provider_id", columnType: "TEXT", notNull: true, primaryKey: true},
			{name: "schema_version", columnType: "INTEGER", notNull: true},
			{name: "settings_json", columnType: "TEXT", notNull: true, requireDefault: true, defaultValue: "'{}'"},
			{name: "updated_at_unix_ms", columnType: "INTEGER", notNull: true},
		},
		checks: []string{
			"CHECK (schema_version > 0)",
			"CHECK (CASE WHEN json_valid(settings_json) THEN json_type(settings_json) = 'object' ELSE 0 END)",
			"FOREIGN KEY (provider_id) REFERENCES providers(id) ON UPDATE RESTRICT ON DELETE CASCADE",
		},
	},
	{
		name:   "provider_profile_settings",
		strict: true,
		columns: []columnSpec{
			{name: "profile_id", columnType: "TEXT", notNull: true, primaryKey: true},
			{name: "provider_id", columnType: "TEXT", notNull: true, primaryKey: true},
			{name: "schema_version", columnType: "INTEGER", notNull: true},
			{name: "settings_json", columnType: "TEXT", notNull: true, requireDefault: true, defaultValue: "'{}'"},
			{name: "updated_at_unix_ms", columnType: "INTEGER", notNull: true},
		},
		checks: []string{
			"CHECK (schema_version > 0)",
			"CHECK (CASE WHEN json_valid(settings_json) THEN json_type(settings_json) = 'object' ELSE 0 END)",
			"FOREIGN KEY (profile_id) REFERENCES profiles(id) ON UPDATE RESTRICT ON DELETE CASCADE",
			"FOREIGN KEY (provider_id) REFERENCES providers(id) ON UPDATE RESTRICT ON DELETE CASCADE",
		},
	},
	{
		name:   "system_state",
		strict: true,
		columns: []columnSpec{
			{name: "key", columnType: "TEXT", notNull: true, primaryKey: true},
			{name: "value_json", columnType: "TEXT", notNull: true},
			{name: "created_at_unix_ms", columnType: "INTEGER", notNull: true},
			{name: "updated_at_unix_ms", columnType: "INTEGER", notNull: true},
		},
		checks: []string{"CHECK (json_valid(value_json))"},
	},
	{
		name:   "provider_active_states",
		strict: true,
		columns: []columnSpec{
			{name: "provider_id", columnType: "TEXT", notNull: true, primaryKey: true},
			{name: "profile_id", columnType: "TEXT", notNull: true},
			{name: "revision", columnType: "INTEGER", notNull: true},
			{name: "updated_at_unix_ms", columnType: "INTEGER", notNull: true},
		},
		checks: []string{
			"CHECK (revision > 0)",
			"FOREIGN KEY (provider_id) REFERENCES providers(id) ON UPDATE RESTRICT ON DELETE CASCADE",
			"FOREIGN KEY (profile_id) REFERENCES profiles(id) ON UPDATE RESTRICT ON DELETE RESTRICT",
		},
	},
	{
		name:   "operations",
		strict: true,
		columns: []columnSpec{
			{name: "id", columnType: "TEXT", primaryKey: true},
			{name: "provider_id", columnType: "TEXT", notNull: true},
			{name: "operation_type", columnType: "TEXT", notNull: true},
			{name: "status", columnType: "TEXT", notNull: true},
			{name: "source_operation_id", columnType: "TEXT"},
			{name: "metadata_schema_version", columnType: "INTEGER", notNull: true},
			{name: "metadata_json", columnType: "TEXT", notNull: true, requireDefault: true, defaultValue: "'{}'"},
			{name: "error_code", columnType: "TEXT", notNull: true, requireDefault: true, defaultValue: "''"},
			{name: "error_message", columnType: "TEXT", notNull: true, requireDefault: true, defaultValue: "''"},
			{name: "resolution_kind", columnType: "TEXT", notNull: true, requireDefault: true, defaultValue: "''"},
			{name: "resolved_at_unix_ms", columnType: "INTEGER", notNull: true, requireDefault: true, defaultValue: "0"},
			{name: "created_at_unix_ms", columnType: "INTEGER", notNull: true},
			{name: "updated_at_unix_ms", columnType: "INTEGER", notNull: true},
		},
		checks: []string{
			"CHECK (operation_type IN ('switch', 'recovery', 'import', 'maintenance'))",
			"CHECK (status IN ('pending', 'failed', 'applied'))",
			"CHECK (metadata_schema_version > 0)",
			"CHECK (CASE WHEN json_valid(metadata_json) THEN json_type(metadata_json) = 'object' ELSE 0 END)",
			"CHECK (resolved_at_unix_ms >= 0)",
			"UNIQUE (provider_id, id)",
			"CHECK (source_operation_id IS NULL OR source_operation_id <> id)",
			"CHECK ((operation_type = 'recovery' AND source_operation_id IS NOT NULL) OR (operation_type <> 'recovery' AND source_operation_id IS NULL))",
			"FOREIGN KEY (provider_id) REFERENCES providers(id) ON UPDATE RESTRICT ON DELETE RESTRICT",
			"FOREIGN KEY (provider_id, source_operation_id) REFERENCES operations(provider_id, id) ON UPDATE RESTRICT ON DELETE CASCADE",
		},
	},
	{
		name:   "operation_profiles",
		strict: true,
		columns: []columnSpec{
			{name: "operation_id", columnType: "TEXT", notNull: true, primaryKey: true},
			{name: "profile_id", columnType: "TEXT", notNull: true, primaryKey: true},
		},
		checks: []string{
			"FOREIGN KEY (operation_id) REFERENCES operations(id) ON UPDATE RESTRICT ON DELETE CASCADE",
			"FOREIGN KEY (profile_id) REFERENCES profiles(id) ON UPDATE RESTRICT ON DELETE RESTRICT",
		},
	},
	{
		name:   "profile_targets",
		strict: true,
		columns: []columnSpec{
			{name: "profile_id", columnType: "TEXT", notNull: true, primaryKey: true},
			{name: "provider_id", columnType: "TEXT", notNull: true, primaryKey: true},
			{name: "target_id", columnType: "TEXT", notNull: true, primaryKey: true},
			{name: "path", columnType: "TEXT", notNull: true},
			{name: "path_key", columnType: "TEXT", notNull: true},
			{name: "format", columnType: "TEXT", notNull: true},
			{name: "strategy", columnType: "TEXT", notNull: true},
			{name: "value_json", columnType: "TEXT", notNull: true},
			{name: "enabled", columnType: "INTEGER", notNull: true, requireDefault: true, defaultValue: "1"},
			{name: "metadata_json", columnType: "TEXT", notNull: true, requireDefault: true, defaultValue: "'{}'"},
			{name: "created_at_unix_ms", columnType: "INTEGER", notNull: true},
			{name: "updated_at_unix_ms", columnType: "INTEGER", notNull: true},
		},
		checks: []string{
			"CHECK (CASE WHEN json_valid(value_json) THEN json_type(value_json) = 'object' ELSE 0 END)",
			"CHECK (enabled IN (0, 1))",
			"CHECK (CASE WHEN json_valid(metadata_json) THEN json_type(metadata_json) = 'object' ELSE 0 END)",
			"FOREIGN KEY (profile_id) REFERENCES profiles(id) ON UPDATE RESTRICT ON DELETE CASCADE",
			"FOREIGN KEY (provider_id) REFERENCES providers(id) ON UPDATE RESTRICT ON DELETE CASCADE",
		},
	},
	{
		name:   "usage_sources",
		strict: true,
		columns: []columnSpec{
			{name: "id", columnType: "INTEGER", primaryKey: true},
			{name: "provider_id", columnType: "TEXT", notNull: true},
			{name: "source_key", columnType: "TEXT", notNull: true},
			{name: "identity_revision", columnType: "INTEGER", notNull: true},
			{name: "sync_generation", columnType: "INTEGER", notNull: true, requireDefault: true, defaultValue: "0"},
			{name: "last_completed_at_unix_ms", columnType: "INTEGER", notNull: true, requireDefault: true, defaultValue: "0"},
			{name: "tracked_units", columnType: "INTEGER", notNull: true, requireDefault: true, defaultValue: "0"},
			{name: "invalid_records", columnType: "INTEGER", notNull: true, requireDefault: true, defaultValue: "0"},
			{name: "unsupported_records", columnType: "INTEGER", notNull: true, requireDefault: true, defaultValue: "0"},
		},
		checks: []string{
			"CHECK (identity_revision > 0)",
			"CHECK (sync_generation >= 0)",
			"CHECK (last_completed_at_unix_ms >= 0)",
			"CHECK (tracked_units >= 0)",
			"CHECK (invalid_records >= 0)",
			"CHECK (unsupported_records >= 0)",
			"FOREIGN KEY (provider_id) REFERENCES providers(id) ON UPDATE RESTRICT ON DELETE CASCADE",
		},
	},
	{
		name:   "usage_sessions",
		strict: true,
		columns: []columnSpec{
			{name: "id", columnType: "INTEGER", primaryKey: true},
			{name: "source_id", columnType: "INTEGER", notNull: true},
			{name: "session_key", columnType: "TEXT", notNull: true},
		},
		checks: []string{
			"CHECK (length(session_key) BETWEEN 1 AND 256)",
			"UNIQUE (source_id, id)",
			"FOREIGN KEY (source_id) REFERENCES usage_sources(id) ON UPDATE RESTRICT ON DELETE CASCADE",
		},
	},
	{
		name:   "usage_models",
		strict: true,
		columns: []columnSpec{
			{name: "id", columnType: "INTEGER", primaryKey: true},
			{name: "source_id", columnType: "INTEGER", notNull: true},
			{name: "model_key", columnType: "TEXT", notNull: true},
		},
		checks: []string{
			"CHECK (length(model_key) BETWEEN 1 AND 200 AND model_key NOT GLOB '*[^A-Za-z0-9._:/@-]*')",
			"UNIQUE (source_id, id)",
			"FOREIGN KEY (source_id) REFERENCES usage_sources(id) ON UPDATE RESTRICT ON DELETE CASCADE",
		},
	},
	{
		name:   "usage_facts",
		strict: true,
		columns: []columnSpec{
			{name: "id", columnType: "INTEGER", primaryKey: true},
			{name: "event_key", columnType: "BLOB", notNull: true},
			{name: "source_id", columnType: "INTEGER", notNull: true},
			{name: "session_id", columnType: "INTEGER"},
			{name: "model_id", columnType: "INTEGER", notNull: true},
			{name: "occurred_at_unix_ms", columnType: "INTEGER", notNull: true, requireDefault: true, defaultValue: "0"},
			{name: "input_tokens", columnType: "INTEGER", notNull: true, requireDefault: true, defaultValue: "0"},
			{name: "cached_input_tokens", columnType: "INTEGER", notNull: true, requireDefault: true, defaultValue: "0"},
			{name: "output_tokens", columnType: "INTEGER", notNull: true, requireDefault: true, defaultValue: "0"},
			{name: "total_tokens", columnType: "INTEGER", notNull: true, requireDefault: true, defaultValue: "0"},
			{name: "estimated_cost_micros", columnType: "INTEGER"},
			{name: "cost_status", columnType: "INTEGER", notNull: true},
		},
		checks: []string{
			"CHECK (typeof(event_key) = 'blob' AND length(event_key) = 32 AND event_key <> zeroblob(32))",
			"CHECK (occurred_at_unix_ms >= 0)",
			"CHECK (input_tokens >= 0)",
			"CHECK (cached_input_tokens >= 0 AND cached_input_tokens <= input_tokens)",
			"CHECK (output_tokens >= 0)",
			"CHECK (total_tokens >= 0)",
			"CHECK (estimated_cost_micros IS NULL OR estimated_cost_micros >= 0)",
			"CHECK (cost_status IN (0, 1, 2))",
			"FOREIGN KEY (source_id) REFERENCES usage_sources(id) ON UPDATE RESTRICT ON DELETE CASCADE",
			"FOREIGN KEY (source_id, session_id) REFERENCES usage_sessions(source_id, id) ON UPDATE RESTRICT ON DELETE RESTRICT",
			"FOREIGN KEY (source_id, model_id) REFERENCES usage_models(source_id, id) ON UPDATE RESTRICT ON DELETE RESTRICT",
			"CHECK ((cost_status IN (1, 2) AND estimated_cost_micros IS NOT NULL) OR (cost_status = 0 AND estimated_cost_micros IS NULL))",
		},
	},
	{
		name:   "codex_usage_import_files",
		strict: true,
		columns: []columnSpec{
			{name: "source_id", columnType: "INTEGER", notNull: true, primaryKey: true},
			{name: "file_key", columnType: "BLOB", notNull: true, primaryKey: true},
			{name: "modified_unix_ms", columnType: "INTEGER", notNull: true, requireDefault: true, defaultValue: "0"},
			{name: "size_bytes", columnType: "INTEGER", notNull: true, requireDefault: true, defaultValue: "0"},
			{name: "imported_facts", columnType: "INTEGER", notNull: true, requireDefault: true, defaultValue: "0"},
			{name: "invalid_lines", columnType: "INTEGER", notNull: true, requireDefault: true, defaultValue: "0"},
			{name: "unsupported_lines", columnType: "INTEGER", notNull: true, requireDefault: true, defaultValue: "0"},
			{name: "parser_revision", columnType: "INTEGER", notNull: true},
			{name: "identity_revision", columnType: "INTEGER", notNull: true},
			{name: "event_digest", columnType: "BLOB", notNull: true},
			{name: "updated_at_unix_ms", columnType: "INTEGER", notNull: true},
		},
		checks: []string{
			"CHECK (typeof(file_key) = 'blob' AND length(file_key) = 32 AND file_key <> zeroblob(32))",
			"CHECK (modified_unix_ms >= 0)",
			"CHECK (size_bytes >= 0)",
			"CHECK (imported_facts >= 0)",
			"CHECK (invalid_lines >= 0)",
			"CHECK (unsupported_lines >= 0)",
			"CHECK (parser_revision > 0)",
			"CHECK (identity_revision > 0)",
			"CHECK (typeof(event_digest) = 'blob' AND length(event_digest) = 32 AND event_digest <> zeroblob(32))",
			"CHECK (updated_at_unix_ms >= 0)",
			"FOREIGN KEY (source_id) REFERENCES usage_sources(id) ON UPDATE RESTRICT ON DELETE CASCADE",
			"WITHOUT ROWID",
		},
	},
	{
		name:   "provider_credentials",
		strict: true,
		columns: []columnSpec{
			{name: "id", columnType: "TEXT", primaryKey: true},
			{name: "provider_id", columnType: "TEXT", notNull: true},
			{name: "credential_kind", columnType: "TEXT", notNull: true},
			{name: "payload_json", columnType: "TEXT", notNull: true},
			{name: "payload_sha256", columnType: "TEXT", notNull: true},
			{name: "metadata_json", columnType: "TEXT", notNull: true, requireDefault: true, defaultValue: "'{}'"},
			{name: "created_at_unix_ms", columnType: "INTEGER", notNull: true},
			{name: "updated_at_unix_ms", columnType: "INTEGER", notNull: true},
		},
		checks: []string{
			"CHECK (CASE WHEN json_valid(payload_json) THEN json_type(payload_json) = 'object' ELSE 0 END)",
			"CHECK (length(payload_sha256) = 64 AND payload_sha256 NOT GLOB '*[^0-9a-f]*')",
			"CHECK (CASE WHEN json_valid(metadata_json) THEN json_type(metadata_json) = 'object' ELSE 0 END)",
			"UNIQUE (provider_id, id)",
			"FOREIGN KEY (provider_id) REFERENCES providers(id) ON UPDATE RESTRICT ON DELETE CASCADE",
		},
	},
	{
		name:   "provider_config_sets",
		strict: true,
		columns: []columnSpec{
			{name: "provider_id", columnType: "TEXT", notNull: true, primaryKey: true},
			{name: "id", columnType: "TEXT", notNull: true, primaryKey: true},
			{name: "config_kind", columnType: "TEXT", notNull: true},
			{name: "name", columnType: "TEXT", notNull: true},
			{name: "description", columnType: "TEXT", notNull: true, requireDefault: true, defaultValue: "''"},
			{name: "payload_text", columnType: "TEXT", notNull: true},
			{name: "payload_sha256", columnType: "TEXT", notNull: true},
			{name: "metadata_json", columnType: "TEXT", notNull: true, requireDefault: true, defaultValue: "'{}'"},
			{name: "created_at_unix_ms", columnType: "INTEGER", notNull: true},
			{name: "updated_at_unix_ms", columnType: "INTEGER", notNull: true},
		},
		checks: []string{
			"CHECK (length(payload_sha256) = 64 AND payload_sha256 NOT GLOB '*[^0-9a-f]*')",
			"CHECK (CASE WHEN json_valid(metadata_json) THEN json_type(metadata_json) = 'object' ELSE 0 END)",
			"FOREIGN KEY (provider_id) REFERENCES providers(id) ON UPDATE RESTRICT ON DELETE CASCADE",
		},
	},
	{
		name:   "profile_credential_bindings",
		strict: true,
		columns: []columnSpec{
			{name: "profile_id", columnType: "TEXT", notNull: true, primaryKey: true},
			{name: "provider_id", columnType: "TEXT", notNull: true, primaryKey: true},
			{name: "slot_id", columnType: "TEXT", notNull: true, primaryKey: true},
			{name: "credential_id", columnType: "TEXT", notNull: true},
			{name: "created_at_unix_ms", columnType: "INTEGER", notNull: true},
			{name: "updated_at_unix_ms", columnType: "INTEGER", notNull: true},
		},
		checks: []string{
			"FOREIGN KEY (profile_id) REFERENCES profiles(id) ON UPDATE RESTRICT ON DELETE CASCADE",
			"FOREIGN KEY (provider_id) REFERENCES providers(id) ON UPDATE RESTRICT ON DELETE CASCADE",
			"FOREIGN KEY (provider_id, credential_id) REFERENCES provider_credentials(provider_id, id) ON UPDATE RESTRICT ON DELETE RESTRICT",
		},
	},
	{
		name:   "profile_config_set_bindings",
		strict: true,
		columns: []columnSpec{
			{name: "profile_id", columnType: "TEXT", notNull: true, primaryKey: true},
			{name: "provider_id", columnType: "TEXT", notNull: true, primaryKey: true},
			{name: "slot_id", columnType: "TEXT", notNull: true, primaryKey: true},
			{name: "config_set_id", columnType: "TEXT", notNull: true},
			{name: "created_at_unix_ms", columnType: "INTEGER", notNull: true},
			{name: "updated_at_unix_ms", columnType: "INTEGER", notNull: true},
		},
		checks: []string{
			"FOREIGN KEY (profile_id) REFERENCES profiles(id) ON UPDATE RESTRICT ON DELETE CASCADE",
			"FOREIGN KEY (provider_id) REFERENCES providers(id) ON UPDATE RESTRICT ON DELETE CASCADE",
			"FOREIGN KEY (provider_id, config_set_id) REFERENCES provider_config_sets(provider_id, id) ON UPDATE RESTRICT ON DELETE RESTRICT",
		},
	},
}

var stableBaselineIndexSpecs = []indexSpec{
	{name: "idx_providers_adapter_id", table: "providers", columns: []string{"adapter_id"}},
	{name: "idx_provider_profile_settings_provider_id", table: "provider_profile_settings", columns: []string{"provider_id"}},
	{name: "idx_provider_active_states_profile_id", table: "provider_active_states", columns: []string{"profile_id"}},
	{name: "idx_operations_provider_status_updated", table: "operations", columns: []string{"provider_id", "status", "updated_at_unix_ms"}},
	{name: "idx_operations_operation_type", table: "operations", columns: []string{"operation_type"}},
	{name: "idx_operations_source_operation_id", table: "operations", columns: []string{"source_operation_id"}},
	{name: "idx_operation_profiles_profile_id", table: "operation_profiles", columns: []string{"profile_id"}},
	{name: "idx_profile_targets_profile_id", table: "profile_targets", columns: []string{"profile_id"}},
	{name: "idx_profile_targets_provider_id", table: "profile_targets", columns: []string{"provider_id"}},
	{name: "idx_profile_targets_enabled", table: "profile_targets", columns: []string{"enabled"}},
	{name: "idx_profile_targets_unique_path", table: "profile_targets", columns: []string{"profile_id", "provider_id", "path_key"}, unique: true},
	{name: "idx_profile_targets_path_key", table: "profile_targets", columns: []string{"path_key"}},
	{name: "idx_usage_sources_provider_source", table: "usage_sources", columns: []string{"provider_id", "source_key"}, unique: true},
	{name: "idx_usage_sessions_source_session", table: "usage_sessions", columns: []string{"source_id", "session_key"}, unique: true},
	{name: "idx_usage_models_source_model", table: "usage_models", columns: []string{"source_id", "model_key"}, unique: true},
	{name: "idx_usage_facts_event_key", table: "usage_facts", columns: []string{"event_key"}, unique: true},
	{name: "idx_usage_facts_source_time", table: "usage_facts", columns: []string{"source_id", "occurred_at_unix_ms"}},
	{name: "idx_usage_facts_source_cost_model_id", table: "usage_facts", columns: []string{"source_id", "cost_status", "model_id", "id"}},
	{name: "idx_provider_credentials_provider_id", table: "provider_credentials", columns: []string{"provider_id"}},
	{name: "idx_provider_credentials_kind", table: "provider_credentials", columns: []string{"credential_kind"}},
	{name: "idx_provider_config_sets_kind", table: "provider_config_sets", columns: []string{"config_kind"}},
	{name: "idx_profile_credential_bindings_provider_id", table: "profile_credential_bindings", columns: []string{"provider_id"}},
	{name: "idx_profile_credential_bindings_credential_id", table: "profile_credential_bindings", columns: []string{"credential_id"}},
	{name: "idx_profile_config_set_bindings_provider_id", table: "profile_config_set_bindings", columns: []string{"provider_id"}},
	{name: "idx_profile_config_set_bindings_config_set_id", table: "profile_config_set_bindings", columns: []string{"config_set_id"}},
}

var stableBaselineTriggerSpecs = []triggerSpec{
	{
		name:  "trg_profile_targets_path_owner_insert",
		table: "profile_targets",
		checks: []string{
			"BEFORE INSERT ON profile_targets",
			"path_key = NEW.path_key",
			"provider_id <> NEW.provider_id",
			"target_id <> NEW.target_id",
			"RAISE(ABORT, '" + profileTargetPathOwnerMessage + "')",
		},
	},
	{
		name:  "trg_profile_targets_path_owner_update",
		table: "profile_targets",
		checks: []string{
			"BEFORE UPDATE OF path, path_key, provider_id, target_id ON profile_targets",
			"path_key = NEW.path_key",
			"profile_id = OLD.profile_id",
			"provider_id = OLD.provider_id",
			"target_id = OLD.target_id",
			"provider_id <> NEW.provider_id",
			"target_id <> NEW.target_id",
			"RAISE(ABORT, '" + profileTargetPathOwnerMessage + "')",
		},
	},
}
