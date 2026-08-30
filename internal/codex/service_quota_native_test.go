package codex

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/strahe/profiledeck/internal/agent"
	"github.com/strahe/profiledeck/internal/apperror"
	codexappserver "github.com/strahe/profiledeck/internal/codex/appserver"
	codexauth "github.com/strahe/profiledeck/internal/codex/auth"
	codexquota "github.com/strahe/profiledeck/internal/codex/quota"
	codexsub2api "github.com/strahe/profiledeck/internal/codex/sub2api"
	profilesruntime "github.com/strahe/profiledeck/internal/runtime"
	"github.com/strahe/profiledeck/internal/store"
)

type failSecondCodexPolicy struct {
	calls int
}

func (policy *failSecondCodexPolicy) RequireAgent(context.Context, agent.ID) error {
	policy.calls++
	if policy.calls > 1 {
		return apperror.New(apperror.AgentDisabled, "Agent is disabled")
	}
	return nil
}

func (policy *failSecondCodexPolicy) RequireProvider(context.Context, string) error {
	return nil
}

type immediateSharedLockRunner struct{}

func (immediateSharedLockRunner) RunWithSharedLock(ctx context.Context, _ string, run func(context.Context) error) error {
	return run(ctx)
}

type trackingSharedLockRunner struct {
	active atomic.Int32
}

func (runner *trackingSharedLockRunner) RunWithSharedLock(ctx context.Context, _ string, run func(context.Context) error) error {
	runner.active.Add(1)
	defer runner.active.Add(-1)
	return run(ctx)
}

type fakeCodexNativeRunner struct {
	read    func(context.Context, string) (codexquota.Snapshot, error)
	refresh func(context.Context, string) error
}

func TestCredentialJobRechecksAgentPolicyAfterAcquiringLock(t *testing.T) {
	runtimeService, err := profilesruntime.NewService(t.TempDir())
	if err != nil {
		t.Fatalf("Runtime: %v", err)
	}
	policy := &failSecondCodexPolicy{}
	service := NewService(runtimeService, nil, immediateSharedLockRunner{}, policy, "")

	_, err = service.RunCredentialJob(context.Background(), RunCodexCredentialJobRequest{
		ProfileID: "work", Kind: CodexCredentialJobQuota,
	})
	assertErrorCode(t, err, apperror.AgentDisabled)
	if policy.calls != 2 {
		t.Fatalf("Agent policy calls = %d, want 2", policy.calls)
	}
}

type fakeCodexQuotaReader struct {
	mu          sync.Mutex
	credentials []codexquota.Credentials
	snapshot    codexquota.Snapshot
	err         error
}

type fakeCodexSub2APIReader struct {
	read func(context.Context, codexsub2api.Request) (codexsub2api.Snapshot, error)
}

func (reader fakeCodexSub2APIReader) Read(ctx context.Context, request codexsub2api.Request) (codexsub2api.Snapshot, error) {
	return reader.read(ctx, request)
}

func (f *fakeCodexQuotaReader) Read(_ context.Context, credentials codexquota.Credentials) (codexquota.Snapshot, error) {
	f.mu.Lock()
	f.credentials = append(f.credentials, credentials)
	f.mu.Unlock()
	return f.snapshot, f.err
}

func (f *fakeCodexNativeRunner) ReadRateLimits(ctx context.Context, home string) (codexquota.Snapshot, error) {
	if f.read == nil {
		return codexquota.Snapshot{}, nil
	}
	return f.read(ctx, home)
}

func (f *fakeCodexNativeRunner) RefreshAccount(ctx context.Context, home string) error {
	if f.refresh == nil {
		return nil
	}
	return f.refresh(ctx, home)
}

