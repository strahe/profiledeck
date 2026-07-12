package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/strahe/profiledeck/internal/store"
	"github.com/strahe/profiledeck/internal/targetfs"
)

func TestApplyRollbackRestoresUpdatedTargetAndPreviousActiveState(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	initResult, err := Init(ctx, InitRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	createGenericProviderAndProfile(t, ctx, configDir, true)

	targetPath := filepath.Join(t.TempDir(), "settings.txt")
	if _, err := CreateProfile(ctx, CreateProfileRequest{ConfigDir: configDir, ID: "profile-b", Name: "Profile B"}); err != nil {
		t.Fatalf("expected second profile create to succeed, got %v", err)
	}
	if _, err := CreateProfileTarget(ctx, CreateProfileTargetRequest{
		ConfigDir:  configDir,
		ProfileID:  "profile-a",
		ProviderID: "provider-a",
		TargetID:   "target-a",
		Path:       targetPath,
		Format:     "text",
		Strategy:   "replace-file",
		ValueJSON:  contentValueJSON(t, "profile-a\n"),
	}); err != nil {
		t.Fatalf("expected profile-a target create to succeed, got %v", err)
	}
	if _, err := CreateProfileTarget(ctx, CreateProfileTargetRequest{
		ConfigDir:  configDir,
		ProfileID:  "profile-b",
		ProviderID: "provider-a",
		TargetID:   "target-a",
		Path:       targetPath,
		Format:     "text",
		Strategy:   "replace-file",
		ValueJSON:  contentValueJSON(t, "profile-b\n"),
	}); err != nil {
		t.Fatalf("expected profile-b target create to succeed, got %v", err)
	}

	firstSwitch, err := ApplySwitch(ctx, ApplySwitchRequest{
		ConfigDir:  configDir,
		ProviderID: "provider-a",
		ProfileID:  "profile-a",
		Confirm:    true,
	})
	if err != nil {
		t.Fatalf("expected first switch to succeed, got %v", err)
	}
	secondSwitch, err := ApplySwitch(ctx, ApplySwitchRequest{
		ConfigDir:  configDir,
		ProviderID: "provider-a",
		ProfileID:  "profile-b",
		Confirm:    true,
	})
	if err != nil {
		t.Fatalf("expected second switch to succeed, got %v", err)
	}
	assertFileContent(t, targetPath, "profile-b\n")

	result, err := ApplyRollback(ctx, ApplyRollbackRequest{
		ConfigDir: configDir,
		BackupID:  secondSwitch.OperationID,
		Confirm:   true,
	})
	if err != nil {
		t.Fatalf("expected rollback to succeed, got %v", err)
	}
	if result.Status != store.OperationStatusApplied || result.SourceOperationID != secondSwitch.OperationID || result.Counts.Restore != 1 {
		t.Fatalf("unexpected rollback result: %#v", result)
	}
	assertFileContent(t, targetPath, "profile-a\n")

	db := openAppTestStore(t, ctx, initResult.DatabasePath)
	defer db.Close()
	activeState, err := db.GetActiveState(ctx, store.ActiveStateScopeProvider, "provider-a")
	if err != nil {
		t.Fatalf("expected active state read to succeed, got %v", err)
	}
	if activeState.ProfileID != "profile-a" || activeState.OperationID != firstSwitch.OperationID {
		t.Fatalf("expected rollback to restore previous active state, got %#v", activeState)
	}
	operation, err := db.GetOperation(ctx, result.OperationID)
	if err != nil {
		t.Fatalf("expected rollback operation read to succeed, got %v", err)
	}
	if operation.OperationType != store.OperationTypeRollback || operation.Status != store.OperationStatusApplied || strings.Contains(operation.MetadataJSON, "profile-a\n") {
		t.Fatalf("unexpected rollback operation: %#v", operation)
	}
	if _, err := os.Stat(result.BackupPath); err != nil {
		t.Fatalf("expected rollback current-state backup to exist, got %v", err)
	}
}

func TestApplyRollbackRemovesCreatedTargetAndClearsActiveState(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	initResult, err := Init(ctx, InitRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	createGenericProviderAndProfile(t, ctx, configDir, true)

	targetPath := filepath.Join(t.TempDir(), "created.txt")
	if _, err := CreateProfileTarget(ctx, CreateProfileTargetRequest{
		ConfigDir:  configDir,
		ProfileID:  "profile-a",
		ProviderID: "provider-a",
		TargetID:   "target-a",
		Path:       targetPath,
		Format:     "text",
		Strategy:   "replace-file",
		ValueJSON:  contentValueJSON(t, "created\n"),
	}); err != nil {
		t.Fatalf("expected target create to succeed, got %v", err)
	}
	switchResult, err := ApplySwitch(ctx, ApplySwitchRequest{
		ConfigDir:  configDir,
		ProviderID: "provider-a",
		ProfileID:  "profile-a",
		Confirm:    true,
	})
	if err != nil {
		t.Fatalf("expected switch to succeed, got %v", err)
	}
	assertFileContent(t, targetPath, "created\n")

	result, err := ApplyRollback(ctx, ApplyRollbackRequest{
		ConfigDir: configDir,
		BackupID:  switchResult.OperationID,
		Confirm:   true,
	})
	if err != nil {
		t.Fatalf("expected rollback to succeed, got %v", err)
	}
	if result.Counts.Remove != 1 {
		t.Fatalf("expected one remove, got %#v", result.Counts)
	}
	if _, err := os.Stat(targetPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected created target to be removed, got %v", err)
	}

	db := openAppTestStore(t, ctx, initResult.DatabasePath)
	defer db.Close()
	if _, err := db.GetActiveState(ctx, store.ActiveStateScopeProvider, "provider-a"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected rollback to clear active state, got %v", err)
	}
}

func TestApplyRollbackRejectsChangedCurrentTarget(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	initResult, err := Init(ctx, InitRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	createGenericProviderAndProfile(t, ctx, configDir, true)

	targetPath := filepath.Join(t.TempDir(), "created.txt")
	if _, err := CreateProfileTarget(ctx, CreateProfileTargetRequest{
		ConfigDir:  configDir,
		ProfileID:  "profile-a",
		ProviderID: "provider-a",
		TargetID:   "target-a",
		Path:       targetPath,
		Format:     "text",
		Strategy:   "replace-file",
		ValueJSON:  contentValueJSON(t, "managed\n"),
	}); err != nil {
		t.Fatalf("expected target create to succeed, got %v", err)
	}
	switchResult, err := ApplySwitch(ctx, ApplySwitchRequest{
		ConfigDir:  configDir,
		ProviderID: "provider-a",
		ProfileID:  "profile-a",
		Confirm:    true,
	})
	if err != nil {
		t.Fatalf("expected switch to succeed, got %v", err)
	}
	if err := os.WriteFile(targetPath, []byte("user-modified\n"), 0o600); err != nil {
		t.Fatalf("expected user modification to succeed, got %v", err)
	}

	_, err = ApplyRollback(ctx, ApplyRollbackRequest{
		ConfigDir: configDir,
		BackupID:  switchResult.OperationID,
		Confirm:   true,
	})
	assertAppErrorCode(t, err, ErrorTargetChanged)
	assertFileContent(t, targetPath, "user-modified\n")
	if failed := countOperationsByStatus(t, initResult.DatabasePath, store.OperationStatusFailed); failed != 1 {
		t.Fatalf("expected one failed rollback operation, got %d", failed)
	}
	if profileID := singleFailedRollbackProfileID(t, initResult.DatabasePath); profileID != "profile-a" {
		t.Fatalf("expected failed rollback to remain bound to profile-a, got %q", profileID)
	}
}

func TestApplyRollbackTreatsAlreadyRestoredUpdateAsNoop(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	initResult, err := Init(ctx, InitRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	createGenericProviderAndProfile(t, ctx, configDir, true)

	targetPath := filepath.Join(t.TempDir(), "settings.txt")
	if err := os.WriteFile(targetPath, []byte("before\n"), 0o600); err != nil {
		t.Fatalf("expected target setup to succeed, got %v", err)
	}
	if _, err := CreateProfileTarget(ctx, CreateProfileTargetRequest{
		ConfigDir:  configDir,
		ProfileID:  "profile-a",
		ProviderID: "provider-a",
		TargetID:   "target-a",
		Path:       targetPath,
		Format:     "text",
		Strategy:   "replace-file",
		ValueJSON:  contentValueJSON(t, "managed\n"),
	}); err != nil {
		t.Fatalf("expected target create to succeed, got %v", err)
	}
	switchResult, err := ApplySwitch(ctx, ApplySwitchRequest{
		ConfigDir:  configDir,
		ProviderID: "provider-a",
		ProfileID:  "profile-a",
		Confirm:    true,
	})
	if err != nil {
		t.Fatalf("expected switch to succeed, got %v", err)
	}
	assertFileContent(t, targetPath, "managed\n")

	if err := os.WriteFile(targetPath, []byte("before\n"), 0o600); err != nil {
		t.Fatalf("expected partial rollback setup to succeed, got %v", err)
	}
	result, err := ApplyRollback(ctx, ApplyRollbackRequest{
		ConfigDir: configDir,
		BackupID:  switchResult.OperationID,
		Confirm:   true,
	})
	if err != nil {
		t.Fatalf("expected retry rollback to succeed, got %v", err)
	}
	if result.Counts.Restore != 0 || result.Counts.Noop != 1 {
		t.Fatalf("expected already-restored update to be noop, got %#v", result.Counts)
	}
	assertFileContent(t, targetPath, "before\n")

	db := openAppTestStore(t, ctx, initResult.DatabasePath)
	defer db.Close()
	if _, err := db.GetActiveState(ctx, store.ActiveStateScopeProvider, "provider-a"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected retry rollback to clear active state, got %v", err)
	}
}

func TestApplyRollbackLockFailureKeepsSourceProfileBinding(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	initResult, err := Init(ctx, InitRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	createGenericProviderAndProfile(t, ctx, configDir, true)

	targetPath := filepath.Join(t.TempDir(), "created.txt")
	if _, err := CreateProfileTarget(ctx, CreateProfileTargetRequest{
		ConfigDir:  configDir,
		ProfileID:  "profile-a",
		ProviderID: "provider-a",
		TargetID:   "target-a",
		Path:       targetPath,
		Format:     "text",
		Strategy:   "replace-file",
		ValueJSON:  contentValueJSON(t, "managed\n"),
	}); err != nil {
		t.Fatalf("expected target create to succeed, got %v", err)
	}
	switchResult, err := ApplySwitch(ctx, ApplySwitchRequest{
		ConfigDir:  configDir,
		ProviderID: "provider-a",
		ProfileID:  "profile-a",
		Confirm:    true,
	})
	if err != nil {
		t.Fatalf("expected switch to succeed, got %v", err)
	}

	_, paths, err := resolveRuntime(configDir)
	if err != nil {
		t.Fatalf("expected runtime resolve to succeed, got %v", err)
	}
	lock, err := targetfs.AcquireLock(paths.Lock, "test-lock-holder")
	if err != nil {
		t.Fatalf("expected lock setup to succeed, got %v", err)
	}
	defer lock.Release()

	_, err = ApplyRollback(ctx, ApplyRollbackRequest{
		ConfigDir: configDir,
		BackupID:  switchResult.OperationID,
		Confirm:   true,
	})
	assertAppErrorCode(t, err, ErrorLockAcquireFailed)
	if profileID := singleFailedRollbackProfileID(t, initResult.DatabasePath); profileID != "profile-a" {
		t.Fatalf("expected failed rollback to remain bound to profile-a, got %q", profileID)
	}
}

func TestBackupAuditShowsOldMetadataButRollbackRejectsIt(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	initResult, err := Init(ctx, InitRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	createGenericProviderAndProfile(t, ctx, configDir, true)

	targetPath := filepath.Join(t.TempDir(), "settings.txt")
	if _, err := CreateProfileTarget(ctx, CreateProfileTargetRequest{
		ConfigDir:  configDir,
		ProfileID:  "profile-a",
		ProviderID: "provider-a",
		TargetID:   "target-a",
		Path:       targetPath,
		Format:     "text",
		Strategy:   "replace-file",
		ValueJSON:  contentValueJSON(t, "managed\n"),
	}); err != nil {
		t.Fatalf("expected target create to succeed, got %v", err)
	}
	switchResult, err := ApplySwitch(ctx, ApplySwitchRequest{
		ConfigDir:  configDir,
		ProviderID: "provider-a",
		ProfileID:  "profile-a",
		Confirm:    true,
	})
	if err != nil {
		t.Fatalf("expected switch to succeed, got %v", err)
	}

	db := openWritableAppTestStore(t, ctx, initResult.DatabasePath)
	var metadata switchOperationMetadata
	if err := json.Unmarshal([]byte(mustOperation(t, ctx, db, switchResult.OperationID).MetadataJSON), &metadata); err != nil {
		t.Fatalf("expected metadata decode to succeed, got %v", err)
	}
	metadata.PreviousActive = nil
	raw, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("expected metadata encode to succeed, got %v", err)
	}
	if err := db.UpdateOperationMetadata(ctx, switchResult.OperationID, string(raw)); err != nil {
		t.Fatalf("expected metadata downgrade to succeed, got %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("expected metadata store close, got %v", err)
	}

	detail, err := ShowBackup(ctx, ShowBackupRequest{ConfigDir: configDir, BackupID: switchResult.OperationID})
	if err != nil {
		t.Fatalf("expected backup show to succeed, got %v", err)
	}
	if !detail.Valid || detail.RollbackSupported || detail.UnsupportedReason == "" {
		t.Fatalf("expected old backup to be visible but unsupported, got %#v", detail)
	}

	_, err = ApplyRollback(ctx, ApplyRollbackRequest{
		ConfigDir: configDir,
		BackupID:  switchResult.OperationID,
		Confirm:   true,
	})
	assertAppErrorCode(t, err, ErrorRollbackUnsupported)
}

func TestShowBackupMissingIDReturnsNotFound(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	_, err := ShowBackup(ctx, ShowBackupRequest{ConfigDir: configDir, BackupID: "missing-backup"})
	assertAppErrorCode(t, err, ErrorBackupNotFound)
}

func TestRollbackTargetsRejectDuplicateLocators(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.json")
	_, err := rollbackTargetsFromMetadata(switchOperationMetadata{
		ProviderID: "generic",
		Targets: []switchOperationTargetMetadata{
			{TargetID: "first", BackendID: targetBackendFile, Path: path, Action: planActionNoop, DesiredSHA256: sha256HexString("first")},
			{TargetID: "second", BackendID: targetBackendFile, Path: path, Action: planActionNoop, DesiredSHA256: sha256HexString("second")},
		},
	}, switchBackupManifest{}, t.TempDir())
	assertAppErrorCode(t, err, ErrorBackupInvalid)
	if err == nil || strings.Contains(err.Error(), sha256HexString(targetBackendFile+"\x00"+path)) {
		t.Fatalf("expected duplicate locator error without locator fingerprint, got %v", err)
	}
}

func TestRollbackTargetsRejectDuplicateTargetIDs(t *testing.T) {
	_, err := rollbackTargetsFromMetadata(switchOperationMetadata{
		ProviderID: "generic",
		Targets: []switchOperationTargetMetadata{
			{TargetID: "same", BackendID: targetBackendFile, Path: filepath.Join(t.TempDir(), "first.json"), Action: planActionNoop, DesiredSHA256: sha256HexString("first")},
			{TargetID: "same", BackendID: targetBackendFile, Path: filepath.Join(t.TempDir(), "second.json"), Action: planActionNoop, DesiredSHA256: sha256HexString("second")},
		},
	}, switchBackupManifest{}, t.TempDir())
	assertAppErrorCode(t, err, ErrorBackupInvalid)
}

func TestApplyRollbackRejectsChangedActiveState(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	createGenericProviderAndProfile(t, ctx, configDir, true)

	targetPath := filepath.Join(t.TempDir(), "settings.txt")
	if _, err := CreateProfile(ctx, CreateProfileRequest{ConfigDir: configDir, ID: "profile-b", Name: "Profile B"}); err != nil {
		t.Fatalf("expected second profile create to succeed, got %v", err)
	}
	for _, profile := range []struct {
		id      string
		content string
	}{
		{id: "profile-a", content: "a\n"},
		{id: "profile-b", content: "b\n"},
	} {
		if _, err := CreateProfileTarget(ctx, CreateProfileTargetRequest{
			ConfigDir:  configDir,
			ProfileID:  profile.id,
			ProviderID: "provider-a",
			TargetID:   "target-a",
			Path:       targetPath,
			Format:     "text",
			Strategy:   "replace-file",
			ValueJSON:  contentValueJSON(t, profile.content),
		}); err != nil {
			t.Fatalf("expected target create to succeed, got %v", err)
		}
	}
	firstSwitch, err := ApplySwitch(ctx, ApplySwitchRequest{ConfigDir: configDir, ProviderID: "provider-a", ProfileID: "profile-a", Confirm: true})
	if err != nil {
		t.Fatalf("expected first switch to succeed, got %v", err)
	}
	if _, err := ApplySwitch(ctx, ApplySwitchRequest{ConfigDir: configDir, ProviderID: "provider-a", ProfileID: "profile-b", Confirm: true}); err != nil {
		t.Fatalf("expected second switch to succeed, got %v", err)
	}

	_, err = ApplyRollback(ctx, ApplyRollbackRequest{
		ConfigDir: configDir,
		BackupID:  firstSwitch.OperationID,
		Confirm:   true,
	})
	assertAppErrorCode(t, err, ErrorTargetChanged)
	assertFileContent(t, targetPath, "b\n")
}

func openWritableAppTestStore(t *testing.T, ctx context.Context, databasePath string) *store.Store {
	t.Helper()

	db, err := store.Open(ctx, databasePath, false)
	if err != nil {
		t.Fatalf("expected writable store open to succeed, got %v", err)
	}
	return db
}

func mustOperation(t *testing.T, ctx context.Context, db *store.Store, id string) store.Operation {
	t.Helper()

	operation, err := db.GetOperation(ctx, id)
	if err != nil {
		t.Fatalf("expected operation read to succeed, got %v", err)
	}
	return operation
}

func singleFailedRollbackProfileID(t *testing.T, databasePath string) string {
	t.Helper()

	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("expected sqlite open to succeed, got %v", err)
	}
	defer db.Close()

	rows, err := db.Query(`
		SELECT profile_id
		FROM operations
		WHERE operation_type = ? AND status = ?
	`, store.OperationTypeRollback, store.OperationStatusFailed)
	if err != nil {
		t.Fatalf("expected failed rollback query to succeed, got %v", err)
	}
	defer rows.Close()

	profileIDs := []string{}
	for rows.Next() {
		var profileID string
		if err := rows.Scan(&profileID); err != nil {
			t.Fatalf("expected failed rollback scan to succeed, got %v", err)
		}
		profileIDs = append(profileIDs, profileID)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("expected failed rollback rows to succeed, got %v", err)
	}
	if len(profileIDs) != 1 {
		t.Fatalf("expected one failed rollback, got %d", len(profileIDs))
	}
	return profileIDs[0]
}
