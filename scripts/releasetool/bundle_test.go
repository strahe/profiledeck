package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAssembleAndVerifyReleaseBundle(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	version, _ := parseReleaseVersion("1.2.3-beta.4")
	handoff := createTestHandoff(t, root, macOSPlatform, version, 17, testReleaseCommit)
	inputs, err := parsePlatformInputs([]string{macOSPlatform + "=" + handoff}, version)
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "bundle")
	if err := assembleRelease(output, version, 17, testReleaseCommit, inputs); err != nil {
		t.Fatal(err)
	}
	definitions, _ := parseReleasePlatforms(macOSPlatform, version)
	metadata, err := verifyReleaseBundle(output, version, 17, testReleaseCommit, definitions)
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata.Platforms) != 1 || metadata.Platforms[0].Platform != macOSPlatform {
		t.Fatalf("unexpected platforms: %#v", metadata.Platforms)
	}
	if err := os.WriteFile(filepath.Join(output, installerDMGName(version)), []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyReleaseBundle(output, version, 17, testReleaseCommit, definitions); err == nil {
		t.Fatal("tampered release bundle passed verification")
	}
}

func TestAssembleReleaseSupportsSyntheticMultiplePlatforms(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	version, _ := parseReleaseVersion("2.0.0-beta.1")
	definitions := []releasePlatformDefinition{
		{Name: "linux", Specs: []releaseAssetSpec{{Name: "ProfileDeck_2.0.0-beta.1_linux_amd64.tar.gz", Role: "archive"}}},
		{Name: "windows", Specs: []releaseAssetSpec{{Name: "ProfileDeck_2.0.0-beta.1_windows_amd64.zip", Role: "archive"}}},
	}
	inputs := make([]releasePlatformInput, 0, len(definitions))
	for _, definition := range definitions {
		directory := createSyntheticHandoff(t, root, definition, version, 23, testReleaseCommit)
		inputs = append(inputs, releasePlatformInput{releasePlatformDefinition: definition, Directory: directory})
	}
	output := filepath.Join(root, "bundle")
	if err := assembleRelease(output, version, 23, testReleaseCommit, inputs); err != nil {
		t.Fatal(err)
	}
	metadata, err := verifyReleaseBundle(output, version, 23, testReleaseCommit, definitions)
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata.Platforms) != 2 || metadata.Platforms[0].Platform != "linux" || metadata.Platforms[1].Platform != "windows" {
		t.Fatalf("unexpected synthetic platforms: %#v", metadata.Platforms)
	}
}

func TestAssembleReleaseRejectsInconsistentHandoffAndExistingOutput(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	version, _ := parseReleaseVersion("1.2.3")
	handoff := createTestHandoff(t, root, macOSPlatform, version, 8, testReleaseCommit)
	inputs, _ := parsePlatformInputs([]string{macOSPlatform + "=" + handoff}, version)
	if err := assembleRelease(filepath.Join(root, "bundle-a"), version, 9, testReleaseCommit, inputs); err == nil {
		t.Fatal("inconsistent build number passed assembly")
	}
	output := filepath.Join(root, "bundle-b")
	if err := os.Mkdir(output, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := assembleRelease(output, version, 8, testReleaseCommit, inputs); err == nil {
		t.Fatal("existing output passed assembly")
	}
}

func TestVerifyReleaseBundleRejectsMetadataConflict(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	version, _ := parseReleaseVersion("1.2.3")
	handoff := createTestHandoff(t, root, macOSPlatform, version, 8, testReleaseCommit)
	inputs, _ := parsePlatformInputs([]string{macOSPlatform + "=" + handoff}, version)
	output := filepath.Join(root, "bundle")
	if err := assembleRelease(output, version, 8, testReleaseCommit, inputs); err != nil {
		t.Fatal(err)
	}
	metadataPath := filepath.Join(output, "release-metadata.json")
	content, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatal(err)
	}
	var metadata releaseBundleMetadata
	if err := decodeStrictJSON(content, &metadata); err != nil {
		t.Fatal(err)
	}
	metadata.Platforms[0].Commit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := writeJSONFile(metadataPath, metadata); err != nil {
		t.Fatal(err)
	}
	definitions, _ := parseReleasePlatforms(macOSPlatform, version)
	if _, err := verifyReleaseBundle(output, version, 8, testReleaseCommit, definitions); err == nil {
		t.Fatal("conflicting platform metadata passed verification")
	}
}

func TestCombinedAssetSpecsRejectsDuplicateAssets(t *testing.T) {
	t.Parallel()
	definitions := []releasePlatformDefinition{
		{Name: "linux", Specs: []releaseAssetSpec{{Name: "ProfileDeck.zip", Role: "archive"}}},
		{Name: "windows", Specs: []releaseAssetSpec{{Name: "ProfileDeck.zip", Role: "archive"}}},
	}
	if _, err := combinedAssetSpecs(definitions); err == nil {
		t.Fatal("asset shared by two platforms was accepted")
	}
}

func createTestHandoff(
	t *testing.T,
	root string,
	platform string,
	version releaseVersion,
	buildNumber int,
	commit string,
) string {
	t.Helper()
	input := filepath.Join(root, "raw-"+platform)
	writeRawPlatformAssets(t, input, version)
	output := filepath.Join(root, "handoff-"+platform)
	if err := createPlatformHandoff(
		input,
		output,
		platform,
		version,
		buildNumber,
		commit,
		time.Date(2026, 7, 22, 12, 30, 0, 0, time.UTC),
	); err != nil {
		t.Fatal(err)
	}
	return output
}

func createSyntheticHandoff(
	t *testing.T,
	root string,
	definition releasePlatformDefinition,
	version releaseVersion,
	buildNumber int,
	commit string,
) string {
	t.Helper()
	directory := filepath.Join(root, "handoff-"+definition.Name)
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, spec := range definition.Specs {
		if err := os.WriteFile(filepath.Join(directory, spec.Name), []byte("synthetic "+definition.Name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := writeChecksums(directory, definition.Specs); err != nil {
		t.Fatal(err)
	}
	if err := writeMetadata(
		directory,
		definition.Name,
		version,
		buildNumber,
		commit,
		time.Date(2026, 7, 22, 12, 30, 0, 0, time.UTC),
		definition.Specs,
	); err != nil {
		t.Fatal(err)
	}
	return directory
}
