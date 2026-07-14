package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/strahe/profiledeck/internal/agent"
	"github.com/strahe/profiledeck/internal/apperror"
	claudecodeauth "github.com/strahe/profiledeck/internal/claudecode/auth"
	claudecodeconfig "github.com/strahe/profiledeck/internal/claudecode/config"
	claudeprofile "github.com/strahe/profiledeck/internal/claudecode/profile"
	"github.com/strahe/profiledeck/internal/maintenance"
	profilecore "github.com/strahe/profiledeck/internal/profile"
	"github.com/strahe/profiledeck/internal/profiletarget"
	"github.com/strahe/profiledeck/internal/runtime"
	"github.com/strahe/profiledeck/internal/store"
	switchtarget "github.com/strahe/profiledeck/internal/switching/target"
	"github.com/strahe/profiledeck/internal/validate"
)

type Service struct {
	runtime     *runtime.Service
	stores      store.Factory
	maintenance maintenance.Runner
	policy      agent.Policy
	targets     switchtarget.Registry
}

func NewService(runtimeService *runtime.Service, stores store.Factory, maintenance maintenance.Runner, policy agent.Policy, targets switchtarget.Registry) *Service {
	return &Service{runtime: runtimeService, stores: stores, maintenance: maintenance, policy: policy, targets: targets}
}

type ClaudeCodeProfileSummary struct {
	Profile                  profilecore.Profile `json:"profile"`
	ProviderID               string              `json:"provider_id"`
	CredentialID             string              `json:"credential_id"`
	CredentialStatus         string              `json:"credential_status"`
	CredentialReferenceCount int                 `json:"credential_reference_count"`
	ExpiresAtUnixMS          int64               `json:"expires_at_unix_ms,omitempty"`
	Active                   bool                `json:"active"`
	ActiveOperationID        string              `json:"active_operation_id,omitempty"`
	UpdatedAtUnixMS          int64               `json:"updated_at_unix_ms"`
	Warnings                 []string            `json:"warnings,omitempty"`
}

type ClaudeCodeProfileListResult struct {
	Profiles []ClaudeCodeProfileSummary `json:"profiles"`
}
type ClaudeCodeProfileDetail struct {
	Summary ClaudeCodeProfileSummary `json:"summary"`
}
type (
	GetClaudeCodeProfileRequest struct {
		ProfileID string `json:"profile_id"`
	}
	CreateClaudeCodeProfileRequest struct {
		ProfileID   string  `json:"profile_id"`
		Name        *string `json:"name,omitempty"`
		Description *string `json:"description,omitempty"`
	}
)

type UpdateClaudeCodeProfileRequest struct {
	ProfileID   string  `json:"profile_id"`
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
}
type SaveActiveClaudeCodeProfileRequest struct {
	ConfirmShared bool `json:"confirm_shared"`
}
type ClaudeCodeProfileSaveResult struct {
	OperationID string                   `json:"operation_id"`
	Summary     ClaudeCodeProfileSummary `json:"summary"`
	Warnings    []string                 `json:"warnings"`
}

type claudeCodeProviderMetadata = claudeprofile.ProviderMetadata

func (service *Service) ListProfiles(ctx context.Context) (ClaudeCodeProfileListResult, error) {
	if err := service.requireAccess(ctx); err != nil {
		return ClaudeCodeProfileListResult{}, err
	}
	db, err := service.stores.OpenHealthy(ctx, true)
	if err != nil {
		return ClaudeCodeProfileListResult{}, err
	}
	defer db.Close()
	if _, err := requireClaudeCodeProvider(ctx, db); err != nil {
		return ClaudeCodeProfileListResult{}, err
	}
	profiles, err := listClaudeCodeProfileSummaries(ctx, db)
	return ClaudeCodeProfileListResult{Profiles: profiles}, err
}

