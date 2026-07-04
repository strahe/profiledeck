package runtime

import (
	"path/filepath"
	"strings"

	"github.com/strahe/profiledeck/internal/app"
)

const runtimeDirName = "profiledeck"

type Paths struct {
	Root     string
	Database string
	Backups  string
	Exports  string
	Logs     string
	Lock     string
}

func ResolvePaths(userConfigDir string) (Paths, error) {
	if strings.TrimSpace(userConfigDir) == "" {
		return Paths{}, app.NewError(app.ErrorInvalidRuntimePath, "user config directory is required")
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
