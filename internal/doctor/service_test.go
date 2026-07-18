package doctor_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/strahe/profiledeck/internal/agent"
	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/codex"
	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	codexpreset "github.com/strahe/profiledeck/internal/codex/preset"
	"github.com/strahe/profiledeck/internal/doctor"
	"github.com/strahe/profiledeck/internal/profiletarget"
	"github.com/strahe/profiledeck/internal/provider"
	profilesruntime "github.com/strahe/profiledeck/internal/runtime"
	"github.com/strahe/profiledeck/internal/store"
	"github.com/strahe/profiledeck/internal/switching"
	"github.com/strahe/profiledeck/internal/targetfs"
)

const (
	deadTestPID           = 999999999
	doctorOSLockStateHeld = "held"
	doctorOSLockStateFree = "free"
)

func TestDoctorBeforeInitReportsDiagnosticWithoutCreatingRuntime(t *testing.T) {
	ctx := context.Background()
	configDir := filepath.Join(t.TempDir(), "config")

	result, err := newDoctorTestApplication(t, configDir, "").Doctor().Run(ctx)
	if err != nil {
		t.Fatalf("expected doctor before init to succeed, got %v", err)
	}
	if result.OverallLevel != doctor.LevelWarning {
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
	if _, err := newDoctorTestApplication(t, configDir, "").Runtime().Init(ctx); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	result, err := newDoctorTestApplication(t, configDir, "").Doctor().Run(ctx)
	if err != nil {
		t.Fatalf("expected doctor to succeed, got %v", err)
	}
	if result.OverallLevel != doctor.LevelOK || len(result.Operations) != 0 || result.Lock.Exists {
		t.Fatalf("expected clean doctor result, got %#v", result)
	}
}

func TestDoctorRejectsUnsupportedSchemaBeforeDatabaseChecks(t *testing.T) {
	ctx := context.Background()
	runtimeService, err := profilesruntime.NewService(t.TempDir())
	if err != nil {
		t.Fatalf("create runtime service: %v", err)
	}
	initialized, err := runtimeService.Init(ctx)
	if err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}

	sqlDB, err := sql.Open("sqlite", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("open runtime database: %v", err)
	}
	unknownName := "209912310001"
	_, insertErr := sqlDB.ExecContext(ctx, `
		INSERT INTO bun_migrations (name, group_id, migrated_at)
		VALUES (?, 99, CURRENT_TIMESTAMP)
	`, unknownName)
	closeErr := sqlDB.Close()
	if err := errors.Join(insertErr, closeErr); err != nil {
		t.Fatalf("insert unsupported migration: %v", err)
	}

	providerChecks := 0
	service := doctor.NewService(runtimeService, nil, []doctor.ProviderCheck{{
		AgentID: agent.Codex,
		Check: func(context.Context, *store.Store) ([]doctor.Finding, error) {
			providerChecks++
			return nil, nil
		},
	}}, nil, nil)
	result, err := service.Run(ctx)
	if err != nil {
		t.Fatalf("run Doctor for unsupported schema: %v", err)
	}
	if result.OverallLevel != doctor.LevelError {
		t.Fatalf("overall level = %q, want %q", result.OverallLevel, doctor.LevelError)
	}
	if providerChecks != 0 {
		t.Fatalf("provider checks = %d, want 0", providerChecks)
	}
	foundUnsupported := false
	for _, finding := range result.Findings {
		if finding.ID == "database_healthy" {
			t.Fatalf("unsupported database was reported healthy: %#v", result.Findings)
		}
		if finding.ID == "database_schema_unsupported" {
			foundUnsupported = true
			if strings.Contains(finding.Message, unknownName) {
				t.Fatalf("Doctor exposed migration name: %#v", finding)
			}
		}
	}
	if !foundUnsupported {
		t.Fatalf("missing database_schema_unsupported finding: %#v", result.Findings)
	}
}

