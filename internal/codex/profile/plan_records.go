package profile

import (
	"github.com/strahe/profiledeck/internal/apperror"
	codexauth "github.com/strahe/profiledeck/internal/codex/auth"
	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	codexpreset "github.com/strahe/profiledeck/internal/codex/preset"
	"github.com/strahe/profiledeck/internal/profiletarget"
	switchtarget "github.com/strahe/profiledeck/internal/switching/target"
)

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

// BindingTargetRecords materializes Codex typed bindings for the plan adapter
// without exposing persistence types across the adapter boundary.
func BindingTargetRecords(
	provider ProviderRecord,
	credentialBindings []CredentialBindingRecord,
	configBindings []ConfigSetBindingRecord,
) ([]TargetRecord, error) {
	metadata, err := codexpreset.DecodeProviderMetadata(provider.MetadataJSON)
	if err != nil || !metadata.Compatible() {
		return nil, apperror.New(apperror.CodexInvalid, "Codex Provider metadata is invalid").WithDetail("provider_id", provider.ID)
	}
	targets := make([]TargetRecord, 0, len(credentialBindings)+len(configBindings))
	for _, binding := range configBindings {
		if binding.SlotID != codexpreset.ConfigSetSlotUserConfig {
			return nil, apperror.New(apperror.CodexInvalid, "Codex Profile contains an unsupported config binding").
				WithDetail("profile_id", binding.ProfileID).WithDetail("slot_id", binding.SlotID)
		}
		valueJSON, err := codexpreset.ConfigSetBindingValueJSON(binding.ConfigSetID)
		if err != nil {
			return nil, err
		}
		metadataJSON, err := codexpreset.TargetMetadataJSON(codexconfig.TargetID, codexpreset.TargetModeConfigSet)
		if err != nil {
			return nil, err
		}
		targets = append(targets, TargetRecord{
			ProfileID: binding.ProfileID, ProviderID: binding.ProviderID, TargetID: codexconfig.TargetID,
			Path: metadata.ConfigPath, Format: profiletarget.FormatTOML, Strategy: profiletarget.StrategyReplaceFile,
			ValueJSON: valueJSON, MetadataJSON: metadataJSON,
		})
	}
	for _, binding := range credentialBindings {
		if binding.SlotID != codexpreset.CredentialSlotAuth {
			return nil, apperror.New(apperror.CodexInvalid, "Codex Profile contains an unsupported login binding").
				WithDetail("profile_id", binding.ProfileID).WithDetail("slot_id", binding.SlotID)
		}
		valueJSON, err := codexpreset.CredentialBindingValueJSON(binding.CredentialID)
		if err != nil {
			return nil, err
		}
		metadataJSON, err := codexpreset.TargetMetadataJSON(codexconfig.AuthTargetID, codexpreset.TargetModeCredential)
		if err != nil {
			return nil, err
		}
		targets = append(targets, TargetRecord{
			ProfileID: binding.ProfileID, ProviderID: binding.ProviderID, TargetID: codexconfig.AuthTargetID,
			Path: metadata.AuthPath, Format: profiletarget.FormatJSON, Strategy: profiletarget.StrategyReplaceFile,
			ValueJSON: valueJSON, MetadataJSON: metadataJSON,
		})
	}
	return targets, nil
}

func CredentialIDFromRecord(target TargetRecord) (string, error) {
	metadata, err := codexpreset.DecodeTargetMetadata(target.MetadataJSON)
	if err != nil {
		return "", apperror.Wrap(apperror.StoreSchemaInvalid, "stored Codex auth target metadata is invalid", err).
			WithDetail("profile_id", target.ProfileID).WithDetail("target_id", target.TargetID)
	}
	if metadata.TargetKind != codexconfig.AuthTargetID || metadata.Mode != codexpreset.TargetModeCredential {
		return "", apperror.New(apperror.CodexInvalid, "stored Codex auth target is not a credential binding").
			WithDetail("profile_id", target.ProfileID).WithDetail("target_id", target.TargetID)
	}
	credentialID, err := codexpreset.ParseCredentialBindingValueJSON(target.ValueJSON)
	if err != nil {
		return "", apperror.Wrap(apperror.StoreSchemaInvalid, "stored Codex auth target value_json is invalid", err).
			WithDetail("profile_id", target.ProfileID).WithDetail("target_id", target.TargetID)
	}
	return credentialID, nil
}

func ConfigSetIDFromRecord(target TargetRecord) (string, error) {
	metadata, err := codexpreset.DecodeTargetMetadata(target.MetadataJSON)
	if err != nil {
		return "", apperror.Wrap(apperror.StoreSchemaInvalid, "stored Codex config target metadata is invalid", err).
			WithDetail("profile_id", target.ProfileID).WithDetail("target_id", target.TargetID)
	}
	if metadata.TargetKind != codexconfig.TargetID || metadata.Mode != codexpreset.TargetModeConfigSet {
		return "", apperror.New(apperror.CodexInvalid, "stored Codex config target is not a config set binding").
			WithDetail("profile_id", target.ProfileID).WithDetail("target_id", target.TargetID)
	}
	configSetID, err := codexpreset.ParseConfigSetBindingValueJSON(target.ValueJSON)
	if err != nil {
		return "", apperror.Wrap(apperror.StoreSchemaInvalid, "stored Codex config target value_json is invalid", err).
			WithDetail("profile_id", target.ProfileID).WithDetail("target_id", target.TargetID)
	}
	return configSetID, nil
}

