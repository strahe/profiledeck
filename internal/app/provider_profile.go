package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/strahe/profiledeck/internal/store"
)

const (
	maxIDLength          = 80
	maxNameLength        = 120
	maxDescriptionLength = 1000
	maxMetadataJSONBytes = 64 * 1024
	redactedValue        = "[REDACTED]"
)

type Provider struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	AdapterID       string         `json:"adapter_id"`
	Enabled         bool           `json:"enabled"`
	Metadata        map[string]any `json:"metadata"`
	CreatedAtUnixMS int64          `json:"created_at_unix_ms"`
	UpdatedAtUnixMS int64          `json:"updated_at_unix_ms"`
}

type Profile struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	Description     string         `json:"description"`
	Metadata        map[string]any `json:"metadata"`
	CreatedAtUnixMS int64          `json:"created_at_unix_ms"`
	UpdatedAtUnixMS int64          `json:"updated_at_unix_ms"`
}

type DeleteResult struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

type ListProvidersRequest struct {
	ConfigDir       string
	IncludeDisabled bool
}

type GetProviderRequest struct {
	ConfigDir string
	ID        string
}

type CreateProviderRequest struct {
	ConfigDir    string
	ID           string
	Name         string
	AdapterID    string
	Enabled      *bool
	MetadataJSON *string
}

type UpdateProviderRequest struct {
	ConfigDir    string
	ID           string
	Name         *string
	AdapterID    *string
	Enabled      *bool
	MetadataJSON *string
}

type DeleteProviderRequest struct {
	ConfigDir string
	ID        string
	Confirm   bool
}

type ListProfilesRequest struct {
	ConfigDir string
}

type GetProfileRequest struct {
	ConfigDir string
	ID        string
}

type CreateProfileRequest struct {
	ConfigDir    string
	ID           string
	Name         string
	Description  string
	MetadataJSON *string
}

type UpdateProfileRequest struct {
	ConfigDir    string
	ID           string
	Name         *string
	Description  *string
	MetadataJSON *string
}

type DeleteProfileRequest struct {
	ConfigDir string
	ID        string
	Confirm   bool
}

