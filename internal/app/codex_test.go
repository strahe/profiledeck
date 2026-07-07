package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"github.com/strahe/profiledeck/internal/codexconfig"
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
	if _, err := CodexProfileSet(ctx, CodexProfileSetRequest{
		ConfigDir: configDir,
		CodexDir:  codexDirA,
		ProfileID: "work",
		Model:     "gpt-5.3-codex",
	}); err != nil {
		t.Fatalf("expected Codex profile set to succeed, got %v", err)
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

func TestCodexProfileSetCreatesAndUpdatesPresetRecords(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	result, err := CodexProfileSet(ctx, CodexProfileSetRequest{
		ConfigDir: configDir,
		CodexDir:  codexDir,
		ProfileID: "work",
		Model:     "gpt-5.3-codex",
		Name:      stringPtr("  Work  "),
	})
	if err != nil {
		t.Fatalf("expected Codex profile set to succeed, got %v", err)
	}
	if result.Provider.ID != codexconfig.ProviderID || result.Provider.AdapterID != codexconfig.AdapterID {
		t.Fatalf("unexpected provider: %#v", result.Provider)
	}
	if result.Profile.ID != "work" || result.Profile.Name != "Work" {
		t.Fatalf("unexpected profile: %#v", result.Profile)
	}
	if result.Target.TargetID != codexconfig.TargetID || result.Target.Path != filepath.Join(codexDir, codexconfig.ConfigFileName) {
		t.Fatalf("unexpected target: %#v", result.Target)
	}

	result, err = CodexProfileSet(ctx, CodexProfileSetRequest{
		ConfigDir:     configDir,
		CodexDir:      codexDir,
		ProfileID:     "work",
		Model:         "gpt-5.4-codex",
		ModelProvider: "openai",
		Description:   stringPtr("  Updated  "),
	})
	if err != nil {
		t.Fatalf("expected repeated Codex profile set to succeed, got %v", err)
	}
	if result.Profile.Name != "Work" || result.Profile.Description != "Updated" {
		t.Fatalf("expected repeated set to update only explicit profile fields, got %#v", result.Profile)
	}

	db := openHealthyAppTestStore(t, ctx, configDir)
	defer db.Close()
	target, err := db.GetProfileTarget(ctx, "work", codexconfig.ProviderID, codexconfig.TargetID)
	if err != nil {
		t.Fatalf("expected stored Codex target, got %v", err)
	}
	managed, err := codexconfig.ParseValueJSON(target.ValueJSON)
	if err != nil {
		t.Fatalf("expected stored value_json to parse, got %v", err)
	}
	if managed.Model != "gpt-5.4-codex" || managed.ModelProvider != "openai" || managed.HasOpenAIBaseURL {
		t.Fatalf("unexpected stored managed config: %#v", managed)
	}
}

func TestCodexProfileSetRejectsInvalidProviderMetadata(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	if _, err := CodexProfileSet(ctx, CodexProfileSetRequest{
		ConfigDir: configDir,
		CodexDir:  codexDir,
		ProfileID: "work",
		Model:     "gpt-5.3-codex",
	}); err != nil {
		t.Fatalf("expected Codex profile set to succeed, got %v", err)
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

	_, err = CodexProfileSet(ctx, CodexProfileSetRequest{
		ConfigDir: configDir,
		CodexDir:  codexDir,
		ProfileID: "work",
		Model:     "gpt-5.4-codex",
	})
	assertAppErrorCode(t, err, ErrorStoreSchemaInvalid)
}

func TestCodexPlanRejectsMutatedPresetTargetPath(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	if _, err := CodexProfileSet(ctx, CodexProfileSetRequest{
		ConfigDir: configDir,
		CodexDir:  codexDir,
		ProfileID: "work",
		Model:     "gpt-5.3-codex",
	}); err != nil {
		t.Fatalf("expected Codex profile set to succeed, got %v", err)
	}

	mutatedPath := filepath.Join(t.TempDir(), "other.toml")
	if _, err := UpdateProfileTarget(ctx, UpdateProfileTargetRequest{
		ConfigDir:  configDir,
		ProfileID:  "work",
		ProviderID: codexconfig.ProviderID,
		TargetID:   codexconfig.TargetID,
		Path:       &mutatedPath,
	}); err != nil {
		t.Fatalf("expected target mutation setup to succeed, got %v", err)
	}

	_, err := BuildPlan(ctx, BuildPlanRequest{
		ConfigDir:  configDir,
		ProviderID: codexconfig.ProviderID,
		ProfileID:  "work",
	})
	assertAppErrorCode(t, err, ErrorCodexInvalid)
}

func TestCodexProfileSetRejectsConflictingProviderAndHome(t *testing.T) {
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
	_, err := CodexProfileSet(ctx, CodexProfileSetRequest{
		ConfigDir: configDir,
		CodexDir:  codexDirA,
		ProfileID: "work",
		Model:     "gpt-5.3-codex",
	})
	assertAppErrorCode(t, err, ErrorCodexInvalid)

	configDir = t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected second init to succeed, got %v", err)
	}
	if _, err := CodexProfileSet(ctx, CodexProfileSetRequest{
		ConfigDir: configDir,
		CodexDir:  codexDirA,
		ProfileID: "work",
		Model:     "gpt-5.3-codex",
	}); err != nil {
		t.Fatalf("expected first Codex home setup to succeed, got %v", err)
	}
	_, err = CodexProfileSet(ctx, CodexProfileSetRequest{
		ConfigDir: configDir,
		CodexDir:  codexDirB,
		ProfileID: "personal",
		Model:     "gpt-5.3-codex",
	})
	assertAppErrorCode(t, err, ErrorCodexInvalid)
}

func TestCodexTargetMetadataCompatibilityIgnoresManagedKeyOrder(t *testing.T) {
	metadata := codexTargetMetadata{
		Preset:        codexconfig.PresetName,
		PresetVersion: codexconfig.PresetVersion,
		TargetKind:    codexconfig.TargetID,
		ManagedKeys:   []string{"openai_base_url", "model_provider", "model"},
	}
	if !metadata.compatible() {
		t.Fatalf("expected managed key order not to affect compatibility")
	}

	metadata.ManagedKeys = []string{"openai_base_url", "model_provider", "model_provider"}
	if metadata.compatible() {
		t.Fatalf("expected duplicate managed keys to be incompatible")
	}
}

func TestCodexSwitchWritesManagedConfigThroughPipeline(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	configPath := filepath.Join(codexDir, codexconfig.ConfigFileName)
	existing := strings.Join([]string{
		`model = "old-model"`,
		`model_provider = "old-provider"`,
		`openai_base_url = "https://old.example.test/v1"`,
		`approval_policy = "never"`,
		`api_key = "raw-secret"`,
		``,
		`[tools]`,
		`web_search = true`,
	}, "\n")
	if err := os.WriteFile(configPath, []byte(existing), 0o600); err != nil {
		t.Fatalf("expected Codex config setup to succeed, got %v", err)
	}
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	baseURL := "https://api.example.test/v1"
	if _, err := CodexProfileSet(ctx, CodexProfileSetRequest{
		ConfigDir:     configDir,
		CodexDir:      codexDir,
		ProfileID:     "work",
		Model:         "gpt-5.3-codex",
		OpenAIBaseURL: &baseURL,
	}); err != nil {
		t.Fatalf("expected Codex profile set to succeed, got %v", err)
	}

	plan, err := BuildPlan(ctx, BuildPlanRequest{
		ConfigDir:  configDir,
		ProviderID: codexconfig.ProviderID,
		ProfileID:  "work",
	})
	if err != nil {
		t.Fatalf("expected Codex plan to succeed, got %v", err)
	}
	if len(plan.Operations) != 1 || plan.Operations[0].Action != planActionUpdate {
		t.Fatalf("unexpected Codex plan: %#v", plan)
	}
	if strings.Contains(plan.Operations[0].BeforePreview.Content, "raw-secret") || strings.Contains(plan.Operations[0].DesiredPreview.Content, "raw-secret") {
		t.Fatalf("expected Codex plan previews to redact existing secret-looking values, got %#v", plan.Operations[0])
	}

	if _, err := ApplySwitch(ctx, ApplySwitchRequest{
		ConfigDir:  configDir,
		ProviderID: codexconfig.ProviderID,
		ProfileID:  "work",
		Confirm:    true,
	}); err != nil {
		t.Fatalf("expected Codex switch to succeed, got %v", err)
	}
	assertCodexConfig(t, configPath, map[string]any{
		"model":           "gpt-5.3-codex",
		"model_provider":  "openai",
		"openai_base_url": baseURL,
		"approval_policy": "never",
		"api_key":         "raw-secret",
	})

	if _, err := CodexProfileSet(ctx, CodexProfileSetRequest{
		ConfigDir: configDir,
		CodexDir:  codexDir,
		ProfileID: "work",
		Model:     "gpt-5.4-codex",
	}); err != nil {
		t.Fatalf("expected Codex profile update without base URL to succeed, got %v", err)
	}
	if _, err := ApplySwitch(ctx, ApplySwitchRequest{
		ConfigDir:  configDir,
		ProviderID: codexconfig.ProviderID,
		ProfileID:  "work",
		Confirm:    true,
	}); err != nil {
		t.Fatalf("expected second Codex switch to succeed, got %v", err)
	}
	raw := readFileString(t, configPath)
	if strings.Contains(raw, "openai_base_url") || strings.Contains(raw, "old.example.test") {
		t.Fatalf("expected stale openai_base_url to be removed, got %q", raw)
	}
	assertCodexConfig(t, configPath, map[string]any{
		"model":           "gpt-5.4-codex",
		"model_provider":  "openai",
		"approval_policy": "never",
		"api_key":         "raw-secret",
	})
}

func TestCodexProfileCaptureSwitchesFullConfigAndAuthThroughPipeline(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	configPath := filepath.Join(codexDir, codexconfig.ConfigFileName)
	authPath := filepath.Join(codexDir, codexconfig.AuthFileName)
	sessionPath := filepath.Join(codexDir, "sessions", "session.jsonl")

	capturedConfig := strings.Join([]string{
		`model = "gpt-5.3-codex"`,
		`approval_policy = "never"`,
		``,
		`[tools]`,
		`web_search = true`,
		``,
	}, "\n")
	capturedAuth := `{"tokens":{"account_id":"work-account","access_token":"captured-secret","refresh_token":"captured-refresh"}}`
	if err := os.WriteFile(configPath, []byte(capturedConfig), 0o600); err != nil {
		t.Fatalf("expected config setup to succeed, got %v", err)
	}
	if err := os.WriteFile(authPath, []byte(capturedAuth), 0o644); err != nil {
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

	capture, err := CodexProfileCapture(ctx, CodexProfileCaptureRequest{
		ConfigDir: configDir,
		CodexDir:  codexDir,
		ProfileID: "work",
		AccountID: "work-account",
		Name:      stringPtr("Work"),
	})
	if err != nil {
		t.Fatalf("expected Codex profile capture to succeed, got %v", err)
	}
	if capture.ConfigTarget.TargetID != codexconfig.TargetID || capture.AuthTarget.TargetID != codexconfig.AuthTargetID {
		t.Fatalf("unexpected captured targets: %#v", capture)
	}
	if capture.Account.PayloadSHA256 == "" {
		t.Fatalf("expected captured account hash")
	}
	if capture.Account.DisplayName != "Work" {
		t.Fatalf("expected captured account display name to use validated name, got %#v", capture.Account)
	}

	if err := os.WriteFile(configPath, []byte(`model = "other"`+"\n"), 0o600); err != nil {
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
	for _, leaked := range []string{"captured-secret", "captured-refresh", "live-secret"} {
		if strings.Contains(planPreviewText(plan.Operations), leaked) {
			t.Fatalf("expected plan previews to redact %q, got %#v", leaked, plan.Operations)
		}
	}
	if !hasOperationPreview(plan.Operations, codexconfig.AuthTargetID, codexAuthPreviewContent) {
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
	if got := readFileString(t, configPath); got != capturedConfig {
		t.Fatalf("expected full config snapshot to be restored, got %q", got)
	}
	assertJSONFile(t, authPath, "captured-secret")
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

func TestCodexProfileCaptureHandlesMissingFiles(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	authPath := filepath.Join(codexDir, codexconfig.AuthFileName)
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	_, err := CodexProfileCapture(ctx, CodexProfileCaptureRequest{
		ConfigDir: configDir,
		CodexDir:  codexDir,
		ProfileID: "work",
		AccountID: "work-account",
	})
	assertAppErrorCode(t, err, ErrorCodexInvalid)
	if !strings.Contains(err.Error(), "auth.json") {
		t.Fatalf("expected missing auth error to mention auth.json, got %v", err)
	}

	if err := os.WriteFile(authPath, []byte(`{"tokens":{"account_id":"work-account","access_token":"secret"}}`), 0o600); err != nil {
		t.Fatalf("expected auth setup to succeed, got %v", err)
	}
	result, err := CodexProfileCapture(ctx, CodexProfileCaptureRequest{
		ConfigDir: configDir,
		CodexDir:  codexDir,
		ProfileID: "work",
		AccountID: "work-account",
	})
	if err != nil {
		t.Fatalf("expected capture without config to succeed, got %v", err)
	}
	if !hasWarning(result.Warnings, "config.toml is missing") {
		t.Fatalf("expected missing config warning, got %#v", result.Warnings)
	}

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
	emptyResult, err := CodexProfileCapture(ctx, CodexProfileCaptureRequest{
		ConfigDir: emptyConfigDir,
		CodexDir:  emptyCodexDir,
		ProfileID: "work",
		AccountID: "work-account",
	})
	if err != nil {
		t.Fatalf("expected capture with existing empty config to succeed, got %v", err)
	}
	if hasWarning(emptyResult.Warnings, "config.toml is missing") {
		t.Fatalf("expected existing empty config not to be reported missing, got %#v", emptyResult.Warnings)
	}
}

func TestCodexProfileCaptureUsesProfileIDAsLocalAccountByDefault(t *testing.T) {
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

	result, err := CodexProfileCapture(ctx, CodexProfileCaptureRequest{
		ConfigDir: configDir,
		CodexDir:  codexDir,
		ProfileID: "team-zhu",
	})
	if err != nil {
		t.Fatalf("expected capture with default account to succeed, got %v", err)
	}
	if result.Profile.ID != "team-zhu" || result.Account.AccountID != "team-zhu" {
		t.Fatalf("expected profile id to be the default local account id, got %#v", result)
	}
	if result.Account.Metadata["codex_account_id"] != "auth-account" {
		t.Fatalf("expected Codex account id to be stored as metadata, got %#v", result.Account.Metadata)
	}
}

func TestCodexProfileCaptureAllowsLocalAccountAliasDistinctFromCodexAccountID(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	authPath := filepath.Join(codexDir, codexconfig.AuthFileName)
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, codexconfig.ConfigFileName), []byte(`model = "gpt-5.3-codex"`+"\n"), 0o600); err != nil {
		t.Fatalf("expected config setup to succeed, got %v", err)
	}
	if err := os.WriteFile(authPath, []byte(`{"tokens":{"account_id":"Team/Shared","access_token":"secret"}}`), 0o600); err != nil {
		t.Fatalf("expected auth setup to succeed, got %v", err)
	}

	result, err := CodexProfileCapture(ctx, CodexProfileCaptureRequest{
		ConfigDir: configDir,
		CodexDir:  codexDir,
		ProfileID: "work",
		AccountID: "local-work",
	})
	if err != nil {
		t.Fatalf("expected local account alias to be independent from Codex account id, got %v", err)
	}
	if result.Account.AccountID != "local-work" || result.Account.Metadata["codex_account_id"] != "Team/Shared" {
		t.Fatalf("unexpected captured local account metadata: %#v", result.Account)
	}

	recapture, err := CodexProfileCapture(ctx, CodexProfileCaptureRequest{
		ConfigDir: configDir,
		CodexDir:  codexDir,
		ProfileID: "work",
	})
	if err != nil {
		t.Fatalf("expected recapture to reuse the existing local account binding, got %v", err)
	}
	if recapture.Account.AccountID != "local-work" {
		t.Fatalf("expected recapture to keep existing local account binding, got %#v", recapture.Account)
	}

	_, err = CodexProfileCapture(ctx, CodexProfileCaptureRequest{
		ConfigDir: configDir,
		CodexDir:  codexDir,
		ProfileID: "work",
		AccountID: "other-local",
	})
	assertAppErrorCode(t, err, ErrorCodexInvalid)
}

func TestCodexProfileSetWithAccountRequiresExistingSecret(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	_, err := CodexProfileSet(ctx, CodexProfileSetRequest{
		ConfigDir: configDir,
		CodexDir:  codexDir,
		ProfileID: "work",
		AccountID: "missing",
		Model:     "gpt-5.3-codex",
	})
	assertAppErrorCode(t, err, ErrorCodexInvalid)

	authFile := filepath.Join(t.TempDir(), codexconfig.AuthFileName)
	if err := os.WriteFile(authFile, []byte(`{"tokens":{"account_id":"work-account","access_token":"secret"}}`), 0o600); err != nil {
		t.Fatalf("expected auth file setup to succeed, got %v", err)
	}
	if _, err := CodexAccountImport(ctx, CodexAccountImportRequest{
		ConfigDir: configDir,
		AccountID: "work-account",
		AuthFile:  authFile,
	}); err != nil {
		t.Fatalf("expected account import to succeed, got %v", err)
	}
	result, err := CodexProfileSet(ctx, CodexProfileSetRequest{
		ConfigDir: configDir,
		CodexDir:  codexDir,
		ProfileID: "work",
		AccountID: "work-account",
		Model:     "gpt-5.3-codex",
	})
	if err != nil {
		t.Fatalf("expected profile set with existing account to succeed, got %v", err)
	}
	if result.AuthTarget == nil || result.AuthTarget.TargetID != codexconfig.AuthTargetID {
		t.Fatalf("expected profile set to bind auth target, got %#v", result)
	}
}

func TestCodexAccountImportAllowsLocalAliasDistinctFromCodexAccountID(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	authFile := filepath.Join(t.TempDir(), codexconfig.AuthFileName)
	if err := os.WriteFile(authFile, []byte(`{"tokens":{"account_id":"Team/Shared","access_token":"secret"}}`), 0o600); err != nil {
		t.Fatalf("expected auth file setup to succeed, got %v", err)
	}

	account, err := CodexAccountImport(ctx, CodexAccountImportRequest{
		ConfigDir: configDir,
		AccountID: "local-work",
		AuthFile:  authFile,
	})
	if err != nil {
		t.Fatalf("expected account import to accept a local alias, got %v", err)
	}
	if account.AccountID != "local-work" || account.Metadata["codex_account_id"] != "Team/Shared" {
		t.Fatalf("unexpected imported account metadata: %#v", account)
	}
}

func TestCodexAccountExportRejectsSymlinkOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions vary on Windows")
	}
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	authFile := filepath.Join(t.TempDir(), codexconfig.AuthFileName)
	if err := os.WriteFile(authFile, []byte(`{"tokens":{"account_id":"work-account","access_token":"secret"}}`), 0o600); err != nil {
		t.Fatalf("expected auth file setup to succeed, got %v", err)
	}
	if _, err := CodexAccountImport(ctx, CodexAccountImportRequest{
		ConfigDir: configDir,
		AccountID: "work-account",
		AuthFile:  authFile,
	}); err != nil {
		t.Fatalf("expected account import to succeed, got %v", err)
	}
	target := filepath.Join(t.TempDir(), "target-auth.json")
	link := filepath.Join(t.TempDir(), "auth-link.json")
	if err := os.WriteFile(target, []byte(`{"tokens":{"account_id":"work-account","access_token":"old"}}`), 0o600); err != nil {
		t.Fatalf("expected export target setup to succeed, got %v", err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("expected symlink setup to succeed, got %v", err)
	}

	_, err := CodexAccountExport(ctx, CodexAccountExportRequest{
		ConfigDir: configDir,
		AccountID: "work-account",
		Output:    link,
		Force:     true,
	})
	assertAppErrorCode(t, err, ErrorCodexInvalid)
	if got := readFileString(t, target); strings.Contains(got, "secret") {
		t.Fatalf("expected symlink target not to receive exported auth, got %q", got)
	}
}

func TestCodexAccountExportExistingFileForceSemantics(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	authPayload := `{"tokens":{"account_id":"work-account","access_token":"secret"}}`
	authFile := filepath.Join(t.TempDir(), codexconfig.AuthFileName)
	if err := os.WriteFile(authFile, []byte(authPayload), 0o600); err != nil {
		t.Fatalf("expected auth file setup to succeed, got %v", err)
	}
	if _, err := CodexAccountImport(ctx, CodexAccountImportRequest{
		ConfigDir: configDir,
		AccountID: "work-account",
		AuthFile:  authFile,
	}); err != nil {
		t.Fatalf("expected account import to succeed, got %v", err)
	}

	output := filepath.Join(t.TempDir(), "auth.json")
	parent := filepath.Dir(output)
	if runtime.GOOS != "windows" {
		if err := os.Chmod(parent, 0o755); err != nil {
			t.Fatalf("expected output parent chmod setup to succeed, got %v", err)
		}
	}
	oldPayload := `{"tokens":{"account_id":"old-account","access_token":"old"}}`
	if err := os.WriteFile(output, []byte(oldPayload), 0o644); err != nil {
		t.Fatalf("expected existing output setup to succeed, got %v", err)
	}
	_, err := CodexAccountExport(ctx, CodexAccountExportRequest{
		ConfigDir: configDir,
		AccountID: "work-account",
		Output:    output,
	})
	assertAppErrorCode(t, err, ErrorCodexInvalid)
	if got := readFileString(t, output); got != oldPayload {
		t.Fatalf("expected non-force export to preserve existing file, got %q", got)
	}

	if _, err := CodexAccountExport(ctx, CodexAccountExportRequest{
		ConfigDir: configDir,
		AccountID: "work-account",
		Output:    output,
		Force:     true,
	}); err != nil {
		t.Fatalf("expected force export to succeed, got %v", err)
	}
	if got := readFileString(t, output); got != authPayload {
		t.Fatalf("expected force export to write auth payload, got %q", got)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(output)
		if err != nil {
			t.Fatalf("expected output stat to succeed, got %v", err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("expected exported auth mode 0600, got %#o", info.Mode().Perm())
		}
		parentInfo, err := os.Stat(parent)
		if err != nil {
			t.Fatalf("expected output parent stat to succeed, got %v", err)
		}
		if parentInfo.Mode().Perm() != 0o755 {
			t.Fatalf("expected export not to chmod existing output parent, got %#o", parentInfo.Mode().Perm())
		}
	}
}

func assertCodexConfig(t *testing.T, path string, want map[string]any) {
	t.Helper()

	var decoded map[string]any
	if err := toml.Unmarshal([]byte(readFileString(t, path)), &decoded); err != nil {
		t.Fatalf("expected Codex config TOML to parse, got %v", err)
	}
	for key, value := range want {
		if decoded[key] != value {
			t.Fatalf("expected %s=%#v, got %#v in %#v", key, value, decoded[key], decoded)
		}
	}
	if tools, ok := decoded["tools"].(map[string]any); !ok || tools["web_search"] != true {
		t.Fatalf("expected tools section to be preserved, got %#v", decoded)
	}
}

func assertJSONFile(t *testing.T, path string, wantToken string) {
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

func hasOperationPreview(operations []PlanOperation, targetID string, preview string) bool {
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
