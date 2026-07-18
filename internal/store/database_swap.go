package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

const (
	databaseSwapFormatVersion      = 1
	databaseSwapPrepared           = "prepared"
	databaseSwapOriginalMoved      = "original_moved"
	databaseSwapCandidateInstalled = "candidate_installed"
)

type databaseSwapState struct {
	FormatVersion  int    `json:"format_version"`
	Phase          string `json:"phase"`
	OriginalExists bool   `json:"original_exists"`
}

// RestoreCandidatePath is the fixed sibling path used to stage a restored
// database before it is exchanged with the live database.
func RestoreCandidatePath(databasePath string) string {
	return databasePath + ".restore-new"
}

func DatabaseSwapPending(databasePath string) bool {
	_, err := os.Lstat(databasePath + ".restore-state")
	return err == nil
}

// ReconcileDatabaseSwap finishes or rolls back an interrupted database
// exchange before any normal database connection is opened.
func ReconcileDatabaseSwap(ctx context.Context, databasePath string) error {
	marker := databasePath + ".restore-state"
	state, exists, err := readDatabaseSwapState(marker)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	oldPath := databasePath + ".restore-old"
	newPath := RestoreCandidatePath(databasePath)

	switch state.Phase {
	case databaseSwapCandidateInstalled:
		if databaseSetExists(databasePath) && validateCurrentDatabase(ctx, databasePath) == nil {
			if err := removeDatabaseSet(oldPath); err != nil {
				return err
			}
			if err := removeDatabaseSet(newPath); err != nil {
				return err
			}
			return finishDatabaseSwap(marker, filepath.Dir(databasePath))
		}
		return rollbackDatabaseSwap(databasePath, oldPath, newPath, marker, state.OriginalExists)
	case databaseSwapPrepared, databaseSwapOriginalMoved:
		if state.Phase == databaseSwapPrepared && state.OriginalExists && !databaseSetExists(oldPath) {
			if err := restoreMovedSidecars(oldPath, databasePath); err != nil {
				return err
			}
			if err := removeDatabaseSet(newPath); err != nil {
				return err
			}
			return finishDatabaseSwap(marker, filepath.Dir(databasePath))
		}
		return rollbackDatabaseSwap(databasePath, oldPath, newPath, marker, state.OriginalExists)
	default:
		return errors.New("database restore state is invalid")
	}
}

// ReplaceDatabase exchanges a prepared candidate with the live database and
// restores the original file set if validation of the published database fails.
func ReplaceDatabase(ctx context.Context, databasePath string) error {
	if err := ReconcileDatabaseSwap(ctx, databasePath); err != nil {
		return fmt.Errorf("reconcile interrupted database restore: %w", err)
	}
	newPath := RestoreCandidatePath(databasePath)
	oldPath := databasePath + ".restore-old"
	marker := databasePath + ".restore-state"
	liveExists, err := regularFileExists(databasePath)
	if err != nil {
		return err
	}
	if !liveExists && databaseSidecarsExist(databasePath) {
		return errors.New("live application database sidecar state is ambiguous")
	}
	if !databaseSetExists(newPath) {
		return errors.New("restored application database candidate is missing")
	}
	if databaseSetExists(oldPath) {
		return errors.New("previous application database restore is not resolved")
	}
	if err := writeDatabaseSwapState(marker, databaseSwapPrepared, liveExists); err != nil {
		return err
	}
	if liveExists {
		if err := renameDatabaseSet(databasePath, oldPath); err != nil {
			return rollbackAfterSwapError(databasePath, oldPath, newPath, marker, liveExists, err)
		}
		if err := syncDatabaseDirectory(databasePath); err != nil {
			return rollbackAfterSwapError(databasePath, oldPath, newPath, marker, liveExists, err)
		}
	}
	if err := writeDatabaseSwapState(marker, databaseSwapOriginalMoved, liveExists); err != nil {
		return rollbackAfterSwapError(databasePath, oldPath, newPath, marker, liveExists, err)
	}
	if err := renameDatabaseSet(newPath, databasePath); err != nil {
		return rollbackAfterSwapError(databasePath, oldPath, newPath, marker, liveExists, err)
	}
	if err := syncDatabaseDirectory(databasePath); err != nil {
		return rollbackAfterSwapError(databasePath, oldPath, newPath, marker, liveExists, err)
	}
	if err := writeDatabaseSwapState(marker, databaseSwapCandidateInstalled, liveExists); err != nil {
		return rollbackAfterSwapError(databasePath, oldPath, newPath, marker, liveExists, err)
	}
	if err := validateCurrentDatabase(ctx, databasePath); err != nil {
		return rollbackAfterSwapError(databasePath, oldPath, newPath, marker, liveExists, err)
	}
	// Once the candidate-installed marker is durable and the published database
	// validates, restoration is committed. Cleanup may be retried at startup;
	// reporting failure after this point would falsely promise the old database.
	if err := removeDatabaseSet(oldPath); err != nil {
		return nil
	}
	_ = finishDatabaseSwap(marker, filepath.Dir(databasePath))
	return nil
}

