package quota

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testAccessToken = "access-token-private"
	testProjectID   = "project-id-private"
	testUserAgent   = "vscode/1.X.X (Antigravity/2.2.1)"
)

var validSummaryJSON = `{
  "groups": [
    {
      "displayName": "Gemini models",
      "buckets": [
        {"bucketId":"gemini-weekly","window":"weekly","remainingFraction":0.25,"resetTime":"2026-07-20T00:00:00Z"},
		{"bucketId":123,"window":"monthly","remainingFraction":"ignored","resetTime":false,"responseSecret":"response-body-private"},
        {"bucketId":"gemini-5h","window":"5h","remainingFraction":0.75,"resetTime":"2026-07-15T08:00:00Z"}
      ],
      "unknown": true
    },
    {
      "displayName": "Claude and GPT models",
      "buckets": [
        {"bucketId":"third-party-5h","window":"5h","remainingFraction":1,"resetTime":"2026-07-15T09:00:00+00:00"}
      ]
    }
  ],
  "unknown": "ignored"
}`

type fixedUserAgent struct {
	value string
	err   error
}

func (provider fixedUserAgent) UserAgent(context.Context) (string, error) {
	return provider.value, provider.err
}

type scriptedHTTPResult struct {
	status int
	body   string
	err    error
}

type recordedRequest struct {
	URL     string
	Method  string
	Headers http.Header
	Body    string
}

type scriptedHTTPClient struct {
	mu       sync.Mutex
	results  []scriptedHTTPResult
	requests []recordedRequest
}

func (client *scriptedHTTPClient) Do(request *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(request.Body)
	client.mu.Lock()
	defer client.mu.Unlock()
	client.requests = append(client.requests, recordedRequest{
		URL: request.URL.String(), Method: request.Method, Headers: request.Header.Clone(), Body: string(body),
	})
	if len(client.results) == 0 {
		return nil, errors.New("unexpected request")
	}
	result := client.results[0]
	client.results = client.results[1:]
	if result.err != nil {
		return nil, result.err
	}
	return &http.Response{
		StatusCode: result.status, Header: make(http.Header),
		Body: io.NopCloser(strings.NewReader(result.body)), Request: request,
	}, nil
}

func newTestClient(doer httpDoer) *Client {
	return &Client{
		http: doer, userAgent: fixedUserAgent{value: testUserAgent},
		loadEndpoint: "https://load.invalid/v1internal:loadCodeAssist",
		summaryEndpoints: []string{
			"https://sandbox.invalid/v1internal:retrieveUserQuotaSummary",
			"https://daily.invalid/v1internal:retrieveUserQuotaSummary",
			"https://prod.invalid/v1internal:retrieveUserQuotaSummary",
		},
		timeout: time.Second, maxResponseBytes: maxResponseBytes,
		now: func() time.Time { return time.Unix(1_789_000_000, 123_000_000) },
	}
}