func TestDoctorTreatsReleasedMaintenanceLockAsSafeResidue(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := newDoctorTestApplication(t, configDir, "").Runtime().Init(ctx); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	if _, err := newDoctorTestApplication(t, configDir, "").Providers().Create(ctx, provider.CreateRequest{
		ID: "provider-a", Name: "Provider A", AdapterID: "generic",
	}); err != nil {
		t.Fatalf("expected Provider create to succeed, got %v", err)
	}

	result, err := newDoctorTestApplication(t, configDir, "").Doctor().Run(ctx)
	if err != nil {
		t.Fatalf("expected Doctor to succeed, got %v", err)
	}
	if result.OverallLevel != doctor.LevelOK || result.Lock.Reason != "maintenance_lock_residue" || !result.Lock.Repairable {
		t.Fatalf("expected safe maintenance lock residue, got %#v", result.Lock)
	}
}

func TestDoctorReportsCodexPresetAndBindingFailures(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	if _, err := newDoctorTestApplication(t, configDir, "").Runtime().Init(ctx); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	writeCodexProfileFixture(t, codexDir, "model = \"gpt-5\"\n", `{"tokens":{"account_id":"work","access_token":"token"}}`)
	created, err := newDoctorTestApplication(t, configDir, codexDir).Codex().CreateProfile(ctx, codex.CreateCodexProfileRequest{ProfileID: "work"})
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
	if err := db.DeleteProfileCredentialBinding(ctx, "work", codexconfig.ProviderID, codexpreset.CredentialSlotAuth); err != nil {
		t.Fatalf("expected login binding delete, got %v", err)
	}
	if err := db.DeleteProfileConfigSetBinding(ctx, "work", codexconfig.ProviderID, codexpreset.ConfigSetSlotUserConfig); err != nil {
		t.Fatalf("expected config binding delete, got %v", err)
	}
	if err := db.DeleteProviderConfigSet(ctx, created.Summary.ConfigSetID); err != nil {
		t.Fatalf("expected unbound config set delete, got %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("expected store close, got %v", err)
	}

	result, err := newDoctorTestApplication(t, configDir, "").Doctor().Run(ctx)
	if err != nil {
		t.Fatalf("expected doctor to succeed, got %v", err)
	}
	for _, findingID := range []string{"codex_preset_v2_invalid", "codex_login_binding_missing", "codex_config_binding_missing"} {
		if !hasDoctorFinding(result.Findings, findingID) {
			t.Fatalf("expected %s finding, got %#v", findingID, result.Findings)
		}
	}
}

func TestDoctorInspectsTypedCodexBindingsWhenProviderMetadataIsInvalid(t *testing.T) {
	ctx, configDir, codexDir := setupCodexSwitchProfiles(t, true)
	first, err := newDoctorTestApplication(t, configDir, codexDir).Codex().GetProfile(ctx, "first")
	if err != nil {
		t.Fatalf("expected first profile, got %v", err)
	}
	db, err := openHealthyStore(ctx, configDir, false)
	if err != nil {
		t.Fatalf("expected writable store, got %v", err)
	}
	if _, err := db.UpsertProfileCredentialBinding(ctx, store.UpsertProfileCredentialBindingParams{
		ProfileID: "first", ProviderID: codexconfig.ProviderID,
		SlotID: "unsupported", CredentialID: first.Summary.CredentialID,
	}); err != nil {
		t.Fatalf("expected unsupported binding setup, got %v", err)
	}
	invalidMetadata := "{"
	if _, err := db.UpdateProvider(ctx, store.UpdateProviderParams{ID: codexconfig.ProviderID, MetadataJSON: &invalidMetadata}); err != nil {
		t.Fatalf("expected invalid metadata setup, got %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("expected store close, got %v", err)
	}

	result, err := newDoctorTestApplication(t, configDir, "").Doctor().Run(ctx)
	if err != nil {
		t.Fatalf("expected Doctor to succeed, got %v", err)
	}
	if !hasDoctorFinding(result.Findings, "codex_preset_v2_invalid") || !hasDoctorFindingForProfile(result.Findings, "codex_login_binding_invalid", "first") {
		t.Fatalf("expected invalid metadata and non-active binding findings, got %#v", result.Findings)
	}
}

func TestDoctorReportsIncompleteOperationsAndRedactsMalformedMetadata(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	initResult, err := newDoctorTestApplication(t, configDir, "").Runtime().Init(ctx)
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
		MetadataJSON: `{"checkpoint":"recovery_created","provider_id":"provider-a","profile_id":"profile-a","recovery_path":"/private/recovery-data"}`,
	}); err != nil {
		t.Fatalf("expected failed switch setup to succeed, got %v", err)
	}
	failedMetadata := `{"checkpoint":"recovery_created","provider_id":"provider-a","profile_id":"profile-a","recovery_path":"/private/recovery-data"}`
	if err := db.MarkOperationFailed(ctx, store.MarkOperationFailedParams{
		ID:           "switch-failed",
		ErrorCode:    string(apperror.TargetWriteFailed),
		ErrorMessage: "write failed OPENAI_API_KEY=raw-error-secret",
		MetadataJSON: &failedMetadata,
	}); err != nil {
		t.Fatalf("expected switch failure setup to succeed, got %v", err)
	}
	if _, err := db.CreatePendingSwitchOperation(ctx, store.CreateSwitchOperationParams{
		ID:           "switch-malformed",
		ProfileID:    "profile-a",
		MetadataJSON: `{"api_key":"raw-secret"`,
	}); err != nil {
		t.Fatalf("expected malformed switch setup to succeed, got %v", err)
	}
	malformedMetadata := `{"api_key":"raw-secret"`
	if err := db.MarkOperationFailed(ctx, store.MarkOperationFailedParams{
		ID:           "switch-malformed",
		ErrorCode:    string(apperror.BackupInvalid),
		ErrorMessage: "recovery data invalid",
		MetadataJSON: &malformedMetadata,
	}); err != nil {
		t.Fatalf("expected malformed switch failure setup to succeed, got %v", err)
	}

	result, err := newDoctorTestApplication(t, configDir, "").Doctor().Run(ctx)
	if err != nil {
		t.Fatalf("expected doctor to succeed, got %v", err)
	}
	if result.OverallLevel != doctor.LevelError || len(result.Operations) != 3 {
		t.Fatalf("expected three unresolved root operations and error overall, got %#v", result)
	}
	pending := doctorTestOperationByID(t, result.Operations, "switch-pending")
	if pending.Level != doctor.LevelError {
		t.Fatalf("expected pending operation error, got %#v", pending)
	}
	failedSwitch := doctorTestOperationByID(t, result.Operations, "switch-failed")
	if failedSwitch.Checkpoint != "recovery_created" || failedSwitch.RecoveryStatus != switching.RecoveryStatusUnrecoverable {
		t.Fatalf("expected failed switch recovery summary, got %#v", failedSwitch)
	}
	if strings.Contains(failedSwitch.ErrorMessage, "raw-error-secret") || !strings.Contains(failedSwitch.ErrorMessage, profiletarget.RedactedValue) {
		t.Fatalf("expected failed switch error message to be redacted, got %#v", failedSwitch)
	}
	malformed := doctorTestOperationByID(t, result.Operations, "switch-malformed")
	if !strings.Contains(malformed.Reason, "metadata_invalid") {
		t.Fatalf("expected malformed metadata reason, got %#v", malformed)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("expected doctor result marshal to succeed, got %v", err)
	}
	if strings.Contains(string(raw), "raw-secret") || strings.Contains(string(raw), "raw-error-secret") || strings.Contains(string(raw), "api_key") || strings.Contains(string(raw), "/private/recovery-data") {
		t.Fatalf("expected doctor result to exclude raw metadata, got %s", raw)
	}
}