func ValidateCredentialRecord(credential CredentialRecord) error {
	if credential.ProviderID != codexconfig.ProviderID || credential.CredentialKind != codexpreset.CredentialKindAuthJSON {
		return apperror.New(apperror.CodexInvalid, "Codex auth credential has unsupported kind").
			WithDetail("credential_id", credential.ID).WithDetail("credential_kind", credential.CredentialKind)
	}
	if switchtarget.SHA256String(credential.PayloadJSON) != credential.PayloadSHA256 {
		return apperror.New(apperror.CodexInvalid, "Codex auth credential payload hash is invalid").WithDetail("credential_id", credential.ID)
	}
	if _, err := codexauth.NormalizePayload([]byte(credential.PayloadJSON)); err != nil {
		return authPayloadError(err).WithDetail("credential_id", credential.ID)
	}
	return nil
}

func ValidateConfigSetRecord(configSet ConfigSetRecord) error {
	if configSet.ProviderID != codexconfig.ProviderID || configSet.ConfigKind != codexpreset.ConfigSetKindTOML {
		return apperror.New(apperror.CodexInvalid, "Codex config set has unsupported kind").
			WithDetail("config_set_id", configSet.ID).WithDetail("config_kind", configSet.ConfigKind)
	}
	if switchtarget.SHA256String(configSet.PayloadText) != configSet.PayloadSHA256 {
		return apperror.New(apperror.CodexInvalid, "Codex config set payload hash is invalid").WithDetail("config_set_id", configSet.ID)
	}
	if err := codexconfig.ValidateTOML(configSet.PayloadText); err != nil {
		return apperror.New(apperror.CodexInvalid, "Codex config set TOML is invalid").WithDetail("config_set_id", configSet.ID)
	}
	return nil
}

func ValidatePlanTargetRecord(provider ProviderRecord, target TargetRecord) error {
	if target.ProviderID != codexconfig.ProviderID {
		return targetRecordInvalid(target, "Codex preset only supports Codex provider targets")
	}
	metadata, err := codexpreset.DecodeTargetMetadata(target.MetadataJSON)
	if err != nil {
		return apperror.Wrap(apperror.StoreSchemaInvalid, "stored Codex target metadata is invalid", err).
			WithDetail("profile_id", target.ProfileID).WithDetail("target_id", target.TargetID)
	}
	if !metadata.Compatible() {
		return apperror.New(apperror.CodexInvalid, "existing codex target was not created by the Codex preset").
			WithDetail("profile_id", target.ProfileID).WithDetail("target_id", target.TargetID)
	}
	providerMetadata, err := codexpreset.DecodeProviderMetadata(provider.MetadataJSON)
	if err != nil {
		return apperror.Wrap(apperror.StoreSchemaInvalid, "stored Codex provider metadata is invalid", err).WithDetail("provider_id", provider.ID)
	}
	if !providerMetadata.Compatible() {
		return apperror.New(apperror.CodexInvalid, "Codex provider was not created by the Codex preset").WithDetail("provider_id", provider.ID)
	}
	names := codexpreset.TargetFormatStrategyNames{
		JSONFormat: profiletarget.FormatJSON, TOMLFormat: profiletarget.FormatTOML,
		ReplaceFileStrategy: profiletarget.StrategyReplaceFile,
	}
	switch target.TargetID {
	case codexconfig.TargetID:
		if !codexpreset.ConfigTargetFormatValid(target.Format, names) || !codexpreset.ConfigTargetStrategyValid(target.Strategy, names) {
			return targetRecordInvalid(target, "Codex config target must use toml with replace-file strategy")
		}
		if target.Path != providerMetadata.ConfigPath {
			return targetRecordInvalid(target, "Codex config target path does not match provider config path")
		}
	case codexconfig.AuthTargetID:
		if !codexpreset.AuthTargetFormatStrategyValid(target.Format, target.Strategy, names) {
			return targetRecordInvalid(target, "Codex auth target must use json with replace-file strategy")
		}
		if target.Path != providerMetadata.AuthPath {
			return targetRecordInvalid(target, "Codex auth target path does not match provider auth path")
		}
	default:
		return targetRecordInvalid(target, "Codex preset only supports config and auth targets")
	}
	return nil
}

func targetRecordInvalid(target TargetRecord, message string) *apperror.Error {
	return apperror.New(apperror.CodexInvalid, message).
		WithDetail("profile_id", target.ProfileID).WithDetail("provider_id", target.ProviderID).WithDetail("target_id", target.TargetID)
}
