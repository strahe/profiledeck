package profiletarget

import (
	"context"
	"errors"
	"sort"

	"github.com/strahe/profiledeck/internal/agent"
	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/maintenance"
	"github.com/strahe/profiledeck/internal/store"
	"github.com/strahe/profiledeck/internal/validate"
)

type Service struct {
	stores       store.Factory
	maintenance  maintenance.Runner
	policy       agent.Policy
	registry     agent.Registry
	reservations []ReservedPathLoader
}

type ReservedPath struct {
	ProviderID string
	TargetID   string
	Path       string
}

type ReservedPathLoader func(context.Context, *store.Store) ([]ReservedPath, error)

func NewService(
	stores store.Factory,
	maintenance maintenance.Runner,
	policy agent.Policy,
	registry agent.Registry,
	reservations ...ReservedPathLoader,
) *Service {
	return &Service{
		stores: stores, maintenance: maintenance, policy: policy, registry: registry,
		reservations: append([]ReservedPathLoader(nil), reservations...),
	}
}

type TextPreview struct {
	Content   string `json:"content"`
	Truncated bool   `json:"truncated"`
}

type ProfileTarget struct {
	ProfileID       string         `json:"profile_id"`
	ProviderID      string         `json:"provider_id"`
	TargetID        string         `json:"target_id"`
	Path            string         `json:"path"`
	Format          string         `json:"format"`
	Strategy        string         `json:"strategy"`
	Enabled         bool           `json:"enabled"`
	ValuePreview    TextPreview    `json:"value_preview"`
	Metadata        map[string]any `json:"metadata"`
	CreatedAtUnixMS int64          `json:"created_at_unix_ms"`
	UpdatedAtUnixMS int64          `json:"updated_at_unix_ms"`
}

type DeleteResult struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

type CreateProfileTargetRequest struct {
	ProfileID    string  `json:"profile_id"`
	ProviderID   string  `json:"provider_id"`
	TargetID     string  `json:"target_id"`
	Path         string  `json:"path"`
	Format       string  `json:"format"`
	Strategy     string  `json:"strategy"`
	ValueJSON    string  `json:"value_json"`
	Enabled      *bool   `json:"enabled,omitempty"`
	MetadataJSON *string `json:"metadata_json,omitempty"`
}

type UpdateProfileTargetRequest struct {
	ProfileID    string  `json:"profile_id"`
	ProviderID   string  `json:"provider_id"`
	TargetID     string  `json:"target_id"`
	Path         *string `json:"path,omitempty"`
	Format       *string `json:"format,omitempty"`
	Strategy     *string `json:"strategy,omitempty"`
	ValueJSON    *string `json:"value_json,omitempty"`
	Enabled      *bool   `json:"enabled,omitempty"`
	MetadataJSON *string `json:"metadata_json,omitempty"`
}

type ListProfileTargetsRequest struct {
	ProfileID       string `json:"profile_id"`
	ProviderID      string `json:"provider_id,omitempty"`
	IncludeDisabled bool   `json:"include_disabled"`
}

type GetProfileTargetRequest struct {
	ProfileID  string `json:"profile_id"`
	ProviderID string `json:"provider_id"`
	TargetID   string `json:"target_id"`
}

type DeleteProfileTargetRequest struct {
	ProfileID  string `json:"profile_id"`
	ProviderID string `json:"provider_id"`
	TargetID   string `json:"target_id"`
	Confirm    bool   `json:"confirm"`
}

func (service *Service) Create(ctx context.Context, req CreateProfileTargetRequest) (ProfileTarget, error) {
	normalized, appErr := normalizeCreateProfileTarget(req)
	if appErr != nil {
		return ProfileTarget{}, appErr
	}
	if service.managedProviderUsesTypedBindings(normalized.ProviderID) {
		return ProfileTarget{}, managedProviderTargetMutationError(normalized.ProviderID, normalized.TargetID)
	}
	if err := service.requireProvider(ctx, normalized.ProviderID); err != nil {
		return ProfileTarget{}, err
	}
	var target store.ProfileTarget
	err := service.maintenance.RunMaintenance(ctx, maintenance.Request{
		Operation: "profile-target-create", ProfileID: normalized.ProfileID, ProviderID: normalized.ProviderID,
		MetadataJSON: `{"kind":"profile-target-create"}`,
	}, func(ctx context.Context, tx *store.Store, _ string) error {
		if _, err := tx.GetProfile(ctx, normalized.ProfileID); err != nil {
			return mapProfileStoreError(err)
		}
		if _, err := tx.GetProvider(ctx, normalized.ProviderID); err != nil {
			return mapProviderStoreError(err)
		}
		if appErr := service.ensurePathOwnership(ctx, tx, normalized.Path, normalized.PathKey, normalized.ProviderID, normalized.TargetID, nil); appErr != nil {
			return appErr
		}
		var err error
		target, err = tx.CreateProfileTarget(ctx, normalized)
		return mapTargetStoreError(err)
	})
	if err != nil {
		return ProfileTarget{}, err
	}
	return profileTargetFromStore(target)
}