func ListProviders(ctx context.Context, req ListProvidersRequest) ([]Provider, error) {
	db, err := openHealthyStore(ctx, req.ConfigDir, true)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	providers, err := db.ListProviders(ctx, req.IncludeDisabled)
	if err != nil {
		return nil, WrapError(ErrorStoreStatusFailed, "failed to list providers", err)
	}
	result := make([]Provider, 0, len(providers))
	for _, provider := range providers {
		value, err := providerFromStore(provider)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, nil
}

func GetProvider(ctx context.Context, req GetProviderRequest) (Provider, error) {
	id, appErr := validateID(req.ID, ErrorProviderInvalid)
	if appErr != nil {
		return Provider{}, appErr
	}

	db, err := openHealthyStore(ctx, req.ConfigDir, true)
	if err != nil {
		return Provider{}, err
	}
	defer db.Close()

	provider, err := db.GetProvider(ctx, id)
	if err != nil {
		return Provider{}, mapProviderStoreError(err)
	}
	return providerFromStore(provider)
}

func CreateProvider(ctx context.Context, req CreateProviderRequest) (Provider, error) {
	id, name, adapterID, metadataJSON, enabled, appErr := normalizeCreateProvider(req)
	if appErr != nil {
		return Provider{}, appErr
	}

	db, err := openHealthyStore(ctx, req.ConfigDir, false)
	if err != nil {
		return Provider{}, err
	}
	defer db.Close()

	provider, err := db.CreateProvider(ctx, store.CreateProviderParams{
		ID:           id,
		Name:         name,
		AdapterID:    adapterID,
		Enabled:      enabled,
		MetadataJSON: metadataJSON,
	})
	if err != nil {
		return Provider{}, mapProviderStoreError(err)
	}
	return providerFromStore(provider)
}

func UpdateProvider(ctx context.Context, req UpdateProviderRequest) (Provider, error) {
	id, params, appErr := normalizeUpdateProvider(req)
	if appErr != nil {
		return Provider{}, appErr
	}

	db, err := openHealthyStore(ctx, req.ConfigDir, false)
	if err != nil {
		return Provider{}, err
	}
	defer db.Close()

	params.ID = id
	provider, err := db.UpdateProvider(ctx, params)
	if err != nil {
		return Provider{}, mapProviderStoreError(err)
	}
	return providerFromStore(provider)
}

func DeleteProvider(ctx context.Context, req DeleteProviderRequest) (DeleteResult, error) {
	id, appErr := validateID(req.ID, ErrorProviderInvalid)
	if appErr != nil {
		return DeleteResult{}, appErr
	}
	if !req.Confirm {
		return DeleteResult{}, NewError(ErrorConfirmationRequired, "provider delete requires confirmation")
	}

	db, err := openHealthyStore(ctx, req.ConfigDir, false)
	if err != nil {
		return DeleteResult{}, err
	}
	defer db.Close()

	if err := db.DeleteProvider(ctx, id); err != nil {
		return DeleteResult{}, mapProviderStoreError(err)
	}
	return DeleteResult{ID: id, Deleted: true}, nil
}

func ListProfiles(ctx context.Context, req ListProfilesRequest) ([]Profile, error) {
	db, err := openHealthyStore(ctx, req.ConfigDir, true)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	profiles, err := db.ListProfiles(ctx)
	if err != nil {
		return nil, WrapError(ErrorStoreStatusFailed, "failed to list profiles", err)
	}
	result := make([]Profile, 0, len(profiles))
	for _, profile := range profiles {
		value, err := profileFromStore(profile)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, nil
}

func GetProfile(ctx context.Context, req GetProfileRequest) (Profile, error) {
	id, appErr := validateID(req.ID, ErrorProfileInvalid)
	if appErr != nil {
		return Profile{}, appErr
	}

	db, err := openHealthyStore(ctx, req.ConfigDir, true)
	if err != nil {
		return Profile{}, err
	}
	defer db.Close()

	profile, err := db.GetProfile(ctx, id)
	if err != nil {
		return Profile{}, mapProfileStoreError(err)
	}
	return profileFromStore(profile)
}

func CreateProfile(ctx context.Context, req CreateProfileRequest) (Profile, error) {
	id, name, description, metadataJSON, appErr := normalizeCreateProfile(req)
	if appErr != nil {
		return Profile{}, appErr
	}

	db, err := openHealthyStore(ctx, req.ConfigDir, false)
	if err != nil {
		return Profile{}, err
	}
	defer db.Close()

	profile, err := db.CreateProfile(ctx, store.CreateProfileParams{
		ID:           id,
		Name:         name,
		Description:  description,
		MetadataJSON: metadataJSON,
	})
	if err != nil {
		return Profile{}, mapProfileStoreError(err)
	}
	return profileFromStore(profile)
}

func UpdateProfile(ctx context.Context, req UpdateProfileRequest) (Profile, error) {
	id, params, appErr := normalizeUpdateProfile(req)
	if appErr != nil {
		return Profile{}, appErr
	}

	db, err := openHealthyStore(ctx, req.ConfigDir, false)
	if err != nil {
		return Profile{}, err
	}
	defer db.Close()

	params.ID = id
	profile, err := db.UpdateProfile(ctx, params)
	if err != nil {
		return Profile{}, mapProfileStoreError(err)
	}
	return profileFromStore(profile)
}

func DeleteProfile(ctx context.Context, req DeleteProfileRequest) (DeleteResult, error) {
	id, appErr := validateID(req.ID, ErrorProfileInvalid)
	if appErr != nil {
		return DeleteResult{}, appErr
	}
	if !req.Confirm {
		return DeleteResult{}, NewError(ErrorConfirmationRequired, "profile delete requires confirmation")
	}

	db, err := openHealthyStore(ctx, req.ConfigDir, false)
	if err != nil {
		return DeleteResult{}, err
	}
	defer db.Close()

	if err := db.DeleteProfile(ctx, id); err != nil {
		return DeleteResult{}, mapProfileStoreError(err)
	}
	return DeleteResult{ID: id, Deleted: true}, nil
}

func openHealthyStore(ctx context.Context, configDir string, readOnly bool) (*store.Store, error) {
	_, paths, err := resolveRuntime(configDir)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(paths.Database); err != nil {
		if os.IsNotExist(err) {
			return nil, NewError(ErrorStoreNotInitialized, "application database is not initialized")
		}
		return nil, WrapError(ErrorStoreStatusFailed, "failed to inspect application database", err)
	}

	db, err := store.Open(ctx, paths.Database, readOnly)
	if err != nil {
		return nil, WrapError(ErrorStoreOpenFailed, "failed to open application database", err)
	}

	status, err := db.Status(ctx)
	if err != nil {
		_ = db.Close()
		return nil, WrapError(ErrorStoreStatusFailed, "failed to inspect application database", err)
	}
	if !status.SchemaHealthy {
		_ = db.Close()
		return nil, NewError(ErrorStoreSchemaInvalid, "application database schema is not healthy")
	}
	return db, nil
}

func normalizeCreateProvider(req CreateProviderRequest) (string, string, string, string, bool, *AppError) {
	id, appErr := validateID(req.ID, ErrorProviderInvalid)
	if appErr != nil {
		return "", "", "", "", false, appErr
	}
	name, appErr := validateName(req.Name, ErrorProviderInvalid)
	if appErr != nil {
		return "", "", "", "", false, appErr
	}
	adapterID, appErr := validateID(req.AdapterID, ErrorProviderInvalid)
	if appErr != nil {
		return "", "", "", "", false, appErr.WithDetail("field", "adapter_id")
	}
	metadataJSON, appErr := normalizeMetadataJSON(req.MetadataJSON, ErrorProviderInvalid)
	if appErr != nil {
		return "", "", "", "", false, appErr
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	return id, name, adapterID, metadataJSON, enabled, nil
}

func normalizeUpdateProvider(req UpdateProviderRequest) (string, store.UpdateProviderParams, *AppError) {
	id, appErr := validateID(req.ID, ErrorProviderInvalid)
	if appErr != nil {
		return "", store.UpdateProviderParams{}, appErr
	}
	if req.Name == nil && req.AdapterID == nil && req.Enabled == nil && req.MetadataJSON == nil {
		return "", store.UpdateProviderParams{}, NewError(ErrorProviderInvalid, "provider update requires at least one changed field")
	}

	params := store.UpdateProviderParams{}
	if req.Name != nil {
		name, appErr := validateName(*req.Name, ErrorProviderInvalid)
		if appErr != nil {
			return "", store.UpdateProviderParams{}, appErr
		}
		params.Name = &name
	}
	if req.AdapterID != nil {
		adapterID, appErr := validateID(*req.AdapterID, ErrorProviderInvalid)
		if appErr != nil {
			return "", store.UpdateProviderParams{}, appErr.WithDetail("field", "adapter_id")
		}
		params.AdapterID = &adapterID
	}
	if req.Enabled != nil {
		enabled := *req.Enabled
		params.Enabled = &enabled
	}
	if req.MetadataJSON != nil {
		metadataJSON, appErr := normalizeMetadataJSON(req.MetadataJSON, ErrorProviderInvalid)
		if appErr != nil {
			return "", store.UpdateProviderParams{}, appErr
		}
		params.MetadataJSON = &metadataJSON
	}
	return id, params, nil
}

func normalizeCreateProfile(req CreateProfileRequest) (string, string, string, string, *AppError) {
	id, appErr := validateID(req.ID, ErrorProfileInvalid)
	if appErr != nil {
		return "", "", "", "", appErr
	}
	name, appErr := validateName(req.Name, ErrorProfileInvalid)
	if appErr != nil {
		return "", "", "", "", appErr
	}
	description, appErr := validateDescription(req.Description, ErrorProfileInvalid)
	if appErr != nil {
		return "", "", "", "", appErr
	}
	metadataJSON, appErr := normalizeMetadataJSON(req.MetadataJSON, ErrorProfileInvalid)
	if appErr != nil {
		return "", "", "", "", appErr
	}
	return id, name, description, metadataJSON, nil
}

func normalizeUpdateProfile(req UpdateProfileRequest) (string, store.UpdateProfileParams, *AppError) {
	id, appErr := validateID(req.ID, ErrorProfileInvalid)
	if appErr != nil {
		return "", store.UpdateProfileParams{}, appErr
	}
	if req.Name == nil && req.Description == nil && req.MetadataJSON == nil {
		return "", store.UpdateProfileParams{}, NewError(ErrorProfileInvalid, "profile update requires at least one changed field")
	}

	params := store.UpdateProfileParams{}
	if req.Name != nil {
		name, appErr := validateName(*req.Name, ErrorProfileInvalid)
		if appErr != nil {
			return "", store.UpdateProfileParams{}, appErr
		}
		params.Name = &name
	}
	if req.Description != nil {
		description, appErr := validateDescription(*req.Description, ErrorProfileInvalid)
		if appErr != nil {
			return "", store.UpdateProfileParams{}, appErr
		}
		params.Description = &description
	}
	if req.MetadataJSON != nil {
		metadataJSON, appErr := normalizeMetadataJSON(req.MetadataJSON, ErrorProfileInvalid)
		if appErr != nil {
			return "", store.UpdateProfileParams{}, appErr
		}
		params.MetadataJSON = &metadataJSON
	}
	return id, params, nil
}

func validateID(value string, code ErrorCode) (string, *AppError) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", NewError(code, "id is required")
	}
	if len(value) > maxIDLength {
		return "", NewError(code, "id is too long")
	}
	for i, r := range value {
		valid := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		if i > 0 {
			valid = valid || r == '.' || r == '_' || r == '-'
		}
		if !valid {
			return "", NewError(code, "id must use lowercase ASCII letters, digits, dots, underscores, or dashes")
		}
	}
	return value, nil
}

func validateName(value string, code ErrorCode) (string, *AppError) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", NewError(code, "name is required")
	}
	if len(value) > maxNameLength {
		return "", NewError(code, "name is too long")
	}
	return value, nil
}

