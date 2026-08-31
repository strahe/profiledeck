package grokbuild

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/strahe/profiledeck/internal/agent"
	"github.com/strahe/profiledeck/internal/apperror"
	grokconfig "github.com/strahe/profiledeck/internal/grokbuild/config"
	grokpreset "github.com/strahe/profiledeck/internal/grokbuild/preset"
	grokprofile "github.com/strahe/profiledeck/internal/grokbuild/profile"
	grokquota "github.com/strahe/profiledeck/internal/grokbuild/quota"
	"github.com/strahe/profiledeck/internal/runtime"
	"github.com/strahe/profiledeck/internal/store"
)

func TestReadProfileQuotaRoutesOnlyActiveProfileToACPReader(t *testing.T) {
	locked := false
	lock := quotaLockRunnerFunc(func(ctx context.Context, _ string, run func(context.Context) error) error {
		locked = true
		defer func() { locked = false }()
		return run(ctx)
	})
	service := newQuotaTestService(t, lock)
	var calls atomic.Int32
	service.quotaReader = quotaReaderFunc(func(_ context.Context, home string) (grokquota.Snapshot, error) {
		calls.Add(1)
		if !locked {
			t.Fatal("quota reader ran outside the shared switch lock")
		}
		if home != service.grokHome {
			t.Fatalf("reader Home = %q, want %q", home, service.grokHome)
		}
		remaining := 75.0
		return grokquota.Snapshot{FetchedAt: time.Unix(1_780_000_000, 0), RemainingPercent: &remaining}, nil
	})

	active, err := service.ReadProfileQuota(context.Background(), ReadGrokBuildProfileQuotaRequest{ProfileID: "active"})
	if err != nil {
		t.Fatalf("read active quota: %v", err)
	}
	if active.Status != GrokBuildProfileQuotaAvailable || active.CredentialID != "credential-active" || active.ConfigSetID != "config-active" || active.Snapshot == nil || active.Snapshot.RemainingPercent == nil || *active.Snapshot.RemainingPercent != 75 {
		t.Fatalf("active quota = %#v", active)
	}

	inactive, err := service.ReadProfileQuota(context.Background(), ReadGrokBuildProfileQuotaRequest{ProfileID: "inactive"})
	if err != nil {
		t.Fatalf("read inactive quota: %v", err)
	}
	if inactive.Status != GrokBuildProfileQuotaInactive || inactive.CredentialID != "credential-inactive" || inactive.ConfigSetID != "config-inactive" || inactive.Snapshot != nil {
		t.Fatalf("inactive quota = %#v", inactive)
	}
	if calls.Load() != 1 {
		t.Fatalf("reader calls = %d, want active Profile only", calls.Load())
	}
}

func TestReadProfileQuotaRechecksAgentPolicyAfterAcquiringLock(t *testing.T) {
	policy := &failSecondGrokBuildPolicy{}
	service := newQuotaTestService(t, immediateQuotaLock{})
	service.policy = policy
	var called atomic.Bool
	service.quotaReader = quotaReaderFunc(func(context.Context, string) (grokquota.Snapshot, error) {
		called.Store(true)
		return grokquota.Snapshot{}, nil
	})

	_, err := service.ReadProfileQuota(context.Background(), ReadGrokBuildProfileQuotaRequest{ProfileID: "active"})
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != apperror.AgentDisabled {
		t.Fatalf("quota error = %v, want code %s", err, apperror.AgentDisabled)
	}
	if policy.calls != 2 {
		t.Fatalf("Agent policy calls = %d, want 2", policy.calls)
	}
	if called.Load() {
		t.Fatal("quota reader ran after Grok Build was disabled")
	}
}

func TestReadProfileQuotaMapsACPFailuresWithoutDetails(t *testing.T) {
	service := newQuotaTestService(t, immediateQuotaLock{})
	secret := "billing-response-secret"
	tests := []struct {
		kind grokquota.ErrorKind
		want GrokBuildProfileQuotaStatus
	}{
		{kind: grokquota.ErrorAuthRequired, want: GrokBuildProfileQuotaAuthRequired},
		{kind: grokquota.ErrorRuntimeUnavailable, want: GrokBuildProfileQuotaRuntimeUnavailable},
		{kind: grokquota.ErrorUnsupported, want: GrokBuildProfileQuotaUnsupported},
		{kind: grokquota.ErrorUnavailable, want: GrokBuildProfileQuotaUnavailable},
	}
	for _, test := range tests {
		service.quotaReader = quotaReaderFunc(func(context.Context, string) (grokquota.Snapshot, error) {
			return grokquota.Snapshot{}, &grokquota.Error{Kind: test.kind, Err: errors.New(secret)}
		})
		result, err := service.ReadProfileQuota(context.Background(), ReadGrokBuildProfileQuotaRequest{ProfileID: "active"})
		if err != nil || result.Status != test.want || result.Snapshot != nil {
			t.Fatalf("kind %s result = %#v, err = %v", test.kind, result, err)
		}
	}
}

