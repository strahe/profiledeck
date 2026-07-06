//go:build windows

package targetfs

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

const (
	lockRangeLow  = ^uint32(0)
	lockRangeHigh = ^uint32(0)
)

func tryLockFile(file *os.File) error {
	var overlapped windows.Overlapped
	err := windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_FAIL_IMMEDIATELY|windows.LOCKFILE_EXCLUSIVE_LOCK,
		0,
		lockRangeLow,
		lockRangeHigh,
		&overlapped,
	)
	if err == nil {
		return nil
	}
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		return errSystemLockHeld
	}
	return err
}

func unlockFileHandle(file *os.File) error {
	var overlapped windows.Overlapped
	return windows.UnlockFileEx(
		windows.Handle(file.Fd()),
		0,
		lockRangeLow,
		lockRangeHigh,
		&overlapped,
	)
}
