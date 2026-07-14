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
		Root:          root,
		Database:      filepath.Join(root, "profiledeck.db"),
		Backups:       filepath.Join(root, "backups"),
		UpdateBackups: filepath.Join(root, "updates", "backups"),
		Exports:       filepath.Join(root, "exports"),
		Logs:          filepath.Join(root, "logs"),
		Lock:          filepath.Join(root, "locks", "switch.lock"),
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
