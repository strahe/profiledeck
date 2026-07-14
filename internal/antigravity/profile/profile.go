package profile

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	agyauth "github.com/strahe/profiledeck/internal/antigravity/auth"
	agyconfig "github.com/strahe/profiledeck/internal/antigravity/config"
	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/store"
	switchtarget "github.com/strahe/profiledeck/internal/switching/target"
)

const credentialRandomBytes = 8

type providerMetadata struct {
	Preset        string `json:"preset"`
	PresetVersion int    `json:"preset_version"`
}

// TargetSpec is the only physical target bound by an agy v2 Profile.
func TargetSpec() switchtarget.KeyringSpec {
	return switchtarget.KeyringSpec{
		ID: agyconfig.TargetID, Service: agyconfig.KeyringService,
		Account: agyconfig.KeyringAccount, Label: "Antigravity login",
	}
}

// RequireCredential validates the stored hidden credential before it influences a plan.
func RequireCredential(ctx context.Context, db *store.Store, credentialID string) (store.ProviderCredential, error) {
	credential, err := db.GetProviderCredential(ctx, credentialID)
	if err != nil {
		return store.ProviderCredential{}, apperror.Wrap(apperror.StoreStatusFailed, "Antigravity login not found", err)
	}
	if err := ValidateCredentialRecord(credential.ProviderID, credential.CredentialKind, credential.PayloadJSON, credential.PayloadSHA256); err != nil {
		return store.ProviderCredential{}, err
	}
	return credential, nil
}

func ValidateCredentialRecord(providerID, credentialKind, payload, payloadSHA256 string) error {
	if providerID != agyconfig.ProviderID || credentialKind != agyconfig.CredentialKind {
		return apperror.New(apperror.AntigravityInvalid, "Antigravity login has unsupported kind")
	}
	// The stored hash authenticates exact payload bytes; normalization only checks schema.
	if switchtarget.SHA256String(payload) != payloadSHA256 {
		return apperror.New(apperror.AntigravityInvalid, "Antigravity login payload hash is invalid")
	}
	if _, _, err := agyauth.Normalize([]byte(payload)); err != nil {
		return apperror.New(apperror.AntigravityInvalid, "Antigravity login is invalid")
	}
	return nil
}

// ValidateProvider rejects incompatible legacy or generic provider records.
func ValidateProvider(provider store.Provider) error {
	return ValidateProviderRecord(provider.AdapterID, provider.MetadataJSON)
}

func ValidateProviderRecord(adapterID, metadataJSON string) error {
	if adapterID != agyconfig.AdapterID {
		return apperror.New(apperror.AntigravityInvalid, "existing Antigravity provider uses a different adapter")
	}
	var metadata providerMetadata
	if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil || metadata.Preset != agyconfig.PresetName || metadata.PresetVersion != agyconfig.PresetVersion {
		return apperror.New(apperror.AntigravityInvalid, "existing Antigravity provider is incompatible with agy v2")
	}
	return nil
}

func RequireProvider(ctx context.Context, db *store.Store) (store.Provider, error) {
	provider, err := db.GetProvider(ctx, agyconfig.ProviderID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.Provider{}, apperror.New(apperror.ProviderNotFound, "provider not found")
		}
		return store.Provider{}, apperror.Wrap(apperror.StoreStatusFailed, "provider store operation failed", err)
	}
	if err := ValidateProvider(provider); err != nil {
		return store.Provider{}, err
	}
	return provider, nil
}

// EnsureProvider creates or repairs the fixed agy v2 provider metadata.
func EnsureProvider(ctx context.Context, db *store.Store) error {
	provider, err := db.GetProvider(ctx, agyconfig.ProviderID)
	if errors.Is(err, store.ErrNotFound) {
		metadata, marshalErr := json.Marshal(providerMetadata{Preset: agyconfig.PresetName, PresetVersion: agyconfig.PresetVersion})
		if marshalErr != nil {
			return apperror.Wrap(apperror.AntigravityInvalid, "failed to encode Antigravity provider metadata", marshalErr)
		}
		_, err = db.CreateProvider(ctx, store.CreateProviderParams{
			ID: agyconfig.ProviderID, Name: agyconfig.ProviderName, AdapterID: agyconfig.AdapterID,
			Enabled: true, MetadataJSON: string(metadata),
		})
		return err
	}
	if err != nil {
		return apperror.Wrap(apperror.StoreStatusFailed, "provider store operation failed", err)
	}
	if err := ValidateProvider(provider); err != nil {
		return err
	}
	if !provider.Enabled {
		return apperror.New(apperror.ProviderDisabled, "Antigravity Provider is disabled").WithDetail("provider_id", provider.ID)
	}
	name := agyconfig.ProviderName
	_, err = db.UpdateProvider(ctx, store.UpdateProviderParams{ID: provider.ID, Name: &name})
	if err != nil {
		return apperror.Wrap(apperror.StoreStatusFailed, "provider store operation failed", err)
	}
	return nil
}

func NewCredentialID(now time.Time) (string, error) {
	randomBytes := make([]byte, credentialRandomBytes)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", err
	}
	return fmt.Sprintf("agy_cred_%d_%s", now.UnixMilli(), hex.EncodeToString(randomBytes)), nil
}
