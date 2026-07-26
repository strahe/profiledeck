package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/strahe/profiledeck/internal/releaseartifact"
)

var checksumLinePattern = regexp.MustCompile(`^([0-9a-f]{64})  ([A-Za-z0-9._-]+)$`)

func runFinalize(args []string, stdout io.Writer) error {
	flags := newFlagSet("finalize")
	version := flags.String("version", "", "release version")
	directory := flags.String("directory", "", "flat release asset directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	contract, err := releaseartifact.NewContract(*version)
	if err != nil {
		return err
	}
	if err := finalizeRelease(*directory, contract); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "Release assets finalized: %s\n", *directory)
	return err
}

func finalizeRelease(directory string, contract releaseartifact.Contract) error {
	contentAssets := withoutChecksums(contract.PublicAssets)
	if err := verifyDirectory(directory, contentAssets); err != nil {
		return fmt.Errorf("verify release assets: %w", err)
	}
	checksumPath := filepath.Join(directory, releaseartifact.ChecksumsName)
	content, err := checksumContent(directory, contentAssets)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(directory, "."+releaseartifact.ChecksumsName+"-*")
	if err != nil {
		return fmt.Errorf("create temporary checksums: %w", err)
	}
	tempPath := temp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(0o644); err != nil {
		_ = temp.Close()
		return fmt.Errorf("set checksum permissions: %w", err)
	}
	if _, err := temp.WriteString(content); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write checksums: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("sync checksums: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close checksums: %w", err)
	}
	if _, err := os.Lstat(checksumPath); err == nil {
		return errors.New("SHA256SUMS already exists")
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect SHA256SUMS: %w", err)
	}
	if err := os.Rename(tempPath, checksumPath); err != nil {
		return fmt.Errorf("commit SHA256SUMS: %w", err)
	}
	committed = true
	return verifyFinalizedDirectory(directory, contract)
}

func verifyFinalizedDirectory(directory string, contract releaseartifact.Contract) error {
	if err := verifyDirectory(directory, contract.PublicAssets); err != nil {
		return err
	}
	checksums, err := readChecksums(filepath.Join(directory, releaseartifact.ChecksumsName))
	if err != nil {
		return err
	}
	contentAssets := withoutChecksums(contract.PublicAssets)
	if len(checksums) != len(contentAssets) {
		return errors.New("SHA256SUMS asset count does not match the release contract")
	}
	for _, asset := range contentAssets {
		expected, ok := checksums[asset.Name]
		if !ok {
			return fmt.Errorf("SHA256SUMS is missing %s", asset.Name)
		}
		actual, _, err := hashFile(filepath.Join(directory, asset.Name))
		if err != nil {
			return err
		}
		if actual != expected {
			return fmt.Errorf("SHA-256 mismatch for %s", asset.Name)
		}
	}
	return nil
}

func verifyDirectory(directory string, assets []releaseartifact.Asset) error {
	if strings.TrimSpace(directory) == "" {
		return errors.New("release asset directory is required")
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return fmt.Errorf("inspect release asset directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("release asset directory must be a real directory")
	}
	expected := make(map[string]struct{}, len(assets))
	for _, asset := range assets {
		expected[asset.Name] = struct{}{}
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("read release asset directory: %w", err)
	}
	if len(entries) != len(expected) {
		return fmt.Errorf("release asset count is %d, want %d", len(entries), len(expected))
	}
	for _, entry := range entries {
		if _, ok := expected[entry.Name()]; !ok {
			return fmt.Errorf("unexpected release asset %s", entry.Name())
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect release asset %s: %w", entry.Name(), err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() < 1 {
			return fmt.Errorf("release asset must be a non-empty regular file: %s", entry.Name())
		}
	}
	return nil
}

func withoutChecksums(assets []releaseartifact.Asset) []releaseartifact.Asset {
	result := make([]releaseartifact.Asset, 0, len(assets)-1)
	for _, asset := range assets {
		if asset.Role != releaseartifact.RoleChecksums {
			result = append(result, asset)
		}
	}
	return result
}

func checksumContent(directory string, assets []releaseartifact.Asset) (string, error) {
	var content strings.Builder
	for _, asset := range assets {
		checksum, _, err := hashFile(filepath.Join(directory, asset.Name))
		if err != nil {
			return "", fmt.Errorf("hash %s: %w", asset.Name, err)
		}
		fmt.Fprintf(&content, "%s  %s\n", checksum, asset.Name)
	}
	return content.String(), nil
}

func hashFile(path string) (string, int64, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", 0, fmt.Errorf("inspect release asset: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() < 1 {
		return "", 0, errors.New("release asset must be a non-empty regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", 0, fmt.Errorf("open release asset: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, fmt.Errorf("hash release asset: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

func readChecksums(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open SHA256SUMS: %w", err)
	}
	defer file.Close()
	checksums := make(map[string]string)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		matches := checksumLinePattern.FindStringSubmatch(scanner.Text())
		if matches == nil {
			return nil, errors.New("SHA256SUMS contains an invalid entry")
		}
		if _, exists := checksums[matches[2]]; exists {
			return nil, fmt.Errorf("SHA256SUMS contains duplicate asset %s", matches[2])
		}
		checksums[matches[2]] = matches[1]
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read SHA256SUMS: %w", err)
	}
	return checksums, nil
}
