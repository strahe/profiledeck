package switching

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/profile"
	"github.com/strahe/profiledeck/internal/profiletarget"
	"github.com/strahe/profiledeck/internal/store"
	switchtarget "github.com/strahe/profiledeck/internal/switching/target"
	"github.com/strahe/profiledeck/internal/switching/transaction"
	"github.com/strahe/profiledeck/internal/targetfs"
)

func TestRecoverOperationRemovesPartiallyCreatedTargetsAndResolvesSource(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	initResult, err := initSwitchingTestRuntime(ctx, configDir)
	if err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}
	createGenericProviderAndProfile(t, ctx, configDir, true)

	dir := t.TempDir()
	firstPath := filepath.Join(dir, "target-a.txt")
	secondPath := filepath.Join(dir, "missing", "target-b.txt")
	createProfileTargetForRecovery(t, ctx, configDir, "profile-a", "target-a", firstPath, "first\n")
	createProfileTargetForRecovery(t, ctx, configDir, "profile-a", "target-b", secondPath, "second\n")
	_, err = newSwitchingTestEnvironment(t, configDir).service.Apply(ctx, ApplySwitchRequest{
		ProviderID: "provider-a", ProfileID: "profile-a", Confirm: true,
	})
	assertErrorCode(t, err, apperror.TargetWriteFailed)
	failedSwitchID := singleOperationIDByTypeStatus(t, initResult.DatabasePath, store.OperationTypeSwitch, store.OperationStatusFailed)

	inspection, err := newSwitchingTestEnvironment(t, configDir).service.InspectRecovery(ctx, failedSwitchID)
	if err != nil || inspection.Status != RecoveryStatusRecoverable || inspection.Action != RecoveryActionRestore {
		t.Fatalf("unexpected recovery inspection: %#v error=%v", inspection, err)
	}
	result, err := newSwitchingTestEnvironment(t, configDir).service.RecoverOperation(ctx, RecoverOperationParams{
		OperationID: failedSwitchID, Confirm: true,
	})
	if err != nil {
		t.Fatalf("recover operation: %v", err)
	}
	if result.Action != RecoveryActionRestore || result.RecoveryOperationID == "" ||
		result.Counts.Remove != 1 || result.Counts.Noop != 1 || !result.RecoveryCleanupCompleted {
		t.Fatalf("unexpected recovery result: %#v", result)
	}
	for _, path := range []string{firstPath, secondPath} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("recovered target %s still exists: %v", path, err)
		}
	}
	_, paths, err := resolveTestRuntime(configDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(paths.Recovery, failedSwitchID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("resolved recovery point still exists: %v", err)
	}

	db := openAppTestStore(t, ctx, initResult.DatabasePath)
	defer db.Close()
	source := mustOperation(t, ctx, db, failedSwitchID)
	if source.Status != store.OperationStatusFailed || source.ResolutionKind != "recovered_pre_switch" || source.ResolvedAtUnixMS == 0 {
		t.Fatalf("source operation did not retain failure and resolution: %#v", source)
	}
	recovery := mustOperation(t, ctx, db, result.RecoveryOperationID)
	if recovery.OperationType != store.OperationTypeRecovery || recovery.Status != store.OperationStatusApplied {
		t.Fatalf("unexpected recovery operation: %#v", recovery)
	}
	incomplete, err := db.ListIncompleteOperations(ctx)
	if err != nil || len(incomplete) != 0 {
		t.Fatalf("resolved source remained in diagnostics: %#v error=%v", incomplete, err)
	}
}

