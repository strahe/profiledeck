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

func TestNormalizePayloadAcceptsSupportedFileAuthModes(t *testing.T) {
	agentIdentityRecord := `{"agent_runtime_id":"runtime","agent_private_key":"synthetic","account_id":"account","chatgpt_user_id":"user","plan_type":"enterprise","chatgpt_account_is_fedramp":false}`
	agentIdentityJWT := syntheticAgentIdentityJWT()
	cases := []struct {
		name      string
		raw       string
		wantMode  Mode
		accountID string
	}{
		{name: "legacy ChatGPT", raw: `{"tokens":{"access_token":"secret"}}`, wantMode: ModeChatGPT},
		{name: "managed ChatGPT", raw: `{"auth_mode":"chatgpt","tokens":{"account_id":" Team/Shared ","access_token":"secret"}}`, wantMode: ModeChatGPT, accountID: "Team/Shared"},
		{name: "external ChatGPT", raw: `{"auth_mode":"chatgptAuthTokens","tokens":{"access_token":"secret"}}`, wantMode: ModeChatGPTAuthTokens},
		{name: "API key", raw: `{"auth_mode":"apikey","OPENAI_API_KEY":"sk-synthetic"}`, wantMode: ModeAPIKey},
		{name: "implicit API key", raw: `{"OPENAI_API_KEY":"sk-synthetic"}`, wantMode: ModeAPIKey},
		{name: "agent identity record", raw: `{"auth_mode":"agentIdentity","agent_identity":` + agentIdentityRecord + `}`, wantMode: ModeAgentIdentity},
		{name: "agent identity JWT", raw: `{"auth_mode":"agentIdentity","agent_identity":"` + agentIdentityJWT + `"}`, wantMode: ModeAgentIdentity},
		{name: "personal access token", raw: `{"auth_mode":"personalAccessToken","personal_access_token":"pat-synthetic"}`, wantMode: ModePersonalAccessToken},
		{name: "implicit personal access token", raw: `{"personal_access_token":"pat-synthetic"}`, wantMode: ModePersonalAccessToken},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload, err := NormalizePayload([]byte(tc.raw))
			if err != nil {
				t.Fatalf("expected auth payload to normalize, got %v", err)
			}
			if payload != tc.raw {
				t.Fatalf("expected payload to preserve raw JSON, got %q", payload)
			}
			accountID, err := ExtractAccountID([]byte(tc.raw))
			if err != nil || accountID != tc.accountID {
				t.Fatalf("unexpected account metadata: %q, %v", accountID, err)
			}
			info, err := Inspect([]byte(tc.raw))
			if err != nil || info.Mode != tc.wantMode {
				t.Fatalf("unexpected auth info: %#v, %v", info, err)
			}
		})
	}
}

