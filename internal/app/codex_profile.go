package app

import (
	"context"
	"errors"
	"sort"

	"github.com/pelletier/go-toml/v2"

	codexauth "github.com/strahe/profiledeck/internal/codex/auth"
	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	"github.com/strahe/profiledeck/internal/store"
)

type ListCodexProfilesRequest struct {
	ConfigDir string
}

type GetCodexProfileRequest struct {
	ConfigDir string
	ProfileID string
}

type CodexProfileListResult struct {
	Profiles []CodexProfileSummary `json:"profiles"`
}

type CodexProfileSummary struct {
	Profile                  Profile  `json:"profile"`
	ProviderID               string   `json:"provider_id"`
	CredentialID             string   `json:"credential_id,omitempty"`
	CredentialReferenceCount int      `json:"credential_reference_count"`
	CodexAccountID           string   `json:"codex_account_id,omitempty"`
	ConfigSetID              string   `json:"config_set_id,omitempty"`
	ConfigSetName            string   `json:"config_set_name,omitempty"`
	ConfigSetReferenceCount  int      `json:"config_set_reference_count"`
	Model                    string   `json:"model,omitempty"`
	ModelProvider            string   `json:"model_provider,omitempty"`
	OpenAIBaseURL            string   `json:"openai_base_url,omitempty"`
	Active                   bool     `json:"active"`
	ActiveOperationID        string   `json:"active_operation_id,omitempty"`
	UpdatedAtUnixMS          int64    `json:"updated_at_unix_ms"`
	Warnings                 []string `json:"warnings,omitempty"`
}

type CodexLoginStateSummary struct {
	CredentialID    string `json:"credential_id"`
	CodexAccountID  string `json:"codex_account_id,omitempty"`
	ReferenceCount  int    `json:"reference_count"`
	UpdatedAtUnixMS int64  `json:"updated_at_unix_ms"`
}

type CodexProfileDetail struct {
	Summary   CodexProfileSummary     `json:"summary"`
	Login     *CodexLoginStateSummary `json:"login,omitempty"`
	ConfigSet *CodexConfigSet         `json:"config_set,omitempty"`
}

func ListCodexProfiles(ctx context.Context, req ListCodexProfilesRequest) (CodexProfileListResult, error) {
	db, err := openHealthyStore(ctx, req.ConfigDir, true)
	if err != nil {
		return CodexProfileListResult{}, err
	}
	defer db.Close()
	summaries, err := listCodexProfileSummaries(ctx, db)
	if err != nil {
		return CodexProfileListResult{}, err
	}
	return CodexProfileListResult{Profiles: summaries}, nil
}

func GetCodexProfile(ctx context.Context, req GetCodexProfileRequest) (CodexProfileDetail, error) {
	profileID, appErr := validateID(req.ProfileID, ErrorProfileInvalid)
	if appErr != nil {
		return CodexProfileDetail{}, appErr
	}
	db, err := openHealthyStore(ctx, req.ConfigDir, true)
	if err != nil {
		return CodexProfileDetail{}, err
	}
	defer db.Close()
	return getCodexProfileFromStore(ctx, db, profileID)
}

func getCodexProfileFromStore(ctx context.Context, db *store.Store, profileID string) (CodexProfileDetail, error) {
	profile, err := db.GetProfile(ctx, profileID)
	if err != nil {
		return CodexProfileDetail{}, mapProfileStoreError(err)
	}
	targets, err := storedCodexBindingTargets(ctx, db, profileID)
	if err != nil {
		return CodexProfileDetail{}, WrapError(ErrorStoreStatusFailed, "failed to list Codex profile targets", err)
	}
	if len(targets) == 0 {
		return CodexProfileDetail{}, NewError(ErrorProfileNotFound, "Codex profile not found").WithDetail("profile_id", profileID)
	}
	active, activeExists, err := codexActiveState(ctx, db)
	if err != nil {
		return CodexProfileDetail{}, err
	}
	summary, login, configSet, _, err := codexProfileSummaryFromStore(ctx, db, profile, targets, active, activeExists)
	if err != nil {
		return CodexProfileDetail{}, err
	}
	return CodexProfileDetail{Summary: summary, Login: login, ConfigSet: configSet}, nil
}

func listCodexProfileSummaries(ctx context.Context, db *store.Store) ([]CodexProfileSummary, error) {
	targets, err := allStoredCodexBindingTargets(ctx, db)
	if err != nil {
		return nil, WrapError(ErrorStoreStatusFailed, "failed to list Codex profile targets", err)
	}
	grouped := map[string][]store.ProfileTarget{}
	for _, target := range targets {
		grouped[target.ProfileID] = append(grouped[target.ProfileID], target)
	}
	active, activeExists, err := codexActiveState(ctx, db)
	if err != nil {
		return nil, err
	}
	profileIDs := make([]string, 0, len(grouped))
	for profileID := range grouped {
		profileIDs = append(profileIDs, profileID)
	}
	sort.Strings(profileIDs)
	result := make([]CodexProfileSummary, 0, len(profileIDs))
	for _, profileID := range profileIDs {
		profile, err := db.GetProfile(ctx, profileID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, mapProfileStoreError(err)
		}
		summary, _, _, _, err := codexProfileSummaryFromStore(ctx, db, profile, grouped[profileID], active, activeExists)
		if err != nil {
			return nil, err
		}
		result = append(result, summary)
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Active != result[j].Active {
			return result[i].Active
		}
		return result[i].Profile.ID < result[j].Profile.ID
	})
	return result, nil
}