func TestReadProfileQuotaDoesNotPersistRefreshedWorkingAuth(t *testing.T) {
	service := newQuotaTestService(t, immediateQuotaLock{})
	before := quotaCredentialPayload(t, service, "credential-active")
	refreshed := `{"login":{"key":"refreshed-synthetic-key","auth_mode":"web_login","create_time":"2026-08-02T00:00:00Z","user_id":"refreshed-synthetic-user","email":null}}`
	service.quotaReader = quotaReaderFunc(func(_ context.Context, home string) (grokquota.Snapshot, error) {
		if err := os.WriteFile(filepath.Join(home, grokconfig.AuthFileName), []byte(refreshed), 0o600); err != nil {
			t.Fatalf("simulate Grok auth refresh: %v", err)
		}
		return grokquota.Snapshot{FetchedAt: time.Unix(1_780_000_000, 0)}, nil
	})

	result, err := service.ReadProfileQuota(context.Background(), ReadGrokBuildProfileQuotaRequest{ProfileID: "active"})
	if err != nil || result.Status != GrokBuildProfileQuotaAvailable {
		t.Fatalf("quota result = %#v, err = %v", result, err)
	}
	if after := quotaCredentialPayload(t, service, "credential-active"); after != before {
		t.Fatal("quota read persisted Grok's refreshed working authentication")
	}
}

