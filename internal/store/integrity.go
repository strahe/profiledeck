package store

import (
	"context"
	"errors"

	bunmigrate "github.com/uptrace/bun/migrate"

	"github.com/strahe/profiledeck/internal/store/migrations"
)

const (
	IntegrityIssueQuickCheck  = "sqlite_quick_check"
	IntegrityIssueForeignKeys = "foreign_keys"
	IntegrityIssueSchema      = "schema"
	IntegrityIssueJSON        = "json"
	IntegrityIssueReferences  = "references"
	IntegrityIssueSystemState = "system_state"
)

type IntegrityScope string

const (
	IntegrityAppliedBaseline IntegrityScope = "applied_baseline"
	IntegrityCurrentBaseline IntegrityScope = "current_baseline"
)

// MigrationState deliberately exposes only counts. Migration names are
// implementation details and may reveal a database version at output boundaries.
type MigrationState struct {
	Applied           int
	Pending           int
	Current           bool
	HasMigrationTable bool
}

type IntegrityIssue struct {
	Kind  string
	Count int
}

// IntegrityReport contains categories and counts only. Callers must not attach
// row identifiers, stored values, or raw SQLite diagnostics to user output.
type IntegrityReport struct {
	Healthy   bool
	Migration MigrationState
	Issues    []IntegrityIssue
}

type schemaContract struct {
	migrationKey     string
	tables           []string
	indexes          []string
	triggers         []string
	jsonQueries      []string
	referenceQueries []string
	stateQueries     []string
}

var (
	migrationInfrastructureStatements = []string{
		`CREATE TABLE IF NOT EXISTS bun_migrations (
			"id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
			"name" VARCHAR,
			"group_id" INTEGER,
			"migrated_at" TIMESTAMP NOT NULL DEFAULT current_timestamp
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS "bun_migrations_name_unique" ON bun_migrations ("name")`,
		`CREATE TABLE IF NOT EXISTS bun_migration_locks (
			"id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
			"table_name" VARCHAR,
			UNIQUE ("table_name")
		)`,
	}
	migrationHistoryTableSpec = tableSpec{
		name: "bun_migrations",
		columns: []columnSpec{
			{name: "id", columnType: "INTEGER", notNull: true, primaryKey: true},
			{name: "name", columnType: "VARCHAR"},
			{name: "group_id", columnType: "INTEGER"},
			{name: "migrated_at", columnType: "TIMESTAMP", notNull: true, requireDefault: true, defaultValue: "current_timestamp"},
		},
		checks: []string{"AUTOINCREMENT"},
	}
	migrationHistoryNameIndexSpec = indexSpec{
		name: "bun_migrations_name_unique", table: "bun_migrations", columns: []string{"name"}, unique: true,
	}
	migrationLockTableSpec = tableSpec{
		name: "bun_migration_locks",
		columns: []columnSpec{
			{name: "id", columnType: "INTEGER", notNull: true, primaryKey: true},
			{name: "table_name", columnType: "VARCHAR"},
		},
		checks: []string{"AUTOINCREMENT", `UNIQUE ("table_name")`},
	}
)

