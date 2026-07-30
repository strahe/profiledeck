package coordination

import (
	"errors"
	"os"
)

func validateLiveFile(path string, file *os.File) error {
	current, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() {
		return errors.New("lock path is not a regular file")
	}
	opened, err := file.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(opened, current) {
		return errors.New("lock file was replaced")
	}
	return nil
}
