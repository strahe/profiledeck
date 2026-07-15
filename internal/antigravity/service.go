package antigravity

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/strahe/profiledeck/internal/agent"
	agyauth "github.com/strahe/profiledeck/internal/antigravity/auth"
	agyconfig "github.com/strahe/profiledeck/internal/antigravity/config"
	agyprofile "github.com/strahe/profiledeck/internal/antigravity/profile"
	agyquota "github.com/strahe/profiledeck/internal/antigravity/quota"
	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/maintenance"
	profilecore "github.com/strahe/profiledeck/internal/profile"
	"github.com/strahe/profiledeck/internal/runtime"
	"github.com/strahe/profiledeck/internal/store"
	switchtarget "github.com/strahe/profiledeck/internal/switching/target"
	"github.com/strahe/profiledeck/internal/validate"
)

type Service struct {
	runtime     *runtime.Service
	stores      store.Factory
	maintenance maintenance.Runner
	sharedLock  maintenance.SharedLockRunner
	policy      agent.Policy
	targets     switchtarget.Registry
	quotaReader agyquota.Reader
	now         func() time.Time
}

func NewService(
	runtimeService *runtime.Service,
	stores store.Factory,
	maintenanceRunner maintenance.Runner,
	sharedLock maintenance.SharedLockRunner,
	policy agent.Policy,
	targets switchtarget.Registry,
) *Service {
	return &Service{
		runtime: runtimeService, stores: stores, maintenance: maintenanceRunner, sharedLock: sharedLock,
		policy: policy, targets: targets, quotaReader: agyquota.NewClient(), now: time.Now,
	}
}

type AntigravityProfileSummary struct {
	Profile                  profilecore.Profile `json:"profile"`
	ProviderID               string              `json:"provider_id"`
	CredentialID             string              `json:"credential_id"`
	CredentialReferenceCount int                 `json:"credential_reference_count"`
	// ExpiresAtUnixMS remains available to machine-readable clients; human
	// surfaces avoid presenting a short-lived access-token expiry as login health.
	ExpiresAtUnixMS   int64    `json:"expires_at_unix_ms,omitempty"`
	Active            bool     `json:"active"`
	ActiveOperationID string   `json:"active_operation_id,omitempty"`
	UpdatedAtUnixMS   int64    `json:"updated_at_unix_ms"`
	Warnings          []string `json:"warnings,omitempty"`
}

type AntigravityProfileListResult struct {
	Profiles []AntigravityProfileSummary `json:"profiles"`
}

type AntigravityProfileDetail struct {
	Summary AntigravityProfileSummary `json:"summary"`
}

type GetAntigravityProfileRequest struct {
	ProfileID string `json:"profile_id"`
}

type CreateAntigravityProfileRequest struct {
	ProfileID   string  `json:"profile_id"`
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
}

type UpdateAntigravityProfileRequest struct {
	ProfileID   string  `json:"profile_id"`
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
}

type AntigravityProfileSaveResult struct {
	OperationID string                    `json:"operation_id"`
	Summary     AntigravityProfileSummary `json:"summary"`
	Warnings    []string                  `json:"warnings"`
}

func antigravityTargetSpec() switchtarget.KeyringSpec {
	return agyprofile.TargetSpec()
}

func (service *Service) ListProfiles(ctx context.Context) (AntigravityProfileListResult, error) {
	if err := service.requireAccess(ctx); err != nil {
		return AntigravityProfileListResult{}, err
	}
	db, err := service.stores.OpenHealthy(ctx, true)
	if err != nil {
		return AntigravityProfileListResult{}, err
	}
	defer db.Close()
	if _, err := requireAntigravityProvider(ctx, db); err != nil {
		return AntigravityProfileListResult{}, err
	}
	summaries, err := listAntigravityProfileSummaries(ctx, db)
	if err != nil {
		return AntigravityProfileListResult{}, err
	}
	return AntigravityProfileListResult{Profiles: summaries}, nil
}

