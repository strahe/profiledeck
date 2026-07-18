package bootstrap

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/strahe/profiledeck/internal/appbackup"
	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/runtime"
	"github.com/strahe/profiledeck/internal/store"
	storemigrations "github.com/strahe/profiledeck/internal/store/migrations"
)

func TestInitializeCreatesRuntimeWithoutBackupAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	runtimeService, err := runtime.NewService(t.TempDir())
	if err != nil {
		t.Fatalf("create runtime service: %v", err)
	}
	backups := &recordingBackupCreator{}
	service := NewService(runtimeService, backups, nil)

	first, err := service.Initialize(ctx)
	if err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}
	if !first.Initialized || !first.SchemaHealthy || first.MigrationsApplied != 3 {
		t.Fatalf("unexpected first initialization result: %#v", first)
	}
	if backups.calls != 0 {
		t.Fatalf("fresh initialization created %d migration backups", backups.calls)
	}
	for _, path := range []string{
		first.RuntimeRoot,
		filepath.Join(first.RuntimeRoot, "backups"),
		filepath.Join(first.RuntimeRoot, "recovery"),
		filepath.Join(first.RuntimeRoot, "exports"),
		filepath.Join(first.RuntimeRoot, "logs"),
		filepath.Join(first.RuntimeRoot, "locks"),
	} {
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			t.Fatalf("runtime directory %s is invalid: info=%#v err=%v", path, info, err)
		}
	}
	if _, err := os.Stat(filepath.Join(first.RuntimeRoot, "updates")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy update backup directory should not exist: %v", err)
	}
	if info, err := os.Stat(first.DatabasePath); err != nil || info.IsDir() {
		t.Fatalf("runtime database is invalid: info=%#v err=%v", info, err)
	}

	second, err := service.Initialize(ctx)
	if err != nil {
		t.Fatalf("initialize runtime again: %v", err)
	}
	if second.MigrationsApplied != 0 || backups.calls != 0 {
		t.Fatalf("unexpected repeated initialization: result=%#v backups=%d", second, backups.calls)
	}
	status, err := runtimeService.Status(ctx)
	if err != nil {
		t.Fatalf("status after initialization: %v", err)
	}
	if !status.Initialized || !status.SchemaHealthy || status.PendingOperations != 0 || status.FailedOperations != 0 {
		t.Fatalf("unexpected initialized status: %#v", status)
	}
}

func TestInitializeBacksUpKnownOldBaselineBeforeMigrating(t *testing.T) {
	ctx := context.Background()
	runtimeService := newRuntimeService(t)
	createInitialBaseline(t, ctx, runtimeService)
	insertSetting(t, ctx, runtimeService.Paths().Database, "upgrade-data", `{"kept":true}`)
	backups := &recordingBackupCreator{
		inspect: func(req appbackup.CreateRequest) {
			if req.Kind != appbackup.KindAutomatic || req.Reason != appbackup.ReasonBeforeMigration {
				t.Fatalf("backup request = %#v", req)
			}
			snapshot := inspectDatabaseSnapshot(t, runtimeService.Paths().Database)
			if len(snapshot.markers) != 1 || snapshot.usageTable {
				t.Fatalf("database changed before backup: %#v", snapshot)
			}
		},
	}
	service := NewService(runtimeService, backups, nil)

	result, err := service.Initialize(ctx)
	if err != nil {
		t.Fatalf("upgrade old baseline: %v", err)
	}
	if result.MigrationsApplied != 2 || backups.calls != 1 {
		t.Fatalf("upgrade result = %#v, backups = %d", result, backups.calls)
	}
	snapshot := inspectDatabaseSnapshot(t, runtimeService.Paths().Database)
	if len(snapshot.markers) != 3 || !snapshot.usageTable || snapshot.setting != `{"kept":true}` {
		t.Fatalf("database after upgrade = %#v", snapshot)
	}
	if _, err := service.Initialize(ctx); err != nil || backups.calls != 1 {
		t.Fatalf("repeated initialization error = %v, backups = %d", err, backups.calls)
	}
}

func TestInitializeBackupFailureLeavesOldBaselineUnchanged(t *testing.T) {
	ctx := context.Background()
	runtimeService := newRuntimeService(t)
	createInitialBaseline(t, ctx, runtimeService)
	insertSetting(t, ctx, runtimeService.Paths().Database, "upgrade-data", `{"kept":true}`)
	before := inspectDatabaseSnapshot(t, runtimeService.Paths().Database)
	backups := &recordingBackupCreator{err: errors.New("SECRET_BACKUP_FAILURE_SENTINEL")}

	_, err := NewService(runtimeService, backups, nil).Initialize(ctx)
	assertAppErrorCode(t, err, apperror.StoreMigrationFailed)
	if strings.Contains(err.Error(), "SECRET_BACKUP_FAILURE_SENTINEL") {
		t.Fatalf("initialization exposed backup error: %v", err)
	}
	after := inspectDatabaseSnapshot(t, runtimeService.Paths().Database)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("database changed after backup failure:\nbefore=%#v\nafter=%#v", before, after)
	}
}

