package codex

import (
	"context"

	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	codexprofile "github.com/strahe/profiledeck/internal/codex/profile"
	"github.com/strahe/profiledeck/internal/doctor"
	"github.com/strahe/profiledeck/internal/store"
)

// HealthCheck inspects persisted Codex state without applying Agent policy.
// The doctor service owns policy filtering so recovery checks stay available.
func (service *Service) HealthCheck(ctx context.Context, db *store.Store) ([]doctor.Finding, error) {
	return codexprofile.InspectHealth(ctx, db), nil
}

// SensitivePaths returns Codex auth working-copy paths for unconditional
// permission checks. It does not return credential payloads.
func (service *Service) SensitivePaths(ctx context.Context, db *store.Store) ([]string, error) {
	targets, err := codexprofile.AllStoredBindingTargets(ctx, db)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(targets))
	for _, target := range targets {
		if target.TargetID == codexconfig.AuthTargetID && target.Path != "" {
			paths = append(paths, target.Path)
		}
	}
	return paths, nil
}
