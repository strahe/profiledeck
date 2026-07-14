package codex

import (
	"errors"
	"testing"
	"time"

	"github.com/strahe/profiledeck/internal/apperror"
	codexquota "github.com/strahe/profiledeck/internal/codex/quota"
	"github.com/strahe/profiledeck/internal/store"
)

func TestSelectCodexQuotaProfilesValidatesAndDeduplicatesSingleProfileRequests(t *testing.T) {
	profile, err := profileFromStore(store.Profile{ID: "work", MetadataJSON: "{}"})
	if err != nil {
		t.Fatalf("expected Profile fixture, got %v", err)
	}
	summaries := []CodexProfileSummary{{Profile: profile}}
	selected, err := selectCodexQuotaProfiles(summaries, []string{"work", "work"})
	if err != nil || len(selected) != 1 || selected[0].Profile.ID != "work" {
		t.Fatalf("expected one selected Profile, selected=%#v err=%v", selected, err)
	}
	_, err = selectCodexQuotaProfiles(summaries, []string{"missing"})
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != apperror.ProfileNotFound {
		t.Fatalf("expected missing Profile error, got %v", err)
	}
}

func TestMapCodexQuotaSnapshotPreservesUsageAndResetSemantics(t *testing.T) {
	count := int64(2)
	snapshot := mapCodexQuotaSnapshot(codexquota.Snapshot{
		FetchedAt: time.Unix(1780000000, 0), PlanType: "plus",
		RateLimit: &codexquota.RateLimit{ID: "codex", Allowed: true, PrimaryWindow: &codexquota.Window{
			UsedPercent: 22, RemainingPercent: 78, LimitWindowSeconds: 18000, ResetAtUnixSeconds: 1780003600,
		}},
		AdditionalRateLimits:  []codexquota.RateLimit{},
		ResetCreditsAvailable: &count,
	})
	if snapshot.FetchedAtUnixMS != 1780000000000 || snapshot.RateLimit == nil || snapshot.RateLimit.PrimaryWindow == nil {
		t.Fatalf("unexpected mapped snapshot: %#v", snapshot)
	}
	if snapshot.RateLimit.PrimaryWindow.UsedPercent != 22 || snapshot.RateLimit.PrimaryWindow.RemainingPercent != 78 {
		t.Fatalf("expected consumed and remaining percentages to stay distinct, got %#v", snapshot.RateLimit.PrimaryWindow)
	}
	if snapshot.ResetCreditsAvailableCount == nil || *snapshot.ResetCreditsAvailableCount != 2 {
		t.Fatalf("unexpected reset credits: %#v", snapshot.ResetCreditsAvailableCount)
	}
}
