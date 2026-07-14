package claudecode

import (
	"context"

	"github.com/strahe/profiledeck/internal/apperror"
	claudetarget "github.com/strahe/profiledeck/internal/claudecode/target"
	switchtarget "github.com/strahe/profiledeck/internal/switching/target"
)

func IsKeychainAuthorizationRequired(err error) bool {
	return claudetarget.IsAuthorizationRequired(err)
}

func (service *Service) inspectTarget(ctx context.Context, spec switchtarget.Spec, allowKeychainInteraction bool) (switchtarget.Snapshot, error) {
	backend, ok := service.targets.Backend(spec.BackendID())
	if !ok {
		return switchtarget.Snapshot{}, apperror.New(apperror.TargetReadFailed, "Claude Code credential backend is unavailable")
	}
	if keychainBackend, ok := backend.(interface {
		InspectWithInteraction(context.Context, switchtarget.Spec, bool) (switchtarget.Snapshot, error)
	}); ok {
		snapshot, err := keychainBackend.InspectWithInteraction(ctx, spec, allowKeychainInteraction)
		if err != nil {
			return switchtarget.Snapshot{}, err
		}
		return snapshot, nil
	}
	snapshot, err := backend.Inspect(ctx, spec)
	if err != nil {
		return switchtarget.Snapshot{}, err
	}
	return snapshot, nil
}
