package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
