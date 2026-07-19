package codex

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/strahe/profiledeck/internal/apperror"
	codexauth "github.com/strahe/profiledeck/internal/codex/auth"
	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	codexpreset "github.com/strahe/profiledeck/internal/codex/preset"
	codexprofile "github.com/strahe/profiledeck/internal/codex/profile"
	"github.com/strahe/profiledeck/internal/store"
)

func codexCredentialIDFromTarget(target store.ProfileTarget) (string, error) {
	return codexprofile.CredentialIDFromTarget(target)
}

func codexConfigSetIDFromTarget(target store.ProfileTarget) (string, error) {
	return codexprofile.ConfigSetIDFromTarget(target)
}

func codexCredentialBindingCount(ctx context.Context, db *store.Store, credentialID string) (int, error) {
	return codexprofile.CredentialBindingCount(ctx, db, credentialID)
}

func codexConfigSetBindingCount(ctx context.Context, db *store.Store, configSetID string) (int, error) {
	return codexprofile.ConfigSetBindingCount(ctx, db, configSetID)
}

func upsertCodexAuthCredential(ctx context.Context, db *store.Store, credentialID, payload string) (store.ProviderCredential, error) {
	return codexprofile.UpsertAuthCredential(ctx, db, credentialID, payload)
}

func upsertCodexConfigSet(ctx context.Context, db *store.Store, configSetID, name, description, payload string) (store.ProviderConfigSet, error) {
	return codexprofile.UpsertConfigSet(ctx, db, configSetID, name, description, payload)
}

func readCodexConfigSnapshot(home codexconfig.Home) (string, bool, *apperror.Error) {
	snapshot, err := codexconfig.ReadSnapshot(home.ConfigPath)
	if err != nil {
		return "", false, codexConfigSnapshotAppError(home.ConfigPath, err)
	}
	return snapshot.Content, snapshot.Missing, nil
}

func readCodexAuthSnapshot(home codexconfig.Home) (codexauth.Snapshot, *apperror.Error) {
	snapshot, err := codexauth.ReadSnapshot(home.AuthPath)
	if err != nil {
		if os.IsNotExist(err) {
			return codexauth.Snapshot{}, apperror.New(apperror.CodexInvalid, codexpreset.FileCredentialStoreHint).WithDetail("auth_path", home.AuthPath)
		}
		var pathErr *os.PathError
		if errors.As(err, &pathErr) {
			return codexauth.Snapshot{}, apperror.Wrap(apperror.CodexInvalid, "failed to read Codex auth", err).WithDetail("path", home.AuthPath)
		}
		return codexauth.Snapshot{}, codexAuthPayloadAppError(err).WithDetail("path", home.AuthPath)
	}
	return snapshot, nil
}

func codexConfigSnapshotAppError(path string, err error) *apperror.Error {
	message := err.Error()
	switch {
	case strings.HasPrefix(message, "read Codex config:"):
		return apperror.Wrap(apperror.CodexInvalid, "failed to read Codex config", err).WithDetail("path", path)
	case strings.HasPrefix(message, "Codex config TOML is invalid:"):
		// TOML parser errors can include source lines, so the raw cause must not
		// cross an output boundary where configuration secrets could be exposed.
		return apperror.New(apperror.CodexInvalid, "Codex config TOML is invalid").WithDetail("path", path)
	case message == "Codex config is too large":
		return apperror.New(apperror.CodexInvalid, "Codex config is too large").WithDetail("path", path)
	default:
		return apperror.Wrap(apperror.CodexInvalid, "Codex config is invalid", err).WithDetail("path", path)
	}
}

func codexAuthPayloadAppError(err error) *apperror.Error {
	message := "Codex auth payload is invalid"
	var fieldErr codexauth.FieldError
	if errors.As(err, &fieldErr) {
		message = "Codex auth account metadata is invalid"
	}
	var sizeErr codexauth.SizeError
	if errors.As(err, &sizeErr) {
		message = "Codex auth payload is too large"
	}
	appErr := apperror.Wrap(apperror.CodexInvalid, message, err)
	if fieldErr.Field != "" {
		appErr = appErr.WithDetail("field", fieldErr.Field)
	}
	if sizeErr.Max > 0 {
		appErr = appErr.WithDetail("size_bytes", sizeErr.Size).WithDetail("max_bytes", sizeErr.Max)
	}
	return appErr
}

func upsertCodexProvider(ctx context.Context, db *store.Store, metadataJSON string, hasProvider bool) (store.Provider, error) {
	return codexprofile.UpsertProvider(ctx, db, metadataJSON, hasProvider)
}

func upsertCodexProfile(ctx context.Context, db *store.Store, profileID string, fields managedProfileFields, hasProfile bool) (store.Profile, error) {
	return codexprofile.UpsertProfile(ctx, db, profileID, codexprofile.ProfileFields{
		CreateName:        fields.CreateName,
		CreateDescription: fields.CreateDescription,
		UpdateName:        fields.UpdateName,
		UpdateDescription: fields.UpdateDescription,
	}, hasProfile)
}

func upsertCodexConfigTarget(ctx context.Context, db *store.Store, profileID string, home codexconfig.Home, valueJSON, metadataJSON string, _ bool) (store.ProfileTarget, error) {
	return codexprofile.UpsertConfigBinding(ctx, db, profileID, home, valueJSON, metadataJSON)
}

func upsertCodexAuthTarget(ctx context.Context, db *store.Store, profileID string, home codexconfig.Home, valueJSON, metadataJSON string, _ bool) (store.ProfileTarget, error) {
	return codexprofile.UpsertAuthBinding(ctx, db, profileID, home, valueJSON, metadataJSON)
}

func codexBindingTargets(ctx context.Context, db *store.Store, profileID string, home codexconfig.Home) ([]store.ProfileTarget, error) {
	return codexprofile.BindingTargets(ctx, db, profileID, home)
}

func codexStoredHome(ctx context.Context, db *store.Store) (codexconfig.Home, error) {
	return codexprofile.StoredHome(ctx, db)
}

func storedCodexBindingTargets(ctx context.Context, db *store.Store, profileID string) ([]store.ProfileTarget, error) {
	return codexprofile.StoredBindingTargets(ctx, db, profileID)
}

func allStoredCodexBindingTargets(ctx context.Context, db *store.Store) ([]store.ProfileTarget, error) {
	return codexprofile.AllStoredBindingTargets(ctx, db)
}

func mapCodexCredentialStoreError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, store.ErrNotFound) {
		return apperror.New(apperror.CodexInvalid, "Codex auth credential not found")
	}
	return apperror.Wrap(apperror.StoreStatusFailed, "Codex auth credential store operation failed", err)
}

func mapCodexConfigSetStoreError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, store.ErrNotFound) {
		return apperror.New(apperror.CodexInvalid, "Codex config set not found")
	}
	if errors.Is(err, store.ErrInUse) {
		return apperror.New(apperror.ProfileInUse, "Codex config set is in use")
	}
	return apperror.Wrap(apperror.StoreStatusFailed, "Codex config set store operation failed", err)
}

func requireCodexAuthCredential(ctx context.Context, db *store.Store, credentialID string) (store.ProviderCredential, error) {
	return codexprofile.RequireAuthCredential(ctx, db, credentialID)
}

func requireCodexConfigSet(ctx context.Context, db *store.Store, configSetID string) (store.ProviderConfigSet, error) {
	return codexprofile.RequireConfigSet(ctx, db, configSetID)
}

func newCodexCredentialID(now time.Time) (string, error) {
	return codexprofile.NewCredentialID(now)
}
