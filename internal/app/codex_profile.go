package app

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"

	"github.com/pelletier/go-toml/v2"
	codexauth "github.com/strahe/profiledeck/internal/codex/auth"
	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	codexpreset "github.com/strahe/profiledeck/internal/codex/preset"
	"github.com/strahe/profiledeck/internal/store"
)

type ListCodexProfilesRequest struct {
	ConfigDir string
}

type GetCodexProfileRequest struct {
	ConfigDir string
	ProfileID string
}

type LoadStoredCodexProfileDraftRequest struct {
	ConfigDir string
	ProfileID string
}

type CodexProfileListResult struct {
	Profiles []CodexProfileSummary `json:"profiles"`
}

type CodexProfileSummary struct {
	Profile           Profile  `json:"profile"`
	ProviderID        string   `json:"provider_id"`
	CodexAccountID    string   `json:"codex_account_id,omitempty"`
	Model             string   `json:"model,omitempty"`
	ModelProvider     string   `json:"model_provider,omitempty"`
	OpenAIBaseURL     string   `json:"openai_base_url,omitempty"`
	TargetCount       int      `json:"target_count"`
	Active            bool     `json:"active"`
	ActiveOperationID string   `json:"active_operation_id,omitempty"`
	UpdatedAtUnixMS   int64    `json:"updated_at_unix_ms"`
	Warnings          []string `json:"warnings,omitempty"`
}

type CodexProfileDetail struct {
	Summary CodexProfileSummary `json:"summary"`
	Targets []ProfileTarget     `json:"targets"`
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

	targets, err := db.ListProfileTargets(ctx, profileID, codexconfig.ProviderID, true)
	if err != nil {
		return CodexProfileDetail{}, WrapError(ErrorStoreStatusFailed, "failed to list Codex profile targets", err)
	}
	if len(targets) == 0 {
		return CodexProfileDetail{}, NewError(ErrorProfileNotFound, "Codex profile not found").WithDetail("profile_id", profileID)
	}

	profile, err := db.GetProfile(ctx, profileID)
	if err != nil {
		return CodexProfileDetail{}, mapProfileStoreError(err)
	}
	active, activeExists, err := codexActiveState(ctx, db)
	if err != nil {
		return CodexProfileDetail{}, err
	}
	summary, fullProfile, err := codexProfileSummaryFromStore(ctx, db, profile, targets, active, activeExists)
	if err != nil {
		return CodexProfileDetail{}, err
	}
	if !fullProfile {
		return CodexProfileDetail{}, NewError(ErrorCodexInvalid, "Codex profile is not a valid full profile").
			WithDetail("profile_id", profileID)
	}

	publicTargets := make([]ProfileTarget, 0, len(targets))
	for _, target := range targets {
		publicTarget, warnings := tolerantCodexProfileTargetFromStore(target)
		summary.Warnings = append(summary.Warnings, warnings...)
		publicTargets = append(publicTargets, publicTarget)
	}
	summary.Warnings = uniqueStrings(summary.Warnings)
	return CodexProfileDetail{Summary: summary, Targets: publicTargets}, nil
}

func LoadStoredCodexProfileDraft(ctx context.Context, req LoadStoredCodexProfileDraftRequest) (CodexProfileDraft, error) {
	profileID, appErr := validateID(req.ProfileID, ErrorProfileInvalid)
	if appErr != nil {
		return CodexProfileDraft{}, appErr
	}

	db, err := openHealthyStore(ctx, req.ConfigDir, true)
	if err != nil {
		return CodexProfileDraft{}, err
	}
	defer db.Close()

	targets, err := db.ListProfileTargets(ctx, profileID, codexconfig.ProviderID, true)
	if err != nil {
		return CodexProfileDraft{}, WrapError(ErrorStoreStatusFailed, "failed to list Codex profile targets", err)
	}
	if len(targets) == 0 {
		return CodexProfileDraft{}, NewError(ErrorProfileNotFound, "Codex profile not found").WithDetail("profile_id", profileID)
	}
	configTarget, authTarget, err := requireCodexFullProfileTargets(profileID, targets)
	if err != nil {
		return CodexProfileDraft{}, err
	}
	configContent, err := replaceFileContentFromValueJSON(configTarget.ValueJSON)
	if err != nil {
		return CodexProfileDraft{}, err
	}
	credentialID, err := codexCredentialIDFromTarget(authTarget)
	if err != nil {
		return CodexProfileDraft{}, err
	}
	credential, err := db.GetProviderCredential(ctx, credentialID)
	if err != nil {
		return CodexProfileDraft{}, mapCodexCredentialStoreError(err)
	}
	home := storedCodexProfileHome(ctx, db, configTarget, authTarget)
	accountID, _ := codexauth.ExtractAccountID([]byte(credential.PayloadJSON))
	model, provider, baseURL := codexConfigSummaryFromContent(configContent)
	return CodexProfileDraft{
		CodexDir:       home.Dir,
		ConfigPath:     home.ConfigPath,
		AuthPath:       home.AuthPath,
		ConfigContent:  configContent,
		AuthContent:    credential.PayloadJSON,
		ConfigSHA256:   sha256HexString(configContent),
		AuthSHA256:     credential.PayloadSHA256,
		CodexAccountID: accountID,
		Model:          model,
		ModelProvider:  provider,
		OpenAIBaseURL:  baseURL,
	}, nil
}

