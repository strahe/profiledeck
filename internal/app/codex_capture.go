package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	codexauth "github.com/strahe/profiledeck/internal/codex/auth"
	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	codexpreset "github.com/strahe/profiledeck/internal/codex/preset"
	"github.com/strahe/profiledeck/internal/store"
)

const (
	codexCredentialRandomBytes = 8
)

func codexCredentialIDFromTarget(target store.ProfileTarget) (string, error) {
	metadata, err := codexpreset.DecodeTargetMetadata(target.MetadataJSON)
	if err != nil {
		return "", WrapError(ErrorStoreSchemaInvalid, "stored Codex auth target metadata is invalid", err).
			WithDetail("profile_id", target.ProfileID).
			WithDetail("target_id", target.TargetID)
	}
	if metadata.TargetKind != codexconfig.AuthTargetID || metadata.Mode != codexpreset.TargetModeCredential {
		return "", NewError(ErrorCodexInvalid, "stored Codex auth target is not a credential binding").
			WithDetail("profile_id", target.ProfileID).
			WithDetail("target_id", target.TargetID)
	}
	credentialID, err := codexpreset.ParseCredentialBindingValueJSON(target.ValueJSON)
	if err != nil {
		return "", WrapError(ErrorStoreSchemaInvalid, "stored Codex auth target value_json is invalid", err).
			WithDetail("profile_id", target.ProfileID).
			WithDetail("target_id", target.TargetID)
	}
	return credentialID, nil
}

func codexConfigSetIDFromTarget(target store.ProfileTarget) (string, error) {
	metadata, err := codexpreset.DecodeTargetMetadata(target.MetadataJSON)
	if err != nil {
		return "", WrapError(ErrorStoreSchemaInvalid, "stored Codex config target metadata is invalid", err).
			WithDetail("profile_id", target.ProfileID).
			WithDetail("target_id", target.TargetID)
	}
	if metadata.TargetKind != codexconfig.TargetID || metadata.Mode != codexpreset.TargetModeConfigSet {
		return "", NewError(ErrorCodexInvalid, "stored Codex config target is not a config set binding").
			WithDetail("profile_id", target.ProfileID).
			WithDetail("target_id", target.TargetID)
	}
	configSetID, err := codexpreset.ParseConfigSetBindingValueJSON(target.ValueJSON)
	if err != nil {
		return "", WrapError(ErrorStoreSchemaInvalid, "stored Codex config target value_json is invalid", err).
			WithDetail("profile_id", target.ProfileID).
			WithDetail("target_id", target.TargetID)
	}
	return configSetID, nil
}

func codexCredentialBindingCount(ctx context.Context, db *store.Store, credentialID string) (int, error) {
	targets, err := db.ListProfileTargetsByProvider(ctx, codexconfig.ProviderID)
	if err != nil {
		return 0, WrapError(ErrorStoreStatusFailed, "failed to list Codex auth targets", err)
	}
	count := 0
	for _, target := range targets {
		if target.TargetID != codexconfig.AuthTargetID || !target.Enabled {
			continue
		}
		current, err := codexpreset.ParseCredentialBindingValueJSON(target.ValueJSON)
		if err != nil {
			continue
		}
		if current == credentialID {
			count++
		}
	}
	return count, nil
}

func codexConfigSetBindingCount(ctx context.Context, db *store.Store, configSetID string) (int, error) {
	count, err := db.CountProviderConfigSetReferences(ctx, configSetID)
	if err != nil {
		return 0, WrapError(ErrorStoreStatusFailed, "failed to count Codex config set bindings", err)
	}
	return count, nil
}

func upsertCodexAuthCredential(ctx context.Context, db *store.Store, credentialID string, payload string) (store.ProviderCredential, error) {
	// Credential identity is ProfileDeck-owned and opaque. Codex tokens.account_id
	// is deliberately ignored here because it is not a stable unique identifier.
	credential, err := db.UpsertProviderCredential(ctx, store.UpsertProviderCredentialParams{
		ID:             credentialID,
		ProviderID:     codexconfig.ProviderID,
		CredentialKind: codexpreset.CredentialKindAuthJSON,
		PayloadJSON:    payload,
		PayloadSHA256:  sha256HexString(payload),
		MetadataJSON:   "{}",
	})
	if err != nil {
		return store.ProviderCredential{}, WrapError(ErrorStoreStatusFailed, "failed to store Codex auth credential", err)
	}
	return credential, nil
}

