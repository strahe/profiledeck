package targetfs

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var errSystemLockHeld = errors.New("system lock is already held")

type Lock struct {
	file     *os.File
	localKey string
}

var localLockRegistry = struct {
	mu    sync.Mutex
	locks map[string]struct{}
}{
	locks: map[string]struct{}{},
}

func AcquireLock(path string, owner string) (Lock, error) {
	token := fmt.Sprintf("%s\npid=%d\ncreated_at_unix_ms=%d\n", owner, os.Getpid(), time.Now().UnixMilli())
	localKey := localLockKey(path)
	if !acquireLocalLock(localKey) {
		return Lock{}, NewError(KindLockHeld, "lock is already held").WithDetail("path", path)
	}
	releaseLocal := true
	defer func() {
		if releaseLocal {
			releaseLocalLock(localKey)
		}
	}()

	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return Lock{}, WrapError(KindLockFailed, "failed to open lock file", err).WithDetail("path", path)
	}
	closeFile := true
	defer func() {
		if closeFile {
			_ = file.Close()
		}
	}()

	if err := tryLockFile(file); err != nil {
		if errors.Is(err, errSystemLockHeld) {
			return Lock{}, NewError(KindLockHeld, "lock is already held").WithDetail("path", path)
		}
		return Lock{}, WrapError(KindLockFailed, "failed to acquire system lock", err).WithDetail("path", path)
	}
	unlockFile := true
	defer func() {
		if unlockFile {
			_ = unlockFileHandle(file)
		}
	}()

	// The file content is diagnostic only. The OS-level file lock is the
	// cross-process safety primitive and is released automatically on crash.
	if err := writeLockToken(file, token); err != nil {
		return Lock{}, err
	}

	closeFile = false
	unlockFile = false
	releaseLocal = false
	return Lock{file: file, localKey: localKey}, nil
}

func writeLockToken(file *os.File, token string) error {
	if err := file.Truncate(0); err != nil {
		return WrapError(KindLockFailed, "failed to truncate lock file", err).WithDetail("path", file.Name())
	}
	if _, err := file.Seek(0, 0); err != nil {
		return WrapError(KindLockFailed, "failed to seek lock file", err).WithDetail("path", file.Name())
	}
	if _, err := io.WriteString(file, token); err != nil {
		return WrapError(KindLockFailed, "failed to write lock file", err).WithDetail("path", file.Name())
	}
	if err := file.Sync(); err != nil {
		return WrapError(KindLockFailed, "failed to sync lock file", err).WithDetail("path", file.Name())
	}
	return nil
}

func (l Lock) Release() {
	if l.file == nil {
		return
	}
	_ = unlockFileHandle(l.file)
	_ = l.file.Close()
	releaseLocalLock(l.localKey)
}

func localLockKey(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(abs)
}

func acquireLocalLock(key string) bool {
	localLockRegistry.mu.Lock()
	defer localLockRegistry.mu.Unlock()
	if _, exists := localLockRegistry.locks[key]; exists {
		return false
	}
	localLockRegistry.locks[key] = struct{}{}
	return true
}

func releaseLocalLock(key string) {
	if key == "" {
		return
	}
	localLockRegistry.mu.Lock()
	defer localLockRegistry.mu.Unlock()
	delete(localLockRegistry.locks, key)
}
