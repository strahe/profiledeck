package transferfile

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWriteAndReadPrivateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.json")
	content := []byte(`{"test":"value"}`)
	result, err := WritePrivateAtomic(context.Background(), WriteRequest{Path: path, Content: content, Mode: 0o600, MaxBytes: 1024})
	if err != nil {
		t.Fatalf("expected private write to succeed, got %v", err)
	}
	read, err := ReadPrivate(context.Background(), path, 1024)
	if err != nil {
		t.Fatalf("expected private read to succeed, got %v", err)
	}
	if string(read.Content) != string(content) || read.SHA256 != result.SHA256 {
		t.Fatalf("unexpected private file result: %#v %#v", result, read)
	}
	if runtime.GOOS != "windows" && read.Mode.Perm() != 0o600 {
		t.Fatalf("expected 0600 mode, got %o", read.Mode.Perm())
	}
}

func TestWriteRequiresExplicitOverwriteAndRejectsSymlinks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profiles.json")
	if _, err := WritePrivateAtomic(context.Background(), WriteRequest{Path: path, Content: []byte("one"), Mode: 0o600, MaxBytes: 1024}); err != nil {
		t.Fatalf("expected initial write to succeed, got %v", err)
	}
	if _, err := WritePrivateAtomic(context.Background(), WriteRequest{Path: path, Content: []byte("two"), Mode: 0o600, MaxBytes: 1024}); !errors.Is(err, ErrExists) {
		t.Fatalf("expected existing file error, got %v", err)
	}
	if _, err := WritePrivateAtomic(context.Background(), WriteRequest{Path: path, Content: []byte("two"), Overwrite: true, Mode: 0o600, MaxBytes: 1024}); err != nil {
		t.Fatalf("expected explicit overwrite to succeed, got %v", err)
	}

	symlink := filepath.Join(dir, "profiles-link.json")
	if err := os.Symlink(path, symlink); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := ReadPrivate(context.Background(), symlink, 1024); !errors.Is(err, ErrNotRegular) {
		t.Fatalf("expected symlink read to be rejected, got %v", err)
	}
	if _, err := WritePrivateAtomic(context.Background(), WriteRequest{Path: symlink, Content: []byte("three"), Overwrite: true, Mode: 0o600, MaxBytes: 1024}); !errors.Is(err, ErrNotRegular) {
		t.Fatalf("expected symlink overwrite to be rejected, got %v", err)
	}
}

func TestReadRejectsBroadPermissionsAndOversizedFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.json")
	if err := os.WriteFile(path, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadPrivate(context.Background(), path, 1024); !errors.Is(err, ErrNotPrivate) {
			t.Fatalf("expected broad permissions to be rejected, got %v", err)
		}
		if err := os.Chmod(path, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ReadPrivate(context.Background(), path, 2); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("expected oversized file to be rejected, got %v", err)
	}
}
