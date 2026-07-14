package automation

import (
	"context"
	"errors"
	"testing"
	"time"

	codexappserver "github.com/strahe/profiledeck/internal/codex/appserver"
	codexauth "github.com/strahe/profiledeck/internal/codex/auth"
	codexquota "github.com/strahe/profiledeck/internal/codex/quota"
)

type testRunner struct {
	readCalls    int
	refreshCalls int
	read         func(int) (codexquota.Snapshot, error)
	refresh      error
}

func (runner *testRunner) ReadRateLimits(_ context.Context, _ string) (codexquota.Snapshot, error) {
	runner.readCalls++
	return runner.read(runner.readCalls)
}

func (runner *testRunner) RefreshAccount(context.Context, string) error {
	runner.refreshCalls++
	return runner.refresh
}

type testReader struct {
	calls int
	read  func(codexquota.Credentials) (codexquota.Snapshot, error)
}

func (reader *testReader) Read(_ context.Context, credentials codexquota.Credentials) (codexquota.Snapshot, error) {
	reader.calls++
	return reader.read(credentials)
}

func TestRunRetriesNativeQuotaAfterAuthenticatedRefresh(t *testing.T) {
	info := codexauth.Info{Mode: codexauth.ModeChatGPT, QuotaSupported: true, RefreshSupported: true}
	runner := &testRunner{read: func(call int) (codexquota.Snapshot, error) {
		if call == 1 {
			return codexquota.Snapshot{}, &codexappserver.Error{Kind: codexappserver.ErrorAuthRequired}
		}
		return codexquota.Snapshot{FetchedAt: time.Unix(1, 0)}, nil
	}}

	result, err := Run(context.Background(), JobQuota, "/private/home", `{"auth_mode":"chatgpt","tokens":{"account_id":"display-only","access_token":"token","refresh_token":"refresh"}}`, info, runner, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusAvailable || result.Snapshot == nil || runner.readCalls != 2 || runner.refreshCalls != 1 {
		t.Fatalf("unexpected native retry result: %#v, reads=%d refreshes=%d", result, runner.readCalls, runner.refreshCalls)
	}
}

func TestRunUsesDirectFallbackOnlyForManualQuota(t *testing.T) {
	info := codexauth.Info{Mode: codexauth.ModeChatGPT, QuotaSupported: true}
	runner := &testRunner{read: func(int) (codexquota.Snapshot, error) {
		return codexquota.Snapshot{}, &codexappserver.Error{Kind: codexappserver.ErrorUnavailable}
	}}
	reader := &testReader{read: func(credentials codexquota.Credentials) (codexquota.Snapshot, error) {
		if credentials.AccountID != "display-only" {
			t.Fatalf("unexpected quota account header: %q", credentials.AccountID)
		}
		return codexquota.Snapshot{FetchedAt: time.Unix(2, 0)}, nil
	}}

	result, err := Run(context.Background(), JobQuota, "/private/home", `{"auth_mode":"chatgpt","tokens":{"account_id":"display-only","access_token":"token"}}`, info, runner, reader, true)
	if err != nil {
		t.Fatal(err)
	}
	if !result.UsedDirectFallback || result.Status != StatusAvailable || result.Snapshot == nil || reader.calls != 1 {
		t.Fatalf("unexpected direct fallback result: %#v, reads=%d", result, reader.calls)
	}
}

func TestRunRejectsUnsupportedKeepaliveWithoutCallingRunner(t *testing.T) {
	info := codexauth.Info{Mode: codexauth.ModeChatGPTAuthTokens, QuotaSupported: true, RefreshSupported: false}
	runner := &testRunner{read: func(int) (codexquota.Snapshot, error) { return codexquota.Snapshot{}, errors.New("unexpected read") }}

	result, err := Run(context.Background(), JobKeepalive, "/private/home", "{}", info, runner, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusUnsupported || result.NativeAttempted || runner.readCalls != 0 || runner.refreshCalls != 0 {
		t.Fatalf("unexpected unsupported keepalive result: %#v", result)
	}
}
