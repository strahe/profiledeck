//go:build windows

package coordination

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/strahe/profiledeck/internal/apperror"
	grokconfig "github.com/strahe/profiledeck/internal/grokbuild/config"
	"github.com/strahe/profiledeck/internal/providercoord"
)

func TestWindowsLockCoversOffsetsBeyondDiagnosticByte(t *testing.T) {
	home := testHome(t)
	first, err := openLockFile(home.LockPath)
	if err != nil {
		t.Fatalf("open first lock file: %v", err)
	}
	defer first.Close()
	if err := writeHolder(first, "1234:1"); err != nil {
		t.Fatalf("write holder: %v", err)
	}
	locked, err := tryExclusiveLock(first)
	if err != nil || !locked {
		t.Fatalf("lock full file: locked=%t err=%v", locked, err)
	}
	defer unlockFile(first)

	second, err := os.OpenFile(home.LockPath, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open second lock file: %v", err)
	}
	defer second.Close()
	if _, err := second.Read(make([]byte, 1)); !errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		t.Fatalf("read through exclusive lock = %v, want ERROR_LOCK_VIOLATION", err)
	}
	overlapped := windows.Overlapped{Offset: 1}
	err = windows.LockFileEx(
		windows.Handle(second.Fd()),
		windows.LOCKFILE_FAIL_IMMEDIATELY|windows.LOCKFILE_EXCLUSIVE_LOCK,
		0,
		1,
		0,
		&overlapped,
	)
	if err == nil {
		_ = windows.UnlockFileEx(windows.Handle(second.Fd()), 0, 1, 0, &overlapped)
		t.Fatal("one-byte lock beyond offset zero bypassed the Grok Build guard")
	}
	if !errors.Is(err, windows.ERROR_LOCK_VIOLATION) &&
		!errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		t.Fatalf("second lock returned an unexpected error: %v", err)
	}
}

func TestWindowsStaleHolderFailsClosedWithoutDeletePending(t *testing.T) {
	home := testHome(t)
	first, err := openRustCompatibleTestLock(home.LockPath)
	if err != nil {
		t.Fatalf("open Rust-compatible lock file: %v", err)
	}
	if err := writeHolder(first, "2147483647:1"); err != nil {
		_ = first.Close()
		t.Fatalf("write stale holder: %v", err)
	}
	locked, err := tryExclusiveLock(first)
	if err != nil || !locked {
		_ = first.Close()
		t.Fatalf("lock stale holder: locked=%t err=%v", locked, err)
	}
	before, err := first.Stat()
	if err != nil {
		_ = unlockFile(first)
		_ = first.Close()
		t.Fatalf("stat stale holder: %v", err)
	}

	coordinator := NewCoordinator(home)
	coordinator.acquireTimeout = 50 * time.Millisecond
	coordinator.pollInterval = 5 * time.Millisecond
	coordinator.now = func() time.Time {
		return time.Now().Add(defaultStaleAfter + time.Second)
	}
	_, err = coordinator.Acquire(context.Background(), providercoord.Request{
		ProviderID: grokconfig.ProviderID,
	})
	assertCode(t, err, apperror.LockAcquireFailed)
	after, err := os.Stat(home.LockPath)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("canonical lock changed during stale contention: before=%#v after=%#v err=%v", before, after, err)
	}
	probe, err := openLockFile(home.LockPath)
	if err != nil {
		t.Fatalf("canonical lock entered delete-pending: %v", err)
	}
	_ = probe.Close()

	if err := unlockFile(first); err != nil {
		_ = first.Close()
		t.Fatalf("unlock stale holder: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close stale holder: %v", err)
	}
	reacquired, err := coordinator.Acquire(context.Background(), providercoord.Request{
		ProviderID: grokconfig.ProviderID,
	})
	if err != nil {
		t.Fatalf("Acquire after holder close: %v", err)
	}
	reacquired.Release()
}

func openRustCompatibleTestLock(path string) (*os.File, error) {
	pathPointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(
		pathPointer,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_ALWAYS,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(handle), path), nil
}