func TestRecoverOperationRestoresUpdatedTargetAndPreviousActiveState(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	initResult, err := initSwitchingTestRuntime(ctx, configDir)
	if err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}
	createGenericProviderAndProfile(t, ctx, configDir, true)
	environment := newSwitchingTestEnvironment(t, configDir)
	if _, err := environment.profiles.Create(ctx, profile.CreateRequest{ID: "profile-b", Name: "Profile B"}); err != nil {
		t.Fatalf("create second profile: %v", err)
	}
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "target-a.txt")
	missingPath := filepath.Join(dir, "missing", "target-b.txt")
	createProfileTargetForRecovery(t, ctx, configDir, "profile-a", "target-a", targetPath, "profile-a\n")
	createProfileTargetForRecovery(t, ctx, configDir, "profile-b", "target-a", targetPath, "profile-b\n")
	createProfileTargetForRecovery(t, ctx, configDir, "profile-b", "target-b", missingPath, "second\n")

	firstSwitch, err := newSwitchingTestEnvironment(t, configDir).service.Apply(ctx, ApplySwitchRequest{
		ProviderID: "provider-a", ProfileID: "profile-a", Confirm: true,
	})
	if err != nil {
		t.Fatalf("apply first switch: %v", err)
	}
	_, err = newSwitchingTestEnvironment(t, configDir).service.Apply(ctx, ApplySwitchRequest{
		ProviderID: "provider-a", ProfileID: "profile-b", Confirm: true,
	})
	assertErrorCode(t, err, apperror.TargetWriteFailed)
	failedSwitchID := singleOperationIDByTypeStatus(t, initResult.DatabasePath, store.OperationTypeSwitch, store.OperationStatusFailed)

	result, err := newSwitchingTestEnvironment(t, configDir).service.RecoverOperation(ctx, RecoverOperationParams{
		OperationID: failedSwitchID, Confirm: true,
	})
	if err != nil {
		t.Fatalf("recover operation: %v", err)
	}
	if result.Counts.Restore != 1 || result.Counts.Noop != 1 || result.RestoredProfileID != "profile-a" {
		t.Fatalf("unexpected recovery result: %#v", result)
	}
	assertFileContent(t, targetPath, "profile-a\n")
	db := openAppTestStore(t, ctx, initResult.DatabasePath)
	defer db.Close()
	active, err := db.GetActiveState(ctx, store.ActiveStateScopeProvider, "provider-a")
	if err != nil || active.ProfileID != "profile-a" || active.OperationID != firstSwitch.OperationID {
		t.Fatalf("previous active state was not restored: %#v error=%v", active, err)
	}
}

func TestRecoverOperationClosesRecordBeforeTargetWrites(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	initResult, err := initSwitchingTestRuntime(ctx, configDir)
	if err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}
	db := openWritableAppTestStore(t, ctx, initResult.DatabasePath)
	if _, err := db.CreatePendingSwitchOperation(ctx, store.CreateSwitchOperationParams{
		ID: "switch-before-writes", ProfileID: "profile-a",
		MetadataJSON: "{\"checkpoint\":\"planned\",\"provider_id\":\"provider-a\",\"profile_id\":\"profile-a\"}",
	}); err != nil {
		t.Fatalf("create pending switch: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	result, err := newSwitchingTestEnvironment(t, configDir).service.RecoverOperation(ctx, RecoverOperationParams{
		OperationID: "switch-before-writes", Confirm: true,
	})
	if err != nil || result.Action != RecoveryActionClose || result.RecoveryOperationID != "" {
		t.Fatalf("unexpected close result: %#v error=%v", result, err)
	}
	db = openAppTestStore(t, ctx, initResult.DatabasePath)
	defer db.Close()
	operation := mustOperation(t, ctx, db, "switch-before-writes")
	if operation.Status != store.OperationStatusPending || operation.ResolutionKind != "closed_before_target_writes" {
		t.Fatalf("pending fact or resolution was lost: %#v", operation)
	}
	if countOperationsByType(t, initResult.DatabasePath, store.OperationTypeRecovery) != 0 {
		t.Fatal("closing a no-write record created a recovery operation")
	}
}

func TestRecoverOperationClosesWhenTargetsAreAlreadyBeforeSwitch(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	initResult, err := initSwitchingTestRuntime(ctx, configDir)
	if err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}
	createGenericProviderAndProfile(t, ctx, configDir, true)
	dir := t.TempDir()
	firstPath := filepath.Join(dir, "target-a.txt")
	secondPath := filepath.Join(dir, "missing", "target-b.txt")
	createProfileTargetForRecovery(t, ctx, configDir, "profile-a", "target-a", firstPath, "first\n")
	createProfileTargetForRecovery(t, ctx, configDir, "profile-a", "target-b", secondPath, "second\n")
	_, err = newSwitchingTestEnvironment(t, configDir).service.Apply(ctx, ApplySwitchRequest{
		ProviderID: "provider-a", ProfileID: "profile-a", Confirm: true,
	})
	assertErrorCode(t, err, apperror.TargetWriteFailed)
	failedSwitchID := singleOperationIDByTypeStatus(t, initResult.DatabasePath, store.OperationTypeSwitch, store.OperationStatusFailed)
	if err := os.Remove(firstPath); err != nil {
		t.Fatalf("restore target to pre-switch state: %v", err)
	}
	inspection, err := newSwitchingTestEnvironment(t, configDir).service.InspectRecovery(ctx, failedSwitchID)
	if err != nil || inspection.Status != RecoveryStatusClosable || inspection.Reason != "targets_already_before_switch" {
		t.Fatalf("unexpected inspection: %#v error=%v", inspection, err)
	}
	result, err := newSwitchingTestEnvironment(t, configDir).service.RecoverOperation(ctx, RecoverOperationParams{
		OperationID: failedSwitchID, Confirm: true,
	})
	if err != nil || result.Action != RecoveryActionClose {
		t.Fatalf("close unchanged operation: %#v error=%v", result, err)
	}
	db := openAppTestStore(t, ctx, initResult.DatabasePath)
	defer db.Close()
	if operation := mustOperation(t, ctx, db, failedSwitchID); operation.ResolutionKind != "closed_targets_unchanged" {
		t.Fatalf("unexpected source resolution: %#v", operation)
	}
}

