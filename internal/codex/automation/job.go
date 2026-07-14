package automation

import (
	"context"
	"errors"

	"github.com/strahe/profiledeck/internal/apperror"
	codexappserver "github.com/strahe/profiledeck/internal/codex/appserver"
	codexauth "github.com/strahe/profiledeck/internal/codex/auth"
	codexquota "github.com/strahe/profiledeck/internal/codex/quota"
)

type JobKind string

const (
	JobQuota     JobKind = "quota"
	JobKeepalive JobKind = "keepalive"
)

type Status string

const (
	StatusAvailable    Status = "available"
	StatusAuthRequired Status = "auth_required"
	StatusUnsupported  Status = "unsupported"
	StatusUnavailable  Status = "unavailable"
)

// NativeRunner is Codex's token-owning app-server integration.
type NativeRunner interface {
	ReadRateLimits(context.Context, string) (codexquota.Snapshot, error)
	RefreshAccount(context.Context, string) error
}

// JobResult contains only runtime state. App maps it into its public DTOs.
type JobResult struct {
	Status             Status
	Snapshot           *codexquota.Snapshot
	NativeAttempted    bool
	NativeErrorKind    codexappserver.ErrorKind
	UsedDirectFallback bool
}

// NormalizeJobKind validates a runtime operation before it can mutate a working copy.
func NormalizeJobKind(value string) (JobKind, *apperror.Error) {
	kind := JobKind(value)
	if kind == "" {
		return JobQuota, nil
	}
	if kind != JobQuota && kind != JobKeepalive {
		return "", apperror.New(apperror.CodexInvalid, "unsupported Codex credential job")
	}
	return kind, nil
}

// Run performs the native quota or keepalive request. It does not access SQLite,
// locks, or external target files; callers capture resulting working-copy changes.
func Run(ctx context.Context, kind JobKind, homeDir, payload string, info codexauth.Info, runner NativeRunner, directReader codexquota.Reader, allowDirectFallback bool) (JobResult, error) {
	result := JobResult{Status: StatusUnavailable}
	if kind == JobQuota && !info.QuotaSupported {
		if info.Mode == codexauth.ModeUnsupported {
			result.Status = StatusUnsupported
		} else {
			result.Status = StatusAuthRequired
		}
		return result, nil
	}
	if kind == JobKeepalive && !info.RefreshSupported {
		result.Status = StatusUnsupported
		return result, nil
	}
	if runner == nil {
		return result, apperror.New(apperror.CodexInvalid, "Codex native credential runner is unavailable")
	}

	var snapshot codexquota.Snapshot
	var err error
	result.NativeAttempted = true
	if kind == JobQuota {
		snapshot, err = runner.ReadRateLimits(ctx, homeDir)
		if err != nil && codexappserver.KindOf(err) == codexappserver.ErrorAuthRequired && info.RefreshSupported {
			// Retry quota only after Codex's native refresh proves that the old
			// access token cannot satisfy the request.
			if refreshErr := runner.RefreshAccount(ctx, homeDir); refreshErr != nil {
				err = refreshErr
			} else {
				snapshot, err = runner.ReadRateLimits(ctx, homeDir)
			}
		}
	} else {
		err = runner.RefreshAccount(ctx, homeDir)
	}
	if err == nil {
		result.Status = StatusAvailable
		if kind == JobQuota {
			result.Snapshot = &snapshot
		}
		return result, nil
	}
	result.NativeErrorKind = codexappserver.KindOf(err)
	if kind == JobQuota && allowDirectFallback &&
		(result.NativeErrorKind == codexappserver.ErrorUnavailable || result.NativeErrorKind == codexappserver.ErrorIncompatible) {
		result.UsedDirectFallback = true
		return readDirect(ctx, payload, directReader, result), nil
	}
	switch result.NativeErrorKind {
	case codexappserver.ErrorAuthRequired, codexappserver.ErrorAuthPermanent:
		result.Status = StatusAuthRequired
	default:
		result.Status = StatusUnavailable
	}
	return result, nil
}

func readDirect(ctx context.Context, payload string, reader codexquota.Reader, result JobResult) JobResult {
	if reader == nil {
		return result
	}
	credentials, err := codexauth.ExtractBackendCredentials([]byte(payload))
	if err != nil {
		result.Status = StatusAuthRequired
		if errors.Is(err, codexauth.ErrUnsupportedAuthMode) {
			result.Status = StatusUnsupported
		}
		return result
	}
	snapshot, err := reader.Read(ctx, codexquota.Credentials{
		AccessToken: credentials.AccessToken, AccountID: credentials.AccountID, FedRAMP: credentials.FedRAMP,
	})
	if err != nil {
		result.Status = StatusUnavailable
		if codexquota.KindOf(err) == codexquota.ErrorAuthenticationRequired {
			result.Status = StatusAuthRequired
		}
		return result
	}
	result.Status = StatusAvailable
	result.Snapshot = &snapshot
	return result
}
