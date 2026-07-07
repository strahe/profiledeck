package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	codexauth "github.com/strahe/profiledeck/internal/codex/auth"
	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	codexpreset "github.com/strahe/profiledeck/internal/codex/preset"
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
	configValueJSON, err := codexpreset.ReplaceFileValueJSON(configContent)
	if err != nil {
		return CodexProfileCaptureResult{}, WrapError(ErrorCodexInvalid, "failed to encode Codex config target value", err)
	}
	providerMetadata, err := codexpreset.ProviderMetadataJSON(home)
	if err != nil {
		return CodexProfileCaptureResult{}, WrapError(ErrorCodexInvalid, "failed to encode Codex provider metadata", err)
	}
	configTargetMetadata, err := codexpreset.TargetMetadataJSON(codexconfig.TargetID, codexpreset.TargetModeFullFile)
	if err != nil {
		return CodexProfileCaptureResult{}, WrapError(ErrorCodexInvalid, "failed to encode Codex config target metadata", err)
	}
	authTargetMetadata, err := codexpreset.TargetMetadataJSON(codexconfig.AuthTargetID, codexpreset.TargetModeFullFile)
	if err != nil {
		return CodexProfileCaptureResult{}, WrapError(ErrorCodexInvalid, "failed to encode Codex auth target metadata", err)
	}
	accountMetadata, err := codexpreset.AccountMetadataJSON(home, authSnapshot.CodexAccountID)
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
		authValueJSON, err := codexpreset.AuthTargetValueJSON(accountID)
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
			SecretKind:    codexpreset.SecretKindAuthJSON,
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
	payload, err := codexauth.NormalizePayload(raw)
	if err != nil {
		return CodexAccount{}, codexAuthPayloadAppError(err).WithDetail("path", req.AuthFile)
	}
	codexAccountID, err := codexauth.ExtractAccountID([]byte(payload))
	if err != nil {
		return CodexAccount{}, codexAuthPayloadAppError(err).WithDetail("path", req.AuthFile)
	}
	displayName := accountID
	if req.Name != nil {
		normalized, appErr := validateName(*req.Name, ErrorCodexInvalid)
		if appErr != nil {
			return CodexAccount{}, appErr
		}
		displayName = normalized
	}
	metadataJSON, err := codexpreset.AccountMetadataJSON(codexconfig.Home{}, codexAccountID)
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
		SecretKind:    codexpreset.SecretKindAuthJSON,
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
	snapshot, err := codexconfig.ReadSnapshot(home.ConfigPath)
	if err != nil {
		return "", false, codexConfigSnapshotAppError(home.ConfigPath, err)
	}
	return snapshot.Content, snapshot.Missing, nil
}

func readCodexAuthSnapshot(home codexconfig.Home) (codexauth.Snapshot, *AppError) {
	snapshot, err := codexauth.ReadSnapshot(home.AuthPath)
	if err != nil {
		if os.IsNotExist(err) {
			return codexauth.Snapshot{}, NewError(ErrorCodexInvalid, codexpreset.FileCredentialStoreHint).WithDetail("auth_path", home.AuthPath)
		}
		var pathErr *os.PathError
		if errors.As(err, &pathErr) {
			return codexauth.Snapshot{}, WrapError(ErrorCodexInvalid, "failed to read Codex auth", err).WithDetail("path", home.AuthPath)
		}
		return codexauth.Snapshot{}, codexAuthPayloadAppError(err).WithDetail("path", home.AuthPath)
	}
	return snapshot, nil
}

func codexConfigSnapshotAppError(path string, err error) *AppError {
	message := err.Error()
	switch {
	case strings.HasPrefix(message, "read Codex config:"):
		return WrapError(ErrorCodexInvalid, "failed to read Codex config", err).WithDetail("path", path)
	case strings.HasPrefix(message, "Codex config TOML is invalid:"):
		return WrapError(ErrorCodexInvalid, "Codex config TOML is invalid", err).WithDetail("path", path)
	case message == "Codex config is too large":
		return NewError(ErrorCodexInvalid, "Codex config is too large").WithDetail("path", path)
	default:
		return WrapError(ErrorCodexInvalid, message, err).WithDetail("path", path)
	}
}

func codexAuthPayloadAppError(err error) *AppError {
	appErr := WrapError(ErrorCodexInvalid, err.Error(), err)
	var fieldErr codexauth.FieldError
	if errors.As(err, &fieldErr) {
		appErr = appErr.WithDetail("field", fieldErr.Field)
	}
	var sizeErr codexauth.SizeError
	if errors.As(err, &sizeErr) {
		appErr = appErr.WithDetail("size_bytes", sizeErr.Size).WithDetail("max_bytes", sizeErr.Max)
	}
	return appErr
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
		existingAccountID, err := codexpreset.ParseAuthTargetValueJSON(targets.Auth.ValueJSON)
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
	existingAccountID, err := codexpreset.ParseAuthTargetValueJSON(targets.Auth.ValueJSON)
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

func upsertCodexProvider(ctx context.Context, db *store.Store, metadataJSON string, hasProvider bool) (store.Provider, error) {
	if !hasProvider {
		provider, err := db.CreateProvider(ctx, store.CreateProviderParams{
			ID:           codexconfig.ProviderID,
			Name:         codexpreset.ProviderName,
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
	name := codexpreset.ProviderName
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
