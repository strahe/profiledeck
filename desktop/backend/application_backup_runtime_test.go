package backend

import (
	"context"
	"testing"
	"time"

	"github.com/strahe/profiledeck/internal/appbackup"
	"github.com/strahe/profiledeck/internal/settings"
)

func TestApplicationBackupRuntimeChecksForOverdueBackupAtStartup(t *testing.T) {
	created := make(chan struct{}, 1)
	result := make(chan *appbackup.BackupDetail, 1)
	runtime := newApplicationBackupRuntime(
		func(context.Context) (settings.Desktop, error) {
			return settings.Desktop{AutomaticBackups: true}, nil
		},
		func(context.Context) (*appbackup.BackupDetail, error) {
			detail := &appbackup.BackupDetail{BackupSummary: appbackup.BackupSummary{ID: "auto-startup"}}
			created <- struct{}{}
			return detail, nil
		},
		func(detail *appbackup.BackupDetail, _ error) { result <- detail },
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runtime.Start(ctx)
	defer runtime.Stop()

	select {
	case <-created:
	case <-time.After(time.Second):
		t.Fatal("startup did not check for an overdue application backup")
	}
	select {
	case detail := <-result:
		if detail == nil || detail.ID != "auto-startup" {
			t.Fatalf("unexpected automatic backup result: %#v", detail)
		}
	case <-time.After(time.Second):
		t.Fatal("automatic backup creation did not emit a result")
	}
}

func TestApplicationBackupRuntimeCanStopImmediatelyAndRestart(_ *testing.T) {
	runtime := newApplicationBackupRuntime(
		func(context.Context) (settings.Desktop, error) {
			return settings.Desktop{AutomaticBackups: false}, nil
		},
		func(context.Context) (*appbackup.BackupDetail, error) { return nil, nil },
		nil,
	)

	for range 20 {
		runtime.Start(context.Background())
		runtime.Stop()
		runtime.Wake()
	}
}
