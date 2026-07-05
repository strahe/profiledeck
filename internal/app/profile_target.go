package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/strahe/profiledeck/internal/store"
)

const (
	targetFormatText = "text"
	targetFormatJSON = "json"
	targetFormatTOML = "toml"
	targetFormatEnv  = "env"

	targetStrategyReplaceFile = "replace-file"
	targetStrategyJSONMerge   = "json-merge"
	targetStrategyTOMLMerge   = "toml-merge"
	targetStrategyEnvMerge    = "env-merge"

	maxPreviewBytes = 4096
)

type TextPreview struct {
	Content   string `json:"content"`
	Truncated bool   `json:"truncated"`
}

type ProfileTarget struct {
	ProfileID       string         `json:"profile_id"`
	ProviderID      string         `json:"provider_id"`
	TargetID        string         `json:"target_id"`
	Path            string         `json:"path"`
	Format          string         `json:"format"`
	Strategy        string         `json:"strategy"`
	Enabled         bool           `json:"enabled"`
	ValuePreview    TextPreview    `json:"value_preview"`
	Metadata        map[string]any `json:"metadata"`
	CreatedAtUnixMS int64          `json:"created_at_unix_ms"`
	UpdatedAtUnixMS int64          `json:"updated_at_unix_ms"`
}

type CreateProfileTargetRequest struct {
	ConfigDir    string
	ProfileID    string
	ProviderID   string
	TargetID     string
	Path         string
	Format       string
	Strategy     string
	ValueJSON    string
	Enabled      *bool
	MetadataJSON *string
}

type UpdateProfileTargetRequest struct {
	ConfigDir    string
	ProfileID    string
	ProviderID   string
	TargetID     string
	Path         *string
	Format       *string
	Strategy     *string
	ValueJSON    *string
	Enabled      *bool
	MetadataJSON *string
}

type ListProfileTargetsRequest struct {
	ConfigDir       string
	ProfileID       string
	ProviderID      string
	IncludeDisabled bool
}

type GetProfileTargetRequest struct {
	ConfigDir  string
	ProfileID  string
	ProviderID string
	TargetID   string
}

type DeleteProfileTargetRequest struct {
	ConfigDir  string
	ProfileID  string
	ProviderID string
	TargetID   string
	Confirm    bool
}

func CreateProfileTarget(ctx context.Context, req CreateProfileTargetRequest) (ProfileTarget, error) {
	normalized, appErr := normalizeCreateProfileTarget(req)
	if appErr != nil {
		return ProfileTarget{}, appErr
	}

	db, err := openHealthyStore(ctx, req.ConfigDir, false)
	if err != nil {
		return ProfileTarget{}, err
	}
	defer db.Close()

	if _, err := db.GetProfile(ctx, normalized.ProfileID); err != nil {
		return ProfileTarget{}, mapProfileStoreError(err)
	}
	if _, err := db.GetProvider(ctx, normalized.ProviderID); err != nil {
		return ProfileTarget{}, mapProviderStoreError(err)
	}
	if appErr := ensureProfileTargetPathOwnership(ctx, db, normalized.Path, normalized.PathKey, normalized.ProviderID, normalized.TargetID, nil); appErr != nil {
		return ProfileTarget{}, appErr
	}

	target, err := db.CreateProfileTarget(ctx, normalized)
	if err != nil {
		return ProfileTarget{}, mapTargetStoreError(err)
	}
	return profileTargetFromStore(target)
}

