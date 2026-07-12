package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	"github.com/strahe/profiledeck/internal/store"
)

func TestCodexProfileExportImportRoundTripIsDeterministicAndInactive(t *testing.T) {
	ctx := context.Background()
	sourceConfigDir := t.TempDir()
	codexDir := t.TempDir()
	writeCodexTransferFixture(t, codexDir, "model = \"gpt-test\"\n", `{"tokens":{"account_id":"shared-display-account"},"access_token":"test-value"}`)
	if _, err := Init(ctx, InitRequest{ConfigDir: sourceConfigDir}); err != nil {
		t.Fatalf("expected source init to succeed, got %v", err)
	}
	if _, err := CreateCodexProfile(ctx, CreateCodexProfileRequest{ConfigDir: sourceConfigDir, CodexDir: codexDir, ProfileID: "work"}); err != nil {
		t.Fatalf("expected first profile create to succeed, got %v", err)
	}
	// The same account metadata still creates an independent opaque credential.
	if _, err := CreateCodexProfile(ctx, CreateCodexProfileRequest{ConfigDir: sourceConfigDir, CodexDir: codexDir, ProfileID: "personal"}); err != nil {
		t.Fatalf("expected second profile create to succeed, got %v", err)
	}
	sourceDB, err := openHealthyStore(ctx, sourceConfigDir, false)
	if err != nil {
		t.Fatalf("expected source settings store, got %v", err)
	}
	if _, err := sourceDB.UpsertProviderProfileSetting(ctx, store.UpsertProviderProfileSettingParams{
		ProfileID: "work", ProviderID: codexconfig.ProviderID,
		QuotaRefreshIntervalSeconds: 300, AuthKeepaliveEnabled: true,
	}); err != nil {
		_ = sourceDB.Close()
		t.Fatalf("expected local automation fixture, got %v", err)
	}
	if err := sourceDB.Close(); err != nil {
		t.Fatalf("expected source settings store close, got %v", err)
	}
	unboundConfig := "model = \"gpt-unbound\"\n"
	if _, err := CreateCodexConfigSet(ctx, CreateCodexConfigSetRequest{
		ConfigDir: sourceConfigDir, CodexDir: codexDir, ConfigSetID: "unbound", Name: "Unbound", ConfigContent: &unboundConfig,
	}); err != nil {
		t.Fatalf("expected unbound Config Set create to succeed, got %v", err)
	}

	exportDir := t.TempDir()
	firstPath := filepath.Join(exportDir, "profiles-a.json")
	first, err := ExportCodexProfiles(ctx, ExportCodexProfilesRequest{ConfigDir: sourceConfigDir, OutputPath: firstPath})
	if err != nil {
		t.Fatalf("expected export to succeed, got %v", err)
	}
	if first.ProfileCount != 2 || first.CredentialCount != 2 || first.ConfigSetCount != 2 {
		t.Fatalf("expected full export to retain profiles, opaque credentials, and unbound Config Sets, got %#v", first)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(firstPath)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("expected sensitive export mode 0600, got %o", info.Mode().Perm())
		}
	}
	secondPath := filepath.Join(exportDir, "profiles-b.json")
	second, err := ExportCodexProfiles(ctx, ExportCodexProfilesRequest{ConfigDir: sourceConfigDir, OutputPath: secondPath})
	if err != nil {
		t.Fatalf("expected repeated export to succeed, got %v", err)
	}
	firstRaw, _ := os.ReadFile(firstPath)
	secondRaw, _ := os.ReadFile(secondPath)
	if string(firstRaw) != string(secondRaw) || first.SHA256 != second.SHA256 {
		t.Fatalf("expected unchanged exports to be byte-identical")
	}
	if strings.Contains(string(firstRaw), "quota_refresh_interval_seconds") || strings.Contains(string(firstRaw), "auth_keepalive_enabled") {
		t.Fatal("expected local automation settings to stay out of sensitive Profile export")
	}
	selected, err := ExportCodexProfiles(ctx, ExportCodexProfilesRequest{
		ConfigDir: sourceConfigDir, ProfileIDs: []string{"work"}, OutputPath: filepath.Join(exportDir, "work.json"),
	})
	if err != nil {
		t.Fatalf("expected selected export to succeed, got %v", err)
	}
	if selected.ProfileCount != 1 || selected.CredentialCount != 1 || selected.ConfigSetCount != 1 {
		t.Fatalf("expected selected export to contain only its dependency closure, got %#v", selected)
	}

	configBefore, _ := os.ReadFile(filepath.Join(codexDir, codexconfig.ConfigFileName))
	authBefore, _ := os.ReadFile(filepath.Join(codexDir, codexconfig.AuthFileName))
	targetConfigDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: targetConfigDir}); err != nil {
		t.Fatalf("expected target init to succeed, got %v", err)
	}
	plan, err := InspectCodexProfileImport(ctx, InspectCodexProfileImportRequest{ConfigDir: targetConfigDir, CodexDir: codexDir, InputPath: firstPath})
	if err != nil {
		t.Fatalf("expected import inspection to succeed, got %v", err)
	}
	if !plan.CanApply || plan.Counts.Conflict != 0 || plan.PlanFingerprint == "" {
		t.Fatalf("expected clean applicable import plan, got %#v", plan)
	}
	result, err := ImportCodexProfiles(ctx, ImportCodexProfilesRequest{
		ConfigDir: targetConfigDir, CodexDir: codexDir, InputPath: firstPath,
		ExpectedPlanFingerprint: plan.PlanFingerprint, Confirm: true,
	})
	if err != nil {
		t.Fatalf("expected import apply to succeed, got %v", err)
	}
	if !result.Changed || result.OperationID == "" {
		t.Fatalf("expected changed import with operation record, got %#v", result)
	}
	profiles, err := ListCodexProfiles(ctx, ListCodexProfilesRequest{ConfigDir: targetConfigDir})
	if err != nil || len(profiles.Profiles) != 2 {
		t.Fatalf("expected two imported profiles, result=%#v err=%v", profiles, err)
	}
	if profiles.Profiles[0].CredentialID == profiles.Profiles[1].CredentialID {
		t.Fatalf("expected duplicate display account ids to retain distinct opaque credentials")
	}
	importedSettings, err := GetCodexSettings(ctx, CodexSettingsRequest{ConfigDir: targetConfigDir})
	if err != nil || len(importedSettings.Profiles) != 2 {
		t.Fatalf("expected imported Profile settings, settings=%#v err=%v", importedSettings, err)
	}
	for _, profileSettings := range importedSettings.Profiles {
		if profileSettings.QuotaRefreshIntervalSeconds != 0 || profileSettings.AuthKeepaliveEnabled {
			t.Fatalf("expected imported automation disabled, got %#v", profileSettings)
		}
	}
	configSets, err := ListCodexConfigSets(ctx, ListCodexConfigSetsRequest{ConfigDir: targetConfigDir})
	if err != nil || len(configSets.ConfigSets) != 2 {
		t.Fatalf("expected bound and unbound Config Sets, result=%#v err=%v", configSets, err)
	}
	db, err := openHealthyStore(ctx, targetConfigDir, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.GetActiveState(ctx, store.ActiveStateScopeProvider, codexconfig.ProviderID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected import not to restore active state, got %v", err)
	}
	operation, err := db.GetOperation(ctx, result.OperationID)
	if err != nil || operation.OperationType != store.OperationTypeImport || operation.Status != store.OperationStatusApplied {
		t.Fatalf("expected applied import operation, operation=%#v err=%v", operation, err)
	}
	configAfter, _ := os.ReadFile(filepath.Join(codexDir, codexconfig.ConfigFileName))
	authAfter, _ := os.ReadFile(filepath.Join(codexDir, codexconfig.AuthFileName))
	if string(configAfter) != string(configBefore) || string(authAfter) != string(authBefore) {
		t.Fatalf("expected import not to write Codex working files")
	}

	noChangePlan, err := InspectCodexProfileImport(ctx, InspectCodexProfileImportRequest{ConfigDir: targetConfigDir, CodexDir: codexDir, InputPath: firstPath})
	if err != nil {
		t.Fatalf("expected repeat inspection to succeed, got %v", err)
	}
	if !noChangePlan.NoChanges || noChangePlan.CanApply || noChangePlan.Counts.Conflict != 0 {
		t.Fatalf("expected repeat import to be an unchanged no-op, got %#v", noChangePlan)
	}
	noChange, err := ImportCodexProfiles(ctx, ImportCodexProfilesRequest{
		ConfigDir: targetConfigDir, CodexDir: codexDir, InputPath: firstPath,
		ExpectedPlanFingerprint: noChangePlan.PlanFingerprint, Confirm: true,
	})
	if err != nil || noChange.Changed || noChange.OperationID != "" {
		t.Fatalf("expected repeat apply to remain a no-op, result=%#v err=%v", noChange, err)
	}
}