func TestClientReadsTwoStepSummaryWithExactIdentityAndBodies(t *testing.T) {
	httpClient := &scriptedHTTPClient{results: []scriptedHTTPResult{
		{status: http.StatusOK, body: `{"cloudaicompanionProject":"` + testProjectID + `","other":"ignored"}`},
		{status: http.StatusOK, body: validSummaryJSON},
	}}
	client := newTestClient(httpClient)
	snapshot, err := client.Read(context.Background(), testAccessToken)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if snapshot.FetchedAt.UnixMilli() != 1_789_000_000_123 || len(snapshot.Groups) != 2 {
		t.Fatalf("unexpected snapshot %#v", snapshot)
	}
	first := snapshot.Groups[0]
	if first.DisplayName != "Gemini models" || len(first.Buckets) != 2 ||
		first.Buckets[0].Window != "5h" || first.Buckets[0].RemainingPercent != 75 ||
		first.Buckets[1].Window != "weekly" || first.Buckets[1].RemainingPercent != 25 {
		t.Fatalf("unexpected ordered group %#v", first)
	}
	if len(httpClient.requests) != 2 {
		t.Fatalf("expected two requests, got %#v", httpClient.requests)
	}
	if httpClient.requests[0].Body != `{"metadata":{"ideType":"ANTIGRAVITY"}}` ||
		httpClient.requests[1].Body != `{"project":"`+testProjectID+`"}` {
		t.Fatalf("unexpected request bodies %#v", httpClient.requests)
	}
	for _, request := range httpClient.requests {
		if request.Method != http.MethodPost || request.Headers.Get("Authorization") != "Bearer "+testAccessToken ||
			request.Headers.Get("Content-Type") != "application/json" || request.Headers.Get("User-Agent") != testUserAgent {
			t.Fatalf("unexpected request %#v", request)
		}
		if len(request.Headers) != 3 {
			t.Fatalf("request added unexpected application headers %#v", request.Headers)
		}
		if strings.Contains(request.URL+request.Body+request.Headers.Get("User-Agent"), "ProfileDeck") {
			t.Fatalf("request exposed product identity %#v", request)
		}
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	for _, private := range []string{testAccessToken, testProjectID, "response-body-private", "ProfileDeck"} {
		if strings.Contains(string(raw), private) {
			t.Fatalf("snapshot exposed %q: %s", private, raw)
		}
	}
}

func TestClientRequiresProjectIDBeforeSummaryRequest(t *testing.T) {
	for _, body := range []string{`{}`, `{"cloudaicompanionProject":null}`, `{"cloudaicompanionProject":"  "}`} {
		t.Run(body, func(t *testing.T) {
			httpClient := &scriptedHTTPClient{results: []scriptedHTTPResult{{status: http.StatusOK, body: body}}}
			_, err := newTestClient(httpClient).Read(context.Background(), testAccessToken)
			if KindOf(err) != ErrorUnavailable || len(httpClient.requests) != 1 {
				t.Fatalf("expected unavailable without summary request, err=%v requests=%#v", err, httpClient.requests)
			}
		})
	}
}

func TestClientSummaryFallbackPolicy(t *testing.T) {
	tests := []struct {
		name      string
		results   []scriptedHTTPResult
		wantKind  ErrorKind
		wantCalls int
	}{
		{
			name: "network error falls back",
			results: []scriptedHTTPResult{
				{status: 200, body: `{"cloudaicompanionProject":"` + testProjectID + `"}`},
				{err: errors.New("network includes " + testAccessToken + " " + testProjectID + " ProfileDeck")},
				{status: 200, body: validSummaryJSON},
			}, wantCalls: 3,
		},
		{
			name: "429 falls back",
			results: []scriptedHTTPResult{
				{status: 200, body: `{"cloudaicompanionProject":"` + testProjectID + `"}`},
				{status: 429, body: "private response"},
				{status: 200, body: validSummaryJSON},
			}, wantCalls: 3,
		},
		{
			name: "5xx falls back",
			results: []scriptedHTTPResult{
				{status: 200, body: `{"cloudaicompanionProject":"` + testProjectID + `"}`},
				{status: 503, body: "private response"},
				{status: 200, body: validSummaryJSON},
			}, wantCalls: 3,
		},
		{
			name: "401 stops as auth required",
			results: []scriptedHTTPResult{
				{status: 200, body: `{"cloudaicompanionProject":"` + testProjectID + `"}`}, {status: 401, body: "private response"},
			}, wantKind: ErrorAuthRequired, wantCalls: 2,
		},
		{
			name: "403 stops as unavailable",
			results: []scriptedHTTPResult{
				{status: 200, body: `{"cloudaicompanionProject":"` + testProjectID + `"}`}, {status: 403, body: "private response"},
			}, wantKind: ErrorUnavailable, wantCalls: 2,
		},
		{
			name: "redirect stops as unavailable",
			results: []scriptedHTTPResult{
				{status: 200, body: `{"cloudaicompanionProject":"` + testProjectID + `"}`}, {status: 302, body: "private response"},
			}, wantKind: ErrorUnavailable, wantCalls: 2,
		},
		{
			name: "malformed success stops as unavailable",
			results: []scriptedHTTPResult{
				{status: 200, body: `{"cloudaicompanionProject":"` + testProjectID + `"}`}, {status: 200, body: `{"groups":"wrong"}`},
			}, wantKind: ErrorUnavailable, wantCalls: 2,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			httpClient := &scriptedHTTPClient{results: append([]scriptedHTTPResult(nil), test.results...)}
			snapshot, err := newTestClient(httpClient).Read(context.Background(), testAccessToken)
			if test.wantKind == "" {
				if err != nil || len(snapshot.Groups) == 0 {
					t.Fatalf("expected fallback success, snapshot=%#v err=%v", snapshot, err)
				}
			} else if KindOf(err) != test.wantKind {
				t.Fatalf("expected %q, got %v", test.wantKind, err)
			}
			if len(httpClient.requests) != test.wantCalls {
				t.Fatalf("expected %d calls, got %#v", test.wantCalls, httpClient.requests)
			}
			for index, request := range httpClient.requests[1:] {
				want := newTestClient(nil).summaryEndpoints[index]
				if request.URL != want {
					t.Fatalf("fallback order mismatch at %d: got %q want %q", index, request.URL, want)
				}
			}
			if err != nil {
				for _, private := range []string{testAccessToken, testProjectID, "private response", "ProfileDeck"} {
					if strings.Contains(err.Error(), private) {
						t.Fatalf("error exposed %q: %v", private, err)
					}
				}
			}
		})
	}
}

