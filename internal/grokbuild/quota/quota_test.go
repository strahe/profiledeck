package quota

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestExchangeInitializesWithoutFileOrTerminalCapabilitiesThenReadsBilling(t *testing.T) {
	input := strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","method":"session/update","params":{}}`,
		`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1}}`,
		`{"jsonrpc":"2.0","method":"auth/updated","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"result":{"config":{"creditUsagePercent":25}}}`,
	}, "\n") + "\n")
	var output bytes.Buffer
	result, err := exchange(context.Background(), &output, input)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if !strings.Contains(string(result), `"creditUsagePercent":25`) {
		t.Fatalf("unexpected billing result: %s", result)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("requests = %d, want initialize and billing only: %q", len(lines), output.String())
	}
	var initialize map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &initialize); err != nil {
		t.Fatalf("decode initialize: %v", err)
	}
	params := initialize["params"].(map[string]any)
	if initialize["jsonrpc"] != "2.0" || initialize["method"] != "initialize" || initialize["id"] != float64(1) || params["protocolVersion"] != float64(1) {
		t.Fatalf("unexpected initialize request: %s", lines[0])
	}
	capabilities := params["clientCapabilities"].(map[string]any)
	filesystem := capabilities["fs"].(map[string]any)
	if capabilities["terminal"] != false || filesystem["readTextFile"] != false || filesystem["writeTextFile"] != false {
		t.Fatalf("initialize advertised capabilities: %s", lines[0])
	}
	meta := params["_meta"].(map[string]any)
	hints := meta["startupHints"].(map[string]any)
	if meta["clientType"] != "profiledeck" || meta["clientVersion"] != "1" || hints["nonInteractive"] != true || hints["skipGitStatus"] != true || hints["skipProjectLayout"] != true {
		t.Fatalf("unexpected initialize metadata: %s", lines[0])
	}
	var billing map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &billing); err != nil {
		t.Fatalf("decode billing request: %v", err)
	}
	billingParams, ok := billing["params"].(map[string]any)
	if strings.Contains(output.String(), "session/") || billing["jsonrpc"] != "2.0" || billing["method"] != "_x.ai/billing" || billing["id"] != float64(2) || !ok || len(billingParams) != 0 {
		t.Fatalf("unexpected request sequence: %q", output.String())
	}
}

func TestACPClientUsesManagedHomeExecutableAndExactCommand(t *testing.T) {
	client := NewACPClient()
	client.now = func() time.Time { return time.Unix(1_780_000_000, 0) }
	client.environ = func() []string {
		return []string{"PATH=/bin", "GROK_HOME=/other", "LANG=en_US.UTF-8"}
	}
	managedHome := t.TempDir()
	managedCommand := filepath.Join(managedHome, "bin", grokExecutableName())
	client.lookPath = func(candidate string) (string, error) {
		if candidate == managedCommand {
			return candidate, nil
		}
		return "", errors.New("unexpected candidate")
	}
	var captured commandSpec
	var stdinClosed atomic.Bool
	var waited atomic.Bool
	client.start = func(_ context.Context, spec commandSpec) (*runningProcess, error) {
		captured = spec
		return &runningProcess{
			stdin: &trackingWriteCloser{closed: &stdinClosed},
			stdout: io.NopCloser(strings.NewReader(strings.Join([]string{
				`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1}}`,
				`{"jsonrpc":"2.0","id":2,"result":{"config":{"creditUsagePercent":12.5}}}`,
			}, "\n") + "\n")),
			wait: func() error {
				waited.Store(true)
				return nil
			},
		}, nil
	}

	snapshot, err := client.Read(context.Background(), managedHome)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if captured.Command != managedCommand || !reflect.DeepEqual(captured.Args, []string{"--no-auto-update", "agent", "--no-leader", "stdio"}) {
		t.Fatalf("command = %q %v", captured.Command, captured.Args)
	}
	if captured.Dir != managedHome || !reflect.DeepEqual(captured.Env, []string{"PATH=/bin", "LANG=en_US.UTF-8", "GROK_HOME=" + managedHome}) {
		t.Fatalf("process environment = dir %q env %v", captured.Dir, captured.Env)
	}
	if snapshot.RemainingPercent == nil || *snapshot.RemainingPercent != 87.5 || !stdinClosed.Load() || !waited.Load() {
		t.Fatalf("snapshot or cleanup = %#v, stdin closed %t, waited %t", snapshot, stdinClosed.Load(), waited.Load())
	}
}

