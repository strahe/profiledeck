package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	codexappserver "github.com/strahe/profiledeck/internal/codex/appserver"
	codexauth "github.com/strahe/profiledeck/internal/codex/auth"
	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	codexquota "github.com/strahe/profiledeck/internal/codex/quota"
	"github.com/strahe/profiledeck/internal/store"
)

type CodexCredentialJobKind string

const (
	CodexCredentialJobQuota     CodexCredentialJobKind = "quota"
	CodexCredentialJobKeepalive CodexCredentialJobKind = "keepalive"
)

type ReadCodexProfileQuotaRequest struct {
	ConfigDir string
	ProfileID string
}

type RunCodexCredentialJobRequest struct {
	ConfigDir           string
	ProfileID           string
	Kind                CodexCredentialJobKind
	AllowDirectFallback bool
}

type CodexCredentialJobResult struct {
	Quota              CodexProfileQuota
	CredentialSHA256   string
	CredentialUpdated  bool
	CredentialConflict bool
	NativeAttempted    bool
	NativeErrorKind    codexappserver.ErrorKind
	UsedDirectFallback bool
}

type codexNativeRunner interface {
	ReadRateLimits(context.Context, string) (codexquota.Snapshot, error)
	RefreshAccount(context.Context, string) error
}

func ReadCodexProfileQuota(ctx context.Context, req ReadCodexProfileQuotaRequest) (CodexProfileQuota, error) {
	result, err := RunCodexCredentialJob(ctx, RunCodexCredentialJobRequest{
		ConfigDir: req.ConfigDir, ProfileID: req.ProfileID,
		Kind: CodexCredentialJobQuota, AllowDirectFallback: true,
	})
	return result.Quota, err
}

func RunCodexCredentialJob(ctx context.Context, req RunCodexCredentialJobRequest) (CodexCredentialJobResult, error) {
	return runCodexCredentialJob(ctx, req, codexappserver.NewRunner(), codexquota.NewClient())
}