func TestNativeQuotaCapturesRotatedActiveCredentialByBinding(t *testing.T) {
	ctx := context.Background()
	configDir, codexDir, created := createManagedCodexQuotaFixture(t, ctx)
	rotated := `{"auth_mode":"chatgpt","tokens":{"account_id":"changed-display-only","access_token":"new-access","refresh_token":"new-refresh"},"last_refresh":"2026-07-11T00:00:00Z"}`
	runner := &fakeCodexNativeRunner{read: func(_ context.Context, home string) (codexquota.Snapshot, error) {
		if home != codexDir {
			t.Fatalf("expected active credential to use real CODEX_HOME, got %q", home)
		}
		if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(rotated), 0o600); err != nil {
			t.Fatalf("expected rotated active auth write, got %v", err)
		}
		return nativeQuotaFixture(), nil
	}}
	result, err := newCodexTestEnvironment(t, configDir, "").codex.runCredentialJob(ctx, RunCodexCredentialJobRequest{
		ProfileID: "work", Kind: CodexCredentialJobQuota,
	}, runner, &fakeCodexQuotaReader{})
	if err != nil || result.Quota.Status != CodexProfileQuotaAvailable || !result.CredentialUpdated {
		t.Fatalf("unexpected active native result: %#v, %v", result, err)
	}
	db, err := openHealthyStore(ctx, configDir, true)
	if err != nil {
		t.Fatalf("expected store open, got %v", err)
	}
	defer db.Close()
	credential, err := db.GetProviderCredential(ctx, created.Summary.CredentialID)
	if err != nil || credential.PayloadJSON != rotated {
		t.Fatalf("expected rotated active credential capture, credential=%#v err=%v", credential, err)
	}
	if !strings.Contains(credential.PayloadJSON, "new-refresh") {
		t.Fatal("expected rotated refresh token to be preserved")
	}
}

func TestNativeQuotaUsesPrivateTemporaryHomeAndCASForInactiveCredential(t *testing.T) {
	ctx := context.Background()
	configDir, codexDir, _ := createManagedCodexQuotaFixture(t, ctx)
	child, err := newCodexTestEnvironment(t, configDir, codexDir).codex.ForkProfile(ctx, ForkCodexProfileRequest{
		SourceProfileID: "work", ProfileID: "inactive",
		CredentialBinding: CodexForkBindingCopyNew, ConfigBinding: CodexForkBindingShareParent,
	})
	if err != nil {
		t.Fatalf("expected inactive fixture, got %v", err)
	}
	rotated := `{"auth_mode":"chatgpt","tokens":{"account_id":"display-only","access_token":"inactive-new","refresh_token":"inactive-refresh"}}`
	var tempHome string
	runner := &fakeCodexNativeRunner{read: func(_ context.Context, home string) (codexquota.Snapshot, error) {
		tempHome = home
		if home == codexDir {
			t.Fatal("expected inactive credential not to use real CODEX_HOME")
		}
		dirInfo, err := os.Stat(home)
		if err != nil {
			t.Fatalf("expected temporary home, info=%v err=%v", dirInfo, err)
		}
		if runtime.GOOS != "windows" && dirInfo.Mode().Perm() != 0o700 {
			t.Fatalf("expected 0700 temporary home, info=%v", dirInfo)
		}
		authPath := filepath.Join(home, "auth.json")
		authInfo, err := os.Stat(authPath)
		if err != nil {
			t.Fatalf("expected temporary auth, info=%v err=%v", authInfo, err)
		}
		if runtime.GOOS != "windows" && authInfo.Mode().Perm() != 0o600 {
			t.Fatalf("expected 0600 temporary auth, info=%v", authInfo)
		}
		if err := os.WriteFile(authPath, []byte(rotated), 0o600); err != nil {
			t.Fatalf("expected rotated inactive auth write, got %v", err)
		}
		return nativeQuotaFixture(), nil
	}}
	result, err := newCodexTestEnvironment(t, configDir, "").codex.runCredentialJob(ctx, RunCodexCredentialJobRequest{
		ProfileID: "inactive", Kind: CodexCredentialJobQuota,
	}, runner, &fakeCodexQuotaReader{})
	if err != nil || !result.CredentialUpdated || result.CredentialConflict {
		t.Fatalf("unexpected inactive native result: %#v, %v", result, err)
	}
	if _, err := os.Stat(tempHome); !os.IsNotExist(err) {
		t.Fatalf("expected temporary CODEX_HOME cleanup, got %v", err)
	}
	db, err := openHealthyStore(ctx, configDir, true)
	if err != nil {
		t.Fatalf("expected store open, got %v", err)
	}
	defer db.Close()
	credential, err := db.GetProviderCredential(ctx, child.Summary.CredentialID)
	if err != nil || credential.PayloadJSON != rotated {
		t.Fatalf("expected inactive CAS update, credential=%#v err=%v", credential, err)
	}
}