func TestACPClientUsesPATHWhenManagedExecutableIsMissing(t *testing.T) {
	client := NewACPClient()
	managedHome := t.TempDir()
	pathCommand := filepath.Join(t.TempDir(), grokExecutableName())
	var candidates []string
	client.lookPath = func(candidate string) (string, error) {
		candidates = append(candidates, candidate)
		if candidate == "grok" {
			return pathCommand, nil
		}
		return "", errors.New("missing")
	}
	var captured commandSpec
	client.start = func(_ context.Context, spec commandSpec) (*runningProcess, error) {
		captured = spec
		return successfulACPProcess(), nil
	}

	if _, err := client.Read(context.Background(), managedHome); err != nil {
		t.Fatalf("Read: %v", err)
	}
	wantCandidates := []string{filepath.Join(managedHome, "bin", grokExecutableName()), "grok"}
	if !reflect.DeepEqual(candidates, wantCandidates) || captured.Command != pathCommand {
		t.Fatalf("candidates = %v, command = %q", candidates, captured.Command)
	}
}

func TestACPClientUsesStableUserLauncherWithCustomManagedHome(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("stable user launcher is supported on macOS and Linux")
	}
	client := NewACPClient()
	managedHome := t.TempDir()
	userHome := t.TempDir()
	userCommand := filepath.Join(userHome, ".local", "bin", "grok")
	client.userHomeDir = func() (string, error) { return userHome, nil }
	var candidates []string
	client.lookPath = func(candidate string) (string, error) {
		candidates = append(candidates, candidate)
		if candidate == userCommand {
			return candidate, nil
		}
		return "", errors.New("missing")
	}
	var captured commandSpec
	client.start = func(_ context.Context, spec commandSpec) (*runningProcess, error) {
		captured = spec
		return successfulACPProcess(), nil
	}

	if _, err := client.Read(context.Background(), managedHome); err != nil {
		t.Fatalf("Read: %v", err)
	}
	wantCandidates := []string{filepath.Join(managedHome, "bin", grokExecutableName()), "grok", userCommand}
	if !reflect.DeepEqual(candidates, wantCandidates) || captured.Command != userCommand || captured.Dir != managedHome {
		t.Fatalf("candidates = %v, command = %q, dir = %q", candidates, captured.Command, captured.Dir)
	}
}

func TestACPClientReportsRuntimeUnavailableWhenAllCandidatesAreMissing(t *testing.T) {
	managedHome := filepath.Join(t.TempDir(), "managed-secret-home")
	client := NewACPClient()
	client.lookPath = func(string) (string, error) { return "", errors.New("missing") }
	var started atomic.Bool
	client.start = func(context.Context, commandSpec) (*runningProcess, error) {
		started.Store(true)
		return nil, errors.New("must not start")
	}

	_, err := client.Read(context.Background(), managedHome)
	if err == nil || KindOf(err) != ErrorRuntimeUnavailable {
		t.Fatalf("runtime error = %v", err)
	}
	if started.Load() || strings.Contains(err.Error(), managedHome) {
		t.Fatalf("runtime error exposed a candidate or started Grok: %v", err)
	}
}

func TestACPClientRejectsNonExecutableCandidate(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("Unix executable permissions are required for this test")
	}
	t.Setenv("PATH", "")
	managedHome := t.TempDir()
	if err := os.Mkdir(filepath.Join(managedHome, "bin"), 0o700); err != nil {
		t.Fatalf("create managed bin: %v", err)
	}
	if err := os.WriteFile(filepath.Join(managedHome, "bin", grokExecutableName()), []byte("not executable"), 0o600); err != nil {
		t.Fatalf("write non-executable Grok: %v", err)
	}
	client := NewACPClient()
	client.userHomeDir = func() (string, error) { return t.TempDir(), nil }
	var started atomic.Bool
	client.start = func(context.Context, commandSpec) (*runningProcess, error) {
		started.Store(true)
		return nil, errors.New("must not start")
	}

	_, err := client.Read(context.Background(), managedHome)
	if err == nil || KindOf(err) != ErrorRuntimeUnavailable || started.Load() {
		t.Fatalf("non-executable candidate error = %v, started = %t", err, started.Load())
	}
}

