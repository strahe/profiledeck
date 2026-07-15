package targetfs

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var errSystemLockHeld = errors.New("system lock is already held")

const lockAcquireMaxAttempts = 8

type Lock struct {
	file     *os.File
	localKey string
	shared   bool
}

type LockProbe struct {
	Path        string
	Exists      bool
	Held        bool
	Unsupported bool
}

var localLockRegistry = struct {
	mu    sync.Mutex
	locks map[string]localLockState
}{
	locks: map[string]localLockState{},
}

type localLockState struct {
	readers int
	writer  bool
}

func AcquireLock(path, owner string) (Lock, error) {
	token := fmt.Sprintf("%s\npid=%d\ncreated_at_unix_ms=%d\n", owner, os.Getpid(), time.Now().UnixMilli())
	localKey := localLockKey(path)
	if !acquireLocalLock(localKey, false) {
		return Lock{}, NewError(KindLockHeld, "lock is already held").WithDetail("path", path)
	}
	releaseLocal := true
	defer func() {
		if releaseLocal {
			releaseLocalLock(localKey, false)
		}
	}()

	for attempt := 0; attempt < lockAcquireMaxAttempts; attempt++ {
		lock, retry, err := acquireLockAttempt(path, token, localKey)
		if err == nil {
			releaseLocal = false
			return lock, nil
		}
		if retry {
			continue
		}
		return Lock{}, err
	}
	return Lock{}, NewError(KindTargetChanged, "lock file changed during acquire").WithDetail("path", path)
}

func acquireLockAttempt(path, token, localKey string) (Lock, bool, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return Lock{}, false, WrapError(KindLockFailed, "failed to open lock file", err).WithDetail("path", path)
	}
	closeFile := true
	defer func() {
		if closeFile {
			_ = file.Close()
		}
	}()

	if err := tryLockFile(file); err != nil {
		if errors.Is(err, errSystemLockHeld) {
			return Lock{}, false, NewError(KindLockHeld, "lock is already held").WithDetail("path", path)
		}
		return Lock{}, false, WrapError(KindLockFailed, "failed to acquire system lock", err).WithDetail("path", path)
	}
	unlockFile := true
	defer func() {
		if unlockFile {
			_ = unlockFileHandle(file)
		}
	}()

	if _, err := verifyOpenedLockFileCurrent(path, file); err != nil {
		if isTargetChangedError(err) {
			return Lock{}, true, nil
		}
		return Lock{}, false, err
	}

	// The file content is diagnostic only. The OS-level file lock is the
	// cross-process safety primitive and is released automatically on crash.
	if err := writeLockToken(file, token); err != nil {
		return Lock{}, false, err
	}

	closeFile = false
	unlockFile = false
	return Lock{file: file, localKey: localKey}, false, nil
}

// AcquireSharedLock holds a process-lifetime data lease. Shared holders may
// coexist, while database replacement requires the exclusive lock above.
func AcquireSharedLock(path string) (Lock, error) {
	localKey := localLockKey(path)
	if !acquireLocalLock(localKey, true) {
		return Lock{}, NewError(KindLockHeld, "lock is already held").WithDetail("path", path)
	}
	releaseLocal := true
	defer func() {
		if releaseLocal {
			releaseLocalLock(localKey, true)
		}
	}()

	for attempt := 0; attempt < lockAcquireMaxAttempts; attempt++ {
		file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
		if err != nil {
			return Lock{}, WrapError(KindLockFailed, "failed to open lock file", err).WithDetail("path", path)
		}
		if err := trySharedLockFile(file); err != nil {
			_ = file.Close()
			if errors.Is(err, errSystemLockHeld) {
				return Lock{}, NewError(KindLockHeld, "lock is already held").WithDetail("path", path)
			}
			return Lock{}, WrapError(KindLockFailed, "failed to acquire shared system lock", err).WithDetail("path", path)
		}
		if _, err := verifyOpenedLockFileCurrent(path, file); err != nil {
			_ = unlockFileHandle(file)
			_ = file.Close()
			if isTargetChangedError(err) {
				continue
			}
			return Lock{}, err
		}
		releaseLocal = false
		return Lock{file: file, localKey: localKey, shared: true}, nil
	}
	return Lock{}, NewError(KindTargetChanged, "lock file changed during acquire").WithDetail("path", path)
}

func ProbeLock(path string) (LockProbe, error) {
	for attempt := 0; attempt < lockAcquireMaxAttempts; attempt++ {
		probe, retry, err := probeLockAttempt(path)
		if err == nil && retry {
			continue
		}
		return probe, err
	}
	return LockProbe{}, NewError(KindTargetChanged, "lock file changed during probe").WithDetail("path", path)
}

func probeLockAttempt(path string) (LockProbe, bool, error) {
	probe := LockProbe{Path: path}
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return probe, false, nil
		}
		return LockProbe{}, false, WrapError(KindLockFailed, "failed to inspect lock file", err).WithDetail("path", path)
	}
	probe.Exists = true
	if info.Mode()&os.ModeSymlink != 0 || info.IsDir() || !info.Mode().IsRegular() {
		return probe, false, NewError(KindLockFailed, "lock path is not a regular file").WithDetail("path", path)
	}

	localKey := localLockKey(path)
	if isLocalLockHeld(localKey) {
		probe.Held = true
		return probe, false, nil
	}

	file, err := openProbeFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return LockProbe{Path: path}, true, nil
		}
		return LockProbe{}, false, WrapError(KindLockFailed, "failed to open lock file", err).WithDetail("path", path)
	}
	defer file.Close()

	if err := tryLockFile(file); err != nil {
		if errors.Is(err, errSystemLockHeld) {
			probe.Held = true
			return probe, false, nil
		}
		var targetErr *Error
		if errors.As(err, &targetErr) && targetErr.Kind == KindUnsupported {
			probe.Unsupported = true
			return probe, false, nil
		}
		return LockProbe{}, false, WrapError(KindLockFailed, "failed to probe system lock", err).WithDetail("path", path)
	}
	lockHeld := true
	defer func() {
		if lockHeld {
			_ = unlockFileHandle(file)
		}
	}()
	if _, err := verifyOpenedLockFileCurrent(path, file); err != nil {
		if isTargetChangedError(err) {
			return LockProbe{}, true, nil
		}
		return LockProbe{}, false, err
	}
	if err := unlockFileHandle(file); err == nil {
		lockHeld = false
	}
	return probe, false, nil
}