func runCodexCredentialJob(ctx context.Context, req RunCodexCredentialJobRequest, runner codexNativeRunner, directReader codexquota.Reader) (CodexCredentialJobResult, error) {
	profileID, appErr := validateID(req.ProfileID, ErrorProfileInvalid)
	if appErr != nil {
		return CodexCredentialJobResult{}, appErr
	}
	kind := req.Kind
	if kind == "" {
		kind = CodexCredentialJobQuota
	}
	if kind != CodexCredentialJobQuota && kind != CodexCredentialJobKeepalive {
		return CodexCredentialJobResult{}, NewError(ErrorCodexInvalid, "unsupported Codex credential job")
	}

	db, lock, _, err := openLockedCodexStore(ctx, req.ConfigDir, "auth-runtime")
	if err != nil {
		return CodexCredentialJobResult{}, err
	}
	defer db.Close()
	defer lock.Release()

	summaries, err := listCodexProfileSummaries(ctx, db)
	if err != nil {
		return CodexCredentialJobResult{}, err
	}
	selected, err := selectCodexQuotaProfiles(summaries, []string{profileID})
	if err != nil {
		return CodexCredentialJobResult{}, err
	}
	summary := selected[0]
	result := CodexCredentialJobResult{Quota: CodexProfileQuota{
		ProfileID: profileID, CredentialID: summary.CredentialID, Status: CodexProfileQuotaUnavailable,
	}}
	if summary.CredentialID == "" {
		result.Quota.Status = CodexProfileQuotaAuthRequired
		return result, nil
	}
	credential, err := requireCodexAuthCredential(ctx, db, summary.CredentialID)
	if err != nil {
		return CodexCredentialJobResult{}, err
	}
	result.CredentialSHA256 = credential.PayloadSHA256

	home, sourcePayload, active, cleanup, err := prepareCodexCredentialHome(ctx, db, summaries, summary.CredentialID, credential.PayloadJSON)
	if err != nil {
		return CodexCredentialJobResult{}, err
	}
	defer cleanup()
	info, err := codexauth.Inspect([]byte(sourcePayload))
	if err != nil {
		result.Quota.Status = CodexProfileQuotaAuthRequired
		return result, nil
	}
	if kind == CodexCredentialJobQuota && !info.QuotaSupported {
		if info.Mode == codexauth.ModeUnsupported {
			result.Quota.Status = CodexProfileQuotaUnsupported
		} else {
			result.Quota.Status = CodexProfileQuotaAuthRequired
		}
		return result, nil
	}
	if kind == CodexCredentialJobKeepalive && !info.RefreshSupported {
		result.Quota.Status = CodexProfileQuotaUnsupported
		return result, nil
	}

	var snapshot codexquota.Snapshot
	result.NativeAttempted = true
	if kind == CodexCredentialJobQuota {
		snapshot, err = runner.ReadRateLimits(ctx, home.Dir)
		if err != nil && codexappserver.KindOf(err) == codexappserver.ErrorAuthRequired && info.RefreshSupported {
			// Codex's proactive refresh path can fall back to the old access token
			// after a refresh failure. Force the native account refresh only after
			// that authenticated quota read fails so permanent refresh-token errors
			// can be distinguished from transient failures and successful recovery
			// can retry the same native quota method.
			if refreshErr := runner.RefreshAccount(ctx, home.Dir); refreshErr != nil {
				err = refreshErr
			} else {
				snapshot, err = runner.ReadRateLimits(ctx, home.Dir)
			}
		}
	} else {
		err = runner.RefreshAccount(ctx, home.Dir)
	}
	if err != nil {
		result.NativeErrorKind = codexappserver.KindOf(err)
		if kind == CodexCredentialJobQuota && req.AllowDirectFallback &&
			(result.NativeErrorKind == codexappserver.ErrorUnavailable || result.NativeErrorKind == codexappserver.ErrorIncompatible) {
			result.UsedDirectFallback = true
			result = readCodexQuotaDirect(ctx, sourcePayload, directReader, result)
			if ctx.Err() != nil {
				return CodexCredentialJobResult{}, ctx.Err()
			}
			return result, nil
		}
	}
	if captureErr := captureCodexCredentialAfterNativeJob(ctx, db, home.AuthPath, credential, sourcePayload, info, active, &result); captureErr != nil {
		return CodexCredentialJobResult{}, captureErr
	}
	if err == nil {
		result.Quota.Status = CodexProfileQuotaAvailable
		if kind == CodexCredentialJobQuota {
			mapped := mapCodexQuotaSnapshot(snapshot)
			result.Quota.Snapshot = &mapped
		}
		return result, nil
	}
	if ctx.Err() != nil {
		return CodexCredentialJobResult{}, ctx.Err()
	}
	switch result.NativeErrorKind {
	case codexappserver.ErrorAuthRequired, codexappserver.ErrorAuthPermanent:
		result.Quota.Status = CodexProfileQuotaAuthRequired
	default:
		result.Quota.Status = CodexProfileQuotaUnavailable
	}
	return result, nil
}

func prepareCodexCredentialHome(ctx context.Context, db *store.Store, summaries []CodexProfileSummary, credentialID string, storedPayload string) (codexconfig.Home, string, bool, func(), error) {
	for _, summary := range summaries {
		if !summary.Active || summary.CredentialID != credentialID {
			continue
		}
		targets, err := db.ListProfileTargets(ctx, summary.Profile.ID, codexconfig.ProviderID, true)
		if err != nil {
			return codexconfig.Home{}, "", false, func() {}, WrapError(ErrorStoreStatusFailed, "failed to list active Codex targets", err)
		}
		for _, target := range targets {
			if target.TargetID != codexconfig.AuthTargetID || !target.Enabled {
				continue
			}
			boundID, err := codexCredentialIDFromTarget(target)
			if err != nil || boundID != credentialID {
				continue
			}
			home := codexconfig.Home{
				Dir: filepath.Dir(target.Path), AuthPath: target.Path,
				ConfigPath: filepath.Join(filepath.Dir(target.Path), codexconfig.ConfigFileName),
			}
			payload := storedPayload
			if snapshot, err := codexauth.ReadSnapshot(target.Path); err == nil {
				payload = snapshot.Payload
			}
			return home, payload, true, func() {}, nil
		}
	}

	tempDir, err := os.MkdirTemp("", "profiledeck-codex-auth-")
	if err != nil {
		return codexconfig.Home{}, "", false, func() {}, WrapError(ErrorRuntimeInitFailed, "failed to create private Codex auth runtime", err)
	}
	cleanup := func() { _ = os.RemoveAll(tempDir) }
	if err := os.Chmod(tempDir, 0o700); err != nil {
		cleanup()
		return codexconfig.Home{}, "", false, func() {}, WrapError(ErrorRuntimeInitFailed, "failed to secure private Codex auth runtime", err)
	}
	home := codexconfig.Home{
		Dir: tempDir, ConfigPath: filepath.Join(tempDir, codexconfig.ConfigFileName),
		AuthPath: filepath.Join(tempDir, codexconfig.AuthFileName),
	}
	file, err := os.OpenFile(home.AuthPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		cleanup()
		return codexconfig.Home{}, "", false, func() {}, WrapError(ErrorRuntimeInitFailed, "failed to create private Codex auth working copy", err)
	}
	_, writeErr := file.WriteString(storedPayload)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		cleanup()
		if writeErr == nil {
			writeErr = closeErr
		}
		return codexconfig.Home{}, "", false, func() {}, WrapError(ErrorRuntimeInitFailed, "failed to write private Codex auth working copy", writeErr)
	}
	return home, storedPayload, false, cleanup, nil
}

