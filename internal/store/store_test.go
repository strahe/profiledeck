package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	bunmigrate "github.com/uptrace/bun/migrate"

	"github.com/strahe/profiledeck/internal/apperror"
	storemigrations "github.com/strahe/profiledeck/internal/store/migrations"
)

func testPayloadSHA256(payload string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(payload)))
}

func TestMigrateCreatesInitialSchema(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "profiledeck.db")

	db := openTestStore(t, ctx, dbPath, false)
	defer closeTestStore(t, db)

	result, err := db.Migrate(ctx)
	if err != nil {
		t.Fatalf("expected migrations to succeed, got %v", err)
	}
	if result.Applied != 4 {
		t.Fatalf("expected 4 migrations to apply, got %d", result.Applied)
	}

	for _, table := range []string{
		"bun_migrations",
		"bun_migration_locks",
		"providers",
		"profiles",
		"provider_profile_settings",
		"settings",
		"active_states",
		"operations",
		"provider_credentials",
		"provider_config_sets",
		"profile_targets",
		"usage_events",
		"usage_import_cursors",
		"system_state",
	} {
		assertSQLiteObjectExists(t, ctx, db, "table", table)
	}

	for _, index := range []string{
		"bun_migrations_name_unique",
		"idx_providers_adapter_id",
		"idx_providers_enabled",
		"idx_provider_profile_settings_provider_id",
		"idx_operations_status",
		"idx_operations_operation_type",
		"idx_provider_credentials_provider_id",
		"idx_provider_credentials_kind",
		"idx_provider_config_sets_provider_id",
		"idx_provider_config_sets_kind",
		"idx_profile_targets_profile_id",
		"idx_profile_targets_provider_id",
		"idx_profile_targets_enabled",
		"idx_profile_targets_unique_path",
		"idx_usage_events_provider_id",
		"idx_usage_events_source",
		"idx_usage_events_source_key",
		"idx_usage_events_model",
		"idx_usage_events_occurred_at",
		"idx_usage_events_cost_status",
		"idx_usage_events_provider_cost_model_id",
		"idx_usage_import_cursors_source",
	} {
		assertSQLiteObjectExists(t, ctx, db, "index", index)
	}

	for _, trigger := range []string{
		"trg_profile_targets_path_owner_insert",
		"trg_profile_targets_path_owner_update",
	} {
		assertSQLiteObjectExists(t, ctx, db, "trigger", trigger)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "profiledeck.db")

	db := openTestStore(t, ctx, dbPath, false)
	defer closeTestStore(t, db)

	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("expected first migration to succeed, got %v", err)
	}
	result, err := db.Migrate(ctx)
	if err != nil {
		t.Fatalf("expected second migration to succeed, got %v", err)
	}
	if result.Applied != 0 {
		t.Fatalf("expected no migrations to apply on second run, got %d", result.Applied)
	}
}

func TestMigrationCompatibilityAcceptsKnownSchemas(t *testing.T) {
	ctx := context.Background()

	t.Run("missing migration table", func(t *testing.T) {
		db := openTestStore(t, ctx, filepath.Join(t.TempDir(), "profiledeck.db"), false)
		defer closeTestStore(t, db)

		if err := db.CheckMigrationCompatibility(ctx); err != nil {
			t.Fatalf("expected a new database to be compatible, got %v", err)
		}
	})

	t.Run("current migrations", func(t *testing.T) {
		db := openTestStore(t, ctx, filepath.Join(t.TempDir(), "profiledeck.db"), false)
		defer closeTestStore(t, db)
		if _, err := db.Migrate(ctx); err != nil {
			t.Fatalf("migrate current database: %v", err)
		}

		if err := db.CheckMigrationCompatibility(ctx); err != nil {
			t.Fatalf("expected the current database to be compatible, got %v", err)
		}
	})

	t.Run("known migration prefix", func(t *testing.T) {
		db := openTestStore(t, ctx, filepath.Join(t.TempDir(), "profiledeck.db"), false)
		defer closeTestStore(t, db)
		createTestMigrationTable(t, ctx, db)
		registered := storemigrations.Migrations.Sorted()
		if len(registered) == 0 {
			t.Fatal("expected registered migrations")
		}
		insertTestMigration(t, ctx, db, registered[0].Name)

		if err := db.CheckMigrationCompatibility(ctx); err != nil {
			t.Fatalf("expected an older migration prefix to be compatible, got %v", err)
		}
	})
}

func TestMigrationIntegrityContractRegistryUsesFilenameDerivedSemanticKeys(t *testing.T) {
	registered := storemigrations.Migrations.Sorted()
	for index := range registered {
		registered[index].Name = fmt.Sprintf("9%013d", index)
	}
	if err := validateMigrationIntegrityContractRegistry(registered); err != nil {
		t.Fatalf("filename-derived migration keys should not depend on numeric names: %v", err)
	}

	duplicate := append(bunmigrate.MigrationSlice(nil), registered...)
	duplicate[1].Comment = duplicate[0].Comment
	if err := validateMigrationIntegrityContractRegistry(duplicate); err == nil {
		t.Fatal("duplicate filename-derived migration key was accepted")
	}
}

func TestMigrationStateRejectsNonContiguousKnownHistory(t *testing.T) {
	ctx := context.Background()
	db := openTestStore(t, ctx, filepath.Join(t.TempDir(), "profiledeck.db"), false)
	defer closeTestStore(t, db)
	createTestMigrationTable(t, ctx, db)
	registered := storemigrations.Migrations.Sorted()
	if len(registered) < 3 {
		t.Fatalf("registered migrations = %d, want at least 3", len(registered))
	}
	insertTestMigration(t, ctx, db, registered[0].Name)
	insertTestMigration(t, ctx, db, registered[2].Name)

	if _, err := db.MigrationState(ctx); !errors.Is(err, ErrInvalidMigrationHistory) {
		t.Fatalf("MigrationState() error = %v, want ErrInvalidMigrationHistory", err)
	}
}

func TestMigrationStateRejectsMalformedInfrastructureBeforeSchemaWrites(t *testing.T) {
	ctx := context.Background()
	db := migratedTestStore(t, ctx)
	defer closeTestStore(t, db)
	if _, err := db.db.DB.ExecContext(ctx, `DROP TABLE bun_migration_locks`); err != nil {
		t.Fatal(err)
	}
	var schemaVersionBefore int
	if err := db.db.DB.QueryRowContext(ctx, `PRAGMA schema_version`).Scan(&schemaVersionBefore); err != nil {
		t.Fatal(err)
	}

	if _, err := db.Migrate(ctx); !errors.Is(err, ErrInvalidMigrationHistory) {
		t.Fatalf("Migrate() error = %v, want ErrInvalidMigrationHistory", err)
	}
	var schemaVersionAfter int
	if err := db.db.DB.QueryRowContext(ctx, `PRAGMA schema_version`).Scan(&schemaVersionAfter); err != nil {
		t.Fatal(err)
	}
	if schemaVersionAfter != schemaVersionBefore {
		t.Fatalf("malformed migration infrastructure changed schema: before=%d after=%d", schemaVersionBefore, schemaVersionAfter)
	}
}

func TestMigrateReplaysCommittedSchemaWhenMarkerIsMissing(t *testing.T) {
	ctx := context.Background()
	db := openTestStore(t, ctx, filepath.Join(t.TempDir(), "profiledeck.db"), false)
	defer closeTestStore(t, db)
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("initialize database: %v", err)
	}
	if _, err := db.UpsertSetting(ctx, UpsertSettingParams{Key: "marker-gap", ValueJSON: `{"kept":true}`}); err != nil {
		t.Fatalf("create business data: %v", err)
	}
	registered := storemigrations.Migrations.Sorted()
	last := registered[len(registered)-1].Name
	if _, err := db.db.DB.ExecContext(ctx, `DELETE FROM bun_migrations WHERE name = ?`, last); err != nil {
		t.Fatalf("remove final migration marker: %v", err)
	}

	baseline, err := db.InspectIntegrity(ctx, IntegrityAppliedBaseline)
	if err != nil || !baseline.Healthy || baseline.Migration.Pending != 1 {
		t.Fatalf("marker-gap baseline = %#v, err = %v", baseline, err)
	}
	result, err := db.Migrate(ctx)
	if err != nil || result.Applied != 1 {
		t.Fatalf("replay marker gap = %#v, err = %v", result, err)
	}
	setting, err := db.GetSetting(ctx, "marker-gap")
	if err != nil || setting.ValueJSON != `{"kept":true}` {
		t.Fatalf("business data after replay = %#v, err = %v", setting, err)
	}
	state, err := db.MigrationState(ctx)
	if err != nil || !state.Current || state.Applied != len(registered) {
		t.Fatalf("migration state after replay = %#v, err = %v", state, err)
	}
}

func TestInspectIntegrityClassifiesVersionedFailuresWithoutStoredValues(t *testing.T) {
	ctx := context.Background()

	t.Run("schema", func(t *testing.T) {
		db := migratedTestStore(t, ctx)
		defer closeTestStore(t, db)
		if _, err := db.db.DB.ExecContext(ctx, `DROP INDEX idx_usage_events_model`); err != nil {
			t.Fatal(err)
		}
		assertIntegrityIssue(t, ctx, db, IntegrityIssueSchema)
	})

	t.Run("json", func(t *testing.T) {
		db := migratedTestStore(t, ctx)
		defer closeTestStore(t, db)
		secret := `{"token":"SECRET_INTEGRITY_SENTINEL"`
		if _, err := db.db.DB.ExecContext(ctx, `INSERT INTO settings (key, value_json, updated_at_unix_ms) VALUES ('invalid-json', ?, 1)`, secret); err != nil {
			t.Fatal(err)
		}
		report := assertIntegrityIssue(t, ctx, db, IntegrityIssueJSON)
		encoded, err := json.Marshal(report)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "SECRET_INTEGRITY_SENTINEL") {
			t.Fatalf("integrity report exposed stored data: %s", encoded)
		}
	})

	t.Run("references", func(t *testing.T) {
		db := migratedTestStore(t, ctx)
		defer closeTestStore(t, db)
		if _, err := db.db.DB.ExecContext(ctx, `
			INSERT INTO provider_profile_settings
				(profile_id, provider_id, quota_refresh_interval_seconds, auth_keepalive_enabled, updated_at_unix_ms)
			VALUES ('missing-profile', 'missing-provider', 0, 0, 1)
		`); err != nil {
			t.Fatal(err)
		}
		assertIntegrityIssue(t, ctx, db, IntegrityIssueReferences)
	})

	t.Run("active state scope", func(t *testing.T) {
		db := migratedTestStore(t, ctx)
		defer closeTestStore(t, db)
		if _, err := db.db.DB.ExecContext(ctx, `
			INSERT INTO active_states
				(scope_type, scope_id, profile_id, operation_id, updated_at_unix_ms)
			VALUES ('unknown', 'unknown', '', '', 1)
		`); err != nil {
			t.Fatal(err)
		}
		assertIntegrityIssue(t, ctx, db, IntegrityIssueReferences)
	})

	t.Run("foreign keys", func(t *testing.T) {
		db := migratedTestStore(t, ctx)
		defer closeTestStore(t, db)
		if _, err := db.db.DB.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.db.DB.ExecContext(ctx, `
			INSERT INTO profile_credential_bindings
				(profile_id, provider_id, slot_id, credential_id, created_at_unix_ms, updated_at_unix_ms)
			VALUES ('missing-profile', 'missing-provider', 'auth', 'missing-credential', 1, 1)
		`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.db.DB.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
			t.Fatal(err)
		}
		assertIntegrityIssue(t, ctx, db, IntegrityIssueForeignKeys)
	})

	t.Run("historical and usage references are allowed", func(t *testing.T) {
		db := migratedTestStore(t, ctx)
		defer closeTestStore(t, db)
		if _, err := db.db.DB.ExecContext(ctx, `
			INSERT INTO operations
				(id, operation_type, status, profile_id, metadata_json, created_at_unix_ms, updated_at_unix_ms)
			VALUES ('historical', 'maintenance', 'applied', 'deleted-profile', '{}', 1, 1)
		`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.db.DB.ExecContext(ctx, `
			INSERT INTO usage_events
				(id, provider_id, source, source_key, cost_status, metadata_json, created_at_unix_ms, updated_at_unix_ms)
			VALUES ('usage', 'unmanaged-provider', 'test', 'source', 'unknown', '{}', 1, 1)
		`); err != nil {
			t.Fatal(err)
		}
		report, err := db.InspectIntegrity(ctx, IntegrityCurrentBaseline)
		if err != nil || !report.Healthy {
			t.Fatalf("allowed historical references failed integrity: report=%#v err=%v", report, err)
		}
	})
}

func TestInspectIntegrityClassifiesSQLiteCorruption(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "profiledeck.db")
	db := openTestStore(t, ctx, path, false)
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.DB.ExecContext(ctx, `CREATE TABLE corruption_probe (value BLOB)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.DB.ExecContext(ctx, `INSERT INTO corruption_probe (value) VALUES (zeroblob(262144))`); err != nil {
		t.Fatal(err)
	}
	var pageSize, rootPage int64
	if err := db.db.DB.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&pageSize); err != nil {
		t.Fatal(err)
	}
	if err := db.db.DB.QueryRowContext(ctx, `SELECT rootpage FROM sqlite_master WHERE name = 'corruption_probe'`).Scan(&rootPage); err != nil {
		t.Fatal(err)
	}
	if err := db.Checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	closeTestStore(t, db)

	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteAt(make([]byte, pageSize), (rootPage-1)*pageSize); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	db = openTestStore(t, ctx, path, true)
	defer closeTestStore(t, db)
	report, err := db.InspectIntegrity(ctx, IntegrityCurrentBaseline)
	if err != nil {
		t.Fatalf("InspectIntegrity() corruption error = %v", err)
	}
	if report.Healthy || len(report.Issues) != 1 || report.Issues[0].Kind != IntegrityIssueQuickCheck {
		t.Fatalf("InspectIntegrity() corruption report = %#v", report)
	}
}

func TestUnsupportedMigrationStopsStatusAndMigrateBeforeSchemaWrites(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "profiledeck.db")
	db := openTestStore(t, ctx, dbPath, false)
	defer closeTestStore(t, db)
	createTestMigrationTable(t, ctx, db)
	unknownName := "209912310001"
	id := insertTestMigration(t, ctx, db, unknownName)
	if id != 1 {
		t.Fatalf("future migration id = %d, want normal auto-generated id 1", id)
	}

	var schemaVersionBefore int
	if err := db.db.DB.QueryRowContext(ctx, `PRAGMA schema_version`).Scan(&schemaVersionBefore); err != nil {
		t.Fatalf("read schema version before rejection: %v", err)
	}
	if _, err := db.Status(ctx); !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatalf("Status() error = %v, want ErrUnsupportedSchema", err)
	}
	if _, err := db.Migrate(ctx); !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatalf("Migrate() error = %v, want ErrUnsupportedSchema", err)
	}

	var schemaVersionAfter, migrationCount int
	if err := db.db.DB.QueryRowContext(ctx, `PRAGMA schema_version`).Scan(&schemaVersionAfter); err != nil {
		t.Fatalf("read schema version after rejection: %v", err)
	}
	if schemaVersionAfter != schemaVersionBefore {
		t.Fatalf("schema version changed after rejection: before=%d after=%d", schemaVersionBefore, schemaVersionAfter)
	}
	if err := db.db.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM bun_migrations`).Scan(&migrationCount); err != nil {
		t.Fatalf("count migrations after rejection: %v", err)
	}
	if migrationCount != 1 {
		t.Fatalf("migration count after rejection = %d, want 1", migrationCount)
	}
	for _, table := range []string{"providers"} {
		exists, err := db.objectExists(ctx, "table", table)
		if err != nil {
			t.Fatalf("inspect %s after rejection: %v", table, err)
		}
		if exists {
			t.Fatalf("unexpected table %s created after rejection", table)
		}
	}

	opened, err := NewFactory(dbPath).OpenHealthy(ctx, true)
	if opened != nil {
		_ = opened.Close()
		t.Fatal("OpenHealthy() returned a Store for an unsupported schema")
	}
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != apperror.StoreSchemaUnsupported {
		t.Fatalf("OpenHealthy() error = %v, want %s", err, apperror.StoreSchemaUnsupported)
	}
	if strings.Contains(err.Error(), unknownName) {
		t.Fatalf("OpenHealthy() exposed migration name: %v", err)
	}
}

