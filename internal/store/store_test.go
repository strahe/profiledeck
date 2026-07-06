package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
	if result.Applied != 2 {
		t.Fatalf("expected 2 migrations to apply, got %d", result.Applied)
	}

	for _, table := range []string{
		"bun_migrations",
		"providers",
		"profiles",
		"settings",
		"active_states",
		"operations",
		"profile_targets",
	} {
		assertSQLiteObjectExists(t, ctx, db, "table", table)
	}

	for _, index := range []string{
		"idx_providers_adapter_id",
		"idx_providers_enabled",
		"idx_operations_status",
		"idx_operations_operation_type",
		"idx_profile_targets_profile_id",
		"idx_profile_targets_provider_id",
		"idx_profile_targets_enabled",
		"idx_profile_targets_unique_path",
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
	if migrationCount != 2 {
		t.Fatalf("expected two migration rows after concurrent migration, got %d", migrationCount)
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

	if err := db.CompleteSwitchOperation(ctx, CompleteSwitchOperationParams{
		ID:           "switch-1",
		ProfileID:    "profile-a",
		ProviderID:   "provider-a",
		MetadataJSON: `{"checkpoint":"complete"}`,
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

func TestRollbackOperationLifecycle(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "profiledeck.db")

	db := openTestStore(t, ctx, dbPath, false)
	defer closeTestStore(t, db)

	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("expected migrations to succeed, got %v", err)
	}

	operation, err := db.CreatePendingRollbackOperation(ctx, CreateRollbackOperationParams{
		ID:           "rollback-1",
		ProfileID:    "profile-a",
		MetadataJSON: `{"checkpoint":"created"}`,
	})
	if err != nil {
		t.Fatalf("expected pending rollback operation create to succeed, got %v", err)
	}
	if operation.OperationType != OperationTypeRollback || operation.Status != OperationStatusPending || operation.ProfileID != "profile-a" {
		t.Fatalf("unexpected pending rollback operation: %#v", operation)
	}

	if err := db.CompleteRollbackOperation(ctx, CompleteRollbackOperationParams{
		ID:         "rollback-1",
		ProfileID:  "profile-a",
		ProviderID: "provider-a",
		RestoredActiveState: &RollbackActiveStateParams{
			ProfileID:   "profile-a",
			OperationID: "switch-previous",
		},
		MetadataJSON: `{"checkpoint":"applied"}`,
	}); err != nil {
		t.Fatalf("expected rollback completion to succeed, got %v", err)
	}
	operation, err = db.GetOperation(ctx, "rollback-1")
	if err != nil {
		t.Fatalf("expected rollback operation read to succeed, got %v", err)
	}
	if operation.Status != OperationStatusApplied || operation.ProfileID != "profile-a" || operation.MetadataJSON != `{"checkpoint":"applied"}` {
		t.Fatalf("unexpected completed rollback operation: %#v", operation)
	}
	activeState, err := db.GetActiveState(ctx, ActiveStateScopeProvider, "provider-a")
	if err != nil {
		t.Fatalf("expected restored active state read to succeed, got %v", err)
	}
	if activeState.ProfileID != "profile-a" || activeState.OperationID != "switch-previous" {
		t.Fatalf("unexpected restored active state: %#v", activeState)
	}

	if _, err := db.CreatePendingRollbackOperation(ctx, CreateRollbackOperationParams{
		ID:           "rollback-2",
		MetadataJSON: `{"checkpoint":"created"}`,
	}); err != nil {
		t.Fatalf("expected second rollback create to succeed, got %v", err)
	}
	if err := db.CompleteRollbackOperation(ctx, CompleteRollbackOperationParams{
		ID:           "rollback-2",
		ProviderID:   "provider-a",
		MetadataJSON: `{"checkpoint":"applied"}`,
	}); err != nil {
		t.Fatalf("expected rollback completion with active delete to succeed, got %v", err)
	}
	if _, err := db.GetActiveState(ctx, ActiveStateScopeProvider, "provider-a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected active state delete after rollback, got %v", err)
	}

	if _, err := db.CreatePendingRollbackOperation(ctx, CreateRollbackOperationParams{
		ID:           "rollback-3",
		MetadataJSON: `{}`,
	}); err != nil {
		t.Fatalf("expected third rollback create to succeed, got %v", err)
	}
	failedMetadata := `{"checkpoint":"failed"}`
	if err := db.MarkOperationFailed(ctx, MarkOperationFailedParams{
		ID:           "rollback-3",
		ErrorCode:    "TARGET_CHANGED",
		ErrorMessage: "changed",
		MetadataJSON: &failedMetadata,
	}); err != nil {
		t.Fatalf("expected rollback failure mark to succeed, got %v", err)
	}
	operation, err = db.GetOperation(ctx, "rollback-3")
	if err != nil {
		t.Fatalf("expected failed rollback read to succeed, got %v", err)
	}
	if operation.Status != OperationStatusFailed || operation.ErrorCode != "TARGET_CHANGED" || operation.MetadataJSON != failedMetadata {
		t.Fatalf("unexpected failed rollback operation: %#v", operation)
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