func TestNativeQuotaInactiveCASNeverOverwritesConcurrentCredential(t *testing.T) {
	ctx := context.Background()
	configDir, codexDir, _ := createManagedCodexQuotaFixture(t, ctx)
	child, err := newCodexTestEnvironment(t, configDir, codexDir).codex.ForkProfile(ctx, ForkCodexProfileRequest{
		SourceProfileID: "work", ProfileID: "inactive",
		CredentialBinding: CodexForkBindingCopyNew, ConfigBinding: CodexForkBindingShareParent,
	})
	if err != nil {
		t.Fatalf("expected inactive fixture, got %v", err)
	}
	concurrent := `{"auth_mode":"chatgpt","tokens":{"account_id":"display-only","access_token":"concurrent","refresh_token":"concurrent-refresh"}}`
	rotated := `{"auth_mode":"chatgpt","tokens":{"account_id":"display-only","access_token":"stale-job","refresh_token":"stale-refresh"}}`
	runner := &fakeCodexNativeRunner{read: func(_ context.Context, home string) (codexquota.Snapshot, error) {
		db, err := openHealthyStore(ctx, configDir, false)
		if err != nil {
			t.Fatalf("expected concurrent store open, got %v", err)
		}
		if _, err := upsertCodexAuthCredential(ctx, db, child.Summary.CredentialID, concurrent); err != nil {
			_ = db.Close()
			t.Fatalf("expected concurrent credential update, got %v", err)
		}
		if err := db.Close(); err != nil {
			t.Fatalf("expected concurrent store close, got %v", err)
		}
		if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(rotated), 0o600); err != nil {
			t.Fatalf("expected stale temporary auth write, got %v", err)
		}
		return nativeQuotaFixture(), nil
	}}
	result, err := newCodexTestEnvironment(t, configDir, "").codex.runCredentialJob(ctx, RunCodexCredentialJobRequest{
		ProfileID: "inactive", Kind: CodexCredentialJobQuota,
	}, runner, &fakeCodexQuotaReader{})
	if err != nil || !result.CredentialConflict || result.CredentialUpdated {
		t.Fatalf("expected inactive CAS conflict, result=%#v err=%v", result, err)
	}
	db, err := openHealthyStore(ctx, configDir, true)
	if err != nil {
		t.Fatalf("expected store open, got %v", err)
	}
	defer db.Close()
	credential, err := db.GetProviderCredential(ctx, child.Summary.CredentialID)
	if err != nil || credential.PayloadJSON != concurrent {
		t.Fatalf("expected concurrent credential to win, credential=%#v err=%v", credential, err)
	}
}

