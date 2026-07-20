package profile

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/store"
)

const (
	DeleteReasonActive                 = "active"
	DeleteReasonUnresolvedOperation    = "unresolved_operation"
	DeleteReasonUnsupportedManagedData = "unsupported_managed_data"
)

// DeleteParticipant releases one managed Provider facet using database-only
// operations. External target state is intentionally outside this contract.
type DeleteParticipant interface {
	ProviderID() string
	DeleteProfileData(context.Context, *store.Store, string) error
}

type DeleteRegistry struct {
	byProvider map[string]DeleteParticipant
}

func NewDeleteRegistry(participants ...DeleteParticipant) (DeleteRegistry, error) {
	registry := DeleteRegistry{byProvider: make(map[string]DeleteParticipant, len(participants))}
	for _, participant := range participants {
		if participant == nil {
			return DeleteRegistry{}, fmt.Errorf("Profile delete participant is required")
		}
		providerID := strings.TrimSpace(participant.ProviderID())
		if providerID == "" {
			return DeleteRegistry{}, fmt.Errorf("Profile delete participant Provider id is required")
		}
		if _, exists := registry.byProvider[providerID]; exists {
			return DeleteRegistry{}, fmt.Errorf("duplicate Profile delete participant for Provider %q", providerID)
		}
		registry.byProvider[providerID] = participant
	}
	return registry, nil
}

func MustDeleteRegistry(participants ...DeleteParticipant) DeleteRegistry {
	registry, err := NewDeleteRegistry(participants...)
	if err != nil {
		panic(err)
	}
	return registry
}

func UnsupportedManagedDataError() error {
	return deleteBlockedError(
		DeleteReasonUnsupportedManagedData,
		"Profile contains unsupported saved data; remove or repair that data and try again",
	)
}

func deleteBlockedError(reason, message string) error {
	return apperror.New(apperror.ProfileInUse, message).WithDetail("reason", reason)
}

func (registry DeleteRegistry) participant(providerID string) (DeleteParticipant, bool) {
	participant, ok := registry.byProvider[strings.TrimSpace(providerID)]
	return participant, ok
}

func (service *Service) deleteManagedData(ctx context.Context, tx *store.Store, profileID string) error {
	credentialBindings, err := tx.ListProfileCredentialBindings(ctx, profileID, "")
	if err != nil {
		return apperror.Wrap(apperror.StoreStatusFailed, "failed to inspect Profile login bindings", err)
	}
	configBindings, err := tx.ListProfileConfigSetBindings(ctx, profileID, "")
	if err != nil {
		return apperror.Wrap(apperror.StoreStatusFailed, "failed to inspect Profile settings bindings", err)
	}

	providerSet := make(map[string]struct{}, len(credentialBindings)+len(configBindings))
	for _, binding := range credentialBindings {
		providerSet[binding.ProviderID] = struct{}{}
	}
	for _, binding := range configBindings {
		providerSet[binding.ProviderID] = struct{}{}
	}
	providerIDs := make([]string, 0, len(providerSet))
	for providerID := range providerSet {
		if _, ok := service.deleteRegistry.participant(providerID); !ok {
			return UnsupportedManagedDataError()
		}
		providerIDs = append(providerIDs, providerID)
	}
	sort.Strings(providerIDs)
	for _, providerID := range providerIDs {
		participant, _ := service.deleteRegistry.participant(providerID)
		if err := participant.DeleteProfileData(ctx, tx, profileID); err != nil {
			return err
		}
	}
	return nil
}
