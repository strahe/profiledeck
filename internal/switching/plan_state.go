package switching

import (
	"context"
	"errors"

	"github.com/strahe/profiledeck/internal/store"
	switchplan "github.com/strahe/profiledeck/internal/switching/plan"
)

type planStateReader struct {
	store *store.Store
}

func newPlanInput(provider store.Provider, profile store.Profile, targets []store.ProfileTarget, db *store.Store) switchplan.Input {
	return switchplan.Input{
		Provider: planProvider(provider),
		Profile:  planProfile(profile),
		Targets:  planTargets(targets),
		State:    planStateReader{store: db},
	}
}

func planProvider(value store.Provider) switchplan.Provider {
	return switchplan.Provider{
		ID: value.ID, Name: value.Name, AdapterID: value.AdapterID, Enabled: value.Enabled, MetadataJSON: value.MetadataJSON,
	}
}

func planProfile(value store.Profile) switchplan.Profile {
	return switchplan.Profile{
		ID: value.ID, Name: value.Name, Description: value.Description, MetadataJSON: value.MetadataJSON,
	}
}

func planTarget(value store.ProfileTarget) switchplan.Target {
	return switchplan.Target{
		ProfileID: value.ProfileID, ProviderID: value.ProviderID, TargetID: value.TargetID,
		Path: value.Path, Format: value.Format, Strategy: value.Strategy, ValueJSON: value.ValueJSON,
		Enabled: value.Enabled, MetadataJSON: value.MetadataJSON,
	}
}

func planTargets(values []store.ProfileTarget) []switchplan.Target {
	result := make([]switchplan.Target, 0, len(values))
	for _, value := range values {
		result = append(result, planTarget(value))
	}
	return result
}

func planStateError(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return switchplan.ErrStateNotFound
	}
	return err
}

func (reader planStateReader) GetActiveState(ctx context.Context, providerID string) (switchplan.ActiveState, error) {
	value, err := reader.store.GetActiveState(ctx, store.ActiveStateScopeProvider, providerID)
	if err != nil {
		return switchplan.ActiveState{}, planStateError(err)
	}
	return switchplan.ActiveState{ProfileID: value.ProfileID, OperationID: value.OperationID}, nil
}

func (reader planStateReader) ListTargets(ctx context.Context, profileID, providerID string, includeDisabled bool) ([]switchplan.Target, error) {
	values, err := reader.store.ListProfileTargets(ctx, profileID, providerID, includeDisabled)
	if err != nil {
		return nil, err
	}
	return planTargets(values), nil
}

func (reader planStateReader) GetCredential(ctx context.Context, id string) (switchplan.Credential, error) {
	value, err := reader.store.GetProviderCredential(ctx, id)
	if err != nil {
		return switchplan.Credential{}, planStateError(err)
	}
	return planCredential(value), nil
}

func (reader planStateReader) ListCredentials(ctx context.Context, providerID string) ([]switchplan.Credential, error) {
	values, err := reader.store.ListProviderCredentials(ctx, providerID)
	if err != nil {
		return nil, err
	}
	result := make([]switchplan.Credential, 0, len(values))
	for _, value := range values {
		result = append(result, planCredential(value))
	}
	return result, nil
}

func planCredential(value store.ProviderCredential) switchplan.Credential {
	return switchplan.Credential{
		ID: value.ID, ProviderID: value.ProviderID, CredentialKind: value.CredentialKind,
		PayloadJSON: value.PayloadJSON, PayloadSHA256: value.PayloadSHA256, MetadataJSON: value.MetadataJSON,
	}
}

func (reader planStateReader) GetConfigSet(ctx context.Context, id string) (switchplan.ConfigSet, error) {
	value, err := reader.store.GetProviderConfigSet(ctx, id)
	if err != nil {
		return switchplan.ConfigSet{}, planStateError(err)
	}
	return planConfigSet(value), nil
}

func (reader planStateReader) ListConfigSets(ctx context.Context, providerID, configKind string) ([]switchplan.ConfigSet, error) {
	values, err := reader.store.ListProviderConfigSets(ctx, providerID, configKind)
	if err != nil {
		return nil, err
	}
	result := make([]switchplan.ConfigSet, 0, len(values))
	for _, value := range values {
		result = append(result, planConfigSet(value))
	}
	return result, nil
}

func planConfigSet(value store.ProviderConfigSet) switchplan.ConfigSet {
	return switchplan.ConfigSet{
		ID: value.ID, ProviderID: value.ProviderID, ConfigKind: value.ConfigKind, Name: value.Name,
		Description: value.Description, PayloadText: value.PayloadText, PayloadSHA256: value.PayloadSHA256,
		MetadataJSON: value.MetadataJSON,
	}
}

func (reader planStateReader) GetCredentialBinding(ctx context.Context, profileID, providerID, slotID string) (switchplan.CredentialBinding, error) {
	value, err := reader.store.GetProfileCredentialBinding(ctx, profileID, providerID, slotID)
	if err != nil {
		return switchplan.CredentialBinding{}, planStateError(err)
	}
	return planCredentialBinding(value), nil
}

func (reader planStateReader) ListCredentialBindings(ctx context.Context, profileID, providerID string) ([]switchplan.CredentialBinding, error) {
	values, err := reader.store.ListProfileCredentialBindings(ctx, profileID, providerID)
	if err != nil {
		return nil, err
	}
	return planCredentialBindings(values), nil
}

func (reader planStateReader) ListCredentialBindingsByProvider(ctx context.Context, providerID string) ([]switchplan.CredentialBinding, error) {
	values, err := reader.store.ListProfileCredentialBindingsByProvider(ctx, providerID)
	if err != nil {
		return nil, err
	}
	return planCredentialBindings(values), nil
}

func planCredentialBinding(value store.ProfileCredentialBinding) switchplan.CredentialBinding {
	return switchplan.CredentialBinding{
		ProfileID: value.ProfileID, ProviderID: value.ProviderID, SlotID: value.SlotID, CredentialID: value.CredentialID,
	}
}

func planCredentialBindings(values []store.ProfileCredentialBinding) []switchplan.CredentialBinding {
	result := make([]switchplan.CredentialBinding, 0, len(values))
	for _, value := range values {
		result = append(result, planCredentialBinding(value))
	}
	return result
}

func (reader planStateReader) ListConfigSetBindings(ctx context.Context, profileID, providerID string) ([]switchplan.ConfigSetBinding, error) {
	values, err := reader.store.ListProfileConfigSetBindings(ctx, profileID, providerID)
	if err != nil {
		return nil, err
	}
	result := make([]switchplan.ConfigSetBinding, 0, len(values))
	for _, value := range values {
		result = append(result, switchplan.ConfigSetBinding{
			ProfileID: value.ProfileID, ProviderID: value.ProviderID, SlotID: value.SlotID, ConfigSetID: value.ConfigSetID,
		})
	}
	return result, nil
}

func (reader planStateReader) CountCredentialReferences(ctx context.Context, credentialID string) (int, error) {
	return reader.store.CountProviderCredentialReferences(ctx, credentialID)
}
