package switching

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/profile"
	"github.com/strahe/profiledeck/internal/profiletarget"
	"github.com/strahe/profiledeck/internal/store"
	switchplan "github.com/strahe/profiledeck/internal/switching/plan"
	switchtarget "github.com/strahe/profiledeck/internal/switching/target"
	"github.com/strahe/profiledeck/internal/switching/transaction"
	"github.com/strahe/profiledeck/internal/targetfs"
)

type recoveryIdentityTestSpec struct {
	id      string
	backend string
	locator string
}

func (spec recoveryIdentityTestSpec) BackendID() string { return spec.backend }
func (spec recoveryIdentityTestSpec) TargetID() string  { return spec.id }
func (spec recoveryIdentityTestSpec) SafeLabel() string { return "recovery target" }
func (spec recoveryIdentityTestSpec) LocatorFingerprint() string {
	return switchtarget.SHA256String(spec.locator)
}
func (spec recoveryIdentityTestSpec) Sensitive() bool         { return true }
func (spec recoveryIdentityTestSpec) RecoveryLocator() string { return spec.locator }
func (spec recoveryIdentityTestSpec) ObjectFingerprint(s switchtarget.Snapshot) string {
	return switchtarget.SHA256String(s.OpaqueState)
}

type recoveryIdentityTestAdapter struct{ switchplan.GenericAdapter }

func (recoveryIdentityTestAdapter) ID() string { return "recovery-test" }

func (recoveryIdentityTestAdapter) ResolveTargetSpec(_, targetID, backendID, path, _ string) (switchtarget.Spec, error) {
	return recoveryIdentityTestSpec{id: targetID, backend: backendID, locator: path}, nil
}