func upsertCodexConfigSet(ctx context.Context, db *store.Store, configSetID string, name string, description string, payload string) (store.ProviderConfigSet, error) {
	configSet, err := db.UpsertProviderConfigSet(ctx, store.UpsertProviderConfigSetParams{
		ID:            configSetID,
		ProviderID:    codexconfig.ProviderID,
		ConfigKind:    codexpreset.ConfigSetKindTOML,
		Name:          name,
		Description:   description,
		PayloadText:   payload,
		PayloadSHA256: sha256HexString(payload),
		MetadataJSON:  "{}",
	})
	if err != nil {
		return store.ProviderConfigSet{}, WrapError(ErrorStoreStatusFailed, "failed to store Codex config set", err)
	}
	return configSet, nil
}

func readCodexConfigSnapshot(home codexconfig.Home) (string, bool, *AppError) {
	snapshot, err := codexconfig.ReadSnapshot(home.ConfigPath)
	if err != nil {
		return "", false, codexConfigSnapshotAppError(home.ConfigPath, err)
	}
	return snapshot.Content, snapshot.Missing, nil
}

func readCodexAuthSnapshot(home codexconfig.Home) (codexauth.Snapshot, *AppError) {
	snapshot, err := codexauth.ReadSnapshot(home.AuthPath)
	if err != nil {
		if os.IsNotExist(err) {
			return codexauth.Snapshot{}, NewError(ErrorCodexInvalid, codexpreset.FileCredentialStoreHint).WithDetail("auth_path", home.AuthPath)
		}
		var pathErr *os.PathError
		if errors.As(err, &pathErr) {
			return codexauth.Snapshot{}, WrapError(ErrorCodexInvalid, "failed to read Codex auth", err).WithDetail("path", home.AuthPath)
		}
		return codexauth.Snapshot{}, codexAuthPayloadAppError(err).WithDetail("path", home.AuthPath)
	}
	return snapshot, nil
}

func codexConfigSnapshotAppError(path string, err error) *AppError {
	message := err.Error()
	switch {
	case strings.HasPrefix(message, "read Codex config:"):
		return WrapError(ErrorCodexInvalid, "failed to read Codex config", err).WithDetail("path", path)
	case strings.HasPrefix(message, "Codex config TOML is invalid:"):
		// TOML parser errors can include source lines, so the raw cause must not
		// cross an output boundary where configuration secrets could be exposed.
		return NewError(ErrorCodexInvalid, "Codex config TOML is invalid").WithDetail("path", path)
	case message == "Codex config is too large":
		return NewError(ErrorCodexInvalid, "Codex config is too large").WithDetail("path", path)
	default:
		return WrapError(ErrorCodexInvalid, message, err).WithDetail("path", path)
	}
}

func codexAuthPayloadAppError(err error) *AppError {
	appErr := WrapError(ErrorCodexInvalid, err.Error(), err)
	var fieldErr codexauth.FieldError
	if errors.As(err, &fieldErr) {
		appErr = appErr.WithDetail("field", fieldErr.Field)
	}
	var sizeErr codexauth.SizeError
	if errors.As(err, &sizeErr) {
		appErr = appErr.WithDetail("size_bytes", sizeErr.Size).WithDetail("max_bytes", sizeErr.Max)
	}
	return appErr
}

