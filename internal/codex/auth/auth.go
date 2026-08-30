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

const maxCredentialLength = 64 * 1024

const (
	ManagedRefreshLeadTime = 5 * time.Minute
	ManagedRefreshFallback = 8 * 24 * time.Hour
)

var (
	ErrMissingAccessToken        = errors.New("Codex auth payload is missing tokens.access_token")
	ErrMissingQuotaAccountID     = errors.New("Codex auth payload is missing account metadata required for direct quota lookup")
	ErrUnsupportedAuthMode       = errors.New("Codex auth mode does not support ChatGPT quota lookup")
	ErrUnsupportedStoredAuthMode = errors.New("Codex auth file uses an unsupported sign-in method")
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
	ModeChatGPT             Mode = "chatgpt"
	ModeChatGPTAuthTokens   Mode = "chatgptAuthTokens"
	ModeAPIKey              Mode = "apikey"
	ModeAgentIdentity       Mode = "agentIdentity"
	ModePersonalAccessToken Mode = "personalAccessToken"
	ModeUnsupported         Mode = "unsupported"
)

type parsedPayload struct {
	Payload      string
	Object       map[string]any
	Tokens       map[string]any
	Mode         Mode
	AccessToken  string
	RefreshToken string
	AccountID    string
	APIKey       string
}

type agentIdentityRecord struct {
	AgentRuntimeID          string  `json:"agent_runtime_id"`
	AgentPrivateKey         string  `json:"agent_private_key"`
	AccountID               string  `json:"account_id"`
	ChatGPTUserID           string  `json:"chatgpt_user_id"`
	Email                   *string `json:"email"`
	PlanType                string  `json:"plan_type"`
	ChatGPTAccountIsFedRAMP *bool   `json:"chatgpt_account_is_fedramp"`
	TaskID                  *string `json:"task_id"`
}

type agentIdentityJWTClaims struct {
	Issuer    string  `json:"iss"`
	Audience  string  `json:"aud"`
	IssuedAt  *uint64 `json:"iat"`
	ExpiresAt *uint64 `json:"exp"`
}

type agentIdentityJWTHeader struct {
	Algorithm string `json:"alg"`
	KeyID     string `json:"kid"`
}

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
	parsed, err := parsePayload(raw)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Payload: parsed.Payload}, nil
}

func NormalizePayload(raw []byte) (string, error) {
	parsed, err := parsePayload(raw)
	if err != nil {
		return "", err
	}
	return parsed.Payload, nil
}

func ExtractAccountID(raw []byte) (string, error) {
	parsed, err := parsePayload(raw)
	if err != nil {
		return "", err
	}
	return parsed.AccountID, nil
}

func ExtractBackendCredentials(raw []byte) (BackendCredentials, error) {
	parsed, err := parsePayload(raw)
	if err != nil {
		return BackendCredentials{}, err
	}
	if parsed.Mode != ModeChatGPT && parsed.Mode != ModeChatGPTAuthTokens {
		return BackendCredentials{}, ErrUnsupportedAuthMode
	}
	if parsed.AccountID == "" {
		return BackendCredentials{}, ErrMissingQuotaAccountID
	}
	return BackendCredentials{
		AccessToken: parsed.AccessToken,
		AccountID:   parsed.AccountID,
		FedRAMP:     fedRAMPFromIDToken(parsed.Tokens),
	}, nil
}

func ExtractAPIKey(raw []byte) (string, error) {
	parsed, err := parsePayload(raw)
	if err != nil {
		return "", err
	}
	if parsed.Mode != ModeAPIKey {
		return "", ErrUnsupportedAuthMode
	}
	return parsed.APIKey, nil
}