func TestReadProfileQuotaHoldsSharedLockUntilCanceled(t *testing.T) {
	lock := newSerialQuotaLock()
	service := newQuotaTestService(t, lock)
	readerStarted := make(chan struct{})
	service.quotaReader = quotaReaderFunc(func(ctx context.Context, _ string) (grokquota.Snapshot, error) {
		close(readerStarted)
		<-ctx.Done()
		return grokquota.Snapshot{}, ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	quotaDone := make(chan error, 1)
	go func() {
		_, err := service.ReadProfileQuota(ctx, ReadGrokBuildProfileQuotaRequest{ProfileID: "active"})
		quotaDone <- err
	}()
	<-readerStarted

	switchEntered := make(chan struct{})
	switchDone := make(chan error, 1)
	go func() {
		switchDone <- lock.RunWithSharedLock(context.Background(), "switch", func(context.Context) error {
			close(switchEntered)
			return nil
		})
	}()
	select {
	case <-switchEntered:
		t.Fatal("switch entered while the quota query held the shared lock")
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	if err := <-quotaDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("quota cancellation = %v", err)
	}
	select {
	case <-switchEntered:
	case <-time.After(time.Second):
		t.Fatal("shared lock was not released after quota cancellation")
	}
	if err := <-switchDone; err != nil {
		t.Fatalf("queued switch: %v", err)
	}
}

func TestReadProfileQuotaRejectsCustomAuthBeforeACPReader(t *testing.T) {
	t.Setenv("GROK_AUTH", `{"custom":true}`)
	service := newQuotaTestService(t, immediateQuotaLock{})
	var called atomic.Bool
	service.quotaReader = quotaReaderFunc(func(context.Context, string) (grokquota.Snapshot, error) {
		called.Store(true)
		return grokquota.Snapshot{}, nil
	})
	result, err := service.ReadProfileQuota(context.Background(), ReadGrokBuildProfileQuotaRequest{ProfileID: "active"})
	if err != nil || result.Status != GrokBuildProfileQuotaUnsupported {
		t.Fatalf("custom auth result = %#v, err = %v", result, err)
	}
	if called.Load() {
		t.Fatal("custom auth started the ACP reader")
	}
}

type quotaReaderFunc func(context.Context, string) (grokquota.Snapshot, error)

type failSecondGrokBuildPolicy struct {
	calls int
}

func (policy *failSecondGrokBuildPolicy) RequireAgent(context.Context, agent.ID) error {
	policy.calls++
	if policy.calls > 1 {
		return apperror.New(apperror.AgentDisabled, "Agent is disabled")
	}
	return nil
}

func (*failSecondGrokBuildPolicy) RequireProvider(context.Context, string) error {
	return nil
}

func (read quotaReaderFunc) Read(ctx context.Context, home string) (grokquota.Snapshot, error) {
	return read(ctx, home)
}

type quotaLockRunnerFunc func(context.Context, string, func(context.Context) error) error

func (run quotaLockRunnerFunc) RunWithSharedLock(ctx context.Context, operation string, callback func(context.Context) error) error {
	return run(ctx, operation, callback)
}

type immediateQuotaLock struct{}

func (immediateQuotaLock) RunWithSharedLock(ctx context.Context, _ string, run func(context.Context) error) error {
	return run(ctx)
}

type serialQuotaLock struct {
	once sync.Once
	gate chan struct{}
}

func newSerialQuotaLock() *serialQuotaLock {
	return &serialQuotaLock{gate: make(chan struct{}, 1)}
}

func (lock *serialQuotaLock) RunWithSharedLock(ctx context.Context, _ string, run func(context.Context) error) error {
	lock.once.Do(func() { lock.gate <- struct{}{} })
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-lock.gate:
	}
	defer func() { lock.gate <- struct{}{} }()
	return run(ctx)
}

func newQuotaTestService(t *testing.T, lock interface {
	RunWithSharedLock(context.Context, string, func(context.Context) error) error
},
) *Service {
	t.Helper()
	ctx := context.Background()
	home := t.TempDir()
	runtimeService, err := runtime.NewService(t.TempDir())
	if err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	if err := runtimeService.EnsureDirectories(); err != nil {
		t.Fatalf("initialize runtime directories: %v", err)
	}
	db, err := runtimeService.StoreFactory().Open(ctx, false)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if _, err := db.Migrate(ctx); err != nil {
		_ = db.Close()
		t.Fatalf("migrate database: %v", err)
	}
	resolvedHome, err := grokconfig.ResolveHome(home)
	if err != nil {
		_ = db.Close()
		t.Fatalf("resolve Grok Home: %v", err)
	}
	metadata, err := grokpreset.ProviderMetadataJSON(resolvedHome)
	if err != nil {
		_ = db.Close()
		t.Fatalf("encode Provider metadata: %v", err)
	}
	if _, err := grokprofile.UpsertProvider(ctx, db, metadata, false); err != nil {
		_ = db.Close()
		t.Fatalf("seed Provider: %v", err)
	}
	seedQuotaProfile(t, ctx, db, resolvedHome, "active", "credential-active", "config-active")
	seedQuotaProfile(t, ctx, db, resolvedHome, "inactive", "credential-inactive", "config-inactive")
	if _, err := db.SetProviderActiveState(ctx, grokconfig.ProviderID, "active"); err != nil {
		_ = db.Close()
		t.Fatalf("set active Profile: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}
	return NewService(runtimeService, nil, lock, nil, home)
}

func seedQuotaProfile(t *testing.T, ctx context.Context, db *store.Store, home grokconfig.Home, profileID, credentialID, configSetID string) {
	t.Helper()
	if _, err := grokprofile.UpsertConfigSet(ctx, db, configSetID, configSetID, "", ""); err != nil {
		t.Fatalf("seed Config Set %s: %v", configSetID, err)
	}
	auth := `{"login":{"key":"synthetic-key","auth_mode":"web_login","create_time":"2026-01-01T00:00:00Z","user_id":"synthetic-user","email":null}}`
	if _, err := grokprofile.UpsertAuthCredential(ctx, db, credentialID, auth); err != nil {
		t.Fatalf("seed credential %s: %v", credentialID, err)
	}
	if _, err := grokprofile.UpsertProfile(ctx, db, profileID, grokprofile.ProfileFields{CreateName: profileID}, false); err != nil {
		t.Fatalf("seed Profile %s: %v", profileID, err)
	}
	if _, err := grokprofile.UpsertConfigBinding(ctx, db, profileID, home, configSetID); err != nil {
		t.Fatalf("seed Config Set binding %s: %v", profileID, err)
	}
	if _, err := grokprofile.UpsertAuthBinding(ctx, db, profileID, home, credentialID); err != nil {
		t.Fatalf("seed credential binding %s: %v", profileID, err)
	}
}

func quotaCredentialPayload(t *testing.T, service *Service, credentialID string) string {
	t.Helper()
	db, err := service.openStore(context.Background(), true)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	credential, err := db.GetProviderCredential(context.Background(), credentialID)
	if err != nil {
		t.Fatalf("read credential %s: %v", credentialID, err)
	}
	return credential.PayloadJSON
}