func TestCodexProfileImportRejectsConflictsWithoutPartialWrites(t *testing.T) {
	ctx := context.Background()
	sourceConfigDir := t.TempDir()
	codexDir := t.TempDir()
	writeCodexTransferFixture(t, codexDir, "model = \"source\"\n", `{"tokens":{"account_id":"source-account"}}`)
	if _, err := Init(ctx, InitRequest{ConfigDir: sourceConfigDir}); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateCodexProfile(ctx, CreateCodexProfileRequest{ConfigDir: sourceConfigDir, CodexDir: codexDir, ProfileID: "work"}); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateCodexProfile(ctx, CreateCodexProfileRequest{ConfigDir: sourceConfigDir, CodexDir: codexDir, ProfileID: "extra"}); err != nil {
		t.Fatal(err)
	}
	bundlePath := filepath.Join(t.TempDir(), "profiles.json")
	if _, err := ExportCodexProfiles(ctx, ExportCodexProfilesRequest{ConfigDir: sourceConfigDir, OutputPath: bundlePath}); err != nil {
		t.Fatal(err)
	}

	targetConfigDir := t.TempDir()
	writeCodexTransferFixture(t, codexDir, "model = \"target\"\n", `{"tokens":{"account_id":"target-account"}}`)
	if _, err := Init(ctx, InitRequest{ConfigDir: targetConfigDir}); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateCodexProfile(ctx, CreateCodexProfileRequest{ConfigDir: targetConfigDir, CodexDir: codexDir, ProfileID: "work"}); err != nil {
		t.Fatal(err)
	}
	plan, err := InspectCodexProfileImport(ctx, InspectCodexProfileImportRequest{ConfigDir: targetConfigDir, CodexDir: codexDir, InputPath: bundlePath})
	if err != nil {
		t.Fatalf("expected conflicts to be reported as a plan, got %v", err)
	}
	if plan.Counts.Conflict == 0 || plan.CanApply {
		t.Fatalf("expected conflicting import plan, got %#v", plan)
	}
	_, err = ImportCodexProfiles(ctx, ImportCodexProfilesRequest{
		ConfigDir: targetConfigDir, CodexDir: codexDir, InputPath: bundlePath,
		ExpectedPlanFingerprint: plan.PlanFingerprint, Confirm: true,
	})
	if !isAppErrorCode(err, ErrorImportConflict) {
		t.Fatalf("expected import conflict, got %v", err)
	}
	if _, err := GetCodexProfile(ctx, GetCodexProfileRequest{ConfigDir: targetConfigDir, ProfileID: "extra"}); !isAppErrorCode(err, ErrorProfileNotFound) {
		t.Fatalf("expected conflicting import not to create non-conflicting rows, got %v", err)
	}
}

