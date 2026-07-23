package codex

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/strahe/profiledeck/internal/apperror"
	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	codexpreset "github.com/strahe/profiledeck/internal/codex/preset"
	"github.com/strahe/profiledeck/internal/maintenance"
	"github.com/strahe/profiledeck/internal/store"
)

type CreateCodexConfigSetRequest struct {
	ConfigSetID   string  `json:"config_set_id"`
	Name          string  `json:"name"`
	Description   string  `json:"description,omitempty"`
	ConfigContent *string `json:"config_content,omitempty"`
}

type CopyCodexConfigSetRequest struct {
	SourceConfigSetID string `json:"source_config_set_id"`
	ConfigSetID       string `json:"config_set_id"`
	Name              string `json:"name"`
	Description       string `json:"description,omitempty"`
}

type UpdateCodexConfigSetRequest struct {
	ConfigSetID string  `json:"config_set_id"`
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
}

type CodexConfigSetListResult struct {
	ConfigSets []CodexConfigSet `json:"config_sets"`
}

type CodexConfigSet struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Description     string `json:"description,omitempty"`
	ReferenceCount  int    `json:"reference_count"`
	Active          bool   `json:"active"`
	Model           string `json:"model,omitempty"`
	ModelProvider   string `json:"model_provider,omitempty"`
	OpenAIBaseURL   string `json:"openai_base_url,omitempty"`
	PayloadSHA256   string `json:"payload_sha256"`
	CreatedAtUnixMS int64  `json:"created_at_unix_ms"`
	UpdatedAtUnixMS int64  `json:"updated_at_unix_ms"`
}

