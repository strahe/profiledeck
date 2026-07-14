package switching

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/profile"
	"github.com/strahe/profiledeck/internal/profiletarget"
	"github.com/strahe/profiledeck/internal/store"
	"github.com/strahe/profiledeck/internal/targetfs"
)

func TestRecoverFailedSwitchRemovesPartiallyCreatedTargets(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	initResult, err := initSwitchingTestRuntime(ctx, configDir)
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	createGenericProviderAndProfile(t, ctx, configDir, true)

	dir := t.TempDir()
	firstPath := filepath.Join(dir, "target-a.txt")
	secondPath := filepath.Join(dir, "missing", "target-b.txt")
	createProfileTargetForRecovery(t, ctx, configDir, "profile-a", "target-a", firstPath, "first\n")
	createProfileTargetForRecovery(t, ctx, configDir, "profile-a", "target-b", secondPath, "second\n")

	_, err = newSwitchingTestEnvironment(t, configDir).service.Apply(ctx, ApplySwitchRequest{
		ProviderID: "provider-a",
		ProfileID:  "profile-a",
		Confirm:    true,
	})
	assertErrorCode(t, err, apperror.TargetWriteFailed)
	assertFileContent(t, firstPath, "first\n")
	if _, err := os.Stat(secondPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected second target to remain absent, got %v", err)
	}

	failedSwitchID := singleOperationIDByTypeStatus(t, initResult.DatabasePath, store.OperationTypeSwitch, store.OperationStatusFailed)
	result, err := newSwitchingTestEnvironment(t, configDir).service.RecoverFailedSwitch(ctx, RecoverFailedSwitchParams{
		OperationID: failedSwitchID,
		Confirm:     true,
	})
	if err != nil {
		t.Fatalf("expected recovery to succeed, got %v", err)
	}
	if result.OperationType != store.OperationTypeRollback || result.RollbackKind != rollbackKindFailedSwitchRecovery || result.SourceOperationID != failedSwitchID {
		t.Fatalf("unexpected recovery result: %#v", result)
	}
	if result.Counts.Remove != 1 || result.Counts.Noop != 1 {
		t.Fatalf("expected one remove and one noop, got %#v", result.Counts)
	}
	for _, path := range []string{firstPath, secondPath} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected recovered target %s to be absent, got %v", path, err)
		}
	}

	db := openAppTestStore(t, ctx, initResult.DatabasePath)
	defer db.Close()
	source := mustOperation(t, ctx, db, failedSwitchID)
	if source.Status != store.OperationStatusFailed {
		t.Fatalf("expected source switch to remain failed, got %#v", source)
	}
	recovery := mustOperation(t, ctx, db, result.OperationID)
	if recovery.OperationType != store.OperationTypeRollback || recovery.Status != store.OperationStatusApplied || !strings.Contains(recovery.MetadataJSON, rollbackKindFailedSwitchRecovery) {
		t.Fatalf("unexpected recovery operation: %#v", recovery)
	}
	if _, err := db.GetActiveState(ctx, store.ActiveStateScopeProvider, "provider-a"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected recovery to leave active state absent, got %v", err)
	}
}