func (service *Service) Update(ctx context.Context, req UpdateProfileTargetRequest) (ProfileTarget, error) {
	ids, appErr := normalizeProfileTargetIDs(req.ProfileID, req.ProviderID, req.TargetID)
	if appErr != nil {
		return ProfileTarget{}, appErr
	}
	if service.managedProviderUsesTypedBindings(ids.ProviderID) {
		return ProfileTarget{}, managedProviderTargetMutationError(ids.ProviderID, ids.TargetID)
	}
	if req.Path == nil && req.Format == nil && req.Strategy == nil && req.ValueJSON == nil && req.Enabled == nil && req.MetadataJSON == nil {
		return ProfileTarget{}, apperror.New(apperror.TargetInvalid, "profile target update requires at least one changed field")
	}

	if err := service.requireProvider(ctx, ids.ProviderID); err != nil {
		return ProfileTarget{}, err
	}
	var target store.ProfileTarget
	err := service.maintenance.RunMaintenance(ctx, maintenance.Request{
		Operation: "profile-target-update", ProfileID: ids.ProfileID, ProviderID: ids.ProviderID,
		MetadataJSON: `{"kind":"profile-target-update"}`,
	}, func(ctx context.Context, tx *store.Store, _ string) error {
		existing, err := tx.GetProfileTarget(ctx, ids.ProfileID, ids.ProviderID, ids.TargetID)
		if err != nil {
			return mapTargetStoreError(err)
		}
		params, appErr := normalizeUpdateProfileTarget(req, existing)
		if appErr != nil {
			return appErr
		}
		finalPath := existing.Path
		finalPathKey := existing.PathKey
		if finalPathKey == "" {
			finalPathKey = PathOwnershipKey(existing.Path)
		}
		if params.Path != nil {
			finalPath = *params.Path
			if params.PathKey != nil {
				finalPathKey = *params.PathKey
			} else {
				finalPathKey = PathOwnershipKey(finalPath)
			}
		}
		if appErr := service.ensurePathOwnership(ctx, tx, finalPath, finalPathKey, existing.ProviderID, existing.TargetID, &ids); appErr != nil {
			return appErr
		}
		target, err = tx.UpdateProfileTarget(ctx, params)
		return mapTargetStoreError(err)
	})
	if err != nil {
		return ProfileTarget{}, err
	}
	return profileTargetFromStore(target)
}

func (service *Service) List(ctx context.Context, req ListProfileTargetsRequest) ([]ProfileTarget, error) {
	profileID, appErr := validate.ID(req.ProfileID, apperror.TargetInvalid)
	if appErr != nil {
		return nil, appErr.WithDetail("field", "profile_id")
	}
	providerID := ""
	if req.ProviderID != "" {
		value, appErr := validate.ID(req.ProviderID, apperror.TargetInvalid)
		if appErr != nil {
			return nil, appErr.WithDetail("field", "provider_id")
		}
		providerID = value
		if err := service.requireProvider(ctx, providerID); err != nil {
			return nil, err
		}
	}

	db, err := service.stores.OpenHealthy(ctx, true)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	targets, err := service.profileTargetsForRead(ctx, db, profileID, providerID, req.IncludeDisabled)
	if err != nil {
		return nil, err
	}
	result := make([]ProfileTarget, 0, len(targets))
	for _, target := range targets {
		value, err := profileTargetFromStore(target)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, nil
}

func (service *Service) Get(ctx context.Context, req GetProfileTargetRequest) (ProfileTarget, error) {
	ids, appErr := normalizeProfileTargetIDs(req.ProfileID, req.ProviderID, req.TargetID)
	if appErr != nil {
		return ProfileTarget{}, appErr
	}

	if err := service.requireProvider(ctx, ids.ProviderID); err != nil {
		return ProfileTarget{}, err
	}
	db, err := service.stores.OpenHealthy(ctx, true)
	if err != nil {
		return ProfileTarget{}, err
	}
	defer db.Close()

	if service.managedProviderUsesTypedBindings(ids.ProviderID) {
		return ProfileTarget{}, managedProviderTargetMutationError(ids.ProviderID, ids.TargetID)
	}
	target, err := db.GetProfileTarget(ctx, ids.ProfileID, ids.ProviderID, ids.TargetID)
	if err != nil {
		return ProfileTarget{}, mapTargetStoreError(err)
	}
	return profileTargetFromStore(target)
}

func (service *Service) profileTargetsForRead(ctx context.Context, db *store.Store, profileID, providerID string, includeDisabled bool) ([]store.ProfileTarget, error) {
	storedTargets, err := db.ListProfileTargets(ctx, profileID, providerID, includeDisabled)
	if err != nil {
		return nil, apperror.Wrap(apperror.StoreStatusFailed, "failed to list profile targets", err)
	}
	// Managed resources do not live in profile_targets. Ignore legacy raw rows;
	// typed Agent services expose their own bindings without leaking locators.
	targets := make([]store.ProfileTarget, 0, len(storedTargets))
	for _, target := range storedTargets {
		visible, visibilityErr := service.providerVisible(ctx, db, target.ProviderID)
		if visibilityErr != nil {
			return nil, visibilityErr
		}
		if visible && !service.managedProviderUsesTypedBindings(target.ProviderID) {
			targets = append(targets, target)
		}
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].ProviderID != targets[j].ProviderID {
			return targets[i].ProviderID < targets[j].ProviderID
		}
		return targets[i].TargetID < targets[j].TargetID
	})
	return targets, nil
}

