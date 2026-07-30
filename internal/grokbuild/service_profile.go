package grokbuild

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/strahe/profiledeck/internal/apperror"
	grokconfig "github.com/strahe/profiledeck/internal/grokbuild/config"
	grokprofile "github.com/strahe/profiledeck/internal/grokbuild/profile"
	"github.com/strahe/profiledeck/internal/profile"
	"github.com/strahe/profiledeck/internal/store"
)

type ProfileListResult struct {
	Profiles []ProfileSummary `json:"profiles"`
}

type ProfileSummary struct {
	Profile                  profile.Profile `json:"profile"`
	ProviderID               string          `json:"provider_id"`
	CredentialID             string          `json:"credential_id,omitempty"`
	CredentialReferenceCount int             `json:"credential_reference_count"`
	ConfigSetID              string          `json:"config_set_id,omitempty"`
	ConfigSetName            string          `json:"config_set_name,omitempty"`
	ConfigSetReferenceCount  int             `json:"config_set_reference_count"`
	Active                   bool            `json:"active"`
	UpdatedAtUnixMS          int64           `json:"updated_at_unix_ms"`
	Warnings                 []string        `json:"warnings,omitempty"`
}

type LoginSummary struct {
	CredentialID    string `json:"credential_id"`
	ReferenceCount  int    `json:"reference_count"`
	UpdatedAtUnixMS int64  `json:"updated_at_unix_ms"`
}

type ProfileDetail struct {
	Summary   ProfileSummary `json:"summary"`
	Login     *LoginSummary  `json:"login,omitempty"`
	ConfigSet *ConfigSet     `json:"config_set,omitempty"`
}

func (service *Service) ListProfiles(ctx context.Context) (ProfileListResult, error) {
	if err := service.requireAccess(ctx); err != nil {
		return ProfileListResult{}, err
	}
	db, err := service.openStore(ctx, true)
	if err != nil {
		return ProfileListResult{}, err
	}
	defer db.Close()
	if _, err := requireProvider(ctx, db); err != nil {
		return ProfileListResult{}, err
	}
	targets, err := grokprofile.AllStoredBindingTargets(ctx, db)
	if err != nil {
		return ProfileListResult{}, err
	}
	grouped := make(map[string][]store.ProfileTarget)
	for _, target := range targets {
		grouped[target.ProfileID] = append(grouped[target.ProfileID], target)
	}
	active, activeExists, err := activeState(ctx, db)
	if err != nil {
		return ProfileListResult{}, err
	}
	ids := make([]string, 0, len(grouped))
	for id := range grouped {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := make([]ProfileSummary, 0, len(ids))
	for _, id := range ids {
		stored, getErr := db.GetProfile(ctx, id)
		if errors.Is(getErr, store.ErrNotFound) {
			continue
		}
		if getErr != nil {
			return ProfileListResult{}, getErr
		}
		summary, _, _, err := profileSummaryFromStore(ctx, db, stored, grouped[id], active, activeExists)
		if err != nil {
			return ProfileListResult{}, err
		}
		result = append(result, summary)
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Active != result[j].Active {
			return result[i].Active
		}
		return result[i].Profile.ID < result[j].Profile.ID
	})
	return ProfileListResult{Profiles: result}, nil
}

func (service *Service) GetProfile(ctx context.Context, rawID string) (ProfileDetail, error) {
	if err := service.requireAccess(ctx); err != nil {
		return ProfileDetail{}, err
	}
	id, appErr := validateID(rawID, apperror.ProfileInvalid)
	if appErr != nil {
		return ProfileDetail{}, appErr
	}
	db, err := service.openStore(ctx, true)
	if err != nil {
		return ProfileDetail{}, err
	}
	defer db.Close()
	if _, err := requireProvider(ctx, db); err != nil {
		return ProfileDetail{}, err
	}
	return getProfileFromStore(ctx, db, id)
}

