package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestMigrateCreatesInitialSchema(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "profiledeck.db")

	db := openTestStore(t, ctx, dbPath, false)
	defer closeTestStore(t, db)

	result, err := db.Migrate(ctx)
	if err != nil {
		t.Fatalf("expected migrations to succeed, got %v", err)
	}
	if result.Applied != 1 {
		t.Fatalf("expected 1 migration to apply, got %d", result.Applied)
	}

	for _, table := range []string{
		"bun_migrations",
		"providers",
		"profiles",
		"settings",
		"active_states",
		"operations",
	} {
		assertSQLiteObjectExists(t, ctx, db, "table", table)
	}

	for _, index := range []string{
		"idx_providers_adapter_id",
		"idx_providers_enabled",
		"idx_operations_status",
		"idx_operations_operation_type",
	} {
		assertSQLiteObjectExists(t, ctx, db, "index", index)
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
	if migrationCount != 1 {
		t.Fatalf("expected one migration row after concurrent migration, got %d", migrationCount)
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

func TestSQLiteDSNNormalizesWindowsPathAndSetsBusyTimeout(t *testing.T) {
	dsn := sqliteDSN(`C:\Users\profiledeck\profiledeck.db`, false)

	if strings.Contains(dsn, `\`) || strings.Contains(dsn, "%5C") {
		t.Fatalf("expected Windows path separators to be normalized, got %q", dsn)
	}
	if !strings.Contains(dsn, "C:/Users/profiledeck/profiledeck.db") {
		t.Fatalf("expected normalized Windows path in DSN, got %q", dsn)
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

	for _, statement := range []string{
		`CREATE TABLE bun_migrations (id INTEGER PRIMARY KEY, name TEXT)`,
		`CREATE TABLE providers (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			adapter_id TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
			created_at_unix_ms INTEGER NOT NULL,
			updated_at_unix_ms INTEGER NOT NULL
		)`,
		`CREATE INDEX idx_providers_adapter_id ON providers(adapter_id)`,
		`CREATE INDEX idx_providers_enabled ON providers(enabled)`,
		`CREATE TABLE profiles (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			metadata_json TEXT NOT NULL DEFAULT '{}',
			created_at_unix_ms INTEGER NOT NULL,
			updated_at_unix_ms INTEGER NOT NULL
		)`,
		`CREATE TABLE settings (
			key TEXT PRIMARY KEY,
			value_json TEXT NOT NULL,
			updated_at_unix_ms INTEGER NOT NULL
		)`,
		`CREATE TABLE active_states (
			scope_type TEXT NOT NULL,
			scope_id TEXT NOT NULL,
			profile_id TEXT NOT NULL DEFAULT '',
			operation_id TEXT NOT NULL DEFAULT '',
			updated_at_unix_ms INTEGER NOT NULL,
			PRIMARY KEY (scope_type, scope_id)
		)`,
		`CREATE TABLE operations (
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
		`CREATE INDEX idx_operations_status ON operations(status)`,
		`CREATE INDEX idx_operations_operation_type ON operations(operation_type)`,
	} {
		if _, err := db.db.DB.ExecContext(ctx, statement); err != nil {
			t.Fatalf("expected drifted schema setup to succeed, got %v", err)
		}
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

func closeTestStore(t *testing.T, db *Store) {
	t.Helper()

	if err := db.Close(); err != nil {
		t.Fatalf("expected store close to succeed, got %v", err)
	}
}

func assertSQLiteObjectExists(t *testing.T, ctx context.Context, db *Store, objectType string, name string) {
	t.Helper()

	exists, err := db.objectExists(ctx, objectType, name)
	if err != nil {
		t.Fatalf("expected object lookup to succeed for %s %s, got %v", objectType, name, err)
	}
	if !exists {
		t.Fatalf("expected %s %s to exist", objectType, name)
	}
}