func TestLateInactiveQuotaCaptureCannotRecreateDeletedCredential(t *testing.T) {
	ctx := context.Background()
	configDir, codexDir, _ := createManagedCodexQuotaFixture(t, ctx)
	environment := newCodexTestEnvironment(t, configDir, codexDir)
	child, err := environment.codex.ForkProfile(ctx, ForkCodexProfileRequest{
		SourceProfileID: "work", ProfileID: "inactive",
		CredentialBinding: CodexForkBindingCopyNew, ConfigBinding: CodexForkBindingShareParent,
	})
	if err != nil {
		t.Fatalf("expected inactive fixture, got %v", err)
	}
	db, err := openHealthyStore(ctx, configDir, false)
	if err != nil {
		t.Fatalf("expected quota capture store open, got %v", err)
	}
	defer db.Close()
	credential, err := db.GetProviderCredential(ctx, child.Summary.CredentialID)
	if err != nil {
		t.Fatalf("expected captured inactive credential, got %v", err)
	}
	sourceInfo, err := codexauth.Inspect([]byte(credential.PayloadJSON))
	if err != nil {
		t.Fatalf("expected valid captured credential, got %v", err)
	}
	rotated := `{"auth_mode":"chatgpt","tokens":{"account_id":"display-only","access_token":"late","refresh_token":"late-refresh"}}`
	authPath := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(authPath, []byte(rotated), 0o600); err != nil {
		t.Fatalf("expected late auth fixture, got %v", err)
	}

	type captureResult struct {
		result CodexCredentialJobResult
		err    error
	}
	started := make(chan struct{})
	allowCapture := make(chan struct{})
	captured := make(chan captureResult, 1)
	go func() {
		close(started)
		<-allowCapture
		result := CodexCredentialJobResult{}
		err := captureCodexCredentialAfterNativeJob(
			ctx, db, authPath, credential, credential.PayloadJSON, sourceInfo, false, &result,
		)
		captured <- captureResult{result: result, err: err}
	}()
	<-started

	deleted, err := environment.profiles.Delete(ctx, "inactive", true)
	if err != nil || !deleted.Deleted {
		t.Fatalf("expected Profile deletion before late capture, result=%#v err=%v", deleted, err)
	}
	close(allowCapture)
	late := <-captured
	assertErrorCode(t, late.err, apperror.CodexInvalid)
	if late.result.CredentialUpdated || late.result.CredentialConflict {
		t.Fatalf("late capture reported a credential mutation: %#v", late.result)
	}
	if _, err := db.GetProviderCredential(ctx, child.Summary.CredentialID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("late quota capture recreated deleted credential: %v", err)
	}
}

func TestManualQuotaFallsBackReadOnlyWhenAppServerUnavailable(t *testing.T) {
	ctx := context.Background()
	configDir, codexDir, created := createManagedCodexQuotaFixture(t, ctx)
	working := `{"auth_mode":"chatgpt","tokens":{"account_id":"working-display","access_token":"working-token","refresh_token":"working-refresh"}}`
	if err := os.WriteFile(filepath.Join(codexDir, "auth.json"), []byte(working), 0o600); err != nil {
		t.Fatalf("expected working copy update, got %v", err)
	}
	runner := &fakeCodexNativeRunner{read: func(context.Context, string) (codexquota.Snapshot, error) {
		return codexquota.Snapshot{}, &codexappserver.Error{Kind: codexappserver.ErrorUnavailable}
	}}
	direct := &fakeCodexQuotaReader{snapshot: nativeQuotaFixture()}
	result, err := newCodexTestEnvironment(t, configDir, "").codex.runCredentialJob(ctx, RunCodexCredentialJobRequest{
		ProfileID: "work", Kind: CodexCredentialJobQuota, AllowDirectFallback: true,
	}, runner, direct)
	if err != nil || !result.UsedDirectFallback || result.Quota.Status != CodexProfileQuotaAvailable {
		t.Fatalf("unexpected fallback result: %#v, %v", result, err)
	}
	if len(direct.credentials) != 1 || direct.credentials[0].AccessToken != "working-token" {
		t.Fatalf("expected fallback to use active working copy, got %#v", direct.credentials)
	}
	db, err := openHealthyStore(ctx, configDir, true)
	if err != nil {
		t.Fatalf("expected store open, got %v", err)
	}
	defer db.Close()
	credential, err := db.GetProviderCredential(ctx, created.Summary.CredentialID)
	if err != nil || strings.Contains(credential.PayloadJSON, "working-token") {
		t.Fatalf("expected compatibility fallback not to capture credentials, credential=%#v err=%v", credential, err)
	}
}