func listCodexProfileSummaries(ctx context.Context, db *store.Store) ([]CodexProfileSummary, error) {
	targets, err := db.ListProfileTargetsByProvider(ctx, codexconfig.ProviderID)
	if err != nil {
		return nil, WrapError(ErrorStoreStatusFailed, "failed to list Codex profile targets", err)
	}
	grouped := map[string][]store.ProfileTarget{}
	for _, target := range targets {
		grouped[target.ProfileID] = append(grouped[target.ProfileID], target)
	}
	if len(grouped) == 0 {
		return []CodexProfileSummary{}, nil
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
		summary, fullProfile, err := codexProfileSummaryFromStore(ctx, db, profile, grouped[profileID], active, activeExists)
		if err != nil {
			return nil, err
		}
		if !fullProfile {
			continue
		}
		result = append(result, summary)
	}
	return result, nil
}

func storedCodexProfileHome(ctx context.Context, db *store.Store, configTarget store.ProfileTarget, authTarget store.ProfileTarget) codexconfig.Home {
	if provider, err := db.GetProvider(ctx, codexconfig.ProviderID); err == nil {
		if metadata, err := codexpreset.DecodeProviderMetadata(provider.MetadataJSON); err == nil && metadata.Compatible() {
			return codexconfig.Home{
				Dir:        metadata.CodexDir,
				ConfigPath: metadata.ConfigPath,
				AuthPath:   metadata.AuthPath,
			}
		}
	}
	dir := filepath.Dir(configTarget.Path)
	return codexconfig.Home{
		Dir:        dir,
		ConfigPath: configTarget.Path,
		AuthPath:   authTarget.Path,
	}
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

func codexProfileSummaryFromStore(ctx context.Context, db *store.Store, profile store.Profile, targets []store.ProfileTarget, active store.ActiveState, activeExists bool) (CodexProfileSummary, bool, error) {
	publicProfile, err := profileFromStore(profile)
	if err != nil {
		return CodexProfileSummary{}, false, err
	}
	summary := CodexProfileSummary{
		Profile:         publicProfile,
		ProviderID:      codexconfig.ProviderID,
		TargetCount:     len(targets),
		UpdatedAtUnixMS: profile.UpdatedAtUnixMS,
		Warnings:        []string{},
	}
	if activeExists && active.ProfileID == profile.ID {
		summary.Active = true
		summary.ActiveOperationID = active.OperationID
	}

	hasConfig := false
	hasFullConfig := false
	hasAuth := false
	hasCredentialAuth := false
	for _, target := range targets {
		if target.UpdatedAtUnixMS > summary.UpdatedAtUnixMS {
			summary.UpdatedAtUnixMS = target.UpdatedAtUnixMS
		}
		switch target.TargetID {
		case codexconfig.TargetID:
			hasConfig = true
			metadata, warning := codexTargetMetadataForSummary(target)
			if warning != "" {
				summary.Warnings = append(summary.Warnings, warning)
				continue
			}
			if metadata.Mode != codexpreset.TargetModeFullFile {
				summary.Warnings = append(summary.Warnings, "Codex config target mode is unsupported")
				continue
			}
			hasFullConfig = true
			summary.Model, summary.ModelProvider, summary.OpenAIBaseURL = codexConfigModelSummary(target.ValueJSON)
		case codexconfig.AuthTargetID:
			hasAuth = true
			codexAccountID, valid, warning, err := codexProfileAuthSummary(ctx, db, target)
			if err != nil {
				return CodexProfileSummary{}, false, err
			}
			if warning != "" {
				summary.Warnings = append(summary.Warnings, warning)
				continue
			}
			if valid {
				hasCredentialAuth = true
				summary.CodexAccountID = codexAccountID
			}
		default:
			summary.Warnings = append(summary.Warnings, "Codex profile contains an unsupported target: "+target.TargetID)
		}
	}

	if hasConfig && !hasFullConfig {
		summary.Warnings = append(summary.Warnings, "Codex profile config is not a full-file profile")
	}
	if !hasConfig && hasAuth {
		summary.Warnings = append(summary.Warnings, "Codex profile has auth without config")
	}
	if hasConfig && !hasAuth {
		summary.Warnings = append(summary.Warnings, "Codex profile has config without auth")
	}
	summary.Warnings = uniqueStrings(summary.Warnings)
	return summary, hasFullConfig && hasCredentialAuth, nil
}

func codexProfileAuthSummary(ctx context.Context, db *store.Store, target store.ProfileTarget) (string, bool, string, error) {
	metadata, warning := codexTargetMetadataForSummary(target)
	if warning != "" {
		return "", false, warning, nil
	}
	if metadata.Mode != codexpreset.TargetModeCredential {
		return "", false, "Codex auth target mode is unsupported", nil
	}
	credentialID, err := codexpreset.ParseCredentialBindingValueJSON(target.ValueJSON)
	if err != nil {
		return "", false, "Codex auth credential binding is invalid", nil
	}
	credential, err := db.GetProviderCredential(ctx, credentialID)
	if errors.Is(err, store.ErrNotFound) {
		return "", false, "Codex auth credential is missing", nil
	}
	if err != nil {
		return "", false, "", mapCodexCredentialStoreError(err)
	}
	if credential.ProviderID != codexconfig.ProviderID || credential.CredentialKind != codexpreset.CredentialKindAuthJSON {
		return "", false, "Codex auth credential kind is unsupported", nil
	}
	codexAccountID, err := codexauth.ExtractAccountID([]byte(credential.PayloadJSON))
	if err != nil {
		return "", false, "Codex auth credential payload is invalid", nil
	}
	return codexAccountID, true, "", nil
}

func codexConfigModelSummary(valueJSON string) (string, string, string) {
	var value map[string]string
	if err := json.Unmarshal([]byte(valueJSON), &value); err != nil {
		return "", "", ""
	}
	content := value["content"]
	if content == "" {
		return "", "", ""
	}
	var decoded map[string]any
	if err := toml.Unmarshal([]byte(content), &decoded); err != nil {
		return "", "", ""
	}
	model, _ := decoded["model"].(string)
	modelProvider, _ := decoded["model_provider"].(string)
	if model != "" && modelProvider == "" {
		modelProvider = codexconfig.DefaultModelProvider
	}
	openAIBaseURL, _ := decoded["openai_base_url"].(string)
	return model, modelProvider, openAIBaseURL
}

func codexTargetMetadataForSummary(target store.ProfileTarget) (codexpreset.TargetMetadata, string) {
	metadata, err := codexpreset.DecodeTargetMetadata(target.MetadataJSON)
	if err != nil {
		return codexpreset.TargetMetadata{}, "Codex target metadata is invalid for " + target.TargetID
	}
	if metadata.TargetKind != target.TargetID {
		return metadata, "Codex target metadata kind does not match " + target.TargetID
	}
	if !metadata.Compatible() {
		return metadata, "Codex target metadata is unsupported for " + target.TargetID
	}
	return metadata, ""
}

func tolerantCodexProfileTargetFromStore(target store.ProfileTarget) (ProfileTarget, []string) {
	warnings := []string{}
	metadata, err := metadataFromJSON(target.MetadataJSON)
	if err != nil {
		warnings = append(warnings, "Codex target metadata is invalid for "+target.TargetID)
		metadata = map[string]any{}
	}
	preview, err := targetValuePreview(target.ProviderID, target.TargetID, target.Format, target.Strategy, target.ValueJSON)
	if err != nil {
		warnings = append(warnings, "Codex target preview is unavailable for "+target.TargetID)
		preview = TextPreview{Content: "[unavailable]", Truncated: false}
	}
	return ProfileTarget{
		ProfileID:       target.ProfileID,
		ProviderID:      target.ProviderID,
		TargetID:        target.TargetID,
		Path:            target.Path,
		Format:          target.Format,
		Strategy:        target.Strategy,
		Enabled:         target.Enabled,
		ValuePreview:    preview,
		Metadata:        redactedMetadataMap(metadata),
		CreatedAtUnixMS: target.CreatedAtUnixMS,
		UpdatedAtUnixMS: target.UpdatedAtUnixMS,
	}, warnings
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
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
