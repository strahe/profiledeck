package profile

import (
	"github.com/strahe/profiledeck/internal/apperror"
	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	"github.com/strahe/profiledeck/internal/store"
)

// FullProfileTargets validates the fixed Config Set and credential target pair.
func FullProfileTargets(profileID string, targets []store.ProfileTarget) (store.ProfileTarget, store.ProfileTarget, error) {
	var configTarget store.ProfileTarget
	var authTarget store.ProfileTarget
	for _, target := range targets {
		switch target.TargetID {
		case codexconfig.TargetID:
			if _, err := ConfigSetIDFromTarget(target); err != nil {
				return store.ProfileTarget{}, store.ProfileTarget{}, err
			}
			configTarget = target
		case codexconfig.AuthTargetID:
			if _, err := CredentialIDFromTarget(target); err != nil {
				return store.ProfileTarget{}, store.ProfileTarget{}, err
			}
			authTarget = target
		}
	}
	if configTarget.TargetID == "" || authTarget.TargetID == "" {
		return store.ProfileTarget{}, store.ProfileTarget{}, apperror.New(apperror.CodexInvalid, "Codex profile is not a valid full profile").WithDetail("profile_id", profileID)
	}
	return configTarget, authTarget, nil
}