func TestSub2APIQuotaReadsOutsideSwitchLock(t *testing.T) {
	ctx := context.Background()
	configDir, _, created := createSub2APICodexQuotaFixture(t, ctx)
	environment := newCodexTestEnvironment(t, configDir, "")
	lock := &trackingSharedLockRunner{}
	environment.codex.sharedLock = lock
	remaining := 75.0
	result, err := environment.codex.runCredentialJob(ctx, RunCodexCredentialJobRequest{
		ProfileID: "work", Kind: CodexCredentialJobQuota,
	}, &fakeCodexNativeRunner{}, &fakeCodexQuotaReader{}, fakeCodexSub2APIReader{read: func(_ context.Context, request codexsub2api.Request) (codexsub2api.Snapshot, error) {
		if lock.active.Load() != 0 {
			t.Fatal("expected API service request outside the shared switch lock")
		}
		if request.BaseURL != "http://api.example.test/openai/v1" || request.APIKey != "synthetic-key" {
			t.Fatalf("unexpected API service request: %#v", request)
		}
		return codexsub2api.Snapshot{
			FetchedAt: time.Unix(1780000000, 0), Mode: "quota_limited", PlanName: "Team",
			KeyState: codexsub2api.KeyStateActive, Unit: "USD", Remaining: &remaining, Windows: []codexsub2api.Window{},
		}, nil
	}})
	if err != nil || result.Quota.Status != CodexProfileQuotaAvailable || result.Quota.Source != CodexQuotaSourceSub2API {
		t.Fatalf("unexpected API service quota result: %#v, %v", result, err)
	}
	if result.Quota.CredentialID != created.Summary.CredentialID || result.Quota.ConfigSetID != created.Summary.ConfigSetID || !result.Quota.InsecureTransport {
		t.Fatalf("unexpected API service binding metadata: %#v", result.Quota)
	}
	if result.Quota.Sub2APISnapshot == nil || result.Quota.Sub2APISnapshot.Remaining == nil || *result.Quota.Sub2APISnapshot.Remaining != remaining {
		t.Fatalf("expected normalized API service snapshot, got %#v", result.Quota.Sub2APISnapshot)
	}
}

func TestSub2APIQuotaDiscardsChangedBinding(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(context.Context, *store.Store, CodexProfileSaveResult) error
	}{
		{
			name: "credential",
			mutate: func(ctx context.Context, db *store.Store, created CodexProfileSaveResult) error {
				_, err := upsertCodexAuthCredential(ctx, db, created.Summary.CredentialID, `{"auth_mode":"apikey","OPENAI_API_KEY":"replacement-key"}`)
				return err
			},
		},
		{
			name: "config set",
			mutate: func(ctx context.Context, db *store.Store, created CodexProfileSaveResult) error {
				_, err := upsertCodexConfigSet(ctx, db, created.Summary.ConfigSetID, "changed", "", "model = \"gpt-5\"\nopenai_base_url = \"https://changed.example.test/v1\"\n")
				return err
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			configDir, _, created := createSub2APICodexQuotaFixture(t, ctx)
			environment := newCodexTestEnvironment(t, configDir, "")
			remaining := 25.0
			result, err := environment.codex.runCredentialJob(ctx, RunCodexCredentialJobRequest{
				ProfileID: "work", Kind: CodexCredentialJobQuota,
			}, &fakeCodexNativeRunner{}, &fakeCodexQuotaReader{}, fakeCodexSub2APIReader{read: func(context.Context, codexsub2api.Request) (codexsub2api.Snapshot, error) {
				db, err := openHealthyStore(ctx, configDir, false)
				if err != nil {
					t.Fatalf("expected concurrent store open, got %v", err)
				}
				defer db.Close()
				if err := test.mutate(ctx, db, created); err != nil {
					t.Fatalf("expected concurrent binding update, got %v", err)
				}
				return codexsub2api.Snapshot{
					FetchedAt: time.Unix(1780000000, 0), Mode: "quota_limited",
					KeyState: codexsub2api.KeyStateActive, Remaining: &remaining, Windows: []codexsub2api.Window{},
				}, nil
			}})
			if err != nil || result.Quota.Status != CodexProfileQuotaUnavailable || result.Quota.Sub2APISnapshot != nil {
				t.Fatalf("expected changed binding result to be discarded, result=%#v err=%v", result, err)
			}
		})
	}
}

