//go:build !windows

package targetfs

import (
	"errors"
	"os"
)

// SyncDirectory asks the operating system to persist directory metadata.
func SyncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}
