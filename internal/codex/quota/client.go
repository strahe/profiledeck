package quota

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	DefaultEndpoint       = "https://chatgpt.com/backend-api/wham/usage"
	defaultRequestTimeout = 15 * time.Second
	maxResponseBytes      = 1024 * 1024
)

type ErrorKind string

const (
	ErrorAuthenticationRequired ErrorKind = "authentication_required"
	ErrorRemoteUnavailable      ErrorKind = "remote_unavailable"
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
	case ErrorAuthenticationRequired:
		return "Codex quota authentication is required"
	case ErrorInvalidResponse:
		return "Codex quota response is invalid"
	default:
		return "Codex quota service is unavailable"
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
	return ErrorRemoteUnavailable
}

type Credentials struct {
	AccessToken string
	AccountID   string
	FedRAMP     bool
}

type Window struct {
	UsedPercent        float64
	RemainingPercent   float64
	LimitWindowSeconds int64
	ResetAfterSeconds  int64
	ResetAtUnixSeconds int64
}

type RateLimit struct {
	ID              string
	Name            string
	Allowed         bool
	LimitReached    bool
	PrimaryWindow   *Window
	SecondaryWindow *Window
}

type Credits struct {
	HasCredits bool
	Unlimited  bool
	Balance    *string
}

type SpendControlLimit struct {
	Source             string
	Limit              string
	Used               string
	Remaining          string
	UsedPercent        float64
	RemainingPercent   float64
	ResetAfterSeconds  int64
	ResetAtUnixSeconds int64
}

type SpendControl struct {
	Reached         bool
	IndividualLimit *SpendControlLimit
}

type Snapshot struct {
	FetchedAt             time.Time
	PlanType              string
	RateLimit             *RateLimit
	AdditionalRateLimits  []RateLimit
	Credits               *Credits
	SpendControl          *SpendControl
	RateLimitReachedType  string
	ResetCreditsAvailable *int64
}

type Reader interface {
	Read(context.Context, Credentials) (Snapshot, error)
}

type Client struct {
	httpClient *http.Client
	endpoint   string
	now        func() time.Time
}

func NewClient() *Client {
	// Keep the endpoint fixed: a profile-controlled model base URL must never
	// receive the hidden ChatGPT bearer token used for quota lookup.
	return &Client{
		httpClient: &http.Client{
			Timeout: defaultRequestTimeout,
			// Refuse redirects so the bearer and account headers stay on the
			// exact fixed endpoint, including redirects to related hosts.
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		endpoint: DefaultEndpoint,
		now:      time.Now,
	}
}

func (c *Client) Read(ctx context.Context, credentials Credentials) (Snapshot, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint, nil)
	if err != nil {
		return Snapshot{}, &Error{Kind: ErrorRemoteUnavailable, Err: err}
	}
	request.Header.Set("Authorization", "Bearer "+credentials.AccessToken)
	request.Header.Set("ChatGPT-Account-Id", credentials.AccountID)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "profiledeck")
	if credentials.FedRAMP {
		request.Header.Set("X-OpenAI-Fedramp", "true")
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return Snapshot{}, &Error{Kind: ErrorRemoteUnavailable, Err: err}
	}
	defer response.Body.Close()

	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		drainResponse(response.Body)
		return Snapshot{}, &Error{Kind: ErrorAuthenticationRequired, StatusCode: response.StatusCode}
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		drainResponse(response.Body)
		return Snapshot{}, &Error{Kind: ErrorRemoteUnavailable, StatusCode: response.StatusCode}
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return Snapshot{}, &Error{Kind: ErrorRemoteUnavailable, Err: err}
	}
	if len(body) > maxResponseBytes {
		return Snapshot{}, &Error{Kind: ErrorInvalidResponse, Err: errors.New("response exceeds size limit")}
	}
	var payload usageResponse
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return Snapshot{}, &Error{Kind: ErrorInvalidResponse, Err: err}
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			err = errors.New("response contains multiple JSON values")
		}
		return Snapshot{}, &Error{Kind: ErrorInvalidResponse, Err: err}
	}

	fetchedAt := c.now().UTC()
	snapshot, err := snapshotFromResponse(payload, fetchedAt)
	if err != nil {
		return Snapshot{}, &Error{Kind: ErrorInvalidResponse, Err: err}
	}
	return snapshot, nil
}

func drainResponse(body io.Reader) {
	_, _ = io.Copy(io.Discard, io.LimitReader(body, 64*1024))
}

type usageResponse struct {
	PlanType              string                        `json:"plan_type"`
	RateLimit             *rateLimitResponse            `json:"rate_limit"`
	Credits               *creditsResponse              `json:"credits"`
	SpendControl          *spendControlResponse         `json:"spend_control"`
	AdditionalRateLimits  []additionalRateLimitResponse `json:"additional_rate_limits"`
	RateLimitReachedType  *rateLimitReachedResponse     `json:"rate_limit_reached_type"`
	RateLimitResetCredits *resetCreditsResponse         `json:"rate_limit_reset_credits"`
}

type rateLimitResponse struct {
	Allowed         bool            `json:"allowed"`
	LimitReached    bool            `json:"limit_reached"`
	PrimaryWindow   *windowResponse `json:"primary_window"`
	SecondaryWindow *windowResponse `json:"secondary_window"`
}

type windowResponse struct {
	UsedPercent        *float64 `json:"used_percent"`
	LimitWindowSeconds *int64   `json:"limit_window_seconds"`
	ResetAfterSeconds  *int64   `json:"reset_after_seconds"`
	ResetAt            *int64   `json:"reset_at"`
}

