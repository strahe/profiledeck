package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	claudecodeauth "github.com/strahe/profiledeck/internal/claudecode/auth"
	claudecodeconfig "github.com/strahe/profiledeck/internal/claudecode/config"
	"github.com/strahe/profiledeck/internal/store"
)

const claudeCodeCredentialRandomBytes = 8

type ClaudeCodeDetectRequest struct {
	ConfigDir                string
	AllowKeychainInteraction bool
}

type ClaudeCodeDetectResult struct {
	ProviderID                    string   `json:"provider_id"`
	AdapterID                     string   `json:"adapter_id"`
	CredentialStatus              string   `json:"credential_status"`
	ExpiresAtUnixMS               int64    `json:"expires_at_unix_ms,omitempty"`
	ProfileDeckInitialized        bool     `json:"profiledeck_initialized"`
	ProviderExists                bool     `json:"provider_exists"`
	ProviderEnabled               bool     `json:"provider_enabled"`
	ProviderCompatible            bool     `json:"provider_compatible"`
	KeychainAuthorizationRequired bool     `json:"keychain_authorization_required"`
	ObservedAuthOverrideHints     []string `json:"observed_auth_override_hints"`
	Warnings                      []string `json:"warnings"`
}

type ClaudeCodeProfileSummary struct {
	Profile                  Profile  `json:"profile"`
	ProviderID               string   `json:"provider_id"`
	CredentialID             string   `json:"credential_id"`
	CredentialStatus         string   `json:"credential_status"`
	CredentialReferenceCount int      `json:"credential_reference_count"`
	ExpiresAtUnixMS          int64    `json:"expires_at_unix_ms,omitempty"`
	Active                   bool     `json:"active"`
	ActiveOperationID        string   `json:"active_operation_id,omitempty"`
	UpdatedAtUnixMS          int64    `json:"updated_at_unix_ms"`
	Warnings                 []string `json:"warnings,omitempty"`
}

type ClaudeCodeProfileListResult struct {
	Profiles []ClaudeCodeProfileSummary `json:"profiles"`
}
type ClaudeCodeProfileDetail struct {
	Summary ClaudeCodeProfileSummary `json:"summary"`
}
type (
	ListClaudeCodeProfilesRequest  struct{ ConfigDir string }
	GetClaudeCodeProfileRequest    struct{ ConfigDir, ProfileID string }
	CreateClaudeCodeProfileRequest struct {
		ConfigDir   string
		ProfileID   string
		Name        *string
		Description *string
	}
)

type UpdateClaudeCodeProfileRequest struct {
	ConfigDir   string
	ProfileID   string
	Name        *string
	Description *string
}
type SaveActiveClaudeCodeProfileRequest struct {
	ConfigDir     string
	ConfirmShared bool
}
type ClaudeCodeProfileSaveResult struct {
	OperationID string                   `json:"operation_id"`
	Summary     ClaudeCodeProfileSummary `json:"summary"`
	Warnings    []string                 `json:"warnings"`
}

type claudeCodeProviderMetadata struct {
	Preset             string `json:"preset"`
	PresetVersion      int    `json:"preset_version"`
	Storage            string `json:"storage"`
	Path               string `json:"path,omitempty"`
	Service            string `json:"service,omitempty"`
	Account            string `json:"account,omitempty"`
	LocatorFingerprint string `json:"locator_fingerprint"`
}