func upsertCodexProvider(ctx context.Context, db *store.Store, metadataJSON string, hasProvider bool) (store.Provider, error) {
	if !hasProvider {
		provider, err := db.CreateProvider(ctx, store.CreateProviderParams{
			ID:           codexconfig.ProviderID,
			Name:         codexpreset.ProviderName,
			AdapterID:    codexconfig.AdapterID,
			Enabled:      true,
			MetadataJSON: metadataJSON,
		})
		if err != nil {
			return store.Provider{}, mapProviderStoreError(err)
		}
		return provider, nil
	}
	enabled := true
	name := codexpreset.ProviderName
	provider, err := db.UpdateProvider(ctx, store.UpdateProviderParams{
		ID:           codexconfig.ProviderID,
		Name:         &name,
		Enabled:      &enabled,
		MetadataJSON: &metadataJSON,
	})
	if err != nil {
		return store.Provider{}, mapProviderStoreError(err)
	}
	return provider, nil
}

func upsertCodexProfile(ctx context.Context, db *store.Store, profileID string, fields codexProfileFields, hasProfile bool) (store.Profile, error) {
	if !hasProfile {
		profile, err := db.CreateProfile(ctx, store.CreateProfileParams{
			ID:           profileID,
			Name:         fields.CreateName,
			Description:  fields.CreateDescription,
			MetadataJSON: "{}",
		})
		if err != nil {
			return store.Profile{}, mapProfileStoreError(err)
		}
		return profile, nil
	}
	profile, err := db.GetProfile(ctx, profileID)
	if err != nil {
		return store.Profile{}, mapProfileStoreError(err)
	}
	if fields.UpdateName == nil && fields.UpdateDescription == nil {
		return profile, nil
	}
	profile, err = db.UpdateProfile(ctx, store.UpdateProfileParams{
		ID:          profileID,
		Name:        fields.UpdateName,
		Description: fields.UpdateDescription,
	})
	if err != nil {
		return store.Profile{}, mapProfileStoreError(err)
	}
	return profile, nil
}

func upsertCodexConfigTarget(ctx context.Context, db *store.Store, profileID string, home codexconfig.Home, valueJSON string, metadataJSON string, hasTarget bool) (store.ProfileTarget, error) {
	enabled := true
	if !hasTarget {
		target, err := db.CreateProfileTarget(ctx, store.CreateProfileTargetParams{
			ProfileID:    profileID,
			ProviderID:   codexconfig.ProviderID,
			TargetID:     codexconfig.TargetID,
			Path:         home.ConfigPath,
			PathKey:      targetPathOwnershipKey(home.ConfigPath),
			Format:       targetFormatTOML,
			Strategy:     targetStrategyReplaceFile,
			ValueJSON:    valueJSON,
			Enabled:      true,
			MetadataJSON: metadataJSON,
		})
		if err != nil {
			return store.ProfileTarget{}, mapTargetStoreError(err)
		}
		return target, nil
	}
	path := home.ConfigPath
	pathKey := targetPathOwnershipKey(home.ConfigPath)
	format := targetFormatTOML
	strategy := targetStrategyReplaceFile
	target, err := db.UpdateProfileTarget(ctx, store.UpdateProfileTargetParams{
		ProfileID:    profileID,
		ProviderID:   codexconfig.ProviderID,
		TargetID:     codexconfig.TargetID,
		Path:         &path,
		PathKey:      &pathKey,
		Format:       &format,
		Strategy:     &strategy,
		ValueJSON:    &valueJSON,
		Enabled:      &enabled,
		MetadataJSON: &metadataJSON,
	})
	if err != nil {
		return store.ProfileTarget{}, mapTargetStoreError(err)
	}
	return target, nil
}

