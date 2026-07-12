package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	codexpreset "github.com/strahe/profiledeck/internal/codex/preset"
	"github.com/strahe/profiledeck/internal/store"
)

func TestCodexDetectBeforeInitIsReadOnly(t *testing.T) {
	ctx := context.Background()
	configDir := filepath.Join(t.TempDir(), "profiledeck")
	codexDir := t.TempDir()

	result, err := CodexDetect(ctx, CodexDetectRequest{
		ConfigDir: configDir,
		CodexDir:  codexDir,
	})
	if err != nil {
		t.Fatalf("expected Codex detect before init to succeed, got %v", err)
	}
	if result.ProviderID != codexconfig.ProviderID || result.AdapterID != codexconfig.AdapterID {
		t.Fatalf("unexpected provider identity: %#v", result)
	}
	if !result.CodexDirExists || result.ConfigStatus != "missing" {
		t.Fatalf("unexpected Codex config status: %#v", result)
	}
	if result.ProfileDeckInitialized || result.ProviderExists {
		t.Fatalf("expected detect before init to report no ProfileDeck state, got %#v", result)
	}
	if _, err := os.Stat(configDir); !os.IsNotExist(err) {
		t.Fatalf("expected detect not to create ProfileDeck config dir, stat error: %v", err)
	}
}

func TestCodexDetectReportsPresetHomeConflict(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDirA := t.TempDir()
	codexDirB := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	writeCodexProfileFixture(t, codexDirA, `model = "gpt-5-codex"`+"\n", `{"tokens":{"account_id":"remote-work","access_token":"secret"}}`)
	if _, err := CreateCodexProfile(ctx, CreateCodexProfileRequest{
		ConfigDir: configDir,
		CodexDir:  codexDirA,
		ProfileID: "work",
	}); err != nil {
		t.Fatalf("expected Codex profile create to succeed, got %v", err)
	}

	result, err := CodexDetect(ctx, CodexDetectRequest{
		ConfigDir: configDir,
		CodexDir:  codexDirB,
	})
	if err != nil {
		t.Fatalf("expected Codex detect to succeed, got %v", err)
	}
	if !result.ProviderExists || result.ProviderCompatible {
		t.Fatalf("expected detect to report incompatible existing provider, got %#v", result)
	}
	if !hasWarning(result.Warnings, "different Codex home") || !hasWarning(result.Warnings, "different config path") {
		t.Fatalf("expected home and config path warnings, got %#v", result.Warnings)
	}
}