func ClaudeCodeDetect(ctx context.Context, req ClaudeCodeDetectRequest) (ClaudeCodeDetectResult, error) {
	result := ClaudeCodeDetectResult{
		ProviderID: claudecodeconfig.ProviderID, AdapterID: claudecodeconfig.AdapterID,
		CredentialStatus: claudecodeauth.StatusMissing, ProviderEnabled: true, ProviderCompatible: true,
		ObservedAuthOverrideHints: observedClaudeCodeAuthOverrideHints(), Warnings: []string{},
	}
	status, err := Status(ctx, StatusRequest{ConfigDir: req.ConfigDir})
	if err != nil {
		return ClaudeCodeDetectResult{}, err
	}
	result.ProfileDeckInitialized = status.Initialized && status.SchemaHealthy
	var spec targetSpec
	if result.ProfileDeckInitialized {
		db, err := openHealthyStore(ctx, req.ConfigDir, true)
		if err != nil {
			return ClaudeCodeDetectResult{}, err
		}
		defer db.Close()
		provider, providerErr := db.GetProvider(ctx, claudecodeconfig.ProviderID)
		if providerErr == nil {
			result.ProviderExists = true
			result.ProviderEnabled = provider.Enabled
			metadata, validationErr := validateClaudeCodeProvider(provider)
			if validationErr != nil {
				result.ProviderCompatible = false
				result.Warnings = append(result.Warnings, "Saved Claude Code credential target is incompatible")
				return result, nil
			}
			spec = claudeCodeTargetSpec(metadata)
			result.Warnings = append(result.Warnings, claudeCodeLocatorWarnings(metadata)...)
		} else if !errors.Is(providerErr, store.ErrNotFound) {
			return ClaudeCodeDetectResult{}, mapProviderStoreError(providerErr)
		}
	}
	if spec == nil {
		locator, err := claudecodeconfig.ResolveLocator()
		if err != nil {
			result.CredentialStatus = claudecodeauth.StatusUnavailable
			result.Warnings = append(result.Warnings, "Claude Code credential target is unavailable")
			return result, nil
		}
		metadata := newClaudeCodeProviderMetadata(locator)
		spec = claudeCodeTargetSpec(metadata)
	}
	snapshot, err := inspectClaudeCodeTarget(ctx, spec, req.AllowKeychainInteraction)
	if err != nil {
		if isClaudeCodeKeychainAuthorizationRequired(err) {
			result.CredentialStatus = claudecodeauth.StatusUnavailable
			result.KeychainAuthorizationRequired = true
			result.Warnings = append(result.Warnings, "Claude Code Keychain authorization is required")
			return result, nil
		}
		var appErr *AppError
		if errors.As(err, &appErr) && (appErr.Code == ErrorClaudeCodeInvalid || appErr.Code == ErrorTargetChanged) {
			result.CredentialStatus = claudecodeauth.StatusInvalid
			result.Warnings = append(result.Warnings, "Claude Code login target is invalid")
		} else {
			result.CredentialStatus = claudecodeauth.StatusUnavailable
			result.Warnings = append(result.Warnings, "Claude Code login could not be read")
		}
		return result, nil
	}
	if !snapshot.Exists {
		return result, nil
	}
	if snapshot.IsSymlink {
		result.CredentialStatus = claudecodeauth.StatusInvalid
		result.Warnings = append(result.Warnings, "Claude Code credential file is a symbolic link and will not be used")
		return result, nil
	}
	_, info, err := claudecodeauth.Normalize([]byte(snapshot.Content))
	if err != nil {
		if claudecodeauth.IsKind(err, claudecodeauth.ErrorUnsupportedAccountType) {
			result.CredentialStatus = claudecodeauth.StatusUnsupported
			result.Warnings = append(result.Warnings, "Claude Code does not report an active Pro, Max, Team, or Enterprise subscription for this login")
		} else {
			result.CredentialStatus = claudecodeauth.StatusInvalid
			result.Warnings = append(result.Warnings, "Claude Code login is invalid")
		}
		return result, nil
	}
	result.CredentialStatus = claudecodeauth.StatusAt(info, time.Now())
	result.ExpiresAtUnixMS = info.ExpiresAtUnixMS
	if info.ExpiryUnknown {
		result.Warnings = append(result.Warnings, "Claude Code login expiry could not be determined")
	}
	return result, nil
}

