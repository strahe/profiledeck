// Package quota reads current Antigravity quota summaries without persisting them.
package quota

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
)

const (
	defaultTimeout   = 15 * time.Second
	maxResponseBytes = 1024 * 1024
	loadEndpoint     = "https://daily-cloudcode-pa.sandbox.googleapis.com/v1internal:loadCodeAssist"
)

var summaryEndpoints = []string{
	"https://daily-cloudcode-pa.sandbox.googleapis.com/v1internal:retrieveUserQuotaSummary",
	"https://daily-cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary",
	"https://cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary",
}

type ErrorKind string

const (
	ErrorAuthRequired ErrorKind = "auth_required"
	ErrorUnavailable  ErrorKind = "unavailable"
)

// Error intentionally omits upstream response and transport details because
// those values can contain credential- or account-specific information.
type Error struct{ Kind ErrorKind }

func (err *Error) Error() string {
	if err != nil && err.Kind == ErrorAuthRequired {
		return "Antigravity authentication is required"
	}
	return "Antigravity quota is unavailable"
}

func KindOf(err error) ErrorKind {
	var quotaErr *Error
	if errors.As(err, &quotaErr) {
		return quotaErr.Kind
	}
	return ErrorUnavailable
}

type Snapshot struct {
	FetchedAt time.Time
	Groups    []Group
}

type Group struct {
	DisplayName string
	Buckets     []Bucket
}

type Bucket struct {
	ID                 string
	Window             string
	RemainingPercent   float64
	ResetAtUnixSeconds int64
}

type Reader interface {
	Read(context.Context, string) (Snapshot, error)
}

type userAgentProvider interface {
	UserAgent(context.Context) (string, error)
}

type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type Client struct {
	http             httpDoer
	userAgent        userAgentProvider
	loadEndpoint     string
	summaryEndpoints []string
	timeout          time.Duration
	maxResponseBytes int64
	now              func() time.Time
}

func NewClient() *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableCompression = true
	return &Client{
		http: &http.Client{
			Transport: transport,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		userAgent: NewInstallationResolver(), loadEndpoint: loadEndpoint,
		summaryEndpoints: append([]string(nil), summaryEndpoints...),
		timeout:          defaultTimeout, maxResponseBytes: maxResponseBytes, now: time.Now,
	}
}

func (client *Client) Read(ctx context.Context, accessToken string) (Snapshot, error) {
	accessToken = strings.TrimSpace(accessToken)
	if accessToken == "" {
		return Snapshot{}, &Error{Kind: ErrorAuthRequired}
	}
	if client == nil || client.http == nil || client.userAgent == nil {
		return Snapshot{}, &Error{Kind: ErrorUnavailable}
	}
	timeout := client.timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	readCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	userAgent, err := client.userAgent.UserAgent(readCtx)
	if err != nil || strings.TrimSpace(userAgent) == "" {
		return Snapshot{}, &Error{Kind: ErrorUnavailable}
	}
	projectID, err := client.readProject(readCtx, accessToken, userAgent)
	if err != nil {
		return Snapshot{}, err
	}
	groups, err := client.readSummary(readCtx, accessToken, userAgent, projectID)
	if err != nil {
		return Snapshot{}, err
	}
	now := time.Now
	if client.now != nil {
		now = client.now
	}
	return Snapshot{FetchedAt: now().UTC(), Groups: groups}, nil
}

func (client *Client) readProject(ctx context.Context, accessToken, userAgent string) (string, error) {
	raw, status, transportFailed, err := client.postJSON(
		ctx, client.loadEndpoint, accessToken, userAgent,
		[]byte(`{"metadata":{"ideType":"ANTIGRAVITY"}}`),
	)
	if transportFailed || err != nil {
		return "", &Error{Kind: ErrorUnavailable}
	}
	if status == http.StatusUnauthorized {
		return "", &Error{Kind: ErrorAuthRequired}
	}
	if status != http.StatusOK {
		return "", &Error{Kind: ErrorUnavailable}
	}
	var response struct {
		ProjectID *string `json:"cloudaicompanionProject"`
	}
	if err := decodeJSON(raw, &response); err != nil || response.ProjectID == nil {
		return "", &Error{Kind: ErrorUnavailable}
	}
	projectID := strings.TrimSpace(*response.ProjectID)
	if projectID == "" {
		return "", &Error{Kind: ErrorUnavailable}
	}
	return projectID, nil
}

func (client *Client) readSummary(ctx context.Context, accessToken, userAgent, projectID string) ([]Group, error) {
	body, err := json.Marshal(struct {
		Project string `json:"project"`
	}{Project: projectID})
	if err != nil {
		return nil, &Error{Kind: ErrorUnavailable}
	}
	for _, endpoint := range client.summaryEndpoints {
		raw, status, transportFailed, requestErr := client.postJSON(ctx, endpoint, accessToken, userAgent, body)
		if transportFailed {
			continue
		}
		if requestErr != nil {
			return nil, &Error{Kind: ErrorUnavailable}
		}
		switch {
		case status == http.StatusOK:
			groups, err := decodeSummary(raw)
			if err != nil {
				return nil, &Error{Kind: ErrorUnavailable}
			}
			return groups, nil
		case status == http.StatusUnauthorized:
			return nil, &Error{Kind: ErrorAuthRequired}
		case status == http.StatusTooManyRequests || status >= 500 && status <= 599:
			continue
		default:
			return nil, &Error{Kind: ErrorUnavailable}
		}
	}
	return nil, &Error{Kind: ErrorUnavailable}
}