func (service *Service) GetProfile(ctx context.Context, req GetClaudeCodeProfileRequest) (ClaudeCodeProfileDetail, error) {
	if err := service.requireAccess(ctx); err != nil {
		return ClaudeCodeProfileDetail{}, err
	}
	profileID, appErr := validate.ID(req.ProfileID, apperror.ProfileInvalid)
	if appErr != nil {
		return ClaudeCodeProfileDetail{}, appErr
	}
	db, err := service.stores.OpenHealthy(ctx, true)
	if err != nil {
		return ClaudeCodeProfileDetail{}, err
	}
	defer db.Close()
	if _, err := requireClaudeCodeProvider(ctx, db); err != nil {
		return ClaudeCodeProfileDetail{}, err
	}
	summary, err := claudeCodeProfileSummary(ctx, db, profileID)
	return ClaudeCodeProfileDetail{Summary: summary}, err
}

func (service *Service) CreateProfile(ctx context.Context, req CreateClaudeCodeProfileRequest) (ClaudeCodeProfileSaveResult, error) {
	if err := service.requireAccess(ctx); err != nil {
		return ClaudeCodeProfileSaveResult{}, err
	}
	profileID, appErr := validate.ID(req.ProfileID, apperror.ProfileInvalid)
	if appErr != nil {
		return ClaudeCodeProfileSaveResult{}, appErr
	}
	fields, appErr := normalizeManagedProfileFields(profileID, req.Name, req.Description)
	if appErr != nil {
		return ClaudeCodeProfileSaveResult{}, appErr
	}
	credentialID, err := newClaudeCodeCredentialID(time.Now())
	if err != nil {
		return ClaudeCodeProfileSaveResult{}, apperror.Wrap(apperror.OperationCreateFailed, "failed to create Claude Code login id", err)
	}
	operationMetadata, err := providerMaintenanceMetadata("claude-code-profile-create", claudecodeconfig.ProviderID, profileID, credentialID)
	if err != nil {
		return ClaudeCodeProfileSaveResult{}, err
	}
	operationID := ""
	err = service.maintenance.RunMaintenance(ctx, maintenance.Request{
		Operation: "claude-code-profile-create", ProfileID: profileID, ProviderID: claudecodeconfig.ProviderID,
		MetadataJSON: operationMetadata, SetActive: true, Record: true,
	}, func(ctx context.Context, tx *store.Store, currentOperationID string) error {
		operationID = currentOperationID
		metadata, _, err := resolveClaudeCodeProviderMetadata(ctx, tx)
		if err != nil {
			return err
		}
		spec := claudeCodeTargetSpec(metadata)
		backend, ok := service.targets.Backend(spec.BackendID())
		if !ok {
			return apperror.New(apperror.TargetReadFailed, "Claude Code credential backend is unavailable")
		}
		snapshot, err := backend.Inspect(ctx, spec)
		if err != nil {
			return err
		}
		payload, _, err := normalizeCurrentClaudeCodeCredential(snapshot)
		if err != nil {
			return err
		}
		// Captures commit only the exact working copy inspected under the shared lock.
		if err := backend.Verify(ctx, spec, snapshot); err != nil {
			return err
		}
		profile, profileErr := tx.GetProfile(ctx, profileID)
		hasProfile := profileErr == nil
		if profileErr != nil && !errors.Is(profileErr, store.ErrNotFound) {
			return mapProfileStoreError(profileErr)
		}
		if hasProfile {
			bindings, err := tx.ListProfileCredentialBindings(ctx, profileID, claudecodeconfig.ProviderID)
			if err != nil {
				return apperror.Wrap(apperror.StoreStatusFailed, "failed to read Claude Code Profile bindings", err)
			}
			configBindings, err := tx.ListProfileConfigSetBindings(ctx, profileID, claudecodeconfig.ProviderID)
			if err != nil {
				return apperror.Wrap(apperror.StoreStatusFailed, "failed to read Claude Code Profile config bindings", err)
			}
			if len(bindings) != 0 || len(configBindings) != 0 {
				return apperror.New(apperror.ProfileAlreadyExists, "Claude Code Profile already exists").WithDetail("profile_id", profileID)
			}
			targets, err := tx.ListProfileTargets(ctx, profileID, claudecodeconfig.ProviderID, true)
			if err != nil {
				return apperror.Wrap(apperror.StoreStatusFailed, "failed to read Claude Code Profile targets", err)
			}
			if len(targets) != 0 {
				return apperror.New(apperror.ClaudeCodeInvalid, "existing Profile contains unsupported Claude Code targets").WithDetail("profile_id", profileID)
			}
		}
		if err := ensureClaudeCodeProvider(ctx, tx, metadata); err != nil {
			return err
		}
		if !hasProfile {
			if _, err := tx.CreateProfile(ctx, store.CreateProfileParams{ID: profileID, Name: fields.CreateName, Description: fields.CreateDescription, MetadataJSON: "{}"}); err != nil {
				return mapProfileStoreError(err)
			}
		} else if fields.UpdateName != nil || fields.UpdateDescription != nil {
			if _, err := tx.UpdateProfile(ctx, store.UpdateProfileParams{ID: profile.ID, Name: fields.UpdateName, Description: fields.UpdateDescription}); err != nil {
				return mapProfileStoreError(err)
			}
		}
		if _, err := tx.UpsertProviderCredential(ctx, store.UpsertProviderCredentialParams{
			ID: credentialID, ProviderID: claudecodeconfig.ProviderID, CredentialKind: claudecodeconfig.CredentialKind,
			PayloadJSON: payload, PayloadSHA256: sha256HexString(payload), MetadataJSON: "{}",
		}); err != nil {
			return apperror.Wrap(apperror.StoreStatusFailed, "failed to save Claude Code login", err)
		}
		if _, err := tx.UpsertProfileCredentialBinding(ctx, store.UpsertProfileCredentialBindingParams{
			ProfileID: profileID, ProviderID: claudecodeconfig.ProviderID, SlotID: claudecodeconfig.CredentialSlot, CredentialID: credentialID,
		}); err != nil {
			return apperror.Wrap(apperror.StoreStatusFailed, "failed to bind Claude Code login", err)
		}
		return nil
	})
	if err != nil {
		return ClaudeCodeProfileSaveResult{}, err
	}
	db, err := service.stores.OpenHealthy(ctx, true)
	if err != nil {
		return ClaudeCodeProfileSaveResult{}, err
	}
	defer db.Close()
	summary, err := claudeCodeProfileSummary(ctx, db, profileID)
	return ClaudeCodeProfileSaveResult{OperationID: operationID, Summary: summary, Warnings: []string{}}, err
}

