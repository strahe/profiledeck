package app

import (
	"context"
	"errors"
	"os"

	codexauth "github.com/strahe/profiledeck/internal/codex/auth"
	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	codexpreset "github.com/strahe/profiledeck/internal/codex/preset"
	"github.com/strahe/profiledeck/internal/store"
)

var codexTargetFormatStrategyNames = codexpreset.TargetFormatStrategyNames{
	JSONFormat:          targetFormatJSON,
	TOMLFormat:          targetFormatTOML,
	TOMLMergeStrategy:   targetStrategyTOMLMerge,
	ReplaceFileStrategy: targetStrategyReplaceFile,
}

type CodexDetectRequest struct {
	ConfigDir string
	CodexDir  string
}

type CodexDetectResult struct {
	ProviderID             string   `json:"provider_id"`
	AdapterID              string   `json:"adapter_id"`
	CodexDir               string   `json:"codex_dir"`
	ConfigPath             string   `json:"config_path"`
	AuthPath               string   `json:"auth_path"`
	CodexDirExists         bool     `json:"codex_dir_exists"`
	ConfigStatus           string   `json:"config_status"`
	AuthStatus             string   `json:"auth_status"`
	ProfileDeckInitialized bool     `json:"profiledeck_initialized"`
	ProviderExists         bool     `json:"provider_exists"`
	ProviderAdapterID      string   `json:"provider_adapter_id"`
	ProviderCompatible     bool     `json:"provider_compatible"`
	Warnings               []string `json:"warnings"`
}

type CodexProfileSetRequest struct {
	ConfigDir     string
	CodexDir      string
	ProfileID     string
	Model         string
	ModelProvider string
	OpenAIBaseURL *string
	AccountID     string
	Name          *string
	Description   *string
}

type CodexProfileSetResult struct {
	Provider    Provider       `json:"provider"`
	Profile     Profile        `json:"profile"`
	Target      ProfileTarget  `json:"target"`
	AuthTarget  *ProfileTarget `json:"auth_target,omitempty"`
	CodexDir    string         `json:"codex_dir"`
	ConfigPath  string         `json:"config_path"`
	AuthPath    string         `json:"auth_path"`
	ManagedKeys []string       `json:"managed_keys"`
	Warnings    []string       `json:"warnings"`
}

type codexExistingTargets struct {
	Config    store.ProfileTarget
	HasConfig bool
	Auth      store.ProfileTarget
	HasAuth   bool
}

type codexProfileFields struct {
	CreateName        string
	CreateDescription string
	UpdateName        *string
	UpdateDescription *string
}

