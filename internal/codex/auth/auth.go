package auth

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/strahe/profiledeck/internal/targetfs"
)

const maxAccountIDLength = 512

const maxAccessTokenLength = 64 * 1024

const (
	ManagedRefreshLeadTime = 5 * time.Minute
	ManagedRefreshFallback = 8 * 24 * time.Hour
)

var (
	ErrMissingAccessToken  = errors.New("Codex auth payload is missing tokens.access_token")
	ErrUnsupportedAuthMode = errors.New("Codex auth mode does not support ChatGPT quota lookup")
)

type Snapshot struct {
	Payload string
}

type BackendCredentials struct {
	AccessToken string
	AccountID   string
	FedRAMP     bool
}

type Mode string

const (
	ModeChatGPT           Mode = "chatgpt"
	ModeChatGPTAuthTokens Mode = "chatgptAuthTokens"
	ModeUnsupported       Mode = "unsupported"
)

type Info struct {
	Mode                 Mode
	QuotaSupported       bool
	RefreshSupported     bool
	HasAccessToken       bool
	HasRefreshToken      bool
	AccessTokenExpiresAt *time.Time
	LastRefreshAt        *time.Time
}

func (i Info) RefreshDueAt(now time.Time) (time.Time, bool) {
	if !i.RefreshSupported || !i.HasRefreshToken {
		return time.Time{}, false
	}
	if i.AccessTokenExpiresAt != nil {
		return i.AccessTokenExpiresAt.Add(-ManagedRefreshLeadTime), true
	}
	if i.LastRefreshAt != nil {
		return i.LastRefreshAt.Add(ManagedRefreshFallback), true
	}
	return now, true
}

type FieldError struct {
	Field string
	Err   error
}

func (e FieldError) Error() string {
	return e.Err.Error()
}

func (e FieldError) Unwrap() error {
	return e.Err
}

type SizeError struct {
	Size int
	Max  int
}

func (e SizeError) Error() string {
	return "Codex auth payload is too large"
}

func ReadSnapshot(path string) (Snapshot, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Snapshot{}, err
	}
	payload, object, err := decodePayload(raw)
	if err != nil {
		return Snapshot{}, err
	}
	if _, err := accountIDFromObject(object); err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Payload: payload}, nil
}

func NormalizePayload(raw []byte) (string, error) {
	payload, object, err := decodePayload(raw)
	if err != nil {
		return "", err
	}
	if _, err := accountIDFromObject(object); err != nil {
		return "", err
	}
	return payload, nil
}

func ExtractAccountID(raw []byte) (string, error) {
	_, object, err := decodePayload(raw)
	if err != nil {
		return "", err
	}
	return accountIDFromObject(object)
}

func ExtractBackendCredentials(raw []byte) (BackendCredentials, error) {
	_, object, err := decodePayload(raw)
	if err != nil {
		return BackendCredentials{}, err
	}
	mode := resolvedMode(object)
	if mode != ModeChatGPT && mode != ModeChatGPTAuthTokens {
		return BackendCredentials{}, ErrUnsupportedAuthMode
	}
	accountID, err := accountIDFromObject(object)
	if err != nil {
		return BackendCredentials{}, err
	}
	tokens, _ := object["tokens"].(map[string]any)
	accessToken, _ := tokens["access_token"].(string)
	accessToken = strings.TrimSpace(accessToken)
	if accessToken == "" {
		return BackendCredentials{}, ErrMissingAccessToken
	}
	if len(accessToken) > maxAccessTokenLength {
		return BackendCredentials{}, errors.New("Codex auth access token is too long")
	}
	for _, r := range accessToken {
		if unicode.IsControl(r) {
			return BackendCredentials{}, errors.New("Codex auth access token cannot contain control characters")
		}
	}
	return BackendCredentials{
		AccessToken: accessToken,
		AccountID:   accountID,
		FedRAMP:     fedRAMPFromIDToken(tokens),
	}, nil
}

