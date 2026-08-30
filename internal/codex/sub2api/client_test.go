package sub2api

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (run roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return run(request)
}

func TestResolveEndpointUsesFixedUsagePath(t *testing.T) {
	tests := []struct {
		name      string
		baseURL   string
		want      string
		insecure  bool
		wantError bool
	}{
		{name: "https path", baseURL: " https://api.example.test/openai/v1 ", want: "https://api.example.test/v1/usage"},
		{name: "http", baseURL: "http://127.0.0.1:8080/v1", want: "http://127.0.0.1:8080/v1/usage", insecure: true},
		{name: "missing", wantError: true},
		{name: "relative", baseURL: "/v1", wantError: true},
		{name: "userinfo", baseURL: "https://user:secret@example.test/v1", wantError: true},
		{name: "query removed", baseURL: "https://example.test/v1?format=legacy", want: "https://example.test/v1/usage"},
		{name: "fragment", baseURL: "https://example.test/v1#usage", wantError: true},
		{name: "unsupported scheme", baseURL: "file:///tmp/sub2api", wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			endpoint, insecure, err := ResolveEndpoint(test.baseURL)
			if test.wantError {
				if err == nil {
					t.Fatalf("ResolveEndpoint(%q) succeeded with %q", test.baseURL, endpoint)
				}
				return
			}
			if err != nil || endpoint != test.want || insecure != test.insecure {
				t.Fatalf("ResolveEndpoint(%q) = %q, %v, %v", test.baseURL, endpoint, insecure, err)
			}
		})
	}
}

func TestClientReadsQuotaLimitedOnce(t *testing.T) {
	var calls atomic.Int32
	client := NewClient()
	client.now = func() time.Time { return time.Date(2026, 8, 30, 1, 2, 3, 0, time.UTC) }
	client.httpClient = &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		if request.Method != http.MethodGet || request.URL.String() != "https://api.example.test/v1/usage" {
			t.Fatalf("unexpected request %s %s", request.Method, request.URL)
		}
		if value := request.Header.Get("Authorization"); value != "Bearer synthetic-api-key" {
			t.Fatalf("Authorization = %q", value)
		}
		return jsonResponse(http.StatusOK, `{
			"mode":"quota_limited","isValid":true,"status":"expired",
			"quota":{"limit":100,"used":25,"remaining":75,"unit":"USD"},
			"rate_limits":[
				{"window":"7d","limit":700,"used":210,"remaining":490},
				{"window":"5h","limit":50,"used":10,"remaining":40,"reset_at":"2026-08-30T05:00:00Z"},
				{"window":"future","limit":1,"used":0,"remaining":1},
				{"window":"5h","limit":999,"used":0,"remaining":999}
			],
			"expires_at":"2026-08-31T00:00:00Z",
			"usage":{"total":{"requests":999}},"daily_usage":[{"cost":1}],"model_stats":{"secret":"ignored"}
		}`), nil
	})}

	snapshot, err := client.Read(context.Background(), Request{BaseURL: "https://api.example.test/custom/v1", APIKey: "synthetic-api-key"})
	if err != nil {
		t.Fatalf("Read returned %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("request calls = %d, want 1", calls.Load())
	}
	if snapshot.Mode != "quota_limited" || snapshot.KeyState != KeyStateExpired || snapshot.Limit == nil || *snapshot.Limit != 100 || snapshot.Remaining == nil || *snapshot.Remaining != 75 {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
	if len(snapshot.Windows) != 2 || snapshot.Windows[0].ID != "5h" || snapshot.Windows[0].RemainingPercent != 80 || snapshot.Windows[1].ID != "7d" {
		t.Fatalf("unexpected windows: %#v", snapshot.Windows)
	}
	if snapshot.ExpiresAt == nil || snapshot.ExpiresAt.Unix() != time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC).Unix() {
		t.Fatalf("unexpected expiry: %#v", snapshot.ExpiresAt)
	}
}