func CodexDetect(ctx context.Context, req CodexDetectRequest) (CodexDetectResult, error) {
	home, err := codexconfig.ResolveHome(req.CodexDir)
	if err != nil {
		return CodexDetectResult{}, WrapError(ErrorCodexInvalid, "failed to resolve Codex home", err)
	}

	result := CodexDetectResult{
		ProviderID:         codexconfig.ProviderID,
		AdapterID:          codexconfig.AdapterID,
		CodexDir:           home.Dir,
		ConfigPath:         home.ConfigPath,
		AuthPath:           home.AuthPath,
		ConfigStatus:       "missing",
		AuthStatus:         "missing",
		ProviderCompatible: true,
	}

	if info, err := os.Stat(home.Dir); err == nil {
		result.CodexDirExists = info.IsDir()
		if !info.IsDir() {
			result.Warnings = append(result.Warnings, "Codex home exists but is not a directory")
		}
	} else if !os.IsNotExist(err) {
		result.Warnings = append(result.Warnings, "failed to inspect Codex home: "+err.Error())
	}

	if raw, err := os.ReadFile(home.ConfigPath); err == nil {
		if err := codexconfig.ValidateTOML(string(raw)); err != nil {
			result.ConfigStatus = "invalid"
			result.Warnings = append(result.Warnings, "Codex config TOML is invalid")
		} else {
			result.ConfigStatus = "valid"
		}
	} else if os.IsNotExist(err) {
		result.ConfigStatus = "missing"
	} else {
		result.ConfigStatus = "unreadable"
		result.Warnings = append(result.Warnings, "failed to read Codex config: "+err.Error())
	}
	if raw, err := os.ReadFile(home.AuthPath); err == nil {
		if _, err := codexauth.NormalizePayload(raw); err != nil {
			result.AuthStatus = "invalid"
			result.Warnings = append(result.Warnings, "Codex auth JSON is invalid")
		} else {
			result.AuthStatus = "valid"
		}
	} else if os.IsNotExist(err) {
		result.AuthStatus = "missing"
	} else {
		result.AuthStatus = "unreadable"
		result.Warnings = append(result.Warnings, "failed to read Codex auth: "+err.Error())
	}

	status, err := Status(ctx, StatusRequest{ConfigDir: req.ConfigDir})
	if err != nil {
		return CodexDetectResult{}, err
	}
	result.ProfileDeckInitialized = status.Initialized && status.SchemaHealthy
	if status.Initialized && !status.SchemaHealthy {
		result.Warnings = append(result.Warnings, "ProfileDeck database schema is not healthy")
		return result, nil
	}
	if !result.ProfileDeckInitialized {
		return result, nil
	}

	db, err := openHealthyStore(ctx, req.ConfigDir, true)
	if err != nil {
		return CodexDetectResult{}, err
	}
	defer db.Close()

	provider, err := db.GetProvider(ctx, codexconfig.ProviderID)
	if errors.Is(err, store.ErrNotFound) {
		return result, nil
	}
	if err != nil {
		return CodexDetectResult{}, mapProviderStoreError(err)
	}
	result.ProviderExists = true
	result.ProviderAdapterID = provider.AdapterID
	result.ProviderCompatible = provider.AdapterID == codexconfig.AdapterID
	if !result.ProviderCompatible {
		result.Warnings = append(result.Warnings, "existing codex provider uses a different adapter")
		return result, nil
	}
	warnings, err := codexDetectCompatibilityWarnings(ctx, db, provider, home)
	if err != nil {
		return CodexDetectResult{}, err
	}
	if len(warnings) > 0 {
		result.ProviderCompatible = false
		result.Warnings = append(result.Warnings, warnings...)
	}
	return result, nil
}

