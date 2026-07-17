package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type releasePlatformDefinition struct {
	Name  string
	Specs []releaseAssetSpec
}

type releaseBundleMetadata struct {
	SchemaVersion int               `json:"schema_version"`
	Version       string            `json:"version"`
	Channel       string            `json:"channel"`
	BuildNumber   int               `json:"build_number"`
	Commit        string            `json:"commit"`
	Platforms     []releaseMetadata `json:"platforms"`
	Assets        []releaseAsset    `json:"assets"`
}

func parseReleasePlatforms(value string, version releaseVersion) ([]releasePlatformDefinition, error) {
	if strings.TrimSpace(value) == "" {
		return nil, fmt.Errorf("release platforms are required")
	}
	seen := make(map[string]struct{})
	definitions := make([]releasePlatformDefinition, 0)
	for _, part := range strings.Split(value, ",") {
		platform := strings.TrimSpace(part)
		if _, exists := seen[platform]; exists {
			return nil, fmt.Errorf("release platform %q is duplicated", platform)
		}
		specs, err := platformAssetSpecs(platform, version)
		if err != nil {
			return nil, err
		}
		seen[platform] = struct{}{}
		definitions = append(definitions, releasePlatformDefinition{Name: platform, Specs: specs})
	}
	sort.Slice(definitions, func(left, right int) bool {
		return definitions[left].Name < definitions[right].Name
	})
	return definitions, nil
}

func assembleRelease(
	releasesDirectory string,
	version releaseVersion,
	buildNumber int,
	commit string,
	definitions []releasePlatformDefinition,
) (string, error) {
	if buildNumber < 1 {
		return "", fmt.Errorf("build number must be a positive integer")
	}
	if !commitPattern.MatchString(commit) {
		return "", fmt.Errorf("commit must be a full lowercase Git SHA")
	}
	if len(definitions) == 0 {
		return "", fmt.Errorf("at least one release platform is required")
	}
	bundleDirectory, err := releaseBundlePath(releasesDirectory, version)
	if err != nil {
		return "", err
	}
	versionDirectory := filepath.Dir(bundleDirectory)
	platformsDirectory := filepath.Join(versionDirectory, "platforms")
	if err := verifyPlatformDirectorySet(platformsDirectory, definitions); err != nil {
		return "", err
	}
	stageDirectory := filepath.Join(versionDirectory, ".bundle.in-progress")
	for _, path := range []string{bundleDirectory, stageDirectory} {
		if _, err := os.Lstat(path); err == nil {
			return "", fmt.Errorf("release bundle path already exists: %s", path)
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("inspect release bundle path: %w", err)
		}
	}
	if err := os.Mkdir(stageDirectory, 0o700); err != nil {
		return "", fmt.Errorf("create release bundle staging directory: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(stageDirectory)
		}
	}()

	publicSpecs, err := combinedAssetSpecs(definitions)
	if err != nil {
		return "", err
	}
	platformMetadata := make([]releaseMetadata, 0, len(definitions))
	for _, definition := range definitions {
		platformDirectory := filepath.Join(platformsDirectory, definition.Name)
		if err := verifyDirectoryLayout(platformDirectory, definition.Specs, true); err != nil {
			return "", fmt.Errorf("verify %s release handoff: %w", definition.Name, err)
		}
		metadata, err := readMetadataWithSpecs(
			platformDirectory,
			definition.Name,
			version,
			definition.Specs,
		)
		if err != nil {
			return "", fmt.Errorf("verify %s release metadata: %w", definition.Name, err)
		}
		if metadata.BuildNumber != buildNumber || metadata.Commit != commit {
			return "", fmt.Errorf("%s release metadata does not match the requested build", definition.Name)
		}
		if definition.Name == macOSPlatform {
			if err := verifyZIPLayout(filepath.Join(platformDirectory, updaterZIPName(version))); err != nil {
				return "", err
			}
		}
		for _, spec := range definition.Specs {
			if err := copyReleaseAsset(
				filepath.Join(platformDirectory, spec.Name),
				filepath.Join(stageDirectory, spec.Name),
			); err != nil {
				return "", err
			}
		}
		platformMetadata = append(platformMetadata, metadata)
	}

	assets, err := writeChecksums(stageDirectory, publicSpecs)
	if err != nil {
		return "", err
	}
	bundleMetadata := releaseBundleMetadata{
		SchemaVersion: releaseMetadataSchemaVersion,
		Version:       version.String(),
		Channel:       version.channel(),
		BuildNumber:   buildNumber,
		Commit:        commit,
		Platforms:     platformMetadata,
		Assets:        assets,
	}
	if err := writeJSONFile(filepath.Join(stageDirectory, "release-metadata.json"), bundleMetadata); err != nil {
		return "", err
	}
	if _, err := verifyReleaseBundle(
		stageDirectory,
		version,
		buildNumber,
		commit,
		definitions,
	); err != nil {
		return "", err
	}
	if err := os.Rename(stageDirectory, bundleDirectory); err != nil {
		return "", fmt.Errorf("commit release bundle: %w", err)
	}
	committed = true
	return bundleDirectory, nil
}

