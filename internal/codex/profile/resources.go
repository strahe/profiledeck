package profile

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"

	"github.com/strahe/profiledeck/internal/apperror"
	codexauth "github.com/strahe/profiledeck/internal/codex/auth"
	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	codexpreset "github.com/strahe/profiledeck/internal/codex/preset"
	"github.com/strahe/profiledeck/internal/profiletarget"
	"github.com/strahe/profiledeck/internal/store"
	switchtarget "github.com/strahe/profiledeck/internal/switching/target"
)

const credentialRandomBytes = 8

// Bindings identifies the two resources that form a Codex Profile.
type Bindings struct {
	ConfigSetID  string
	CredentialID string
}

func targetRecordFromStore(target store.ProfileTarget) TargetRecord {
	return TargetRecord{
		ProfileID: target.ProfileID, ProviderID: target.ProviderID, TargetID: target.TargetID,
		Path: target.Path, Format: target.Format, Strategy: target.Strategy,
		ValueJSON: target.ValueJSON, MetadataJSON: target.MetadataJSON,
	}
}

// CredentialIDFromTarget validates and reads an auth resource binding.
func CredentialIDFromTarget(target store.ProfileTarget) (string, error) {
	return CredentialIDFromRecord(targetRecordFromStore(target))
}

// ConfigSetIDFromTarget validates and reads a config resource binding.
func ConfigSetIDFromTarget(target store.ProfileTarget) (string, error) {
	return ConfigSetIDFromRecord(targetRecordFromStore(target))
}

// CredentialBindingCount reports how many Profiles bind a hidden Codex credential.
func CredentialBindingCount(ctx context.Context, db *store.Store, credentialID string) (int, error) {
	count, err := db.CountProviderCredentialReferences(ctx, credentialID)
	if err != nil {
		return 0, apperror.Wrap(apperror.StoreStatusFailed, "failed to count Codex login bindings", err)
	}
	return count, nil
}

// ConfigSetBindingCount reports how many Profiles bind a Codex Config Set.
func ConfigSetBindingCount(ctx context.Context, db *store.Store, configSetID string) (int, error) {
	count, err := db.CountProviderConfigSetReferences(ctx, configSetID)
	if err != nil {
		return 0, apperror.Wrap(apperror.StoreStatusFailed, "failed to count Codex config set bindings", err)
	}
	return count, nil
}

// UpsertAuthCredential stores a hidden, opaque Codex credential.
func UpsertAuthCredential(ctx context.Context, db *store.Store, credentialID, payload string) (store.ProviderCredential, error) {
	// Credential identity is ProfileDeck-owned and opaque. Codex tokens.account_id
	// is display metadata only and must never select, merge, or overwrite credentials.
	credential, err := db.UpsertProviderCredential(ctx, store.UpsertProviderCredentialParams{
		ID:             credentialID,
		ProviderID:     codexconfig.ProviderID,
		CredentialKind: codexpreset.CredentialKindAuthJSON,
		PayloadJSON:    payload,
		PayloadSHA256:  switchtarget.SHA256String(payload),
		MetadataJSON:   "{}",
	})
	if err != nil {
		return store.ProviderCredential{}, apperror.Wrap(apperror.StoreStatusFailed, "failed to store Codex auth credential", err)
	}
	return credential, nil
}

// UpsertConfigSet stores a named Codex TOML Config Set.
func UpsertConfigSet(ctx context.Context, db *store.Store, configSetID, name, description, payload string) (store.ProviderConfigSet, error) {
	configSet, err := db.UpsertProviderConfigSet(ctx, store.UpsertProviderConfigSetParams{
		ID:            configSetID,
		ProviderID:    codexconfig.ProviderID,
		ConfigKind:    codexpreset.ConfigSetKindTOML,
		Name:          name,
		Description:   description,
		PayloadText:   payload,
		PayloadSHA256: switchtarget.SHA256String(payload),
		MetadataJSON:  "{}",
	})
	if err != nil {
		return store.ProviderConfigSet{}, apperror.Wrap(apperror.StoreStatusFailed, "failed to store Codex config set", err)
	}
	return configSet, nil
}

