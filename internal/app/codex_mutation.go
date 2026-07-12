package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
	CodexForkBindingShareParent = "share-parent"
	CodexForkBindingCopyNew     = "copy-new"
	codexSharedConfigSetID      = "shared"
	codexSharedConfigSetName    = "Shared"
	codexMaintenanceRandomBytes = 6
)

type CreateCodexProfileRequest struct {
	ConfigDir               string  `json:"config_dir"`
	CodexDir                string  `json:"codex_dir"`
	ProfileID               string  `json:"profile_id"`
	Name                    *string `json:"name,omitempty"`
	Description             *string `json:"description,omitempty"`
	NewConfigSetID          string  `json:"new_config_set_id,omitempty"`
	NewConfigSetName        *string `json:"new_config_set_name,omitempty"`
	NewConfigSetDescription *string `json:"new_config_set_description,omitempty"`
	ConfigContent           *string `json:"config_content,omitempty"`
	AuthContent             *string `json:"auth_content,omitempty"`
}

type ForkCodexProfileRequest struct {
	ConfigDir               string  `json:"config_dir"`
	CodexDir                string  `json:"codex_dir"`
	SourceProfileID         string  `json:"source_profile_id"`
	ProfileID               string  `json:"profile_id"`
	CredentialBinding       string  `json:"credential_binding"`
	ConfigBinding           string  `json:"config_binding"`
	NewConfigSetID          string  `json:"new_config_set_id,omitempty"`
	NewConfigSetName        *string `json:"new_config_set_name,omitempty"`
	NewConfigSetDescription *string `json:"new_config_set_description,omitempty"`
	Name                    *string `json:"name,omitempty"`
	Description             *string `json:"description,omitempty"`
}

type UpdateCodexProfileConfigSetRequest struct {
	ConfigDir   string `json:"config_dir"`
	ProfileID   string `json:"profile_id"`
	ConfigSetID string `json:"config_set_id"`
}

type SaveActiveCodexProfileStateRequest struct {
	ConfigDir string `json:"config_dir"`
	CodexDir  string `json:"codex_dir"`
}

type CodexProfileSaveResult struct {
	OperationID string              `json:"operation_id"`
	Provider    Provider            `json:"provider"`
	Profile     Profile             `json:"profile"`
	Summary     CodexProfileSummary `json:"summary"`
	ConfigSet   CodexConfigSet      `json:"config_set"`
	CodexDir    string              `json:"codex_dir"`
	ConfigPath  string              `json:"config_path"`
	AuthPath    string              `json:"auth_path"`
	Warnings    []string            `json:"warnings,omitempty"`
}

type CodexProfileStateSaveResult struct {
	OperationID              string         `json:"operation_id"`
	ProfileID                string         `json:"profile_id"`
	CredentialID             string         `json:"credential_id"`
	CredentialReferenceCount int            `json:"credential_reference_count"`
	ConfigSet                CodexConfigSet `json:"config_set"`
	Warnings                 []string       `json:"warnings,omitempty"`
}

type codexProfilePayload struct {
	Home          codexconfig.Home
	ConfigContent string
	AuthPayload   string
}

