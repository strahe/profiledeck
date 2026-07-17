package main

import (
	"fmt"
	"os"
	"path/filepath"
)

type releaseWorkspace struct {
	root      string
	stage     string
	work      string
	artifacts string
	final     string
}

func newReleaseWorkspace(root string, version releaseVersion) (releaseWorkspace, error) {
	if root == "" {
		return releaseWorkspace{}, fmt.Errorf("releases directory is required")
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return releaseWorkspace{}, fmt.Errorf("resolve releases directory: %w", err)
	}
	absoluteRoot = filepath.Clean(absoluteRoot)
	if absoluteRoot == string(filepath.Separator) {
		return releaseWorkspace{}, fmt.Errorf("releases directory cannot be the filesystem root")
	}
	stage := filepath.Join(absoluteRoot, "."+version.tag()+".in-progress")
	if filepath.Dir(stage) != absoluteRoot ||
		filepath.Base(stage) != "."+version.tag()+".in-progress" {
		return releaseWorkspace{}, fmt.Errorf("unsafe release staging path")
	}
	return releaseWorkspace{
		root:      absoluteRoot,
		stage:     stage,
		work:      filepath.Join(stage, "work"),
		artifacts: filepath.Join(stage, "artifacts"),
		final:     filepath.Join(absoluteRoot, version.tag()),
	}, nil
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
	if err := os.Rename(workspace.artifacts, workspace.final); err != nil {
		return fmt.Errorf("commit release artifacts: %w", err)
	}
	return nil
}

func (workspace releaseWorkspace) removeFinal() error {
	if filepath.Dir(workspace.final) != workspace.root {
		return fmt.Errorf("unsafe final release path")
	}
	info, err := os.Lstat(workspace.final)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect final release directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("final release path is not a directory: %s", workspace.final)
	}
	if err := os.RemoveAll(workspace.final); err != nil {
		return fmt.Errorf("remove consumed release artifacts: %w", err)
	}
	return nil
}