func Inspect(raw []byte) (Info, error) {
	parsed, err := parsePayload(raw)
	if err != nil {
		return Info{}, err
	}
	info := Info{Mode: parsed.Mode}
	info.HasAccessToken = parsed.AccessToken != ""
	info.HasRefreshToken = parsed.RefreshToken != ""
	info.QuotaSupported = (info.Mode == ModeChatGPT || info.Mode == ModeChatGPTAuthTokens) && info.HasAccessToken
	info.RefreshSupported = info.Mode == ModeChatGPT && info.HasRefreshToken
	if expiresAt, ok := accessTokenExpiry(parsed.AccessToken); ok {
		info.AccessTokenExpiresAt = &expiresAt
	}
	if rawLastRefresh, ok := parsed.Object["last_refresh"].(string); ok {
		if parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(rawLastRefresh)); err == nil {
			parsed = parsed.UTC()
			info.LastRefreshAt = &parsed
		}
	}
	return info, nil
}

func parsePayload(raw []byte) (parsedPayload, error) {
	payload, object, err := decodePayload(raw)
	if err != nil {
		return parsedPayload{}, err
	}
	mode, err := resolvedMode(object)
	if err != nil {
		return parsedPayload{}, err
	}
	parsed := parsedPayload{Payload: payload, Object: object, Mode: mode}
	switch mode {
	case ModeChatGPT, ModeChatGPTAuthTokens:
		tokens, ok := object["tokens"].(map[string]any)
		if !ok {
			return parsedPayload{}, FieldError{Field: "tokens.access_token", Err: ErrMissingAccessToken}
		}
		parsed.Tokens = tokens
		parsed.AccessToken, err = requiredSafeString(tokens, "access_token", "tokens.access_token", ErrMissingAccessToken)
		if err != nil {
			return parsedPayload{}, err
		}
		parsed.RefreshToken, err = optionalSafeString(tokens, "refresh_token", "tokens.refresh_token")
		if err != nil {
			return parsedPayload{}, err
		}
		parsed.AccountID, err = optionalAccountID(tokens)
		if err != nil {
			return parsedPayload{}, err
		}
	case ModeAPIKey:
		parsed.APIKey, err = requiredSafeString(object, "OPENAI_API_KEY", "OPENAI_API_KEY", errors.New("Codex auth payload is missing OPENAI_API_KEY"))
		if err != nil {
			return parsedPayload{}, err
		}
	case ModeAgentIdentity:
		if err := validateAgentIdentity(object); err != nil {
			return parsedPayload{}, err
		}
	case ModePersonalAccessToken:
		if _, err := requiredSafeString(object, "personal_access_token", "personal_access_token", errors.New("Codex auth payload is missing personal_access_token")); err != nil {
			return parsedPayload{}, err
		}
	default:
		return parsedPayload{}, ErrUnsupportedStoredAuthMode
	}
	return parsed, nil
}

func resolvedMode(object map[string]any) (Mode, error) {
	if rawMode, exists := object["auth_mode"]; exists {
		mode, ok := rawMode.(string)
		if !ok {
			return ModeUnsupported, ErrUnsupportedStoredAuthMode
		}
		switch mode {
		case string(ModeChatGPT):
			return ModeChatGPT, nil
		case string(ModeChatGPTAuthTokens):
			return ModeChatGPTAuthTokens, nil
		case string(ModeAPIKey):
			return ModeAPIKey, nil
		case string(ModeAgentIdentity):
			return ModeAgentIdentity, nil
		case string(ModePersonalAccessToken):
			return ModePersonalAccessToken, nil
		default:
			return ModeUnsupported, ErrUnsupportedStoredAuthMode
		}
	}
	// Match Codex's implicit auth-mode precedence so stale ChatGPT tokens are
	// never used when another login mechanism owns auth.json.
	for _, candidate := range []struct {
		field string
		mode  Mode
	}{
		{field: "OPENAI_API_KEY", mode: ModeAPIKey},
		{field: "agent_identity", mode: ModeAgentIdentity},
		{field: "personal_access_token", mode: ModePersonalAccessToken},
		{field: "bedrock_api_key", mode: ModeUnsupported},
	} {
		field := candidate.field
		if _, exists := object[field]; exists {
			if candidate.mode == ModeUnsupported {
				return ModeUnsupported, ErrUnsupportedStoredAuthMode
			}
			return candidate.mode, nil
		}
	}
	return ModeChatGPT, nil
}

