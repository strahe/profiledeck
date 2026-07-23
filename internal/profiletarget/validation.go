// Package profiletarget owns validation, patching, and safe presentation of generic Profile targets.
package profiletarget

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/targetformat"
)

const (
	FormatText = targetformat.FormatText
	FormatJSON = targetformat.FormatJSON
	FormatTOML = targetformat.FormatTOML
	FormatEnv  = targetformat.FormatEnv

	StrategyReplaceFile = targetformat.StrategyReplaceFile
	StrategyJSONMerge   = targetformat.StrategyJSONMerge
	StrategyTOMLMerge   = targetformat.StrategyTOMLMerge
	StrategyEnvMerge    = targetformat.StrategyEnvMerge

	MaxPreviewBytes = 4096
	RedactedValue   = "[REDACTED]"
)

// Target is the generic target state required to validate or render a desired value.
type Target struct {
	Path      string
	Format    string
	Strategy  string
	ValueJSON string
}

// Read is the inspected content needed by merge strategies.
type Read struct {
	Exists  bool
	Content string
}

// Preview is a redacted, bounded text value.
type Preview struct {
	Content   string
	Truncated bool
}

// ValidatePath normalizes an absolute target path.
func ValidatePath(value string) (string, *apperror.Error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", apperror.New(apperror.TargetInvalid, "target path is required")
	}
	value = filepath.Clean(value)
	if !filepath.IsAbs(value) {
		return "", apperror.New(apperror.TargetInvalid, "target path must be absolute")
	}
	return value, nil
}

// PathOwnershipKey preserves the target path while normalizing its ownership key
// for the default filesystem semantics of the current platform.
func PathOwnershipKey(path string) string {
	return PathOwnershipKeyForOS(path, runtime.GOOS)
}

// PathOwnershipKeyForOS is exposed for cross-platform ownership tests.
func PathOwnershipKeyForOS(path, goos string) string {
	switch goos {
	case "darwin", "windows":
		return strings.ToLower(path)
	default:
		return path
	}
}

// Normalize validates compatible target fields and returns a canonical value_json object.
func Normalize(formatValue, strategyValue, rawValueJSON string) (string, string, string, *apperror.Error) {
	format, appErr := ValidateFormat(formatValue)
	if appErr != nil {
		return "", "", "", appErr
	}
	strategy, appErr := ValidateStrategy(strategyValue)
	if appErr != nil {
		return "", "", "", appErr
	}
	if !FormatStrategyAllowed(format, strategy) {
		return "", "", "", apperror.New(apperror.TargetInvalid, "target format and strategy are incompatible")
	}
	value, appErr := DecodeSingleJSONObject(rawValueJSON, apperror.TargetInvalid, "value_json")
	if appErr != nil {
		return "", "", "", appErr
	}
	if appErr := ValidateValueShape(format, strategy, value); appErr != nil {
		return "", "", "", appErr
	}
	normalized, err := json.Marshal(value)
	if err != nil {
		return "", "", "", apperror.Wrap(apperror.TargetInvalid, "failed to normalize value_json", err)
	}
	return format, strategy, string(normalized), nil
}

func ValidateFormat(value string) (string, *apperror.Error) {
	value = strings.TrimSpace(value)
	if targetformat.BuiltinRegistry().HasFormat(value) {
		return value, nil
	}
	return "", apperror.New(apperror.TargetInvalid, "target format is invalid")
}

func ValidateStrategy(value string) (string, *apperror.Error) {
	value = strings.TrimSpace(value)
	if targetformat.BuiltinRegistry().HasStrategy(value) {
		return value, nil
	}
	return "", apperror.New(apperror.TargetInvalid, "target strategy is invalid")
}

func FormatStrategyAllowed(format, strategy string) bool {
	return targetformat.BuiltinRegistry().Allows(format, strategy)
}

// ValidateValueShape rejects ambiguous target values before they reach a target backend.
func ValidateValueShape(format, strategy string, value map[string]any) *apperror.Error {
	if strategy == StrategyReplaceFile {
		content, ok := value["content"].(string)
		if !ok || len(value) != 1 {
			return apperror.New(apperror.TargetInvalid, `replace-file value_json must be {"content": string}`)
		}
		_ = content
		return nil
	}
	if ContainsReservedEnvRefObject(value) {
		return apperror.New(apperror.TargetInvalid, "value_json cannot contain ProfileDeck env reference objects")
	}
	target := Target{Format: format, Strategy: strategy}
	switch strategy {
	case StrategyTOMLMerge:
		if _, appErr := jsonObjectToTOML(value, target); appErr != nil {
			return appErr
		}
	case StrategyEnvMerge:
		if _, appErr := envPatchValues(value, target); appErr != nil {
			return appErr
		}
	}
	return nil
}

// DecodeSingleJSONObject decodes exactly one JSON object while preserving numbers.
func DecodeSingleJSONObject(raw string, code apperror.Code, field string) (map[string]any, *apperror.Error) {
	if strings.TrimSpace(raw) == "" {
		return nil, apperror.New(code, fmt.Sprintf("%s is required", field))
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, apperror.New(code, fmt.Sprintf("%s must be valid JSON object", field))
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, apperror.New(code, fmt.Sprintf("%s must contain one JSON object", field))
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, apperror.New(code, fmt.Sprintf("%s must be a JSON object", field))
	}
	if object == nil {
		return map[string]any{}, nil
	}
	return object, nil
}

func EnvRefDisplay(value any) (string, bool) {
	object, ok := value.(map[string]any)
	if !ok || len(object) != 2 {
		return "", false
	}
	refType, ok := object["ref_type"].(string)
	if !ok || refType != "env" {
		return "", false
	}
	name, ok := object["name"].(string)
	if !ok || !ValidEnvName(name) {
		return "", false
	}
	return "<env:" + name + ">", true
}

func ContainsReservedEnvRefObject(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		refType, hasRefType := typed["ref_type"].(string)
		_, hasName := typed["name"]
		if hasRefType && refType == "env" && hasName {
			return true
		}
		for _, child := range typed {
			if ContainsReservedEnvRefObject(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if ContainsReservedEnvRefObject(child) {
				return true
			}
		}
	}
	return false
}

func ValidEnvName(value string) bool {
	if value == "" {
		return false
	}
	for i, r := range value {
		valid := r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z'
		if i > 0 {
			valid = valid || r >= '0' && r <= '9'
		}
		if !valid {
			return false
		}
	}
	return true
}

func MarshalJSONNoEscape(value any) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSpace(buf.Bytes()), nil
}