// UpsertConfigBinding updates the typed Config Set slot and returns its working-copy target.
func UpsertConfigBinding(ctx context.Context, db *store.Store, profileID string, home codexconfig.Home, valueJSON, metadataJSON string) (store.ProfileTarget, error) {
	configSetID, err := codexpreset.ParseConfigSetBindingValueJSON(valueJSON)
	if err != nil {
		return store.ProfileTarget{}, apperror.Wrap(apperror.CodexInvalid, "Codex config binding is invalid", err)
	}
	binding, err := db.UpsertProfileConfigSetBinding(ctx, store.UpsertProfileConfigSetBindingParams{
		ProfileID: profileID, ProviderID: codexconfig.ProviderID,
		SlotID: codexpreset.ConfigSetSlotUserConfig, ConfigSetID: configSetID,
	})
	if err != nil {
		return store.ProfileTarget{}, apperror.Wrap(apperror.StoreStatusFailed, "failed to store Codex config binding", err)
	}
	return ConfigTargetFromBinding(home, binding, valueJSON, metadataJSON), nil
}

// UpsertAuthBinding updates the typed credential slot and returns its working-copy target.
func UpsertAuthBinding(ctx context.Context, db *store.Store, profileID string, home codexconfig.Home, valueJSON, metadataJSON string) (store.ProfileTarget, error) {
	credentialID, err := codexpreset.ParseCredentialBindingValueJSON(valueJSON)
	if err != nil {
		return store.ProfileTarget{}, apperror.Wrap(apperror.CodexInvalid, "Codex login binding is invalid", err)
	}
	binding, err := db.UpsertProfileCredentialBinding(ctx, store.UpsertProfileCredentialBindingParams{
		ProfileID: profileID, ProviderID: codexconfig.ProviderID,
		SlotID: codexpreset.CredentialSlotAuth, CredentialID: credentialID,
	})
	if err != nil {
		return store.ProfileTarget{}, apperror.Wrap(apperror.StoreStatusFailed, "failed to store Codex login binding", err)
	}
	return AuthTargetFromBinding(home, binding, valueJSON, metadataJSON), nil
}

// ConfigTargetFromBinding materializes the Config Set working-copy target.
func ConfigTargetFromBinding(home codexconfig.Home, binding store.ProfileConfigSetBinding, valueJSON, metadataJSON string) store.ProfileTarget {
	return store.ProfileTarget{
		ProfileID: binding.ProfileID, ProviderID: binding.ProviderID, TargetID: codexconfig.TargetID,
		Path: home.ConfigPath, PathKey: profiletarget.PathOwnershipKey(home.ConfigPath), Format: profiletarget.FormatTOML,
		Strategy: profiletarget.StrategyReplaceFile, ValueJSON: valueJSON, Enabled: true, MetadataJSON: metadataJSON,
		CreatedAtUnixMS: binding.CreatedAtUnixMS, UpdatedAtUnixMS: binding.UpdatedAtUnixMS,
	}
}

// AuthTargetFromBinding materializes the hidden credential working-copy target.
func AuthTargetFromBinding(home codexconfig.Home, binding store.ProfileCredentialBinding, valueJSON, metadataJSON string) store.ProfileTarget {
	return store.ProfileTarget{
		ProfileID: binding.ProfileID, ProviderID: binding.ProviderID, TargetID: codexconfig.AuthTargetID,
		Path: home.AuthPath, PathKey: profiletarget.PathOwnershipKey(home.AuthPath), Format: profiletarget.FormatJSON,
		Strategy: profiletarget.StrategyReplaceFile, ValueJSON: valueJSON, Enabled: true, MetadataJSON: metadataJSON,
		CreatedAtUnixMS: binding.CreatedAtUnixMS, UpdatedAtUnixMS: binding.UpdatedAtUnixMS,
	}
}

// BindingTargets materializes the typed Config Set and credential slots for one Profile.
func BindingTargets(ctx context.Context, db *store.Store, profileID string, home codexconfig.Home) ([]store.ProfileTarget, error) {
	credentialBindings, err := db.ListProfileCredentialBindings(ctx, profileID, codexconfig.ProviderID)
	if err != nil {
		return nil, apperror.Wrap(apperror.StoreStatusFailed, "failed to list Codex login bindings", err)
	}
	configBindings, err := db.ListProfileConfigSetBindings(ctx, profileID, codexconfig.ProviderID)
	if err != nil {
		return nil, apperror.Wrap(apperror.StoreStatusFailed, "failed to list Codex config bindings", err)
	}
	targets := make([]store.ProfileTarget, 0, len(credentialBindings)+len(configBindings))
	for _, binding := range configBindings {
		if binding.SlotID != codexpreset.ConfigSetSlotUserConfig {
			return nil, unsupportedBinding("Codex profile contains an unsupported config binding", binding.ProfileID, binding.SlotID)
		}
		valueJSON, err := codexpreset.ConfigSetBindingValueJSON(binding.ConfigSetID)
		if err != nil {
			return nil, err
		}
		metadataJSON, err := codexpreset.TargetMetadataJSON(codexconfig.TargetID, codexpreset.TargetModeConfigSet)
		if err != nil {
			return nil, err
		}
		targets = append(targets, ConfigTargetFromBinding(home, binding, valueJSON, metadataJSON))
	}
	for _, binding := range credentialBindings {
		if binding.SlotID != codexpreset.CredentialSlotAuth {
			return nil, unsupportedBinding("Codex profile contains an unsupported login binding", binding.ProfileID, binding.SlotID)
		}
		valueJSON, err := codexpreset.CredentialBindingValueJSON(binding.CredentialID)
		if err != nil {
			return nil, err
		}
		metadataJSON, err := codexpreset.TargetMetadataJSON(codexconfig.AuthTargetID, codexpreset.TargetModeCredential)
		if err != nil {
			return nil, err
		}
		targets = append(targets, AuthTargetFromBinding(home, binding, valueJSON, metadataJSON))
	}
	return targets, nil
}

