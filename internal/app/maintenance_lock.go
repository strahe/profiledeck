package app

import (
	"context"
	"strings"
	"time"

	"github.com/strahe/profiledeck/internal/store"
	"github.com/strahe/profiledeck/internal/targetfs"
)

func openLockedMaintenanceStore(ctx context.Context, configDir, operation string) (*store.Store, targetfs.Lock, string, error) {
	_, paths, err := resolveRuntime(configDir)
	if err != nil {
		return nil, targetfs.Lock{}, "", err
	}
	db, err := openHealthyStore(ctx, configDir, false)
	if err != nil {
		return nil, targetfs.Lock{}, "", err
	}
	operationID, err := newSwitchOperationID(time.Now())
	if err != nil {
		_ = db.Close()
		return nil, targetfs.Lock{}, "", WrapError(ErrorOperationCreateFailed, "failed to create maintenance operation id", err)
	}
	operationID = strings.Replace(operationID, "switch-", operation+"-", 1)
	// Mutations that can change a plan definition share the external-target lock
	// so a switch cannot rebuild against one definition and commit against another.
	lock, err := acquireSwitchLock(paths.Lock, operationID)
	if err != nil {
		_ = db.Close()
		return nil, targetfs.Lock{}, "", err
	}
	return db, lock, operationID, nil
}
