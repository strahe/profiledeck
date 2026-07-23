package antigravity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"github.com/strahe/profiledeck/internal/agent"
	"github.com/strahe/profiledeck/internal/apperror"
	profilecore "github.com/strahe/profiledeck/internal/profile"
	"github.com/strahe/profiledeck/internal/store"
	"github.com/strahe/profiledeck/internal/validate"
)

type managedProfileFields struct {
	CreateName        string
	CreateDescription string
	UpdateName        *string
	UpdateDescription *string
}

func normalizeManagedProfileFields(profileID string, name, description *string) (managedProfileFields, *apperror.Error) {
	fields := managedProfileFields{CreateName: profileID}
	if name != nil {
		value, appErr := validate.Name(*name, apperror.ProfileInvalid)
		if appErr != nil {
			return managedProfileFields{}, appErr
		}
		fields.CreateName = value
		fields.UpdateName = &value
	}
	if description != nil {
		value, appErr := validate.Description(*description, apperror.ProfileInvalid)
		if appErr != nil {
			return managedProfileFields{}, appErr
		}
		fields.CreateDescription = value
		fields.UpdateDescription = &value
	}
	return fields, nil
}

func profileFromStore(stored store.Profile) (profilecore.Profile, error) {
	metadata, err := validate.StoredMetadata(stored.MetadataJSON)
	if err != nil {
		return profilecore.Profile{}, err
	}
	return profilecore.Profile{
		ID: stored.ID, Name: stored.Name, Description: stored.Description, Metadata: metadata,
		CreatedAtUnixMS: stored.CreatedAtUnixMS, UpdatedAtUnixMS: stored.UpdatedAtUnixMS,
	}, nil
}

func mapProviderStoreError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		return apperror.New(apperror.ProviderNotFound, "Antigravity Provider not found")
	case errors.Is(err, store.ErrAlreadyExists):
		return apperror.New(apperror.ProviderAlreadyExists, "Antigravity Provider already exists")
	case errors.Is(err, store.ErrInUse):
		return apperror.New(apperror.ProviderInUse, "Antigravity Provider is in use")
	default:
		return apperror.Wrap(apperror.StoreStatusFailed, "Antigravity Provider store operation failed", err)
	}
}

func mapProfileStoreError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		return apperror.New(apperror.ProfileNotFound, "Antigravity Profile not found")
	case errors.Is(err, store.ErrAlreadyExists):
		return apperror.New(apperror.ProfileAlreadyExists, "Antigravity Profile already exists")
	case errors.Is(err, store.ErrInUse):
		return apperror.New(apperror.ProfileInUse, "Antigravity Profile is in use")
	default:
		return apperror.Wrap(apperror.StoreStatusFailed, "Antigravity Profile store operation failed", err)
	}
}

func sha256HexString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func (service *Service) requireAccess(ctx context.Context) error {
	if service.policy == nil {
		return nil
	}
	return service.policy.RequireAgent(ctx, agent.Antigravity)
}
