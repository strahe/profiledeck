package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testReleaseCommit = "0123456789abcdef0123456789abcdef01234567"

func TestCreatePlatformHandoffBuildsVerifiedAtomicOutput(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	version, _ := parseReleaseVersion("1.2.3-beta.4")
	input := filepath.Join(root, "raw")
	writeRawPlatformAssets(t, input, version)
	output := filepath.Join(root, "final", "macos")
	builtAt := time.Date(2026, 7, 22, 12, 30, 0, 0, time.UTC)

	if err := createPlatformHandoff(input, output, macOSPlatform, version, 17, testReleaseCommit, builtAt); err != nil {
		t.Fatal(err)
	}
	specs, _ := platformAssetSpecs(macOSPlatform, version)
	if err := verifyDirectoryLayout(output, specs, true, true); err != nil {
		t.Fatal(err)
	}
	metadata, err := readMetadata(output, macOSPlatform, version, specs)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.BuildNumber != 17 || metadata.Commit != testReleaseCommit || metadata.BuiltAt != builtAt.Format(time.RFC3339) {
		t.Fatalf("unexpected metadata: %#v", metadata)
	}
	if _, err := os.Stat(filepath.Join(input, updaterZIPName(version))); err != nil {
		t.Fatalf("raw input was changed: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(output))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "macos" {
		t.Fatalf("staging output remained after commit: %#v", entries)
	}
}

func TestCreatePlatformHandoffRejectsUnsafeOrUnexpectedInput(t *testing.T) {
	t.Parallel()
	version, _ := parseReleaseVersion("1.2.3")
	builtAt := time.Date(2026, 7, 22, 12, 30, 0, 0, time.UTC)

	tests := []struct {
		name   string
		mutate func(t *testing.T, input string)
	}{
		{
			name: "unexpected file",
			mutate: func(t *testing.T, input string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(input, "extra.txt"), []byte("extra"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "missing asset",
			mutate: func(t *testing.T, input string) {
				t.Helper()
				if err := os.Remove(filepath.Join(input, updaterSignatureName(version))); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "empty asset",
			mutate: func(t *testing.T, input string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(input, installerDMGName(version)), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "symlink asset",
			mutate: func(t *testing.T, input string) {
				t.Helper()
				path := filepath.Join(input, updaterZIPName(version))
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(installerDMGName(version), path); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "symlink directory",
			mutate: func(t *testing.T, input string) {
				t.Helper()
				realInput := input + "-real"
				if err := os.Rename(input, realInput); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(realInput, input); err != nil {
					t.Fatal(err)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			input := filepath.Join(root, "raw")
			writeRawPlatformAssets(t, input, version)
			test.mutate(t, input)
			output := filepath.Join(root, "macos")
			if err := createPlatformHandoff(input, output, macOSPlatform, version, 1, testReleaseCommit, builtAt); err == nil {
				t.Fatal("unsafe input passed handoff creation")
			}
			if _, err := os.Lstat(output); !os.IsNotExist(err) {
				t.Fatalf("failed handoff left final output: %v", err)
			}
		})
	}
}

func TestCreatePlatformHandoffDoesNotOverwriteOutput(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	version, _ := parseReleaseVersion("1.2.3")
	input := filepath.Join(root, "raw")
	writeRawPlatformAssets(t, input, version)
	output := filepath.Join(root, "macos")
	if err := os.Mkdir(output, 0o700); err != nil {
		t.Fatal(err)
	}
	err := createPlatformHandoff(
		input,
		output,
		macOSPlatform,
		version,
		1,
		testReleaseCommit,
		time.Date(2026, 7, 22, 12, 30, 0, 0, time.UTC),
	)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("handoff error = %v, want existing output rejection", err)
	}
}

func writeRawPlatformAssets(t *testing.T, directory string, version releaseVersion) {
	t.Helper()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	specs, err := platformAssetSpecs(macOSPlatform, version)
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range specs {
		if err := os.WriteFile(filepath.Join(directory, spec.Name), []byte("content for "+spec.Name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
