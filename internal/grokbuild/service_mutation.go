package grokbuild

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/strahe/profiledeck/internal/apperror"
	grokauth "github.com/strahe/profiledeck/internal/grokbuild/auth"
	grokconfig "github.com/strahe/profiledeck/internal/grokbuild/config"
	grokpreset "github.com/strahe/profiledeck/internal/grokbuild/preset"
	grokprofile "github.com/strahe/profiledeck/internal/grokbuild/profile"
	"github.com/strahe/profiledeck/internal/maintenance"
	"github.com/strahe/profiledeck/internal/profile"
	"github.com/strahe/profiledeck/internal/profiletarget"
	"github.com/strahe/profiledeck/internal/provider"
	"github.com/strahe/profiledeck/internal/providercoord"
	"github.com/strahe/profiledeck/internal/store"
	switchtarget "github.com/strahe/profiledeck/internal/switching/target"
)

const (
	ForkBindingShareParent = "share-parent"
	ForkBindingCopyNew     = "copy-new"
	sharedConfigSetID      = "shared"
	sharedConfigSetName    = "Shared"
)

type CreateProfileRequest struct {
	ProfileID               string  `json:"profile_id"`
	Name                    *string `json:"name,omitempty"`
	Description             *string `json:"description,omitempty"`
	NewConfigSetID          string  `json:"new_config_set_id,omitempty"`
	NewConfigSetName        *string `json:"new_config_set_name,omitempty"`
	NewConfigSetDescription *string `json:"new_config_set_description,omitempty"`
}