var schemaContracts = []schemaContract{
	{
		migrationKey: "initial_schema",
		tables: []string{
			"providers", "profiles", "provider_profile_settings", "settings",
			"active_states", "operations", "provider_credentials",
			"provider_config_sets", "profile_credential_bindings",
			"profile_config_set_bindings",
		},
		indexes: []string{
			"idx_providers_adapter_id", "idx_providers_enabled",
			"idx_provider_profile_settings_provider_id", "idx_operations_status",
			"idx_operations_operation_type", "idx_provider_credentials_provider_id",
			"idx_provider_credentials_kind", "idx_provider_credentials_provider_id_id",
			"idx_provider_config_sets_provider_id", "idx_provider_config_sets_kind",
			"idx_provider_config_sets_provider_id_id",
			"idx_profile_credential_bindings_provider_id",
			"idx_profile_credential_bindings_credential_id",
			"idx_profile_config_set_bindings_provider_id",
			"idx_profile_config_set_bindings_config_set_id",
		},
		jsonQueries: []string{
			`SELECT COUNT(1) FROM providers WHERE NOT (` + jsonObjectExpression("metadata_json") + `)`,
			`SELECT COUNT(1) FROM profiles WHERE NOT (` + jsonObjectExpression("metadata_json") + `)`,
			`SELECT COUNT(1) FROM settings WHERE json_valid(value_json) = 0`,
			`SELECT COUNT(1) FROM operations WHERE NOT (` + jsonObjectExpression("metadata_json") + `)`,
			`SELECT COUNT(1) FROM provider_credentials WHERE NOT (` + jsonObjectExpression("payload_json") + `) OR NOT (` + jsonObjectExpression("metadata_json") + `)`,
			`SELECT COUNT(1) FROM provider_config_sets WHERE NOT (` + jsonObjectExpression("metadata_json") + `)`,
		},
		referenceQueries: []string{
			`SELECT COUNT(1) FROM provider_profile_settings AS value
				WHERE NOT EXISTS (SELECT 1 FROM profiles WHERE profiles.id = value.profile_id)
					OR NOT EXISTS (SELECT 1 FROM providers WHERE providers.id = value.provider_id)`,
			`SELECT COUNT(1) FROM active_states AS value
				WHERE value.scope_type <> 'provider'
					OR NOT EXISTS (SELECT 1 FROM providers WHERE providers.id = value.scope_id)
					OR (value.profile_id <> '' AND NOT EXISTS (SELECT 1 FROM profiles WHERE profiles.id = value.profile_id))
					OR (value.operation_id <> '' AND NOT EXISTS (SELECT 1 FROM operations WHERE operations.id = value.operation_id))`,
			`SELECT COUNT(1) FROM provider_credentials AS value
				WHERE NOT EXISTS (SELECT 1 FROM providers WHERE providers.id = value.provider_id)`,
			`SELECT COUNT(1) FROM provider_config_sets AS value
				WHERE NOT EXISTS (SELECT 1 FROM providers WHERE providers.id = value.provider_id)`,
		},
	},
	{
		migrationKey: "profile_targets",
		tables:       []string{"profile_targets"},
		indexes:      []string{"idx_profile_targets_profile_id", "idx_profile_targets_provider_id", "idx_profile_targets_enabled", "idx_profile_targets_unique_path"},
		triggers:     []string{"trg_profile_targets_path_owner_insert", "trg_profile_targets_path_owner_update"},
		jsonQueries: []string{
			`SELECT COUNT(1) FROM profile_targets WHERE NOT (` + jsonObjectExpression("value_json") + `) OR NOT (` + jsonObjectExpression("metadata_json") + `)`,
		},
		referenceQueries: []string{
			`SELECT COUNT(1) FROM profile_targets AS value
				WHERE NOT EXISTS (SELECT 1 FROM profiles WHERE profiles.id = value.profile_id)
					OR NOT EXISTS (SELECT 1 FROM providers WHERE providers.id = value.provider_id)`,
		},
	},
	{
		migrationKey: "usage",
		tables: []string{
			"usage_sources", "usage_sessions", "usage_models", "usage_facts",
			"codex_usage_import_files",
		},
		indexes: []string{
			"idx_usage_sources_provider_source", "idx_usage_sessions_source_session",
			"idx_usage_models_source_model", "idx_usage_facts_event_key",
			"idx_usage_facts_source_time", "idx_usage_facts_source_cost_model_id",
		},
		referenceQueries: []string{
			`SELECT COUNT(1) FROM usage_sessions AS value
				WHERE NOT EXISTS (SELECT 1 FROM usage_sources WHERE usage_sources.id = value.source_id)`,
			`SELECT COUNT(1) FROM usage_models AS value
				WHERE NOT EXISTS (SELECT 1 FROM usage_sources WHERE usage_sources.id = value.source_id)`,
			`SELECT COUNT(1) FROM usage_facts AS value
				WHERE NOT EXISTS (SELECT 1 FROM usage_sources WHERE usage_sources.id = value.source_id)
					OR (value.session_id IS NOT NULL AND NOT EXISTS (
						SELECT 1 FROM usage_sessions
						WHERE usage_sessions.id = value.session_id AND usage_sessions.source_id = value.source_id
					))
					OR NOT EXISTS (
						SELECT 1 FROM usage_models
						WHERE usage_models.id = value.model_id AND usage_models.source_id = value.source_id
					)`,
			`SELECT COUNT(1) FROM codex_usage_import_files AS value
				WHERE NOT EXISTS (
					SELECT 1 FROM usage_sources
					WHERE usage_sources.id = value.source_id
						AND usage_sources.provider_id = 'codex'
						AND usage_sources.identity_revision = value.identity_revision
				)`,
		},
	},
	{
		migrationKey: "system_state",
		tables:       []string{"system_state"},
		jsonQueries: []string{
			`SELECT COUNT(1) FROM system_state WHERE json_valid(value_json) = 0`,
		},
		stateQueries: []string{
			`SELECT COUNT(1) FROM system_state
				WHERE key <> 'recovery.cleanup_required'
					OR CASE
						WHEN json_valid(value_json) = 0 THEN 1
						WHEN json_type(value_json) = 'true' THEN 0
						ELSE 1
					END = 1`,
		},
	},
	{
		migrationKey: "profile_target_path_lookup",
		indexes:      []string{"idx_profile_targets_path_key"},
	},
}

