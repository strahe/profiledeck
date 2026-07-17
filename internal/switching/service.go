package switching

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/strahe/profiledeck/internal/agent"
	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/maintenance"
	"github.com/strahe/profiledeck/internal/runtime"
	"github.com/strahe/profiledeck/internal/store"
	"github.com/strahe/profiledeck/internal/targetfs"
)

const operationRandomBytes = 6

type Service struct {
	paths          runtime.Paths
	stores         store.Factory
	policy         agent.Policy
	dependencies   Dependencies
	sharedLockGate chan struct{}
}

func NewService(paths runtime.Paths, stores store.Factory, policy agent.Policy, dependencies Dependencies) *Service {
	sharedLockGate := make(chan struct{}, 1)
	sharedLockGate <- struct{}{}
	return &Service{
		paths:          paths,
		stores:         stores,
		policy:         policy,
		dependencies:   dependencies,
		sharedLockGate: sharedLockGate,
	}
}

func (service *Service) RunMaintenance(ctx context.Context, req maintenance.Request, mutation maintenance.Func) error {
	if mutation == nil {
		return apperror.New(apperror.CommandFailed, "maintenance mutation is required")
	}
	operationID, err := newOperationID(req.Operation, time.Now())
	if err != nil {
		return apperror.Wrap(apperror.OperationCreateFailed, "failed to create maintenance operation id", err)
	}
	db, err := service.stores.OpenHealthy(ctx, false)
	if err != nil {
		return err
	}
	defer db.Close()
	lock, err := acquireLock(service.paths.Lock, operationID)
	if err != nil {
		return err
	}
	defer lock.Release()
	if req.ProviderID != "" {
		if err := service.requireProviderWithStore(ctx, db, req.ProviderID); err != nil {
			return err
		}
	}

	return db.WithTransaction(ctx, func(tx *store.Store) error {
		if err := mutation(ctx, tx, operationID); err != nil {
			return err
		}
		if !req.Record {
			return nil
		}
		metadata := strings.TrimSpace(req.MetadataJSON)
		if metadata == "" {
			metadata = "{}"
		}
		if _, err := tx.CreateAppliedMaintenanceOperation(ctx, store.CreateAppliedMaintenanceOperationParams{
			ID: operationID, ProfileID: req.ProfileID, ProviderID: req.ProviderID, MetadataJSON: metadata, SetActive: req.SetActive,
		}); err != nil {
			return apperror.Wrap(apperror.OperationCreateFailed, "failed to record maintenance operation", err)
		}
		return nil
	})
}

func (service *Service) RunWithSharedLock(ctx context.Context, operation string, run func(context.Context) error) error {
	if run == nil {
		return apperror.New(apperror.CommandFailed, "shared-lock operation is required")
	}
	// Queue same-process runtime work so it cannot be misreported as another
	// ProfileDeck process; the filesystem lock still rejects external contention.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-service.sharedLockGate:
	}
	defer func() { service.sharedLockGate <- struct{}{} }()

	operationID, err := newOperationID(operation, time.Now())
	if err != nil {
		return apperror.Wrap(apperror.OperationCreateFailed, "failed to create shared-lock operation id", err)
	}
	lock, err := acquireLock(service.paths.Lock, operationID)
	if err != nil {
		return err
	}
	defer lock.Release()
	return run(ctx)
}

func (service *Service) RequireProvider(ctx context.Context, providerID string) error {
	if service.policy == nil {
		return nil
	}
	return service.policy.RequireProvider(ctx, providerID)
}

func (service *Service) requireProviderWithStore(ctx context.Context, db *store.Store, providerID string) error {
	if service.policy == nil {
		return nil
	}
	if policy, ok := service.policy.(agent.StorePolicy); ok {
		return policy.RequireProviderWithStore(ctx, db, providerID)
	}
	return service.policy.RequireProvider(ctx, providerID)
}

func newOperationID(operation string, now time.Time) (string, error) {
	operation = strings.Trim(strings.TrimSpace(operation), "-")
	if operation == "" {
		operation = "maintenance"
	}
	randomBytes := make([]byte, operationRandomBytes)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-%d-%s", operation, now.UnixMilli(), hex.EncodeToString(randomBytes)), nil
}

func acquireLock(path, owner string) (targetfs.Lock, error) {
	lock, err := targetfs.AcquireLock(path, owner)
	if err == nil {
		return lock, nil
	}
	var targetErr *targetfs.Error
	if !errors.As(err, &targetErr) {
		return targetfs.Lock{}, err
	}
	code := apperror.CommandFailed
	switch targetErr.Kind {
	case targetfs.KindLockHeld, targetfs.KindLockFailed:
		code = apperror.LockAcquireFailed
	case targetfs.KindTargetChanged:
		code = apperror.TargetChanged
	}
	appErr := apperror.Wrap(code, targetErr.Message, err)
	for key, value := range targetErr.Details {
		appErr = appErr.WithDetail(key, value)
	}
	return targetfs.Lock{}, appErr
}
