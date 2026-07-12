package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	agyauth "github.com/strahe/profiledeck/internal/antigravity/auth"
	agyconfig "github.com/strahe/profiledeck/internal/antigravity/config"
	"github.com/strahe/profiledeck/internal/store"
)

const antigravityCredentialRandomBytes = 8

type AntigravityDetectRequest struct {
	ConfigDir string
}

type AntigravityDetectResult struct {
	ProviderID             string   `json:"provider_id"`
	AdapterID              string   `json:"adapter_id"`
	CredentialStatus       string   `json:"credential_status"`
	ProfileDeckInitialized bool     `json:"profiledeck_initialized"`
	ProviderExists         bool     `json:"provider_exists"`
	ProviderCompatible     bool     `json:"provider_compatible"`
	Warnings               []string `json:"warnings"`
}

type AntigravityProfileSummary struct {
	Profile                  Profile  `json:"profile"`
	ProviderID               string   `json:"provider_id"`
	CredentialID             string   `json:"credential_id"`
	CredentialReferenceCount int      `json:"credential_reference_count"`
	ExpiresAtUnixMS          int64    `json:"expires_at_unix_ms,omitempty"`
	Active                   bool     `json:"active"`
	ActiveOperationID        string   `json:"active_operation_id,omitempty"`
	UpdatedAtUnixMS          int64    `json:"updated_at_unix_ms"`
	Warnings                 []string `json:"warnings,omitempty"`
}

type AntigravityProfileListResult struct {
	Profiles []AntigravityProfileSummary `json:"profiles"`
}

type AntigravityProfileDetail struct {
	Summary AntigravityProfileSummary `json:"summary"`
}

type ListAntigravityProfilesRequest struct {
	ConfigDir string
}

type GetAntigravityProfileRequest struct {
	ConfigDir string
	ProfileID string
}

type CreateAntigravityProfileRequest struct {
	ConfigDir   string
	ProfileID   string
	Name        *string
	Description *string
}

type UpdateAntigravityProfileRequest struct {
	ConfigDir   string
	ProfileID   string
	Name        *string
	Description *string
}

type SaveActiveAntigravityProfileRequest struct {
	ConfigDir string
}

type AntigravityProfileSaveResult struct {
	OperationID string                    `json:"operation_id"`
	Summary     AntigravityProfileSummary `json:"summary"`
	Warnings    []string                  `json:"warnings"`
}

type antigravityProviderMetadata struct {
	Preset        string `json:"preset"`
	PresetVersion int    `json:"preset_version"`
}

func antigravityTargetSpec() keyringTargetSpec {
	return keyringTargetSpec{
		ID: agyconfig.TargetID, Service: agyconfig.KeyringService,
		Account: agyconfig.KeyringAccount, Label: "Antigravity login",
	}
}

func AntigravityDetect(ctx context.Context, req AntigravityDetectRequest) (AntigravityDetectResult, error) {
	result := AntigravityDetectResult{
		ProviderID: agyconfig.ProviderID, AdapterID: agyconfig.AdapterID,
		CredentialStatus: "missing", ProviderCompatible: true, Warnings: []string{},
	}
	backend := targetBackends[targetBackendKeyring]
	snapshot, err := backend.Inspect(ctx, antigravityTargetSpec())
	if err != nil {
		result.CredentialStatus = "unavailable"
		result.Warnings = append(result.Warnings, "Antigravity login could not be read")
	} else if snapshot.Exists {
		if _, _, err := agyauth.Normalize([]byte(snapshot.Content)); err != nil {
			result.CredentialStatus = "invalid"
			result.Warnings = append(result.Warnings, "Antigravity login is not compatible with agy v2")
		} else {
			result.CredentialStatus = "valid"
		}
	}
	status, err := Status(ctx, StatusRequest(req))
	if err != nil {
		return AntigravityDetectResult{}, err
	}
	result.ProfileDeckInitialized = status.Initialized && status.SchemaHealthy
	if !result.ProfileDeckInitialized {
		return result, nil
	}
	db, err := openHealthyStore(ctx, req.ConfigDir, true)
	if err != nil {
		return AntigravityDetectResult{}, err
	}
	defer db.Close()
	provider, err := db.GetProvider(ctx, agyconfig.ProviderID)
	if errors.Is(err, store.ErrNotFound) {
		return result, nil
	}
	if err != nil {
		return AntigravityDetectResult{}, mapProviderStoreError(err)
	}
	result.ProviderExists = true
	if err := validateAntigravityProvider(provider); err != nil {
		result.ProviderCompatible = false
		result.Warnings = append(result.Warnings, "Existing Antigravity profiles are not compatible with agy v2")
	}
	return result, nil
}

