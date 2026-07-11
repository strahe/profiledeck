package app

import (
	"context"
	"errors"
	"sort"
	"strings"

	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	codexpreset "github.com/strahe/profiledeck/internal/codex/preset"
	"github.com/strahe/profiledeck/internal/store"
)

type ListCodexConfigSetsRequest struct {
	ConfigDir string
}

type GetCodexConfigSetRequest struct {
	ConfigDir   string
	ConfigSetID string
}

type CreateCodexConfigSetRequest struct {
	ConfigDir     string  `json:"config_dir"`
	CodexDir      string  `json:"codex_dir"`
	ConfigSetID   string  `json:"config_set_id"`
	Name          string  `json:"name"`
	Description   string  `json:"description,omitempty"`
	ConfigContent *string `json:"config_content,omitempty"`
}

type CopyCodexConfigSetRequest struct {
	ConfigDir         string `json:"config_dir"`
	SourceConfigSetID string `json:"source_config_set_id"`
	ConfigSetID       string `json:"config_set_id"`
	Name              string `json:"name"`
	Description       string `json:"description,omitempty"`
}

type UpdateCodexConfigSetRequest struct {
	ConfigDir   string  `json:"config_dir"`
	ConfigSetID string  `json:"config_set_id"`
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
}

type DeleteCodexConfigSetRequest struct {
	ConfigDir   string `json:"config_dir"`
	ConfigSetID string `json:"config_set_id"`
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

func ListCodexConfigSets(ctx context.Context, req ListCodexConfigSetsRequest) (CodexConfigSetListResult, error) {
	db, err := openHealthyStore(ctx, req.ConfigDir, true)
	if err != nil {
		return CodexConfigSetListResult{}, err
	}
	defer db.Close()

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
	var appErr *AppError
	if !errors.As(err, &appErr) {
		return false
	}
	return appErr.Code == ErrorCodexInvalid || appErr.Code == ErrorStoreSchemaInvalid
}

func GetCodexConfigSet(ctx context.Context, req GetCodexConfigSetRequest) (CodexConfigSet, error) {
	id, appErr := validateID(req.ConfigSetID, ErrorCodexInvalid)
	if appErr != nil {
		return CodexConfigSet{}, appErr
	}
	db, err := openHealthyStore(ctx, req.ConfigDir, true)
	if err != nil {
		return CodexConfigSet{}, err
	}
	defer db.Close()
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

func CreateCodexConfigSet(ctx context.Context, req CreateCodexConfigSetRequest) (CodexConfigSet, error) {
	id, name, description, err := normalizeCodexConfigSetFields(req.ConfigSetID, req.Name, req.Description)
	if err != nil {
		return CodexConfigSet{}, err
	}
	db, lock, _, err := openLockedCodexStore(ctx, req.ConfigDir, "config-set-create")
	if err != nil {
		return CodexConfigSet{}, err
	}
	defer db.Close()
	defer lock.Release()

	home, err := resolveCodexMutationHome(req.CodexDir)
	if err != nil {
		return CodexConfigSet{}, err
	}
	content, err := loadCodexConfigContent(home, req.ConfigContent)
	if err != nil {
		return CodexConfigSet{}, err
	}
	var stored store.ProviderConfigSet
	err = db.WithTransaction(ctx, func(txStore *store.Store) error {
		_, hasProvider, err := codexPreflightProvider(ctx, txStore, home)
		if err != nil {
			return err
		}
		if _, getErr := txStore.GetProviderConfigSet(ctx, id); getErr == nil {
			return NewError(ErrorProfileAlreadyExists, "Codex config set already exists").WithDetail("config_set_id", id)
		} else if !errors.Is(getErr, store.ErrNotFound) {
			return mapCodexConfigSetStoreError(getErr)
		}
		metadataJSON, err := codexpreset.ProviderMetadataJSON(home)
		if err != nil {
			return WrapError(ErrorCodexInvalid, "failed to encode Codex provider metadata", err)
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
	activeID, err := activeCodexConfigSetID(ctx, db)
	if err != nil {
		return CodexConfigSet{}, err
	}
	return codexConfigSetFromStore(ctx, db, stored, activeID)
}

func CopyCodexConfigSet(ctx context.Context, req CopyCodexConfigSetRequest) (CodexConfigSet, error) {
	sourceID, appErr := validateID(req.SourceConfigSetID, ErrorCodexInvalid)
	if appErr != nil {
		return CodexConfigSet{}, appErr
	}
	id, name, description, err := normalizeCodexConfigSetFields(req.ConfigSetID, req.Name, req.Description)
	if err != nil {
		return CodexConfigSet{}, err
	}
	if sourceID == id {
		return CodexConfigSet{}, NewError(ErrorCodexInvalid, "source and destination config set ids must differ")
	}
	db, lock, _, err := openLockedCodexStore(ctx, req.ConfigDir, "config-set-copy")
	if err != nil {
		return CodexConfigSet{}, err
	}
	defer db.Close()
	defer lock.Release()

	var stored store.ProviderConfigSet
	err = db.WithTransaction(ctx, func(txStore *store.Store) error {
		source, err := requireCodexConfigSet(ctx, txStore, sourceID)
		if err != nil {
			return err
		}
		if _, getErr := txStore.GetProviderConfigSet(ctx, id); getErr == nil {
			return NewError(ErrorProfileAlreadyExists, "Codex config set already exists").WithDetail("config_set_id", id)
		} else if !errors.Is(getErr, store.ErrNotFound) {
			return mapCodexConfigSetStoreError(getErr)
		}
		stored, err = upsertCodexConfigSet(ctx, txStore, id, name, description, source.PayloadText)
		return err
	})
	if err != nil {
		return CodexConfigSet{}, wrapCodexMutationTxError("Codex config set copy transaction failed", err)
	}
	activeID, err := activeCodexConfigSetID(ctx, db)
	if err != nil {
		return CodexConfigSet{}, err
	}
	return codexConfigSetFromStore(ctx, db, stored, activeID)
}

func UpdateCodexConfigSet(ctx context.Context, req UpdateCodexConfigSetRequest) (CodexConfigSet, error) {
	id, appErr := validateID(req.ConfigSetID, ErrorCodexInvalid)
	if appErr != nil {
		return CodexConfigSet{}, appErr
	}
	var name *string
	if req.Name != nil {
		value, appErr := validateName(*req.Name, ErrorCodexInvalid)
		if appErr != nil {
			return CodexConfigSet{}, appErr
		}
		name = &value
	}
	var description *string
	if req.Description != nil {
		value, appErr := validateDescription(*req.Description, ErrorCodexInvalid)
		if appErr != nil {
			return CodexConfigSet{}, appErr
		}
		description = &value
	}
	db, lock, _, err := openLockedCodexStore(ctx, req.ConfigDir, "config-set-update")
	if err != nil {
		return CodexConfigSet{}, err
	}
	defer db.Close()
	defer lock.Release()
	stored, err := db.UpdateProviderConfigSet(ctx, store.UpdateProviderConfigSetParams{ID: id, Name: name, Description: description})
	if err != nil {
		return CodexConfigSet{}, mapCodexConfigSetStoreError(err)
	}
	activeID, err := activeCodexConfigSetID(ctx, db)
	if err != nil {
		return CodexConfigSet{}, err
	}
	return codexConfigSetFromStore(ctx, db, stored, activeID)
}

func DeleteCodexConfigSet(ctx context.Context, req DeleteCodexConfigSetRequest) error {
	id, appErr := validateID(req.ConfigSetID, ErrorCodexInvalid)
	if appErr != nil {
		return appErr
	}
	db, lock, _, err := openLockedCodexStore(ctx, req.ConfigDir, "config-set-delete")
	if err != nil {
		return err
	}
	defer db.Close()
	defer lock.Release()
	if err := db.DeleteProviderConfigSet(ctx, id); err != nil {
		return mapCodexConfigSetStoreError(err)
	}
	return nil
}

func normalizeCodexConfigSetFields(idRaw, nameRaw, descriptionRaw string) (string, string, string, error) {
	id, appErr := validateID(idRaw, ErrorCodexInvalid)
	if appErr != nil {
		return "", "", "", appErr
	}
	name, appErr := validateName(nameRaw, ErrorCodexInvalid)
	if appErr != nil {
		return "", "", "", appErr
	}
	description, appErr := validateDescription(descriptionRaw, ErrorCodexInvalid)
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
	targets, err := db.ListProfileTargets(ctx, active.ProfileID, codexconfig.ProviderID, true)
	if err != nil {
		return "", WrapError(ErrorStoreStatusFailed, "failed to read active Codex profile targets", err)
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