func TestACPClientClassifiesOnlyProcessStartFailuresAsRuntimeUnavailable(t *testing.T) {
	managedHome := t.TempDir()
	secretCommand := filepath.Join(t.TempDir(), "private-command")
	client := NewACPClient()
	client.lookPath = func(string) (string, error) { return secretCommand, nil }

	_, err := client.Read(context.Background(), managedHome)
	if err == nil || KindOf(err) != ErrorRuntimeUnavailable || strings.Contains(err.Error(), secretCommand) {
		t.Fatalf("start error = %v", err)
	}

	secretDetail := "private-pipe-detail"
	client.start = func(context.Context, commandSpec) (*runningProcess, error) {
		return nil, errors.New(secretDetail)
	}
	_, err = client.Read(context.Background(), managedHome)
	if err == nil || KindOf(err) != ErrorUnavailable || strings.Contains(err.Error(), secretDetail) {
		t.Fatalf("pipe setup error = %v", err)
	}
}

func TestACPClientRejectsAuthEnvironmentBeforeStarting(t *testing.T) {
	for _, name := range []string{"GROK_AUTH", "GROK_AUTH_PATH", "grok_auth", "grok_auth_path"} {
		t.Run(name, func(t *testing.T) {
			client := NewACPClient()
			client.environ = func() []string { return []string{"PATH=/bin", name + "="} }
			var resolved atomic.Bool
			client.lookPath = func(string) (string, error) {
				resolved.Store(true)
				return "", errors.New("must not resolve")
			}
			var started atomic.Bool
			client.start = func(context.Context, commandSpec) (*runningProcess, error) {
				started.Store(true)
				return nil, errors.New("must not start")
			}

			_, err := client.Read(context.Background(), "/managed/grok")
			if err == nil || KindOf(err) != ErrorUnsupported {
				t.Fatalf("auth override error = %v", err)
			}
			if resolved.Load() || started.Load() {
				t.Fatal("auth override resolved or started Grok")
			}
		})
	}
}

func TestACPClientTimeoutClosesAndReapsProcess(t *testing.T) {
	client := NewACPClient()
	client.timeout = 20 * time.Millisecond
	client.lookPath = func(candidate string) (string, error) { return candidate, nil }
	var stdinClosed atomic.Bool
	var reaped atomic.Bool
	var reading atomic.Bool
	var waitedWhileReading atomic.Bool
	client.start = func(ctx context.Context, _ commandSpec) (*runningProcess, error) {
		reader, writer := io.Pipe()
		go func() {
			<-ctx.Done()
			_ = writer.Close()
		}()
		return &runningProcess{
			stdin:  &trackingWriteCloser{closed: &stdinClosed},
			stdout: &trackingReadCloser{ReadCloser: reader, reading: &reading},
			wait: func() error {
				waitedWhileReading.Store(reading.Load())
				reaped.Store(true)
				return ctx.Err()
			},
			kill: func() error { return nil },
		}, nil
	}

	_, err := client.Read(context.Background(), t.TempDir())
	if err == nil || KindOf(err) != ErrorUnavailable || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout error = %v", err)
	}
	if !stdinClosed.Load() || !reaped.Load() || waitedWhileReading.Load() {
		t.Fatalf("process cleanup = stdin closed %t, reaped %t, waited while reading %t", stdinClosed.Load(), reaped.Load(), waitedWhileReading.Load())
	}
}

