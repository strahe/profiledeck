//go:build windows

package targetfs

import (
	"crypto/sha256"
	"encoding/hex"
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

func openProbeFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDWR, 0)
}

func removeVerifiedLockFile(path string, file *os.File, expectedSHA256 string, currentInfo os.FileInfo, lockHeld, fileOpen *bool) error {
	if *lockHeld {
		if err := unlockFileHandle(file); err != nil {
			return WrapError(KindLockFailed, "failed to release lock before removal", err).WithDetail("path", path)
		}
		*lockHeld = false
	}
	if *fileOpen {
		if err := file.Close(); err != nil {
			return WrapError(KindLockFailed, "failed to close lock file before removal", err).WithDetail("path", path)
		}
		*fileOpen = false
	}

	latestInfo, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return NewError(KindTargetChanged, "lock file disappeared").WithDetail("path", path)
		}
		return WrapError(KindLockFailed, "failed to inspect lock file before removal", err).WithDetail("path", path)
	}
	if latestInfo.Mode()&os.ModeSymlink != 0 || latestInfo.IsDir() || !latestInfo.Mode().IsRegular() {
		return NewError(KindTargetChanged, "lock path is not a regular file").WithDetail("path", path)
	}
	if !os.SameFile(currentInfo, latestInfo) {
		return NewError(KindTargetChanged, "lock file changed").WithDetail("path", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return WrapError(KindLockFailed, "failed to read lock file before removal", err).WithDetail("path", path)
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != expectedSHA256 {
		return NewError(KindTargetChanged, "lock file changed").WithDetail("path", path)
	}
	if err := os.Remove(path); err != nil {
		if isWindowsFileInUseError(err) {
			return NewError(KindLockHeld, "lock file is still in use").WithDetail("path", path)
		}
		return WrapError(KindLockFailed, "failed to remove lock file", err).WithDetail("path", path)
	}
	return nil
}

func isWindowsFileInUseError(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) ||
		errors.Is(err, windows.ERROR_LOCK_VIOLATION) ||
		errors.Is(err, windows.ERROR_SHARING_VIOLATION)
}
