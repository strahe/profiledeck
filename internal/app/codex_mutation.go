package app

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	codexauth "github.com/strahe/profiledeck/internal/codex/auth"
	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	codexpreset "github.com/strahe/profiledeck/internal/codex/preset"
	"github.com/strahe/profiledeck/internal/store"
	"github.com/strahe/profiledeck/internal/targetfs"
)

const (
	CodexForkAuthBindingShareParent = "share-parent"
	CodexForkAuthBindingCopyNew     = "copy-new"

	CodexSyncAuthUpdateDefault   = ""
	CodexSyncAuthUpdateShared    = "update-shared"
	CodexSyncAuthUpdateForkNew   = "fork-new"
	codexSharedCredentialWarning = "shared Codex auth credential updated"
)

type LoadCodexProfileDraftRequest struct {
	ConfigDir string
	CodexDir  string
}

type CreateCodexProfileRequest struct {
	ConfigDir     string  `json:"config_dir"`
	CodexDir      string  `json:"codex_dir"`
	ProfileID     string  `json:"profile_id"`
	Name          *string `json:"name,omitempty"`
	Description   *string `json:"description,omitempty"`
	ConfigContent *string `json:"config_content,omitempty"`
	AuthContent   *string `json:"auth_content,omitempty"`
}

type ForkCodexProfileRequest struct {
	ConfigDir       string  `json:"config_dir"`
	CodexDir        string  `json:"codex_dir"`
	SourceProfileID string  `json:"source_profile_id"`
	ProfileID       string  `json:"profile_id"`
	AuthBinding     string  `json:"auth_binding"`
	Name            *string `json:"name,omitempty"`
	Description     *string `json:"description,omitempty"`
}

type SyncCodexProfileRequest struct {
	ConfigDir     string  `json:"config_dir"`
	CodexDir      string  `json:"codex_dir"`
	ProfileID     string  `json:"profile_id"`
	AuthUpdate    string  `json:"auth_update,omitempty"`
	ConfigContent *string `json:"config_content,omitempty"`
	AuthContent   *string `json:"auth_content,omitempty"`
}

type CodexProfileDraft struct {
	CodexDir       string `json:"codex_dir"`
	ConfigPath     string `json:"config_path"`
	AuthPath       string `json:"auth_path"`
	ConfigContent  string `json:"config_content"`
	AuthContent    string `json:"auth_content"`
	ConfigSHA256   string `json:"config_sha256"`
	AuthSHA256     string `json:"auth_sha256"`
	CodexAccountID string `json:"codex_account_id,omitempty"`
	Model          string `json:"model,omitempty"`
	ModelProvider  string `json:"model_provider,omitempty"`
	OpenAIBaseURL  string `json:"openai_base_url,omitempty"`
}

type CodexProfileSaveResult struct {
	Provider     Provider      `json:"provider"`
	Profile      Profile       `json:"profile"`
	ConfigTarget ProfileTarget `json:"config_target"`
	AuthTarget   ProfileTarget `json:"auth_target"`
	CodexDir     string        `json:"codex_dir"`
	ConfigPath   string        `json:"config_path"`
	AuthPath     string        `json:"auth_path"`
	Warnings     []string      `json:"warnings"`
}

type codexProfilePayload struct {
	Home          codexconfig.Home
	ConfigContent string
	AuthPayload   string
}

func LoadCodexProfileDraft(ctx context.Context, req LoadCodexProfileDraftRequest) (CodexProfileDraft, error) {
	payload, err := loadCodexProfilePayload(req.CodexDir, nil, nil)
	if err != nil {
		return CodexProfileDraft{}, err
	}
	accountID, _ := codexauth.ExtractAccountID([]byte(payload.AuthPayload))
	model, provider, baseURL := codexConfigSummaryFromContent(payload.ConfigContent)
	return CodexProfileDraft{
		CodexDir:       payload.Home.Dir,
		ConfigPath:     payload.Home.ConfigPath,
		AuthPath:       payload.Home.AuthPath,
		ConfigContent:  payload.ConfigContent,
		AuthContent:    payload.AuthPayload,
		ConfigSHA256:   sha256HexString(payload.ConfigContent),
		AuthSHA256:     sha256HexString(payload.AuthPayload),
		CodexAccountID: accountID,
		Model:          model,
		ModelProvider:  provider,
		OpenAIBaseURL:  baseURL,
	}, nil
}

