package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestCreateSnapshotWaitsForInitialWriterLock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*sqliteBusyTimeout)
	defer cancel()
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "source.db")
	factory := NewFactory(sourcePath)
	db, err := factory.Open(ctx, false)
	if err != nil {
		t.Fatalf("open source database: %v", err)
	}
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate source database: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close source database: %v", err)
	}

	blocker, err := factory.OpenHealthy(ctx, false)
	if err != nil {
		t.Fatalf("open write blocker: %v", err)
	}
	defer blocker.Close()
	connection, err := blocker.db.DB.Conn(ctx)
	if err != nil {
		t.Fatalf("open write blocker connection: %v", err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, "BEGIN EXCLUSIVE"); err != nil {
		t.Fatalf("acquire exclusive writer lock: %v", err)
	}
	locked := true
	defer func() {
		if locked {
			_, _ = connection.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	destination := filepath.Join(dir, "snapshot.db")
	result := make(chan error, 1)
	go func() {
		result <- factory.CreateSnapshot(ctx, destination)
	}()

	timer := time.NewTimer(sqliteBusyTimeout + 500*time.Millisecond)
	select {
	case err := <-result:
		timer.Stop()
		t.Fatalf("snapshot returned before the initial writer lock was released: %v", err)
	case <-timer.C:
	}
	if _, err := connection.ExecContext(ctx, "COMMIT"); err != nil {
		t.Fatalf("release exclusive writer lock: %v", err)
	}
	locked = false

	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("create snapshot after releasing writer lock: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("snapshot did not complete after releasing writer lock: %v", ctx.Err())
	}
}