func Inspect(raw []byte) (Info, error) {
	_, object, err := decodePayload(raw)
	if err != nil {
		return Info{}, err
	}
	info := Info{Mode: resolvedMode(object)}
	tokens, _ := object["tokens"].(map[string]any)
	accessToken, _ := tokens["access_token"].(string)
	accessToken = strings.TrimSpace(accessToken)
	info.HasAccessToken = accessToken != ""
	refreshToken, _ := tokens["refresh_token"].(string)
	info.HasRefreshToken = strings.TrimSpace(refreshToken) != ""
	_, accountErr := accountIDFromObject(object)
	info.QuotaSupported = (info.Mode == ModeChatGPT || info.Mode == ModeChatGPTAuthTokens) && info.HasAccessToken && accountErr == nil
	info.RefreshSupported = info.Mode == ModeChatGPT && info.HasRefreshToken
	if expiresAt, ok := accessTokenExpiry(accessToken); ok {
		info.AccessTokenExpiresAt = &expiresAt
	}
	if rawLastRefresh, ok := object["last_refresh"].(string); ok {
		if parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(rawLastRefresh)); err == nil {
			parsed = parsed.UTC()
			info.LastRefreshAt = &parsed
		}
	}
	return info, nil
}

func resolvedMode(object map[string]any) Mode {
	if rawMode, exists := object["auth_mode"]; exists && rawMode != nil {
		mode, ok := rawMode.(string)
		if !ok {
			return ModeUnsupported
		}
		switch mode {
		case string(ModeChatGPT):
			return ModeChatGPT
		case string(ModeChatGPTAuthTokens):
			return ModeChatGPTAuthTokens
		default:
			return ModeUnsupported
		}
	}
	// Match Codex's implicit auth-mode precedence so stale ChatGPT tokens are
	// never used when another login mechanism owns auth.json.
	for _, field := range []string{"OPENAI_API_KEY", "agent_identity", "personal_access_token", "bedrock_api_key"} {
		if value, exists := object[field]; exists && value != nil {
			return ModeUnsupported
		}
	}
	return ModeChatGPT
}

func accessTokenExpiry(token string) (time.Time, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[1] == "" {
		return time.Time{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, false
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var claims struct {
		ExpiresAt json.Number `json:"exp"`
	}
	if err := decoder.Decode(&claims); err != nil {
		return time.Time{}, false
	}
	expiresAt, err := claims.ExpiresAt.Int64()
	if err != nil || expiresAt <= 0 {
		return time.Time{}, false
	}
	return time.Unix(expiresAt, 0).UTC(), true
}

func fedRAMPFromIDToken(tokens map[string]any) bool {
	raw, ok := tokens["id_token"].(string)
	if !ok {
		return false
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	var claims struct {
		Auth struct {
			ChatGPTAccountIsFedRAMP bool `json:"chatgpt_account_is_fedramp"`
		} `json:"https://api.openai.com/auth"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return false
	}
	return claims.Auth.ChatGPTAccountIsFedRAMP
}

func NormalizeExternalAccountID(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", errors.New("Codex auth payload is missing tokens.account_id")
	}
	if len(value) > maxAccountIDLength {
		return "", errors.New("Codex auth account id is too long")
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return "", errors.New("Codex auth account id cannot contain control characters")
		}
	}
	return value, nil
}

func decodePayload(raw []byte) (string, map[string]any, error) {
	if len(raw) > targetfs.MaxFileBytes {
		return "", nil, SizeError{Size: len(raw), Max: targetfs.MaxFileBytes}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", nil, fmt.Errorf("Codex auth payload must be a JSON object: %w", err)
	}
	object, ok := value.(map[string]any)
	if !ok {
		return "", nil, errors.New("Codex auth payload must be a JSON object")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err != nil {
			return "", nil, fmt.Errorf("Codex auth payload must contain one JSON object: %w", err)
		}
		return "", nil, errors.New("Codex auth payload must contain one JSON object")
	}
	return string(raw), object, nil
}

func accountIDFromObject(object map[string]any) (string, error) {
	tokens, ok := object["tokens"].(map[string]any)
	if !ok {
		return "", errors.New("Codex auth payload is missing tokens.account_id")
	}
	raw, ok := tokens["account_id"].(string)
	if !ok || strings.TrimSpace(raw) == "" {
		return "", errors.New("Codex auth payload is missing tokens.account_id")
	}
	accountID, err := NormalizeExternalAccountID(raw)
	if err != nil {
		return "", FieldError{Field: "tokens.account_id", Err: err}
	}
	return accountID, nil
}