func upsertCodexAuthTarget(ctx context.Context, db *store.Store, profileID string, home codexconfig.Home, valueJSON string, metadataJSON string, hasTarget bool) (store.ProfileTarget, error) {
	enabled := true
	if !hasTarget {
		target, err := db.CreateProfileTarget(ctx, store.CreateProfileTargetParams{
			ProfileID:    profileID,
			ProviderID:   codexconfig.ProviderID,
			TargetID:     codexconfig.AuthTargetID,
			Path:         home.AuthPath,
			PathKey:      targetPathOwnershipKey(home.AuthPath),
			Format:       targetFormatJSON,
			Strategy:     targetStrategyReplaceFile,
			ValueJSON:    valueJSON,
			Enabled:      true,
			MetadataJSON: metadataJSON,
		})
		if err != nil {
			return store.ProfileTarget{}, mapTargetStoreError(err)
		}
		return target, nil
	}
	path := home.AuthPath
	pathKey := targetPathOwnershipKey(home.AuthPath)
	format := targetFormatJSON
	strategy := targetStrategyReplaceFile
	target, err := db.UpdateProfileTarget(ctx, store.UpdateProfileTargetParams{
		ProfileID:    profileID,
		ProviderID:   codexconfig.ProviderID,
		TargetID:     codexconfig.AuthTargetID,
		Path:         &path,
		PathKey:      &pathKey,
		Format:       &format,
		Strategy:     &strategy,
		ValueJSON:    &valueJSON,
		Enabled:      &enabled,
		MetadataJSON: &metadataJSON,
	})
	if err != nil {
		return store.ProfileTarget{}, mapTargetStoreError(err)
	}
	return target, nil
}

func mapCodexCredentialStoreError(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return NewError(ErrorCodexInvalid, "Codex auth credential not found")
	}
	return WrapError(ErrorStoreStatusFailed, "Codex auth credential store operation failed", err)
}

func mapCodexConfigSetStoreError(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return NewError(ErrorCodexInvalid, "Codex config set not found")
	}
	if errors.Is(err, store.ErrInUse) {
		return NewError(ErrorProfileInUse, "Codex config set is in use")
	}
	return WrapError(ErrorStoreStatusFailed, "Codex config set store operation failed", err)
}

func requireCodexAuthCredential(ctx context.Context, db *store.Store, credentialID string) (store.ProviderCredential, error) {
	credential, err := db.GetProviderCredential(ctx, credentialID)
	if err != nil {
		return store.ProviderCredential{}, mapCodexCredentialStoreError(err)
	}
	if credential.ProviderID != codexconfig.ProviderID || credential.CredentialKind != codexpreset.CredentialKindAuthJSON {
		return store.ProviderCredential{}, NewError(ErrorCodexInvalid, "Codex auth credential has unsupported kind").
			WithDetail("credential_id", credentialID).
			WithDetail("credential_kind", credential.CredentialKind)
	}
	if _, err := codexauth.NormalizePayload([]byte(credential.PayloadJSON)); err != nil {
		return store.ProviderCredential{}, codexAuthPayloadAppError(err).WithDetail("credential_id", credentialID)
	}
	return credential, nil
}

func requireCodexConfigSet(ctx context.Context, db *store.Store, configSetID string) (store.ProviderConfigSet, error) {
	configSet, err := db.GetProviderConfigSet(ctx, configSetID)
	if err != nil {
		return store.ProviderConfigSet{}, mapCodexConfigSetStoreError(err)
	}
	if configSet.ProviderID != codexconfig.ProviderID || configSet.ConfigKind != codexpreset.ConfigSetKindTOML {
		return store.ProviderConfigSet{}, NewError(ErrorCodexInvalid, "Codex config set has unsupported kind").
			WithDetail("config_set_id", configSetID).
			WithDetail("config_kind", configSet.ConfigKind)
	}
	if sha256HexString(configSet.PayloadText) != configSet.PayloadSHA256 {
		return store.ProviderConfigSet{}, NewError(ErrorCodexInvalid, "Codex config set payload hash is invalid").
			WithDetail("config_set_id", configSetID)
	}
	if err := codexconfig.ValidateTOML(configSet.PayloadText); err != nil {
		return store.ProviderConfigSet{}, NewError(ErrorCodexInvalid, "Codex config set TOML is invalid").
			WithDetail("config_set_id", configSetID)
	}
	return configSet, nil
}

func newCodexCredentialID(now time.Time) (string, error) {
	randomBytes := make([]byte, codexCredentialRandomBytes)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", err
	}
	return fmt.Sprintf("cred_%d_%s", now.UnixMilli(), hex.EncodeToString(randomBytes)), nil
}