func validateDescription(value string, code ErrorCode) (string, *AppError) {
	value = strings.TrimSpace(value)
	if len(value) > maxDescriptionLength {
		return "", NewError(code, "description is too long")
	}
	return value, nil
}

func normalizeMetadataJSON(raw *string, code ErrorCode) (string, *AppError) {
	if raw == nil {
		return "{}", nil
	}

	decoder := json.NewDecoder(strings.NewReader(*raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", NewError(code, "metadata_json must be valid JSON object")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return "", NewError(code, "metadata_json must contain one JSON object")
	}

	metadata, ok := value.(map[string]any)
	if !ok {
		return "", NewError(code, "metadata_json must be a JSON object")
	}
	if err := rejectCredentialMetadataKeys(metadata); err != nil {
		return "", NewError(code, err.Error())
	}

	normalized, err := json.Marshal(metadata)
	if err != nil {
		return "", WrapError(code, "failed to normalize metadata_json", err)
	}
	if len(normalized) > maxMetadataJSONBytes {
		return "", NewError(code, "metadata_json is too large")
	}
	return string(normalized), nil
}

func metadataFromJSON(raw string) (map[string]any, error) {
	var metadata map[string]any
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&metadata); err != nil {
		return nil, WrapError(ErrorStoreSchemaInvalid, "stored metadata_json is invalid", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, NewError(ErrorStoreSchemaInvalid, "stored metadata_json contains extra JSON values")
	}
	if metadata == nil {
		return map[string]any{}, nil
	}
	return metadata, nil
}

func rejectCredentialMetadataKeys(value any) error {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if isCredentialMetadataKey(key) {
				return errors.New("metadata_json cannot contain credential keys")
			}
			if err := rejectCredentialMetadataKeys(child); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range typed {
			if err := rejectCredentialMetadataKeys(child); err != nil {
				return err
			}
		}
	}
	return nil
}