// StoredHome reads the validated working-copy paths from the Codex provider metadata.
func StoredHome(ctx context.Context, db *store.Store) (codexconfig.Home, error) {
	provider, err := db.GetProvider(ctx, codexconfig.ProviderID)
	if err != nil {
		return codexconfig.Home{}, mapProviderStoreError(err)
	}
	if provider.AdapterID != codexconfig.AdapterID {
		return codexconfig.Home{}, apperror.New(apperror.CodexInvalid, "stored Codex provider adapter is invalid")
	}
	metadata, err := codexpreset.DecodeProviderMetadata(provider.MetadataJSON)
	if err != nil || !metadata.Compatible() {
		return codexconfig.Home{}, apperror.New(apperror.CodexInvalid, "stored Codex provider metadata is invalid")
	}
	return codexconfig.Home{Dir: metadata.CodexDir, ConfigPath: metadata.ConfigPath, AuthPath: metadata.AuthPath}, nil
}

// StoredBindingTargets materializes one Profile's bindings using persisted working-copy paths.
func StoredBindingTargets(ctx context.Context, db *store.Store, profileID string) ([]store.ProfileTarget, error) {
	home, err := StoredHome(ctx, db)
	if err != nil {
		return nil, err
	}
	return BindingTargets(ctx, db, profileID, home)
}

// AllStoredBindingTargets materializes all typed bindings when the stored provider is compatible.
func AllStoredBindingTargets(ctx context.Context, db *store.Store) ([]store.ProfileTarget, error) {
	provider, err := db.GetProvider(ctx, codexconfig.ProviderID)
	if errors.Is(err, store.ErrNotFound) {
		return []store.ProfileTarget{}, nil
	}
	if err != nil {
		return nil, mapProviderStoreError(err)
	}
	metadata, err := codexpreset.DecodeProviderMetadata(provider.MetadataJSON)
	if provider.AdapterID != codexconfig.AdapterID || err != nil || !metadata.Compatible() {
		// Detection and doctor still inspect typed binding presence when provider
		// metadata is damaged; this helper must not invent a working-copy path.
		return []store.ProfileTarget{}, nil
	}
	home := codexconfig.Home{Dir: metadata.CodexDir, ConfigPath: metadata.ConfigPath, AuthPath: metadata.AuthPath}
	credentialBindings, err := db.ListProfileCredentialBindingsByProvider(ctx, codexconfig.ProviderID)
	if err != nil {
		return nil, err
	}
	configBindings, err := db.ListProfileConfigSetBindingsByProvider(ctx, codexconfig.ProviderID)
	if err != nil {
		return nil, err
	}
	targets := make([]store.ProfileTarget, 0, len(credentialBindings)+len(configBindings))
	for _, binding := range configBindings {
		if binding.SlotID != codexpreset.ConfigSetSlotUserConfig {
			return nil, unsupportedBinding("Codex profile contains an unsupported config binding", binding.ProfileID, binding.SlotID)
		}
		valueJSON, _ := codexpreset.ConfigSetBindingValueJSON(binding.ConfigSetID)
		metadataJSON, _ := codexpreset.TargetMetadataJSON(codexconfig.TargetID, codexpreset.TargetModeConfigSet)
		targets = append(targets, ConfigTargetFromBinding(home, binding, valueJSON, metadataJSON))
	}
	for _, binding := range credentialBindings {
		if binding.SlotID != codexpreset.CredentialSlotAuth {
			return nil, unsupportedBinding("Codex profile contains an unsupported login binding", binding.ProfileID, binding.SlotID)
		}
		valueJSON, _ := codexpreset.CredentialBindingValueJSON(binding.CredentialID)
		metadataJSON, _ := codexpreset.TargetMetadataJSON(codexconfig.AuthTargetID, codexpreset.TargetModeCredential)
		targets = append(targets, AuthTargetFromBinding(home, binding, valueJSON, metadataJSON))
	}
	return targets, nil
}

