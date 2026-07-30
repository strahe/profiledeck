package profile

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/strahe/profiledeck/internal/apperror"
	grokauth "github.com/strahe/profiledeck/internal/grokbuild/auth"
	grokconfig "github.com/strahe/profiledeck/internal/grokbuild/config"
	grokpreset "github.com/strahe/profiledeck/internal/grokbuild/preset"
	"github.com/strahe/profiledeck/internal/profiletarget"
	"github.com/strahe/profiledeck/internal/store"
	switchtarget "github.com/strahe/profiledeck/internal/switching/target"
)

const credentialRandomBytes = 8

type Bindings struct {
	ConfigSetID  string
	CredentialID string
}

type ProfileFields struct {
	CreateName        string
	CreateDescription string
	UpdateName        *string
	UpdateDescription *string
}

type ProviderRecord struct {
	ID           string
	MetadataJSON string
}

type TargetRecord struct {
	ProfileID    string
	ProviderID   string
	TargetID     string
	Path         string
	Format       string
	Strategy     string
	ValueJSON    string
	MetadataJSON string
}

type CredentialRecord struct {
	ID             string
	ProviderID     string
	CredentialKind string
	PayloadJSON    string
	PayloadSHA256  string
	MetadataJSON   string
}

type ConfigSetRecord struct {
	ID            string
	ProviderID    string
	ConfigKind    string
	Name          string
	Description   string
	PayloadText   string
	PayloadSHA256 string
	MetadataJSON  string
}

type CredentialBindingRecord struct {
	ProfileID    string
	ProviderID   string
	SlotID       string
	CredentialID string
}

type ConfigSetBindingRecord struct {
	ProfileID   string
	ProviderID  string
	SlotID      string
	ConfigSetID string
}

func UpsertProvider(
	ctx context.Context,
	db *store.Store,
	metadataJSON string,
	exists bool,
) (store.Provider, error) {
	if !exists {
		value, err := db.CreateProvider(ctx, store.CreateProviderParams{
			ID: grokconfig.ProviderID, Name: grokpreset.ProviderName,
			AdapterID: grokconfig.AdapterID, MetadataJSON: metadataJSON,
		})
		return value, mapProviderStoreError(err)
	}
	name := grokpreset.ProviderName
	value, err := db.UpdateProvider(ctx, store.UpdateProviderParams{
		ID: grokconfig.ProviderID, Name: &name, MetadataJSON: &metadataJSON,
	})
	return value, mapProviderStoreError(err)
}

func UpsertProfile(
	ctx context.Context,
	db *store.Store,
	profileID string,
	fields ProfileFields,
	exists bool,
) (store.Profile, error) {
	if !exists {
		value, err := db.CreateProfile(ctx, store.CreateProfileParams{
			ID: profileID, Name: fields.CreateName, Description: fields.CreateDescription, MetadataJSON: "{}",
		})
		return value, mapProfileStoreError(err)
	}
	value, err := db.GetProfile(ctx, profileID)
	if err != nil {
		return store.Profile{}, mapProfileStoreError(err)
	}
	if fields.UpdateName == nil && fields.UpdateDescription == nil {
		return value, nil
	}
	value, err = db.UpdateProfile(ctx, store.UpdateProfileParams{
		ID: profileID, Name: fields.UpdateName, Description: fields.UpdateDescription,
	})
	return value, mapProfileStoreError(err)
}

func UpsertAuthCredential(
	ctx context.Context,
	db *store.Store,
	credentialID string,
	payload string,
) (store.ProviderCredential, error) {
	if _, err := grokauth.Validate([]byte(payload)); err != nil {
		return store.ProviderCredential{}, authPayloadError(err)
	}
	value, err := db.UpsertProviderCredential(ctx, store.UpsertProviderCredentialParams{
		ID: credentialID, ProviderID: grokconfig.ProviderID,
		CredentialKind: grokpreset.CredentialKindAuthJSON,
		PayloadJSON:    payload, PayloadSHA256: switchtarget.SHA256String(payload), MetadataJSON: "{}",
	})
	if err != nil {
		return store.ProviderCredential{}, apperror.Wrap(apperror.StoreStatusFailed, "failed to store Grok Build login", err)
	}
	return value, nil
}