func (service *Service) UpdateProfile(ctx context.Context, req UpdateClaudeCodeProfileRequest) (ClaudeCodeProfileDetail, error) {
	if err := service.requireAccess(ctx); err != nil {
		return ClaudeCodeProfileDetail{}, err
	}
	profileID, appErr := validate.ID(req.ProfileID, apperror.ProfileInvalid)
	if appErr != nil {
		return ClaudeCodeProfileDetail{}, appErr
	}
	if req.Name == nil && req.Description == nil {
		return ClaudeCodeProfileDetail{}, apperror.New(apperror.ProfileInvalid, "Claude Code Profile update requires a name or description")
	}
	name, description := req.Name, req.Description
	if name != nil {
		value, appErr := validate.Name(*name, apperror.ProfileInvalid)
		if appErr != nil {
			return ClaudeCodeProfileDetail{}, appErr
		}
		name = &value
	}
	if description != nil {
		value, appErr := validate.Description(*description, apperror.ProfileInvalid)
		if appErr != nil {
			return ClaudeCodeProfileDetail{}, appErr
		}
		description = &value
	}
	metadata, err := providerMaintenanceMetadata("claude-code-profile-update", claudecodeconfig.ProviderID, profileID, "")
	if err != nil {
		return ClaudeCodeProfileDetail{}, err
	}
	err = service.maintenance.RunMaintenance(ctx, maintenance.Request{
		Operation: "claude-code-profile-update", ProfileID: profileID, ProviderID: claudecodeconfig.ProviderID,
		MetadataJSON: metadata, Record: true,
	}, func(ctx context.Context, tx *store.Store, _ string) error {
		if _, err := requireClaudeCodeProvider(ctx, tx); err != nil {
			return err
		}
		// A corrupt typed binding must not produce a partial shared-Profile update.
		if _, err := claudeCodeProfileSummary(ctx, tx, profileID); err != nil {
			return err
		}
		_, err := tx.UpdateProfile(ctx, store.UpdateProfileParams{ID: profileID, Name: name, Description: description})
		return mapProfileStoreError(err)
	})
	if err != nil {
		return ClaudeCodeProfileDetail{}, err
	}
	db, err := service.stores.OpenHealthy(ctx, true)
	if err != nil {
		return ClaudeCodeProfileDetail{}, err
	}
	defer db.Close()
	summary, err := claudeCodeProfileSummary(ctx, db, profileID)
	return ClaudeCodeProfileDetail{Summary: summary}, err
}

