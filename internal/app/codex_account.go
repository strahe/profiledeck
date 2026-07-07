package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/strahe/profiledeck/internal/codexconfig"
	"github.com/strahe/profiledeck/internal/store"
	"github.com/strahe/profiledeck/internal/targetfs"
)

type CodexAccount struct {
	ProviderID      string         `json:"provider_id"`
	AccountID       string         `json:"account_id"`
	SecretKind      string         `json:"secret_kind"`
	PayloadSHA256   string         `json:"payload_sha256"`
	DisplayName     string         `json:"display_name"`
	Metadata        map[string]any `json:"metadata"`
	CreatedAtUnixMS int64          `json:"created_at_unix_ms"`
	UpdatedAtUnixMS int64          `json:"updated_at_unix_ms"`
}

type CodexProfileCaptureRequest struct {
	ConfigDir   string
	CodexDir    string
	ProfileID   string
	AccountID   string
	Name        *string
	Description *string
}

type CodexProfileCaptureResult struct {
	Provider     Provider      `json:"provider"`
	Profile      Profile       `json:"profile"`
	Account      CodexAccount  `json:"account"`
	ConfigTarget ProfileTarget `json:"config_target"`
	AuthTarget   ProfileTarget `json:"auth_target"`
	CodexDir     string        `json:"codex_dir"`
	ConfigPath   string        `json:"config_path"`
	AuthPath     string        `json:"auth_path"`
	Warnings     []string      `json:"warnings"`
}

type CodexAccountListRequest struct {
	ConfigDir string
}

type CodexAccountShowRequest struct {
	ConfigDir string
	AccountID string
}

type CodexAccountImportRequest struct {
	ConfigDir string
	AccountID string
	AuthFile  string
	Name      *string
}

type CodexAccountExportRequest struct {
	ConfigDir string
	AccountID string
	Output    string
	Force     bool
}

type CodexAccountExportResult struct {
	ProviderID    string `json:"provider_id"`
	AccountID     string `json:"account_id"`
	Output        string `json:"output"`
	PayloadSHA256 string `json:"payload_sha256"`
}

type codexAuthSnapshot struct {
	Payload        string
	CodexAccountID string
}