func (service *Service) Delete(ctx context.Context, req DeleteProfileTargetRequest) (DeleteResult, error) {
	ids, appErr := normalizeProfileTargetIDs(req.ProfileID, req.ProviderID, req.TargetID)
	if appErr != nil {
		return DeleteResult{}, appErr
	}
	if service.managedProviderUsesTypedBindings(ids.ProviderID) {
		return DeleteResult{}, managedProviderTargetMutationError(ids.ProviderID, ids.TargetID)
	}
	if !req.Confirm {
		return DeleteResult{}, apperror.New(apperror.ConfirmationRequired, "profile target delete requires confirmation")
	}

	if err := service.requireProvider(ctx, ids.ProviderID); err != nil {
		return DeleteResult{}, err
	}
	err := service.maintenance.RunMaintenance(ctx, maintenance.Request{
		Operation: "profile-target-delete", ProfileID: ids.ProfileID, ProviderID: ids.ProviderID,
		MetadataJSON: `{"kind":"profile-target-delete"}`,
	}, func(ctx context.Context, tx *store.Store, _ string) error {
		return mapTargetStoreError(tx.DeleteProfileTarget(ctx, ids.ProfileID, ids.ProviderID, ids.TargetID))
	})
	if err != nil {
		return DeleteResult{}, err
	}
	return DeleteResult{ID: ids.TargetID, Deleted: true}, nil
}

func (service *Service) managedProviderUsesTypedBindings(providerID string) bool {
	_, managed := service.registry.AgentForProvider(providerID)
	return managed
}

func managedProviderTargetMutationError(providerID, targetID string) *apperror.Error {
	return apperror.New(apperror.TargetInvalid, "managed Provider targets can only be changed through Provider profile services").
		WithDetail("provider_id", providerID).
		WithDetail("target_id", targetID)
}

// Identity identifies one persisted Profile target when checking path reuse.
type Identity struct {
	ProfileID  string
	ProviderID string
	TargetID   string
}