func (service *Service) SaveActiveProfile(ctx context.Context, req SaveActiveClaudeCodeProfileRequest) (ClaudeCodeProfileSaveResult, error) {
	if err := service.requireAccess(ctx); err != nil {
		return ClaudeCodeProfileSaveResult{}, err
	}
	operationID := ""
	profileID := ""
	references := 0
	err := service.maintenance.RunMaintenance(ctx, maintenance.Request{
		Operation: "claude-code-profile-save-current", ProviderID: claudecodeconfig.ProviderID, Record: false,
	}, func(ctx context.Context, tx *store.Store, currentOperationID string) error {
		operationID = currentOperationID
		provider, err := requireClaudeCodeProvider(ctx, tx)
		if err != nil {
			return err
		}
		metadata, err := validateClaudeCodeProvider(provider)
		if err != nil {
			return err
		}
		spec := claudeCodeTargetSpec(metadata)
		backend, ok := service.targets.Backend(spec.BackendID())
		if !ok {
			return apperror.New(apperror.TargetReadFailed, "Claude Code credential backend is unavailable")
		}
		snapshot, err := backend.Inspect(ctx, spec)
		if err != nil {
			return err
		}
		payload, _, err := normalizeCurrentClaudeCodeCredential(snapshot)
		if err != nil {
			return err
		}
		active, err := tx.GetActiveState(ctx, store.ActiveStateScopeProvider, claudecodeconfig.ProviderID)
		if errors.Is(err, store.ErrNotFound) {
			return apperror.New(apperror.ProfileNotFound, "no active Claude Code Profile")
		}
		if err != nil {
			return apperror.Wrap(apperror.StoreStatusFailed, "failed to read active Claude Code Profile", err)
		}
		profileID = active.ProfileID
		if _, err := claudeCodeProfileSummary(ctx, tx, profileID); err != nil {
			return err
		}
		binding, err := tx.GetProfileCredentialBinding(ctx, profileID, claudecodeconfig.ProviderID, claudecodeconfig.CredentialSlot)
		if err != nil {
			return apperror.New(apperror.ClaudeCodeInvalid, "active Claude Code Profile login binding is missing")
		}
		credential, err := requireClaudeCodeCredential(ctx, tx, binding.CredentialID)
		if err != nil {
			return err
		}
		references, err = tx.CountProviderCredentialReferences(ctx, credential.ID)
		if err != nil {
			return apperror.Wrap(apperror.StoreStatusFailed, "failed to count Claude Code login references", err)
		}
		if references > 1 && !req.ConfirmShared {
			return apperror.New(apperror.ConfirmationRequired, fmt.Sprintf("saving the current Claude Code login changes %d Profiles; confirm with --yes", references)).WithDetail("affected_profiles", references)
		}
		// Re-read the same file or Keychain item immediately before committing.
		if err := backend.Verify(ctx, spec, snapshot); err != nil {
			return err
		}
		if _, err := tx.UpsertProviderCredential(ctx, store.UpsertProviderCredentialParams{
			ID: credential.ID, ProviderID: credential.ProviderID, CredentialKind: credential.CredentialKind,
			PayloadJSON: payload, PayloadSHA256: sha256HexString(payload), MetadataJSON: credential.MetadataJSON,
		}); err != nil {
			return apperror.Wrap(apperror.StoreStatusFailed, "failed to save Claude Code login", err)
		}
		operationMetadata, err := providerMaintenanceMetadata("claude-code-profile-save-current", claudecodeconfig.ProviderID, profileID, credential.ID)
		if err != nil {
			return err
		}
		_, err = tx.CreateAppliedMaintenanceOperation(ctx, store.CreateAppliedMaintenanceOperationParams{
			ID: operationID, ProfileID: profileID, ProviderID: claudecodeconfig.ProviderID, MetadataJSON: operationMetadata,
		})
		return err
	})
	if err != nil {
		return ClaudeCodeProfileSaveResult{}, err
	}
	db, err := service.stores.OpenHealthy(ctx, true)
	if err != nil {
		return ClaudeCodeProfileSaveResult{}, err
	}
	defer db.Close()
	summary, err := claudeCodeProfileSummary(ctx, db, profileID)
	warnings := []string{}
	if references > 1 {
		warnings = append(warnings, "shared Claude Code login state updated")
	}
	return ClaudeCodeProfileSaveResult{OperationID: operationID, Summary: summary, Warnings: warnings}, err
}

