package runtime

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const runtimeDirName = "profiledeck"

var ErrEmptyUserConfigDir = errors.New("user config directory is required")

type Paths struct {
	Root     string
	Database string
	Backups  string
	Exports  string
	Logs     string
	Lock     string
}

func DefaultUserConfigDir() (string, error) {
	return os.UserConfigDir()
}

func ResolvePaths(userConfigDir string) (Paths, error) {
	if strings.TrimSpace(userConfigDir) == "" {
		return Paths{}, ErrEmptyUserConfigDir
	}

	root := filepath.Join(userConfigDir, runtimeDirName)
	return Paths{
		Root:     root,
		Database: filepath.Join(root, "profiledeck.db"),
		Backups:  filepath.Join(root, "backups"),
		Exports:  filepath.Join(root, "exports"),
		Logs:     filepath.Join(root, "logs"),
		Lock:     filepath.Join(root, "locks", "switch.lock"),
	}, nil
}
