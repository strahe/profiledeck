// Package recoverycleanup owns durable cleanup of completed switch recovery
// material. Callers must hold the shared switch lock while reconciling.
package recoverycleanup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/runtime"
	"github.com/strahe/profiledeck/internal/store"
	"github.com/strahe/profiledeck/internal/targetfs"
)

type State string

const (
	StateClean    State = "clean"
	StateRequired State = "required"
	StateUnknown  State = "unknown"
)

type Inspection struct {
	State State
}

func (inspection Inspection) CleanupRequired() bool {
	return inspection.State != StateClean
}

type RetryRecoveryCleanupResult struct {
	RecoveryCleanupCompleted bool `json:"recovery_cleanup_completed"`
}

type Service struct {
	recoveryRoot  string
	syncDirectory func(string) error
	remove        func(string) error
	removeAll     func(string) error
}

func NewService(paths runtime.Paths) *Service {
	return &Service{
		recoveryRoot: paths.Recovery, syncDirectory: targetfs.SyncDirectory,
		remove: os.Remove, removeAll: os.RemoveAll,
	}
}

func (service *Service) Inspect(ctx context.Context, db *store.Store) (Inspection, error) {
	if service == nil || db == nil || strings.TrimSpace(service.recoveryRoot) == "" {
		return Inspection{State: StateUnknown}, nil
	}
	required, err := db.RecoveryCleanupRequired(ctx)
	if err != nil {
		return Inspection{}, err
	}
	unresolved, valid, err := unresolvedSwitchOperationIDs(ctx, db)
	if err != nil {
		return Inspection{}, err
	}
	if !valid {
		return Inspection{State: StateUnknown}, nil
	}

	info, err := os.Lstat(service.recoveryRoot)
	if errors.Is(err, os.ErrNotExist) {
		return Inspection{State: StateRequired}, nil
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return Inspection{State: StateUnknown}, nil
	}
	entries, err := os.ReadDir(service.recoveryRoot)
	if err != nil {
		return Inspection{State: StateUnknown}, nil
	}
	for _, entry := range entries {
		if _, keep := unresolved[entry.Name()]; !keep {
			return Inspection{State: StateRequired}, nil
		}
	}
	if required {
		return Inspection{State: StateRequired}, nil
	}
	return Inspection{State: StateClean}, nil
}

func (service *Service) CleanupRequired(ctx context.Context, db *store.Store) (bool, error) {
	inspection, err := service.Inspect(ctx, db)
	if err != nil {
		return false, err
	}
	return inspection.CleanupRequired(), nil
}

// ReconcileLocked removes only recovery material that cannot belong to an
// unresolved root switch. The switch lock is the authority that closes the
// inspection-to-delete race with other ProfileDeck processes.
func (service *Service) ReconcileLocked(ctx context.Context, db *store.Store) (RetryRecoveryCleanupResult, error) {
	if service == nil || db == nil || strings.TrimSpace(service.recoveryRoot) == "" {
		return RetryRecoveryCleanupResult{}, cleanupRequiredError()
	}
	if _, err := db.RecoveryCleanupRequired(ctx); err != nil {
		return RetryRecoveryCleanupResult{}, reconcileStateError(err)
	}
	unresolved, valid, err := unresolvedSwitchOperationIDs(ctx, db)
	if err != nil || !valid {
		return RetryRecoveryCleanupResult{}, cleanupRequiredError()
	}
	if err := db.RequireRecoveryCleanup(ctx); err != nil {
		return RetryRecoveryCleanupResult{}, reconcileStateError(err)
	}
	rootInfo, err := service.ensureRecoveryRoot()
	if err != nil {
		return RetryRecoveryCleanupResult{}, cleanupRequiredError()
	}
	entries, err := os.ReadDir(service.recoveryRoot)
	if err != nil {
		return RetryRecoveryCleanupResult{}, cleanupRequiredError()
	}

	var cleanupErr error
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return RetryRecoveryCleanupResult{}, err
		}
		if _, keep := unresolved[entry.Name()]; keep {
			continue
		}
		if err := validateRecoveryRoot(service.recoveryRoot, rootInfo); err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
			break
		}
		child := filepath.Join(service.recoveryRoot, entry.Name())
		childInfo, err := os.Lstat(child)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
			continue
		}
		if childInfo.IsDir() && childInfo.Mode()&os.ModeSymlink == 0 {
			err = service.removeAll(child)
		} else {
			err = service.remove(child)
		}
		cleanupErr = errors.Join(cleanupErr, err)
	}
	if err := validateRecoveryRoot(service.recoveryRoot, rootInfo); err != nil {
		cleanupErr = errors.Join(cleanupErr, err)
	} else if err := service.syncDirectory(service.recoveryRoot); err != nil {
		cleanupErr = errors.Join(cleanupErr, err)
	}
	if cleanupErr != nil {
		return RetryRecoveryCleanupResult{}, cleanupRequiredError()
	}
	if err := db.ClearRecoveryCleanup(ctx); err != nil {
		return RetryRecoveryCleanupResult{}, reconcileStateError(err)
	}
	return RetryRecoveryCleanupResult{RecoveryCleanupCompleted: true}, nil
}

func (service *Service) ensureRecoveryRoot() (os.FileInfo, error) {
	info, err := os.Lstat(service.recoveryRoot)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(service.recoveryRoot, 0o700); err != nil {
			return nil, err
		}
		info, err = os.Lstat(service.recoveryRoot)
	}
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, errors.New("recovery root is not a directory")
	}
	if err := os.Chmod(service.recoveryRoot, 0o700); err != nil {
		return nil, err
	}
	// Repeating the parent sync makes a prior create-then-sync failure
	// recoverable without remembering another filesystem-only state.
	if err := service.syncDirectory(filepath.Dir(service.recoveryRoot)); err != nil {
		return nil, err
	}
	return info, nil
}

func unresolvedSwitchOperationIDs(ctx context.Context, db *store.Store) (map[string]struct{}, bool, error) {
	operations, err := db.ListIncompleteOperations(ctx)
	if err != nil {
		return nil, false, err
	}
	result := make(map[string]struct{})
	for _, operation := range operations {
		if operation.OperationType != store.OperationTypeSwitch {
			continue
		}
		if !safeOperationID(operation.ID) {
			return nil, false, nil
		}
		result[operation.ID] = struct{}{}
	}
	return result, true, nil
}

func safeOperationID(value string) bool {
	if len(value) == 0 || len(value) > 80 || filepath.Base(value) != value {
		return false
	}
	for index, char := range value {
		valid := char >= 'a' && char <= 'z' || char >= '0' && char <= '9'
		if index > 0 {
			valid = valid || char == '.' || char == '_' || char == '-'
		}
		if !valid {
			return false
		}
	}
	return true
}

func validateRecoveryRoot(path string, expected os.FileInfo) error {
	current, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if current.Mode()&os.ModeSymlink != 0 || !current.IsDir() || !os.SameFile(expected, current) {
		return errors.New("recovery root changed")
	}
	return nil
}

func cleanupRequiredError() *apperror.Error {
	return apperror.New(
		apperror.OperationRecoveryCleanupRequired,
		"recovery files still need cleanup before switching or restore can continue",
	)
}

func reconcileStateError(err error) error {
	if errors.Is(err, store.ErrInvalidSystemState) {
		return apperror.New(
			apperror.StoreSchemaInvalid,
			"ProfileDeck local data is not in a valid state; run profiledeck doctor or restore a known-good application backup",
		)
	}
	return cleanupRequiredError()
}
