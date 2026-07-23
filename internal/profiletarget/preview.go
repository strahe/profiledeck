package profiletarget

import (
	"encoding/json"
	"io"
	"strings"

	"github.com/strahe/profiledeck/internal/apperror"
)

// ValuePreview returns a redacted preview for a generic stored target value.
func ValuePreview(format, strategy, raw string) (Preview, error) {
	value, appErr := DecodeSingleJSONObject(raw, apperror.StoreSchemaInvalid, "stored value_json")
	if appErr != nil {
		return Preview{}, appErr
	}
	if strategy == StrategyReplaceFile {
		return replaceFileTargetValuePreview(value)
	}
	redacted := RedactValue(format, strategy, value)
	rawPreview, err := MarshalJSONNoEscape(redacted)
	if err != nil {
		return Preview{}, NewPreviewEncodingError(err)
	}
	return TruncatePreview(string(rawPreview)), nil
}

func NewPreviewEncodingError(cause error) error {
	return previewEncodingError(cause)
}

func previewEncodingError(cause error) error {
	return wrapStoreSchemaError("failed to encode target value preview", cause)
}

func newStoreSchemaError(message string) *apperror.Error {
	return apperror.New(apperror.StoreSchemaInvalid, message)
}

func wrapStoreSchemaError(message string, cause error) *apperror.Error {
	return apperror.Wrap(apperror.StoreSchemaInvalid, message, cause)
}

func replaceFileTargetValuePreview(value map[string]any) (Preview, error) {
	content, ok := value["content"].(string)
	if !ok || len(value) != 1 {
		return Preview{}, newStoreSchemaError(`stored replace-file value_json must be {"content": string}`)
	}
	contentPreview := PreviewSensitiveText(content)
	rawPreview, err := MarshalJSONNoEscape(map[string]any{"content": contentPreview.Content})
	if err != nil {
		return Preview{}, wrapStoreSchemaError("failed to encode target value preview", err)
	}
	preview := TruncatePreview(string(rawPreview))
	preview.Truncated = preview.Truncated || contentPreview.Truncated
	return preview, nil
}

// RedactValue redacts structured values before they cross an output boundary.
func RedactValue(_, strategy string, value map[string]any) any {
	if strategy == StrategyReplaceFile {
		redacted := make(map[string]any, len(value))
		for key, child := range value {
			if key == "content" {
				if content, ok := child.(string); ok {
					redacted[key] = RedactSensitiveText(content)
					continue
				}
			}
			redacted[key] = RedactStructuredSecrets(child)
		}
		return redacted
	}
	return RedactStructuredSecrets(value)
}

// RedactStructuredSecrets recursively redacts values under sensitive keys.
func RedactStructuredSecrets(value any) any {
	if display, ok := EnvRefDisplay(value); ok {
		return display
	}
	switch typed := value.(type) {
	case map[string]any:
		redacted := make(map[string]any, len(typed))
		for key, child := range typed {
			if IsSensitiveOutputKey(key) {
				if display, ok := EnvRefDisplay(child); ok {
					redacted[key] = display
				} else {
					redacted[key] = RedactedValue
				}
				continue
			}
			redacted[key] = RedactStructuredSecrets(child)
		}
		return redacted
	case []any:
		redacted := make([]any, len(typed))
		for i, child := range typed {
			redacted[i] = RedactStructuredSecrets(child)
		}
		return redacted
	default:
		return typed
	}
}

// RedactSensitiveText handles structured JSON and common key-value text safely.
func RedactSensitiveText(value string) string {
	if redacted, ok := redactJSONText(value); ok {
		return redacted
	}
	lines := strings.SplitAfter(value, "\n")
	for i, line := range lines {
		lines[i] = redactSensitiveTextLine(line)
	}
	return strings.Join(lines, "")
}

func PreviewSensitiveText(value string) Preview {
	return TruncatePreview(RedactSensitiveText(value))
}

func redactJSONText(value string) (string, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
		return "", false
	}
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return "", false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return "", false
	}
	switch decoded.(type) {
	case map[string]any, []any:
	default:
		return "", false
	}
	raw, err := MarshalJSONNoEscape(RedactStructuredSecrets(decoded))
	if err != nil {
		return "", false
	}
	return string(raw), true
}

func redactSensitiveTextLine(line string) string {
	lineEnding := ""
	if strings.HasSuffix(line, "\n") {
		lineEnding = "\n"
		line = strings.TrimSuffix(line, "\n")
	}
	if redacted, ok := redactQuotedKeyValueAssignments(line); ok {
		line = redacted
	}
	if redacted, ok := redactPlainKeyValueAssignments(line); ok {
		line = redacted
	}
	return line + lineEnding
}