func normalizeCreateProfileTarget(req CreateProfileTargetRequest) (store.CreateProfileTargetParams, *apperror.Error) {
	ids, appErr := normalizeProfileTargetIDs(req.ProfileID, req.ProviderID, req.TargetID)
	if appErr != nil {
		return store.CreateProfileTargetParams{}, appErr
	}
	path, appErr := ValidatePath(req.Path)
	if appErr != nil {
		return store.CreateProfileTargetParams{}, appErr
	}
	pathKey := PathOwnershipKey(path)
	format, strategy, valueJSON, appErr := Normalize(req.Format, req.Strategy, req.ValueJSON)
	if appErr != nil {
		return store.CreateProfileTargetParams{}, appErr
	}
	metadataJSON, appErr := validate.MetadataJSON(req.MetadataJSON, apperror.TargetInvalid)
	if appErr != nil {
		return store.CreateProfileTargetParams{}, appErr
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	return store.CreateProfileTargetParams{
		ProfileID:    ids.ProfileID,
		ProviderID:   ids.ProviderID,
		TargetID:     ids.TargetID,
		Path:         path,
		PathKey:      pathKey,
		Format:       format,
		Strategy:     strategy,
		ValueJSON:    valueJSON,
		Enabled:      enabled,
		MetadataJSON: metadataJSON,
	}, nil
}

func normalizeUpdateProfileTarget(req UpdateProfileTargetRequest, existing store.ProfileTarget) (store.UpdateProfileTargetParams, *apperror.Error) {
	finalFormat := existing.Format
	finalStrategy := existing.Strategy
	finalValueJSON := existing.ValueJSON

	params := store.UpdateProfileTargetParams{
		ProfileID:  existing.ProfileID,
		ProviderID: existing.ProviderID,
		TargetID:   existing.TargetID,
	}
	if req.Path != nil {
		path, appErr := ValidatePath(*req.Path)
		if appErr != nil {
			return store.UpdateProfileTargetParams{}, appErr
		}
		params.Path = &path
		pathKey := PathOwnershipKey(path)
		params.PathKey = &pathKey
	}
	if req.Format != nil {
		format, appErr := ValidateFormat(*req.Format)
		if appErr != nil {
			return store.UpdateProfileTargetParams{}, appErr
		}
		finalFormat = format
		params.Format = &format
	}
	if req.Strategy != nil {
		strategy, appErr := ValidateStrategy(*req.Strategy)
		if appErr != nil {
			return store.UpdateProfileTargetParams{}, appErr
		}
		finalStrategy = strategy
		params.Strategy = &strategy
	}
	if req.ValueJSON != nil {
		finalValueJSON = *req.ValueJSON
	}
	_, _, normalizedValueJSON, appErr := Normalize(finalFormat, finalStrategy, finalValueJSON)
	if appErr != nil {
		return store.UpdateProfileTargetParams{}, appErr
	}
	if req.ValueJSON != nil {
		params.ValueJSON = &normalizedValueJSON
	}
	if req.Format != nil || req.Strategy != nil {
		if req.ValueJSON == nil {
			params.ValueJSON = &normalizedValueJSON
		}
	}
	if req.Enabled != nil {
		enabled := *req.Enabled
		params.Enabled = &enabled
	}
	if req.MetadataJSON != nil {
		metadataJSON, appErr := validate.MetadataJSON(req.MetadataJSON, apperror.TargetInvalid)
		if appErr != nil {
			return store.UpdateProfileTargetParams{}, appErr
		}
		params.MetadataJSON = &metadataJSON
	}

	return params, nil
}

// EnsurePathOwnership rejects ambiguous reuse of a physical target path.
func EnsurePathOwnership(ctx context.Context, db *store.Store, path, pathKey, providerID, targetID string, current *Identity) *apperror.Error {
	targets, err := db.ListProfileTargetsByPathKey(ctx, pathKey)
	if err != nil {
		return apperror.Wrap(apperror.StoreStatusFailed, "failed to inspect target path ownership", err)
	}
	for _, target := range targets {
		if current != nil && target.ProfileID == current.ProfileID && target.ProviderID == current.ProviderID && target.TargetID == current.TargetID {
			continue
		}
		// One logical target may be shared across profiles; other path reuse would make apply ownership ambiguous.
		if target.ProviderID == providerID && target.TargetID == targetID {
			continue
		}
		return apperror.New(apperror.TargetAlreadyExists, "target path is already owned by another profile target").
			WithDetail("path", path).
			WithDetail("owner_profile_id", target.ProfileID).
			WithDetail("owner_provider_id", target.ProviderID).
			WithDetail("owner_target_id", target.TargetID)
	}
	return nil
}

func (service *Service) ensurePathOwnership(
	ctx context.Context,
	db *store.Store,
	path, pathKey, providerID, targetID string,
	current *Identity,
) *apperror.Error {
	if appErr := EnsurePathOwnership(ctx, db, path, pathKey, providerID, targetID, current); appErr != nil {
		return appErr
	}
	for _, load := range service.reservations {
		if load == nil {
			continue
		}
		reserved, err := load(ctx, db)
		if err != nil {
			return apperror.Wrap(apperror.StoreStatusFailed, "failed to inspect managed target path ownership", err)
		}
		for _, target := range reserved {
			if target.Path == "" || pathKey != PathOwnershipKey(target.Path) {
				continue
			}
			return apperror.New(apperror.TargetAlreadyExists, "target path is reserved by a managed Provider target").
				WithDetail("path", path).
				WithDetail("owner_provider_id", target.ProviderID).
				WithDetail("owner_target_id", target.TargetID)
		}
	}
	return nil
}

func normalizeProfileTargetIDs(profileID, providerID, targetID string) (Identity, *apperror.Error) {
	normalizedProfileID, appErr := validate.ID(profileID, apperror.TargetInvalid)
	if appErr != nil {
		return Identity{}, appErr.WithDetail("field", "profile_id")
	}
	normalizedProviderID, appErr := validate.ID(providerID, apperror.TargetInvalid)
	if appErr != nil {
		return Identity{}, appErr.WithDetail("field", "provider_id")
	}
	normalizedTargetID, appErr := validate.ID(targetID, apperror.TargetInvalid)
	if appErr != nil {
		return Identity{}, appErr.WithDetail("field", "target_id")
	}
	return Identity{
		ProfileID:  normalizedProfileID,
		ProviderID: normalizedProviderID,
		TargetID:   normalizedTargetID,
	}, nil
}

func profileTargetFromStore(target store.ProfileTarget) (ProfileTarget, error) {
	metadata, err := validate.StoredMetadata(target.MetadataJSON)
	if err != nil {
		return ProfileTarget{}, err
	}
	preview, err := targetValuePreview(target.ProviderID, target.TargetID, target.Format, target.Strategy, target.ValueJSON)
	if err != nil {
		return ProfileTarget{}, err
	}
	return ProfileTarget{
		ProfileID:       target.ProfileID,
		ProviderID:      target.ProviderID,
		TargetID:        target.TargetID,
		Path:            target.Path,
		Format:          target.Format,
		Strategy:        target.Strategy,
		Enabled:         target.Enabled,
		ValuePreview:    preview,
		Metadata:        metadata,
		CreatedAtUnixMS: target.CreatedAtUnixMS,
		UpdatedAtUnixMS: target.UpdatedAtUnixMS,
	}, nil
}

func targetValuePreview(_, _, format, strategy, raw string) (TextPreview, error) {
	_, appErr := DecodeSingleJSONObject(raw, apperror.StoreSchemaInvalid, "stored value_json")
	if appErr != nil {
		return TextPreview{}, appErr
	}
	preview, err := ValuePreview(format, strategy, raw)
	if err != nil {
		return TextPreview{}, err
	}
	return TextPreview(preview), nil
}

func mapTargetStoreError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		return apperror.New(apperror.TargetNotFound, "profile target not found")
	case errors.Is(err, store.ErrPathOwned):
		return apperror.New(apperror.TargetAlreadyExists, "target path is already owned by another profile target")
	case errors.Is(err, store.ErrAlreadyExists):
		return apperror.New(apperror.TargetAlreadyExists, "profile target already exists")
	default:
		return apperror.Wrap(apperror.StoreStatusFailed, "profile target store operation failed", err)
	}
}