func TestUsageSchemaSupportsPartialCost(t *testing.T) {
	ctx := context.Background()
	db := openTestStore(t, ctx, filepath.Join(t.TempDir(), "profiledeck.db"), false)
	defer closeTestStore(t, db)
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("expected migrations to succeed, got %v", err)
	}
	partialCost := int64(42)
	if result, err := db.InsertUsageEvents(ctx, []CreateUsageEventParams{{
		ID: "partial-usage", ProviderID: "codex", Source: "codex-session-jsonl", SourceKey: "source",
		SessionID: "session", Model: "gpt-5.6-sol", TotalTokens: 1,
		EstimatedCostMicros: &partialCost, CostStatus: UsageCostStatusPartial,
	}}); err != nil || result.Inserted != 1 {
		t.Fatalf("expected base usage schema to accept partial cost, result=%#v err=%v", result, err)
	}
	storeStatus, err := db.Status(ctx)
	if err != nil || !storeStatus.SchemaHealthy {
		t.Fatalf("expected usage schema to remain healthy, status=%#v err=%v", storeStatus, err)
	}
}

func TestConcurrentMigrateIsIdempotent(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "profiledeck.db")

	const workers = 8
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()

			db, err := Open(ctx, dbPath, false)
			if err != nil {
				errs <- err
				return
			}
			defer func() {
				if err := db.Close(); err != nil {
					errs <- err
				}
			}()

			if _, err := db.Migrate(ctx); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("expected concurrent migration to succeed, got %v", err)
		}
	}

	db := openTestStore(t, ctx, dbPath, true)
	defer closeTestStore(t, db)

	status, err := db.Status(ctx)
	if err != nil {
		t.Fatalf("expected status after concurrent migration to succeed, got %v", err)
	}
	if !status.SchemaHealthy {
		t.Fatalf("expected schema after concurrent migration to be healthy")
	}

	var migrationCount int
	if err := db.db.DB.QueryRowContext(ctx, "SELECT COUNT(1) FROM bun_migrations").Scan(&migrationCount); err != nil {
		t.Fatalf("expected migration count query to succeed, got %v", err)
	}
	if migrationCount != 4 {
		t.Fatalf("expected four migration rows after concurrent migration, got %d", migrationCount)
	}
}

func TestMigrateRetriesTransientSQLiteBusy(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "profiledeck.db")

	db := openTestStore(t, ctx, dbPath, false)
	defer closeTestStore(t, db)
	if _, err := db.db.DB.ExecContext(ctx, "PRAGMA busy_timeout = 1"); err != nil {
		t.Fatalf("set short migration busy timeout: %v", err)
	}

	blocker, err := sql.Open(sqliteDriverName, sqliteDSN(dbPath, false))
	if err != nil {
		t.Fatalf("open migration blocker: %v", err)
	}
	defer blocker.Close()
	blocker.SetMaxOpenConns(1)
	conn, err := blocker.Conn(ctx)
	if err != nil {
		t.Fatalf("open migration blocker connection: %v", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN EXCLUSIVE"); err != nil {
		t.Fatalf("begin exclusive migration blocker: %v", err)
	}

	_, busyErr := db.migrateOnce(ctx)
	if !isSQLiteBusyError(busyErr) {
		t.Fatalf("migrateOnce() error while locked = %v, want SQLite busy", busyErr)
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		t.Fatalf("release migration blocker: %v", err)
	}

	attempts := 0
	result, migrateErr := migrateWithRetry(ctx, func(ctx context.Context) (MigrationResult, error) {
		attempts++
		if attempts == 1 {
			return MigrationResult{}, busyErr
		}
		return db.migrateOnce(ctx)
	})
	if migrateErr != nil {
		t.Fatalf("Migrate() error after transient lock = %v", migrateErr)
	}
	if attempts != 2 {
		t.Fatalf("Migrate() attempts = %d, want 2", attempts)
	}
	if result.Applied != 4 {
		t.Fatalf("Migrate() applied = %d, want 4", result.Applied)
	}
}

