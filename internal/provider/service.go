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
	Metadata        map[string]any `json:"metadata"`
	CreatedAtUnixMS int64          `json:"created_at_unix_ms"`
	UpdatedAtUnixMS int64          `json:"updated_at_unix_ms"`
}

type ActiveState struct {
	ProviderID       string `json:"provider_id"`
	ProviderName     string `json:"provider_name"`
	ProfileID        string `json:"profile_id"`
	ProfileName      string `json:"profile_name"`
	Revision         int64  `json:"revision"`
	UpdatedAtUnixMS  int64  `json:"updated_at_unix_ms"`
	ProfileAvailable bool   `json:"profile_available"`
}

type DeleteResult struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

type CreateRequest struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	AdapterID    string  `json:"adapter_id"`
	MetadataJSON *string `json:"metadata_json,omitempty"`
}

type UpdateRequest struct {
	ID           string  `json:"id"`
	Name         *string `json:"name,omitempty"`
	AdapterID    *string `json:"adapter_id,omitempty"`
	MetadataJSON *string `json:"metadata_json,omitempty"`
}

type Service struct {
	stores      store.Factory
	maintenance maintenance.Runner
	registry    agent.Registry
}

func NewService(stores store.Factory, maintenance maintenance.Runner, registry agent.Registry) *Service {
	return &Service{stores: stores, maintenance: maintenance, registry: registry}
}

func (service *Service) List(ctx context.Context) ([]Provider, error) {
	db, err := service.stores.OpenHealthy(ctx, true)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	providers, err := db.ListProviders(ctx)
	if err != nil {
		return nil, apperror.Wrap(apperror.StoreStatusFailed, "failed to list Providers", err)
	}
	result := make([]Provider, 0, len(providers))
	for _, stored := range providers {
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
	id, name, adapterID, metadataJSON, appErr := normalizeCreate(req)
	if appErr != nil {
		return Provider{}, appErr
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
			ID: id, Name: name, AdapterID: adapterID, MetadataJSON: metadataJSON,
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
	err := service.maintenance.RunMaintenance(ctx, maintenance.Request{
		Operation: "provider-delete", ProviderID: id, MetadataJSON: `{"kind":"provider-delete"}`,
	}, func(ctx context.Context, tx *store.Store, _ string) error {
		// Unresolved switch rows are live recovery evidence. Resolved history is
		// disposable and must be removed explicitly before the Provider FK allows
		// owned state to cascade.
		unresolved, err := tx.CountUnresolvedProviderOperations(ctx, id)
		if err != nil {
			return apperror.Wrap(apperror.StoreStatusFailed, "failed to inspect unfinished Provider operations", err)
		}
		if unresolved > 0 {
			return apperror.New(
				apperror.ProviderInUse,
				"Provider has an unfinished operation; resolve it in Diagnostics and try again",
			)
		}
		if err := tx.DeleteResolvedProviderOperations(ctx, id); err != nil {
			return apperror.Wrap(apperror.StoreStatusFailed, "failed to delete Provider operation history", err)
		}
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
	providers, err := db.ListProviders(ctx)
	if err != nil {
		return nil, apperror.Wrap(apperror.StoreStatusFailed, "failed to list Providers", err)
	}
	result := make([]ActiveState, 0, len(providers))
	for _, storedProvider := range providers {
		active, err := db.GetActiveState(ctx, storedProvider.ID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, apperror.Wrap(apperror.StoreStatusFailed, "failed to read active Provider state", err).WithDetail("provider_id", storedProvider.ID)
		}
		state := ActiveState{
			ProviderID: storedProvider.ID, ProviderName: storedProvider.Name, ProfileID: active.ProfileID,
			Revision: active.Revision, UpdatedAtUnixMS: active.UpdatedAtUnixMS,
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

func normalizeCreate(req CreateRequest) (string, string, string, string, *apperror.Error) {
	id, appErr := validate.ID(req.ID, apperror.ProviderInvalid)
	if appErr != nil {
		return "", "", "", "", appErr
	}
	name, appErr := validate.Name(req.Name, apperror.ProviderInvalid)
	if appErr != nil {
		return "", "", "", "", appErr
	}
	adapterID, appErr := validate.ID(req.AdapterID, apperror.ProviderInvalid)
	if appErr != nil {
		return "", "", "", "", appErr.WithDetail("field", "adapter_id")
	}
	metadataJSON, appErr := validate.MetadataJSON(req.MetadataJSON, apperror.ProviderInvalid)
	if appErr != nil {
		return "", "", "", "", appErr
	}
	return id, name, adapterID, metadataJSON, nil
}

func normalizeUpdate(req UpdateRequest) (string, store.UpdateProviderParams, *apperror.Error) {
	id, appErr := validate.ID(req.ID, apperror.ProviderInvalid)
	if appErr != nil {
		return "", store.UpdateProviderParams{}, appErr
	}
	if req.Name == nil && req.AdapterID == nil && req.MetadataJSON == nil {
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
		ID: stored.ID, Name: stored.Name, AdapterID: stored.AdapterID, Metadata: metadata,
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