func TestRecoverOperationRejectsThirdPartyTargetChangeWithoutWriting(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	initResult, err := initSwitchingTestRuntime(ctx, configDir)
	if err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}
	createGenericProviderAndProfile(t, ctx, configDir, true)
	dir := t.TempDir()
	firstPath := filepath.Join(dir, "target-a.txt")
	secondPath := filepath.Join(dir, "missing", "target-b.txt")
	createProfileTargetForRecovery(t, ctx, configDir, "profile-a", "target-a", firstPath, "first\n")
	createProfileTargetForRecovery(t, ctx, configDir, "profile-a", "target-b", secondPath, "second\n")
	_, err = newSwitchingTestEnvironment(t, configDir).service.Apply(ctx, ApplySwitchRequest{
		ProviderID: "provider-a", ProfileID: "profile-a", Confirm: true,
	})
	assertErrorCode(t, err, apperror.TargetWriteFailed)
	failedSwitchID := singleOperationIDByTypeStatus(t, initResult.DatabasePath, store.OperationTypeSwitch, store.OperationStatusFailed)
	if err := os.WriteFile(firstPath, []byte("third-party\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	inspection, err := newSwitchingTestEnvironment(t, configDir).service.InspectRecovery(ctx, failedSwitchID)
	if err != nil || inspection.Status != RecoveryStatusUnrecoverable || inspection.Reason != "target_state_unrecognized" {
		t.Fatalf("unexpected third-party inspection: %#v error=%v", inspection, err)
	}
	_, err = newSwitchingTestEnvironment(t, configDir).service.RecoverOperation(ctx, RecoverOperationParams{
		OperationID: failedSwitchID, Confirm: true,
	})
	assertErrorCode(t, err, apperror.RecoveryUnsupported)
	assertFileContent(t, firstPath, "third-party\n")
	if countOperationsByType(t, initResult.DatabasePath, store.OperationTypeRecovery) != 0 {
		t.Fatal("unsafe target state created a recovery operation")
	}
}

func TestRecoverOperationRetriesOriginalSwitchAfterPartialRecoveryFailure(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	initResult, err := initSwitchingTestRuntime(ctx, configDir)
	if err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}
	backend := &retryRecoveryFileBackend{}
	environment := newSwitchingTestEnvironmentWithTargets(t, configDir, switchtarget.MustRegistry(backend))
	createGenericProviderAndProfile(t, ctx, configDir, true)
	dir := t.TempDir()
	firstPath := filepath.Join(dir, "target-a.txt")
	secondPath := filepath.Join(dir, "target-b.txt")
	thirdPath := filepath.Join(dir, "target-c.txt")
	if err := os.WriteFile(firstPath, []byte("before-a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secondPath, []byte("before-b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(thirdPath, []byte("before-c\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, target := range []struct {
		id      string
		path    string
		desired string
	}{
		{id: "target-a", path: firstPath, desired: "after-a\n"},
		{id: "target-b", path: secondPath, desired: "after-b\n"},
		{id: "target-c", path: thirdPath, desired: "after-c\n"},
	} {
		if _, err := environment.targets.Create(ctx, profiletarget.CreateProfileTargetRequest{
			ProfileID: "profile-a", ProviderID: "provider-a", TargetID: target.id,
			Path: target.path, Format: "text", Strategy: "replace-file", ValueJSON: contentValueJSON(t, target.desired),
		}); err != nil {
			t.Fatalf("create target %s: %v", target.id, err)
		}
	}

	_, err = environment.service.Apply(ctx, ApplySwitchRequest{
		ProviderID: "provider-a", ProfileID: "profile-a", Confirm: true,
	})
	assertErrorCode(t, err, apperror.TargetWriteFailed)
	failedSwitchID := singleOperationIDByTypeStatus(t, initResult.DatabasePath, store.OperationTypeSwitch, store.OperationStatusFailed)
	assertFileContent(t, firstPath, "after-a\n")
	assertFileContent(t, secondPath, "after-b\n")
	assertFileContent(t, thirdPath, "before-c\n")

	_, err = environment.service.RecoverOperation(ctx, RecoverOperationParams{OperationID: failedSwitchID, Confirm: true})
	assertErrorCode(t, err, apperror.TargetWriteFailed)
	assertFileContent(t, firstPath, "before-a\n")
	assertFileContent(t, secondPath, "after-b\n")
	assertFileContent(t, thirdPath, "before-c\n")
	failedRecoveryID := singleOperationIDByTypeStatus(t, initResult.DatabasePath, store.OperationTypeRecovery, store.OperationStatusFailed)
	db := openAppTestStore(t, ctx, initResult.DatabasePath)
	failedRecovery := mustOperation(t, ctx, db, failedRecoveryID)
	source := mustOperation(t, ctx, db, failedSwitchID)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if failedRecovery.ResolutionKind != "recovery_attempt_failed" || failedRecovery.ResolvedAtUnixMS == 0 {
		t.Fatalf("failed recovery attempt remained a root operation: %#v", failedRecovery)
	}
	if source.ResolvedAtUnixMS != 0 {
		t.Fatalf("original switch was resolved by a failed recovery: %#v", source)
	}
	_, paths, err := resolveTestRuntime(configDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(paths.Recovery, failedSwitchID)); err != nil {
		t.Fatalf("original recovery point was removed after failed attempt: %v", err)
	}

	result, err := environment.service.RecoverOperation(ctx, RecoverOperationParams{OperationID: failedSwitchID, Confirm: true})
	if err != nil {
		t.Fatalf("retry operation recovery: %v", err)
	}
	if result.SourceOperationID != failedSwitchID || result.RecoveryOperationID == "" || result.RecoveryOperationID == failedRecoveryID {
		t.Fatalf("retry did not resolve the original switch: %#v", result)
	}
	assertFileContent(t, firstPath, "before-a\n")
	assertFileContent(t, secondPath, "before-b\n")
	assertFileContent(t, thirdPath, "before-c\n")
	if countOperationsByType(t, initResult.DatabasePath, store.OperationTypeRecovery) != 2 {
		t.Fatal("retry did not create exactly one sibling recovery attempt")
	}
	db = openAppTestStore(t, ctx, initResult.DatabasePath)
	source = mustOperation(t, ctx, db, failedSwitchID)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if source.ResolutionKind != "recovered_pre_switch" || source.ResolvedAtUnixMS == 0 {
		t.Fatalf("original switch was not resolved after retry: %#v", source)
	}
}

type retryRecoveryFileBackend struct {
	switchtarget.FileBackend
	restoreCalls int
}

func (backend *retryRecoveryFileBackend) Apply(
	ctx context.Context,
	raw switchtarget.Spec,
	snapshot switchtarget.Snapshot,
	desired string,
	mode os.FileMode,
	useMode bool,
) error {
	if spec, ok := raw.(switchtarget.FileSpec); ok && spec.ID == "target-c" {
		return apperror.New(apperror.TargetWriteFailed, "injected third target write failure")
	}
	return backend.FileBackend.Apply(ctx, raw, snapshot, desired, mode, useMode)
}

func (backend *retryRecoveryFileBackend) Restore(
	ctx context.Context,
	raw switchtarget.Spec,
	current switchtarget.Snapshot,
	sourcePath string,
	sourceSHA string,
	mode os.FileMode,
	useMode bool,
) error {
	backend.restoreCalls++
	if backend.restoreCalls == 2 {
		return apperror.New(apperror.TargetWriteFailed, "injected recovery write failure")
	}
	return backend.FileBackend.Restore(ctx, raw, current, sourcePath, sourceSHA, mode, useMode)
}

func TestInspectRecoveryReportsRunningWhileSwitchLockIsHeld(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	initResult, err := initSwitchingTestRuntime(ctx, configDir)
	if err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}
	db := openWritableAppTestStore(t, ctx, initResult.DatabasePath)
	if _, err := db.CreatePendingSwitchOperation(ctx, store.CreateSwitchOperationParams{
		ID: "switch-running", ProfileID: "profile-a", MetadataJSON: "{\"checkpoint\":\"created\"}",
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	_, paths, err := resolveTestRuntime(configDir)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := targetfs.AcquireLock(paths.Lock, "switch-running")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	inspection, err := newSwitchingTestEnvironment(t, configDir).service.InspectRecovery(ctx, "switch-running")
	if err != nil || inspection.Status != RecoveryStatusRunning || inspection.Action != "" {
		t.Fatalf("unexpected running inspection: %#v error=%v", inspection, err)
	}
	_, err = newSwitchingTestEnvironment(t, configDir).service.RecoverOperation(ctx, RecoverOperationParams{
		OperationID: "switch-running", Confirm: true,
	})
	assertErrorCode(t, err, apperror.LockAcquireFailed)
	if countOperationsByType(t, initResult.DatabasePath, store.OperationTypeRecovery) != 0 {
		t.Fatal("running switch created a recovery operation")
	}
}

func TestInspectRecoveryRejectsLegacySwitchMetadata(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	initResult, err := initSwitchingTestRuntime(ctx, configDir)
	if err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}
	db := openWritableAppTestStore(t, ctx, initResult.DatabasePath)
	if _, err := db.CreatePendingSwitchOperation(ctx, store.CreateSwitchOperationParams{
		ID:        "switch-legacy-metadata",
		ProfileID: "profile-a",
		MetadataJSON: `{
			"checkpoint":"recovery_created",
			"provider_id":"provider-a",
			"profile_id":"profile-a",
			"backup_path":"/legacy/switch-backup"
		}`,
	}); err != nil {
		t.Fatalf("create pending switch: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	inspection, err := newSwitchingTestEnvironment(t, configDir).service.InspectRecovery(ctx, "switch-legacy-metadata")
	if err != nil {
		t.Fatalf("inspect recovery: %v", err)
	}
	if inspection.Status != RecoveryStatusUnrecoverable || inspection.Reason != "operation_metadata_invalid" {
		t.Fatalf("legacy operation metadata was accepted: %#v", inspection)
	}
	_, err = newSwitchingTestEnvironment(t, configDir).service.RecoverOperation(ctx, RecoverOperationParams{
		OperationID: "switch-legacy-metadata",
		Confirm:     true,
	})
	assertErrorCode(t, err, apperror.RecoveryUnsupported)
	if countOperationsByType(t, initResult.DatabasePath, store.OperationTypeRecovery) != 0 {
		t.Fatal("legacy operation metadata created a recovery operation")
	}
}

func TestRecoveryRejectsTargetMetadataWithoutBackendID(t *testing.T) {
	t.Parallel()
	_, err := recoveryTargetsFromMetadataWithAdapter(
		switchOperationMetadata{
			ProviderID: "provider-a",
			ProfileID:  "profile-a",
			Targets: []switchOperationTargetMetadata{{
				TargetID: "target-a", Path: filepath.Join(t.TempDir(), "target.json"),
				Action: planActionCreate, DesiredSHA256: "desired",
			}},
		},
		transaction.Manifest{},
		t.TempDir(),
		nil,
	)
	assertErrorCode(t, err, apperror.RecoveryUnsupported)
}

func createProfileTargetForRecovery(t *testing.T, ctx context.Context, configDir, profileID, targetID, path, content string) {
	t.Helper()
	if _, err := newSwitchingTestEnvironment(t, configDir).targets.Create(ctx, profiletarget.CreateProfileTargetRequest{
		ProfileID: profileID, ProviderID: "provider-a", TargetID: targetID,
		Path: path, Format: "text", Strategy: "replace-file", ValueJSON: contentValueJSON(t, content),
	}); err != nil {
		t.Fatalf("create profile target %s: %v", targetID, err)
	}
}

func openWritableAppTestStore(t *testing.T, ctx context.Context, databasePath string) *store.Store {
	t.Helper()
	db, err := store.Open(ctx, databasePath, false)
	if err != nil {
		t.Fatalf("open writable store: %v", err)
	}
	return db
}

func mustOperation(t *testing.T, ctx context.Context, db *store.Store, id string) store.Operation {
	t.Helper()
	operation, err := db.GetOperation(ctx, id)
	if err != nil {
		t.Fatalf("read operation %s: %v", id, err)
	}
	return operation
}

func singleOperationIDByTypeStatus(t *testing.T, databasePath, operationType, status string) string {
	t.Helper()
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	rows, err := db.Query("SELECT id FROM operations WHERE operation_type = ? AND status = ? ORDER BY created_at_unix_ms ASC, id ASC", operationType, status)
	if err != nil {
		t.Fatalf("query operations: %v", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 {
		t.Fatalf("expected one %s %s operation, got %d: %v", operationType, status, len(ids), ids)
	}
	return ids[0]
}

func countOperationsByType(t *testing.T, databasePath, operationType string) int {
	t.Helper()
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow("SELECT COUNT(1) FROM operations WHERE operation_type = ?", operationType).Scan(&count); err != nil {
		t.Fatalf("count operations: %v", err)
	}
	return count
}