func TestSystemStateRegistryAcceptsOnlyTypedRecoveryCleanupKey(t *testing.T) {
	ctx := context.Background()
	db := migratedTestStore(t, ctx)
	defer closeTestStore(t, db)

	if required, err := db.RecoveryCleanupRequired(ctx); err != nil || required {
		t.Fatalf("initial cleanup state = %t, %v", required, err)
	}
	if err := db.RequireRecoveryCleanup(ctx); err != nil {
		t.Fatalf("RequireRecoveryCleanup() error = %v", err)
	}
	if required, err := db.RecoveryCleanupRequired(ctx); err != nil || !required {
		t.Fatalf("required cleanup state = %t, %v", required, err)
	}
	if report, err := db.InspectIntegrity(ctx, IntegrityCurrentBaseline); err != nil || !report.Healthy {
		t.Fatalf("registered state report = %#v, %v", report, err)
	}
	if err := db.ClearRecoveryCleanup(ctx); err != nil {
		t.Fatalf("ClearRecoveryCleanup() error = %v", err)
	}

	if _, err := db.db.DB.ExecContext(ctx, `
		INSERT INTO system_state (key, value_json, created_at_unix_ms, updated_at_unix_ms)
		VALUES ('future.safety_state', 'true', 1, 1)
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.RecoveryCleanupRequired(ctx); !errors.Is(err, ErrInvalidSystemState) {
		t.Fatalf("RecoveryCleanupRequired() error = %v, want ErrInvalidSystemState", err)
	}
	assertIntegrityIssue(t, ctx, db, IntegrityIssueSystemState)
	if _, err := db.db.DB.ExecContext(ctx, `DELETE FROM system_state`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.DB.ExecContext(ctx, `
		INSERT INTO system_state (key, value_json, created_at_unix_ms, updated_at_unix_ms)
		VALUES (?, 'false', 1, 1)
	`, recoveryCleanupStateKey); err != nil {
		t.Fatal(err)
	}
	assertIntegrityIssue(t, ctx, db, IntegrityIssueSystemState)
	if _, err := db.db.DB.ExecContext(ctx, `DELETE FROM system_state`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.DB.ExecContext(ctx, `
		INSERT INTO system_state (key, value_json, created_at_unix_ms, updated_at_unix_ms)
		VALUES (NULL, 'true', 1, 1)
	`); err == nil {
		t.Fatal("system_state accepted a NULL key")
	}
}

func TestProviderConfigSetCRUDAndReferences(t *testing.T) {
	ctx := context.Background()
	db := openTestStore(t, ctx, filepath.Join(t.TempDir(), "profiledeck.db"), false)
	defer closeTestStore(t, db)
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("expected migrations to succeed, got %v", err)
	}
	if _, err := db.CreateProvider(ctx, CreateProviderParams{ID: "codex", Name: "Codex", AdapterID: "codex", Enabled: true, MetadataJSON: "{}"}); err != nil {
		t.Fatalf("expected provider create to succeed, got %v", err)
	}
	if _, err := db.CreateProvider(ctx, CreateProviderParams{ID: "other", Name: "Other", AdapterID: "generic", Enabled: true, MetadataJSON: "{}"}); err != nil {
		t.Fatalf("expected second provider create to succeed, got %v", err)
	}

	payload := "model = \"gpt-5\"\n"
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(payload)))
	created, err := db.UpsertProviderConfigSet(ctx, UpsertProviderConfigSetParams{
		ID:            " shared ",
		ProviderID:    "codex",
		ConfigKind:    "codex-config-toml",
		Name:          " Shared ",
		Description:   " Common settings ",
		PayloadText:   payload,
		PayloadSHA256: digest,
	})
	if err != nil {
		t.Fatalf("expected config set create to succeed, got %v", err)
	}
	if created.ID != "shared" || created.Name != "Shared" || created.Description != "Common settings" || created.PayloadText != payload || created.PayloadSHA256 != digest {
		t.Fatalf("unexpected created config set: %#v", created)
	}
	if created.MetadataJSON != "{}" {
		t.Fatalf("expected default metadata object, got %q", created.MetadataJSON)
	}
	if _, err := db.UpsertProviderConfigSet(ctx, UpsertProviderConfigSetParams{
		ID: created.ID, ProviderID: "other", ConfigKind: created.ConfigKind, Name: created.Name,
		PayloadText: payload, PayloadSHA256: digest,
	}); err == nil {
		t.Fatalf("expected config set provider identity change to be rejected")
	}

	updatedName := "Default"
	updatedDescription := "Used by work profiles"
	updated, err := db.UpdateProviderConfigSet(ctx, UpdateProviderConfigSetParams{
		ID:          created.ID,
		Name:        &updatedName,
		Description: &updatedDescription,
	})
	if err != nil {
		t.Fatalf("expected config set update to succeed, got %v", err)
	}
	if updated.Name != updatedName || updated.Description != updatedDescription || updated.PayloadText != payload {
		t.Fatalf("unexpected updated config set: %#v", updated)
	}

	sets, err := db.ListProviderConfigSets(ctx, "codex", "codex-config-toml")
	if err != nil || len(sets) != 1 || sets[0].ID != created.ID {
		t.Fatalf("unexpected config set list: sets=%#v err=%v", sets, err)
	}

	if _, err := db.CreateProfile(ctx, CreateProfileParams{ID: "profile-a", Name: "Profile A", MetadataJSON: "{}"}); err != nil {
		t.Fatalf("expected profile create to succeed, got %v", err)
	}
	if _, err := db.UpsertProfileConfigSetBinding(ctx, UpsertProfileConfigSetBindingParams{
		ProfileID: "profile-a", ProviderID: "codex", SlotID: "user-config", ConfigSetID: created.ID,
	}); err != nil {
		t.Fatalf("expected profile Config Set binding to succeed, got %v", err)
	}
	references, err := db.CountProviderConfigSetReferences(ctx, created.ID)
	if err != nil || references != 1 {
		t.Fatalf("expected one config set reference, got count=%d err=%v", references, err)
	}
	if err := db.DeleteProviderConfigSet(ctx, created.ID); !errors.Is(err, ErrInUse) {
		t.Fatalf("expected referenced config set deletion to fail with ErrInUse, got %v", err)
	}
	if err := db.DeleteProfileConfigSetBinding(ctx, "profile-a", "codex", "user-config"); err != nil {
		t.Fatalf("expected profile Config Set binding delete to succeed, got %v", err)
	}
	if _, err := db.UpsertProfileConfigSetBinding(ctx, UpsertProfileConfigSetBindingParams{
		ProfileID: "profile-a", ProviderID: "other", SlotID: "user-config", ConfigSetID: created.ID,
	}); err == nil {
		t.Fatalf("expected cross-provider Config Set binding to be rejected")
	}
	references, err = db.CountProviderConfigSetReferences(ctx, created.ID)
	if err != nil || references != 0 {
		t.Fatalf("expected other-provider target not to reference config set, got count=%d err=%v", references, err)
	}
	if err := db.DeleteProviderConfigSet(ctx, created.ID); err != nil {
		t.Fatalf("expected unreferenced config set delete to succeed, got %v", err)
	}
}

func TestProviderConfigSetRejectsInvalidPayload(t *testing.T) {
	ctx := context.Background()
	db := openTestStore(t, ctx, filepath.Join(t.TempDir(), "profiledeck.db"), false)
	defer closeTestStore(t, db)
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("expected migrations to succeed, got %v", err)
	}

	validPayload := "model = \"gpt-5\"\n"
	validHash := fmt.Sprintf("%x", sha256.Sum256([]byte(validPayload)))
	for _, tc := range []struct {
		name    string
		payload string
		hash    string
	}{
		{name: "invalid hash", payload: validPayload, hash: "not-a-hash"},
		{name: "mismatched hash", payload: validPayload + "# changed", hash: validHash},
		{name: "oversized payload", payload: strings.Repeat("x", maxProviderConfigSetPayloadBytes+1), hash: fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Repeat("x", maxProviderConfigSetPayloadBytes+1))))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := db.UpsertProviderConfigSet(ctx, UpsertProviderConfigSetParams{
				ID:            "shared",
				ProviderID:    "codex",
				ConfigKind:    "codex-config-toml",
				Name:          "Shared",
				PayloadText:   tc.payload,
				PayloadSHA256: tc.hash,
			})
			if err == nil {
				t.Fatalf("expected invalid config set payload to be rejected")
			}
		})
	}
}

func TestProviderCredentialCRUD(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "profiledeck.db")

	db := openTestStore(t, ctx, dbPath, false)
	defer closeTestStore(t, db)
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("expected migrations to succeed, got %v", err)
	}

	createdPayload := `{"tokens":{"account_id":"Team/Shared","access_token":"raw"}}`
	createdHash := testPayloadSHA256(createdPayload)
	created, err := db.UpsertProviderCredential(ctx, UpsertProviderCredentialParams{
		ID:             " cred-work ",
		ProviderID:     " codex ",
		CredentialKind: " codex-auth-json ",
		PayloadJSON:    createdPayload,
		PayloadSHA256:  createdHash,
	})
	if err != nil {
		t.Fatalf("expected credential create to succeed, got %v", err)
	}
	if created.ID != "cred-work" || created.ProviderID != "codex" || created.CredentialKind != "codex-auth-json" || created.PayloadJSON != `{"tokens":{"account_id":"Team/Shared","access_token":"raw"}}` {
		t.Fatalf("unexpected created credential: %#v", created)
	}
	if created.MetadataJSON != "{}" {
		t.Fatalf("expected default metadata JSON object, got %q", created.MetadataJSON)
	}

	updatedPayload := `{"tokens":{"account_id":"Team/Shared","access_token":"new"}}`
	updatedHash := testPayloadSHA256(updatedPayload)
	updated, err := db.UpsertProviderCredential(ctx, UpsertProviderCredentialParams{
		ID:             "cred-work",
		ProviderID:     "codex",
		CredentialKind: "codex-auth-json",
		PayloadJSON:    updatedPayload,
		PayloadSHA256:  updatedHash,
		MetadataJSON:   `{"source":"test"}`,
	})
	if err != nil {
		t.Fatalf("expected credential update to succeed, got %v", err)
	}
	if updated.PayloadJSON != updatedPayload || updated.PayloadSHA256 != updatedHash {
		t.Fatalf("unexpected updated credential: %#v", updated)
	}
	if updated.CreatedAtUnixMS != created.CreatedAtUnixMS || updated.UpdatedAtUnixMS < created.UpdatedAtUnixMS {
		t.Fatalf("unexpected credential timestamps: created=%#v updated=%#v", created, updated)
	}

	personalPayload := `{"tokens":{"account_id":"Team/Shared","access_token":"personal"}}`
	_, err = db.UpsertProviderCredential(ctx, UpsertProviderCredentialParams{
		ID:             "cred-personal",
		ProviderID:     "codex",
		CredentialKind: "codex-auth-json",
		PayloadJSON:    personalPayload,
		PayloadSHA256:  testPayloadSHA256(personalPayload),
	})
	if err != nil {
		t.Fatalf("expected a second opaque credential with the same Codex account id to succeed, got %v", err)
	}

	list, err := db.ListProviderCredentials(ctx, "codex")
	if err != nil {
		t.Fatalf("expected credential list to succeed, got %v", err)
	}
	if len(list) != 2 || list[0].ID != "cred-personal" || list[1].ID != "cred-work" {
		t.Fatalf("unexpected credential list: %#v", list)
	}

	blankIDPayload := `{"tokens":{"account_id":"work","access_token":"raw"}}`
	_, err = db.UpsertProviderCredential(ctx, UpsertProviderCredentialParams{
		ID:             "  ",
		ProviderID:     "codex",
		CredentialKind: "codex-auth-json",
		PayloadJSON:    blankIDPayload,
		PayloadSHA256:  testPayloadSHA256(blankIDPayload),
	})
	if err == nil {
		t.Fatalf("expected blank credential id to be rejected")
	}
}

func TestTypedResourceBindingsEnforceProviderAndSharing(t *testing.T) {
	ctx := context.Background()
	db := openTestStore(t, ctx, filepath.Join(t.TempDir(), "profiledeck.db"), false)
	defer closeTestStore(t, db)
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("expected migrations to succeed, got %v", err)
	}
	var foreignKeysEnabled int
	if err := db.executor().QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeysEnabled); err != nil || foreignKeysEnabled != 1 {
		t.Fatalf("expected SQLite foreign key enforcement, got enabled=%d err=%v", foreignKeysEnabled, err)
	}
	for _, provider := range []CreateProviderParams{
		{ID: "codex", Name: "Codex", AdapterID: "codex", Enabled: true, MetadataJSON: "{}"},
		{ID: "other", Name: "Other", AdapterID: "generic", Enabled: true, MetadataJSON: "{}"},
	} {
		if _, err := db.CreateProvider(ctx, provider); err != nil {
			t.Fatalf("expected provider create to succeed, got %v", err)
		}
	}
	for _, profileID := range []string{"work", "personal"} {
		if _, err := db.CreateProfile(ctx, CreateProfileParams{ID: profileID, Name: profileID, MetadataJSON: "{}"}); err != nil {
			t.Fatalf("expected Profile create to succeed, got %v", err)
		}
	}
	for _, credential := range []UpsertProviderCredentialParams{
		{ID: "shared-login", ProviderID: "codex", CredentialKind: "codex-auth-json", PayloadJSON: `{"token":"shared"}`, PayloadSHA256: testPayloadSHA256(`{"token":"shared"}`)},
		{ID: "copied-login", ProviderID: "codex", CredentialKind: "codex-auth-json", PayloadJSON: `{"token":"copy"}`, PayloadSHA256: testPayloadSHA256(`{"token":"copy"}`)},
		{ID: "other-login", ProviderID: "other", CredentialKind: "other-auth", PayloadJSON: `{"token":"other"}`, PayloadSHA256: testPayloadSHA256(`{"token":"other"}`)},
	} {
		if _, err := db.UpsertProviderCredential(ctx, credential); err != nil {
			t.Fatalf("expected credential create to succeed, got %v", err)
		}
	}
	for _, configSet := range []UpsertProviderConfigSetParams{
		{ID: "shared-config", ProviderID: "codex", ConfigKind: "toml", Name: "Shared", PayloadText: "shared = true\n", PayloadSHA256: testPayloadSHA256("shared = true\n")},
		{ID: "other-config", ProviderID: "other", ConfigKind: "text", Name: "Other", PayloadText: "other\n", PayloadSHA256: testPayloadSHA256("other\n")},
	} {
		if _, err := db.UpsertProviderConfigSet(ctx, configSet); err != nil {
			t.Fatalf("expected Config Set create to succeed, got %v", err)
		}
	}
	for _, profileID := range []string{"work", "personal"} {
		if _, err := db.UpsertProfileCredentialBinding(ctx, UpsertProfileCredentialBindingParams{
			ProfileID: profileID, ProviderID: "codex", SlotID: "auth", CredentialID: "shared-login",
		}); err != nil {
			t.Fatalf("expected shared credential binding to succeed, got %v", err)
		}
		if _, err := db.UpsertProfileConfigSetBinding(ctx, UpsertProfileConfigSetBindingParams{
			ProfileID: profileID, ProviderID: "codex", SlotID: "user-config", ConfigSetID: "shared-config",
		}); err != nil {
			t.Fatalf("expected shared Config Set binding to succeed, got %v", err)
		}
	}
	if references, err := db.CountProviderCredentialReferences(ctx, "shared-login"); err != nil || references != 2 {
		t.Fatalf("expected shared credential to have two references, got count=%d err=%v", references, err)
	}
	if _, err := db.UpsertProfileCredentialBinding(ctx, UpsertProfileCredentialBindingParams{
		ProfileID: "personal", ProviderID: "codex", SlotID: "auth", CredentialID: "copied-login",
	}); err != nil {
		t.Fatalf("expected copied credential rebind to succeed, got %v", err)
	}
	if references, err := db.CountProviderCredentialReferences(ctx, "shared-login"); err != nil || references != 1 {
		t.Fatalf("expected shared credential reference count to decrease, got count=%d err=%v", references, err)
	}
	if references, err := db.CountProviderConfigSetReferences(ctx, "shared-config"); err != nil || references != 2 {
		t.Fatalf("expected shared Config Set to have two references, got count=%d err=%v", references, err)
	}
	if _, err := db.UpsertProfileCredentialBinding(ctx, UpsertProfileCredentialBindingParams{
		ProfileID: "work", ProviderID: "other", SlotID: "auth", CredentialID: "shared-login",
	}); err == nil {
		t.Fatalf("expected cross-provider credential binding to be rejected")
	}
	if _, err := db.UpsertProfileConfigSetBinding(ctx, UpsertProfileConfigSetBindingParams{
		ProfileID: "work", ProviderID: "other", SlotID: "user-config", ConfigSetID: "shared-config",
	}); err == nil {
		t.Fatalf("expected cross-provider Config Set binding to be rejected")
	}
	if _, err := db.UpsertProfileCredentialBinding(ctx, UpsertProfileCredentialBindingParams{
		ProfileID: "missing", ProviderID: "codex", SlotID: "auth", CredentialID: "shared-login",
	}); err == nil {
		t.Fatalf("expected binding for a missing Profile to be rejected")
	}
	if _, err := db.executor().ExecContext(ctx, "DELETE FROM provider_credentials WHERE id = ?", "shared-login"); err == nil {
		t.Fatalf("expected referenced credential deletion to be rejected by the database")
	}
	if err := db.DeleteProvider(ctx, "other"); !errors.Is(err, ErrInUse) {
		t.Fatalf("expected Provider with an unbound resource to remain in use, got %v", err)
	}
}

func TestProviderCredentialRejectsInvalidPayloads(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "profiledeck.db")

	db := openTestStore(t, ctx, dbPath, false)
	defer closeTestStore(t, db)
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("expected migrations to succeed, got %v", err)
	}

	for _, tc := range []struct {
		name           string
		credentialKind string
		payload        string
	}{
		{name: "blank kind", credentialKind: " ", payload: `{}`},
		{name: "invalid json", credentialKind: "codex-auth-json", payload: `{`},
		{name: "non object", credentialKind: "codex-auth-json", payload: `[]`},
		{name: "multiple values", credentialKind: "codex-auth-json", payload: `{"tokens":{"account_id":"work"}} {}`},
		{name: "oversized", credentialKind: "codex-auth-json", payload: `{"payload":"` + strings.Repeat("x", maxProviderCredentialPayloadBytes) + `"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := db.UpsertProviderCredential(ctx, UpsertProviderCredentialParams{
				ID:             "cred-work",
				ProviderID:     "codex",
				CredentialKind: tc.credentialKind,
				PayloadJSON:    tc.payload,
				PayloadSHA256:  testPayloadSHA256(tc.payload),
			})
			if err == nil {
				t.Fatalf("expected credential payload to be rejected")
			}
		})
	}

	validPayload := `{"tokens":{"access_token":"raw"}}`
	for _, tc := range []struct {
		name string
		hash string
	}{
		{name: "invalid hash", hash: "not-a-sha256"},
		{name: "mismatched hash", hash: testPayloadSHA256(`{"tokens":{}}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := db.UpsertProviderCredential(ctx, UpsertProviderCredentialParams{
				ID:             "cred-work",
				ProviderID:     "codex",
				CredentialKind: "codex-auth-json",
				PayloadJSON:    validPayload,
				PayloadSHA256:  tc.hash,
			})
			if err == nil {
				t.Fatalf("expected credential payload hash to be rejected")
			}
		})
	}
}

func TestCompareAndSwapProviderCredentialRejectsStalePayload(t *testing.T) {
	ctx := context.Background()
	db := openTestStore(t, ctx, filepath.Join(t.TempDir(), "profiledeck.db"), false)
	defer closeTestStore(t, db)
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("expected migrations, got %v", err)
	}
	payload := `{"tokens":{"account_id":"display","access_token":"old"}}`
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(payload)))
	if _, err := db.UpsertProviderCredential(ctx, UpsertProviderCredentialParams{
		ID: "credential", ProviderID: "codex", CredentialKind: "codex-auth-json",
		PayloadJSON: payload, PayloadSHA256: hash,
	}); err != nil {
		t.Fatalf("expected credential fixture, got %v", err)
	}
	rotated := `{"tokens":{"account_id":"display","access_token":"new","refresh_token":"rotated"}}`
	rotatedHash := fmt.Sprintf("%x", sha256.Sum256([]byte(rotated)))
	updated, swapped, err := db.CompareAndSwapProviderCredential(ctx, hash, UpsertProviderCredentialParams{
		ID: "credential", ProviderID: "codex", CredentialKind: "codex-auth-json",
		PayloadJSON: rotated, PayloadSHA256: rotatedHash,
	})
	if err != nil || !swapped || updated.PayloadSHA256 != rotatedHash {
		t.Fatalf("expected credential CAS, updated=%#v swapped=%v err=%v", updated, swapped, err)
	}
	concurrent := `{"tokens":{"account_id":"display","access_token":"concurrent"}}`
	concurrentHash := fmt.Sprintf("%x", sha256.Sum256([]byte(concurrent)))
	if _, err := db.UpsertProviderCredential(ctx, UpsertProviderCredentialParams{
		ID: "credential", ProviderID: "codex", CredentialKind: "codex-auth-json",
		PayloadJSON: concurrent, PayloadSHA256: concurrentHash,
	}); err != nil {
		t.Fatalf("expected concurrent replacement, got %v", err)
	}
	_, swapped, err = db.CompareAndSwapProviderCredential(ctx, rotatedHash, UpsertProviderCredentialParams{
		ID: "credential", ProviderID: "codex", CredentialKind: "codex-auth-json",
		PayloadJSON: rotated, PayloadSHA256: rotatedHash,
	})
	if err != nil || swapped {
		t.Fatalf("expected stale CAS not to overwrite, swapped=%v err=%v", swapped, err)
	}
	current, err := db.GetProviderCredential(ctx, "credential")
	if err != nil || current.PayloadSHA256 != concurrentHash {
		t.Fatalf("expected concurrent payload to win, credential=%#v err=%v", current, err)
	}
}

func TestDefaultMetadataJSONValuesAreObjects(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "profiledeck.db")

	db := openTestStore(t, ctx, dbPath, false)
	defer closeTestStore(t, db)

	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("expected migrations to succeed, got %v", err)
	}

	_, err := db.db.DB.ExecContext(ctx, `
		INSERT INTO providers (id, name, adapter_id, created_at_unix_ms, updated_at_unix_ms)
		VALUES ('provider-1', 'Provider 1', 'adapter-1', 1, 1)
	`)
	if err != nil {
		t.Fatalf("expected provider insert to succeed, got %v", err)
	}
	_, err = db.db.DB.ExecContext(ctx, `
		INSERT INTO profiles (id, name, created_at_unix_ms, updated_at_unix_ms)
		VALUES ('profile-1', 'Profile 1', 1, 1)
	`)
	if err != nil {
		t.Fatalf("expected profile insert to succeed, got %v", err)
	}
	_, err = db.db.DB.ExecContext(ctx, `
		INSERT INTO operations (id, operation_type, status, created_at_unix_ms, updated_at_unix_ms)
		VALUES ('operation-1', 'maintenance', 'pending', 1, 1)
	`)
	if err != nil {
		t.Fatalf("expected operation insert to succeed, got %v", err)
	}

	for _, query := range []string{
		"SELECT metadata_json FROM providers WHERE id = 'provider-1'",
		"SELECT metadata_json FROM profiles WHERE id = 'profile-1'",
		"SELECT metadata_json FROM operations WHERE id = 'operation-1'",
	} {
		var raw string
		if err := db.db.DB.QueryRowContext(ctx, query).Scan(&raw); err != nil {
			t.Fatalf("expected metadata query to succeed, got %v", err)
		}
		var value map[string]any
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			t.Fatalf("expected metadata_json to be valid JSON object, got %q: %v", raw, err)
		}
		if len(value) != 0 {
			t.Fatalf("expected default metadata_json to be empty object, got %#v", value)
		}
	}
}

func TestOpenConfiguresSQLiteConnection(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "profiledeck.db")

	db := openTestStore(t, ctx, dbPath, false)
	defer closeTestStore(t, db)

	if got := db.db.DB.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("expected max open connections to be 1, got %d", got)
	}

	var timeoutMS int
	if err := db.db.DB.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&timeoutMS); err != nil {
		t.Fatalf("expected busy_timeout query to succeed, got %v", err)
	}
	if timeoutMS != int(sqliteBusyTimeout.Milliseconds()) {
		t.Fatalf("expected busy_timeout %d, got %d", sqliteBusyTimeout.Milliseconds(), timeoutMS)
	}
}

func TestWithTransactionRollsBackCRUD(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "profiledeck.db")

	db := openTestStore(t, ctx, dbPath, false)
	defer closeTestStore(t, db)

	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("expected migrations to succeed, got %v", err)
	}
	errRollback := errors.New("rollback")
	err := db.WithTransaction(ctx, func(txStore *Store) error {
		if _, err := txStore.CreateProvider(ctx, CreateProviderParams{
			ID:        "provider-1",
			Name:      "Provider 1",
			AdapterID: "adapter-1",
			Enabled:   true,
		}); err != nil {
			return err
		}
		return errRollback
	})
	if !errors.Is(err, errRollback) {
		t.Fatalf("expected rollback error, got %v", err)
	}
	if _, err := db.GetProvider(ctx, "provider-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected provider insert to roll back, got %v", err)
	}
}

func TestSQLiteDSNNormalizesWindowsPathAndSetsBusyTimeout(t *testing.T) {
	dsn := sqliteDSN(`C:\Users\profiledeck\profiledeck.db`, false)

	if strings.Contains(dsn, `\`) || strings.Contains(dsn, "%5C") {
		t.Fatalf("expected Windows path separators to be normalized, got %q", dsn)
	}
	if !strings.HasPrefix(dsn, "file:///C:/Users/profiledeck/profiledeck.db?") {
		t.Fatalf("expected drive-letter path to remain a URI path, got %q", dsn)
	}
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("expected valid SQLite URI, got %q: %v", dsn, err)
	}
	if parsed.Host != "" || parsed.Path != "/C:/Users/profiledeck/profiledeck.db" {
		t.Fatalf("expected empty URI authority and normalized path, got %#v", parsed)
	}
	if !strings.Contains(dsn, "mode=rwc") {
		t.Fatalf("expected read-write-create mode in DSN, got %q", dsn)
	}
	if !strings.Contains(dsn, "_pragma=busy_timeout%285000%29") {
		t.Fatalf("expected busy_timeout pragma in DSN, got %q", dsn)
	}
}

func TestReopenStoreAndReadStatus(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "profiledeck.db")

	db := openTestStore(t, ctx, dbPath, false)
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("expected migrations to succeed, got %v", err)
	}
	closeTestStore(t, db)

	db = openTestStore(t, ctx, dbPath, true)
	defer closeTestStore(t, db)

	status, err := db.Status(ctx)
	if err != nil {
		t.Fatalf("expected status to succeed, got %v", err)
	}
	if !status.SchemaHealthy {
		t.Fatalf("expected schema to be healthy")
	}
	if status.PendingOperations != 0 || status.FailedOperations != 0 {
		t.Fatalf("expected no operations, got pending=%d failed=%d", status.PendingOperations, status.FailedOperations)
	}
}

func TestStatusCountsPendingAndFailedOperations(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "profiledeck.db")

	db := openTestStore(t, ctx, dbPath, false)
	defer closeTestStore(t, db)

	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("expected migrations to succeed, got %v", err)
	}

	_, err := db.db.DB.ExecContext(ctx, `
		INSERT INTO operations (id, operation_type, status, created_at_unix_ms, updated_at_unix_ms)
		VALUES
			('operation-1', 'maintenance', 'pending', 1, 1),
			('operation-2', 'maintenance', 'failed', 1, 1),
			('operation-3', 'maintenance', 'applied', 1, 1)
	`)
	if err != nil {
		t.Fatalf("expected operation insert to succeed, got %v", err)
	}

	status, err := db.Status(ctx)
	if err != nil {
		t.Fatalf("expected status to succeed, got %v", err)
	}
	if status.PendingOperations != 1 || status.FailedOperations != 1 {
		t.Fatalf("unexpected operation counts: pending=%d failed=%d", status.PendingOperations, status.FailedOperations)
	}
}

func TestUsageEventsAreIdempotentAndSummarized(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "profiledeck.db")

	db := openTestStore(t, ctx, dbPath, false)
	defer closeTestStore(t, db)

	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("expected migrations to succeed, got %v", err)
	}

	cost := int64(123)
	params := []CreateUsageEventParams{{
		ID:                  "event-1",
		ProviderID:          "codex",
		Source:              "codex-session-jsonl",
		SourceKey:           "source-key",
		SessionID:           "session-1",
		Model:               "gpt-5.3-codex",
		InputTokens:         10,
		CachedInputTokens:   2,
		OutputTokens:        3,
		TotalTokens:         13,
		EstimatedCostMicros: &cost,
		CostStatus:          UsageCostStatusEstimated,
		MetadataJSON:        "{}",
	}}

	first, err := db.InsertUsageEvents(ctx, params)
	if err != nil {
		t.Fatalf("expected first usage insert to succeed, got %v", err)
	}
	if first.Inserted != 1 || first.Duplicates != 0 {
		t.Fatalf("expected inserted=1 duplicates=0, got %#v", first)
	}
	second, err := db.InsertUsageEvents(ctx, params)
	if err != nil {
		t.Fatalf("expected duplicate usage insert to succeed, got %v", err)
	}
	if second.Inserted != 0 || second.Duplicates != 1 {
		t.Fatalf("expected inserted=0 duplicates=1, got %#v", second)
	}

	summary, err := db.UsageSummary(ctx, "codex")
	if err != nil {
		t.Fatalf("expected usage summary to succeed, got %v", err)
	}
	if summary.EventCount != 1 || summary.InputTokens != 10 || summary.CachedInputTokens != 2 || summary.OutputTokens != 3 || summary.TotalTokens != 13 {
		t.Fatalf("unexpected usage summary: %#v", summary)
	}
	if summary.EstimatedCostMicros != cost || summary.UnknownCostEvents != 0 || summary.EstimatedCostEventCount != 1 {
		t.Fatalf("unexpected usage cost summary: %#v", summary)
	}
	if len(summary.Sources) != 1 || summary.Sources[0] != "codex-session-jsonl" {
		t.Fatalf("unexpected usage sources: %#v", summary.Sources)
	}
}

func TestUsageEventsDeduplicateStableIDAndKeepEarlierObservation(t *testing.T) {
	ctx := context.Background()
	db := openTestStore(t, ctx, filepath.Join(t.TempDir(), "profiledeck.db"), false)
	defer closeTestStore(t, db)
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("expected migrations to succeed, got %v", err)
	}

	cost := int64(123)
	forkCopy := CreateUsageEventParams{
		ID:         "stable-event",
		ProviderID: "codex", Source: "codex-session-jsonl", SourceKey: "source-fork",
		SessionID: "session-parent", Model: "gpt-5.3-codex", OccurredAtUnixMS: 2_000,
		InputTokens: 10, CachedInputTokens: 2, OutputTokens: 3, TotalTokens: 13,
		EstimatedCostMicros: &cost, CostStatus: UsageCostStatusEstimated,
		MetadataJSON: `{"line_index":20}`,
	}
	if result, err := db.InsertUsageEvents(ctx, []CreateUsageEventParams{forkCopy}); err != nil || result.Inserted != 1 {
		t.Fatalf("expected fork observation insert, result=%#v err=%v", result, err)
	}
	parent := forkCopy
	parent.SourceKey = "source-parent"
	parent.Model = "openai/gpt-5.3-codex-2026-07-01"
	parent.OccurredAtUnixMS = 1_000
	parent.EstimatedCostMicros = nil
	parent.CostStatus = UsageCostStatusUnknown
	parent.MetadataJSON = `{"line_index":3}`
	if result, err := db.InsertUsageEvents(ctx, []CreateUsageEventParams{parent}); err != nil || result.Inserted != 0 || result.Duplicates != 1 {
		t.Fatalf("expected parent observation to share the stable event ID, result=%#v err=%v", result, err)
	}

	var id string
	var sourceKey string
	var model string
	var occurredAt int64
	var estimatedCost sql.NullInt64
	var costStatus string
	var metadataJSON string
	if err := db.executor().QueryRowContext(ctx, `SELECT id, source_key, model, occurred_at_unix_ms,
		estimated_cost_micros, cost_status, metadata_json
		FROM usage_events WHERE id = ?`, forkCopy.ID,
	).Scan(&id, &sourceKey, &model, &occurredAt, &estimatedCost, &costStatus, &metadataJSON); err != nil {
		t.Fatalf("expected canonical usage event, got %v", err)
	}
	if id != forkCopy.ID || sourceKey != "source-parent" || model != parent.Model || occurredAt != 1_000 || estimatedCost.Valid ||
		costStatus != UsageCostStatusUnknown || metadataJSON != `{"line_index":3}` {
		t.Fatalf("expected earliest observation fields on the existing event, id=%q source=%q model=%q occurred=%d cost=%#v status=%q metadata=%s", id, sourceKey, model, occurredAt, estimatedCost, costStatus, metadataJSON)
	}
	summary, err := db.UsageSummary(ctx, "codex")
	if err != nil || summary.EventCount != 1 || summary.TotalTokens != 13 {
		t.Fatalf("expected one counted stable event, summary=%#v err=%v", summary, err)
	}
}

func TestUsageSummaryReportsDistinctSources(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "profiledeck.db")

	db := openTestStore(t, ctx, dbPath, false)
	defer closeTestStore(t, db)

	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("expected migrations to succeed, got %v", err)
	}

	cost := int64(1)
	result, err := db.InsertUsageEvents(ctx, []CreateUsageEventParams{
		{
			ID:                  "event-1",
			ProviderID:          "codex",
			Source:              "codex-archive-jsonl",
			SourceKey:           "source-a",
			InputTokens:         1,
			TotalTokens:         1,
			EstimatedCostMicros: &cost,
			CostStatus:          UsageCostStatusEstimated,
			MetadataJSON:        "{}",
		},
		{
			ID:                  "event-2",
			ProviderID:          "codex",
			Source:              "codex-session-jsonl",
			SourceKey:           "source-b",
			InputTokens:         1,
			TotalTokens:         1,
			EstimatedCostMicros: &cost,
			CostStatus:          UsageCostStatusEstimated,
			MetadataJSON:        "{}",
		},
	})
	if err != nil {
		t.Fatalf("expected usage events insert to succeed, got %v", err)
	}
	if result.Inserted != 2 {
		t.Fatalf("expected two usage events, got %#v", result)
	}

	summary, err := db.UsageSummary(ctx, "codex")
	if err != nil {
		t.Fatalf("expected usage summary to succeed, got %v", err)
	}
	if strings.Join(summary.Sources, ",") != "codex-archive-jsonl,codex-session-jsonl" {
		t.Fatalf("unexpected sorted usage sources: %#v", summary.Sources)
	}
}

func TestUsageImportCursorUpsert(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "profiledeck.db")

	db := openTestStore(t, ctx, dbPath, false)
	defer closeTestStore(t, db)

	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("expected migrations to succeed, got %v", err)
	}

	if err := db.UpsertUsageImportCursor(ctx, UpsertUsageImportCursorParams{
		ProviderID:       "codex",
		Source:           "codex-session-jsonl",
		SourceKey:        "source-key",
		ModifiedUnixMS:   100,
		SizeBytes:        20,
		ImportedEvents:   1,
		InvalidLines:     2,
		UnsupportedLines: 3,
		MetadataJSON:     "{}",
	}); err != nil {
		t.Fatalf("expected cursor upsert to succeed, got %v", err)
	}
	if err := db.UpsertUsageImportCursor(ctx, UpsertUsageImportCursorParams{
		ProviderID:       "codex",
		Source:           "codex-session-jsonl",
		SourceKey:        "source-key",
		ModifiedUnixMS:   200,
		SizeBytes:        30,
		ImportedEvents:   4,
		InvalidLines:     5,
		UnsupportedLines: 6,
		MetadataJSON:     "{}",
	}); err != nil {
		t.Fatalf("expected cursor update to succeed, got %v", err)
	}

	cursor, err := db.GetUsageImportCursor(ctx, "codex", "codex-session-jsonl", "source-key")
	if err != nil {
		t.Fatalf("expected cursor query to succeed, got %v", err)
	}
	if cursor.ModifiedUnixMS != 200 || cursor.SizeBytes != 30 || cursor.ImportedEvents != 4 || cursor.InvalidLines != 5 || cursor.UnsupportedLines != 6 {
		t.Fatalf("unexpected cursor after update: %#v", cursor)
	}
	if _, err := db.db.DB.ExecContext(ctx, "UPDATE usage_import_cursors SET updated_at_unix_ms = 1 WHERE source_key = 'source-key'"); err != nil {
		t.Fatalf("expected cursor timestamp fixture update, got %v", err)
	}
	if err := db.TouchUsageImportCursor(ctx, "codex", "codex-session-jsonl", "source-key"); err != nil {
		t.Fatalf("expected cursor touch to succeed, got %v", err)
	}
	touched, err := db.GetUsageImportCursor(ctx, "codex", "codex-session-jsonl", "source-key")
	if err != nil {
		t.Fatalf("expected touched cursor query, got %v", err)
	}
	if touched.UpdatedAtUnixMS <= 1 || touched.ModifiedUnixMS != 200 || touched.SizeBytes != 30 || touched.ImportedEvents != 4 || touched.InvalidLines != 5 || touched.UnsupportedLines != 6 {
		t.Fatalf("expected touch to preserve cursor state, got %#v", touched)
	}
}

func TestCommitUsageImportIsAtomicAndIdempotent(t *testing.T) {
	ctx := context.Background()
	db := openTestStore(t, ctx, filepath.Join(t.TempDir(), "profiledeck.db"), false)
	defer closeTestStore(t, db)
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("expected migrations to succeed, got %v", err)
	}

	cost := int64(42)
	event := CreateUsageEventParams{
		ID:                  "event-atomic",
		ProviderID:          "codex",
		Source:              "codex-session-jsonl",
		SourceKey:           "source-atomic",
		SessionID:           "session-atomic",
		Model:               "gpt-5.3-codex",
		OccurredAtUnixMS:    1_000,
		InputTokens:         10,
		CachedInputTokens:   2,
		OutputTokens:        3,
		TotalTokens:         13,
		EstimatedCostMicros: &cost,
		CostStatus:          UsageCostStatusEstimated,
	}
	cursor := UpsertUsageImportCursorParams{
		ProviderID:     "codex",
		Source:         "codex-session-jsonl",
		SourceKey:      "source-atomic",
		ModifiedUnixMS: 100,
		SizeBytes:      200,
		ImportedEvents: 1,
	}

	first, err := db.CommitUsageImport(ctx, CommitUsageImportParams{Events: []CreateUsageEventParams{event}, Cursor: cursor})
	if err != nil || first.Inserted != 1 || first.Duplicates != 0 {
		t.Fatalf("expected first atomic import to insert one event, result=%#v err=%v", first, err)
	}
	second, err := db.CommitUsageImport(ctx, CommitUsageImportParams{Events: []CreateUsageEventParams{event}, Cursor: cursor})
	if err != nil || second.Inserted != 0 || second.Duplicates != 1 {
		t.Fatalf("expected repeated atomic import to deduplicate, result=%#v err=%v", second, err)
	}
	earlierDuplicate := event
	earlierDuplicate.SourceKey = "source-earlier-duplicate"
	earlierDuplicate.OccurredAtUnixMS = 500
	rollbackCursor := cursor
	rollbackCursor.SourceKey = "source-canonical-rollback"
	rollbackCursor.SizeBytes = -1
	if _, err := db.CommitUsageImport(ctx, CommitUsageImportParams{
		Events: []CreateUsageEventParams{earlierDuplicate},
		Cursor: rollbackCursor,
	}); err == nil {
		t.Fatalf("expected invalid cursor to roll back canonical observation update")
	}
	var canonicalSourceKey string
	var canonicalOccurredAt int64
	if err := db.executor().QueryRowContext(ctx, `SELECT source_key, occurred_at_unix_ms
		FROM usage_events WHERE id = ?`, event.ID).Scan(&canonicalSourceKey, &canonicalOccurredAt); err != nil {
		t.Fatalf("expected canonical event after rollback, got %v", err)
	}
	if canonicalSourceKey != event.SourceKey || canonicalOccurredAt != event.OccurredAtUnixMS {
		t.Fatalf("expected failed cursor to roll back canonical update, source=%q occurred=%d", canonicalSourceKey, canonicalOccurredAt)
	}

	failingEvent := event
	failingEvent.ID = "event-rolled-back"
	failingCursor := cursor
	failingCursor.SourceKey = "source-rolled-back"
	failingCursor.SizeBytes = -1
	if _, err := db.CommitUsageImport(ctx, CommitUsageImportParams{
		Events: []CreateUsageEventParams{failingEvent},
		Cursor: failingCursor,
	}); err == nil {
		t.Fatalf("expected invalid cursor to fail the atomic import")
	}

	summary, err := db.UsageSummary(ctx, "codex")
	if err != nil {
		t.Fatalf("expected usage summary after rollback, got %v", err)
	}
	if summary.EventCount != 1 {
		t.Fatalf("expected failed import event to roll back, got %#v", summary)
	}
	if _, err := db.GetUsageImportCursor(ctx, "codex", "codex-session-jsonl", "source-rolled-back"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected failed import cursor not to advance, got %v", err)
	}
	failingCursor.SizeBytes = 1
	retry, err := db.CommitUsageImport(ctx, CommitUsageImportParams{
		Events: []CreateUsageEventParams{failingEvent},
		Cursor: failingCursor,
	})
	if err != nil || retry.Inserted != 1 || retry.Duplicates != 0 {
		t.Fatalf("expected rolled-back event ID to remain insertable, result=%#v err=%v", retry, err)
	}
}

func TestUsageReportAggregatesRangeModelsBucketsAndImportHealth(t *testing.T) {
	ctx := context.Background()
	db := openTestStore(t, ctx, filepath.Join(t.TempDir(), "profiledeck.db"), false)
	defer closeTestStore(t, db)
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("expected migrations to succeed, got %v", err)
	}

	cost10 := int64(10)
	cost20 := int64(20)
	cost5 := int64(5)
	events := []CreateUsageEventParams{
		{ID: "event-a1", ProviderID: "codex", Source: "codex-session-jsonl", SourceKey: "source-a", SessionID: "session-a", Model: "model-a", OccurredAtUnixMS: 1_000, InputTokens: 100, CachedInputTokens: 40, OutputTokens: 20, TotalTokens: 120, EstimatedCostMicros: &cost10, CostStatus: UsageCostStatusEstimated},
		{ID: "event-a2", ProviderID: "codex", Source: "codex-session-jsonl", SourceKey: "source-a", SessionID: "session-a", Model: "model-a", OccurredAtUnixMS: 1_500, InputTokens: 50, CachedInputTokens: 10, OutputTokens: 10, TotalTokens: 60, CostStatus: UsageCostStatusUnknown},
		{ID: "event-b1", ProviderID: "codex", Source: "codex-session-jsonl", SourceKey: "source-b", SessionID: "session-b", Model: "model-b", OccurredAtUnixMS: 2_500, InputTokens: 80, CachedInputTokens: 80, OutputTokens: 20, TotalTokens: 100, EstimatedCostMicros: &cost20, CostStatus: UsageCostStatusEstimated},
		{ID: "event-undated", ProviderID: "codex", Source: "codex-session-jsonl", SourceKey: "source-c", SessionID: "session-c", Model: "model-b", InputTokens: 30, OutputTokens: 5, TotalTokens: 35, EstimatedCostMicros: &cost5, CostStatus: UsageCostStatusEstimated},
	}
	if result, err := db.InsertUsageEvents(ctx, events); err != nil || result.Inserted != len(events) {
		t.Fatalf("expected usage fixture insert, result=%#v err=%v", result, err)
	}
	if err := db.UpsertUsageImportCursor(ctx, UpsertUsageImportCursorParams{
		ProviderID: "codex", Source: "codex-session-jsonl", SourceKey: "source-a",
		SizeBytes: 100, ImportedEvents: 2, InvalidLines: 3, UnsupportedLines: 4,
	}); err != nil {
		t.Fatalf("expected import cursor fixture, got %v", err)
	}

	start := int64(1_000)
	report, err := db.UsageReport(ctx, UsageReportQuery{
		ProviderID:  "codex",
		StartUnixMS: &start,
		EndUnixMS:   3_000,
		Buckets: []UsageTimeBucket{
			{StartUnixMS: 1_000, EndUnixMS: 2_000},
			{StartUnixMS: 2_000, EndUnixMS: 2_400},
			{StartUnixMS: 2_400, EndUnixMS: 3_000},
		},
	})
	if err != nil {
		t.Fatalf("expected usage report query, got %v", err)
	}
	if report.Summary.EventCount != 3 || report.Summary.SessionCount != 2 || report.Summary.FreshInputTokens != 100 || report.Summary.TotalTokens != 280 {
		t.Fatalf("unexpected ranged aggregate: %#v", report.Summary)
	}
	if report.Summary.EstimatedCostMicros != 30 || report.Summary.EstimatedTokenCount != 220 || report.Summary.UnknownCostEvents != 1 || report.Summary.UndatedEventCount != 1 {
		t.Fatalf("unexpected ranged cost and undated aggregate: %#v", report.Summary)
	}
	if len(report.Trend) != 3 || report.Trend[0].TotalTokens != 180 || report.Trend[1].EventCount != 0 || report.Trend[2].TotalTokens != 100 {
		t.Fatalf("unexpected zero-filled trend: %#v", report.Trend)
	}
	if len(report.Models) != 2 || report.Models[0].Model != "model-a" || report.Models[0].SessionCount != 1 || report.Models[1].Model != "model-b" {
		t.Fatalf("unexpected model summary or ordering: %#v", report.Models)
	}
	if report.ImportSummary.TrackedFiles != 1 || report.ImportSummary.InvalidLines != 3 || report.ImportSummary.UnsupportedLines != 4 || report.ImportSummary.LastSyncedAtUnixMS == 0 {
		t.Fatalf("unexpected import summary: %#v", report.ImportSummary)
	}

	all, err := db.UsageReport(ctx, UsageReportQuery{ProviderID: "codex", EndUnixMS: 3_000})
	if err != nil {
		t.Fatalf("expected all-time usage report, got %v", err)
	}
	if all.Summary.EventCount != 4 || all.Summary.SessionCount != 3 || all.Summary.UndatedEventCount != 1 || all.Summary.TotalTokens != 315 {
		t.Fatalf("expected undated event in all-time totals, got %#v", all.Summary)
	}
}

func TestUpdateUnknownUsageEventCostsIsFilteredAtomicAndIdempotent(t *testing.T) {
	ctx := context.Background()
	db := openTestStore(t, ctx, filepath.Join(t.TempDir(), "profiledeck.db"), false)
	defer closeTestStore(t, db)
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("expected migrations to succeed, got %v", err)
	}
	events := []CreateUsageEventParams{
		{ID: "candidate-a", ProviderID: "codex", Source: "codex-session-jsonl", SourceKey: "source-a", Model: "gpt-5.6-sol", InputTokens: 100, CachedInputTokens: 20, OutputTokens: 10, TotalTokens: 110, CostStatus: UsageCostStatusUnknown},
		{ID: "candidate-b", ProviderID: "codex", Source: "codex-session-jsonl", SourceKey: "source-b", Model: "other-model", InputTokens: 50, OutputTokens: 5, TotalTokens: 55, CostStatus: UsageCostStatusUnknown},
	}
	if result, err := db.InsertUsageEvents(ctx, events); err != nil || result.Inserted != len(events) {
		t.Fatalf("expected usage candidates, result=%#v err=%v", result, err)
	}

	candidates, err := db.ListUnknownUsageCostCandidates(ctx, "codex", []string{"gpt-5.6-sol"}, "", 10)
	if err != nil || len(candidates) != 1 || candidates[0].ID != "candidate-a" || candidates[0].CachedInputTokens != 20 {
		t.Fatalf("unexpected filtered candidates: %#v err=%v", candidates, err)
	}
	updated, err := db.UpdateUnknownUsageEventCosts(ctx, []UpdateUsageEventCostParams{{
		ID: "candidate-a", EstimatedCostMicros: 123, CostStatus: UsageCostStatusPartial,
	}})
	if err != nil || updated != 1 {
		t.Fatalf("expected one partial cost update, updated=%d err=%v", updated, err)
	}
	updated, err = db.UpdateUnknownUsageEventCosts(ctx, []UpdateUsageEventCostParams{{
		ID: "candidate-a", EstimatedCostMicros: 456, CostStatus: UsageCostStatusPartial,
	}})
	if err != nil || updated != 0 {
		t.Fatalf("expected repeated backfill not to overwrite classified cost, updated=%d err=%v", updated, err)
	}
	var cost int64
	var status string
	if err := db.executor().QueryRowContext(ctx, "SELECT estimated_cost_micros, cost_status FROM usage_events WHERE id = ?", "candidate-a").Scan(&cost, &status); err != nil {
		t.Fatalf("expected updated usage event, got %v", err)
	}
	if cost != 123 || status != UsageCostStatusPartial {
		t.Fatalf("unexpected persisted partial cost: cost=%d status=%q", cost, status)
	}
	report, err := db.UsageReport(ctx, UsageReportQuery{ProviderID: "codex", EndUnixMS: 1})
	if err != nil {
		t.Fatalf("expected usage report after partial update, got %v", err)
	}
	if report.Summary.EstimatedCostMicros != 123 || report.Summary.EstimatedTokenCount != 110 ||
		report.Summary.EstimatedCostEventCount != 0 || report.Summary.PartialCostEventCount != 1 || report.Summary.UnknownCostEvents != 1 {
		t.Fatalf("unexpected partial cost aggregate: %#v", report.Summary)
	}
	summary, err := db.UsageSummary(ctx, "codex")
	if err != nil || summary.PartialCostEvents != 1 || summary.UnknownCostEvents != 1 || summary.EstimatedCostMicros != 123 {
		t.Fatalf("unexpected partial legacy summary: %#v err=%v", summary, err)
	}

	_, err = db.UpdateUnknownUsageEventCosts(ctx, []UpdateUsageEventCostParams{
		{ID: "candidate-b", EstimatedCostMicros: 99, CostStatus: UsageCostStatusEstimated},
		{ID: "", EstimatedCostMicros: 1, CostStatus: UsageCostStatusPartial},
	})
	if err == nil {
		t.Fatalf("expected invalid batch to fail")
	}
	if err := db.executor().QueryRowContext(ctx, "SELECT cost_status FROM usage_events WHERE id = ?", "candidate-b").Scan(&status); err != nil {
		t.Fatalf("expected rolled back usage event, got %v", err)
	}
	if status != UsageCostStatusUnknown {
		t.Fatalf("expected invalid batch to roll back, got %q", status)
	}
}

func TestUsageReportTransactionKeepsAConsistentReadSnapshot(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "profiledeck.db")
	db := openTestStore(t, ctx, dbPath, false)
	defer closeTestStore(t, db)
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("expected migrations to succeed, got %v", err)
	}
	var journalMode string
	if err := db.db.DB.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&journalMode); err != nil || strings.ToLower(journalMode) != "wal" {
		t.Fatalf("expected WAL mode for concurrent snapshot test, mode=%q err=%v", journalMode, err)
	}

	cost := int64(1)
	first := CreateUsageEventParams{ID: "snapshot-first", ProviderID: "codex", Source: "codex-session-jsonl", SourceKey: "source-first", SessionID: "session-first", Model: "model-a", OccurredAtUnixMS: 1_000, InputTokens: 10, TotalTokens: 10, EstimatedCostMicros: &cost, CostStatus: UsageCostStatusEstimated}
	if result, err := db.InsertUsageEvents(ctx, []CreateUsageEventParams{first}); err != nil || result.Inserted != 1 {
		t.Fatalf("expected first snapshot fixture, result=%#v err=%v", result, err)
	}

	writer := openTestStore(t, ctx, dbPath, false)
	defer closeTestStore(t, writer)
	second := first
	second.ID = "snapshot-second"
	second.SourceKey = "source-second"
	second.SessionID = "session-second"
	second.OccurredAtUnixMS = 1_500
	start := int64(500)
	var snapshot UsageReportSnapshot
	err := db.WithTransaction(ctx, func(txStore *Store) error {
		if _, err := txStore.EarliestDatedUsageUnixMS(ctx, "codex"); err != nil {
			return err
		}
		writeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		writeDone := make(chan error, 1)
		go func() {
			_, writeErr := writer.InsertUsageEvents(writeCtx, []CreateUsageEventParams{second})
			writeDone <- writeErr
		}()
		if err := <-writeDone; err != nil {
			return fmt.Errorf("concurrent fixture write failed: %w", err)
		}
		var reportErr error
		snapshot, reportErr = txStore.UsageReport(ctx, UsageReportQuery{
			ProviderID:  "codex",
			StartUnixMS: &start,
			EndUnixMS:   2_000,
			Buckets:     []UsageTimeBucket{{StartUnixMS: 500, EndUnixMS: 2_000}},
		})
		return reportErr
	})
	if err != nil {
		t.Fatalf("expected concurrent snapshot report, got %v", err)
	}
	if snapshot.Summary.EventCount != 1 || snapshot.Trend[0].EventCount != 1 || len(snapshot.Models) != 1 {
		t.Fatalf("expected all report queries to retain the first snapshot, got %#v", snapshot)
	}
	after, err := db.UsageSummary(ctx, "codex")
	if err != nil || after.EventCount != 2 {
		t.Fatalf("expected concurrent event after report transaction, summary=%#v err=%v", after, err)
	}
}

func TestUsageReportDoesNotExposeUnsafePersistedModelLabels(t *testing.T) {
	ctx := context.Background()
	db := openTestStore(t, ctx, filepath.Join(t.TempDir(), "profiledeck.db"), false)
	defer closeTestStore(t, db)
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("expected migrations to succeed, got %v", err)
	}
	cost := int64(1)
	events := []CreateUsageEventParams{
		{ID: "unsafe-model", ProviderID: "codex", Source: "codex-session-jsonl", SourceKey: "source-a", SessionID: "session-a", Model: "SECRET PROMPT VALUE", InputTokens: 1, TotalTokens: 1, EstimatedCostMicros: &cost, CostStatus: UsageCostStatusEstimated},
		{ID: "blank-model", ProviderID: "codex", Source: "codex-session-jsonl", SourceKey: "source-b", SessionID: "session-b", InputTokens: 1, TotalTokens: 1, EstimatedCostMicros: &cost, CostStatus: UsageCostStatusEstimated},
	}
	if result, err := db.InsertUsageEvents(ctx, events); err != nil || result.Inserted != 2 {
		t.Fatalf("expected unsafe model fixtures, result=%#v err=%v", result, err)
	}
	report, err := db.UsageReport(ctx, UsageReportQuery{ProviderID: "codex"})
	if err != nil {
		t.Fatalf("expected safe usage report, got %v", err)
	}
	if len(report.Models) != 1 || report.Models[0].Model != "unknown" || report.Models[0].EventCount != 2 {
		t.Fatalf("expected unsafe models to merge into a safe label, got %#v", report.Models)
	}
}

func TestListIncompleteOperationsFiltersAndSorts(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "profiledeck.db")

	db := openTestStore(t, ctx, dbPath, false)
	defer closeTestStore(t, db)

	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("expected migrations to succeed, got %v", err)
	}

	_, err := db.db.DB.ExecContext(ctx, `
		INSERT INTO operations (id, operation_type, status, metadata_json, created_at_unix_ms, updated_at_unix_ms)
		VALUES
			('operation-applied', 'maintenance', 'applied', '{}', 1, 1),
			('operation-failed-b', 'maintenance', 'failed', '{}', 1, 20),
			('operation-pending-a', 'maintenance', 'pending', '{}', 1, 10),
			('operation-failed-a', 'maintenance', 'failed', '{}', 1, 20)
	`)
	if err != nil {
		t.Fatalf("expected operation setup to succeed, got %v", err)
	}

	operations, err := db.ListIncompleteOperations(ctx)
	if err != nil {
		t.Fatalf("expected incomplete operation list to succeed, got %v", err)
	}
	if operationIDs(operations) != "operation-pending-a,operation-failed-a,operation-failed-b" {
		t.Fatalf("unexpected incomplete operations: %#v", operations)
	}
}

func TestSwitchOperationLifecycle(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "profiledeck.db")

	db := openTestStore(t, ctx, dbPath, false)
	defer closeTestStore(t, db)

	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("expected migrations to succeed, got %v", err)
	}

	operation, err := db.CreatePendingSwitchOperation(ctx, CreateSwitchOperationParams{
		ID:           "switch-1",
		ProfileID:    "profile-a",
		MetadataJSON: `{"checkpoint":"created"}`,
	})
	if err != nil {
		t.Fatalf("expected pending switch operation create to succeed, got %v", err)
	}
	if operation.OperationType != OperationTypeSwitch || operation.Status != OperationStatusPending || operation.ProfileID != "profile-a" {
		t.Fatalf("unexpected pending operation: %#v", operation)
	}
	if operation.MetadataJSON != `{"checkpoint":"created"}` {
		t.Fatalf("unexpected operation metadata: %s", operation.MetadataJSON)
	}

	if err := db.UpdateOperationMetadata(ctx, "switch-1", `{"checkpoint":"backup"}`); err != nil {
		t.Fatalf("expected metadata update to succeed, got %v", err)
	}
	operation, err = db.GetOperation(ctx, "switch-1")
	if err != nil {
		t.Fatalf("expected operation read to succeed, got %v", err)
	}
	if operation.MetadataJSON != `{"checkpoint":"backup"}` || operation.Status != OperationStatusPending {
		t.Fatalf("unexpected operation after metadata update: %#v", operation)
	}

	configPayload := "model = \"gpt-5\"\n"
	configHash := fmt.Sprintf("%x", sha256.Sum256([]byte(configPayload)))
	credentialPayload := `{"token":"hidden"}`
	if err := db.CompleteSwitchOperation(ctx, CompleteSwitchOperationParams{
		ID:           "switch-1",
		ProfileID:    "profile-a",
		ProviderID:   "provider-a",
		MetadataJSON: `{"checkpoint":"complete"}`,
		CredentialUpdates: []UpsertProviderCredentialParams{{
			ID: "credential-a", ProviderID: "provider-a", CredentialKind: "json", PayloadJSON: credentialPayload, PayloadSHA256: testPayloadSHA256(credentialPayload),
		}},
		ConfigSetUpdates: []UpsertProviderConfigSetParams{{
			ID: "config-a", ProviderID: "provider-a", ConfigKind: "toml", Name: "Config A", PayloadText: configPayload, PayloadSHA256: configHash,
		}},
	}); err != nil {
		t.Fatalf("expected switch completion to succeed, got %v", err)
	}
	operation, err = db.GetOperation(ctx, "switch-1")
	if err != nil {
		t.Fatalf("expected operation read after completion to succeed, got %v", err)
	}
	if operation.Status != OperationStatusApplied || operation.ErrorCode != "" || operation.ErrorMessage != "" || operation.MetadataJSON != `{"checkpoint":"complete"}` {
		t.Fatalf("unexpected completed operation: %#v", operation)
	}

	activeState, err := db.GetActiveState(ctx, ActiveStateScopeProvider, "provider-a")
	if err != nil {
		t.Fatalf("expected active state read to succeed, got %v", err)
	}
	if activeState.ProfileID != "profile-a" || activeState.OperationID != "switch-1" {
		t.Fatalf("unexpected active state: %#v", activeState)
	}
	if credential, err := db.GetProviderCredential(ctx, "credential-a"); err != nil || credential.PayloadJSON != `{"token":"hidden"}` {
		t.Fatalf("expected credential update to commit with switch, got %#v err=%v", credential, err)
	}
	if configSet, err := db.GetProviderConfigSet(ctx, "config-a"); err != nil || configSet.PayloadText != configPayload {
		t.Fatalf("expected config set update to commit with switch, got %#v err=%v", configSet, err)
	}
	if required, err := db.RecoveryCleanupRequired(ctx); err != nil || !required {
		t.Fatalf("switch completion cleanup state = %t, %v", required, err)
	}

	if _, err := db.CreatePendingSwitchOperation(ctx, CreateSwitchOperationParams{
		ID:           "switch-2",
		ProfileID:    "profile-b",
		MetadataJSON: `{}`,
	}); err != nil {
		t.Fatalf("expected second pending switch operation create to succeed, got %v", err)
	}
	failedMetadata := `{"checkpoint":"failed"}`
	if err := db.MarkOperationFailed(ctx, MarkOperationFailedParams{
		ID:           "switch-2",
		ErrorCode:    "TARGET_WRITE_FAILED",
		ErrorMessage: "write failed",
		MetadataJSON: &failedMetadata,
	}); err != nil {
		t.Fatalf("expected operation failure mark to succeed, got %v", err)
	}
	operation, err = db.GetOperation(ctx, "switch-2")
	if err != nil {
		t.Fatalf("expected failed operation read to succeed, got %v", err)
	}
	if operation.Status != OperationStatusFailed || operation.ErrorCode != "TARGET_WRITE_FAILED" || operation.ErrorMessage != "write failed" || operation.MetadataJSON != failedMetadata {
		t.Fatalf("unexpected failed operation: %#v", operation)
	}
}

func TestRecoveryOperationAtomicallyResolvesSourceAndRestoresActiveState(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "profiledeck.db")

	db := openTestStore(t, ctx, dbPath, false)
	defer closeTestStore(t, db)

	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("expected migrations to succeed, got %v", err)
	}

	if _, err := db.CreatePendingSwitchOperation(ctx, CreateSwitchOperationParams{
		ID:           "switch-source",
		ProfileID:    "profile-next",
		MetadataJSON: `{"checkpoint":"recovery_created"}`,
	}); err != nil {
		t.Fatalf("create source switch operation: %v", err)
	}
	failedMetadata := `{"checkpoint":"recovery_created"}`
	if err := db.MarkOperationFailed(ctx, MarkOperationFailedParams{
		ID: "switch-source", ErrorCode: "TARGET_WRITE_FAILED", ErrorMessage: "write failed", MetadataJSON: &failedMetadata,
	}); err != nil {
		t.Fatalf("mark source switch failed: %v", err)
	}

	operation, err := db.CreatePendingRecoveryOperation(ctx, CreateRecoveryOperationParams{
		ID:           "recovery-1",
		ProfileID:    "profile-a",
		MetadataJSON: `{"checkpoint":"created"}`,
	})
	if err != nil {
		t.Fatalf("create pending recovery operation: %v", err)
	}
	if operation.OperationType != OperationTypeRecovery || operation.Status != OperationStatusPending || operation.ProfileID != "profile-a" {
		t.Fatalf("unexpected pending recovery operation: %#v", operation)
	}

	if err := db.CompleteRecoveryOperation(ctx, CompleteRecoveryOperationParams{
		ID: "recovery-1", SourceOperationID: "switch-source", ResolutionKind: "recovered_pre_switch",
		ProfileID: "profile-a", ProviderID: "provider-a",
		RestoredActiveState: &RecoveryActiveStateParams{
			ProfileID:   "profile-a",
			OperationID: "switch-previous",
		},
		MetadataJSON: `{"checkpoint":"applied"}`,
	}); err != nil {
		t.Fatalf("complete recovery operation: %v", err)
	}
	operation, err = db.GetOperation(ctx, "recovery-1")
	if err != nil {
		t.Fatalf("read recovery operation: %v", err)
	}
	if operation.Status != OperationStatusApplied || operation.ProfileID != "profile-a" || operation.MetadataJSON != `{"checkpoint":"applied"}` {
		t.Fatalf("unexpected completed recovery operation: %#v", operation)
	}
	source, err := db.GetOperation(ctx, "switch-source")
	if err != nil || source.Status != OperationStatusFailed || source.ResolutionKind != "recovered_pre_switch" || source.ResolvedAtUnixMS == 0 {
		t.Fatalf("source failure and resolution were not retained: %#v error=%v", source, err)
	}
	activeState, err := db.GetActiveState(ctx, ActiveStateScopeProvider, "provider-a")
	if err != nil {
		t.Fatalf("expected restored active state read to succeed, got %v", err)
	}
	if activeState.ProfileID != "profile-a" || activeState.OperationID != "switch-previous" {
		t.Fatalf("unexpected restored active state: %#v", activeState)
	}
	if required, err := db.RecoveryCleanupRequired(ctx); err != nil || !required {
		t.Fatalf("recovery completion cleanup state = %t, %v", required, err)
	}
	incomplete, err := db.ListIncompleteOperations(ctx)
	if err != nil || len(incomplete) != 0 {
		t.Fatalf("resolved source remained incomplete: %#v error=%v", incomplete, err)
	}
}

func TestSwitchCompletionRollsBackWhenCleanupRegistrationFails(t *testing.T) {
	ctx := context.Background()
	db := migratedTestStore(t, ctx)
	defer closeTestStore(t, db)
	if _, err := db.CreatePendingSwitchOperation(ctx, CreateSwitchOperationParams{
		ID: "switch-rollback", ProfileID: "profile-a", MetadataJSON: `{}`,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.DB.ExecContext(ctx, `DROP TABLE system_state`); err != nil {
		t.Fatal(err)
	}
	if err := db.CompleteSwitchOperation(ctx, CompleteSwitchOperationParams{
		ID: "switch-rollback", ProfileID: "profile-a", ProviderID: "provider-a", MetadataJSON: `{}`,
	}); err == nil {
		t.Fatal("CompleteSwitchOperation() succeeded without cleanup registration")
	}
	operation, err := db.GetOperation(ctx, "switch-rollback")
	if err != nil || operation.Status != OperationStatusPending {
		t.Fatalf("operation after rollback = %#v, %v", operation, err)
	}
	if _, err := db.GetActiveState(ctx, ActiveStateScopeProvider, "provider-a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("active state committed without cleanup registration: %v", err)
	}
}

func TestOtherCleanupRegistrationPointsRollBackAtomically(t *testing.T) {
	ctx := context.Background()

	t.Run("recovery completion", func(t *testing.T) {
		db := migratedTestStore(t, ctx)
		defer closeTestStore(t, db)
		if _, err := db.CreatePendingSwitchOperation(ctx, CreateSwitchOperationParams{
			ID: "switch-source", ProfileID: "profile-next", MetadataJSON: `{}`,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := db.CreatePendingRecoveryOperation(ctx, CreateRecoveryOperationParams{
			ID: "recovery-attempt", ProfileID: "profile-next", MetadataJSON: `{}`,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := db.db.DB.ExecContext(ctx, `
			INSERT INTO active_states (scope_type, scope_id, profile_id, operation_id, updated_at_unix_ms)
			VALUES (?, ?, ?, ?, 1)
		`, ActiveStateScopeProvider, "provider-a", "profile-before", "switch-before"); err != nil {
			t.Fatal(err)
		}
		if _, err := db.db.DB.ExecContext(ctx, `DROP TABLE system_state`); err != nil {
			t.Fatal(err)
		}

		err := db.CompleteRecoveryOperation(ctx, CompleteRecoveryOperationParams{
			ID: "recovery-attempt", SourceOperationID: "switch-source", ResolutionKind: "recovered_pre_switch",
			ProfileID: "profile-restored", ProviderID: "provider-a",
			RestoredActiveState: &RecoveryActiveStateParams{ProfileID: "profile-restored", OperationID: "switch-restored"},
			MetadataJSON:        `{}`,
		})
		if err == nil {
			t.Fatal("CompleteRecoveryOperation() succeeded without cleanup registration")
		}
		recovery, recoveryErr := db.GetOperation(ctx, "recovery-attempt")
		source, sourceErr := db.GetOperation(ctx, "switch-source")
		active, activeErr := db.GetActiveState(ctx, ActiveStateScopeProvider, "provider-a")
		if recoveryErr != nil || recovery.Status != OperationStatusPending ||
			sourceErr != nil || source.ResolvedAtUnixMS != 0 ||
			activeErr != nil || active.ProfileID != "profile-before" || active.OperationID != "switch-before" {
			t.Fatalf("recovery rollback = recovery %#v/%v source %#v/%v active %#v/%v", recovery, recoveryErr, source, sourceErr, active, activeErr)
		}
	})

	t.Run("source closure", func(t *testing.T) {
		db := migratedTestStore(t, ctx)
		defer closeTestStore(t, db)
		if _, err := db.CreatePendingSwitchOperation(ctx, CreateSwitchOperationParams{
			ID: "switch-close", ProfileID: "profile-a", MetadataJSON: `{}`,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := db.db.DB.ExecContext(ctx, `DROP TABLE system_state`); err != nil {
			t.Fatal(err)
		}
		if err := db.ResolveSwitchOperationForCleanup(ctx, "switch-close", "closed_before_target_writes"); err == nil {
			t.Fatal("ResolveSwitchOperationForCleanup() succeeded without cleanup registration")
		}
		operation, err := db.GetOperation(ctx, "switch-close")
		if err != nil || operation.ResolvedAtUnixMS != 0 || operation.ResolutionKind != "" {
			t.Fatalf("source closure rollback = %#v, %v", operation, err)
		}
	})

	t.Run("application restore preparation", func(t *testing.T) {
		db := migratedTestStore(t, ctx)
		defer closeTestStore(t, db)
		if _, err := db.CreatePendingSwitchOperation(ctx, CreateSwitchOperationParams{
			ID: "switch-restore", ProfileID: "profile-a", MetadataJSON: `{}`,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := db.db.DB.ExecContext(ctx, `
			INSERT INTO active_states (scope_type, scope_id, profile_id, operation_id, updated_at_unix_ms)
			VALUES (?, ?, ?, ?, 1)
		`, ActiveStateScopeProvider, "provider-a", "profile-a", "switch-restore"); err != nil {
			t.Fatal(err)
		}
		if _, err := db.db.DB.ExecContext(ctx, `DROP TABLE system_state`); err != nil {
			t.Fatal(err)
		}
		if err := db.PrepareForApplicationRestore(ctx); err == nil {
			t.Fatal("PrepareForApplicationRestore() succeeded without cleanup registration")
		}
		operation, operationErr := db.GetOperation(ctx, "switch-restore")
		active, activeErr := db.GetActiveState(ctx, ActiveStateScopeProvider, "provider-a")
		if operationErr != nil || operation.ResolvedAtUnixMS != 0 ||
			activeErr != nil || active.ProfileID != "profile-a" || active.OperationID != "switch-restore" {
			t.Fatalf("restore preparation rollback = operation %#v/%v active %#v/%v", operation, operationErr, active, activeErr)
		}
	})
}

func TestCleanupRegistrationIsLimitedToDedicatedResolutionPaths(t *testing.T) {
	ctx := context.Background()
	db := migratedTestStore(t, ctx)
	defer closeTestStore(t, db)
	for _, id := range []string{"switch-generic", "switch-cleanup"} {
		if _, err := db.CreatePendingSwitchOperation(ctx, CreateSwitchOperationParams{
			ID: id, ProfileID: "profile-a", MetadataJSON: `{}`,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.ResolveOperation(ctx, "switch-generic", "generic_resolution"); err != nil {
		t.Fatal(err)
	}
	if required, err := db.RecoveryCleanupRequired(ctx); err != nil || required {
		t.Fatalf("generic resolution cleanup state = %t, %v", required, err)
	}
	if err := db.ResolveSwitchOperationForCleanup(ctx, "switch-cleanup", "closed_before_target_writes"); err != nil {
		t.Fatal(err)
	}
	if required, err := db.RecoveryCleanupRequired(ctx); err != nil || !required {
		t.Fatalf("dedicated resolution cleanup state = %t, %v", required, err)
	}
	if err := db.ClearRecoveryCleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.PrepareForApplicationRestore(ctx); err != nil {
		t.Fatal(err)
	}
	if required, err := db.RecoveryCleanupRequired(ctx); err != nil || !required {
		t.Fatalf("restore preparation cleanup state = %t, %v", required, err)
	}
}

func TestSettingCRUD(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "profiledeck.db")

	db := openTestStore(t, ctx, dbPath, false)
	defer closeTestStore(t, db)
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("expected migrations to succeed, got %v", err)
	}

	_, err := db.GetSetting(ctx, "desktop.language")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected missing setting to return ErrNotFound, got %v", err)
	}

	created, err := db.UpsertSetting(ctx, UpsertSettingParams{Key: " desktop.language ", ValueJSON: `"auto"`})
	if err != nil {
		t.Fatalf("expected setting create to succeed, got %v", err)
	}
	if created.Key != "desktop.language" || created.ValueJSON != `"auto"` || created.UpdatedAtUnixMS == 0 {
		t.Fatalf("unexpected created setting: %#v", created)
	}

	updated, err := db.UpsertSetting(ctx, UpsertSettingParams{Key: "desktop.language", ValueJSON: `"zh-CN"`})
	if err != nil {
		t.Fatalf("expected setting update to succeed, got %v", err)
	}
	if updated.ValueJSON != `"zh-CN"` || updated.UpdatedAtUnixMS < created.UpdatedAtUnixMS {
		t.Fatalf("unexpected updated setting: %#v", updated)
	}

	_, err = db.UpsertSetting(ctx, UpsertSettingParams{Key: "desktop.language", ValueJSON: `"auto" "extra"`})
	if err == nil {
		t.Fatalf("expected multiple JSON values to fail")
	}
}

func TestProviderProfileSettingsDefaultsValidationAndProfileDeleteCleanup(t *testing.T) {
	ctx := context.Background()
	db := openTestStore(t, ctx, filepath.Join(t.TempDir(), "profiledeck.db"), false)
	defer closeTestStore(t, db)
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("expected migrations, got %v", err)
	}
	if _, err := db.CreateProvider(ctx, CreateProviderParams{ID: "codex", Name: "Codex", AdapterID: "codex", Enabled: true, MetadataJSON: `{}`}); err != nil {
		t.Fatalf("expected provider fixture, got %v", err)
	}
	if _, err := db.CreateProfile(ctx, CreateProfileParams{ID: "work", Name: "Work", MetadataJSON: `{}`}); err != nil {
		t.Fatalf("expected profile fixture, got %v", err)
	}
	if _, err := db.GetProviderProfileSetting(ctx, "work", "codex"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected absent settings to represent defaults, got %v", err)
	}
	created, err := db.UpsertProviderProfileSetting(ctx, UpsertProviderProfileSettingParams{
		ProfileID: "work", ProviderID: "codex", QuotaRefreshIntervalSeconds: 600, AuthKeepaliveEnabled: true,
	})
	if err != nil || created.QuotaRefreshIntervalSeconds != 600 || !created.AuthKeepaliveEnabled {
		t.Fatalf("unexpected provider Profile setting: %#v, %v", created, err)
	}
	if _, err := db.UpsertProviderProfileSetting(ctx, UpsertProviderProfileSettingParams{
		ProfileID: "work", ProviderID: "codex", QuotaRefreshIntervalSeconds: 900,
	}); err == nil {
		t.Fatal("expected unsupported interval rejection")
	}
	if err := db.DeleteProfile(ctx, "work"); err != nil {
		t.Fatalf("expected profile delete with local settings cleanup, got %v", err)
	}
	if _, err := db.GetProviderProfileSetting(ctx, "work", "codex"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected deleted Profile settings cleanup, got %v", err)
	}
}

func TestProviderCRUD(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "profiledeck.db")

	db := openTestStore(t, ctx, dbPath, false)
	defer closeTestStore(t, db)

	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("expected migrations to succeed, got %v", err)
	}

	if _, err := db.CreateProvider(ctx, CreateProviderParams{
		ID:           "provider-b",
		Name:         "Provider B",
		AdapterID:    "adapter-b",
		Enabled:      false,
		MetadataJSON: `{"region":"us"}`,
	}); err != nil {
		t.Fatalf("expected disabled provider create to succeed, got %v", err)
	}
	provider, err := db.CreateProvider(ctx, CreateProviderParams{
		ID:           "provider-a",
		Name:         "Provider A",
		AdapterID:    "adapter-a",
		Enabled:      true,
		MetadataJSON: `{}`,
	})
	if err != nil {
		t.Fatalf("expected provider create to succeed, got %v", err)
	}
	if provider.ID != "provider-a" || !provider.Enabled || provider.MetadataJSON != "{}" {
		t.Fatalf("unexpected created provider: %#v", provider)
	}
	if _, err := db.CreateProvider(ctx, CreateProviderParams{
		ID:           "provider-a",
		Name:         "Duplicate",
		AdapterID:    "adapter-a",
		Enabled:      true,
		MetadataJSON: `{}`,
	}); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("expected duplicate provider error, got %v", err)
	}

	enabledOnly, err := db.ListProviders(ctx, false)
	if err != nil {
		t.Fatalf("expected provider list to succeed, got %v", err)
	}
	if gotIDs(enabledOnly) != "provider-a" {
		t.Fatalf("expected enabled provider list to contain provider-a, got %#v", enabledOnly)
	}

	all, err := db.ListProviders(ctx, true)
	if err != nil {
		t.Fatalf("expected all provider list to succeed, got %v", err)
	}
	if gotIDs(all) != "provider-a,provider-b" {
		t.Fatalf("expected id-sorted provider list, got %#v", all)
	}

	name := "Provider A Updated"
	adapterID := "adapter-updated"
	enabled := false
	metadata := `{"tier":"paid"}`
	updated, err := db.UpdateProvider(ctx, UpdateProviderParams{
		ID:           "provider-a",
		Name:         &name,
		AdapterID:    &adapterID,
		Enabled:      &enabled,
		MetadataJSON: &metadata,
	})
	if err != nil {
		t.Fatalf("expected provider update to succeed, got %v", err)
	}
	if updated.Name != name || updated.AdapterID != adapterID || updated.Enabled || updated.MetadataJSON != metadata {
		t.Fatalf("unexpected updated provider: %#v", updated)
	}
	metadataOnly := `{"region":"eu"}`
	updated, err = db.UpdateProvider(ctx, UpdateProviderParams{
		ID:           "provider-a",
		MetadataJSON: &metadataOnly,
	})
	if err != nil {
		t.Fatalf("expected provider partial update to succeed, got %v", err)
	}
	if updated.Name != name || updated.AdapterID != adapterID || updated.Enabled || updated.MetadataJSON != metadataOnly {
		t.Fatalf("expected provider partial update to preserve omitted fields, got %#v", updated)
	}

	if err := db.DeleteProvider(ctx, "provider-a"); err != nil {
		t.Fatalf("expected provider delete to succeed, got %v", err)
	}
	if _, err := db.GetProvider(ctx, "provider-a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected deleted provider to be missing, got %v", err)
	}
}

func TestProviderDeleteInUseProtection(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "profiledeck.db")

	db := openTestStore(t, ctx, dbPath, false)
	defer closeTestStore(t, db)

	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("expected migrations to succeed, got %v", err)
	}
	if _, err := db.CreateProvider(ctx, CreateProviderParams{
		ID:           "provider-active",
		Name:         "Provider Active",
		AdapterID:    "generic",
		Enabled:      true,
		MetadataJSON: `{}`,
	}); err != nil {
		t.Fatalf("expected active provider create to succeed, got %v", err)
	}
	if _, err := db.db.DB.ExecContext(ctx, `
		INSERT INTO active_states (scope_type, scope_id, profile_id, operation_id, updated_at_unix_ms)
		VALUES (?, ?, ?, ?, ?)
	`, ActiveStateScopeProvider, "provider-active", "profile-a", "switch-a", 1); err != nil {
		t.Fatalf("expected active state setup to succeed, got %v", err)
	}
	if err := db.DeleteProvider(ctx, "provider-active"); !errors.Is(err, ErrInUse) {
		t.Fatalf("expected active provider delete to fail, got %v", err)
	}

	if _, err := db.CreateProvider(ctx, CreateProviderParams{
		ID:           "provider-history",
		Name:         "Provider History",
		AdapterID:    "generic",
		Enabled:      true,
		MetadataJSON: `{}`,
	}); err != nil {
		t.Fatalf("expected history provider create to succeed, got %v", err)
	}
	if _, err := db.db.DB.ExecContext(ctx, `
		INSERT INTO operations (id, operation_type, status, profile_id, metadata_json, created_at_unix_ms, updated_at_unix_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, "switch-history", OperationTypeSwitch, OperationStatusApplied, "profile-a", `{"provider_id":"provider-history"}`, 1, 1); err != nil {
		t.Fatalf("expected operation setup to succeed, got %v", err)
	}
	if err := db.DeleteProvider(ctx, "provider-history"); !errors.Is(err, ErrInUse) {
		t.Fatalf("expected operation-referenced provider delete to fail, got %v", err)
	}
}

func TestConcurrentCreateDuplicateReturnsAlreadyExists(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "profiledeck.db")

	db := openTestStore(t, ctx, dbPath, false)
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("expected migrations to succeed, got %v", err)
	}
	closeTestStore(t, db)

	assertConcurrentProviderCreate(t, ctx, dbPath)
	assertConcurrentProfileCreate(t, ctx, dbPath)
}

func TestProfileCRUDAndInUseDelete(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "profiledeck.db")

	db := openTestStore(t, ctx, dbPath, false)
	defer closeTestStore(t, db)

	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("expected migrations to succeed, got %v", err)
	}

	for _, params := range []CreateProfileParams{
		{ID: "profile-b", Name: "Profile B", Description: "B", MetadataJSON: `{}`},
		{ID: "profile-a", Name: "Profile A", Description: "", MetadataJSON: `{"mode":"work"}`},
		{ID: "profile-c", Name: "Profile C", Description: "", MetadataJSON: `{}`},
	} {
		if _, err := db.CreateProfile(ctx, params); err != nil {
			t.Fatalf("expected profile create to succeed, got %v", err)
		}
	}
	if _, err := db.CreateProfile(ctx, CreateProfileParams{
		ID:           "profile-a",
		Name:         "Duplicate",
		Description:  "",
		MetadataJSON: `{}`,
	}); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("expected duplicate profile error, got %v", err)
	}

	profiles, err := db.ListProfiles(ctx)
	if err != nil {
		t.Fatalf("expected profile list to succeed, got %v", err)
	}
	if gotProfileIDs(profiles) != "profile-a,profile-b,profile-c" {
		t.Fatalf("expected id-sorted profile list, got %#v", profiles)
	}

	description := "Updated profile"
	metadata := `{"mode":"personal"}`
	updated, err := db.UpdateProfile(ctx, UpdateProfileParams{
		ID:           "profile-a",
		Description:  &description,
		MetadataJSON: &metadata,
	})
	if err != nil {
		t.Fatalf("expected profile update to succeed, got %v", err)
	}
	if updated.Description != description || updated.MetadataJSON != metadata {
		t.Fatalf("unexpected updated profile: %#v", updated)
	}
	name := "Profile A Updated"
	updated, err = db.UpdateProfile(ctx, UpdateProfileParams{
		ID:   "profile-a",
		Name: &name,
	})
	if err != nil {
		t.Fatalf("expected profile partial update to succeed, got %v", err)
	}
	if updated.Name != name || updated.Description != description || updated.MetadataJSON != metadata {
		t.Fatalf("expected profile partial update to preserve omitted fields, got %#v", updated)
	}

	_, err = db.db.DB.ExecContext(ctx, `
		INSERT INTO active_states (scope_type, scope_id, profile_id, updated_at_unix_ms)
		VALUES ('global', 'default', 'profile-a', 1)
	`)
	if err != nil {
		t.Fatalf("expected active state setup to succeed, got %v", err)
	}
	if err := db.DeleteProfile(ctx, "profile-a"); !errors.Is(err, ErrInUse) {
		t.Fatalf("expected in-use profile delete to fail, got %v", err)
	}

	_, err = db.db.DB.ExecContext(ctx, `
		INSERT INTO operations (id, operation_type, status, profile_id, created_at_unix_ms, updated_at_unix_ms)
		VALUES ('operation-c', 'switch', 'applied', 'profile-c', 1, 1)
	`)
	if err != nil {
		t.Fatalf("expected operation setup to succeed, got %v", err)
	}
	if err := db.DeleteProfile(ctx, "profile-c"); !errors.Is(err, ErrInUse) {
		t.Fatalf("expected operation-referenced profile delete to fail, got %v", err)
	}

	if err := db.DeleteProfile(ctx, "profile-b"); err != nil {
		t.Fatalf("expected unused profile delete to succeed, got %v", err)
	}
	if _, err := db.GetProfile(ctx, "profile-b"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected deleted profile to be missing, got %v", err)
	}
}