func (client *Client) postJSON(
	ctx context.Context,
	endpoint, accessToken, userAgent string,
	body []byte,
) (raw []byte, status int, transportFailed bool, err error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, 0, false, err
	}
	// Keep application identity limited to the three fields used by Antigravity;
	// do not add product, machine, or session identifiers here.
	request.Header.Set("Authorization", "Bearer "+accessToken)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", userAgent)
	response, err := client.http.Do(request)
	if err != nil {
		return nil, 0, true, nil
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, response.StatusCode, false, nil
	}
	limit := client.maxResponseBytes
	if limit <= 0 {
		limit = maxResponseBytes
	}
	raw, err = io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return nil, response.StatusCode, false, errors.New("quota response is invalid")
	}
	return raw, response.StatusCode, false, nil
}

type rawSummary struct {
	Groups *[]rawGroup `json:"groups"`
}

type rawGroup struct {
	DisplayName *string            `json:"displayName"`
	Buckets     *[]json.RawMessage `json:"buckets"`
}

type rawBucket struct {
	ID                *string  `json:"bucketId"`
	RemainingFraction *float64 `json:"remainingFraction"`
	ResetTime         *string  `json:"resetTime"`
}

func decodeSummary(raw []byte) ([]Group, error) {
	var response rawSummary
	if err := decodeJSON(raw, &response); err != nil || response.Groups == nil || len(*response.Groups) == 0 {
		return nil, errors.New("quota summary is invalid")
	}
	groups := make([]Group, 0, len(*response.Groups))
	for _, upstreamGroup := range *response.Groups {
		if upstreamGroup.DisplayName == nil || strings.TrimSpace(*upstreamGroup.DisplayName) == "" || upstreamGroup.Buckets == nil {
			return nil, errors.New("quota group is invalid")
		}
		group := Group{DisplayName: strings.TrimSpace(*upstreamGroup.DisplayName), Buckets: []Bucket{}}
		for _, rawBucket := range *upstreamGroup.Buckets {
			upstreamBucket, window, recognized, err := decodeRecognizedBucket(rawBucket)
			if err != nil {
				return nil, err
			}
			if !recognized {
				continue
			}
			if upstreamBucket.ID == nil || strings.TrimSpace(*upstreamBucket.ID) == "" ||
				upstreamBucket.RemainingFraction == nil || math.IsNaN(*upstreamBucket.RemainingFraction) ||
				math.IsInf(*upstreamBucket.RemainingFraction, 0) || *upstreamBucket.RemainingFraction < 0 ||
				*upstreamBucket.RemainingFraction > 1 || upstreamBucket.ResetTime == nil {
				return nil, errors.New("quota bucket is invalid")
			}
			resetAt, err := time.Parse(time.RFC3339, strings.TrimSpace(*upstreamBucket.ResetTime))
			if err != nil {
				return nil, errors.New("quota bucket is invalid")
			}
			group.Buckets = append(group.Buckets, Bucket{
				ID: strings.TrimSpace(*upstreamBucket.ID), Window: window,
				RemainingPercent:   *upstreamBucket.RemainingFraction * 100,
				ResetAtUnixSeconds: resetAt.Unix(),
			})
		}
		if len(group.Buckets) == 0 {
			continue
		}
		sort.SliceStable(group.Buckets, func(i, j int) bool {
			return windowOrder(group.Buckets[i].Window) < windowOrder(group.Buckets[j].Window)
		})
		groups = append(groups, group)
	}
	return groups, nil
}

func decodeRecognizedBucket(raw json.RawMessage) (rawBucket, string, bool, error) {
	var discriminator struct {
		Window json.RawMessage `json:"window"`
	}
	if err := json.Unmarshal(raw, &discriminator); err != nil {
		return rawBucket{}, "", false, errors.New("quota bucket is invalid")
	}
	var window string
	if len(discriminator.Window) == 0 || json.Unmarshal(discriminator.Window, &window) != nil {
		return rawBucket{}, "", false, nil
	}
	window = strings.TrimSpace(window)
	if window != "5h" && window != "weekly" {
		return rawBucket{}, "", false, nil
	}
	var bucket rawBucket
	if err := json.Unmarshal(raw, &bucket); err != nil {
		return rawBucket{}, "", false, errors.New("quota bucket is invalid")
	}
	return bucket, window, true, nil
}

func decodeJSON(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("json contains trailing data")
	}
	return nil
}

func windowOrder(window string) int {
	if window == "5h" {
		return 0
	}
	return 1
}