func TestSnapshotFromUnrestrictedSubscription(t *testing.T) {
	valid := true
	unlimited := -1.0
	dailyLimit, dailyUsed := 20.0, 5.0
	weeklyLimit, weeklyUsed := 100.0, 125.0
	payload := usageResponse{
		Mode: "unrestricted", IsValid: &valid, PlanName: "  Team\nPlan  ", Unit: " USD ", Remaining: &unlimited,
		Subscription: &subscriptionResponse{
			DailyLimitUSD: &dailyLimit, DailyUsageUSD: &dailyUsed,
			WeeklyLimitUSD: &weeklyLimit, WeeklyUsageUSD: &weeklyUsed,
		},
	}
	snapshot, err := snapshotFromResponse(payload, time.Unix(100, 0))
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Unlimited || snapshot.Remaining != nil || snapshot.PlanName != "TeamPlan" || snapshot.KeyState != KeyStateActive {
		t.Fatalf("unexpected subscription snapshot: %#v", snapshot)
	}
	if len(snapshot.Windows) != 2 || snapshot.Windows[0].ID != "daily" || snapshot.Windows[0].RemainingPercent != 75 || snapshot.Windows[1].Remaining != 0 {
		t.Fatalf("unexpected subscription windows: %#v", snapshot.Windows)
	}
}

func TestSnapshotFromWalletAllowsNegativeBalanceAndMissingUnit(t *testing.T) {
	valid := true
	balance := -12.5
	snapshot, err := snapshotFromResponse(usageResponse{
		Mode: "unrestricted", IsValid: &valid, PlanName: "Wallet", Balance: &balance, Remaining: &balance,
	}, time.Unix(100, 0))
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Balance == nil || *snapshot.Balance != balance || snapshot.Remaining != nil || snapshot.Unit != "" {
		t.Fatalf("unexpected wallet snapshot: %#v", snapshot)
	}
}

func TestSnapshotRejectsMissingUnrestrictedValues(t *testing.T) {
	valid := true
	_, err := snapshotFromResponse(usageResponse{Mode: "unrestricted", IsValid: &valid}, time.Unix(100, 0))
	if err == nil {
		t.Fatal("expected missing unrestricted values to fail")
	}
}

func TestClientMapsFailuresWithoutResponseDetails(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		kind   ErrorKind
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, body: `{"secret":"do-not-return"}`, kind: ErrorAuthenticationRequired},
		{name: "forbidden", status: http.StatusForbidden, body: `{}`, kind: ErrorAuthenticationRequired},
		{name: "not found", status: http.StatusNotFound, body: `{}`, kind: ErrorUnsupported},
		{name: "remote", status: http.StatusTooManyRequests, body: `{}`, kind: ErrorUnavailable},
		{name: "malformed", status: http.StatusOK, body: `{`, kind: ErrorInvalidResponse},
		{name: "invalid key", status: http.StatusOK, body: `{"mode":"quota_limited","isValid":false}`, kind: ErrorAuthenticationRequired},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := NewClient()
			client.httpClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				return jsonResponse(test.status, test.body), nil
			})}
			_, err := client.Read(context.Background(), Request{BaseURL: "https://api.example.test", APIKey: "secret-key"})
			if KindOf(err) != test.kind {
				t.Fatalf("error = %v, kind = %q", err, KindOf(err))
			}
			if err != nil && (strings.Contains(err.Error(), "secret-key") || strings.Contains(err.Error(), "do-not-return")) {
				t.Fatalf("error exposed secret details: %v", err)
			}
		})
	}
}

