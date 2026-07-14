package claudecode

import (
	"context"
	"errors"

	"github.com/strahe/profiledeck/internal/apperror"
	claudeprofile "github.com/strahe/profiledeck/internal/claudecode/profile"
	"github.com/strahe/profiledeck/internal/doctor"
	"github.com/strahe/profiledeck/internal/store"
	switchtarget "github.com/strahe/profiledeck/internal/switching/target"
)

// HealthCheck inspects persisted Claude Code state without prompting for
// Keychain access. Authorization requirements are reported as diagnostics.
func (service *Service) HealthCheck(ctx context.Context, db *store.Store) ([]doctor.Finding, error) {
	findings := claudeprofile.InspectHealth(ctx, db, func(ctx context.Context, spec switchtarget.Spec) claudeprofile.TargetInspection {
		snapshot, inspectErr := service.inspectTarget(ctx, spec, false)
		inspection := claudeprofile.TargetInspection{Snapshot: snapshot, Err: inspectErr}
		if inspectErr == nil {
			return inspection
		}
		inspection.KeychainAuthorizationRequired = IsKeychainAuthorizationRequired(inspectErr)
		var appErr *apperror.Error
		inspection.KeychainReferenceInvalid = errors.As(inspectErr, &appErr) &&
			(appErr.Code == apperror.ClaudeCodeInvalid || appErr.Code == apperror.TargetChanged)
		return inspection
	})
	return findings, nil
}