func TestRecoverFailedSwitchRestoresUpdatedTargetAndPreviousActiveState(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	initResult, err := initSwitchingTestRuntime(ctx, configDir)
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	createGenericProviderAndProfile(t, ctx, configDir, true)
	if _, err := newSwitchingTestEnvironment(t, configDir).profiles.Create(ctx, profile.CreateRequest{ID: "profile-b", Name: "Profile B"}); err != nil {
		t.Fatalf("expected second profile create to succeed, got %v", err)
	}

	dir := t.TempDir()
	targetPath := filepath.Join(dir, "target-a.txt")
	missingPath := filepath.Join(dir, "missing", "target-b.txt")
	createProfileTargetForRecovery(t, ctx, configDir, "profile-a", "target-a", targetPath, "profile-a\n")
	createProfileTargetForRecovery(t, ctx, configDir, "profile-b", "target-a", targetPath, "profile-b\n")
	createProfileTargetForRecovery(t, ctx, configDir, "profile-b", "target-b", missingPath, "second\n")

	firstSwitch, err := newSwitchingTestEnvironment(t, configDir).service.Apply(ctx, ApplySwitchRequest{
		ProviderID: "provider-a",
		ProfileID:  "profile-a",
		Confirm:    true,
	})
	if err != nil {
		t.Fatalf("expected first switch to succeed, got %v", err)
	}
	_, err = newSwitchingTestEnvironment(t, configDir).service.Apply(ctx, ApplySwitchRequest{
		ProviderID: "provider-a",
		ProfileID:  "profile-b",
		Confirm:    true,
	})
	assertErrorCode(t, err, apperror.TargetWriteFailed)
	assertFileContent(t, targetPath, "profile-b\n")

	failedSwitchID := singleOperationIDByTypeStatus(t, initResult.DatabasePath, store.OperationTypeSwitch, store.OperationStatusFailed)
	result, err := newSwitchingTestEnvironment(t, configDir).service.RecoverFailedSwitch(ctx, RecoverFailedSwitchParams{
		OperationID: failedSwitchID,
		Confirm:     true,
	})
	if err != nil {
		t.Fatalf("expected recovery to succeed, got %v", err)
	}
	if result.Counts.Restore != 1 || result.Counts.Noop != 1 {
		t.Fatalf("expected one restore and one noop, got %#v", result.Counts)
	}
	assertFileContent(t, targetPath, "profile-a\n")

	db := openAppTestStore(t, ctx, initResult.DatabasePath)
	defer db.Close()
	activeState, err := db.GetActiveState(ctx, store.ActiveStateScopeProvider, "provider-a")
	if err != nil {
		t.Fatalf("expected active state read to succeed, got %v", err)
	}
	if activeState.ProfileID != "profile-a" || activeState.OperationID != firstSwitch.OperationID {
		t.Fatalf("expected recovery to restore previous active state, got %#v", activeState)
	}
}

func TestRecoverFailedSwitchRejectsUnrecoverableCheckpoint(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	initResult, err := initSwitchingTestRuntime(ctx, configDir)
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	db := openWritableAppTestStore(t, ctx, initResult.DatabasePath)
	defer db.Close()
	if _, err := db.CreatePendingSwitchOperation(ctx, store.CreateSwitchOperationParams{
		ID:           "switch-before-backup",
		ProfileID:    "profile-a",
		MetadataJSON: `{"checkpoint":"planned","provider_id":"provider-a","profile_id":"profile-a"}`,
	}); err != nil {
		t.Fatalf("expected switch operation setup to succeed, got %v", err)
	}
	metadata := `{"checkpoint":"planned","provider_id":"provider-a","profile_id":"profile-a"}`
	if err := db.MarkOperationFailed(ctx, store.MarkOperationFailedParams{
		ID:           "switch-before-backup",
		ErrorCode:    string(apperror.TargetWriteFailed),
		ErrorMessage: "write failed",
		MetadataJSON: &metadata,
	}); err != nil {
		t.Fatalf("expected failed operation setup to succeed, got %v", err)
	}

	_, err = newSwitchingTestEnvironment(t, configDir).service.RecoverFailedSwitch(ctx, RecoverFailedSwitchParams{
		OperationID: "switch-before-backup",
		Confirm:     true,
	})
	assertErrorCode(t, err, apperror.RecoveryUnsupported)
	if rollbackCount := countOperationsByType(t, initResult.DatabasePath, store.OperationTypeRollback); rollbackCount != 0 {
		t.Fatalf("expected no recovery operation for unsupported source, got %d", rollbackCount)
	}
}

func TestRecoverFailedSwitchRejectsAppliedSwitchBackupID(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := initSwitchingTestRuntime(ctx, configDir); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	createGenericProviderAndProfile(t, ctx, configDir, true)

	targetPath := filepath.Join(t.TempDir(), "target.txt")
	createProfileTargetForRecovery(t, ctx, configDir, "profile-a", "target-a", targetPath, "managed\n")
	switchResult, err := newSwitchingTestEnvironment(t, configDir).service.Apply(ctx, ApplySwitchRequest{
		ProviderID: "provider-a",
		ProfileID:  "profile-a",
		Confirm:    true,
	})
	if err != nil {
		t.Fatalf("expected switch to succeed, got %v", err)
	}

	_, err = newSwitchingTestEnvironment(t, configDir).service.RecoverFailedSwitch(ctx, RecoverFailedSwitchParams{
		OperationID: switchResult.OperationID,
		Confirm:     true,
	})
	assertErrorCode(t, err, apperror.RecoveryUnsupported)
}