func ListClaudeCodeProfiles(ctx context.Context, req ListClaudeCodeProfilesRequest) (ClaudeCodeProfileListResult, error) {
	db, err := openHealthyStore(ctx, req.ConfigDir, true)
	if err != nil {
		return ClaudeCodeProfileListResult{}, err
	}
	defer db.Close()
	profiles, err := listClaudeCodeProfileSummaries(ctx, db)
	return ClaudeCodeProfileListResult{Profiles: profiles}, err
}

func GetClaudeCodeProfile(ctx context.Context, req GetClaudeCodeProfileRequest) (ClaudeCodeProfileDetail, error) {
	profileID, appErr := validateID(req.ProfileID, ErrorProfileInvalid)
	if appErr != nil {
		return ClaudeCodeProfileDetail{}, appErr
	}
	db, err := openHealthyStore(ctx, req.ConfigDir, true)
	if err != nil {
		return ClaudeCodeProfileDetail{}, err
	}
	defer db.Close()
	summary, err := claudeCodeProfileSummary(ctx, db, profileID)
	return ClaudeCodeProfileDetail{Summary: summary}, err
}

func CreateClaudeCodeProfile(ctx context.Context, req CreateClaudeCodeProfileRequest) (ClaudeCodeProfileSaveResult, error) {
	profileID, appErr := validateID(req.ProfileID, ErrorProfileInvalid)
	if appErr != nil {
		return ClaudeCodeProfileSaveResult{}, appErr
	}
	fields, appErr := normalizeManagedProfileFields(profileID, req.Name, req.Description)
	if appErr != nil {
		return ClaudeCodeProfileSaveResult{}, appErr
	}
	db, lock, operationID, err := openLockedMaintenanceStore(ctx, req.ConfigDir, "claude-code-profile-create")
	if err != nil {
		return ClaudeCodeProfileSaveResult{}, err
	}
	defer db.Close()
	defer lock.Release()
	metadata, _, err := resolveClaudeCodeProviderMetadata(ctx, db)
	if err != nil {
		return ClaudeCodeProfileSaveResult{}, err
	}
	spec := claudeCodeTargetSpec(metadata)
	snapshot, err := targetBackends[spec.BackendID()].Inspect(ctx, spec)
	if err != nil {
		return ClaudeCodeProfileSaveResult{}, err
	}
	payload, _, err := normalizeCurrentClaudeCodeCredential(snapshot)
	if err != nil {
		return ClaudeCodeProfileSaveResult{}, err
	}
	credentialID, err := newClaudeCodeCredentialID(time.Now())
	if err != nil {
		return ClaudeCodeProfileSaveResult{}, WrapError(ErrorOperationCreateFailed, "failed to create Claude Code login id", err)
	}
	// Captures commit only the exact working copy that was inspected. This
	// narrows the unavoidable external-writer race before the database commit.
	if err := targetBackends[spec.BackendID()].Verify(ctx, spec, snapshot); err != nil {
		return ClaudeCodeProfileSaveResult{}, err
	}
	err = db.WithTransaction(ctx, func(tx *store.Store) error {
		profile, profileErr := tx.GetProfile(ctx, profileID)
		hasProfile := profileErr == nil
		if profileErr != nil && !errors.Is(profileErr, store.ErrNotFound) {
			return mapProfileStoreError(profileErr)
		}
		if hasProfile {
			bindings, err := tx.ListProfileCredentialBindings(ctx, profileID, claudecodeconfig.ProviderID)
			if err != nil {
				return WrapError(ErrorStoreStatusFailed, "failed to read Claude Code Profile bindings", err)
			}
			configBindings, err := tx.ListProfileConfigSetBindings(ctx, profileID, claudecodeconfig.ProviderID)
			if err != nil {
				return WrapError(ErrorStoreStatusFailed, "failed to read Claude Code Profile config bindings", err)
			}
			if len(bindings) != 0 || len(configBindings) != 0 {
				return NewError(ErrorProfileAlreadyExists, "Claude Code Profile already exists").WithDetail("profile_id", profileID)
			}
			targets, err := tx.ListProfileTargets(ctx, profileID, claudecodeconfig.ProviderID, true)
			if err != nil {
				return WrapError(ErrorStoreStatusFailed, "failed to read Claude Code Profile targets", err)
			}
			if len(targets) != 0 {
				return NewError(ErrorClaudeCodeInvalid, "existing Profile contains unsupported Claude Code targets").WithDetail("profile_id", profileID)
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
			return WrapError(ErrorStoreStatusFailed, "failed to save Claude Code login", err)
		}
		if _, err := tx.UpsertProfileCredentialBinding(ctx, store.UpsertProfileCredentialBindingParams{
			ProfileID: profileID, ProviderID: claudecodeconfig.ProviderID, SlotID: claudecodeconfig.CredentialSlot, CredentialID: credentialID,
		}); err != nil {
			return WrapError(ErrorStoreStatusFailed, "failed to bind Claude Code login", err)
		}
		operationMetadata, err := providerMaintenanceMetadata("claude-code-profile-create", claudecodeconfig.ProviderID, profileID, credentialID)
		if err != nil {
			return err
		}
		_, err = tx.CreateAppliedMaintenanceOperation(ctx, store.CreateAppliedMaintenanceOperationParams{
			ID: operationID, ProfileID: profileID, ProviderID: claudecodeconfig.ProviderID, MetadataJSON: operationMetadata, SetActive: true,
		})
		return err
	})
	if err != nil {
		return ClaudeCodeProfileSaveResult{}, err
	}
	summary, err := claudeCodeProfileSummary(ctx, db, profileID)
	return ClaudeCodeProfileSaveResult{OperationID: operationID, Summary: summary, Warnings: []string{}}, err
}

func UpdateClaudeCodeProfile(ctx context.Context, req UpdateClaudeCodeProfileRequest) (ClaudeCodeProfileDetail, error) {
	profileID, appErr := validateID(req.ProfileID, ErrorProfileInvalid)
	if appErr != nil {
		return ClaudeCodeProfileDetail{}, appErr
	}
	if req.Name == nil && req.Description == nil {
		return ClaudeCodeProfileDetail{}, NewError(ErrorProfileInvalid, "Claude Code Profile update requires a name or description")
	}
	db, err := openHealthyStore(ctx, req.ConfigDir, false)
	if err != nil {
		return ClaudeCodeProfileDetail{}, err
	}
	defer db.Close()
	if _, err := requireClaudeCodeProvider(ctx, db); err != nil {
		return ClaudeCodeProfileDetail{}, err
	}
	// Validate the complete typed-binding shape before mutating the shared
	// Profile so a corrupt Claude Code binding cannot produce a partial update.
	if _, err := claudeCodeProfileSummary(ctx, db, profileID); err != nil {
		return ClaudeCodeProfileDetail{}, err
	}
	name, description := req.Name, req.Description
	if name != nil {
		value, appErr := validateName(*name, ErrorProfileInvalid)
		if appErr != nil {
			return ClaudeCodeProfileDetail{}, appErr
		}
		name = &value
	}
	if description != nil {
		value, appErr := validateDescription(*description, ErrorProfileInvalid)
		if appErr != nil {
			return ClaudeCodeProfileDetail{}, appErr
		}
		description = &value
	}
	if _, err := db.UpdateProfile(ctx, store.UpdateProfileParams{ID: profileID, Name: name, Description: description}); err != nil {
		return ClaudeCodeProfileDetail{}, mapProfileStoreError(err)
	}
	summary, err := claudeCodeProfileSummary(ctx, db, profileID)
	return ClaudeCodeProfileDetail{Summary: summary}, err
}

func SaveActiveClaudeCodeProfile(ctx context.Context, req SaveActiveClaudeCodeProfileRequest) (ClaudeCodeProfileSaveResult, error) {
	db, lock, operationID, err := openLockedMaintenanceStore(ctx, req.ConfigDir, "claude-code-profile-save-current")
	if err != nil {
		return ClaudeCodeProfileSaveResult{}, err
	}
	defer db.Close()
	defer lock.Release()
	provider, err := requireClaudeCodeProvider(ctx, db)
	if err != nil {
		return ClaudeCodeProfileSaveResult{}, err
	}
	if !provider.Enabled {
		return ClaudeCodeProfileSaveResult{}, NewError(ErrorProviderDisabled, "Claude Code provider is disabled")
	}
	metadata, err := validateClaudeCodeProvider(provider)
	if err != nil {
		return ClaudeCodeProfileSaveResult{}, err
	}
	spec := claudeCodeTargetSpec(metadata)
	snapshot, err := targetBackends[spec.BackendID()].Inspect(ctx, spec)
	if err != nil {
		return ClaudeCodeProfileSaveResult{}, err
	}
	payload, _, err := normalizeCurrentClaudeCodeCredential(snapshot)
	if err != nil {
		return ClaudeCodeProfileSaveResult{}, err
	}
	active, err := db.GetActiveState(ctx, store.ActiveStateScopeProvider, claudecodeconfig.ProviderID)
	if errors.Is(err, store.ErrNotFound) {
		return ClaudeCodeProfileSaveResult{}, NewError(ErrorProfileNotFound, "no active Claude Code Profile")
	}
	if err != nil {
		return ClaudeCodeProfileSaveResult{}, WrapError(ErrorStoreStatusFailed, "failed to read active Claude Code Profile", err)
	}
	if _, err := claudeCodeProfileSummary(ctx, db, active.ProfileID); err != nil {
		return ClaudeCodeProfileSaveResult{}, err
	}
	binding, err := db.GetProfileCredentialBinding(ctx, active.ProfileID, claudecodeconfig.ProviderID, claudecodeconfig.CredentialSlot)
	if err != nil {
		return ClaudeCodeProfileSaveResult{}, NewError(ErrorClaudeCodeInvalid, "active Claude Code Profile login binding is missing")
	}
	credential, err := requireClaudeCodeCredential(ctx, db, binding.CredentialID)
	if err != nil {
		return ClaudeCodeProfileSaveResult{}, err
	}
	references, err := db.CountProviderCredentialReferences(ctx, credential.ID)
	if err != nil {
		return ClaudeCodeProfileSaveResult{}, WrapError(ErrorStoreStatusFailed, "failed to count Claude Code login references", err)
	}
	if references > 1 && !req.ConfirmShared {
		return ClaudeCodeProfileSaveResult{}, NewError(
			ErrorConfirmationRequired,
			fmt.Sprintf("saving the current Claude Code login changes %d Profiles; confirm with --yes", references),
		).WithDetail("affected_profiles", references)
	}
	// Re-read the same file state or Keychain item reference immediately before
	// committing the explicit capture to its hidden credential.
	if err := targetBackends[spec.BackendID()].Verify(ctx, spec, snapshot); err != nil {
		return ClaudeCodeProfileSaveResult{}, err
	}
	err = db.WithTransaction(ctx, func(tx *store.Store) error {
		if _, err := tx.UpsertProviderCredential(ctx, store.UpsertProviderCredentialParams{
			ID: credential.ID, ProviderID: credential.ProviderID, CredentialKind: credential.CredentialKind,
			PayloadJSON: payload, PayloadSHA256: sha256HexString(payload), MetadataJSON: credential.MetadataJSON,
		}); err != nil {
			return WrapError(ErrorStoreStatusFailed, "failed to save Claude Code login", err)
		}
		operationMetadata, err := providerMaintenanceMetadata("claude-code-profile-save-current", claudecodeconfig.ProviderID, active.ProfileID, credential.ID)
		if err != nil {
			return err
		}
		_, err = tx.CreateAppliedMaintenanceOperation(ctx, store.CreateAppliedMaintenanceOperationParams{
			ID: operationID, ProfileID: active.ProfileID, ProviderID: claudecodeconfig.ProviderID, MetadataJSON: operationMetadata,
		})
		return err
	})
	if err != nil {
		return ClaudeCodeProfileSaveResult{}, err
	}
	summary, err := claudeCodeProfileSummary(ctx, db, active.ProfileID)
	warnings := []string{}
	if references > 1 {
		warnings = append(warnings, "shared Claude Code login state updated")
	}
	return ClaudeCodeProfileSaveResult{OperationID: operationID, Summary: summary, Warnings: warnings}, err
}

func normalizeCurrentClaudeCodeCredential(snapshot targetSnapshot) (string, claudecodeauth.Info, error) {
	if !snapshot.Exists {
		return "", claudecodeauth.Info{}, NewError(ErrorClaudeCodeInvalid, "Claude Code is not signed in; run Claude Code /login first")
	}
	if snapshot.IsSymlink {
		return "", claudecodeauth.Info{}, NewError(ErrorClaudeCodeInvalid, "Claude Code credential file is a symbolic link and will not be used")
	}
	payload, info, err := claudecodeauth.Normalize([]byte(snapshot.Content))
	if err != nil {
		if claudecodeauth.IsKind(err, claudecodeauth.ErrorUnsupportedAccountType) {
			return "", claudecodeauth.Info{}, NewError(ErrorClaudeCodeInvalid, "Claude Code login does not report an active Pro, Max, Team, or Enterprise subscription").WithDetail("reason", "unsupported_account_type")
		}
		return "", claudecodeauth.Info{}, NewError(ErrorClaudeCodeInvalid, "Claude Code login is invalid")
	}
	return payload, info, nil
}

func listClaudeCodeProfileSummaries(ctx context.Context, db *store.Store) ([]ClaudeCodeProfileSummary, error) {
	bindings, err := db.ListProfileCredentialBindingsByProvider(ctx, claudecodeconfig.ProviderID)
	if err != nil {
		return nil, WrapError(ErrorStoreStatusFailed, "failed to list Claude Code Profile bindings", err)
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
		return ClaudeCodeProfileSummary{}, NewError(ErrorProfileNotFound, "Claude Code Profile not found").WithDetail("profile_id", profileID)
	}
	if err != nil {
		return ClaudeCodeProfileSummary{}, WrapError(ErrorStoreStatusFailed, "failed to read Claude Code Profile binding", err)
	}
	bindings, err := db.ListProfileCredentialBindings(ctx, profileID, claudecodeconfig.ProviderID)
	if err != nil {
		return ClaudeCodeProfileSummary{}, WrapError(ErrorStoreStatusFailed, "failed to read Claude Code Profile bindings", err)
	}
	configBindings, err := db.ListProfileConfigSetBindings(ctx, profileID, claudecodeconfig.ProviderID)
	if err != nil {
		return ClaudeCodeProfileSummary{}, WrapError(ErrorStoreStatusFailed, "failed to read Claude Code Profile config bindings", err)
	}
	targets, err := db.ListProfileTargets(ctx, profileID, claudecodeconfig.ProviderID, true)
	if err != nil {
		return ClaudeCodeProfileSummary{}, WrapError(ErrorStoreStatusFailed, "failed to read Claude Code Profile targets", err)
	}
	if len(bindings) != 1 || bindings[0].SlotID != claudecodeconfig.CredentialSlot || bindings[0].CredentialID != binding.CredentialID || len(configBindings) != 0 || len(targets) != 0 {
		return ClaudeCodeProfileSummary{}, NewError(ErrorClaudeCodeInvalid, "Claude Code Profile bindings are missing or unsupported").WithDetail("profile_id", profileID)
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
		return ClaudeCodeProfileSummary{}, WrapError(ErrorStoreStatusFailed, "failed to count Claude Code login references", err)
	}
	active, err := db.GetActiveState(ctx, store.ActiveStateScopeProvider, claudecodeconfig.ProviderID)
	if err == nil && active.ProfileID == profileID {
		summary.Active, summary.ActiveOperationID = true, active.OperationID
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		return ClaudeCodeProfileSummary{}, WrapError(ErrorStoreStatusFailed, "failed to read active Claude Code Profile", err)
	}
	return summary, nil
}

func requireClaudeCodeCredential(ctx context.Context, db *store.Store, credentialID string) (store.ProviderCredential, error) {
	credential, err := db.GetProviderCredential(ctx, credentialID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.ProviderCredential{}, NewError(ErrorClaudeCodeInvalid, "Claude Code login is missing")
		}
		return store.ProviderCredential{}, WrapError(ErrorStoreStatusFailed, "Claude Code login not found", err)
	}
	if credential.ProviderID != claudecodeconfig.ProviderID || credential.CredentialKind != claudecodeconfig.CredentialKind {
		return store.ProviderCredential{}, NewError(ErrorClaudeCodeInvalid, "Claude Code login has unsupported kind")
	}
	if sha256HexString(credential.PayloadJSON) != credential.PayloadSHA256 {
		return store.ProviderCredential{}, NewError(ErrorClaudeCodeInvalid, "Claude Code login payload hash is invalid")
	}
	normalized, _, err := claudecodeauth.Normalize([]byte(credential.PayloadJSON))
	if err != nil || normalized != credential.PayloadJSON {
		return store.ProviderCredential{}, NewError(ErrorClaudeCodeInvalid, "Claude Code login is invalid")
	}
	return credential, nil
}

