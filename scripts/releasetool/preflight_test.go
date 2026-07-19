package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeCommandRunner struct {
	outputs map[string][]byte
	errors  map[string]error
}

const (
	identityOneFingerprint = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	identityTwoFingerprint = "2222222222222222222222222222222222222222"
)

func (runner fakeCommandRunner) run(_ context.Context, name string, args ...string) ([]byte, error) {
	key := strings.Join(append([]string{name}, args...), "\x00")
	if err := runner.errors[key]; err != nil {
		return nil, err
	}
	return runner.outputs[key], nil
}

func TestParseDeveloperIDIdentities(t *testing.T) {
	t.Parallel()
	output := `
  1) aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa "Developer ID Application: Example One (TEAMONE)"
  2) BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB "Apple Development: Example One (TEAMONE)"
  3) 2222222222222222222222222222222222222222 "Developer ID Application: Example Two (TEAMTWO)"
  4) AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA "Developer ID Application: Example One (TEAMONE)"
     3 valid identities found
`
	identities := parseDeveloperIDIdentities(output)
	if len(identities) != 2 {
		t.Fatalf("identity count = %d", len(identities))
	}
	if identities[0].fingerprint != identityOneFingerprint || identities[0].name != "Developer ID Application: Example One (TEAMONE)" {
		t.Fatalf("first identity = %#v", identities[0])
	}
	if identities[1].fingerprint != identityTwoFingerprint || identities[1].name != "Developer ID Application: Example Two (TEAMTWO)" {
		t.Fatalf("second identity = %#v", identities[1])
	}
}

func TestFormatDeveloperIDIdentitySupportsSigningAndReleaseMetadata(t *testing.T) {
	t.Parallel()
	identity := developerIDIdentity{
		fingerprint: identityOneFingerprint,
		name:        "Developer ID Application: Example One (TEAMONE)",
	}
	if got, err := formatDeveloperIDIdentity(identity, "fingerprint"); err != nil || got != identityOneFingerprint {
		t.Fatalf("fingerprint output = %q, %v", got, err)
	}
	if got, err := formatDeveloperIDIdentity(identity, "name"); err != nil || got != identity.name {
		t.Fatalf("name output = %q, %v", got, err)
	}
	if _, err := formatDeveloperIDIdentity(identity, "json"); err == nil {
		t.Fatal("unsupported identity output was accepted")
	}
}

func TestPromptDeveloperIDIdentityRetriesAndSelects(t *testing.T) {
	t.Parallel()
	identities := []developerIDIdentity{
		{
			fingerprint: identityOneFingerprint,
			name:        "Developer ID Application: Example (TEAMONE)",
		},
		{
			fingerprint: identityTwoFingerprint,
			name:        "Developer ID Application: Example (TEAMONE)",
		},
	}
	input := bytes.NewBufferString("invalid\n3\n2\n")
	var output bytes.Buffer
	identity, err := promptDeveloperIDIdentity(identities, input, &output)
	if err != nil {
		t.Fatal(err)
	}
	if identity.fingerprint != identityTwoFingerprint {
		t.Fatalf("identity = %#v", identity)
	}
	for _, expected := range []string{
		"Multiple Developer ID Application identities were found",
		"AAAAAAAA...AAAAAA",
		"22222222...222222",
		"Enter a number from 1 to 2, or q to cancel.",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("prompt does not contain %q: %q", expected, output.String())
		}
	}
}

func TestPromptDeveloperIDIdentityCanBeCancelled(t *testing.T) {
	t.Parallel()
	identities := []developerIDIdentity{
		{fingerprint: identityOneFingerprint, name: "Developer ID Application: Example One (TEAMONE)"},
		{fingerprint: identityTwoFingerprint, name: "Developer ID Application: Example Two (TEAMTWO)"},
	}
	if _, err := promptDeveloperIDIdentity(identities, strings.NewReader("q\n"), &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatal("identity selection was not cancelled")
	}
	if _, err := promptDeveloperIDIdentity(identities, strings.NewReader(""), &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatal("closed identity selection input was not cancelled")
	}
}