func jsonObjectExpression(column string) string {
	// CASE prevents json_type from evaluating malformed JSON.
	return `CASE WHEN json_valid(` + column + `) = 0 THEN 0 WHEN json_type(` + column + `) = 'object' THEN 1 ELSE 0 END = 1`
}

func validateMigrationIntegrityContractRegistry(registered bunmigrate.MigrationSlice) error {
	if len(registered) != len(schemaContracts) {
		return errors.New("migration integrity contract registry is incomplete")
	}
	seenMigrationKeys := make(map[string]struct{}, len(registered))
	for index, migration := range registered {
		// Bun owns numeric migration identities; the filename-derived comment
		// binds each ordered migration to its versioned integrity contract.
		if _, exists := seenMigrationKeys[migration.Comment]; exists {
			return errors.New("migration integrity contract registry contains duplicate keys")
		}
		seenMigrationKeys[migration.Comment] = struct{}{}
		if schemaContracts[index].migrationKey != migration.Comment {
			return errors.New("migration integrity contract registry is out of order")
		}
	}
	return nil
}

// MigrationState validates that applied migrations are exactly a continuous
// prefix of the migrations known to this binary.
func (s *Store) MigrationState(ctx context.Context) (MigrationState, error) {
	registered := migrations.Migrations.Sorted()
	if err := validateMigrationIntegrityContractRegistry(registered); err != nil {
		return MigrationState{}, err
	}
	state := MigrationState{Pending: len(registered), Current: len(registered) == 0}
	migrationTableExists, lockTableExists, err := s.migrationInfrastructureTablePresence(ctx)
	if err != nil {
		return state, err
	}
	if !migrationTableExists {
		if lockTableExists {
			return MigrationState{}, ErrInvalidMigrationHistory
		}
		return state, nil
	}
	state.HasMigrationTable = true
	infrastructureHealthy, err := s.migrationInfrastructureHealthy(ctx)
	if err != nil {
		return MigrationState{}, err
	}
	if !infrastructureHealthy {
		return MigrationState{}, ErrInvalidMigrationHistory
	}
	var invalidMarkers int
	if err := s.executor().QueryRowContext(ctx, `
		SELECT COUNT(1) FROM bun_migrations
		WHERE id <= 0 OR name IS NULL OR name = '' OR group_id IS NULL OR group_id <= 0
	`).Scan(&invalidMarkers); err != nil {
		return MigrationState{}, err
	}
	if invalidMarkers > 0 {
		return MigrationState{}, ErrInvalidMigrationHistory
	}

	known := make(map[string]int, len(registered))
	for index, migration := range registered {
		known[migration.Name] = index
	}
	applied := make([]bool, len(registered))
	rows, err := s.executor().QueryContext(ctx, `SELECT name FROM bun_migrations`)
	if err != nil {
		return MigrationState{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return MigrationState{}, err
		}
		index, ok := known[name]
		if !ok {
			return MigrationState{}, ErrUnsupportedSchema
		}
		if applied[index] {
			return MigrationState{}, ErrInvalidMigrationHistory
		}
		applied[index] = true
	}
	if err := rows.Err(); err != nil {
		return MigrationState{}, err
	}

	appliedCount := 0
	for _, isApplied := range applied {
		if !isApplied {
			break
		}
		appliedCount++
	}
	for _, isApplied := range applied[appliedCount:] {
		if isApplied {
			return MigrationState{}, ErrInvalidMigrationHistory
		}
	}
	state.Applied = appliedCount
	state.Pending = len(registered) - appliedCount
	state.Current = state.Pending == 0
	return state, nil
}

