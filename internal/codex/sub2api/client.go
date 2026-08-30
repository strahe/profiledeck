package sub2api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
)

const (
	defaultRequestTimeout = 15 * time.Second
	maxResponseBytes      = 1024 * 1024
	maxPlanNameRunes      = 120
	maxUnitRunes          = 16
)

type ErrorKind string

const (
	ErrorUnsupported            ErrorKind = "unsupported"
	ErrorAuthenticationRequired ErrorKind = "authentication_required"
	ErrorUnavailable            ErrorKind = "unavailable"
	ErrorInvalidResponse        ErrorKind = "invalid_response"
)

type Error struct {
	Kind       ErrorKind
	StatusCode int
	Err        error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	switch e.Kind {
	case ErrorUnsupported:
		return "API service usage limits are unsupported"
	case ErrorAuthenticationRequired:
		return "API service authentication is required"
	case ErrorInvalidResponse:
		return "API service usage response is invalid"
	default:
		return "API service usage limits are unavailable"
	}
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func KindOf(err error) ErrorKind {
	var readErr *Error
	if errors.As(err, &readErr) {
		return readErr.Kind
	}
	return ErrorUnavailable
}

type KeyState string

const (
	KeyStateActive         KeyState = "active"
	KeyStateQuotaExhausted KeyState = "quota_exhausted"
	KeyStateExpired        KeyState = "expired"
	KeyStateUnknown        KeyState = "unknown"
)

type Window struct {
	ID               string
	Limit            float64
	Used             float64
	Remaining        float64
	RemainingPercent float64
	ResetAt          *time.Time
}

type Snapshot struct {
	FetchedAt time.Time
	Mode      string
	PlanName  string
	KeyState  KeyState
	Unit      string
	Unlimited bool
	Limit     *float64
	Used      *float64
	Remaining *float64
	Balance   *float64
	ExpiresAt *time.Time
	Windows   []Window
}

type Request struct {
	BaseURL string
	APIKey  string
}

type Reader interface {
	Read(context.Context, Request) (Snapshot, error)
}

type Client struct {
	httpClient *http.Client
	now        func() time.Time
}

func NewClient() *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: defaultRequestTimeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		now: time.Now,
	}
}

func ResolveEndpoint(raw string) (string, bool, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", false, errors.New("API service base URL is missing")
	}
	parsed, err := url.Parse(value)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" || parsed.Opaque != "" {
		return "", false, errors.New("API service base URL is invalid")
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return "", false, errors.New("API service base URL scheme is unsupported")
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return "", false, errors.New("API service base URL contains unsupported components")
	}
	parsed.Path = "/v1/usage"
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	return parsed.String(), parsed.Scheme == "http", nil
}

func (c *Client) Read(ctx context.Context, input Request) (Snapshot, error) {
	endpoint, _, err := ResolveEndpoint(input.BaseURL)
	if err != nil {
		return Snapshot{}, &Error{Kind: ErrorUnsupported, Err: err}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Snapshot{}, &Error{Kind: ErrorUnavailable, Err: err}
	}
	request.Header.Set("Authorization", "Bearer "+input.APIKey)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "profiledeck")

	response, err := c.httpClient.Do(request)
	if err != nil {
		return Snapshot{}, &Error{Kind: ErrorUnavailable, Err: err}
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		drainResponse(response.Body)
		return Snapshot{}, &Error{Kind: ErrorAuthenticationRequired, StatusCode: response.StatusCode}
	}
	if response.StatusCode == http.StatusNotFound {
		drainResponse(response.Body)
		return Snapshot{}, &Error{Kind: ErrorUnsupported, StatusCode: response.StatusCode}
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		drainResponse(response.Body)
		return Snapshot{}, &Error{Kind: ErrorUnavailable, StatusCode: response.StatusCode}
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return Snapshot{}, &Error{Kind: ErrorUnavailable, Err: err}
	}
	if len(body) > maxResponseBytes {
		return Snapshot{}, &Error{Kind: ErrorInvalidResponse, Err: errors.New("response exceeds size limit")}
	}
	var payload usageResponse
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&payload); err != nil {
		return Snapshot{}, &Error{Kind: ErrorInvalidResponse, Err: err}
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("response contains multiple JSON values")
		}
		return Snapshot{}, &Error{Kind: ErrorInvalidResponse, Err: err}
	}
	snapshot, err := snapshotFromResponse(payload, c.now().UTC())
	if err != nil {
		var readErr *Error
		if errors.As(err, &readErr) {
			return Snapshot{}, readErr
		}
		return Snapshot{}, &Error{Kind: ErrorInvalidResponse, Err: err}
	}
	return snapshot, nil
}

