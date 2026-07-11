package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/strahe/profiledeck/internal/store"
)

const tomlSemanticRewriteWarning = "toml semantic rewrite may drop formatting/comments"

func desiredTargetContent(target store.ProfileTarget, before targetPlanRead) (string, []string, error) {
	switch target.Strategy {
	case targetStrategyReplaceFile:
		content, err := replaceFileContentFromValueJSON(target.ValueJSON)
		return content, nil, err
	case targetStrategyJSONMerge:
		content, err := mergeJSONTargetContent(target, before)
		return content, nil, err
	case targetStrategyTOMLMerge:
		content, err := mergeTOMLTargetContent(target, before)
		warnings := []string{}
		if before.FileExists {
			warnings = append(warnings, tomlSemanticRewriteWarning)
		}
		return content, warnings, err
	case targetStrategyEnvMerge:
		content, err := mergeEnvTargetContent(target, before)
		return content, nil, err
	default:
		return "", nil, NewError(ErrorTargetInvalid, "target strategy is invalid")
	}
}

func mergeJSONTargetContent(target store.ProfileTarget, before targetPlanRead) (string, error) {
	patch, appErr := decodeSingleJSONObject(target.ValueJSON, ErrorTargetInvalid, "stored value_json")
	if appErr != nil {
		return "", appErr
	}
	if appErr := validateStoredTargetValue(target, patch); appErr != nil {
		return "", appErr
	}

	base := map[string]any{}
	if before.FileExists {
		decoded, appErr := decodeJSONTargetObject(before.Content, target)
		if appErr != nil {
			return "", appErr
		}
		base = decoded
	}

	merged := mergeObjects(base, patch)
	content, err := marshalIndentedJSONNoEscape(merged)
	if err != nil {
		return "", targetContentInvalidError(target, "failed to encode merged JSON target content", err)
	}
	return content, nil
}

