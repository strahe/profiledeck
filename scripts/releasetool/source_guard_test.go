package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseSourceGuardRequiresTheExactCleanCommit(t *testing.T) {
	t.Parallel()
	repository := t.TempDir()
	runGit(t, repository, "init", "--quiet")
	runGit(t, repository, "config", "user.email", "release-test@profiledeck.invalid")
	runGit(t, repository, "config", "user.name", "ProfileDeck Release Test")
	runGit(t, repository, "config", "commit.gpgsign", "false")
	runGit(t, repository, "config", "core.hooksPath", "/dev/null")

	tracked := filepath.Join(repository, "tracked.txt")
	if err := os.WriteFile(tracked, []byte("clean\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, ".gitignore"), []byte("build/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repository, "add", "tracked.txt", ".gitignore")
	runGit(t, repository, "commit", "--quiet", "-m", "initial")
	commit := strings.TrimSpace(runGit(t, repository, "rev-parse", "HEAD"))

	scriptDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(scriptDirectory, "..", "release", "verify-release-source.sh")
	verify := func(expected string) error {
		command := exec.Command("bash", script, expected)
		command.Dir = repository
		return command.Run()
	}

	if err := verify(commit); err != nil {
		t.Fatalf("clean matching source was rejected: %v", err)
	}
	if err := os.Mkdir(filepath.Join(repository, "build"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "build", "artifact"), []byte("ignored\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verify(commit); err != nil {
		t.Fatalf("ignored build output made the release source dirty: %v", err)
	}
	if err := verify(strings.Repeat("0", 40)); err == nil {
		t.Fatal("mismatched source commit was accepted")
	}
	if err := os.WriteFile(tracked, []byte("modified\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verify(commit); err == nil {
		t.Fatal("tracked source changes were accepted")
	}
	if err := os.WriteFile(tracked, []byte("clean\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "untracked.txt"), []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verify(commit); err == nil {
		t.Fatal("untracked source changes were accepted")
	}
}

func runGit(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = directory
	command.Env = append(os.Environ(), "GIT_DEFAULT_HASH=sha1")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %s: %v", strings.Join(arguments, " "), output, err)
	}
	return string(output)
}
