package profile

import (
	"context"

	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	codexpreset "github.com/strahe/profiledeck/internal/codex/preset"
	"github.com/strahe/profiledeck/internal/store"
)

// ProfileFields contains already-validated Profile metadata for a Codex facet.
type ProfileFields struct {
	CreateName        string
	CreateDescription string
	UpdateName        *string
	UpdateDescription *string
}

// UpsertProvider persists the fixed Codex provider record.
func UpsertProvider(ctx context.Context, db *store.Store, metadataJSON string, exists bool) (store.Provider, error) {
	if !exists {
		provider, err := db.CreateProvider(ctx, store.CreateProviderParams{
			ID:           codexconfig.ProviderID,
			Name:         codexpreset.ProviderName,
			AdapterID:    codexconfig.AdapterID,
			MetadataJSON: metadataJSON,
		})
		if err != nil {
			return store.Provider{}, mapProviderStoreError(err)
		}
		return provider, nil
	}
	name := codexpreset.ProviderName
	provider, err := db.UpdateProvider(ctx, store.UpdateProviderParams{
		ID:           codexconfig.ProviderID,
		Name:         &name,
		MetadataJSON: &metadataJSON,
	})
	if err != nil {
		return store.Provider{}, mapProviderStoreError(err)
	}
	return provider, nil
}

// UpsertProfile creates or updates the global Profile fields owned by a Codex operation.
func UpsertProfile(ctx context.Context, db *store.Store, profileID string, fields ProfileFields, exists bool) (store.Profile, error) {
	if !exists {
		profile, err := db.CreateProfile(ctx, store.CreateProfileParams{
			ID:           profileID,
			Name:         fields.CreateName,
			Description:  fields.CreateDescription,
			MetadataJSON: "{}",
		})
		if err != nil {
			return store.Profile{}, mapProfileStoreError(err)
		}
		return profile, nil
	}
	profile, err := db.GetProfile(ctx, profileID)
	if err != nil {
		return store.Profile{}, mapProfileStoreError(err)
	}
	if fields.UpdateName == nil && fields.UpdateDescription == nil {
		return profile, nil
	}
	profile, err = db.UpdateProfile(ctx, store.UpdateProfileParams{
		ID:          profileID,
		Name:        fields.UpdateName,
		Description: fields.UpdateDescription,
	})
	if err != nil {
		return store.Profile{}, mapProfileStoreError(err)
	}
	return profile, nil
}