func TestSub2APIQuotaDoesNotExposeAPIKeyInFailure(t *testing.T) {
	ctx := context.Background()
	configDir, _, _ := createSub2APICodexQuotaFixture(t, ctx)
	result, err := newCodexTestEnvironment(t, configDir, "").codex.runCredentialJob(ctx, RunCodexCredentialJobRequest{
		ProfileID: "work", Kind: CodexCredentialJobQuota,
	}, &fakeCodexNativeRunner{}, &fakeCodexQuotaReader{}, fakeCodexSub2APIReader{read: func(context.Context, codexsub2api.Request) (codexsub2api.Snapshot, error) {
		return codexsub2api.Snapshot{}, errors.New("synthetic-key must stay private")
	}})
	encoded, marshalErr := json.Marshal(struct {
		Result CodexCredentialJobResult `json:"result"`
		Error  string                   `json:"error"`
	}{Result: result, Error: errorString(err)})
	if marshalErr != nil {
		t.Fatalf("expected result encoding, got %v", marshalErr)
	}
	if strings.Contains(string(encoded), "synthetic-key") || result.Quota.Status != CodexProfileQuotaUnavailable {
		t.Fatalf("API key escaped failure boundary: %s", encoded)
	}
}

func TestNativeKeepaliveClassifiesPermanentAndExternalAuthFailures(t *testing.T) {
	t.Run("quota auth failure probes managed refresh", func(t *testing.T) {
		ctx := context.Background()
		configDir, _, _ := createManagedCodexQuotaFixture(t, ctx)
		var refreshCalls atomic.Int32
		runner := &fakeCodexNativeRunner{
			read: func(context.Context, string) (codexquota.Snapshot, error) {
				return codexquota.Snapshot{}, &codexappserver.Error{Kind: codexappserver.ErrorAuthRequired}
			},
			refresh: func(context.Context, string) error {
				refreshCalls.Add(1)
				return &codexappserver.Error{Kind: codexappserver.ErrorAuthPermanent}
			},
		}
		result, err := newCodexTestEnvironment(t, configDir, "").codex.runCredentialJob(ctx, RunCodexCredentialJobRequest{
			ProfileID: "work", Kind: CodexCredentialJobQuota,
		}, runner, &fakeCodexQuotaReader{})
		if err != nil || refreshCalls.Load() != 1 || result.NativeErrorKind != codexappserver.ErrorAuthPermanent || result.Quota.Status != CodexProfileQuotaAuthRequired {
			t.Fatalf("expected permanent managed refresh classification, result=%#v refresh_calls=%d err=%v", result, refreshCalls.Load(), err)
		}
	})

	t.Run("permanent managed failure", func(t *testing.T) {
		ctx := context.Background()
		configDir, _, _ := createManagedCodexQuotaFixture(t, ctx)
		runner := &fakeCodexNativeRunner{refresh: func(context.Context, string) error {
			return &codexappserver.Error{Kind: codexappserver.ErrorAuthPermanent}
		}}
		result, err := newCodexTestEnvironment(t, configDir, "").codex.runCredentialJob(ctx, RunCodexCredentialJobRequest{
			ProfileID: "work", Kind: CodexCredentialJobKeepalive,
		}, runner, &fakeCodexQuotaReader{})
		if err != nil || result.NativeErrorKind != codexappserver.ErrorAuthPermanent || result.Quota.Status != CodexProfileQuotaAuthRequired {
			t.Fatalf("unexpected permanent failure: %#v, %v", result, err)
		}
	})

	t.Run("external auth is quota only", func(t *testing.T) {
		ctx := context.Background()
		configDir := t.TempDir()
		codexDir := t.TempDir()
		if _, err := initCodexTestRuntime(ctx, configDir); err != nil {
			t.Fatalf("expected init, got %v", err)
		}
		writeCodexProfileFixture(t, codexDir, "model = \"gpt-5\"\n", `{"auth_mode":"chatgptAuthTokens","tokens":{"account_id":"display","access_token":"token","refresh_token":"external"}}`)
		if _, err := newCodexTestEnvironment(t, configDir, codexDir).codex.CreateProfile(ctx, CreateCodexProfileRequest{ProfileID: "work"}); err != nil {
			t.Fatalf("expected profile create, got %v", err)
		}
		var calls atomic.Int32
		runner := &fakeCodexNativeRunner{refresh: func(context.Context, string) error { calls.Add(1); return nil }}
		result, err := newCodexTestEnvironment(t, configDir, "").codex.runCredentialJob(ctx, RunCodexCredentialJobRequest{
			ProfileID: "work", Kind: CodexCredentialJobKeepalive,
		}, runner, &fakeCodexQuotaReader{})
		if err != nil || result.Quota.Status != CodexProfileQuotaUnsupported || calls.Load() != 0 {
			t.Fatalf("expected external auth keepalive rejection, result=%#v calls=%d err=%v", result, calls.Load(), err)
		}
	})
}

