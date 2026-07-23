package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type releasePlatformDefinition struct {
	Name  string
	Specs []releaseAssetSpec
}

type releasePlatformInput struct {
	releasePlatformDefinition
	Directory string
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
	sort.Slice(definitions, func(left, right int) bool { return definitions[left].Name < definitions[right].Name })
	return definitions, nil
}

func parsePlatformInputs(values []string, version releaseVersion) ([]releasePlatformInput, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("at least one platform handoff is required")
	}
	seen := make(map[string]struct{})
	inputs := make([]releasePlatformInput, 0, len(values))
	for _, value := range values {
		platform, directory, found := strings.Cut(value, "=")
		platform = strings.TrimSpace(platform)
		directory = strings.TrimSpace(directory)
		if !found || directory == "" {
			return nil, fmt.Errorf("handoff must use platform=directory")
		}
		if _, exists := seen[platform]; exists {
			return nil, fmt.Errorf("release platform %q is duplicated", platform)
		}
		specs, err := platformAssetSpecs(platform, version)
		if err != nil {
			return nil, err
		}
		seen[platform] = struct{}{}
		inputs = append(inputs, releasePlatformInput{
			releasePlatformDefinition: releasePlatformDefinition{Name: platform, Specs: specs},
			Directory:                 directory,
		})
	}
	sort.Slice(inputs, func(left, right int) bool { return inputs[left].Name < inputs[right].Name })
	return inputs, nil
}

func assembleRelease(
	output string,
	version releaseVersion,
	buildNumber int,
	commit string,
	inputs []releasePlatformInput,
) error {
	if output == "" {
		return fmt.Errorf("release bundle output directory is required")
	}
	if buildNumber < 1 || !commitPattern.MatchString(commit) {
		return fmt.Errorf("release build number or commit is invalid")
	}
	definitions := definitionsFromInputs(inputs)
	publicSpecs, err := combinedAssetSpecs(definitions)
	if err != nil {
		return err
	}
	output, stage, err := prepareAtomicDirectory(output)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(stage)
		}
	}()
	platformMetadata := make([]releaseMetadata, 0, len(inputs))
	for _, input := range inputs {
		if err := verifyDirectoryLayout(input.Directory, input.Specs, true, true); err != nil {
			return fmt.Errorf("verify %s release handoff: %w", input.Name, err)
		}
		metadata, err := readMetadata(input.Directory, input.Name, version, input.Specs)
		if err != nil {
			return fmt.Errorf("verify %s release metadata: %w", input.Name, err)
		}
		if metadata.BuildNumber != buildNumber || metadata.Commit != commit {
			return fmt.Errorf("%s release metadata does not match the requested build", input.Name)
		}
		for _, spec := range input.Specs {
			if err := copyReleaseAsset(filepath.Join(input.Directory, spec.Name), filepath.Join(stage, spec.Name)); err != nil {
				return err
			}
		}
		platformMetadata = append(platformMetadata, metadata)
	}
	assets, err := writeChecksums(stage, publicSpecs)
	if err != nil {
		return err
	}
	metadata := releaseBundleMetadata{
		SchemaVersion: releaseMetadataSchemaVersion,
		Version:       version.String(),
		Channel:       version.channel(),
		BuildNumber:   buildNumber,
		Commit:        commit,
		Platforms:     platformMetadata,
		Assets:        assets,
	}
	if err := writeJSONFile(filepath.Join(stage, "release-metadata.json"), metadata); err != nil {
		return err
	}
	if _, err := verifyReleaseBundle(stage, version, buildNumber, commit, definitions); err != nil {
		return err
	}
	if err := commitAtomicDirectory(stage, output); err != nil {
		return err
	}
	committed = true
	return nil
}

func verifyReleaseBundle(
	directory string,
	version releaseVersion,
	buildNumber int,
	commit string,
	definitions []releasePlatformDefinition,
) (releaseBundleMetadata, error) {
	if directory == "" {
		return releaseBundleMetadata{}, fmt.Errorf("release bundle directory is required")
	}
	publicSpecs, err := combinedAssetSpecs(definitions)
	if err != nil {
		return releaseBundleMetadata{}, err
	}
	if err := verifyDirectoryLayout(directory, publicSpecs, true, true); err != nil {
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
	if metadata.SchemaVersion != releaseMetadataSchemaVersion || metadata.Version != version.String() ||
		metadata.Channel != version.channel() || metadata.BuildNumber != buildNumber || metadata.Commit != commit {
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
	return metadata, nil
}

func definitionsFromInputs(inputs []releasePlatformInput) []releasePlatformDefinition {
	definitions := make([]releasePlatformDefinition, 0, len(inputs))
	for _, input := range inputs {
		definitions = append(definitions, input.releasePlatformDefinition)
	}
	return definitions
}

func combinedAssetSpecs(definitions []releasePlatformDefinition) ([]releaseAssetSpec, error) {
	if len(definitions) == 0 {
		return nil, fmt.Errorf("at least one release platform is required")
	}
	seenPlatforms := make(map[string]struct{})
	seenAssets := make(map[string]string)
	specs := make([]releaseAssetSpec, 0)
	for _, definition := range definitions {
		if !platformNamePattern.MatchString(definition.Name) || len(definition.Specs) == 0 {
			return nil, fmt.Errorf("release platform definition is invalid")
		}
		if _, exists := seenPlatforms[definition.Name]; exists {
			return nil, fmt.Errorf("release platform %q is duplicated", definition.Name)
		}
		seenPlatforms[definition.Name] = struct{}{}
		for _, spec := range definition.Specs {
			if !checksumLinePattern.MatchString(strings.Repeat("0", 64)+"  "+spec.Name) || spec.Role == "" {
				return nil, fmt.Errorf("release asset definition is invalid")
			}
			if existing, found := seenAssets[spec.Name]; found {
				return nil, fmt.Errorf("release asset %s is shared by platforms %s and %s", spec.Name, existing, definition.Name)
			}
			seenAssets[spec.Name] = definition.Name
			specs = append(specs, spec)
		}
	}
	sort.Slice(specs, func(left, right int) bool { return specs[left].Name < specs[right].Name })
	return specs, nil
}
