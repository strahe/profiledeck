package claudecode

import (
	"context"
	"errors"

	claudecodeconfig "github.com/strahe/profiledeck/internal/claudecode/config"
	"github.com/strahe/profiledeck/internal/store"
	"github.com/strahe/profiledeck/internal/usage"
)

type usageProvisioner struct{}

func NewUsageProvisioner() usage.ProviderProvisioner { return usageProvisioner{} }

func (usageProvisioner) Ensure(ctx context.Context, db *store.Store, mode usage.SyncProvisionMode) error {
	provider, err := db.GetProvider(ctx, claudecodeconfig.ProviderID)
	if err == nil {
		_, err = validateClaudeCodeProvider(provider)
		return err
	}
	if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if mode != usage.SyncProvisionProvider {
		return store.ErrUsageProviderMissing
	}
	locator, err := claudecodeconfig.ResolveLocator()
	if err != nil {
		return err
	}
	metadata := newClaudeCodeProviderMetadata(locator)
	if err := ensureClaudeCodePathOwnership(ctx, db, metadata); err != nil {
		return err
	}
	return createClaudeCodeProvider(ctx, db, metadata)
}
