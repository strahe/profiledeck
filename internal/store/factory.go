package store

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/strahe/profiledeck/internal/apperror"
)

// Factory binds a database path without retaining a database connection.
// Each service operation opens and closes its own Store so process entrypoints
// share persistence semantics without sharing connection lifetime.
type Factory struct {
	databasePath string
	accessGate   *AccessGate
}

func NewFactory(databasePath string) Factory {
	return Factory{databasePath: strings.TrimSpace(databasePath), accessGate: newAccessGate()}
}

func (factory Factory) DatabasePath() string {
	return factory.databasePath
}

func (factory Factory) AccessGate() *AccessGate {
	return factory.accessGate
}

func (factory Factory) Open(ctx context.Context, readOnly bool) (*Store, error) {
	if factory.databasePath == "" {
		return nil, apperror.New(apperror.InvalidRuntimePath, "application database path is required")
	}
	accessLease := factory.accessGate.acquireShared(ctx)
	return factory.open(ctx, readOnly, accessLease)
}

func (factory Factory) open(ctx context.Context, readOnly bool, accessLease *accessLease) (*Store, error) {
	db, err := Open(ctx, factory.databasePath, readOnly)
	if err != nil {
		accessLease.close()
		return nil, apperror.Wrap(apperror.StoreOpenFailed, "failed to open application database", err)
	}
	db.accessLease = accessLease
	return db, nil
}

func (factory Factory) OpenHealthy(ctx context.Context, readOnly bool) (*Store, error) {
	if factory.databasePath == "" {
		return nil, apperror.New(apperror.InvalidRuntimePath, "application database path is required")
	}
	accessLease := factory.accessGate.acquireShared(ctx)
	if _, err := os.Stat(factory.databasePath); err != nil {
		accessLease.close()
		if os.IsNotExist(err) {
			return nil, apperror.New(apperror.StoreNotInitialized, "application database is not initialized")
		}
		return nil, apperror.Wrap(apperror.StoreStatusFailed, "failed to inspect application database", err)
	}

	db, err := factory.open(ctx, readOnly, accessLease)
	if err != nil {
		return nil, err
	}
	status, err := db.Status(ctx)
	if err != nil {
		_ = db.Close()
		if errors.Is(err, ErrUnsupportedSchema) {
			return nil, apperror.New(apperror.StoreSchemaUnsupported, apperror.StoreSchemaUnsupportedMessage)
		}
		if errors.Is(err, ErrInvalidMigrationHistory) {
			return nil, apperror.New(apperror.StoreSchemaInvalid, "ProfileDeck local data is not in a valid state; run profiledeck doctor or restore a known-good application backup")
		}
		return nil, apperror.Wrap(apperror.StoreStatusFailed, "failed to inspect application database", err)
	}
	if !status.SchemaHealthy {
		_ = db.Close()
		return nil, apperror.New(apperror.StoreSchemaInvalid, "application database schema is not healthy")
	}
	return db, nil
}