func UpdateProfileTarget(ctx context.Context, req UpdateProfileTargetRequest) (ProfileTarget, error) {
	ids, appErr := normalizeProfileTargetIDs(req.ProfileID, req.ProviderID, req.TargetID)
	if appErr != nil {
		return ProfileTarget{}, appErr
	}
	if req.Path == nil && req.Format == nil && req.Strategy == nil && req.ValueJSON == nil && req.Enabled == nil && req.MetadataJSON == nil {
		return ProfileTarget{}, NewError(ErrorTargetInvalid, "profile target update requires at least one changed field")
	}

	db, err := openHealthyStore(ctx, req.ConfigDir, false)
	if err != nil {
		return ProfileTarget{}, err
	}
	defer db.Close()

	existing, err := db.GetProfileTarget(ctx, ids.ProfileID, ids.ProviderID, ids.TargetID)
	if err != nil {
		return ProfileTarget{}, mapTargetStoreError(err)
	}

	params, appErr := normalizeUpdateProfileTarget(req, existing)
	if appErr != nil {
		return ProfileTarget{}, appErr
	}
	finalPath := existing.Path
	finalPathKey := existing.PathKey
	if finalPathKey == "" {
		finalPathKey = targetPathOwnershipKey(existing.Path)
	}
	if params.Path != nil {
		finalPath = *params.Path
		if params.PathKey != nil {
			finalPathKey = *params.PathKey
		} else {
			finalPathKey = targetPathOwnershipKey(finalPath)
		}
	}
	if appErr := ensureProfileTargetPathOwnership(ctx, db, finalPath, finalPathKey, existing.ProviderID, existing.TargetID, &ids); appErr != nil {
		return ProfileTarget{}, appErr
	}
	target, err := db.UpdateProfileTarget(ctx, params)
	if err != nil {
		return ProfileTarget{}, mapTargetStoreError(err)
	}
	return profileTargetFromStore(target)
}

