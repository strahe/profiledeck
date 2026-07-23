package main

import (
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

const releaseMetadataSchemaVersion = 1

var (
	checksumLinePattern = regexp.MustCompile(`^([0-9a-f]{64})  ([A-Za-z0-9._-]+)$`)
	platformNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	commitPattern       = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

type releaseAssetSpec struct {
	Name string `json:"name"`
	Role string `json:"role"`
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

func createPlatformHandoff(
	input string,
	output string,
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
	if input == "" || output == "" {
		return fmt.Errorf("handoff input and output directories are required")
	}
	if !commitPattern.MatchString(commit) {
		return fmt.Errorf("commit must be a full lowercase Git SHA")
	}
	if err := verifyDirectoryLayout(input, specs, false, false); err != nil {
		return fmt.Errorf("verify raw platform assets: %w", err)
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
	for _, spec := range specs {
		if err := copyReleaseAsset(filepath.Join(input, spec.Name), filepath.Join(stage, spec.Name)); err != nil {
			return err
		}
	}
	if _, err := writeChecksums(stage, specs); err != nil {
		return err
	}
	if err := writeMetadata(stage, platform, version, buildNumber, commit, builtAt, specs); err != nil {
		return err
	}
	if _, err := readMetadata(stage, platform, version, specs); err != nil {
		return err
	}
	if err := commitAtomicDirectory(stage, output); err != nil {
		return err
	}
	committed = true
	return nil
}

func prepareAtomicDirectory(output string) (string, string, error) {
	absolute, err := filepath.Abs(output)
	if err != nil {
		return "", "", fmt.Errorf("resolve output directory: %w", err)
	}
	absolute = filepath.Clean(absolute)
	if absolute == string(filepath.Separator) {
		return "", "", fmt.Errorf("output directory cannot be the filesystem root")
	}
	if _, err := os.Lstat(absolute); err == nil {
		return "", "", fmt.Errorf("release output already exists: %s", absolute)
	} else if !os.IsNotExist(err) {
		return "", "", fmt.Errorf("inspect release output: %w", err)
	}
	parent := filepath.Dir(absolute)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", "", fmt.Errorf("create release output parent: %w", err)
	}
	stage, err := os.MkdirTemp(parent, "."+filepath.Base(absolute)+".in-progress-")
	if err != nil {
		return "", "", fmt.Errorf("create release output staging directory: %w", err)
	}
	return absolute, stage, nil
}

func commitAtomicDirectory(stage, output string) error {
	if _, err := os.Lstat(output); err == nil {
		return fmt.Errorf("release output already exists: %s", output)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect release output: %w", err)
	}
	// A completed handoff appears at its final path only after every contract check passes.
	if err := os.Rename(stage, output); err != nil {
		return fmt.Errorf("commit release output: %w", err)
	}
	return nil
}

func hashFile(path string, spec releaseAssetSpec) (releaseAsset, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return releaseAsset{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() < 1 {
		return releaseAsset{}, fmt.Errorf("release asset must be a non-empty regular file: %s", spec.Name)
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
	return releaseAsset{
		Name: spec.Name, Role: spec.Role, SHA256: hex.EncodeToString(hash.Sum(nil)), Size: size,
	}, nil
}

func writeChecksums(directory string, specs []releaseAssetSpec) ([]releaseAsset, error) {
	assets := make([]releaseAsset, 0, len(specs))
	var content strings.Builder
	for _, spec := range specs {
		asset, err := hashFile(filepath.Join(directory, spec.Name), spec)
		if err != nil {
			return nil, fmt.Errorf("hash %s: %w", spec.Name, err)
		}
		assets = append(assets, asset)
		fmt.Fprintf(&content, "%s  %s\n", asset.SHA256, asset.Name)
	}
	if err := os.WriteFile(filepath.Join(directory, "SHA256SUMS"), []byte(content.String()), 0o644); err != nil {
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
		matches := checksumLinePattern.FindStringSubmatch(scanner.Text())
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
	if len(checksums) != len(specs) {
		return nil, fmt.Errorf("SHA256SUMS asset count does not match the release contract")
	}
	for _, spec := range specs {
		if _, ok := checksums[spec.Name]; !ok {
			return nil, fmt.Errorf("SHA256SUMS is missing %s", spec.Name)
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
	specs []releaseAssetSpec,
) error {
	if buildNumber < 1 || !commitPattern.MatchString(commit) {
		return fmt.Errorf("release build number or commit is invalid")
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

func readMetadata(
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

func validateMetadata(metadata releaseMetadata, platform string, version releaseVersion, specs []releaseAssetSpec) error {
	if metadata.SchemaVersion != releaseMetadataSchemaVersion || metadata.Platform != platform {
		return fmt.Errorf("release metadata schema or platform does not match")
	}
	if metadata.Version != version.String() || metadata.Channel != version.channel() {
		return fmt.Errorf("release metadata version or channel does not match")
	}
	if metadata.BuildNumber < 1 || !commitPattern.MatchString(metadata.Commit) {
		return fmt.Errorf("release metadata build number or commit is invalid")
	}
	parsed, err := time.Parse(time.RFC3339, metadata.BuiltAt)
	if err != nil || parsed.Format(time.RFC3339) != metadata.BuiltAt {
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

func verifyDirectoryLayout(directory string, specs []releaseAssetSpec, checksums, metadata bool) error {
	info, err := os.Lstat(directory)
	if err != nil {
		return fmt.Errorf("inspect release directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("release directory must not be a symlink or regular file")
	}
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
	expected := assetSpecNames(specs)
	if checksums {
		expected = append(expected, "SHA256SUMS")
	}
	if metadata {
		expected = append(expected, "release-metadata.json")
	}
	sort.Strings(actual)
	sort.Strings(expected)
	if strings.Join(actual, "\n") != strings.Join(expected, "\n") {
		return fmt.Errorf("release directory contents do not match the release contract")
	}
	return nil
}

func assetSpecNames(specs []releaseAssetSpec) []string {
	names := make([]string, 0, len(specs))
	for _, spec := range specs {
		names = append(names, spec.Name)
	}
	sort.Strings(names)
	return names
}

func writeJSONFile(path string, value any) error {
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode JSON: %w", err)
	}
	if err := os.WriteFile(path, append(content, '\n'), 0o644); err != nil {
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

func copyReleaseAsset(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return fmt.Errorf("inspect release asset %s: %w", filepath.Base(source), err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() < 1 {
		return fmt.Errorf("release asset must be a non-empty regular file: %s", filepath.Base(source))
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
