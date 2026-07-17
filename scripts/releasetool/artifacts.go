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

var checksumLinePattern = regexp.MustCompile(`^([0-9a-f]{64})  ([A-Za-z0-9._-]+)$`)

type releaseAsset struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type releaseMetadata struct {
	Version     string         `json:"version"`
	Channel     string         `json:"channel"`
	BuildNumber int            `json:"build_number"`
	Commit      string         `json:"commit"`
	BuiltAt     string         `json:"built_at"`
	Assets      []releaseAsset `json:"assets"`
}

func expectedAssetNames(version releaseVersion) []string {
	names := []string{updaterZIPName(version), installerDMGName(version)}
	sort.Strings(names)
	return names
}

func hashFile(path string) (releaseAsset, error) {
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
		Name:   filepath.Base(path),
		SHA256: hex.EncodeToString(hash.Sum(nil)),
		Size:   size,
	}, nil
}

func writeChecksums(directory string, version releaseVersion) ([]releaseAsset, error) {
	assets := make([]releaseAsset, 0, 2)
	for _, name := range expectedAssetNames(version) {
		asset, err := hashFile(filepath.Join(directory, name))
		if err != nil {
			return nil, fmt.Errorf("hash %s: %w", name, err)
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

func readChecksums(path string, version releaseVersion) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open SHA256SUMS: %w", err)
	}
	defer file.Close()
	checksums := make(map[string]string, 2)
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
	expected := expectedAssetNames(version)
	if len(checksums) != len(expected) {
		return nil, fmt.Errorf("SHA256SUMS must contain exactly the updater ZIP and installer DMG")
	}
	for _, name := range expected {
		if _, ok := checksums[name]; !ok {
			return nil, fmt.Errorf("SHA256SUMS is missing %s", name)
		}
	}
	return checksums, nil
}

func verifyChecksums(directory string, version releaseVersion) ([]releaseAsset, error) {
	checksums, err := readChecksums(filepath.Join(directory, "SHA256SUMS"), version)
	if err != nil {
		return nil, err
	}
	assets := make([]releaseAsset, 0, len(checksums))
	for _, name := range expectedAssetNames(version) {
		asset, err := hashFile(filepath.Join(directory, name))
		if err != nil {
			return nil, fmt.Errorf("hash %s: %w", name, err)
		}
		if checksums[name] != asset.SHA256 {
			return nil, fmt.Errorf("SHA-256 mismatch for %s", name)
		}
		assets = append(assets, asset)
	}
	return assets, nil
}

func writeMetadata(
	directory string,
	version releaseVersion,
	buildNumber int,
	commit string,
	builtAt time.Time,
) error {
	if !commitPattern.MatchString(commit) {
		return fmt.Errorf("commit must be a full lowercase Git SHA")
	}
	assets, err := verifyChecksums(directory, version)
	if err != nil {
		return err
	}
	metadata := releaseMetadata{
		Version:     version.String(),
		Channel:     version.channel(),
		BuildNumber: buildNumber,
		Commit:      commit,
		BuiltAt:     builtAt.UTC().Format(time.RFC3339),
		Assets:      assets,
	}
	content, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("encode release metadata: %w", err)
	}
	content = append(content, '\n')
	if err := os.WriteFile(filepath.Join(directory, "release-metadata.json"), content, 0o644); err != nil {
		return fmt.Errorf("write release metadata: %w", err)
	}
	return nil
}

func readMetadata(directory string, version releaseVersion) (releaseMetadata, error) {
	content, err := os.ReadFile(filepath.Join(directory, "release-metadata.json"))
	if err != nil {
		return releaseMetadata{}, fmt.Errorf("read release metadata: %w", err)
	}
	var metadata releaseMetadata
	decoder := json.NewDecoder(strings.NewReader(string(content)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&metadata); err != nil {
		return releaseMetadata{}, fmt.Errorf("decode release metadata: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return releaseMetadata{}, fmt.Errorf("release metadata contains trailing content")
	}
	if metadata.Version != version.String() || metadata.Channel != version.channel() {
		return releaseMetadata{}, fmt.Errorf("release metadata version or channel does not match")
	}
	if metadata.BuildNumber < 1 || !commitPattern.MatchString(metadata.Commit) {
		return releaseMetadata{}, fmt.Errorf("release metadata build number or commit is invalid")
	}
	if parsed, err := time.Parse(time.RFC3339, metadata.BuiltAt); err != nil ||
		parsed.Format(time.RFC3339) != metadata.BuiltAt {
		return releaseMetadata{}, fmt.Errorf("release metadata build time is invalid")
	}
	assets, err := verifyChecksums(directory, version)
	if err != nil {
		return releaseMetadata{}, err
	}
	if len(metadata.Assets) != len(assets) {
		return releaseMetadata{}, fmt.Errorf("release metadata assets do not match")
	}
	for index := range assets {
		if metadata.Assets[index] != assets[index] {
			return releaseMetadata{}, fmt.Errorf("release metadata asset %s does not match", assets[index].Name)
		}
	}
	return metadata, nil
}

func verifyDirectoryLayout(directory string, version releaseVersion) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("read release directory: %w", err)
	}
	actual := make([]string, 0, len(entries))
	for _, entry := range entries {
		actual = append(actual, entry.Name())
	}
	sort.Strings(actual)
	expected := append(expectedAssetNames(version), "SHA256SUMS", "release-metadata.json")
	sort.Strings(expected)
	if strings.Join(actual, "\n") != strings.Join(expected, "\n") {
		return fmt.Errorf("release directory must contain exactly the ZIP, DMG, SHA256SUMS, and release-metadata.json")
	}
	return nil
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
