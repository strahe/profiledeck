package grokbuild

import (
	"context"

	"github.com/strahe/profiledeck/internal/doctor"
	grokconfig "github.com/strahe/profiledeck/internal/grokbuild/config"
	grokprofile "github.com/strahe/profiledeck/internal/grokbuild/profile"
	"github.com/strahe/profiledeck/internal/store"
)

func (service *Service) HealthCheck(ctx context.Context, db *store.Store) ([]doctor.Finding, error) {
	targets, err := grokprofile.AllStoredBindingTargets(ctx, db)
	if err != nil {
		return []doctor.Finding{{
			ID: "grok_build_profile_check_failed", Level: doctor.LevelWarning,
			Message: "failed to inspect Grok Build Profiles",
		}}, nil
	}
	findings := []doctor.Finding{}
	for _, target := range targets {
		switch target.TargetID {
		case grokconfig.AuthTargetID:
			id, err := grokprofile.CredentialIDFromTarget(target)
			if err == nil {
				_, err = grokprofile.RequireAuthCredential(ctx, db, id)
			}
			if err != nil {
				findings = append(findings, doctor.Finding{
					ID: "grok_build_login_invalid", Level: doctor.LevelError,
					Message: "Grok Build Profile references a missing or invalid login",
					Details: map[string]any{"profile_id": target.ProfileID},
				})
			}
		case grokconfig.ConfigTargetID:
			id, err := grokprofile.ConfigSetIDFromTarget(target)
			if err == nil {
				_, err = grokprofile.RequireConfigSet(ctx, db, id)
			}
			if err != nil {
				findings = append(findings, doctor.Finding{
					ID: "grok_build_config_set_invalid", Level: doctor.LevelError,
					Message: "Grok Build Profile references a missing or invalid Config Set",
					Details: map[string]any{"profile_id": target.ProfileID},
				})
			}
		}
	}
	return findings, nil
}

func (service *Service) SensitivePaths(ctx context.Context, db *store.Store) ([]string, error) {
	targets, err := grokprofile.AllStoredBindingTargets(ctx, db)
	if err != nil {
		return nil, err
	}
	paths := []string{}
	for _, target := range targets {
		if target.TargetID == grokconfig.AuthTargetID && target.Path != "" {
			paths = append(paths, target.Path)
		}
	}
	return grokprofile.UniqueStrings(paths), nil
}
