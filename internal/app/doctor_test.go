package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	codexpreset "github.com/strahe/profiledeck/internal/codex/preset"
	"github.com/strahe/profiledeck/internal/store"
	"github.com/strahe/profiledeck/internal/targetfs"
)

const deadTestPID = 999999999

func TestDoctorBeforeInitReportsDiagnosticWithoutCreatingRuntime(t *testing.T) {
	ctx := context.Background()
	configDir := filepath.Join(t.TempDir(), "config")

	result, err := Doctor(ctx, DoctorRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected doctor before init to succeed, got %v", err)
	}
	if result.OverallLevel != DoctorLevelWarning {
		t.Fatalf("expected warning overall before init, got %#v", result)
	}
	if len(result.Findings) != 1 || result.Findings[0].ID != "database_not_initialized" {
		t.Fatalf("expected database_not_initialized finding, got %#v", result.Findings)
	}
	if result.Lock.Exists {
		t.Fatalf("expected missing lock before init, got %#v", result.Lock)
	}
	if _, err := os.Stat(configDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected doctor not to create runtime dirs, got %v", err)
	}
}

func TestDoctorHealthyDatabaseReportsOK(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	result, err := Doctor(ctx, DoctorRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected doctor to succeed, got %v", err)
	}
	if result.OverallLevel != DoctorLevelOK || len(result.Operations) != 0 || result.Lock.Exists {
		t.Fatalf("expected clean doctor result, got %#v", result)
	}
}

func TestDoctorReportsCodexPresetAndBindingFailures(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	writeCodexProfileFixture(t, codexDir, "model = \"gpt-5\"\n", `{"tokens":{"account_id":"work","access_token":"token"}}`)
	created, err := CreateCodexProfile(ctx, CreateCodexProfileRequest{ConfigDir: configDir, CodexDir: codexDir, ProfileID: "work"})
	if err != nil {
		t.Fatalf("expected profile create to succeed, got %v", err)
	}

	db, err := openHealthyStore(ctx, configDir, false)
	if err != nil {
		t.Fatalf("expected writable store, got %v", err)
	}
	provider, err := db.GetProvider(ctx, codexconfig.ProviderID)
	if err != nil {
		t.Fatalf("expected Codex provider, got %v", err)
	}
	metadata, err := codexpreset.DecodeProviderMetadata(provider.MetadataJSON)
	if err != nil {
		t.Fatalf("expected provider metadata, got %v", err)
	}
	metadata.PresetVersion = 1
	metadataRaw, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("expected provider metadata encode, got %v", err)
	}
	metadataJSON := string(metadataRaw)
	if _, err := db.UpdateProvider(ctx, store.UpdateProviderParams{ID: codexconfig.ProviderID, MetadataJSON: &metadataJSON}); err != nil {
		t.Fatalf("expected provider metadata update, got %v", err)
	}
	targets, err := db.ListProfileTargets(ctx, "work", codexconfig.ProviderID, true)
	if err != nil {
		t.Fatalf("expected Codex targets, got %v", err)
	}
	var configTarget store.ProfileTarget
	for _, target := range targets {
		if target.TargetID == codexconfig.TargetID {
			configTarget = target
		}
		if err := db.DeleteProfileTarget(ctx, "work", codexconfig.ProviderID, target.TargetID); err != nil {
			t.Fatalf("expected target delete, got %v", err)
		}
	}
	if err := db.DeleteProviderConfigSet(ctx, created.Summary.ConfigSetID); err != nil {
		t.Fatalf("expected config set delete, got %v", err)
	}
	if _, err := db.CreateProfileTarget(ctx, store.CreateProfileTargetParams{
		ProfileID: "work", ProviderID: codexconfig.ProviderID, TargetID: configTarget.TargetID,
		Path: configTarget.Path, PathKey: configTarget.PathKey, Format: configTarget.Format, Strategy: configTarget.Strategy,
		ValueJSON: configTarget.ValueJSON, Enabled: true, MetadataJSON: configTarget.MetadataJSON,
	}); err != nil {
		t.Fatalf("expected missing-resource config binding setup, got %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("expected store close, got %v", err)
	}

	result, err := Doctor(ctx, DoctorRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected doctor to succeed, got %v", err)
	}
	for _, findingID := range []string{"codex_preset_v2_invalid", "codex_login_binding_missing", "codex_config_set_invalid"} {
		if !hasDoctorFinding(result.Findings, findingID) {
			t.Fatalf("expected %s finding, got %#v", findingID, result.Findings)
		}
	}
}

