//go:build darwin || linux || freebsd || openbsd || netbsd || dragonfly

package targetfs

import (
	"errors"
	"os"
	"syscall"
)

func tryLockFile(file *os.File) error {
	err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return nil
	}
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return errSystemLockHeld
	}
	return err
}

func unlockFileHandle(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}