func CreateCodexProfile(ctx context.Context, req CreateCodexProfileRequest) (CodexProfileSaveResult, error) {
	profileID, appErr := validateID(req.ProfileID, ErrorProfileInvalid)
	if appErr != nil {
		return CodexProfileSaveResult{}, appErr
	}
	fields, appErr := normalizeManagedProfileFields(profileID, req.Name, req.Description)
	if appErr != nil {
		return CodexProfileSaveResult{}, appErr
	}
	home, err := resolveCodexMutationHome(req.CodexDir)
	if err != nil {
		return CodexProfileSaveResult{}, err
	}
	db, lock, operationID, err := openLockedCodexStore(ctx, req.ConfigDir, "profile-create")
	if err != nil {
		return CodexProfileSaveResult{}, err
	}
	defer db.Close()
	defer lock.Release()
	payload, err := loadCodexProfilePayload(home, req.ConfigContent, req.AuthContent)
	if err != nil {
		return CodexProfileSaveResult{}, err
	}

	var provider store.Provider
	var profile store.Profile
	var configSet store.ProviderConfigSet
	err = db.WithTransaction(ctx, func(txStore *store.Store) error {
		currentProvider, hasProvider, err := codexPreflightProvider(ctx, txStore, home)
		if err != nil {
			return err
		}
		_, hasProfile, err := codexPreflightProfile(ctx, txStore, profileID)
		if err != nil {
			return err
		}
		if hasProfile {
			hasBindings, err := codexProfileHasBindings(ctx, txStore, profileID)
			if err != nil {
				return err
			}
			if hasBindings {
				return NewError(ErrorProfileAlreadyExists, "Codex profile already exists").WithDetail("profile_id", profileID)
			}
		}
		configSet, err = resolveCreatedProfileConfigSet(ctx, txStore, payload.ConfigContent, req)
		if err != nil {
			return err
		}
		credentialID, err := newCodexCredentialID(time.Now())
		if err != nil {
			return WrapError(ErrorCodexInvalid, "failed to generate Codex credential id", err)
		}
		if _, err := upsertCodexAuthCredential(ctx, txStore, credentialID, payload.AuthPayload); err != nil {
			return err
		}
		providerMetadata, err := codexpreset.ProviderMetadataJSON(home)
		if err != nil {
			return WrapError(ErrorCodexInvalid, "failed to encode Codex provider metadata", err)
		}
		provider, err = upsertCodexProvider(ctx, txStore, providerMetadata, hasProvider)
		if err != nil {
			return err
		}
		_ = currentProvider
		profile, err = upsertCodexProfile(ctx, txStore, profileID, fields, hasProfile)
		if err != nil {
			return err
		}
		if _, _, err := createCodexProfileTargets(ctx, txStore, profileID, home, configSet.ID, credentialID); err != nil {
			return err
		}
		metadata, err := codexMaintenanceMetadata("profile-create", profileID, configSet.ID, credentialID)
		if err != nil {
			return err
		}
		_, err = txStore.CreateAppliedMaintenanceOperation(ctx, store.CreateAppliedMaintenanceOperationParams{
			ID: operationID, ProfileID: profileID, ProviderID: codexconfig.ProviderID, MetadataJSON: metadata, SetActive: true,
		})
		return err
	})
	if err != nil {
		return CodexProfileSaveResult{}, wrapCodexMutationTxError("Codex profile create transaction failed", err)
	}
	return codexProfileSaveResultFromStore(ctx, db, provider, profile, configSet, operationID, payload.Home, nil)
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
	credentialBinding, appErr := normalizeCodexForkBinding(req.CredentialBinding, "credential")
	if appErr != nil {
		return CodexProfileSaveResult{}, appErr
	}
	configBinding, appErr := normalizeCodexForkBinding(req.ConfigBinding, "config")
	if appErr != nil {
		return CodexProfileSaveResult{}, appErr
	}
	if credentialBinding == CodexForkBindingShareParent && configBinding == CodexForkBindingShareParent {
		return CodexProfileSaveResult{}, NewError(ErrorCodexInvalid, "a Codex fork must copy at least one binding")
	}
	fields, appErr := normalizeManagedProfileFields(profileID, req.Name, req.Description)
	if appErr != nil {
		return CodexProfileSaveResult{}, appErr
	}
	home, err := resolveCodexMutationHome(req.CodexDir)
	if err != nil {
		return CodexProfileSaveResult{}, err
	}
	db, lock, operationID, err := openLockedCodexStore(ctx, req.ConfigDir, "profile-fork")
	if err != nil {
		return CodexProfileSaveResult{}, err
	}
	defer db.Close()
	defer lock.Release()

	var provider store.Provider
	var profile store.Profile
	var configSet store.ProviderConfigSet
	err = db.WithTransaction(ctx, func(txStore *store.Store) error {
		var hasProvider bool
		provider, hasProvider, err = codexPreflightProvider(ctx, txStore, home)
		if err != nil {
			return err
		}
		if !hasProvider {
			return NewError(ErrorProviderNotFound, "Codex provider not found")
		}
		if _, err := txStore.GetProfile(ctx, sourceProfileID); err != nil {
			return mapProfileStoreError(err)
		}
		_, hasProfile, err := codexPreflightProfile(ctx, txStore, profileID)
		if err != nil {
			return err
		}
		if hasProfile {
			hasBindings, err := codexProfileHasBindings(ctx, txStore, profileID)
			if err != nil {
				return err
			}
			if hasBindings {
				return NewError(ErrorProfileAlreadyExists, "Codex profile already exists").WithDetail("profile_id", profileID)
			}
		}
		sourceTargets, err := codexBindingTargets(ctx, txStore, sourceProfileID, home)
		if err != nil {
			return WrapError(ErrorStoreStatusFailed, "failed to read source Codex profile targets", err)
		}
		sourceConfigTarget, sourceAuthTarget, err := requireCodexFullProfileTargets(sourceProfileID, sourceTargets)
		if err != nil {
			return err
		}
		sourceConfigSetID, err := codexConfigSetIDFromTarget(sourceConfigTarget)
		if err != nil {
			return err
		}
		sourceConfigSet, err := requireCodexConfigSet(ctx, txStore, sourceConfigSetID)
		if err != nil {
			return err
		}
		configSet = sourceConfigSet
		if configBinding == CodexForkBindingCopyNew {
			configSet, err = copyForkedCodexConfigSet(ctx, txStore, sourceConfigSet, req)
			if err != nil {
				return err
			}
		}
		credentialID, err := codexCredentialIDFromTarget(sourceAuthTarget)
		if err != nil {
			return err
		}
		sourceCredential, err := requireCodexAuthCredential(ctx, txStore, credentialID)
		if err != nil {
			return err
		}
		if credentialBinding == CodexForkBindingCopyNew {
			credentialID, err = newCodexCredentialID(time.Now())
			if err != nil {
				return WrapError(ErrorCodexInvalid, "failed to generate Codex credential id", err)
			}
			if _, err := upsertCodexAuthCredential(ctx, txStore, credentialID, sourceCredential.PayloadJSON); err != nil {
				return err
			}
		}
		profile, err = upsertCodexProfile(ctx, txStore, profileID, fields, hasProfile)
		if err != nil {
			return err
		}
		if _, _, err := createCodexProfileTargets(ctx, txStore, profileID, home, configSet.ID, credentialID); err != nil {
			return err
		}
		metadata, err := codexMaintenanceMetadata("profile-fork", profileID, configSet.ID, credentialID)
		if err != nil {
			return err
		}
		_, err = txStore.CreateAppliedMaintenanceOperation(ctx, store.CreateAppliedMaintenanceOperationParams{
			ID: operationID, ProfileID: profileID, ProviderID: codexconfig.ProviderID, MetadataJSON: metadata,
		})
		return err
	})
	if err != nil {
		return CodexProfileSaveResult{}, wrapCodexMutationTxError("Codex profile fork transaction failed", err)
	}
	return codexProfileSaveResultFromStore(ctx, db, provider, profile, configSet, operationID, home, nil)
}