func TestInitializeRejectsNonContiguousHistoryBeforeBackup(t *testing.T) {
	ctx := context.Background()
	runtimeService := newRuntimeService(t)
	if _, err := NewService(runtimeService, nil, nil).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	registered := storemigrations.Migrations.Sorted()
	execDatabaseStatements(t, runtimeService.Paths().Database,
		`DELETE FROM bun_migrations WHERE name = '`+registered[1].Name+`'`,
	)
	backups := &recordingBackupCreator{}

	_, err := NewService(runtimeService, backups, nil).Initialize(ctx)
	assertAppErrorCode(t, err, apperror.StoreSchemaInvalid)
	if backups.calls != 0 {
		t.Fatalf("invalid migration history created %d backups", backups.calls)
	}
}

func TestInitializeRefusesUpgradeWhenAnotherDataLeaseIsActive(t *testing.T) {
	ctx := context.Background()
	runtimeService := newRuntimeService(t)
	createInitialBaseline(t, ctx, runtimeService)
	blocker, err := runtime.AcquireDataLease(runtimeService.Paths().DataLock)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close()
	before := inspectDatabaseSnapshot(t, runtimeService.Paths().Database)
	backups := &recordingBackupCreator{}

	_, err = NewService(runtimeService, backups, nil).Initialize(ctx)
	assertAppErrorCode(t, err, apperror.LockAcquireFailed)
	if backups.calls != 0 {
		t.Fatalf("blocked upgrade created %d backups", backups.calls)
	}
	if after := inspectDatabaseSnapshot(t, runtimeService.Paths().Database); !reflect.DeepEqual(after, before) {
		t.Fatalf("blocked upgrade changed database: before=%#v after=%#v", before, after)
	}
}

func TestInitializeRejectsPostMigrationSchemaDriftAndKeepsBackup(t *testing.T) {
	ctx := context.Background()
	runtimeService := newRuntimeService(t)
	createInitialBaseline(t, ctx, runtimeService)
	execDatabaseStatements(t, runtimeService.Paths().Database, `CREATE TABLE profile_targets (
		profile_id TEXT,
		provider_id TEXT,
		target_id TEXT,
		path TEXT,
		path_key TEXT,
		format TEXT,
		strategy TEXT,
		value_json TEXT,
		enabled INTEGER,
		metadata_json TEXT,
		created_at_unix_ms INTEGER,
		updated_at_unix_ms INTEGER,
		PRIMARY KEY (profile_id, provider_id, target_id)
	)`)
	backups := &recordingBackupCreator{}

	_, err := NewService(runtimeService, backups, nil).Initialize(ctx)
	assertAppErrorCode(t, err, apperror.StoreSchemaInvalid)
	if backups.calls != 1 {
		t.Fatalf("post-migration validation created %d backups", backups.calls)
	}
	snapshot := inspectDatabaseSnapshot(t, runtimeService.Paths().Database)
	if len(snapshot.markers) != 3 {
		t.Fatalf("post-validation failure masked committed migration state: %#v", snapshot)
	}
}

func TestInitializeRejectsInvalidAppliedBaselineBeforeBackup(t *testing.T) {
	for _, defect := range []string{"quick", "foreign_keys", "schema", "json", "references"} {
		t.Run(defect, func(t *testing.T) {
			ctx := context.Background()
			runtimeService := newRuntimeService(t)
			createInitialBaseline(t, ctx, runtimeService)
			applyBaselineIntegrityDefect(t, runtimeService.Paths().Database, defect)
			before := inspectDatabaseSnapshot(t, runtimeService.Paths().Database)
			backups := &recordingBackupCreator{}

			_, err := NewService(runtimeService, backups, nil).Initialize(ctx)
			assertAppErrorCode(t, err, apperror.StoreSchemaInvalid)
			if backups.calls != 0 {
				t.Fatalf("invalid baseline created %d backups", backups.calls)
			}
			if strings.Contains(err.Error(), "SECRET_INTEGRITY_SENTINEL") {
				t.Fatalf("initialization exposed stored data: %v", err)
			}
			after := inspectDatabaseSnapshot(t, runtimeService.Paths().Database)
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("invalid baseline changed during initialization: before=%#v after=%#v", before, after)
			}
		})
	}
}

type recordingBackupCreator struct {
	calls   int
	err     error
	inspect func(appbackup.CreateRequest)
}

func (creator *recordingBackupCreator) Create(
	_ context.Context,
	req appbackup.CreateRequest,
) (appbackup.BackupDetail, error) {
	creator.calls++
	if creator.inspect != nil {
		creator.inspect(req)
	}
	return appbackup.BackupDetail{}, creator.err
}

type databaseSnapshot struct {
	schemaVersion int
	markers       []string
	usageTable    bool
	setting       string
}