func TestProfileTargetCRUDAndReferences(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "profiledeck.db")

	db := openTestStore(t, ctx, dbPath, false)
	defer closeTestStore(t, db)

	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("expected migrations to succeed, got %v", err)
	}
	if _, err := db.CreateProvider(ctx, CreateProviderParams{
		ID:           "provider-a",
		Name:         "Provider A",
		AdapterID:    "generic",
		Enabled:      true,
		MetadataJSON: `{}`,
	}); err != nil {
		t.Fatalf("expected provider create to succeed, got %v", err)
	}
	if _, err := db.CreateProfile(ctx, CreateProfileParams{
		ID:           "profile-a",
		Name:         "Profile A",
		Description:  "",
		MetadataJSON: `{}`,
	}); err != nil {
		t.Fatalf("expected profile create to succeed, got %v", err)
	}

	targetPath := filepath.Join(t.TempDir(), "target-b.txt")
	target, err := db.CreateProfileTarget(ctx, CreateProfileTargetParams{
		ProfileID:    "profile-a",
		ProviderID:   "provider-a",
		TargetID:     "target-b",
		Path:         targetPath,
		Format:       "text",
		Strategy:     "replace-file",
		ValueJSON:    `{"content":"b"}`,
		Enabled:      true,
		MetadataJSON: `{}`,
	})
	if err != nil {
		t.Fatalf("expected profile target create to succeed, got %v", err)
	}
	if target.TargetID != "target-b" || target.PathKey != targetPath || !target.Enabled {
		t.Fatalf("unexpected created target: %#v", target)
	}
	if _, err := db.CreateProfileTarget(ctx, CreateProfileTargetParams{
		ProfileID:    "profile-a",
		ProviderID:   "provider-a",
		TargetID:     "target-a",
		Path:         filepath.Join(t.TempDir(), "target-a.txt"),
		Format:       "json",
		Strategy:     "json-merge",
		ValueJSON:    `{"model":"x"}`,
		Enabled:      false,
		MetadataJSON: `{}`,
	}); err != nil {
		t.Fatalf("expected disabled profile target create to succeed, got %v", err)
	}

	enabledTargets, err := db.ListProfileTargets(ctx, "profile-a", "provider-a", false)
	if err != nil {
		t.Fatalf("expected profile target list to succeed, got %v", err)
	}
	if gotTargetIDs(enabledTargets) != "target-b" {
		t.Fatalf("expected only enabled target-b, got %#v", enabledTargets)
	}
	allTargets, err := db.ListProfileTargets(ctx, "profile-a", "provider-a", true)
	if err != nil {
		t.Fatalf("expected all profile target list to succeed, got %v", err)
	}
	if gotTargetIDs(allTargets) != "target-a,target-b" {
		t.Fatalf("expected target-id sorted list, got %#v", allTargets)
	}

	updatedPath := filepath.Join(t.TempDir(), "updated.txt")
	enabled := true
	updated, err := db.UpdateProfileTarget(ctx, UpdateProfileTargetParams{
		ProfileID:  "profile-a",
		ProviderID: "provider-a",
		TargetID:   "target-a",
		Path:       &updatedPath,
		Enabled:    &enabled,
	})
	if err != nil {
		t.Fatalf("expected profile target update to succeed, got %v", err)
	}
	if updated.Path != updatedPath || updated.PathKey != updatedPath || !updated.Enabled {
		t.Fatalf("unexpected updated target: %#v", updated)
	}

	providerRefs, err := db.CountProviderTargetReferences(ctx, "provider-a")
	if err != nil {
		t.Fatalf("expected provider reference count to succeed, got %v", err)
	}
	profileRefs, err := db.CountProfileTargetReferences(ctx, "profile-a")
	if err != nil {
		t.Fatalf("expected profile reference count to succeed, got %v", err)
	}
	if providerRefs != 2 || profileRefs != 2 {
		t.Fatalf("unexpected target references: provider=%d profile=%d", providerRefs, profileRefs)
	}
	if err := db.DeleteProvider(ctx, "provider-a"); !errors.Is(err, ErrInUse) {
		t.Fatalf("expected target-referenced provider delete to fail, got %v", err)
	}
	if err := db.DeleteProfile(ctx, "profile-a"); !errors.Is(err, ErrInUse) {
		t.Fatalf("expected target-referenced profile delete to fail, got %v", err)
	}

	if err := db.DeleteProfileTarget(ctx, "profile-a", "provider-a", "target-a"); err != nil {
		t.Fatalf("expected profile target delete to succeed, got %v", err)
	}
	if _, err := db.GetProfileTarget(ctx, "profile-a", "provider-a", "target-a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected deleted target to be missing, got %v", err)
	}
}

