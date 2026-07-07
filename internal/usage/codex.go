package usage

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/strahe/profiledeck/internal/codexconfig"
)

const maxCodexSessionLineBytes = 16 * 1024 * 1024

func DefaultCodexDir() (string, error) {
	home, err := codexconfig.ResolveHome("")
	if err != nil {
		return "", err
	}
	return home.Dir, nil
}

func ListCodexSessionFiles(codexDir string) ([]SourceFile, error) {
	codexDir = strings.TrimSpace(codexDir)
	if codexDir == "" {
		defaultDir, err := DefaultCodexDir()
		if err != nil {
			return nil, err
		}
		codexDir = defaultDir
	}

	sessionsDir := filepath.Join(codexDir, "sessions")
	if _, err := os.Stat(sessionsDir); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var files []SourceFile
	err := filepath.WalkDir(sessionsDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(path), ".jsonl") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		sourceKey, err := SourceKey(path)
		if err != nil {
			return err
		}
		files = append(files, SourceFile{
			Path:           path,
			SourceKey:      sourceKey,
			ModifiedUnixMS: info.ModTime().UnixMilli(),
			SizeBytes:      info.Size(),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].Path < files[j].Path
	})
	return files, nil
}

func ParseCodexSessionFile(file SourceFile) (FileParseResult, error) {
	return parseCodexSessionFile(file, maxCodexSessionLineBytes)
}

func parseCodexSessionFile(file SourceFile, maxLineBytes int) (FileParseResult, error) {
	handle, err := os.Open(file.Path)
	if err != nil {
		return FileParseResult{}, err
	}
	defer handle.Close()

	result := FileParseResult{}
	reader := bufio.NewReaderSize(handle, 64*1024)

	sessionID := strings.TrimSuffix(filepath.Base(file.Path), filepath.Ext(file.Path))
	hasLogSessionID := false
	model := ""
	previousTotals := map[string]TokenCounts{}
	var lineIndex int64
	for {
		rawLine, tooLong, err := readCodexSessionLine(reader, maxLineBytes)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return FileParseResult{}, err
		}
		lineIndex++
		if tooLong {
			result.InvalidLines++
			continue
		}

		line := bytes.TrimSpace(rawLine)
		if len(line) == 0 {
			continue
		}

		var payload any
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.UseNumber()
		if err := decoder.Decode(&payload); err != nil {
			result.InvalidLines++
			continue
		}
		object, ok := payload.(map[string]any)
		if !ok {
			result.UnsupportedLines++
			continue
		}

		eventType := eventTypeFromObject(object)
		if found := sessionIDFromObject(object, eventType); found != "" {
			sessionID = found
			hasLogSessionID = true
		}
		if found := modelFromObject(object); found != "" {
			model = found
		}

		counts, usageKind, cumulative, ok := tokenCountsFromObject(object)
		if !ok {
			continue
		}
		counts.normalizeTotal()
		if !counts.valid() {
			result.UnsupportedLines++
			continue
		}

		delta := counts
		if cumulative {
			previous := previousTotals[sessionID]
			delta = deltaFromCumulative(previous, counts)
			previousTotals[sessionID] = counts
		}
		delta.normalizeTotal()
		if !delta.valid() {
			result.UnsupportedLines++
			continue
		}
		if delta.empty() {
			continue
		}

		costMicros, costStatus := EstimateCostMicros(model, delta)
		result.Events = append(result.Events, Event{
			ID:                  EventID(ProviderCodex, SourceCodexSessionJSONL, lineIndex, eventIdentitySessionID(sessionID, file.SourceKey, hasLogSessionID), model, delta),
			ProviderID:          ProviderCodex,
			Source:              SourceCodexSessionJSONL,
			SourceKey:           file.SourceKey,
			SessionID:           sessionID,
			Model:               model,
			OccurredAtUnixMS:    occurredAtUnixMS(object),
			InputTokens:         delta.InputTokens,
			CachedInputTokens:   delta.CachedInputTokens,
			OutputTokens:        delta.OutputTokens,
			TotalTokens:         delta.TotalTokens,
			EstimatedCostMicros: costMicros,
			CostStatus:          costStatus,
			MetadataJSON:        MetadataJSON(lineIndex, eventType, usageKind),
		})
	}
	return result, nil
}

func readCodexSessionLine(reader *bufio.Reader, limit int) ([]byte, bool, error) {
	if limit <= 0 {
		limit = maxCodexSessionLineBytes
	}

	var line []byte
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(fragment) > 0 {
			if len(line)+len(fragment) > limit {
				switch {
				case err == nil || errors.Is(err, io.EOF):
					return nil, true, nil
				case errors.Is(err, bufio.ErrBufferFull):
					if discardErr := discardCodexSessionLine(reader); discardErr != nil {
						return nil, false, discardErr
					}
					return nil, true, nil
				default:
					return nil, false, err
				}
			}
			line = append(line, fragment...)
		}

		switch {
		case err == nil:
			return line, false, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			if len(line) == 0 {
				return nil, false, io.EOF
			}
			return line, false, nil
		default:
			return nil, false, err
		}
	}
}