func newRuntimeService(t *testing.T) *runtime.Service {
	t.Helper()
	runtimeService, err := runtime.NewService(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return runtimeService
}

func createInitialBaseline(t *testing.T, ctx context.Context, runtimeService *runtime.Service) {
	t.Helper()
	if _, err := NewService(runtimeService, nil, nil).Initialize(ctx); err != nil {
		t.Fatalf("initialize fixture database: %v", err)
	}
	registered := storemigrations.Migrations.Sorted()
	if len(registered) != 3 {
		t.Fatalf("registered migrations = %d, want 3", len(registered))
	}
	execDatabaseStatements(t, runtimeService.Paths().Database,
		`DROP INDEX idx_usage_import_cursors_source`,
		`DROP TABLE usage_import_cursors`,
		`DROP INDEX idx_usage_events_provider_cost_model_id`,
		`DROP INDEX idx_usage_events_cost_status`,
		`DROP INDEX idx_usage_events_occurred_at`,
		`DROP INDEX idx_usage_events_model`,
		`DROP INDEX idx_usage_events_source_key`,
		`DROP INDEX idx_usage_events_source`,
		`DROP INDEX idx_usage_events_provider_id`,
		`DROP TABLE usage_events`,
		`DROP TRIGGER trg_profile_targets_path_owner_update`,
		`DROP TRIGGER trg_profile_targets_path_owner_insert`,
		`DROP INDEX idx_profile_targets_unique_path`,
		`DROP INDEX idx_profile_targets_enabled`,
		`DROP INDEX idx_profile_targets_provider_id`,
		`DROP INDEX idx_profile_targets_profile_id`,
		`DROP TABLE profile_targets`,
		`DELETE FROM bun_migrations WHERE name IN ('`+registered[1].Name+`', '`+registered[2].Name+`')`,
	)
}

func insertSetting(t *testing.T, ctx context.Context, path, key, value string) {
	t.Helper()
	db, err := store.Open(ctx, path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.UpsertSetting(ctx, store.UpsertSettingParams{Key: key, ValueJSON: value}); err != nil {
		t.Fatal(err)
	}
}

func inspectDatabaseSnapshot(t *testing.T, path string) databaseSnapshot {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var snapshot databaseSnapshot
	if err := db.QueryRow(`PRAGMA schema_version`).Scan(&snapshot.schemaVersion); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(`SELECT name FROM bun_migrations ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		snapshot.markers = append(snapshot.markers, name)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
	var usageCount int
	if err := db.QueryRow(`SELECT COUNT(1) FROM sqlite_master WHERE type = 'table' AND name = 'usage_events'`).Scan(&usageCount); err != nil {
		t.Fatal(err)
	}
	snapshot.usageTable = usageCount > 0
	if err := db.QueryRow(`SELECT COALESCE((SELECT value_json FROM settings WHERE key = 'upgrade-data'), '')`).Scan(&snapshot.setting); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func execDatabaseStatements(t *testing.T, path string, statements ...string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("execute fixture statement: %v", err)
		}
	}
}

func assertAppErrorCode(t *testing.T, err error, code apperror.Code) {
	t.Helper()
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != code {
		t.Fatalf("error = %v, want %s", err, code)
	}
}

func applyBaselineIntegrityDefect(t *testing.T, path, defect string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	var corruptRootPage, pageSize int64
	switch defect {
	case "quick":
		if _, err := db.Exec(`CREATE TABLE corruption_probe (value BLOB)`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO corruption_probe (value) VALUES (zeroblob(262144))`); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow(`PRAGMA page_size`).Scan(&pageSize); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow(`SELECT rootpage FROM sqlite_master WHERE name = 'corruption_probe'`).Scan(&corruptRootPage); err != nil {
			t.Fatal(err)
		}
	case "foreign_keys":
		if _, err := db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO profile_credential_bindings
			(profile_id, provider_id, slot_id, credential_id, created_at_unix_ms, updated_at_unix_ms)
			VALUES ('missing-profile', 'missing-provider', 'auth', 'missing-credential', 1, 1)`); err != nil {
			t.Fatal(err)
		}
	case "schema":
		if _, err := db.Exec(`DROP INDEX idx_providers_enabled`); err != nil {
			t.Fatal(err)
		}
	case "json":
		if _, err := db.Exec(`INSERT INTO settings (key, value_json, updated_at_unix_ms)
			VALUES ('invalid-json', '{"token":"SECRET_INTEGRITY_SENTINEL"', 1)`); err != nil {
			t.Fatal(err)
		}
	case "references":
		if _, err := db.Exec(`INSERT INTO provider_profile_settings
			(profile_id, provider_id, quota_refresh_interval_seconds, auth_keepalive_enabled, updated_at_unix_ms)
			VALUES ('missing-profile', 'missing-provider', 0, 0, 1)`); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unknown database defect %q", defect)
	}
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if corruptRootPage > 0 {
		file, err := os.OpenFile(path, os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.WriteAt(make([]byte, pageSize), (corruptRootPage-1)*pageSize); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