func TestCodexProfileCreateRejectsInvalidProviderMetadata(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	writeCodexProfileFixture(t, codexDir, `model = "gpt-5-codex"`+"\n", `{"tokens":{"account_id":"remote-work","access_token":"secret"}}`)
	if _, err := CreateCodexProfile(ctx, CreateCodexProfileRequest{
		ConfigDir: configDir,
		CodexDir:  codexDir,
		ProfileID: "work",
	}); err != nil {
		t.Fatalf("expected Codex profile create to succeed, got %v", err)
	}

	db, err := openHealthyStore(ctx, configDir, false)
	if err != nil {
		t.Fatalf("expected healthy store to open, got %v", err)
	}
	badMetadata := "{"
	if _, err := db.UpdateProvider(ctx, store.UpdateProviderParams{
		ID:           codexconfig.ProviderID,
		MetadataJSON: &badMetadata,
	}); err != nil {
		_ = db.Close()
		t.Fatalf("expected provider metadata mutation setup to succeed, got %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("expected store close to succeed, got %v", err)
	}

	result, err := CodexDetect(ctx, CodexDetectRequest{
		ConfigDir: configDir,
		CodexDir:  codexDir,
	})
	if err != nil {
		t.Fatalf("expected Codex detect to report invalid metadata as a warning, got %v", err)
	}
	if result.ProviderCompatible || !hasWarning(result.Warnings, "metadata is invalid") {
		t.Fatalf("expected invalid metadata warning and incompatible provider, got %#v", result)
	}

	_, err = CreateCodexProfile(ctx, CreateCodexProfileRequest{
		ConfigDir: configDir,
		CodexDir:  codexDir,
		ProfileID: "work",
	})
	assertAppErrorCode(t, err, ErrorStoreSchemaInvalid)
	_, err = ExportCodexProfiles(ctx, ExportCodexProfilesRequest{
		ConfigDir: configDir, OutputPath: filepath.Join(t.TempDir(), "profiles.json"),
	})
	assertAppErrorCode(t, err, ErrorExportFailed)
}

func TestCodexPlanIgnoresGenericTargetPath(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	writeCodexProfileFixture(t, codexDir, `model = "gpt-5-codex"`+"\n", `{"tokens":{"account_id":"remote-work","access_token":"secret"}}`)
	if _, err := CreateCodexProfile(ctx, CreateCodexProfileRequest{
		ConfigDir: configDir,
		CodexDir:  codexDir,
		ProfileID: "work",
	}); err != nil {
		t.Fatalf("expected Codex profile create to succeed, got %v", err)
	}

	mutatedPath := filepath.Join(t.TempDir(), "other.toml")
	db, err := openHealthyStore(ctx, configDir, false)
	if err != nil {
		t.Fatalf("expected store open to succeed, got %v", err)
	}
	if _, err := db.CreateProfileTarget(ctx, store.CreateProfileTargetParams{
		ProfileID: "work", ProviderID: codexconfig.ProviderID, TargetID: codexconfig.TargetID,
		Path: mutatedPath, PathKey: targetPathOwnershipKey(mutatedPath), Format: targetFormatTOML,
		Strategy: targetStrategyReplaceFile, ValueJSON: `{}`, Enabled: true, MetadataJSON: `{}`,
	}); err != nil {
		_ = db.Close()
		t.Fatalf("expected target mutation setup to succeed, got %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("expected store close to succeed, got %v", err)
	}

	plan, err := BuildPlan(ctx, BuildPlanRequest{
		ConfigDir:  configDir,
		ProviderID: codexconfig.ProviderID,
		ProfileID:  "work",
	})
	if err != nil {
		t.Fatalf("expected typed Codex bindings to ignore generic target path, got %v", err)
	}
	for _, operation := range plan.Operations {
		if operation.Path == mutatedPath {
			t.Fatalf("expected generic target path not to enter Codex plan: %#v", plan.Operations)
		}
	}
}

func TestCodexPlanRejectsUnsupportedTypedSlot(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	writeCodexProfileFixture(t, codexDir, `model = "gpt-5-codex"`+"\n", `{"tokens":{"account_id":"remote-work","access_token":"secret"}}`)
	if _, err := CreateCodexProfile(ctx, CreateCodexProfileRequest{
		ConfigDir: configDir, CodexDir: codexDir, ProfileID: "work",
	}); err != nil {
		t.Fatalf("expected Codex profile create to succeed, got %v", err)
	}
	db, err := openHealthyStore(ctx, configDir, false)
	if err != nil {
		t.Fatalf("expected store open to succeed, got %v", err)
	}
	authBinding, err := db.GetProfileCredentialBinding(ctx, "work", codexconfig.ProviderID, codexpreset.CredentialSlotAuth)
	if err != nil {
		_ = db.Close()
		t.Fatalf("expected auth binding, got %v", err)
	}
	if _, err := db.UpsertProfileCredentialBinding(ctx, store.UpsertProfileCredentialBindingParams{
		ProfileID: "work", ProviderID: codexconfig.ProviderID,
		SlotID: "unsupported", CredentialID: authBinding.CredentialID,
	}); err != nil {
		_ = db.Close()
		t.Fatalf("expected unsupported binding fixture, got %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("expected store close, got %v", err)
	}
	_, err = BuildPlan(ctx, BuildPlanRequest{
		ConfigDir: configDir, ProviderID: codexconfig.ProviderID, ProfileID: "work",
	})
	assertAppErrorCode(t, err, ErrorCodexInvalid)
}

func TestCodexProfileCreateCanAttachToExistingGlobalProfile(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	if _, err := CreateProfile(ctx, CreateProfileRequest{
		ConfigDir: configDir, ID: "shared", Name: "Shared Profile", Description: "Used by multiple Agents",
	}); err != nil {
		t.Fatalf("expected global Profile create to succeed, got %v", err)
	}
	writeCodexProfileFixture(t, codexDir, "model = \"gpt-5\"\n", `{"tokens":{"account_id":"display","access_token":"token"}}`)
	result, err := CreateCodexProfile(ctx, CreateCodexProfileRequest{
		ConfigDir: configDir, CodexDir: codexDir, ProfileID: "shared",
	})
	if err != nil {
		t.Fatalf("expected Codex bindings to attach, got %v", err)
	}
	if result.Profile.Name != "Shared Profile" || result.Profile.Description != "Used by multiple Agents" {
		t.Fatalf("expected existing global Profile metadata to remain unchanged, got %#v", result.Profile)
	}
	_, err = CreateCodexProfile(ctx, CreateCodexProfileRequest{
		ConfigDir: configDir, CodexDir: codexDir, ProfileID: "shared",
	})
	assertAppErrorCode(t, err, ErrorProfileAlreadyExists)
}

func TestCodexProfileCreateRejectsConflictingProviderAndHome(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDirA := t.TempDir()
	codexDirB := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	if _, err := CreateProvider(ctx, CreateProviderRequest{
		ConfigDir: configDir,
		ID:        codexconfig.ProviderID,
		Name:      "Codex",
		AdapterID: "generic",
	}); err != nil {
		t.Fatalf("expected conflicting provider setup to succeed, got %v", err)
	}
	writeCodexProfileFixture(t, codexDirA, `model = "gpt-5-codex"`+"\n", `{"tokens":{"account_id":"remote-work","access_token":"secret"}}`)
	_, err := CreateCodexProfile(ctx, CreateCodexProfileRequest{
		ConfigDir: configDir,
		CodexDir:  codexDirA,
		ProfileID: "work",
	})
	assertAppErrorCode(t, err, ErrorCodexInvalid)

	configDir = t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected second init to succeed, got %v", err)
	}
	if _, err := CreateCodexProfile(ctx, CreateCodexProfileRequest{
		ConfigDir: configDir,
		CodexDir:  codexDirA,
		ProfileID: "work",
	}); err != nil {
		t.Fatalf("expected first Codex home create to succeed, got %v", err)
	}
	writeCodexProfileFixture(t, codexDirB, `model = "gpt-5-codex"`+"\n", `{"tokens":{"account_id":"remote-personal","access_token":"secret"}}`)
	_, err = CreateCodexProfile(ctx, CreateCodexProfileRequest{
		ConfigDir: configDir,
		CodexDir:  codexDirB,
		ProfileID: "personal",
	})
	assertAppErrorCode(t, err, ErrorCodexInvalid)
}

func TestCodexGenericManagedTargetIsNotAProfileBinding(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	home, err := codexconfig.ResolveHome(codexDir)
	if err != nil {
		t.Fatalf("expected Codex home to resolve, got %v", err)
	}
	providerMetadata, err := codexpreset.ProviderMetadataJSON(home)
	if err != nil {
		t.Fatalf("expected provider metadata to encode, got %v", err)
	}
	db, err := openHealthyStore(ctx, configDir, false)
	if err != nil {
		t.Fatalf("expected writable store to open, got %v", err)
	}
	if _, err := db.CreateProvider(ctx, store.CreateProviderParams{
		ID:           codexconfig.ProviderID,
		Name:         codexpreset.ProviderName,
		AdapterID:    codexconfig.AdapterID,
		Enabled:      true,
		MetadataJSON: providerMetadata,
	}); err != nil {
		t.Fatalf("expected provider setup to succeed, got %v", err)
	}
	if _, err := db.CreateProfile(ctx, store.CreateProfileParams{
		ID:           "legacy",
		Name:         "Legacy",
		MetadataJSON: "{}",
	}); err != nil {
		t.Fatalf("expected legacy profile setup to succeed, got %v", err)
	}
	legacyMetadata := `{"preset":"codex","preset_version":1,"target_kind":"config","mode":"managed-keys","managed_keys":["model","model_provider","openai_base_url"]}`
	if _, err := db.CreateProfileTarget(ctx, store.CreateProfileTargetParams{
		ProfileID:    "legacy",
		ProviderID:   codexconfig.ProviderID,
		TargetID:     codexconfig.TargetID,
		Path:         home.ConfigPath,
		PathKey:      targetPathOwnershipKey(home.ConfigPath),
		Format:       targetFormatTOML,
		Strategy:     targetStrategyTOMLMerge,
		ValueJSON:    `{"model":"gpt-5-codex","model_provider":"openai"}`,
		Enabled:      true,
		MetadataJSON: legacyMetadata,
	}); err != nil {
		t.Fatalf("expected legacy target setup to succeed, got %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("expected store close to succeed, got %v", err)
	}

	list, err := ListCodexProfiles(ctx, ListCodexProfilesRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected profile list to succeed, got %v", err)
	}
	if len(list.Profiles) != 0 {
		t.Fatalf("expected generic targets not to create Codex profiles, got %#v", list.Profiles)
	}

	_, err = BuildPlan(ctx, BuildPlanRequest{
		ConfigDir:  configDir,
		ProviderID: codexconfig.ProviderID,
		ProfileID:  "legacy",
	})
	assertAppErrorCode(t, err, ErrorCodexInvalid)
}

func TestCodexGenericAccountRefTargetIsNotAProfileBinding(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	home, err := codexconfig.ResolveHome(codexDir)
	if err != nil {
		t.Fatalf("expected Codex home to resolve, got %v", err)
	}
	providerMetadata, err := codexpreset.ProviderMetadataJSON(home)
	if err != nil {
		t.Fatalf("expected provider metadata to encode, got %v", err)
	}
	configMetadataRaw, err := json.Marshal(codexpreset.TargetMetadata{
		Preset: codexconfig.PresetName, PresetVersion: codexconfig.PresetVersion,
		TargetKind: codexconfig.TargetID, Mode: "full-file",
	})
	if err != nil {
		t.Fatalf("expected config metadata to encode, got %v", err)
	}
	configValueRaw, err := json.Marshal(map[string]string{"content": `model = "gpt-5-codex"` + "\n"})
	if err != nil {
		t.Fatalf("expected config value to encode, got %v", err)
	}
	legacyAuthMetadataRaw, err := json.Marshal(codexpreset.TargetMetadata{
		Preset: codexconfig.PresetName, PresetVersion: codexconfig.PresetVersion,
		TargetKind: codexconfig.AuthTargetID, Mode: "full-file",
	})
	if err != nil {
		t.Fatalf("expected legacy auth metadata to encode, got %v", err)
	}
	configMetadata := string(configMetadataRaw)
	configValue := string(configValueRaw)
	legacyAuthMetadata := string(legacyAuthMetadataRaw)
	db, err := openHealthyStore(ctx, configDir, false)
	if err != nil {
		t.Fatalf("expected writable store to open, got %v", err)
	}
	if _, err := db.CreateProvider(ctx, store.CreateProviderParams{
		ID:           codexconfig.ProviderID,
		Name:         codexpreset.ProviderName,
		AdapterID:    codexconfig.AdapterID,
		Enabled:      true,
		MetadataJSON: providerMetadata,
	}); err != nil {
		t.Fatalf("expected provider setup to succeed, got %v", err)
	}
	if _, err := db.CreateProfile(ctx, store.CreateProfileParams{
		ID:           "legacy-auth",
		Name:         "Legacy Auth",
		MetadataJSON: "{}",
	}); err != nil {
		t.Fatalf("expected legacy profile setup to succeed, got %v", err)
	}
	if _, err := db.CreateProfileTarget(ctx, store.CreateProfileTargetParams{
		ProfileID:    "legacy-auth",
		ProviderID:   codexconfig.ProviderID,
		TargetID:     codexconfig.TargetID,
		Path:         home.ConfigPath,
		PathKey:      targetPathOwnershipKey(home.ConfigPath),
		Format:       targetFormatTOML,
		Strategy:     targetStrategyReplaceFile,
		ValueJSON:    configValue,
		Enabled:      true,
		MetadataJSON: configMetadata,
	}); err != nil {
		t.Fatalf("expected config target setup to succeed, got %v", err)
	}
	if _, err := db.CreateProfileTarget(ctx, store.CreateProfileTargetParams{
		ProfileID:    "legacy-auth",
		ProviderID:   codexconfig.ProviderID,
		TargetID:     codexconfig.AuthTargetID,
		Path:         home.AuthPath,
		PathKey:      targetPathOwnershipKey(home.AuthPath),
		Format:       targetFormatJSON,
		Strategy:     targetStrategyReplaceFile,
		ValueJSON:    `{"account_id":"local-work"}`,
		Enabled:      true,
		MetadataJSON: legacyAuthMetadata,
	}); err != nil {
		t.Fatalf("expected legacy auth target setup to succeed, got %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("expected store close to succeed, got %v", err)
	}

	list, err := ListCodexProfiles(ctx, ListCodexProfilesRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected profile list to succeed, got %v", err)
	}
	if len(list.Profiles) != 0 {
		t.Fatalf("expected generic targets not to create Codex profiles, got %#v", list.Profiles)
	}

	_, err = BuildPlan(ctx, BuildPlanRequest{
		ConfigDir:  configDir,
		ProviderID: codexconfig.ProviderID,
		ProfileID:  "legacy-auth",
	})
	assertAppErrorCode(t, err, ErrorCodexInvalid)
}

func TestCodexProfileCreateSwitchesFullConfigAndAuthThroughPipeline(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	configPath := filepath.Join(codexDir, codexconfig.ConfigFileName)
	authPath := filepath.Join(codexDir, codexconfig.AuthFileName)
	sessionPath := filepath.Join(codexDir, "sessions", "session.jsonl")

	desiredConfig := strings.Join([]string{
		`model = "gpt-5.3-codex"`,
		`approval_policy = "never"`,
		`api_key = "desired-config-secret"`,
		``,
		`[tools]`,
		`web_search = true`,
		``,
	}, "\n")
	desiredAuth := `{"tokens":{"account_id":"work-account","access_token":"desired-secret","refresh_token":"desired-refresh"}}`
	if err := os.WriteFile(configPath, []byte(desiredConfig), 0o600); err != nil {
		t.Fatalf("expected config setup to succeed, got %v", err)
	}
	if err := os.WriteFile(authPath, []byte(desiredAuth), 0o644); err != nil {
		t.Fatalf("expected auth setup to succeed, got %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(sessionPath), 0o700); err != nil {
		t.Fatalf("expected session dir setup to succeed, got %v", err)
	}
	if err := os.WriteFile(sessionPath, []byte("session-data"), 0o600); err != nil {
		t.Fatalf("expected session setup to succeed, got %v", err)
	}
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	created, err := CreateCodexProfile(ctx, CreateCodexProfileRequest{
		ConfigDir: configDir,
		CodexDir:  codexDir,
		ProfileID: "work",
		Name:      stringPtr("Work"),
	})
	if err != nil {
		t.Fatalf("expected Codex profile create to succeed, got %v", err)
	}
	if created.Summary.ConfigSetID != codexSharedConfigSetID || created.Summary.CredentialID == "" {
		t.Fatalf("unexpected created bindings: %#v", created)
	}

	if err := os.WriteFile(configPath, []byte(`model = "other"`+"\n"+`api_key = "live-config-secret"`+"\n"), 0o600); err != nil {
		t.Fatalf("expected config mutation to succeed, got %v", err)
	}
	if err := os.WriteFile(authPath, []byte(`{"unknown_auth_shape":"live-secret"}`), 0o644); err != nil {
		t.Fatalf("expected auth mutation to succeed, got %v", err)
	}

	plan, err := BuildPlan(ctx, BuildPlanRequest{
		ConfigDir:  configDir,
		ProviderID: codexconfig.ProviderID,
		ProfileID:  "work",
	})
	if err != nil {
		t.Fatalf("expected Codex plan to succeed, got %v", err)
	}
	if len(plan.Operations) != 2 {
		t.Fatalf("expected config and auth operations, got %#v", plan)
	}
	for _, leaked := range []string{"desired-secret", "desired-refresh", "live-secret", "desired-config-secret", "live-config-secret"} {
		if strings.Contains(planPreviewText(plan.Operations), leaked) {
			t.Fatalf("expected plan previews to redact %q, got %#v", leaked, plan.Operations)
		}
	}
	if !hasOperationPreview(plan.Operations, codexconfig.AuthTargetID, codexpreset.AuthPreviewContent) {
		t.Fatalf("expected auth preview to be fully redacted, got %#v", plan.Operations)
	}

	if _, err := ApplySwitch(ctx, ApplySwitchRequest{
		ConfigDir:  configDir,
		ProviderID: codexconfig.ProviderID,
		ProfileID:  "work",
		Confirm:    true,
	}); err != nil {
		t.Fatalf("expected Codex switch to succeed, got %v", err)
	}
	if got := readFileString(t, configPath); !strings.Contains(got, `model = "other"`) {
		t.Fatalf("expected valid active config working copy to be checked in and retained, got %q", got)
	}
	assertJSONFile(t, authPath, "desired-secret")
	if runtime.GOOS != "windows" {
		info, err := os.Stat(authPath)
		if err != nil {
			t.Fatalf("expected auth stat to succeed, got %v", err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("expected auth.json mode 0600, got %#o", info.Mode().Perm())
		}
	}
	if got := readFileString(t, sessionPath); got != "session-data" {
		t.Fatalf("expected sessions to be untouched, got %q", got)
	}
}

func TestCodexProfileCreateHandlesMissingFiles(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	authPath := filepath.Join(codexDir, codexconfig.AuthFileName)
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	_, err := CreateCodexProfile(ctx, CreateCodexProfileRequest{
		ConfigDir: configDir,
		CodexDir:  codexDir,
		ProfileID: "work",
	})
	assertAppErrorCode(t, err, ErrorCodexInvalid)
	if !strings.Contains(err.Error(), "config.toml") {
		t.Fatalf("expected missing config error to mention config.toml, got %v", err)
	}

	if err := os.WriteFile(authPath, []byte(`{"tokens":{"account_id":"work-account","access_token":"secret"}}`), 0o600); err != nil {
		t.Fatalf("expected auth setup to succeed, got %v", err)
	}
	_, err = CreateCodexProfile(ctx, CreateCodexProfileRequest{
		ConfigDir: configDir,
		CodexDir:  codexDir,
		ProfileID: "work",
	})
	assertAppErrorCode(t, err, ErrorCodexInvalid)

	emptyConfigDir := t.TempDir()
	emptyCodexDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: emptyConfigDir}); err != nil {
		t.Fatalf("expected second init to succeed, got %v", err)
	}
	if err := os.WriteFile(filepath.Join(emptyCodexDir, codexconfig.ConfigFileName), nil, 0o600); err != nil {
		t.Fatalf("expected empty config setup to succeed, got %v", err)
	}
	if err := os.WriteFile(filepath.Join(emptyCodexDir, codexconfig.AuthFileName), []byte(`{"tokens":{"account_id":"work-account","access_token":"secret"}}`), 0o600); err != nil {
		t.Fatalf("expected empty config auth setup to succeed, got %v", err)
	}
	emptyResult, err := CreateCodexProfile(ctx, CreateCodexProfileRequest{
		ConfigDir: emptyConfigDir,
		CodexDir:  emptyCodexDir,
		ProfileID: "work",
	})
	if err != nil {
		t.Fatalf("expected create with existing empty config to succeed, got %v", err)
	}
	if hasWarning(emptyResult.Warnings, "config.toml is missing") {
		t.Fatalf("expected existing empty config not to be reported missing, got %#v", emptyResult.Warnings)
	}
}

func TestCodexProfileCreateStoresHiddenCredentialAndSummarizesCodexAccountID(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, codexconfig.ConfigFileName), []byte(`model = "gpt-5.3-codex"`+"\n"), 0o600); err != nil {
		t.Fatalf("expected config setup to succeed, got %v", err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, codexconfig.AuthFileName), []byte(`{"tokens":{"account_id":"auth-account","access_token":"secret"}}`), 0o600); err != nil {
		t.Fatalf("expected auth setup to succeed, got %v", err)
	}

	result, err := CreateCodexProfile(ctx, CreateCodexProfileRequest{
		ConfigDir: configDir,
		CodexDir:  codexDir,
		ProfileID: "team-zhu",
	})
	if err != nil {
		t.Fatalf("expected create to succeed, got %v", err)
	}
	if result.Profile.ID != "team-zhu" {
		t.Fatalf("unexpected create result: %#v", result)
	}
	list, err := ListCodexProfiles(ctx, ListCodexProfilesRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected Codex profile list to succeed, got %v", err)
	}
	if len(list.Profiles) != 1 || list.Profiles[0].CodexAccountID != "auth-account" {
		t.Fatalf("expected Codex account id to be summarized from credential payload, got %#v", list.Profiles)
	}
	db := openHealthyAppTestStore(t, ctx, configDir)
	defer db.Close()
	credentials, err := db.ListProviderCredentials(ctx, codexconfig.ProviderID)
	if err != nil {
		t.Fatalf("expected credentials to list, got %v", err)
	}
	if len(credentials) != 1 || strings.Contains(credentials[0].MetadataJSON, "auth-account") {
		t.Fatalf("expected one opaque credential without Codex account id metadata, got %#v", credentials)
	}
}

func assertJSONFile(t *testing.T, path, wantToken string) {
	t.Helper()

	var decoded map[string]any
	if err := json.Unmarshal([]byte(readFileString(t, path)), &decoded); err != nil {
		t.Fatalf("expected auth JSON to parse, got %v", err)
	}
	tokens, ok := decoded["tokens"].(map[string]any)
	if !ok || tokens["access_token"] != wantToken {
		t.Fatalf("unexpected auth JSON: %#v", decoded)
	}
}

func hasOperationPreview(operations []PlanOperation, targetID, preview string) bool {
	for _, op := range operations {
		if op.TargetID == targetID && op.DesiredPreview.Content == preview {
			return true
		}
	}
	return false
}

func planPreviewText(operations []PlanOperation) string {
	var builder strings.Builder
	for _, op := range operations {
		builder.WriteString(op.BeforePreview.Content)
		builder.WriteString(op.DesiredPreview.Content)
		builder.WriteString(op.AfterPreview.Content)
	}
	return builder.String()
}

func openHealthyAppTestStore(t *testing.T, ctx context.Context, configDir string) *store.Store {
	t.Helper()

	db, err := openHealthyStore(ctx, configDir, true)
	if err != nil {
		t.Fatalf("expected healthy store to open, got %v", err)
	}
	return db
}

func hasWarning(warnings []string, want string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, want) {
			return true
		}
	}
	return false
}
