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