func TestRecoverFailedSwitchRejectsChangedActiveState(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	initResult, err := initSwitchingTestRuntime(ctx, configDir)
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	createGenericProviderAndProfile(t, ctx, configDir, true)

	dir := t.TempDir()
	firstPath := filepath.Join(dir, "target-a.txt")
	secondPath := filepath.Join(dir, "missing", "target-b.txt")
	createProfileTargetForRecovery(t, ctx, configDir, "profile-a", "target-a", firstPath, "first\n")
	createProfileTargetForRecovery(t, ctx, configDir, "profile-a", "target-b", secondPath, "second\n")
	_, err = newSwitchingTestEnvironment(t, configDir).service.Apply(ctx, ApplySwitchRequest{ProviderID: "provider-a", ProfileID: "profile-a", Confirm: true})
	assertErrorCode(t, err, apperror.TargetWriteFailed)
	failedSwitchID := singleOperationIDByTypeStatus(t, initResult.DatabasePath, store.OperationTypeSwitch, store.OperationStatusFailed)

	db := openWritableAppTestStore(t, ctx, initResult.DatabasePath)
	if _, err := db.CreatePendingSwitchOperation(ctx, store.CreateSwitchOperationParams{
		ID:           "switch-active-mismatch",
		ProfileID:    "profile-a",
		MetadataJSON: `{"checkpoint":"planned"}`,
	}); err != nil {
		t.Fatalf("expected active mismatch operation setup to succeed, got %v", err)
	}
	if err := db.CompleteSwitchOperation(ctx, store.CompleteSwitchOperationParams{
		ID:           "switch-active-mismatch",
		ProfileID:    "profile-a",
		ProviderID:   "provider-a",
		MetadataJSON: `{"checkpoint":"applied"}`,
	}); err != nil {
		t.Fatalf("expected active mismatch setup to succeed, got %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("expected writable store close to succeed, got %v", err)
	}

	_, err = newSwitchingTestEnvironment(t, configDir).service.RecoverFailedSwitch(ctx, RecoverFailedSwitchParams{
		OperationID: failedSwitchID,
		Confirm:     true,
	})
	assertErrorCode(t, err, apperror.TargetChanged)
	assertFileContent(t, firstPath, "first\n")
	if rollbackCount := countOperationsByType(t, initResult.DatabasePath, store.OperationTypeRollback); rollbackCount != 0 {
		t.Fatalf("expected no recovery operation for active-state mismatch, got %d", rollbackCount)
	}
}

func TestRecoverFailedSwitchRejectsUnknownTargetStateBeforeMutation(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	initResult, err := initSwitchingTestRuntime(ctx, configDir)
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	createGenericProviderAndProfile(t, ctx, configDir, true)

	dir := t.TempDir()
	firstPath := filepath.Join(dir, "target-a.txt")
	secondPath := filepath.Join(dir, "missing", "target-b.txt")
	createProfileTargetForRecovery(t, ctx, configDir, "profile-a", "target-a", firstPath, "first\n")
	createProfileTargetForRecovery(t, ctx, configDir, "profile-a", "target-b", secondPath, "second\n")

	_, err = newSwitchingTestEnvironment(t, configDir).service.Apply(ctx, ApplySwitchRequest{ProviderID: "provider-a", ProfileID: "profile-a", Confirm: true})
	assertErrorCode(t, err, apperror.TargetWriteFailed)
	failedSwitchID := singleOperationIDByTypeStatus(t, initResult.DatabasePath, store.OperationTypeSwitch, store.OperationStatusFailed)
	if err := os.WriteFile(firstPath, []byte("manual\n"), 0o600); err != nil {
		t.Fatalf("expected manual change to succeed, got %v", err)
	}

	_, err = newSwitchingTestEnvironment(t, configDir).service.RecoverFailedSwitch(ctx, RecoverFailedSwitchParams{
		OperationID: failedSwitchID,
		Confirm:     true,
	})
	assertErrorCode(t, err, apperror.TargetChanged)
	assertFileContent(t, firstPath, "manual\n")
	if rollbackCount := countOperationsByType(t, initResult.DatabasePath, store.OperationTypeRollback); rollbackCount != 0 {
		t.Fatalf("expected no recovery operation before target known-state validation, got %d", rollbackCount)
	}
}

func TestRecoverFailedSwitchLockFailureRecordsFailedRecoveryOperation(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	initResult, err := initSwitchingTestRuntime(ctx, configDir)
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	createGenericProviderAndProfile(t, ctx, configDir, true)

	dir := t.TempDir()
	firstPath := filepath.Join(dir, "target-a.txt")
	secondPath := filepath.Join(dir, "missing", "target-b.txt")
	createProfileTargetForRecovery(t, ctx, configDir, "profile-a", "target-a", firstPath, "first\n")
	createProfileTargetForRecovery(t, ctx, configDir, "profile-a", "target-b", secondPath, "second\n")
	_, err = newSwitchingTestEnvironment(t, configDir).service.Apply(ctx, ApplySwitchRequest{ProviderID: "provider-a", ProfileID: "profile-a", Confirm: true})
	assertErrorCode(t, err, apperror.TargetWriteFailed)
	failedSwitchID := singleOperationIDByTypeStatus(t, initResult.DatabasePath, store.OperationTypeSwitch, store.OperationStatusFailed)

	_, paths, err := resolveTestRuntime(configDir)
	if err != nil {
		t.Fatalf("expected runtime resolve to succeed, got %v", err)
	}
	lock, err := targetfs.AcquireLock(paths.Lock, "external-lock")
	if err != nil {
		t.Fatalf("expected lock setup to succeed, got %v", err)
	}
	defer lock.Release()

	_, err = newSwitchingTestEnvironment(t, configDir).service.RecoverFailedSwitch(ctx, RecoverFailedSwitchParams{
		OperationID: failedSwitchID,
		Confirm:     true,
	})
	assertErrorCode(t, err, apperror.LockAcquireFailed)

	recoveryID := singleOperationIDByTypeStatus(t, initResult.DatabasePath, store.OperationTypeRollback, store.OperationStatusFailed)
	db := openAppTestStore(t, ctx, initResult.DatabasePath)
	defer db.Close()
	recovery := mustOperation(t, ctx, db, recoveryID)
	if !strings.Contains(recovery.MetadataJSON, rollbackKindFailedSwitchRecovery) {
		t.Fatalf("expected failed recovery metadata to include recovery kind, got %s", recovery.MetadataJSON)
	}
	source := mustOperation(t, ctx, db, failedSwitchID)
	if source.Status != store.OperationStatusFailed {
		t.Fatalf("expected source failed switch to remain failed, got %#v", source)
	}
}

func createProfileTargetForRecovery(t *testing.T, ctx context.Context, configDir, profileID, targetID, path, content string) {
	t.Helper()

	if _, err := newSwitchingTestEnvironment(t, configDir).targets.Create(ctx, profiletarget.CreateProfileTargetRequest{
		ProfileID:  profileID,
		ProviderID: "provider-a",
		TargetID:   targetID,
		Path:       path,
		Format:     "text",
		Strategy:   "replace-file",
		ValueJSON:  contentValueJSON(t, content),
	}); err != nil {
		t.Fatalf("expected profile target %s setup to succeed, got %v", targetID, err)
	}
}

func singleOperationIDByTypeStatus(t *testing.T, databasePath, operationType, status string) string {
	t.Helper()

	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("expected sqlite open to succeed, got %v", err)
	}
	defer db.Close()

	rows, err := db.Query(`
		SELECT id
		FROM operations
		WHERE operation_type = ? AND status = ?
		ORDER BY created_at_unix_ms ASC, id ASC
	`, operationType, status)
	if err != nil {
		t.Fatalf("expected operation query to succeed, got %v", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("expected operation id scan to succeed, got %v", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("expected operation rows to succeed, got %v", err)
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
		t.Fatalf("expected sqlite open to succeed, got %v", err)
	}
	defer db.Close()

	var count int
	if err := db.QueryRow(`
		SELECT COUNT(1)
		FROM operations
		WHERE operation_type = ?
	`, operationType).Scan(&count); err != nil {
		t.Fatalf("expected operation count query to succeed, got %v", err)
	}
	return count
}