func createManagedCodexQuotaFixture(t *testing.T, ctx context.Context) (string, string, CodexProfileSaveResult) {
	t.Helper()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	if _, err := initCodexTestRuntime(ctx, configDir); err != nil {
		t.Fatalf("expected init, got %v", err)
	}
	payload := `{"auth_mode":"chatgpt","tokens":{"account_id":"display-only","access_token":"old-access","refresh_token":"old-refresh"},"last_refresh":"2026-07-01T00:00:00Z"}`
	writeCodexProfileFixture(t, codexDir, "model = \"gpt-5\"\n", payload)
	created, err := newCodexTestEnvironment(t, configDir, codexDir).codex.CreateProfile(ctx, CreateCodexProfileRequest{ProfileID: "work"})
	if err != nil {
		t.Fatalf("expected profile create, got %v", err)
	}
	return configDir, codexDir, created
}

func createSub2APICodexQuotaFixture(t *testing.T, ctx context.Context) (string, string, CodexProfileSaveResult) {
	t.Helper()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	if _, err := initCodexTestRuntime(ctx, configDir); err != nil {
		t.Fatalf("expected init, got %v", err)
	}
	writeCodexProfileFixture(
		t,
		codexDir,
		"model = \"gpt-5\"\nopenai_base_url = \"http://api.example.test/openai/v1\"\n",
		`{"auth_mode":"apikey","OPENAI_API_KEY":"synthetic-key"}`,
	)
	created, err := newCodexTestEnvironment(t, configDir, codexDir).codex.CreateProfile(ctx, CreateCodexProfileRequest{ProfileID: "work"})
	if err != nil {
		t.Fatalf("expected API key profile create, got %v", err)
	}
	return configDir, codexDir, created
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func nativeQuotaFixture() codexquota.Snapshot {
	return codexquota.Snapshot{
		FetchedAt: time.Unix(1780000000, 0), PlanType: "plus",
		RateLimit: &codexquota.RateLimit{ID: "codex", Allowed: true, PrimaryWindow: &codexquota.Window{
			UsedPercent: 20, RemainingPercent: 80, LimitWindowSeconds: 18000, ResetAtUnixSeconds: 1780003600,
		}},
		AdditionalRateLimits: []codexquota.RateLimit{},
	}
}
