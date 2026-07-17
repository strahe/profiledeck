package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func requireRegularFile(path, label string) (os.FileInfo, error) {
	if path == "" {
		return nil, fmt.Errorf("%s path is required", label)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect %s: %w", label, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s must be a regular file", label)
	}
	return info, nil
}

func verifyCandidateMatchesDMG(candidatePath, releaseDMGPath string) error {
	candidateInfo, err := requireRegularFile(candidatePath, "release candidate")
	if err != nil {
		return err
	}
	releaseInfo, err := requireRegularFile(releaseDMGPath, "release DMG")
	if err != nil {
		return err
	}
	if candidateInfo.Size() != releaseInfo.Size() {
		return fmt.Errorf("release candidate does not match the verified DMG")
	}
	candidate, err := hashFile(candidatePath)
	if err != nil {
		return fmt.Errorf("hash release candidate: %w", err)
	}
	release, err := hashFile(releaseDMGPath)
	if err != nil {
		return fmt.Errorf("hash release DMG: %w", err)
	}
	if candidate.SHA256 != release.SHA256 {
		return fmt.Errorf("release candidate does not match the verified DMG")
	}
	return nil
}

func promoteCandidateDMG(sourcePath, candidatePath string) error {
	if filepath.Ext(candidatePath) != ".dmg" {
		return fmt.Errorf("release candidate path must end in .dmg")
	}
	if _, err := requireRegularFile(sourcePath, "release DMG"); err != nil {
		return err
	}
	sourceAbsolute, err := filepath.Abs(sourcePath)
	if err != nil {
		return fmt.Errorf("resolve release DMG path: %w", err)
	}
	candidateAbsolute, err := filepath.Abs(candidatePath)
	if err != nil {
		return fmt.Errorf("resolve release candidate path: %w", err)
	}
	if sourceAbsolute == candidateAbsolute {
		return fmt.Errorf("release candidate must not overwrite its source")
	}
	if info, err := os.Lstat(candidateAbsolute); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("existing release candidate must be a regular file")
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect existing release candidate: %w", err)
	}
	parent := filepath.Dir(candidateAbsolute)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create release candidate directory: %w", err)
	}
	parentInfo, err := os.Lstat(parent)
	if err != nil {
		return fmt.Errorf("inspect release candidate directory: %w", err)
	}
	if parentInfo.Mode()&os.ModeSymlink != 0 || !parentInfo.IsDir() {
		return fmt.Errorf("release candidate directory must be a directory")
	}

	source, err := os.Open(sourceAbsolute)
	if err != nil {
		return fmt.Errorf("open release DMG: %w", err)
	}
	defer source.Close()
	temporary, err := os.CreateTemp(parent, ".ProfileDeck-candidate-*.dmg")
	if err != nil {
		return fmt.Errorf("create temporary release candidate: %w", err)
	}
	temporaryPath := temporary.Name()
	keepTemporary := false
	defer func() {
		_ = temporary.Close()
		if !keepTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o644); err != nil {
		return fmt.Errorf("set release candidate permissions: %w", err)
	}
	if _, err := io.Copy(temporary, source); err != nil {
		return fmt.Errorf("copy release candidate: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync release candidate: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close release candidate: %w", err)
	}
	if err := verifyCandidateMatchesDMG(temporaryPath, sourceAbsolute); err != nil {
		return err
	}
	// Publish beside the destination first so a failed copy never replaces the
	// last verified candidate.
	if err := os.Rename(temporaryPath, candidateAbsolute); err != nil {
		return fmt.Errorf("replace release candidate: %w", err)
	}
	keepTemporary = true
	return nil
}