func TestCodexProfileImportAttachesToExistingGlobalProfile(t *testing.T) {
	ctx := context.Background()
	sourceConfigDir := t.TempDir()
	codexDir := t.TempDir()
	writeCodexTransferFixture(t, codexDir, "model = \"source\"\n", `{"tokens":{"account_id":"source-account"}}`)
	if _, err := Init(ctx, InitRequest{ConfigDir: sourceConfigDir}); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateCodexProfile(ctx, CreateCodexProfileRequest{ConfigDir: sourceConfigDir, CodexDir: codexDir, ProfileID: "shared"}); err != nil {
		t.Fatal(err)
	}
	bundlePath := filepath.Join(t.TempDir(), "profiles.json")
	if _, err := ExportCodexProfiles(ctx, ExportCodexProfilesRequest{
		ConfigDir: sourceConfigDir, ProfileIDs: []string{"shared"}, OutputPath: bundlePath,
	}); err != nil {
		t.Fatal(err)
	}

	targetConfigDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: targetConfigDir}); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateProfile(ctx, CreateProfileRequest{
		ConfigDir: targetConfigDir, ID: "shared", Name: "Existing global name", Description: "Keep this metadata",
	}); err != nil {
		t.Fatal(err)
	}
	plan, err := InspectCodexProfileImport(ctx, InspectCodexProfileImportRequest{
		ConfigDir: targetConfigDir, CodexDir: codexDir, InputPath: bundlePath,
	})
	if err != nil || !plan.CanApply || plan.Counts.Conflict != 0 {
		t.Fatalf("expected attachable import plan, got %#v err=%v", plan, err)
	}
	result, err := ImportCodexProfiles(ctx, ImportCodexProfilesRequest{
		ConfigDir: targetConfigDir, CodexDir: codexDir, InputPath: bundlePath,
		ExpectedPlanFingerprint: plan.PlanFingerprint, Confirm: true,
	})
	if err != nil || !result.Changed {
		t.Fatalf("expected import attach to succeed, got %#v err=%v", result, err)
	}
	detail, err := GetCodexProfile(ctx, GetCodexProfileRequest{ConfigDir: targetConfigDir, ProfileID: "shared"})
	if err != nil {
		t.Fatalf("expected attached Codex Profile, got %v", err)
	}
	if detail.Summary.Profile.Name != "Existing global name" || detail.Summary.Profile.Description != "Keep this metadata" {
		t.Fatalf("expected global Profile metadata to be preserved, got %#v", detail.Summary.Profile)
	}
	repeat, err := InspectCodexProfileImport(ctx, InspectCodexProfileImportRequest{
		ConfigDir: targetConfigDir, CodexDir: codexDir, InputPath: bundlePath,
	})
	if err != nil || !repeat.NoChanges || repeat.Counts.Conflict != 0 {
		t.Fatalf("expected preserved global metadata not to create a repeat conflict, got %#v err=%v", repeat, err)
	}
}