func discardCodexSessionLine(reader *bufio.Reader) error {
	for {
		_, err := reader.ReadSlice('\n')
		switch {
		case err == nil:
			return nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			return nil
		default:
			return err
		}
	}
}

func eventIdentitySessionID(sessionID string, sourceKey string, hasLogSessionID bool) string {
	if hasLogSessionID {
		return sessionID
	}
	// Without a log-provided session id, the filename fallback is not globally
	// unique. Include the cursor key only for this fallback identity.
	return sourceKey + "\x00" + sessionID
}

func tokenCountsFromObject(object map[string]any) (TokenCounts, string, bool, bool) {
	if usageObject, ok := usageObjectFromKnownPaths(object, "total_token_usage"); ok {
		counts, valid := tokenCountsFromUsageObject(usageObject)
		return counts, "total_token_usage", true, valid
	}
	if usageObject, ok := usageObjectFromKnownPaths(object, "last_token_usage"); ok {
		counts, valid := tokenCountsFromUsageObject(usageObject)
		return counts, "last_token_usage", false, valid
	}
	return TokenCounts{}, "", false, false
}

func usageObjectFromKnownPaths(object map[string]any, key string) (map[string]any, bool) {
	for _, path := range [][]string{
		{"payload", "info", key},
		{"payload", key},
		{"info", key},
		{key},
	} {
		if value, ok := objectAtPath(object, path...); ok {
			return value, true
		}
	}
	return nil, false
}

func tokenCountsFromUsageObject(object map[string]any) (TokenCounts, bool) {
	input, inputOK := int64Field(object, "input_tokens")
	cached, _ := int64Field(object, "cached_input_tokens")
	if cached == 0 {
		cached, _ = int64Field(object, "cache_read_input_tokens")
	}
	output, outputOK := int64Field(object, "output_tokens")
	total, _ := int64Field(object, "total_tokens")
	if !inputOK && !outputOK && cached == 0 && total == 0 {
		return TokenCounts{}, false
	}
	return TokenCounts{
		InputTokens:       input,
		CachedInputTokens: cached,
		OutputTokens:      output,
		TotalTokens:       total,
	}, true
}

func deltaFromCumulative(previous TokenCounts, current TokenCounts) TokenCounts {
	if previous.empty() || current.InputTokens < previous.InputTokens ||
		current.OutputTokens < previous.OutputTokens {
		return current
	}

	cachedInputTokens := current.CachedInputTokens - previous.CachedInputTokens
	if current.CachedInputTokens < previous.CachedInputTokens {
		cachedInputTokens = current.CachedInputTokens
	}
	totalTokens := current.TotalTokens - previous.TotalTokens
	if current.TotalTokens < previous.TotalTokens {
		totalTokens = current.InputTokens - previous.InputTokens + current.OutputTokens - previous.OutputTokens
	}
	return TokenCounts{
		InputTokens:       current.InputTokens - previous.InputTokens,
		CachedInputTokens: cachedInputTokens,
		OutputTokens:      current.OutputTokens - previous.OutputTokens,
		TotalTokens:       totalTokens,
	}
}

func eventTypeFromObject(object map[string]any) string {
	if value := stringField(object, "type"); value != "" {
		return value
	}
	if payload, ok := objectField(object, "payload"); ok {
		if value := stringField(payload, "type"); value != "" {
			return value
		}
	}
	if msg, ok := objectField(object, "msg"); ok {
		if value := stringField(msg, "type"); value != "" {
			return value
		}
	}
	return ""
}

func sessionIDFromObject(object map[string]any, eventType string) string {
	if value := stringField(object, "session_id"); value != "" {
		return value
	}
	if payload, ok := objectField(object, "payload"); ok {
		if value := stringField(payload, "session_id"); value != "" {
			return value
		}
	}
	if eventType == "session_meta" {
		if value := stringField(object, "id"); value != "" {
			return value
		}
		if payload, ok := objectField(object, "payload"); ok {
			if value := stringField(payload, "id"); value != "" {
				return value
			}
		}
	}
	return ""
}

func modelFromObject(object map[string]any) string {
	for _, holder := range []map[string]any{object} {
		if value := modelFromMap(holder); value != "" {
			return normalizeCodexModel(value)
		}
	}

	if payload, ok := objectField(object, "payload"); ok {
		if value := modelFromMap(payload); value != "" {
			return normalizeCodexModel(value)
		}
		if info, ok := objectField(payload, "info"); ok {
			if value := modelFromMap(info); value != "" {
				return normalizeCodexModel(value)
			}
		}
	}

	if context, ok := objectField(object, "context"); ok {
		if value := modelFromMap(context); value != "" {
			return normalizeCodexModel(value)
		}
	}
	return ""
}