func TestDoctorReportsIncompleteOperationsAndRedactsMalformedMetadata(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	initResult, err := Init(ctx, InitRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	db := openWritableAppTestStore(t, ctx, initResult.DatabasePath)
	defer db.Close()
	if _, err := db.CreatePendingSwitchOperation(ctx, store.CreateSwitchOperationParams{
		ID:           "switch-pending",
		ProfileID:    "profile-a",
		MetadataJSON: `{"checkpoint":"planned","provider_id":"provider-a","profile_id":"profile-a"}`,
	}); err != nil {
		t.Fatalf("expected pending switch setup to succeed, got %v", err)
	}
	if _, err := db.CreatePendingSwitchOperation(ctx, store.CreateSwitchOperationParams{
		ID:           "switch-failed",
		ProfileID:    "profile-a",
		MetadataJSON: `{"checkpoint":"backed_up","provider_id":"provider-a","profile_id":"profile-a","backup_path":"/tmp/profiledeck-backup"}`,
	}); err != nil {
		t.Fatalf("expected failed switch setup to succeed, got %v", err)
	}
	failedMetadata := `{"checkpoint":"backed_up","provider_id":"provider-a","profile_id":"profile-a","backup_path":"/tmp/profiledeck-backup"}`
	if err := db.MarkOperationFailed(ctx, store.MarkOperationFailedParams{
		ID:           "switch-failed",
		ErrorCode:    string(ErrorTargetWriteFailed),
		ErrorMessage: "write failed OPENAI_API_KEY=raw-error-secret",
		MetadataJSON: &failedMetadata,
	}); err != nil {
		t.Fatalf("expected switch failure setup to succeed, got %v", err)
	}
	if _, err := db.CreatePendingRollbackOperation(ctx, store.CreateRollbackOperationParams{
		ID:           "rollback-failed",
		ProfileID:    "profile-a",
		MetadataJSON: `{"api_key":"raw-secret"`,
	}); err != nil {
		t.Fatalf("expected rollback setup to succeed, got %v", err)
	}
	malformedMetadata := `{"api_key":"raw-secret"`
	if err := db.MarkOperationFailed(ctx, store.MarkOperationFailedParams{
		ID:           "rollback-failed",
		ErrorCode:    string(ErrorBackupInvalid),
		ErrorMessage: "backup invalid",
		MetadataJSON: &malformedMetadata,
	}); err != nil {
		t.Fatalf("expected rollback failure setup to succeed, got %v", err)
	}
	if _, err := db.CreatePendingRollbackOperation(ctx, store.CreateRollbackOperationParams{
		ID:           "rollback-created-failed",
		ProfileID:    "profile-a",
		MetadataJSON: `{"checkpoint":"created","backup_id":"backup-a","source_operation_id":"switch-origin"}`,
	}); err != nil {
		t.Fatalf("expected created rollback setup to succeed, got %v", err)
	}
	createdRollbackMetadata := `{"checkpoint":"created","backup_id":"backup-a","source_operation_id":"switch-origin"}`
	if err := db.MarkOperationFailed(ctx, store.MarkOperationFailedParams{
		ID:           "rollback-created-failed",
		ErrorCode:    string(ErrorBackupInvalid),
		ErrorMessage: "backup invalid",
		MetadataJSON: &createdRollbackMetadata,
	}); err != nil {
		t.Fatalf("expected created rollback failure setup to succeed, got %v", err)
	}

	result, err := Doctor(ctx, DoctorRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected doctor to succeed, got %v", err)
	}
	if result.OverallLevel != DoctorLevelError || len(result.Operations) != 4 {
		t.Fatalf("expected four incomplete operations and error overall, got %#v", result)
	}
	pending := doctorTestOperationByID(t, result.Operations, "switch-pending")
	if pending.Level != DoctorLevelError {
		t.Fatalf("expected pending operation error, got %#v", pending)
	}
	failedSwitch := doctorTestOperationByID(t, result.Operations, "switch-failed")
	if failedSwitch.Checkpoint != "backed_up" || failedSwitch.BackupPath == "" {
		t.Fatalf("expected failed switch metadata summary, got %#v", failedSwitch)
	}
	if strings.Contains(failedSwitch.ErrorMessage, "raw-error-secret") || !strings.Contains(failedSwitch.ErrorMessage, redactedValue) {
		t.Fatalf("expected failed switch error message to be redacted, got %#v", failedSwitch)
	}
	failedRollback := doctorTestOperationByID(t, result.Operations, "rollback-failed")
	if !strings.Contains(failedRollback.Reason, "metadata_invalid") {
		t.Fatalf("expected malformed metadata reason, got %#v", failedRollback)
	}
	createdRollback := doctorTestOperationByID(t, result.Operations, "rollback-created-failed")
	if createdRollback.BackupID != "backup-a" || createdRollback.SourceOperationID != "switch-origin" {
		t.Fatalf("expected failed rollback backup summary, got %#v", createdRollback)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("expected doctor result marshal to succeed, got %v", err)
	}
	if strings.Contains(string(raw), "raw-secret") || strings.Contains(string(raw), "raw-error-secret") || strings.Contains(string(raw), "api_key") {
		t.Fatalf("expected doctor result to exclude raw metadata, got %s", raw)
	}
}

func TestDoctorReportsActiveLockAndPendingOperationAsWarning(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	initResult, err := Init(ctx, InitRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	db := openWritableAppTestStore(t, ctx, initResult.DatabasePath)
	defer db.Close()
	if _, err := db.CreatePendingSwitchOperation(ctx, store.CreateSwitchOperationParams{
		ID:           "switch-active",
		ProfileID:    "profile-a",
		MetadataJSON: `{"checkpoint":"planned"}`,
	}); err != nil {
		t.Fatalf("expected pending operation setup to succeed, got %v", err)
	}
	_, paths, err := resolveRuntime(configDir)
	if err != nil {
		t.Fatalf("expected runtime resolve to succeed, got %v", err)
	}
	lock, err := targetfs.AcquireLock(paths.Lock, "switch-active")
	if err != nil {
		t.Fatalf("expected lock setup to succeed, got %v", err)
	}
	defer lock.Release()

	result, err := Doctor(ctx, DoctorRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected doctor to succeed, got %v", err)
	}
	if result.Lock.Repairable || result.Lock.OSLockState != doctorOSLockStateHeld {
		t.Fatalf("expected active lock to be non-repairable, got %#v", result.Lock)
	}
	if len(result.Operations) != 1 || result.Operations[0].Level != DoctorLevelWarning {
		t.Fatalf("expected active pending operation warning, got %#v", result.Operations)
	}
}

func TestDoctorReportsStaleFailedLockAndRepairRemovesOnlyLock(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	initResult, err := Init(ctx, InitRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	db := openWritableAppTestStore(t, ctx, initResult.DatabasePath)
	if _, err := db.CreatePendingSwitchOperation(ctx, store.CreateSwitchOperationParams{
		ID:           "switch-stale",
		ProfileID:    "profile-a",
		MetadataJSON: `{"checkpoint":"backed_up","provider_id":"provider-a","profile_id":"profile-a"}`,
	}); err != nil {
		t.Fatalf("expected failed operation setup to succeed, got %v", err)
	}
	failedMetadata := `{"checkpoint":"backed_up","provider_id":"provider-a","profile_id":"profile-a"}`
	if err := db.MarkOperationFailed(ctx, store.MarkOperationFailedParams{
		ID:           "switch-stale",
		ErrorCode:    string(ErrorTargetWriteFailed),
		ErrorMessage: "write failed",
		MetadataJSON: &failedMetadata,
	}); err != nil {
		t.Fatalf("expected failed operation mark to succeed, got %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("expected setup store close to succeed, got %v", err)
	}
	_, paths, err := resolveRuntime(configDir)
	if err != nil {
		t.Fatalf("expected runtime resolve to succeed, got %v", err)
	}
	writeTestLockFile(t, paths.Lock, "switch-stale", deadTestPID)

	result, err := Doctor(ctx, DoctorRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected doctor to succeed, got %v", err)
	}
	if !result.Lock.StaleCandidate || !result.Lock.Repairable || result.Lock.OperationStatus != store.OperationStatusFailed {
		t.Fatalf("expected stale failed lock, got %#v", result.Lock)
	}

	_, err = RepairDoctorLock(ctx, DoctorRepairLockRequest{ConfigDir: configDir})
	assertAppErrorCode(t, err, ErrorConfirmationRequired)
	repair, err := RepairDoctorLock(ctx, DoctorRepairLockRequest{ConfigDir: configDir, Confirm: true})
	if err != nil {
		t.Fatalf("expected stale lock repair to succeed, got %v", err)
	}
	if !repair.Repaired {
		t.Fatalf("expected repaired result, got %#v", repair)
	}
	if _, err := os.Stat(paths.Lock); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected lock file to be removed, got %v", err)
	}
	db = openAppTestStore(t, ctx, initResult.DatabasePath)
	defer db.Close()
	operation, err := db.GetOperation(ctx, "switch-stale")
	if err != nil {
		t.Fatalf("expected operation to remain, got %v", err)
	}
	if operation.Status != store.OperationStatusFailed {
		t.Fatalf("expected repair not to modify operation, got %#v", operation)
	}
}

func TestDoctorReportsFailedSwitchRecoveryStatus(t *testing.T) {
	ctx := context.Background()

	t.Run("recoverable", func(t *testing.T) {
		configDir := t.TempDir()
		initResult, err := Init(ctx, InitRequest{ConfigDir: configDir})
		if err != nil {
			t.Fatalf("expected init to succeed, got %v", err)
		}
		createGenericProviderAndProfile(t, ctx, configDir, true)
		dir := t.TempDir()
		firstPath := filepath.Join(dir, "target-a.txt")
		secondPath := filepath.Join(dir, "missing", "target-b.txt")
		createProfileTargetForRecovery(t, ctx, configDir, "profile-a", "target-a", firstPath, "first\n")
		createProfileTargetForRecovery(t, ctx, configDir, "profile-a", "target-b", secondPath, "second\n")
		_, err = ApplySwitch(ctx, ApplySwitchRequest{ConfigDir: configDir, ProviderID: "provider-a", ProfileID: "profile-a", Confirm: true})
		assertAppErrorCode(t, err, ErrorTargetWriteFailed)
		failedSwitchID := singleOperationIDByTypeStatus(t, initResult.DatabasePath, store.OperationTypeSwitch, store.OperationStatusFailed)

		result, err := Doctor(ctx, DoctorRequest{ConfigDir: configDir})
		if err != nil {
			t.Fatalf("expected doctor to succeed, got %v", err)
		}
		operation := doctorTestOperationByID(t, result.Operations, failedSwitchID)
		if operation.RecoveryStatus != RecoveryStatusRecoverable || operation.RecoveryReason == "" {
			t.Fatalf("expected recoverable failed switch, got %#v", operation)
		}
	})

	t.Run("unrecoverable before backup", func(t *testing.T) {
		configDir := t.TempDir()
		initResult, err := Init(ctx, InitRequest{ConfigDir: configDir})
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
			t.Fatalf("expected operation setup to succeed, got %v", err)
		}
		metadata := `{"checkpoint":"planned","provider_id":"provider-a","profile_id":"profile-a"}`
		if err := db.MarkOperationFailed(ctx, store.MarkOperationFailedParams{
			ID:           "switch-before-backup",
			ErrorCode:    string(ErrorTargetWriteFailed),
			ErrorMessage: "write failed",
			MetadataJSON: &metadata,
		}); err != nil {
			t.Fatalf("expected failed operation setup to succeed, got %v", err)
		}

		result, err := Doctor(ctx, DoctorRequest{ConfigDir: configDir})
		if err != nil {
			t.Fatalf("expected doctor to succeed, got %v", err)
		}
		operation := doctorTestOperationByID(t, result.Operations, "switch-before-backup")
		if operation.RecoveryStatus != RecoveryStatusUnrecoverable {
			t.Fatalf("expected unrecoverable failed switch, got %#v", operation)
		}
	})

	t.Run("unknown target state", func(t *testing.T) {
		configDir := t.TempDir()
		initResult, err := Init(ctx, InitRequest{ConfigDir: configDir})
		if err != nil {
			t.Fatalf("expected init to succeed, got %v", err)
		}
		createGenericProviderAndProfile(t, ctx, configDir, true)
		dir := t.TempDir()
		firstPath := filepath.Join(dir, "target-a.txt")
		secondPath := filepath.Join(dir, "missing", "target-b.txt")
		createProfileTargetForRecovery(t, ctx, configDir, "profile-a", "target-a", firstPath, "first\n")
		createProfileTargetForRecovery(t, ctx, configDir, "profile-a", "target-b", secondPath, "second\n")
		_, err = ApplySwitch(ctx, ApplySwitchRequest{ConfigDir: configDir, ProviderID: "provider-a", ProfileID: "profile-a", Confirm: true})
		assertAppErrorCode(t, err, ErrorTargetWriteFailed)
		failedSwitchID := singleOperationIDByTypeStatus(t, initResult.DatabasePath, store.OperationTypeSwitch, store.OperationStatusFailed)
		large := strings.Repeat("x", targetfs.MaxFileBytes+1)
		if err := os.WriteFile(firstPath, []byte(large), 0o600); err != nil {
			t.Fatalf("expected large target setup to succeed, got %v", err)
		}

		result, err := Doctor(ctx, DoctorRequest{ConfigDir: configDir})
		if err != nil {
			t.Fatalf("expected doctor to succeed, got %v", err)
		}
		operation := doctorTestOperationByID(t, result.Operations, failedSwitchID)
		if operation.RecoveryStatus != RecoveryStatusUnknown {
			t.Fatalf("expected unknown recovery state, got %#v", operation)
		}
	})
}

func TestDoctorReportsMissingOperationLockAsRepairable(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	_, paths, err := resolveRuntime(configDir)
	if err != nil {
		t.Fatalf("expected runtime resolve to succeed, got %v", err)
	}
	writeTestLockFile(t, paths.Lock, "switch-missing", deadTestPID)

	result, err := Doctor(ctx, DoctorRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected doctor to succeed, got %v", err)
	}
	if !result.Lock.Repairable || result.Lock.OperationStatus != "missing" {
		t.Fatalf("expected missing operation lock to be repairable, got %#v", result.Lock)
	}
}

func TestDoctorReportsAppliedLockResidueAsOKAndRepairable(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	initResult, err := Init(ctx, InitRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	db := openWritableAppTestStore(t, ctx, initResult.DatabasePath)
	if _, err := db.CreatePendingSwitchOperation(ctx, store.CreateSwitchOperationParams{
		ID:           "switch-applied",
		ProfileID:    "profile-a",
		MetadataJSON: `{"checkpoint":"planned"}`,
	}); err != nil {
		t.Fatalf("expected pending switch setup to succeed, got %v", err)
	}
	if err := db.CompleteSwitchOperation(ctx, store.CompleteSwitchOperationParams{
		ID:           "switch-applied",
		ProfileID:    "profile-a",
		ProviderID:   "provider-a",
		MetadataJSON: `{"checkpoint":"applied"}`,
	}); err != nil {
		t.Fatalf("expected applied switch setup to succeed, got %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("expected setup store close to succeed, got %v", err)
	}
	_, paths, err := resolveRuntime(configDir)
	if err != nil {
		t.Fatalf("expected runtime resolve to succeed, got %v", err)
	}
	writeTestLockFile(t, paths.Lock, "switch-applied", deadTestPID)

	result, err := Doctor(ctx, DoctorRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected doctor to succeed, got %v", err)
	}
	if result.OverallLevel != DoctorLevelOK || !result.Lock.Repairable || result.Lock.Reason != "applied_operation_lock_residue" {
		t.Fatalf("expected applied lock residue to be OK and repairable, got %#v", result)
	}

	if _, err := RepairDoctorLock(ctx, DoctorRepairLockRequest{ConfigDir: configDir, Confirm: true}); err != nil {
		t.Fatalf("expected applied lock residue repair to succeed, got %v", err)
	}
	if _, err := os.Stat(paths.Lock); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected lock file to be removed, got %v", err)
	}
	db = openAppTestStore(t, ctx, initResult.DatabasePath)
	defer db.Close()
	operation, err := db.GetOperation(ctx, "switch-applied")
	if err != nil {
		t.Fatalf("expected operation to remain, got %v", err)
	}
	if operation.Status != store.OperationStatusApplied {
		t.Fatalf("expected repair not to modify applied operation, got %#v", operation)
	}
}

func TestDoctorReportsMalformedLockAsRepairableWhenUnlocked(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name    string
		content string
	}{
		{name: "empty", content: ""},
		{name: "missing pid", content: "switch-broken\ncreated_at_unix_ms=1\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configDir := t.TempDir()
			if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
				t.Fatalf("expected init to succeed, got %v", err)
			}
			_, paths, err := resolveRuntime(configDir)
			if err != nil {
				t.Fatalf("expected runtime resolve to succeed, got %v", err)
			}
			if err := os.WriteFile(paths.Lock, []byte(tc.content), 0o600); err != nil {
				t.Fatalf("expected malformed lock setup to succeed, got %v", err)
			}

			result, err := Doctor(ctx, DoctorRequest{ConfigDir: configDir})
			if err != nil {
				t.Fatalf("expected doctor to succeed, got %v", err)
			}
			if !result.Lock.Repairable || result.Lock.Reason != "malformed_lock_file" || result.Lock.OSLockState != doctorOSLockStateFree {
				t.Fatalf("expected malformed unlocked lock to be repairable, got %#v", result.Lock)
			}
			if _, err := RepairDoctorLock(ctx, DoctorRepairLockRequest{ConfigDir: configDir, Confirm: true}); err != nil {
				t.Fatalf("expected malformed lock repair to succeed, got %v", err)
			}
			if _, err := os.Stat(paths.Lock); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("expected malformed lock file to be removed, got %v", err)
			}
		})
	}
}