func UpdateCodexProfileConfigSet(ctx context.Context, req UpdateCodexProfileConfigSetRequest) (CodexProfileDetail, error) {
	profileID, appErr := validateID(req.ProfileID, ErrorProfileInvalid)
	if appErr != nil {
		return CodexProfileDetail{}, appErr
	}
	configSetID, appErr := validateID(req.ConfigSetID, ErrorCodexInvalid)
	if appErr != nil {
		return CodexProfileDetail{}, appErr
	}
	db, lock, operationID, err := openLockedCodexStore(ctx, req.ConfigDir, "profile-set-config")
	if err != nil {
		return CodexProfileDetail{}, err
	}
	defer db.Close()
	defer lock.Release()
	err = db.WithTransaction(ctx, func(txStore *store.Store) error {
		active, exists, err := codexActiveState(ctx, txStore)
		if err != nil {
			return err
		}
		if exists && active.ProfileID == profileID {
			return NewError(ErrorCodexInvalid, "active Codex profile config set cannot be changed").WithDetail("profile_id", profileID)
		}
		if _, err := requireCodexConfigSet(ctx, txStore, configSetID); err != nil {
			return err
		}
		targets, err := storedCodexBindingTargets(ctx, txStore, profileID)
		if err != nil {
			return WrapError(ErrorStoreStatusFailed, "failed to read Codex profile targets", err)
		}
		configTarget, _, err := requireCodexFullProfileTargets(profileID, targets)
		if err != nil {
			return err
		}
		if _, err := txStore.UpsertProfileConfigSetBinding(ctx, store.UpsertProfileConfigSetBindingParams{
			ProfileID: profileID, ProviderID: codexconfig.ProviderID,
			SlotID: codexpreset.ConfigSetSlotUserConfig, ConfigSetID: configSetID,
		}); err != nil {
			return WrapError(ErrorStoreStatusFailed, "failed to update Codex config binding", err)
		}
		_ = configTarget
		metadata, err := codexMaintenanceMetadata("profile-set-config", profileID, configSetID, "")
		if err != nil {
			return err
		}
		_, err = txStore.CreateAppliedMaintenanceOperation(ctx, store.CreateAppliedMaintenanceOperationParams{
			ID: operationID, ProfileID: profileID, ProviderID: codexconfig.ProviderID, MetadataJSON: metadata,
		})
		return err
	})
	if err != nil {
		return CodexProfileDetail{}, wrapCodexMutationTxError("Codex profile config set update failed", err)
	}
	return getCodexProfileFromStore(ctx, db, profileID)
}

