package profile

import (
	"context"
	"errors"

	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	codexpreset "github.com/strahe/profiledeck/internal/codex/preset"
	doctorcore "github.com/strahe/profiledeck/internal/doctor"
	"github.com/strahe/profiledeck/internal/store"
)

// InspectHealth validates Codex profile resources and bindings without reading
// the active working copy. Target inspection is composed by the app facade.
func InspectHealth(ctx context.Context, db *store.Store) []doctorcore.Finding {
	if db == nil {
		return nil
	}
	provider, err := db.GetProvider(ctx, codexconfig.ProviderID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return []doctorcore.Finding{{ID: "codex_provider_check_failed", Level: doctorcore.LevelWarning, Message: "failed to inspect Codex provider"}}
	}
	findings := []doctorcore.Finding{}
	metadata, err := codexpreset.DecodeProviderMetadata(provider.MetadataJSON)
	if provider.AdapterID != codexconfig.AdapterID || err != nil || !metadata.Compatible() {
		findings = append(findings, doctorcore.Finding{
			ID: "codex_preset_v2_invalid", Level: doctorcore.LevelError,
			Message: "Codex provider is not compatible with preset v2",
		})
	}
	credentialBindings, err := db.ListProfileCredentialBindingsByProvider(ctx, codexconfig.ProviderID)
	if err != nil {
		return append(findings, doctorcore.Finding{ID: "codex_binding_check_failed", Level: doctorcore.LevelWarning, Message: "failed to inspect Codex profile bindings"})
	}
	configBindings, err := db.ListProfileConfigSetBindingsByProvider(ctx, codexconfig.ProviderID)
	if err != nil {
		return append(findings, doctorcore.Finding{ID: "codex_binding_check_failed", Level: doctorcore.LevelWarning, Message: "failed to inspect Codex profile bindings"})
	}
	credentialBindingsByProfile := map[string][]store.ProfileCredentialBinding{}
	configBindingsByProfile := map[string][]store.ProfileConfigSetBinding{}
	profileIDs := map[string]struct{}{}
	for _, binding := range credentialBindings {
		credentialBindingsByProfile[binding.ProfileID] = append(credentialBindingsByProfile[binding.ProfileID], binding)
		profileIDs[binding.ProfileID] = struct{}{}
	}
	for _, binding := range configBindings {
		configBindingsByProfile[binding.ProfileID] = append(configBindingsByProfile[binding.ProfileID], binding)
		profileIDs[binding.ProfileID] = struct{}{}
	}
	if active, activeErr := db.GetActiveState(ctx, codexconfig.ProviderID); activeErr == nil {
		profileIDs[active.ProfileID] = struct{}{}
	} else if !errors.Is(activeErr, store.ErrNotFound) {
		findings = append(findings, doctorcore.Finding{ID: "codex_active_state_check_failed", Level: doctorcore.LevelWarning, Message: "failed to inspect active Codex Profile"})
	}
	for profileID := range profileIDs {
		if _, err := db.GetProfile(ctx, profileID); err != nil {
			findings = append(findings, doctorcore.Finding{
				ID: "codex_profile_missing", Level: doctorcore.LevelError,
				Message: "Codex binding references a missing Profile", Details: map[string]any{"profile_id": profileID},
			})
			continue
		}
		profileConfigBindings := configBindingsByProfile[profileID]
		if len(profileConfigBindings) == 0 {
			findings = append(findings, doctorcore.Finding{
				ID: "codex_config_binding_missing", Level: doctorcore.LevelError,
				Message: "Codex profile config binding is missing", Details: map[string]any{"profile_id": profileID},
			})
		} else if len(profileConfigBindings) != 1 || profileConfigBindings[0].SlotID != codexpreset.ConfigSetSlotUserConfig {
			findings = append(findings, doctorcore.Finding{
				ID: "codex_config_binding_invalid", Level: doctorcore.LevelError,
				Message: "Codex profile config binding is invalid", Details: map[string]any{"profile_id": profileID},
			})
		} else if _, err := RequireConfigSet(ctx, db, profileConfigBindings[0].ConfigSetID); err != nil {
			findings = append(findings, doctorcore.Finding{
				ID: "codex_config_set_invalid", Level: doctorcore.LevelError,
				Message: "Codex profile references a missing or invalid config set",
				Details: map[string]any{"profile_id": profileID, "config_set_id": profileConfigBindings[0].ConfigSetID},
			})
		}
		profileCredentialBindings := credentialBindingsByProfile[profileID]
		if len(profileCredentialBindings) == 0 {
			findings = append(findings, doctorcore.Finding{
				ID: "codex_login_binding_missing", Level: doctorcore.LevelError,
				Message: "Codex profile login binding is missing", Details: map[string]any{"profile_id": profileID},
			})
		} else if len(profileCredentialBindings) != 1 || profileCredentialBindings[0].SlotID != codexpreset.CredentialSlotAuth {
			findings = append(findings, doctorcore.Finding{
				ID: "codex_login_binding_invalid", Level: doctorcore.LevelError,
				Message: "Codex profile login binding is invalid", Details: map[string]any{"profile_id": profileID},
			})
		} else if _, err := RequireAuthCredential(ctx, db, profileCredentialBindings[0].CredentialID); err != nil {
			findings = append(findings, doctorcore.Finding{
				ID: "codex_login_state_invalid", Level: doctorcore.LevelError,
				Message: "Codex profile references missing or invalid login state", Details: map[string]any{"profile_id": profileID},
			})
		}
	}
	configSets, err := db.ListProviderConfigSets(ctx, codexconfig.ProviderID, codexpreset.ConfigSetKindTOML)
	if err != nil {
		return append(findings, doctorcore.Finding{ID: "codex_config_set_check_failed", Level: doctorcore.LevelWarning, Message: "failed to inspect Codex config sets"})
	}
	for _, configSet := range configSets {
		if _, err := RequireConfigSet(ctx, db, configSet.ID); err != nil {
			findings = append(findings, doctorcore.Finding{
				ID: "codex_config_set_invalid", Level: doctorcore.LevelError,
				Message: "Codex config set payload is invalid", Details: map[string]any{"config_set_id": configSet.ID},
			})
		}
	}
	return findings
}
