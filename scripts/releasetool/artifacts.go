package main

import (
	"archive/zip"
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	releaseMetadataSchemaVersion = 1
	macOSPlatform                = "macos"
	assetRoleUpdater             = "updater"
	assetRoleInstaller           = "installer"
)

var (
	checksumLinePattern = regexp.MustCompile(`^([0-9a-f]{64})  ([A-Za-z0-9._-]+)$`)
	platformNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
)

type releaseAssetSpec struct {
	Name string
	Role string
}

type releaseAsset struct {
	Name   string `json:"name"`
	Role   string `json:"role"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type releaseMetadata struct {
	SchemaVersion int            `json:"schema_version"`
	Platform      string         `json:"platform"`
	Version       string         `json:"version"`
	Channel       string         `json:"channel"`
	BuildNumber   int            `json:"build_number"`
	Commit        string         `json:"commit"`
	BuiltAt       string         `json:"built_at"`
	Assets        []releaseAsset `json:"assets"`
}

func platformAssetSpecs(platform string, version releaseVersion) ([]releaseAssetSpec, error) {
	if !platformNamePattern.MatchString(platform) {
		return nil, fmt.Errorf("release platform is invalid")
	}
	var specs []releaseAssetSpec
	switch platform {
	case macOSPlatform:
		specs = []releaseAssetSpec{
			{Name: updaterZIPName(version), Role: assetRoleUpdater},
			{Name: installerDMGName(version), Role: assetRoleInstaller},
		}
	default:
		return nil, fmt.Errorf("unsupported release platform %q", platform)
	}
	sort.Slice(specs, func(left, right int) bool {
		return specs[left].Name < specs[right].Name
	})
	return specs, nil
}

func assetSpecNames(specs []releaseAssetSpec) []string {
	names := make([]string, 0, len(specs))
	for _, spec := range specs {
		names = append(names, spec.Name)
	}
	sort.Strings(names)
	return names
}

func hashFile(path string, spec releaseAssetSpec) (releaseAsset, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return releaseAsset{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return releaseAsset{}, fmt.Errorf("release asset is not a regular file: %s", spec.Name)
	}
	file, err := os.Open(path)
	if err != nil {
		return releaseAsset{}, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return releaseAsset{}, err
	}
	if size < 1 {
		return releaseAsset{}, fmt.Errorf("release asset is empty: %s", spec.Name)
	}
	return releaseAsset{
		Name:   spec.Name,
		Role:   spec.Role,
		SHA256: hex.EncodeToString(hash.Sum(nil)),
		Size:   size,
	}, nil
}

func writeChecksums(directory string, specs []releaseAssetSpec) ([]releaseAsset, error) {
	assets := make([]releaseAsset, 0, len(specs))
	for _, spec := range specs {
		asset, err := hashFile(filepath.Join(directory, spec.Name), spec)
		if err != nil {
			return nil, fmt.Errorf("hash %s: %w", spec.Name, err)
		}
		assets = append(assets, asset)
	}
	var content strings.Builder
	for _, asset := range assets {
		fmt.Fprintf(&content, "%s  %s\n", asset.SHA256, asset.Name)
	}
	path := filepath.Join(directory, "SHA256SUMS")
	if err := os.WriteFile(path, []byte(content.String()), 0o644); err != nil {
		return nil, fmt.Errorf("write SHA256SUMS: %w", err)
	}
	return assets, nil
}

func readChecksums(path string, specs []releaseAssetSpec) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open SHA256SUMS: %w", err)
	}
	defer file.Close()
	checksums := make(map[string]string, len(specs))
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			return nil, fmt.Errorf("SHA256SUMS contains an empty line")
		}
		matches := checksumLinePattern.FindStringSubmatch(line)
		if matches == nil {
			return nil, fmt.Errorf("SHA256SUMS contains an invalid entry")
		}
		if _, exists := checksums[matches[2]]; exists {
			return nil, fmt.Errorf("SHA256SUMS contains duplicate asset %s", matches[2])
		}
		checksums[matches[2]] = matches[1]
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read SHA256SUMS: %w", err)
	}
	expected := assetSpecNames(specs)
	if len(checksums) != len(expected) {
		return nil, fmt.Errorf("SHA256SUMS asset count does not match the release contract")
	}
	for _, name := range expected {
		if _, ok := checksums[name]; !ok {
			return nil, fmt.Errorf("SHA256SUMS is missing %s", name)
		}
	}
	return checksums, nil
}

func verifyChecksums(directory string, specs []releaseAssetSpec) ([]releaseAsset, error) {
	checksums, err := readChecksums(filepath.Join(directory, "SHA256SUMS"), specs)
	if err != nil {
		return nil, err
	}
	assets := make([]releaseAsset, 0, len(specs))
	for _, spec := range specs {
		asset, err := hashFile(filepath.Join(directory, spec.Name), spec)
		if err != nil {
			return nil, fmt.Errorf("hash %s: %w", spec.Name, err)
		}
		if checksums[spec.Name] != asset.SHA256 {
			return nil, fmt.Errorf("SHA-256 mismatch for %s", spec.Name)
		}
		assets = append(assets, asset)
	}
	return assets, nil
}

func writeMetadata(
	directory string,
	platform string,
	version releaseVersion,
	buildNumber int,
	commit string,
	builtAt time.Time,
) error {
	specs, err := platformAssetSpecs(platform, version)
	if err != nil {
		return err
	}
	return writeMetadataWithSpecs(
		directory,
		platform,
		version,
		buildNumber,
		commit,
		builtAt,
		specs,
	)
}

func writeMetadataWithSpecs(
	directory string,
	platform string,
	version releaseVersion,
	buildNumber int,
	commit string,
	builtAt time.Time,
	specs []releaseAssetSpec,
) error {
	if buildNumber < 1 {
		return fmt.Errorf("build number must be a positive integer")
	}
	if !commitPattern.MatchString(commit) {
		return fmt.Errorf("commit must be a full lowercase Git SHA")
	}
	assets, err := verifyChecksums(directory, specs)
	if err != nil {
		return err
	}
	metadata := releaseMetadata{
		SchemaVersion: releaseMetadataSchemaVersion,
		Platform:      platform,
		Version:       version.String(),
		Channel:       version.channel(),
		BuildNumber:   buildNumber,
		Commit:        commit,
		BuiltAt:       builtAt.UTC().Format(time.RFC3339),
		Assets:        assets,
	}
	return writeJSONFile(filepath.Join(directory, "release-metadata.json"), metadata)
}

func readMetadata(directory, platform string, version releaseVersion) (releaseMetadata, error) {
	specs, err := platformAssetSpecs(platform, version)
	if err != nil {
		return releaseMetadata{}, err
	}
	return readMetadataWithSpecs(directory, platform, version, specs)
}

func readMetadataWithSpecs(
	directory string,
	platform string,
	version releaseVersion,
	specs []releaseAssetSpec,
) (releaseMetadata, error) {
	content, err := os.ReadFile(filepath.Join(directory, "release-metadata.json"))
	if err != nil {
		return releaseMetadata{}, fmt.Errorf("read release metadata: %w", err)
	}
	var metadata releaseMetadata
	if err := decodeStrictJSON(content, &metadata); err != nil {
		return releaseMetadata{}, fmt.Errorf("decode release metadata: %w", err)
	}
	if err := validateMetadata(metadata, platform, version, specs); err != nil {
		return releaseMetadata{}, err
	}
	assets, err := verifyChecksums(directory, specs)
	if err != nil {
		return releaseMetadata{}, err
	}
	if !equalReleaseAssets(metadata.Assets, assets) {
		return releaseMetadata{}, fmt.Errorf("release metadata assets do not match")
	}
	return metadata, nil
}

func validateMetadata(
	metadata releaseMetadata,
	platform string,
	version releaseVersion,
	specs []releaseAssetSpec,
) error {
	if metadata.SchemaVersion != releaseMetadataSchemaVersion || metadata.Platform != platform {
		return fmt.Errorf("release metadata schema or platform does not match")
	}
	if metadata.Version != version.String() || metadata.Channel != version.channel() {
		return fmt.Errorf("release metadata version or channel does not match")
	}
	if metadata.BuildNumber < 1 || !commitPattern.MatchString(metadata.Commit) {
		return fmt.Errorf("release metadata build number or commit is invalid")
	}
	if parsed, err := time.Parse(time.RFC3339, metadata.BuiltAt); err != nil ||
		parsed.Format(time.RFC3339) != metadata.BuiltAt {
		return fmt.Errorf("release metadata build time is invalid")
	}
	if len(metadata.Assets) != len(specs) {
		return fmt.Errorf("release metadata assets do not match")
	}
	for index, spec := range specs {
		asset := metadata.Assets[index]
		if asset.Name != spec.Name || asset.Role != spec.Role || asset.Size < 1 ||
			!checksumLinePattern.MatchString(asset.SHA256+"  "+asset.Name) {
			return fmt.Errorf("release metadata asset %s is invalid", spec.Name)
		}
	}
	return nil
}

func verifyDirectoryLayout(directory string, specs []releaseAssetSpec, includeMetadata bool) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("read release directory: %w", err)
	}
	actual := make([]string, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect release directory entry %s: %w", entry.Name(), err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("release directory entry is not a regular file: %s", entry.Name())
		}
		actual = append(actual, entry.Name())
	}
	sort.Strings(actual)
	expected := append(assetSpecNames(specs), "SHA256SUMS")
	if includeMetadata {
		expected = append(expected, "release-metadata.json")
	}
	sort.Strings(expected)
	if strings.Join(actual, "\n") != strings.Join(expected, "\n") {
		return fmt.Errorf("release directory contents do not match the release contract")
	}
	return nil
}

func verifyPlatformDirectoryLayout(directory, platform string, version releaseVersion) error {
	specs, err := platformAssetSpecs(platform, version)
	if err != nil {
		return err
	}
	return verifyDirectoryLayout(directory, specs, true)
}

func verifyRemoteDirectoryLayout(directory string, specs []releaseAssetSpec) error {
	return verifyDirectoryLayout(directory, specs, false)
}

func verifyZIPLayout(path string) error {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Errorf("open updater ZIP: %w", err)
	}
	defer reader.Close()
	if len(reader.File) == 0 {
		return fmt.Errorf("updater ZIP is empty")
	}
	for _, file := range reader.File {
		name := strings.TrimPrefix(file.Name, "./")
		clean := filepath.ToSlash(filepath.Clean(name))
		if clean == "." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
			return fmt.Errorf("updater ZIP contains an unsafe path")
		}
		if clean == "__MACOSX" || strings.HasPrefix(clean, "__MACOSX/") {
			return fmt.Errorf("updater ZIP contains a top-level __MACOSX directory")
		}
		if clean != "ProfileDeck.app" && !strings.HasPrefix(clean, "ProfileDeck.app/") {
			return fmt.Errorf("updater ZIP must contain exactly one top-level ProfileDeck.app")
		}
	}
	return nil
}

func writeJSONFile(path string, value any) error {
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode JSON: %w", err)
	}
	content = append(content, '\n')
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	return nil
}

func decodeStrictJSON(content []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(content)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return fmt.Errorf("JSON contains trailing content")
	}
	return nil
}

func equalReleaseAssets(left, right []releaseAsset) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