func TestDiscoverIdentityRequiresAnUnambiguousSelection(t *testing.T) {
	t.Parallel()
	key := strings.Join([]string{"security", "find-identity", "-v", "-p", "codesigning"}, "\x00")
	runner := fakeCommandRunner{outputs: map[string][]byte{
		key: []byte(`
1) AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA "Developer ID Application: Example One (TEAMONE)"
2) 2222222222222222222222222222222222222222 "Developer ID Application: Example Two (TEAMTWO)"
`),
	}}
	if _, err := discoverIdentity(context.Background(), runner, "", ""); err == nil || !strings.Contains(err.Error(), "40-character SHA-1 fingerprint") {
		t.Fatal("ambiguous identities were accepted")
	}
	identity, err := discoverIdentity(
		context.Background(),
		runner,
		"Developer ID Application: Example Two (TEAMTWO)",
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	if identity.fingerprint != identityTwoFingerprint {
		t.Fatalf("identity = %#v", identity)
	}
	identity, err = discoverIdentity(context.Background(), runner, strings.ToLower(identityOneFingerprint), "")
	if err != nil {
		t.Fatal(err)
	}
	if identity.fingerprint != identityOneFingerprint {
		t.Fatalf("identity = %#v", identity)
	}
	if _, err := discoverIdentity(context.Background(), runner, "apple-notary", ""); err == nil || !strings.Contains(err.Error(), "security find-identity") {
		t.Fatal("invalid identity selector did not provide recovery guidance")
	}
	runner.errors = map[string]error{key: errors.New("keychain unavailable")}
	if _, err := discoverIdentity(context.Background(), runner, "", ""); err == nil {
		t.Fatal("security failure was ignored")
	}
}

func TestDiscoverIdentityRejectsDuplicateNames(t *testing.T) {
	t.Parallel()
	key := strings.Join([]string{"security", "find-identity", "-v", "-p", "codesigning"}, "\x00")
	const name = "Developer ID Application: Example (TEAMONE)"
	runner := fakeCommandRunner{outputs: map[string][]byte{
		key: []byte(`
1) AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA "Developer ID Application: Example (TEAMONE)"
2) 2222222222222222222222222222222222222222 "Developer ID Application: Example (TEAMONE)"
`),
	}}
	if _, err := discoverIdentity(context.Background(), runner, name, ""); err == nil || !strings.Contains(err.Error(), "matches 2") {
		t.Fatal("duplicate identity name was accepted")
	}
	identity, err := discoverIdentity(context.Background(), runner, identityTwoFingerprint, "")
	if err != nil {
		t.Fatal(err)
	}
	if identity.fingerprint != identityTwoFingerprint || identity.name != name {
		t.Fatalf("identity = %#v", identity)
	}
}

func TestDiscoverIdentityUsesExplicitKeychain(t *testing.T) {
	t.Parallel()
	const keychain = "/tmp/profiledeck-release.keychain-db"
	key := strings.Join(
		[]string{"security", "find-identity", "-v", "-p", "codesigning", keychain},
		"\x00",
	)
	runner := fakeCommandRunner{outputs: map[string][]byte{
		key: []byte(`1) AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA "Developer ID Application: Example (TEAMONE)"`),
	}}
	identity, err := discoverIdentity(context.Background(), runner, "", keychain)
	if err != nil {
		t.Fatal(err)
	}
	if identity.fingerprint != identityOneFingerprint || identity.name != "Developer ID Application: Example (TEAMONE)" {
		t.Fatalf("identity = %#v", identity)
	}
}

func TestVerifySourceStateRequiresTheBuiltCommitAndCleanTree(t *testing.T) {
	t.Parallel()
	commit := "0123456789abcdef0123456789abcdef01234567"
	headKey := strings.Join([]string{"git", "rev-parse", "HEAD"}, "\x00")
	statusKey := strings.Join([]string{"git", "status", "--porcelain"}, "\x00")
	runner := fakeCommandRunner{outputs: map[string][]byte{
		headKey:   []byte(commit + "\n"),
		statusKey: nil,
	}}
	if err := verifySourceState(context.Background(), runner, commit); err != nil {
		t.Fatal(err)
	}

	runner.outputs[headKey] = []byte("ffffffffffffffffffffffffffffffffffffffff\n")
	if err := verifySourceState(context.Background(), runner, commit); err == nil {
		t.Fatal("changed Git HEAD was accepted")
	}

	runner.outputs[headKey] = []byte(commit + "\n")
	runner.outputs[statusKey] = []byte(" M desktop/update/service.go\n")
	if err := verifySourceState(context.Background(), runner, commit); err == nil {
		t.Fatal("dirty source tree was accepted")
	}
}
