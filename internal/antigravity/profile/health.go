package profile

import (
	"context"
	"errors"

	agyauth "github.com/strahe/profiledeck/internal/antigravity/auth"
	agyconfig "github.com/strahe/profiledeck/internal/antigravity/config"
	doctorcore "github.com/strahe/profiledeck/internal/doctor"
	"github.com/strahe/profiledeck/internal/store"
	switchtarget "github.com/strahe/profiledeck/internal/switching/target"
)

// TargetInspector is composed by the app so provider health checks do not own
// target backend selection or external credential-store access.
type TargetInspector func(context.Context, switchtarget.Spec) (switchtarget.Snapshot, error)

// InspectHealth validates agy v2 resource bindings and the active credential-store working copy.
func InspectHealth(ctx context.Context, db *store.Store, inspect TargetInspector) []doctorcore.Finding {
	if db == nil {
		return nil
	}
	provider, err := db.GetProvider(ctx, agyconfig.ProviderID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return []doctorcore.Finding{{ID: "antigravity_provider_check_failed", Level: doctorcore.LevelWarning, Message: "failed to inspect Antigravity provider"}}
	}
	findings := []doctorcore.Finding{}
	if err := ValidateProvider(provider); err != nil {
		findings = append(findings, doctorcore.Finding{
			ID: "antigravity_agy_v2_invalid", Level: doctorcore.LevelError,
			Message: "Antigravity provider is not compatible with agy v2",
		})
	}
	bindings, err := db.ListProfileCredentialBindingsByProvider(ctx, agyconfig.ProviderID)
	if err != nil {
		return append(findings, doctorcore.Finding{ID: "antigravity_binding_check_failed", Level: doctorcore.LevelWarning, Message: "failed to inspect Antigravity profile bindings"})
	}
	grouped := map[string][]store.ProfileCredentialBinding{}
	for _, binding := range bindings {
		grouped[binding.ProfileID] = append(grouped[binding.ProfileID], binding)
	}
	configBindings, err := db.ListProfileConfigSetBindingsByProvider(ctx, agyconfig.ProviderID)
	if err != nil {
		return append(findings, doctorcore.Finding{ID: "antigravity_binding_check_failed", Level: doctorcore.LevelWarning, Message: "failed to inspect Antigravity profile bindings"})
	}
	configBindingCounts := map[string]int{}
	for _, binding := range configBindings {
		configBindingCounts[binding.ProfileID]++
		if _, ok := grouped[binding.ProfileID]; !ok {
			grouped[binding.ProfileID] = nil
		}
	}
	if active, activeErr := db.GetActiveState(ctx, store.ActiveStateScopeProvider, agyconfig.ProviderID); activeErr == nil {
		if _, ok := grouped[active.ProfileID]; !ok {
			grouped[active.ProfileID] = nil
		}
	} else if activeErr != nil && !errors.Is(activeErr, store.ErrNotFound) {
		findings = append(findings, doctorcore.Finding{ID: "antigravity_active_state_check_failed", Level: doctorcore.LevelWarning, Message: "failed to inspect active Antigravity Profile"})
	}
	for profileID, profileBindings := range grouped {
		if _, err := db.GetProfile(ctx, profileID); err != nil {
			findings = append(findings, doctorcore.Finding{
				ID: "antigravity_profile_missing", Level: doctorcore.LevelError,
				Message: "Antigravity binding references a missing Profile", Details: map[string]any{"profile_id": profileID},
			})
			continue
		}
		if len(profileBindings) != 1 || profileBindings[0].SlotID != agyconfig.CredentialSlot || configBindingCounts[profileID] != 0 {
			findings = append(findings, doctorcore.Finding{
				ID: "antigravity_login_binding_invalid", Level: doctorcore.LevelError,
				Message: "Antigravity Profile bindings are missing or unsupported", Details: map[string]any{"profile_id": profileID},
			})
			continue
		}
		if _, err := RequireCredential(ctx, db, profileBindings[0].CredentialID); err != nil {
			findings = append(findings, doctorcore.Finding{
				ID: "antigravity_login_state_invalid", Level: doctorcore.LevelError,
				Message: "Antigravity Profile references missing or invalid login state", Details: map[string]any{"profile_id": profileID},
			})
		}
	}
	if inspect == nil {
		return append(findings, doctorcore.Finding{
			ID: "antigravity_login_unavailable", Level: doctorcore.LevelWarning,
			Message: "Antigravity login could not be read from the system credential store",
		})
	}
	snapshot, err := inspect(ctx, TargetSpec())
	if err != nil {
		return append(findings, doctorcore.Finding{
			ID: "antigravity_login_unavailable", Level: doctorcore.LevelWarning,
			Message: "Antigravity login could not be read from the system credential store",
		})
	}
	if !snapshot.Exists {
		return append(findings, doctorcore.Finding{
			ID: "antigravity_login_missing", Level: doctorcore.LevelWarning,
			Message: "Antigravity is not signed in with agy v2",
		})
	}
	if _, _, err := agyauth.Normalize([]byte(snapshot.Content)); err != nil {
		findings = append(findings, doctorcore.Finding{
			ID: "antigravity_login_invalid", Level: doctorcore.LevelError,
			Message: "Antigravity login is not compatible with agy v2",
		})
	}
	return findings
}