func redactQuotedKeyValueAssignments(value string) (string, bool) {
	var builder strings.Builder
	last := 0
	changed := false
	for i := 0; i < len(value); i++ {
		if value[i] != '"' {
			continue
		}
		keyEnd := jsonStringEndIndex(value, i)
		if keyEnd < 0 {
			break
		}
		key := value[i+1 : keyEnd]
		separator := keyEnd + 1
		for separator < len(value) && (value[separator] == ' ' || value[separator] == '\t') {
			separator++
		}
		if separator >= len(value) || value[separator] != ':' {
			i = keyEnd
			continue
		}
		valueStart := separator + 1
		for valueStart < len(value) && (value[valueStart] == ' ' || value[valueStart] == '\t') {
			valueStart++
		}
		if valueStart >= len(value) || !IsSensitiveOutputKey(key) {
			i = keyEnd
			continue
		}
		if value[valueStart] != '"' {
			if !changed {
				builder.Grow(len(value))
			}
			builder.WriteString(value[last:valueStart])
			builder.WriteString(`"` + RedactedValue + `"`)
			last = len(value)
			changed = true
			break
		}
		valueEnd := jsonStringEndIndex(value, valueStart)
		if valueEnd < 0 {
			if !changed {
				builder.Grow(len(value))
			}
			builder.WriteString(value[last:valueStart])
			builder.WriteString(`"` + RedactedValue + `"`)
			last = len(value)
			changed = true
			break
		}
		if !changed {
			builder.Grow(len(value))
		}
		builder.WriteString(value[last:valueStart])
		builder.WriteString(`"` + RedactedValue + `"`)
		last = valueEnd + 1
		changed = true
		i = valueEnd
	}
	if !changed {
		return value, false
	}
	builder.WriteString(value[last:])
	return builder.String(), true
}

func jsonStringEndIndex(value string, start int) int {
	if start < 0 || start >= len(value) || value[start] != '"' {
		return -1
	}
	escaped := false
	for i := start + 1; i < len(value); i++ {
		switch {
		case escaped:
			escaped = false
		case value[i] == '\\':
			escaped = true
		case value[i] == '"':
			return i
		}
	}
	return -1
}

func redactPlainKeyValueAssignments(value string) (string, bool) {
	var builder strings.Builder
	last := 0
	changed := false
	for i := 0; i < len(value); i++ {
		if value[i] != '=' && value[i] != ':' {
			continue
		}
		keyEnd := i
		for keyEnd > 0 && (value[keyEnd-1] == ' ' || value[keyEnd-1] == '\t') {
			keyEnd--
		}
		keyStart := keyEnd
		for keyStart > 0 && isPlainAssignmentKeyChar(value[keyStart-1]) {
			keyStart--
		}
		if keyStart == keyEnd || keyStart > 0 && value[keyStart-1] == '"' {
			continue
		}
		key := value[keyStart:keyEnd]
		if !IsSensitiveOutputKey(key) {
			continue
		}
		valueStart := i + 1
		for valueStart < len(value) && (value[valueStart] == ' ' || value[valueStart] == '\t') {
			valueStart++
		}
		valueEnd := plainAssignmentValueEnd(value, valueStart)
		if !changed {
			builder.Grow(len(value))
		}
		builder.WriteString(value[last:valueStart])
		builder.WriteString(RedactedValue)
		last = valueEnd
		changed = true
		i = valueEnd - 1
	}
	if !changed {
		return value, false
	}
	builder.WriteString(value[last:])
	return builder.String(), true
}

func isPlainAssignmentKeyChar(value byte) bool {
	return value == '_' || value == '-' || value == '.' || value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z' || value >= '0' && value <= '9'
}

func plainAssignmentValueEnd(value string, start int) int {
	if start >= len(value) {
		return start
	}
	if value[start] == '"' || value[start] == '\'' {
		return plainQuotedValueEnd(value, start)
	}
	for i := start; i < len(value); i++ {
		switch value[i] {
		case ',', ';', '}', ']':
			return i
		case ' ', '\t':
			if plainAssignmentStartsAt(value, i+1) {
				return i
			}
		}
	}
	return len(value)
}

func plainQuotedValueEnd(value string, start int) int {
	quote := value[start]
	escaped := false
	for i := start + 1; i < len(value); i++ {
		switch {
		case escaped:
			escaped = false
		case value[i] == '\\':
			escaped = true
		case value[i] == quote:
			return i + 1
		}
	}
	return len(value)
}

func plainAssignmentStartsAt(value string, start int) bool {
	for start < len(value) && (value[start] == ' ' || value[start] == '\t') {
		start++
	}
	keyStart := start
	for start < len(value) && isPlainAssignmentKeyChar(value[start]) {
		start++
	}
	if start == keyStart {
		return false
	}
	for start < len(value) && (value[start] == ' ' || value[start] == '\t') {
		start++
	}
	return start < len(value) && (value[start] == '=' || value[start] == ':')
}

// TruncatePreview preserves UTF-8 boundaries at the presentation size limit.
func TruncatePreview(value string) Preview {
	if len(value) <= MaxPreviewBytes {
		return Preview{Content: value}
	}
	end := 0
	for i := range value {
		if i > MaxPreviewBytes {
			break
		}
		end = i
	}
	if end == 0 {
		end = MaxPreviewBytes
	}
	return Preview{Content: value[:end], Truncated: true}
}

// IsSensitiveOutputKey recognizes secret-bearing structured output keys.
func IsSensitiveOutputKey(key string) bool {
	_, compact := normalizeKey(key)
	for _, marker := range []string{"apikey", "token", "accesstoken", "refreshtoken", "secret", "password", "authorization", "bearer"} {
		if strings.Contains(compact, marker) {
			return true
		}
	}
	return false
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
	for _, r := range strings.TrimSpace(key) {
		switch {
		case r >= 'A' && r <= 'Z':
			if previousLowerOrDigit {
				flush()
			}
			current = append(current, r+'a'-'A')
			previousLowerOrDigit = false
		case r >= 'a' && r <= 'z':
			current = append(current, r)
			previousLowerOrDigit = true
		case r >= '0' && r <= '9':
			current = append(current, r)
			previousLowerOrDigit = true
		default:
			flush()
			previousLowerOrDigit = false
		}
	}
	flush()
	return words, strings.Join(words, "")
}