func decodeJSONTargetObject(raw string, target store.ProfileTarget) (map[string]any, *AppError) {
	if strings.TrimSpace(raw) == "" {
		return map[string]any{}, nil
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, targetContentInvalidError(target, "target JSON content is invalid", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err != nil {
			return nil, targetContentInvalidError(target, "target JSON content contains extra values", err)
		}
		return nil, targetContentInvalidError(target, "target JSON content contains extra values", nil)
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, targetContentInvalidError(target, "target JSON content must be an object", nil)
	}
	if object == nil {
		return map[string]any{}, nil
	}
	return object, nil
}

func mergeTOMLTargetContent(target store.ProfileTarget, before targetPlanRead) (string, error) {
	patch, appErr := decodeSingleJSONObject(target.ValueJSON, ErrorTargetInvalid, "stored value_json")
	if appErr != nil {
		return "", appErr
	}
	if appErr := validateStoredTargetValue(target, patch); appErr != nil {
		return "", appErr
	}
	tomlPatch, appErr := jsonObjectToTOML(patch, target)
	if appErr != nil {
		return "", appErr
	}

	base := map[string]any{}
	if before.FileExists {
		if err := toml.Unmarshal([]byte(before.Content), &base); err != nil {
			return "", targetContentInvalidError(target, "target TOML content is invalid", err)
		}
		if base == nil {
			base = map[string]any{}
		}
	}

	merged := mergeObjects(base, tomlPatch)
	raw, err := toml.Marshal(merged)
	if err != nil {
		return "", targetContentInvalidError(target, "failed to encode merged TOML target content", err)
	}
	content := string(raw)
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return content, nil
}

func jsonObjectToTOML(value map[string]any, target store.ProfileTarget) (map[string]any, *AppError) {
	converted := make(map[string]any, len(value))
	for key, child := range value {
		tomlValue, appErr := jsonValueToTOML(child, target)
		if appErr != nil {
			return nil, appErr
		}
		converted[key] = tomlValue
	}
	return converted, nil
}

func jsonValueToTOML(value any, target store.ProfileTarget) (any, *AppError) {
	switch typed := value.(type) {
	case nil:
		return nil, targetContentInvalidError(target, "toml-merge value_json cannot contain null", nil)
	case map[string]any:
		return jsonObjectToTOML(typed, target)
	case []any:
		converted := make([]any, len(typed))
		for i, child := range typed {
			tomlValue, appErr := jsonValueToTOML(child, target)
			if appErr != nil {
				return nil, appErr
			}
			converted[i] = tomlValue
		}
		return converted, nil
	case json.Number:
		if strings.ContainsAny(typed.String(), ".eE") {
			value, err := typed.Float64()
			if err != nil {
				return nil, targetContentInvalidError(target, "toml-merge value_json contains invalid number", err)
			}
			return value, nil
		}
		value, err := typed.Int64()
		if err == nil {
			return value, nil
		}
		floatValue, floatErr := typed.Float64()
		if floatErr != nil {
			return nil, targetContentInvalidError(target, "toml-merge value_json contains invalid number", floatErr)
		}
		return floatValue, nil
	case string, bool, int64, int, float64:
		return typed, nil
	default:
		return nil, targetContentInvalidError(target, fmt.Sprintf("toml-merge value_json contains unsupported value type %T", value), nil)
	}
}

func mergeEnvTargetContent(target store.ProfileTarget, before targetPlanRead) (string, error) {
	patch, appErr := decodeSingleJSONObject(target.ValueJSON, ErrorTargetInvalid, "stored value_json")
	if appErr != nil {
		return "", appErr
	}
	if appErr := validateStoredTargetValue(target, patch); appErr != nil {
		return "", appErr
	}
	values, appErr := envPatchValues(patch, target)
	if appErr != nil {
		return "", appErr
	}
	return mergeEnvContent(before.Content, values, target)
}

func validateStoredTargetValue(target store.ProfileTarget, value map[string]any) *AppError {
	appErr := validateTargetValueShape(target.Format, target.Strategy, value)
	if appErr == nil {
		return nil
	}
	return appErr.
		WithDetail("path", target.Path).
		WithDetail("format", target.Format).
		WithDetail("strategy", target.Strategy)
}

func envPatchValues(patch map[string]any, target store.ProfileTarget) (map[string]string, *AppError) {
	values := make(map[string]string, len(patch))
	for key, value := range patch {
		if !validEnvName(key) {
			return nil, targetContentInvalidError(target, "env-merge value_json contains invalid env key", nil).WithDetail("key", key)
		}
		stringValue, ok := value.(string)
		if !ok {
			return nil, targetContentInvalidError(target, "env-merge values must be strings", nil).WithDetail("key", key)
		}
		if strings.ContainsAny(stringValue, "\n\r") {
			return nil, targetContentInvalidError(target, "env-merge value is invalid", nil).WithDetail("key", key)
		}
		values[key] = stringValue
	}
	return values, nil
}

type envLine struct {
	body       string
	ending     string
	key        string
	valueStart int
	valueEnd   int
	assignment bool
}

func mergeEnvContent(content string, patch map[string]string, target store.ProfileTarget) (string, error) {
	lines := splitEnvLines(content)
	seen := map[string]int{}
	for i := range lines {
		parsed, appErr := parseEnvLine(lines[i], target)
		if appErr != nil {
			return "", appErr
		}
		lines[i] = parsed
		if !parsed.assignment {
			continue
		}
		if _, ok := seen[parsed.key]; ok {
			return "", targetContentInvalidError(target, "env target contains duplicate keys", nil).WithDetail("key", parsed.key)
		}
		seen[parsed.key] = i
	}

	var builder strings.Builder
	for _, line := range lines {
		if line.assignment {
			if value, ok := patch[line.key]; ok {
				builder.WriteString(line.body[:line.valueStart])
				builder.WriteString(value)
				builder.WriteString(line.body[line.valueEnd:])
				builder.WriteString(line.ending)
				continue
			}
		}
		builder.WriteString(line.body)
		builder.WriteString(line.ending)
	}

	newKeys := make([]string, 0, len(patch))
	for key := range patch {
		if _, ok := seen[key]; !ok {
			newKeys = append(newKeys, key)
		}
	}
	sort.Strings(newKeys)
	if len(newKeys) == 0 {
		return builder.String(), nil
	}

	current := builder.String()
	if current != "" && !strings.HasSuffix(current, "\n") {
		builder.WriteString("\n")
	}
	for _, key := range newKeys {
		builder.WriteString(key)
		builder.WriteString("=")
		builder.WriteString(patch[key])
		builder.WriteString("\n")
	}
	return builder.String(), nil
}

func splitEnvLines(content string) []envLine {
	lines := []envLine{}
	for content != "" {
		index := strings.IndexByte(content, '\n')
		raw := content
		if index >= 0 {
			raw = content[:index+1]
			content = content[index+1:]
		} else {
			content = ""
		}

		line := envLine{body: raw}
		switch {
		case strings.HasSuffix(raw, "\r\n"):
			line.body = strings.TrimSuffix(raw, "\r\n")
			line.ending = "\r\n"
		case strings.HasSuffix(raw, "\n"):
			line.body = strings.TrimSuffix(raw, "\n")
			line.ending = "\n"
		}
		lines = append(lines, line)
	}
	return lines
}

func parseEnvLine(line envLine, target store.ProfileTarget) (envLine, *AppError) {
	if strings.TrimSpace(line.body) == "" {
		return line, nil
	}
	trimmedLeft := strings.TrimLeft(line.body, " \t")
	if strings.HasPrefix(trimmedLeft, "#") {
		return line, nil
	}

	position := 0
	for position < len(line.body) && isEnvHorizontalSpace(line.body[position]) {
		position++
	}
	if strings.HasPrefix(line.body[position:], "export") {
		afterExport := position + len("export")
		if afterExport < len(line.body) && isEnvHorizontalSpace(line.body[afterExport]) {
			position = afterExport
			for position < len(line.body) && isEnvHorizontalSpace(line.body[position]) {
				position++
			}
		}
	}

	keyStart := position
	if position >= len(line.body) || !isEnvNameStart(line.body[position]) {
		return line, targetContentInvalidError(target, "env target contains unsupported line", nil)
	}
	position++
	for position < len(line.body) && isEnvNamePart(line.body[position]) {
		position++
	}
	key := line.body[keyStart:position]
	for position < len(line.body) && isEnvHorizontalSpace(line.body[position]) {
		position++
	}
	if position >= len(line.body) || line.body[position] != '=' {
		return line, targetContentInvalidError(target, "env target contains unsupported line", nil).WithDetail("key", key)
	}
	position++
	for position < len(line.body) && isEnvHorizontalSpace(line.body[position]) {
		position++
	}
	valueEnd, appErr := envValueEnd(line.body, position, target, key)
	if appErr != nil {
		return line, appErr
	}

	line.key = key
	line.valueStart = position
	line.valueEnd = valueEnd
	line.assignment = true
	return line, nil
}

func envValueEnd(body string, start int, target store.ProfileTarget, key string) (int, *AppError) {
	commentStart, appErr := envCommentSuffixStart(body, start, target, key)
	if appErr != nil {
		return 0, appErr
	}
	if commentStart >= 0 {
		return commentStart, nil
	}
	return len(body), nil
}

func envCommentSuffixStart(body string, start int, target store.ProfileTarget, key string) (int, *AppError) {
	quote := byte(0)
	escaped := false
	for i := start; i < len(body); i++ {
		current := body[i]
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if quote == '"' && current == '\\' {
				escaped = true
				continue
			}
			if current == quote {
				quote = 0
			}
			continue
		}
		if (current == '"' || current == '\'') && (i == start || isEnvHorizontalSpace(body[i-1])) {
			quote = current
			continue
		}
		if current == '#' && i > start && isEnvHorizontalSpace(body[i-1]) {
			return i - 1, nil
		}
	}
	if quote != 0 {
		return 0, targetContentInvalidError(target, "env target contains malformed quoted value", nil).WithDetail("key", key)
	}
	return -1, nil
}

