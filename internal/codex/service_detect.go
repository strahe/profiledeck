package codex

import (
	"context"
	"errors"
	"os"

	"github.com/strahe/profiledeck/internal/apperror"
	codexauth "github.com/strahe/profiledeck/internal/codex/auth"
	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	codexpreset "github.com/strahe/profiledeck/internal/codex/preset"
	"github.com/strahe/profiledeck/internal/profiletarget"
	"github.com/strahe/profiledeck/internal/store"
)

var codexTargetFormatStrategyNames = codexpreset.TargetFormatStrategyNames{
	JSONFormat:          profiletarget.FormatJSON,
	TOMLFormat:          profiletarget.FormatTOML,
	ReplaceFileStrategy: profiletarget.StrategyReplaceFile,
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

type codexExistingTargets struct {
	Config    store.ProfileTarget
	HasConfig bool
	Auth      store.ProfileTarget
	HasAuth   bool
}

type managedProfileFields struct {
	CreateName        string
	CreateDescription string
	UpdateName        *string
	UpdateDescription *string
}

func (service *Service) Detect(ctx context.Context) (CodexDetectResult, error) {
	if err := service.requireAccess(ctx); err != nil {
		return CodexDetectResult{}, err
	}
	home, err := service.resolveHome()
	if err != nil {
		return CodexDetectResult{}, err
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
	status, err := service.runtime.Status(ctx)
	if err != nil {
		return CodexDetectResult{}, err
	}
	result.ProfileDeckInitialized = status.Initialized && status.SchemaHealthy
	var (
		db       *store.Store
		provider store.Provider
	)
	if result.ProfileDeckInitialized {
		db, err = service.openStore(ctx, true)
		if err != nil {
			return CodexDetectResult{}, err
		}
		defer db.Close()

		provider, err = db.GetProvider(ctx, codexconfig.ProviderID)
		switch {
		case err == nil:
			result.ProviderExists = true
			result.ProviderAdapterID = provider.AdapterID
			result.ProviderCompatible = provider.AdapterID == codexconfig.AdapterID
			if !provider.Enabled {
				return CodexDetectResult{}, apperror.New(apperror.ProviderDisabled, "Codex Provider is disabled").
					WithDetail("provider_id", codexconfig.ProviderID)
			}
		case !errors.Is(err, store.ErrNotFound):
			return CodexDetectResult{}, mapProviderStoreError(err)
		}
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

	if status.Initialized && !status.SchemaHealthy {
		result.Warnings = append(result.Warnings, "ProfileDeck database schema is not healthy")
		return result, nil
	}
	if !result.ProfileDeckInitialized {
		return result, nil
	}

	if !result.ProviderExists {
		return result, nil
	}
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

func requireExistingCodexHome(home codexconfig.Home) *apperror.Error {
	info, err := os.Stat(home.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return apperror.New(apperror.CodexInvalid, "Codex home does not exist").WithDetail("codex_dir", home.Dir)
		}
		return apperror.Wrap(apperror.CodexInvalid, "failed to inspect Codex home", err).WithDetail("codex_dir", home.Dir)
	}
	if !info.IsDir() {
		return apperror.New(apperror.CodexInvalid, "Codex home is not a directory").WithDetail("codex_dir", home.Dir)
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
		return store.Provider{}, false, apperror.New(apperror.CodexInvalid, "existing codex provider uses a different adapter").
			WithDetail("adapter_id", provider.AdapterID)
	}
	if !provider.Enabled {
		return store.Provider{}, false, apperror.New(apperror.ProviderDisabled, "Codex Provider is disabled").
			WithDetail("provider_id", codexconfig.ProviderID)
	}
	if provider.MetadataJSON != "" {
		metadata, err := codexpreset.DecodeProviderMetadata(provider.MetadataJSON)
		if err != nil {
			return store.Provider{}, false, apperror.Wrap(apperror.StoreSchemaInvalid, "stored Codex provider metadata is invalid", err)
		}
		if metadata.Preset == codexconfig.PresetName && metadata.PresetVersion != codexconfig.PresetVersion {
			return store.Provider{}, false, apperror.New(apperror.CodexInvalid, "existing codex provider preset version is unsupported")
		}
		if metadata.Preset == codexconfig.PresetName && metadata.CodexDir != "" && metadata.CodexDir != home.Dir {
			return store.Provider{}, false, apperror.New(apperror.CodexInvalid, "existing codex provider uses a different Codex home").
				WithDetail("existing_codex_dir", metadata.CodexDir).
				WithDetail("requested_codex_dir", home.Dir)
		}
		if metadata.Preset == codexconfig.PresetName && metadata.ConfigPath != "" && metadata.ConfigPath != home.ConfigPath {
			return store.Provider{}, false, apperror.New(apperror.CodexInvalid, "existing codex provider uses a different config path").
				WithDetail("existing_config_path", metadata.ConfigPath).
				WithDetail("requested_config_path", home.ConfigPath)
		}
		if metadata.Preset == codexconfig.PresetName && metadata.AuthPath != "" && metadata.AuthPath != home.AuthPath {
			return store.Provider{}, false, apperror.New(apperror.CodexInvalid, "existing codex provider uses a different auth path").
				WithDetail("existing_auth_path", metadata.AuthPath).
				WithDetail("requested_auth_path", home.AuthPath)
		}
	}
	return provider, true, nil
}

func normalizeManagedProfileFields(profileID string, name, description *string) (managedProfileFields, *apperror.Error) {
	fields := managedProfileFields{
		CreateName: profileID,
	}
	if name != nil {
		normalized, appErr := validateName(*name, apperror.ProfileInvalid)
		if appErr != nil {
			return managedProfileFields{}, appErr
		}
		fields.CreateName = normalized
		fields.UpdateName = &normalized
	}
	if description != nil {
		normalized, appErr := validateDescription(*description, apperror.ProfileInvalid)
		if appErr != nil {
			return managedProfileFields{}, appErr
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

func codexProfileHasBindings(ctx context.Context, db *store.Store, profileID string) (bool, error) {
	credentialBindings, err := db.ListProfileCredentialBindings(ctx, profileID, codexconfig.ProviderID)
	if err != nil {
		return false, apperror.Wrap(apperror.StoreStatusFailed, "failed to inspect Codex login bindings", err)
	}
	configBindings, err := db.ListProfileConfigSetBindings(ctx, profileID, codexconfig.ProviderID)
	if err != nil {
		return false, apperror.Wrap(apperror.StoreStatusFailed, "failed to inspect Codex config bindings", err)
	}
	return len(credentialBindings) != 0 || len(configBindings) != 0, nil
}

func codexPreflightTargets(ctx context.Context, db *store.Store, home codexconfig.Home, profileID string) (codexExistingTargets, error) {
	targets, err := codexBindingTargets(ctx, db, profileID, home)
	if err != nil {
		return codexExistingTargets{}, err
	}

	current := codexExistingTargets{}
	for _, target := range targets {
		if appErr := requireCodexTargetForHome(target, home); appErr != nil {
			return codexExistingTargets{}, appErr
		}
		switch target.TargetID {
		case codexconfig.TargetID:
			current.Config = target
			current.HasConfig = true
		case codexconfig.AuthTargetID:
			current.Auth = target
			current.HasAuth = true
		}
	}

	var currentConfigIDs *profiletarget.Identity
	if current.HasConfig {
		currentConfigIDs = &profiletarget.Identity{
			ProfileID:  current.Config.ProfileID,
			ProviderID: current.Config.ProviderID,
			TargetID:   current.Config.TargetID,
		}
	}
	if appErr := profiletarget.EnsurePathOwnership(ctx, db, home.ConfigPath, profiletarget.PathOwnershipKey(home.ConfigPath), codexconfig.ProviderID, codexconfig.TargetID, currentConfigIDs); appErr != nil {
		return codexExistingTargets{}, appErr
	}

	var currentAuthIDs *profiletarget.Identity
	if current.HasAuth {
		currentAuthIDs = &profiletarget.Identity{
			ProfileID:  current.Auth.ProfileID,
			ProviderID: current.Auth.ProviderID,
			TargetID:   current.Auth.TargetID,
		}
	}
	if appErr := profiletarget.EnsurePathOwnership(ctx, db, home.AuthPath, profiletarget.PathOwnershipKey(home.AuthPath), codexconfig.ProviderID, codexconfig.AuthTargetID, currentAuthIDs); appErr != nil {
		return codexExistingTargets{}, appErr
	}

	return current, nil
}

func requireCodexTargetForHome(target store.ProfileTarget, home codexconfig.Home) *apperror.Error {
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
	case codexconfig.AuthTargetID:
		if target.Path != home.AuthPath {
			return codexTargetInvalid(target, "existing codex auth target uses a different auth path").
				WithDetail("existing_auth_path", target.Path).
				WithDetail("requested_auth_path", home.AuthPath)
		}
	default:
		return codexTargetInvalid(target, "existing codex provider has an unsupported target")
	}
	return nil
}

func codexTargetInvalid(target store.ProfileTarget, message string) *apperror.Error {
	return apperror.New(apperror.CodexInvalid, message).
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

	targets, err := allStoredCodexBindingTargets(ctx, db)
	if err != nil {
		return nil, err
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