func RemoveStaleLockFile(path, expectedSHA256 string) error {
	if expectedSHA256 == "" {
		return NewError(KindTargetChanged, "expected lock file hash is required").WithDetail("path", path)
	}

	initialInfo, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return NewError(KindTargetChanged, "lock file disappeared").WithDetail("path", path)
		}
		return WrapError(KindLockFailed, "failed to inspect lock file", err).WithDetail("path", path)
	}
	if initialInfo.Mode()&os.ModeSymlink != 0 || initialInfo.IsDir() || !initialInfo.Mode().IsRegular() {
		return NewError(KindTargetChanged, "lock path is not a regular file").WithDetail("path", path)
	}

	localKey := localLockKey(path)
	if !acquireLocalLock(localKey, false) {
		return NewError(KindLockHeld, "lock is already held").WithDetail("path", path)
	}
	defer releaseLocalLock(localKey, false)

	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return NewError(KindTargetChanged, "lock file disappeared").WithDetail("path", path)
		}
		return WrapError(KindLockFailed, "failed to open lock file", err).WithDetail("path", path)
	}
	fileOpen := true
	defer func() {
		if fileOpen {
			_ = file.Close()
		}
	}()

	if err := tryLockFile(file); err != nil {
		if errors.Is(err, errSystemLockHeld) {
			return NewError(KindLockHeld, "lock is already held").WithDetail("path", path)
		}
		return WrapError(KindLockFailed, "failed to acquire system lock", err).WithDetail("path", path)
	}
	lockHeld := true
	defer func() {
		if lockHeld {
			_ = unlockFileHandle(file)
		}
	}()

	raw, err := io.ReadAll(file)
	if err != nil {
		return WrapError(KindLockFailed, "failed to read lock file", err).WithDetail("path", path)
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != expectedSHA256 {
		return NewError(KindTargetChanged, "lock file changed").WithDetail("path", path)
	}

	currentInfo, err := verifyOpenedLockFileCurrent(path, file)
	if err != nil {
		return err
	}
	if err := removeVerifiedLockFile(path, file, expectedSHA256, currentInfo, &lockHeld, &fileOpen); err != nil {
		var targetErr *Error
		if errors.As(err, &targetErr) {
			return targetErr
		}
		return WrapError(KindLockFailed, "failed to remove lock file", err).WithDetail("path", path)
	}
	syncParentDirBestEffort(filepath.Dir(path))
	return nil
}

func verifyOpenedLockFileCurrent(path string, file *os.File) (os.FileInfo, error) {
	// Unix flock attaches to the opened file, not the path; this check keeps
	// an unlinked or replaced lock file from becoming a second global lock.
	currentInfo, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, NewError(KindTargetChanged, "lock file disappeared").WithDetail("path", path)
		}
		return nil, WrapError(KindLockFailed, "failed to inspect lock file", err).WithDetail("path", path)
	}
	if currentInfo.Mode()&os.ModeSymlink != 0 || currentInfo.IsDir() || !currentInfo.Mode().IsRegular() {
		return nil, NewError(KindTargetChanged, "lock path is not a regular file").WithDetail("path", path)
	}
	openedInfo, err := file.Stat()
	if err != nil {
		return nil, WrapError(KindLockFailed, "failed to stat lock file", err).WithDetail("path", path)
	}
	if !os.SameFile(openedInfo, currentInfo) {
		return nil, NewError(KindTargetChanged, "lock file changed").WithDetail("path", path)
	}
	return currentInfo, nil
}

func isTargetChangedError(err error) bool {
	var targetErr *Error
	return errors.As(err, &targetErr) && targetErr.Kind == KindTargetChanged
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
	releaseLocalLock(l.localKey, l.shared)
}

func localLockKey(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return normalizeLocalLockKey(filepath.Clean(path))
	}
	return normalizeLocalLockKey(filepath.Clean(abs))
}

func acquireLocalLock(key string, shared bool) bool {
	localLockRegistry.mu.Lock()
	defer localLockRegistry.mu.Unlock()
	state := localLockRegistry.locks[key]
	if shared {
		if state.writer {
			return false
		}
		state.readers++
		localLockRegistry.locks[key] = state
		return true
	}
	if state.writer || state.readers != 0 {
		return false
	}
	state.writer = true
	localLockRegistry.locks[key] = state
	return true
}

func isLocalLockHeld(key string) bool {
	localLockRegistry.mu.Lock()
	defer localLockRegistry.mu.Unlock()
	state := localLockRegistry.locks[key]
	return state.writer || state.readers != 0
}

func releaseLocalLock(key string, shared bool) {
	if key == "" {
		return
	}
	localLockRegistry.mu.Lock()
	defer localLockRegistry.mu.Unlock()
	state := localLockRegistry.locks[key]
	if shared {
		if state.readers > 0 {
			state.readers--
		}
	} else {
		state.writer = false
	}
	if state.readers == 0 && !state.writer {
		delete(localLockRegistry.locks, key)
		return
	}
	localLockRegistry.locks[key] = state
}
