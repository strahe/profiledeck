package store

import (
	"context"
	"os"
	"strings"

	"github.com/strahe/profiledeck/internal/apperror"
)

// Factory binds a database path without retaining a database connection.
// Each service operation opens and closes its own Store so process entrypoints
// share persistence semantics without sharing connection lifetime.
type Factory struct {
	databasePath string
}

func NewFactory(databasePath string) Factory {
	return Factory{databasePath: strings.TrimSpace(databasePath)}
}

func (factory Factory) DatabasePath() string {
	return factory.databasePath
}

func (factory Factory) Open(ctx context.Context, readOnly bool) (*Store, error) {
	if factory.databasePath == "" {
		return nil, apperror.New(apperror.InvalidRuntimePath, "application database path is required")
	}
	db, err := Open(ctx, factory.databasePath, readOnly)
	if err != nil {
		return nil, apperror.Wrap(apperror.StoreOpenFailed, "failed to open application database", err)
	}
	return db, nil
}

func (factory Factory) OpenHealthy(ctx context.Context, readOnly bool) (*Store, error) {
	if _, err := os.Stat(factory.databasePath); err != nil {
		if os.IsNotExist(err) {
			return nil, apperror.New(apperror.StoreNotInitialized, "application database is not initialized")
		}
		return nil, apperror.Wrap(apperror.StoreStatusFailed, "failed to inspect application database", err)
	}

	db, err := factory.Open(ctx, readOnly)
	if err != nil {
		return nil, err
	}
	status, err := db.Status(ctx)
	if err != nil {
		_ = db.Close()
		return nil, apperror.Wrap(apperror.StoreStatusFailed, "failed to inspect application database", err)
	}
	if !status.SchemaHealthy {
		_ = db.Close()
		return nil, apperror.New(apperror.StoreSchemaInvalid, "application database schema is not healthy")
	}
	return db, nil
}
