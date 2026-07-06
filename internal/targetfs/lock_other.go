//go:build !darwin && !linux && !freebsd && !openbsd && !netbsd && !dragonfly && !windows

package targetfs

import (
	"os"
	"runtime"
)

func tryLockFile(file *os.File) error {
	return NewError(KindUnsupported, "system file locks are not supported on "+runtime.GOOS)
}

func unlockFileHandle(file *os.File) error {
	return nil
}

func openProbeFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY, 0)
}

func removeVerifiedLockFile(path string, file *os.File, expectedSHA256 string, currentInfo os.FileInfo, lockHeld *bool, fileOpen *bool) error {
	return os.Remove(path)
}