func requiredSafeString(object map[string]any, key, field string, missing error) (string, error) {
	raw, ok := object[key].(string)
	if !ok || strings.TrimSpace(raw) == "" {
		return "", FieldError{Field: field, Err: missing}
	}
	value := strings.TrimSpace(raw)
	if len(value) > maxCredentialLength {
		return "", FieldError{Field: field, Err: errors.New("Codex auth credential is too long")}
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return "", FieldError{Field: field, Err: errors.New("Codex auth credential cannot contain control characters")}
		}
	}
	return value, nil
}

func optionalSafeString(object map[string]any, key, field string) (string, error) {
	value, exists := object[key]
	if !exists || value == nil {
		return "", nil
	}
	return requiredSafeString(object, key, field, errors.New("Codex auth credential is empty"))
}

func validateAgentIdentity(object map[string]any) error {
	value, exists := object["agent_identity"]
	if !exists || value == nil {
		return FieldError{Field: "agent_identity", Err: errors.New("Codex auth payload is missing agent_identity")}
	}
	switch value := value.(type) {
	case string:
		jwt, err := requiredSafeString(object, "agent_identity", "agent_identity", errors.New("Codex auth payload is missing agent_identity"))
		if err != nil {
			return err
		}
		if jwt != value {
			return invalidAgentIdentityError()
		}
		return validateAgentIdentityJWT(jwt)
	case map[string]any:
		raw, err := json.Marshal(value)
		if err != nil {
			return invalidAgentIdentityError()
		}
		return validateAgentIdentityRecord(raw)
	default:
		return invalidAgentIdentityError()
	}
}

func validateAgentIdentityJWT(jwt string) error {
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		return invalidAgentIdentityError()
	}
	decoded := make([][]byte, len(parts))
	for index, part := range parts {
		if part == "" {
			return invalidAgentIdentityError()
		}
		value, err := base64.RawURLEncoding.DecodeString(part)
		if err != nil {
			return invalidAgentIdentityError()
		}
		decoded[index] = value
	}
	var header agentIdentityJWTHeader
	if err := json.Unmarshal(decoded[0], &header); err != nil || header.Algorithm != "RS256" ||
		strings.TrimSpace(header.KeyID) == "" || header.KeyID != strings.TrimSpace(header.KeyID) {
		return invalidAgentIdentityError()
	}
	var claims agentIdentityJWTClaims
	if err := json.Unmarshal(decoded[1], &claims); err != nil ||
		strings.TrimSpace(claims.Issuer) == "" || claims.Issuer != strings.TrimSpace(claims.Issuer) ||
		strings.TrimSpace(claims.Audience) == "" || claims.Audience != strings.TrimSpace(claims.Audience) ||
		claims.IssuedAt == nil || claims.ExpiresAt == nil {
		return invalidAgentIdentityError()
	}
	return validateAgentIdentityRecord(decoded[1])
}

func validateAgentIdentityRecord(raw []byte) error {
	var record agentIdentityRecord
	if err := json.Unmarshal(raw, &record); err != nil || record.ChatGPTAccountIsFedRAMP == nil {
		return invalidAgentIdentityError()
	}
	for _, value := range []string{
		record.AgentRuntimeID,
		record.AgentPrivateKey,
		record.AccountID,
		record.ChatGPTUserID,
		record.PlanType,
	} {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || value != trimmed || len(value) > maxCredentialLength {
			return invalidAgentIdentityError()
		}
		for _, r := range value {
			if unicode.IsControl(r) {
				return invalidAgentIdentityError()
			}
		}
	}
	return nil
}

func invalidAgentIdentityError() error {
	return FieldError{Field: "agent_identity", Err: errors.New("Codex auth payload has invalid agent_identity")}
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

func optionalAccountID(tokens map[string]any) (string, error) {
	value, exists := tokens["account_id"]
	if !exists || value == nil {
		return "", nil
	}
	raw, ok := value.(string)
	if !ok {
		return "", FieldError{Field: "tokens.account_id", Err: errors.New("Codex auth account id is invalid")}
	}
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}
	accountID, err := NormalizeExternalAccountID(raw)
	if err != nil {
		return "", nil
	}
	return accountID, nil
}