func TestCodexProfileImportRejectsChangedPlan(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	writeCodexTransferFixture(t, codexDir, "model = \"gpt-test\"\n", `{"tokens":{"account_id":"test-account"}}`)
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateCodexProfile(ctx, CreateCodexProfileRequest{ConfigDir: configDir, CodexDir: codexDir, ProfileID: "work"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "profiles.json")
	if _, err := ExportCodexProfiles(ctx, ExportCodexProfilesRequest{ConfigDir: configDir, OutputPath: path}); err != nil {
		t.Fatal(err)
	}
	targetConfigDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: targetConfigDir}); err != nil {
		t.Fatal(err)
	}
	_, err := ImportCodexProfiles(ctx, ImportCodexProfilesRequest{
		ConfigDir: targetConfigDir, CodexDir: codexDir, InputPath: path,
		ExpectedPlanFingerprint: "stale-plan", Confirm: true,
	})
	if !isAppErrorCode(err, ErrorImportPlanChanged) {
		t.Fatalf("expected changed import plan rejection, got %v", err)
	}
}

func TestCodexProfileExportCannotReplaceRuntimeOrWorkingFiles(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	writeCodexTransferFixture(t, codexDir, "model = \"gpt-test\"\n", `{"tokens":{"account_id":"test-account"}}`)
	initialized, err := Init(ctx, InitRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreateCodexProfile(ctx, CreateCodexProfileRequest{ConfigDir: configDir, CodexDir: codexDir, ProfileID: "work"}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{initialized.DatabasePath, filepath.Join(codexDir, codexconfig.AuthFileName), filepath.Join(codexDir, codexconfig.ConfigFileName)} {
		if _, err := ExportCodexProfiles(ctx, ExportCodexProfilesRequest{ConfigDir: configDir, OutputPath: path, Overwrite: true}); !isAppErrorCode(err, ErrorExportFailed) {
			t.Fatalf("expected protected export path %s to be rejected, got %v", path, err)
		}
	}
	if _, err := GetCodexProfile(ctx, GetCodexProfileRequest{ConfigDir: configDir, ProfileID: "work"}); err != nil {
		t.Fatalf("expected rejected exports to preserve ProfileDeck state, got %v", err)
	}
}

func writeCodexTransferFixture(t *testing.T, codexDir, config, auth string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(codexDir, codexconfig.ConfigFileName), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, codexconfig.AuthFileName), []byte(auth), 0o600); err != nil {
		t.Fatal(err)
	}
}

func isAppErrorCode(err error, code ErrorCode) bool {
	var appErr *AppError
	return errors.As(err, &appErr) && appErr.Code == code
}
