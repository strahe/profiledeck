package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	bunmigrate "github.com/uptrace/bun/migrate"

	"github.com/strahe/profiledeck/internal/store/migrations"
	"github.com/strahe/profiledeck/internal/targetformat"
)

const (
	IntegrityIssueQuickCheck  = "sqlite_quick_check"
	IntegrityIssueForeignKeys = "foreign_keys"
	IntegrityIssueSchema      = "schema"
	IntegrityIssueJSON        = "json"
	IntegrityIssueReferences  = "references"
	IntegrityIssueSystemState = "system_state"
	IntegrityIssueContentHash = "content_hash"
	IntegrityIssueMetadata    = "metadata"
	IntegrityIssueTarget      = "target_registry"
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

// schemaContract is the complete immutable baseline after one migration.
// Existing objects must be copied into later contracts before their specs change.
type schemaContract struct {
	migrationKey                   string
	tableSpecs                     []tableSpec
	indexSpecs                     []indexSpec
	triggerSpecs                   []triggerSpec
	jsonQueries                    []string
	referenceQueries               []string
	stateQueries                   []string
	checkContentHashes             bool
	operationMetadataSchemaVersion int
	checkTargetRegistry            bool
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
		migrationKey: "stable_baseline",
		tableSpecs:   stableBaselineTableSpecs,
		indexSpecs:   stableBaselineIndexSpecs,
		triggerSpecs: stableBaselineTriggerSpecs,
		jsonQueries: []string{
			`SELECT COUNT(1) FROM providers WHERE NOT (` + jsonObjectExpression("metadata_json") + `)`,
			`SELECT COUNT(1) FROM profiles WHERE NOT (` + jsonObjectExpression("metadata_json") + `)`,
			`SELECT COUNT(1) FROM settings WHERE json_valid(value_json) = 0`,
			`SELECT COUNT(1) FROM provider_settings WHERE NOT (` + jsonObjectExpression("settings_json") + `)`,
			`SELECT COUNT(1) FROM provider_profile_settings WHERE NOT (` + jsonObjectExpression("settings_json") + `)`,
			`SELECT COUNT(1) FROM operations WHERE NOT (` + jsonObjectExpression("metadata_json") + `)`,
			`SELECT COUNT(1) FROM provider_credentials
				WHERE NOT (` + jsonObjectExpression("payload_json") + `)
					OR NOT (` + jsonObjectExpression("metadata_json") + `)`,
			`SELECT COUNT(1) FROM provider_config_sets WHERE NOT (` + jsonObjectExpression("metadata_json") + `)`,
			`SELECT COUNT(1) FROM profile_targets
				WHERE NOT (` + jsonObjectExpression("value_json") + `)
					OR NOT (` + jsonObjectExpression("metadata_json") + `)`,
			`SELECT COUNT(1) FROM system_state WHERE json_valid(value_json) = 0`,
		},
		referenceQueries: []string{
			`SELECT COUNT(1) FROM provider_settings AS value
				WHERE NOT EXISTS (SELECT 1 FROM providers WHERE providers.id = value.provider_id)`,
			`SELECT COUNT(1) FROM provider_profile_settings AS value
				WHERE NOT EXISTS (SELECT 1 FROM profiles WHERE profiles.id = value.profile_id)
					OR NOT EXISTS (SELECT 1 FROM providers WHERE providers.id = value.provider_id)`,
			`SELECT COUNT(1) FROM provider_active_states AS value
				WHERE NOT EXISTS (SELECT 1 FROM providers WHERE providers.id = value.provider_id)
					OR NOT EXISTS (SELECT 1 FROM profiles WHERE profiles.id = value.profile_id)`,
			`SELECT COUNT(1) FROM operations AS value
				WHERE NOT EXISTS (SELECT 1 FROM providers WHERE providers.id = value.provider_id)
					OR (value.source_operation_id IS NOT NULL AND NOT EXISTS (
						SELECT 1 FROM operations AS source
						WHERE source.id = value.source_operation_id
							AND source.provider_id = value.provider_id
					))`,
			`SELECT COUNT(1) FROM operation_profiles AS value
				WHERE NOT EXISTS (SELECT 1 FROM operations WHERE operations.id = value.operation_id)
					OR NOT EXISTS (SELECT 1 FROM profiles WHERE profiles.id = value.profile_id)`,
			`SELECT COUNT(1) FROM provider_credentials AS value
				WHERE NOT EXISTS (SELECT 1 FROM providers WHERE providers.id = value.provider_id)`,
			`SELECT COUNT(1) FROM provider_config_sets AS value
				WHERE NOT EXISTS (SELECT 1 FROM providers WHERE providers.id = value.provider_id)`,
			`SELECT COUNT(1) FROM profile_credential_bindings AS value
				WHERE NOT EXISTS (SELECT 1 FROM profiles WHERE profiles.id = value.profile_id)
					OR NOT EXISTS (SELECT 1 FROM providers WHERE providers.id = value.provider_id)
					OR NOT EXISTS (
						SELECT 1 FROM provider_credentials
						WHERE provider_credentials.provider_id = value.provider_id
							AND provider_credentials.id = value.credential_id
					)`,
			`SELECT COUNT(1) FROM profile_config_set_bindings AS value
				WHERE NOT EXISTS (SELECT 1 FROM profiles WHERE profiles.id = value.profile_id)
					OR NOT EXISTS (SELECT 1 FROM providers WHERE providers.id = value.provider_id)
					OR NOT EXISTS (
						SELECT 1 FROM provider_config_sets
						WHERE provider_config_sets.provider_id = value.provider_id
							AND provider_config_sets.id = value.config_set_id
					)`,
			`SELECT COUNT(1) FROM profile_targets AS value
				WHERE NOT EXISTS (SELECT 1 FROM profiles WHERE profiles.id = value.profile_id)
					OR NOT EXISTS (SELECT 1 FROM providers WHERE providers.id = value.provider_id)`,
			`SELECT COUNT(1) FROM usage_sources AS value
				WHERE NOT EXISTS (SELECT 1 FROM providers WHERE providers.id = value.provider_id)`,
			`SELECT COUNT(1) FROM usage_sessions AS value
				WHERE NOT EXISTS (SELECT 1 FROM usage_sources WHERE usage_sources.id = value.source_id)`,
			`SELECT COUNT(1) FROM usage_models AS value
				WHERE NOT EXISTS (SELECT 1 FROM usage_sources WHERE usage_sources.id = value.source_id)`,
			`SELECT COUNT(1) FROM usage_facts AS value
				WHERE NOT EXISTS (SELECT 1 FROM usage_sources WHERE usage_sources.id = value.source_id)
					OR (value.session_id IS NOT NULL AND NOT EXISTS (
						SELECT 1 FROM usage_sessions
						WHERE usage_sessions.id = value.session_id
							AND usage_sessions.source_id = value.source_id
					))
					OR NOT EXISTS (
						SELECT 1 FROM usage_models
						WHERE usage_models.id = value.model_id
							AND usage_models.source_id = value.source_id
					)`,
			`SELECT COUNT(1) FROM codex_usage_import_files AS value
				WHERE NOT EXISTS (
					SELECT 1 FROM usage_sources
					WHERE usage_sources.id = value.source_id
						AND usage_sources.provider_id = 'codex'
						AND usage_sources.identity_revision = value.identity_revision
				)`,
		},
		stateQueries: []string{
			fmt.Sprintf(`SELECT COUNT(1) FROM provider_settings
				WHERE schema_version <> %d`, stableBaselineProviderSettingsSchemaVersion),
			fmt.Sprintf(`SELECT COUNT(1) FROM provider_profile_settings
				WHERE schema_version <> %d`, stableBaselineProviderSettingsSchemaVersion),
			`SELECT COUNT(1) FROM provider_active_states
				WHERE revision <= 0`,
			fmt.Sprintf(`SELECT COUNT(1) FROM operations
				WHERE metadata_schema_version <> %d
					OR (operation_type = 'recovery' AND source_operation_id IS NULL)
					OR (operation_type <> 'recovery' AND source_operation_id IS NOT NULL)`,
				stableBaselineOperationMetadataSchemaVersion),
			`SELECT COUNT(1) FROM system_state
				WHERE key <> 'recovery.cleanup_required'
					OR CASE
						WHEN json_valid(value_json) = 0 THEN 1
						WHEN json_type(value_json) = 'true' THEN 0
						ELSE 1
					END = 1`,
		},
		checkContentHashes:             true,
		operationMetadataSchemaVersion: stableBaselineOperationMetadataSchemaVersion,
		checkTargetRegistry:            true,
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
	var contract *schemaContract
	switch scope {
	case IntegrityAppliedBaseline:
		contract, err = schemaContractForApplied(state.Applied)
	case IntegrityCurrentBaseline:
		contract, err = schemaContractForApplied(len(schemaContracts))
	default:
		return IntegrityReport{}, errors.New("unknown integrity scope")
	}
	if err != nil {
		return IntegrityReport{}, err
	}
	issues, err := s.inspectContractIntegrity(ctx, contract)
	if err != nil {
		return IntegrityReport{}, err
	}
	if scope == IntegrityCurrentBaseline && !state.Current {
		issues = addIntegrityIssue(issues, IntegrityIssueSchema, 1)
	}
	return IntegrityReport{
		Healthy:   len(issues) == 0,
		Migration: state,
		Issues:    issues,
	}, nil
}

func (s *Store) inspectContractIntegrity(
	ctx context.Context,
	contract *schemaContract,
) ([]IntegrityIssue, error) {
	issues := []IntegrityIssue{}
	if err := s.QuickCheck(ctx); err != nil {
		if !errors.Is(err, ErrQuickCheckFailed) {
			return nil, err
		}
		issues = addIntegrityIssue(issues, IntegrityIssueQuickCheck, 1)
	}

	if contract != nil {
		schemaHealthy, err := s.schemaContractHealthy(ctx, *contract)
		if err != nil {
			return nil, err
		}
		if !schemaHealthy {
			return addIntegrityIssue(issues, IntegrityIssueSchema, 1), nil
		}
	}

	foreignKeys, err := s.foreignKeyViolationCount(ctx)
	if err != nil {
		return nil, err
	}
	issues = addIntegrityIssue(issues, IntegrityIssueForeignKeys, foreignKeys)
	if contract == nil {
		return issues, nil
	}

	for _, check := range []struct {
		kind    string
		queries []string
	}{
		{kind: IntegrityIssueJSON, queries: contract.jsonQueries},
		{kind: IntegrityIssueReferences, queries: contract.referenceQueries},
		{kind: IntegrityIssueSystemState, queries: contract.stateQueries},
	} {
		count, err := s.queryViolationCount(ctx, check.queries)
		if err != nil {
			return nil, err
		}
		issues = addIntegrityIssue(issues, check.kind, count)
	}

	if contract.checkContentHashes {
		hashInvalid, err := s.contentHashViolationCount(ctx)
		if err != nil {
			return nil, err
		}
		issues = addIntegrityIssue(issues, IntegrityIssueContentHash, hashInvalid)
	}

	if contract.operationMetadataSchemaVersion > 0 {
		metadataInvalid, err := s.operationMetadataViolationCount(
			ctx,
			contract.operationMetadataSchemaVersion,
		)
		if err != nil {
			return nil, err
		}
		issues = addIntegrityIssue(issues, IntegrityIssueMetadata, metadataInvalid)
	}

	if contract.checkTargetRegistry {
		targetInvalid, err := s.profileTargetRegistryViolationCount(ctx)
		if err != nil {
			return nil, err
		}
		issues = addIntegrityIssue(issues, IntegrityIssueTarget, targetInvalid)
	}
	return issues, nil
}

func addIntegrityIssue(issues []IntegrityIssue, kind string, count int) []IntegrityIssue {
	if count <= 0 {
		return issues
	}
	for _, issue := range issues {
		if issue.Kind == kind {
			return issues
		}
	}
	return append(issues, IntegrityIssue{Kind: kind, Count: count})
}

func (s *Store) contentHashViolationCount(ctx context.Context) (int, error) {
	count := 0
	for _, query := range []string{
		`SELECT payload_json, payload_sha256 FROM provider_credentials`,
		`SELECT payload_text, payload_sha256 FROM provider_config_sets`,
	} {
		rows, err := s.executor().QueryContext(ctx, query)
		if err != nil {
			return 0, err
		}
		for rows.Next() {
			var payload, storedHash string
			if err := rows.Scan(&payload, &storedHash); err != nil {
				_ = rows.Close()
				return 0, err
			}
			sum := sha256.Sum256([]byte(payload))
			if hex.EncodeToString(sum[:]) != storedHash {
				count++
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return 0, err
		}
		if err := rows.Close(); err != nil {
			return 0, err
		}
	}
	return count, nil
}

func (s *Store) operationMetadataViolationCount(
	ctx context.Context,
	expectedSchemaVersion int,
) (int, error) {
	// Each metadata version needs its own decoder before it can become part of
	// a later integrity contract; never interpret an unknown version as V1.
	if expectedSchemaVersion != stableBaselineOperationMetadataSchemaVersion {
		return 0, ErrUnsupportedSchema
	}
	type operationMetadataRecord struct {
		id         string
		providerID string
		version    int
		raw        string
	}
	rows, err := s.executor().QueryContext(
		ctx,
		`SELECT id, provider_id, metadata_schema_version, metadata_json
		FROM operations
		ORDER BY id ASC`,
	)
	if err != nil {
		return 0, err
	}
	records := []operationMetadataRecord{}
	for rows.Next() {
		var record operationMetadataRecord
		if err := rows.Scan(&record.id, &record.providerID, &record.version, &record.raw); err != nil {
			_ = rows.Close()
			return 0, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}

	profileIDsByOperation := make(map[string][]string)
	rows, err = s.executor().QueryContext(
		ctx,
		`SELECT operation_id, profile_id
		FROM operation_profiles
		ORDER BY operation_id ASC, profile_id ASC`,
	)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var operationID, profileID string
		if err := rows.Scan(&operationID, &profileID); err != nil {
			_ = rows.Close()
			return 0, err
		}
		profileIDsByOperation[operationID] = append(profileIDsByOperation[operationID], profileID)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}

	count := 0
	for _, record := range records {
		if record.version != expectedSchemaVersion {
			count++
			continue
		}
		var metadata struct {
			ProviderID        string   `json:"provider_id"`
			RelatedProfileIDs []string `json:"related_profile_ids"`
		}
		if err := json.Unmarshal([]byte(record.raw), &metadata); err != nil ||
			metadata.ProviderID != record.providerID {
			count++
			continue
		}
		profileIDs := profileIDsByOperation[record.id]
		// The normalized comparison checks set equality; the raw length check
		// rejects duplicates or blank values that normalization would hide.
		if !equalStrings(uniqueNonEmptyStrings(metadata.RelatedProfileIDs), profileIDs) ||
			len(metadata.RelatedProfileIDs) != len(profileIDs) {
			count++
		}
	}
	return count, nil
}

func (s *Store) profileTargetRegistryViolationCount(ctx context.Context) (int, error) {
	rows, err := s.executor().QueryContext(
		ctx,
		`SELECT format, strategy
		FROM profile_targets
		ORDER BY profile_id, provider_id, target_id`,
	)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	registry := targetformat.BuiltinRegistry()
	count := 0
	for rows.Next() {
		var format, strategy string
		if err := rows.Scan(&format, &strategy); err != nil {
			return 0, err
		}
		if !registry.AllowsCanonical(format, strategy) {
			count++
		}
	}
	return count, rows.Err()
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

func (s *Store) queryViolationCount(ctx context.Context, queries []string) (int, error) {
	total := 0
	for _, query := range queries {
		var count int
		if err := s.executor().QueryRowContext(ctx, query).Scan(&count); err != nil {
			return 0, err
		}
		total += count
	}
	return total, nil
}

func schemaContractForApplied(applied int) (*schemaContract, error) {
	if applied < 0 || applied > len(schemaContracts) {
		return nil, ErrUnsupportedSchema
	}
	if applied == 0 {
		return nil, nil
	}
	return &schemaContracts[applied-1], nil
}

func (s *Store) schemaHealthyForApplied(ctx context.Context, applied int) (bool, error) {
	contract, err := schemaContractForApplied(applied)
	if err != nil {
		return false, err
	}
	if applied > 0 {
		ok, err := s.objectExists(ctx, "table", "bun_migrations")
		if err != nil || !ok {
			return false, err
		}
	}
	if contract == nil {
		return true, nil
	}
	return s.schemaContractHealthy(ctx, *contract)
}

func (s *Store) schemaContractHealthy(ctx context.Context, contract schemaContract) (bool, error) {
	for _, spec := range contract.tableSpecs {
		ok, err := s.tableSchemaHealthy(ctx, spec)
		if err != nil || !ok {
			return false, err
		}
	}
	for _, spec := range contract.indexSpecs {
		ok, err := s.indexSchemaHealthy(ctx, spec)
		if err != nil || !ok {
			return false, err
		}
	}
	for _, spec := range contract.triggerSpecs {
		ok, err := s.triggerSchemaHealthy(ctx, spec)
		if err != nil || !ok {
			return false, err
		}
	}
	return true, nil
}

// validateUnmarkedStableBaseline prevents IF NOT EXISTS from filling in a
// partial Beta or drifted schema. A missing marker is replay-safe only when
// every existing application table already forms one valid Stable baseline.
func (s *Store) validateUnmarkedStableBaseline(ctx context.Context) error {
	rows, err := s.executor().QueryContext(ctx, `
		SELECT name
		FROM sqlite_schema
		WHERE type = 'table'
			AND name NOT LIKE 'sqlite_%'
			AND name NOT IN ('bun_migrations', 'bun_migration_locks')
		ORDER BY name ASC
	`)
	if err != nil {
		return err
	}
	tableNames := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			return err
		}
		tableNames = append(tableNames, name)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(tableNames) == 0 {
		return nil
	}

	baseline := &schemaContracts[0]
	expectedTables := make(map[string]struct{}, len(baseline.tableSpecs))
	for _, spec := range baseline.tableSpecs {
		expectedTables[spec.name] = struct{}{}
	}
	if len(tableNames) != len(expectedTables) {
		return ErrUnsupportedSchema
	}
	for _, name := range tableNames {
		if _, ok := expectedTables[name]; !ok {
			return ErrUnsupportedSchema
		}
	}

	issues, err := s.inspectContractIntegrity(ctx, baseline)
	if err != nil {
		return err
	}
	if len(issues) != 0 {
		return ErrUnsupportedSchema
	}
	return nil
}