func SaveActiveCodexProfileState(ctx context.Context, req SaveActiveCodexProfileStateRequest) (CodexProfileStateSaveResult, error) {
	home, err := resolveCodexMutationHome(req.CodexDir)
	if err != nil {
		return CodexProfileStateSaveResult{}, err
	}
	db, lock, operationID, err := openLockedCodexStore(ctx, req.ConfigDir, "profile-save-current")
	if err != nil {
		return CodexProfileStateSaveResult{}, err
	}
	defer db.Close()
	defer lock.Release()
	payload, err := loadCodexProfilePayload(home, nil, nil)
	if err != nil {
		return CodexProfileStateSaveResult{}, err
	}

	var profileID string
	var credentialID string
	var configSet store.ProviderConfigSet
	err = db.WithTransaction(ctx, func(txStore *store.Store) error {
		_, hasProvider, err := codexPreflightProvider(ctx, txStore, home)
		if err != nil {
			return err
		}
		if !hasProvider {
			return NewError(ErrorProviderNotFound, "Codex provider not found")
		}
		active, exists, err := codexActiveState(ctx, txStore)
		if err != nil {
			return err
		}
		if !exists {
			return NewError(ErrorProfileNotFound, "no active Codex profile")
		}
		profileID = active.ProfileID
		targets, err := codexBindingTargets(ctx, txStore, profileID, home)
		if err != nil {
			return WrapError(ErrorStoreStatusFailed, "failed to read active Codex profile targets", err)
		}
		configTarget, authTarget, err := requireCodexFullProfileTargets(profileID, targets)
		if err != nil {
			return err
		}
		configSetID, err := codexConfigSetIDFromTarget(configTarget)
		if err != nil {
			return err
		}
		configSet, err = requireCodexConfigSet(ctx, txStore, configSetID)
		if err != nil {
			return err
		}
		credentialID, err = codexCredentialIDFromTarget(authTarget)
		if err != nil {
			return err
		}
		credential, err := requireCodexAuthCredential(ctx, txStore, credentialID)
		if err != nil {
			return err
		}
		configSet, err = upsertCodexConfigSet(ctx, txStore, configSet.ID, configSet.Name, configSet.Description, payload.ConfigContent)
		if err != nil {
			return err
		}
		if _, err := txStore.UpsertProviderCredential(ctx, store.UpsertProviderCredentialParams{
			ID: credential.ID, ProviderID: credential.ProviderID, CredentialKind: credential.CredentialKind,
			PayloadJSON: payload.AuthPayload, PayloadSHA256: sha256HexString(payload.AuthPayload), MetadataJSON: credential.MetadataJSON,
		}); err != nil {
			return mapCodexCredentialStoreError(err)
		}
		metadata, err := codexMaintenanceMetadata("profile-save-current", profileID, configSet.ID, credentialID)
		if err != nil {
			return err
		}
		_, err = txStore.CreateAppliedMaintenanceOperation(ctx, store.CreateAppliedMaintenanceOperationParams{
			ID: operationID, ProfileID: profileID, ProviderID: codexconfig.ProviderID, MetadataJSON: metadata,
		})
		return err
	})
	if err != nil {
		return CodexProfileStateSaveResult{}, wrapCodexMutationTxError("Codex active profile state save failed", err)
	}
	credentialReferences, err := codexCredentialBindingCount(ctx, db, credentialID)
	if err != nil {
		return CodexProfileStateSaveResult{}, err
	}
	publicConfigSet, err := codexConfigSetFromStore(ctx, db, configSet, configSet.ID)
	if err != nil {
		return CodexProfileStateSaveResult{}, err
	}
	warnings := []string{}
	if credentialReferences > 1 {
		warnings = append(warnings, "shared Codex login state updated")
	}
	if publicConfigSet.ReferenceCount > 1 {
		warnings = append(warnings, "shared Codex config set updated")
	}
	return CodexProfileStateSaveResult{
		OperationID: operationID, ProfileID: profileID, CredentialID: credentialID,
		CredentialReferenceCount: credentialReferences, ConfigSet: publicConfigSet, Warnings: warnings,
	}, nil
}