func getProfileFromStore(ctx context.Context, db *store.Store, id string) (ProfileDetail, error) {
	stored, err := db.GetProfile(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return ProfileDetail{}, apperror.New(apperror.ProfileNotFound, "Grok Build Profile was not found")
	}
	if err != nil {
		return ProfileDetail{}, err
	}
	targets, err := grokprofile.StoredBindingTargets(ctx, db, id)
	if err != nil {
		return ProfileDetail{}, err
	}
	if len(targets) == 0 {
		return ProfileDetail{}, apperror.New(apperror.ProfileNotFound, "Grok Build Profile was not found")
	}
	active, activeExists, err := activeState(ctx, db)
	if err != nil {
		return ProfileDetail{}, err
	}
	summary, login, configSet, err := profileSummaryFromStore(ctx, db, stored, targets, active, activeExists)
	if err != nil {
		return ProfileDetail{}, err
	}
	return ProfileDetail{Summary: summary, Login: login, ConfigSet: configSet}, nil
}

func profileSummaryFromStore(
	ctx context.Context,
	db *store.Store,
	stored store.Profile,
	targets []store.ProfileTarget,
	active store.ActiveState,
	activeExists bool,
) (ProfileSummary, *LoginSummary, *ConfigSet, error) {
	public, err := publicProfile(stored)
	if err != nil {
		return ProfileSummary{}, nil, nil, err
	}
	summary := ProfileSummary{
		Profile: public, ProviderID: grokconfig.ProviderID,
		Active:          activeExists && active.ProfileID == stored.ID,
		UpdatedAtUnixMS: stored.UpdatedAtUnixMS,
	}
	var login *LoginSummary
	var configSet *ConfigSet
	configTarget, authTarget, fullErr := grokprofile.FullProfileTargets(stored.ID, targets)
	if fullErr != nil {
		summary.Warnings = append(summary.Warnings, "Grok Build Profile bindings are missing or invalid")
		return summary, nil, nil, nil
	}
	configSetID, err := grokprofile.ConfigSetIDFromTarget(configTarget)
	if err != nil {
		summary.Warnings = append(summary.Warnings, "Grok Build Config Set binding is invalid")
	} else if value, getErr := grokprofile.RequireConfigSet(ctx, db, configSetID); getErr != nil {
		summary.Warnings = append(summary.Warnings, "Grok Build Config Set is missing or invalid")
	} else {
		activeID := ""
		if summary.Active {
			activeID = configSetID
		}
		publicSet, publicErr := configSetFromStore(ctx, db, value, activeID)
		if publicErr != nil {
			return ProfileSummary{}, nil, nil, publicErr
		}
		configSet = &publicSet
		summary.ConfigSetID = publicSet.ID
		summary.ConfigSetName = publicSet.Name
		summary.ConfigSetReferenceCount = publicSet.ReferenceCount
		if value.UpdatedAtUnixMS > summary.UpdatedAtUnixMS {
			summary.UpdatedAtUnixMS = value.UpdatedAtUnixMS
		}
	}
	credentialID, err := grokprofile.CredentialIDFromTarget(authTarget)
	if err != nil {
		summary.Warnings = append(summary.Warnings, "Grok Build login binding is invalid")
	} else if value, getErr := grokprofile.RequireAuthCredential(ctx, db, credentialID); getErr != nil {
		summary.Warnings = append(summary.Warnings, "Grok Build login is missing or invalid")
	} else {
		references, countErr := grokprofile.CredentialBindingCount(ctx, db, credentialID)
		if countErr != nil {
			return ProfileSummary{}, nil, nil, countErr
		}
		login = &LoginSummary{
			CredentialID: credentialID, ReferenceCount: references,
			UpdatedAtUnixMS: value.UpdatedAtUnixMS,
		}
		summary.CredentialID = credentialID
		summary.CredentialReferenceCount = references
		if value.UpdatedAtUnixMS > summary.UpdatedAtUnixMS {
			summary.UpdatedAtUnixMS = value.UpdatedAtUnixMS
		}
	}
	summary.Warnings = uniqueStrings(summary.Warnings)
	return summary, login, configSet, nil
}

func activeState(ctx context.Context, db *store.Store) (store.ActiveState, bool, error) {
	value, err := db.GetActiveState(ctx, grokconfig.ProviderID)
	if errors.Is(err, store.ErrNotFound) {
		return store.ActiveState{}, false, nil
	}
	if err != nil {
		return store.ActiveState{}, false, apperror.Wrap(apperror.StoreStatusFailed, "failed to read active Grok Build Profile", err)
	}
	return value, true, nil
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}
