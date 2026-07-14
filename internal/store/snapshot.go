package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	sqlite "modernc.org/sqlite"
)

const (
	snapshotStepPages = 128
	snapshotStepDelay = 25 * time.Millisecond
)

type onlineBackuper interface {
	NewBackup(string) (*sqlite.Backup, error)
}

// CreateSnapshot uses SQLite's online backup API so the copy represents one
// consistent database state even while the Desktop process is serving writes.
func (factory Factory) CreateSnapshot(ctx context.Context, destination string) error {
	destination = filepath.Clean(destination)
	if destination == "." || destination == string(filepath.Separator) {
		return errors.New("snapshot destination is required")
	}
	dir := filepath.Dir(destination)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create snapshot directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("secure snapshot directory: %w", err)
	}
	if _, err := os.Stat(destination); err == nil {
		return errors.New("snapshot destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect snapshot destination: %w", err)
	}

	temp, err := os.CreateTemp(dir, ".profiledeck-snapshot-*.db")
	if err != nil {
		return fmt.Errorf("create snapshot file: %w", err)
	}
	tempPath := temp.Name()
	if closeErr := temp.Close(); closeErr != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("close snapshot file: %w", closeErr)
	}
	defer func() { _ = os.Remove(tempPath) }()
	if err := os.Chmod(tempPath, 0o600); err != nil {
		return fmt.Errorf("secure snapshot file: %w", err)
	}

	source, err := factory.OpenHealthy(ctx, true)
	if err != nil {
		return err
	}
	defer source.Close()

	connection, err := source.db.DB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("open snapshot source connection: %w", err)
	}
	defer connection.Close()

	err = connection.Raw(func(raw any) error {
		backuper, ok := raw.(onlineBackuper)
		if !ok {
			return fmt.Errorf("sqlite driver %T does not support online backup", raw)
		}
		backup, err := backuper.NewBackup(tempPath)
		if err != nil {
			return err
		}
		stepErr := stepBackup(ctx, backup)
		finishErr := backup.Finish()
		return errors.Join(stepErr, finishErr)
	})
	if err != nil {
		return fmt.Errorf("create online database snapshot: %w", err)
	}

	// Windows requires a writable handle for Sync because it calls
	// FlushFileBuffers, even though the completed snapshot is not modified.
	file, err := os.OpenFile(tempPath, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("open completed snapshot: %w", err)
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(syncErr, closeErr); err != nil {
		return fmt.Errorf("sync completed snapshot: %w", err)
	}
	if err := os.Chmod(tempPath, 0o600); err != nil {
		return fmt.Errorf("secure completed snapshot: %w", err)
	}
	// Link publishes without replacing a path created after the earlier check;
	// a plain rename would silently overwrite it on Unix.
	if err := os.Link(tempPath, destination); err != nil {
		return fmt.Errorf("publish completed snapshot: %w", err)
	}
	if err := os.Remove(tempPath); err != nil {
		return fmt.Errorf("remove snapshot staging file: %w", err)
	}
	return nil
}

func stepBackup(ctx context.Context, backup *sqlite.Backup) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		more, err := backup.Step(snapshotStepPages)
		if err != nil && !isSQLiteBusyError(err) {
			return err
		}
		if err == nil && !more {
			return nil
		}
		// Online backups may be retried after SQLITE_BUSY or SQLITE_LOCKED.
		// Yield between chunks too, so concurrent writers can release their locks.
		timer := time.NewTimer(snapshotStepDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
