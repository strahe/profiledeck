package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/strahe/profiledeck/internal/releaseartifact"
)

func TestFinalizeCreatesAndVerifiesChecksums(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	contract := writeReleaseAssets(t, directory, "1.2.3-beta.4")
	var output bytes.Buffer
	if err := run([]string{"finalize", "--version", contract.Version, "--directory", directory}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), directory) {
		t.Fatalf("finalize output = %q", output.String())
	}
	if err := verifyFinalizedDirectory(directory, contract); err != nil {
		t.Fatal(err)
	}
}

func TestFinalizeRejectsInvalidAssetDirectories(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*testing.T, string, releaseartifact.Contract)
	}{
		{
			name: "missing",
			mutate: func(t *testing.T, directory string, contract releaseartifact.Contract) {
				t.Helper()
				name, _ := contract.AssetName(releaseartifact.RoleLinuxRPM)
				if err := os.Remove(filepath.Join(directory, name)); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "extra",
			mutate: func(t *testing.T, directory string, _ releaseartifact.Contract) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(directory, "extra"), []byte("extra"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "empty",
			mutate: func(t *testing.T, directory string, contract releaseartifact.Contract) {
				t.Helper()
				name, _ := contract.AssetName(releaseartifact.RoleLinuxDEB)
				if err := os.WriteFile(filepath.Join(directory, name), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "symlink",
			mutate: func(t *testing.T, directory string, contract releaseartifact.Contract) {
				t.Helper()
				name, _ := contract.AssetName(releaseartifact.RoleLinuxDEB)
				target, _ := contract.AssetName(releaseartifact.RoleLinuxRPM)
				if err := os.Remove(filepath.Join(directory, name)); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, filepath.Join(directory, name)); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "directory",
			mutate: func(t *testing.T, directory string, contract releaseartifact.Contract) {
				t.Helper()
				name, _ := contract.AssetName(releaseartifact.RoleLinuxDEB)
				if err := os.Remove(filepath.Join(directory, name)); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(filepath.Join(directory, name), 0o700); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			directory := t.TempDir()
			contract := writeReleaseAssets(t, directory, "1.2.3")
			test.mutate(t, directory, contract)
			if err := finalizeRelease(directory, contract); err == nil {
				t.Fatal("invalid release directory was finalized")
			}
			if _, err := os.Lstat(filepath.Join(directory, releaseartifact.ChecksumsName)); !os.IsNotExist(err) {
				t.Fatalf("failed finalize left SHA256SUMS: %v", err)
			}
		})
	}
}

func TestFinalizedDirectoryRejectsCorruptOrDuplicateChecksums(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		mutate func(string) error
	}{
		{
			name: "corrupt",
			mutate: func(checksums string) error {
				return os.WriteFile(checksums, []byte(strings.Repeat("0", 64)+"  updates.json\n"), 0o644)
			},
		},
		{
			name: "duplicate",
			mutate: func(checksums string) error {
				content, err := os.ReadFile(checksums)
				if err != nil {
					return err
				}
				first := strings.SplitN(string(content), "\n", 2)[0] + "\n"
				return os.WriteFile(checksums, append(content, []byte(first)...), 0o644)
			},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			directory := t.TempDir()
			contract := writeReleaseAssets(t, directory, "1.2.3")
			if err := finalizeRelease(directory, contract); err != nil {
				t.Fatal(err)
			}
			if err := test.mutate(filepath.Join(directory, releaseartifact.ChecksumsName)); err != nil {
				t.Fatal(err)
			}
			if err := verifyFinalizedDirectory(directory, contract); err == nil {
				t.Fatal("invalid checksums were accepted")
			}
		})
	}
}

func writeReleaseAssets(t *testing.T, directory, version string) releaseartifact.Contract {
	t.Helper()
	contract, err := releaseartifact.NewContract(version)
	if err != nil {
		t.Fatal(err)
	}
	for _, asset := range withoutChecksums(contract.PublicAssets) {
		if err := os.WriteFile(
			filepath.Join(directory, asset.Name),
			[]byte("content for "+asset.Name),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
	}
	return contract
}
