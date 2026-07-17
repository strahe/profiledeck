package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNotaryCredentialArgs(t *testing.T) {
	t.Parallel()
	if got := strings.Join(notaryCredentialArgs("apple-notary", ""), " "); got != "--keychain-profile apple-notary" {
		t.Fatalf("default credentials = %q", got)
	}
	if got := strings.Join(
		notaryCredentialArgs("apple-notary", "/tmp/release.keychain-db"),
		" ",
	); got != "--keychain-profile apple-notary --keychain /tmp/release.keychain-db" {
		t.Fatalf("explicit credentials = %q", got)
	}
}

func TestNotarizeUsesExplicitKeychainForSubmissionAndLog(t *testing.T) {
	t.Parallel()
	const (
		input    = "/tmp/ProfileDeck.dmg"
		profile  = "apple-notary"
		keychain = "/tmp/release.keychain-db"
		id       = "01234567-89ab-cdef-0123-456789abcdef"
	)
	submitKey := strings.Join([]string{
		"xcrun", "notarytool", "submit", input,
		"--keychain-profile", profile,
		"--keychain", keychain,
		"--wait", "--output-format", "json",
	}, "\x00")
	logKey := strings.Join([]string{
		"xcrun", "notarytool", "log", id,
		"--keychain-profile", profile,
		"--keychain", keychain,
	}, "\x00")
	runner := &scriptedCommandRunner{results: map[string][]scriptedCommandResult{
		submitKey: {{output: []byte(`{"id":"` + id + `","status":"Invalid","message":"rejected"}`)}},
		logKey:    {{output: []byte(`{"issues":[]}`)}},
	}}
	if _, err := notarize(context.Background(), runner, input, profile, keychain); err == nil {
		t.Fatal("rejected notarization succeeded")
	}
	if len(runner.results[submitKey]) != 0 || len(runner.results[logKey]) != 0 {
		t.Fatal("notarization did not use the explicit Keychain for both submission and log retrieval")
	}
}

func TestParseNotarizationResult(t *testing.T) {
	t.Parallel()
	result, err := parseNotarizationResult([]byte(`{
		"id": "01234567-89ab-cdef-0123-456789abcdef",
		"status": "Accepted",
		"message": "Processing complete",
		"future_field": true
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "Accepted" || result.ID == "" {
		t.Fatalf("unexpected result: %#v", result)
	}
	for _, content := range []string{`{}`, `{"id":"id"}`, `not-json`} {
		if _, err := parseNotarizationResult([]byte(content)); err == nil {
			t.Fatalf("parseNotarizationResult(%q) succeeded, want error", content)
		}
	}
}

func TestWriteInfoPlist(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	templatePath := filepath.Join(root, "Info.plist.tmpl")
	outputPath := filepath.Join(root, "bundle", "Info.plist")
	if err := os.WriteFile(
		templatePath,
		[]byte("<string>@SHORT_VERSION@</string><string>@BUILD_NUMBER@</string>"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	version, _ := parseReleaseVersion("1.2.3-beta.4")
	if err := writeInfoPlist(templatePath, outputPath, version, 7); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(content); !strings.Contains(got, "1.2.3") || !strings.Contains(got, ">7<") {
		t.Fatalf("rendered Info.plist = %q", got)
	}
}