func ListProfileTargets(ctx context.Context, req ListProfileTargetsRequest) ([]ProfileTarget, error) {
	profileID, appErr := validateID(req.ProfileID, ErrorTargetInvalid)
	if appErr != nil {
		return nil, appErr.WithDetail("field", "profile_id")
	}
	providerID := ""
	if req.ProviderID != "" {
		value, appErr := validateID(req.ProviderID, ErrorTargetInvalid)
		if appErr != nil {
			return nil, appErr.WithDetail("field", "provider_id")
		}
		providerID = value
	}

	db, err := openHealthyStore(ctx, req.ConfigDir, true)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	targets, err := db.ListProfileTargets(ctx, profileID, providerID, req.IncludeDisabled)
	if err != nil {
		return nil, WrapError(ErrorStoreStatusFailed, "failed to list profile targets", err)
	}
	result := make([]ProfileTarget, 0, len(targets))
	for _, target := range targets {
		value, err := profileTargetFromStore(target)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, nil
}

func GetProfileTarget(ctx context.Context, req GetProfileTargetRequest) (ProfileTarget, error) {
	ids, appErr := normalizeProfileTargetIDs(req.ProfileID, req.ProviderID, req.TargetID)
	if appErr != nil {
		return ProfileTarget{}, appErr
	}

	db, err := openHealthyStore(ctx, req.ConfigDir, true)
	if err != nil {
		return ProfileTarget{}, err
	}
	defer db.Close()

	target, err := db.GetProfileTarget(ctx, ids.ProfileID, ids.ProviderID, ids.TargetID)
	if err != nil {
		return ProfileTarget{}, mapTargetStoreError(err)
	}
	return profileTargetFromStore(target)
}

func DeleteProfileTarget(ctx context.Context, req DeleteProfileTargetRequest) (DeleteResult, error) {
	ids, appErr := normalizeProfileTargetIDs(req.ProfileID, req.ProviderID, req.TargetID)
	if appErr != nil {
		return DeleteResult{}, appErr
	}
	if !req.Confirm {
		return DeleteResult{}, NewError(ErrorConfirmationRequired, "profile target delete requires confirmation")
	}

	db, err := openHealthyStore(ctx, req.ConfigDir, false)
	if err != nil {
		return DeleteResult{}, err
	}
	defer db.Close()

	if err := db.DeleteProfileTarget(ctx, ids.ProfileID, ids.ProviderID, ids.TargetID); err != nil {
		return DeleteResult{}, mapTargetStoreError(err)
	}
	return DeleteResult{ID: ids.TargetID, Deleted: true}, nil
}

type profileTargetIDs struct {
	ProfileID  string
	ProviderID string
	TargetID   string
}

func normalizeCreateProfileTarget(req CreateProfileTargetRequest) (store.CreateProfileTargetParams, *AppError) {
	ids, appErr := normalizeProfileTargetIDs(req.ProfileID, req.ProviderID, req.TargetID)
	if appErr != nil {
		return store.CreateProfileTargetParams{}, appErr
	}
	path, appErr := validateTargetPath(req.Path)
	if appErr != nil {
		return store.CreateProfileTargetParams{}, appErr
	}
	pathKey := targetPathOwnershipKey(path)
	format, strategy, valueJSON, appErr := normalizeTargetFormatStrategyAndValue(req.Format, req.Strategy, req.ValueJSON)
	if appErr != nil {
		return store.CreateProfileTargetParams{}, appErr
	}
	metadataJSON, appErr := normalizeMetadataJSON(req.MetadataJSON, ErrorTargetInvalid)
	if appErr != nil {
		return store.CreateProfileTargetParams{}, appErr
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	return store.CreateProfileTargetParams{
		ProfileID:    ids.ProfileID,
		ProviderID:   ids.ProviderID,
		TargetID:     ids.TargetID,
		Path:         path,
		PathKey:      pathKey,
		Format:       format,
		Strategy:     strategy,
		ValueJSON:    valueJSON,
		Enabled:      enabled,
		MetadataJSON: metadataJSON,
	}, nil
}

func normalizeUpdateProfileTarget(req UpdateProfileTargetRequest, existing store.ProfileTarget) (store.UpdateProfileTargetParams, *AppError) {
	finalFormat := existing.Format
	finalStrategy := existing.Strategy
	finalValueJSON := existing.ValueJSON

	params := store.UpdateProfileTargetParams{
		ProfileID:  existing.ProfileID,
		ProviderID: existing.ProviderID,
		TargetID:   existing.TargetID,
	}
	if req.Path != nil {
		path, appErr := validateTargetPath(*req.Path)
		if appErr != nil {
			return store.UpdateProfileTargetParams{}, appErr
		}
		params.Path = &path
		pathKey := targetPathOwnershipKey(path)
		params.PathKey = &pathKey
	}
	if req.Format != nil {
		format, appErr := validateTargetFormat(*req.Format)
		if appErr != nil {
			return store.UpdateProfileTargetParams{}, appErr
		}
		finalFormat = format
		params.Format = &format
	}
	if req.Strategy != nil {
		strategy, appErr := validateTargetStrategy(*req.Strategy)
		if appErr != nil {
			return store.UpdateProfileTargetParams{}, appErr
		}
		finalStrategy = strategy
		params.Strategy = &strategy
	}
	if req.ValueJSON != nil {
		finalValueJSON = *req.ValueJSON
	}
	_, _, normalizedValueJSON, appErr := normalizeTargetFormatStrategyAndValue(finalFormat, finalStrategy, finalValueJSON)
	if appErr != nil {
		return store.UpdateProfileTargetParams{}, appErr
	}
	if req.ValueJSON != nil {
		params.ValueJSON = &normalizedValueJSON
	}
	if req.Format != nil || req.Strategy != nil {
		if req.ValueJSON == nil {
			params.ValueJSON = &normalizedValueJSON
		}
	}
	if req.Enabled != nil {
		enabled := *req.Enabled
		params.Enabled = &enabled
	}
	if req.MetadataJSON != nil {
		metadataJSON, appErr := normalizeMetadataJSON(req.MetadataJSON, ErrorTargetInvalid)
		if appErr != nil {
			return store.UpdateProfileTargetParams{}, appErr
		}
		params.MetadataJSON = &metadataJSON
	}

	return params, nil
}

func ensureProfileTargetPathOwnership(ctx context.Context, db *store.Store, path string, pathKey string, providerID string, targetID string, current *profileTargetIDs) *AppError {
	targets, err := db.ListProfileTargetsByPathKey(ctx, pathKey)
	if err != nil {
		return WrapError(ErrorStoreStatusFailed, "failed to inspect target path ownership", err)
	}
	for _, target := range targets {
		if current != nil && target.ProfileID == current.ProfileID && target.ProviderID == current.ProviderID && target.TargetID == current.TargetID {
			continue
		}
		// One logical target may be shared across profiles; other path reuse would make apply ownership ambiguous.
		if target.ProviderID == providerID && target.TargetID == targetID {
			continue
		}
		return NewError(ErrorTargetAlreadyExists, "target path is already owned by another profile target").
			WithDetail("path", path).
			WithDetail("owner_profile_id", target.ProfileID).
			WithDetail("owner_provider_id", target.ProviderID).
			WithDetail("owner_target_id", target.TargetID)
	}
	return nil
}

func targetPathOwnershipKey(path string) string {
	return targetPathOwnershipKeyForOS(path, runtime.GOOS)
}

func targetPathOwnershipKeyForOS(path string, goos string) string {
	switch goos {
	case "darwin", "windows":
		// Keep the I/O path exact, but compare ownership using the default case-insensitive filesystem semantics.
		return strings.ToLower(path)
	default:
		return path
	}
}

func normalizeProfileTargetIDs(profileID string, providerID string, targetID string) (profileTargetIDs, *AppError) {
	normalizedProfileID, appErr := validateID(profileID, ErrorTargetInvalid)
	if appErr != nil {
		return profileTargetIDs{}, appErr.WithDetail("field", "profile_id")
	}
	normalizedProviderID, appErr := validateID(providerID, ErrorTargetInvalid)
	if appErr != nil {
		return profileTargetIDs{}, appErr.WithDetail("field", "provider_id")
	}
	normalizedTargetID, appErr := validateID(targetID, ErrorTargetInvalid)
	if appErr != nil {
		return profileTargetIDs{}, appErr.WithDetail("field", "target_id")
	}
	return profileTargetIDs{
		ProfileID:  normalizedProfileID,
		ProviderID: normalizedProviderID,
		TargetID:   normalizedTargetID,
	}, nil
}

func validateTargetPath(value string) (string, *AppError) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", NewError(ErrorTargetInvalid, "target path is required")
	}
	value = filepath.Clean(value)
	if !filepath.IsAbs(value) {
		return "", NewError(ErrorTargetInvalid, "target path must be absolute")
	}
	return value, nil
}