func TestInspectTreatsUnusableAccountMetadataAsAbsent(t *testing.T) {
	for _, tc := range []struct {
		name      string
		accountID string
	}{
		{name: "null", accountID: "null"},
		{name: "blank", accountID: `" "`},
		{name: "unsafe", accountID: `"bad\nid"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := `{"auth_mode":"chatgpt","tokens":{"account_id":` + tc.accountID + `,"access_token":"token","refresh_token":"refresh"}}`
			if _, err := NormalizePayload([]byte(raw)); err != nil {
				t.Fatalf("expected optional account metadata not to invalidate auth, got %v", err)
			}
			if accountID, err := ExtractAccountID([]byte(raw)); err != nil || accountID != "" {
				t.Fatalf("expected unusable account metadata to be omitted, got %q, %v", accountID, err)
			}
			info, err := Inspect([]byte(raw))
			if err != nil || !info.QuotaSupported || !info.RefreshSupported {
				t.Fatalf("expected native capabilities to ignore optional account metadata, got %#v, %v", info, err)
			}
			if _, err := ExtractBackendCredentials([]byte(raw)); !errors.Is(err, ErrMissingQuotaAccountID) {
				t.Fatalf("expected direct quota fallback to require account metadata, got %v", err)
			}
		})
	}
}

func TestNormalizePayloadRejectsInvalidShapes(t *testing.T) {
	cases := []struct {
		name       string
		raw        string
		secretText string
	}{
		{name: "invalid json", raw: `{`},
		{name: "non object", raw: `[]`},
		{name: "multiple values", raw: `{"tokens":{"access_token":"secret"}} {}`},
		{name: "missing access token", raw: `{"tokens":{"account_id":"a"}}`},
		{name: "empty access token", raw: `{"tokens":{"access_token":" "}}`},
		{name: "invalid account type", raw: `{"tokens":{"account_id":42,"access_token":"secret"}}`},
		{name: "invalid auth mode type", raw: `{"auth_mode":42,"tokens":{"access_token":"secret"}}`},
		{name: "unknown auth mode", raw: `{"auth_mode":"future","tokens":{"access_token":"secret"}}`},
		{name: "headers auth mode", raw: `{"auth_mode":"headers"}`},
		{name: "Bedrock auth mode", raw: `{"auth_mode":"bedrockApiKey","bedrock_api_key":{"api_key":"secret"}}`},
		{name: "implicit Bedrock", raw: `{"bedrock_api_key":{"api_key":"secret"}}`},
		{name: "implicit null API key", raw: `{"OPENAI_API_KEY":null,"tokens":{"access_token":"secret"}}`},
		{name: "implicit null agent identity", raw: `{"agent_identity":null,"tokens":{"access_token":"secret"}}`},
		{name: "implicit null personal access token", raw: `{"personal_access_token":null,"tokens":{"access_token":"secret"}}`},
		{name: "implicit null Bedrock", raw: `{"bedrock_api_key":null,"tokens":{"access_token":"secret"}}`},
		{name: "missing API key", raw: `{"auth_mode":"apikey"}`},
		{name: "empty API key", raw: `{"auth_mode":"apikey","OPENAI_API_KEY":" "}`},
		{name: "invalid API key type", raw: `{"auth_mode":"apikey","OPENAI_API_KEY":42}`},
		{name: "control character API key", raw: "{\"auth_mode\":\"apikey\",\"OPENAI_API_KEY\":\"api-secret\\nvalue\"}", secretText: "api-secret"},
		{name: "missing agent identity", raw: `{"auth_mode":"agentIdentity"}`},
		{name: "empty agent identity", raw: `{"auth_mode":"agentIdentity","agent_identity":{}}`},
		{name: "empty agent identity JWT", raw: `{"auth_mode":"agentIdentity","agent_identity":" "}`},
		{name: "agent identity JWT missing claims", raw: `{"auth_mode":"agentIdentity","agent_identity":"` + syntheticAgentIdentityJWTWithClaims(`{}`) + `"}`},
		{name: "invalid agent identity type", raw: `{"auth_mode":"agentIdentity","agent_identity":42}`},
		{name: "incomplete agent identity record", raw: `{"auth_mode":"agentIdentity","agent_identity":{"agent_runtime_id":"runtime","agent_private_key":"agent-secret"}}`, secretText: "agent-secret"},
		{name: "missing personal access token", raw: `{"auth_mode":"personalAccessToken"}`},
		{name: "empty personal access token", raw: `{"auth_mode":"personalAccessToken","personal_access_token":" "}`},
		{name: "invalid personal access token type", raw: `{"auth_mode":"personalAccessToken","personal_access_token":42}`},
		{name: "control character personal access token", raw: "{\"auth_mode\":\"personalAccessToken\",\"personal_access_token\":\"pat-secret\\nvalue\"}", secretText: "pat-secret"},
		{name: "invalid refresh token type", raw: `{"tokens":{"access_token":"secret","refresh_token":42}}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NormalizePayload([]byte(tc.raw)); err == nil {
				t.Fatalf("expected invalid auth payload to fail")
			} else if tc.secretText != "" && strings.Contains(err.Error(), tc.secretText) {
				t.Fatalf("expected credential error to stay redacted, got %v", err)
			}
		})
	}
}