type usageResponse struct {
	Mode         string                `json:"mode"`
	IsValid      *bool                 `json:"isValid"`
	Status       string                `json:"status"`
	Quota        *quotaResponse        `json:"quota"`
	Remaining    *float64              `json:"remaining"`
	Unit         string                `json:"unit"`
	RateLimits   []rateLimitResponse   `json:"rate_limits"`
	ExpiresAt    *time.Time            `json:"expires_at"`
	PlanName     string                `json:"planName"`
	Subscription *subscriptionResponse `json:"subscription"`
	Balance      *float64              `json:"balance"`
}

type quotaResponse struct {
	Limit     *float64 `json:"limit"`
	Used      *float64 `json:"used"`
	Remaining *float64 `json:"remaining"`
	Unit      string   `json:"unit"`
}

type rateLimitResponse struct {
	Window    string     `json:"window"`
	Limit     *float64   `json:"limit"`
	Used      *float64   `json:"used"`
	Remaining *float64   `json:"remaining"`
	ResetAt   *time.Time `json:"reset_at"`
}

type subscriptionResponse struct {
	DailyUsageUSD     *float64   `json:"daily_usage_usd"`
	WeeklyUsageUSD    *float64   `json:"weekly_usage_usd"`
	MonthlyUsageUSD   *float64   `json:"monthly_usage_usd"`
	DailyLimitUSD     *float64   `json:"daily_limit_usd"`
	WeeklyLimitUSD    *float64   `json:"weekly_limit_usd"`
	MonthlyLimitUSD   *float64   `json:"monthly_limit_usd"`
	WeeklyWindowStart *time.Time `json:"weekly_window_start"`
	ExpiresAt         *time.Time `json:"expires_at"`
}

func snapshotFromResponse(payload usageResponse, fetchedAt time.Time) (Snapshot, error) {
	mode := strings.TrimSpace(payload.Mode)
	if mode != "quota_limited" && mode != "unrestricted" {
		return Snapshot{}, errors.New("mode is missing or unsupported")
	}
	if payload.IsValid == nil {
		return Snapshot{}, errors.New("isValid is missing")
	}
	if !*payload.IsValid {
		return Snapshot{}, &Error{Kind: ErrorAuthenticationRequired}
	}
	result := Snapshot{
		FetchedAt: fetchedAt,
		Mode:      mode,
		PlanName:  sanitizeText(payload.PlanName, maxPlanNameRunes),
		KeyState:  normalizeKeyState(payload.Status, mode),
		Unit:      sanitizeText(payload.Unit, maxUnitRunes),
		Windows:   []Window{},
	}

	if mode == "quota_limited" {
		if payload.Quota != nil {
			if payload.Quota.Limit == nil || payload.Quota.Used == nil || payload.Quota.Remaining == nil {
				return Snapshot{}, errors.New("quota fields are missing")
			}
			if err := validateNonNegative(*payload.Quota.Limit); err != nil {
				return Snapshot{}, errors.New("quota limit is invalid")
			}
			if err := validateNonNegative(*payload.Quota.Used); err != nil {
				return Snapshot{}, errors.New("quota used value is invalid")
			}
			if err := validateNonNegative(*payload.Quota.Remaining); err != nil {
				return Snapshot{}, errors.New("quota remaining value is invalid")
			}
			result.Limit = cloneNumber(payload.Quota.Limit)
			result.Used = cloneNumber(payload.Quota.Used)
			result.Remaining = cloneNumber(payload.Quota.Remaining)
			if unit := sanitizeText(payload.Quota.Unit, maxUnitRunes); unit != "" {
				result.Unit = unit
			}
		} else if payload.Remaining != nil {
			if err := validateNonNegative(*payload.Remaining); err != nil {
				return Snapshot{}, errors.New("remaining value is invalid")
			}
			result.Remaining = cloneNumber(payload.Remaining)
		} else {
			return Snapshot{}, errors.New("quota is missing")
		}
		for _, raw := range payload.RateLimits {
			window, include, err := rateLimitFromResponse(raw)
			if err != nil {
				return Snapshot{}, err
			}
			if include {
				result.Windows = append(result.Windows, window)
			}
		}
		result.Windows = orderWindows(result.Windows)
		result.ExpiresAt = cloneTime(payload.ExpiresAt)
		return result, nil
	}

	if payload.Subscription != nil {
		if payload.Remaining == nil {
			return Snapshot{}, errors.New("subscription remaining value is missing")
		}
		switch {
		case *payload.Remaining == -1:
			result.Unlimited = true
		case validateNonNegative(*payload.Remaining) == nil:
			result.Remaining = cloneNumber(payload.Remaining)
		default:
			return Snapshot{}, errors.New("subscription remaining value is invalid")
		}
		for _, input := range []struct {
			id    string
			limit *float64
			used  *float64
		}{
			{id: "daily", limit: payload.Subscription.DailyLimitUSD, used: payload.Subscription.DailyUsageUSD},
			{id: "weekly", limit: payload.Subscription.WeeklyLimitUSD, used: payload.Subscription.WeeklyUsageUSD},
			{id: "monthly", limit: payload.Subscription.MonthlyLimitUSD, used: payload.Subscription.MonthlyUsageUSD},
		} {
			window, include, err := subscriptionWindow(input.id, input.limit, input.used)
			if err != nil {
				return Snapshot{}, err
			}
			if include {
				result.Windows = append(result.Windows, window)
			}
		}
		result.ExpiresAt = cloneTime(payload.Subscription.ExpiresAt)
		return result, nil
	}

	if payload.Balance != nil {
		if !isFinite(*payload.Balance) {
			return Snapshot{}, errors.New("wallet balance is invalid")
		}
		result.Balance = cloneNumber(payload.Balance)
		if payload.Remaining != nil {
			if !isFinite(*payload.Remaining) {
				return Snapshot{}, errors.New("wallet remaining value is invalid")
			}
			if *payload.Remaining >= 0 {
				result.Remaining = cloneNumber(payload.Remaining)
			}
		}
		return result, nil
	}

	if payload.Remaining != nil {
		if *payload.Remaining == -1 {
			result.Unlimited = true
		} else if validateNonNegative(*payload.Remaining) == nil {
			result.Remaining = cloneNumber(payload.Remaining)
		} else {
			return Snapshot{}, errors.New("remaining value is invalid")
		}
		return result, nil
	}
	return Snapshot{}, errors.New("unrestricted quota is missing")
}