func isCredentialMetadataKey(key string) bool {
	words, compact := normalizeMetadataKey(key)
	switch {
	case compact == "token":
		return true
	case strings.Contains(compact, "apikey"):
		return true
	case strings.Contains(compact, "accesstoken"):
		return true
	case strings.Contains(compact, "refreshtoken"):
		return true
	case strings.Contains(compact, "authtoken"):
		return true
	case containsAnyWord(words, "secret", "password", "authorization", "bearer"):
		return true
	case hasWordSequence(words, "api", "key"):
		return true
	default:
		return len(words) > 0 && words[len(words)-1] == "token"
	}
}

func isSensitiveOutputKey(key string) bool {
	_, compact := normalizeMetadataKey(key)
	for _, marker := range []string{
		"apikey",
		"token",
		"accesstoken",
		"refreshtoken",
		"secret",
		"password",
		"authorization",
		"bearer",
	} {
		if strings.Contains(compact, marker) {
			return true
		}
	}
	return false
}

func normalizeMetadataKey(key string) ([]string, string) {
	var words []string
	var current []rune
	var previousLowerOrDigit bool

	flush := func() {
		if len(current) == 0 {
			return
		}
		words = append(words, strings.ToLower(string(current)))
		current = current[:0]
	}

	for _, r := range strings.TrimSpace(key) {
		switch {
		case r >= 'A' && r <= 'Z':
			if previousLowerOrDigit {
				flush()
			}
			current = append(current, r+'a'-'A')
			previousLowerOrDigit = false
		case r >= 'a' && r <= 'z':
			current = append(current, r)
			previousLowerOrDigit = true
		case r >= '0' && r <= '9':
			current = append(current, r)
			previousLowerOrDigit = true
		default:
			flush()
			previousLowerOrDigit = false
		}
	}
	flush()
	return words, strings.Join(words, "")
}