func TestDecodeBillingPrefersCurrentFieldsAndPreservesOptionalSummary(t *testing.T) {
	fetchedAt := time.Date(2026, time.August, 2, 1, 2, 3, 0, time.UTC)
	snapshot, err := decodeBilling(json.RawMessage(`{
		"config": {
			"creditUsagePercent": 37.5,
			"currentPeriod": {"type":"USAGE_PERIOD_TYPE_WEEKLY","start":"2026-08-01T00:00:00Z","end":"2026-08-08T00:00:00Z"},
			"monthlyLimit": {"val":10000}, "used":{"val":9900},
			"prepaidBalance": {}, "onDemandCap":{"val":5000}, "onDemandUsed":{"val":1250},
			"isUnifiedBillingUser": false,
			"history": [{"totalUsed":{"val":999999}}]
		},
		"onDemandEnabled": true,
		"subscriptionTier": " SuperGrok Heavy ",
		"futureField": {"ignored":true}
	}`), fetchedAt)
	if err != nil {
		t.Fatalf("decodeBilling: %v", err)
	}
	if snapshot.CreditUsagePercent == nil || *snapshot.CreditUsagePercent != 37.5 || snapshot.RemainingPercent == nil || *snapshot.RemainingPercent != 62.5 {
		t.Fatalf("percent mapping = %#v", snapshot)
	}
	if snapshot.PeriodDurationSeconds == nil || *snapshot.PeriodDurationSeconds != 7*24*60*60 || snapshot.PeriodType != "USAGE_PERIOD_TYPE_WEEKLY" {
		t.Fatalf("period mapping = %#v", snapshot)
	}
	if snapshot.PrepaidBalanceCents == nil || *snapshot.PrepaidBalanceCents != 0 || snapshot.OnDemandEnabled == nil || !*snapshot.OnDemandEnabled {
		t.Fatalf("optional mapping = %#v", snapshot)
	}
	if snapshot.SubscriptionTier != "SuperGrok Heavy" || snapshot.UnifiedBillingUser == nil || *snapshot.UnifiedBillingUser {
		t.Fatalf("summary mapping = %#v", snapshot)
	}
}

func TestDecodeBillingFallsBackToLegacyFieldsAndClampsPercent(t *testing.T) {
	snapshot, err := decodeBilling(json.RawMessage(`{
		"config": {
			"monthlyLimit":{"val":2000}, "used":{"val":2500},
			"billingPeriodStart":"2026-08-01T00:00:00Z",
			"billingPeriodEnd":"2026-09-01T00:00:00Z"
		}
	}`), time.Unix(0, 0))
	if err != nil {
		t.Fatalf("decodeBilling: %v", err)
	}
	if snapshot.CreditUsagePercent == nil || *snapshot.CreditUsagePercent != 100 || snapshot.RemainingPercent == nil || *snapshot.RemainingPercent != 0 {
		t.Fatalf("legacy percent = %#v", snapshot)
	}
	if snapshot.PeriodEnd == nil || snapshot.IncludedLimitCents == nil || *snapshot.IncludedLimitCents != 2000 {
		t.Fatalf("legacy mapping = %#v", snapshot)
	}
}

func TestDecodeBillingPreservesZeroValuesAndOptionalAbsence(t *testing.T) {
	snapshot, err := decodeBilling(json.RawMessage(`{
		"config": {
			"creditUsagePercent": 0,
			"monthlyLimit": {},
			"used": {},
			"onDemandCap": {},
			"onDemandUsed": {},
			"prepaidBalance": {},
			"isUnifiedBillingUser": false
		},
		"onDemandEnabled": false
	}`), time.Unix(0, 0))
	if err != nil {
		t.Fatalf("decodeBilling: %v", err)
	}
	if snapshot.RemainingPercent == nil || *snapshot.RemainingPercent != 100 || snapshot.IncludedLimitCents == nil || *snapshot.IncludedLimitCents != 0 {
		t.Fatalf("zero credits mapping = %#v", snapshot)
	}
	if snapshot.OnDemandEnabled == nil || *snapshot.OnDemandEnabled || snapshot.UnifiedBillingUser == nil || *snapshot.UnifiedBillingUser {
		t.Fatalf("false option mapping = %#v", snapshot)
	}
	if snapshot.PeriodStart != nil || snapshot.PeriodEnd != nil || snapshot.SubscriptionTier != "" {
		t.Fatalf("absent optional fields = %#v", snapshot)
	}
}

