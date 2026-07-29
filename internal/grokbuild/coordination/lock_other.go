//go:build !darwin && !linux && !freebsd && !openbsd && !netbsd && !dragonfly && !windows

package coordination

import (
	"errors"
	"os"
)

func openLockFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
}

func tryExclusiveLock(*os.File) (bool, error) {
	return false, errors.New("grok build file locking is unsupported on this platform")
}

func unlockFile(*os.File) error                   { return nil }
func retireStaleLock(*Coordinator, *os.File) bool { return false }
func processAlive(int) bool                       { return true }
