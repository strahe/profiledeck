package codex

import (
	"context"
	"errors"

	"github.com/strahe/profiledeck/internal/apperror"
	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	codexpreset "github.com/strahe/profiledeck/internal/codex/preset"
	"github.com/strahe/profiledeck/internal/profiletarget"
	"github.com/strahe/profiledeck/internal/store"
)

// ReservedPaths exposes persisted Codex working-copy ownership to generic
// Profile Target validation even when the Provider or Desktop Agent is disabled.
func (service *Service) ReservedPaths(ctx context.Context, db *store.Store) ([]profiletarget.ReservedPath, error) {
	provider, err := db.GetProvider(ctx, codexconfig.ProviderID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, apperror.Wrap(apperror.StoreStatusFailed, "failed to read Codex Provider target paths", err)
	}
	metadata, err := codexpreset.DecodeProviderMetadata(provider.MetadataJSON)
	if err != nil || !metadata.Compatible() {
		return nil, apperror.New(apperror.StoreSchemaInvalid, "stored Codex Provider target paths are invalid")
	}
	return []profiletarget.ReservedPath{
		{ProviderID: codexconfig.ProviderID, TargetID: codexconfig.TargetID, Path: metadata.ConfigPath},
		{ProviderID: codexconfig.ProviderID, TargetID: codexconfig.AuthTargetID, Path: metadata.AuthPath},
	}, nil
}