func resolveCreatedProfileConfigSet(ctx context.Context, db *store.Store, content string, req CreateCodexProfileRequest) (store.ProviderConfigSet, error) {
	active, activeExists, err := codexActiveState(ctx, db)
	if err != nil {
		return store.ProviderConfigSet{}, err
	}
	newID := strings.TrimSpace(req.NewConfigSetID)
	if !activeExists && newID != "" {
		return store.ProviderConfigSet{}, NewError(ErrorCodexInvalid, "the first Codex profile must create the shared config set")
	}
	if activeExists && newID == "" {
		targets, err := storedCodexBindingTargets(ctx, db, active.ProfileID)
		if err != nil {
			return store.ProviderConfigSet{}, WrapError(ErrorStoreStatusFailed, "failed to read active Codex profile targets", err)
		}
		configTarget, _, err := requireCodexFullProfileTargets(active.ProfileID, targets)
		if err != nil {
			return store.ProviderConfigSet{}, err
		}
		configSetID, err := codexConfigSetIDFromTarget(configTarget)
		if err != nil {
			return store.ProviderConfigSet{}, err
		}
		current, err := requireCodexConfigSet(ctx, db, configSetID)
		if err != nil {
			return store.ProviderConfigSet{}, err
		}
		return upsertCodexConfigSet(ctx, db, current.ID, current.Name, current.Description, content)
	}
	if !activeExists && newID == "" {
		if shared, err := db.GetProviderConfigSet(ctx, codexSharedConfigSetID); err == nil {
			if shared.ProviderID != codexconfig.ProviderID || shared.ConfigKind != codexpreset.ConfigSetKindTOML {
				return store.ProviderConfigSet{}, NewError(ErrorCodexInvalid, "shared Codex config set has unsupported kind")
			}
			return upsertCodexConfigSet(ctx, db, shared.ID, shared.Name, shared.Description, content)
		} else if !errors.Is(err, store.ErrNotFound) {
			return store.ProviderConfigSet{}, mapCodexConfigSetStoreError(err)
		}
		newID = codexSharedConfigSetID
	}
	id, appErr := validateID(newID, ErrorCodexInvalid)
	if appErr != nil {
		return store.ProviderConfigSet{}, appErr
	}
	if _, err := db.GetProviderConfigSet(ctx, id); err == nil {
		return store.ProviderConfigSet{}, NewError(ErrorProfileAlreadyExists, "Codex config set already exists").WithDetail("config_set_id", id)
	} else if !errors.Is(err, store.ErrNotFound) {
		return store.ProviderConfigSet{}, mapCodexConfigSetStoreError(err)
	}
	name := id
	if id == codexSharedConfigSetID {
		name = codexSharedConfigSetName
	}
	if req.NewConfigSetName != nil {
		name, appErr = validateName(*req.NewConfigSetName, ErrorCodexInvalid)
		if appErr != nil {
			return store.ProviderConfigSet{}, appErr
		}
	}
	description := ""
	if req.NewConfigSetDescription != nil {
		description, appErr = validateDescription(*req.NewConfigSetDescription, ErrorCodexInvalid)
		if appErr != nil {
			return store.ProviderConfigSet{}, appErr
		}
	}
	return upsertCodexConfigSet(ctx, db, id, name, description, content)
}

