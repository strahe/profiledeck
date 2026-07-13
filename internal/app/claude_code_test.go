package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	claudecodeauth "github.com/strahe/profiledeck/internal/claudecode/auth"
	claudecodeconfig "github.com/strahe/profiledeck/internal/claudecode/config"
	claudekeychain "github.com/strahe/profiledeck/internal/claudecode/keychain"
	"github.com/strahe/profiledeck/internal/store"
)

type failClaudeCodePostVerifyBackend struct {
	targetBackend
	applied  bool
	restored bool
}

func (backend *failClaudeCodePostVerifyBackend) Apply(ctx context.Context, spec targetSpec, snapshot targetSnapshot, desired string, mode os.FileMode, useMode bool) error {
	if err := backend.targetBackend.Apply(ctx, spec, snapshot, desired, mode, useMode); err != nil {
		return err
	}
	backend.applied = true
	return nil
}

func (backend *failClaudeCodePostVerifyBackend) Restore(ctx context.Context, spec targetSpec, snapshot targetSnapshot, sourcePath, sourceSHA string, mode os.FileMode, useMode bool) error {
	if err := backend.targetBackend.Restore(ctx, spec, snapshot, sourcePath, sourceSHA, mode, useMode); err != nil {
		return err
	}
	backend.restored = true
	return nil
}

func (backend *failClaudeCodePostVerifyBackend) Verify(ctx context.Context, spec targetSpec, snapshot targetSnapshot) error {
	if backend.applied || backend.restored {
		return NewError(ErrorTargetChanged, "Claude Code credential changed during post-verify")
	}
	return backend.targetBackend.Verify(ctx, spec, snapshot)
}

