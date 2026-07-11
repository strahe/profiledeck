package appserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestExchangeInitializesSkipsNotificationsAndReadsRateLimits(t *testing.T) {
	input := strings.NewReader(strings.Join([]string{
		`{"method":"remoteControl/status/changed","params":{"status":"disabled"}}`,
		`{"id":1,"result":{"userAgent":"codex-test","codexHome":"/tmp/test"}}`,
		`{"method":"account/updated","params":{"authMode":"chatgpt"}}`,
		`{"id":2,"result":{"rateLimits":{"limitId":"codex","planType":"plus","primary":{"usedPercent":22,"windowDurationMins":300,"resetsAt":1780003600}},"rateLimitResetCredits":{"availableCount":2}}}`,
	}, "\n") + "\n")
	var output bytes.Buffer
	result, err := exchange(context.Background(), &output, input, "account/rateLimits/read", nil)
	if err != nil {
		t.Fatalf("expected protocol exchange, got %v", err)
	}
	if !strings.Contains(string(result), `"availableCount":2`) {
		t.Fatalf("unexpected result: %s", result)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected initialize, initialized, and method request, got %q", output.String())
	}
	var initialize map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &initialize); err != nil || initialize["method"] != "initialize" {
		t.Fatalf("unexpected initialize request: %q, %v", lines[0], err)
	}
	if !strings.Contains(lines[0], "optOutNotificationMethods") || !strings.Contains(lines[1], `"method":"initialized"`) || !strings.Contains(lines[2], `"method":"account/rateLimits/read"`) {
		t.Fatalf("unexpected handshake output: %q", output.String())
	}
}

func TestDecodeRateLimitsMapsNativeSnapshot(t *testing.T) {
	fetchedAt := time.Unix(1780000000, 0)
	snapshot, err := decodeRateLimits(json.RawMessage(`{
		"rateLimits":{"limitId":"codex","limitName":"Codex","planType":"pro","primary":{"usedPercent":22,"windowDurationMins":300,"resetsAt":1780003600},"credits":{"hasCredits":true,"unlimited":false,"balance":"12.5"},"individualLimit":{"limit":"100","used":"25","remainingPercent":75,"resetsAt":1780086400}},
		"rateLimitsByLimitId":{"codex":{"limitId":"codex","primary":{"usedPercent":22,"windowDurationMins":300,"resetsAt":1780003600}},"spark":{"limitId":"spark","limitName":"Spark","primary":{"usedPercent":10,"windowDurationMins":60,"resetsAt":1780000600}}},
		"rateLimitResetCredits":{"availableCount":3}
	}`), fetchedAt)
	if err != nil {
		t.Fatalf("expected native snapshot mapping, got %v", err)
	}
	if snapshot.PlanType != "pro" || snapshot.RateLimit == nil || snapshot.RateLimit.PrimaryWindow == nil {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
	if snapshot.RateLimit.PrimaryWindow.RemainingPercent != 78 || snapshot.RateLimit.PrimaryWindow.LimitWindowSeconds != 18000 {
		t.Fatalf("unexpected primary window: %#v", snapshot.RateLimit.PrimaryWindow)
	}
	if len(snapshot.AdditionalRateLimits) != 1 || snapshot.AdditionalRateLimits[0].ID != "spark" {
		t.Fatalf("expected deterministic additional limit mapping, got %#v", snapshot.AdditionalRateLimits)
	}
	if snapshot.SpendControl == nil || snapshot.SpendControl.IndividualLimit == nil || snapshot.SpendControl.IndividualLimit.UsedPercent != 25 {
		t.Fatalf("unexpected spend control: %#v", snapshot.SpendControl)
	}
	if snapshot.ResetCreditsAvailable == nil || *snapshot.ResetCreditsAvailable != 3 {
		t.Fatalf("unexpected reset credits: %#v", snapshot.ResetCreditsAvailable)
	}
}

func TestProtocolErrorsAreClassifiedWithoutRawDetails(t *testing.T) {
	rawSecret := "refresh-token-secret"
	err := classifyProtocolError(&protocolError{Code: -32000, Message: "refresh token was already used: " + rawSecret}, false)
	if KindOf(err) != ErrorAuthPermanent {
		t.Fatalf("expected permanent auth error, got %v", err)
	}
	if strings.Contains(err.Error(), rawSecret) || strings.Contains(err.Error(), "already used") {
		t.Fatalf("expected redacted protocol error, got %v", err)
	}
	if KindOf(classifyProtocolError(&protocolError{Code: -32601, Message: "raw method detail"}, false)) != ErrorIncompatible {
		t.Fatal("expected missing method to be protocol incompatible")
	}
}

func TestExchangeRejectsExternalTokenRefreshRequestWithoutWaiting(t *testing.T) {
	input := strings.NewReader(strings.Join([]string{
		`{"id":1,"result":{"userAgent":"codex-test","codexHome":"/tmp/test"}}`,
		`{"id":"server-1","method":"account/chatgptAuthTokens/refresh","params":{"reason":"unauthorized"}}`,
	}, "\n") + "\n")
	var output bytes.Buffer
	_, err := exchange(context.Background(), &output, input, "account/rateLimits/read", nil)
	if err == nil || KindOf(err) != ErrorAuthRequired {
		t.Fatalf("expected unsupported external token refresh to require auth, got %v", err)
	}
}

func TestRunnerTimeoutCancelsAndCleansUpProcess(t *testing.T) {
	runner := NewRunner()
	runner.Timeout = 20 * time.Millisecond
	var stdinClosed atomic.Bool
	runner.start = func(ctx context.Context, _, _ string) (*runningProcess, error) {
		reader, writer := io.Pipe()
		wait := make(chan error, 1)
		go func() {
			<-ctx.Done()
			_ = writer.Close()
			wait <- ctx.Err()
			close(wait)
		}()
		return &runningProcess{
			stdin: &trackingWriteCloser{closed: &stdinClosed}, stdout: reader, wait: wait,
			kill: func() error { return nil },
		}, nil
	}
	_, err := runner.ReadRateLimits(context.Background(), t.TempDir())
	if err == nil || KindOf(err) != ErrorTransient || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected timeout cancellation, got %v", err)
	}
	if !stdinClosed.Load() {
		t.Fatal("expected process stdin cleanup")
	}
}

func TestCommandDisablesUnrelatedStartupFeatures(t *testing.T) {
	joined := strings.Join(commandArgs(), " ")
	for _, expected := range []string{
		"--disable remote_plugin", "--disable apps", `cli_auth_credentials_store="file"`,
		"analytics.enabled=false", "memories.use_memories=false", "memories.generate_memories=false", "include_apps_instructions=false",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("expected app-server argument %q in %q", expected, joined)
		}
	}
}

type trackingWriteCloser struct {
	bytes.Buffer
	closed *atomic.Bool
}

func (w *trackingWriteCloser) Close() error {
	w.closed.Store(true)
	return nil
}
