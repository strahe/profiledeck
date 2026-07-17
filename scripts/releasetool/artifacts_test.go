package main

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestChecksumsAndMetadataFailClosed(t *testing.T) {
	t.Parallel()
	version, _ := parseReleaseVersion("1.2.3-beta.4")
	specs, _ := platformAssetSpecs(macOSPlatform, version)
	directory := t.TempDir()
	for _, spec := range specs {
		if err := os.WriteFile(filepath.Join(directory, spec.Name), []byte(spec.Name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := writeChecksums(directory, specs); err != nil {
		t.Fatal(err)
	}
	const commit = "0123456789abcdef0123456789abcdef01234567"
	builtAt := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	if err := writeMetadata(directory, macOSPlatform, version, 4, commit, builtAt); err != nil {
		t.Fatal(err)
	}
	metadata, err := readMetadata(directory, macOSPlatform, version)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.SchemaVersion != releaseMetadataSchemaVersion ||
		metadata.Platform != macOSPlatform || metadata.Channel != "beta" ||
		metadata.BuildNumber != 4 || metadata.Commit != commit || len(metadata.Assets) != 2 {
		t.Fatalf("unexpected metadata: %#v", metadata)
	}
	for _, asset := range metadata.Assets {
		if asset.Role == "" {
			t.Fatalf("asset role is missing: %#v", asset)
		}
	}
	metadataPath := filepath.Join(directory, "release-metadata.json")
	originalMetadata, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatal(err)
	}
	tamperedMetadata := bytes.Replace(
		originalMetadata,
		[]byte(`"version": "1.2.3-beta.4"`),
		[]byte(`"version": "1.2.4"`),
		1,
	)
	if bytes.Equal(originalMetadata, tamperedMetadata) {
		t.Fatal("metadata fixture did not contain the expected version")
	}
	if err := os.WriteFile(metadataPath, tamperedMetadata, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readMetadata(directory, macOSPlatform, version); err == nil {
		t.Fatal("metadata for another version passed verification")
	}
	if err := os.WriteFile(metadataPath, originalMetadata, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(directory, updaterZIPName(version)),
		[]byte("tampered"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := readMetadata(directory, macOSPlatform, version); err == nil {
		t.Fatal("tampered asset passed metadata verification")
	}
}

func TestReadChecksumsRejectsMalformedOrMissingEntries(t *testing.T) {
	t.Parallel()
	version, _ := parseReleaseVersion("1.2.3")
	specs, _ := platformAssetSpecs(macOSPlatform, version)
	directory := t.TempDir()
	path := filepath.Join(directory, "SHA256SUMS")
	hash := strings.Repeat("0", 64)
	for _, content := range []string{
		"not-a-checksum\n",
		"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef  " +
			updaterZIPName(version) + "\n",
		"0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF  " +
			updaterZIPName(version) + "\n",
		hash + "  " + updaterZIPName(version) + "\n" +
			hash + "  " + updaterZIPName(version) + "\n",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readChecksums(path, specs); err == nil {
			t.Fatalf("readChecksums accepted %q", content)
		}
	}
}

func TestPlatformMetadataRejectsUnknownFieldsAndPlatforms(t *testing.T) {
	t.Parallel()
	version, _ := parseReleaseVersion("1.2.3")
	if _, err := platformAssetSpecs("windows", version); err == nil {
		t.Fatal("unsupported platform was accepted")
	}
	directory := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(directory, "release-metadata.json"),
		[]byte(`{"schema_version":1,"unexpected":true}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := readMetadata(directory, macOSPlatform, version); err == nil {
		t.Fatal("metadata with an unknown field was accepted")
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

func TestVerifyRemoteDirectoryLayoutRejectsExtraFiles(t *testing.T) {
	t.Parallel()
	version, _ := parseReleaseVersion("1.2.3")
	specs, _ := platformAssetSpecs(macOSPlatform, version)
	directory := t.TempDir()
	for _, name := range append(assetSpecNames(specs), "SHA256SUMS") {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := verifyRemoteDirectoryLayout(directory, specs); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "release-metadata.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyRemoteDirectoryLayout(directory, specs); err == nil {
		t.Fatal("downloaded release with an extra file passed verification")
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