func containsAnyWord(words []string, targets ...string) bool {
	for _, word := range words {
		for _, target := range targets {
			if word == target {
				return true
			}
		}
	}
	return false
}

func hasWordSequence(words []string, sequence ...string) bool {
	if len(sequence) == 0 || len(words) < len(sequence) {
		return false
	}
	for i := 0; i <= len(words)-len(sequence); i++ {
		matched := true
		for j, want := range sequence {
			if words[i+j] != want {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func redactMetadata(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		redacted := make(map[string]any, len(typed))
		for key, child := range typed {
			if isSensitiveOutputKey(key) {
				redacted[key] = redactedValue
				continue
			}
			redacted[key] = redactMetadata(child)
		}
		return redacted
	case []any:
		redacted := make([]any, len(typed))
		for i, child := range typed {
			redacted[i] = redactMetadata(child)
		}
		return redacted
	default:
		return typed
	}
}

func providerFromStore(provider store.Provider) (Provider, error) {
	metadata, err := metadataFromJSON(provider.MetadataJSON)
	if err != nil {
		return Provider{}, err
	}
	return Provider{
		ID:              provider.ID,
		Name:            provider.Name,
		AdapterID:       provider.AdapterID,
		Enabled:         provider.Enabled,
		Metadata:        redactMetadata(metadata).(map[string]any),
		CreatedAtUnixMS: provider.CreatedAtUnixMS,
		UpdatedAtUnixMS: provider.UpdatedAtUnixMS,
	}, nil
}

func profileFromStore(profile store.Profile) (Profile, error) {
	metadata, err := metadataFromJSON(profile.MetadataJSON)
	if err != nil {
		return Profile{}, err
	}
	return Profile{
		ID:              profile.ID,
		Name:            profile.Name,
		Description:     profile.Description,
		Metadata:        redactMetadata(metadata).(map[string]any),
		CreatedAtUnixMS: profile.CreatedAtUnixMS,
		UpdatedAtUnixMS: profile.UpdatedAtUnixMS,
	}, nil
}

func mapProviderStoreError(err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return NewError(ErrorProviderNotFound, "provider not found")
	case errors.Is(err, store.ErrAlreadyExists):
		return NewError(ErrorProviderAlreadyExists, "provider already exists")
	case errors.Is(err, store.ErrInUse):
		return NewError(ErrorProviderInUse, "provider is in use")
	default:
		return WrapError(ErrorStoreStatusFailed, "provider store operation failed", err)
	}
}

func mapProfileStoreError(err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return NewError(ErrorProfileNotFound, "profile not found")
	case errors.Is(err, store.ErrAlreadyExists):
		return NewError(ErrorProfileAlreadyExists, "profile already exists")
	case errors.Is(err, store.ErrInUse):
		return NewError(ErrorProfileInUse, "profile is in use")
	default:
		return WrapError(ErrorStoreStatusFailed, "profile store operation failed", err)
	}
}