func normalizeTargetFormatStrategyAndValue(formatValue string, strategyValue string, rawValueJSON string) (string, string, string, *AppError) {
	format, appErr := validateTargetFormat(formatValue)
	if appErr != nil {
		return "", "", "", appErr
	}
	strategy, appErr := validateTargetStrategy(strategyValue)
	if appErr != nil {
		return "", "", "", appErr
	}
	if !targetFormatStrategyAllowed(format, strategy) {
		return "", "", "", NewError(ErrorTargetInvalid, "target format and strategy are incompatible")
	}

	value, appErr := decodeSingleJSONObject(rawValueJSON, ErrorTargetInvalid, "value_json")
	if appErr != nil {
		return "", "", "", appErr
	}
	if appErr := validateTargetValueShape(format, strategy, value); appErr != nil {
		return "", "", "", appErr
	}
	normalized, err := json.Marshal(value)
	if err != nil {
		return "", "", "", WrapError(ErrorTargetInvalid, "failed to normalize value_json", err)
	}
	return format, strategy, string(normalized), nil
}

func validateTargetFormat(value string) (string, *AppError) {
	value = strings.TrimSpace(value)
	switch value {
	case targetFormatText, targetFormatJSON, targetFormatTOML, targetFormatEnv:
		return value, nil
	default:
		return "", NewError(ErrorTargetInvalid, "target format is invalid")
	}
}

func validateTargetStrategy(value string) (string, *AppError) {
	value = strings.TrimSpace(value)
	switch value {
	case targetStrategyReplaceFile, targetStrategyJSONMerge, targetStrategyTOMLMerge, targetStrategyEnvMerge:
		return value, nil
	default:
		return "", NewError(ErrorTargetInvalid, "target strategy is invalid")
	}
}

func targetFormatStrategyAllowed(format string, strategy string) bool {
	switch strategy {
	case targetStrategyReplaceFile:
		return format == targetFormatText || format == targetFormatJSON || format == targetFormatTOML || format == targetFormatEnv
	case targetStrategyJSONMerge:
		return format == targetFormatJSON
	case targetStrategyTOMLMerge:
		return format == targetFormatTOML
	case targetStrategyEnvMerge:
		return format == targetFormatEnv
	default:
		return false
	}
}