func copyForkedCodexConfigSet(ctx context.Context, db *store.Store, source store.ProviderConfigSet, req ForkCodexProfileRequest) (store.ProviderConfigSet, error) {
	id, appErr := validateID(req.NewConfigSetID, ErrorCodexInvalid)
	if appErr != nil {
		return store.ProviderConfigSet{}, appErr
	}
	if _, err := db.GetProviderConfigSet(ctx, id); err == nil {
		return store.ProviderConfigSet{}, NewError(ErrorProfileAlreadyExists, "Codex config set already exists").WithDetail("config_set_id", id)
	} else if !errors.Is(err, store.ErrNotFound) {
		return store.ProviderConfigSet{}, mapCodexConfigSetStoreError(err)
	}
	name := id
	if req.NewConfigSetName != nil {
		name, appErr = validateName(*req.NewConfigSetName, ErrorCodexInvalid)
		if appErr != nil {
			return store.ProviderConfigSet{}, appErr
		}
	}
	description := source.Description
	if req.NewConfigSetDescription != nil {
		description, appErr = validateDescription(*req.NewConfigSetDescription, ErrorCodexInvalid)
		if appErr != nil {
			return store.ProviderConfigSet{}, appErr
		}
	}
	return upsertCodexConfigSet(ctx, db, id, name, description, source.PayloadText)
}

func createCodexProfileTargets(ctx context.Context, db *store.Store, profileID string, home codexconfig.Home, configSetID, credentialID string) (store.ProfileTarget, store.ProfileTarget, error) {
	targets, err := codexPreflightTargets(ctx, db, home, profileID)
	if err != nil {
		return store.ProfileTarget{}, store.ProfileTarget{}, err
	}
	if targets.HasConfig || targets.HasAuth {
		return store.ProfileTarget{}, store.ProfileTarget{}, NewError(ErrorProfileAlreadyExists, "Codex profile targets already exist").WithDetail("profile_id", profileID)
	}
	configValue, err := codexpreset.ConfigSetBindingValueJSON(configSetID)
	if err != nil {
		return store.ProfileTarget{}, store.ProfileTarget{}, err
	}
	authValue, err := codexpreset.CredentialBindingValueJSON(credentialID)
	if err != nil {
		return store.ProfileTarget{}, store.ProfileTarget{}, err
	}
	configMetadata, err := codexpreset.TargetMetadataJSON(codexconfig.TargetID, codexpreset.TargetModeConfigSet)
	if err != nil {
		return store.ProfileTarget{}, store.ProfileTarget{}, err
	}
	authMetadata, err := codexpreset.TargetMetadataJSON(codexconfig.AuthTargetID, codexpreset.TargetModeCredential)
	if err != nil {
		return store.ProfileTarget{}, store.ProfileTarget{}, err
	}
	configTarget, err := upsertCodexConfigTarget(ctx, db, profileID, home, configValue, configMetadata, false)
	if err != nil {
		return store.ProfileTarget{}, store.ProfileTarget{}, err
	}
	authTarget, err := upsertCodexAuthTarget(ctx, db, profileID, home, authValue, authMetadata, false)
	if err != nil {
		return store.ProfileTarget{}, store.ProfileTarget{}, err
	}
	return configTarget, authTarget, nil
}

func requireCodexFullProfileTargets(profileID string, targets []store.ProfileTarget) (store.ProfileTarget, store.ProfileTarget, error) {
	var configTarget store.ProfileTarget
	var authTarget store.ProfileTarget
	for _, target := range targets {
		switch target.TargetID {
		case codexconfig.TargetID:
			if _, err := codexConfigSetIDFromTarget(target); err != nil {
				return store.ProfileTarget{}, store.ProfileTarget{}, err
			}
			configTarget = target
		case codexconfig.AuthTargetID:
			if _, err := codexCredentialIDFromTarget(target); err != nil {
				return store.ProfileTarget{}, store.ProfileTarget{}, err
			}
			authTarget = target
		}
	}
	if configTarget.TargetID == "" || authTarget.TargetID == "" {
		return store.ProfileTarget{}, store.ProfileTarget{}, NewError(ErrorCodexInvalid, "Codex profile is not a valid full profile").WithDetail("profile_id", profileID)
	}
	return configTarget, authTarget, nil
}