func verifyReleaseBundle(
	directory string,
	version releaseVersion,
	buildNumber int,
	commit string,
	definitions []releasePlatformDefinition,
) (releaseBundleMetadata, error) {
	publicSpecs, err := combinedAssetSpecs(definitions)
	if err != nil {
		return releaseBundleMetadata{}, err
	}
	if err := verifyDirectoryLayout(directory, publicSpecs, true); err != nil {
		return releaseBundleMetadata{}, err
	}
	content, err := os.ReadFile(filepath.Join(directory, "release-metadata.json"))
	if err != nil {
		return releaseBundleMetadata{}, fmt.Errorf("read release bundle metadata: %w", err)
	}
	var metadata releaseBundleMetadata
	if err := decodeStrictJSON(content, &metadata); err != nil {
		return releaseBundleMetadata{}, fmt.Errorf("decode release bundle metadata: %w", err)
	}
	if metadata.SchemaVersion != releaseMetadataSchemaVersion ||
		metadata.Version != version.String() || metadata.Channel != version.channel() {
		return releaseBundleMetadata{}, fmt.Errorf("release bundle schema, version, or channel does not match")
	}
	if metadata.BuildNumber != buildNumber || metadata.Commit != commit {
		return releaseBundleMetadata{}, fmt.Errorf("release bundle does not match the requested build")
	}
	if len(metadata.Platforms) != len(definitions) {
		return releaseBundleMetadata{}, fmt.Errorf("release bundle platform set does not match")
	}
	for index, definition := range definitions {
		platformMetadata := metadata.Platforms[index]
		if err := validateMetadata(platformMetadata, definition.Name, version, definition.Specs); err != nil {
			return releaseBundleMetadata{}, err
		}
		if platformMetadata.BuildNumber != buildNumber || platformMetadata.Commit != commit {
			return releaseBundleMetadata{}, fmt.Errorf("%s release metadata does not match the bundle", definition.Name)
		}
	}
	assets, err := verifyChecksums(directory, publicSpecs)
	if err != nil {
		return releaseBundleMetadata{}, err
	}
	if !equalReleaseAssets(metadata.Assets, assets) {
		return releaseBundleMetadata{}, fmt.Errorf("release bundle assets do not match")
	}
	assetByName := make(map[string]releaseAsset, len(assets))
	for _, asset := range assets {
		assetByName[asset.Name] = asset
	}
	for _, platformMetadata := range metadata.Platforms {
		for _, asset := range platformMetadata.Assets {
			if assetByName[asset.Name] != asset {
				return releaseBundleMetadata{}, fmt.Errorf("platform asset %s does not match the release bundle", asset.Name)
			}
		}
	}
	if containsPlatform(definitions, macOSPlatform) {
		if err := verifyZIPLayout(filepath.Join(directory, updaterZIPName(version))); err != nil {
			return releaseBundleMetadata{}, err
		}
	}
	return metadata, nil
}

