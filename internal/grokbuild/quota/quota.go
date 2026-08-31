package quota

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"
)

type ErrorKind string

const (
	ErrorUnavailable        ErrorKind = "unavailable"
	ErrorRuntimeUnavailable ErrorKind = "runtime_unavailable"
	ErrorUnsupported        ErrorKind = "unsupported"
	ErrorAuthRequired       ErrorKind = "auth_required"
)

type Error struct {
	Kind ErrorKind
	Err  error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	switch e.Kind {
	case ErrorRuntimeUnavailable:
		return "Grok Build could not be started"
	case ErrorUnsupported:
		return "Grok Build credits are not supported"
	case ErrorAuthRequired:
		return "Grok Build authentication is required"
	default:
		return "Grok Build credits are unavailable"
	}
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func KindOf(err error) ErrorKind {
	var quotaErr *Error
	if errors.As(err, &quotaErr) {
		return quotaErr.Kind
	}
	return ErrorUnavailable
}

type Reader interface {
	Read(context.Context, string) (Snapshot, error)
}

type Snapshot struct {
	FetchedAt             time.Time
	CreditUsagePercent    *float64
	RemainingPercent      *float64
	PeriodType            string
	PeriodStart           *time.Time
	PeriodEnd             *time.Time
	PeriodDurationSeconds *int64
	IncludedLimitCents    *int64
	IncludedUsedCents     *int64
	PrepaidBalanceCents   *int64
	OnDemandCapCents      *int64
	OnDemandUsedCents     *int64
	OnDemandEnabled       *bool
	UnifiedBillingUser    *bool
	SubscriptionTier      string
}

type billingResponse struct {
	Config           *billingConfig `json:"config"`
	OnDemandEnabled  *bool          `json:"onDemandEnabled"`
	SubscriptionTier *string        `json:"subscriptionTier"`
}

type billingConfig struct {
	CreditUsagePercent *float64     `json:"creditUsagePercent"`
	CurrentPeriod      *usagePeriod `json:"currentPeriod"`
	MonthlyLimit       *centValue   `json:"monthlyLimit"`
	Used               *centValue   `json:"used"`
	OnDemandCap        *centValue   `json:"onDemandCap"`
	OnDemandUsed       *centValue   `json:"onDemandUsed"`
	PrepaidBalance     *centValue   `json:"prepaidBalance"`
	UnifiedBillingUser *bool        `json:"isUnifiedBillingUser"`
	BillingPeriodStart *string      `json:"billingPeriodStart"`
	BillingPeriodEnd   *string      `json:"billingPeriodEnd"`
}

type usagePeriod struct {
	Type  *string `json:"type"`
	Start *string `json:"start"`
	End   *string `json:"end"`
}

type centValue struct {
	Val int64 `json:"val"`
}

func decodeBilling(raw json.RawMessage, fetchedAt time.Time) (Snapshot, error) {
	var response billingResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return Snapshot{}, &Error{Kind: ErrorUnavailable, Err: err}
	}
	if response.Config == nil {
		return Snapshot{}, &Error{Kind: ErrorUnavailable}
	}
	config := response.Config
	usedPercent := normalizedPercent(config.CreditUsagePercent)
	if usedPercent == nil && config.Used != nil && config.MonthlyLimit != nil && config.MonthlyLimit.Val > 0 {
		value := float64(config.Used.Val) / float64(config.MonthlyLimit.Val) * 100
		usedPercent = normalizedPercent(&value)
	}
	var remainingPercent *float64
	if usedPercent != nil {
		value := 100 - *usedPercent
		remainingPercent = &value
	}

	periodType := ""
	var currentStart, currentEnd *string
	if config.CurrentPeriod != nil {
		if config.CurrentPeriod.Type != nil {
			periodType = strings.TrimSpace(*config.CurrentPeriod.Type)
		}
		currentStart = config.CurrentPeriod.Start
		currentEnd = config.CurrentPeriod.End
	}
	periodStart := firstTime(currentStart, config.BillingPeriodStart)
	periodEnd := firstTime(currentEnd, config.BillingPeriodEnd)
	var periodDurationSeconds *int64
	if periodStart != nil && periodEnd != nil && periodEnd.After(*periodStart) {
		value := int64(periodEnd.Sub(*periodStart) / time.Second)
		periodDurationSeconds = &value
	}

	result := Snapshot{
		FetchedAt:             fetchedAt.UTC(),
		CreditUsagePercent:    usedPercent,
		RemainingPercent:      remainingPercent,
		PeriodType:            periodType,
		PeriodStart:           periodStart,
		PeriodEnd:             periodEnd,
		PeriodDurationSeconds: periodDurationSeconds,
		IncludedLimitCents:    cents(config.MonthlyLimit),
		IncludedUsedCents:     cents(config.Used),
		PrepaidBalanceCents:   cents(config.PrepaidBalance),
		OnDemandCapCents:      cents(config.OnDemandCap),
		OnDemandUsedCents:     cents(config.OnDemandUsed),
		OnDemandEnabled:       response.OnDemandEnabled,
		UnifiedBillingUser:    config.UnifiedBillingUser,
	}
	if response.SubscriptionTier != nil {
		result.SubscriptionTier = strings.TrimSpace(*response.SubscriptionTier)
	}
	return result, nil
}

func normalizedPercent(value *float64) *float64 {
	if value == nil || math.IsNaN(*value) || math.IsInf(*value, 0) {
		return nil
	}
	normalized := min(100, max(0, *value))
	return &normalized
}

func firstTime(values ...*string) *time.Time {
	for _, value := range values {
		if value == nil || strings.TrimSpace(*value) == "" {
			continue
		}
		parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(*value))
		if err == nil {
			utc := parsed.UTC()
			return &utc
		}
	}
	return nil
}

func cents(value *centValue) *int64 {
	if value == nil {
		return nil
	}
	result := value.Val
	return &result
}
