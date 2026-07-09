package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"

	"github.com/strahe/profiledeck/internal/targetfs"
)

const maxAccountIDLength = 512

type Snapshot struct {
	Payload string
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
