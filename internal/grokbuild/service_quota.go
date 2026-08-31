package grokbuild

import (
	"context"
	"path/filepath"

	"github.com/strahe/profiledeck/internal/apperror"
	grokconfig "github.com/strahe/profiledeck/internal/grokbuild/config"
	grokprofile "github.com/strahe/profiledeck/internal/grokbuild/profile"
	grokquota "github.com/strahe/profiledeck/internal/grokbuild/quota"
)

type GrokBuildProfileQuotaStatus string

const (
	GrokBuildProfileQuotaAvailable          GrokBuildProfileQuotaStatus = "available"
	GrokBuildProfileQuotaInactive           GrokBuildProfileQuotaStatus = "inactive"
	GrokBuildProfileQuotaAuthRequired       GrokBuildProfileQuotaStatus = "auth_required"
	GrokBuildProfileQuotaUnsupported        GrokBuildProfileQuotaStatus = "unsupported"
	GrokBuildProfileQuotaRuntimeUnavailable GrokBuildProfileQuotaStatus = "runtime_unavailable"
	GrokBuildProfileQuotaUnavailable        GrokBuildProfileQuotaStatus = "unavailable"
)

type ReadGrokBuildProfileQuotaRequest struct {
	ProfileID string `json:"profile_id"`
}

type GrokBuildProfileQuota struct {
	ProfileID    string                      `json:"profile_id"`
	CredentialID string                      `json:"credential_id"`
	ConfigSetID  string                      `json:"config_set_id"`
	Status       GrokBuildProfileQuotaStatus `json:"status"`
	Snapshot     *GrokBuildQuotaSnapshot     `json:"snapshot,omitempty"`
}

type GrokBuildQuotaSnapshot struct {
	FetchedAtUnixMS        int64    `json:"fetched_at_unix_ms"`
	CreditUsagePercent     *float64 `json:"credit_usage_percent,omitempty"`
	RemainingPercent       *float64 `json:"remaining_percent,omitempty"`
	PeriodType             string   `json:"period_type,omitempty"`
	PeriodStartUnixSeconds *int64   `json:"period_start_unix_seconds,omitempty"`
	ResetAtUnixSeconds     *int64   `json:"reset_at_unix_seconds,omitempty"`
	PeriodDurationSeconds  *int64   `json:"period_duration_seconds,omitempty"`
	IncludedLimitCents     *int64   `json:"included_limit_cents,omitempty"`
	IncludedUsedCents      *int64   `json:"included_used_cents,omitempty"`
	PrepaidBalanceCents    *int64   `json:"prepaid_balance_cents,omitempty"`
	OnDemandCapCents       *int64   `json:"on_demand_cap_cents,omitempty"`
	OnDemandUsedCents      *int64   `json:"on_demand_used_cents,omitempty"`
	OnDemandEnabled        *bool    `json:"on_demand_enabled,omitempty"`
	UnifiedBillingUser     *bool    `json:"unified_billing_user,omitempty"`
	SubscriptionTier       string   `json:"subscription_tier,omitempty"`
}

type grokBuildQuotaTarget struct {
	ProfileID    string
	CredentialID string
	ConfigSetID  string
	Active       bool
	Home         grokconfig.Home
}

