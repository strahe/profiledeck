package profile

import (
	"context"
	"errors"

	"github.com/strahe/profiledeck/internal/apperror"
	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	codexpreset "github.com/strahe/profiledeck/internal/codex/preset"
	globalprofile "github.com/strahe/profiledeck/internal/profile"
	"github.com/strahe/profiledeck/internal/store"
)

type DeleteParticipant struct{}

func (DeleteParticipant) ProviderID() string { return codexconfig.ProviderID }

func (DeleteParticipant) DeleteProfileData(ctx context.Context, db *store.Store, profileID string) error {
	credentialBindings, err := db.ListProfileCredentialBindings(ctx, profileID, codexconfig.ProviderID)
	if err != nil {
		return apperror.Wrap(apperror.StoreStatusFailed, "failed to inspect Codex Profile logins", err)
	}
	configBindings, err := db.ListProfileConfigSetBindings(ctx, profileID, codexconfig.ProviderID)
	if err != nil {
		return apperror.Wrap(apperror.StoreStatusFailed, "failed to inspect Codex Profile settings", err)
	}
	for _, binding := range credentialBindings {
		if binding.SlotID != codexpreset.CredentialSlotAuth {
			return globalprofile.UnsupportedManagedDataError()
		}
		credential, err := db.GetProviderCredential(ctx, binding.CredentialID)
		if err != nil {
			return apperror.Wrap(apperror.StoreStatusFailed, "failed to inspect a saved Codex login", err)
		}
		if credential.ProviderID != codexconfig.ProviderID || credential.CredentialKind != codexpreset.CredentialKindAuthJSON {
			return globalprofile.UnsupportedManagedDataError()
		}
	}
	for _, binding := range configBindings {
		if binding.SlotID != codexpreset.ConfigSetSlotUserConfig {
			return globalprofile.UnsupportedManagedDataError()
		}
		configSet, err := db.GetProviderConfigSet(ctx, binding.ConfigSetID)
		if err != nil {
			return apperror.Wrap(apperror.StoreStatusFailed, "failed to inspect saved Codex settings", err)
		}
		if configSet.ProviderID != codexconfig.ProviderID || configSet.ConfigKind != codexpreset.ConfigSetKindTOML {
			return globalprofile.UnsupportedManagedDataError()
		}
	}

	for _, binding := range credentialBindings {
		if err := db.DeleteProfileCredentialBinding(ctx, profileID, codexconfig.ProviderID, binding.SlotID); err != nil {
			return apperror.Wrap(apperror.StoreStatusFailed, "failed to delete a Codex Profile login binding", err)
		}
		if err := db.DeleteProviderCredential(ctx, binding.CredentialID); err != nil && !errors.Is(err, store.ErrInUse) {
			return apperror.Wrap(apperror.StoreStatusFailed, "failed to delete an unshared Codex login", err)
		}
	}
	for _, binding := range configBindings {
		if err := db.DeleteProfileConfigSetBinding(ctx, profileID, codexconfig.ProviderID, binding.SlotID); err != nil {
			return apperror.Wrap(apperror.StoreStatusFailed, "failed to delete a Codex Profile settings binding", err)
		}
		if err := db.DeleteProviderConfigSet(ctx, binding.ConfigSetID); err != nil && !errors.Is(err, store.ErrInUse) {
			return apperror.Wrap(apperror.StoreStatusFailed, "failed to delete unshared Codex settings", err)
		}
	}
	return nil
}
