package preset

import (
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
	configRaw, err := TargetMetadataJSON(codexconfig.TargetID, TargetModeConfigSet)
	if err != nil {
		t.Fatalf("expected config-set target metadata to encode, got %v", err)
	}
	config, err := DecodeTargetMetadata(configRaw)
	if err != nil {
		t.Fatalf("expected config-set target metadata to decode, got %v", err)
	}
	if !config.Compatible() {
		t.Fatalf("expected config-set target metadata to be compatible: %#v", config)
	}

	authRaw, err := TargetMetadataJSON(codexconfig.AuthTargetID, TargetModeCredential)
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
	legacyManaged := TargetMetadata{
		Preset:        codexconfig.PresetName,
		PresetVersion: codexconfig.PresetVersion,
		TargetKind:    codexconfig.TargetID,
		Mode:          "managed-keys",
	}
	if legacyManaged.Compatible() {
		t.Fatalf("expected legacy managed target metadata to be incompatible")
	}
}

func TestTargetValueJSONHelpers(t *testing.T) {
	configRaw, err := ConfigSetBindingValueJSON(" shared ")
	if err != nil {
		t.Fatalf("expected config value to encode, got %v", err)
	}
	configSetID, err := ParseConfigSetBindingValueJSON(configRaw)
	if err != nil {
		t.Fatalf("expected config binding to parse, got %v", err)
	}
	if configSetID != "shared" {
		t.Fatalf("unexpected config set id: %q", configSetID)
	}

	authRaw, err := CredentialBindingValueJSON("cred_work")
	if err != nil {
		t.Fatalf("expected auth target value to encode, got %v", err)
	}
	credentialID, err := ParseCredentialBindingValueJSON(authRaw)
	if err != nil {
		t.Fatalf("expected auth target value to parse, got %v", err)
	}
	if credentialID != "cred_work" {
		t.Fatalf("expected credential id, got %q", credentialID)
	}
}