func TestProfileTargetUniquePath(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "profiledeck.db")

	db := openTestStore(t, ctx, dbPath, false)
	defer closeTestStore(t, db)

	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("expected migrations to succeed, got %v", err)
	}
	if _, err := db.CreateProvider(ctx, CreateProviderParams{
		ID:           "provider-a",
		Name:         "Provider A",
		AdapterID:    "generic",
		Enabled:      true,
		MetadataJSON: `{}`,
	}); err != nil {
		t.Fatalf("expected first provider create to succeed, got %v", err)
	}
	if _, err := db.CreateProvider(ctx, CreateProviderParams{
		ID:           "provider-b",
		Name:         "Provider B",
		AdapterID:    "generic",
		Enabled:      true,
		MetadataJSON: `{}`,
	}); err != nil {
		t.Fatalf("expected second provider create to succeed, got %v", err)
	}
	if _, err := db.CreateProfile(ctx, CreateProfileParams{
		ID:           "profile-a",
		Name:         "Profile A",
		Description:  "",
		MetadataJSON: `{}`,
	}); err != nil {
		t.Fatalf("expected first profile create to succeed, got %v", err)
	}
	if _, err := db.CreateProfile(ctx, CreateProfileParams{
		ID:           "profile-b",
		Name:         "Profile B",
		Description:  "",
		MetadataJSON: `{}`,
	}); err != nil {
		t.Fatalf("expected second profile create to succeed, got %v", err)
	}
	path := filepath.Join(t.TempDir(), "target.txt")
	for _, tc := range []struct {
		profileID  string
		providerID string
		targetID   string
		wantErr    error
	}{
		{profileID: "profile-a", providerID: "provider-a", targetID: "target-a"},
		{profileID: "profile-b", providerID: "provider-a", targetID: "target-a"},
		{profileID: "profile-a", providerID: "provider-a", targetID: "target-b", wantErr: ErrPathOwned},
		{profileID: "profile-b", providerID: "provider-b", targetID: "target-b", wantErr: ErrPathOwned},
	} {
		_, err := db.CreateProfileTarget(ctx, CreateProfileTargetParams{
			ProfileID:    tc.profileID,
			ProviderID:   tc.providerID,
			TargetID:     tc.targetID,
			Path:         path,
			Format:       "text",
			Strategy:     "replace-file",
			ValueJSON:    `{"content":"x"}`,
			Enabled:      true,
			MetadataJSON: `{}`,
		})
		if tc.wantErr == nil && err != nil {
			t.Fatalf("expected target create to succeed for %#v, got %v", tc, err)
		}
		if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
			t.Fatalf("expected duplicate scoped target path to fail with %v, got %v", tc.wantErr, err)
		}
	}
}

