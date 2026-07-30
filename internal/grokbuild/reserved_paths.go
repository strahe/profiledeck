package grokbuild

import (
	"context"
	"errors"

	"github.com/strahe/profiledeck/internal/apperror"
	grokconfig "github.com/strahe/profiledeck/internal/grokbuild/config"
	grokpreset "github.com/strahe/profiledeck/internal/grokbuild/preset"
	"github.com/strahe/profiledeck/internal/profiletarget"
	"github.com/strahe/profiledeck/internal/store"
)

func (service *Service) ReservedPaths(ctx context.Context, db *store.Store) ([]profiletarget.ReservedPath, error) {
	stored, err := db.GetProvider(ctx, grokconfig.ProviderID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, apperror.Wrap(apperror.StoreStatusFailed, "failed to read Grok Build target paths", err)
	}
	metadata, err := grokpreset.DecodeProviderMetadata(stored.MetadataJSON)
	if err != nil || !metadata.Compatible() {
		return nil, apperror.New(apperror.StoreSchemaInvalid, "stored Grok Build target paths are invalid")
	}
	return []profiletarget.ReservedPath{
		{ProviderID: grokconfig.ProviderID, TargetID: grokconfig.AuthTargetID, Path: metadata.AuthPath},
		{ProviderID: grokconfig.ProviderID, TargetID: grokconfig.ConfigTargetID, Path: metadata.ConfigPath},
	}, nil
}
