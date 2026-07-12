package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

const MaxPayloadBytes = 1024 * 1024

type Token struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	RefreshToken string `json:"refresh_token"`
	Expiry       string `json:"expiry"`
}

type Payload struct {
	Token      Token  `json:"token"`
	AuthMethod string `json:"auth_method"`
}

func Normalize(raw []byte) (string, Payload, error) {
	if len(raw) == 0 {
		return "", Payload{}, errors.New("antigravity login is empty")
	}
	if len(raw) > MaxPayloadBytes {
		return "", Payload{}, errors.New("antigravity login is too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var payload Payload
	if err := decoder.Decode(&payload); err != nil {
		return "", Payload{}, errors.New("antigravity login JSON is invalid")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return "", Payload{}, errors.New("antigravity login JSON contains trailing data")
	}
	payload.Token.AccessToken = strings.TrimSpace(payload.Token.AccessToken)
	payload.Token.TokenType = strings.TrimSpace(payload.Token.TokenType)
	payload.Token.RefreshToken = strings.TrimSpace(payload.Token.RefreshToken)
	payload.Token.Expiry = strings.TrimSpace(payload.Token.Expiry)
	payload.AuthMethod = strings.TrimSpace(payload.AuthMethod)
	if payload.Token.AccessToken == "" {
		return "", Payload{}, errors.New("antigravity access token is missing")
	}
	if payload.Token.RefreshToken == "" {
		return "", Payload{}, errors.New("antigravity refresh token is missing")
	}
	if payload.Token.TokenType != "Bearer" {
		return "", Payload{}, errors.New("antigravity token type is unsupported")
	}
	if payload.AuthMethod != "consumer" {
		return "", Payload{}, errors.New("antigravity auth method is unsupported")
	}
	if _, err := time.Parse(time.RFC3339Nano, payload.Token.Expiry); err != nil {
		return "", Payload{}, errors.New("antigravity token expiry is invalid")
	}
	normalized, err := json.Marshal(payload)
	if err != nil {
		return "", Payload{}, fmt.Errorf("encode antigravity login: %w", err)
	}
	return string(normalized), payload, nil
}

func ExpiryUnixMS(payload Payload) int64 {
	expiry, err := time.Parse(time.RFC3339Nano, payload.Token.Expiry)
	if err != nil {
		return 0
	}
	return expiry.UnixMilli()
}