func resolveClaudeCodeProviderMetadata(ctx context.Context, db *store.Store) (claudeCodeProviderMetadata, bool, error) {
	provider, err := db.GetProvider(ctx, claudecodeconfig.ProviderID)
	if err == nil {
		metadata, err := validateClaudeCodeProvider(provider)
		return metadata, true, err
	}
	if !errors.Is(err, store.ErrNotFound) {
		return claudeCodeProviderMetadata{}, false, mapProviderStoreError(err)
	}
	locator, err := claudecodeconfig.ResolveLocator()
	if err != nil {
		return claudeCodeProviderMetadata{}, false, NewError(ErrorClaudeCodeInvalid, "Claude Code credential target is unavailable")
	}
	return newClaudeCodeProviderMetadata(locator), false, nil
}

func newClaudeCodeProviderMetadata(locator claudecodeconfig.Locator) claudeCodeProviderMetadata {
	metadata := claudeCodeProviderMetadata{
		Preset: claudecodeconfig.PresetName, PresetVersion: claudecodeconfig.PresetVersion,
		Storage: locator.Storage, Path: locator.Path, Service: locator.Service, Account: locator.Account,
	}
	metadata.LocatorFingerprint = claudeCodeLocatorFingerprint(metadata)
	return metadata
}

func createClaudeCodeProvider(ctx context.Context, db *store.Store, metadata claudeCodeProviderMetadata) error {
	raw, err := json.Marshal(metadata)
	if err != nil {
		return WrapError(ErrorStoreStatusFailed, "failed to encode Claude Code provider", err)
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
	name, enabled := claudecodeconfig.ProviderName, true
	if _, err := db.UpdateProvider(ctx, store.UpdateProviderParams{ID: provider.ID, Name: &name, Enabled: &enabled}); err != nil {
		return mapProviderStoreError(err)
	}
	return nil
}

func requireClaudeCodeProvider(ctx context.Context, db *store.Store) (store.Provider, error) {
	provider, err := db.GetProvider(ctx, claudecodeconfig.ProviderID)
	if err != nil {
		return store.Provider{}, mapProviderStoreError(err)
	}
	if _, err := validateClaudeCodeProvider(provider); err != nil {
		return store.Provider{}, err
	}
	return provider, nil
}

func validateClaudeCodeProvider(provider store.Provider) (claudeCodeProviderMetadata, error) {
	if provider.ID != claudecodeconfig.ProviderID || provider.AdapterID != claudecodeconfig.AdapterID {
		return claudeCodeProviderMetadata{}, NewError(ErrorClaudeCodeInvalid, "existing Claude Code provider uses a different adapter")
	}
	var metadata claudeCodeProviderMetadata
	decoder := json.NewDecoder(strings.NewReader(provider.MetadataJSON))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&metadata); err != nil || metadata.Preset != claudecodeconfig.PresetName || metadata.PresetVersion != claudecodeconfig.PresetVersion {
		return claudeCodeProviderMetadata{}, NewError(ErrorClaudeCodeInvalid, "existing Claude Code provider is incompatible")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return claudeCodeProviderMetadata{}, NewError(ErrorClaudeCodeInvalid, "existing Claude Code provider is incompatible")
	}
	if metadata.LocatorFingerprint == "" || metadata.LocatorFingerprint != claudeCodeLocatorFingerprint(metadata) {
		return claudeCodeProviderMetadata{}, NewError(ErrorClaudeCodeInvalid, "Claude Code credential locator is invalid")
	}
	switch metadata.Storage {
	case claudecodeconfig.StorageFile:
		if !filepath.IsAbs(metadata.Path) || filepath.Clean(metadata.Path) != metadata.Path || filepath.Base(metadata.Path) != claudecodeconfig.CredentialsFile || metadata.Service != "" || metadata.Account != "" {
			return claudeCodeProviderMetadata{}, NewError(ErrorClaudeCodeInvalid, "Claude Code credential file locator is invalid")
		}
	case claudecodeconfig.StorageKeychain:
		if metadata.Path != "" || metadata.Service != claudecodeconfig.KeychainService || strings.TrimSpace(metadata.Account) == "" || metadata.Account != strings.TrimSpace(metadata.Account) {
			return claudeCodeProviderMetadata{}, NewError(ErrorClaudeCodeInvalid, "Claude Code Keychain locator is invalid")
		}
	default:
		return claudeCodeProviderMetadata{}, NewError(ErrorClaudeCodeInvalid, "Claude Code credential storage is unsupported")
	}
	return metadata, nil
}

