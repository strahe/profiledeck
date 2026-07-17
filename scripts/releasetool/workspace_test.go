package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReleaseWorkspacePreparesAndCommitsWithoutOverwriting(t *testing.T) {
	t.Parallel()
	version, _ := parseReleaseVersion("1.2.3-beta.4")
	workspace, err := newReleaseWorkspace(filepath.Join(t.TempDir(), "releases"), version)
	if err != nil {
		t.Fatal(err)
	}
	if err := workspace.prepare(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(workspace.stage)
	if err != nil {
		t.Fatal(err)
	}
	if permissions := info.Mode().Perm(); permissions != 0o700 {
		t.Fatalf("staging permissions = %o, want 700", permissions)
	}
	if err := os.WriteFile(filepath.Join(workspace.artifacts, "asset"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := workspace.commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(workspace.final, "asset")); err != nil {
		t.Fatal(err)
	}
	if err := workspace.commit(); err == nil {
		t.Fatal("second commit succeeded, want no-overwrite error")
	}
	if err := workspace.cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(workspace.final); err != nil {
		t.Fatalf("cleanup removed committed release: %v", err)
	}
}

func TestReleaseWorkspaceRejectsUnsafeRoot(t *testing.T) {
	t.Parallel()
	version, _ := parseReleaseVersion("1.2.3")
	if _, err := newReleaseWorkspace(string(filepath.Separator), version); err == nil {
		t.Fatal("filesystem root was accepted as the releases directory")
	}
}