func modelFromMap(object map[string]any) string {
	for _, key := range []string{"model", "model_name", "model_id", "model_slug"} {
		if value := stringField(object, key); value != "" {
			return value
		}
	}
	return ""
}

func normalizeCodexModel(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	if index := strings.LastIndex(value, "/"); index >= 0 {
		value = value[index+1:]
	}
	if len(value) > len("-2006-01-02") {
		suffix := value[len(value)-len("-2006-01-02"):]
		if isISODaySuffix(suffix) {
			value = value[:len(value)-len(suffix)]
		}
	}
	if len(value) > len("-20060102") {
		suffix := value[len(value)-len("-20060102"):]
		if isCompactDaySuffix(suffix) {
			value = value[:len(value)-len(suffix)]
		}
	}
	return value
}

func isISODaySuffix(value string) bool {
	return len(value) == len("-2006-01-02") &&
		value[0] == '-' &&
		isDigitString(value[1:5]) &&
		value[5] == '-' &&
		isDigitString(value[6:8]) &&
		value[8] == '-' &&
		isDigitString(value[9:11])
}

func isCompactDaySuffix(value string) bool {
	return len(value) == len("-20060102") &&
		value[0] == '-' &&
		isDigitString(value[1:])
}

func isDigitString(value string) bool {
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return value != ""
}

func occurredAtUnixMS(object map[string]any) int64 {
	for _, key := range []string{"timestamp", "created_at", "time"} {
		if value, ok := object[key]; ok {
			if timestamp := unixMSFromValue(value); timestamp > 0 {
				return timestamp
			}
		}
	}
	return 0
}

func unixMSFromValue(value any) int64 {
	switch typed := value.(type) {
	case string:
		typed = strings.TrimSpace(typed)
		if typed == "" {
			return 0
		}
		if parsed, err := time.Parse(time.RFC3339Nano, typed); err == nil {
			return parsed.UnixMilli()
		}
		return 0
	case json.Number:
		number, err := typed.Int64()
		if err == nil {
			return unixMSFromNumber(number)
		}
		value, err := typed.Float64()
		if err != nil {
			return 0
		}
		return unixMSFromFloat(value)
	case float64:
		return unixMSFromFloat(typed)
	default:
		return 0
	}
}

func unixMSFromFloat(value float64) int64 {
	if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}

	var millis float64
	switch {
	case value >= 1_000_000_000_000_000_000:
		millis = value / 1_000_000
	case value >= 100_000_000_000_000:
		millis = value / 1_000
	case value >= 100_000_000_000:
		millis = value
	default:
		millis = value * 1000
	}
	if millis <= 0 || millis > float64(math.MaxInt64) {
		return 0
	}
	return int64(math.Round(millis))
}

func unixMSFromNumber(value int64) int64 {
	if value <= 0 {
		return 0
	}
	switch {
	case value >= 1_000_000_000_000_000_000:
		return value / 1_000_000
	case value >= 100_000_000_000_000:
		return value / 1_000
	case value >= 100_000_000_000:
		return value
	default:
		return value * 1000
	}
}

func objectAtPath(object map[string]any, path ...string) (map[string]any, bool) {
	if len(path) == 0 {
		return nil, false
	}
	current := object
	for _, key := range path[:len(path)-1] {
		next, ok := objectField(current, key)
		if !ok {
			return nil, false
		}
		current = next
	}
	return objectField(current, path[len(path)-1])
}

func objectField(object map[string]any, key string) (map[string]any, bool) {
	value, ok := object[key]
	if !ok {
		return nil, false
	}
	child, ok := value.(map[string]any)
	return child, ok
}

func stringField(object map[string]any, key string) string {
	value, ok := object[key]
	if !ok {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}

func int64Field(object map[string]any, key string) (int64, bool) {
	value, ok := object[key]
	if !ok {
		return 0, false
	}
	switch typed := value.(type) {
	case json.Number:
		number, err := typed.Int64()
		if err != nil {
			return 0, false
		}
		return number, true
	case float64:
		return int64(typed), true
	case int64:
		return typed, true
	case int:
		return int64(typed), true
	default:
		return 0, false
	}
}

func (tokens *TokenCounts) normalizeTotal() {
	if tokens.TotalTokens == 0 {
		tokens.TotalTokens = tokens.InputTokens + tokens.OutputTokens
	}
}

func (tokens TokenCounts) valid() bool {
	return tokens.InputTokens >= 0 &&
		tokens.CachedInputTokens >= 0 &&
		tokens.OutputTokens >= 0 &&
		tokens.TotalTokens >= 0
}

func (tokens TokenCounts) empty() bool {
	return tokens.InputTokens == 0 &&
		tokens.CachedInputTokens == 0 &&
		tokens.OutputTokens == 0 &&
		tokens.TotalTokens == 0
}