func isEnvHorizontalSpace(value byte) bool {
	return value == ' ' || value == '\t'
}

func isEnvNameStart(value byte) bool {
	return value == '_' || value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}

func isEnvNamePart(value byte) bool {
	return isEnvNameStart(value) || value >= '0' && value <= '9'
}

func mergeObjects(base, patch map[string]any) map[string]any {
	merged := make(map[string]any, len(base)+len(patch))
	for key, value := range base {
		merged[key] = value
	}
	for key, patchValue := range patch {
		patchObject, patchIsObject := patchValue.(map[string]any)
		baseObject, baseIsObject := merged[key].(map[string]any)
		if patchIsObject && baseIsObject {
			merged[key] = mergeObjects(baseObject, patchObject)
			continue
		}
		merged[key] = patchValue
	}
	return merged
}

func marshalIndentedJSONNoEscape(value any) (string, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func targetContentInvalidError(target store.ProfileTarget, message string, cause error) *AppError {
	var appErr *AppError
	if cause == nil {
		appErr = NewError(ErrorTargetInvalid, message)
	} else {
		appErr = WrapError(ErrorTargetInvalid, message, cause)
	}
	return appErr.
		WithDetail("path", target.Path).
		WithDetail("format", target.Format).
		WithDetail("strategy", target.Strategy)
}