func CodexProfileSet(ctx context.Context, req CodexProfileSetRequest) (CodexProfileSetResult, error) {
	profileID, appErr := validateID(req.ProfileID, ErrorProfileInvalid)
	if appErr != nil {
		return CodexProfileSetResult{}, appErr
	}
	home, err := codexconfig.ResolveHome(req.CodexDir)
	if err != nil {
		return CodexProfileSetResult{}, WrapError(ErrorCodexInvalid, "failed to resolve Codex home", err)
	}
	if appErr := requireExistingCodexHome(home); appErr != nil {
		return CodexProfileSetResult{}, appErr
	}
	profileFields, appErr := normalizeCodexProfileFields(profileID, req.Name, req.Description)
	if appErr != nil {
		return CodexProfileSetResult{}, appErr
	}
	accountID, appErr := normalizeOptionalCodexAccountID(req.AccountID)
	if appErr != nil {
		return CodexProfileSetResult{}, appErr
	}
	managed, err := codexconfig.NormalizeManaged(req.Model, req.ModelProvider, req.OpenAIBaseURL)
	if err != nil {
		return CodexProfileSetResult{}, WrapError(ErrorCodexInvalid, "Codex profile config is invalid", err)
	}
	valueJSON, err := codexconfig.ValueJSON(managed)
	if err != nil {
		return CodexProfileSetResult{}, WrapError(ErrorCodexInvalid, "failed to encode Codex target value", err)
	}
	providerMetadata, err := codexpreset.ProviderMetadataJSON(home)
	if err != nil {
		return CodexProfileSetResult{}, WrapError(ErrorCodexInvalid, "failed to encode Codex provider metadata", err)
	}
	targetMetadata, err := codexpreset.TargetMetadataJSON(codexconfig.TargetID, codexpreset.TargetModeManagedKeys)
	if err != nil {
		return CodexProfileSetResult{}, WrapError(ErrorCodexInvalid, "failed to encode Codex preset metadata", err)
	}
	authTargetMetadata, err := codexpreset.TargetMetadataJSON(codexconfig.AuthTargetID, codexpreset.TargetModeFullFile)
	if err != nil {
		return CodexProfileSetResult{}, WrapError(ErrorCodexInvalid, "failed to encode Codex auth target metadata", err)
	}
	authValueJSON, err := codexpreset.AuthTargetValueJSON(accountID)
	if err != nil {
		return CodexProfileSetResult{}, WrapError(ErrorCodexInvalid, "failed to encode Codex auth target value", err)
	}

	db, err := openHealthyStore(ctx, req.ConfigDir, false)
	if err != nil {
		return CodexProfileSetResult{}, err
	}
	defer db.Close()

	var provider store.Provider
	var profile store.Profile
	var target store.ProfileTarget
	var authTarget *store.ProfileTarget
	if err := db.WithTransaction(ctx, func(txStore *store.Store) error {
		var hasProvider bool
		var hasProfile bool
		var targets codexExistingTargets
		var err error

		provider, hasProvider, err = codexPreflightProvider(ctx, txStore, home)
		if err != nil {
			return err
		}
		profile, hasProfile, err = codexPreflightProfile(ctx, txStore, profileID)
		if err != nil {
			return err
		}
		targets, err = codexPreflightTargets(ctx, txStore, home, profileID)
		if err != nil {
			return err
		}
		if accountID != "" {
			if _, err := txStore.GetProviderAccountSecret(ctx, codexconfig.ProviderID, accountID); err != nil {
				if errors.Is(err, store.ErrNotFound) {
					return NewError(ErrorCodexInvalid, "Codex account does not exist").WithDetail("account_id", accountID)
				}
				return WrapError(ErrorStoreStatusFailed, "failed to read Codex account", err)
			}
		}

		if !hasProvider {
			provider, err = txStore.CreateProvider(ctx, store.CreateProviderParams{
				ID:           codexconfig.ProviderID,
				Name:         codexpreset.ProviderName,
				AdapterID:    codexconfig.AdapterID,
				Enabled:      true,
				MetadataJSON: providerMetadata,
			})
		} else {
			enabled := true
			name := codexpreset.ProviderName
			provider, err = txStore.UpdateProvider(ctx, store.UpdateProviderParams{
				ID:           codexconfig.ProviderID,
				Name:         &name,
				Enabled:      &enabled,
				MetadataJSON: &providerMetadata,
			})
		}
		if err != nil {
			return mapProviderStoreError(err)
		}

		if !hasProfile {
			profile, err = txStore.CreateProfile(ctx, store.CreateProfileParams{
				ID:           profileID,
				Name:         profileFields.CreateName,
				Description:  profileFields.CreateDescription,
				MetadataJSON: "{}",
			})
		} else if profileFields.UpdateName != nil || profileFields.UpdateDescription != nil {
			profile, err = txStore.UpdateProfile(ctx, store.UpdateProfileParams{
				ID:          profileID,
				Name:        profileFields.UpdateName,
				Description: profileFields.UpdateDescription,
			})
		}
		if err != nil {
			return mapProfileStoreError(err)
		}

		enabled := true
		if !targets.HasConfig {
			target, err = txStore.CreateProfileTarget(ctx, store.CreateProfileTargetParams{
				ProfileID:    profileID,
				ProviderID:   codexconfig.ProviderID,
				TargetID:     codexconfig.TargetID,
				Path:         home.ConfigPath,
				PathKey:      targetPathOwnershipKey(home.ConfigPath),
				Format:       targetFormatTOML,
				Strategy:     targetStrategyTOMLMerge,
				ValueJSON:    valueJSON,
				Enabled:      true,
				MetadataJSON: targetMetadata,
			})
		} else {
			path := home.ConfigPath
			pathKey := targetPathOwnershipKey(home.ConfigPath)
			format := targetFormatTOML
			strategy := targetStrategyTOMLMerge
			target, err = txStore.UpdateProfileTarget(ctx, store.UpdateProfileTargetParams{
				ProfileID:    profileID,
				ProviderID:   codexconfig.ProviderID,
				TargetID:     codexconfig.TargetID,
				Path:         &path,
				PathKey:      &pathKey,
				Format:       &format,
				Strategy:     &strategy,
				ValueJSON:    &valueJSON,
				Enabled:      &enabled,
				MetadataJSON: &targetMetadata,
			})
		}
		if err != nil {
			return mapTargetStoreError(err)
		}
		if accountID != "" {
			if !targets.HasAuth {
				created, err := txStore.CreateProfileTarget(ctx, store.CreateProfileTargetParams{
					ProfileID:    profileID,
					ProviderID:   codexconfig.ProviderID,
					TargetID:     codexconfig.AuthTargetID,
					Path:         home.AuthPath,
					PathKey:      targetPathOwnershipKey(home.AuthPath),
					Format:       targetFormatJSON,
					Strategy:     targetStrategyReplaceFile,
					ValueJSON:    authValueJSON,
					Enabled:      true,
					MetadataJSON: authTargetMetadata,
				})
				if err != nil {
					return mapTargetStoreError(err)
				}
				authTarget = &created
			} else {
				path := home.AuthPath
				pathKey := targetPathOwnershipKey(home.AuthPath)
				format := targetFormatJSON
				strategy := targetStrategyReplaceFile
				updated, err := txStore.UpdateProfileTarget(ctx, store.UpdateProfileTargetParams{
					ProfileID:    profileID,
					ProviderID:   codexconfig.ProviderID,
					TargetID:     codexconfig.AuthTargetID,
					Path:         &path,
					PathKey:      &pathKey,
					Format:       &format,
					Strategy:     &strategy,
					ValueJSON:    &authValueJSON,
					Enabled:      &enabled,
					MetadataJSON: &authTargetMetadata,
				})
				if err != nil {
					return mapTargetStoreError(err)
				}
				authTarget = &updated
			}
		} else if targets.HasAuth {
			existing := targets.Auth
			authTarget = &existing
		}
		return nil
	}); err != nil {
		var appErr *AppError
		if errors.As(err, &appErr) {
			return CodexProfileSetResult{}, appErr
		}
		return CodexProfileSetResult{}, WrapError(ErrorStoreStatusFailed, "Codex profile set transaction failed", err)
	}

	publicProvider, err := providerFromStore(provider)
	if err != nil {
		return CodexProfileSetResult{}, err
	}
	publicProfile, err := profileFromStore(profile)
	if err != nil {
		return CodexProfileSetResult{}, err
	}
	publicTarget, err := profileTargetFromStore(target)
	if err != nil {
		return CodexProfileSetResult{}, err
	}
	var publicAuthTarget *ProfileTarget
	if authTarget != nil {
		value, err := profileTargetFromStore(*authTarget)
		if err != nil {
			return CodexProfileSetResult{}, err
		}
		publicAuthTarget = &value
	}

	return CodexProfileSetResult{
		Provider:    publicProvider,
		Profile:     publicProfile,
		Target:      publicTarget,
		AuthTarget:  publicAuthTarget,
		CodexDir:    home.Dir,
		ConfigPath:  home.ConfigPath,
		AuthPath:    home.AuthPath,
		ManagedKeys: codexconfig.ManagedKeys(),
	}, nil
}