func TestNormalizePayloadUsesExplicitModeOverStaleFields(t *testing.T) {
	raw := `{"auth_mode":"apikey","OPENAI_API_KEY":"sk-synthetic","tokens":{"access_token":42},"personal_access_token":42}`
	if payload, err := NormalizePayload([]byte(raw)); err != nil || payload != raw {
		t.Fatalf("expected explicit API key mode to ignore stale fields, payload=%q err=%v", payload, err)
	}
}

func TestNormalizePayloadRejectsOversizedPayload(t *testing.T) {
	raw := []byte(strings.Repeat("x", targetfs.MaxFileBytes+1))
	if _, err := NormalizePayload(raw); err == nil {
		t.Fatalf("expected oversized auth payload to fail")
	}
}

func TestNormalizePayloadRejectsOversizedCredential(t *testing.T) {
	raw := `{"auth_mode":"apikey","OPENAI_API_KEY":"` + strings.Repeat("x", maxCredentialLength+1) + `"}`
	if _, err := NormalizePayload([]byte(raw)); err == nil {
		t.Fatal("expected oversized credential to fail")
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
		{name: "API key auth", raw: `{"auth_mode":"apikey","OPENAI_API_KEY":"key"}`, want: ErrUnsupportedAuthMode},
		{name: "implicit API key auth", raw: `{"OPENAI_API_KEY":"key","tokens":{"account_id":"work","access_token":"secret"}}`, want: ErrUnsupportedAuthMode},
		{name: "implicit personal access token auth", raw: `{"personal_access_token":"pat","tokens":{"account_id":"work","access_token":"secret"}}`, want: ErrUnsupportedAuthMode},
		{name: "unsupported stored mode", raw: `{"auth_mode":"headers"}`, want: ErrUnsupportedStoredAuthMode},
		{name: "missing token", raw: `{"tokens":{"account_id":"work"}}`, want: ErrMissingAccessToken},
		{name: "missing direct quota account", raw: `{"tokens":{"access_token":"secret"}}`, want: ErrMissingQuotaAccountID},
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
	raw := `{"auth_mode":"apikey","OPENAI_API_KEY":"sk-synthetic"}`
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

func TestInspectDisablesChatGPTCapabilitiesForOtherSupportedModes(t *testing.T) {
	agentIdentityRecord := `{"agent_runtime_id":"runtime","agent_private_key":"synthetic","account_id":"account","chatgpt_user_id":"user","plan_type":"enterprise","chatgpt_account_is_fedramp":false}`
	cases := []struct {
		name string
		raw  string
		mode Mode
	}{
		{name: "API key", raw: `{"auth_mode":"apikey","OPENAI_API_KEY":"sk-synthetic"}`, mode: ModeAPIKey},
		{name: "agent identity", raw: `{"auth_mode":"agentIdentity","agent_identity":` + agentIdentityRecord + `}`, mode: ModeAgentIdentity},
		{name: "personal access token", raw: `{"auth_mode":"personalAccessToken","personal_access_token":"pat-synthetic"}`, mode: ModePersonalAccessToken},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info, err := Inspect([]byte(tc.raw))
			if err != nil {
				t.Fatalf("expected supported auth inspection, got %v", err)
			}
			if info.Mode != tc.mode || info.QuotaSupported || info.RefreshSupported || info.HasAccessToken || info.HasRefreshToken {
				t.Fatalf("unexpected non-ChatGPT auth capabilities: %#v", info)
			}
		})
	}
}

func syntheticAgentIdentityJWT() string {
	return syntheticAgentIdentityJWTWithClaims(`{"iss":"https://chatgpt.com/codex-backend/agent-identity","aud":"codex-app-server","iat":1700000000,"exp":4000000000,"agent_runtime_id":"runtime","agent_private_key":"synthetic","account_id":"account","chatgpt_user_id":"user","plan_type":"enterprise","chatgpt_account_is_fedramp":false}`)
}

func syntheticAgentIdentityJWTWithClaims(rawClaims string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","kid":"test-key"}`))
	claims := base64.RawURLEncoding.EncodeToString([]byte(rawClaims))
	signature := base64.RawURLEncoding.EncodeToString([]byte("signature"))
	return header + "." + claims + "." + signature
}