type creditsResponse struct {
	HasCredits bool    `json:"has_credits"`
	Unlimited  bool    `json:"unlimited"`
	Balance    *string `json:"balance"`
}

type spendControlResponse struct {
	Reached         bool                       `json:"reached"`
	IndividualLimit *spendControlLimitResponse `json:"individual_limit"`
}

type spendControlLimitResponse struct {
	Source            *string  `json:"source"`
	Limit             string   `json:"limit"`
	Used              string   `json:"used"`
	Remaining         string   `json:"remaining"`
	UsedPercent       *float64 `json:"used_percent"`
	RemainingPercent  *float64 `json:"remaining_percent"`
	ResetAfterSeconds int64    `json:"reset_after_seconds"`
	ResetAt           int64    `json:"reset_at"`
}

type additionalRateLimitResponse struct {
	LimitName      string             `json:"limit_name"`
	MeteredFeature string             `json:"metered_feature"`
	RateLimit      *rateLimitResponse `json:"rate_limit"`
}

type rateLimitReachedResponse struct {
	Type string `json:"type"`
}

type resetCreditsResponse struct {
	AvailableCount int64 `json:"available_count"`
}

func snapshotFromResponse(payload usageResponse, fetchedAt time.Time) (Snapshot, error) {
	planType := strings.TrimSpace(payload.PlanType)
	if planType == "" {
		return Snapshot{}, errors.New("plan type is missing")
	}
	result := Snapshot{
		FetchedAt:            fetchedAt,
		PlanType:             planType,
		AdditionalRateLimits: []RateLimit{},
	}
	if payload.RateLimit != nil {
		limit, err := rateLimitFromResponse("codex", "", payload.RateLimit, fetchedAt)
		if err != nil {
			return Snapshot{}, err
		}
		result.RateLimit = &limit
	}
	for _, additional := range payload.AdditionalRateLimits {
		limit, err := rateLimitFromResponse(additional.MeteredFeature, additional.LimitName, additional.RateLimit, fetchedAt)
		if err != nil {
			return Snapshot{}, err
		}
		result.AdditionalRateLimits = append(result.AdditionalRateLimits, limit)
	}
	if payload.Credits != nil {
		result.Credits = &Credits{
			HasCredits: payload.Credits.HasCredits,
			Unlimited:  payload.Credits.Unlimited,
			Balance:    payload.Credits.Balance,
		}
	}
	if payload.SpendControl != nil {
		result.SpendControl = &SpendControl{Reached: payload.SpendControl.Reached}
		if value := payload.SpendControl.IndividualLimit; value != nil {
			usedPercent := numberOrZero(value.UsedPercent)
			remainingPercent := clampPercent(100 - usedPercent)
			if value.RemainingPercent != nil {
				remainingPercent = clampPercent(*value.RemainingPercent)
			}
			source := ""
			if value.Source != nil {
				source = *value.Source
			}
			result.SpendControl.IndividualLimit = &SpendControlLimit{
				Source: source, Limit: value.Limit, Used: value.Used, Remaining: value.Remaining,
				UsedPercent: clampPercent(usedPercent), RemainingPercent: remainingPercent,
				ResetAfterSeconds: value.ResetAfterSeconds, ResetAtUnixSeconds: value.ResetAt,
			}
		}
	}
	if payload.RateLimitReachedType != nil {
		result.RateLimitReachedType = payload.RateLimitReachedType.Type
	}
	if payload.RateLimitResetCredits != nil {
		count := payload.RateLimitResetCredits.AvailableCount
		result.ResetCreditsAvailable = &count
	}
	return result, nil
}

func rateLimitFromResponse(id string, name string, value *rateLimitResponse, fetchedAt time.Time) (RateLimit, error) {
	result := RateLimit{ID: id, Name: name}
	if value == nil {
		return result, nil
	}
	result.Allowed = value.Allowed
	result.LimitReached = value.LimitReached
	var err error
	result.PrimaryWindow, err = windowFromResponse(value.PrimaryWindow, fetchedAt)
	if err != nil {
		return RateLimit{}, fmt.Errorf("primary window: %w", err)
	}
	result.SecondaryWindow, err = windowFromResponse(value.SecondaryWindow, fetchedAt)
	if err != nil {
		return RateLimit{}, fmt.Errorf("secondary window: %w", err)
	}
	return result, nil
}

func windowFromResponse(value *windowResponse, fetchedAt time.Time) (*Window, error) {
	if value == nil {
		return nil, nil
	}
	if value.UsedPercent == nil || value.LimitWindowSeconds == nil || value.ResetAt == nil {
		return nil, errors.New("required fields are missing")
	}
	if *value.LimitWindowSeconds <= 0 || *value.ResetAt <= 0 {
		return nil, errors.New("window duration and reset timestamp must be positive")
	}
	resetAfter := *value.ResetAt - fetchedAt.Unix()
	if value.ResetAfterSeconds != nil {
		resetAfter = *value.ResetAfterSeconds
	}
	if resetAfter < 0 {
		resetAfter = 0
	}
	usedPercent := clampPercent(*value.UsedPercent)
	return &Window{
		UsedPercent: usedPercent, RemainingPercent: clampPercent(100 - usedPercent),
		LimitWindowSeconds: *value.LimitWindowSeconds, ResetAfterSeconds: resetAfter,
		ResetAtUnixSeconds: *value.ResetAt,
	}, nil
}

func numberOrZero(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}

func clampPercent(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}