func TestProfileTargetPathKeyPreventsCaseVariantOwnershipBypass(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "profiledeck.db")

	db := openTestStore(t, ctx, dbPath, false)
	defer closeTestStore(t, db)

	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("expected migrations to succeed, got %v", err)
	}

	targetDir := t.TempDir()
	lowerPath := filepath.Join(targetDir, "settings.json")
	upperPath := filepath.Join(targetDir, "SETTINGS.JSON")
	pathKey := strings.ToLower(lowerPath)
	if _, err := db.CreateProfileTarget(ctx, CreateProfileTargetParams{
		ProfileID:    "profile-a",
		ProviderID:   "provider-a",
		TargetID:     "settings",
		Path:         lowerPath,
		PathKey:      pathKey,
		Format:       "text",
		Strategy:     "replace-file",
		ValueJSON:    `{"content":"x"}`,
		Enabled:      true,
		MetadataJSON: `{}`,
	}); err != nil {
		t.Fatalf("expected first case variant target create to succeed, got %v", err)
	}

	_, err := db.CreateProfileTarget(ctx, CreateProfileTargetParams{
		ProfileID:    "profile-b",
		ProviderID:   "provider-b",
		TargetID:     "settings",
		Path:         upperPath,
		PathKey:      strings.ToLower(upperPath),
		Format:       "text",
		Strategy:     "replace-file",
		ValueJSON:    `{"content":"x"}`,
		Enabled:      true,
		MetadataJSON: `{}`,
	})
	if !errors.Is(err, ErrPathOwned) {
		t.Fatalf("expected case variant target path to fail with ErrPathOwned, got %v", err)
	}
}