func CodexProfileCapture(ctx context.Context, req CodexProfileCaptureRequest) (CodexProfileCaptureResult, error) {
	profileID, appErr := validateID(req.ProfileID, ErrorProfileInvalid)
	if appErr != nil {
		return CodexProfileCaptureResult{}, appErr
	}
	home, err := codexconfig.ResolveHome(req.CodexDir)
	if err != nil {
		return CodexProfileCaptureResult{}, WrapError(ErrorCodexInvalid, "failed to resolve Codex home", err)
	}
	if appErr := requireExistingCodexHome(home); appErr != nil {
		return CodexProfileCaptureResult{}, appErr
	}
	profileFields, appErr := normalizeCodexProfileFields(profileID, req.Name, req.Description)
	if appErr != nil {
		return CodexProfileCaptureResult{}, appErr
	}

	warnings := []string{}
	configContent, configMissing, appErr := readCodexConfigSnapshot(home)
	if appErr != nil {
		return CodexProfileCaptureResult{}, appErr
	}
	if configMissing {
		warnings = append(warnings, "Codex config.toml is missing; captured an empty config snapshot")
	}
	authSnapshot, appErr := readCodexAuthSnapshot(home)
	if appErr != nil {
		return CodexProfileCaptureResult{}, appErr
	}
	configValueJSON, err := codexReplaceFileValueJSON(configContent)
	if err != nil {
		return CodexProfileCaptureResult{}, WrapError(ErrorCodexInvalid, "failed to encode Codex config target value", err)
	}
	providerMetadata, err := codexProviderMetadataJSON(home)
	if err != nil {
		return CodexProfileCaptureResult{}, WrapError(ErrorCodexInvalid, "failed to encode Codex provider metadata", err)
	}
	configTargetMetadata, err := codexTargetMetadataJSON(codexconfig.TargetID, codexTargetModeFullFile)
	if err != nil {
		return CodexProfileCaptureResult{}, WrapError(ErrorCodexInvalid, "failed to encode Codex config target metadata", err)
	}
	authTargetMetadata, err := codexTargetMetadataJSON(codexconfig.AuthTargetID, codexTargetModeFullFile)
	if err != nil {
		return CodexProfileCaptureResult{}, WrapError(ErrorCodexInvalid, "failed to encode Codex auth target metadata", err)
	}
	accountMetadata, err := codexAccountMetadataJSON(home, authSnapshot.CodexAccountID)
	if err != nil {
		return CodexProfileCaptureResult{}, WrapError(ErrorCodexInvalid, "failed to encode Codex account metadata", err)
	}

	db, err := openHealthyStore(ctx, req.ConfigDir, false)
	if err != nil {
		return CodexProfileCaptureResult{}, err
	}
	defer db.Close()

	var provider store.Provider
	var profile store.Profile
	var account store.ProviderAccountSecret
	var configTarget store.ProfileTarget
	var authTarget store.ProfileTarget
	var accountID string
	if err := db.WithTransaction(ctx, func(txStore *store.Store) error {
		var hasProvider bool
		var hasProfile bool
		var targets codexExistingTargets
		var err error

		provider, hasProvider, err = codexPreflightProvider(ctx, txStore, home)
		if err != nil {
			return err
		}
		profile, hasProfile, err = codexPreflightProfile(ctx, txStore, profileID)
		if err != nil {
			return err
		}
		targets, err = codexPreflightTargets(ctx, txStore, home, profileID)
		if err != nil {
			return err
		}
		var appErr *AppError
		accountID, appErr = resolveCodexCaptureAccountID(profileID, req.AccountID, targets)
		if appErr != nil {
			return appErr
		}
		if appErr := requireCodexCaptureProfileAccount(targets, profileID, accountID); appErr != nil {
			return appErr
		}
		authValueJSON, err := codexAuthTargetValueJSON(accountID)
		if err != nil {
			return WrapError(ErrorCodexInvalid, "failed to encode Codex auth target value", err)
		}

		provider, err = upsertCodexProvider(ctx, txStore, providerMetadata, hasProvider)
		if err != nil {
			return err
		}
		profile, err = upsertCodexProfile(ctx, txStore, profileID, profileFields, hasProfile)
		if err != nil {
			return err
		}
		account, err = txStore.UpsertProviderAccountSecret(ctx, store.UpsertProviderAccountSecretParams{
			ProviderID:    codexconfig.ProviderID,
			AccountID:     accountID,
			SecretKind:    codexSecretKindAuthJSON,
			PayloadJSON:   authSnapshot.Payload,
			PayloadSHA256: sha256HexString(authSnapshot.Payload),
			DisplayName:   codexAccountDisplayName(accountID, profileFields, req.Name),
			MetadataJSON:  accountMetadata,
		})
		if err != nil {
			return WrapError(ErrorStoreStatusFailed, "failed to store Codex account secret", err)
		}
		configTarget, err = upsertCodexConfigTarget(ctx, txStore, profileID, home, configValueJSON, configTargetMetadata, targets.HasConfig)
		if err != nil {
			return err
		}
		authTarget, err = upsertCodexAuthTarget(ctx, txStore, profileID, home, authValueJSON, authTargetMetadata, targets.HasAuth)
		if err != nil {
			return err
		}
		return nil
	}); err != nil {
		var appErr *AppError
		if errors.As(err, &appErr) {
			return CodexProfileCaptureResult{}, appErr
		}
		return CodexProfileCaptureResult{}, WrapError(ErrorStoreStatusFailed, "Codex profile capture transaction failed", err)
	}

	return codexProfileCaptureResult(provider, profile, account, configTarget, authTarget, home, warnings)
}

func CodexAccountList(ctx context.Context, req CodexAccountListRequest) ([]CodexAccount, error) {
	db, err := openHealthyStore(ctx, req.ConfigDir, true)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	secrets, err := db.ListProviderAccountSecrets(ctx, codexconfig.ProviderID)
	if err != nil {
		return nil, WrapError(ErrorStoreStatusFailed, "failed to list Codex accounts", err)
	}
	result := make([]CodexAccount, 0, len(secrets))
	for _, secret := range secrets {
		account, err := codexAccountFromStore(secret)
		if err != nil {
			return nil, err
		}
		result = append(result, account)
	}
	return result, nil
}

