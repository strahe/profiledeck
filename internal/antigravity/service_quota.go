package antigravity

import (
	"context"
	"time"

	agyauth "github.com/strahe/profiledeck/internal/antigravity/auth"
	agyquota "github.com/strahe/profiledeck/internal/antigravity/quota"
	"github.com/strahe/profiledeck/internal/apperror"
	switchtarget "github.com/strahe/profiledeck/internal/switching/target"
	"github.com/strahe/profiledeck/internal/validate"
)

type AntigravityProfileQuotaStatus string

const (
	AntigravityProfileQuotaAvailable    AntigravityProfileQuotaStatus = "available"
	AntigravityProfileQuotaInactive     AntigravityProfileQuotaStatus = "inactive"
	AntigravityProfileQuotaAuthRequired AntigravityProfileQuotaStatus = "auth_required"
	AntigravityProfileQuotaUnavailable  AntigravityProfileQuotaStatus = "unavailable"
)

type AntigravityProfileQuota struct {
	ProfileID    string                        `json:"profile_id"`
	CredentialID string                        `json:"credential_id"`
	Status       AntigravityProfileQuotaStatus `json:"status"`
	Snapshot     *AntigravityQuotaSnapshot     `json:"snapshot,omitempty"`
}

type AntigravityQuotaSnapshot struct {
	FetchedAtUnixMS int64                   `json:"fetched_at_unix_ms"`
	Groups          []AntigravityQuotaGroup `json:"groups"`
}

type AntigravityQuotaGroup struct {
	DisplayName string                   `json:"display_name"`
	Buckets     []AntigravityQuotaBucket `json:"buckets"`
}

type AntigravityQuotaBucket struct {
	BucketID           string  `json:"bucket_id"`
	Window             string  `json:"window"`
	RemainingPercent   float64 `json:"remaining_percent"`
	ResetAtUnixSeconds int64   `json:"reset_at_unix_seconds"`
}

func (service *Service) ReadProfileQuota(ctx context.Context, rawProfileID string) (AntigravityProfileQuota, error) {
	if err := service.requireAccess(ctx); err != nil {
		return AntigravityProfileQuota{}, err
	}
	profileID, appErr := validate.ID(rawProfileID, apperror.ProfileInvalid)
	if appErr != nil {
		return AntigravityProfileQuota{}, appErr
	}
	db, err := service.stores.OpenHealthy(ctx, true)
	if err != nil {
		return AntigravityProfileQuota{}, err
	}
	if _, err := requireAntigravityProvider(ctx, db); err != nil {
		_ = db.Close()
		return AntigravityProfileQuota{}, err
	}
	summary, err := antigravityProfileSummary(ctx, db, profileID)
	if closeErr := db.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return AntigravityProfileQuota{}, err
	}
	result := AntigravityProfileQuota{
		ProfileID: profileID, CredentialID: summary.CredentialID, Status: AntigravityProfileQuotaUnavailable,
	}
	if service.sharedLock == nil {
		return result, nil
	}
	var accessToken string
	// Capture the bound identity and Keyring working copy under the switch lock,
	// but never hold that lock across the network read.
	err = service.sharedLock.RunWithSharedLock(ctx, "antigravity-quota-read", func(ctx context.Context) error {
		if err := service.requireAccess(ctx); err != nil {
			return err
		}
		db, err := service.stores.OpenHealthy(ctx, true)
		if err != nil {
			return err
		}
		defer db.Close()
		if _, err := requireAntigravityProvider(ctx, db); err != nil {
			return err
		}
		summary, err := antigravityProfileSummary(ctx, db, profileID)
		if err != nil {
			return err
		}
		result.CredentialID = summary.CredentialID
		if !summary.Active {
			result.Status = AntigravityProfileQuotaInactive
			return nil
		}
		backend, ok := service.targets.Backend(switchtarget.BackendKeyring)
		if !ok {
			return nil
		}
		snapshot, err := backend.Inspect(ctx, antigravityTargetSpec())
		if err != nil {
			return nil
		}
		if !snapshot.Exists {
			result.Status = AntigravityProfileQuotaAuthRequired
			return nil
		}
		_, payload, err := agyauth.Normalize([]byte(snapshot.Content))
		if err != nil {
			result.Status = AntigravityProfileQuotaAuthRequired
			return nil
		}
		expiresAt := time.UnixMilli(agyauth.ExpiryUnixMS(payload))
		now := time.Now
		if service.now != nil {
			now = service.now
		}
		if !expiresAt.After(now().Add(30 * time.Second)) {
			result.Status = AntigravityProfileQuotaAuthRequired
			return nil
		}
		accessToken = payload.Token.AccessToken
		return nil
	})
	if err != nil {
		return AntigravityProfileQuota{}, err
	}
	if result.Status != AntigravityProfileQuotaUnavailable || accessToken == "" || service.quotaReader == nil {
		return result, nil
	}
	snapshot, err := service.quotaReader.Read(ctx, accessToken)
	accessToken = ""
	if err != nil {
		if ctx.Err() != nil {
			return AntigravityProfileQuota{}, ctx.Err()
		}
		if agyquota.KindOf(err) == agyquota.ErrorAuthRequired {
			result.Status = AntigravityProfileQuotaAuthRequired
		}
		return result, nil
	}
	mapped := mapAntigravityQuotaSnapshot(snapshot)
	result.Status = AntigravityProfileQuotaAvailable
	result.Snapshot = &mapped
	return result, nil
}

func mapAntigravityQuotaSnapshot(snapshot agyquota.Snapshot) AntigravityQuotaSnapshot {
	result := AntigravityQuotaSnapshot{
		FetchedAtUnixMS: snapshot.FetchedAt.UnixMilli(),
		Groups:          make([]AntigravityQuotaGroup, 0, len(snapshot.Groups)),
	}
	for _, group := range snapshot.Groups {
		mapped := AntigravityQuotaGroup{
			DisplayName: group.DisplayName,
			Buckets:     make([]AntigravityQuotaBucket, 0, len(group.Buckets)),
		}
		for _, bucket := range group.Buckets {
			mapped.Buckets = append(mapped.Buckets, AntigravityQuotaBucket{
				BucketID: bucket.ID, Window: bucket.Window, RemainingPercent: bucket.RemainingPercent,
				ResetAtUnixSeconds: bucket.ResetAtUnixSeconds,
			})
		}
		result.Groups = append(result.Groups, mapped)
	}
	return result
}
