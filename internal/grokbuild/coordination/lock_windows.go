//go:build windows

package coordination

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

const fullRange = uint32(0xffffffff)

func openLockFile(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err == nil && (info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular()) {
		return nil, errors.New("lock path is not a regular file")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := validateLiveFile(path, file); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func tryExclusiveLock(file *os.File) (bool, error) {
	overlapped := windows.Overlapped{}
	err := windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_FAIL_IMMEDIATELY|windows.LOCKFILE_EXCLUSIVE_LOCK,
		0,
		fullRange,
		fullRange,
		&overlapped,
	)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		return false, nil
	}
	return false, err
}

func unlockFile(file *os.File) error {
	overlapped := windows.Overlapped{}
	return windows.UnlockFileEx(
		windows.Handle(file.Fd()),
		0,
		fullRange,
		fullRange,
		&overlapped,
	)
}

func retireStaleLock(*Coordinator, *os.File) bool {
	// A Grok holder cannot detect lock-file replacement on Windows. As long
	// as the kernel range lock is held, fail closed instead of creating two
	// writers or leaving the canonical path delete-pending.
	return false
}

func processAlive(int) bool { return true }