func CodexAccountShow(ctx context.Context, req CodexAccountShowRequest) (CodexAccount, error) {
	accountID, appErr := normalizeCodexAccountID(req.AccountID)
	if appErr != nil {
		return CodexAccount{}, appErr
	}
	db, err := openHealthyStore(ctx, req.ConfigDir, true)
	if err != nil {
		return CodexAccount{}, err
	}
	defer db.Close()
	secret, err := db.GetProviderAccountSecret(ctx, codexconfig.ProviderID, accountID)
	if err != nil {
		return CodexAccount{}, mapCodexAccountStoreError(err)
	}
	return codexAccountFromStore(secret)
}

func CodexAccountImport(ctx context.Context, req CodexAccountImportRequest) (CodexAccount, error) {
	accountID, appErr := normalizeCodexAccountID(req.AccountID)
	if appErr != nil {
		return CodexAccount{}, appErr
	}
	raw, err := os.ReadFile(req.AuthFile)
	if err != nil {
		return CodexAccount{}, WrapError(ErrorCodexInvalid, "failed to read Codex auth file", err).WithDetail("path", req.AuthFile)
	}
	payload, appErr := normalizeCodexAuthPayload(raw)
	if appErr != nil {
		return CodexAccount{}, appErr.WithDetail("path", req.AuthFile)
	}
	codexAccountID, appErr := codexAuthPayloadAccountID([]byte(payload))
	if appErr != nil {
		return CodexAccount{}, appErr.WithDetail("path", req.AuthFile)
	}
	displayName := accountID
	if req.Name != nil {
		normalized, appErr := validateName(*req.Name, ErrorCodexInvalid)
		if appErr != nil {
			return CodexAccount{}, appErr
		}
		displayName = normalized
	}
	metadataJSON, err := codexAccountMetadataJSON(codexconfig.Home{}, codexAccountID)
	if err != nil {
		return CodexAccount{}, WrapError(ErrorCodexInvalid, "failed to encode Codex account metadata", err)
	}

	db, err := openHealthyStore(ctx, req.ConfigDir, false)
	if err != nil {
		return CodexAccount{}, err
	}
	defer db.Close()
	secret, err := db.UpsertProviderAccountSecret(ctx, store.UpsertProviderAccountSecretParams{
		ProviderID:    codexconfig.ProviderID,
		AccountID:     accountID,
		SecretKind:    codexSecretKindAuthJSON,
		PayloadJSON:   payload,
		PayloadSHA256: sha256HexString(payload),
		DisplayName:   displayName,
		MetadataJSON:  metadataJSON,
	})
	if err != nil {
		return CodexAccount{}, WrapError(ErrorStoreStatusFailed, "failed to import Codex account", err)
	}
	return codexAccountFromStore(secret)
}

func CodexAccountExport(ctx context.Context, req CodexAccountExportRequest) (CodexAccountExportResult, error) {
	accountID, appErr := normalizeCodexAccountID(req.AccountID)
	if appErr != nil {
		return CodexAccountExportResult{}, appErr
	}
	output := strings.TrimSpace(req.Output)
	if output == "" {
		return CodexAccountExportResult{}, NewError(ErrorCodexInvalid, "output path is required")
	}
	output, err := filepath.Abs(output)
	if err != nil {
		return CodexAccountExportResult{}, WrapError(ErrorCodexInvalid, "failed to resolve output path", err)
	}

	db, err := openHealthyStore(ctx, req.ConfigDir, true)
	if err != nil {
		return CodexAccountExportResult{}, err
	}
	defer db.Close()
	secret, err := db.GetProviderAccountSecret(ctx, codexconfig.ProviderID, accountID)
	if err != nil {
		return CodexAccountExportResult{}, mapCodexAccountStoreError(err)
	}
	// CodexAccountExport is the explicit sensitive export path. Regular command
	// output still returns metadata only, but the destination file intentionally
	// receives raw auth JSON for manual editing and migration.
	if err := writeCodexAccountExport(ctx, output, secret.PayloadJSON, req.Force); err != nil {
		return CodexAccountExportResult{}, err
	}
	return CodexAccountExportResult{
		ProviderID:    secret.ProviderID,
		AccountID:     secret.AccountID,
		Output:        output,
		PayloadSHA256: secret.PayloadSHA256,
	}, nil
}

