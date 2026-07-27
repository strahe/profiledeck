package runtime

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePaths(t *testing.T) {
	configDir := filepath.Join(string(filepath.Separator), "tmp", "config")

	paths, err := ResolvePaths(configDir)
	if err != nil {
		t.Fatalf("expected paths to resolve, got %v", err)
	}

	root := filepath.Join(configDir, "profiledeck")
	want := Paths{
		Root:       root,
		Database:   filepath.Join(root, "profiledeck.db"),
		Backups:    filepath.Join(root, "backups"),
		Recovery:   filepath.Join(root, "recovery"),
		Logs:       filepath.Join(root, "logs"),
		Lock:       filepath.Join(root, "locks", "switch.lock"),
		DataLock:   filepath.Join(root, "locks", "data.lock"),
		BackupLock: filepath.Join(root, "locks", "backup.lock"),
	}

	if paths != want {
		t.Fatalf("unexpected paths:\nwant: %#v\n got: %#v", want, paths)
	}
}

func TestResolvePathsRejectsEmptyConfigDir(t *testing.T) {
	_, err := ResolvePaths("")
	if err == nil {
		t.Fatalf("expected error for empty config dir")
	}

	if !errors.Is(err, ErrEmptyUserConfigDir) {
		t.Fatalf("expected ErrEmptyUserConfigDir, got %T", err)
	}
}

func TestResolvePathsDoesNotCreateDirectories(t *testing.T) {
	configDir := filepath.Join(t.TempDir(), "config")

	_, err := ResolvePaths(configDir)
	if err != nil {
		t.Fatalf("expected paths to resolve, got %v", err)
	}

	if _, err := os.Stat(configDir); !os.IsNotExist(err) {
		t.Fatalf("expected resolver not to create config dir, stat error: %v", err)
	}
}

func TestEnsureDirectoriesLeavesLegacyExportFilesUntouched(t *testing.T) {
	service, err := NewService(t.TempDir())
	if err != nil {
		t.Fatalf("create runtime service: %v", err)
	}
	legacyExport := filepath.Join(service.Paths().Root, "exports", "saved.json")
	if err := os.MkdirAll(filepath.Dir(legacyExport), 0o700); err != nil {
		t.Fatalf("create legacy export directory: %v", err)
	}
	if err := os.WriteFile(legacyExport, []byte("legacy export\n"), 0o600); err != nil {
		t.Fatalf("write legacy export: %v", err)
	}

	if err := service.EnsureDirectories(); err != nil {
		t.Fatalf("ensure runtime directories: %v", err)
	}
	content, err := os.ReadFile(legacyExport)
	if err != nil || string(content) != "legacy export\n" {
		t.Fatalf("legacy export changed: content=%q err=%v", content, err)
	}
}