func codexActiveState(ctx context.Context, db *store.Store) (store.ActiveState, bool, error) {
	active, err := db.GetActiveState(ctx, store.ActiveStateScopeProvider, codexconfig.ProviderID)
	if errors.Is(err, store.ErrNotFound) {
		return store.ActiveState{}, false, nil
	}
	if err != nil {
		return store.ActiveState{}, false, WrapError(ErrorStoreStatusFailed, "failed to read active Codex profile state", err)
	}
	return active, true, nil
}

func codexProfileSummaryFromStore(ctx context.Context, db *store.Store, profile store.Profile, targets []store.ProfileTarget, active store.ActiveState, activeExists bool) (CodexProfileSummary, *CodexLoginStateSummary, *CodexConfigSet, bool, error) {
	publicProfile, err := profileFromStore(profile)
	if err != nil {
		return CodexProfileSummary{}, nil, nil, false, err
	}
	summary := CodexProfileSummary{
		Profile: publicProfile, ProviderID: codexconfig.ProviderID,
		UpdatedAtUnixMS: profile.UpdatedAtUnixMS, Warnings: []string{},
	}
	if activeExists && active.ProfileID == profile.ID {
		summary.Active = true
		summary.ActiveOperationID = active.OperationID
	}
	var login *CodexLoginStateSummary
	var publicConfigSet *CodexConfigSet
	hasConfigTarget := false
	hasAuthTarget := false
	validConfig := false
	validAuth := false
	for _, target := range targets {
		if target.UpdatedAtUnixMS > summary.UpdatedAtUnixMS {
			summary.UpdatedAtUnixMS = target.UpdatedAtUnixMS
		}
		switch target.TargetID {
		case codexconfig.TargetID:
			hasConfigTarget = true
			configSetID, err := codexConfigSetIDFromTarget(target)
			if err != nil {
				summary.Warnings = append(summary.Warnings, "Codex config set binding is invalid")
				continue
			}
			configSet, err := requireCodexConfigSet(ctx, db, configSetID)
			if err != nil {
				summary.Warnings = append(summary.Warnings, "Codex config set is missing or invalid")
				continue
			}
			activeID := ""
			if summary.Active {
				activeID = configSetID
			}
			value, err := codexConfigSetFromStore(ctx, db, configSet, activeID)
			if err != nil {
				return CodexProfileSummary{}, nil, nil, false, err
			}
			publicConfigSet = &value
			summary.ConfigSetID = value.ID
			summary.ConfigSetName = value.Name
			summary.ConfigSetReferenceCount = value.ReferenceCount
			summary.Model = value.Model
			summary.ModelProvider = value.ModelProvider
			summary.OpenAIBaseURL = value.OpenAIBaseURL
			if value.UpdatedAtUnixMS > summary.UpdatedAtUnixMS {
				summary.UpdatedAtUnixMS = value.UpdatedAtUnixMS
			}
			validConfig = true
		case codexconfig.AuthTargetID:
			hasAuthTarget = true
			credentialID, err := codexCredentialIDFromTarget(target)
			if err != nil {
				summary.Warnings = append(summary.Warnings, "Codex login binding is invalid")
				continue
			}
			credential, err := requireCodexAuthCredential(ctx, db, credentialID)
			if err != nil {
				summary.Warnings = append(summary.Warnings, "Codex login state is missing or invalid")
				continue
			}
			accountID, err := codexauth.ExtractAccountID([]byte(credential.PayloadJSON))
			if err != nil {
				summary.Warnings = append(summary.Warnings, "Codex login state is invalid")
				continue
			}
			references, err := codexCredentialBindingCount(ctx, db, credentialID)
			if err != nil {
				return CodexProfileSummary{}, nil, nil, false, err
			}
			login = &CodexLoginStateSummary{
				CredentialID: credentialID, CodexAccountID: accountID,
				ReferenceCount: references, UpdatedAtUnixMS: credential.UpdatedAtUnixMS,
			}
			summary.CredentialID = credentialID
			summary.CredentialReferenceCount = references
			summary.CodexAccountID = accountID
			if credential.UpdatedAtUnixMS > summary.UpdatedAtUnixMS {
				summary.UpdatedAtUnixMS = credential.UpdatedAtUnixMS
			}
			validAuth = true
		default:
			summary.Warnings = append(summary.Warnings, "Codex profile contains an unsupported target: "+target.TargetID)
		}
	}
	if !hasConfigTarget {
		summary.Warnings = append(summary.Warnings, "Codex profile config binding is missing")
	}
	if !hasAuthTarget {
		summary.Warnings = append(summary.Warnings, "Codex profile login binding is missing")
	}
	summary.Warnings = uniqueStrings(summary.Warnings)
	return summary, login, publicConfigSet, validConfig && validAuth, nil
}

func parseCodexConfigSummary(content string) (string, string, string) {
	var decoded map[string]any
	if err := toml.Unmarshal([]byte(content), &decoded); err != nil {
		return "", "", ""
	}
	model, _ := decoded["model"].(string)
	provider, _ := decoded["model_provider"].(string)
	if model != "" && provider == "" {
		provider = codexconfig.DefaultModelProvider
	}
	baseURL, _ := decoded["openai_base_url"].(string)
	if providers, ok := decoded["model_providers"].(map[string]any); ok {
		if selected, ok := providers[provider].(map[string]any); ok {
			if value, ok := selected["base_url"].(string); ok && value != "" {
				baseURL = value
			}
		}
	}
	return model, provider, baseURL
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
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
