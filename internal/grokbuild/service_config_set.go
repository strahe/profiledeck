package grokbuild

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/strahe/profiledeck/internal/apperror"
	grokconfig "github.com/strahe/profiledeck/internal/grokbuild/config"
	grokpreset "github.com/strahe/profiledeck/internal/grokbuild/preset"
	grokprofile "github.com/strahe/profiledeck/internal/grokbuild/profile"
	"github.com/strahe/profiledeck/internal/maintenance"
	"github.com/strahe/profiledeck/internal/store"
)

type CreateConfigSetRequest struct {
	ConfigSetID string `json:"config_set_id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type CopyConfigSetRequest struct {
	SourceConfigSetID string `json:"source_config_set_id"`
	ConfigSetID       string `json:"config_set_id"`
	Name              string `json:"name"`
	Description       string `json:"description,omitempty"`
}

type UpdateConfigSetRequest struct {
	ConfigSetID string  `json:"config_set_id"`
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
}

type ConfigSetListResult struct {
	ConfigSets []ConfigSet `json:"config_sets"`
}

// ConfigSet intentionally contains no configuration body or content-derived
// fingerprint because Grok Build settings are sensitive at every output boundary.
type ConfigSet struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Description     string `json:"description,omitempty"`
	ReferenceCount  int    `json:"reference_count"`
	Active          bool   `json:"active"`
	CreatedAtUnixMS int64  `json:"created_at_unix_ms"`
	UpdatedAtUnixMS int64  `json:"updated_at_unix_ms"`
}

func (service *Service) ListConfigSets(ctx context.Context) (ConfigSetListResult, error) {
	if err := service.requireAccess(ctx); err != nil {
		return ConfigSetListResult{}, err
	}
	db, err := service.openStore(ctx, true)
	if err != nil {
		return ConfigSetListResult{}, err
	}
	defer db.Close()
	if _, err := requireProvider(ctx, db); err != nil {
		return ConfigSetListResult{}, err
	}
	values, err := db.ListProviderConfigSets(ctx, grokconfig.ProviderID, grokpreset.ConfigSetKindTOML)
	if err != nil {
		return ConfigSetListResult{}, err
	}
	activeID, err := displayActiveConfigSetID(ctx, db)
	if err != nil {
		return ConfigSetListResult{}, err
	}
	result := make([]ConfigSet, 0, len(values))
	for _, value := range values {
		public, err := configSetFromStore(ctx, db, value, activeID)
		if err != nil {
			return ConfigSetListResult{}, err
		}
		result = append(result, public)
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Active != result[j].Active {
			return result[i].Active
		}
		if strings.EqualFold(result[i].Name, result[j].Name) {
			return result[i].ID < result[j].ID
		}
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})
	return ConfigSetListResult{ConfigSets: result}, nil
}

func (service *Service) GetConfigSet(ctx context.Context, rawID string) (ConfigSet, error) {
	if err := service.requireAccess(ctx); err != nil {
		return ConfigSet{}, err
	}
	id, appErr := validateID(rawID, apperror.GrokBuildInvalid)
	if appErr != nil {
		return ConfigSet{}, appErr
	}
	db, err := service.openStore(ctx, true)
	if err != nil {
		return ConfigSet{}, err
	}
	defer db.Close()
	if _, err := requireProvider(ctx, db); err != nil {
		return ConfigSet{}, err
	}
	value, err := grokprofile.RequireConfigSet(ctx, db, id)
	if err != nil {
		return ConfigSet{}, err
	}
	activeID, err := displayActiveConfigSetID(ctx, db)
	if err != nil {
		return ConfigSet{}, err
	}
	return configSetFromStore(ctx, db, value, activeID)
}

func (service *Service) CreateConfigSet(ctx context.Context, req CreateConfigSetRequest) (ConfigSet, error) {
	if err := service.requireAccess(ctx); err != nil {
		return ConfigSet{}, err
	}
	id, name, description, err := normalizeConfigSetFields(req.ConfigSetID, req.Name, req.Description)
	if err != nil {
		return ConfigSet{}, err
	}
	home, err := service.resolveExistingHome()
	if err != nil {
		return ConfigSet{}, err
	}
	var stored store.ProviderConfigSet
	err = service.maintenance.RunMaintenance(ctx, maintenance.Request{
		Operation: "grok-build-config-set-create", ProviderID: grokconfig.ProviderID,
	}, func(ctx context.Context, tx *store.Store, _ string) error {
		_, providerExists, err := preflightProvider(ctx, tx, home)
		if err != nil {
			return err
		}
		if _, err := tx.GetProviderConfigSet(ctx, grokconfig.ProviderID, id); err == nil {
			return apperror.New(apperror.ProfileAlreadyExists, "Grok Build Config Set already exists")
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		snapshot, err := readConfigSnapshot(home)
		if err != nil {
			return err
		}
		metadataJSON, err := grokpreset.ProviderMetadataJSON(home)
		if err != nil {
			return err
		}
		if _, err := grokprofile.UpsertProvider(ctx, tx, metadataJSON, providerExists); err != nil {
			return err
		}
		stored, err = grokprofile.UpsertConfigSet(ctx, tx, id, name, description, snapshot.Content)
		if err != nil {
			return err
		}
		return validateConfigSnapshot(home, snapshot)
	})
	if err != nil {
		return ConfigSet{}, err
	}
	return service.publicConfigSet(ctx, stored)
}

func (service *Service) CopyConfigSet(ctx context.Context, req CopyConfigSetRequest) (ConfigSet, error) {
	if err := service.requireAccess(ctx); err != nil {
		return ConfigSet{}, err
	}
	sourceID, appErr := validateID(req.SourceConfigSetID, apperror.GrokBuildInvalid)
	if appErr != nil {
		return ConfigSet{}, appErr
	}
	id, name, description, err := normalizeConfigSetFields(req.ConfigSetID, req.Name, req.Description)
	if err != nil {
		return ConfigSet{}, err
	}
	if sourceID == id {
		return ConfigSet{}, apperror.New(apperror.GrokBuildInvalid, "source and destination Config Set ids must differ")
	}
	var stored store.ProviderConfigSet
	err = service.maintenance.RunMaintenance(ctx, maintenance.Request{
		Operation: "grok-build-config-set-copy", ProviderID: grokconfig.ProviderID,
	}, func(ctx context.Context, tx *store.Store, _ string) error {
		if _, err := requireProvider(ctx, tx); err != nil {
			return err
		}
		source, err := grokprofile.RequireConfigSet(ctx, tx, sourceID)
		if err != nil {
			return err
		}
		if _, err := tx.GetProviderConfigSet(ctx, grokconfig.ProviderID, id); err == nil {
			return apperror.New(apperror.ProfileAlreadyExists, "Grok Build Config Set already exists")
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		stored, err = grokprofile.UpsertConfigSet(ctx, tx, id, name, description, source.PayloadText)
		return err
	})
	if err != nil {
		return ConfigSet{}, err
	}
	return service.publicConfigSet(ctx, stored)
}

func (service *Service) UpdateConfigSet(ctx context.Context, req UpdateConfigSetRequest) (ConfigSet, error) {
	if err := service.requireAccess(ctx); err != nil {
		return ConfigSet{}, err
	}
	id, appErr := validateID(req.ConfigSetID, apperror.GrokBuildInvalid)
	if appErr != nil {
		return ConfigSet{}, appErr
	}
	var name *string
	if req.Name != nil {
		value, appErr := validateName(*req.Name, apperror.GrokBuildInvalid)
		if appErr != nil {
			return ConfigSet{}, appErr
		}
		name = &value
	}
	var description *string
	if req.Description != nil {
		value, appErr := validateDescription(*req.Description, apperror.GrokBuildInvalid)
		if appErr != nil {
			return ConfigSet{}, appErr
		}
		description = &value
	}
	var stored store.ProviderConfigSet
	err := service.maintenance.RunMaintenance(ctx, maintenance.Request{
		Operation: "grok-build-config-set-update", ProviderID: grokconfig.ProviderID,
	}, func(ctx context.Context, tx *store.Store, _ string) error {
		if _, err := grokprofile.RequireConfigSet(ctx, tx, id); err != nil {
			return err
		}
		var err error
		stored, err = tx.UpdateProviderConfigSet(ctx, store.UpdateProviderConfigSetParams{
			ProviderID: grokconfig.ProviderID, ID: id, Name: name, Description: description,
		})
		return err
	})
	if err != nil {
		return ConfigSet{}, err
	}
	return service.publicConfigSet(ctx, stored)
}

func (service *Service) DeleteConfigSet(ctx context.Context, rawID string) error {
	if err := service.requireAccess(ctx); err != nil {
		return err
	}
	id, appErr := validateID(rawID, apperror.GrokBuildInvalid)
	if appErr != nil {
		return appErr
	}
	return service.maintenance.RunMaintenance(ctx, maintenance.Request{
		Operation: "grok-build-config-set-delete", ProviderID: grokconfig.ProviderID,
	}, func(ctx context.Context, tx *store.Store, _ string) error {
		if _, err := grokprofile.RequireConfigSet(ctx, tx, id); err != nil {
			return err
		}
		err := tx.DeleteProviderConfigSet(ctx, grokconfig.ProviderID, id)
		if errors.Is(err, store.ErrInUse) {
			return apperror.New(apperror.ProfileInUse, "Grok Build Config Set is in use")
		}
		return err
	})
}

func normalizeConfigSetFields(idRaw, nameRaw, descriptionRaw string) (string, string, string, error) {
	id, appErr := validateID(idRaw, apperror.GrokBuildInvalid)
	if appErr != nil {
		return "", "", "", appErr
	}
	name, appErr := validateName(nameRaw, apperror.GrokBuildInvalid)
	if appErr != nil {
		return "", "", "", appErr
	}
	description, appErr := validateDescription(descriptionRaw, apperror.GrokBuildInvalid)
	if appErr != nil {
		return "", "", "", appErr
	}
	return id, name, description, nil
}

func (service *Service) publicConfigSet(ctx context.Context, stored store.ProviderConfigSet) (ConfigSet, error) {
	db, err := service.openStore(ctx, true)
	if err != nil {
		return ConfigSet{}, err
	}
	defer db.Close()
	activeID, err := displayActiveConfigSetID(ctx, db)
	if err != nil {
		return ConfigSet{}, err
	}
	return configSetFromStore(ctx, db, stored, activeID)
}

func configSetFromStore(ctx context.Context, db *store.Store, stored store.ProviderConfigSet, activeID string) (ConfigSet, error) {
	if err := grokprofile.ValidateConfigSetRecord(grokprofile.ConfigSetRecord{
		ID: stored.ID, ProviderID: stored.ProviderID, ConfigKind: stored.ConfigKind,
		Name: stored.Name, Description: stored.Description, PayloadText: stored.PayloadText,
		PayloadSHA256: stored.PayloadSHA256, MetadataJSON: stored.MetadataJSON,
	}); err != nil {
		return ConfigSet{}, err
	}
	references, err := grokprofile.ConfigSetBindingCount(ctx, db, stored.ID)
	if err != nil {
		return ConfigSet{}, err
	}
	return ConfigSet{
		ID: stored.ID, Name: stored.Name, Description: stored.Description,
		ReferenceCount: references, Active: stored.ID == activeID,
		CreatedAtUnixMS: stored.CreatedAtUnixMS, UpdatedAtUnixMS: stored.UpdatedAtUnixMS,
	}, nil
}

func activeConfigSetID(ctx context.Context, db *store.Store) (string, error) {
	active, exists, err := activeState(ctx, db)
	if err != nil || !exists {
		return "", err
	}
	targets, err := grokprofile.StoredBindingTargets(ctx, db, active.ProfileID)
	if err != nil {
		return "", err
	}
	configTarget, _, err := grokprofile.FullProfileTargets(active.ProfileID, targets)
	if err != nil {
		return "", err
	}
	return grokprofile.ConfigSetIDFromTarget(configTarget)
}

func displayActiveConfigSetID(ctx context.Context, db *store.Store) (string, error) {
	if _, err := grokprofile.StoredHome(ctx, db); err != nil {
		return "", err
	}
	id, err := activeConfigSetID(ctx, db)
	var appErr *apperror.Error
	if errors.As(err, &appErr) &&
		(appErr.Code == apperror.GrokBuildInvalid || appErr.Code == apperror.StoreSchemaInvalid) {
		// Keep Config Set management available when only the active binding
		// needs repair; provider and storage failures still fail closed.
		return "", nil
	}
	return id, err
}