func CreateCodexProfile(ctx context.Context, req CreateCodexProfileRequest) (CodexProfileSaveResult, error) {
	profileID, appErr := validateID(req.ProfileID, ErrorProfileInvalid)
	if appErr != nil {
		return CodexProfileSaveResult{}, appErr
	}
	fields, appErr := normalizeCodexProfileFields(profileID, req.Name, req.Description)
	if appErr != nil {
		return CodexProfileSaveResult{}, appErr
	}
	payload, err := loadCodexProfilePayload(req.CodexDir, req.ConfigContent, req.AuthContent)
	if err != nil {
		return CodexProfileSaveResult{}, err
	}

	return saveNewCodexProfile(ctx, req.ConfigDir, payload, profileID, fields, "", true)
}

func ForkCodexProfile(ctx context.Context, req ForkCodexProfileRequest) (CodexProfileSaveResult, error) {
	sourceProfileID, appErr := validateID(req.SourceProfileID, ErrorProfileInvalid)
	if appErr != nil {
		return CodexProfileSaveResult{}, appErr
	}
	profileID, appErr := validateID(req.ProfileID, ErrorProfileInvalid)
	if appErr != nil {
		return CodexProfileSaveResult{}, appErr
	}
	authBinding, appErr := normalizeCodexForkAuthBinding(req.AuthBinding)
	if appErr != nil {
		return CodexProfileSaveResult{}, appErr
	}
	fields, appErr := normalizeCodexProfileFields(profileID, req.Name, req.Description)
	if appErr != nil {
		return CodexProfileSaveResult{}, appErr
	}
	home, err := resolveCodexMutationHome(req.CodexDir)
	if err != nil {
		return CodexProfileSaveResult{}, err
	}

	db, err := openHealthyStore(ctx, req.ConfigDir, false)
	if err != nil {
		return CodexProfileSaveResult{}, err
	}
	defer db.Close()

	var result CodexProfileSaveResult
	if err := db.WithTransaction(ctx, func(txStore *store.Store) error {
		provider, hasProvider, err := codexPreflightProvider(ctx, txStore, home)
		if err != nil {
			return err
		}
		if _, err := txStore.GetProfile(ctx, sourceProfileID); err != nil {
			return mapProfileStoreError(err)
		}
		sourceTargets, err := txStore.ListProfileTargets(ctx, sourceProfileID, codexconfig.ProviderID, true)
		if err != nil {
			return WrapError(ErrorStoreStatusFailed, "failed to read source Codex profile targets", err)
		}
		sourceConfig, sourceAuth, err := requireCodexFullProfileTargets(sourceProfileID, sourceTargets)
		if err != nil {
			return err
		}
		configContent, err := replaceFileContentFromValueJSON(sourceConfig.ValueJSON)
		if err != nil {
			return err
		}
		sourceCredentialID, err := codexCredentialIDFromTarget(sourceAuth)
		if err != nil {
			return err
		}
		sourceCredential, err := requireCodexAuthCredential(ctx, txStore, sourceCredentialID)
		if err != nil {
			return err
		}
		credentialID := sourceCredentialID
		if authBinding == CodexForkAuthBindingCopyNew {
			credentialID, err = newCodexCredentialID(time.Now())
			if err != nil {
				return WrapError(ErrorCodexInvalid, "failed to generate Codex credential id", err)
			}
			if _, err := upsertCodexAuthCredential(ctx, txStore, credentialID, sourceCredential.PayloadJSON); err != nil {
				return err
			}
		}
		payload := codexProfilePayload{
			Home:          home,
			ConfigContent: configContent,
			AuthPayload:   "",
		}
		provider, profile, configTarget, authTarget, warnings, err := createCodexProfileTargets(ctx, txStore, payload, profileID, fields, credentialID, hasProvider)
		if err != nil {
			return err
		}
		result, err = codexProfileSaveResult(provider, profile, configTarget, authTarget, home, warnings)
		return err
	}); err != nil {
		return CodexProfileSaveResult{}, wrapCodexMutationTxError("Codex profile fork transaction failed", err)
	}
	return result, nil
}

