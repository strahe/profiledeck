package main

import (
	"fmt"
	"os"
	"path/filepath"
)

type releaseWorkspace struct {
	root          string
	versionRoot   string
	platformsRoot string
	platform      string
	stage         string
	work          string
	artifacts     string
	final         string
}

func newReleaseWorkspace(
	root string,
	version releaseVersion,
	platform string,
) (releaseWorkspace, error) {
	absoluteRoot, err := resolveReleaseRoot(root)
	if err != nil {
		return releaseWorkspace{}, err
	}
	if _, err := platformAssetSpecs(platform, version); err != nil {
		return releaseWorkspace{}, err
	}
	versionRoot := filepath.Join(absoluteRoot, version.tag())
	platformsRoot := filepath.Join(versionRoot, "platforms")
	stageName := "." + version.tag() + "-" + platform + ".in-progress"
	stage := filepath.Join(absoluteRoot, stageName)
	if filepath.Dir(stage) != absoluteRoot || filepath.Base(stage) != stageName {
		return releaseWorkspace{}, fmt.Errorf("unsafe release staging path")
	}
	return releaseWorkspace{
		root:          absoluteRoot,
		versionRoot:   versionRoot,
		platformsRoot: platformsRoot,
		platform:      platform,
		stage:         stage,
		work:          filepath.Join(stage, "work"),
		artifacts:     filepath.Join(stage, "artifacts"),
		final:         filepath.Join(platformsRoot, platform),
	}, nil
}

func resolveReleaseRoot(root string) (string, error) {
	if root == "" {
		return "", fmt.Errorf("releases directory is required")
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve releases directory: %w", err)
	}
	absoluteRoot = filepath.Clean(absoluteRoot)
	if absoluteRoot == string(filepath.Separator) {
		return "", fmt.Errorf("releases directory cannot be the filesystem root")
	}
	return absoluteRoot, nil
}

func releaseBundlePath(root string, version releaseVersion) (string, error) {
	absoluteRoot, err := resolveReleaseRoot(root)
	if err != nil {
		return "", err
	}
	return filepath.Join(absoluteRoot, version.tag(), "bundle"), nil
}

func (workspace releaseWorkspace) prepare() error {
	if _, err := os.Lstat(workspace.final); err == nil {
		return fmt.Errorf("release output already exists: %s", workspace.final)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect release output: %w", err)
	}
	if _, err := os.Lstat(workspace.stage); err == nil {
		return fmt.Errorf("release staging directory already exists: %s", workspace.stage)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect release staging directory: %w", err)
	}
	if err := os.MkdirAll(workspace.root, 0o755); err != nil {
		return fmt.Errorf("create releases directory: %w", err)
	}
	if err := os.Mkdir(workspace.stage, 0o700); err != nil {
		return fmt.Errorf("create release staging directory: %w", err)
	}
	for _, directory := range []string{workspace.work, workspace.artifacts} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			_ = os.RemoveAll(workspace.stage)
			return fmt.Errorf("create release workspace: %w", err)
		}
	}
	return nil
}

func (workspace releaseWorkspace) cleanup() error {
	info, err := os.Lstat(workspace.stage)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect release staging directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return os.Remove(workspace.stage)
	}
	if !info.IsDir() {
		return fmt.Errorf("release staging path is not a directory: %s", workspace.stage)
	}
	if err := os.RemoveAll(workspace.stage); err != nil {
		return fmt.Errorf("remove release staging directory: %w", err)
	}
	return nil
}

func (workspace releaseWorkspace) commit() error {
	if _, err := os.Lstat(workspace.final); err == nil {
		return fmt.Errorf("release output already exists: %s", workspace.final)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect release output: %w", err)
	}
	info, err := os.Stat(workspace.artifacts)
	if err != nil {
		return fmt.Errorf("inspect staged release artifacts: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("staged release artifacts are not a directory")
	}
	if err := os.MkdirAll(workspace.platformsRoot, 0o755); err != nil {
		return fmt.Errorf("create platform release directory: %w", err)
	}
	// Platform outputs are committed independently so future build jobs can converge on one version root.
	if err := os.Rename(workspace.artifacts, workspace.final); err != nil {
		return fmt.Errorf("commit release artifacts: %w", err)
	}
	return nil
}
