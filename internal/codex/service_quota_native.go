package codex

import (
	"context"
	"os"
	"path/filepath"

	"github.com/strahe/profiledeck/internal/apperror"
	codexappserver "github.com/strahe/profiledeck/internal/codex/appserver"
	codexauth "github.com/strahe/profiledeck/internal/codex/auth"
	codexautomation "github.com/strahe/profiledeck/internal/codex/automation"
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
	ProfileID string
}

type RunCodexCredentialJobRequest struct {
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

func (service *Service) ReadProfileQuota(ctx context.Context, req ReadCodexProfileQuotaRequest) (CodexProfileQuota, error) {
	result, err := service.RunCredentialJob(ctx, RunCodexCredentialJobRequest{
		ProfileID: req.ProfileID,
		Kind:      CodexCredentialJobQuota, AllowDirectFallback: true,
	})
	return result.Quota, err
}

func (service *Service) RunCredentialJob(ctx context.Context, req RunCodexCredentialJobRequest) (CodexCredentialJobResult, error) {
	if err := service.requireAccess(ctx); err != nil {
		return CodexCredentialJobResult{}, err
	}
	return service.runCredentialJob(ctx, req, codexappserver.NewRunner(), codexquota.NewClient())
}

func (service *Service) runCredentialJob(ctx context.Context, req RunCodexCredentialJobRequest, runner codexNativeRunner, directReader codexquota.Reader) (CodexCredentialJobResult, error) {
	profileID, appErr := validateID(req.ProfileID, apperror.ProfileInvalid)
	if appErr != nil {
		return CodexCredentialJobResult{}, appErr
	}
	kind, appErr := codexautomation.NormalizeJobKind(string(req.Kind))
	if appErr != nil {
		return CodexCredentialJobResult{}, appErr
	}

	var result CodexCredentialJobResult
	err := service.sharedLock.RunWithSharedLock(ctx, "codex-auth-runtime", func(ctx context.Context) error {
		if err := service.requireAccess(ctx); err != nil {
			return err
		}
		db, err := service.openStore(ctx, false)
		if err != nil {
			return err
		}
		defer db.Close()
		if _, err := requireCodexProvider(ctx, db); err != nil {
			return err
		}
		var runErr error
		result, runErr = runCodexCredentialJobLocked(ctx, db, profileID, kind, req.AllowDirectFallback, runner, directReader)
		return runErr
	})
	return result, err
}

func runCodexCredentialJobLocked(
	ctx context.Context,
	db *store.Store,
	profileID string,
	kind codexautomation.JobKind,
	allowDirectFallback bool,
	runner codexNativeRunner,
	directReader codexquota.Reader,
) (CodexCredentialJobResult, error) {
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
	job, err := codexautomation.Run(ctx, kind, home.Dir, sourcePayload, info, runner, directReader, allowDirectFallback)
	if err != nil {
		return CodexCredentialJobResult{}, err
	}
	result.NativeAttempted = job.NativeAttempted
	result.NativeErrorKind = job.NativeErrorKind
	result.UsedDirectFallback = job.UsedDirectFallback
	result.Quota.Status = CodexProfileQuotaStatus(job.Status)
	if job.Snapshot != nil {
		mapped := mapCodexQuotaSnapshot(*job.Snapshot)
		result.Quota.Snapshot = &mapped
	}
	if job.UsedDirectFallback {
		if ctx.Err() != nil {
			return CodexCredentialJobResult{}, ctx.Err()
		}
		return result, nil
	}
	if !job.NativeAttempted {
		return result, nil
	}
	if captureErr := captureCodexCredentialAfterNativeJob(ctx, db, home.AuthPath, credential, sourcePayload, info, active, &result); captureErr != nil {
		return CodexCredentialJobResult{}, captureErr
	}
	if ctx.Err() != nil {
		return CodexCredentialJobResult{}, ctx.Err()
	}
	return result, nil
}

func prepareCodexCredentialHome(ctx context.Context, db *store.Store, summaries []CodexProfileSummary, credentialID, storedPayload string) (codexconfig.Home, string, bool, func(), error) {
	for _, summary := range summaries {
		if !summary.Active || summary.CredentialID != credentialID {
			continue
		}
		targets, err := storedCodexBindingTargets(ctx, db, summary.Profile.ID)
		if err != nil {
			return codexconfig.Home{}, "", false, func() {}, apperror.Wrap(apperror.StoreStatusFailed, "failed to list active Codex targets", err)
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
		return codexconfig.Home{}, "", false, func() {}, apperror.Wrap(apperror.RuntimeInitFailed, "failed to create private Codex auth runtime", err)
	}
	cleanup := func() { _ = os.RemoveAll(tempDir) }
	if err := os.Chmod(tempDir, 0o700); err != nil {
		cleanup()
		return codexconfig.Home{}, "", false, func() {}, apperror.Wrap(apperror.RuntimeInitFailed, "failed to secure private Codex auth runtime", err)
	}
	home := codexconfig.Home{
		Dir: tempDir, ConfigPath: filepath.Join(tempDir, codexconfig.ConfigFileName),
		AuthPath: filepath.Join(tempDir, codexconfig.AuthFileName),
	}
	file, err := os.OpenFile(home.AuthPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		cleanup()
		return codexconfig.Home{}, "", false, func() {}, apperror.Wrap(apperror.RuntimeInitFailed, "failed to create private Codex auth working copy", err)
	}
	_, writeErr := file.WriteString(storedPayload)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		cleanup()
		if writeErr == nil {
			writeErr = closeErr
		}
		return codexconfig.Home{}, "", false, func() {}, apperror.Wrap(apperror.RuntimeInitFailed, "failed to write private Codex auth working copy", writeErr)
	}
	return home, storedPayload, false, cleanup, nil
}

func captureCodexCredentialAfterNativeJob(ctx context.Context, db *store.Store, authPath string, credential store.ProviderCredential, sourcePayload string, sourceInfo codexauth.Info, active bool, result *CodexCredentialJobResult) error {
	snapshot, err := codexauth.ReadSnapshot(authPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return apperror.Wrap(apperror.CodexInvalid, "failed to capture Codex auth after native request", err)
	}
	if snapshot.Payload == sourcePayload && sha256HexString(snapshot.Payload) == credential.PayloadSHA256 {
		return nil
	}
	updatedInfo, err := codexauth.Inspect([]byte(snapshot.Payload))
	if err != nil {
		return apperror.New(apperror.CodexInvalid, "Codex wrote an invalid auth working copy")
	}
	// A managed refresh must never discard the refresh token that keeps an
	// inactive credential recoverable in future app sessions.
	if sourceInfo.RefreshSupported && !updatedInfo.HasRefreshToken {
		return apperror.New(apperror.CodexInvalid, "Codex token refresh did not preserve the refresh token")
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
