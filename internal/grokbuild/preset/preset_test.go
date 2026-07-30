package preset

import (
	"path/filepath"
	"testing"

	grokconfig "github.com/strahe/profiledeck/internal/grokbuild/config"
)

func TestProviderMetadataRoundTrip(t *testing.T) {
	dir := t.TempDir()
	home := grokconfig.Home{
		Dir:        dir,
		ConfigPath: filepath.Join(dir, grokconfig.ConfigFileName),
		AuthPath:   filepath.Join(dir, grokconfig.AuthFileName),
		LockPath:   filepath.Join(dir, grokconfig.LockFileName),
	}
	raw, err := ProviderMetadataJSON(home)
	if err != nil {
		t.Fatalf("ProviderMetadataJSON: %v", err)
	}
	metadata, err := DecodeProviderMetadata(raw)
	if err != nil {
		t.Fatalf("DecodeProviderMetadata: %v", err)
	}
	if !metadata.Compatible() || metadata.GrokHome != dir ||
		metadata.ConfigPath != home.ConfigPath || metadata.AuthPath != home.AuthPath {
		t.Fatalf("metadata = %#v", metadata)
	}
}

func TestBindingValuesRequireOneResourceID(t *testing.T) {
	raw, err := CredentialBindingValueJSON("credential-a")
	if err != nil {
		t.Fatalf("CredentialBindingValueJSON: %v", err)
	}
	if id, err := ParseCredentialBindingValueJSON(raw); err != nil || id != "credential-a" {
		t.Fatalf("ParseCredentialBindingValueJSON = %q, %v", id, err)
	}
	if _, err := ParseCredentialBindingValueJSON(`{"credential_id":"a","extra":"b"}`); err == nil {
		t.Fatal("extra credential binding field unexpectedly accepted")
	}
}
