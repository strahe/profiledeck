package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testReleaseCommit = "0123456789abcdef0123456789abcdef01234567"

func TestAssembleReleaseBuildsVerifiedMacOSBundle(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	version, _ := parseReleaseVersion("1.2.3-beta.4")
	definitions, err := parseReleasePlatforms(macOSPlatform, version)
	if err != nil {
		t.Fatal(err)
	}
	writeMacOSPlatformHandoff(t, root, version, 17, testReleaseCommit)

	bundle, err := assembleRelease(root, version, 17, testReleaseCommit, definitions)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := verifyReleaseBundle(
		bundle,
		version,
		17,
		testReleaseCommit,
		definitions,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata.Platforms) != 1 || metadata.Platforms[0].Platform != macOSPlatform {
		t.Fatalf("unexpected platform metadata: %#v", metadata.Platforms)
	}
	assertDirectoryNames(t, bundle, []string{
		installerDMGName(version),
		updaterZIPName(version),
		"SHA256SUMS",
		"release-metadata.json",
	})

	if err := os.WriteFile(
		filepath.Join(bundle, installerDMGName(version)),
		[]byte("tampered"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyReleaseBundle(
		bundle,
		version,
		17,
		testReleaseCommit,
		definitions,
	); err == nil {
		t.Fatal("tampered release bundle passed verification")
	}
}

func TestAssembleReleaseRequiresExactPlatformSet(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	version, _ := parseReleaseVersion("1.2.3")
	definitions, _ := parseReleasePlatforms(macOSPlatform, version)
	writeMacOSPlatformHandoff(t, root, version, 3, testReleaseCommit)
	if err := os.Mkdir(
		filepath.Join(root, version.tag(), "platforms", "unexpected"),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := assembleRelease(root, version, 3, testReleaseCommit, definitions); err == nil {
		t.Fatal("unexpected platform passed release assembly")
	}
}

func TestAssembleReleaseRejectsInconsistentMetadataAndUnsafeAssets(t *testing.T) {
	t.Parallel()
	version, _ := parseReleaseVersion("1.2.3")
	definitions, _ := parseReleasePlatforms(macOSPlatform, version)

	t.Run("build number", func(t *testing.T) {
		root := t.TempDir()
		writeMacOSPlatformHandoff(t, root, version, 9, testReleaseCommit)
		if _, err := assembleRelease(root, version, 10, testReleaseCommit, definitions); err == nil {
			t.Fatal("inconsistent platform build number passed release assembly")
		}
	})

	t.Run("commit", func(t *testing.T) {
		root := t.TempDir()
		writeMacOSPlatformHandoff(
			t,
			root,
			version,
			10,
			"ffffffffffffffffffffffffffffffffffffffff",
		)
		if _, err := assembleRelease(root, version, 10, testReleaseCommit, definitions); err == nil {
			t.Fatal("inconsistent platform commit passed release assembly")
		}
	})

	t.Run("symlink", func(t *testing.T) {
		root := t.TempDir()
		directory := writeMacOSPlatformHandoff(t, root, version, 10, testReleaseCommit)
		asset := filepath.Join(directory, installerDMGName(version))
		if err := os.Remove(asset); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("SHA256SUMS", asset); err != nil {
			t.Fatal(err)
		}
		if _, err := assembleRelease(root, version, 10, testReleaseCommit, definitions); err == nil {
			t.Fatal("symlinked platform asset passed release assembly")
		}
	})
}

func TestAssembleReleaseSupportsSyntheticMultiplePlatforms(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	version, _ := parseReleaseVersion("2.0.0-beta.1")
	definitions := []releasePlatformDefinition{
		{
			Name: "linux",
			Specs: []releaseAssetSpec{{
				Name: "ProfileDeck_2.0.0-beta.1_linux_amd64.tar.gz",
				Role: "archive",
			}},
		},
		{
			Name: "windows",
			Specs: []releaseAssetSpec{{
				Name: "ProfileDeck_2.0.0-beta.1_windows_amd64.zip",
				Role: "archive",
			}},
		},
	}
	for _, definition := range definitions {
		writeSyntheticPlatformHandoff(
			t,
			root,
			version,
			definition,
			23,
			testReleaseCommit,
		)
	}

	bundle, err := assembleRelease(root, version, 23, testReleaseCommit, definitions)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := verifyReleaseBundle(
		bundle,
		version,
		23,
		testReleaseCommit,
		definitions,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := metadata.Platforms[0].Platform + "," + metadata.Platforms[1].Platform; got != "linux,windows" {
		t.Fatalf("platforms = %q", got)
	}
	if len(metadata.Assets) != 2 {
		t.Fatalf("bundle assets = %#v", metadata.Assets)
	}
}

func TestParseReleasePlatformsRejectsUnknownDuplicateAndEmptyPlatforms(t *testing.T) {
	t.Parallel()
	version, _ := parseReleaseVersion("1.2.3")
	for _, value := range []string{"", "macos,macos", "windows", "macos,"} {
		if _, err := parseReleasePlatforms(value, version); err == nil {
			t.Fatalf("parseReleasePlatforms(%q) succeeded, want error", value)
		}
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

func writeMacOSPlatformHandoff(
	t *testing.T,
	root string,
	version releaseVersion,
	buildNumber int,
	commit string,
) string {
	t.Helper()
	directory := filepath.Join(root, version.tag(), "platforms", macOSPlatform)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	writeTestZIP(t, filepath.Join(directory, updaterZIPName(version)), []string{
		"ProfileDeck.app/",
		"ProfileDeck.app/Contents/",
		"ProfileDeck.app/Contents/Info.plist",
	})
	if err := os.WriteFile(
		filepath.Join(directory, installerDMGName(version)),
		[]byte("signed notarized dmg"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	specs, _ := platformAssetSpecs(macOSPlatform, version)
	if _, err := writeChecksums(directory, specs); err != nil {
		t.Fatal(err)
	}
	if err := writeMetadata(
		directory,
		macOSPlatform,
		version,
		buildNumber,
		commit,
		time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC),
	); err != nil {
		t.Fatal(err)
	}
	return directory
}

func writeSyntheticPlatformHandoff(
	t *testing.T,
	root string,
	version releaseVersion,
	definition releasePlatformDefinition,
	buildNumber int,
	commit string,
) {
	t.Helper()
	directory := filepath.Join(root, version.tag(), "platforms", definition.Name)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, spec := range definition.Specs {
		if err := os.WriteFile(filepath.Join(directory, spec.Name), []byte(spec.Name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := writeChecksums(directory, definition.Specs); err != nil {
		t.Fatal(err)
	}
	if err := writeMetadataWithSpecs(
		directory,
		definition.Name,
		version,
		buildNumber,
		commit,
		time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC),
		definition.Specs,
	); err != nil {
		t.Fatal(err)
	}
}

func assertDirectoryNames(t *testing.T, directory string, expected []string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	actual := make([]string, 0, len(entries))
	for _, entry := range entries {
		actual = append(actual, entry.Name())
	}
	if strings.Join(actual, "\n") != strings.Join(expected, "\n") {
		t.Fatalf("directory entries = %q, want %q", actual, expected)
	}
}
