package profile

import (
	"context"
	"errors"
	"sort"

	"github.com/strahe/profiledeck/internal/apperror"
	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	codexpreset "github.com/strahe/profiledeck/internal/codex/preset"
	profilebundle "github.com/strahe/profiledeck/internal/codex/profilebundle"
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

// BuildBundle reads stored Codex resource bindings for the explicit sensitive
// export path. It never reads or writes active working-copy files.
func BuildBundle(ctx context.Context, db *store.Store, profileIDs []string) (profilebundle.Bundle, error) {
	fullExport := len(profileIDs) == 0
	provider, err := db.GetProvider(ctx, codexconfig.ProviderID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return profilebundle.Bundle{}, mapProviderStoreError(err)
	}
	if err == nil {
		metadata, metadataErr := codexpreset.DecodeProviderMetadata(provider.MetadataJSON)
		if provider.AdapterID != codexconfig.AdapterID || metadataErr != nil || !metadata.Compatible() {
			return profilebundle.Bundle{}, apperror.New(apperror.CodexInvalid, "stored Codex provider metadata is invalid")
		}
	}
	targets, err := AllStoredBindingTargets(ctx, db)
	if err != nil {
		return profilebundle.Bundle{}, err
	}
	grouped := make(map[string][]store.ProfileTarget)
	for _, target := range targets {
		grouped[target.ProfileID] = append(grouped[target.ProfileID], target)
	}
	if len(profileIDs) == 0 {
		profileIDs = make([]string, 0, len(grouped))
		for profileID := range grouped {
			profileIDs = append(profileIDs, profileID)
		}
		sort.Strings(profileIDs)
	}

	profiles := make([]profilebundle.Profile, 0, len(profileIDs))
	credentials := make(map[string]profilebundle.Credential)
	configSets := make(map[string]profilebundle.ConfigSet)
	for _, profileID := range profileIDs {
		storedProfile, err := db.GetProfile(ctx, profileID)
		if err != nil {
			return profilebundle.Bundle{}, mapProfileStoreError(err)
		}
		profileTargets := grouped[profileID]
		configTarget, authTarget, err := FullProfileTargets(profileID, profileTargets)
		if err != nil || len(profileTargets) != 2 {
			return profilebundle.Bundle{}, apperror.New(apperror.CodexInvalid, "Codex profile is not exportable as a full profile").WithDetail("profile_id", profileID)
		}
		credentialID, err := CredentialIDFromTarget(authTarget)
		if err != nil {
			return profilebundle.Bundle{}, err
		}
		credential, err := RequireAuthCredential(ctx, db, credentialID)
		if err != nil {
			return profilebundle.Bundle{}, err
		}
		configSetID, err := ConfigSetIDFromTarget(configTarget)
		if err != nil {
			return profilebundle.Bundle{}, err
		}
		configSet, err := RequireConfigSet(ctx, db, configSetID)
		if err != nil {
			return profilebundle.Bundle{}, err
		}
		profiles = append(profiles, profilebundle.Profile{
			ID: storedProfile.ID, Name: storedProfile.Name, Description: storedProfile.Description,
			CredentialID: credential.ID, ConfigSetID: configSet.ID,
		})
		credentials[credential.ID] = profilebundle.Credential{
			ID: credential.ID, Kind: credential.CredentialKind,
			PayloadJSON: credential.PayloadJSON, PayloadSHA256: credential.PayloadSHA256,
		}
		configSets[configSet.ID] = configSetBundleRecord(configSet)
	}
	if fullExport {
		allConfigSets, err := db.ListProviderConfigSets(ctx, codexconfig.ProviderID, codexpreset.ConfigSetKindTOML)
		if err != nil {
			return profilebundle.Bundle{}, err
		}
		for _, configSet := range allConfigSets {
			configSets[configSet.ID] = configSetBundleRecord(configSet)
		}
	}

	credentialList := make([]profilebundle.Credential, 0, len(credentials))
	for _, credential := range credentials {
		credentialList = append(credentialList, credential)
	}
	configSetList := make([]profilebundle.ConfigSet, 0, len(configSets))
	for _, configSet := range configSets {
		configSetList = append(configSetList, configSet)
	}
	return profilebundle.New(profiles, credentialList, configSetList), nil
}

func configSetBundleRecord(configSet store.ProviderConfigSet) profilebundle.ConfigSet {
	return profilebundle.ConfigSet{
		ID: configSet.ID, Kind: configSet.ConfigKind, Name: configSet.Name, Description: configSet.Description,
		PayloadText: configSet.PayloadText, PayloadSHA256: configSet.PayloadSHA256,
	}
}