func captureCodexCredentialAfterNativeJob(ctx context.Context, db *store.Store, authPath string, credential store.ProviderCredential, sourcePayload string, sourceInfo codexauth.Info, active bool, result *CodexCredentialJobResult) error {
	snapshot, err := codexauth.ReadSnapshot(authPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return WrapError(ErrorCodexInvalid, "failed to capture Codex auth after native request", err)
	}
	if snapshot.Payload == sourcePayload && sha256HexString(snapshot.Payload) == credential.PayloadSHA256 {
		return nil
	}
	updatedInfo, err := codexauth.Inspect([]byte(snapshot.Payload))
	if err != nil {
		return NewError(ErrorCodexInvalid, "Codex wrote an invalid auth working copy")
	}
	// A managed refresh must never discard the refresh token that keeps an
	// inactive credential recoverable in future app sessions.
	if sourceInfo.RefreshSupported && !updatedInfo.HasRefreshToken {
		return NewError(ErrorCodexInvalid, "Codex token refresh did not preserve the refresh token")
	}
	params := store.UpsertProviderCredentialParams{
		ID: credential.ID, ProviderID: credential.ProviderID, CredentialKind: credential.CredentialKind,
		PayloadJSON: snapshot.Payload, PayloadSHA256: sha256HexString(snapshot.Payload), MetadataJSON: credential.MetadataJSON,
	}
	if active {
		updated, err := db.UpsertProviderCredential(ctx, params)
		if err != nil {
			return mapCodexCredentialStoreError(err)
		}
		result.CredentialSHA256 = updated.PayloadSHA256
		result.CredentialUpdated = true
		return nil
	}
	updated, swapped, err := db.CompareAndSwapProviderCredential(ctx, credential.PayloadSHA256, params)
	if err != nil {
		return mapCodexCredentialStoreError(err)
	}
	if !swapped {
		// Inactive jobs operate on a temporary copy. A concurrent credential
		// replacement wins; stale refreshed tokens must never overwrite it.
		result.CredentialConflict = true
		return nil
	}
	result.CredentialSHA256 = updated.PayloadSHA256
	result.CredentialUpdated = true
	return nil
}

func readCodexQuotaDirect(ctx context.Context, payload string, reader codexquota.Reader, result CodexCredentialJobResult) CodexCredentialJobResult {
	credentials, err := codexauth.ExtractBackendCredentials([]byte(payload))
	if err != nil {
		result.Quota.Status = CodexProfileQuotaAuthRequired
		if errors.Is(err, codexauth.ErrUnsupportedAuthMode) {
			result.Quota.Status = CodexProfileQuotaUnsupported
		}
		return result
	}
	snapshot, err := reader.Read(ctx, codexquota.Credentials{
		AccessToken: credentials.AccessToken, AccountID: credentials.AccountID, FedRAMP: credentials.FedRAMP,
	})
	if err != nil {
		result.Quota.Status = CodexProfileQuotaUnavailable
		if codexquota.KindOf(err) == codexquota.ErrorAuthenticationRequired {
			result.Quota.Status = CodexProfileQuotaAuthRequired
		}
		return result
	}
	result.Quota.Status = CodexProfileQuotaAvailable
	mapped := mapCodexQuotaSnapshot(snapshot)
	result.Quota.Snapshot = &mapped
	return result
}
