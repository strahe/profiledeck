//go:build linux || windows

package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func ResolveLocator() (Locator, error) {
	root := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR"))
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Locator{}, errors.New("failed to resolve the user home directory")
		}
		root = filepath.Join(home, ".claude")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return Locator{}, errors.New("failed to resolve the Claude Code config directory")
	}
	return Locator{Storage: StorageFile, Path: filepath.Clean(filepath.Join(abs, CredentialsFile))}, nil
}
