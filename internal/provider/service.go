// Package provider owns generic Provider lifecycle and resolved active state.
package provider

import (
	"context"
	"errors"

	"github.com/strahe/profiledeck/internal/agent"
	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/maintenance"
	"github.com/strahe/profiledeck/internal/store"
	"github.com/strahe/profiledeck/internal/validate"
)

type Provider struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	AdapterID       string         `json:"adapter_id"`
	Enabled         bool           `json:"enabled"`
	Metadata        map[string]any `json:"metadata"`
	CreatedAtUnixMS int64          `json:"created_at_unix_ms"`
	UpdatedAtUnixMS int64          `json:"updated_at_unix_ms"`
}

type ActiveState struct {
	ProviderID       string `json:"provider_id"`
	ProviderName     string `json:"provider_name"`
	ProfileID        string `json:"profile_id"`
	ProfileName      string `json:"profile_name"`
	OperationID      string `json:"operation_id"`
	UpdatedAtUnixMS  int64  `json:"updated_at_unix_ms"`
	ProfileAvailable bool   `json:"profile_available"`
}

type DeleteResult struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

type ListRequest struct {
	IncludeDisabled bool `json:"include_disabled"`
}

type CreateRequest struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	AdapterID    string  `json:"adapter_id"`
	Enabled      *bool   `json:"enabled,omitempty"`
	MetadataJSON *string `json:"metadata_json,omitempty"`
}

type UpdateRequest struct {
	ID           string  `json:"id"`
	Name         *string `json:"name,omitempty"`
	AdapterID    *string `json:"adapter_id,omitempty"`
	Enabled      *bool   `json:"enabled,omitempty"`
	MetadataJSON *string `json:"metadata_json,omitempty"`
}

type Service struct {
	stores      store.Factory
	maintenance maintenance.Runner
	policy      agent.Policy
	registry    agent.Registry
}

func NewService(stores store.Factory, maintenance maintenance.Runner, policy agent.Policy, registry agent.Registry) *Service {
	return &Service{stores: stores, maintenance: maintenance, policy: policy, registry: registry}
}

