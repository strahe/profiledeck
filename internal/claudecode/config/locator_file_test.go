//go:build linux || windows

package config

import (
	"path/filepath"
	"testing"
)

func TestResolveLocatorUsesClaudeConfigDirAndOfficialFilename(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", root)
	locator, err := ResolveLocator()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, CredentialsFile)
	if locator.Storage != StorageFile || locator.Path != want || locator.Service != "" || locator.Account != "" {
		t.Fatalf("ResolveLocator() = %#v, want file %q", locator, want)
	}
}