func TestDoctorParsesLockFieldsWithWhitespace(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	_, paths, err := resolveRuntime(configDir)
	if err != nil {
		t.Fatalf("expected runtime resolve to succeed, got %v", err)
	}
	if err := os.WriteFile(paths.Lock, []byte("switch-missing\npid = 999999999\ncreated_at_unix_ms = 1\n"), 0o600); err != nil {
		t.Fatalf("expected lock setup to succeed, got %v", err)
	}

	result, err := Doctor(ctx, DoctorRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected doctor to succeed, got %v", err)
	}
	if result.Lock.PID != 999999999 || result.Lock.CreatedAtUnixMS != 1 || !result.Lock.Repairable {
		t.Fatalf("expected whitespace lock fields to parse, got %#v", result.Lock)
	}
}

func TestDoctorRefusesUnsafeLocks(t *testing.T) {
	ctx := context.Background()
	t.Run("pending operation", func(t *testing.T) {
		configDir := t.TempDir()
		initResult, err := Init(ctx, InitRequest{ConfigDir: configDir})
		if err != nil {
			t.Fatalf("expected init to succeed, got %v", err)
		}
		db := openWritableAppTestStore(t, ctx, initResult.DatabasePath)
		defer db.Close()
		if _, err := db.CreatePendingSwitchOperation(ctx, store.CreateSwitchOperationParams{
			ID:           "switch-pending-lock",
			ProfileID:    "profile-a",
			MetadataJSON: `{"checkpoint":"planned"}`,
		}); err != nil {
			t.Fatalf("expected pending operation setup to succeed, got %v", err)
		}
		_, paths, err := resolveRuntime(configDir)
		if err != nil {
			t.Fatalf("expected runtime resolve to succeed, got %v", err)
		}
		writeTestLockFile(t, paths.Lock, "switch-pending-lock", deadTestPID)
		result, err := Doctor(ctx, DoctorRequest{ConfigDir: configDir})
		if err != nil {
			t.Fatalf("expected doctor to succeed, got %v", err)
		}
		if result.Lock.Repairable || result.Lock.Reason != "pending_operation" {
			t.Fatalf("expected pending operation lock to be unsafe, got %#v", result.Lock)
		}
	})

	t.Run("pending operation with reused pid", func(t *testing.T) {
		configDir := t.TempDir()
		initResult, err := Init(ctx, InitRequest{ConfigDir: configDir})
		if err != nil {
			t.Fatalf("expected init to succeed, got %v", err)
		}
		db := openWritableAppTestStore(t, ctx, initResult.DatabasePath)
		defer db.Close()
		if _, err := db.CreatePendingSwitchOperation(ctx, store.CreateSwitchOperationParams{
			ID:           "switch-pending-reused-pid",
			ProfileID:    "profile-a",
			MetadataJSON: `{"checkpoint":"planned"}`,
		}); err != nil {
			t.Fatalf("expected pending operation setup to succeed, got %v", err)
		}
		_, paths, err := resolveRuntime(configDir)
		if err != nil {
			t.Fatalf("expected runtime resolve to succeed, got %v", err)
		}
		writeTestLockFile(t, paths.Lock, "switch-pending-reused-pid", os.Getpid())
		result, err := Doctor(ctx, DoctorRequest{ConfigDir: configDir})
		if err != nil {
			t.Fatalf("expected doctor to succeed, got %v", err)
		}
		if result.Lock.Repairable || result.Lock.Reason != "pending_operation" {
			t.Fatalf("expected pending operation lock to remain unsafe, got %#v", result.Lock)
		}
		operation := doctorTestOperationByID(t, result.Operations, "switch-pending-reused-pid")
		if operation.Level != DoctorLevelError || operation.Reason != "pending_operation_without_active_lock" {
			t.Fatalf("expected reused pid not to mark pending operation active, got %#v", operation)
		}
	})

	t.Run("reused pid with free os lock", func(t *testing.T) {
		configDir := t.TempDir()
		if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
			t.Fatalf("expected init to succeed, got %v", err)
		}
		_, paths, err := resolveRuntime(configDir)
		if err != nil {
			t.Fatalf("expected runtime resolve to succeed, got %v", err)
		}
		writeTestLockFile(t, paths.Lock, "switch-alive", os.Getpid())
		result, err := Doctor(ctx, DoctorRequest{ConfigDir: configDir})
		if err != nil {
			t.Fatalf("expected doctor to succeed, got %v", err)
		}
		if !result.Lock.Repairable || result.Lock.Reason != "stale_lock_candidate" || result.Lock.OSLockState != doctorOSLockStateFree {
			t.Fatalf("expected free OS lock to override reused pid, got %#v", result.Lock)
		}
	})

	t.Run("malformed lock without database", func(t *testing.T) {
		configDir := t.TempDir()
		_, paths, err := resolveRuntime(configDir)
		if err != nil {
			t.Fatalf("expected runtime resolve to succeed, got %v", err)
		}
		if err := os.MkdirAll(filepath.Dir(paths.Lock), 0o700); err != nil {
			t.Fatalf("expected lock dir setup to succeed, got %v", err)
		}
		if err := os.WriteFile(paths.Lock, []byte("switch-stale\n"), 0o600); err != nil {
			t.Fatalf("expected malformed lock setup to succeed, got %v", err)
		}
		result, err := Doctor(ctx, DoctorRequest{ConfigDir: configDir})
		if err != nil {
			t.Fatalf("expected doctor to succeed, got %v", err)
		}
		if result.Lock.Repairable || result.Lock.Reason != "database_unavailable" {
			t.Fatalf("expected malformed lock without database to be unsafe, got %#v", result.Lock)
		}
	})

	t.Run("unknown owner", func(t *testing.T) {
		configDir := t.TempDir()
		if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
			t.Fatalf("expected init to succeed, got %v", err)
		}
		_, paths, err := resolveRuntime(configDir)
		if err != nil {
			t.Fatalf("expected runtime resolve to succeed, got %v", err)
		}
		writeTestLockFile(t, paths.Lock, "external-owner", deadTestPID)
		result, err := Doctor(ctx, DoctorRequest{ConfigDir: configDir})
		if err != nil {
			t.Fatalf("expected doctor to succeed, got %v", err)
		}
		if result.Lock.Repairable || result.Lock.Reason != "owner_not_profiledeck_operation" {
			t.Fatalf("expected unknown owner to be unsafe, got %#v", result.Lock)
		}
	})

	t.Run("database unreadable", func(t *testing.T) {
		configDir := t.TempDir()
		_, paths, err := resolveRuntime(configDir)
		if err != nil {
			t.Fatalf("expected runtime resolve to succeed, got %v", err)
		}
		if err := os.MkdirAll(filepath.Dir(paths.Database), 0o700); err != nil {
			t.Fatalf("expected runtime dir setup to succeed, got %v", err)
		}
		if err := os.Mkdir(paths.Database, 0o700); err != nil {
			t.Fatalf("expected database path directory setup to succeed, got %v", err)
		}
		if err := os.MkdirAll(filepath.Dir(paths.Lock), 0o700); err != nil {
			t.Fatalf("expected lock dir setup to succeed, got %v", err)
		}
		writeTestLockFile(t, paths.Lock, "switch-stale", deadTestPID)
		result, err := Doctor(ctx, DoctorRequest{ConfigDir: configDir})
		if err != nil {
			t.Fatalf("expected doctor to succeed, got %v", err)
		}
		if result.Lock.Repairable || result.Lock.Reason != "database_unavailable" {
			t.Fatalf("expected database unavailable lock to be unsafe, got %#v", result.Lock)
		}
	})
}