func UpsertConfigSet(
	ctx context.Context,
	db *store.Store,
	configSetID string,
	name string,
	description string,
	payload string,
) (store.ProviderConfigSet, error) {
	if err := grokconfig.ValidateTOML(payload); err != nil {
		return store.ProviderConfigSet{}, apperror.New(apperror.GrokBuildInvalid, "Grok Build Config Set TOML is invalid")
	}
	value, err := db.UpsertProviderConfigSet(ctx, store.UpsertProviderConfigSetParams{
		ID: configSetID, ProviderID: grokconfig.ProviderID,
		ConfigKind: grokpreset.ConfigSetKindTOML, Name: name, Description: description,
		PayloadText: payload, PayloadSHA256: switchtarget.SHA256String(payload), MetadataJSON: "{}",
	})
	if err != nil {
		return store.ProviderConfigSet{}, apperror.Wrap(apperror.StoreStatusFailed, "failed to store Grok Build Config Set", err)
	}
	return value, nil
}

func UpsertConfigBinding(
	ctx context.Context,
	db *store.Store,
	profileID string,
	home grokconfig.Home,
	configSetID string,
) (store.ProfileTarget, error) {
	binding, err := db.UpsertProfileConfigSetBinding(ctx, store.UpsertProfileConfigSetBindingParams{
		ProfileID: profileID, ProviderID: grokconfig.ProviderID,
		SlotID: grokpreset.ConfigSetSlotUserConfig, ConfigSetID: configSetID,
	})
	if err != nil {
		return store.ProfileTarget{}, apperror.Wrap(apperror.StoreStatusFailed, "failed to store Grok Build Config Set binding", err)
	}
	return configTargetFromBinding(home, binding)
}

func UpsertAuthBinding(
	ctx context.Context,
	db *store.Store,
	profileID string,
	home grokconfig.Home,
	credentialID string,
) (store.ProfileTarget, error) {
	binding, err := db.UpsertProfileCredentialBinding(ctx, store.UpsertProfileCredentialBindingParams{
		ProfileID: profileID, ProviderID: grokconfig.ProviderID,
		SlotID: grokpreset.CredentialSlotAuth, CredentialID: credentialID,
	})
	if err != nil {
		return store.ProfileTarget{}, apperror.Wrap(apperror.StoreStatusFailed, "failed to store Grok Build login binding", err)
	}
	return authTargetFromBinding(home, binding)
}