// RequireAuthCredential verifies a hidden Codex auth resource before planning.
func RequireAuthCredential(ctx context.Context, db *store.Store, credentialID string) (store.ProviderCredential, error) {
	credential, err := db.GetProviderCredential(ctx, credentialID)
	if err != nil {
		return store.ProviderCredential{}, mapCredentialStoreError(err)
	}
	if err := ValidateCredentialRecord(CredentialRecord{
		ID: credential.ID, ProviderID: credential.ProviderID, CredentialKind: credential.CredentialKind,
		PayloadJSON: credential.PayloadJSON, PayloadSHA256: credential.PayloadSHA256, MetadataJSON: credential.MetadataJSON,
	}); err != nil {
		return store.ProviderCredential{}, err
	}
	return credential, nil
}

// RequireConfigSet verifies a Codex TOML Config Set before planning.
func RequireConfigSet(ctx context.Context, db *store.Store, configSetID string) (store.ProviderConfigSet, error) {
	configSet, err := db.GetProviderConfigSet(ctx, configSetID)
	if err != nil {
		return store.ProviderConfigSet{}, mapConfigSetStoreError(err)
	}
	if err := ValidateConfigSetRecord(ConfigSetRecord{
		ID: configSet.ID, ProviderID: configSet.ProviderID, ConfigKind: configSet.ConfigKind,
		Name: configSet.Name, Description: configSet.Description, PayloadText: configSet.PayloadText,
		PayloadSHA256: configSet.PayloadSHA256, MetadataJSON: configSet.MetadataJSON,
	}); err != nil {
		return store.ProviderConfigSet{}, err
	}
	return configSet, nil
}

// AuthPayloadsEqual compares JSON semantically and preserves JSON numbers.
func AuthPayloadsEqual(left, right string) bool {
	leftValue, leftErr := decodeAuthPayload(left)
	rightValue, rightErr := decodeAuthPayload(right)
	return leftErr == nil && rightErr == nil && reflect.DeepEqual(leftValue, rightValue)
}

func decodeAuthPayload(payload string) (any, error) {
	decoder := json.NewDecoder(strings.NewReader(payload))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("Codex auth payload contains multiple JSON values")
		}
		return nil, err
	}
	return value, nil
}

// ActiveBindings reads the active Profile bindings without treating account_id as identity.
func ActiveBindings(ctx context.Context, db *store.Store) (Bindings, []string, error) {
	active, err := db.GetActiveState(ctx, store.ActiveStateScopeProvider, codexconfig.ProviderID)
	if errors.Is(err, store.ErrNotFound) {
		return Bindings{}, nil, nil
	}
	if err != nil {
		return Bindings{}, nil, apperror.Wrap(apperror.StoreStatusFailed, "failed to read active Codex profile state", err)
	}
	credentialBindings, err := db.ListProfileCredentialBindings(ctx, active.ProfileID, codexconfig.ProviderID)
	if err != nil {
		return Bindings{}, nil, apperror.Wrap(apperror.StoreStatusFailed, "failed to read active Codex login bindings", err)
	}
	configBindings, err := db.ListProfileConfigSetBindings(ctx, active.ProfileID, codexconfig.ProviderID)
	if err != nil {
		return Bindings{}, nil, apperror.Wrap(apperror.StoreStatusFailed, "failed to read active Codex config bindings", err)
	}
	bindings := Bindings{}
	warnings := []string{}
	credentialBindingsUnsupported := false
	for _, binding := range credentialBindings {
		if binding.SlotID == codexpreset.CredentialSlotAuth && bindings.CredentialID == "" {
			bindings.CredentialID = binding.CredentialID
			continue
		}
		credentialBindingsUnsupported = true
	}
	if credentialBindingsUnsupported {
		bindings.CredentialID = ""
		warnings = append(warnings, "active Codex login bindings are unsupported; auth working copy will not be captured")
	}
	configBindingsUnsupported := false
	for _, binding := range configBindings {
		if binding.SlotID == codexpreset.ConfigSetSlotUserConfig && bindings.ConfigSetID == "" {
			bindings.ConfigSetID = binding.ConfigSetID
			continue
		}
		configBindingsUnsupported = true
	}
	if configBindingsUnsupported {
		bindings.ConfigSetID = ""
		warnings = append(warnings, "active Codex config bindings are unsupported; config working copy will not be captured")
	}
	if bindings.ConfigSetID == "" {
		warnings = append(warnings, "active Codex config binding is missing; config working copy will not be captured")
	}
	if bindings.CredentialID == "" {
		warnings = append(warnings, "active Codex login binding is missing; auth working copy will not be captured")
	}
	return bindings, UniqueStrings(warnings), nil
}

