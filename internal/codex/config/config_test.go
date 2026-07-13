package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/strahe/profiledeck/internal/targetfs"
)

func TestResolveHomeOrder(t *testing.T) {
	explicit := filepath.Join(t.TempDir(), "explicit")
	envHome := filepath.Join(t.TempDir(), "env")
	userHome := t.TempDir()

	t.Setenv("CODEX_HOME", envHome)
	t.Setenv("HOME", userHome)
	t.Setenv("USERPROFILE", userHome)

	got, err := ResolveHome(explicit)
	if err != nil {
		t.Fatalf("expected explicit home to resolve, got %v", err)
	}
	wantExplicit, err := filepath.Abs(explicit)
	if err != nil {
		t.Fatalf("expected abs explicit path, got %v", err)
	}
	if got.Dir != filepath.Clean(wantExplicit) {
		t.Fatalf("expected explicit dir %q, got %q", filepath.Clean(wantExplicit), got.Dir)
	}
	if got.ConfigPath != filepath.Join(got.Dir, ConfigFileName) {
		t.Fatalf("unexpected explicit config path: %#v", got)
	}
	if got.AuthPath != filepath.Join(got.Dir, AuthFileName) {
		t.Fatalf("unexpected explicit auth path: %#v", got)
	}

	got, err = ResolveHome("")
	if err != nil {
		t.Fatalf("expected CODEX_HOME to resolve, got %v", err)
	}
	wantEnv, err := filepath.Abs(envHome)
	if err != nil {
		t.Fatalf("expected abs env path, got %v", err)
	}
	if got.Dir != filepath.Clean(wantEnv) {
		t.Fatalf("expected CODEX_HOME dir %q, got %q", filepath.Clean(wantEnv), got.Dir)
	}

	t.Setenv("CODEX_HOME", "")
	got, err = ResolveHome("")
	if err != nil {
		t.Fatalf("expected fallback home to resolve, got %v", err)
	}
	if got.Dir != filepath.Join(userHome, ".codex") {
		t.Fatalf("expected fallback dir %q, got %q", filepath.Join(userHome, ".codex"), got.Dir)
	}
}

func TestReadSnapshotHandlesMissingConfigAsEmpty(t *testing.T) {
	snapshot, err := ReadSnapshot(filepath.Join(t.TempDir(), ConfigFileName))
	if err != nil {
		t.Fatalf("expected missing config to be accepted, got %v", err)
	}
	if !snapshot.Missing || snapshot.Content != "" {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
}

func TestReadSnapshotRejectsInvalidTOML(t *testing.T) {
	path := filepath.Join(t.TempDir(), ConfigFileName)
	if err := os.WriteFile(path, []byte(`model = "unterminated`), 0o600); err != nil {
		t.Fatalf("expected config setup to succeed, got %v", err)
	}
	if _, err := ReadSnapshot(path); err == nil {
		t.Fatalf("expected invalid TOML snapshot to fail")
	}
}

func TestReadSnapshotRejectsOversizedConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), ConfigFileName)
	if err := os.WriteFile(path, []byte(strings.Repeat("x", targetfs.MaxFileBytes+1)), 0o600); err != nil {
		t.Fatalf("expected config setup to succeed, got %v", err)
	}
	if _, err := ReadSnapshot(path); err == nil {
		t.Fatalf("expected oversized config snapshot to fail")
	}
}
