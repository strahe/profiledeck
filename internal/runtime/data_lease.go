package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/store"
	"github.com/strahe/profiledeck/internal/targetfs"
)

// DataLease keeps normal ProfileDeck processes from observing a database file
// replacement while allowing CLI and Desktop readers to coexist.
type DataLease struct {
	mu     sync.Mutex
	path   string
	shared targetfs.Lock
	gate   *store.AccessGate
	closed bool
}

func AcquireDataLease(path string, accessGates ...*store.AccessGate) (*DataLease, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, apperror.Wrap(apperror.LockAcquireFailed, "failed to create application data lock directory", err)
	}
	lock, err := targetfs.AcquireSharedLock(path)
	if err != nil {
		return nil, mapDataLockError(err)
	}
	var gate *store.AccessGate
	if len(accessGates) > 0 {
		gate = accessGates[0]
	}
	return &DataLease{path: path, shared: lock, gate: gate}, nil
}

func (lease *DataLease) RunExclusive(ctx context.Context, owner string, run func(context.Context) error) error {
	if lease == nil || run == nil {
		return apperror.New(apperror.CommandFailed, "exclusive application data operation is unavailable")
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.closed {
		return apperror.New(apperror.LockAcquireFailed, "application data lease is closed")
	}

	return lease.gate.RunExclusive(ctx, func(ctx context.Context) error {
		lease.shared.Release()
		exclusive, err := targetfs.AcquireLock(lease.path, owner)
		if err != nil {
			reacquireErr := lease.reacquireShared()
			if reacquireErr != nil {
				return errors.Join(mapDataLockError(err), reacquireErr)
			}
			return mapDataLockError(err)
		}
		runErr := run(ctx)
		exclusive.Release()
		if err := lease.reacquireShared(); err != nil {
			return errors.Join(runErr, err)
		}
		return runErr
	})
}

func (lease *DataLease) Close() {
	if lease == nil {
		return
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.closed {
		return
	}
	lease.closed = true
	lease.shared.Release()
}

func (lease *DataLease) reacquireShared() error {
	lock, err := targetfs.AcquireSharedLock(lease.path)
	if err != nil {
		// Without the process-lifetime shared lock, normal database access is no
		// longer safe to coordinate with another process's restore.
		lease.closed = true
		return mapDataLockError(err)
	}
	lease.shared = lock
	return nil
}

func mapDataLockError(err error) error {
	var targetErr *targetfs.Error
	if !errors.As(err, &targetErr) {
		return err
	}
	appErr := apperror.Wrap(apperror.LockAcquireFailed, "application data is in use by another ProfileDeck process", err)
	for key, value := range targetErr.Details {
		appErr = appErr.WithDetail(key, value)
	}
	return appErr
}
