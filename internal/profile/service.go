// Package profile owns global Profile lifecycle.
package profile

import (
	"context"
	"errors"

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/maintenance"
	"github.com/strahe/profiledeck/internal/store"
	"github.com/strahe/profiledeck/internal/validate"
)

type Profile struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	Description     string         `json:"description"`
	Metadata        map[string]any `json:"metadata"`
	CreatedAtUnixMS int64          `json:"created_at_unix_ms"`
	UpdatedAtUnixMS int64          `json:"updated_at_unix_ms"`
}

type CreateRequest struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Description  string  `json:"description"`
	MetadataJSON *string `json:"metadata_json,omitempty"`
}

type UpdateRequest struct {
	ID           string  `json:"id"`
	Name         *string `json:"name,omitempty"`
	Description  *string `json:"description,omitempty"`
	MetadataJSON *string `json:"metadata_json,omitempty"`
}

type DeleteResult struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

type Service struct {
	stores         store.Factory
	maintenance    maintenance.Runner
	deleteRegistry DeleteRegistry
}

func NewService(stores store.Factory, maintenance maintenance.Runner, deleteRegistry DeleteRegistry) *Service {
	return &Service{stores: stores, maintenance: maintenance, deleteRegistry: deleteRegistry}
}

func (service *Service) List(ctx context.Context) ([]Profile, error) {
	db, err := service.stores.OpenHealthy(ctx, true)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	profiles, err := db.ListProfiles(ctx)
	if err != nil {
		return nil, apperror.Wrap(apperror.StoreStatusFailed, "failed to list Profiles", err)
	}
	result := make([]Profile, 0, len(profiles))
	for _, stored := range profiles {
		value, err := FromStore(stored)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, nil
}

func (service *Service) Get(ctx context.Context, id string) (Profile, error) {
	id, appErr := validate.ID(id, apperror.ProfileInvalid)
	if appErr != nil {
		return Profile{}, appErr
	}
	db, err := service.stores.OpenHealthy(ctx, true)
	if err != nil {
		return Profile{}, err
	}
	defer db.Close()
	stored, err := db.GetProfile(ctx, id)
	if err != nil {
		return Profile{}, mapStoreError(err)
	}
	return FromStore(stored)
}

func (service *Service) Create(ctx context.Context, req CreateRequest) (Profile, error) {
	id, name, description, metadataJSON, appErr := normalizeCreate(req)
	if appErr != nil {
		return Profile{}, appErr
	}
	var created store.Profile
	err := service.maintenance.RunMaintenance(ctx, maintenance.Request{
		Operation: "profile-create", ProfileID: id, MetadataJSON: `{"kind":"profile-create"}`,
	}, func(ctx context.Context, tx *store.Store, _ string) error {
		var err error
		created, err = tx.CreateProfile(ctx, store.CreateProfileParams{
			ID: id, Name: name, Description: description, MetadataJSON: metadataJSON,
		})
		return mapStoreError(err)
	})
	if err != nil {
		return Profile{}, err
	}
	return FromStore(created)
}

func (service *Service) Update(ctx context.Context, req UpdateRequest) (Profile, error) {
	id, params, appErr := normalizeUpdate(req)
	if appErr != nil {
		return Profile{}, appErr
	}
	var updated store.Profile
	err := service.maintenance.RunMaintenance(ctx, maintenance.Request{
		Operation: "profile-update", ProfileID: id, MetadataJSON: `{"kind":"profile-update"}`,
	}, func(ctx context.Context, tx *store.Store, _ string) error {
		params.ID = id
		var err error
		updated, err = tx.UpdateProfile(ctx, params)
		return mapStoreError(err)
	})
	if err != nil {
		return Profile{}, err
	}
	return FromStore(updated)
}

func (service *Service) Delete(ctx context.Context, id string, confirm bool) (DeleteResult, error) {
	id, appErr := validate.ID(id, apperror.ProfileInvalid)
	if appErr != nil {
		return DeleteResult{}, appErr
	}
	if !confirm {
		return DeleteResult{}, apperror.New(apperror.ConfirmationRequired, "Profile delete requires confirmation")
	}
	// Deletion is database-only lifecycle cleanup. It records no operation and
	// never enters the external target transaction pipeline.
	err := service.maintenance.RunMaintenance(ctx, maintenance.Request{
		Operation: "profile-delete", ProfileID: id, MetadataJSON: `{"kind":"profile-delete"}`,
	}, func(ctx context.Context, tx *store.Store, _ string) error {
		if _, err := tx.GetProfile(ctx, id); err != nil {
			return mapStoreError(err)
		}
		activeReferences, err := tx.CountActiveProfileReferences(ctx, id)
		if err != nil {
			return apperror.Wrap(apperror.StoreStatusFailed, "failed to inspect current Profile state", err)
		}
		if activeReferences > 0 {
			return deleteBlockedError(
				DeleteReasonActive,
				"Profile is current in at least one Agent; use another Profile there and try again",
			)
		}
		unresolvedOperations, err := tx.CountUnresolvedProfileOperations(ctx, id)
		if err != nil {
			return apperror.Wrap(apperror.StoreStatusFailed, "failed to inspect unfinished Profile operations", err)
		}
		if unresolvedOperations > 0 {
			return deleteBlockedError(
				DeleteReasonUnresolvedOperation,
				"Profile has an unfinished operation; resolve it in Diagnostics and try again",
			)
		}
		if err := service.deleteManagedData(ctx, tx, id); err != nil {
			return err
		}
		if err := tx.DeleteProfileTargetsByProfile(ctx, id); err != nil {
			return apperror.Wrap(apperror.StoreStatusFailed, "failed to delete Profile target data", err)
		}
		return mapStoreError(tx.DeleteProfile(ctx, id))
	})
	if err != nil {
		return DeleteResult{}, err
	}
	return DeleteResult{ID: id, Deleted: true}, nil
}

func normalizeCreate(req CreateRequest) (string, string, string, string, *apperror.Error) {
	id, appErr := validate.ID(req.ID, apperror.ProfileInvalid)
	if appErr != nil {
		return "", "", "", "", appErr
	}
	name, appErr := validate.Name(req.Name, apperror.ProfileInvalid)
	if appErr != nil {
		return "", "", "", "", appErr
	}
	description, appErr := validate.Description(req.Description, apperror.ProfileInvalid)
	if appErr != nil {
		return "", "", "", "", appErr
	}
	metadataJSON, appErr := validate.MetadataJSON(req.MetadataJSON, apperror.ProfileInvalid)
	if appErr != nil {
		return "", "", "", "", appErr
	}
	return id, name, description, metadataJSON, nil
}

func normalizeUpdate(req UpdateRequest) (string, store.UpdateProfileParams, *apperror.Error) {
	id, appErr := validate.ID(req.ID, apperror.ProfileInvalid)
	if appErr != nil {
		return "", store.UpdateProfileParams{}, appErr
	}
	if req.Name == nil && req.Description == nil && req.MetadataJSON == nil {
		return "", store.UpdateProfileParams{}, apperror.New(apperror.ProfileInvalid, "Profile update requires at least one changed field")
	}
	params := store.UpdateProfileParams{}
	if req.Name != nil {
		name, appErr := validate.Name(*req.Name, apperror.ProfileInvalid)
		if appErr != nil {
			return "", store.UpdateProfileParams{}, appErr
		}
		params.Name = &name
	}
	if req.Description != nil {
		description, appErr := validate.Description(*req.Description, apperror.ProfileInvalid)
		if appErr != nil {
			return "", store.UpdateProfileParams{}, appErr
		}
		params.Description = &description
	}
	if req.MetadataJSON != nil {
		metadataJSON, appErr := validate.MetadataJSON(req.MetadataJSON, apperror.ProfileInvalid)
		if appErr != nil {
			return "", store.UpdateProfileParams{}, appErr
		}
		params.MetadataJSON = &metadataJSON
	}
	return id, params, nil
}

// FromStore maps a persisted Profile to its public representation.
func FromStore(stored store.Profile) (Profile, error) {
	metadata, err := validate.StoredMetadata(stored.MetadataJSON)
	if err != nil {
		return Profile{}, err
	}
	return Profile{
		ID: stored.ID, Name: stored.Name, Description: stored.Description, Metadata: metadata,
		CreatedAtUnixMS: stored.CreatedAtUnixMS, UpdatedAtUnixMS: stored.UpdatedAtUnixMS,
	}, nil
}

func mapStoreError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		return apperror.New(apperror.ProfileNotFound, "Profile not found")
	case errors.Is(err, store.ErrAlreadyExists):
		return apperror.New(apperror.ProfileAlreadyExists, "Profile already exists")
	case errors.Is(err, store.ErrInUse):
		return apperror.New(apperror.ProfileInUse, "Profile is in use")
	default:
		return apperror.Wrap(apperror.StoreStatusFailed, "Profile store operation failed", err)
	}
}