func mapProviderStoreError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		return apperror.New(apperror.ProviderNotFound, "Provider not found")
	case errors.Is(err, store.ErrAlreadyExists):
		return apperror.New(apperror.ProviderAlreadyExists, "Provider already exists")
	case errors.Is(err, store.ErrInUse):
		return apperror.New(apperror.ProviderInUse, "Provider is in use")
	default:
		return apperror.Wrap(apperror.StoreStatusFailed, "Provider store operation failed", err)
	}
}

func mapProfileStoreError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		return apperror.New(apperror.ProfileNotFound, "Profile not found")
	case errors.Is(err, store.ErrAlreadyExists):
		return apperror.New(apperror.ProfileAlreadyExists, "Profile already exists")
	case errors.Is(err, store.ErrInUse):
		return apperror.New(apperror.ProfileInUse, "Profile is in use")
	default:
		return apperror.Wrap(apperror.StoreStatusFailed, "Profile store operation failed", err)
	}
}

func (service *Service) requireProvider(ctx context.Context, providerID string) error {
	if service.policy == nil {
		return nil
	}
	return service.policy.RequireProvider(ctx, providerID)
}

func (service *Service) providerVisible(ctx context.Context, db *store.Store, providerID string) (bool, error) {
	var err error
	if policy, ok := service.policy.(agent.StorePolicy); ok {
		err = policy.RequireProviderWithStore(ctx, db, providerID)
	} else {
		err = service.requireProvider(ctx, providerID)
	}
	if err == nil {
		return true, nil
	}
	var appErr *apperror.Error
	if errors.As(err, &appErr) && appErr.Code == apperror.AgentDisabled {
		return false, nil
	}
	return false, err
}
