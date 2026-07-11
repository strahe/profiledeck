package auth

import (
	"encoding/base64"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/strahe/profiledeck/internal/targetfs"
)

func TestNormalizePayloadAcceptsCodexAuthObject(t *testing.T) {
	raw := []byte(`{"tokens":{"account_id":" Team/Shared ","access_token":"secret"}}`)

	payload, err := NormalizePayload(raw)
	if err != nil {
		t.Fatalf("expected auth payload to normalize, got %v", err)
	}
	if payload != string(raw) {
		t.Fatalf("expected payload to preserve raw JSON, got %q", payload)
	}
	accountID, err := ExtractAccountID(raw)
	if err != nil {
		t.Fatalf("expected account id to extract, got %v", err)
	}
	if accountID != "Team/Shared" {
		t.Fatalf("expected trimmed Codex account id, got %q", accountID)
	}
}

func TestNormalizePayloadRejectsInvalidShapes(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{name: "invalid json", raw: `{`},
		{name: "non object", raw: `[]`},
		{name: "multiple values", raw: `{"tokens":{"account_id":"a"}} {}`},
		{name: "missing account", raw: `{"tokens":{"access_token":"secret"}}`},
		{name: "control character account", raw: "{\"tokens\":{\"account_id\":\"bad\\nid\"}}"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NormalizePayload([]byte(tc.raw)); err == nil {
				t.Fatalf("expected invalid auth payload to fail")
			}
		})
	}
}

func TestNormalizePayloadRejectsOversizedPayload(t *testing.T) {
	raw := []byte(strings.Repeat("x", targetfs.MaxFileBytes+1))
	if _, err := NormalizePayload(raw); err == nil {
		t.Fatalf("expected oversized auth payload to fail")
	}
}

func TestExtractBackendCredentialsAcceptsChatGPTTokens(t *testing.T) {
	claims := base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_is_fedramp":true}}`))
	raw := `{"auth_mode":"chatgpt","tokens":{"account_id":" Team/Shared ","access_token":" jwt-token ","id_token":"e30.` + claims + `.signature"}}`
	credentials, err := ExtractBackendCredentials([]byte(raw))
	if err != nil {
		t.Fatalf("expected backend credentials to extract, got %v", err)
	}
	if credentials.AccountID != "Team/Shared" || credentials.AccessToken != "jwt-token" || !credentials.FedRAMP {
		t.Fatalf("unexpected backend credentials: %#v", credentials)
	}
}

func TestExtractBackendCredentialsTreatsMissingOrMalformedFedRAMPClaimAsFalse(t *testing.T) {
	for _, idToken := range []string{"", "not-a-jwt"} {
		raw := `{"tokens":{"account_id":"work","access_token":"token","id_token":"` + idToken + `"}}`
		credentials, err := ExtractBackendCredentials([]byte(raw))
		if err != nil {
			t.Fatalf("expected backend credentials to extract, got %v", err)
		}
		if credentials.FedRAMP {
			t.Fatalf("expected malformed or missing claim not to enable FedRAMP routing")
		}
	}
}

func TestExtractBackendCredentialsRejectsUnsupportedOrUnsafeTokens(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want error
	}{
		{name: "unsupported auth", raw: `{"auth_mode":"apikey","tokens":{"account_id":"work","access_token":"secret"}}`, want: ErrUnsupportedAuthMode},
		{name: "invalid auth mode type", raw: `{"auth_mode":42,"tokens":{"account_id":"work","access_token":"secret"}}`, want: ErrUnsupportedAuthMode},
		{name: "implicit api key auth", raw: `{"OPENAI_API_KEY":"key","tokens":{"account_id":"work","access_token":"secret"}}`, want: ErrUnsupportedAuthMode},
		{name: "implicit personal access token auth", raw: `{"personal_access_token":"pat","tokens":{"account_id":"work","access_token":"secret"}}`, want: ErrUnsupportedAuthMode},
		{name: "missing token", raw: `{"tokens":{"account_id":"work"}}`, want: ErrMissingAccessToken},
		{name: "control character", raw: `{"tokens":{"account_id":"work","access_token":"bad\nvalue"}}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ExtractBackendCredentials([]byte(tc.raw))
			if err == nil {
				t.Fatal("expected credential extraction to fail")
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("expected %v, got %v", tc.want, err)
			}
			if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "bad") {
				t.Fatalf("expected credential error to stay redacted, got %v", err)
			}
		})
	}
}

func TestReadSnapshotPreservesMissingFileError(t *testing.T) {
	_, err := ReadSnapshot(filepath.Join(t.TempDir(), "missing-auth.json"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("expected missing auth error to preserve fs.ErrNotExist, got %v", err)
	}
}

func TestReadSnapshotReturnsPayload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	raw := `{"tokens":{"account_id":"work-account","access_token":"secret"}}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("expected auth setup to succeed, got %v", err)
	}

	snapshot, err := ReadSnapshot(path)
	if err != nil {
		t.Fatalf("expected auth snapshot to read, got %v", err)
	}
	if snapshot.Payload != raw {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
}

func TestInspectSchedulesManagedRefreshFromAccessTokenExpiry(t *testing.T) {
	expiresAt := time.Unix(1780003600, 0).UTC()
	claims := base64.RawURLEncoding.EncodeToString([]byte(`{"exp":1780003600}`))
	raw := `{"auth_mode":"chatgpt","tokens":{"account_id":"display","access_token":"e30.` + claims + `.signature","refresh_token":"refresh"},"last_refresh":"2026-01-01T00:00:00Z"}`
	info, err := Inspect([]byte(raw))
	if err != nil {
		t.Fatalf("expected auth inspection, got %v", err)
	}
	if info.Mode != ModeChatGPT || !info.QuotaSupported || !info.RefreshSupported || info.AccessTokenExpiresAt == nil || !info.AccessTokenExpiresAt.Equal(expiresAt) {
		t.Fatalf("unexpected managed auth info: %#v", info)
	}
	dueAt, ok := info.RefreshDueAt(time.Unix(1780000000, 0))
	if !ok || !dueAt.Equal(expiresAt.Add(-5*time.Minute)) {
		t.Fatalf("expected exp minus five minutes, got %s, %v", dueAt, ok)
	}
}

func TestInspectFallsBackToLastRefreshAndRejectsExternalKeepalive(t *testing.T) {
	lastRefresh := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	managed := `{"tokens":{"account_id":"display","access_token":"opaque","refresh_token":"refresh"},"last_refresh":"2026-07-01T12:00:00Z"}`
	info, err := Inspect([]byte(managed))
	if err != nil {
		t.Fatalf("expected managed auth inspection, got %v", err)
	}
	dueAt, ok := info.RefreshDueAt(lastRefresh)
	if !ok || !dueAt.Equal(lastRefresh.Add(8*24*time.Hour)) {
		t.Fatalf("expected eight day fallback, got %s, %v", dueAt, ok)
	}

	external := `{"auth_mode":"chatgptAuthTokens","tokens":{"account_id":"display","access_token":"opaque","refresh_token":"ignored"}}`
	info, err = Inspect([]byte(external))
	if err != nil {
		t.Fatalf("expected external auth inspection, got %v", err)
	}
	if info.Mode != ModeChatGPTAuthTokens || !info.QuotaSupported || info.RefreshSupported {
		t.Fatalf("expected quota-only external auth, got %#v", info)
	}
	if _, ok := info.RefreshDueAt(lastRefresh); ok {
		t.Fatal("expected external auth not to schedule native refresh")
	}
}