func validateTargetValueShape(format string, strategy string, value map[string]any) *AppError {
	if strategy == targetStrategyReplaceFile {
		content, ok := value["content"].(string)
		if !ok || len(value) != 1 {
			return NewError(ErrorTargetInvalid, `replace-file value_json must be {"content": string}`)
		}
		_ = content
		return nil
	}
	if containsReservedEnvRefObject(value) {
		return NewError(ErrorTargetInvalid, "value_json cannot contain ProfileDeck env reference objects")
	}
	target := store.ProfileTarget{Format: format, Strategy: strategy}
	switch strategy {
	case targetStrategyTOMLMerge:
		if _, appErr := jsonObjectToTOML(value, target); appErr != nil {
			return appErr
		}
	case targetStrategyEnvMerge:
		if _, appErr := envPatchValues(value, target); appErr != nil {
			return appErr
		}
	}
	return nil
}

func decodeSingleJSONObject(raw string, code ErrorCode, field string) (map[string]any, *AppError) {
	if strings.TrimSpace(raw) == "" {
		return nil, NewError(code, fmt.Sprintf("%s is required", field))
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, NewError(code, fmt.Sprintf("%s must be valid JSON object", field))
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, NewError(code, fmt.Sprintf("%s must contain one JSON object", field))
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, NewError(code, fmt.Sprintf("%s must be a JSON object", field))
	}
	if object == nil {
		return map[string]any{}, nil
	}
	return object, nil
}

func envRefDisplay(value any) (string, bool) {
	object, ok := value.(map[string]any)
	if !ok || len(object) != 2 {
		return "", false
	}
	refType, ok := object["ref_type"].(string)
	if !ok || refType != "env" {
		return "", false
	}
	name, ok := object["name"].(string)
	if !ok || !validEnvName(name) {
		return "", false
	}
	return "<env:" + name + ">", true
}

func containsReservedEnvRefObject(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		refType, hasRefType := typed["ref_type"].(string)
		_, hasName := typed["name"]
		if hasRefType && refType == "env" && hasName {
			return true
		}
		for _, child := range typed {
			if containsReservedEnvRefObject(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if containsReservedEnvRefObject(child) {
				return true
			}
		}
	}
	return false
}

func validEnvName(value string) bool {
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

func profileTargetFromStore(target store.ProfileTarget) (ProfileTarget, error) {
	metadata, err := metadataFromJSON(target.MetadataJSON)
	if err != nil {
		return ProfileTarget{}, err
	}
	preview, err := targetValuePreview(target.Format, target.Strategy, target.ValueJSON)
	if err != nil {
		return ProfileTarget{}, err
	}
	return ProfileTarget{
		ProfileID:       target.ProfileID,
		ProviderID:      target.ProviderID,
		TargetID:        target.TargetID,
		Path:            target.Path,
		Format:          target.Format,
		Strategy:        target.Strategy,
		Enabled:         target.Enabled,
		ValuePreview:    preview,
		Metadata:        redactMetadata(metadata).(map[string]any),
		CreatedAtUnixMS: target.CreatedAtUnixMS,
		UpdatedAtUnixMS: target.UpdatedAtUnixMS,
	}, nil
}

func targetValuePreview(format string, strategy string, raw string) (TextPreview, error) {
	value, appErr := decodeSingleJSONObject(raw, ErrorStoreSchemaInvalid, "stored value_json")
	if appErr != nil {
		return TextPreview{}, appErr
	}
	if strategy == targetStrategyReplaceFile {
		return replaceFileTargetValuePreview(value)
	}
	redacted := redactTargetValue(format, strategy, value)
	rawPreview, err := marshalJSONNoEscape(redacted)
	if err != nil {
		return TextPreview{}, WrapError(ErrorStoreSchemaInvalid, "failed to encode target value preview", err)
	}
	return truncatePreview(string(rawPreview)), nil
}

func replaceFileTargetValuePreview(value map[string]any) (TextPreview, error) {
	content, ok := value["content"].(string)
	if !ok || len(value) != 1 {
		return TextPreview{}, NewError(ErrorStoreSchemaInvalid, `stored replace-file value_json must be {"content": string}`)
	}
	contentPreview := previewSensitiveText(content)
	rawPreview, err := marshalJSONNoEscape(map[string]any{"content": contentPreview.Content})
	if err != nil {
		return TextPreview{}, WrapError(ErrorStoreSchemaInvalid, "failed to encode target value preview", err)
	}
	preview := truncatePreview(string(rawPreview))
	preview.Truncated = preview.Truncated || contentPreview.Truncated
	return preview, nil
}

func marshalJSONNoEscape(value any) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSpace(buf.Bytes()), nil
}

