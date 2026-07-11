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

func TestListCodexProfilesSummarizesBindingsAndActiveState(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	writeCodexProfileFixture(t, codexDir, "model = \"gpt-5-codex\"\nopenai_base_url = \"https://work.example.test/v1\"\n", `{"tokens":{"account_id":"remote-work","access_token":"work-secret"}}`)
	if _, err := CreateCodexProfile(ctx, CreateCodexProfileRequest{ConfigDir: configDir, CodexDir: codexDir, ProfileID: "work"}); err != nil {
		t.Fatalf("expected work profile create to succeed, got %v", err)
	}
	writeCodexProfileFixture(t, codexDir, "model = \"gpt-5-mini\"\nmodel_provider = \"openai\"\n", `{"tokens":{"account_id":"remote-personal","access_token":"personal-secret"}}`)
	if _, err := CreateCodexProfile(ctx, CreateCodexProfileRequest{
		ConfigDir: configDir, CodexDir: codexDir, ProfileID: "personal",
		NewConfigSetID: "personal-config", NewConfigSetName: stringPointer("Personal"),
	}); err != nil {
		t.Fatalf("expected personal profile create to succeed, got %v", err)
	}
	if _, err := CreateProfile(ctx, CreateProfileRequest{ConfigDir: configDir, ID: "generic", Name: "Generic"}); err != nil {
		t.Fatalf("expected generic profile create to succeed, got %v", err)
	}

	result, err := ListCodexProfiles(ctx, ListCodexProfilesRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected list to succeed, got %v", err)
	}
	if len(result.Profiles) != 2 {
		t.Fatalf("expected two Codex profiles, got %#v", result.Profiles)
	}
	byID := codexProfileSummariesByID(result.Profiles)
	work := byID["work"]
	if work.Active || work.CodexAccountID != "remote-work" || work.ConfigSetID != codexSharedConfigSetID || work.Model != "gpt-5-codex" || work.OpenAIBaseURL != "https://work.example.test/v1" {
		t.Fatalf("unexpected work summary: %#v", work)
	}
	personal := byID["personal"]
	if !personal.Active || personal.CodexAccountID != "remote-personal" || personal.ConfigSetID != "personal-config" || personal.ConfigSetName != "Personal" || personal.Model != "gpt-5-mini" {
		t.Fatalf("unexpected personal summary: %#v", personal)
	}
	if _, ok := byID["generic"]; ok {
		t.Fatalf("expected generic profile to be excluded")
	}
}

func TestGetCodexProfileReturnsResourceSummariesWithoutRawPayloads(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	writeCodexProfileFixture(t, codexDir, "model = \"gpt-5-codex\"\napi_key = \"config-secret\"\n", `{"tokens":{"account_id":"remote-work","access_token":"desired-secret","refresh_token":"desired-refresh"}}`)
	if _, err := CreateCodexProfile(ctx, CreateCodexProfileRequest{ConfigDir: configDir, CodexDir: codexDir, ProfileID: "snapshot"}); err != nil {
		t.Fatalf("expected create to succeed, got %v", err)
	}
	detail, err := GetCodexProfile(ctx, GetCodexProfileRequest{ConfigDir: configDir, ProfileID: "snapshot"})
	if err != nil {
		t.Fatalf("expected show to succeed, got %v", err)
	}
	if detail.Login == nil || detail.ConfigSet == nil || detail.Summary.Profile.ID != "snapshot" {
		t.Fatalf("unexpected detail: %#v", detail)
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		t.Fatalf("expected detail to marshal, got %v", err)
	}
	for _, leaked := range []string{"config-secret", "desired-secret", "desired-refresh", "access_token", "refresh_token", "api_key"} {
		if strings.Contains(string(raw), leaked) {
			t.Fatalf("expected detail not to expose %q, got %s", leaked, raw)
		}
	}

	if _, err := CreateProfile(ctx, CreateProfileRequest{ConfigDir: configDir, ID: "generic", Name: "Generic"}); err != nil {
		t.Fatalf("expected generic profile create to succeed, got %v", err)
	}
	_, err = GetCodexProfile(ctx, GetCodexProfileRequest{ConfigDir: configDir, ProfileID: "generic"})
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Code != ErrorProfileNotFound {
		t.Fatalf("expected Codex profile not found error, got %v", err)
	}
}

func TestListCodexProfilesReportsMalformedBinding(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	writeCodexProfileFixture(t, codexDir, "model = \"gpt-5-codex\"\n", `{"tokens":{"account_id":"remote-work","access_token":"secret"}}`)
	if _, err := CreateCodexProfile(ctx, CreateCodexProfileRequest{ConfigDir: configDir, CodexDir: codexDir, ProfileID: "snapshot"}); err != nil {
		t.Fatalf("expected create to succeed, got %v", err)
	}
	db, err := openHealthyStore(ctx, configDir, false)
	if err != nil {
		t.Fatalf("expected store open, got %v", err)
	}
	badMetadata := "{not-json"
	if _, err := db.UpdateProfileTarget(ctx, store.UpdateProfileTargetParams{
		ProfileID: "snapshot", ProviderID: codexconfig.ProviderID, TargetID: codexconfig.TargetID, MetadataJSON: &badMetadata,
	}); err != nil {
		_ = db.Close()
		t.Fatalf("expected target mutation setup, got %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("expected store close, got %v", err)
	}
	result, err := ListCodexProfiles(ctx, ListCodexProfilesRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected tolerant list, got %v", err)
	}
	if len(result.Profiles) != 1 || !hasWarning(result.Profiles[0].Warnings, "Codex config set binding is invalid") {
		t.Fatalf("expected malformed binding warning, got %#v", result.Profiles)
	}
}

func writeCodexProfileFixture(t *testing.T, codexDir, config, auth string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(codexDir, codexconfig.ConfigFileName), []byte(config), 0o600); err != nil {
		t.Fatalf("expected Codex config fixture to write, got %v", err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, codexconfig.AuthFileName), []byte(auth), 0o600); err != nil {
		t.Fatalf("expected Codex auth fixture to write, got %v", err)
	}
}

func codexProfileSummariesByID(summaries []CodexProfileSummary) map[string]CodexProfileSummary {
	result := map[string]CodexProfileSummary{}
	for _, summary := range summaries {
		result[summary.Profile.ID] = summary
	}
	return result
}

func stringPointer(value string) *string {
	return &value
}
