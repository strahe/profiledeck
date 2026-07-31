package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestCreateSnapshotSucceedsAlongsideConcurrentWriter(t *testing.T) {
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

	writer, err := factory.OpenHealthy(ctx, false)
	if err != nil {
		t.Fatalf("open concurrent writer: %v", err)
	}
	defer writer.Close()
	connection, err := writer.db.DB.Conn(ctx)
	if err != nil {
		t.Fatalf("open writer connection: %v", err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("begin concurrent write transaction: %v", err)
	}
	defer func() {
		_, _ = connection.ExecContext(context.Background(), "ROLLBACK")
	}()

	destination := filepath.Join(dir, "snapshot.db")
	if err := factory.CreateSnapshot(ctx, destination); err != nil {
		t.Fatalf("create snapshot alongside concurrent writer: %v", err)
	}
	snapshot, err := NewFactory(destination).OpenHealthy(ctx, true)
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	defer snapshot.Close()
	status, err := snapshot.Status(ctx)
	if err != nil || !status.SchemaHealthy {
		t.Fatalf("snapshot status healthy=%v err=%v", status.SchemaHealthy, err)
	}
}