func (service *Service) GetProfile(ctx context.Context, req GetAntigravityProfileRequest) (AntigravityProfileDetail, error) {
	if err := service.requireAccess(ctx); err != nil {
		return AntigravityProfileDetail{}, err
	}
	profileID, appErr := validate.ID(req.ProfileID, apperror.ProfileInvalid)
	if appErr != nil {
		return AntigravityProfileDetail{}, appErr
	}
	db, err := service.stores.OpenHealthy(ctx, true)
	if err != nil {
		return AntigravityProfileDetail{}, err
	}
	defer db.Close()
	if _, err := requireAntigravityProvider(ctx, db); err != nil {
		return AntigravityProfileDetail{}, err
	}
	summary, err := antigravityProfileSummary(ctx, db, profileID)
	if err != nil {
		return AntigravityProfileDetail{}, err
	}
	return AntigravityProfileDetail{Summary: summary}, nil
}

func (service *Service) CreateProfile(ctx context.Context, req CreateAntigravityProfileRequest) (AntigravityProfileSaveResult, error) {
	if err := service.requireAccess(ctx); err != nil {
		return AntigravityProfileSaveResult{}, err
	}
	profileID, appErr := validate.ID(req.ProfileID, apperror.ProfileInvalid)
	if appErr != nil {
		return AntigravityProfileSaveResult{}, appErr
	}
	fields, appErr := normalizeManagedProfileFields(profileID, req.Name, req.Description)
	if appErr != nil {
		return AntigravityProfileSaveResult{}, appErr
	}
	credentialID, err := newAntigravityCredentialID(time.Now())
	if err != nil {
		return AntigravityProfileSaveResult{}, apperror.Wrap(apperror.OperationCreateFailed, "failed to create Antigravity login id", err)
	}
	metadata, err := providerMaintenanceMetadata("antigravity-profile-create", agyconfig.ProviderID, profileID, credentialID)
	if err != nil {
		return AntigravityProfileSaveResult{}, err
	}
	operationID := ""
	err = service.maintenance.RunMaintenance(ctx, maintenance.Request{
		Operation: "antigravity-profile-create", ProfileID: profileID, ProviderID: agyconfig.ProviderID,
		MetadataJSON: metadata, SetActive: true, Record: true,
	}, func(ctx context.Context, tx *store.Store, currentOperationID string) error {
		operationID = currentOperationID
		backend, ok := service.targets.Backend(switchtarget.BackendKeyring)
		if !ok {
			return apperror.New(apperror.TargetReadFailed, "Antigravity credential backend is unavailable")
		}
		snapshot, err := backend.Inspect(ctx, antigravityTargetSpec())
		if err != nil {
			return err
		}
		if !snapshot.Exists {
			return apperror.New(apperror.AntigravityInvalid, "Antigravity is not signed in; sign in to Antigravity first")
		}
		payload, _, err := agyauth.Normalize([]byte(snapshot.Content))
		if err != nil {
			return apperror.New(apperror.AntigravityInvalid, "Antigravity login is not supported by ProfileDeck")
		}
		profile, profileErr := tx.GetProfile(ctx, profileID)
		hasProfile := profileErr == nil
		if profileErr != nil && !errors.Is(profileErr, store.ErrNotFound) {
			return mapProfileStoreError(profileErr)
		}
		if hasProfile {
			bindings, err := tx.ListProfileCredentialBindings(ctx, profileID, agyconfig.ProviderID)
			if err != nil {
				return apperror.Wrap(apperror.StoreStatusFailed, "failed to read Antigravity profile bindings", err)
			}
			configBindings, err := tx.ListProfileConfigSetBindings(ctx, profileID, agyconfig.ProviderID)
			if err != nil {
				return apperror.Wrap(apperror.StoreStatusFailed, "failed to read Antigravity profile config bindings", err)
			}
			if len(bindings) != 0 || len(configBindings) != 0 {
				return apperror.New(apperror.ProfileAlreadyExists, "Antigravity profile already exists").WithDetail("profile_id", profileID)
			}
		}
		if err := ensureAntigravityProvider(ctx, tx); err != nil {
			return err
		}
		if !hasProfile {
			if _, err := tx.CreateProfile(ctx, store.CreateProfileParams{
				ID: profileID, Name: fields.CreateName, Description: fields.CreateDescription, MetadataJSON: "{}",
			}); err != nil {
				return mapProfileStoreError(err)
			}
		} else if fields.UpdateName != nil || fields.UpdateDescription != nil {
			if _, err := tx.UpdateProfile(ctx, store.UpdateProfileParams{
				ID: profile.ID, Name: fields.UpdateName, Description: fields.UpdateDescription,
			}); err != nil {
				return mapProfileStoreError(err)
			}
		}
		if _, err := tx.UpsertProviderCredential(ctx, store.UpsertProviderCredentialParams{
			ID: credentialID, ProviderID: agyconfig.ProviderID, CredentialKind: agyconfig.CredentialKind,
			PayloadJSON: payload, PayloadSHA256: sha256HexString(payload), MetadataJSON: "{}",
		}); err != nil {
			return apperror.Wrap(apperror.StoreStatusFailed, "failed to save Antigravity login", err)
		}
		if _, err := tx.UpsertProfileCredentialBinding(ctx, store.UpsertProfileCredentialBindingParams{
			ProfileID: profileID, ProviderID: agyconfig.ProviderID,
			SlotID: agyconfig.CredentialSlot, CredentialID: credentialID,
		}); err != nil {
			return apperror.Wrap(apperror.StoreStatusFailed, "failed to bind Antigravity login", err)
		}
		return nil
	})
	if err != nil {
		return AntigravityProfileSaveResult{}, err
	}
	db, err := service.stores.OpenHealthy(ctx, true)
	if err != nil {
		return AntigravityProfileSaveResult{}, err
	}
	defer db.Close()
	summary, err := antigravityProfileSummary(ctx, db, profileID)
	if err != nil {
		return AntigravityProfileSaveResult{}, err
	}
	return AntigravityProfileSaveResult{OperationID: operationID, Summary: summary, Warnings: []string{}}, nil
}