func rateLimitFromResponse(raw rateLimitResponse) (Window, bool, error) {
	id := strings.TrimSpace(raw.Window)
	if id != "5h" && id != "1d" && id != "7d" {
		return Window{}, false, nil
	}
	if raw.Limit == nil || raw.Used == nil || raw.Remaining == nil {
		return Window{}, false, errors.New("rate limit fields are missing")
	}
	if validateNonNegative(*raw.Limit) != nil || validateNonNegative(*raw.Used) != nil || validateNonNegative(*raw.Remaining) != nil {
		return Window{}, false, errors.New("rate limit values are invalid")
	}
	return Window{
		ID: id, Limit: *raw.Limit, Used: *raw.Used, Remaining: *raw.Remaining,
		RemainingPercent: percent(*raw.Remaining, *raw.Limit), ResetAt: cloneTime(raw.ResetAt),
	}, true, nil
}

func subscriptionWindow(id string, limit, used *float64) (Window, bool, error) {
	if limit == nil {
		return Window{}, false, nil
	}
	if validateNonNegative(*limit) != nil || used == nil || validateNonNegative(*used) != nil {
		return Window{}, false, errors.New("subscription limit values are invalid")
	}
	remaining := math.Max(0, *limit-*used)
	return Window{ID: id, Limit: *limit, Used: *used, Remaining: remaining, RemainingPercent: percent(remaining, *limit)}, true, nil
}

func orderWindows(values []Window) []Window {
	result := make([]Window, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, id := range []string{"5h", "1d", "7d", "daily", "weekly", "monthly"} {
		for _, value := range values {
			if value.ID == id {
				if _, exists := seen[id]; exists {
					continue
				}
				seen[id] = struct{}{}
				result = append(result, value)
			}
		}
	}
	return result
}

func normalizeKeyState(raw, mode string) KeyState {
	switch strings.TrimSpace(raw) {
	case "active":
		return KeyStateActive
	case "quota_exhausted":
		return KeyStateQuotaExhausted
	case "expired":
		return KeyStateExpired
	default:
		if mode == "unrestricted" && strings.TrimSpace(raw) == "" {
			return KeyStateActive
		}
		return KeyStateUnknown
	}
}

func sanitizeText(raw string, maxRunes int) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	runes := make([]rune, 0, min(len([]rune(value)), maxRunes))
	for _, current := range value {
		if unicode.IsControl(current) {
			continue
		}
		runes = append(runes, current)
		if len(runes) == maxRunes {
			break
		}
	}
	return strings.TrimSpace(string(runes))
}

func validateNonNegative(value float64) error {
	if !isFinite(value) || value < 0 {
		return errors.New("value must be non-negative")
	}
	return nil
}

func isFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func percent(remaining, limit float64) float64 {
	if limit <= 0 {
		return 0
	}
	return math.Max(0, math.Min(100, remaining/limit*100))
}

func cloneNumber(value *float64) *float64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := value.UTC()
	return &cloned
}

func drainResponse(body io.Reader) {
	_, _ = io.Copy(io.Discard, io.LimitReader(body, 64*1024))
}