func SyncCodexProfile(ctx context.Context, req SyncCodexProfileRequest) (CodexProfileSaveResult, error) {
	profileID, appErr := validateID(req.ProfileID, ErrorProfileInvalid)
	if appErr != nil {
		return CodexProfileSaveResult{}, appErr
	}
	authUpdate, appErr := normalizeCodexSyncAuthUpdate(req.AuthUpdate)
	if appErr != nil {
		return CodexProfileSaveResult{}, appErr
	}
	payload, err := loadCodexProfilePayload(req.CodexDir, req.ConfigContent, req.AuthContent)
	if err != nil {
		return CodexProfileSaveResult{}, err
	}

	db, err := openHealthyStore(ctx, req.ConfigDir, false)
	if err != nil {
		return CodexProfileSaveResult{}, err
	}
	defer db.Close()

	var result CodexProfileSaveResult
	if err := db.WithTransaction(ctx, func(txStore *store.Store) error {
		provider, hasProvider, err := codexPreflightProvider(ctx, txStore, payload.Home)
		if err != nil {
			return err
		}
		profile, hasProfile, err := codexPreflightProfile(ctx, txStore, profileID)
		if err != nil {
			return err
		}
		if !hasProfile {
			return NewError(ErrorProfileNotFound, "Codex profile not found").WithDetail("profile_id", profileID)
		}
		targets, err := codexPreflightTargets(ctx, txStore, payload.Home, profileID)
		if err != nil {
			return err
		}
		if !targets.HasConfig || !targets.HasAuth {
			return NewError(ErrorCodexInvalid, "Codex profile is not a valid full profile").WithDetail("profile_id", profileID)
		}
		credentialID, warnings, err := resolveCodexSyncCredential(ctx, txStore, targets.Auth, payload.AuthPayload, authUpdate)
		if err != nil {
			return err
		}
		configValueJSON, authValueJSON, providerMetadata, configMetadata, authMetadata, err := codexMutationEncodedValues(payload, credentialID)
		if err != nil {
			return err
		}
		provider, err = upsertCodexProvider(ctx, txStore, providerMetadata, hasProvider)
		if err != nil {
			return err
		}
		if _, err := upsertCodexAuthCredential(ctx, txStore, credentialID, payload.AuthPayload); err != nil {
			return err
		}
		configTarget, err := upsertCodexConfigTarget(ctx, txStore, profileID, payload.Home, configValueJSON, configMetadata, targets.HasConfig)
		if err != nil {
			return err
		}
		authTarget, err := upsertCodexAuthTarget(ctx, txStore, profileID, payload.Home, authValueJSON, authMetadata, targets.HasAuth)
		if err != nil {
			return err
		}
		result, err = codexProfileSaveResult(provider, profile, configTarget, authTarget, payload.Home, warnings)
		return err
	}); err != nil {
		return CodexProfileSaveResult{}, wrapCodexMutationTxError("Codex profile sync transaction failed", err)
	}
	return result, nil
}

func saveNewCodexProfile(ctx context.Context, configDir string, payload codexProfilePayload, profileID string, fields codexProfileFields, credentialID string, createCredential bool) (CodexProfileSaveResult, error) {
	db, err := openHealthyStore(ctx, configDir, false)
	if err != nil {
		return CodexProfileSaveResult{}, err
	}
	defer db.Close()

	var result CodexProfileSaveResult
	if err := db.WithTransaction(ctx, func(txStore *store.Store) error {
		provider, hasProvider, err := codexPreflightProvider(ctx, txStore, payload.Home)
		if err != nil {
			return err
		}
		_, hasProfile, err := codexPreflightProfile(ctx, txStore, profileID)
		if err != nil {
			return err
		}
		if hasProfile {
			return NewError(ErrorProfileAlreadyExists, "profile already exists").WithDetail("profile_id", profileID)
		}
		if credentialID == "" {
			credentialID, err = newCodexCredentialID(time.Now())
			if err != nil {
				return WrapError(ErrorCodexInvalid, "failed to generate Codex credential id", err)
			}
		}
		provider, profile, configTarget, authTarget, warnings, err := createCodexProfileTargets(ctx, txStore, payload, profileID, fields, credentialID, hasProvider)
		if err != nil {
			return err
		}
		if createCredential {
			if _, err := upsertCodexAuthCredential(ctx, txStore, credentialID, payload.AuthPayload); err != nil {
				return err
			}
		}
		result, err = codexProfileSaveResult(provider, profile, configTarget, authTarget, payload.Home, warnings)
		return err
	}); err != nil {
		return CodexProfileSaveResult{}, wrapCodexMutationTxError("Codex profile create transaction failed", err)
	}
	return result, nil
}

