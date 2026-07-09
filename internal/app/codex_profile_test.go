package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	"github.com/strahe/profiledeck/internal/store"
)

func TestListCodexProfilesSummarizesSnapshotPresetAndActive(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	writeCodexProfileFixture(t, codexDir, `model = "gpt-5-codex"`+"\n", `{"tokens":{"account_id":"remote-work","access_token":"captured-secret"}}`)

	if _, err := CodexProfileCapture(ctx, CodexProfileCaptureRequest{
		ConfigDir: configDir,
		CodexDir:  codexDir,
		ProfileID: "snapshot",
		AccountID: "local-work",
	}); err != nil {
		t.Fatalf("expected capture to succeed, got %v", err)
	}
	if _, err := CodexProfileSet(ctx, CodexProfileSetRequest{
		ConfigDir:     configDir,
		CodexDir:      codexDir,
		ProfileID:     "preset",
		Model:         "gpt-5-codex",
		ModelProvider: "openai",
		OpenAIBaseURL: nil,
		AccountID:     "",
		Name:          nil,
		Description:   nil,
	}); err != nil {
		t.Fatalf("expected profile set to succeed, got %v", err)
	}
	if _, err := CreateProfile(ctx, CreateProfileRequest{ConfigDir: configDir, ID: "generic", Name: "Generic"}); err != nil {
		t.Fatalf("expected generic profile to be created, got %v", err)
	}
	completeCodexProfileSwitchForTest(t, ctx, configDir, "switch-active", "preset")

	result, err := ListCodexProfiles(ctx, ListCodexProfilesRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected list to succeed, got %v", err)
	}
	if len(result.Profiles) != 2 {
		t.Fatalf("expected only Codex profiles, got %#v", result.Profiles)
	}
	byID := codexProfileSummariesByID(result.Profiles)
	if got := byID["snapshot"]; got.SaveKind != CodexProfileSaveKindSnapshot || got.TargetCount != 2 || got.AccountID != "local-work" || got.Active {
		t.Fatalf("unexpected snapshot summary: %#v", got)
	}
	if got := byID["snapshot"]; got.Model != "gpt-5-codex" || got.ModelProvider != "openai" {
		t.Fatalf("expected snapshot model summary, got %#v", got)
	}
	if got := byID["preset"]; got.SaveKind != CodexProfileSaveKindConfigPreset || got.TargetCount != 1 || !got.Active || got.ActiveOperationID != "switch-active" {
		t.Fatalf("unexpected preset summary: %#v", got)
	}
	if got := byID["preset"]; got.Model != "gpt-5-codex" || got.ModelProvider != "openai" {
		t.Fatalf("expected preset model summary, got %#v", got)
	}
	if _, ok := byID["generic"]; ok {
		t.Fatalf("expected generic profile to be excluded, got %#v", byID["generic"])
	}
}

func TestGetCodexProfileRejectsNonCodexProfileAndRedactsTargets(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	writeCodexProfileFixture(t, codexDir, `model = "gpt-5-codex"`+"\n"+`api_key = "config-secret"`+"\n", `{"tokens":{"account_id":"remote-work","access_token":"captured-secret","refresh_token":"captured-refresh"}}`)
	if _, err := CodexProfileCapture(ctx, CodexProfileCaptureRequest{
		ConfigDir: configDir,
		CodexDir:  codexDir,
		ProfileID: "snapshot",
		AccountID: "local-work",
	}); err != nil {
		t.Fatalf("expected capture to succeed, got %v", err)
	}
	if _, err := CreateProfile(ctx, CreateProfileRequest{ConfigDir: configDir, ID: "generic", Name: "Generic"}); err != nil {
		t.Fatalf("expected generic profile to be created, got %v", err)
	}

	detail, err := GetCodexProfile(ctx, GetCodexProfileRequest{ConfigDir: configDir, ProfileID: "snapshot"})
	if err != nil {
		t.Fatalf("expected show to succeed, got %v", err)
	}
	if detail.Summary.Profile.ID != "snapshot" || len(detail.Targets) != 2 {
		t.Fatalf("unexpected detail: %#v", detail)
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		t.Fatalf("expected detail to marshal, got %v", err)
	}
	for _, leaked := range []string{"config-secret", "captured-secret", "captured-refresh", "access_token", "refresh_token"} {
		if strings.Contains(string(raw), leaked) {
			t.Fatalf("expected Codex profile detail to redact %q, got %s", leaked, raw)
		}
	}

	_, err = GetCodexProfile(ctx, GetCodexProfileRequest{ConfigDir: configDir, ProfileID: "generic"})
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Code != ErrorProfileNotFound || appErr.Message != "Codex profile not found" {
		t.Fatalf("expected stable Codex profile not found error, got %v", err)
	}
}