func readCodexConfigSnapshot(home codexconfig.Home) (string, bool, *AppError) {
	raw, err := os.ReadFile(home.ConfigPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", true, nil
		}
		return "", false, WrapError(ErrorCodexInvalid, "failed to read Codex config", err).WithDetail("path", home.ConfigPath)
	}
	if len(raw) > maxTargetContentBytes {
		return "", false, NewError(ErrorCodexInvalid, "Codex config is too large").WithDetail("path", home.ConfigPath)
	}
	content := string(raw)
	if err := codexconfig.ValidateTOML(content); err != nil {
		return "", false, WrapError(ErrorCodexInvalid, "Codex config TOML is invalid", err).WithDetail("path", home.ConfigPath)
	}
	return content, false, nil
}

func readCodexAuthSnapshot(home codexconfig.Home) (codexAuthSnapshot, *AppError) {
	raw, err := os.ReadFile(home.AuthPath)
	if err != nil {
		if os.IsNotExist(err) {
			return codexAuthSnapshot{}, NewError(ErrorCodexInvalid, codexFileCredentialStoreHint).WithDetail("auth_path", home.AuthPath)
		}
		return codexAuthSnapshot{}, WrapError(ErrorCodexInvalid, "failed to read Codex auth", err).WithDetail("path", home.AuthPath)
	}
	payload, object, appErr := decodeCodexAuthPayload(raw)
	if appErr != nil {
		return codexAuthSnapshot{}, appErr.WithDetail("path", home.AuthPath)
	}
	accountID, appErr := codexAuthAccountIDFromObject(object)
	if appErr != nil {
		return codexAuthSnapshot{}, appErr.WithDetail("path", home.AuthPath)
	}
	return codexAuthSnapshot{Payload: payload, CodexAccountID: accountID}, nil
}

func normalizeCodexAuthPayload(raw []byte) (string, *AppError) {
	payload, object, appErr := decodeCodexAuthPayload(raw)
	if appErr != nil {
		return "", appErr
	}
	if _, appErr := codexAuthAccountIDFromObject(object); appErr != nil {
		return "", appErr
	}
	return payload, nil
}

func decodeCodexAuthPayload(raw []byte) (string, map[string]any, *AppError) {
	if len(raw) > maxTargetContentBytes {
		return "", nil, NewError(ErrorCodexInvalid, "Codex auth payload is too large").
			WithDetail("size_bytes", len(raw)).
			WithDetail("max_bytes", maxTargetContentBytes)
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", nil, WrapError(ErrorCodexInvalid, "Codex auth payload must be a JSON object", err)
	}
	object, ok := value.(map[string]any)
	if !ok {
		return "", nil, NewError(ErrorCodexInvalid, "Codex auth payload must be a JSON object")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err != nil {
			return "", nil, WrapError(ErrorCodexInvalid, "Codex auth payload must contain one JSON object", err)
		}
		return "", nil, NewError(ErrorCodexInvalid, "Codex auth payload must contain one JSON object")
	}
	return string(raw), object, nil
}

func codexAuthPayloadAccountID(raw []byte) (string, *AppError) {
	_, object, appErr := decodeCodexAuthPayload(raw)
	if appErr != nil {
		return "", appErr
	}
	return codexAuthAccountIDFromObject(object)
}

func codexAuthAccountIDFromObject(object map[string]any) (string, *AppError) {
	tokens, ok := object["tokens"].(map[string]any)
	if !ok {
		return "", NewError(ErrorCodexInvalid, "Codex auth payload is missing tokens.account_id")
	}
	raw, ok := tokens["account_id"].(string)
	if !ok || strings.TrimSpace(raw) == "" {
		return "", NewError(ErrorCodexInvalid, "Codex auth payload is missing tokens.account_id")
	}
	accountID, appErr := normalizeCodexExternalAccountID(raw)
	if appErr != nil {
		return "", appErr.WithDetail("field", "tokens.account_id")
	}
	return accountID, nil
}

