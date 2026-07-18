package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type failingWriter struct {
	err error
}

func (writer failingWriter) Write([]byte) (int, error) {
	return 0, writer.err
}

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
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if _, err := notarizeWithWriters(
		context.Background(),
		runner,
		input,
		profile,
		keychain,
		&stdout,
		&stderr,
	); err == nil {
		t.Fatal("rejected notarization succeeded")
	}
	if len(runner.results[submitKey]) != 0 || len(runner.results[logKey]) != 0 {
		t.Fatal("notarization did not use the explicit Keychain for both submission and log retrieval")
	}
}

func TestNotarizeDoesNotExposeSubmissionOrCommandDetails(t *testing.T) {
	t.Parallel()
	const (
		input      = "/private/tmp/ProfileDeck.dmg"
		profile    = "apple-notary"
		keychain   = "/private/tmp/release.keychain-db"
		id         = "01234567-89ab-cdef-0123-456789abcdef"
		rawSecret  = "private-notary-secret"
		rawSummary = "private status summary"
		identity   = "Developer ID Application: Example Person (TEAMID1234)"
		privateDir = "/Users/Example Person/private/ProfileDeck.app/Contents/MacOS/ProfileDeck"
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

	t.Run("accepted", func(t *testing.T) {
		runner := &scriptedCommandRunner{results: map[string][]scriptedCommandResult{
			submitKey: {{output: []byte(`{"id":"` + id + `","status":"Accepted"}`)}},
		}}
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if _, err := notarizeWithWriters(
			context.Background(),
			runner,
			input,
			profile,
			keychain,
			&stdout,
			&stderr,
		); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(stdout.String(), id) || stderr.Len() != 0 {
			t.Fatalf("unsafe accepted output: stdout=%q stderr=%q", stdout.String(), stderr.String())
		}
	})

	t.Run("rejected", func(t *testing.T) {
		log := `{
			"jobId":"` + id + `",
			"status":"Invalid",
			"statusSummary":"` + rawSummary + `: signing failed for ` + identity + ` (` + id + `)",
			"issues":[{
				"severity":"error",
				"code":"invalid.signature",
				"path":"` + privateDir + `",
				"message":"` + privateDir + ` used ` + identity + `; password=` + rawSecret + `; ANTHROPIC_AUTH_TOKEN=` + rawSecret + `; OPENAI_API_KEY=` + rawSecret + `; bearer ` + rawSecret + `; owner@example.com; request ` + id + `",
				"architecture":"arm64"
			}]
		}`
		runner := &scriptedCommandRunner{results: map[string][]scriptedCommandResult{
			submitKey: {{output: []byte(`{"id":"` + id + `","status":"Invalid"}`)}},
			logKey:    {{output: []byte(log)}},
		}}
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		_, err := notarizeWithWriters(
			context.Background(),
			runner,
			input,
			profile,
			keychain,
			&stdout,
			&stderr,
		)
		if err == nil {
			t.Fatal("rejected notarization succeeded")
		}
		combined := stdout.String() + stderr.String() + err.Error()
		for _, leaked := range []string{id, rawSecret, rawSummary, identity, "TEAMID1234", "/Users/Example Person", "owner@example.com"} {
			if strings.Contains(combined, leaked) {
				t.Fatalf("notarization output leaked %q: %s", leaked, combined)
			}
		}
		for _, expected := range []string{"Invalid", "invalid.signature", "arm64", "ProfileDeck", "password=[REDACTED]"} {
			if !strings.Contains(combined, expected) {
				t.Fatalf("safe notarization output is missing %q: %s", expected, combined)
			}
		}
	})

	t.Run("command failure", func(t *testing.T) {
		runner := &scriptedCommandRunner{results: map[string][]scriptedCommandResult{
			submitKey: {{err: errors.New("request " + id + " failed with " + rawSecret)}},
		}}
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		_, err := notarizeWithWriters(
			context.Background(),
			runner,
			input,
			profile,
			keychain,
			&stdout,
			&stderr,
		)
		if err == nil {
			t.Fatal("failed command succeeded")
		}
		if combined := stdout.String() + stderr.String() + err.Error(); strings.Contains(combined, id) || strings.Contains(combined, rawSecret) {
			t.Fatalf("raw command failure leaked: %s", combined)
		}
	})

	t.Run("unsafe log fallback", func(t *testing.T) {
		for name, logResult := range map[string]scriptedCommandResult{
			"malformed JSON":  {output: []byte("not-json " + id + " " + rawSecret)},
			"command failure": {err: errors.New("request " + id + " failed with " + rawSecret)},
		} {
			logResult := logResult
			t.Run(name, func(t *testing.T) {
				runner := &scriptedCommandRunner{results: map[string][]scriptedCommandResult{
					submitKey: {{output: []byte(`{"id":"` + id + `","status":"Invalid"}`)}},
					logKey:    {logResult},
				}}
				var stdout bytes.Buffer
				var stderr bytes.Buffer
				_, err := notarizeWithWriters(
					context.Background(),
					runner,
					input,
					profile,
					keychain,
					&stdout,
					&stderr,
				)
				if err == nil {
					t.Fatal("rejected notarization succeeded")
				}
				combined := stdout.String() + stderr.String() + err.Error()
				if strings.Contains(combined, id) || strings.Contains(combined, rawSecret) {
					t.Fatalf("unsafe notarization log fallback: %s", combined)
				}
			})
		}
	})

	t.Run("writer failure", func(t *testing.T) {
		runner := &scriptedCommandRunner{results: map[string][]scriptedCommandResult{
			submitKey: {{output: []byte(`{"id":"` + id + `","status":"Accepted"}`)}},
		}}
		_, err := notarizeWithWriters(
			context.Background(),
			runner,
			input,
			profile,
			keychain,
			failingWriter{err: errors.New(rawSecret)},
			&bytes.Buffer{},
		)
		if err == nil || strings.Contains(err.Error(), rawSecret) {
			t.Fatalf("unsafe writer failure: %v", err)
		}
	})
}

func TestParseNotarizationLogRejectsRawOrEmptyContent(t *testing.T) {
	t.Parallel()
	for _, content := range []string{`{}`, `not-json`, `{"jobId":"hidden"}`} {
		if _, err := parseNotarizationLog([]byte(content)); err == nil {
			t.Fatalf("parseNotarizationLog(%q) succeeded", content)
		}
	}
	log, err := parseNotarizationLog([]byte(`{"status":"Invalid","issues":[]}`))
	if err != nil || log.Status != "Invalid" {
		t.Fatalf("valid notarization log = %#v, %v", log, err)
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