func (s *Store) migrationInfrastructureTablePresence(ctx context.Context) (bool, bool, error) {
	var migrationTables, lockTables int
	// Read both objects from one SQLite snapshot. Separate existence queries can
	// straddle another process's atomic initialization commit and invent a
	// malformed half-old, half-new infrastructure state.
	err := s.executor().QueryRowContext(ctx, `
		SELECT
			COUNT(CASE WHEN name = 'bun_migrations' THEN 1 END),
			COUNT(CASE WHEN name = 'bun_migration_locks' THEN 1 END)
		FROM sqlite_master
		WHERE type = 'table' AND name IN ('bun_migrations', 'bun_migration_locks')
	`).Scan(&migrationTables, &lockTables)
	return migrationTables == 1, lockTables == 1, err
}

func (s *Store) migrationInfrastructureHealthy(ctx context.Context) (bool, error) {
	for _, spec := range []tableSpec{migrationHistoryTableSpec, migrationLockTableSpec} {
		healthy, err := s.tableSchemaHealthy(ctx, spec)
		if err != nil || !healthy {
			return false, err
		}
	}
	return s.indexSchemaHealthy(ctx, migrationHistoryNameIndexSpec)
}

// InspectIntegrity is the shared versioned contract for bootstrap, Doctor,
// application backups, and restore candidates. Ordinary Store opens stay cheap.
func (s *Store) InspectIntegrity(ctx context.Context, scope IntegrityScope) (IntegrityReport, error) {
	state, err := s.MigrationState(ctx)
	if err != nil {
		return IntegrityReport{}, err
	}
	report := IntegrityReport{Healthy: true, Migration: state, Issues: []IntegrityIssue{}}
	addIssue := func(kind string, count int) {
		if count <= 0 {
			return
		}
		for _, issue := range report.Issues {
			if issue.Kind == kind {
				return
			}
		}
		report.Healthy = false
		report.Issues = append(report.Issues, IntegrityIssue{Kind: kind, Count: count})
	}

	if err := s.QuickCheck(ctx); err != nil {
		if !errors.Is(err, ErrQuickCheckFailed) {
			return IntegrityReport{}, err
		}
		addIssue(IntegrityIssueQuickCheck, 1)
		return report, nil
	}

	contractCount := state.Applied
	if scope == IntegrityCurrentBaseline {
		contractCount = len(schemaContracts)
		if !state.Current {
			addIssue(IntegrityIssueSchema, 1)
		}
	} else if scope != IntegrityAppliedBaseline {
		return IntegrityReport{}, errors.New("unknown integrity scope")
	}
	schemaHealthy, err := s.schemaHealthyForApplied(ctx, contractCount)
	if err != nil {
		return IntegrityReport{}, err
	}
	if !schemaHealthy {
		addIssue(IntegrityIssueSchema, 1)
		return report, nil
	}

	foreignKeys, err := s.foreignKeyViolationCount(ctx)
	if err != nil {
		return IntegrityReport{}, err
	}
	addIssue(IntegrityIssueForeignKeys, foreignKeys)

	jsonInvalid, err := s.contractQueryCount(ctx, contractCount, func(contract schemaContract) []string {
		return contract.jsonQueries
	})
	if err != nil {
		return IntegrityReport{}, err
	}
	addIssue(IntegrityIssueJSON, jsonInvalid)

	referencesInvalid, err := s.contractQueryCount(ctx, contractCount, func(contract schemaContract) []string {
		return contract.referenceQueries
	})
	if err != nil {
		return IntegrityReport{}, err
	}
	addIssue(IntegrityIssueReferences, referencesInvalid)

	stateInvalid, err := s.contractQueryCount(ctx, contractCount, func(contract schemaContract) []string {
		return contract.stateQueries
	})
	if err != nil {
		return IntegrityReport{}, err
	}
	addIssue(IntegrityIssueSystemState, stateInvalid)
	return report, nil
}