func (service *Service) ReadProfileQuota(
	ctx context.Context,
	req ReadGrokBuildProfileQuotaRequest,
) (GrokBuildProfileQuota, error) {
	if err := service.requireAccess(ctx); err != nil {
		return GrokBuildProfileQuota{}, err
	}
	profileID, appErr := validateID(req.ProfileID, apperror.ProfileInvalid)
	if appErr != nil {
		return GrokBuildProfileQuota{}, appErr
	}
	if service.sharedLock == nil {
		return GrokBuildProfileQuota{}, apperror.New(apperror.GrokBuildInvalid, "Grok Build credits are unavailable")
	}
	var result GrokBuildProfileQuota
	err := service.sharedLock.RunWithSharedLock(ctx, "grok-build-quota-read", func(ctx context.Context) error {
		if err := service.requireAccess(ctx); err != nil {
			return err
		}
		target, err := service.resolveQuotaTarget(ctx, profileID)
		if err != nil {
			return err
		}
		result = GrokBuildProfileQuota{
			ProfileID: target.ProfileID, CredentialID: target.CredentialID,
			ConfigSetID: target.ConfigSetID, Status: GrokBuildProfileQuotaUnavailable,
		}
		if !target.Active {
			result.Status = GrokBuildProfileQuotaInactive
			return nil
		}
		if grokconfig.UnsupportedAuthEnvironment() {
			result.Status = GrokBuildProfileQuotaUnsupported
			return nil
		}
		if service.quotaReader == nil {
			return nil
		}
		snapshot, err := service.quotaReader.Read(ctx, target.Home.Dir)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			switch grokquota.KindOf(err) {
			case grokquota.ErrorAuthRequired:
				result.Status = GrokBuildProfileQuotaAuthRequired
			case grokquota.ErrorRuntimeUnavailable:
				result.Status = GrokBuildProfileQuotaRuntimeUnavailable
			case grokquota.ErrorUnsupported:
				result.Status = GrokBuildProfileQuotaUnsupported
			default:
				result.Status = GrokBuildProfileQuotaUnavailable
			}
			return nil
		}
		mapped := mapGrokBuildQuotaSnapshot(snapshot)
		result.Status = GrokBuildProfileQuotaAvailable
		result.Snapshot = &mapped
		return nil
	})
	if err != nil {
		return GrokBuildProfileQuota{}, err
	}
	return result, nil
}

func (service *Service) resolveQuotaTarget(ctx context.Context, profileID string) (grokBuildQuotaTarget, error) {
	home, err := service.resolveExistingHome()
	if err != nil {
		return grokBuildQuotaTarget{}, err
	}
	db, err := service.openStore(ctx, true)
	if err != nil {
		return grokBuildQuotaTarget{}, err
	}
	defer db.Close()
	if _, err := requireProvider(ctx, db); err != nil {
		return grokBuildQuotaTarget{}, err
	}
	storedHome, err := grokprofile.StoredHome(ctx, db)
	if err != nil {
		return grokBuildQuotaTarget{}, err
	}
	if filepath.Clean(storedHome.Dir) != filepath.Clean(home.Dir) ||
		filepath.Clean(storedHome.AuthPath) != filepath.Clean(home.AuthPath) ||
		filepath.Clean(storedHome.ConfigPath) != filepath.Clean(home.ConfigPath) {
		return grokBuildQuotaTarget{}, apperror.New(apperror.GrokBuildInvalid, "stored Grok Build Home does not match the managed Home")
	}
	detail, err := getProfileFromStore(ctx, db, profileID)
	if err != nil {
		return grokBuildQuotaTarget{}, err
	}
	if detail.Summary.CredentialID == "" || detail.Summary.ConfigSetID == "" {
		return grokBuildQuotaTarget{}, apperror.New(apperror.GrokBuildInvalid, "Grok Build Profile bindings are missing or invalid")
	}
	return grokBuildQuotaTarget{
		ProfileID: profileID, CredentialID: detail.Summary.CredentialID,
		ConfigSetID: detail.Summary.ConfigSetID, Active: detail.Summary.Active, Home: home,
	}, nil
}

func mapGrokBuildQuotaSnapshot(snapshot grokquota.Snapshot) GrokBuildQuotaSnapshot {
	result := GrokBuildQuotaSnapshot{
		FetchedAtUnixMS:       snapshot.FetchedAt.UnixMilli(),
		CreditUsagePercent:    snapshot.CreditUsagePercent,
		RemainingPercent:      snapshot.RemainingPercent,
		PeriodType:            snapshot.PeriodType,
		PeriodDurationSeconds: snapshot.PeriodDurationSeconds,
		IncludedLimitCents:    snapshot.IncludedLimitCents,
		IncludedUsedCents:     snapshot.IncludedUsedCents,
		PrepaidBalanceCents:   snapshot.PrepaidBalanceCents,
		OnDemandCapCents:      snapshot.OnDemandCapCents,
		OnDemandUsedCents:     snapshot.OnDemandUsedCents,
		OnDemandEnabled:       snapshot.OnDemandEnabled,
		UnifiedBillingUser:    snapshot.UnifiedBillingUser,
		SubscriptionTier:      snapshot.SubscriptionTier,
	}
	if snapshot.PeriodStart != nil {
		value := snapshot.PeriodStart.Unix()
		result.PeriodStartUnixSeconds = &value
	}
	if snapshot.PeriodEnd != nil {
		value := snapshot.PeriodEnd.Unix()
		result.ResetAtUnixSeconds = &value
	}
	return result
}