func normalizeCodexExternalAccountID(raw string) (string, *AppError) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", NewError(ErrorCodexInvalid, "Codex auth payload is missing tokens.account_id")
	}
	if len(value) > maxCodexAuthAccountIDLength {
		return "", NewError(ErrorCodexInvalid, "Codex auth account id is too long")
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return "", NewError(ErrorCodexInvalid, "Codex auth account id cannot contain control characters")
		}
	}
	return value, nil
}

func normalizeCodexAccountID(raw string) (string, *AppError) {
	return validateID(raw, ErrorCodexInvalid)
}

func normalizeOptionalCodexAccountID(raw string) (string, *AppError) {
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}
	return normalizeCodexAccountID(raw)
}

func resolveCodexCaptureAccountID(profileID string, requested string, targets codexExistingTargets) (string, *AppError) {
	// ProfileDeck account IDs are local aliases. Codex tokens.account_id is
	// retained as metadata only because it is not unique for every local login.
	if strings.TrimSpace(requested) != "" {
		return normalizeCodexAccountID(requested)
	}
	if targets.HasAuth {
		existingAccountID, err := parseCodexAuthTargetValueJSON(targets.Auth.ValueJSON)
		if err != nil {
			return "", WrapError(ErrorStoreSchemaInvalid, "stored Codex auth target value_json is invalid", err).
				WithDetail("profile_id", profileID).
				WithDetail("target_id", codexconfig.AuthTargetID)
		}
		return existingAccountID, nil
	}
	return normalizeCodexAccountID(profileID)
}

func requireCodexCaptureProfileAccount(targets codexExistingTargets, profileID string, accountID string) *AppError {
	if !targets.HasAuth {
		return nil
	}
	existingAccountID, err := parseCodexAuthTargetValueJSON(targets.Auth.ValueJSON)
	if err != nil {
		return WrapError(ErrorStoreSchemaInvalid, "stored Codex auth target value_json is invalid", err).
			WithDetail("profile_id", profileID).
			WithDetail("target_id", codexconfig.AuthTargetID)
	}
	if existingAccountID != accountID {
		return NewError(ErrorCodexInvalid, "existing Codex profile is bound to a different account").
			WithDetail("profile_id", profileID).
			WithDetail("existing_account_id", existingAccountID).
			WithDetail("requested_account_id", accountID)
	}
	return nil
}

func codexAccountDisplayName(accountID string, fields codexProfileFields, name *string) string {
	if name == nil {
		return accountID
	}
	return fields.CreateName
}