func createCodexProfileTargets(ctx context.Context, db *store.Store, payload codexProfilePayload, profileID string, fields codexProfileFields, credentialID string, hasProvider bool) (store.Provider, store.Profile, store.ProfileTarget, store.ProfileTarget, []string, error) {
	targets, err := codexPreflightTargets(ctx, db, payload.Home, profileID)
	if err != nil {
		return store.Provider{}, store.Profile{}, store.ProfileTarget{}, store.ProfileTarget{}, nil, err
	}
	if targets.HasConfig || targets.HasAuth {
		return store.Provider{}, store.Profile{}, store.ProfileTarget{}, store.ProfileTarget{}, nil, NewError(ErrorProfileAlreadyExists, "Codex profile targets already exist").WithDetail("profile_id", profileID)
	}
	configValueJSON, authValueJSON, providerMetadata, configMetadata, authMetadata, err := codexMutationEncodedValues(payload, credentialID)
	if err != nil {
		return store.Provider{}, store.Profile{}, store.ProfileTarget{}, store.ProfileTarget{}, nil, err
	}
	provider, err := upsertCodexProvider(ctx, db, providerMetadata, hasProvider)
	if err != nil {
		return store.Provider{}, store.Profile{}, store.ProfileTarget{}, store.ProfileTarget{}, nil, err
	}
	profile, err := upsertCodexProfile(ctx, db, profileID, fields, false)
	if err != nil {
		return store.Provider{}, store.Profile{}, store.ProfileTarget{}, store.ProfileTarget{}, nil, err
	}
	configTarget, err := upsertCodexConfigTarget(ctx, db, profileID, payload.Home, configValueJSON, configMetadata, false)
	if err != nil {
		return store.Provider{}, store.Profile{}, store.ProfileTarget{}, store.ProfileTarget{}, nil, err
	}
	authTarget, err := upsertCodexAuthTarget(ctx, db, profileID, payload.Home, authValueJSON, authMetadata, false)
	if err != nil {
		return store.Provider{}, store.Profile{}, store.ProfileTarget{}, store.ProfileTarget{}, nil, err
	}
	return provider, profile, configTarget, authTarget, []string{}, nil
}

func loadCodexProfilePayload(codexDir string, configContent *string, authContent *string) (codexProfilePayload, error) {
	home, err := resolveCodexMutationHome(codexDir)
	if err != nil {
		return codexProfilePayload{}, err
	}
	config, err := loadCodexConfigContent(home, configContent)
	if err != nil {
		return codexProfilePayload{}, err
	}
	auth, err := loadCodexAuthPayload(home, authContent)
	if err != nil {
		return codexProfilePayload{}, err
	}
	return codexProfilePayload{Home: home, ConfigContent: config, AuthPayload: auth}, nil
}

func resolveCodexMutationHome(codexDir string) (codexconfig.Home, error) {
	home, err := codexconfig.ResolveHome(codexDir)
	if err != nil {
		return codexconfig.Home{}, WrapError(ErrorCodexInvalid, "failed to resolve Codex home", err)
	}
	if appErr := requireExistingCodexHome(home); appErr != nil {
		return codexconfig.Home{}, appErr
	}
	return home, nil
}