func TestConcurrentProfileTargetPathOwnerConflict(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "profiledeck.db")

	db := openTestStore(t, ctx, dbPath, false)
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("expected migrations to succeed, got %v", err)
	}
	closeTestStore(t, db)

	const workers = 8
	path := filepath.Join(t.TempDir(), "shared-target.txt")
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			db, err := Open(ctx, dbPath, false)
			if err != nil {
				errs <- err
				return
			}

			_, err = db.CreateProfileTarget(ctx, CreateProfileTargetParams{
				ProfileID:    fmt.Sprintf("profile-%d", worker),
				ProviderID:   fmt.Sprintf("provider-%d", worker),
				TargetID:     "settings",
				Path:         path,
				Format:       "text",
				Strategy:     "replace-file",
				ValueJSON:    `{"content":"x"}`,
				Enabled:      true,
				MetadataJSON: `{}`,
			})
			if closeErr := db.Close(); err == nil {
				err = closeErr
			}
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)

	assertOneSuccessAndRestErr(t, errs, workers, ErrPathOwned)
}

func TestSchemaMissingDatabaseIsUnhealthy(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "profiledeck.db")

	sqlDB, err := sql.Open(sqliteDriverName, sqliteDSN(dbPath, false))
	if err != nil {
		t.Fatalf("expected sqlite open to succeed, got %v", err)
	}
	if err := sqlDB.PingContext(ctx); err != nil {
		t.Fatalf("expected sqlite ping to succeed, got %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("expected sqlite close to succeed, got %v", err)
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("expected database file to exist, got %v", err)
	}

	db := openTestStore(t, ctx, dbPath, true)
	defer closeTestStore(t, db)

	status, err := db.Status(ctx)
	if err != nil {
		t.Fatalf("expected status to succeed, got %v", err)
	}
	if status.SchemaHealthy {
		t.Fatalf("expected missing schema to be unhealthy")
	}
}

