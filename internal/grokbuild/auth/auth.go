package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/strahe/profiledeck/internal/targetfs"
)

type Snapshot struct {
	Payload string
}

type SizeError struct {
	Size int
	Max  int
}

func (e SizeError) Error() string { return "Grok auth payload is too large" }

func ReadSnapshot(path string) (Snapshot, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Snapshot{}, err
	}
	payload, err := Validate(raw)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Payload: payload}, nil
}

// Validate checks the upstream AuthStore shape while preserving the exact
// source bytes for storage and later restoration.
func Validate(raw []byte) (string, error) {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "" {
		return "", errors.New("grok auth payload is empty")
	}
	if len(raw) > targetfs.MaxFileBytes {
		return "", SizeError{Size: len(raw), Max: targetfs.MaxFileBytes}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var store map[string]json.RawMessage
	if err := decoder.Decode(&store); err != nil {
		return "", fmt.Errorf("grok auth payload must be an AuthStore object: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err != nil {
			return "", fmt.Errorf("grok auth payload must contain one AuthStore object: %w", err)
		}
		return "", errors.New("grok auth payload must contain one AuthStore object")
	}
	if len(store) == 0 {
		return "", errors.New("grok auth store is empty")
	}
	for _, entry := range store {
		if err := validateEntry(entry); err != nil {
			return "", err
		}
	}
	return string(raw), nil
}

func validateEntry(raw json.RawMessage) error {
	var entry map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entry); err != nil || entry == nil {
		return errors.New("grok auth store contains an invalid entry")
	}
	if !isJSONString(entry["key"]) {
		return errors.New("grok auth entry key is invalid")
	}
	mode, ok := stringValue(entry["auth_mode"])
	if !ok {
		return errors.New("grok auth entry mode is invalid")
	}
	switch mode {
	case "web_login", "grok", "oidc", "external", "api_key":
	default:
		return errors.New("grok auth entry mode is unsupported")
	}
	created, ok := stringValue(entry["create_time"])
	if !ok {
		return errors.New("grok auth entry creation time is invalid")
	}
	if _, err := time.Parse(time.RFC3339Nano, created); err != nil {
		return errors.New("grok auth entry creation time is invalid")
	}
	if !isJSONString(entry["user_id"]) {
		return errors.New("grok auth entry user is invalid")
	}
	email, exists := entry["email"]
	if exists && !bytes.Equal(bytes.TrimSpace(email), []byte("null")) && !isJSONString(email) {
		return errors.New("grok auth entry email is invalid")
	}
	return nil
}

func isJSONString(raw json.RawMessage) bool {
	_, ok := stringValue(raw)
	return ok
}

func stringValue(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return "", false
	}
	return value, true
}
