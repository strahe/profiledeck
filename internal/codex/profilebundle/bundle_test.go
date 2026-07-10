package profilebundle

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	codexpreset "github.com/strahe/profiledeck/internal/codex/preset"
)

func TestEncodeIsDeterministicAndPreservesResourceSharing(t *testing.T) {
	auth := `{"tokens":{"account_id":"test-account"},"access_token":"test-value"}`
	config := "model = \"gpt-test\"\n"
	bundle := New(
		[]Profile{
			{ID: "work", Name: "Work", CredentialID: "cred-a", ConfigSetID: "shared"},
			{ID: "personal", Name: "Personal", CredentialID: "cred-b", ConfigSetID: "shared"},
		},
		[]Credential{
			{ID: "cred-b", Kind: codexpreset.CredentialKindAuthJSON, PayloadJSON: auth, PayloadSHA256: digest(auth)},
			{ID: "cred-a", Kind: codexpreset.CredentialKindAuthJSON, PayloadJSON: auth, PayloadSHA256: digest(auth)},
		},
		[]ConfigSet{{ID: "shared", Kind: codexpreset.ConfigSetKindTOML, Name: "Shared", PayloadText: config, PayloadSHA256: digest(config)}},
	)

	first, err := Encode(bundle)
	if err != nil {
		t.Fatalf("expected bundle encode to succeed, got %v", err)
	}
	second, err := Encode(bundle)
	if err != nil {
		t.Fatalf("expected repeated bundle encode to succeed, got %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("expected repeated exports to be byte-identical")
	}
	if strings.Index(string(first), `"id": "personal"`) > strings.Index(string(first), `"id": "work"`) {
		t.Fatalf("expected profiles to use deterministic id ordering: %s", first)
	}

	decoded, err := Decode(first)
	if err != nil {
		t.Fatalf("expected exported bundle to decode, got %v", err)
	}
	if len(decoded.Profiles) != 2 || len(decoded.ConfigSets) != 1 || decoded.Profiles[0].ConfigSetID != decoded.Profiles[1].ConfigSetID {
		t.Fatalf("expected shared Config Set binding to round trip, got %#v", decoded)
	}
}

func TestDecodeRejectsDanglingCredentialsAndDigestChanges(t *testing.T) {
	auth := `{"tokens":{"account_id":"test-account"}}`
	config := "model = \"gpt-test\"\n"
	base := New(
		[]Profile{{ID: "work", Name: "Work", CredentialID: "cred-a", ConfigSetID: "shared"}},
		[]Credential{{ID: "cred-a", Kind: codexpreset.CredentialKindAuthJSON, PayloadJSON: auth, PayloadSHA256: digest(auth)}},
		[]ConfigSet{{ID: "shared", Kind: codexpreset.ConfigSetKindTOML, Name: "Shared", PayloadText: config, PayloadSHA256: digest(config)}},
	)

	base.Credentials = append(base.Credentials, Credential{ID: "cred-unused", Kind: codexpreset.CredentialKindAuthJSON, PayloadJSON: auth, PayloadSHA256: digest(auth)})
	if _, err := Encode(base); err == nil || !strings.Contains(err.Error(), "not referenced") {
		t.Fatalf("expected unreferenced hidden credential to be rejected, got %v", err)
	}

	base.Credentials = base.Credentials[:1]
	base.ConfigSets[0].PayloadText += "approval_policy = \"never\"\n"
	if _, err := Encode(base); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("expected changed payload digest to be rejected, got %v", err)
	}
}

func TestDecodeRejectsUnknownFieldsAndTrailingValues(t *testing.T) {
	for _, raw := range []string{
		`{"format":"profiledeck.codex-profile-bundle","version":1,"provider_id":"codex","preset_version":2,"profiles":[],"credentials":[],"config_sets":[],"unexpected":true}`,
		`{} {}`,
	} {
		if _, err := Decode([]byte(raw)); err == nil {
			t.Fatalf("expected invalid bundle %q to be rejected", raw)
		}
	}
}

func TestValidateRejectsOversizedConfigSetPayload(t *testing.T) {
	auth := `{"tokens":{"account_id":"test-account"}}`
	config := strings.Repeat("#", 16*1024*1024+1)
	bundle := New(
		[]Profile{{ID: "work", Name: "Work", CredentialID: "cred-a", ConfigSetID: "shared"}},
		[]Credential{{ID: "cred-a", Kind: codexpreset.CredentialKindAuthJSON, PayloadJSON: auth, PayloadSHA256: digest(auth)}},
		[]ConfigSet{{ID: "shared", Kind: codexpreset.ConfigSetKindTOML, Name: "Shared", PayloadText: config, PayloadSHA256: digest(config)}},
	)
	if _, err := Encode(bundle); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected oversized Config Set payload to be rejected, got %v", err)
	}
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
