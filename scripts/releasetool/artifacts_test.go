package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestChecksumsAndMetadataFailClosed(t *testing.T) {
	t.Parallel()
	version, _ := parseReleaseVersion("1.2.3-beta.4")
	directory := t.TempDir()
	for _, name := range expectedAssetNames(version) {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := writeChecksums(directory, version); err != nil {
		t.Fatal(err)
	}
	const commit = "0123456789abcdef0123456789abcdef01234567"
	builtAt := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	if err := writeMetadata(directory, version, 4, commit, builtAt); err != nil {
		t.Fatal(err)
	}
	metadata, err := readMetadata(directory, version)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Channel != "beta" || metadata.BuildNumber != 4 ||
		metadata.Commit != commit || len(metadata.Assets) != 2 {
		t.Fatalf("unexpected metadata: %#v", metadata)
	}
	if err := os.WriteFile(
		filepath.Join(directory, updaterZIPName(version)),
		[]byte("tampered"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := readMetadata(directory, version); err == nil {
		t.Fatal("tampered asset passed metadata verification")
	}
}

func TestReadChecksumsRejectsMalformedOrMissingEntries(t *testing.T) {
	t.Parallel()
	version, _ := parseReleaseVersion("1.2.3")
	directory := t.TempDir()
	path := filepath.Join(directory, "SHA256SUMS")
	for _, content := range []string{
		"not-a-checksum\n",
		"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef  " +
			updaterZIPName(version) + "\n",
		"0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF  " +
			updaterZIPName(version) + "\n",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readChecksums(path, version); err == nil {
			t.Fatalf("readChecksums accepted %q", content)
		}
	}
}

func TestVerifyZIPLayout(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	valid := filepath.Join(root, "valid.zip")
	writeTestZIP(t, valid, []string{
		"ProfileDeck.app/",
		"ProfileDeck.app/Contents/",
		"ProfileDeck.app/Contents/Info.plist",
	})
	if err := verifyZIPLayout(valid); err != nil {
		t.Fatal(err)
	}
	invalid := filepath.Join(root, "invalid.zip")
	writeTestZIP(t, invalid, []string{
		"ProfileDeck.app/Contents/Info.plist",
		"__MACOSX/metadata",
	})
	if err := verifyZIPLayout(invalid); err == nil {
		t.Fatal("ZIP with __MACOSX passed verification")
	}
}

func writeTestZIP(t *testing.T, path string, names []string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for _, name := range names {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if name[len(name)-1] == '/' {
			continue
		}
		if _, err := entry.Write([]byte("content")); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