func TestDoctorWarnsAboutWeakCodexAuthPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode checks do not apply on Windows")
	}
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	authPath := filepath.Join(codexDir, codexconfig.AuthFileName)
	if err := os.WriteFile(filepath.Join(codexDir, codexconfig.ConfigFileName), []byte(`model = "gpt-5.3-codex"`+"\n"), 0o600); err != nil {
		t.Fatalf("expected Codex config setup to succeed, got %v", err)
	}
	if err := os.WriteFile(authPath, []byte(`{"tokens":{"account_id":"work-account","access_token":"secret"}}`), 0o600); err != nil {
		t.Fatalf("expected Codex auth setup to succeed, got %v", err)
	}
	if err := os.Chmod(authPath, 0o644); err != nil {
		t.Fatalf("expected Codex auth chmod setup to succeed, got %v", err)
	}
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	if _, err := CreateCodexProfile(ctx, CreateCodexProfileRequest{
		ConfigDir: configDir,
		CodexDir:  codexDir,
		ProfileID: "work",
	}); err != nil {
		t.Fatalf("expected Codex create to succeed, got %v", err)
	}

	result, err := Doctor(ctx, DoctorRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected doctor to succeed, got %v", err)
	}
	if !hasDoctorFinding(result.Findings, "codex_auth_target_permissions_weak") {
		t.Fatalf("expected weak Codex auth permission warning, got %#v", result.Findings)
	}
}

func writeTestLockFile(t *testing.T, path string, owner string, pid int) {
	t.Helper()

	content := owner + "\npid=" + strconv.Itoa(pid) + "\ncreated_at_unix_ms=1\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("expected lock file setup to succeed, got %v", err)
	}
}

func doctorTestOperationByID(t *testing.T, operations []DoctorOperation, id string) DoctorOperation {
	t.Helper()

	for _, operation := range operations {
		if operation.ID == id {
			return operation
		}
	}
	t.Fatalf("expected doctor operation %s in %#v", id, operations)
	return DoctorOperation{}
}

func hasDoctorFinding(findings []DoctorFinding, id string) bool {
	for _, finding := range findings {
		if finding.ID == id {
			return true
		}
	}
	return false
}
