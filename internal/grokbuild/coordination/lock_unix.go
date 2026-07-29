//go:build darwin || linux || freebsd || openbsd || netbsd || dragonfly

package coordination

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func openLockFile(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if err := validateLiveFile(path, file); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func tryExclusiveLock(file *os.File) (bool, error) {
	err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return false, nil
	}
	return false, err
}

func unlockFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}

func retireStaleLock(coordinator *Coordinator, file *os.File) bool {
	if !coordinator.holderIsStale(file) {
		return false
	}
	err := os.Remove(coordinator.home.LockPath)
	return err == nil || errors.Is(err, os.ErrNotExist)
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