func TestClientLoadStatusPolicy(t *testing.T) {
	for _, test := range []struct {
		status int
		kind   ErrorKind
	}{
		{status: 401, kind: ErrorAuthRequired},
		{status: 403, kind: ErrorUnavailable},
		{status: 429, kind: ErrorUnavailable},
		{status: 500, kind: ErrorUnavailable},
		{status: 302, kind: ErrorUnavailable},
	} {
		t.Run(http.StatusText(test.status), func(t *testing.T) {
			httpClient := &scriptedHTTPClient{results: []scriptedHTTPResult{{status: test.status, body: "private response"}}}
			_, err := newTestClient(httpClient).Read(context.Background(), testAccessToken)
			if KindOf(err) != test.kind || len(httpClient.requests) != 1 {
				t.Fatalf("unexpected result kind=%q err=%v requests=%#v", test.kind, err, httpClient.requests)
			}
		})
	}
}

func TestClientLoadNetworkFailureDoesNotContinueToSummary(t *testing.T) {
	httpClient := &scriptedHTTPClient{results: []scriptedHTTPResult{{err: errors.New("private network failure")}}}
	if _, err := newTestClient(httpClient).Read(context.Background(), testAccessToken); KindOf(err) != ErrorUnavailable || len(httpClient.requests) != 1 {
		t.Fatalf("expected one unavailable load request, err=%v requests=%#v", err, httpClient.requests)
	}
}

func TestClientRejectsMalformedRecognizedBuckets(t *testing.T) {
	invalidBuckets := []string{
		`{"window":"5h","remainingFraction":0.5,"resetTime":"2026-07-15T08:00:00Z"}`,
		`{"bucketId":"id","window":"weekly","resetTime":"2026-07-15T08:00:00Z"}`,
		`{"bucketId":"id","window":"weekly","remainingFraction":-0.1,"resetTime":"2026-07-15T08:00:00Z"}`,
		`{"bucketId":"id","window":"weekly","remainingFraction":1.1,"resetTime":"2026-07-15T08:00:00Z"}`,
		`{"bucketId":"id","window":"weekly","remainingFraction":0.5,"resetTime":"not-a-time"}`,
	}
	for _, bucket := range invalidBuckets {
		raw := `{"groups":[{"displayName":"Models","buckets":[` + bucket + `]}]}`
		if _, err := decodeSummary([]byte(raw)); err == nil {
			t.Fatalf("expected malformed recognized bucket to fail: %s", raw)
		}
	}
	for _, raw := range []string{
		`{"groups":[{"displayName":"","buckets":[]}]}`,
		`{"groups":[{"displayName":"Models"}]}`,
		`{"groups":[]}`,
	} {
		if _, err := decodeSummary([]byte(raw)); err == nil {
			t.Fatalf("expected malformed summary to fail: %s", raw)
		}
	}
}

