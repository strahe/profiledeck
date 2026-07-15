package backend

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/strahe/profiledeck/internal/appbackup"
	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/settings"
)

type applicationBackupRuntime struct {
	getSettings func(context.Context) (settings.Desktop, error)
	createDue   func(context.Context) (*appbackup.BackupDetail, error)
	onResult    func(*appbackup.BackupDetail, error)

	mu     sync.Mutex
	cancel context.CancelFunc
	wake   chan struct{}
	done   chan struct{}
}

func newApplicationBackupRuntime(
	getSettings func(context.Context) (settings.Desktop, error),
	createDue func(context.Context) (*appbackup.BackupDetail, error),
	onResult func(*appbackup.BackupDetail, error),
) *applicationBackupRuntime {
	return &applicationBackupRuntime{getSettings: getSettings, createDue: createDue, onResult: onResult}
}

func (runtime *applicationBackupRuntime) Start(parent context.Context) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	wake := make(chan struct{}, 1)
	done := make(chan struct{})
	runtime.cancel = cancel
	runtime.wake = wake
	runtime.done = done
	go runtime.schedule(ctx, wake, done)
}

func (runtime *applicationBackupRuntime) Stop() {
	runtime.mu.Lock()
	cancel, done := runtime.cancel, runtime.done
	runtime.cancel = nil
	runtime.wake = nil
	runtime.done = nil
	runtime.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

func (runtime *applicationBackupRuntime) Wake() {
	runtime.mu.Lock()
	wake := runtime.wake
	runtime.mu.Unlock()
	if wake == nil {
		return
	}
	select {
	case wake <- struct{}{}:
	default:
	}
}

func (runtime *applicationBackupRuntime) schedule(ctx context.Context, wake <-chan struct{}, done chan<- struct{}) {
	// A scheduler owns its channels for its whole lifetime so a rapid Stop/Start
	// cannot redirect an old goroutine to the next scheduler's signals.
	defer close(done)
	runtime.runIfEnabled(ctx)
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runtime.runIfEnabled(ctx)
		case <-wake:
			runtime.runIfEnabled(ctx)
		}
	}
}

func (runtime *applicationBackupRuntime) runIfEnabled(ctx context.Context) {
	settings, err := runtime.getSettings(ctx)
	if err != nil || !settings.AutomaticBackups {
		return
	}
	detail, err := runtime.createDue(ctx)
	if (detail != nil || err != nil) && runtime.onResult != nil {
		runtime.onResult(detail, err)
	}
	if err != nil && ctx.Err() == nil {
		var appErr *apperror.Error
		if errors.As(err, &appErr) {
			log.Printf("profiledeck: automatic application backup failed: %s", appErr.Code)
			return
		}
		log.Printf("profiledeck: automatic application backup failed")
	}
}