func codexAccountMetadataJSON(home codexconfig.Home, codexAccountID string) (string, error) {
	metadata := map[string]any{
		"preset":           codexconfig.PresetName,
		"preset_version":   codexconfig.PresetVersion,
		"codex_account_id": codexAccountID,
	}
	if home.Dir != "" {
		metadata["codex_dir"] = home.Dir
	}
	if home.AuthPath != "" {
		metadata["auth_path"] = home.AuthPath
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func codexReplaceFileValueJSON(content string) (string, error) {
	raw, err := json.Marshal(map[string]string{"content": content})
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func codexAuthTargetValueJSON(accountID string) (string, error) {
	raw, err := json.Marshal(map[string]string{"account_id": accountID})
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func parseCodexAuthTargetValueJSON(raw string) (string, error) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	var value map[string]string
	if err := decoder.Decode(&value); err != nil {
		return "", err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err != nil {
			return "", err
		}
		return "", errors.New("auth target value_json must contain one JSON object")
	}
	accountID := strings.TrimSpace(value["account_id"])
	if accountID == "" || len(value) != 1 {
		return "", errors.New(`auth target value_json must be {"account_id": string}`)
	}
	return accountID, nil
}

func upsertCodexProvider(ctx context.Context, db *store.Store, metadataJSON string, hasProvider bool) (store.Provider, error) {
	if !hasProvider {
		provider, err := db.CreateProvider(ctx, store.CreateProviderParams{
			ID:           codexconfig.ProviderID,
			Name:         codexProviderName,
			AdapterID:    codexconfig.AdapterID,
			Enabled:      true,
			MetadataJSON: metadataJSON,
		})
		if err != nil {
			return store.Provider{}, mapProviderStoreError(err)
		}
		return provider, nil
	}
	enabled := true
	name := codexProviderName
	provider, err := db.UpdateProvider(ctx, store.UpdateProviderParams{
		ID:           codexconfig.ProviderID,
		Name:         &name,
		Enabled:      &enabled,
		MetadataJSON: &metadataJSON,
	})
	if err != nil {
		return store.Provider{}, mapProviderStoreError(err)
	}
	return provider, nil
}

func upsertCodexProfile(ctx context.Context, db *store.Store, profileID string, fields codexProfileFields, hasProfile bool) (store.Profile, error) {
	if !hasProfile {
		profile, err := db.CreateProfile(ctx, store.CreateProfileParams{
			ID:           profileID,
			Name:         fields.CreateName,
			Description:  fields.CreateDescription,
			MetadataJSON: "{}",
		})
		if err != nil {
			return store.Profile{}, mapProfileStoreError(err)
		}
		return profile, nil
	}
	profile, err := db.GetProfile(ctx, profileID)
	if err != nil {
		return store.Profile{}, mapProfileStoreError(err)
	}
	if fields.UpdateName == nil && fields.UpdateDescription == nil {
		return profile, nil
	}
	profile, err = db.UpdateProfile(ctx, store.UpdateProfileParams{
		ID:          profileID,
		Name:        fields.UpdateName,
		Description: fields.UpdateDescription,
	})
	if err != nil {
		return store.Profile{}, mapProfileStoreError(err)
	}
	return profile, nil
}

func upsertCodexConfigTarget(ctx context.Context, db *store.Store, profileID string, home codexconfig.Home, valueJSON string, metadataJSON string, hasTarget bool) (store.ProfileTarget, error) {
	enabled := true
	if !hasTarget {
		target, err := db.CreateProfileTarget(ctx, store.CreateProfileTargetParams{
			ProfileID:    profileID,
			ProviderID:   codexconfig.ProviderID,
			TargetID:     codexconfig.TargetID,
			Path:         home.ConfigPath,
			PathKey:      targetPathOwnershipKey(home.ConfigPath),
			Format:       targetFormatTOML,
			Strategy:     targetStrategyReplaceFile,
			ValueJSON:    valueJSON,
			Enabled:      true,
			MetadataJSON: metadataJSON,
		})
		if err != nil {
			return store.ProfileTarget{}, mapTargetStoreError(err)
		}
		return target, nil
	}
	path := home.ConfigPath
	pathKey := targetPathOwnershipKey(home.ConfigPath)
	format := targetFormatTOML
	strategy := targetStrategyReplaceFile
	target, err := db.UpdateProfileTarget(ctx, store.UpdateProfileTargetParams{
		ProfileID:    profileID,
		ProviderID:   codexconfig.ProviderID,
		TargetID:     codexconfig.TargetID,
		Path:         &path,
		PathKey:      &pathKey,
		Format:       &format,
		Strategy:     &strategy,
		ValueJSON:    &valueJSON,
		Enabled:      &enabled,
		MetadataJSON: &metadataJSON,
	})
	if err != nil {
		return store.ProfileTarget{}, mapTargetStoreError(err)
	}
	return target, nil
}

func upsertCodexAuthTarget(ctx context.Context, db *store.Store, profileID string, home codexconfig.Home, valueJSON string, metadataJSON string, hasTarget bool) (store.ProfileTarget, error) {
	enabled := true
	if !hasTarget {
		target, err := db.CreateProfileTarget(ctx, store.CreateProfileTargetParams{
			ProfileID:    profileID,
			ProviderID:   codexconfig.ProviderID,
			TargetID:     codexconfig.AuthTargetID,
			Path:         home.AuthPath,
			PathKey:      targetPathOwnershipKey(home.AuthPath),
			Format:       targetFormatJSON,
			Strategy:     targetStrategyReplaceFile,
			ValueJSON:    valueJSON,
			Enabled:      true,
			MetadataJSON: metadataJSON,
		})
		if err != nil {
			return store.ProfileTarget{}, mapTargetStoreError(err)
		}
		return target, nil
	}
	path := home.AuthPath
	pathKey := targetPathOwnershipKey(home.AuthPath)
	format := targetFormatJSON
	strategy := targetStrategyReplaceFile
	target, err := db.UpdateProfileTarget(ctx, store.UpdateProfileTargetParams{
		ProfileID:    profileID,
		ProviderID:   codexconfig.ProviderID,
		TargetID:     codexconfig.AuthTargetID,
		Path:         &path,
		PathKey:      &pathKey,
		Format:       &format,
		Strategy:     &strategy,
		ValueJSON:    &valueJSON,
		Enabled:      &enabled,
		MetadataJSON: &metadataJSON,
	})
	if err != nil {
		return store.ProfileTarget{}, mapTargetStoreError(err)
	}
	return target, nil
}

func codexProfileCaptureResult(provider store.Provider, profile store.Profile, account store.ProviderAccountSecret, configTarget store.ProfileTarget, authTarget store.ProfileTarget, home codexconfig.Home, warnings []string) (CodexProfileCaptureResult, error) {
	publicProvider, err := providerFromStore(provider)
	if err != nil {
		return CodexProfileCaptureResult{}, err
	}
	publicProfile, err := profileFromStore(profile)
	if err != nil {
		return CodexProfileCaptureResult{}, err
	}
	publicAccount, err := codexAccountFromStore(account)
	if err != nil {
		return CodexProfileCaptureResult{}, err
	}
	publicConfigTarget, err := profileTargetFromStore(configTarget)
	if err != nil {
		return CodexProfileCaptureResult{}, err
	}
	publicAuthTarget, err := profileTargetFromStore(authTarget)
	if err != nil {
		return CodexProfileCaptureResult{}, err
	}
	return CodexProfileCaptureResult{
		Provider:     publicProvider,
		Profile:      publicProfile,
		Account:      publicAccount,
		ConfigTarget: publicConfigTarget,
		AuthTarget:   publicAuthTarget,
		CodexDir:     home.Dir,
		ConfigPath:   home.ConfigPath,
		AuthPath:     home.AuthPath,
		Warnings:     warnings,
	}, nil
}

func codexAccountFromStore(secret store.ProviderAccountSecret) (CodexAccount, error) {
	metadata, err := metadataFromJSON(secret.MetadataJSON)
	if err != nil {
		return CodexAccount{}, err
	}
	return CodexAccount{
		ProviderID:      secret.ProviderID,
		AccountID:       secret.AccountID,
		SecretKind:      secret.SecretKind,
		PayloadSHA256:   secret.PayloadSHA256,
		DisplayName:     secret.DisplayName,
		Metadata:        redactMetadata(metadata).(map[string]any),
		CreatedAtUnixMS: secret.CreatedAtUnixMS,
		UpdatedAtUnixMS: secret.UpdatedAtUnixMS,
	}, nil
}

func mapCodexAccountStoreError(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return NewError(ErrorCodexInvalid, "Codex account not found")
	}
	return WrapError(ErrorStoreStatusFailed, "Codex account store operation failed", err)
}

func writeCodexAccountExport(ctx context.Context, path string, payload string, force bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return WrapError(ErrorCodexInvalid, "failed to create output directory", err).WithDetail("path", filepath.Dir(path))
	}

	state, err := targetfs.Inspect(ctx, path)
	if err != nil {
		return mapTargetFSError(err)
	}
	if state.Exists {
		if state.IsSymlink {
			return NewError(ErrorCodexInvalid, "output path must not be a symlink").WithDetail("path", path)
		}
		if state.IsDir {
			return NewError(ErrorCodexInvalid, "output path is a directory").WithDetail("path", path)
		}
		if !state.IsRegular {
			return NewError(ErrorCodexInvalid, "output path is not a regular file").WithDetail("path", path)
		}
		if !force {
			return NewError(ErrorCodexInvalid, "output file already exists").WithDetail("path", path)
		}
	}

	err = targetfs.AtomicWriteContent(ctx, targetfs.AtomicWriteContentRequest{
		Expected: targetfs.ExpectedTarget{
			TargetID: codexconfig.AuthTargetID,
			Path:     path,
			Exists:   state.Exists,
			SHA256:   state.SHA256,
		},
		Content: payload,
		Mode:    0o600,
		UseMode: true,
	})
	if err != nil {
		return mapTargetFSError(err)
	}
	return nil
}
