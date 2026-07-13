package auth

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestNormalizeSubscriptionLoginPreservesUnknownFieldsAndNumbers(t *testing.T) {
	raw := `{"claudeAiOauth":{"accessToken":"access","refreshToken":"refresh","subscriptionType":"max","expiresAt":4102444800000,"future":{"precise":900719925474099312345}},"top":"kept"}`
	normalized, info, err := Normalize([]byte(raw))
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if info.CompatibilityVersion != CompatibilityVersion || info.SubscriptionType != "max" || info.ExpiresAtUnixMS != 4102444800000 || !info.HasExpiry || info.ExpiryUnknown {
		t.Fatalf("unexpected info: %#v", info)
	}
	if !strings.Contains(normalized, `"precise":900719925474099312345`) || !strings.Contains(normalized, `"top":"kept"`) {
		t.Fatalf("unknown fields or precise number were not preserved: %s", normalized)
	}
	var decoded map[string]any
	decoder := json.NewDecoder(strings.NewReader(normalized))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		t.Fatalf("normalized JSON is invalid: %v", err)
	}
}

func TestNormalizeRejectsUnsupportedAndInvalidLogins(t *testing.T) {
	tests := []struct {
		name        string
		raw         string
		unsupported bool
	}{
		{name: "console shape", raw: `{"apiKey":"secret"}`, unsupported: true},
		{name: "missing subscription", raw: `{"claudeAiOauth":{"accessToken":"a","refreshToken":"r"}}`, unsupported: true},
		{name: "null subscription", raw: `{"claudeAiOauth":{"accessToken":"a","refreshToken":"r","subscriptionType":null}}`, unsupported: true},
		{name: "oauth wrong type", raw: `{"claudeAiOauth":"invalid"}`},
		{name: "empty subscription", raw: `{"claudeAiOauth":{"accessToken":"a","refreshToken":"r","subscriptionType":""}}`},
		{name: "subscription wrong type", raw: `{"claudeAiOauth":{"accessToken":"a","refreshToken":"r","subscriptionType":1}}`},
		{name: "missing access", raw: `{"claudeAiOauth":{"refreshToken":"r","subscriptionType":"max"}}`},
		{name: "missing refresh", raw: `{"claudeAiOauth":{"accessToken":"a","subscriptionType":"max"}}`},
		{name: "trailing JSON", raw: `{"claudeAiOauth":{"accessToken":"a","refreshToken":"r","subscriptionType":"max"}} {}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := Normalize([]byte(test.raw))
			if err == nil {
				t.Fatal("Normalize() unexpectedly succeeded")
			}
			if got := IsKind(err, ErrorUnsupportedAccountType); got != test.unsupported {
				t.Fatalf("unsupported = %v, want %v; error = %v", got, test.unsupported, err)
			}
		})
	}
}

func TestNormalizeAcceptsUnenumeratedSubscriptionTypeWithoutExpiry(t *testing.T) {
	_, info, err := Normalize([]byte(`{"claudeAiOauth":{"accessToken":"a","refreshToken":"r","subscriptionType":"future-tier"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if info.SubscriptionType != "future-tier" || info.HasExpiry || info.ExpiryUnknown {
		t.Fatalf("unexpected compatibility info: %#v", info)
	}
}

func TestNormalizeExpiryCompatibility(t *testing.T) {
	base := `{"claudeAiOauth":{"accessToken":"a","refreshToken":"r","subscriptionType":"team","expiresAt":%s}}`
	tests := []struct {
		value   string
		unixMS  int64
		has     bool
		unknown bool
	}{
		{value: "0", has: true},
		{value: "2000000000", unixMS: 2000000000000, has: true},
		{value: "2000000000000", unixMS: 2000000000000, has: true},
		{value: `"later"`, unknown: true},
		{value: "1.5", unknown: true},
		{value: "-1", unknown: true},
		{value: "999999999999999999999999", unknown: true},
	}
	for _, test := range tests {
		_, info, err := Normalize([]byte(strings.Replace(base, "%s", test.value, 1)))
		if err != nil {
			t.Fatalf("Normalize(%s) error = %v", test.value, err)
		}
		if info.ExpiresAtUnixMS != test.unixMS || info.HasExpiry != test.has || info.ExpiryUnknown != test.unknown {
			t.Fatalf("Normalize(%s) info = %#v", test.value, info)
		}
	}
}

func TestStatusAtSupportsExpiredAndExpiring(t *testing.T) {
	now := time.Unix(2_000_000_000, 0)
	if got := StatusAt(Info{ExpiresAtUnixMS: now.Add(-time.Second).UnixMilli(), HasExpiry: true}, now); got != StatusExpired {
		t.Fatalf("expired status = %s", got)
	}
	if got := StatusAt(Info{ExpiresAtUnixMS: now.Add(time.Hour).UnixMilli(), HasExpiry: true}, now); got != StatusExpiring {
		t.Fatalf("expiring status = %s", got)
	}
	if got := StatusAt(Info{ExpiresAtUnixMS: now.Add(ExpiringWindow + time.Hour).UnixMilli(), HasExpiry: true}, now); got != StatusValid {
		t.Fatalf("valid status = %s", got)
	}
	if got := StatusAt(Info{ExpiryUnknown: true}, now); got != StatusValid {
		t.Fatalf("unknown expiry status = %s", got)
	}
	if got := StatusAt(Info{ExpiresAtUnixMS: 0, HasExpiry: true}, now); got != StatusExpired {
		t.Fatalf("epoch expiry status = %s", got)
	}
}

func TestNormalizeRejectsOversizedPayload(t *testing.T) {
	_, _, err := Normalize(make([]byte, MaxPayloadBytes+1))
	if err == nil {
		t.Fatal("oversized payload unexpectedly succeeded")
	}
}
