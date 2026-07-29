package profile

import (
	"context"
	"errors"

	"github.com/strahe/profiledeck/internal/apperror"
	grokconfig "github.com/strahe/profiledeck/internal/grokbuild/config"
	grokpreset "github.com/strahe/profiledeck/internal/grokbuild/preset"
	globalprofile "github.com/strahe/profiledeck/internal/profile"
	"github.com/strahe/profiledeck/internal/store"
)

type DeleteParticipant struct{}

func (DeleteParticipant) ProviderID() string { return grokconfig.ProviderID }

func (DeleteParticipant) DeleteProfileData(ctx context.Context, db *store.Store, profileID string) error {
	credentials, err := db.ListProfileCredentialBindings(ctx, profileID, grokconfig.ProviderID)
	if err != nil {
		return apperror.Wrap(apperror.StoreStatusFailed, "failed to inspect Grok Build login bindings", err)
	}
	configSets, err := db.ListProfileConfigSetBindings(ctx, profileID, grokconfig.ProviderID)
	if err != nil {
		return apperror.Wrap(apperror.StoreStatusFailed, "failed to inspect Grok Build Config Set bindings", err)
	}
	for _, binding := range credentials {
		credential, err := db.GetProviderCredential(ctx, binding.CredentialID)
		if err != nil || binding.SlotID != grokpreset.CredentialSlotAuth ||
			credential.ProviderID != grokconfig.ProviderID ||
			credential.CredentialKind != grokpreset.CredentialKindAuthJSON {
			return globalprofile.UnsupportedManagedDataError()
		}
	}
	for _, binding := range configSets {
		configSet, err := db.GetProviderConfigSet(ctx, grokconfig.ProviderID, binding.ConfigSetID)
		if err != nil || binding.SlotID != grokpreset.ConfigSetSlotUserConfig ||
			configSet.ProviderID != grokconfig.ProviderID ||
			configSet.ConfigKind != grokpreset.ConfigSetKindTOML {
			return globalprofile.UnsupportedManagedDataError()
		}
	}
	for _, binding := range credentials {
		if err := db.DeleteProfileCredentialBinding(ctx, profileID, grokconfig.ProviderID, binding.SlotID); err != nil {
			return err
		}
		if err := db.DeleteProviderCredential(ctx, binding.CredentialID); err != nil && !errors.Is(err, store.ErrInUse) {
			return err
		}
	}
	for _, binding := range configSets {
		if err := db.DeleteProfileConfigSetBinding(ctx, profileID, grokconfig.ProviderID, binding.SlotID); err != nil {
			return err
		}
		if err := db.DeleteProviderConfigSet(ctx, grokconfig.ProviderID, binding.ConfigSetID); err != nil && !errors.Is(err, store.ErrInUse) {
			return err
		}
	}
	return nil
}
