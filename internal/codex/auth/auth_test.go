package auth

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

func TestReadSnapshotPreservesMissingFileError(t *testing.T) {
	_, err := ReadSnapshot(filepath.Join(t.TempDir(), "missing-auth.json"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("expected missing auth error to preserve fs.ErrNotExist, got %v", err)
	}
}

func TestReadSnapshotReturnsPayloadAndCodexAccountID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	raw := `{"tokens":{"account_id":"work-account","access_token":"secret"}}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("expected auth setup to succeed, got %v", err)
	}

	snapshot, err := ReadSnapshot(path)
	if err != nil {
		t.Fatalf("expected auth snapshot to read, got %v", err)
	}
	if snapshot.Payload != raw || snapshot.CodexAccountID != "work-account" {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
}