func TestClientRejectsOversizedResponseAndRedirect(t *testing.T) {
	t.Run("oversized", func(t *testing.T) {
		client := NewClient()
		client.httpClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, strings.Repeat(" ", maxResponseBytes+1)), nil
		})}
		_, err := client.Read(context.Background(), Request{BaseURL: "https://api.example.test", APIKey: "key"})
		if KindOf(err) != ErrorInvalidResponse {
			t.Fatalf("oversized error = %v", err)
		}
	})

	var destinationCalls atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		destinationCalls.Add(1)
	}))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, destination.URL, http.StatusFound)
	}))
	defer source.Close()
	client := NewClient()
	_, err := client.Read(context.Background(), Request{BaseURL: source.URL, APIKey: "key"})
	if KindOf(err) != ErrorUnavailable || destinationCalls.Load() != 0 {
		t.Fatalf("redirect error = %v, destination calls = %d", err, destinationCalls.Load())
	}
}

func TestSnapshotRejectsNonFiniteAndNegativeLimitedValues(t *testing.T) {
	valid := true
	limit, used, remaining := 100.0, math.Inf(1), 50.0
	_, err := snapshotFromResponse(usageResponse{
		Mode: "quota_limited", IsValid: &valid,
		Quota: &quotaResponse{Limit: &limit, Used: &used, Remaining: &remaining},
	}, time.Unix(100, 0))
	if err == nil {
		t.Fatal("expected non-finite usage to fail")
	}
	used = -1
	_, err = snapshotFromResponse(usageResponse{
		Mode: "quota_limited", IsValid: &valid,
		Quota: &quotaResponse{Limit: &limit, Used: &used, Remaining: &remaining},
	}, time.Unix(100, 0))
	if err == nil {
		t.Fatal("expected negative usage to fail")
	}
	used = math.NaN()
	_, err = snapshotFromResponse(usageResponse{
		Mode: "quota_limited", IsValid: &valid,
		Quota: &quotaResponse{Limit: &limit, Used: &used, Remaining: &remaining},
	}, time.Unix(100, 0))
	if err == nil {
		t.Fatal("expected NaN usage to fail")
	}
}

func TestSnapshotNormalizesQuotaExhaustedState(t *testing.T) {
	valid := true
	zero, limit := 0.0, 100.0
	snapshot, err := snapshotFromResponse(usageResponse{
		Mode: "quota_limited", IsValid: &valid, Status: "quota_exhausted",
		Quota: &quotaResponse{Limit: &limit, Used: &limit, Remaining: &zero},
	}, time.Unix(100, 0))
	if err != nil || snapshot.KeyState != KeyStateQuotaExhausted || snapshot.Remaining == nil || *snapshot.Remaining != 0 {
		t.Fatalf("unexpected exhausted snapshot: %#v, %v", snapshot, err)
	}
}

func TestSnapshotAcceptsZeroLimitsAndRejectsMissingQuota(t *testing.T) {
	valid := true
	zero := 0.0
	snapshot, err := snapshotFromResponse(usageResponse{
		Mode: "quota_limited", IsValid: &valid,
		Quota:      &quotaResponse{Limit: &zero, Used: &zero, Remaining: &zero},
		RateLimits: []rateLimitResponse{{Window: "5h", Limit: &zero, Used: &zero, Remaining: &zero}},
	}, time.Unix(100, 0))
	if err != nil || snapshot.Limit == nil || *snapshot.Limit != 0 || len(snapshot.Windows) != 1 || snapshot.Windows[0].RemainingPercent != 0 {
		t.Fatalf("expected zero-limit exhausted snapshot, got %#v, %v", snapshot, err)
	}

	_, err = snapshotFromResponse(usageResponse{Mode: "quota_limited", IsValid: &valid}, time.Unix(100, 0))
	if err == nil {
		t.Fatal("expected missing quota to fail")
	}
}

func TestClientMapsTransportFailure(t *testing.T) {
	client := NewClient()
	client.httpClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("offline")
	})}
	_, err := client.Read(context.Background(), Request{BaseURL: "https://api.example.test", APIKey: "key"})
	if KindOf(err) != ErrorUnavailable {
		t.Fatalf("transport error = %v", err)
	}
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
