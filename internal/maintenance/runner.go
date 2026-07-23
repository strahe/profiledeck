// Package maintenance defines narrow database-mutation and shared-lock runners.
package maintenance

import (
	"context"

	"github.com/strahe/profiledeck/internal/store"
)

type Request struct {
	Operation         string
	ProfileID         string
	ProviderID        string
	RelatedProfileIDs []string
	ActiveProfileID   string
	MetadataJSON      string
	Record            bool
}

type Func func(context.Context, *store.Store, string) error

type Runner interface {
	RunMaintenance(context.Context, Request, Func) error
}

type SharedLockRunner interface {
	RunWithSharedLock(context.Context, string, func(context.Context) error) error
}