func requireExistingCodexHome(home codexconfig.Home) *AppError {
	info, err := os.Stat(home.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return NewError(ErrorCodexInvalid, "Codex home does not exist").WithDetail("codex_dir", home.Dir)
		}
		return WrapError(ErrorCodexInvalid, "failed to inspect Codex home", err).WithDetail("codex_dir", home.Dir)
	}
	if !info.IsDir() {
		return NewError(ErrorCodexInvalid, "Codex home is not a directory").WithDetail("codex_dir", home.Dir)
	}
	return nil
}

func codexPreflightProvider(ctx context.Context, db *store.Store, home codexconfig.Home) (store.Provider, bool, error) {
	provider, err := db.GetProvider(ctx, codexconfig.ProviderID)
	if errors.Is(err, store.ErrNotFound) {
		return store.Provider{}, false, nil
	}
	if err != nil {
		return store.Provider{}, false, mapProviderStoreError(err)
	}
	if provider.AdapterID != codexconfig.AdapterID {
		return store.Provider{}, false, NewError(ErrorCodexInvalid, "existing codex provider uses a different adapter").
			WithDetail("adapter_id", provider.AdapterID)
	}
	if provider.MetadataJSON != "" {
		metadata, err := codexpreset.DecodeProviderMetadata(provider.MetadataJSON)
		if err != nil {
			return store.Provider{}, false, WrapError(ErrorStoreSchemaInvalid, "stored Codex provider metadata is invalid", err)
		}
		if metadata.Preset == codexconfig.PresetName && metadata.PresetVersion != codexconfig.PresetVersion {
			return store.Provider{}, false, NewError(ErrorCodexInvalid, "existing codex provider preset version is unsupported")
		}
		if metadata.Preset == codexconfig.PresetName && metadata.CodexDir != "" && metadata.CodexDir != home.Dir {
			return store.Provider{}, false, NewError(ErrorCodexInvalid, "existing codex provider uses a different Codex home").
				WithDetail("existing_codex_dir", metadata.CodexDir).
				WithDetail("requested_codex_dir", home.Dir)
		}
		if metadata.Preset == codexconfig.PresetName && metadata.ConfigPath != "" && metadata.ConfigPath != home.ConfigPath {
			return store.Provider{}, false, NewError(ErrorCodexInvalid, "existing codex provider uses a different config path").
				WithDetail("existing_config_path", metadata.ConfigPath).
				WithDetail("requested_config_path", home.ConfigPath)
		}
		if metadata.Preset == codexconfig.PresetName && metadata.AuthPath != "" && metadata.AuthPath != home.AuthPath {
			return store.Provider{}, false, NewError(ErrorCodexInvalid, "existing codex provider uses a different auth path").
				WithDetail("existing_auth_path", metadata.AuthPath).
				WithDetail("requested_auth_path", home.AuthPath)
		}
	}
	return provider, true, nil
}