func TestDriftedSchemaIsUnhealthy(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "profiledeck.db")

	db := openTestStore(t, ctx, dbPath, false)
	defer closeTestStore(t, db)

	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("expected migrations to succeed, got %v", err)
	}
	if _, err := db.db.DB.ExecContext(ctx, `ALTER TABLE providers DROP COLUMN metadata_json`); err != nil {
		t.Fatalf("expected drifted schema setup to succeed, got %v", err)
	}

	status, err := db.Status(ctx)
	if err != nil {
		t.Fatalf("expected status to succeed for drifted schema, got %v", err)
	}
	if status.SchemaHealthy {
		t.Fatalf("expected drifted schema to be unhealthy")
	}
}

func TestDriftedIndexIsUnhealthy(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "profiledeck.db")

	db := openTestStore(t, ctx, dbPath, false)
	defer closeTestStore(t, db)

	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("expected migrations to succeed, got %v", err)
	}
	if _, err := db.db.DB.ExecContext(ctx, "DROP INDEX idx_operations_status"); err != nil {
		t.Fatalf("expected index drop to succeed, got %v", err)
	}
	if _, err := db.db.DB.ExecContext(ctx, "CREATE INDEX idx_operations_status ON operations(operation_type)"); err != nil {
		t.Fatalf("expected drifted index setup to succeed, got %v", err)
	}

	status, err := db.Status(ctx)
	if err != nil {
		t.Fatalf("expected status to succeed for drifted index, got %v", err)
	}
	if status.SchemaHealthy {
		t.Fatalf("expected drifted index to be unhealthy")
	}
}

func TestDriftedUniquePathIndexIsUnhealthy(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "profiledeck.db")

	db := openTestStore(t, ctx, dbPath, false)
	defer closeTestStore(t, db)

	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("expected migrations to succeed, got %v", err)
	}
	if _, err := db.db.DB.ExecContext(ctx, "DROP INDEX idx_profile_targets_unique_path"); err != nil {
		t.Fatalf("expected unique path index drop to succeed, got %v", err)
	}
	if _, err := db.db.DB.ExecContext(ctx, "CREATE INDEX idx_profile_targets_unique_path ON profile_targets(profile_id, provider_id, path_key)"); err != nil {
		t.Fatalf("expected non-unique path index setup to succeed, got %v", err)
	}

	status, err := db.Status(ctx)
	if err != nil {
		t.Fatalf("expected status to succeed for drifted unique path index, got %v", err)
	}
	if status.SchemaHealthy {
		t.Fatalf("expected non-unique target path index to be unhealthy")
	}
}

func TestMissingPathOwnerTriggerIsUnhealthy(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "profiledeck.db")

	db := openTestStore(t, ctx, dbPath, false)
	defer closeTestStore(t, db)

	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("expected migrations to succeed, got %v", err)
	}
	if _, err := db.db.DB.ExecContext(ctx, "DROP TRIGGER trg_profile_targets_path_owner_insert"); err != nil {
		t.Fatalf("expected path owner trigger drop to succeed, got %v", err)
	}

	status, err := db.Status(ctx)
	if err != nil {
		t.Fatalf("expected status to succeed for missing path owner trigger, got %v", err)
	}
	if status.SchemaHealthy {
		t.Fatalf("expected missing path owner trigger to be unhealthy")
	}
}

func TestStatusIgnoresExtraExpressionIndexes(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "profiledeck.db")

	db := openTestStore(t, ctx, dbPath, false)
	defer closeTestStore(t, db)

	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("expected migrations to succeed, got %v", err)
	}
	if _, err := db.db.DB.ExecContext(ctx, "CREATE INDEX idx_providers_name_expr ON providers(lower(name))"); err != nil {
		t.Fatalf("expected expression index setup to succeed, got %v", err)
	}

	status, err := db.Status(ctx)
	if err != nil {
		t.Fatalf("expected status to ignore extra expression indexes, got %v", err)
	}
	if !status.SchemaHealthy {
		t.Fatalf("expected schema with extra expression index to remain healthy")
	}
}

func TestExpressionIndexForRequiredNameIsUnhealthy(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "profiledeck.db")

	db := openTestStore(t, ctx, dbPath, false)
	defer closeTestStore(t, db)

	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("expected migrations to succeed, got %v", err)
	}
	if _, err := db.db.DB.ExecContext(ctx, "DROP INDEX idx_providers_enabled"); err != nil {
		t.Fatalf("expected index drop to succeed, got %v", err)
	}
	if _, err := db.db.DB.ExecContext(ctx, "CREATE INDEX idx_providers_enabled ON providers(lower(name))"); err != nil {
		t.Fatalf("expected expression index setup to succeed, got %v", err)
	}

	status, err := db.Status(ctx)
	if err != nil {
		t.Fatalf("expected expression index on required name to report unhealthy without error, got %v", err)
	}
	if status.SchemaHealthy {
		t.Fatalf("expected expression index on required name to be unhealthy")
	}
}

func openTestStore(t *testing.T, ctx context.Context, dbPath string, readOnly bool) *Store {
	t.Helper()

	db, err := Open(ctx, dbPath, readOnly)
	if err != nil {
		t.Fatalf("expected store open to succeed, got %v", err)
	}
	return db
}

func migratedTestStore(t *testing.T, ctx context.Context) *Store {
	t.Helper()
	db := openTestStore(t, ctx, filepath.Join(t.TempDir(), "profiledeck.db"), false)
	if _, err := db.Migrate(ctx); err != nil {
		_ = db.Close()
		t.Fatalf("migrate test store: %v", err)
	}
	return db
}

func assertIntegrityIssue(t *testing.T, ctx context.Context, db *Store, kind string) IntegrityReport {
	t.Helper()
	report, err := db.InspectIntegrity(ctx, IntegrityCurrentBaseline)
	if err != nil {
		t.Fatalf("InspectIntegrity() error = %v", err)
	}
	if report.Healthy {
		t.Fatalf("InspectIntegrity() report unexpectedly healthy: %#v", report)
	}
	for _, issue := range report.Issues {
		if issue.Kind == kind && issue.Count > 0 {
			return report
		}
	}
	t.Fatalf("InspectIntegrity() issues = %#v, want %q", report.Issues, kind)
	return IntegrityReport{}
}

func closeTestStore(t *testing.T, db *Store) {
	t.Helper()

	if err := db.Close(); err != nil {
		t.Fatalf("expected store close to succeed, got %v", err)
	}
}

func assertSQLiteObjectExists(t *testing.T, ctx context.Context, db *Store, objectType, name string) {
	t.Helper()

	exists, err := db.objectExists(ctx, objectType, name)
	if err != nil {
		t.Fatalf("expected object lookup to succeed for %s %s, got %v", objectType, name, err)
	}
	if !exists {
		t.Fatalf("expected %s %s to exist", objectType, name)
	}
}

func createTestMigrationTable(t *testing.T, ctx context.Context, db *Store) {
	t.Helper()
	for _, statement := range []string{
		`CREATE TABLE bun_migrations (
			"id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
			"name" VARCHAR,
			"group_id" INTEGER,
			"migrated_at" TIMESTAMP NOT NULL DEFAULT current_timestamp
		)`,
		`CREATE UNIQUE INDEX "bun_migrations_name_unique" ON bun_migrations ("name")`,
		`CREATE TABLE bun_migration_locks (
			"id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
			"table_name" VARCHAR,
			UNIQUE ("table_name")
		)`,
	} {
		if _, err := db.db.DB.ExecContext(ctx, statement); err != nil {
			t.Fatalf("create migration infrastructure: %v", err)
		}
	}
}

func insertTestMigration(t *testing.T, ctx context.Context, db *Store, name string) int64 {
	t.Helper()
	result, err := db.db.DB.ExecContext(ctx, `
		INSERT INTO bun_migrations (name, group_id, migrated_at)
		VALUES (?, 1, CURRENT_TIMESTAMP)
	`, name)
	if err != nil {
		t.Fatalf("insert migration %q: %v", name, err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("read migration id for %q: %v", name, err)
	}
	return id
}

func assertConcurrentProviderCreate(t *testing.T, ctx context.Context, dbPath string) {
	t.Helper()

	const workers = 8
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			db, err := Open(ctx, dbPath, false)
			if err != nil {
				errs <- err
				return
			}

			_, err = db.CreateProvider(ctx, CreateProviderParams{
				ID:           "provider-concurrent",
				Name:         "Provider Concurrent",
				AdapterID:    "adapter-concurrent",
				Enabled:      true,
				MetadataJSON: `{}`,
			})
			if closeErr := db.Close(); err == nil {
				err = closeErr
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)

	assertOneSuccessAndRestAlreadyExists(t, errs, workers)
}

func assertConcurrentProfileCreate(t *testing.T, ctx context.Context, dbPath string) {
	t.Helper()

	const workers = 8
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			db, err := Open(ctx, dbPath, false)
			if err != nil {
				errs <- err
				return
			}

			_, err = db.CreateProfile(ctx, CreateProfileParams{
				ID:           "profile-concurrent",
				Name:         "Profile Concurrent",
				Description:  "",
				MetadataJSON: `{}`,
			})
			if closeErr := db.Close(); err == nil {
				err = closeErr
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)

	assertOneSuccessAndRestAlreadyExists(t, errs, workers)
}

func assertOneSuccessAndRestAlreadyExists(t *testing.T, errs <-chan error, total int) {
	t.Helper()

	assertOneSuccessAndRestErr(t, errs, total, ErrAlreadyExists)
}

func assertOneSuccessAndRestErr(t *testing.T, errs <-chan error, total int, want error) {
	t.Helper()

	successes := 0
	failures := 0
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, want):
			failures++
		default:
			t.Fatalf("expected nil or %v, got %v", want, err)
		}
	}
	if successes != 1 || failures != total-1 {
		t.Fatalf("expected one success and %d %v, got success=%d failures=%d", total-1, want, successes, failures)
	}
}

func gotIDs(providers []Provider) string {
	ids := make([]string, 0, len(providers))
	for _, provider := range providers {
		ids = append(ids, provider.ID)
	}
	return strings.Join(ids, ",")
}

func gotProfileIDs(profiles []Profile) string {
	ids := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		ids = append(ids, profile.ID)
	}
	return strings.Join(ids, ",")
}

func operationIDs(operations []Operation) string {
	ids := make([]string, 0, len(operations))
	for _, operation := range operations {
		ids = append(ids, operation.ID)
	}
	return strings.Join(ids, ",")
}

func gotTargetIDs(targets []ProfileTarget) string {
	ids := make([]string, 0, len(targets))
	for _, target := range targets {
		ids = append(ids, target.TargetID)
	}
	return strings.Join(ids, ",")
}