func (service *Service) UpdateProfile(ctx context.Context, req UpdateAntigravityProfileRequest) (AntigravityProfileDetail, error) {
	if err := service.requireAccess(ctx); err != nil {
		return AntigravityProfileDetail{}, err
	}
	profileID, appErr := validate.ID(req.ProfileID, apperror.ProfileInvalid)
	if appErr != nil {
		return AntigravityProfileDetail{}, appErr
	}
	if req.Name == nil && req.Description == nil {
		return AntigravityProfileDetail{}, apperror.New(apperror.ProfileInvalid, "Antigravity profile update requires a name or description")
	}
	name := req.Name
	description := req.Description
	if name != nil {
		value, appErr := validate.Name(*name, apperror.ProfileInvalid)
		if appErr != nil {
			return AntigravityProfileDetail{}, appErr
		}
		name = &value
	}
	if description != nil {
		value, appErr := validate.Description(*description, apperror.ProfileInvalid)
		if appErr != nil {
			return AntigravityProfileDetail{}, appErr
		}
		description = &value
	}
	metadata, err := providerMaintenanceMetadata("antigravity-profile-update", agyconfig.ProviderID, profileID, "")
	if err != nil {
		return AntigravityProfileDetail{}, err
	}
	err = service.maintenance.RunMaintenance(ctx, maintenance.Request{
		Operation: "antigravity-profile-update", ProfileID: profileID, ProviderID: agyconfig.ProviderID,
		MetadataJSON: metadata, Record: true,
	}, func(ctx context.Context, tx *store.Store, _ string) error {
		if _, err := requireAntigravityProvider(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.GetProfileCredentialBinding(ctx, profileID, agyconfig.ProviderID, agyconfig.CredentialSlot); err != nil {
			return apperror.New(apperror.ProfileNotFound, "Antigravity profile not found").WithDetail("profile_id", profileID)
		}
		_, err := tx.UpdateProfile(ctx, store.UpdateProfileParams{ID: profileID, Name: name, Description: description})
		return mapProfileStoreError(err)
	})
	if err != nil {
		return AntigravityProfileDetail{}, err
	}
	db, err := service.stores.OpenHealthy(ctx, true)
	if err != nil {
		return AntigravityProfileDetail{}, err
	}
	defer db.Close()
	summary, err := antigravityProfileSummary(ctx, db, profileID)
	return AntigravityProfileDetail{Summary: summary}, err
}

func (service *Service) SaveActiveProfile(ctx context.Context) (AntigravityProfileSaveResult, error) {
	if err := service.requireAccess(ctx); err != nil {
		return AntigravityProfileSaveResult{}, err
	}
	profileID := ""
	operationID := ""
	err := service.maintenance.RunMaintenance(ctx, maintenance.Request{
		Operation: "antigravity-profile-save-current", ProviderID: agyconfig.ProviderID, Record: false,
	}, func(ctx context.Context, tx *store.Store, currentOperationID string) error {
		operationID = currentOperationID
		if _, err := requireAntigravityProvider(ctx, tx); err != nil {
			return err
		}
		backend, ok := service.targets.Backend(switchtarget.BackendKeyring)
		if !ok {
			return apperror.New(apperror.TargetReadFailed, "Antigravity credential backend is unavailable")
		}
		snapshot, err := backend.Inspect(ctx, antigravityTargetSpec())
		if err != nil {
			return err
		}
		if !snapshot.Exists {
			return apperror.New(apperror.AntigravityInvalid, "Antigravity is not signed in; sign in to Antigravity first")
		}
		payload, _, err := agyauth.Normalize([]byte(snapshot.Content))
		if err != nil {
			return apperror.New(apperror.AntigravityInvalid, "Antigravity login is not supported by ProfileDeck")
		}
		active, err := tx.GetActiveState(ctx, store.ActiveStateScopeProvider, agyconfig.ProviderID)
		if errors.Is(err, store.ErrNotFound) {
			return apperror.New(apperror.ProfileNotFound, "no active Antigravity profile")
		}
		if err != nil {
			return apperror.Wrap(apperror.StoreStatusFailed, "failed to read active Antigravity profile", err)
		}
		profileID = active.ProfileID
		binding, err := tx.GetProfileCredentialBinding(ctx, profileID, agyconfig.ProviderID, agyconfig.CredentialSlot)
		if err != nil {
			return apperror.New(apperror.AntigravityInvalid, "active Antigravity profile login binding is missing")
		}
		credential, err := requireAntigravityCredential(ctx, tx, binding.CredentialID)
		if err != nil {
			return err
		}
		// Profiles bound to the same credential intentionally share one saved login lifecycle.
		if _, err := tx.UpsertProviderCredential(ctx, store.UpsertProviderCredentialParams{
			ID: credential.ID, ProviderID: credential.ProviderID, CredentialKind: credential.CredentialKind,
			PayloadJSON: payload, PayloadSHA256: sha256HexString(payload), MetadataJSON: credential.MetadataJSON,
		}); err != nil {
			return apperror.Wrap(apperror.StoreStatusFailed, "failed to save Antigravity login", err)
		}
		metadata, err := providerMaintenanceMetadata("antigravity-profile-save-current", agyconfig.ProviderID, profileID, credential.ID)
		if err != nil {
			return err
		}
		_, err = tx.CreateAppliedMaintenanceOperation(ctx, store.CreateAppliedMaintenanceOperationParams{
			ID: operationID, ProfileID: profileID, ProviderID: agyconfig.ProviderID, MetadataJSON: metadata,
		})
		return err
	})
	if err != nil {
		return AntigravityProfileSaveResult{}, err
	}
	db, err := service.stores.OpenHealthy(ctx, true)
	if err != nil {
		return AntigravityProfileSaveResult{}, err
	}
	defer db.Close()
	summary, err := antigravityProfileSummary(ctx, db, profileID)
	if err != nil {
		return AntigravityProfileSaveResult{}, err
	}
	warnings := []string{}
	if summary.CredentialReferenceCount > 1 {
		warnings = append(warnings, "shared Antigravity login state updated")
	}
	return AntigravityProfileSaveResult{OperationID: operationID, Summary: summary, Warnings: warnings}, nil
}

func listAntigravityProfileSummaries(ctx context.Context, db *store.Store) ([]AntigravityProfileSummary, error) {
	bindings, err := db.ListProfileCredentialBindingsByProvider(ctx, agyconfig.ProviderID)
	if err != nil {
		return nil, apperror.Wrap(apperror.StoreStatusFailed, "failed to list Antigravity profile bindings", err)
	}
	profileIDs := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		if binding.SlotID == agyconfig.CredentialSlot {
			profileIDs = append(profileIDs, binding.ProfileID)
		}
	}
	sort.Strings(profileIDs)
	result := make([]AntigravityProfileSummary, 0, len(profileIDs))
	for _, profileID := range profileIDs {
		summary, err := antigravityProfileSummary(ctx, db, profileID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
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

func antigravityProfileSummary(ctx context.Context, db *store.Store, profileID string) (AntigravityProfileSummary, error) {
	profile, err := db.GetProfile(ctx, profileID)
	if err != nil {
		return AntigravityProfileSummary{}, mapProfileStoreError(err)
	}
	publicProfile, err := profileFromStore(profile)
	if err != nil {
		return AntigravityProfileSummary{}, err
	}
	binding, err := db.GetProfileCredentialBinding(ctx, profileID, agyconfig.ProviderID, agyconfig.CredentialSlot)
	if errors.Is(err, store.ErrNotFound) {
		return AntigravityProfileSummary{}, apperror.New(apperror.ProfileNotFound, "Antigravity profile not found").WithDetail("profile_id", profileID)
	}
	if err != nil {
		return AntigravityProfileSummary{}, apperror.Wrap(apperror.StoreStatusFailed, "failed to read Antigravity profile binding", err)
	}
	summary := AntigravityProfileSummary{
		Profile: publicProfile, ProviderID: agyconfig.ProviderID, CredentialID: binding.CredentialID,
		UpdatedAtUnixMS: max(profile.UpdatedAtUnixMS, binding.UpdatedAtUnixMS), Warnings: []string{},
	}
	credential, err := requireAntigravityCredential(ctx, db, binding.CredentialID)
	if err != nil {
		summary.Warnings = append(summary.Warnings, "Antigravity login is missing or invalid")
	} else {
		_, payload, _ := agyauth.Normalize([]byte(credential.PayloadJSON))
		summary.ExpiresAtUnixMS = agyauth.ExpiryUnixMS(payload)
		summary.UpdatedAtUnixMS = max(summary.UpdatedAtUnixMS, credential.UpdatedAtUnixMS)
	}
	references, err := db.CountProviderCredentialReferences(ctx, binding.CredentialID)
	if err != nil {
		return AntigravityProfileSummary{}, apperror.Wrap(apperror.StoreStatusFailed, "failed to count Antigravity login references", err)
	}
	summary.CredentialReferenceCount = references
	active, err := db.GetActiveState(ctx, store.ActiveStateScopeProvider, agyconfig.ProviderID)
	if err == nil && active.ProfileID == profileID {
		summary.Active = true
		summary.ActiveOperationID = active.OperationID
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		return AntigravityProfileSummary{}, apperror.Wrap(apperror.StoreStatusFailed, "failed to read active Antigravity profile", err)
	}
	return summary, nil
}

func requireAntigravityCredential(ctx context.Context, db *store.Store, credentialID string) (store.ProviderCredential, error) {
	return agyprofile.RequireCredential(ctx, db, credentialID)
}

func ensureAntigravityProvider(ctx context.Context, db *store.Store) error {
	return agyprofile.EnsureProvider(ctx, db)
}

func validateAntigravityProvider(provider store.Provider) error {
	return agyprofile.ValidateProvider(provider)
}

func requireAntigravityProvider(ctx context.Context, db *store.Store) (store.Provider, error) {
	provider, err := db.GetProvider(ctx, agyconfig.ProviderID)
	if err != nil {
		return store.Provider{}, mapProviderStoreError(err)
	}
	if err := validateAntigravityProvider(provider); err != nil {
		return store.Provider{}, err
	}
	if err := providerEnabled(provider); err != nil {
		return store.Provider{}, err
	}
	return provider, nil
}

func newAntigravityCredentialID(now time.Time) (string, error) {
	return agyprofile.NewCredentialID(now)
}

func providerMaintenanceMetadata(action, providerID, profileID, credentialID string) (string, error) {
	raw, err := json.Marshal(map[string]any{
		"action": action, "provider_id": providerID, "profile_id": profileID, "credential_id": credentialID,
	})
	return string(raw), err
}