func TestDoctorReportsActiveLockAndPendingOperationAsWarning(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	initResult, err := newDoctorTestApplication(t, configDir, "").Runtime().Init(ctx)
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

	result, err := newDoctorTestApplication(t, configDir, "").Doctor().Run(ctx)
	if err != nil {
		t.Fatalf("expected doctor to succeed, got %v", err)
	}
	if result.Lock.Repairable || result.Lock.OSLockState != doctorOSLockStateHeld {
		t.Fatalf("expected active lock to be non-repairable, got %#v", result.Lock)
	}
	if len(result.Operations) != 1 || result.Operations[0].Level != doctor.LevelWarning {
		t.Fatalf("expected active pending operation warning, got %#v", result.Operations)
	}
}

func TestDoctorReportsStaleFailedLockAndRepairRemovesOnlyLock(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	initResult, err := newDoctorTestApplication(t, configDir, "").Runtime().Init(ctx)
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
		ErrorCode:    string(apperror.TargetWriteFailed),
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

	result, err := newDoctorTestApplication(t, configDir, "").Doctor().Run(ctx)
	if err != nil {
		t.Fatalf("expected doctor to succeed, got %v", err)
	}
	if !result.Lock.StaleCandidate || !result.Lock.Repairable || result.Lock.OperationStatus != store.OperationStatusFailed {
		t.Fatalf("expected stale failed lock, got %#v", result.Lock)
	}

	_, err = newDoctorTestApplication(t, configDir, "").Doctor().RepairLock(ctx, false)
	assertAppErrorCode(t, err, apperror.ConfirmationRequired)
	repair, err := newDoctorTestApplication(t, configDir, "").Doctor().RepairLock(ctx, true)
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
		initResult, err := newDoctorTestApplication(t, configDir, "").Runtime().Init(ctx)
		if err != nil {
			t.Fatalf("expected init to succeed, got %v", err)
		}
		createGenericProviderAndProfile(t, ctx, configDir, true)
		dir := t.TempDir()
		firstPath := filepath.Join(dir, "target-a.txt")
		secondPath := filepath.Join(dir, "missing", "target-b.txt")
		createProfileTargetForRecovery(t, ctx, configDir, "profile-a", "target-a", firstPath, "first\n")
		createProfileTargetForRecovery(t, ctx, configDir, "profile-a", "target-b", secondPath, "second\n")
		_, err = newDoctorTestApplication(t, configDir, "").Switching().Apply(ctx, switching.ApplySwitchRequest{ProviderID: "provider-a", ProfileID: "profile-a", Confirm: true})
		assertAppErrorCode(t, err, apperror.TargetWriteFailed)
		failedSwitchID := singleOperationIDByTypeStatus(t, initResult.DatabasePath, store.OperationTypeSwitch, store.OperationStatusFailed)

		result, err := newDoctorTestApplication(t, configDir, "").Doctor().Run(ctx)
		if err != nil {
			t.Fatalf("expected doctor to succeed, got %v", err)
		}
		operation := doctorTestOperationByID(t, result.Operations, failedSwitchID)
		if operation.RecoveryStatus != switching.RecoveryStatusRecoverable || operation.RecoveryReason == "" {
			t.Fatalf("expected recoverable failed switch, got %#v", operation)
		}
	})

	t.Run("closable before target writes", func(t *testing.T) {
		configDir := t.TempDir()
		initResult, err := newDoctorTestApplication(t, configDir, "").Runtime().Init(ctx)
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
			ErrorCode:    string(apperror.TargetWriteFailed),
			ErrorMessage: "write failed",
			MetadataJSON: &metadata,
		}); err != nil {
			t.Fatalf("expected failed operation setup to succeed, got %v", err)
		}

		result, err := newDoctorTestApplication(t, configDir, "").Doctor().Run(ctx)
		if err != nil {
			t.Fatalf("expected doctor to succeed, got %v", err)
		}
		operation := doctorTestOperationByID(t, result.Operations, "switch-before-backup")
		if operation.RecoveryStatus != switching.RecoveryStatusClosable || operation.RecoveryAction != switching.RecoveryActionClose {
			t.Fatalf("expected closable failed switch, got %#v", operation)
		}
	})

	t.Run("unknown target state", func(t *testing.T) {
		configDir := t.TempDir()
		initResult, err := newDoctorTestApplication(t, configDir, "").Runtime().Init(ctx)
		if err != nil {
			t.Fatalf("expected init to succeed, got %v", err)
		}
		createGenericProviderAndProfile(t, ctx, configDir, true)
		dir := t.TempDir()
		firstPath := filepath.Join(dir, "target-a.txt")
		secondPath := filepath.Join(dir, "missing", "target-b.txt")
		createProfileTargetForRecovery(t, ctx, configDir, "profile-a", "target-a", firstPath, "first\n")
		createProfileTargetForRecovery(t, ctx, configDir, "profile-a", "target-b", secondPath, "second\n")
		_, err = newDoctorTestApplication(t, configDir, "").Switching().Apply(ctx, switching.ApplySwitchRequest{ProviderID: "provider-a", ProfileID: "profile-a", Confirm: true})
		assertAppErrorCode(t, err, apperror.TargetWriteFailed)
		failedSwitchID := singleOperationIDByTypeStatus(t, initResult.DatabasePath, store.OperationTypeSwitch, store.OperationStatusFailed)
		large := strings.Repeat("x", targetfs.MaxFileBytes+1)
		if err := os.WriteFile(firstPath, []byte(large), 0o600); err != nil {
			t.Fatalf("expected large target setup to succeed, got %v", err)
		}

		result, err := newDoctorTestApplication(t, configDir, "").Doctor().Run(ctx)
		if err != nil {
			t.Fatalf("expected doctor to succeed, got %v", err)
		}
		operation := doctorTestOperationByID(t, result.Operations, failedSwitchID)
		if operation.RecoveryStatus != switching.RecoveryStatusUnknown {
			t.Fatalf("expected unknown recovery state, got %#v", operation)
		}
	})
}

