package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
)

const (
	// CompatibilityVersion identifies the fail-closed claudeAiOauth shape
	// accepted by this parser; it is independent of subscription tier values.
	CompatibilityVersion = 1
	MaxPayloadBytes      = 1024 * 1024
	ExpiringWindow       = 5 * 24 * time.Hour

	StatusValid       = "valid"
	StatusExpiring    = "expiring"
	StatusExpired     = "expired"
	StatusMissing     = "missing"
	StatusInvalid     = "invalid"
	StatusUnsupported = "unsupported"
	StatusUnavailable = "unavailable"
)

type ErrorKind string

const (
	ErrorInvalid                ErrorKind = "invalid"
	ErrorUnsupportedAccountType ErrorKind = "unsupported_account_type"
)

type CompatibilityError struct {
	Kind ErrorKind
}

func (err *CompatibilityError) Error() string {
	if err != nil && err.Kind == ErrorUnsupportedAccountType {
		return "Claude Code login does not report an active Pro, Max, Team, or Enterprise subscription"
	}
	return "Claude Code login is invalid"
}

type Info struct {
	CompatibilityVersion int
	SubscriptionType     string
	ExpiresAtUnixMS      int64
	HasExpiry            bool
	ExpiryUnknown        bool
}

func Normalize(raw []byte) (string, Info, error) {
	if len(raw) == 0 || len(raw) > MaxPayloadBytes {
		return "", Info{}, &CompatibilityError{Kind: ErrorInvalid}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var root map[string]any
	if err := decoder.Decode(&root); err != nil || root == nil {
		return "", Info{}, &CompatibilityError{Kind: ErrorInvalid}
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", Info{}, &CompatibilityError{Kind: ErrorInvalid}
	}
	oauthValue, exists := root["claudeAiOauth"]
	if !exists {
		return "", Info{}, &CompatibilityError{Kind: ErrorUnsupportedAccountType}
	}
	oauth, ok := oauthValue.(map[string]any)
	if !ok || oauth == nil {
		return "", Info{}, &CompatibilityError{Kind: ErrorInvalid}
	}
	if !nonEmptyString(oauth["accessToken"]) || !nonEmptyString(oauth["refreshToken"]) {
		return "", Info{}, &CompatibilityError{Kind: ErrorInvalid}
	}
	subscriptionValue, exists := oauth["subscriptionType"]
	if !exists || subscriptionValue == nil {
		return "", Info{}, &CompatibilityError{Kind: ErrorUnsupportedAccountType}
	}
	subscriptionType, ok := subscriptionValue.(string)
	if !ok || strings.TrimSpace(subscriptionType) == "" {
		return "", Info{}, &CompatibilityError{Kind: ErrorInvalid}
	}
	info := Info{CompatibilityVersion: CompatibilityVersion, SubscriptionType: subscriptionType}
	if value, exists := oauth["expiresAt"]; exists {
		if unixMS, ok := expiryUnixMS(value); ok {
			info.ExpiresAtUnixMS = unixMS
			info.HasExpiry = true
		} else {
			info.ExpiryUnknown = true
		}
	}
	normalized, err := json.Marshal(root)
	if err != nil {
		return "", Info{}, &CompatibilityError{Kind: ErrorInvalid}
	}
	return string(normalized), info, nil
}

func StatusAt(info Info, now time.Time) string {
	if !info.HasExpiry || info.ExpiryUnknown {
		return StatusValid
	}
	expires := time.UnixMilli(info.ExpiresAtUnixMS)
	if !expires.After(now) {
		return StatusExpired
	}
	if expires.Sub(now) <= ExpiringWindow {
		return StatusExpiring
	}
	return StatusValid
}

func IsKind(err error, kind ErrorKind) bool {
	var compatibilityErr *CompatibilityError
	return errors.As(err, &compatibilityErr) && compatibilityErr.Kind == kind
}

func nonEmptyString(value any) bool {
	text, ok := value.(string)
	return ok && strings.TrimSpace(text) != ""
}

func expiryUnixMS(value any) (int64, bool) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	integer, err := number.Int64()
	if err != nil || integer < 0 {
		return 0, false
	}
	// Values below 100 billion are seconds; larger values are milliseconds.
	// The threshold keeps the seconds conversion well within int64.
	if integer < 100_000_000_000 {
		integer *= 1000
	}
	return integer, true
}
