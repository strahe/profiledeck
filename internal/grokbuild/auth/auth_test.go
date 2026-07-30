package auth

import (
	"strings"
	"testing"
)

func TestValidatePreservesOriginalAuthStoreBytes(t *testing.T) {
	raw := "{\n  \"scope-b\": {\"key\":\"token-b\",\"auth_mode\":\"api_key\",\"create_time\":\"2026-07-29T00:00:00Z\",\"user_id\":\"\",\"email\":null,\"future\":{\"enabled\":true}},\n  \"scope-a\": {\"key\":\"token-a\",\"auth_mode\":\"oidc\",\"create_time\":\"2026-07-29T00:00:00.123Z\",\"user_id\":\"user-a\",\"email\":\"user@example.invalid\"}\n}\n"
	payload, err := Validate([]byte(raw))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if payload != raw {
		t.Fatal("auth payload was normalized instead of preserved")
	}
}

func TestValidateRejectsMissingEmptyAndInvalidAuthStores(t *testing.T) {
	validEntry := `{"key":"token","auth_mode":"oidc","create_time":"2026-07-29T00:00:00Z","user_id":"user","email":null}`
	tests := []struct {
		name string
		raw  string
	}{
		{name: "missing bytes", raw: ""},
		{name: "whitespace", raw: " \n"},
		{name: "empty object", raw: "{}"},
		{name: "array", raw: "[]"},
		{name: "trailing value", raw: `{"scope":` + validEntry + `} {}`},
		{name: "entry array", raw: `{"scope":[]}`},
		{name: "missing key", raw: `{"scope":{"auth_mode":"oidc","create_time":"2026-07-29T00:00:00Z","user_id":"user","email":null}}`},
		{name: "invalid mode", raw: `{"scope":{"key":"token","auth_mode":"future","create_time":"2026-07-29T00:00:00Z","user_id":"user","email":null}}`},
		{name: "invalid time", raw: `{"scope":{"key":"token","auth_mode":"oidc","create_time":"today","user_id":"user","email":null}}`},
		{name: "invalid email", raw: `{"scope":{"key":"token","auth_mode":"oidc","create_time":"2026-07-29T00:00:00Z","user_id":"user","email":42}}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Validate([]byte(test.raw)); err == nil {
				t.Fatal("Validate unexpectedly succeeded")
			}
		})
	}
}

func TestValidateAcceptsUpstreamAuthModeAliases(t *testing.T) {
	for _, mode := range []string{"web_login", "grok", "oidc", "external", "api_key"} {
		t.Run(mode, func(t *testing.T) {
			raw := `{"scope":{"key":"token","auth_mode":"` + mode + `","create_time":"2026-07-29T00:00:00Z","user_id":"user","email":null}}`
			if _, err := Validate([]byte(raw)); err != nil {
				t.Fatalf("Validate: %v", err)
			}
		})
	}
}

func TestValidateAcceptsMissingOptionalEmail(t *testing.T) {
	raw := `{"scope":{"key":"token","auth_mode":"oidc","create_time":"2026-07-29T00:00:00Z","user_id":"user"}}`
	if _, err := Validate([]byte(raw)); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidationErrorDoesNotContainSourceValues(t *testing.T) {
	secret := "source-secret-must-not-appear"
	raw := `{"scope":{"key":"` + secret + `","auth_mode":42,"create_time":"2026-07-29T00:00:00Z","user_id":"user","email":null}}`
	_, err := Validate([]byte(raw))
	if err == nil {
		t.Fatal("Validate unexpectedly succeeded")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("validation error exposed source content: %v", err)
	}
}