func validateCurrentDatabase(ctx context.Context, path string) error {
	db, err := Open(ctx, path, false)
	if err != nil {
		return err
	}
	defer db.Close()
	report, err := db.InspectIntegrity(ctx, IntegrityCurrentBaseline)
	if err != nil {
		return err
	}
	if !report.Healthy {
		return errors.New("restored application database integrity is unhealthy")
	}
	return db.Checkpoint(ctx)
}

func rollbackAfterSwapError(databasePath, oldPath, newPath, marker string, originalExists bool, cause error) error {
	rollbackErr := rollbackDatabaseSwap(databasePath, oldPath, newPath, marker, originalExists)
	return errors.Join(cause, rollbackErr)
}

func rollbackDatabaseSwap(databasePath, oldPath, newPath, marker string, originalExists bool) error {
	if originalExists {
		if !databaseSetExists(oldPath) {
			return errors.New("original application database is unavailable during restore rollback")
		}
		if err := removeDatabaseSet(databasePath); err != nil {
			return err
		}
		if err := renameDatabaseSet(oldPath, databasePath); err != nil {
			return err
		}
	} else if err := removeDatabaseSet(databasePath); err != nil {
		return err
	}
	if err := removeDatabaseSet(newPath); err != nil {
		return err
	}
	return finishDatabaseSwap(marker, filepath.Dir(databasePath))
}

func finishDatabaseSwap(marker, dir string) error {
	if err := os.Remove(marker); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncDirectoryPath(dir)
}

func databaseSetExists(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular()
}

func regularFileExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, errors.New("application database path is not a regular file")
	}
	return true, nil
}

func databaseSidecarsExist(path string) bool {
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Lstat(path + suffix); err == nil {
			return true
		}
	}
	return false
}

func renameDatabaseSet(source, destination string) error {
	movedSuffixes := make([]string, 0, 3)
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if err := os.Rename(source+suffix, destination+suffix); err == nil {
			movedSuffixes = append(movedSuffixes, suffix)
		} else if !errors.Is(err, os.ErrNotExist) {
			_ = moveSuffixesBack(destination, source, movedSuffixes)
			return err
		}
	}
	if err := os.Rename(source, destination); err != nil {
		_ = moveSuffixesBack(destination, source, movedSuffixes)
		return err
	}
	return nil
}

func moveSuffixesBack(source, destination string, suffixes []string) error {
	var result error
	for index := len(suffixes) - 1; index >= 0; index-- {
		if err := os.Rename(source+suffixes[index], destination+suffixes[index]); err != nil {
			result = errors.Join(result, err)
		}
	}
	return result
}

func restoreMovedSidecars(source, destination string) error {
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Lstat(source + suffix); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return err
		}
		if _, err := os.Lstat(destination + suffix); err == nil {
			return errors.New("database restore sidecar state is ambiguous")
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.Rename(source+suffix, destination+suffix); err != nil {
			return err
		}
	}
	return nil
}

func removeDatabaseSet(path string) error {
	var result error
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		if err := os.Remove(path + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			result = errors.Join(result, err)
		}
	}
	return result
}

func readDatabaseSwapState(path string) (databaseSwapState, bool, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return databaseSwapState{}, false, nil
	}
	if err != nil {
		return databaseSwapState{}, false, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 4097))
	decoder.DisallowUnknownFields()
	var state databaseSwapState
	if err := decoder.Decode(&state); err != nil {
		return databaseSwapState{}, false, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return databaseSwapState{}, false, errors.New("database restore state contains extra data")
	}
	if state.FormatVersion != databaseSwapFormatVersion {
		return databaseSwapState{}, false, errors.New("database restore state format is not supported")
	}
	return state, true, nil
}

func writeDatabaseSwapState(path, phase string, originalExists bool) error {
	dir := filepath.Dir(path)
	file, err := os.CreateTemp(dir, ".profiledeck-restore-state-*.tmp")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	defer func() {
		if file != nil {
			_ = file.Close()
		}
		_ = os.Remove(tempPath)
	}()
	if err := os.Chmod(tempPath, 0o600); err != nil {
		return err
	}
	if err := json.NewEncoder(file).Encode(databaseSwapState{
		FormatVersion: databaseSwapFormatVersion, Phase: phase, OriginalExists: originalExists,
	}); err != nil {
		return err
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	file = nil
	if err := errors.Join(syncErr, closeErr); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	return syncDirectoryPath(dir)
}

func syncDatabaseDirectory(databasePath string) error {
	return syncDirectoryPath(filepath.Dir(databasePath))
}

func syncDirectoryPath(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}