func TestClaudeCodeCreateSwitchCaptureKnownMatchAndRollback(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	credentialDir := t.TempDir()
	credentialPath := filepath.Join(credentialDir, claudecodeconfig.CredentialsFile)
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	seedClaudeCodeFileProvider(t, ctx, configDir, credentialPath)
	writeClaudeCodeCredential(t, credentialPath, testClaudeCodePayload("access-a", "refresh-a", 4102444800000))
	first, err := CreateClaudeCodeProfile(ctx, CreateClaudeCodeProfileRequest{ConfigDir: configDir, ProfileID: "first"})
	if err != nil || !first.Summary.Active || first.Summary.CredentialStatus != "valid" || !strings.HasPrefix(first.OperationID, "claude-code-profile-create-") {
		t.Fatalf("Create first = %#v, error = %v", first, err)
	}
	writeClaudeCodeCredential(t, credentialPath, testClaudeCodePayload("access-b", "refresh-b", 4102444800000))
	second, err := CreateClaudeCodeProfile(ctx, CreateClaudeCodeProfileRequest{ConfigDir: configDir, ProfileID: "second"})
	if err != nil || !second.Summary.Active {
		t.Fatalf("Create second = %#v, error = %v", second, err)
	}

	plan, err := BuildPlan(ctx, BuildPlanRequest{ConfigDir: configDir, ProviderID: claudecodeconfig.ProviderID, ProfileID: "first"})
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	if len(plan.Operations) != 1 || plan.Operations[0].Action != planActionUpdate || plan.Operations[0].BackendID != targetBackendFile {
		t.Fatalf("unexpected switch plan: %#v", plan.Operations)
	}
	planJSON := mustJSON(t, plan)
	for _, secret := range []string{"access-a", "refresh-a", "access-b", "refresh-b", "accessToken", "refreshToken"} {
		if bytes.Contains(planJSON, []byte(secret)) {
			t.Fatalf("public Claude Code plan leaked %q: %s", secret, planJSON)
		}
	}
	result, err := ApplySwitch(ctx, ApplySwitchRequest{
		ConfigDir: configDir, ProviderID: claudecodeconfig.ProviderID, ProfileID: "first",
		ExpectedPlanFingerprint: plan.PlanFingerprint, Confirm: true,
	})
	if err != nil {
		t.Fatalf("ApplySwitch() error = %v", err)
	}
	if !strings.HasPrefix(result.OperationID, "claude-code-switch-") {
		t.Fatalf("operation id = %q", result.OperationID)
	}
	manifestRaw, err := os.ReadFile(filepath.Join(result.BackupPath, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	db, err := openHealthyStore(ctx, configDir, true)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := db.GetOperation(ctx, result.OperationID)
	if closeErr := db.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	for boundary, raw := range map[string][]byte{"operation metadata": []byte(operation.MetadataJSON), "backup manifest": manifestRaw, "switch result": mustJSON(t, result)} {
		for _, secret := range []string{"access-a", "refresh-a", "access-b", "refresh-b", "accessToken", "refreshToken"} {
			if bytes.Contains(raw, []byte(secret)) {
				t.Fatalf("Claude Code %s leaked %q: %s", boundary, secret, raw)
			}
		}
	}
	backupDetail, err := ShowBackup(ctx, ShowBackupRequest{ConfigDir: configDir, BackupID: result.OperationID})
	if err != nil || len(backupDetail.Entries) != 1 || backupDetail.Entries[0].BeforeSHA256 != "" {
		t.Fatalf("public Claude Code file backup exposed a login hash: detail=%#v error=%v", backupDetail, err)
	}
	backupDetailJSON := mustJSON(t, backupDetail)
	for _, secret := range []string{"access-a", "refresh-a", "access-b", "refresh-b", "accessToken", "refreshToken"} {
		if bytes.Contains(backupDetailJSON, []byte(secret)) {
			t.Fatalf("public Claude Code backup detail leaked %q: %s", secret, backupDetailJSON)
		}
	}
	assertClaudeCodeWorkingPayload(t, credentialPath, "access-a")
	if _, err := ApplyRollback(ctx, ApplyRollbackRequest{ConfigDir: configDir, BackupID: result.OperationID, Confirm: true}); err != nil {
		t.Fatalf("ApplyRollback() error = %v", err)
	}
	assertClaudeCodeWorkingPayload(t, credentialPath, "access-b")

	// A working copy that matches another known credential must not overwrite
	// the credential bound to the active Profile.
	writeClaudeCodeCredential(t, credentialPath, testClaudeCodePayload("access-a", "refresh-a", 4102444800000))
	knownPlan, err := BuildPlan(ctx, BuildPlanRequest{ConfigDir: configDir, ProviderID: claudecodeconfig.ProviderID, ProfileID: "first"})
	if err != nil {
		t.Fatalf("known-match BuildPlan() error = %v", err)
	}
	if len(knownPlan.StateCaptures) != 0 {
		t.Fatalf("known credential was scheduled to overwrite active login: %#v", knownPlan.StateCaptures)
	}
	secondCredential := claudeCodeCredentialForProfile(t, ctx, configDir, "second")
	if !strings.Contains(secondCredential.PayloadJSON, "access-b") {
		t.Fatalf("second credential was overwritten: %s", secondCredential.PayloadJSON)
	}

	// A new valid version of the active credential is captured atomically with
	// the subsequent switch.
	writeClaudeCodeCredential(t, credentialPath, testClaudeCodePayload("access-b-refreshed", "refresh-b", 4102444800000))
	capturePlan, err := BuildPlan(ctx, BuildPlanRequest{ConfigDir: configDir, ProviderID: claudecodeconfig.ProviderID, ProfileID: "first"})
	if err != nil {
		t.Fatalf("capture BuildPlan() error = %v", err)
	}
	if len(capturePlan.StateCaptures) != 1 || capturePlan.StateCaptures[0].ResourceID != second.Summary.CredentialID {
		t.Fatalf("unexpected active capture: %#v", capturePlan.StateCaptures)
	}
	if _, err := ApplySwitch(ctx, ApplySwitchRequest{ConfigDir: configDir, ProviderID: claudecodeconfig.ProviderID, ProfileID: "first", Confirm: true}); err != nil {
		t.Fatalf("capturing switch error = %v", err)
	}
	if _, err := ApplySwitch(ctx, ApplySwitchRequest{ConfigDir: configDir, ProviderID: claudecodeconfig.ProviderID, ProfileID: "second", Confirm: true}); err != nil {
		t.Fatalf("switch back error = %v", err)
	}
	assertClaudeCodeWorkingPayload(t, credentialPath, "access-b-refreshed")
}

func TestClaudeCodeFilePostVerifyFailureDoesNotCommitActiveState(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	credentialPath := filepath.Join(t.TempDir(), claudecodeconfig.CredentialsFile)
	initResult, err := Init(ctx, InitRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatal(err)
	}
	seedClaudeCodeFileProvider(t, ctx, configDir, credentialPath)
	writeClaudeCodeCredential(t, credentialPath, testClaudeCodePayload("first", "first-refresh", 4102444800000))
	if _, err := CreateClaudeCodeProfile(ctx, CreateClaudeCodeProfileRequest{ConfigDir: configDir, ProfileID: "first"}); err != nil {
		t.Fatal(err)
	}
	writeClaudeCodeCredential(t, credentialPath, testClaudeCodePayload("second", "second-refresh", 4102444800000))
	if _, err := CreateClaudeCodeProfile(ctx, CreateClaudeCodeProfileRequest{ConfigDir: configDir, ProfileID: "second"}); err != nil {
		t.Fatal(err)
	}

	previous := targetBackends[targetBackendFile]
	failing := &failClaudeCodePostVerifyBackend{targetBackend: previous}
	targetBackends[targetBackendFile] = failing
	t.Cleanup(func() { targetBackends[targetBackendFile] = previous })
	_, err = ApplySwitch(ctx, ApplySwitchRequest{ConfigDir: configDir, ProviderID: claudecodeconfig.ProviderID, ProfileID: "first", Confirm: true})
	assertAppErrorCode(t, err, ErrorTargetChanged)
	db, err := store.Open(ctx, initResult.DatabasePath, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	active, err := db.GetActiveState(ctx, store.ActiveStateScopeProvider, claudecodeconfig.ProviderID)
	if err != nil || active.ProfileID != "second" {
		t.Fatalf("active state advanced after failed post-verify: active=%#v error=%v", active, err)
	}
	failedSwitchID := singleOperationIDByTypeStatus(t, initResult.DatabasePath, store.OperationTypeSwitch, store.OperationStatusFailed)
	if !strings.HasPrefix(failedSwitchID, "claude-code-switch-") {
		t.Fatalf("failed switch operation id = %q", failedSwitchID)
	}
}

func TestClaudeCodeRollbackPostVerifyFailureDoesNotCommitActiveState(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	credentialPath := filepath.Join(t.TempDir(), claudecodeconfig.CredentialsFile)
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatal(err)
	}
	seedClaudeCodeFileProvider(t, ctx, configDir, credentialPath)
	writeClaudeCodeCredential(t, credentialPath, testClaudeCodePayload("first", "first-refresh", 4102444800000))
	if _, err := CreateClaudeCodeProfile(ctx, CreateClaudeCodeProfileRequest{ConfigDir: configDir, ProfileID: "first"}); err != nil {
		t.Fatal(err)
	}
	writeClaudeCodeCredential(t, credentialPath, testClaudeCodePayload("second", "second-refresh", 4102444800000))
	if _, err := CreateClaudeCodeProfile(ctx, CreateClaudeCodeProfileRequest{ConfigDir: configDir, ProfileID: "second"}); err != nil {
		t.Fatal(err)
	}
	switched, err := ApplySwitch(ctx, ApplySwitchRequest{ConfigDir: configDir, ProviderID: claudecodeconfig.ProviderID, ProfileID: "first", Confirm: true})
	if err != nil {
		t.Fatal(err)
	}

	previous := targetBackends[targetBackendFile]
	failing := &failClaudeCodePostVerifyBackend{targetBackend: previous}
	targetBackends[targetBackendFile] = failing
	t.Cleanup(func() { targetBackends[targetBackendFile] = previous })
	_, err = ApplyRollback(ctx, ApplyRollbackRequest{ConfigDir: configDir, BackupID: switched.OperationID, Confirm: true})
	assertAppErrorCode(t, err, ErrorTargetChanged)
	db, err := openHealthyStore(ctx, configDir, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	active, err := db.GetActiveState(ctx, store.ActiveStateScopeProvider, claudecodeconfig.ProviderID)
	if err != nil || active.ProfileID != "first" {
		t.Fatalf("active state changed after failed rollback post-verify: active=%#v error=%v", active, err)
	}
}

func TestClaudeCodeExpiredWorkingCopyDoesNotAutoOverwriteAndSharedSaveRequiresConfirmation(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	credentialPath := filepath.Join(t.TempDir(), claudecodeconfig.CredentialsFile)
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatal(err)
	}
	seedClaudeCodeFileProvider(t, ctx, configDir, credentialPath)
	writeClaudeCodeCredential(t, credentialPath, testClaudeCodePayload("active", "refresh", 4102444800000))
	created, err := CreateClaudeCodeProfile(ctx, CreateClaudeCodeProfileRequest{ConfigDir: configDir, ProfileID: "work"})
	if err != nil {
		t.Fatal(err)
	}
	db, err := openHealthyStore(ctx, configDir, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateProfile(ctx, store.CreateProfileParams{ID: "shared", Name: "shared", MetadataJSON: "{}"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpsertProfileCredentialBinding(ctx, store.UpsertProfileCredentialBindingParams{
		ProfileID: "shared", ProviderID: claudecodeconfig.ProviderID, SlotID: claudecodeconfig.CredentialSlot, CredentialID: created.Summary.CredentialID,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	writeClaudeCodeCredential(t, credentialPath, testClaudeCodePayload("expired-refresh", "refresh", 1))
	plan, err := BuildPlan(ctx, BuildPlanRequest{ConfigDir: configDir, ProviderID: claudecodeconfig.ProviderID, ProfileID: "work"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.StateCaptures) != 0 || len(plan.Operations) != 1 || plan.Operations[0].Action != planActionUpdate {
		t.Fatalf("expired working copy was not kept out of automatic capture: captures=%#v operations=%#v", plan.StateCaptures, plan.Operations)
	}
	_, err = SaveActiveClaudeCodeProfile(ctx, SaveActiveClaudeCodeProfileRequest{ConfigDir: configDir})
	assertAppErrorCode(t, err, ErrorConfirmationRequired)
	if !strings.Contains(err.Error(), "2 Profiles") {
		t.Fatalf("shared confirmation does not report affected Profile count: %v", err)
	}
	saved, err := SaveActiveClaudeCodeProfile(ctx, SaveActiveClaudeCodeProfileRequest{ConfigDir: configDir, ConfirmShared: true})
	if err != nil {
		t.Fatalf("confirmed shared save error = %v", err)
	}
	if !strings.HasPrefix(saved.OperationID, "claude-code-profile-save-current-") {
		t.Fatalf("save-current operation id = %q", saved.OperationID)
	}
	shared, err := GetClaudeCodeProfile(ctx, GetClaudeCodeProfileRequest{ConfigDir: configDir, ProfileID: "shared"})
	if err != nil || shared.Summary.CredentialStatus != "expired" || shared.Summary.CredentialReferenceCount != 2 {
		t.Fatalf("shared summary = %#v, error = %v", shared, err)
	}
	writeClaudeCodeCredential(t, credentialPath, testClaudeCodePayload("renewed", "refresh-renewed", 4102444800000))
	sharedCapturePlan, err := BuildPlan(ctx, BuildPlanRequest{ConfigDir: configDir, ProviderID: claudecodeconfig.ProviderID, ProfileID: "work"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sharedCapturePlan.StateCaptures) != 1 || !strings.Contains(strings.Join(sharedCapturePlan.Warnings, "\n"), "shared by 2 Profiles") {
		t.Fatalf("shared automatic capture warning = %#v, captures = %#v", sharedCapturePlan.Warnings, sharedCapturePlan.StateCaptures)
	}
}

func TestClaudeCodeUnboundKnownCredentialDoesNotOverwriteActiveCredential(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	credentialPath := filepath.Join(t.TempDir(), claudecodeconfig.CredentialsFile)
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatal(err)
	}
	seedClaudeCodeFileProvider(t, ctx, configDir, credentialPath)
	writeClaudeCodeCredential(t, credentialPath, testClaudeCodePayload("active", "active-refresh", 4102444800000))
	created, err := CreateClaudeCodeProfile(ctx, CreateClaudeCodeProfileRequest{ConfigDir: configDir, ProfileID: "work"})
	if err != nil {
		t.Fatal(err)
	}

	unboundPayload, _, err := claudecodeauth.Normalize([]byte(testClaudeCodePayload("unbound", "unbound-refresh", 4102444800000)))
	if err != nil {
		t.Fatal(err)
	}
	db, err := openHealthyStore(ctx, configDir, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpsertProviderCredential(ctx, store.UpsertProviderCredentialParams{
		ID: "unbound-login", ProviderID: claudecodeconfig.ProviderID, CredentialKind: claudecodeconfig.CredentialKind,
		PayloadJSON: unboundPayload, PayloadSHA256: sha256HexString(unboundPayload), MetadataJSON: "{}",
	}); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	writeClaudeCodeCredential(t, credentialPath, testClaudeCodePayload("unbound", "unbound-refresh", 4102444800000))
	plan, err := BuildPlan(ctx, BuildPlanRequest{ConfigDir: configDir, ProviderID: claudecodeconfig.ProviderID, ProfileID: "work"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.StateCaptures) != 0 || len(plan.Operations) != 1 || plan.Operations[0].Action != planActionUpdate {
		t.Fatalf("unbound known login was treated as an active login refresh: captures=%#v operations=%#v", plan.StateCaptures, plan.Operations)
	}
	active := claudeCodeCredentialForProfile(t, ctx, configDir, "work")
	if active.ID != created.Summary.CredentialID || !strings.Contains(active.PayloadJSON, `"accessToken":"active"`) {
		t.Fatalf("active login was overwritten by an unbound known login: %#v", active)
	}
}

func TestClaudeCodeDoctorChecksUnboundCredentialSchema(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	credentialPath := filepath.Join(t.TempDir(), claudecodeconfig.CredentialsFile)
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatal(err)
	}
	seedClaudeCodeFileProvider(t, ctx, configDir, credentialPath)
	invalidPayload := `{"claudeAiOauth":{"accessToken":"access","refreshToken":"refresh"}}`
	db, err := openHealthyStore(ctx, configDir, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpsertProviderCredential(ctx, store.UpsertProviderCredentialParams{
		ID: "unbound-invalid", ProviderID: claudecodeconfig.ProviderID, CredentialKind: claudecodeconfig.CredentialKind,
		PayloadJSON: invalidPayload, PayloadSHA256: sha256HexString(invalidPayload), MetadataJSON: "{}",
	}); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	nonCanonicalPayload := `{ "claudeAiOauth": { "accessToken": "access", "refreshToken": "refresh", "subscriptionType": "max" } }`
	if _, err := db.UpsertProviderCredential(ctx, store.UpsertProviderCredentialParams{
		ID: "unbound-noncanonical", ProviderID: claudecodeconfig.ProviderID, CredentialKind: claudecodeconfig.CredentialKind,
		PayloadJSON: nonCanonicalPayload, PayloadSHA256: sha256HexString(nonCanonicalPayload), MetadataJSON: "{}",
	}); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if _, err := requireClaudeCodeCredential(ctx, db, "unbound-noncanonical"); err == nil {
		_ = db.Close()
		t.Fatal("non-canonical Claude Code login storage was accepted")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	doctor, err := Doctor(ctx, DoctorRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatal(err)
	}
	if !hasDoctorFinding(doctor.Findings, "claude_code_login_state_invalid") {
		t.Fatalf("Doctor did not report an invalid unbound Claude Code login: %#v", doctor.Findings)
	}
}

func TestClaudeCodeProviderRejectsGenericTargetsAndDoesNotRegisterClaudeAlias(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	credentialPath := filepath.Join(t.TempDir(), claudecodeconfig.CredentialsFile)
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatal(err)
	}
	seedClaudeCodeFileProvider(t, ctx, configDir, credentialPath)
	writeClaudeCodeCredential(t, credentialPath, testClaudeCodePayload("access", "refresh", 4102444800000))
	if _, err := CreateClaudeCodeProfile(ctx, CreateClaudeCodeProfileRequest{ConfigDir: configDir, ProfileID: "work"}); err != nil {
		t.Fatal(err)
	}
	_, err := CreateProfileTarget(ctx, CreateProfileTargetRequest{
		ConfigDir: configDir, ProfileID: "work", ProviderID: claudecodeconfig.ProviderID,
		TargetID: "settings", Path: filepath.Join(t.TempDir(), "settings.json"), Format: targetFormatJSON,
		Strategy: targetStrategyReplaceFile, ValueJSON: `{}`,
	})
	assertAppErrorCode(t, err, ErrorTargetInvalid)

	db, err := openHealthyStore(ctx, configDir, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateProfileTarget(ctx, store.CreateProfileTargetParams{
		ProfileID: "work", ProviderID: claudecodeconfig.ProviderID, TargetID: "corrupt-settings",
		Path: filepath.Join(t.TempDir(), "settings.json"), Format: targetFormatJSON,
		Strategy: targetStrategyReplaceFile, ValueJSON: `{}`, Enabled: false, MetadataJSON: `{}`,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	changedName := "Changed before validation"
	_, err = UpdateClaudeCodeProfile(ctx, UpdateClaudeCodeProfileRequest{ConfigDir: configDir, ProfileID: "work", Name: &changedName})
	assertAppErrorCode(t, err, ErrorClaudeCodeInvalid)
	unchanged, err := GetProfile(ctx, GetProfileRequest{ConfigDir: configDir, ID: "work"})
	if err != nil || unchanged.Name == changedName {
		t.Fatalf("invalid Claude Code binding partially updated the shared Profile: profile=%#v error=%v", unchanged, err)
	}
	doctor, err := Doctor(ctx, DoctorRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatal(err)
	}
	if !hasDoctorFindingForProfile(doctor.Findings, "claude_code_login_binding_invalid", "work") {
		t.Fatalf("Doctor did not report unsupported Claude Code generic target: %#v", doctor.Findings)
	}
	_, err = BuildPlan(ctx, BuildPlanRequest{ConfigDir: configDir, ProviderID: claudecodeconfig.ProviderID, ProfileID: "work"})
	assertAppErrorCode(t, err, ErrorClaudeCodeInvalid)
}

func TestClaudeCodeFileSymlinkIsBlockedDuringPlanning(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	credentialDir := t.TempDir()
	credentialPath := filepath.Join(credentialDir, claudecodeconfig.CredentialsFile)
	realPath := filepath.Join(credentialDir, "real-credentials.json")
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatal(err)
	}
	seedClaudeCodeFileProvider(t, ctx, configDir, credentialPath)
	payload := testClaudeCodePayload("access", "refresh", 4102444800000)
	writeClaudeCodeCredential(t, credentialPath, payload)
	if _, err := CreateClaudeCodeProfile(ctx, CreateClaudeCodeProfileRequest{ConfigDir: configDir, ProfileID: "work"}); err != nil {
		t.Fatal(err)
	}
	writeClaudeCodeCredential(t, realPath, payload)
	if err := os.Remove(credentialPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realPath, credentialPath); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	plan, err := BuildPlan(ctx, BuildPlanRequest{ConfigDir: configDir, ProviderID: claudecodeconfig.ProviderID, ProfileID: "work"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Operations) != 1 || plan.Operations[0].Action != planActionUnsupported || plan.Operations[0].StatusReason != planReasonTargetIsSymlink {
		t.Fatalf("symlink plan = %#v", plan.Operations)
	}
}

func TestClaudeCodeFileMissingTargetCanBeRestoredOnlyWhenParentExists(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	credentialDir := t.TempDir()
	credentialPath := filepath.Join(credentialDir, claudecodeconfig.CredentialsFile)
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatal(err)
	}
	seedClaudeCodeFileProvider(t, ctx, configDir, credentialPath)
	writeClaudeCodeCredential(t, credentialPath, testClaudeCodePayload("access", "refresh", 4102444800000))
	if _, err := CreateClaudeCodeProfile(ctx, CreateClaudeCodeProfileRequest{ConfigDir: configDir, ProfileID: "work"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(credentialPath); err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPlan(ctx, BuildPlanRequest{ConfigDir: configDir, ProviderID: claudecodeconfig.ProviderID, ProfileID: "work"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Operations) != 1 || plan.Operations[0].Action != planActionCreate {
		t.Fatalf("missing file plan = %#v", plan.Operations)
	}
	if _, err := ApplySwitch(ctx, ApplySwitchRequest{
		ConfigDir: configDir, ProviderID: claudecodeconfig.ProviderID, ProfileID: "work",
		ExpectedPlanFingerprint: plan.PlanFingerprint, Confirm: true,
	}); err != nil {
		t.Fatal(err)
	}
	assertClaudeCodeWorkingPayload(t, credentialPath, "access")

	if err := os.Remove(credentialPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(credentialDir); err != nil {
		t.Fatal(err)
	}
	plan, err = BuildPlan(ctx, BuildPlanRequest{ConfigDir: configDir, ProviderID: claudecodeconfig.ProviderID, ProfileID: "work"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Operations) != 1 || plan.Operations[0].Action != planActionUnsupported || plan.Operations[0].StatusReason != planReasonTargetMissing {
		t.Fatalf("missing parent plan = %#v", plan.Operations)
	}
}

func TestClaudeCodeExpiredCredentialCanSwitchAndSaveRenewal(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	credentialPath := filepath.Join(t.TempDir(), claudecodeconfig.CredentialsFile)
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatal(err)
	}
	seedClaudeCodeFileProvider(t, ctx, configDir, credentialPath)
	writeClaudeCodeCredential(t, credentialPath, testClaudeCodePayload("valid", "valid-refresh", 4102444800000))
	if _, err := CreateClaudeCodeProfile(ctx, CreateClaudeCodeProfileRequest{ConfigDir: configDir, ProfileID: "valid"}); err != nil {
		t.Fatal(err)
	}
	writeClaudeCodeCredential(t, credentialPath, testClaudeCodePayload("expired", "expired-refresh", 1))
	expired, err := CreateClaudeCodeProfile(ctx, CreateClaudeCodeProfileRequest{ConfigDir: configDir, ProfileID: "expired"})
	if err != nil || expired.Summary.CredentialStatus != claudecodeauth.StatusExpired {
		t.Fatalf("expired create = %#v, error = %v", expired, err)
	}
	if _, err := ApplySwitch(ctx, ApplySwitchRequest{ConfigDir: configDir, ProviderID: claudecodeconfig.ProviderID, ProfileID: "valid", Confirm: true}); err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPlan(ctx, BuildPlanRequest{ConfigDir: configDir, ProviderID: claudecodeconfig.ProviderID, ProfileID: "expired"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(plan.Warnings, "\n"), "expired") {
		t.Fatalf("expired switch warning = %#v", plan.Warnings)
	}
	if _, err := ApplySwitch(ctx, ApplySwitchRequest{
		ConfigDir: configDir, ProviderID: claudecodeconfig.ProviderID, ProfileID: "expired",
		ExpectedPlanFingerprint: plan.PlanFingerprint, Confirm: true,
	}); err != nil {
		t.Fatal(err)
	}
	assertClaudeCodeWorkingPayload(t, credentialPath, "expired")

	writeClaudeCodeCredential(t, credentialPath, testClaudeCodePayload("renewed", "renewed-refresh", 4102444800000))
	renewed, err := SaveActiveClaudeCodeProfile(ctx, SaveActiveClaudeCodeProfileRequest{ConfigDir: configDir})
	if err != nil || renewed.Summary.CredentialStatus != claudecodeauth.StatusValid {
		t.Fatalf("renewed save = %#v, error = %v", renewed, err)
	}
}

func TestClaudeCodeProviderMetadataRejectsTrailingJSON(t *testing.T) {
	metadata := newClaudeCodeProviderMetadata(claudecodeconfig.Locator{Storage: claudecodeconfig.StorageFile, Path: filepath.Join(t.TempDir(), claudecodeconfig.CredentialsFile)})
	raw, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	_, err = validateClaudeCodeProvider(store.Provider{ID: claudecodeconfig.ProviderID, AdapterID: claudecodeconfig.AdapterID, MetadataJSON: string(raw) + `{}`})
	assertAppErrorCode(t, err, ErrorClaudeCodeInvalid)
	unsupported := newClaudeCodeProviderMetadata(claudecodeconfig.Locator{Storage: claudecodeconfig.StorageFile, Path: filepath.Join(t.TempDir(), "other.json")})
	unsupportedRaw, err := json.Marshal(unsupported)
	if err != nil {
		t.Fatal(err)
	}
	_, err = validateClaudeCodeProvider(store.Provider{ID: claudecodeconfig.ProviderID, AdapterID: claudecodeconfig.AdapterID, MetadataJSON: string(unsupportedRaw)})
	assertAppErrorCode(t, err, ErrorClaudeCodeInvalid)
	nonCleanPath := t.TempDir() + string(os.PathSeparator) + "nested" + string(os.PathSeparator) + ".." + string(os.PathSeparator) + claudecodeconfig.CredentialsFile
	nonClean := newClaudeCodeProviderMetadata(claudecodeconfig.Locator{Storage: claudecodeconfig.StorageFile, Path: nonCleanPath})
	nonCleanRaw, err := json.Marshal(nonClean)
	if err != nil {
		t.Fatal(err)
	}
	_, err = validateClaudeCodeProvider(store.Provider{ID: claudecodeconfig.ProviderID, AdapterID: claudecodeconfig.AdapterID, MetadataJSON: string(nonCleanRaw)})
	assertAppErrorCode(t, err, ErrorClaudeCodeInvalid)
	spacedAccount := newClaudeCodeProviderMetadata(claudecodeconfig.Locator{Storage: claudecodeconfig.StorageKeychain, Service: claudecodeconfig.KeychainService, Account: " tester "})
	spacedRaw, err := json.Marshal(spacedAccount)
	if err != nil {
		t.Fatal(err)
	}
	_, err = validateClaudeCodeProvider(store.Provider{ID: claudecodeconfig.ProviderID, AdapterID: claudecodeconfig.AdapterID, MetadataJSON: string(spacedRaw)})
	assertAppErrorCode(t, err, ErrorClaudeCodeInvalid)
}

func TestClaudeCodeObservedAuthOverrideHintsExposeNamesOnly(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "override-secret-value")
	t.Setenv("CLAUDE_CODE_USE_BEDROCK", "1")
	hints := observedClaudeCodeAuthOverrideHints()
	raw := mustJSON(t, hints)
	if !bytes.Contains(raw, []byte("ANTHROPIC_AUTH_TOKEN")) || !bytes.Contains(raw, []byte("CLAUDE_CODE_USE_BEDROCK")) {
		t.Fatalf("observed hints = %s", raw)
	}
	if bytes.Contains(raw, []byte("override-secret-value")) {
		t.Fatalf("observed hints leaked an environment value: %s", raw)
	}
}

func TestClaudeCodeCreateReenablesManagedProvider(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	credentialPath := filepath.Join(t.TempDir(), claudecodeconfig.CredentialsFile)
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatal(err)
	}
	seedClaudeCodeFileProvider(t, ctx, configDir, credentialPath)
	db, err := openHealthyStore(ctx, configDir, false)
	if err != nil {
		t.Fatal(err)
	}
	disabled, customName := false, "Custom"
	if _, err := db.UpdateProvider(ctx, store.UpdateProviderParams{ID: claudecodeconfig.ProviderID, Name: &customName, Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	writeClaudeCodeCredential(t, credentialPath, testClaudeCodePayload("access", "refresh", 4102444800000))
	detect, err := ClaudeCodeDetect(ctx, ClaudeCodeDetectRequest{ConfigDir: configDir})
	if err != nil || detect.ProviderEnabled {
		t.Fatalf("disabled Provider detect = %#v, error = %v", detect, err)
	}
	if _, err := CreateClaudeCodeProfile(ctx, CreateClaudeCodeProfileRequest{ConfigDir: configDir, ProfileID: "work"}); err != nil {
		t.Fatal(err)
	}
	provider, err := GetProvider(ctx, GetProviderRequest{ConfigDir: configDir, ID: claudecodeconfig.ProviderID})
	if err != nil || !provider.Enabled || provider.Name != claudecodeconfig.ProviderName {
		t.Fatalf("managed provider = %#v, error = %v", provider, err)
	}
	if _, err := UpdateProvider(ctx, UpdateProviderRequest{ConfigDir: configDir, ID: claudecodeconfig.ProviderID, Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	_, err = SaveActiveClaudeCodeProfile(ctx, SaveActiveClaudeCodeProfileRequest{ConfigDir: configDir})
	assertAppErrorCode(t, err, ErrorProviderDisabled)
}

func TestClaudeCodeRecoveryUsesSavedKeychainAccount(t *testing.T) {
	spec, err := (claudeCodePlanAdapter{}).ResolveTargetSpec(
		claudecodeconfig.ProviderID,
		claudecodeconfig.TargetID,
		targetBackendClaudeCodeKeychain,
		"saved-short-user",
		"Claude Code login",
	)
	if err != nil {
		t.Fatal(err)
	}
	keychainSpec, ok := spec.(claudeCodeKeychainTargetSpec)
	if !ok || keychainSpec.Service != claudecodeconfig.KeychainService || keychainSpec.Account != "saved-short-user" {
		t.Fatalf("recovery spec = %#v", spec)
	}
	if _, err := (claudeCodePlanAdapter{}).ResolveTargetSpec(
		claudecodeconfig.ProviderID,
		claudecodeconfig.TargetID,
		targetBackendClaudeCodeKeychain,
		"",
		"Claude Code login",
	); err == nil {
		t.Fatal("recovery unexpectedly accepted a missing saved Keychain account")
	}
	if _, err := (claudeCodePlanAdapter{}).ResolveTargetSpec(
		claudecodeconfig.ProviderID,
		claudecodeconfig.TargetID,
		targetBackendFile,
		filepath.Join(t.TempDir(), "other.json"),
		"Claude Code login",
	); err == nil {
		t.Fatal("recovery unexpectedly accepted a non-official credential filename")
	}
}

func TestClaudeCodeDetectRequiresExplicitKeychainAuthorization(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatal(err)
	}
	seedClaudeCodeKeychainProvider(t, ctx, configDir, "tester")
	reference := []byte("detect-reference")
	driver := &fakeClaudeCodeKeychainDriver{
		references: []claudekeychain.Reference{{Persistent: reference, Service: claudecodeconfig.KeychainService, Account: "tester"}},
		items: map[string]claudekeychain.Item{
			string(reference): {Service: claudecodeconfig.KeychainService, Account: "tester", Data: []byte(testClaudeCodePayload("access", "refresh", 4102444800000))},
		},
		requireReadInteraction: true,
	}
	previous := targetBackends[targetBackendClaudeCodeKeychain]
	targetBackends[targetBackendClaudeCodeKeychain] = claudeCodeKeychainTargetBackend{driver: driver}
	t.Cleanup(func() { targetBackends[targetBackendClaudeCodeKeychain] = previous })

	passive, err := ClaudeCodeDetect(ctx, ClaudeCodeDetectRequest{ConfigDir: configDir})
	if err != nil || !passive.KeychainAuthorizationRequired || passive.CredentialStatus != claudecodeauth.StatusUnavailable {
		t.Fatalf("passive ClaudeCodeDetect() = %#v, error = %v", passive, err)
	}
	authorized, err := ClaudeCodeDetect(ctx, ClaudeCodeDetectRequest{ConfigDir: configDir, AllowKeychainInteraction: true})
	if err != nil || authorized.KeychainAuthorizationRequired || authorized.CredentialStatus != claudecodeauth.StatusValid {
		t.Fatalf("authorized ClaudeCodeDetect() = %#v, error = %v", authorized, err)
	}
	if len(driver.findAllowInteractions) != 2 || driver.findAllowInteractions[0] || !driver.findAllowInteractions[1] || len(driver.readAllowInteractions) != 2 || driver.readAllowInteractions[0] || !driver.readAllowInteractions[1] {
		t.Fatalf("detect interaction flags: find=%#v read=%#v", driver.findAllowInteractions, driver.readAllowInteractions)
	}
}

func TestClaudeCodeDoctorDoesNotRequestKeychainAuthorizationUI(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatal(err)
	}
	seedClaudeCodeKeychainProvider(t, ctx, configDir, "tester")
	reference := []byte("doctor-reference")
	driver := &fakeClaudeCodeKeychainDriver{
		references: []claudekeychain.Reference{{Persistent: reference, Service: claudecodeconfig.KeychainService, Account: "tester"}},
		items: map[string]claudekeychain.Item{
			string(reference): {Service: claudecodeconfig.KeychainService, Account: "tester", Data: []byte(testClaudeCodePayload("access", "refresh", 4102444800000))},
		},
		requireReadInteraction: true,
	}
	previous := targetBackends[targetBackendClaudeCodeKeychain]
	targetBackends[targetBackendClaudeCodeKeychain] = claudeCodeKeychainTargetBackend{driver: driver}
	t.Cleanup(func() { targetBackends[targetBackendClaudeCodeKeychain] = previous })

	result, err := Doctor(ctx, DoctorRequest{ConfigDir: configDir})
	if err != nil || !hasDoctorFinding(result.Findings, "claude_code_keychain_authorization_required") {
		t.Fatalf("Claude Code Doctor result = %#v, error = %v", result, err)
	}
	if len(driver.findAllowInteractions) != 1 || driver.findAllowInteractions[0] || len(driver.readAllowInteractions) != 1 || driver.readAllowInteractions[0] {
		t.Fatalf("Doctor interaction flags: find=%#v read=%#v", driver.findAllowInteractions, driver.readAllowInteractions)
	}
}

func TestClaudeCodeKeychainRollbackUsesOriginalPersistentReference(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatal(err)
	}
	seedClaudeCodeKeychainProvider(t, ctx, configDir, "tester")
	reference := []byte("original-reference")
	driver := &fakeClaudeCodeKeychainDriver{
		references: []claudekeychain.Reference{{Persistent: reference, Service: claudecodeconfig.KeychainService, Account: "tester"}},
		items: map[string]claudekeychain.Item{
			string(reference): {Service: claudecodeconfig.KeychainService, Account: "tester", Data: []byte(testClaudeCodePayload("access-a", "refresh-a", 4102444800000))},
		},
	}
	previous := targetBackends[targetBackendClaudeCodeKeychain]
	targetBackends[targetBackendClaudeCodeKeychain] = claudeCodeKeychainTargetBackend{driver: driver}
	t.Cleanup(func() { targetBackends[targetBackendClaudeCodeKeychain] = previous })
	if _, err := CreateClaudeCodeProfile(ctx, CreateClaudeCodeProfileRequest{ConfigDir: configDir, ProfileID: "first"}); err != nil {
		t.Fatal(err)
	}
	item := driver.items[string(reference)]
	item.Data = []byte(testClaudeCodePayload("access-b", "refresh-b", 4102444800000))
	driver.items[string(reference)] = item
	if _, err := CreateClaudeCodeProfile(ctx, CreateClaudeCodeProfileRequest{ConfigDir: configDir, ProfileID: "second"}); err != nil {
		t.Fatal(err)
	}
	reviewedPlan, err := BuildPlan(ctx, BuildPlanRequest{ConfigDir: configDir, ProviderID: claudecodeconfig.ProviderID, ProfileID: "first"})
	if err != nil {
		t.Fatal(err)
	}
	if encoded := mustJSON(t, reviewedPlan); bytes.Contains(encoded, reference) || bytes.Contains(encoded, []byte("object_sha256")) {
		t.Fatalf("public plan leaked Keychain object identity: %s", encoded)
	}
	reviewReplacement := []byte("review-replacement-reference")
	delete(driver.items, string(reference))
	driver.references = []claudekeychain.Reference{{Persistent: reviewReplacement, Service: claudecodeconfig.KeychainService, Account: "tester"}}
	driver.items[string(reviewReplacement)] = item
	updatesBeforeReview := len(driver.updates)
	_, err = ApplySwitch(ctx, ApplySwitchRequest{
		ConfigDir: configDir, ProviderID: claudecodeconfig.ProviderID, ProfileID: "first",
		ExpectedPlanFingerprint: reviewedPlan.PlanFingerprint, Confirm: true,
	})
	assertAppErrorCode(t, err, ErrorTargetChanged)
	if len(driver.updates) != updatesBeforeReview {
		t.Fatal("reviewed plan wrote to a recreated Keychain item")
	}
	delete(driver.items, string(reviewReplacement))
	driver.references = []claudekeychain.Reference{{Persistent: reference, Service: claudecodeconfig.KeychainService, Account: "tester"}}
	driver.items[string(reference)] = item
	noOpSwitch, err := ApplySwitch(ctx, ApplySwitchRequest{ConfigDir: configDir, ProviderID: claudecodeconfig.ProviderID, ProfileID: "second", Confirm: true})
	if err != nil {
		t.Fatalf("Keychain no-op switch error = %v", err)
	}
	noOpBackup, err := ShowBackup(ctx, ShowBackupRequest{ConfigDir: configDir, BackupID: noOpSwitch.OperationID})
	if err != nil || len(noOpBackup.Entries) != 1 || noOpBackup.Entries[0].Action != planActionNoop || noOpBackup.Entries[0].Path != "" || noOpBackup.Entries[0].BeforeSHA256 != "" {
		t.Fatalf("public Keychain no-op backup = %#v, error = %v", noOpBackup, err)
	}
	if _, err := ApplyRollback(ctx, ApplyRollbackRequest{ConfigDir: configDir, BackupID: noOpSwitch.OperationID, Confirm: true}); err != nil {
		t.Fatalf("Keychain no-op rollback error = %v", err)
	}

	firstSwitch, err := ApplySwitch(ctx, ApplySwitchRequest{ConfigDir: configDir, ProviderID: claudecodeconfig.ProviderID, ProfileID: "first", Confirm: true})
	if err != nil {
		t.Fatal(err)
	}
	backup, err := ShowBackup(ctx, ShowBackupRequest{ConfigDir: configDir, BackupID: firstSwitch.OperationID})
	if err != nil || len(backup.Entries) != 1 || backup.Entries[0].Path != "" || backup.Entries[0].BeforeSHA256 != "" {
		t.Fatalf("public Keychain backup = %#v, error = %v", backup, err)
	}
	if encoded := mustJSON(t, backup); bytes.Contains(encoded, reference) || bytes.Contains(encoded, []byte("private_locator")) {
		t.Fatalf("public backup leaked persistent reference: %s", encoded)
	}
	if _, err := ApplyRollback(ctx, ApplyRollbackRequest{ConfigDir: configDir, BackupID: firstSwitch.OperationID, Confirm: true}); err != nil {
		t.Fatalf("exact-reference rollback error = %v", err)
	}
	if !strings.Contains(string(driver.items[string(reference)].Data), "access-b") {
		t.Fatal("exact-reference rollback did not restore the previous login")
	}

	secondSwitch, err := ApplySwitch(ctx, ApplySwitchRequest{ConfigDir: configDir, ProviderID: claudecodeconfig.ProviderID, ProfileID: "first", Confirm: true})
	if err != nil {
		t.Fatal(err)
	}
	replacement := []byte("replacement-reference")
	delete(driver.items, string(reference))
	driver.references = []claudekeychain.Reference{{Persistent: replacement, Service: claudecodeconfig.KeychainService, Account: "tester"}}
	driver.items[string(replacement)] = claudekeychain.Item{
		Service: claudecodeconfig.KeychainService, Account: "tester", Data: []byte(testClaudeCodePayload("access-a", "refresh-a", 4102444800000)),
	}
	updatesBefore := len(driver.updates)
	_, err = ApplyRollback(ctx, ApplyRollbackRequest{ConfigDir: configDir, BackupID: secondSwitch.OperationID, Confirm: true})
	assertAppErrorCode(t, err, ErrorTargetChanged)
	if len(driver.updates) != updatesBefore || !strings.Contains(string(driver.items[string(replacement)].Data), "access-a") {
		t.Fatal("rollback wrote to a recreated Keychain item")
	}
}

func TestClaudeCodeFailedSwitchRecoveryRejectsRecreatedKeychainItem(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	initResult, err := Init(ctx, InitRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatal(err)
	}
	seedClaudeCodeKeychainProvider(t, ctx, configDir, "tester")
	reference := []byte("recovery-original-reference")
	driver := &fakeClaudeCodeKeychainDriver{
		references: []claudekeychain.Reference{{Persistent: reference, Service: claudecodeconfig.KeychainService, Account: "tester"}},
		items: map[string]claudekeychain.Item{
			string(reference): {Service: claudecodeconfig.KeychainService, Account: "tester", Data: []byte(testClaudeCodePayload("access-a", "refresh-a", 4102444800000))},
		},
	}
	previous := targetBackends[targetBackendClaudeCodeKeychain]
	targetBackends[targetBackendClaudeCodeKeychain] = claudeCodeKeychainTargetBackend{driver: driver}
	t.Cleanup(func() { targetBackends[targetBackendClaudeCodeKeychain] = previous })
	if _, err := CreateClaudeCodeProfile(ctx, CreateClaudeCodeProfileRequest{ConfigDir: configDir, ProfileID: "first"}); err != nil {
		t.Fatal(err)
	}
	item := driver.items[string(reference)]
	item.Data = []byte(testClaudeCodePayload("access-b", "refresh-b", 4102444800000))
	driver.items[string(reference)] = item
	if _, err := CreateClaudeCodeProfile(ctx, CreateClaudeCodeProfileRequest{ConfigDir: configDir, ProfileID: "second"}); err != nil {
		t.Fatal(err)
	}

	driver.postReadErr = fmt.Errorf("post-update read failed")
	_, err = ApplySwitch(ctx, ApplySwitchRequest{ConfigDir: configDir, ProviderID: claudecodeconfig.ProviderID, ProfileID: "first", Confirm: true})
	assertAppErrorCode(t, err, ErrorTargetWriteFailed)
	driver.postReadErr = nil
	failedSwitchID := singleOperationIDByTypeStatus(t, initResult.DatabasePath, store.OperationTypeSwitch, store.OperationStatusFailed)

	replacement := []byte("recovery-replacement-reference")
	delete(driver.items, string(reference))
	driver.references = []claudekeychain.Reference{{Persistent: replacement, Service: claudecodeconfig.KeychainService, Account: "tester"}}
	driver.items[string(replacement)] = claudekeychain.Item{
		Service: claudecodeconfig.KeychainService, Account: "tester", Data: []byte(testClaudeCodePayload("access-a", "refresh-a", 4102444800000)),
	}
	updatesBefore := len(driver.updates)
	_, err = RecoverFailedSwitch(ctx, RecoverFailedSwitchParams{ConfigDir: configDir, OperationID: failedSwitchID, Confirm: true})
	assertAppErrorCode(t, err, ErrorTargetChanged)
	if len(driver.updates) != updatesBefore || !strings.Contains(string(driver.items[string(replacement)].Data), "access-a") {
		t.Fatal("failed-switch recovery wrote to a recreated Keychain item")
	}
}

func TestClaudeCodeReplacementProbeUsesAtomicRenameAndCleansUp(t *testing.T) {
	directory := t.TempDir()
	if !claudeCodeFileReplacementAvailable(filepath.Join(directory, claudecodeconfig.CredentialsFile)) {
		t.Fatal("replacement probe unexpectedly failed in a writable directory")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("replacement probe left temporary files: %#v", entries)
	}
}

func seedClaudeCodeFileProvider(t *testing.T, ctx context.Context, configDir, credentialPath string) {
	t.Helper()
	db, err := openHealthyStore(ctx, configDir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	metadata := newClaudeCodeProviderMetadata(claudecodeconfig.Locator{Storage: claudecodeconfig.StorageFile, Path: credentialPath})
	raw, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateProvider(ctx, store.CreateProviderParams{
		ID: claudecodeconfig.ProviderID, Name: claudecodeconfig.ProviderName, AdapterID: claudecodeconfig.AdapterID,
		Enabled: true, MetadataJSON: string(raw),
	}); err != nil {
		t.Fatal(err)
	}
}

func seedClaudeCodeKeychainProvider(t *testing.T, ctx context.Context, configDir, account string) {
	t.Helper()
	db, err := openHealthyStore(ctx, configDir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	metadata := newClaudeCodeProviderMetadata(claudecodeconfig.Locator{
		Storage: claudecodeconfig.StorageKeychain, Service: claudecodeconfig.KeychainService, Account: account,
	})
	raw, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateProvider(ctx, store.CreateProviderParams{
		ID: claudecodeconfig.ProviderID, Name: claudecodeconfig.ProviderName, AdapterID: claudecodeconfig.AdapterID,
		Enabled: true, MetadataJSON: string(raw),
	}); err != nil {
		t.Fatal(err)
	}
}

func writeClaudeCodeCredential(t *testing.T, path, payload string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
}

func testClaudeCodePayload(access, refresh string, expiresAt int64) string {
	return `{"claudeAiOauth":{"accessToken":"` + access + `","refreshToken":"` + refresh + `","subscriptionType":"max","expiresAt":` + fmt.Sprint(expiresAt) + `},"unknown":{"kept":true}}`
}

func assertClaudeCodeWorkingPayload(t *testing.T, path, access string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"accessToken":"`+access+`"`) {
		t.Fatalf("working copy does not contain expected login")
	}
}

func claudeCodeCredentialForProfile(t *testing.T, ctx context.Context, configDir, profileID string) store.ProviderCredential {
	t.Helper()
	db, err := openHealthyStore(ctx, configDir, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	binding, err := db.GetProfileCredentialBinding(ctx, profileID, claudecodeconfig.ProviderID, claudecodeconfig.CredentialSlot)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := db.GetProviderCredential(ctx, binding.CredentialID)
	if err != nil {
		t.Fatal(err)
	}
	return credential
}
