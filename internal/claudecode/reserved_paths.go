package claudecode

import (
	"context"
	"errors"

	"github.com/strahe/profiledeck/internal/apperror"
	claudecodeconfig "github.com/strahe/profiledeck/internal/claudecode/config"
	"github.com/strahe/profiledeck/internal/profiletarget"
	"github.com/strahe/profiledeck/internal/store"
	switchtarget "github.com/strahe/profiledeck/internal/switching/target"
)

// ReservedPaths exposes a persisted file-backed credential target without
// exposing credential content or keychain locators.
func (service *Service) ReservedPaths(ctx context.Context, db *store.Store) ([]profiletarget.ReservedPath, error) {
	provider, err := db.GetProvider(ctx, claudecodeconfig.ProviderID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, apperror.Wrap(apperror.StoreStatusFailed, "failed to read Claude Code Provider target path", err)
	}
	metadata, err := validateClaudeCodeProvider(provider)
	if err != nil {
		return nil, err
	}
	file, ok := claudeCodeTargetSpec(metadata).(switchtarget.FileSpec)
	if !ok {
		return nil, nil
	}
	return []profiletarget.ReservedPath{{
		ProviderID: claudecodeconfig.ProviderID,
		TargetID:   claudecodeconfig.TargetID,
		Path:       file.Path,
	}}, nil
}