func loadCodexProfilePayload(home codexconfig.Home, configContent, authContent *string) (codexProfilePayload, error) {
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
		return "", NewError(ErrorCodexInvalid, "Codex config TOML is invalid").WithDetail("config_path", home.ConfigPath)
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

func normalizeCodexForkBinding(raw, kind string) (string, *AppError) {
	value := strings.TrimSpace(raw)
	if value == CodexForkBindingShareParent || value == CodexForkBindingCopyNew {
		return value, nil
	}
	return "", NewError(ErrorCodexInvalid, "unsupported Codex fork "+kind+" binding").
		WithDetail("binding", raw).
		WithDetail("supported", []string{CodexForkBindingShareParent, CodexForkBindingCopyNew})
}

func openLockedCodexStore(ctx context.Context, configDir, operation string) (*store.Store, targetfs.Lock, string, error) {
	_, paths, err := resolveRuntime(configDir)
	if err != nil {
		return nil, targetfs.Lock{}, "", err
	}
	db, err := openHealthyStore(ctx, configDir, false)
	if err != nil {
		return nil, targetfs.Lock{}, "", err
	}
	operationID, err := newCodexMaintenanceOperationID(operation, time.Now())
	if err != nil {
		_ = db.Close()
		return nil, targetfs.Lock{}, "", WrapError(ErrorOperationCreateFailed, "failed to create Codex maintenance operation id", err)
	}
	lock, err := acquireSwitchLock(paths.Lock, operationID)
	if err != nil {
		_ = db.Close()
		return nil, targetfs.Lock{}, "", err
	}
	return db, lock, operationID, nil
}

func newCodexMaintenanceOperationID(operation string, now time.Time) (string, error) {
	randomBytes := make([]byte, codexMaintenanceRandomBytes)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", err
	}
	return fmt.Sprintf("codex-%s-%d-%s", operation, now.UnixMilli(), hex.EncodeToString(randomBytes)), nil
}

func codexMaintenanceMetadata(action, profileID, configSetID, credentialID string) (string, error) {
	metadata := map[string]any{
		"action":      action,
		"provider_id": codexconfig.ProviderID,
		"profile_id":  profileID,
	}
	if configSetID != "" {
		metadata["config_set_id"] = configSetID
	}
	if credentialID != "" {
		metadata["credential_id"] = credentialID
	}
	raw, err := json.Marshal(metadata)
	return string(raw), err
}

func codexProfileSaveResultFromStore(ctx context.Context, db *store.Store, provider store.Provider, profile store.Profile, configSet store.ProviderConfigSet, operationID string, home codexconfig.Home, warnings []string) (CodexProfileSaveResult, error) {
	publicProvider, err := providerFromStore(provider)
	if err != nil {
		return CodexProfileSaveResult{}, err
	}
	publicProfile, err := profileFromStore(profile)
	if err != nil {
		return CodexProfileSaveResult{}, err
	}
	detail, err := getCodexProfileFromStore(ctx, db, profile.ID)
	if err != nil {
		return CodexProfileSaveResult{}, err
	}
	activeID, err := activeCodexConfigSetID(ctx, db)
	if err != nil {
		return CodexProfileSaveResult{}, err
	}
	publicConfigSet, err := codexConfigSetFromStore(ctx, db, configSet, activeID)
	if err != nil {
		return CodexProfileSaveResult{}, err
	}
	return CodexProfileSaveResult{
		OperationID: operationID, Provider: publicProvider, Profile: publicProfile, Summary: detail.Summary,
		ConfigSet: publicConfigSet, CodexDir: home.Dir, ConfigPath: home.ConfigPath, AuthPath: home.AuthPath, Warnings: warnings,
	}, nil
}

func codexConfigSummaryFromContent(content string) (model, provider, baseURL string) {
	return parseCodexConfigSummary(content)
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
