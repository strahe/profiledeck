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
	Root       string
	Database   string
	Backups    string
	Recovery   string
	Exports    string
	Logs       string
	Lock       string
	DataLock   string
	BackupLock string
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
		Root:       root,
		Database:   filepath.Join(root, "profiledeck.db"),
		Backups:    filepath.Join(root, "backups"),
		Recovery:   filepath.Join(root, "recovery"),
		Exports:    filepath.Join(root, "exports"),
		Logs:       filepath.Join(root, "logs"),
		Lock:       filepath.Join(root, "locks", "switch.lock"),
		DataLock:   filepath.Join(root, "locks", "data.lock"),
		BackupLock: filepath.Join(root, "locks", "backup.lock"),
	}, nil
}
