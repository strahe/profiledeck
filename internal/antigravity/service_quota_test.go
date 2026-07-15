package antigravity

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	agyconfig "github.com/strahe/profiledeck/internal/antigravity/config"
	agyquota "github.com/strahe/profiledeck/internal/antigravity/quota"
	"github.com/strahe/profiledeck/internal/apperror"
)

type fakeAntigravityQuotaReader struct {
	calls  int
	tokens []string
	read   func(context.Context, string) (agyquota.Snapshot, error)
}

func (reader *fakeAntigravityQuotaReader) Read(ctx context.Context, token string) (agyquota.Snapshot, error) {
	reader.calls++
	reader.tokens = append(reader.tokens, token)
	if reader.read != nil {
		return reader.read(ctx, token)
	}
	return agyquota.Snapshot{}, &agyquota.Error{Kind: agyquota.ErrorUnavailable}
}

type trackingSharedLockRunner struct{ locked *bool }

func (runner trackingSharedLockRunner) RunWithSharedLock(ctx context.Context, _ string, run func(context.Context) error) error {
	*runner.locked = true
	defer func() { *runner.locked = false }()
	return run(ctx)
}

func TestAntigravityReadProfileQuotaCapturesCurrentKeyringIdentityUnderLock(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	client := &fakeKeyringClient{value: testAgyPayloadWithExpiry("stored-token-private", "stored-refresh", now.Add(time.Hour)), exists: true}
	configDir := t.TempDir()
	if _, err := initAntigravityTestRuntime(ctx, configDir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	environment := newAntigravityTestEnvironment(t, configDir, client)
	created, err := environment.antigravity.CreateProfile(ctx, CreateAntigravityProfileRequest{ProfileID: "work"})
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	client.value = testAgyPayloadWithExpiry("keyring-token-private", "keyring-refresh", now.Add(time.Hour))
	locked := false
	client.onGet = func() {
		if !locked {
			t.Fatal("Keyring was read outside the shared lock")
		}
	}
	reader := &fakeAntigravityQuotaReader{read: func(_ context.Context, token string) (agyquota.Snapshot, error) {
		if locked {
			t.Fatal("network reader ran while the shared lock was held")
		}
		if token != "keyring-token-private" {
			t.Fatalf("reader received saved database token instead of Keyring token: %q", token)
		}
		return agyquota.Snapshot{
			FetchedAt: now,
			Groups: []agyquota.Group{{DisplayName: "Models", Buckets: []agyquota.Bucket{{
				ID: "models-5h", Window: "5h", RemainingPercent: 80, ResetAtUnixSeconds: now.Add(time.Hour).Unix(),
			}}}},
		}, nil
	}}
	environment.antigravity.sharedLock = trackingSharedLockRunner{locked: &locked}
	environment.antigravity.quotaReader = reader
	environment.antigravity.now = func() time.Time { return now }

	result, err := environment.antigravity.ReadProfileQuota(ctx, "work")
	if err != nil {
		t.Fatalf("ReadProfileQuota: %v", err)
	}
	if result.Status != AntigravityProfileQuotaAvailable || result.ProfileID != "work" ||
		result.CredentialID != created.Summary.CredentialID || result.Snapshot == nil || len(result.Snapshot.Groups) != 1 {
		t.Fatalf("unexpected quota result %#v", result)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	for _, private := range []string{"stored-token-private", "stored-refresh", "keyring-token-private", "keyring-refresh", "access_token", "refresh_token"} {
		if strings.Contains(string(raw), private) {
			t.Fatalf("quota DTO exposed %q: %s", private, raw)
		}
	}

	db, err := openHealthyStore(ctx, configDir, true)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()
	credential, err := db.GetProviderCredential(ctx, created.Summary.CredentialID)
	if err != nil {
		t.Fatalf("read saved credential: %v", err)
	}
	if !strings.Contains(credential.PayloadJSON, "stored-token-private") || strings.Contains(credential.PayloadJSON, "keyring-token-private") {
		t.Fatalf("quota read wrote Keyring working copy into the database")
	}
}

func TestAntigravityReadProfileQuotaDoesNotTouchInactiveProfileKeyringOrNetwork(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	configDir := t.TempDir()
	if _, err := initAntigravityTestRuntime(ctx, configDir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	client := &fakeKeyringClient{value: testAgyPayloadWithExpiry("first", "first-refresh", now.Add(time.Hour)), exists: true}
	environment := newAntigravityTestEnvironment(t, configDir, client)
	first, err := environment.antigravity.CreateProfile(ctx, CreateAntigravityProfileRequest{ProfileID: "first"})
	if err != nil {
		t.Fatalf("Create first: %v", err)
	}
	client.value = testAgyPayloadWithExpiry("second", "second-refresh", now.Add(time.Hour))
	if _, err := environment.antigravity.CreateProfile(ctx, CreateAntigravityProfileRequest{ProfileID: "second"}); err != nil {
		t.Fatalf("Create second: %v", err)
	}
	reader := &fakeAntigravityQuotaReader{}
	environment.antigravity.quotaReader = reader
	keyringCalls := client.getCalls
	result, err := environment.antigravity.ReadProfileQuota(ctx, "first")
	if err != nil {
		t.Fatalf("ReadProfileQuota: %v", err)
	}
	if result.Status != AntigravityProfileQuotaInactive || result.CredentialID != first.Summary.CredentialID ||
		result.Snapshot != nil || client.getCalls != keyringCalls || reader.calls != 0 {
		t.Fatalf("inactive read touched runtime state: result=%#v keyringCalls=%d readerCalls=%d", result, client.getCalls-keyringCalls, reader.calls)
	}
}

func TestAntigravityReadProfileQuotaRequiresFreshCurrentToken(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	configDir := t.TempDir()
	if _, err := initAntigravityTestRuntime(ctx, configDir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	client := &fakeKeyringClient{value: testAgyPayloadWithExpiry("valid", "refresh", now.Add(time.Hour)), exists: true}
	environment := newAntigravityTestEnvironment(t, configDir, client)
	if _, err := environment.antigravity.CreateProfile(ctx, CreateAntigravityProfileRequest{ProfileID: "work"}); err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	environment.antigravity.now = func() time.Time { return now }
	for _, test := range []struct {
		name   string
		value  string
		exists bool
	}{
		{name: "missing", exists: false},
		{name: "invalid", value: `{"token":{"access_token":"private"}}`, exists: true},
		{name: "expires in thirty seconds", value: testAgyPayloadWithExpiry("private", "refresh", now.Add(30*time.Second)), exists: true},
		{name: "expired", value: testAgyPayloadWithExpiry("private", "refresh", now.Add(-time.Second)), exists: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			client.value, client.exists = test.value, test.exists
			reader := &fakeAntigravityQuotaReader{}
			environment.antigravity.quotaReader = reader
			result, err := environment.antigravity.ReadProfileQuota(ctx, "work")
			if err != nil || result.Status != AntigravityProfileQuotaAuthRequired || result.Snapshot != nil || reader.calls != 0 {
				t.Fatalf("expected auth required without network, result=%#v err=%v calls=%d", result, err, reader.calls)
			}
		})
	}
}

func TestAntigravityReadProfileQuotaMapsReaderFailuresWithoutDetails(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	configDir := t.TempDir()
	if _, err := initAntigravityTestRuntime(ctx, configDir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	client := &fakeKeyringClient{value: testAgyPayloadWithExpiry("private", "refresh", now.Add(time.Hour)), exists: true}
	environment := newAntigravityTestEnvironment(t, configDir, client)
	if _, err := environment.antigravity.CreateProfile(ctx, CreateAntigravityProfileRequest{ProfileID: "work"}); err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	environment.antigravity.now = func() time.Time { return now }
	for _, test := range []struct {
		kind agyquota.ErrorKind
		want AntigravityProfileQuotaStatus
	}{
		{kind: agyquota.ErrorAuthRequired, want: AntigravityProfileQuotaAuthRequired},
		{kind: agyquota.ErrorUnavailable, want: AntigravityProfileQuotaUnavailable},
	} {
		environment.antigravity.quotaReader = &fakeAntigravityQuotaReader{read: func(context.Context, string) (agyquota.Snapshot, error) {
			return agyquota.Snapshot{}, &agyquota.Error{Kind: test.kind}
		}}
		result, err := environment.antigravity.ReadProfileQuota(ctx, "work")
		if err != nil || result.Status != test.want || result.Snapshot != nil {
			t.Fatalf("expected %q, result=%#v err=%v", test.want, result, err)
		}
	}
}

func TestAntigravityReadProfileQuotaValidatesProfileAndBinding(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := initAntigravityTestRuntime(ctx, configDir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	client := &fakeKeyringClient{value: testAgyPayloadWithExpiry("private", "refresh", time.Now().Add(time.Hour)), exists: true}
	environment := newAntigravityTestEnvironment(t, configDir, client)
	if _, err := environment.antigravity.CreateProfile(ctx, CreateAntigravityProfileRequest{ProfileID: "work"}); err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	_, err := environment.antigravity.ReadProfileQuota(ctx, "missing")
	assertErrorCode(t, err, apperror.ProfileNotFound)

	db, err := openHealthyStore(ctx, configDir, false)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := db.DeleteProfileCredentialBinding(ctx, "work", agyconfig.ProviderID, agyconfig.CredentialSlot); err != nil {
		_ = db.Close()
		t.Fatalf("delete binding: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	_, err = environment.antigravity.ReadProfileQuota(ctx, "work")
	assertErrorCode(t, err, apperror.ProfileNotFound)
}

func testAgyPayloadWithExpiry(access, refresh string, expiry time.Time) string {
	return fmt.Sprintf(
		`{"token":{"access_token":%q,"token_type":"Bearer","refresh_token":%q,"expiry":%q},"auth_method":"consumer"}`,
		access, refresh, expiry.UTC().Format(time.RFC3339Nano),
	)
}