func redactTargetValue(_ string, strategy string, value map[string]any) any {
	if strategy == targetStrategyReplaceFile {
		redacted := make(map[string]any, len(value))
		for key, child := range value {
			if key == "content" {
				if content, ok := child.(string); ok {
					redacted[key] = redactSensitiveText(content)
					continue
				}
			}
			redacted[key] = redactStructuredSecrets(child)
		}
		return redacted
	}
	return redactStructuredSecrets(value)
}

func redactStructuredSecrets(value any) any {
	if display, ok := envRefDisplay(value); ok {
		return display
	}
	switch typed := value.(type) {
	case map[string]any:
		redacted := make(map[string]any, len(typed))
		for key, child := range typed {
			if isSensitiveOutputKey(key) {
				if display, ok := envRefDisplay(child); ok {
					redacted[key] = display
				} else {
					redacted[key] = redactedValue
				}
				continue
			}
			redacted[key] = redactStructuredSecrets(child)
		}
		return redacted
	case []any:
		redacted := make([]any, len(typed))
		for i, child := range typed {
			redacted[i] = redactStructuredSecrets(child)
		}
		return redacted
	default:
		return typed
	}
}

func redactSensitiveText(value string) string {
	if redacted, ok := redactJSONText(value); ok {
		return redacted
	}
	lines := strings.SplitAfter(value, "\n")
	for i, line := range lines {
		lines[i] = redactSensitiveTextLine(line)
	}
	return strings.Join(lines, "")
}

func previewSensitiveText(value string) TextPreview {
	preview := truncatePreview(value)
	preview.Content = redactSensitiveText(preview.Content)
	return preview
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
	raw, err := marshalJSONNoEscape(redactStructuredSecrets(decoded))
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
		if valueStart >= len(value) || value[valueStart] != '"' || !isSensitiveOutputKey(key) {
			i = keyEnd
			continue
		}
		valueEnd := jsonStringEndIndex(value, valueStart)
		if valueEnd < 0 {
			if !changed {
				builder.Grow(len(value))
			}
			builder.WriteString(value[last:valueStart])
			builder.WriteString(`"` + redactedValue + `"`)
			last = len(value)
			changed = true
			break
		}
		if !changed {
			builder.Grow(len(value))
		}
		builder.WriteString(value[last:valueStart])
		builder.WriteString(`"` + redactedValue + `"`)
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
		if !isSensitiveOutputKey(key) {
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
		builder.WriteString(redactedValue)
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
	return value == '_' || value == '-' || value == '.' ||
		value >= 'A' && value <= 'Z' ||
		value >= 'a' && value <= 'z' ||
		value >= '0' && value <= '9'
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

func truncatePreview(value string) TextPreview {
	if len(value) <= maxPreviewBytes {
		return TextPreview{Content: value}
	}
	end := 0
	for i := range value {
		if i > maxPreviewBytes {
			break
		}
		end = i
	}
	if end == 0 {
		end = maxPreviewBytes
	}
	return TextPreview{
		Content:   value[:end],
		Truncated: true,
	}
}

func mapTargetStoreError(err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return NewError(ErrorTargetNotFound, "profile target not found")
	case errors.Is(err, store.ErrPathOwned):
		return NewError(ErrorTargetAlreadyExists, "target path is already owned by another profile target")
	case errors.Is(err, store.ErrAlreadyExists):
		return NewError(ErrorTargetAlreadyExists, "profile target already exists")
	default:
		return WrapError(ErrorStoreStatusFailed, "profile target store operation failed", err)
	}
}