func normalizeCodexProfileFields(profileID string, name *string, description *string) (codexProfileFields, *AppError) {
	fields := codexProfileFields{
		CreateName: profileID,
	}
	if name != nil {
		normalized, appErr := validateName(*name, ErrorProfileInvalid)
		if appErr != nil {
			return codexProfileFields{}, appErr
		}
		fields.CreateName = normalized
		fields.UpdateName = &normalized
	}
	if description != nil {
		normalized, appErr := validateDescription(*description, ErrorProfileInvalid)
		if appErr != nil {
			return codexProfileFields{}, appErr
		}
		fields.CreateDescription = normalized
		fields.UpdateDescription = &normalized
	}
	return fields, nil
}

func codexPreflightProfile(ctx context.Context, db *store.Store, profileID string) (store.Profile, bool, error) {
	profile, err := db.GetProfile(ctx, profileID)
	if errors.Is(err, store.ErrNotFound) {
		return store.Profile{}, false, nil
	}
	if err != nil {
		return store.Profile{}, false, mapProfileStoreError(err)
	}
	return profile, true, nil
}

func codexPreflightTargets(ctx context.Context, db *store.Store, home codexconfig.Home, profileID string) (codexExistingTargets, error) {
	targets, err := db.ListProfileTargetsByProvider(ctx, codexconfig.ProviderID)
	if err != nil {
		return codexExistingTargets{}, WrapError(ErrorStoreStatusFailed, "failed to inspect Codex profile targets", err)
	}

	current := codexExistingTargets{}
	for _, target := range targets {
		if appErr := requireCodexTargetForHome(target, home); appErr != nil {
			return codexExistingTargets{}, appErr
		}
		if target.ProfileID == profileID {
			switch target.TargetID {
			case codexconfig.TargetID:
				current.Config = target
				current.HasConfig = true
			case codexconfig.AuthTargetID:
				current.Auth = target
				current.HasAuth = true
			}
		}
	}

	var currentConfigIDs *profileTargetIDs
	if current.HasConfig {
		currentConfigIDs = &profileTargetIDs{
			ProfileID:  current.Config.ProfileID,
			ProviderID: current.Config.ProviderID,
			TargetID:   current.Config.TargetID,
		}
	}
	if appErr := ensureProfileTargetPathOwnership(ctx, db, home.ConfigPath, targetPathOwnershipKey(home.ConfigPath), codexconfig.ProviderID, codexconfig.TargetID, currentConfigIDs); appErr != nil {
		return codexExistingTargets{}, appErr
	}

	var currentAuthIDs *profileTargetIDs
	if current.HasAuth {
		currentAuthIDs = &profileTargetIDs{
			ProfileID:  current.Auth.ProfileID,
			ProviderID: current.Auth.ProviderID,
			TargetID:   current.Auth.TargetID,
		}
	}
	if appErr := ensureProfileTargetPathOwnership(ctx, db, home.AuthPath, targetPathOwnershipKey(home.AuthPath), codexconfig.ProviderID, codexconfig.AuthTargetID, currentAuthIDs); appErr != nil {
		return codexExistingTargets{}, appErr
	}

	return current, nil
}

