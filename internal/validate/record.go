// Package validate owns shared validation for persisted public record fields.
package validate

import (
	"encoding/json"
	"io"
	"strings"

	"github.com/strahe/profiledeck/internal/apperror"
)

const (
	maxIDLength          = 80
	maxNameLength        = 120
	maxDescriptionLength = 1000
	maxMetadataJSONBytes = 64 * 1024
)

func ID(value string, code apperror.Code) (string, *apperror.Error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", apperror.New(code, "id is required")
	}
	if len(value) > maxIDLength {
		return "", apperror.New(code, "id is too long")
	}
	for index, char := range value {
		valid := char >= 'a' && char <= 'z' || char >= '0' && char <= '9'
		if index > 0 {
			valid = valid || char == '.' || char == '_' || char == '-'
		}
		if !valid {
			return "", apperror.New(code, "id must use lowercase ASCII letters, digits, dots, underscores, or dashes")
		}
	}
	return value, nil
}

func Name(value string, code apperror.Code) (string, *apperror.Error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", apperror.New(code, "name is required")
	}
	if len(value) > maxNameLength {
		return "", apperror.New(code, "name is too long")
	}
	return value, nil
}

func Description(value string, code apperror.Code) (string, *apperror.Error) {
	value = strings.TrimSpace(value)
	if len(value) > maxDescriptionLength {
		return "", apperror.New(code, "description is too long")
	}
	return value, nil
}

func MetadataJSON(raw *string, code apperror.Code) (string, *apperror.Error) {
	if raw == nil {
		return "{}", nil
	}
	metadata, appErr := decodeMetadata(*raw, code, "metadata_json")
	if appErr != nil {
		return "", appErr
	}
	if containsSensitiveKey(metadata) {
		return "", apperror.New(code, "metadata_json cannot contain credential keys")
	}
	normalized, err := json.Marshal(metadata)
	if err != nil {
		return "", apperror.Wrap(code, "failed to normalize metadata_json", err)
	}
	if len(normalized) > maxMetadataJSONBytes {
		return "", apperror.New(code, "metadata_json is too large")
	}
	return string(normalized), nil
}

func StoredMetadata(raw string) (map[string]any, error) {
	metadata, appErr := decodeMetadata(raw, apperror.StoreSchemaInvalid, "stored metadata_json")
	if appErr != nil {
		return nil, appErr
	}
	redacted, ok := redactMetadata(metadata).(map[string]any)
	if !ok {
		return map[string]any{}, nil
	}
	return redacted, nil
}

func decodeMetadata(raw string, code apperror.Code, field string) (map[string]any, *apperror.Error) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, apperror.New(code, field+" must be valid JSON object")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, apperror.New(code, field+" must contain one JSON object")
	}
	metadata, ok := value.(map[string]any)
	if !ok {
		return nil, apperror.New(code, field+" must be a JSON object")
	}
	if metadata == nil {
		return map[string]any{}, nil
	}
	return metadata, nil
}

func containsSensitiveKey(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if credentialKey(key) || containsSensitiveKey(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if containsSensitiveKey(child) {
				return true
			}
		}
	}
	return false
}

func redactMetadata(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		redacted := make(map[string]any, len(typed))
		for key, child := range typed {
			if sensitiveKey(key) {
				redacted[key] = "[REDACTED]"
			} else {
				redacted[key] = redactMetadata(child)
			}
		}
		return redacted
	case []any:
		redacted := make([]any, len(typed))
		for index, child := range typed {
			redacted[index] = redactMetadata(child)
		}
		return redacted
	default:
		return typed
	}
}

func sensitiveKey(key string) bool {
	compact := compactKey(key)
	for _, marker := range []string{"apikey", "token", "accesstoken", "refreshtoken", "secret", "password", "authorization", "bearer"} {
		if strings.Contains(compact, marker) {
			return true
		}
	}
	return false
}

func credentialKey(key string) bool {
	words, compact := normalizeKey(key)
	switch {
	case compact == "token":
		return true
	case strings.Contains(compact, "apikey"), strings.Contains(compact, "accesstoken"),
		strings.Contains(compact, "refreshtoken"), strings.Contains(compact, "authtoken"):
		return true
	case containsWord(words, "secret", "password", "authorization", "bearer"):
		return true
	case containsSequence(words, "api", "key"):
		return true
	default:
		return len(words) > 0 && words[len(words)-1] == "token"
	}
}

func normalizeKey(key string) ([]string, string) {
	var words []string
	var current []rune
	previousLowerOrDigit := false
	flush := func() {
		if len(current) == 0 {
			return
		}
		words = append(words, strings.ToLower(string(current)))
		current = current[:0]
	}
	for _, char := range strings.TrimSpace(key) {
		switch {
		case char >= 'A' && char <= 'Z':
			if previousLowerOrDigit {
				flush()
			}
			current = append(current, char+'a'-'A')
			previousLowerOrDigit = false
		case char >= 'a' && char <= 'z', char >= '0' && char <= '9':
			current = append(current, char)
			previousLowerOrDigit = true
		default:
			flush()
			previousLowerOrDigit = false
		}
	}
	flush()
	return words, strings.Join(words, "")
}

func containsWord(words []string, targets ...string) bool {
	for _, word := range words {
		for _, target := range targets {
			if word == target {
				return true
			}
		}
	}
	return false
}

func containsSequence(words []string, sequence ...string) bool {
	for index := 0; index+len(sequence) <= len(words); index++ {
		matched := true
		for offset, expected := range sequence {
			if words[index+offset] != expected {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func compactKey(key string) string {
	var builder strings.Builder
	for _, char := range strings.TrimSpace(key) {
		switch {
		case char >= 'A' && char <= 'Z':
			builder.WriteRune(char + 'a' - 'A')
		case char >= 'a' && char <= 'z', char >= '0' && char <= '9':
			builder.WriteRune(char)
		}
	}
	return builder.String()
}
