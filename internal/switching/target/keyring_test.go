package target

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteKeyringBackupRemovesPartialFileAfterWriteFailure(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "credential.bak")
	err := writeKeyringBackup(destination, "credential", func() (*os.File, error) {
		file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return nil, err
		}
		if err := file.Close(); err != nil {
			return nil, err
		}
		return file, nil
	})
	if err == nil {
		t.Fatal("expected write failure")
	}
	if _, statErr := os.Stat(destination); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected incomplete backup to be removed, stat error = %v", statErr)
	}
}