func requireCodexTargetMetadata(target store.ProfileTarget) *AppError {
	metadata, err := codexpreset.DecodeTargetMetadata(target.MetadataJSON)
	if err != nil {
		return WrapError(ErrorStoreSchemaInvalid, "stored Codex target metadata is invalid", err).
			WithDetail("profile_id", target.ProfileID).
			WithDetail("target_id", target.TargetID)
	}
	if !metadata.Compatible() {
		return NewError(ErrorCodexInvalid, "existing codex target was not created by the Codex preset").
			WithDetail("profile_id", target.ProfileID).
			WithDetail("target_id", target.TargetID)
	}
	return nil
}

func requireCodexTargetForHome(target store.ProfileTarget, home codexconfig.Home) *AppError {
	if target.ProviderID != codexconfig.ProviderID {
		return codexTargetInvalid(target, "existing Codex target has an unsupported provider")
	}
	switch target.TargetID {
	case codexconfig.TargetID:
		if target.Path != home.ConfigPath {
			return codexTargetInvalid(target, "existing codex target uses a different config path").
				WithDetail("existing_config_path", target.Path).
				WithDetail("requested_config_path", home.ConfigPath)
		}
		if !codexConfigTargetFormatValid(target) {
			return codexTargetInvalid(target, "existing codex config target uses an unsupported format").
				WithDetail("format", target.Format)
		}
		if !codexConfigTargetStrategyValid(target) {
			return codexTargetInvalid(target, "existing codex config target uses an unsupported strategy").
				WithDetail("strategy", target.Strategy)
		}
	case codexconfig.AuthTargetID:
		if target.Path != home.AuthPath {
			return codexTargetInvalid(target, "existing codex auth target uses a different auth path").
				WithDetail("existing_auth_path", target.Path).
				WithDetail("requested_auth_path", home.AuthPath)
		}
		if !codexAuthTargetFormatStrategyValid(target) {
			return codexTargetInvalid(target, "existing codex auth target uses an unsupported format or strategy").
				WithDetail("format", target.Format).
				WithDetail("strategy", target.Strategy)
		}
	default:
		return codexTargetInvalid(target, "existing codex provider has an unsupported target")
	}
	return requireCodexTargetMetadata(target)
}

