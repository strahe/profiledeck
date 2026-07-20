package profile

import (
	"context"
	"errors"

	agyconfig "github.com/strahe/profiledeck/internal/antigravity/config"
	"github.com/strahe/profiledeck/internal/apperror"
	globalprofile "github.com/strahe/profiledeck/internal/profile"
	"github.com/strahe/profiledeck/internal/store"
)

type DeleteParticipant struct{}

func (DeleteParticipant) ProviderID() string { return agyconfig.ProviderID }

func (DeleteParticipant) DeleteProfileData(ctx context.Context, db *store.Store, profileID string) error {
	credentialBindings, err := db.ListProfileCredentialBindings(ctx, profileID, agyconfig.ProviderID)
	if err != nil {
		return apperror.Wrap(apperror.StoreStatusFailed, "failed to inspect Antigravity Profile logins", err)
	}
	configBindings, err := db.ListProfileConfigSetBindings(ctx, profileID, agyconfig.ProviderID)
	if err != nil {
		return apperror.Wrap(apperror.StoreStatusFailed, "failed to inspect Antigravity Profile settings", err)
	}
	if len(configBindings) != 0 {
		return globalprofile.UnsupportedManagedDataError()
	}
	for _, binding := range credentialBindings {
		if binding.SlotID != agyconfig.CredentialSlot {
			return globalprofile.UnsupportedManagedDataError()
		}
		credential, err := db.GetProviderCredential(ctx, binding.CredentialID)
		if err != nil {
			return apperror.Wrap(apperror.StoreStatusFailed, "failed to inspect a saved Antigravity login", err)
		}
		if credential.ProviderID != agyconfig.ProviderID || credential.CredentialKind != agyconfig.CredentialKind {
			return globalprofile.UnsupportedManagedDataError()
		}
	}

	for _, binding := range credentialBindings {
		if err := db.DeleteProfileCredentialBinding(ctx, profileID, agyconfig.ProviderID, binding.SlotID); err != nil {
			return apperror.Wrap(apperror.StoreStatusFailed, "failed to delete an Antigravity Profile login binding", err)
		}
		if err := db.DeleteProviderCredential(ctx, binding.CredentialID); err != nil && !errors.Is(err, store.ErrInUse) {
			return apperror.Wrap(apperror.StoreStatusFailed, "failed to delete an unshared Antigravity login", err)
		}
	}
	return nil
}