func verifyPlatformDirectorySet(
	platformsDirectory string,
	definitions []releasePlatformDefinition,
) error {
	entries, err := os.ReadDir(platformsDirectory)
	if err != nil {
		return fmt.Errorf("read platform release directory: %w", err)
	}
	actual := make([]string, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect platform release %s: %w", entry.Name(), err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("platform release is not a directory: %s", entry.Name())
		}
		actual = append(actual, entry.Name())
	}
	expected := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		expected = append(expected, definition.Name)
	}
	sort.Strings(actual)
	sort.Strings(expected)
	// An explicit platform set prevents a missing build from silently producing a partial release.
	if strings.Join(actual, "\n") != strings.Join(expected, "\n") {
		return fmt.Errorf("platform release directory does not match the expected platform set")
	}
	return nil
}

func combinedAssetSpecs(definitions []releasePlatformDefinition) ([]releaseAssetSpec, error) {
	seen := make(map[string]string)
	specs := make([]releaseAssetSpec, 0)
	for _, definition := range definitions {
		if !platformNamePattern.MatchString(definition.Name) || len(definition.Specs) == 0 {
			return nil, fmt.Errorf("release platform definition is invalid")
		}
		for _, spec := range definition.Specs {
			if !checksumLinePattern.MatchString(strings.Repeat("0", 64)+"  "+spec.Name) || spec.Role == "" {
				return nil, fmt.Errorf("release asset definition is invalid")
			}
			if existing, found := seen[spec.Name]; found {
				return nil, fmt.Errorf(
					"release asset %s is shared by platforms %s and %s",
					spec.Name,
					existing,
					definition.Name,
				)
			}
			seen[spec.Name] = definition.Name
			specs = append(specs, spec)
		}
	}
	sort.Slice(specs, func(left, right int) bool {
		return specs[left].Name < specs[right].Name
	})
	return specs, nil
}

func bundleAssetSpecs(
	metadata releaseBundleMetadata,
	definitions []releasePlatformDefinition,
) ([]releaseAssetSpec, error) {
	expected, err := combinedAssetSpecs(definitions)
	if err != nil {
		return nil, err
	}
	actual := make([]releaseAssetSpec, 0, len(metadata.Assets))
	for _, asset := range metadata.Assets {
		actual = append(actual, releaseAssetSpec{Name: asset.Name, Role: asset.Role})
	}
	sort.Slice(actual, func(left, right int) bool {
		return actual[left].Name < actual[right].Name
	})
	if len(actual) != len(expected) {
		return nil, fmt.Errorf("release bundle asset contract does not match the expected platforms")
	}
	for index := range expected {
		if actual[index] != expected[index] {
			return nil, fmt.Errorf("release bundle asset contract does not match the expected platforms")
		}
	}
	return actual, nil
}

func containsPlatform(definitions []releasePlatformDefinition, platform string) bool {
	for _, definition := range definitions {
		if definition.Name == platform {
			return true
		}
	}
	return false
}

func copyReleaseAsset(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return fmt.Errorf("inspect release asset %s: %w", filepath.Base(source), err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("release asset is not a regular file: %s", filepath.Base(source))
	}
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open release asset %s: %w", filepath.Base(source), err)
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("create release asset %s: %w", filepath.Base(destination), err)
	}
	copied := false
	defer func() {
		_ = output.Close()
		if !copied {
			_ = os.Remove(destination)
		}
	}()
	if _, err := io.Copy(output, input); err != nil {
		return fmt.Errorf("copy release asset %s: %w", filepath.Base(source), err)
	}
	if err := output.Close(); err != nil {
		return fmt.Errorf("close release asset %s: %w", filepath.Base(destination), err)
	}
	copied = true
	return nil
}

func removeReleaseBundle(releasesDirectory string, version releaseVersion) error {
	directory, err := releaseBundlePath(releasesDirectory, version)
	if err != nil {
		return err
	}
	info, err := os.Lstat(directory)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect release bundle: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("release bundle path is not a directory: %s", directory)
	}
	if err := os.RemoveAll(directory); err != nil {
		return fmt.Errorf("remove consumed release bundle: %w", err)
	}
	return nil
}