func TestDoctorReportsMissingOperationLockAsRepairable(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := newDoctorTestApplication(t, configDir, "").Runtime().Init(ctx); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	_, paths, err := resolveRuntime(configDir)
	if err != nil {
		t.Fatalf("expected runtime resolve to succeed, got %v", err)
	}
	writeTestLockFile(t, paths.Lock, "switch-missing", deadTestPID)

	result, err := newDoctorTestApplication(t, configDir, "").Doctor().Run(ctx)
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
	initResult, err := newDoctorTestApplication(t, configDir, "").Runtime().Init(ctx)
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

	result, err := newDoctorTestApplication(t, configDir, "").Doctor().Run(ctx)
	if err != nil {
		t.Fatalf("expected doctor to succeed, got %v", err)
	}
	if result.OverallLevel != doctor.LevelOK || !result.Lock.Repairable || result.Lock.Reason != "applied_operation_lock_residue" {
		t.Fatalf("expected applied lock residue to be OK and repairable, got %#v", result)
	}

	if _, err := newDoctorTestApplication(t, configDir, "").Doctor().RepairLock(ctx, true); err != nil {
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
			if _, err := newDoctorTestApplication(t, configDir, "").Runtime().Init(ctx); err != nil {
				t.Fatalf("expected init to succeed, got %v", err)
			}
			_, paths, err := resolveRuntime(configDir)
			if err != nil {
				t.Fatalf("expected runtime resolve to succeed, got %v", err)
			}
			if err := os.WriteFile(paths.Lock, []byte(tc.content), 0o600); err != nil {
				t.Fatalf("expected malformed lock setup to succeed, got %v", err)
			}

			result, err := newDoctorTestApplication(t, configDir, "").Doctor().Run(ctx)
			if err != nil {
				t.Fatalf("expected doctor to succeed, got %v", err)
			}
			if !result.Lock.Repairable || result.Lock.Reason != "malformed_lock_file" || result.Lock.OSLockState != doctorOSLockStateFree {
				t.Fatalf("expected malformed unlocked lock to be repairable, got %#v", result.Lock)
			}
			if _, err := newDoctorTestApplication(t, configDir, "").Doctor().RepairLock(ctx, true); err != nil {
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
	if _, err := newDoctorTestApplication(t, configDir, "").Runtime().Init(ctx); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	_, paths, err := resolveRuntime(configDir)
	if err != nil {
		t.Fatalf("expected runtime resolve to succeed, got %v", err)
	}
	if err := os.WriteFile(paths.Lock, []byte("switch-missing\npid = 999999999\ncreated_at_unix_ms = 1\n"), 0o600); err != nil {
		t.Fatalf("expected lock setup to succeed, got %v", err)
	}

	result, err := newDoctorTestApplication(t, configDir, "").Doctor().Run(ctx)
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
		initResult, err := newDoctorTestApplication(t, configDir, "").Runtime().Init(ctx)
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
		result, err := newDoctorTestApplication(t, configDir, "").Doctor().Run(ctx)
		if err != nil {
			t.Fatalf("expected doctor to succeed, got %v", err)
		}
		if result.Lock.Repairable || result.Lock.Reason != "pending_operation" {
			t.Fatalf("expected pending operation lock to be unsafe, got %#v", result.Lock)
		}
	})

	t.Run("pending operation with reused pid", func(t *testing.T) {
		configDir := t.TempDir()
		initResult, err := newDoctorTestApplication(t, configDir, "").Runtime().Init(ctx)
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
		result, err := newDoctorTestApplication(t, configDir, "").Doctor().Run(ctx)
		if err != nil {
			t.Fatalf("expected doctor to succeed, got %v", err)
		}
		if result.Lock.Repairable || result.Lock.Reason != "pending_operation" {
			t.Fatalf("expected pending operation lock to remain unsafe, got %#v", result.Lock)
		}
		operation := doctorTestOperationByID(t, result.Operations, "switch-pending-reused-pid")
		if operation.Level != doctor.LevelError || operation.Reason != "pending_operation_without_active_lock" {
			t.Fatalf("expected reused pid not to mark pending operation active, got %#v", operation)
		}
	})

	t.Run("reused pid with free os lock", func(t *testing.T) {
		configDir := t.TempDir()
		if _, err := newDoctorTestApplication(t, configDir, "").Runtime().Init(ctx); err != nil {
			t.Fatalf("expected init to succeed, got %v", err)
		}
		_, paths, err := resolveRuntime(configDir)
		if err != nil {
			t.Fatalf("expected runtime resolve to succeed, got %v", err)
		}
		writeTestLockFile(t, paths.Lock, "switch-alive", os.Getpid())
		result, err := newDoctorTestApplication(t, configDir, "").Doctor().Run(ctx)
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
		result, err := newDoctorTestApplication(t, configDir, "").Doctor().Run(ctx)
		if err != nil {
			t.Fatalf("expected doctor to succeed, got %v", err)
		}
		if result.Lock.Repairable || result.Lock.Reason != "database_unavailable" {
			t.Fatalf("expected malformed lock without database to be unsafe, got %#v", result.Lock)
		}
	})

	t.Run("unknown owner", func(t *testing.T) {
		configDir := t.TempDir()
		if _, err := newDoctorTestApplication(t, configDir, "").Runtime().Init(ctx); err != nil {
			t.Fatalf("expected init to succeed, got %v", err)
		}
		_, paths, err := resolveRuntime(configDir)
		if err != nil {
			t.Fatalf("expected runtime resolve to succeed, got %v", err)
		}
		writeTestLockFile(t, paths.Lock, "external-owner", deadTestPID)
		result, err := newDoctorTestApplication(t, configDir, "").Doctor().Run(ctx)
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
		result, err := newDoctorTestApplication(t, configDir, "").Doctor().Run(ctx)
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
	if _, err := newDoctorTestApplication(t, configDir, "").Runtime().Init(ctx); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	if _, err := newDoctorTestApplication(t, configDir, codexDir).Codex().CreateProfile(ctx, codex.CreateCodexProfileRequest{
		ProfileID: "work",
	}); err != nil {
		t.Fatalf("expected Codex create to succeed, got %v", err)
	}

	result, err := newDoctorTestApplication(t, configDir, "").Doctor().Run(ctx)
	if err != nil {
		t.Fatalf("expected doctor to succeed, got %v", err)
	}
	if !hasDoctorFinding(result.Findings, "codex_auth_target_permissions_weak") {
		t.Fatalf("expected weak Codex auth permission warning, got %#v", result.Findings)
	}
}

func writeTestLockFile(t *testing.T, path, owner string, pid int) {
	t.Helper()

	content := owner + "\npid=" + strconv.Itoa(pid) + "\ncreated_at_unix_ms=1\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("expected lock file setup to succeed, got %v", err)
	}
}

func doctorTestOperationByID(t *testing.T, operations []doctor.DoctorOperation, id string) doctor.DoctorOperation {
	t.Helper()

	for _, operation := range operations {
		if operation.ID == id {
			return operation
		}
	}
	t.Fatalf("expected doctor operation %s in %#v", id, operations)
	return doctor.DoctorOperation{}
}

func hasDoctorFinding(findings []doctor.Finding, id string) bool {
	for _, finding := range findings {
		if finding.ID == id {
			return true
		}
	}
	return false
}

func hasDoctorFindingForProfile(findings []doctor.Finding, id, profileID string) bool {
	for _, finding := range findings {
		if finding.ID == id && finding.Details["profile_id"] == profileID {
			return true
		}
	}
	return false
}
