package app

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/pelletier/go-toml/v2"
	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	codexpreset "github.com/strahe/profiledeck/internal/codex/preset"
	"github.com/strahe/profiledeck/internal/store"
)

const (
	CodexProfileSaveKindSnapshot     = "snapshot"
	CodexProfileSaveKindConfigPreset = "config-preset"
	CodexProfileSaveKindAuthOnly     = "auth-only"
	CodexProfileSaveKindUnknown      = "unknown"
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
	Profile           Profile  `json:"profile"`
	ProviderID        string   `json:"provider_id"`
	SaveKind          string   `json:"save_kind"`
	AccountID         string   `json:"account_id,omitempty"`
	Model             string   `json:"model,omitempty"`
	ModelProvider     string   `json:"model_provider,omitempty"`
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
	summary, err := codexProfileSummaryFromStore(profile, targets, active, activeExists)
	if err != nil {
		return CodexProfileDetail{}, err
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
		summary, err := codexProfileSummaryFromStore(profile, grouped[profileID], active, activeExists)
		if err != nil {
			return nil, err
		}
		result = append(result, summary)
	}
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

func codexProfileSummaryFromStore(profile store.Profile, targets []store.ProfileTarget, active store.ActiveState, activeExists bool) (CodexProfileSummary, error) {
	publicProfile, err := profileFromStore(profile)
	if err != nil {
		return CodexProfileSummary{}, err
	}
	summary := CodexProfileSummary{
		Profile:         publicProfile,
		ProviderID:      codexconfig.ProviderID,
		SaveKind:        CodexProfileSaveKindUnknown,
		TargetCount:     len(targets),
		UpdatedAtUnixMS: profile.UpdatedAtUnixMS,
		Warnings:        []string{},
	}
	if activeExists && active.ProfileID == profile.ID {
		summary.Active = true
		summary.ActiveOperationID = active.OperationID
	}

	hasConfig := false
	configMode := ""
	hasAuth := false
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
			configMode = metadata.ModeOrDefault()
			summary.Model, summary.ModelProvider = codexConfigModelSummary(target.ValueJSON, configMode)
		case codexconfig.AuthTargetID:
			hasAuth = true
			if _, warning := codexTargetMetadataForSummary(target); warning != "" {
				summary.Warnings = append(summary.Warnings, warning)
			}
			accountID, err := codexpreset.ParseAuthTargetValueJSON(target.ValueJSON)
			if err != nil {
				summary.Warnings = append(summary.Warnings, "Codex auth account binding is invalid")
			} else {
				summary.AccountID = accountID
			}
		default:
			summary.Warnings = append(summary.Warnings, "Codex profile contains an unsupported target: "+target.TargetID)
		}
	}

	switch {
	case hasConfig && configMode == codexpreset.TargetModeFullFile:
		summary.SaveKind = CodexProfileSaveKindSnapshot
	case hasConfig && configMode == codexpreset.TargetModeManagedKeys:
		summary.SaveKind = CodexProfileSaveKindConfigPreset
	case !hasConfig && hasAuth:
		summary.SaveKind = CodexProfileSaveKindAuthOnly
	default:
		summary.SaveKind = CodexProfileSaveKindUnknown
	}
	summary.Warnings = uniqueStrings(summary.Warnings)
	return summary, nil
}

func codexConfigModelSummary(valueJSON string, mode string) (string, string) {
	switch mode {
	case codexpreset.TargetModeManagedKeys:
		managed, err := codexconfig.ParseValueJSON(valueJSON)
		if err != nil {
			return "", ""
		}
		return managed.Model, managed.ModelProvider
	case codexpreset.TargetModeFullFile:
		var value map[string]string
		if err := json.Unmarshal([]byte(valueJSON), &value); err != nil {
			return "", ""
		}
		content := value["content"]
		if content == "" {
			return "", ""
		}
		var decoded map[string]any
		if err := toml.Unmarshal([]byte(content), &decoded); err != nil {
			return "", ""
		}
		model, _ := decoded["model"].(string)
		modelProvider, _ := decoded["model_provider"].(string)
		if model != "" && modelProvider == "" {
			modelProvider = codexconfig.DefaultModelProvider
		}
		return model, modelProvider
	default:
		return "", ""
	}
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
		Metadata:        redactMetadata(metadata).(map[string]any),
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