func TestClientIgnoresUnsupportedWindowsBeforeValidatingTheirFields(t *testing.T) {
	raw := `{"groups":[{"displayName":"Models","buckets":[
		{"bucketId":123,"window":"monthly","remainingFraction":"invalid","resetTime":false},
		{"bucketId":"models-5h","window":"5h","remainingFraction":0.5,"resetTime":"2026-07-15T08:00:00Z"}
	]}]}`
	groups, err := decodeSummary([]byte(raw))
	if err != nil || len(groups) != 1 || len(groups[0].Buckets) != 1 || groups[0].Buckets[0].Window != "5h" {
		t.Fatalf("expected unsupported window to be ignored, groups=%#v err=%v", groups, err)
	}
}

func TestClientTreatsOnlyUnsupportedWindowsAsEmptySummary(t *testing.T) {
	httpClient := &scriptedHTTPClient{results: []scriptedHTTPResult{
		{status: http.StatusOK, body: `{"cloudaicompanionProject":"` + testProjectID + `"}`},
		{status: http.StatusOK, body: `{"groups":[{"displayName":"Models","buckets":[
			{"bucketId":123,"window":"monthly","remainingFraction":"invalid","resetTime":false}
		]}]}`},
	}}
	snapshot, err := newTestClient(httpClient).Read(context.Background(), testAccessToken)
	if err != nil || snapshot.Groups == nil || len(snapshot.Groups) != 0 || len(httpClient.requests) != 2 {
		t.Fatalf("expected an available empty summary, snapshot=%#v err=%v requests=%#v", snapshot, err, httpClient.requests)
	}
}

func TestClientEnforcesOverallTimeout(t *testing.T) {
	client := newTestClient(httpDoerFunc(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	}))
	client.timeout = 20 * time.Millisecond
	started := time.Now()
	_, err := client.Read(context.Background(), testAccessToken)
	if KindOf(err) != ErrorUnavailable || time.Since(started) > time.Second {
		t.Fatalf("expected bounded unavailable result, elapsed=%s err=%v", time.Since(started), err)
	}
}

func TestClientRejectsOversizedResponse(t *testing.T) {
	httpClient := &scriptedHTTPClient{results: []scriptedHTTPResult{{status: 200, body: strings.Repeat("x", 65)}}}
	client := newTestClient(httpClient)
	client.maxResponseBytes = 64
	if _, err := client.Read(context.Background(), testAccessToken); KindOf(err) != ErrorUnavailable {
		t.Fatalf("expected oversized response to be unavailable, got %v", err)
	}
}

func TestClientRejectsOversizedSummaryResponse(t *testing.T) {
	httpClient := &scriptedHTTPClient{results: []scriptedHTTPResult{
		{status: 200, body: `{"cloudaicompanionProject":"` + testProjectID + `"}`},
		{status: 200, body: strings.Repeat("x", 129)},
	}}
	client := newTestClient(httpClient)
	client.maxResponseBytes = 128
	if _, err := client.Read(context.Background(), testAccessToken); KindOf(err) != ErrorUnavailable || len(httpClient.requests) != 2 {
		t.Fatalf("expected oversized summary to stop as unavailable, err=%v requests=%#v", err, httpClient.requests)
	}
}

func TestProductionClientRefusesRedirects(t *testing.T) {
	redirected := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/load" {
			http.Redirect(writer, request, "/target", http.StatusFound)
			return
		}
		redirected++
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client := NewClient()
	client.userAgent = fixedUserAgent{value: testUserAgent}
	client.loadEndpoint = server.URL + "/load"
	client.summaryEndpoints = []string{server.URL + "/summary"}
	if _, err := client.Read(context.Background(), testAccessToken); KindOf(err) != ErrorUnavailable {
		t.Fatalf("expected redirect to be unavailable, got %v", err)
	}
	if redirected != 0 {
		t.Fatalf("client followed redirect %d times", redirected)
	}
}

type httpDoerFunc func(*http.Request) (*http.Response, error)

func (function httpDoerFunc) Do(request *http.Request) (*http.Response, error) {
	return function(request)
}