func (service *Service) ListConfigSets(ctx context.Context) (CodexConfigSetListResult, error) {
	if err := service.requireAccess(ctx); err != nil {
		return CodexConfigSetListResult{}, err
	}
	db, err := service.openStore(ctx, true)
	if err != nil {
		return CodexConfigSetListResult{}, err
	}
	defer db.Close()
	if _, err := requireCodexProvider(ctx, db); err != nil {
		return CodexConfigSetListResult{}, err
	}

	sets, err := db.ListProviderConfigSets(ctx, codexconfig.ProviderID, codexpreset.ConfigSetKindTOML)
	if err != nil {
		return CodexConfigSetListResult{}, mapCodexConfigSetStoreError(err)
	}
	activeID, err := activeCodexConfigSetID(ctx, db)
	if err != nil {
		// Keep Config Set management available for repair without hiding storage failures.
		if !isInvalidCodexActiveBinding(err) {
			return CodexConfigSetListResult{}, err
		}
		activeID = ""
	}
	result := make([]CodexConfigSet, 0, len(sets))
	for _, configSet := range sets {
		public, err := codexConfigSetFromStore(ctx, db, configSet, activeID)
		if err != nil {
			return CodexConfigSetListResult{}, err
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
	return CodexConfigSetListResult{ConfigSets: result}, nil
}

func isInvalidCodexActiveBinding(err error) bool {
	var appErr *apperror.Error
	if !errors.As(err, &appErr) {
		return false
	}
	return appErr.Code == apperror.CodexInvalid || appErr.Code == apperror.StoreSchemaInvalid
}

func (service *Service) GetConfigSet(ctx context.Context, rawID string) (CodexConfigSet, error) {
	if err := service.requireAccess(ctx); err != nil {
		return CodexConfigSet{}, err
	}
	id, appErr := validateID(rawID, apperror.CodexInvalid)
	if appErr != nil {
		return CodexConfigSet{}, appErr
	}
	db, err := service.openStore(ctx, true)
	if err != nil {
		return CodexConfigSet{}, err
	}
	defer db.Close()
	if _, err := requireCodexProvider(ctx, db); err != nil {
		return CodexConfigSet{}, err
	}
	configSet, err := requireCodexConfigSet(ctx, db, id)
	if err != nil {
		return CodexConfigSet{}, err
	}
	activeID, err := activeCodexConfigSetID(ctx, db)
	if err != nil {
		return CodexConfigSet{}, err
	}
	return codexConfigSetFromStore(ctx, db, configSet, activeID)
}

func (service *Service) CreateConfigSet(ctx context.Context, req CreateCodexConfigSetRequest) (CodexConfigSet, error) {
	if err := service.requireAccess(ctx); err != nil {
		return CodexConfigSet{}, err
	}
	id, name, description, err := normalizeCodexConfigSetFields(req.ConfigSetID, req.Name, req.Description)
	if err != nil {
		return CodexConfigSet{}, err
	}
	home, err := service.resolveExistingHome()
	if err != nil {
		return CodexConfigSet{}, err
	}
	content, err := loadCodexConfigContent(home, req.ConfigContent)
	if err != nil {
		return CodexConfigSet{}, err
	}
	var stored store.ProviderConfigSet
	err = service.maintenance.RunMaintenance(ctx, maintenance.Request{
		Operation: "codex-config-set-create", ProviderID: codexconfig.ProviderID, Record: false,
	}, func(ctx context.Context, txStore *store.Store, _ string) error {
		_, hasProvider, err := codexPreflightProvider(ctx, txStore, home)
		if err != nil {
			return err
		}
		if _, getErr := txStore.GetProviderConfigSet(ctx, codexconfig.ProviderID, id); getErr == nil {
			return apperror.New(apperror.ProfileAlreadyExists, "Codex config set already exists").WithDetail("config_set_id", id)
		} else if !errors.Is(getErr, store.ErrNotFound) {
			return mapCodexConfigSetStoreError(getErr)
		}
		metadataJSON, err := codexpreset.ProviderMetadataJSON(home)
		if err != nil {
			return apperror.Wrap(apperror.CodexInvalid, "failed to encode Codex provider metadata", err)
		}
		if _, err := upsertCodexProvider(ctx, txStore, metadataJSON, hasProvider); err != nil {
			return err
		}
		stored, err = upsertCodexConfigSet(ctx, txStore, id, name, description, content)
		return err
	})
	if err != nil {
		return CodexConfigSet{}, wrapCodexMutationTxError("Codex config set create transaction failed", err)
	}
	db, err := service.openStore(ctx, true)
	if err != nil {
		return CodexConfigSet{}, err
	}
	defer db.Close()
	activeID, err := activeCodexConfigSetID(ctx, db)
	if err != nil {
		return CodexConfigSet{}, err
	}
	return codexConfigSetFromStore(ctx, db, stored, activeID)
}

func (service *Service) CopyConfigSet(ctx context.Context, req CopyCodexConfigSetRequest) (CodexConfigSet, error) {
	if err := service.requireAccess(ctx); err != nil {
		return CodexConfigSet{}, err
	}
	sourceID, appErr := validateID(req.SourceConfigSetID, apperror.CodexInvalid)
	if appErr != nil {
		return CodexConfigSet{}, appErr
	}
	id, name, description, err := normalizeCodexConfigSetFields(req.ConfigSetID, req.Name, req.Description)
	if err != nil {
		return CodexConfigSet{}, err
	}
	if sourceID == id {
		return CodexConfigSet{}, apperror.New(apperror.CodexInvalid, "source and destination config set ids must differ")
	}
	var stored store.ProviderConfigSet
	err = service.maintenance.RunMaintenance(ctx, maintenance.Request{
		Operation: "codex-config-set-copy", ProviderID: codexconfig.ProviderID, Record: false,
	}, func(ctx context.Context, txStore *store.Store, _ string) error {
		if _, err := requireCodexProvider(ctx, txStore); err != nil {
			return err
		}
		source, err := requireCodexConfigSet(ctx, txStore, sourceID)
		if err != nil {
			return err
		}
		if _, getErr := txStore.GetProviderConfigSet(ctx, codexconfig.ProviderID, id); getErr == nil {
			return apperror.New(apperror.ProfileAlreadyExists, "Codex config set already exists").WithDetail("config_set_id", id)
		} else if !errors.Is(getErr, store.ErrNotFound) {
			return mapCodexConfigSetStoreError(getErr)
		}
		stored, err = upsertCodexConfigSet(ctx, txStore, id, name, description, source.PayloadText)
		return err
	})
	if err != nil {
		return CodexConfigSet{}, wrapCodexMutationTxError("Codex config set copy transaction failed", err)
	}
	db, err := service.openStore(ctx, true)
	if err != nil {
		return CodexConfigSet{}, err
	}
	defer db.Close()
	activeID, err := activeCodexConfigSetID(ctx, db)
	if err != nil {
		return CodexConfigSet{}, err
	}
	return codexConfigSetFromStore(ctx, db, stored, activeID)
}

func (service *Service) UpdateConfigSet(ctx context.Context, req UpdateCodexConfigSetRequest) (CodexConfigSet, error) {
	if err := service.requireAccess(ctx); err != nil {
		return CodexConfigSet{}, err
	}
	id, appErr := validateID(req.ConfigSetID, apperror.CodexInvalid)
	if appErr != nil {
		return CodexConfigSet{}, appErr
	}
	var name *string
	if req.Name != nil {
		value, appErr := validateName(*req.Name, apperror.CodexInvalid)
		if appErr != nil {
			return CodexConfigSet{}, appErr
		}
		name = &value
	}
	var description *string
	if req.Description != nil {
		value, appErr := validateDescription(*req.Description, apperror.CodexInvalid)
		if appErr != nil {
			return CodexConfigSet{}, appErr
		}
		description = &value
	}
	var stored store.ProviderConfigSet
	err := service.maintenance.RunMaintenance(ctx, maintenance.Request{
		Operation: "codex-config-set-update", ProviderID: codexconfig.ProviderID, Record: false,
	}, func(ctx context.Context, txStore *store.Store, _ string) error {
		if _, err := requireCodexProvider(ctx, txStore); err != nil {
			return err
		}
		var err error
		stored, err = txStore.UpdateProviderConfigSet(ctx, store.UpdateProviderConfigSetParams{
			ProviderID:  codexconfig.ProviderID,
			ID:          id,
			Name:        name,
			Description: description,
		})
		return mapCodexConfigSetStoreError(err)
	})
	if err != nil {
		return CodexConfigSet{}, err
	}
	db, err := service.openStore(ctx, true)
	if err != nil {
		return CodexConfigSet{}, err
	}
	defer db.Close()
	activeID, err := activeCodexConfigSetID(ctx, db)
	if err != nil {
		return CodexConfigSet{}, err
	}
	return codexConfigSetFromStore(ctx, db, stored, activeID)
}

func (service *Service) DeleteConfigSet(ctx context.Context, rawID string) error {
	if err := service.requireAccess(ctx); err != nil {
		return err
	}
	id, appErr := validateID(rawID, apperror.CodexInvalid)
	if appErr != nil {
		return appErr
	}
	return service.maintenance.RunMaintenance(ctx, maintenance.Request{
		Operation: "codex-config-set-delete", ProviderID: codexconfig.ProviderID, Record: false,
	}, func(ctx context.Context, txStore *store.Store, _ string) error {
		if _, err := requireCodexProvider(ctx, txStore); err != nil {
			return err
		}
		return mapCodexConfigSetStoreError(txStore.DeleteProviderConfigSet(ctx, codexconfig.ProviderID, id))
	})
}

func normalizeCodexConfigSetFields(idRaw, nameRaw, descriptionRaw string) (string, string, string, error) {
	id, appErr := validateID(idRaw, apperror.CodexInvalid)
	if appErr != nil {
		return "", "", "", appErr
	}
	name, appErr := validateName(nameRaw, apperror.CodexInvalid)
	if appErr != nil {
		return "", "", "", appErr
	}
	description, appErr := validateDescription(descriptionRaw, apperror.CodexInvalid)
	if appErr != nil {
		return "", "", "", appErr
	}
	return id, name, description, nil
}

func activeCodexConfigSetID(ctx context.Context, db *store.Store) (string, error) {
	active, exists, err := codexActiveState(ctx, db)
	if err != nil || !exists {
		return "", err
	}
	targets, err := storedCodexBindingTargets(ctx, db, active.ProfileID)
	if err != nil {
		return "", apperror.Wrap(apperror.StoreStatusFailed, "failed to read active Codex profile targets", err)
	}
	configTarget, _, err := requireCodexFullProfileTargets(active.ProfileID, targets)
	if err != nil {
		return "", err
	}
	return codexConfigSetIDFromTarget(configTarget)
}

func codexConfigSetFromStore(ctx context.Context, db *store.Store, configSet store.ProviderConfigSet, activeID string) (CodexConfigSet, error) {
	references, err := codexConfigSetBindingCount(ctx, db, configSet.ID)
	if err != nil {
		return CodexConfigSet{}, err
	}
	model, provider, baseURL := codexConfigSummaryFromContent(configSet.PayloadText)
	return CodexConfigSet{
		ID:              configSet.ID,
		Name:            configSet.Name,
		Description:     configSet.Description,
		ReferenceCount:  references,
		Active:          configSet.ID == activeID,
		Model:           model,
		ModelProvider:   provider,
		OpenAIBaseURL:   baseURL,
		PayloadSHA256:   configSet.PayloadSHA256,
		CreatedAtUnixMS: configSet.CreatedAtUnixMS,
		UpdatedAtUnixMS: configSet.UpdatedAtUnixMS,
	}, nil
}
