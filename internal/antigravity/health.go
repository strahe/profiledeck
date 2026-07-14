package antigravity

import (
	"context"

	agyprofile "github.com/strahe/profiledeck/internal/antigravity/profile"
	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/doctor"
	"github.com/strahe/profiledeck/internal/store"
	switchtarget "github.com/strahe/profiledeck/internal/switching/target"
)

// HealthCheck inspects persisted Antigravity state and its working credential.
func (service *Service) HealthCheck(ctx context.Context, db *store.Store) ([]doctor.Finding, error) {
	findings := agyprofile.InspectHealth(ctx, db, func(ctx context.Context, spec switchtarget.Spec) (switchtarget.Snapshot, error) {
		backend, ok := service.targets.Backend(spec.BackendID())
		if !ok {
			return switchtarget.Snapshot{}, apperror.New(apperror.TargetReadFailed, "Antigravity credential backend is unavailable")
		}
		return backend.Inspect(ctx, spec)
	})
	return findings, nil
}