func claudeCodeTargetSpec(metadata claudeCodeProviderMetadata) targetSpec {
	if metadata.Storage == claudecodeconfig.StorageKeychain {
		return claudeCodeKeychainTargetSpec{ID: claudecodeconfig.TargetID, Service: metadata.Service, Account: metadata.Account, Label: "Claude Code login"}
	}
	return fileTargetSpec{ID: claudecodeconfig.TargetID, Path: metadata.Path, NeedsContent: true, Secret: true, Label: "Claude Code login"}
}

func claudeCodeLocatorFingerprint(metadata claudeCodeProviderMetadata) string {
	return sha256HexString(strings.Join([]string{metadata.Storage, metadata.Path, metadata.Service, metadata.Account}, "\x00"))
}

func claudeCodeLocatorWarnings(saved claudeCodeProviderMetadata) []string {
	current, err := claudecodeconfig.ResolveLocator()
	if err != nil {
		return []string{"This ProfileDeck process could not resolve the current Claude Code credential target; the saved target will still be used"}
	}
	observed := newClaudeCodeProviderMetadata(current)
	if observed.LocatorFingerprint != saved.LocatorFingerprint {
		if saved.Storage == claudecodeconfig.StorageFile && observed.Storage == claudecodeconfig.StorageFile {
			return []string{"This process observes a different CLAUDE_CONFIG_DIR; ProfileDeck will continue using the saved Claude Code credential target"}
		}
		return []string{"This process observes a different Claude Code credential locator; ProfileDeck will continue using the saved target"}
	}
	return nil
}

func observedClaudeCodeAuthOverrideHints() []string {
	names := []string{
		"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY",
		"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN",
	}
	result := []string{}
	for _, name := range names {
		if _, ok := os.LookupEnv(name); ok {
			result = append(result, name)
		}
	}
	return result
}

func newClaudeCodeCredentialID(now time.Time) (string, error) {
	randomBytes := make([]byte, claudeCodeCredentialRandomBytes)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", err
	}
	return fmt.Sprintf("claude_code_cred_%d_%s", now.UnixMilli(), hex.EncodeToString(randomBytes)), nil
}