// LoadKnownResourceContent returns valid stored resources used to avoid cross-Profile capture.
func LoadKnownResourceContent(ctx context.Context, db *store.Store) ([]string, map[string]struct{}, error) {
	credentials, err := db.ListProviderCredentials(ctx, codexconfig.ProviderID)
	if err != nil {
		return nil, nil, apperror.Wrap(apperror.StoreStatusFailed, "failed to list Codex login resources", err)
	}
	authPayloads := make([]string, 0, len(credentials))
	for _, credential := range credentials {
		valid, credentialErr := RequireAuthCredential(ctx, db, credential.ID)
		if credentialErr == nil {
			authPayloads = append(authPayloads, valid.PayloadJSON)
		}
	}
	configSets, err := db.ListProviderConfigSets(ctx, codexconfig.ProviderID, codexpreset.ConfigSetKindTOML)
	if err != nil {
		return nil, nil, apperror.Wrap(apperror.StoreStatusFailed, "failed to list Codex config resources", err)
	}
	configHashes := make(map[string]struct{}, len(configSets))
	for _, configSet := range configSets {
		valid, configErr := RequireConfigSet(ctx, db, configSet.ID)
		if configErr == nil {
			configHashes[valid.PayloadSHA256] = struct{}{}
		}
	}
	return authPayloads, configHashes, nil
}

// ValidatePlanTarget validates the fixed two-target Codex preset contract.
func ValidatePlanTarget(provider store.Provider, target store.ProfileTarget) error {
	return ValidatePlanTargetRecord(
		ProviderRecord{ID: provider.ID, MetadataJSON: provider.MetadataJSON},
		targetRecordFromStore(target),
	)
}

// NewCredentialID allocates an opaque ProfileDeck-owned credential ID.
func NewCredentialID(now time.Time) (string, error) {
	randomBytes := make([]byte, credentialRandomBytes)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", err
	}
	return fmt.Sprintf("cred_%d_%s", now.UnixMilli(), hex.EncodeToString(randomBytes)), nil
}

// UniqueStrings preserves first-seen warning order.
func UniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func mapCredentialStoreError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, store.ErrNotFound) {
		return apperror.New(apperror.CodexInvalid, "Codex auth credential not found")
	}
	return apperror.Wrap(apperror.StoreStatusFailed, "Codex auth credential store operation failed", err)
}

func mapConfigSetStoreError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, store.ErrNotFound) {
		return apperror.New(apperror.CodexInvalid, "Codex config set not found")
	}
	if errors.Is(err, store.ErrInUse) {
		return apperror.New(apperror.ProfileInUse, "Codex config set is in use")
	}
	return apperror.Wrap(apperror.StoreStatusFailed, "Codex config set store operation failed", err)
}

func mapProviderStoreError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		return apperror.New(apperror.ProviderNotFound, "provider not found")
	case errors.Is(err, store.ErrAlreadyExists):
		return apperror.New(apperror.ProviderAlreadyExists, "provider already exists")
	case errors.Is(err, store.ErrInUse):
		return apperror.New(apperror.ProviderInUse, "provider is in use")
	default:
		return apperror.Wrap(apperror.StoreStatusFailed, "provider store operation failed", err)
	}
}

func mapProfileStoreError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		return apperror.New(apperror.ProfileNotFound, "profile not found")
	case errors.Is(err, store.ErrAlreadyExists):
		return apperror.New(apperror.ProfileAlreadyExists, "profile already exists")
	case errors.Is(err, store.ErrInUse):
		return apperror.New(apperror.ProfileInUse, "profile is in use")
	default:
		return apperror.Wrap(apperror.StoreStatusFailed, "profile store operation failed", err)
	}
}

func unsupportedBinding(message, profileID, slotID string) *apperror.Error {
	return apperror.New(apperror.CodexInvalid, message).
		WithDetail("profile_id", profileID).
		WithDetail("slot_id", slotID)
}

func authPayloadError(err error) *apperror.Error {
	appErr := apperror.Wrap(apperror.CodexInvalid, err.Error(), err)
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