func ListAntigravityProfiles(ctx context.Context, req ListAntigravityProfilesRequest) (AntigravityProfileListResult, error) {
	db, err := openHealthyStore(ctx, req.ConfigDir, true)
	if err != nil {
		return AntigravityProfileListResult{}, err
	}
	defer db.Close()
	summaries, err := listAntigravityProfileSummaries(ctx, db)
	if err != nil {
		return AntigravityProfileListResult{}, err
	}
	return AntigravityProfileListResult{Profiles: summaries}, nil
}

func GetAntigravityProfile(ctx context.Context, req GetAntigravityProfileRequest) (AntigravityProfileDetail, error) {
	profileID, appErr := validateID(req.ProfileID, ErrorProfileInvalid)
	if appErr != nil {
		return AntigravityProfileDetail{}, appErr
	}
	db, err := openHealthyStore(ctx, req.ConfigDir, true)
	if err != nil {
		return AntigravityProfileDetail{}, err
	}
	defer db.Close()
	summary, err := antigravityProfileSummary(ctx, db, profileID)
	if err != nil {
		return AntigravityProfileDetail{}, err
	}
	return AntigravityProfileDetail{Summary: summary}, nil
}

func CreateAntigravityProfile(ctx context.Context, req CreateAntigravityProfileRequest) (AntigravityProfileSaveResult, error) {
	profileID, appErr := validateID(req.ProfileID, ErrorProfileInvalid)
	if appErr != nil {
		return AntigravityProfileSaveResult{}, appErr
	}
	fields, appErr := normalizeManagedProfileFields(profileID, req.Name, req.Description)
	if appErr != nil {
		return AntigravityProfileSaveResult{}, appErr
	}
	db, lock, operationID, err := openLockedMaintenanceStore(ctx, req.ConfigDir, "antigravity-profile-create")
	if err != nil {
		return AntigravityProfileSaveResult{}, err
	}
	defer db.Close()
	defer lock.Release()
	backend := targetBackends[targetBackendKeyring]
	snapshot, err := backend.Inspect(ctx, antigravityTargetSpec())
	if err != nil {
		return AntigravityProfileSaveResult{}, err
	}
	if !snapshot.Exists {
		return AntigravityProfileSaveResult{}, NewError(ErrorAntigravityInvalid, "Antigravity is not signed in; sign in with agy first")
	}
	payload, _, err := agyauth.Normalize([]byte(snapshot.Content))
	if err != nil {
		return AntigravityProfileSaveResult{}, NewError(ErrorAntigravityInvalid, "Antigravity login is not compatible with agy v2")
	}
	credentialID, err := newAntigravityCredentialID(time.Now())
	if err != nil {
		return AntigravityProfileSaveResult{}, WrapError(ErrorOperationCreateFailed, "failed to create Antigravity login id", err)
	}
	err = db.WithTransaction(ctx, func(tx *store.Store) error {
		profile, profileErr := tx.GetProfile(ctx, profileID)
		hasProfile := profileErr == nil
		if profileErr != nil && !errors.Is(profileErr, store.ErrNotFound) {
			return mapProfileStoreError(profileErr)
		}
		if hasProfile {
			bindings, err := tx.ListProfileCredentialBindings(ctx, profileID, agyconfig.ProviderID)
			if err != nil {
				return WrapError(ErrorStoreStatusFailed, "failed to read Antigravity profile bindings", err)
			}
			configBindings, err := tx.ListProfileConfigSetBindings(ctx, profileID, agyconfig.ProviderID)
			if err != nil {
				return WrapError(ErrorStoreStatusFailed, "failed to read Antigravity profile config bindings", err)
			}
			if len(bindings) != 0 || len(configBindings) != 0 {
				return NewError(ErrorProfileAlreadyExists, "Antigravity profile already exists").WithDetail("profile_id", profileID)
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
			return WrapError(ErrorStoreStatusFailed, "failed to save Antigravity login", err)
		}
		if _, err := tx.UpsertProfileCredentialBinding(ctx, store.UpsertProfileCredentialBindingParams{
			ProfileID: profileID, ProviderID: agyconfig.ProviderID,
			SlotID: agyconfig.CredentialSlot, CredentialID: credentialID,
		}); err != nil {
			return WrapError(ErrorStoreStatusFailed, "failed to bind Antigravity login", err)
		}
		metadata, err := providerMaintenanceMetadata("antigravity-profile-create", agyconfig.ProviderID, profileID, credentialID)
		if err != nil {
			return err
		}
		_, err = tx.CreateAppliedMaintenanceOperation(ctx, store.CreateAppliedMaintenanceOperationParams{
			ID: operationID, ProfileID: profileID, ProviderID: agyconfig.ProviderID,
			MetadataJSON: metadata, SetActive: true,
		})
		return err
	})
	if err != nil {
		return AntigravityProfileSaveResult{}, err
	}
	summary, err := antigravityProfileSummary(ctx, db, profileID)
	if err != nil {
		return AntigravityProfileSaveResult{}, err
	}
	return AntigravityProfileSaveResult{OperationID: operationID, Summary: summary, Warnings: []string{}}, nil
}

func UpdateAntigravityProfile(ctx context.Context, req UpdateAntigravityProfileRequest) (AntigravityProfileDetail, error) {
	profileID, appErr := validateID(req.ProfileID, ErrorProfileInvalid)
	if appErr != nil {
		return AntigravityProfileDetail{}, appErr
	}
	if req.Name == nil && req.Description == nil {
		return AntigravityProfileDetail{}, NewError(ErrorProfileInvalid, "Antigravity profile update requires a name or description")
	}
	db, err := openHealthyStore(ctx, req.ConfigDir, false)
	if err != nil {
		return AntigravityProfileDetail{}, err
	}
	defer db.Close()
	if _, err := requireAntigravityProvider(ctx, db); err != nil {
		return AntigravityProfileDetail{}, err
	}
	if _, err := db.GetProfileCredentialBinding(ctx, profileID, agyconfig.ProviderID, agyconfig.CredentialSlot); err != nil {
		return AntigravityProfileDetail{}, NewError(ErrorProfileNotFound, "Antigravity profile not found").WithDetail("profile_id", profileID)
	}
	name := req.Name
	description := req.Description
	if name != nil {
		value, appErr := validateName(*name, ErrorProfileInvalid)
		if appErr != nil {
			return AntigravityProfileDetail{}, appErr
		}
		name = &value
	}
	if description != nil {
		value, appErr := validateDescription(*description, ErrorProfileInvalid)
		if appErr != nil {
			return AntigravityProfileDetail{}, appErr
		}
		description = &value
	}
	if _, err := db.UpdateProfile(ctx, store.UpdateProfileParams{ID: profileID, Name: name, Description: description}); err != nil {
		return AntigravityProfileDetail{}, mapProfileStoreError(err)
	}
	summary, err := antigravityProfileSummary(ctx, db, profileID)
	return AntigravityProfileDetail{Summary: summary}, err
}

func SaveActiveAntigravityProfile(ctx context.Context, req SaveActiveAntigravityProfileRequest) (AntigravityProfileSaveResult, error) {
	db, lock, operationID, err := openLockedMaintenanceStore(ctx, req.ConfigDir, "antigravity-profile-save-current")
	if err != nil {
		return AntigravityProfileSaveResult{}, err
	}
	defer db.Close()
	defer lock.Release()
	if _, err := requireAntigravityProvider(ctx, db); err != nil {
		return AntigravityProfileSaveResult{}, err
	}
	snapshot, err := targetBackends[targetBackendKeyring].Inspect(ctx, antigravityTargetSpec())
	if err != nil {
		return AntigravityProfileSaveResult{}, err
	}
	if !snapshot.Exists {
		return AntigravityProfileSaveResult{}, NewError(ErrorAntigravityInvalid, "Antigravity is not signed in; sign in with agy first")
	}
	payload, _, err := agyauth.Normalize([]byte(snapshot.Content))
	if err != nil {
		return AntigravityProfileSaveResult{}, NewError(ErrorAntigravityInvalid, "Antigravity login is not compatible with agy v2")
	}
	profileID := ""
	err = db.WithTransaction(ctx, func(tx *store.Store) error {
		active, err := tx.GetActiveState(ctx, store.ActiveStateScopeProvider, agyconfig.ProviderID)
		if errors.Is(err, store.ErrNotFound) {
			return NewError(ErrorProfileNotFound, "no active Antigravity profile")
		}
		if err != nil {
			return WrapError(ErrorStoreStatusFailed, "failed to read active Antigravity profile", err)
		}
		profileID = active.ProfileID
		binding, err := tx.GetProfileCredentialBinding(ctx, profileID, agyconfig.ProviderID, agyconfig.CredentialSlot)
		if err != nil {
			return NewError(ErrorAntigravityInvalid, "active Antigravity profile login binding is missing")
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
			return WrapError(ErrorStoreStatusFailed, "failed to save Antigravity login", err)
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
		return nil, WrapError(ErrorStoreStatusFailed, "failed to list Antigravity profile bindings", err)
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
		return AntigravityProfileSummary{}, NewError(ErrorProfileNotFound, "Antigravity profile not found").WithDetail("profile_id", profileID)
	}
	if err != nil {
		return AntigravityProfileSummary{}, WrapError(ErrorStoreStatusFailed, "failed to read Antigravity profile binding", err)
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
		return AntigravityProfileSummary{}, WrapError(ErrorStoreStatusFailed, "failed to count Antigravity login references", err)
	}
	summary.CredentialReferenceCount = references
	active, err := db.GetActiveState(ctx, store.ActiveStateScopeProvider, agyconfig.ProviderID)
	if err == nil && active.ProfileID == profileID {
		summary.Active = true
		summary.ActiveOperationID = active.OperationID
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		return AntigravityProfileSummary{}, WrapError(ErrorStoreStatusFailed, "failed to read active Antigravity profile", err)
	}
	return summary, nil
}

func requireAntigravityCredential(ctx context.Context, db *store.Store, credentialID string) (store.ProviderCredential, error) {
	credential, err := db.GetProviderCredential(ctx, credentialID)
	if err != nil {
		return store.ProviderCredential{}, WrapError(ErrorStoreStatusFailed, "Antigravity login not found", err)
	}
	if credential.ProviderID != agyconfig.ProviderID || credential.CredentialKind != agyconfig.CredentialKind {
		return store.ProviderCredential{}, NewError(ErrorAntigravityInvalid, "Antigravity login has unsupported kind")
	}
	// The stored hash authenticates the exact payload bytes. Normalization is a
	// separate schema check and must not let reformatted or altered storage pass.
	if sha256HexString(credential.PayloadJSON) != credential.PayloadSHA256 {
		return store.ProviderCredential{}, NewError(ErrorAntigravityInvalid, "Antigravity login payload hash is invalid")
	}
	if _, _, err := agyauth.Normalize([]byte(credential.PayloadJSON)); err != nil {
		return store.ProviderCredential{}, NewError(ErrorAntigravityInvalid, "Antigravity login is invalid")
	}
	return credential, nil
}

func ensureAntigravityProvider(ctx context.Context, db *store.Store) error {
	provider, err := db.GetProvider(ctx, agyconfig.ProviderID)
	if errors.Is(err, store.ErrNotFound) {
		metadata, _ := json.Marshal(antigravityProviderMetadata{Preset: agyconfig.PresetName, PresetVersion: agyconfig.PresetVersion})
		_, err = db.CreateProvider(ctx, store.CreateProviderParams{
			ID: agyconfig.ProviderID, Name: agyconfig.ProviderName, AdapterID: agyconfig.AdapterID,
			Enabled: true, MetadataJSON: string(metadata),
		})
		return err
	}
	if err != nil {
		return mapProviderStoreError(err)
	}
	if err := validateAntigravityProvider(provider); err != nil {
		return err
	}
	name := agyconfig.ProviderName
	enabled := true
	_, err = db.UpdateProvider(ctx, store.UpdateProviderParams{
		ID: provider.ID, Name: &name, Enabled: &enabled,
	})
	if err != nil {
		return mapProviderStoreError(err)
	}
	return nil
}

func validateAntigravityProvider(provider store.Provider) error {
	if provider.AdapterID != agyconfig.AdapterID {
		return NewError(ErrorAntigravityInvalid, "existing Antigravity provider uses a different adapter")
	}
	var metadata antigravityProviderMetadata
	if err := json.Unmarshal([]byte(provider.MetadataJSON), &metadata); err != nil || metadata.Preset != agyconfig.PresetName || metadata.PresetVersion != agyconfig.PresetVersion {
		return NewError(ErrorAntigravityInvalid, "existing Antigravity provider is incompatible with agy v2")
	}
	return nil
}

func requireAntigravityProvider(ctx context.Context, db *store.Store) (store.Provider, error) {
	provider, err := db.GetProvider(ctx, agyconfig.ProviderID)
	if err != nil {
		return store.Provider{}, mapProviderStoreError(err)
	}
	if err := validateAntigravityProvider(provider); err != nil {
		return store.Provider{}, err
	}
	return provider, nil
}

func newAntigravityCredentialID(now time.Time) (string, error) {
	randomBytes := make([]byte, antigravityCredentialRandomBytes)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", err
	}
	return fmt.Sprintf("agy_cred_%d_%s", now.UnixMilli(), hex.EncodeToString(randomBytes)), nil
}

func providerMaintenanceMetadata(action, providerID, profileID, credentialID string) (string, error) {
	raw, err := json.Marshal(map[string]any{
		"action": action, "provider_id": providerID, "profile_id": profileID, "credential_id": credentialID,
	})
	return string(raw), err
}