func TestApplyRollbackRestoresUpdatedTargetAndPreviousActiveState(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	initResult, err := initSwitchingTestRuntime(ctx, configDir)
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	createGenericProviderAndProfile(t, ctx, configDir, true)

	targetPath := filepath.Join(t.TempDir(), "settings.txt")
	if _, err := newSwitchingTestEnvironment(t, configDir).profiles.Create(ctx, profile.CreateRequest{ID: "profile-b", Name: "Profile B"}); err != nil {
		t.Fatalf("expected second profile create to succeed, got %v", err)
	}
	if _, err := newSwitchingTestEnvironment(t, configDir).targets.Create(ctx, profiletarget.CreateProfileTargetRequest{
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
	if _, err := newSwitchingTestEnvironment(t, configDir).targets.Create(ctx, profiletarget.CreateProfileTargetRequest{
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

	firstSwitch, err := newSwitchingTestEnvironment(t, configDir).service.Apply(ctx, ApplySwitchRequest{
		ProviderID: "provider-a",
		ProfileID:  "profile-a",
		Confirm:    true,
	})
	if err != nil {
		t.Fatalf("expected first switch to succeed, got %v", err)
	}
	secondSwitch, err := newSwitchingTestEnvironment(t, configDir).service.Apply(ctx, ApplySwitchRequest{
		ProviderID: "provider-a",
		ProfileID:  "profile-b",
		Confirm:    true,
	})
	if err != nil {
		t.Fatalf("expected second switch to succeed, got %v", err)
	}
	assertFileContent(t, targetPath, "profile-b\n")

	result, err := newSwitchingTestEnvironment(t, configDir).service.Rollback(ctx, ApplyRollbackRequest{
		BackupID: secondSwitch.OperationID,
		Confirm:  true,
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
	initResult, err := initSwitchingTestRuntime(ctx, configDir)
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	createGenericProviderAndProfile(t, ctx, configDir, true)

	targetPath := filepath.Join(t.TempDir(), "created.txt")
	if _, err := newSwitchingTestEnvironment(t, configDir).targets.Create(ctx, profiletarget.CreateProfileTargetRequest{
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
	switchResult, err := newSwitchingTestEnvironment(t, configDir).service.Apply(ctx, ApplySwitchRequest{
		ProviderID: "provider-a",
		ProfileID:  "profile-a",
		Confirm:    true,
	})
	if err != nil {
		t.Fatalf("expected switch to succeed, got %v", err)
	}
	assertFileContent(t, targetPath, "created\n")

	result, err := newSwitchingTestEnvironment(t, configDir).service.Rollback(ctx, ApplyRollbackRequest{
		BackupID: switchResult.OperationID,
		Confirm:  true,
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
	initResult, err := initSwitchingTestRuntime(ctx, configDir)
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	createGenericProviderAndProfile(t, ctx, configDir, true)

	targetPath := filepath.Join(t.TempDir(), "created.txt")
	if _, err := newSwitchingTestEnvironment(t, configDir).targets.Create(ctx, profiletarget.CreateProfileTargetRequest{
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
	switchResult, err := newSwitchingTestEnvironment(t, configDir).service.Apply(ctx, ApplySwitchRequest{
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

	_, err = newSwitchingTestEnvironment(t, configDir).service.Rollback(ctx, ApplyRollbackRequest{
		BackupID: switchResult.OperationID,
		Confirm:  true,
	})
	assertErrorCode(t, err, apperror.TargetChanged)
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
	initResult, err := initSwitchingTestRuntime(ctx, configDir)
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	createGenericProviderAndProfile(t, ctx, configDir, true)

	targetPath := filepath.Join(t.TempDir(), "settings.txt")
	if err := os.WriteFile(targetPath, []byte("before\n"), 0o600); err != nil {
		t.Fatalf("expected target setup to succeed, got %v", err)
	}
	if _, err := newSwitchingTestEnvironment(t, configDir).targets.Create(ctx, profiletarget.CreateProfileTargetRequest{
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
	switchResult, err := newSwitchingTestEnvironment(t, configDir).service.Apply(ctx, ApplySwitchRequest{
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
	result, err := newSwitchingTestEnvironment(t, configDir).service.Rollback(ctx, ApplyRollbackRequest{
		BackupID: switchResult.OperationID,
		Confirm:  true,
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
	initResult, err := initSwitchingTestRuntime(ctx, configDir)
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	createGenericProviderAndProfile(t, ctx, configDir, true)

	targetPath := filepath.Join(t.TempDir(), "created.txt")
	if _, err := newSwitchingTestEnvironment(t, configDir).targets.Create(ctx, profiletarget.CreateProfileTargetRequest{
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
	switchResult, err := newSwitchingTestEnvironment(t, configDir).service.Apply(ctx, ApplySwitchRequest{
		ProviderID: "provider-a",
		ProfileID:  "profile-a",
		Confirm:    true,
	})
	if err != nil {
		t.Fatalf("expected switch to succeed, got %v", err)
	}

	_, paths, err := resolveTestRuntime(configDir)
	if err != nil {
		t.Fatalf("expected runtime resolve to succeed, got %v", err)
	}
	lock, err := targetfs.AcquireLock(paths.Lock, "test-lock-holder")
	if err != nil {
		t.Fatalf("expected lock setup to succeed, got %v", err)
	}
	defer lock.Release()

	_, err = newSwitchingTestEnvironment(t, configDir).service.Rollback(ctx, ApplyRollbackRequest{
		BackupID: switchResult.OperationID,
		Confirm:  true,
	})
	assertErrorCode(t, err, apperror.LockAcquireFailed)
	if profileID := singleFailedRollbackProfileID(t, initResult.DatabasePath); profileID != "profile-a" {
		t.Fatalf("expected failed rollback to remain bound to profile-a, got %q", profileID)
	}
}

func TestBackupAuditShowsOldMetadataButRollbackRejectsIt(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	initResult, err := initSwitchingTestRuntime(ctx, configDir)
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	createGenericProviderAndProfile(t, ctx, configDir, true)

	targetPath := filepath.Join(t.TempDir(), "settings.txt")
	if _, err := newSwitchingTestEnvironment(t, configDir).targets.Create(ctx, profiletarget.CreateProfileTargetRequest{
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
	switchResult, err := newSwitchingTestEnvironment(t, configDir).service.Apply(ctx, ApplySwitchRequest{
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

	detail, err := newSwitchingTestEnvironment(t, configDir).service.ShowBackup(ctx, switchResult.OperationID)
	if err != nil {
		t.Fatalf("expected backup show to succeed, got %v", err)
	}
	if !detail.Valid || detail.RollbackSupported || detail.UnsupportedReason == "" {
		t.Fatalf("expected old backup to be visible but unsupported, got %#v", detail)
	}

	_, err = newSwitchingTestEnvironment(t, configDir).service.Rollback(ctx, ApplyRollbackRequest{
		BackupID: switchResult.OperationID,
		Confirm:  true,
	})
	assertErrorCode(t, err, apperror.RollbackUnsupported)
}

func TestShowBackupMissingIDReturnsNotFound(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := initSwitchingTestRuntime(ctx, configDir); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	_, err := newSwitchingTestEnvironment(t, configDir).service.ShowBackup(ctx, "missing-backup")
	assertErrorCode(t, err, apperror.BackupNotFound)
}

func TestRollbackTargetsRejectDuplicateLocators(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.json")
	_, err := rollbackTargetsFromMetadata(switchOperationMetadata{
		ProviderID: "generic",
		Targets: []switchOperationTargetMetadata{
			{TargetID: "first", BackendID: switchtarget.BackendFile, Path: path, Action: planActionNoop, DesiredSHA256: switchtarget.SHA256String("first")},
			{TargetID: "second", BackendID: switchtarget.BackendFile, Path: path, Action: planActionNoop, DesiredSHA256: switchtarget.SHA256String("second")},
		},
	}, transaction.Manifest{}, t.TempDir())
	assertErrorCode(t, err, apperror.BackupInvalid)
	if err == nil || strings.Contains(err.Error(), switchtarget.SHA256String(switchtarget.BackendFile+"\x00"+path)) {
		t.Fatalf("expected duplicate locator error without locator fingerprint, got %v", err)
	}
}

func TestRollbackTargetsRejectDuplicateTargetIDs(t *testing.T) {
	_, err := rollbackTargetsFromMetadata(switchOperationMetadata{
		ProviderID: "generic",
		Targets: []switchOperationTargetMetadata{
			{TargetID: "same", BackendID: switchtarget.BackendFile, Path: filepath.Join(t.TempDir(), "first.json"), Action: planActionNoop, DesiredSHA256: switchtarget.SHA256String("first")},
			{TargetID: "same", BackendID: switchtarget.BackendFile, Path: filepath.Join(t.TempDir(), "second.json"), Action: planActionNoop, DesiredSHA256: switchtarget.SHA256String("second")},
		},
	}, transaction.Manifest{}, t.TempDir())
	assertErrorCode(t, err, apperror.BackupInvalid)
}

func TestRollbackRecoveryIdentityUsesTargetContract(t *testing.T) {
	metadata := switchOperationMetadata{
		ProviderID: "future-provider",
		Targets: []switchOperationTargetMetadata{{
			TargetID: "credential", BackendID: "future-backend", Path: "saved-locator",
			Action: planActionNoop, FileExists: true,
			BeforeSHA256: switchtarget.SHA256String("current"), DesiredSHA256: switchtarget.SHA256String("current"),
		}},
	}
	entry := transaction.Entry{
		TargetID: "credential", BackendID: "future-backend", Path: "saved-locator",
		Action: planActionNoop, Existed: true, BeforeSHA256: switchtarget.SHA256String("current"),
		PrivateLocator: "opaque-object-reference",
	}
	if _, err := rollbackTargetsFromMetadataWithAdapter(
		metadata,
		transaction.Manifest{Entries: []transaction.Entry{entry}},
		t.TempDir(),
		recoveryIdentityTestAdapter{},
	); err != nil {
		t.Fatalf("recovery identity contract was not accepted: %v", err)
	}

	entry.PrivateLocator = ""
	_, err := rollbackTargetsFromMetadataWithAdapter(
		metadata,
		transaction.Manifest{Entries: []transaction.Entry{entry}},
		t.TempDir(),
		recoveryIdentityTestAdapter{},
	)
	assertErrorCode(t, err, apperror.BackupInvalid)
}

func TestApplyRollbackRejectsChangedActiveState(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := initSwitchingTestRuntime(ctx, configDir); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	createGenericProviderAndProfile(t, ctx, configDir, true)

	targetPath := filepath.Join(t.TempDir(), "settings.txt")
	if _, err := newSwitchingTestEnvironment(t, configDir).profiles.Create(ctx, profile.CreateRequest{ID: "profile-b", Name: "Profile B"}); err != nil {
		t.Fatalf("expected second profile create to succeed, got %v", err)
	}
	for _, profile := range []struct {
		id      string
		content string
	}{
		{id: "profile-a", content: "a\n"},
		{id: "profile-b", content: "b\n"},
	} {
		if _, err := newSwitchingTestEnvironment(t, configDir).targets.Create(ctx, profiletarget.CreateProfileTargetRequest{
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
	firstSwitch, err := newSwitchingTestEnvironment(t, configDir).service.Apply(ctx, ApplySwitchRequest{ProviderID: "provider-a", ProfileID: "profile-a", Confirm: true})
	if err != nil {
		t.Fatalf("expected first switch to succeed, got %v", err)
	}
	if _, err := newSwitchingTestEnvironment(t, configDir).service.Apply(ctx, ApplySwitchRequest{ProviderID: "provider-a", ProfileID: "profile-b", Confirm: true}); err != nil {
		t.Fatalf("expected second switch to succeed, got %v", err)
	}

	_, err = newSwitchingTestEnvironment(t, configDir).service.Rollback(ctx, ApplyRollbackRequest{
		BackupID: firstSwitch.OperationID,
		Confirm:  true,
	})
	assertErrorCode(t, err, apperror.TargetChanged)
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
