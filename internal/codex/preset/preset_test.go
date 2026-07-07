package preset

import (
	"encoding/json"
	"reflect"
	"testing"

	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
)

func TestProviderMetadataJSONRoundTrips(t *testing.T) {
	home := codexconfig.Home{Dir: "/tmp/codex", ConfigPath: "/tmp/codex/config.toml", AuthPath: "/tmp/codex/auth.json"}

	raw, err := ProviderMetadataJSON(home)
	if err != nil {
		t.Fatalf("expected metadata to encode, got %v", err)
	}
	metadata, err := DecodeProviderMetadata(raw)
	if err != nil {
		t.Fatalf("expected metadata to decode, got %v", err)
	}
	if !metadata.Compatible() {
		t.Fatalf("expected metadata to be compatible: %#v", metadata)
	}
	if metadata.CodexDir != home.Dir || metadata.ConfigPath != home.ConfigPath || metadata.AuthPath != home.AuthPath {
		t.Fatalf("unexpected metadata: %#v", metadata)
	}
}

func TestTargetMetadataCompatibility(t *testing.T) {
	managedRaw, err := TargetMetadataJSON(codexconfig.TargetID, TargetModeManagedKeys)
	if err != nil {
		t.Fatalf("expected managed target metadata to encode, got %v", err)
	}
	managed, err := DecodeTargetMetadata(managedRaw)
	if err != nil {
		t.Fatalf("expected managed target metadata to decode, got %v", err)
	}
	if !managed.Compatible() {
		t.Fatalf("expected managed target metadata to be compatible: %#v", managed)
	}

	fullRaw, err := TargetMetadataJSON(codexconfig.TargetID, TargetModeFullFile)
	if err != nil {
		t.Fatalf("expected full-file target metadata to encode, got %v", err)
	}
	full, err := DecodeTargetMetadata(fullRaw)
	if err != nil {
		t.Fatalf("expected full-file target metadata to decode, got %v", err)
	}
	if !full.Compatible() {
		t.Fatalf("expected full-file target metadata to be compatible: %#v", full)
	}

	authRaw, err := TargetMetadataJSON(codexconfig.AuthTargetID, TargetModeFullFile)
	if err != nil {
		t.Fatalf("expected auth target metadata to encode, got %v", err)
	}
	auth, err := DecodeTargetMetadata(authRaw)
	if err != nil {
		t.Fatalf("expected auth target metadata to decode, got %v", err)
	}
	if !auth.Compatible() {
		t.Fatalf("expected auth target metadata to be compatible: %#v", auth)
	}
}

func TestManagedTargetMetadataIgnoresManagedKeyOrder(t *testing.T) {
	metadata := TargetMetadata{
		Preset:        codexconfig.PresetName,
		PresetVersion: codexconfig.PresetVersion,
		TargetKind:    codexconfig.TargetID,
		Mode:          TargetModeManagedKeys,
		ManagedKeys:   []string{"openai_base_url", "model_provider", "model"},
	}
	if !metadata.Compatible() {
		t.Fatalf("expected managed key set to be order-insensitive")
	}

	metadata.ManagedKeys = []string{"openai_base_url", "model_provider", "model_provider"}
	if metadata.Compatible() {
		t.Fatalf("expected duplicate managed keys to be incompatible")
	}
}

func TestTargetValueJSONHelpers(t *testing.T) {
	configRaw, err := ReplaceFileValueJSON("model = \"gpt-5.3-codex\"\n")
	if err != nil {
		t.Fatalf("expected config value to encode, got %v", err)
	}
	var configValue map[string]string
	if err := json.Unmarshal([]byte(configRaw), &configValue); err != nil {
		t.Fatalf("expected config value to decode, got %v", err)
	}
	if configValue["content"] != "model = \"gpt-5.3-codex\"\n" {
		t.Fatalf("unexpected config value: %#v", configValue)
	}

	authRaw, err := AuthTargetValueJSON("local-work")
	if err != nil {
		t.Fatalf("expected auth target value to encode, got %v", err)
	}
	accountID, err := ParseAuthTargetValueJSON(authRaw)
	if err != nil {
		t.Fatalf("expected auth target value to parse, got %v", err)
	}
	if accountID != "local-work" {
		t.Fatalf("expected local account id, got %q", accountID)
	}
}

func TestAccountMetadataJSON(t *testing.T) {
	home := codexconfig.Home{Dir: "/tmp/codex", AuthPath: "/tmp/codex/auth.json"}
	raw, err := AccountMetadataJSON(home, "Team/Shared")
	if err != nil {
		t.Fatalf("expected account metadata to encode, got %v", err)
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		t.Fatalf("expected account metadata to decode, got %v", err)
	}
	want := map[string]any{
		"preset":           codexconfig.PresetName,
		"preset_version":   float64(codexconfig.PresetVersion),
		"codex_account_id": "Team/Shared",
		"codex_dir":        "/tmp/codex",
		"auth_path":        "/tmp/codex/auth.json",
	}
	if !reflect.DeepEqual(metadata, want) {
		t.Fatalf("unexpected account metadata: %#v", metadata)
	}
}