type ForkProfileRequest struct {
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

type UpdateProfileConfigSetRequest struct {
	ProfileID   string `json:"profile_id"`
	ConfigSetID string `json:"config_set_id"`
}

type ProfileSaveResult struct {
	OperationID string            `json:"operation_id"`
	Provider    provider.Provider `json:"provider"`
	Profile     profile.Profile   `json:"profile"`
	Summary     ProfileSummary    `json:"summary"`
	ConfigSet   ConfigSet         `json:"config_set"`
	GrokHome    string            `json:"grok_home"`
	ConfigPath  string            `json:"config_path"`
	AuthPath    string            `json:"auth_path"`
	Warnings    []string          `json:"warnings,omitempty"`
}

type ProfileStateSaveResult struct {
	OperationID              string    `json:"operation_id"`
	ProfileID                string    `json:"profile_id"`
	CredentialID             string    `json:"credential_id"`
	CredentialReferenceCount int       `json:"credential_reference_count"`
	ConfigSet                ConfigSet `json:"config_set"`
	Warnings                 []string  `json:"warnings,omitempty"`
}

type managedProfileFields struct {
	CreateName        string
	CreateDescription string
	UpdateName        *string
	UpdateDescription *string
}

type workingCopy struct {
	AuthPayload   string
	ConfigContent string
	ConfigMissing bool
}

func (service *Service) CreateProfile(ctx context.Context, req CreateProfileRequest) (ProfileSaveResult, error) {
	if err := service.requireAccess(ctx); err != nil {
		return ProfileSaveResult{}, err
	}
	profileID, appErr := validateID(req.ProfileID, apperror.ProfileInvalid)
	if appErr != nil {
		return ProfileSaveResult{}, appErr
	}
	fields, appErr := normalizeProfileFields(profileID, req.Name, req.Description)
	if appErr != nil {
		return ProfileSaveResult{}, appErr
	}
	home, err := service.resolveExistingHome()
	if err != nil {
		return ProfileSaveResult{}, err
	}
	var storedProvider store.Provider
	var storedProfile store.Profile
	var storedConfigSet store.ProviderConfigSet
	var operationID string
	var warnings []string
	err = service.maintenance.RunMaintenance(ctx, maintenance.Request{
		Operation: "grok-build-profile-create", ProviderID: grokconfig.ProviderID,
		Record:              false,
		CoordinationTargets: coordinationTargets(home),
	}, func(ctx context.Context, tx *store.Store, currentOperationID string) error {
		operationID = currentOperationID
		_, providerExists, err := preflightProvider(ctx, tx, home)
		if err != nil {
			return err
		}
		_, profileExists, err := preflightProfile(ctx, tx, profileID)
		if err != nil {
			return err
		}
		if profileExists {
			hasBindings, err := profileHasBindings(ctx, tx, profileID)
			if err != nil {
				return err
			}
			if hasBindings {
				return apperror.New(apperror.ProfileAlreadyExists, "Grok Build Profile already exists")
			}
		}
		authSnapshot, err := readAuthSnapshot(home)
		if err != nil {
			return err
		}
		if err := ensureManagedPathOwnership(ctx, tx, home); err != nil {
			return err
		}
		metadataJSON, err := grokpreset.ProviderMetadataJSON(home)
		if err != nil {
			return err
		}
		storedProvider, err = grokprofile.UpsertProvider(ctx, tx, metadataJSON, providerExists)
		if err != nil {
			return err
		}
		var configSnapshot *grokconfig.Snapshot
		storedConfigSet, configSnapshot, err = resolveCreatedConfigSet(ctx, tx, home, req)
		if err != nil {
			return err
		}
		if grokconfig.HasAuthOverride(storedConfigSet.PayloadText) {
			warnings = append(warnings, grokconfig.AuthOverrideWarning)
		}
		credentialID, err := grokprofile.NewCredentialID(time.Now())
		if err != nil {
			return apperror.Wrap(apperror.GrokBuildInvalid, "failed to create Grok Build login id", err)
		}
		if _, err := grokprofile.UpsertAuthCredential(ctx, tx, credentialID, authSnapshot.Payload); err != nil {
			return err
		}
		storedProfile, err = grokprofile.UpsertProfile(ctx, tx, profileID, grokprofile.ProfileFields{
			CreateName: fields.CreateName, CreateDescription: fields.CreateDescription,
			UpdateName: fields.UpdateName, UpdateDescription: fields.UpdateDescription,
		}, profileExists)
		if err != nil {
			return err
		}
		if _, err := grokprofile.UpsertConfigBinding(ctx, tx, profileID, home, storedConfigSet.ID); err != nil {
			return err
		}
		if _, err := grokprofile.UpsertAuthBinding(ctx, tx, profileID, home, credentialID); err != nil {
			return err
		}
		if _, err := tx.CreateAppliedMaintenanceOperation(ctx, store.CreateAppliedMaintenanceOperationParams{
			ID: operationID, ProviderID: grokconfig.ProviderID,
			RelatedProfileIDs: []string{profileID}, ActiveProfileID: profileID,
			MetadataSchemaVersion: store.OperationMetadataSchemaVersion,
			MetadataJSON:          maintenanceMetadata("profile-create"),
		}); err != nil {
			return apperror.Wrap(apperror.OperationCreateFailed, "failed to record Grok Build Profile creation", err)
		}
		if err := validateAuthSnapshot(home, authSnapshot); err != nil {
			return err
		}
		if configSnapshot != nil {
			if err := validateConfigSnapshot(home, *configSnapshot); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return ProfileSaveResult{}, err
	}
	return service.profileSaveResult(ctx, storedProvider, storedProfile, storedConfigSet, operationID, home, warnings)
}

func (service *Service) ForkProfile(ctx context.Context, req ForkProfileRequest) (ProfileSaveResult, error) {
	if err := service.requireAccess(ctx); err != nil {
		return ProfileSaveResult{}, err
	}
	sourceID, appErr := validateID(req.SourceProfileID, apperror.ProfileInvalid)
	if appErr != nil {
		return ProfileSaveResult{}, appErr
	}
	profileID, appErr := validateID(req.ProfileID, apperror.ProfileInvalid)
	if appErr != nil {
		return ProfileSaveResult{}, appErr
	}
	if sourceID == profileID {
		return ProfileSaveResult{}, apperror.New(apperror.ProfileInvalid, "source and destination Profile ids must differ")
	}
	credentialBinding, appErr := normalizeForkBinding(req.CredentialBinding, "login")
	if appErr != nil {
		return ProfileSaveResult{}, appErr
	}
	configBinding, appErr := normalizeForkBinding(req.ConfigBinding, "Config Set")
	if appErr != nil {
		return ProfileSaveResult{}, appErr
	}
	if credentialBinding != ForkBindingCopyNew && configBinding != ForkBindingCopyNew {
		return ProfileSaveResult{}, apperror.New(apperror.GrokBuildInvalid, "fork must copy at least one Grok Build resource")
	}
	fields, appErr := normalizeProfileFields(profileID, req.Name, req.Description)
	if appErr != nil {
		return ProfileSaveResult{}, appErr
	}
	home, err := service.resolveHome()
	if err != nil {
		return ProfileSaveResult{}, err
	}
	var storedProvider store.Provider
	var storedProfile store.Profile
	var storedConfigSet store.ProviderConfigSet
	var operationID string
	err = service.maintenance.RunMaintenance(ctx, maintenance.Request{
		Operation: "grok-build-profile-fork", ProviderID: grokconfig.ProviderID,
		ProfileID: profileID, Record: true, MetadataJSON: maintenanceMetadata("profile-fork"),
	}, func(ctx context.Context, tx *store.Store, currentOperationID string) error {
		operationID = currentOperationID
		var err error
		storedProvider, _, err = preflightProvider(ctx, tx, home)
		if err != nil {
			return err
		}
		if storedProvider.ID == "" {
			return apperror.New(apperror.ProviderNotFound, "Grok Build Provider was not found")
		}
		_, profileExists, err := preflightProfile(ctx, tx, profileID)
		if err != nil {
			return err
		}
		if profileExists {
			hasBindings, err := profileHasBindings(ctx, tx, profileID)
			if err != nil {
				return err
			}
			if hasBindings {
				return apperror.New(apperror.ProfileAlreadyExists, "Grok Build Profile already exists")
			}
		}
		sourceTargets, err := grokprofile.StoredBindingTargets(ctx, tx, sourceID)
		if err != nil {
			return err
		}
		configTarget, authTarget, err := grokprofile.FullProfileTargets(sourceID, sourceTargets)
		if err != nil {
			return err
		}
		configSetID, err := grokprofile.ConfigSetIDFromTarget(configTarget)
		if err != nil {
			return err
		}
		sourceConfigSet, err := grokprofile.RequireConfigSet(ctx, tx, configSetID)
		if err != nil {
			return err
		}
		storedConfigSet = sourceConfigSet
		if configBinding == ForkBindingCopyNew {
			storedConfigSet, err = copyForkConfigSet(ctx, tx, sourceConfigSet, req)
			if err != nil {
				return err
			}
		}
		credentialID, err := grokprofile.CredentialIDFromTarget(authTarget)
		if err != nil {
			return err
		}
		sourceCredential, err := grokprofile.RequireAuthCredential(ctx, tx, credentialID)
		if err != nil {
			return err
		}
		if credentialBinding == ForkBindingCopyNew {
			credentialID, err = grokprofile.NewCredentialID(time.Now())
			if err != nil {
				return err
			}
			if _, err := grokprofile.UpsertAuthCredential(ctx, tx, credentialID, sourceCredential.PayloadJSON); err != nil {
				return err
			}
		}
		storedProfile, err = grokprofile.UpsertProfile(ctx, tx, profileID, grokprofile.ProfileFields{
			CreateName: fields.CreateName, CreateDescription: fields.CreateDescription,
			UpdateName: fields.UpdateName, UpdateDescription: fields.UpdateDescription,
		}, profileExists)
		if err != nil {
			return err
		}
		if _, err := grokprofile.UpsertConfigBinding(ctx, tx, profileID, home, storedConfigSet.ID); err != nil {
			return err
		}
		_, err = grokprofile.UpsertAuthBinding(ctx, tx, profileID, home, credentialID)
		return err
	})
	if err != nil {
		return ProfileSaveResult{}, err
	}
	return service.profileSaveResult(ctx, storedProvider, storedProfile, storedConfigSet, operationID, home, nil)
}

func (service *Service) UpdateProfileConfigSet(ctx context.Context, req UpdateProfileConfigSetRequest) (ProfileDetail, error) {
	if err := service.requireAccess(ctx); err != nil {
		return ProfileDetail{}, err
	}
	profileID, appErr := validateID(req.ProfileID, apperror.ProfileInvalid)
	if appErr != nil {
		return ProfileDetail{}, appErr
	}
	configSetID, appErr := validateID(req.ConfigSetID, apperror.GrokBuildInvalid)
	if appErr != nil {
		return ProfileDetail{}, appErr
	}
	err := service.maintenance.RunMaintenance(ctx, maintenance.Request{
		Operation: "grok-build-profile-set-config", ProviderID: grokconfig.ProviderID,
		ProfileID: profileID, Record: true, MetadataJSON: maintenanceMetadata("profile-set-config"),
	}, func(ctx context.Context, tx *store.Store, _ string) error {
		active, exists, err := activeState(ctx, tx)
		if err != nil {
			return err
		}
		if exists && active.ProfileID == profileID {
			return apperror.New(apperror.GrokBuildInvalid, "active Grok Build Profile Config Set cannot be changed")
		}
		if _, err := grokprofile.RequireConfigSet(ctx, tx, configSetID); err != nil {
			return err
		}
		home, err := grokprofile.StoredHome(ctx, tx)
		if err != nil {
			return err
		}
		targets, err := grokprofile.BindingTargets(ctx, tx, profileID, home)
		if err != nil {
			return err
		}
		if _, _, err := grokprofile.FullProfileTargets(profileID, targets); err != nil {
			return err
		}
		_, err = grokprofile.UpsertConfigBinding(ctx, tx, profileID, home, configSetID)
		return err
	})
	if err != nil {
		return ProfileDetail{}, err
	}
	db, err := service.openStore(ctx, true)
	if err != nil {
		return ProfileDetail{}, err
	}
	defer db.Close()
	return getProfileFromStore(ctx, db, profileID)
}

func (service *Service) SaveActiveProfileState(ctx context.Context) (ProfileStateSaveResult, error) {
	return service.saveActiveProfileState(ctx, "", 0, 0)
}

func (service *Service) SaveActiveProfileStateFor(ctx context.Context, expectedProfileID string, expectedCredentialReferences, expectedConfigReferences int) (ProfileStateSaveResult, error) {
	return service.saveActiveProfileState(ctx, expectedProfileID, expectedCredentialReferences, expectedConfigReferences)
}

func (service *Service) saveActiveProfileState(ctx context.Context, expectedProfileID string, expectedCredentialReferences, expectedConfigReferences int) (ProfileStateSaveResult, error) {
	if err := service.requireAccess(ctx); err != nil {
		return ProfileStateSaveResult{}, err
	}
	home, err := service.resolveExistingHome()
	if err != nil {
		return ProfileStateSaveResult{}, err
	}
	var profileID string
	var credentialID string
	var configSet store.ProviderConfigSet
	var operationID string
	err = service.maintenance.RunMaintenance(ctx, maintenance.Request{
		Operation: "grok-build-profile-save-current", ProviderID: grokconfig.ProviderID,
		Record:              false,
		CoordinationTargets: coordinationTargets(home),
	}, func(ctx context.Context, tx *store.Store, currentOperationID string) error {
		operationID = currentOperationID
		if _, exists, err := preflightProvider(ctx, tx, home); err != nil {
			return err
		} else if !exists {
			return apperror.New(apperror.ProviderNotFound, "Grok Build Provider was not found")
		}
		working, err := readWorkingCopy(home)
		if err != nil {
			return err
		}
		active, exists, err := activeState(ctx, tx)
		if err != nil {
			return err
		}
		if !exists {
			if expectedProfileID != "" {
				return apperror.New(apperror.ProfileChanged, "active Grok Build Profile changed")
			}
			return apperror.New(apperror.ProfileNotFound, "no active Grok Build Profile")
		}
		if expectedProfileID != "" && active.ProfileID != expectedProfileID {
			return apperror.New(apperror.ProfileChanged, "active Grok Build Profile changed")
		}
		profileID = active.ProfileID
		targets, err := grokprofile.BindingTargets(ctx, tx, profileID, home)
		if err != nil {
			return err
		}
		configTarget, authTarget, err := grokprofile.FullProfileTargets(profileID, targets)
		if err != nil {
			return err
		}
		configSetID, err := grokprofile.ConfigSetIDFromTarget(configTarget)
		if err != nil {
			return err
		}
		configSet, err = grokprofile.RequireConfigSet(ctx, tx, configSetID)
		if err != nil {
			return err
		}
		credentialID, err = grokprofile.CredentialIDFromTarget(authTarget)
		if err != nil {
			return err
		}
		credential, err := grokprofile.RequireAuthCredential(ctx, tx, credentialID)
		if err != nil {
			return err
		}
		if expectedProfileID != "" {
			credentialReferences, err := grokprofile.CredentialBindingCount(ctx, tx, credentialID)
			if err != nil {
				return err
			}
			configReferences, err := grokprofile.ConfigSetBindingCount(ctx, tx, configSet.ID)
			if err != nil {
				return err
			}
			if credentialReferences != expectedCredentialReferences || configReferences != expectedConfigReferences {
				return apperror.New(apperror.ProfileSharingChanged, "Grok Build Profile sharing changed before save")
			}
		}
		// A missing working file must never erase the database-owned Config Set
		// or allow the login to be updated without its settings.
		if working.ConfigMissing {
			return apperror.New(
				apperror.GrokBuildInvalid,
				"Grok Build config.toml is required to update the current Profile",
			)
		}
		configSet, err = grokprofile.UpsertConfigSet(
			ctx, tx, configSet.ID, configSet.Name, configSet.Description, working.ConfigContent,
		)
		if err != nil {
			return err
		}
		if _, err := tx.UpsertProviderCredential(ctx, store.UpsertProviderCredentialParams{
			ID: credential.ID, ProviderID: credential.ProviderID, CredentialKind: credential.CredentialKind,
			PayloadJSON: working.AuthPayload, PayloadSHA256: switchtarget.SHA256String(working.AuthPayload),
			MetadataJSON: credential.MetadataJSON,
		}); err != nil {
			return err
		}
		relatedProfileIDs, err := tx.ListProviderResourceProfileIDs(
			ctx,
			grokconfig.ProviderID,
			[]string{credentialID},
			[]string{configSet.ID},
		)
		if err != nil {
			return apperror.Wrap(
				apperror.StoreStatusFailed,
				"failed to resolve Profiles affected by saved Grok Build state",
				err,
			)
		}
		if _, err := tx.CreateAppliedMaintenanceOperation(ctx, store.CreateAppliedMaintenanceOperationParams{
			ID: operationID, ProviderID: grokconfig.ProviderID,
			RelatedProfileIDs:     relatedProfileIDs,
			MetadataSchemaVersion: store.OperationMetadataSchemaVersion,
			MetadataJSON:          maintenanceMetadata("profile-save-current"),
		}); err != nil {
			return apperror.Wrap(apperror.OperationCreateFailed, "failed to record Grok Build state save", err)
		}
		if err := validateWorkingCopy(home, working); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return ProfileStateSaveResult{}, err
	}
	db, err := service.openStore(ctx, true)
	if err != nil {
		return ProfileStateSaveResult{}, err
	}
	defer db.Close()
	credentialReferences, err := grokprofile.CredentialBindingCount(ctx, db, credentialID)
	if err != nil {
		return ProfileStateSaveResult{}, err
	}
	activeID, err := activeConfigSetID(ctx, db)
	if err != nil {
		return ProfileStateSaveResult{}, err
	}
	publicSet, err := configSetFromStore(ctx, db, configSet, activeID)
	if err != nil {
		return ProfileStateSaveResult{}, err
	}
	warnings := []string{}
	if credentialReferences > 1 {
		warnings = append(warnings, "shared Grok Build login updated")
	}
	if publicSet.ReferenceCount > 1 {
		warnings = append(warnings, "shared Grok Build Config Set updated")
	}
	if grokconfig.HasAuthOverride(configSet.PayloadText) {
		warnings = append(warnings, grokconfig.AuthOverrideWarning)
	}
	return ProfileStateSaveResult{
		OperationID: operationID, ProfileID: profileID, CredentialID: credentialID,
		CredentialReferenceCount: credentialReferences, ConfigSet: publicSet, Warnings: warnings,
	}, nil
}

func readWorkingCopy(home grokconfig.Home) (workingCopy, error) {
	authSnapshot, err := readAuthSnapshot(home)
	if err != nil {
		return workingCopy{}, err
	}
	configSnapshot, err := readConfigSnapshot(home)
	if err != nil {
		return workingCopy{}, err
	}
	return workingCopy{
		AuthPayload:   authSnapshot.Payload,
		ConfigContent: configSnapshot.Content,
		ConfigMissing: configSnapshot.Missing,
	}, nil
}

func readAuthSnapshot(home grokconfig.Home) (grokauth.Snapshot, error) {
	authSnapshot, err := grokauth.ReadSnapshot(home.AuthPath)
	if errors.Is(err, os.ErrNotExist) {
		return grokauth.Snapshot{}, apperror.New(apperror.GrokBuildInvalid, "Grok Build auth.json is required")
	}
	if err != nil {
		return grokauth.Snapshot{}, authReadError(err)
	}
	return authSnapshot, nil
}

func validateAuthSnapshot(home grokconfig.Home, expected grokauth.Snapshot) error {
	current, err := readAuthSnapshot(home)
	if err != nil || current != expected {
		return apperror.New(apperror.TargetChanged, "Grok Build auth.json changed before it could be saved")
	}
	return nil
}

func validateWorkingCopy(home grokconfig.Home, expected workingCopy) error {
	current, err := readWorkingCopy(home)
	if err != nil {
		return apperror.New(apperror.TargetChanged, "Grok Build working copy changed before it could be saved")
	}
	if current.AuthPayload != expected.AuthPayload ||
		current.ConfigContent != expected.ConfigContent ||
		current.ConfigMissing != expected.ConfigMissing {
		return apperror.New(apperror.TargetChanged, "Grok Build working copy changed before it could be saved")
	}
	return nil
}

func readConfigSnapshot(home grokconfig.Home) (grokconfig.Snapshot, error) {
	snapshot, err := grokconfig.ReadSnapshot(home.ConfigPath)
	if err == nil {
		return snapshot, nil
	}
	// Parser causes can contain source snippets, so do not retain the raw cause.
	return grokconfig.Snapshot{}, apperror.New(apperror.GrokBuildInvalid, "Grok Build config.toml is invalid or unreadable")
}

func validateConfigSnapshot(home grokconfig.Home, expected grokconfig.Snapshot) error {
	current, err := readConfigSnapshot(home)
	if err != nil || current != expected {
		return apperror.New(apperror.TargetChanged, "Grok Build config.toml changed before it could be saved")
	}
	return nil
}

func authReadError(err error) error {
	var sizeErr grokauth.SizeError
	if errors.As(err, &sizeErr) {
		return apperror.New(apperror.GrokBuildInvalid, "Grok Build auth.json is too large")
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return apperror.New(apperror.GrokBuildInvalid, "Grok Build auth.json could not be read")
	}
	return apperror.New(apperror.GrokBuildInvalid, "Grok Build auth.json is invalid")
}

func preflightProvider(
	ctx context.Context,
	db *store.Store,
	home grokconfig.Home,
) (store.Provider, bool, error) {
	stored, err := db.GetProvider(ctx, grokconfig.ProviderID)
	if errors.Is(err, store.ErrNotFound) {
		return store.Provider{}, false, nil
	}
	if err != nil {
		return store.Provider{}, false, err
	}
	if stored.AdapterID != grokconfig.AdapterID {
		return store.Provider{}, false, apperror.New(apperror.GrokBuildInvalid, "stored Grok Build Provider uses another adapter")
	}
	metadata, err := grokpreset.DecodeProviderMetadata(stored.MetadataJSON)
	if err != nil || !metadata.Compatible() {
		return store.Provider{}, false, apperror.New(apperror.GrokBuildInvalid, "stored Grok Build Provider metadata is invalid")
	}
	if metadata.GrokHome != home.Dir ||
		metadata.ConfigPath != home.ConfigPath ||
		metadata.AuthPath != home.AuthPath {
		return store.Provider{}, false, apperror.New(
			apperror.GrokBuildInvalid,
			"stored Grok Build Home does not match the requested Home",
		)
	}
	return stored, true, nil
}

func preflightProfile(ctx context.Context, db *store.Store, id string) (store.Profile, bool, error) {
	value, err := db.GetProfile(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return store.Profile{}, false, nil
	}
	return value, err == nil, err
}

func profileHasBindings(ctx context.Context, db *store.Store, id string) (bool, error) {
	credentials, err := db.ListProfileCredentialBindings(ctx, id, grokconfig.ProviderID)
	if err != nil {
		return false, err
	}
	configSets, err := db.ListProfileConfigSetBindings(ctx, id, grokconfig.ProviderID)
	if err != nil {
		return false, err
	}
	return len(credentials) != 0 || len(configSets) != 0, nil
}

func ensureManagedPathOwnership(ctx context.Context, db *store.Store, home grokconfig.Home) error {
	if appErr := profiletarget.EnsurePathOwnership(
		ctx, db, home.AuthPath, profiletarget.PathOwnershipKey(home.AuthPath),
		grokconfig.ProviderID, grokconfig.AuthTargetID, nil,
	); appErr != nil {
		return appErr
	}
	if appErr := profiletarget.EnsurePathOwnership(
		ctx, db, home.ConfigPath, profiletarget.PathOwnershipKey(home.ConfigPath),
		grokconfig.ProviderID, grokconfig.ConfigTargetID, nil,
	); appErr != nil {
		return appErr
	}
	return nil
}

func resolveCreatedConfigSet(
	ctx context.Context,
	db *store.Store,
	home grokconfig.Home,
	req CreateProfileRequest,
) (store.ProviderConfigSet, *grokconfig.Snapshot, error) {
	active, activeExists, err := activeState(ctx, db)
	if err != nil {
		return store.ProviderConfigSet{}, nil, err
	}
	newID := strings.TrimSpace(req.NewConfigSetID)
	if !activeExists && newID != "" {
		return store.ProviderConfigSet{}, nil, apperror.New(
			apperror.GrokBuildInvalid,
			"the first Grok Build Profile must use the shared Config Set",
		)
	}
	if activeExists && newID == "" {
		targets, err := grokprofile.StoredBindingTargets(ctx, db, active.ProfileID)
		if err != nil {
			return store.ProviderConfigSet{}, nil, err
		}
		configTarget, _, err := grokprofile.FullProfileTargets(active.ProfileID, targets)
		if err != nil {
			return store.ProviderConfigSet{}, nil, err
		}
		configSetID, err := grokprofile.ConfigSetIDFromTarget(configTarget)
		if err != nil {
			return store.ProviderConfigSet{}, nil, err
		}
		current, err := grokprofile.RequireConfigSet(ctx, db, configSetID)
		if err != nil {
			return store.ProviderConfigSet{}, nil, err
		}
		// Reuse changes only the new Profile binding; the saved shared payload
		// remains owned by explicit capture and save-current operations.
		return current, nil, nil
	}
	if !activeExists && newID == "" {
		if _, err := db.GetProviderConfigSet(ctx, grokconfig.ProviderID, sharedConfigSetID); err == nil {
			shared, err := grokprofile.RequireConfigSet(ctx, db, sharedConfigSetID)
			return shared, nil, err
		} else if !errors.Is(err, store.ErrNotFound) {
			return store.ProviderConfigSet{}, nil, err
		}
		newID = sharedConfigSetID
	}
	id, appErr := validateID(newID, apperror.GrokBuildInvalid)
	if appErr != nil {
		return store.ProviderConfigSet{}, nil, appErr
	}
	if _, err := db.GetProviderConfigSet(ctx, grokconfig.ProviderID, id); err == nil {
		return store.ProviderConfigSet{}, nil, apperror.New(apperror.ProfileAlreadyExists, "Grok Build Config Set already exists")
	} else if !errors.Is(err, store.ErrNotFound) {
		return store.ProviderConfigSet{}, nil, err
	}
	name := id
	if id == sharedConfigSetID {
		name = sharedConfigSetName
	}
	if req.NewConfigSetName != nil {
		name, appErr = validateName(*req.NewConfigSetName, apperror.GrokBuildInvalid)
		if appErr != nil {
			return store.ProviderConfigSet{}, nil, appErr
		}
	}
	description := ""
	if req.NewConfigSetDescription != nil {
		description, appErr = validateDescription(*req.NewConfigSetDescription, apperror.GrokBuildInvalid)
		if appErr != nil {
			return store.ProviderConfigSet{}, nil, appErr
		}
	}
	snapshot, err := readConfigSnapshot(home)
	if err != nil {
		return store.ProviderConfigSet{}, nil, err
	}
	stored, err := grokprofile.UpsertConfigSet(ctx, db, id, name, description, snapshot.Content)
	if err != nil {
		return store.ProviderConfigSet{}, nil, err
	}
	return stored, &snapshot, nil
}

func copyForkConfigSet(
	ctx context.Context,
	db *store.Store,
	source store.ProviderConfigSet,
	req ForkProfileRequest,
) (store.ProviderConfigSet, error) {
	id, appErr := validateID(req.NewConfigSetID, apperror.GrokBuildInvalid)
	if appErr != nil {
		return store.ProviderConfigSet{}, appErr
	}
	if _, err := db.GetProviderConfigSet(ctx, grokconfig.ProviderID, id); err == nil {
		return store.ProviderConfigSet{}, apperror.New(apperror.ProfileAlreadyExists, "Grok Build Config Set already exists")
	} else if !errors.Is(err, store.ErrNotFound) {
		return store.ProviderConfigSet{}, err
	}
	name := id
	if req.NewConfigSetName != nil {
		name, appErr = validateName(*req.NewConfigSetName, apperror.GrokBuildInvalid)
		if appErr != nil {
			return store.ProviderConfigSet{}, appErr
		}
	}
	description := source.Description
	if req.NewConfigSetDescription != nil {
		description, appErr = validateDescription(*req.NewConfigSetDescription, apperror.GrokBuildInvalid)
		if appErr != nil {
			return store.ProviderConfigSet{}, appErr
		}
	}
	return grokprofile.UpsertConfigSet(ctx, db, id, name, description, source.PayloadText)
}

func normalizeProfileFields(id string, name, description *string) (managedProfileFields, *apperror.Error) {
	fields := managedProfileFields{CreateName: id}
	if name != nil {
		value, appErr := validateName(*name, apperror.ProfileInvalid)
		if appErr != nil {
			return managedProfileFields{}, appErr
		}
		fields.CreateName = value
		fields.UpdateName = &value
	}
	if description != nil {
		value, appErr := validateDescription(*description, apperror.ProfileInvalid)
		if appErr != nil {
			return managedProfileFields{}, appErr
		}
		fields.CreateDescription = value
		fields.UpdateDescription = &value
	}
	return fields, nil
}

func normalizeForkBinding(raw, label string) (string, *apperror.Error) {
	value := strings.TrimSpace(raw)
	if value == ForkBindingShareParent || value == ForkBindingCopyNew {
		return value, nil
	}
	return "", apperror.New(apperror.GrokBuildInvalid, "unsupported Grok Build fork "+label+" binding")
}

func coordinationTargets(home grokconfig.Home) []providercoord.Target {
	return []providercoord.Target{{
		ID: grokconfig.AuthTargetID, BackendID: switchtarget.BackendFile, Path: home.AuthPath,
	}}
}

func maintenanceMetadata(action string) string {
	raw, _ := json.Marshal(map[string]string{
		"action": action, "provider_id": grokconfig.ProviderID,
	})
	return string(raw)
}

func (service *Service) profileSaveResult(
	ctx context.Context,
	storedProvider store.Provider,
	storedProfile store.Profile,
	storedConfigSet store.ProviderConfigSet,
	operationID string,
	home grokconfig.Home,
	warnings []string,
) (ProfileSaveResult, error) {
	publicProvider, err := publicProvider(storedProvider)
	if err != nil {
		return ProfileSaveResult{}, err
	}
	publicProfile, err := publicProfile(storedProfile)
	if err != nil {
		return ProfileSaveResult{}, err
	}
	db, err := service.openStore(ctx, true)
	if err != nil {
		return ProfileSaveResult{}, err
	}
	defer db.Close()
	detail, err := getProfileFromStore(ctx, db, storedProfile.ID)
	if err != nil {
		return ProfileSaveResult{}, err
	}
	activeID, err := activeConfigSetID(ctx, db)
	if err != nil {
		return ProfileSaveResult{}, err
	}
	configSet, err := configSetFromStore(ctx, db, storedConfigSet, activeID)
	if err != nil {
		return ProfileSaveResult{}, err
	}
	return ProfileSaveResult{
		OperationID: operationID, Provider: publicProvider, Profile: publicProfile,
		Summary: detail.Summary, ConfigSet: configSet,
		GrokHome: home.Dir, ConfigPath: home.ConfigPath, AuthPath: home.AuthPath,
		Warnings: warnings,
	}, nil
}