func codexTargetInvalid(target store.ProfileTarget, message string) *AppError {
	return NewError(ErrorCodexInvalid, message).
		WithDetail("profile_id", target.ProfileID).
		WithDetail("provider_id", target.ProviderID).
		WithDetail("target_id", target.TargetID)
}

func codexConfigTargetFormatValid(target store.ProfileTarget) bool {
	return codexpreset.ConfigTargetFormatValid(target.Format, codexTargetFormatStrategyNames)
}

func codexConfigTargetStrategyValid(target store.ProfileTarget) bool {
	return codexpreset.ConfigTargetStrategyValid(target.Strategy, codexTargetFormatStrategyNames)
}

func codexAuthTargetFormatStrategyValid(target store.ProfileTarget) bool {
	return codexpreset.AuthTargetFormatStrategyValid(target.Format, target.Strategy, codexTargetFormatStrategyNames)
}

func codexDetectCompatibilityWarnings(ctx context.Context, db *store.Store, provider store.Provider, home codexconfig.Home) ([]string, error) {
	warnings := []string{}
	if provider.MetadataJSON != "" {
		metadata, err := codexpreset.DecodeProviderMetadata(provider.MetadataJSON)
		if err != nil {
			warnings = append(warnings, "existing codex provider metadata is invalid")
		} else if metadata.Preset == codexconfig.PresetName {
			if metadata.PresetVersion != codexconfig.PresetVersion {
				warnings = append(warnings, "existing codex provider preset version is unsupported")
			}
			if metadata.CodexDir != "" && metadata.CodexDir != home.Dir {
				warnings = append(warnings, "existing codex provider uses a different Codex home")
			}
			if metadata.ConfigPath != "" && metadata.ConfigPath != home.ConfigPath {
				warnings = append(warnings, "existing codex provider uses a different config path")
			}
			if metadata.AuthPath != "" && metadata.AuthPath != home.AuthPath {
				warnings = append(warnings, "existing codex provider uses a different auth path")
			}
		}
	}

	targets, err := db.ListProfileTargetsByProvider(ctx, codexconfig.ProviderID)
	if err != nil {
		return nil, WrapError(ErrorStoreStatusFailed, "failed to inspect Codex profile targets", err)
	}
	for _, target := range targets {
		warnings = append(warnings, codexDetectTargetWarnings(target, home)...)
	}
	return warnings, nil
}

func codexDetectTargetWarnings(target store.ProfileTarget, home codexconfig.Home) []string {
	warnings := []string{}
	switch target.TargetID {
	case codexconfig.TargetID:
		if target.Path != home.ConfigPath {
			warnings = append(warnings, "existing codex config target uses a different config path")
		}
		if !codexConfigTargetFormatValid(target) {
			warnings = append(warnings, "existing codex config target uses an unsupported format")
		}
		if !codexConfigTargetStrategyValid(target) {
			warnings = append(warnings, "existing codex config target uses an unsupported strategy")
		}
	case codexconfig.AuthTargetID:
		if target.Path != home.AuthPath {
			warnings = append(warnings, "existing codex auth target uses a different auth path")
		}
		if !codexAuthTargetFormatStrategyValid(target) {
			warnings = append(warnings, "existing codex auth target uses an unsupported format or strategy")
		}
	default:
		return append(warnings, "existing codex provider has an unsupported target")
	}
	metadata, err := codexpreset.DecodeTargetMetadata(target.MetadataJSON)
	if err != nil {
		warnings = append(warnings, "existing codex target metadata is invalid")
	} else if !metadata.Compatible() {
		warnings = append(warnings, "existing codex target was not created by the Codex preset")
	}
	return warnings
}
