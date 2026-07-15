package runtime

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/store"
)

func TestDataLeaseExclusiveWaitsForLocalStoresAndBlocksNewOpens(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	factory := store.NewFactory(filepath.Join(dir, "profiledeck.db"))
	db, err := factory.Open(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	lease, err := AcquireDataLease(filepath.Join(dir, "locks", "data.lock"), factory.AccessGate())
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	held, err := factory.Open(ctx, true)
	if err != nil {
		t.Fatal(err)
	}

	exclusiveEntered := make(chan struct{})
	releaseExclusive := make(chan struct{})
	exclusiveDone := make(chan error, 1)
	go func() {
		exclusiveDone <- lease.RunExclusive(ctx, "test-restore", func(exclusiveCtx context.Context) error {
			inside, err := factory.OpenHealthy(exclusiveCtx, true)
			if err != nil {
				return err
			}
			defer inside.Close()
			close(exclusiveEntered)
			<-releaseExclusive
			return nil
		})
	}()

	select {
	case <-exclusiveEntered:
		t.Fatal("exclusive access started before the existing Store closed")
	case err := <-exclusiveDone:
		t.Fatalf("exclusive access ended before the existing Store closed: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-exclusiveEntered:
	case err := <-exclusiveDone:
		t.Fatalf("exclusive access failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("exclusive access did not start after the existing Store closed")
	}

	openDone := make(chan error, 1)
	go func() {
		opened, err := factory.OpenHealthy(ctx, true)
		if err == nil {
			err = opened.Close()
		}
		openDone <- err
	}()
	select {
	case err := <-openDone:
		t.Fatalf("Store opened during exclusive access: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseExclusive)
	select {
	case err := <-exclusiveDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("exclusive access did not finish")
	}
	select {
	case err := <-openDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Store did not open after exclusive access finished")
	}
}

func TestDataLeaseBecomesUnusableWhenSharedLockCannotBeReacquired(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	lease, err := AcquireDataLease(filepath.Join(dir, "data.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()

	err = lease.RunExclusive(context.Background(), "test-restore", func(context.Context) error {
		lease.path = filepath.Join(dir, "missing", "data.lock")
		return nil
	})
	if err == nil {
		t.Fatal("expected shared lock reacquisition to fail")
	}

	called := false
	err = lease.RunExclusive(context.Background(), "test-retry", func(context.Context) error {
		called = true
		return nil
	})
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != apperror.LockAcquireFailed || called {
		t.Fatalf("broken lease retry called=%t err=%v", called, err)
	}
}
