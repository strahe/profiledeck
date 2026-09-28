package config

import (
	"os"
	"path/filepath"
	"strings"
)

func ResolveConfigDir(explicit string) (string, error) {
	root := strings.TrimSpace(explicit)
	if root == "" {
		root = strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR"))
	}
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		root = filepath.Join(home, ".claude")
	}
	absolute, err := filepath.Abs(root)
	return filepath.Clean(absolute), err
}