func TestProtocolFailuresAreBoundedAndRedacted(t *testing.T) {
	secret := "upstream-secret-response"
	err := classifyProtocolError(&protocolError{Code: -32001, Message: "auth_required", Data: json.RawMessage(`"` + secret + `"`)}, false)
	if KindOf(err) != ErrorAuthRequired || strings.Contains(err.Error(), secret) {
		t.Fatalf("auth error = %v", err)
	}
	if KindOf(classifyProtocolError(&protocolError{Code: -32000, Message: "Authentication required"}, false)) != ErrorAuthRequired {
		t.Fatal("authentication-required error did not map to auth_required")
	}
	if KindOf(classifyProtocolError(&protocolError{Code: -32601, Message: secret}, false)) != ErrorUnsupported {
		t.Fatal("method-not-found did not map to unsupported")
	}
	if KindOf(classifyProtocolError(&protocolError{Code: -32601, Message: secret}, true)) != ErrorUnsupported {
		t.Fatal("initialize method-not-found did not map to unsupported")
	}
	for _, status := range []string{"401", "403"} {
		data, marshalErr := json.Marshal("Billing service error: HTTP " + status)
		if marshalErr != nil {
			t.Fatalf("encode billing auth error: %v", marshalErr)
		}
		err = classifyProtocolError(&protocolError{Code: internalErrorCode, Message: "Internal error", Data: data}, false)
		if KindOf(err) != ErrorAuthRequired || strings.Contains(err.Error(), status) {
			t.Fatalf("billing HTTP %s error = %v", status, err)
		}
	}
	for _, detail := range []string{
		"Billing service error: unauthorized",
		"BILLING SERVICE ERROR: UNAUTHORIZED",
	} {
		data, marshalErr := json.Marshal(detail)
		if marshalErr != nil {
			t.Fatalf("encode billing authentication error: %v", marshalErr)
		}
		err = classifyProtocolError(&protocolError{Code: internalErrorCode, Message: "Internal error", Data: data}, false)
		if KindOf(err) != ErrorAuthRequired || strings.Contains(strings.ToLower(err.Error()), "unauthorized") {
			t.Fatalf("billing authentication error = %v", err)
		}
	}
	for _, data := range []json.RawMessage{
		json.RawMessage(`"Billing service error: HTTP 429"`),
		json.RawMessage(`"Billing service error: user unauthorized"`),
		json.RawMessage(`"Billing service error: token expired"`),
		json.RawMessage(`{"status":401}`),
		json.RawMessage(`"Billing service error: HTTP 401"`),
	} {
		code := internalErrorCode
		if string(data) == `"Billing service error: HTTP 401"` {
			code = -32000
		}
		if KindOf(classifyProtocolError(&protocolError{Code: code, Message: "Internal error", Data: data}, false)) != ErrorUnavailable {
			t.Fatalf("non-authoritative billing error mapped as authentication failure: %s", data)
		}
	}

	oversized := strings.Repeat("x", maxProtocolBytes+1) + "\n"
	scanner := bufio.NewScanner(strings.NewReader(oversized))
	scanner.Buffer(make([]byte, 64*1024), maxProtocolBytes)
	if _, err := readResponse(context.Background(), scanner, "1", false); err == nil || KindOf(err) != ErrorUnavailable {
		t.Fatalf("oversized message error = %v", err)
	}
	if _, err := decodeBilling(json.RawMessage(`{"config":`), time.Now()); err == nil || KindOf(err) != ErrorUnavailable {
		t.Fatalf("malformed billing error = %v", err)
	}
}

type trackingWriteCloser struct {
	bytes.Buffer
	closed *atomic.Bool
}

func successfulACPProcess() *runningProcess {
	return &runningProcess{
		stdin: &trackingWriteCloser{closed: &atomic.Bool{}},
		stdout: io.NopCloser(strings.NewReader(strings.Join([]string{
			`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1}}`,
			`{"jsonrpc":"2.0","id":2,"result":{"config":{"creditUsagePercent":12.5}}}`,
		}, "\n") + "\n")),
		wait: func() error { return nil },
	}
}

type trackingReadCloser struct {
	io.ReadCloser
	reading *atomic.Bool
}

func (r *trackingReadCloser) Read(buffer []byte) (int, error) {
	r.reading.Store(true)
	defer r.reading.Store(false)
	return r.ReadCloser.Read(buffer)
}

func (w *trackingWriteCloser) Close() error {
	w.closed.Store(true)
	return nil
}
