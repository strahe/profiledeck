package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeCommandRunner struct {
	outputs map[string][]byte
	errors  map[string]error
}

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
  1) AAA "Developer ID Application: Example One (TEAMONE)"
  2) BBB "Apple Development: Example One (TEAMONE)"
  3) CCC "Developer ID Application: Example Two (TEAMTWO)"
     3 valid identities found
`
	identities := parseDeveloperIDIdentities(output)
	if got := strings.Join(identities, "\n"); got != "Developer ID Application: Example One (TEAMONE)\nDeveloper ID Application: Example Two (TEAMTWO)" {
		t.Fatalf("identities = %q", got)
	}
}

func TestDiscoverIdentityRequiresAnUnambiguousMatch(t *testing.T) {
	t.Parallel()
	key := strings.Join([]string{"security", "find-identity", "-v", "-p", "codesigning"}, "\x00")
	runner := fakeCommandRunner{outputs: map[string][]byte{
		key: []byte(`
1) AAA "Developer ID Application: Example One (TEAMONE)"
2) BBB "Developer ID Application: Example Two (TEAMTWO)"
`),
	}}
	if _, err := discoverIdentity(context.Background(), runner, "", ""); err == nil {
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
	if identity != "Developer ID Application: Example Two (TEAMTWO)" {
		t.Fatalf("identity = %q", identity)
	}
	runner.errors = map[string]error{key: errors.New("keychain unavailable")}
	if _, err := discoverIdentity(context.Background(), runner, "", ""); err == nil {
		t.Fatal("security failure was ignored")
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
		key: []byte(`1) AAA "Developer ID Application: Example (TEAMONE)"`),
	}}
	identity, err := discoverIdentity(context.Background(), runner, "", keychain)
	if err != nil {
		t.Fatal(err)
	}
	if identity != "Developer ID Application: Example (TEAMONE)" {
		t.Fatalf("identity = %q", identity)
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