func BindingTargets(
	ctx context.Context,
	db *store.Store,
	profileID string,
	home grokconfig.Home,
) ([]store.ProfileTarget, error) {
	credentialBindings, err := db.ListProfileCredentialBindings(ctx, profileID, grokconfig.ProviderID)
	if err != nil {
		return nil, apperror.Wrap(apperror.StoreStatusFailed, "failed to list Grok Build login bindings", err)
	}
	configBindings, err := db.ListProfileConfigSetBindings(ctx, profileID, grokconfig.ProviderID)
	if err != nil {
		return nil, apperror.Wrap(apperror.StoreStatusFailed, "failed to list Grok Build Config Set bindings", err)
	}
	targets := make([]store.ProfileTarget, 0, len(credentialBindings)+len(configBindings))
	for _, binding := range configBindings {
		if binding.SlotID != grokpreset.ConfigSetSlotUserConfig {
			return nil, unsupportedBinding(binding.ProfileID, binding.SlotID)
		}
		target, err := configTargetFromBinding(home, binding)
		if err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	for _, binding := range credentialBindings {
		if binding.SlotID != grokpreset.CredentialSlotAuth {
			return nil, unsupportedBinding(binding.ProfileID, binding.SlotID)
		}
		target, err := authTargetFromBinding(home, binding)
		if err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	return targets, nil
}

func StoredHome(ctx context.Context, db *store.Store) (grokconfig.Home, error) {
	provider, err := db.GetProvider(ctx, grokconfig.ProviderID)
	if err != nil {
		return grokconfig.Home{}, mapProviderStoreError(err)
	}
	if provider.AdapterID != grokconfig.AdapterID {
		return grokconfig.Home{}, apperror.New(apperror.GrokBuildInvalid, "stored Grok Build Provider adapter is invalid")
	}
	metadata, err := grokpreset.DecodeProviderMetadata(provider.MetadataJSON)
	if err != nil || !metadata.Compatible() {
		return grokconfig.Home{}, apperror.New(apperror.GrokBuildInvalid, "stored Grok Build Provider metadata is invalid")
	}
	return grokconfig.Home{
		Dir: metadata.GrokHome, ConfigPath: metadata.ConfigPath, AuthPath: metadata.AuthPath,
		LockPath: filepath.Join(metadata.GrokHome, grokconfig.LockFileName),
	}, nil
}

func StoredBindingTargets(ctx context.Context, db *store.Store, profileID string) ([]store.ProfileTarget, error) {
	home, err := StoredHome(ctx, db)
	if err != nil {
		return nil, err
	}
	return BindingTargets(ctx, db, profileID, home)
}

func AllStoredBindingTargets(ctx context.Context, db *store.Store) ([]store.ProfileTarget, error) {
	home, err := StoredHome(ctx, db)
	if errors.Is(err, store.ErrNotFound) {
		return []store.ProfileTarget{}, nil
	}
	if err != nil {
		var appErr *apperror.Error
		if errors.As(err, &appErr) && appErr.Code == apperror.ProviderNotFound {
			return []store.ProfileTarget{}, nil
		}
		return nil, err
	}
	credentialBindings, err := db.ListProfileCredentialBindingsByProvider(ctx, grokconfig.ProviderID)
	if err != nil {
		return nil, err
	}
	configBindings, err := db.ListProfileConfigSetBindingsByProvider(ctx, grokconfig.ProviderID)
	if err != nil {
		return nil, err
	}
	targets := make([]store.ProfileTarget, 0, len(credentialBindings)+len(configBindings))
	for _, binding := range configBindings {
		if binding.SlotID != grokpreset.ConfigSetSlotUserConfig {
			return nil, unsupportedBinding(binding.ProfileID, binding.SlotID)
		}
		target, err := configTargetFromBinding(home, binding)
		if err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	for _, binding := range credentialBindings {
		if binding.SlotID != grokpreset.CredentialSlotAuth {
			return nil, unsupportedBinding(binding.ProfileID, binding.SlotID)
		}
		target, err := authTargetFromBinding(home, binding)
		if err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	return targets, nil
}

func configTargetFromBinding(
	home grokconfig.Home,
	binding store.ProfileConfigSetBinding,
) (store.ProfileTarget, error) {
	valueJSON, err := grokpreset.ConfigSetBindingValueJSON(binding.ConfigSetID)
	if err != nil {
		return store.ProfileTarget{}, err
	}
	metadataJSON, err := grokpreset.TargetMetadataJSON(grokconfig.ConfigTargetID, grokpreset.TargetModeConfigSet)
	if err != nil {
		return store.ProfileTarget{}, err
	}
	return store.ProfileTarget{
		ProfileID: binding.ProfileID, ProviderID: binding.ProviderID, TargetID: grokconfig.ConfigTargetID,
		Path: home.ConfigPath, PathKey: profiletarget.PathOwnershipKey(home.ConfigPath),
		Format: profiletarget.FormatTOML, Strategy: profiletarget.StrategyReplaceFile,
		ValueJSON: valueJSON, Enabled: true, MetadataJSON: metadataJSON,
		CreatedAtUnixMS: binding.CreatedAtUnixMS, UpdatedAtUnixMS: binding.UpdatedAtUnixMS,
	}, nil
}

func authTargetFromBinding(
	home grokconfig.Home,
	binding store.ProfileCredentialBinding,
) (store.ProfileTarget, error) {
	valueJSON, err := grokpreset.CredentialBindingValueJSON(binding.CredentialID)
	if err != nil {
		return store.ProfileTarget{}, err
	}
	metadataJSON, err := grokpreset.TargetMetadataJSON(grokconfig.AuthTargetID, grokpreset.TargetModeCredential)
	if err != nil {
		return store.ProfileTarget{}, err
	}
	return store.ProfileTarget{
		ProfileID: binding.ProfileID, ProviderID: binding.ProviderID, TargetID: grokconfig.AuthTargetID,
		Path: home.AuthPath, PathKey: profiletarget.PathOwnershipKey(home.AuthPath),
		Format: profiletarget.FormatJSON, Strategy: profiletarget.StrategyReplaceFile,
		ValueJSON: valueJSON, Enabled: true, MetadataJSON: metadataJSON,
		CreatedAtUnixMS: binding.CreatedAtUnixMS, UpdatedAtUnixMS: binding.UpdatedAtUnixMS,
	}, nil
}

func BindingTargetRecords(
	provider ProviderRecord,
	credentialBindings []CredentialBindingRecord,
	configBindings []ConfigSetBindingRecord,
) ([]TargetRecord, error) {
	metadata, err := grokpreset.DecodeProviderMetadata(provider.MetadataJSON)
	if err != nil || !metadata.Compatible() {
		return nil, apperror.New(apperror.GrokBuildInvalid, "Grok Build Provider metadata is invalid")
	}
	home := grokconfig.Home{
		Dir: metadata.GrokHome, ConfigPath: metadata.ConfigPath, AuthPath: metadata.AuthPath,
	}
	targets := make([]TargetRecord, 0, len(credentialBindings)+len(configBindings))
	for _, binding := range configBindings {
		if binding.SlotID != grokpreset.ConfigSetSlotUserConfig {
			return nil, unsupportedBinding(binding.ProfileID, binding.SlotID)
		}
		valueJSON, _ := grokpreset.ConfigSetBindingValueJSON(binding.ConfigSetID)
		metadataJSON, _ := grokpreset.TargetMetadataJSON(grokconfig.ConfigTargetID, grokpreset.TargetModeConfigSet)
		targets = append(targets, TargetRecord{
			ProfileID: binding.ProfileID, ProviderID: binding.ProviderID, TargetID: grokconfig.ConfigTargetID,
			Path: home.ConfigPath, Format: profiletarget.FormatTOML, Strategy: profiletarget.StrategyReplaceFile,
			ValueJSON: valueJSON, MetadataJSON: metadataJSON,
		})
	}
	for _, binding := range credentialBindings {
		if binding.SlotID != grokpreset.CredentialSlotAuth {
			return nil, unsupportedBinding(binding.ProfileID, binding.SlotID)
		}
		valueJSON, _ := grokpreset.CredentialBindingValueJSON(binding.CredentialID)
		metadataJSON, _ := grokpreset.TargetMetadataJSON(grokconfig.AuthTargetID, grokpreset.TargetModeCredential)
		targets = append(targets, TargetRecord{
			ProfileID: binding.ProfileID, ProviderID: binding.ProviderID, TargetID: grokconfig.AuthTargetID,
			Path: home.AuthPath, Format: profiletarget.FormatJSON, Strategy: profiletarget.StrategyReplaceFile,
			ValueJSON: valueJSON, MetadataJSON: metadataJSON,
		})
	}
	return targets, nil
}

func CredentialIDFromTarget(target store.ProfileTarget) (string, error) {
	return CredentialIDFromRecord(targetRecordFromStore(target))
}

func ConfigSetIDFromTarget(target store.ProfileTarget) (string, error) {
	return ConfigSetIDFromRecord(targetRecordFromStore(target))
}

func CredentialIDFromRecord(target TargetRecord) (string, error) {
	metadata, err := grokpreset.DecodeTargetMetadata(target.MetadataJSON)
	if err != nil || metadata.TargetKind != grokconfig.AuthTargetID ||
		metadata.Mode != grokpreset.TargetModeCredential {
		return "", apperror.New(apperror.StoreSchemaInvalid, "stored Grok Build login binding is invalid")
	}
	id, err := grokpreset.ParseCredentialBindingValueJSON(target.ValueJSON)
	if err != nil {
		return "", apperror.New(apperror.StoreSchemaInvalid, "stored Grok Build login binding is invalid")
	}
	return id, nil
}

func ConfigSetIDFromRecord(target TargetRecord) (string, error) {
	metadata, err := grokpreset.DecodeTargetMetadata(target.MetadataJSON)
	if err != nil || metadata.TargetKind != grokconfig.ConfigTargetID ||
		metadata.Mode != grokpreset.TargetModeConfigSet {
		return "", apperror.New(apperror.StoreSchemaInvalid, "stored Grok Build Config Set binding is invalid")
	}
	id, err := grokpreset.ParseConfigSetBindingValueJSON(target.ValueJSON)
	if err != nil {
		return "", apperror.New(apperror.StoreSchemaInvalid, "stored Grok Build Config Set binding is invalid")
	}
	return id, nil
}

func RequireAuthCredential(
	ctx context.Context,
	db *store.Store,
	credentialID string,
) (store.ProviderCredential, error) {
	value, err := db.GetProviderCredential(ctx, credentialID)
	if err != nil {
		return store.ProviderCredential{}, mapCredentialStoreError(err)
	}
	if err := ValidateCredentialRecord(CredentialRecord{
		ID: value.ID, ProviderID: value.ProviderID, CredentialKind: value.CredentialKind,
		PayloadJSON: value.PayloadJSON, PayloadSHA256: value.PayloadSHA256, MetadataJSON: value.MetadataJSON,
	}); err != nil {
		return store.ProviderCredential{}, err
	}
	return value, nil
}

func RequireConfigSet(
	ctx context.Context,
	db *store.Store,
	configSetID string,
) (store.ProviderConfigSet, error) {
	value, err := db.GetProviderConfigSet(ctx, grokconfig.ProviderID, configSetID)
	if err != nil {
		return store.ProviderConfigSet{}, mapConfigSetStoreError(err)
	}
	if err := ValidateConfigSetRecord(ConfigSetRecord{
		ID: value.ID, ProviderID: value.ProviderID, ConfigKind: value.ConfigKind,
		Name: value.Name, Description: value.Description, PayloadText: value.PayloadText,
		PayloadSHA256: value.PayloadSHA256, MetadataJSON: value.MetadataJSON,
	}); err != nil {
		return store.ProviderConfigSet{}, err
	}
	return value, nil
}

func ValidateCredentialRecord(credential CredentialRecord) error {
	if credential.ProviderID != grokconfig.ProviderID ||
		credential.CredentialKind != grokpreset.CredentialKindAuthJSON {
		return apperror.New(apperror.GrokBuildInvalid, "Grok Build login has an unsupported kind")
	}
	if switchtarget.SHA256String(credential.PayloadJSON) != credential.PayloadSHA256 {
		return apperror.New(apperror.GrokBuildInvalid, "Grok Build login integrity check failed")
	}
	if _, err := grokauth.Validate([]byte(credential.PayloadJSON)); err != nil {
		return authPayloadError(err)
	}
	return nil
}

func ValidateConfigSetRecord(configSet ConfigSetRecord) error {
	if configSet.ProviderID != grokconfig.ProviderID ||
		configSet.ConfigKind != grokpreset.ConfigSetKindTOML {
		return apperror.New(apperror.GrokBuildInvalid, "Grok Build Config Set has an unsupported kind")
	}
	if switchtarget.SHA256String(configSet.PayloadText) != configSet.PayloadSHA256 {
		return apperror.New(apperror.GrokBuildInvalid, "Grok Build Config Set integrity check failed")
	}
	if err := grokconfig.ValidateTOML(configSet.PayloadText); err != nil {
		return apperror.New(apperror.GrokBuildInvalid, "Grok Build Config Set TOML is invalid")
	}
	return nil
}

func ValidatePlanTargetRecord(provider ProviderRecord, target TargetRecord) error {
	if target.ProviderID != grokconfig.ProviderID {
		return targetInvalid("Grok Build preset received an incompatible target")
	}
	providerMetadata, err := grokpreset.DecodeProviderMetadata(provider.MetadataJSON)
	if err != nil || !providerMetadata.Compatible() {
		return apperror.New(apperror.GrokBuildInvalid, "Grok Build Provider metadata is invalid")
	}
	targetMetadata, err := grokpreset.DecodeTargetMetadata(target.MetadataJSON)
	if err != nil || !targetMetadata.Compatible() {
		return targetInvalid("Grok Build target metadata is invalid")
	}
	switch target.TargetID {
	case grokconfig.AuthTargetID:
		if target.Path != providerMetadata.AuthPath ||
			target.Format != profiletarget.FormatJSON ||
			target.Strategy != profiletarget.StrategyReplaceFile {
			return targetInvalid("Grok Build auth target contract is invalid")
		}
	case grokconfig.ConfigTargetID:
		if target.Path != providerMetadata.ConfigPath ||
			target.Format != profiletarget.FormatTOML ||
			target.Strategy != profiletarget.StrategyReplaceFile {
			return targetInvalid("Grok Build config target contract is invalid")
		}
	default:
		return targetInvalid("Grok Build supports only auth and config targets")
	}
	return nil
}

func FullProfileTargets(
	profileID string,
	targets []store.ProfileTarget,
) (store.ProfileTarget, store.ProfileTarget, error) {
	var configTarget store.ProfileTarget
	var authTarget store.ProfileTarget
	for _, target := range targets {
		switch target.TargetID {
		case grokconfig.ConfigTargetID:
			if _, err := ConfigSetIDFromTarget(target); err != nil {
				return store.ProfileTarget{}, store.ProfileTarget{}, err
			}
			configTarget = target
		case grokconfig.AuthTargetID:
			if _, err := CredentialIDFromTarget(target); err != nil {
				return store.ProfileTarget{}, store.ProfileTarget{}, err
			}
			authTarget = target
		}
	}
	if configTarget.TargetID == "" || authTarget.TargetID == "" {
		return store.ProfileTarget{}, store.ProfileTarget{}, apperror.New(
			apperror.GrokBuildInvalid,
			"Grok Build Profile must contain one login and one Config Set",
		).WithDetail("profile_id", profileID)
	}
	return configTarget, authTarget, nil
}

func CredentialBindingCount(ctx context.Context, db *store.Store, credentialID string) (int, error) {
	count, err := db.CountProviderCredentialReferences(ctx, credentialID)
	if err != nil {
		return 0, apperror.Wrap(apperror.StoreStatusFailed, "failed to count Grok Build login references", err)
	}
	return count, nil
}

func ConfigSetBindingCount(ctx context.Context, db *store.Store, configSetID string) (int, error) {
	count, err := db.CountProviderConfigSetReferences(ctx, grokconfig.ProviderID, configSetID)
	if err != nil {
		return 0, apperror.Wrap(apperror.StoreStatusFailed, "failed to count Grok Build Config Set references", err)
	}
	return count, nil
}

func NewCredentialID(now time.Time) (string, error) {
	randomBytes := make([]byte, credentialRandomBytes)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", err
	}
	return fmt.Sprintf("cred_%d_%s", now.UnixMilli(), hex.EncodeToString(randomBytes)), nil
}

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

func targetRecordFromStore(target store.ProfileTarget) TargetRecord {
	return TargetRecord{
		ProfileID: target.ProfileID, ProviderID: target.ProviderID, TargetID: target.TargetID,
		Path: target.Path, Format: target.Format, Strategy: target.Strategy,
		ValueJSON: target.ValueJSON, MetadataJSON: target.MetadataJSON,
	}
}

func unsupportedBinding(profileID, slotID string) error {
	return apperror.New(apperror.GrokBuildInvalid, "Grok Build Profile contains an unsupported binding").
		WithDetail("profile_id", profileID).WithDetail("slot_id", slotID)
}

func targetInvalid(message string) error {
	return apperror.New(apperror.GrokBuildInvalid, message)
}

func authPayloadError(err error) *apperror.Error {
	var sizeErr grokauth.SizeError
	if errors.As(err, &sizeErr) {
		return apperror.New(apperror.GrokBuildInvalid, "Grok Build auth.json is too large")
	}
	return apperror.New(apperror.GrokBuildInvalid, "Grok Build auth.json is invalid")
}

func mapCredentialStoreError(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return apperror.New(apperror.GrokBuildInvalid, "Grok Build login was not found")
	}
	return apperror.Wrap(apperror.StoreStatusFailed, "Grok Build login store operation failed", err)
}

func mapConfigSetStoreError(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return apperror.New(apperror.GrokBuildInvalid, "Grok Build Config Set was not found")
	}
	if errors.Is(err, store.ErrInUse) {
		return apperror.New(apperror.ProfileInUse, "Grok Build Config Set is in use")
	}
	return apperror.Wrap(apperror.StoreStatusFailed, "Grok Build Config Set store operation failed", err)
}

func mapProviderStoreError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrNotFound):
		return apperror.New(apperror.ProviderNotFound, "Grok Build Provider was not found")
	case errors.Is(err, store.ErrAlreadyExists):
		return apperror.New(apperror.ProviderAlreadyExists, "Grok Build Provider already exists")
	default:
		return apperror.Wrap(apperror.StoreStatusFailed, "Grok Build Provider store operation failed", err)
	}
}

func mapProfileStoreError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrNotFound):
		return apperror.New(apperror.ProfileNotFound, "Profile was not found")
	case errors.Is(err, store.ErrAlreadyExists):
		return apperror.New(apperror.ProfileAlreadyExists, "Profile already exists")
	case errors.Is(err, store.ErrInUse):
		return apperror.New(apperror.ProfileInUse, "Profile is in use")
	default:
		return apperror.Wrap(apperror.StoreStatusFailed, "Profile store operation failed", err)
	}
}
