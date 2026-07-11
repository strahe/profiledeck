package app

import codexquota "github.com/strahe/profiledeck/internal/codex/quota"

type CodexProfileQuotaStatus string

const (
	CodexProfileQuotaAvailable    CodexProfileQuotaStatus = "available"
	CodexProfileQuotaAuthRequired CodexProfileQuotaStatus = "auth_required"
	CodexProfileQuotaUnsupported  CodexProfileQuotaStatus = "unsupported"
	CodexProfileQuotaUnavailable  CodexProfileQuotaStatus = "unavailable"
)

type CodexProfileQuota struct {
	ProfileID    string                  `json:"profile_id"`
	CredentialID string                  `json:"credential_id,omitempty"`
	Status       CodexProfileQuotaStatus `json:"status"`
	Snapshot     *CodexQuotaSnapshot     `json:"snapshot,omitempty"`
}

type CodexQuotaSnapshot struct {
	FetchedAtUnixMS            int64                   `json:"fetched_at_unix_ms"`
	PlanType                   string                  `json:"plan_type,omitempty"`
	RateLimit                  *CodexQuotaRateLimit    `json:"rate_limit,omitempty"`
	AdditionalRateLimits       []CodexQuotaRateLimit   `json:"additional_rate_limits"`
	Credits                    *CodexQuotaCredits      `json:"credits,omitempty"`
	SpendControl               *CodexQuotaSpendControl `json:"spend_control,omitempty"`
	RateLimitReachedType       string                  `json:"rate_limit_reached_type,omitempty"`
	ResetCreditsAvailableCount *int64                  `json:"reset_credits_available_count,omitempty"`
}

type CodexQuotaRateLimit struct {
	ID              string            `json:"id"`
	Name            string            `json:"name,omitempty"`
	Allowed         bool              `json:"allowed"`
	LimitReached    bool              `json:"limit_reached"`
	PrimaryWindow   *CodexQuotaWindow `json:"primary_window,omitempty"`
	SecondaryWindow *CodexQuotaWindow `json:"secondary_window,omitempty"`
}

type CodexQuotaWindow struct {
	UsedPercent        float64 `json:"used_percent"`
	RemainingPercent   float64 `json:"remaining_percent"`
	LimitWindowSeconds int64   `json:"limit_window_seconds"`
	ResetAfterSeconds  int64   `json:"reset_after_seconds"`
	ResetAtUnixSeconds int64   `json:"reset_at_unix_seconds"`
}

type CodexQuotaCredits struct {
	HasCredits bool    `json:"has_credits"`
	Unlimited  bool    `json:"unlimited"`
	Balance    *string `json:"balance,omitempty"`
}

type CodexQuotaSpendControl struct {
	Reached         bool                         `json:"reached"`
	IndividualLimit *CodexQuotaSpendControlLimit `json:"individual_limit,omitempty"`
}

type CodexQuotaSpendControlLimit struct {
	Source             string  `json:"source,omitempty"`
	Limit              string  `json:"limit"`
	Used               string  `json:"used"`
	Remaining          string  `json:"remaining"`
	UsedPercent        float64 `json:"used_percent"`
	RemainingPercent   float64 `json:"remaining_percent"`
	ResetAfterSeconds  int64   `json:"reset_after_seconds"`
	ResetAtUnixSeconds int64   `json:"reset_at_unix_seconds"`
}

func selectCodexQuotaProfiles(summaries []CodexProfileSummary, profileIDs []string) ([]CodexProfileSummary, error) {
	if len(profileIDs) == 0 {
		return summaries, nil
	}
	byID := make(map[string]CodexProfileSummary, len(summaries))
	for _, summary := range summaries {
		byID[summary.Profile.ID] = summary
	}
	selected := make([]CodexProfileSummary, 0, len(profileIDs))
	seen := make(map[string]struct{}, len(profileIDs))
	for _, rawID := range profileIDs {
		profileID, appErr := validateID(rawID, ErrorProfileInvalid)
		if appErr != nil {
			return nil, appErr
		}
		if _, exists := seen[profileID]; exists {
			continue
		}
		summary, exists := byID[profileID]
		if !exists {
			return nil, NewError(ErrorProfileNotFound, "Codex profile not found").WithDetail("profile_id", profileID)
		}
		seen[profileID] = struct{}{}
		selected = append(selected, summary)
	}
	return selected, nil
}

func mapCodexQuotaSnapshot(snapshot codexquota.Snapshot) CodexQuotaSnapshot {
	result := CodexQuotaSnapshot{
		FetchedAtUnixMS: snapshot.FetchedAt.UnixMilli(), PlanType: snapshot.PlanType,
		AdditionalRateLimits: make([]CodexQuotaRateLimit, 0, len(snapshot.AdditionalRateLimits)),
		RateLimitReachedType: snapshot.RateLimitReachedType,
	}
	if snapshot.RateLimit != nil {
		value := mapCodexQuotaRateLimit(*snapshot.RateLimit)
		result.RateLimit = &value
	}
	for _, limit := range snapshot.AdditionalRateLimits {
		result.AdditionalRateLimits = append(result.AdditionalRateLimits, mapCodexQuotaRateLimit(limit))
	}
	if snapshot.Credits != nil {
		result.Credits = &CodexQuotaCredits{HasCredits: snapshot.Credits.HasCredits, Unlimited: snapshot.Credits.Unlimited, Balance: snapshot.Credits.Balance}
	}
	if snapshot.SpendControl != nil {
		result.SpendControl = &CodexQuotaSpendControl{Reached: snapshot.SpendControl.Reached}
		if limit := snapshot.SpendControl.IndividualLimit; limit != nil {
			result.SpendControl.IndividualLimit = &CodexQuotaSpendControlLimit{
				Source: limit.Source, Limit: limit.Limit, Used: limit.Used, Remaining: limit.Remaining,
				UsedPercent: limit.UsedPercent, RemainingPercent: limit.RemainingPercent,
				ResetAfterSeconds: limit.ResetAfterSeconds, ResetAtUnixSeconds: limit.ResetAtUnixSeconds,
			}
		}
	}
	if snapshot.ResetCreditsAvailable != nil {
		count := *snapshot.ResetCreditsAvailable
		result.ResetCreditsAvailableCount = &count
	}
	return result
}

func mapCodexQuotaRateLimit(limit codexquota.RateLimit) CodexQuotaRateLimit {
	return CodexQuotaRateLimit{
		ID: limit.ID, Name: limit.Name, Allowed: limit.Allowed, LimitReached: limit.LimitReached,
		PrimaryWindow: mapCodexQuotaWindow(limit.PrimaryWindow), SecondaryWindow: mapCodexQuotaWindow(limit.SecondaryWindow),
	}
}

func mapCodexQuotaWindow(window *codexquota.Window) *CodexQuotaWindow {
	if window == nil {
		return nil
	}
	return &CodexQuotaWindow{
		UsedPercent: window.UsedPercent, RemainingPercent: window.RemainingPercent,
		LimitWindowSeconds: window.LimitWindowSeconds, ResetAfterSeconds: window.ResetAfterSeconds,
		ResetAtUnixSeconds: window.ResetAtUnixSeconds,
	}
}