func TestListCodexProfilesWarnsForMalformedTargetMetadata(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	writeCodexProfileFixture(t, codexDir, `model = "gpt-5-codex"`+"\n", `{"tokens":{"account_id":"remote-work","access_token":"captured-secret"}}`)
	if _, err := CodexProfileCapture(ctx, CodexProfileCaptureRequest{
		ConfigDir: configDir,
		CodexDir:  codexDir,
		ProfileID: "snapshot",
		AccountID: "local-work",
	}); err != nil {
		t.Fatalf("expected capture to succeed, got %v", err)
	}

	db, err := openHealthyStore(ctx, configDir, false)
	if err != nil {
		t.Fatalf("expected store open to succeed, got %v", err)
	}
	badMetadata := "{not-json"
	if _, err := db.UpdateProfileTarget(ctx, store.UpdateProfileTargetParams{
		ProfileID:    "snapshot",
		ProviderID:   codexconfig.ProviderID,
		TargetID:     codexconfig.TargetID,
		MetadataJSON: &badMetadata,
	}); err != nil {
		t.Fatalf("expected profile target metadata update to succeed, got %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("expected store close to succeed, got %v", err)
	}

	result, err := ListCodexProfiles(ctx, ListCodexProfilesRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected malformed target metadata not to fail list, got %v", err)
	}
	if len(result.Profiles) != 1 {
		t.Fatalf("expected profile to remain visible, got %#v", result.Profiles)
	}
	got := result.Profiles[0]
	if got.SaveKind != CodexProfileSaveKindUnknown || len(got.Warnings) == 0 {
		t.Fatalf("expected warning and unknown save kind, got %#v", got)
	}
	detail, err := GetCodexProfile(ctx, GetCodexProfileRequest{ConfigDir: configDir, ProfileID: "snapshot"})
	if err != nil {
		t.Fatalf("expected malformed target metadata not to fail show, got %v", err)
	}
	if len(detail.Targets) != 2 || len(detail.Summary.Warnings) == 0 {
		t.Fatalf("expected detail to include targets and warnings, got %#v", detail)
	}
}

func writeCodexProfileFixture(t *testing.T, codexDir string, config string, auth string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(codexDir, codexconfig.ConfigFileName), []byte(config), 0o600); err != nil {
		t.Fatalf("expected Codex config fixture to write, got %v", err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, codexconfig.AuthFileName), []byte(auth), 0o600); err != nil {
		t.Fatalf("expected Codex auth fixture to write, got %v", err)
	}
}

func completeCodexProfileSwitchForTest(t *testing.T, ctx context.Context, configDir string, operationID string, profileID string) {
	t.Helper()
	db, err := openHealthyStore(ctx, configDir, false)
	if err != nil {
		t.Fatalf("expected store open to succeed, got %v", err)
	}
	defer db.Close()
	if _, err := db.CreatePendingSwitchOperation(ctx, store.CreateSwitchOperationParams{
		ID:           operationID,
		ProfileID:    profileID,
		MetadataJSON: "{}",
	}); err != nil {
		t.Fatalf("expected switch operation create to succeed, got %v", err)
	}
	if err := db.CompleteSwitchOperation(ctx, store.CompleteSwitchOperationParams{
		ID:           operationID,
		ProviderID:   codexconfig.ProviderID,
		ProfileID:    profileID,
		MetadataJSON: "{}",
	}); err != nil {
		t.Fatalf("expected switch operation completion to succeed, got %v", err)
	}
}

func codexProfileSummariesByID(summaries []CodexProfileSummary) map[string]CodexProfileSummary {
	result := map[string]CodexProfileSummary{}
	for _, summary := range summaries {
		result[summary.Profile.ID] = summary
	}
	return result
}