func loadCodexConfigContent(home codexconfig.Home, content *string) (string, error) {
	if content == nil {
		configContent, missing, appErr := readCodexConfigSnapshot(home)
		if appErr != nil {
			return "", appErr
		}
		if missing {
			return "", NewError(ErrorCodexInvalid, "Codex config.toml is required").WithDetail("config_path", home.ConfigPath)
		}
		return configContent, nil
	}
	if len(*content) > targetfs.MaxFileBytes {
		return "", NewError(ErrorCodexInvalid, "Codex config is too large").WithDetail("config_path", home.ConfigPath)
	}
	if err := codexconfig.ValidateTOML(*content); err != nil {
		return "", WrapError(ErrorCodexInvalid, "Codex config TOML is invalid", err).WithDetail("config_path", home.ConfigPath)
	}
	return *content, nil
}

func loadCodexAuthPayload(home codexconfig.Home, content *string) (string, error) {
	if content == nil {
		snapshot, appErr := readCodexAuthSnapshot(home)
		if appErr != nil {
			return "", appErr
		}
		return snapshot.Payload, nil
	}
	payload, err := codexauth.NormalizePayload([]byte(*content))
	if err != nil {
		return "", codexAuthPayloadAppError(err).WithDetail("path", home.AuthPath)
	}
	return payload, nil
}

func codexMutationEncodedValues(payload codexProfilePayload, credentialID string) (configValueJSON string, authValueJSON string, providerMetadata string, configTargetMetadata string, authTargetMetadata string, err error) {
	configValueJSON, err = codexpreset.ReplaceFileValueJSON(payload.ConfigContent)
	if err != nil {
		return "", "", "", "", "", WrapError(ErrorCodexInvalid, "failed to encode Codex config target value", err)
	}
	authValueJSON, err = codexpreset.CredentialBindingValueJSON(credentialID)
	if err != nil {
		return "", "", "", "", "", WrapError(ErrorCodexInvalid, "failed to encode Codex auth target value", err)
	}
	providerMetadata, err = codexpreset.ProviderMetadataJSON(payload.Home)
	if err != nil {
		return "", "", "", "", "", WrapError(ErrorCodexInvalid, "failed to encode Codex provider metadata", err)
	}
	configTargetMetadata, err = codexpreset.TargetMetadataJSON(codexconfig.TargetID, codexpreset.TargetModeFullFile)
	if err != nil {
		return "", "", "", "", "", WrapError(ErrorCodexInvalid, "failed to encode Codex config target metadata", err)
	}
	authTargetMetadata, err = codexpreset.TargetMetadataJSON(codexconfig.AuthTargetID, codexpreset.TargetModeCredential)
	if err != nil {
		return "", "", "", "", "", WrapError(ErrorCodexInvalid, "failed to encode Codex auth target metadata", err)
	}
	return configValueJSON, authValueJSON, providerMetadata, configTargetMetadata, authTargetMetadata, nil
}

func requireCodexFullProfileTargets(profileID string, targets []store.ProfileTarget) (store.ProfileTarget, store.ProfileTarget, error) {
	var configTarget store.ProfileTarget
	var authTarget store.ProfileTarget
	hasConfig := false
	hasAuth := false
	for _, target := range targets {
		switch target.TargetID {
		case codexconfig.TargetID:
			metadata, err := codexpreset.DecodeTargetMetadata(target.MetadataJSON)
			if err != nil {
				return store.ProfileTarget{}, store.ProfileTarget{}, WrapError(ErrorStoreSchemaInvalid, "stored Codex config target metadata is invalid", err)
			}
			if metadata.Mode != codexpreset.TargetModeFullFile {
				return store.ProfileTarget{}, store.ProfileTarget{}, NewError(ErrorCodexInvalid, "Codex config target is not full-file").WithDetail("profile_id", profileID)
			}
			configTarget = target
			hasConfig = true
		case codexconfig.AuthTargetID:
			if _, err := codexCredentialIDFromTarget(target); err != nil {
				return store.ProfileTarget{}, store.ProfileTarget{}, err
			}
			authTarget = target
			hasAuth = true
		}
	}
	if !hasConfig || !hasAuth {
		return store.ProfileTarget{}, store.ProfileTarget{}, NewError(ErrorCodexInvalid, "Codex profile is not a valid full profile").WithDetail("profile_id", profileID)
	}
	return configTarget, authTarget, nil
}

