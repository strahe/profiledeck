package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPromoteCandidateDMGReplacesThePreviousCandidate(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	source := filepath.Join(directory, "versioned.dmg")
	candidate := filepath.Join(directory, "bin", "ProfileDeck.dmg")
	if err := os.WriteFile(source, []byte("verified release"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Dir(candidate), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(candidate, []byte("previous release"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := promoteCandidateDMG(source, candidate); err != nil {
		t.Fatal(err)
	}
	if err := verifyCandidateMatchesDMG(candidate, source); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(candidate)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "verified release" {
		t.Fatalf("candidate content = %q", content)
	}
	info, err := os.Stat(candidate)
	if err != nil {
		t.Fatal(err)
	}
	if permissions := info.Mode().Perm(); permissions != 0o644 {
		t.Fatalf("candidate permissions = %o, want 644", permissions)
	}
}

func TestCandidateDMGRejectsUnsafeOrMismatchedInputs(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	source := filepath.Join(directory, "versioned.dmg")
	candidate := filepath.Join(directory, "ProfileDeck.dmg")
	if err := os.WriteFile(source, []byte("verified release"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(candidate, []byte("different release"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyCandidateMatchesDMG(candidate, source); err == nil {
		t.Fatal("mismatched candidate passed verification")
	}
	if err := promoteCandidateDMG(source, filepath.Join(directory, "ProfileDeck.app")); err == nil {
		t.Fatal("non-DMG candidate path was accepted")
	}
	symlink := filepath.Join(directory, "symlink.dmg")
	if err := os.Symlink(candidate, symlink); err != nil {
		t.Fatal(err)
	}
	if err := promoteCandidateDMG(source, symlink); err == nil {
		t.Fatal("symlink candidate was accepted")
	}
}
