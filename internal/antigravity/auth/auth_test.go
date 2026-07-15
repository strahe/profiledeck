package auth

import "testing"

func TestNormalizeConsumerOAuthPayload(t *testing.T) {
	raw := []byte(`{"token":{"access_token":" access ","token_type":"Bearer","refresh_token":" refresh ","expiry":"2026-07-12T04:00:00.000000Z"},"auth_method":"consumer"}`)
	normalized, payload, err := Normalize(raw)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if payload.Token.AccessToken != "access" || payload.Token.RefreshToken != "refresh" {
		t.Fatalf("expected trimmed token fields, got %#v", payload.Token)
	}
	if normalized == string(raw) {
		t.Fatalf("expected canonical payload")
	}
}

func TestNormalizeRejectsNonV2Payload(t *testing.T) {
	for _, raw := range []string{
		`{}`,
		`{"token":{"access_token":"a","token_type":"bearer","refresh_token":"r","expiry":"2026-07-12T04:00:00Z"},"auth_method":"consumer"}`,
		`{"token":{"access_token":"a","token_type":"Bearer","refresh_token":"r","expiry":"bad"},"auth_method":"consumer"}`,
		`{"token":{"access_token":"a","token_type":"Bearer","refresh_token":"r","expiry":"2026-07-12T04:00:00Z"},"auth_method":"consumer","extra":true}`,
	} {
		if _, _, err := Normalize([]byte(raw)); err == nil {
			t.Fatalf("expected rejection for %s", raw)
		}
	}
}