func resolveCodexSyncCredential(ctx context.Context, db *store.Store, authTarget store.ProfileTarget, authPayload string, authUpdate string) (string, []string, error) {
	credentialID, err := codexCredentialIDFromTarget(authTarget)
	if err != nil {
		return "", nil, err
	}
	credential, err := db.GetProviderCredential(ctx, credentialID)
	if err != nil {
		return "", nil, mapCodexCredentialStoreError(err)
	}
	if credential.PayloadJSON == authPayload && authUpdate != CodexSyncAuthUpdateForkNew {
		return credentialID, []string{}, nil
	}
	count, err := codexCredentialBindingCount(ctx, db, credentialID)
	if err != nil {
		return "", nil, err
	}
	if authUpdate == CodexSyncAuthUpdateForkNew {
		id, err := newCodexCredentialID(time.Now())
		if err != nil {
			return "", nil, WrapError(ErrorCodexInvalid, "failed to generate Codex credential id", err)
		}
		return id, []string{}, nil
	}
	if count > 1 && authUpdate == CodexSyncAuthUpdateDefault {
		return "", nil, NewError(ErrorCodexInvalid, "Codex auth credential is shared; choose an auth update mode").
			WithDetail("profile_id", authTarget.ProfileID).
			WithDetail("supported_auth_updates", []string{CodexSyncAuthUpdateShared, CodexSyncAuthUpdateForkNew})
	}
	warnings := []string{}
	if count > 1 {
		warnings = append(warnings, codexSharedCredentialWarning)
	}
	return credentialID, warnings, nil
}

func normalizeCodexForkAuthBinding(raw string) (string, *AppError) {
	switch strings.TrimSpace(raw) {
	case CodexForkAuthBindingShareParent, CodexForkAuthBindingCopyNew:
		return strings.TrimSpace(raw), nil
	default:
		return "", NewError(ErrorCodexInvalid, "unsupported Codex fork auth binding").
			WithDetail("auth_binding", raw).
			WithDetail("supported", []string{CodexForkAuthBindingShareParent, CodexForkAuthBindingCopyNew})
	}
}

func normalizeCodexSyncAuthUpdate(raw string) (string, *AppError) {
	switch strings.TrimSpace(raw) {
	case "", CodexSyncAuthUpdateShared, CodexSyncAuthUpdateForkNew:
		return strings.TrimSpace(raw), nil
	default:
		return "", NewError(ErrorCodexInvalid, "unsupported Codex sync auth update").
			WithDetail("auth_update", raw).
			WithDetail("supported", []string{CodexSyncAuthUpdateShared, CodexSyncAuthUpdateForkNew})
	}
}

func codexConfigSummaryFromContent(content string) (model string, provider string, baseURL string) {
	valueJSON, err := codexpreset.ReplaceFileValueJSON(content)
	if err != nil {
		return "", "", ""
	}
	return codexConfigModelSummary(valueJSON)
}

func codexProfileSaveResult(provider store.Provider, profile store.Profile, configTarget store.ProfileTarget, authTarget store.ProfileTarget, home codexconfig.Home, warnings []string) (CodexProfileSaveResult, error) {
	publicProvider, err := providerFromStore(provider)
	if err != nil {
		return CodexProfileSaveResult{}, err
	}
	publicProfile, err := profileFromStore(profile)
	if err != nil {
		return CodexProfileSaveResult{}, err
	}
	publicConfigTarget, err := profileTargetFromStore(configTarget)
	if err != nil {
		return CodexProfileSaveResult{}, err
	}
	publicAuthTarget, err := profileTargetFromStore(authTarget)
	if err != nil {
		return CodexProfileSaveResult{}, err
	}
	return CodexProfileSaveResult{
		Provider:     publicProvider,
		Profile:      publicProfile,
		ConfigTarget: publicConfigTarget,
		AuthTarget:   publicAuthTarget,
		CodexDir:     home.Dir,
		ConfigPath:   home.ConfigPath,
		AuthPath:     home.AuthPath,
		Warnings:     warnings,
	}, nil
}

func wrapCodexMutationTxError(message string, err error) error {
	var appErr *AppError
	if errors.As(err, &appErr) {
		return appErr
	}
	if errors.Is(err, os.ErrNotExist) {
		return NewError(ErrorCodexInvalid, "Codex profile source is missing")
	}
	return WrapError(ErrorStoreStatusFailed, message, err)
}