func normalizeCurrentClaudeCodeCredential(snapshot switchtarget.Snapshot) (string, claudecodeauth.Info, error) {
	if !snapshot.Exists {
		return "", claudecodeauth.Info{}, apperror.New(apperror.ClaudeCodeInvalid, "Claude Code is not signed in; run Claude Code /login first")
	}
	if snapshot.IsSymlink {
		return "", claudecodeauth.Info{}, apperror.New(apperror.ClaudeCodeInvalid, "Claude Code credential file is a symbolic link and will not be used")
	}
	payload, info, err := claudecodeauth.Normalize([]byte(snapshot.Content))
	if err != nil {
		if claudecodeauth.IsKind(err, claudecodeauth.ErrorUnsupportedAccountType) {
			return "", claudecodeauth.Info{}, apperror.New(apperror.ClaudeCodeInvalid, "Claude Code login does not report an active Pro, Max, Team, or Enterprise subscription").WithDetail("reason", "unsupported_account_type")
		}
		return "", claudecodeauth.Info{}, apperror.New(apperror.ClaudeCodeInvalid, "Claude Code login is invalid")
	}
	return payload, info, nil
}

func listClaudeCodeProfileSummaries(ctx context.Context, db *store.Store) ([]ClaudeCodeProfileSummary, error) {
	bindings, err := db.ListProfileCredentialBindingsByProvider(ctx, claudecodeconfig.ProviderID)
	if err != nil {
		return nil, apperror.Wrap(apperror.StoreStatusFailed, "failed to list Claude Code Profile bindings", err)
	}
	ids := []string{}
	for _, binding := range bindings {
		if binding.SlotID == claudecodeconfig.CredentialSlot {
			ids = append(ids, binding.ProfileID)
		}
	}
	sort.Strings(ids)
	result := make([]ClaudeCodeProfileSummary, 0, len(ids))
	for _, id := range ids {
		summary, err := claudeCodeProfileSummary(ctx, db, id)
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

func claudeCodeProfileSummary(ctx context.Context, db *store.Store, profileID string) (ClaudeCodeProfileSummary, error) {
	profile, err := db.GetProfile(ctx, profileID)
	if err != nil {
		return ClaudeCodeProfileSummary{}, mapProfileStoreError(err)
	}
	publicProfile, err := profileFromStore(profile)
	if err != nil {
		return ClaudeCodeProfileSummary{}, err
	}
	binding, err := db.GetProfileCredentialBinding(ctx, profileID, claudecodeconfig.ProviderID, claudecodeconfig.CredentialSlot)
	if errors.Is(err, store.ErrNotFound) {
		return ClaudeCodeProfileSummary{}, apperror.New(apperror.ProfileNotFound, "Claude Code Profile not found").WithDetail("profile_id", profileID)
	}
	if err != nil {
		return ClaudeCodeProfileSummary{}, apperror.Wrap(apperror.StoreStatusFailed, "failed to read Claude Code Profile binding", err)
	}
	bindings, err := db.ListProfileCredentialBindings(ctx, profileID, claudecodeconfig.ProviderID)
	if err != nil {
		return ClaudeCodeProfileSummary{}, apperror.Wrap(apperror.StoreStatusFailed, "failed to read Claude Code Profile bindings", err)
	}
	configBindings, err := db.ListProfileConfigSetBindings(ctx, profileID, claudecodeconfig.ProviderID)
	if err != nil {
		return ClaudeCodeProfileSummary{}, apperror.Wrap(apperror.StoreStatusFailed, "failed to read Claude Code Profile config bindings", err)
	}
	targets, err := db.ListProfileTargets(ctx, profileID, claudecodeconfig.ProviderID, true)
	if err != nil {
		return ClaudeCodeProfileSummary{}, apperror.Wrap(apperror.StoreStatusFailed, "failed to read Claude Code Profile targets", err)
	}
	if len(bindings) != 1 || bindings[0].SlotID != claudecodeconfig.CredentialSlot || bindings[0].CredentialID != binding.CredentialID || len(configBindings) != 0 || len(targets) != 0 {
		return ClaudeCodeProfileSummary{}, apperror.New(apperror.ClaudeCodeInvalid, "Claude Code Profile bindings are missing or unsupported").WithDetail("profile_id", profileID)
	}
	summary := ClaudeCodeProfileSummary{
		Profile: publicProfile, ProviderID: claudecodeconfig.ProviderID, CredentialID: binding.CredentialID,
		CredentialStatus: claudecodeauth.StatusInvalid, UpdatedAtUnixMS: max(profile.UpdatedAtUnixMS, binding.UpdatedAtUnixMS),
	}
	credential, err := requireClaudeCodeCredential(ctx, db, binding.CredentialID)
	if err != nil {
		summary.Warnings = append(summary.Warnings, "Claude Code login is missing or invalid")
	} else {
		_, info, _ := claudecodeauth.Normalize([]byte(credential.PayloadJSON))
		summary.CredentialStatus = claudecodeauth.StatusAt(info, time.Now())
		summary.ExpiresAtUnixMS = info.ExpiresAtUnixMS
		if info.ExpiryUnknown {
			summary.Warnings = append(summary.Warnings, "Claude Code login expiry could not be determined")
		}
		summary.UpdatedAtUnixMS = max(summary.UpdatedAtUnixMS, credential.UpdatedAtUnixMS)
	}
	summary.CredentialReferenceCount, err = db.CountProviderCredentialReferences(ctx, binding.CredentialID)
	if err != nil {
		return ClaudeCodeProfileSummary{}, apperror.Wrap(apperror.StoreStatusFailed, "failed to count Claude Code login references", err)
	}
	active, err := db.GetActiveState(ctx, store.ActiveStateScopeProvider, claudecodeconfig.ProviderID)
	if err == nil && active.ProfileID == profileID {
		summary.Active, summary.ActiveOperationID = true, active.OperationID
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		return ClaudeCodeProfileSummary{}, apperror.Wrap(apperror.StoreStatusFailed, "failed to read active Claude Code Profile", err)
	}
	return summary, nil
}

func requireClaudeCodeCredential(ctx context.Context, db *store.Store, credentialID string) (store.ProviderCredential, error) {
	return claudeprofile.RequireCredential(ctx, db, credentialID)
}

func resolveClaudeCodeProviderMetadata(ctx context.Context, db *store.Store) (claudeCodeProviderMetadata, bool, error) {
	provider, err := db.GetProvider(ctx, claudecodeconfig.ProviderID)
	if err == nil {
		if !provider.Enabled {
			return claudeCodeProviderMetadata{}, true, apperror.New(apperror.ProviderDisabled, "Claude Code Provider is disabled")
		}
		metadata, err := validateClaudeCodeProvider(provider)
		return metadata, true, err
	}
	if !errors.Is(err, store.ErrNotFound) {
		return claudeCodeProviderMetadata{}, false, mapProviderStoreError(err)
	}
	locator, err := claudecodeconfig.ResolveLocator()
	if err != nil {
		return claudeCodeProviderMetadata{}, false, apperror.New(apperror.ClaudeCodeInvalid, "Claude Code credential target is unavailable")
	}
	return newClaudeCodeProviderMetadata(locator), false, nil
}

func newClaudeCodeProviderMetadata(locator claudecodeconfig.Locator) claudeCodeProviderMetadata {
	return claudeprofile.NewProviderMetadata(locator)
}

func createClaudeCodeProvider(ctx context.Context, db *store.Store, metadata claudeCodeProviderMetadata) error {
	if appErr := ensureClaudeCodePathOwnership(ctx, db, metadata); appErr != nil {
		return appErr
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		return apperror.Wrap(apperror.StoreStatusFailed, "failed to encode Claude Code provider", err)
	}
	_, err = db.CreateProvider(ctx, store.CreateProviderParams{
		ID: claudecodeconfig.ProviderID, Name: claudecodeconfig.ProviderName, AdapterID: claudecodeconfig.AdapterID,
		Enabled: true, MetadataJSON: string(raw),
	})
	if err == nil {
		return nil
	}
	return mapProviderStoreError(err)
}

func ensureClaudeCodeProvider(ctx context.Context, db *store.Store, metadata claudeCodeProviderMetadata) error {
	if appErr := ensureClaudeCodePathOwnership(ctx, db, metadata); appErr != nil {
		return appErr
	}
	provider, err := db.GetProvider(ctx, claudecodeconfig.ProviderID)
	if errors.Is(err, store.ErrNotFound) {
		return createClaudeCodeProvider(ctx, db, metadata)
	}
	if err != nil {
		return mapProviderStoreError(err)
	}
	if _, err := validateClaudeCodeProvider(provider); err != nil {
		return err
	}
	if !provider.Enabled {
		return apperror.New(apperror.ProviderDisabled, "Claude Code Provider is disabled")
	}
	name := claudecodeconfig.ProviderName
	if _, err := db.UpdateProvider(ctx, store.UpdateProviderParams{ID: provider.ID, Name: &name}); err != nil {
		return mapProviderStoreError(err)
	}
	return nil
}

func ensureClaudeCodePathOwnership(ctx context.Context, db *store.Store, metadata claudeCodeProviderMetadata) *apperror.Error {
	file, ok := claudeCodeTargetSpec(metadata).(switchtarget.FileSpec)
	if !ok {
		return nil
	}
	return profiletarget.EnsurePathOwnership(
		ctx, db, file.Path, profiletarget.PathOwnershipKey(file.Path),
		claudecodeconfig.ProviderID, claudecodeconfig.TargetID, nil,
	)
}

func requireClaudeCodeProvider(ctx context.Context, db *store.Store) (store.Provider, error) {
	provider, err := db.GetProvider(ctx, claudecodeconfig.ProviderID)
	if err != nil {
		return store.Provider{}, mapProviderStoreError(err)
	}
	if _, err := validateClaudeCodeProvider(provider); err != nil {
		return store.Provider{}, err
	}
	if !provider.Enabled {
		return store.Provider{}, apperror.New(apperror.ProviderDisabled, "Claude Code Provider is disabled")
	}
	return provider, nil
}

func validateClaudeCodeProvider(provider store.Provider) (claudeCodeProviderMetadata, error) {
	return claudeprofile.ValidateProvider(provider)
}

func claudeCodeTargetSpec(metadata claudeCodeProviderMetadata) switchtarget.Spec {
	return claudeprofile.TargetSpec(metadata)
}

func claudeCodeLocatorWarnings(saved claudeCodeProviderMetadata) []string {
	return claudeprofile.LocatorWarnings(saved)
}

func observedClaudeCodeAuthOverrideHints() []string {
	return claudeprofile.ObservedAuthOverrideHints()
}

func newClaudeCodeCredentialID(now time.Time) (string, error) {
	return claudeprofile.NewCredentialID(now)
}
