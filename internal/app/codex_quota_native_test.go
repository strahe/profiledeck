package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	codexappserver "github.com/strahe/profiledeck/internal/codex/appserver"
	codexquota "github.com/strahe/profiledeck/internal/codex/quota"
)

type fakeCodexNativeRunner struct {
	read    func(context.Context, string) (codexquota.Snapshot, error)
	refresh func(context.Context, string) error
}

type fakeCodexQuotaReader struct {
	mu          sync.Mutex
	credentials []codexquota.Credentials
	snapshot    codexquota.Snapshot
	err         error
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
	result, err := runCodexCredentialJob(ctx, RunCodexCredentialJobRequest{
		ConfigDir: configDir, ProfileID: "work", Kind: CodexCredentialJobQuota,
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
	child, err := ForkCodexProfile(ctx, ForkCodexProfileRequest{
		ConfigDir: configDir, CodexDir: codexDir, SourceProfileID: "work", ProfileID: "inactive",
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
		if err != nil || dirInfo.Mode().Perm() != 0o700 {
			t.Fatalf("expected 0700 temporary home, info=%v err=%v", dirInfo, err)
		}
		authPath := filepath.Join(home, "auth.json")
		authInfo, err := os.Stat(authPath)
		if err != nil || authInfo.Mode().Perm() != 0o600 {
			t.Fatalf("expected 0600 temporary auth, info=%v err=%v", authInfo, err)
		}
		if err := os.WriteFile(authPath, []byte(rotated), 0o600); err != nil {
			t.Fatalf("expected rotated inactive auth write, got %v", err)
		}
		return nativeQuotaFixture(), nil
	}}
	result, err := runCodexCredentialJob(ctx, RunCodexCredentialJobRequest{
		ConfigDir: configDir, ProfileID: "inactive", Kind: CodexCredentialJobQuota,
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
	child, err := ForkCodexProfile(ctx, ForkCodexProfileRequest{
		ConfigDir: configDir, CodexDir: codexDir, SourceProfileID: "work", ProfileID: "inactive",
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
	result, err := runCodexCredentialJob(ctx, RunCodexCredentialJobRequest{
		ConfigDir: configDir, ProfileID: "inactive", Kind: CodexCredentialJobQuota,
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
	result, err := runCodexCredentialJob(ctx, RunCodexCredentialJobRequest{
		ConfigDir: configDir, ProfileID: "work", Kind: CodexCredentialJobQuota, AllowDirectFallback: true,
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
		result, err := runCodexCredentialJob(ctx, RunCodexCredentialJobRequest{
			ConfigDir: configDir, ProfileID: "work", Kind: CodexCredentialJobQuota,
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
		result, err := runCodexCredentialJob(ctx, RunCodexCredentialJobRequest{
			ConfigDir: configDir, ProfileID: "work", Kind: CodexCredentialJobKeepalive,
		}, runner, &fakeCodexQuotaReader{})
		if err != nil || result.NativeErrorKind != codexappserver.ErrorAuthPermanent || result.Quota.Status != CodexProfileQuotaAuthRequired {
			t.Fatalf("unexpected permanent failure: %#v, %v", result, err)
		}
	})

	t.Run("external auth is quota only", func(t *testing.T) {
		ctx := context.Background()
		configDir := t.TempDir()
		codexDir := t.TempDir()
		if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
			t.Fatalf("expected init, got %v", err)
		}
		writeCodexProfileFixture(t, codexDir, "model = \"gpt-5\"\n", `{"auth_mode":"chatgptAuthTokens","tokens":{"account_id":"display","access_token":"token","refresh_token":"external"}}`)
		if _, err := CreateCodexProfile(ctx, CreateCodexProfileRequest{ConfigDir: configDir, CodexDir: codexDir, ProfileID: "work"}); err != nil {
			t.Fatalf("expected profile create, got %v", err)
		}
		var calls atomic.Int32
		runner := &fakeCodexNativeRunner{refresh: func(context.Context, string) error { calls.Add(1); return nil }}
		result, err := runCodexCredentialJob(ctx, RunCodexCredentialJobRequest{
			ConfigDir: configDir, ProfileID: "work", Kind: CodexCredentialJobKeepalive,
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
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init, got %v", err)
	}
	payload := `{"auth_mode":"chatgpt","tokens":{"account_id":"display-only","access_token":"old-access","refresh_token":"old-refresh"},"last_refresh":"2026-07-01T00:00:00Z"}`
	writeCodexProfileFixture(t, codexDir, "model = \"gpt-5\"\n", payload)
	created, err := CreateCodexProfile(ctx, CreateCodexProfileRequest{ConfigDir: configDir, CodexDir: codexDir, ProfileID: "work"})
	if err != nil {
		t.Fatalf("expected profile create, got %v", err)
	}
	return configDir, codexDir, created
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