func (service *Service) List(ctx context.Context, req ListRequest) ([]Provider, error) {
	db, err := service.stores.OpenHealthy(ctx, true)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	providers, err := db.ListProviders(ctx, req.IncludeDisabled)
	if err != nil {
		return nil, apperror.Wrap(apperror.StoreStatusFailed, "failed to list Providers", err)
	}
	result := make([]Provider, 0, len(providers))
	for _, stored := range providers {
		allowed, err := service.visible(ctx, db, stored.ID)
		if err != nil {
			return nil, err
		}
		if !allowed {
			continue
		}
		value, err := FromStore(stored)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, nil
}

func (service *Service) Get(ctx context.Context, id string) (Provider, error) {
	id, appErr := validate.ID(id, apperror.ProviderInvalid)
	if appErr != nil {
		return Provider{}, appErr
	}
	if err := service.require(ctx, id); err != nil {
		return Provider{}, err
	}
	db, err := service.stores.OpenHealthy(ctx, true)
	if err != nil {
		return Provider{}, err
	}
	defer db.Close()
	stored, err := db.GetProvider(ctx, id)
	if err != nil {
		return Provider{}, mapStoreError(err)
	}
	return FromStore(stored)
}

func (service *Service) Create(ctx context.Context, req CreateRequest) (Provider, error) {
	id, name, adapterID, metadataJSON, enabled, appErr := normalizeCreate(req)
	if appErr != nil {
		return Provider{}, appErr
	}
	if err := service.require(ctx, id); err != nil {
		return Provider{}, err
	}
	if _, managed := service.registry.AgentForProvider(id); managed {
		// Managed Provider identity and target metadata are created only by the
		// owning Agent service so generic CRUD cannot install an incompatible record.
		return Provider{}, apperror.New(apperror.ProviderInvalid, "managed Provider can only be created through its Agent service").
			WithDetail("provider_id", id)
	}
	var created store.Provider
	err := service.maintenance.RunMaintenance(ctx, maintenance.Request{
		Operation: "provider-create", ProviderID: id, MetadataJSON: `{"kind":"provider-create"}`,
	}, func(ctx context.Context, tx *store.Store, _ string) error {
		var err error
		created, err = tx.CreateProvider(ctx, store.CreateProviderParams{
			ID: id, Name: name, AdapterID: adapterID, Enabled: enabled, MetadataJSON: metadataJSON,
		})
		return mapStoreError(err)
	})
	if err != nil {
		return Provider{}, err
	}
	return FromStore(created)
}

func (service *Service) Update(ctx context.Context, req UpdateRequest) (Provider, error) {
	id, params, appErr := normalizeUpdate(req)
	if appErr != nil {
		return Provider{}, appErr
	}
	if err := service.require(ctx, id); err != nil {
		return Provider{}, err
	}
	if _, managed := service.registry.AgentForProvider(id); managed && (params.AdapterID != nil || params.MetadataJSON != nil) {
		// Managed target identity belongs to the typed Agent service.
		return Provider{}, apperror.New(apperror.ProviderInvalid, "managed Provider adapter and metadata can only be changed through its Agent service").WithDetail("provider_id", id)
	}
	var updated store.Provider
	err := service.maintenance.RunMaintenance(ctx, maintenance.Request{
		Operation: "provider-update", ProviderID: id, MetadataJSON: `{"kind":"provider-update"}`,
	}, func(ctx context.Context, tx *store.Store, _ string) error {
		params.ID = id
		var err error
		updated, err = tx.UpdateProvider(ctx, params)
		return mapStoreError(err)
	})
	if err != nil {
		return Provider{}, err
	}
	return FromStore(updated)
}

func (service *Service) Delete(ctx context.Context, id string, confirm bool) (DeleteResult, error) {
	id, appErr := validate.ID(id, apperror.ProviderInvalid)
	if appErr != nil {
		return DeleteResult{}, appErr
	}
	if !confirm {
		return DeleteResult{}, apperror.New(apperror.ConfirmationRequired, "Provider delete requires confirmation")
	}
	if err := service.require(ctx, id); err != nil {
		return DeleteResult{}, err
	}
	err := service.maintenance.RunMaintenance(ctx, maintenance.Request{
		Operation: "provider-delete", ProviderID: id, MetadataJSON: `{"kind":"provider-delete"}`,
	}, func(ctx context.Context, tx *store.Store, _ string) error {
		return mapStoreError(tx.DeleteProvider(ctx, id))
	})
	if err != nil {
		return DeleteResult{}, err
	}
	return DeleteResult{ID: id, Deleted: true}, nil
}

func (service *Service) ListActiveStates(ctx context.Context) ([]ActiveState, error) {
	db, err := service.stores.OpenHealthy(ctx, true)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	providers, err := db.ListProviders(ctx, true)
	if err != nil {
		return nil, apperror.Wrap(apperror.StoreStatusFailed, "failed to list Providers", err)
	}
	result := make([]ActiveState, 0, len(providers))
	for _, storedProvider := range providers {
		allowed, err := service.visible(ctx, db, storedProvider.ID)
		if err != nil {
			return nil, err
		}
		if !allowed {
			continue
		}
		active, err := db.GetActiveState(ctx, store.ActiveStateScopeProvider, storedProvider.ID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, apperror.Wrap(apperror.StoreStatusFailed, "failed to read active Provider state", err).WithDetail("provider_id", storedProvider.ID)
		}
		state := ActiveState{
			ProviderID: storedProvider.ID, ProviderName: storedProvider.Name, ProfileID: active.ProfileID,
			OperationID: active.OperationID, UpdatedAtUnixMS: active.UpdatedAtUnixMS,
		}
		storedProfile, err := db.GetProfile(ctx, active.ProfileID)
		if errors.Is(err, store.ErrNotFound) {
			state.ProfileAvailable = false
		} else if err != nil {
			return nil, apperror.Wrap(apperror.StoreStatusFailed, "failed to read active Profile", err).WithDetail("profile_id", active.ProfileID)
		} else {
			state.ProfileName = storedProfile.Name
			state.ProfileAvailable = true
		}
		result = append(result, state)
	}
	return result, nil
}

func (service *Service) require(ctx context.Context, providerID string) error {
	if service.policy == nil {
		return nil
	}
	return service.policy.RequireProvider(ctx, providerID)
}

func (service *Service) visible(ctx context.Context, db *store.Store, providerID string) (bool, error) {
	var err error
	if policy, ok := service.policy.(agent.StorePolicy); ok {
		err = policy.RequireProviderWithStore(ctx, db, providerID)
	} else {
		err = service.require(ctx, providerID)
	}
	if err == nil {
		return true, nil
	}
	var appErr *apperror.Error
	if errors.As(err, &appErr) && appErr.Code == apperror.AgentDisabled {
		return false, nil
	}
	return false, err
}

func normalizeCreate(req CreateRequest) (string, string, string, string, bool, *apperror.Error) {
	id, appErr := validate.ID(req.ID, apperror.ProviderInvalid)
	if appErr != nil {
		return "", "", "", "", false, appErr
	}
	name, appErr := validate.Name(req.Name, apperror.ProviderInvalid)
	if appErr != nil {
		return "", "", "", "", false, appErr
	}
	adapterID, appErr := validate.ID(req.AdapterID, apperror.ProviderInvalid)
	if appErr != nil {
		return "", "", "", "", false, appErr.WithDetail("field", "adapter_id")
	}
	metadataJSON, appErr := validate.MetadataJSON(req.MetadataJSON, apperror.ProviderInvalid)
	if appErr != nil {
		return "", "", "", "", false, appErr
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	return id, name, adapterID, metadataJSON, enabled, nil
}

func normalizeUpdate(req UpdateRequest) (string, store.UpdateProviderParams, *apperror.Error) {
	id, appErr := validate.ID(req.ID, apperror.ProviderInvalid)
	if appErr != nil {
		return "", store.UpdateProviderParams{}, appErr
	}
	if req.Name == nil && req.AdapterID == nil && req.Enabled == nil && req.MetadataJSON == nil {
		return "", store.UpdateProviderParams{}, apperror.New(apperror.ProviderInvalid, "Provider update requires at least one changed field")
	}
	params := store.UpdateProviderParams{}
	if req.Name != nil {
		name, appErr := validate.Name(*req.Name, apperror.ProviderInvalid)
		if appErr != nil {
			return "", store.UpdateProviderParams{}, appErr
		}
		params.Name = &name
	}
	if req.AdapterID != nil {
		adapterID, appErr := validate.ID(*req.AdapterID, apperror.ProviderInvalid)
		if appErr != nil {
			return "", store.UpdateProviderParams{}, appErr.WithDetail("field", "adapter_id")
		}
		params.AdapterID = &adapterID
	}
	if req.Enabled != nil {
		enabled := *req.Enabled
		params.Enabled = &enabled
	}
	if req.MetadataJSON != nil {
		metadataJSON, appErr := validate.MetadataJSON(req.MetadataJSON, apperror.ProviderInvalid)
		if appErr != nil {
			return "", store.UpdateProviderParams{}, appErr
		}
		params.MetadataJSON = &metadataJSON
	}
	return id, params, nil
}

// FromStore maps a persisted Provider to its public representation.
func FromStore(stored store.Provider) (Provider, error) {
	metadata, err := validate.StoredMetadata(stored.MetadataJSON)
	if err != nil {
		return Provider{}, err
	}
	return Provider{
		ID: stored.ID, Name: stored.Name, AdapterID: stored.AdapterID, Enabled: stored.Enabled, Metadata: metadata,
		CreatedAtUnixMS: stored.CreatedAtUnixMS, UpdatedAtUnixMS: stored.UpdatedAtUnixMS,
	}, nil
}

func mapStoreError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		return apperror.New(apperror.ProviderNotFound, "Provider not found")
	case errors.Is(err, store.ErrAlreadyExists):
		return apperror.New(apperror.ProviderAlreadyExists, "Provider already exists")
	case errors.Is(err, store.ErrInUse):
		return apperror.New(apperror.ProviderInUse, "Provider is in use")
	default:
		return apperror.Wrap(apperror.StoreStatusFailed, "Provider store operation failed", err)
	}
}
