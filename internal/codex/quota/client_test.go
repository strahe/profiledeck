package quota

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClientReadsCodexQuotaAndMapsRemainingPercent(t *testing.T) {
	const accessToken = "raw-access-token"
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/wham/usage" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+accessToken {
			t.Fatalf("unexpected authorization header: %q", got)
		}
		if got := r.Header.Get("ChatGPT-Account-Id"); got != "workspace-1" {
			t.Fatalf("unexpected account header: %q", got)
		}
		if got := r.Header.Get("X-OpenAI-Fedramp"); got != "true" {
			t.Fatalf("unexpected FedRAMP header: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"plan_type":"plus",
			"rate_limit":{"allowed":true,"limit_reached":false,
				"primary_window":{"used_percent":22,"limit_window_seconds":18000,"reset_after_seconds":3600,"reset_at":1780003600},
				"secondary_window":{"used_percent":63,"limit_window_seconds":604800,"reset_after_seconds":7200,"reset_at":1780007200}},
			"credits":{"has_credits":true,"unlimited":false,"balance":"12.50"},
			"spend_control":{"reached":false,"individual_limit":{"source":"member","limit":"100","used":"25","remaining":"75","used_percent":25,"remaining_percent":75,"reset_after_seconds":86400,"reset_at":1780086400}},
			"additional_rate_limits":[{"limit_name":"Spark","metered_feature":"codex_spark","rate_limit":{"allowed":true,"limit_reached":false,"primary_window":{"used_percent":10,"limit_window_seconds":3600,"reset_after_seconds":600,"reset_at":1780000600}}}],
			"rate_limit_reset_credits":{"available_count":2}
		}`))
	})

	client := &Client{
		httpClient: &http.Client{Transport: handlerRoundTripper{handler: handler}},
		endpoint:   "https://quota.test/wham/usage",
		now:        func() time.Time { return time.Unix(1780000000, 0) },
	}
	snapshot, err := client.Read(context.Background(), Credentials{AccessToken: accessToken, AccountID: "workspace-1", FedRAMP: true})
	if err != nil {
		t.Fatalf("expected quota read to succeed, got %v", err)
	}
	if snapshot.PlanType != "plus" || snapshot.RateLimit == nil || snapshot.RateLimit.PrimaryWindow == nil || snapshot.RateLimit.SecondaryWindow == nil {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
	if snapshot.RateLimit.PrimaryWindow.UsedPercent != 22 || snapshot.RateLimit.PrimaryWindow.RemainingPercent != 78 {
		t.Fatalf("expected used percentage to be converted to remaining, got %#v", snapshot.RateLimit.PrimaryWindow)
	}
	if len(snapshot.AdditionalRateLimits) != 1 || snapshot.AdditionalRateLimits[0].ID != "codex_spark" {
		t.Fatalf("unexpected additional limits: %#v", snapshot.AdditionalRateLimits)
	}
	if snapshot.Credits == nil || snapshot.Credits.Balance == nil || *snapshot.Credits.Balance != "12.50" {
		t.Fatalf("unexpected credits: %#v", snapshot.Credits)
	}
	if snapshot.SpendControl == nil || snapshot.SpendControl.IndividualLimit == nil || snapshot.SpendControl.IndividualLimit.RemainingPercent != 75 {
		t.Fatalf("unexpected spend control: %#v", snapshot.SpendControl)
	}
	if snapshot.ResetCreditsAvailable == nil || *snapshot.ResetCreditsAvailable != 2 {
		t.Fatalf("unexpected reset credits: %#v", snapshot.ResetCreditsAvailable)
	}
}

type handlerRoundTripper struct {
	handler http.Handler
}

func (transport handlerRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	recorder := httptest.NewRecorder()
	transport.handler.ServeHTTP(recorder, request)
	return recorder.Result(), nil
}

func TestClientClassifiesFailuresWithoutLeakingResponseBody(t *testing.T) {
	tests := []struct {
		name string
		code int
		body string
		kind ErrorKind
	}{
		{name: "unauthorized", code: http.StatusUnauthorized, body: `{"error":"raw-secret"}`, kind: ErrorAuthenticationRequired},
		{name: "server error", code: http.StatusBadGateway, body: `raw-secret`, kind: ErrorRemoteUnavailable},
		{name: "invalid json", code: http.StatusOK, body: `{"rate_limit":`, kind: ErrorInvalidResponse},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.code)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			client := &Client{httpClient: server.Client(), endpoint: server.URL, now: time.Now}
			_, err := client.Read(context.Background(), Credentials{AccessToken: "token-secret", AccountID: "workspace"})
			if err == nil || KindOf(err) != tc.kind {
				t.Fatalf("expected %s error, got %v", tc.kind, err)
			}
			if strings.Contains(err.Error(), "raw-secret") || strings.Contains(err.Error(), "token-secret") {
				t.Fatalf("expected redacted error, got %v", err)
			}
		})
	}
}

func TestClientRejectsOversizedOrIncompleteResponses(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "oversized", body: strings.Repeat("x", maxResponseBytes+1)},
		{name: "incomplete", body: `{"plan_type":"plus","rate_limit":{"allowed":true,"primary_window":{"used_percent":5}}}`},
		{name: "zero duration", body: `{"plan_type":"plus","rate_limit":{"allowed":true,"primary_window":{"used_percent":5,"limit_window_seconds":0,"reset_at":1780003600}}}`},
		{name: "zero reset timestamp", body: `{"plan_type":"plus","rate_limit":{"allowed":true,"primary_window":{"used_percent":5,"limit_window_seconds":18000,"reset_at":0}}}`},
		{name: "missing plan", body: `{}`},
		{name: "multiple values", body: `{} {}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			client := &Client{httpClient: server.Client(), endpoint: server.URL, now: time.Now}
			_, err := client.Read(context.Background(), Credentials{AccessToken: "token", AccountID: "workspace"})
			if err == nil || KindOf(err) != ErrorInvalidResponse {
				t.Fatalf("expected invalid response for body size %d, got %v", len(tc.body), err)
			}
		})
	}
}

func TestNewClientDoesNotFollowRedirects(t *testing.T) {
	redirected := false
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirected = true
	}))
	defer target.Close()

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer source.Close()

	client := NewClient()
	client.endpoint = source.URL
	_, err := client.Read(context.Background(), Credentials{AccessToken: "token-secret", AccountID: "workspace"})
	if err == nil || KindOf(err) != ErrorRemoteUnavailable {
		t.Fatalf("expected redirect to be rejected, got %v", err)
	}
	if redirected {
		t.Fatal("expected fixed-endpoint client not to follow redirects")
	}
}

func TestClientPreservesContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := NewClient()
	_, err := client.Read(ctx, Credentials{AccessToken: "token", AccountID: "workspace"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation to be preserved, got %v", err)
	}
}