func (s *Store) foreignKeyViolationCount(ctx context.Context) (int, error) {
	rows, err := s.executor().QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var table, parent string
		var rowID any
		var foreignKeyID int
		if err := rows.Scan(&table, &rowID, &parent, &foreignKeyID); err != nil {
			return 0, err
		}
		count++
	}
	return count, rows.Err()
}

func (s *Store) contractQueryCount(
	ctx context.Context,
	contractCount int,
	queries func(schemaContract) []string,
) (int, error) {
	total := 0
	if contractCount > len(schemaContracts) {
		return 0, ErrUnsupportedSchema
	}
	for _, contract := range schemaContracts[:contractCount] {
		for _, query := range queries(contract) {
			var count int
			if err := s.executor().QueryRowContext(ctx, query).Scan(&count); err != nil {
				return 0, err
			}
			total += count
		}
	}
	return total, nil
}

func (s *Store) schemaHealthyForApplied(ctx context.Context, applied int) (bool, error) {
	if applied < 0 || applied > len(schemaContracts) {
		return false, ErrUnsupportedSchema
	}
	if applied > 0 {
		ok, err := s.objectExists(ctx, "table", "bun_migrations")
		if err != nil || !ok {
			return false, err
		}
	}
	tables := tableSpecsByName(initialTableSpecs)
	indexes := indexSpecsByName(initialIndexSpecs)
	triggers := triggerSpecsByName(initialTriggerSpecs)
	for _, contract := range schemaContracts[:applied] {
		for _, name := range contract.tables {
			spec, ok := tables[name]
			if !ok {
				return false, nil
			}
			ok, err := s.tableSchemaHealthy(ctx, spec)
			if err != nil || !ok {
				return false, err
			}
		}
		for _, name := range contract.indexes {
			spec, ok := indexes[name]
			if !ok {
				return false, nil
			}
			ok, err := s.indexSchemaHealthy(ctx, spec)
			if err != nil || !ok {
				return false, err
			}
		}
		for _, name := range contract.triggers {
			spec, ok := triggers[name]
			if !ok {
				return false, nil
			}
			ok, err := s.triggerSchemaHealthy(ctx, spec)
			if err != nil || !ok {
				return false, err
			}
		}
	}
	return true, nil
}

func tableSpecsByName(specs []tableSpec) map[string]tableSpec {
	result := make(map[string]tableSpec, len(specs))
	for _, spec := range specs {
		result[spec.name] = spec
	}
	return result
}

func indexSpecsByName(specs []indexSpec) map[string]indexSpec {
	result := make(map[string]indexSpec, len(specs))
	for _, spec := range specs {
		result[spec.name] = spec
	}
	return result
}

func triggerSpecsByName(specs []triggerSpec) map[string]triggerSpec {
	result := make(map[string]triggerSpec, len(specs))
	for _, spec := range specs {
		result[spec.name] = spec
	}
	return result
}
