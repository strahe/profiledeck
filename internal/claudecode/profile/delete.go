package profile

import (
	"context"
	"errors"

	"github.com/strahe/profiledeck/internal/apperror"
	claudecodeconfig "github.com/strahe/profiledeck/internal/claudecode/config"
	globalprofile "github.com/strahe/profiledeck/internal/profile"
	"github.com/strahe/profiledeck/internal/store"
)

type DeleteParticipant struct{}

func (DeleteParticipant) ProviderID() string { return claudecodeconfig.ProviderID }

func (DeleteParticipant) DeleteProfileData(ctx context.Context, db *store.Store, profileID string) error {
	credentialBindings, err := db.ListProfileCredentialBindings(ctx, profileID, claudecodeconfig.ProviderID)
	if err != nil {
		return apperror.Wrap(apperror.StoreStatusFailed, "failed to inspect Claude Code Profile logins", err)
	}
	configBindings, err := db.ListProfileConfigSetBindings(ctx, profileID, claudecodeconfig.ProviderID)
	if err != nil {
		return apperror.Wrap(apperror.StoreStatusFailed, "failed to inspect Claude Code Profile settings", err)
	}
	if len(configBindings) != 0 {
		return globalprofile.UnsupportedManagedDataError()
	}
	for _, binding := range credentialBindings {
		if binding.SlotID != claudecodeconfig.CredentialSlot {
			return globalprofile.UnsupportedManagedDataError()
		}
		credential, err := db.GetProviderCredential(ctx, binding.CredentialID)
		if err != nil {
			return apperror.Wrap(apperror.StoreStatusFailed, "failed to inspect a saved Claude Code login", err)
		}
		if credential.ProviderID != claudecodeconfig.ProviderID || credential.CredentialKind != claudecodeconfig.CredentialKind {
			return globalprofile.UnsupportedManagedDataError()
		}
	}

	for _, binding := range credentialBindings {
		if err := db.DeleteProfileCredentialBinding(ctx, profileID, claudecodeconfig.ProviderID, binding.SlotID); err != nil {
			return apperror.Wrap(apperror.StoreStatusFailed, "failed to delete a Claude Code Profile login binding", err)
		}
		if err := db.DeleteProviderCredential(ctx, binding.CredentialID); err != nil && !errors.Is(err, store.ErrInUse) {
			return apperror.Wrap(apperror.StoreStatusFailed, "failed to delete an unshared Claude Code login", err)
		}
	}
	return nil
}
