package usage

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseCodexSessionFileComputesCumulativeDeltas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	writeTestFile(t, path, strings.Join([]string{
		`{"type":"session_meta","session_id":"session-1"}`,
		`{"type":"turn_context","model":"gpt-5.3-codex"}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":20,"output_tokens":10,"total_tokens":110},"prompt":"do not store me","content":"do not store me"}},"timestamp":"2026-07-06T00:00:00Z"}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":150,"cached_input_tokens":25,"output_tokens":15,"total_tokens":165}}},"timestamp":"2026-07-06T00:01:00Z"}`,
	}, "\n"))

	sourceKey, err := SourceKey(path)
	if err != nil {
		t.Fatalf("expected source key, got %v", err)
	}
	result, err := ParseCodexSessionFile(SourceFile{Path: path, SourceKey: sourceKey})
	if err != nil {
		t.Fatalf("expected parse to succeed, got %v", err)
	}
	if result.InvalidLines != 0 || result.UnsupportedLines != 0 {
		t.Fatalf("expected no invalid or unsupported lines, got %#v", result)
	}
	if len(result.Events) != 2 {
		t.Fatalf("expected two usage events, got %d", len(result.Events))
	}

	first := result.Events[0]
	if first.SessionID != "session-1" || first.Model != "gpt-5.3-codex" {
		t.Fatalf("unexpected first event identity: %#v", first)
	}
	if first.InputTokens != 100 || first.CachedInputTokens != 20 || first.OutputTokens != 10 || first.TotalTokens != 110 {
		t.Fatalf("unexpected first token counts: %#v", first)
	}
	if first.CostStatus != CostStatusEstimated || first.EstimatedCostMicros == nil {
		t.Fatalf("expected first event to have estimated cost, got %#v", first)
	}
	if strings.Contains(first.MetadataJSON, "do not store me") || strings.Contains(first.MetadataJSON, "prompt") || strings.Contains(first.MetadataJSON, "content") {
		t.Fatalf("expected metadata to exclude raw prompt/content, got %s", first.MetadataJSON)
	}

	second := result.Events[1]
	if second.InputTokens != 50 || second.CachedInputTokens != 5 || second.OutputTokens != 5 || second.TotalTokens != 55 {
		t.Fatalf("unexpected second delta counts: %#v", second)
	}
}

func TestParseCodexSessionFileDoesNotResetWhenCachedCumulativeDecreases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	writeTestFile(t, path, strings.Join([]string{
		`{"type":"session_meta","session_id":"session-1"}`,
		`{"type":"turn_context","model":"gpt-5.3-codex"}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":20,"output_tokens":10,"total_tokens":110}}}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":150,"cached_input_tokens":5,"output_tokens":15,"total_tokens":165}}}}`,
	}, "\n"))

	sourceKey, err := SourceKey(path)
	if err != nil {
		t.Fatalf("expected source key, got %v", err)
	}
	result, err := ParseCodexSessionFile(SourceFile{Path: path, SourceKey: sourceKey})
	if err != nil {
		t.Fatalf("expected parse to succeed, got %v", err)
	}
	if len(result.Events) != 2 {
		t.Fatalf("expected two usage events, got %d", len(result.Events))
	}

	second := result.Events[1]
	if second.InputTokens != 50 || second.CachedInputTokens != 5 || second.OutputTokens != 5 || second.TotalTokens != 55 {
		t.Fatalf("expected cached decrease not to reset primary cumulative counters, got %#v", second)
	}
}

func TestParseCodexSessionFileUsesLastTokenUsageFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	writeTestFile(t, path, strings.Join([]string{
		`{"type":"session_meta","session_id":"session-1"}`,
		`{"type":"turn_context","model":"unknown-model"}`,
		`{"type":"event_msg","payload":{"type":"token_count","last_token_usage":{"input_tokens":5,"cache_read_input_tokens":1,"output_tokens":2}}}`,
	}, "\n"))

	sourceKey, err := SourceKey(path)
	if err != nil {
		t.Fatalf("expected source key, got %v", err)
	}
	result, err := ParseCodexSessionFile(SourceFile{Path: path, SourceKey: sourceKey})
	if err != nil {
		t.Fatalf("expected parse to succeed, got %v", err)
	}
	if len(result.Events) != 1 {
		t.Fatalf("expected one usage event, got %d", len(result.Events))
	}

	event := result.Events[0]
	if event.InputTokens != 5 || event.CachedInputTokens != 1 || event.OutputTokens != 2 || event.TotalTokens != 7 {
		t.Fatalf("unexpected fallback token counts: %#v", event)
	}
	if event.CostStatus != CostStatusUnknown || event.EstimatedCostMicros != nil {
		t.Fatalf("expected unknown cost for unknown model, got %#v", event)
	}
}

func TestParseCodexSessionFileUsesTokenCountInfoModel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	writeTestFile(t, path, `{"type":"event_msg","payload":{"type":"token_count","info":{"model_name":"openai/gpt-5.3-codex-2026-07-06","total_token_usage":{"input_tokens":10,"cached_input_tokens":2,"output_tokens":3}}},"timestamp":1751846400000000}`)

	sourceKey, err := SourceKey(path)
	if err != nil {
		t.Fatalf("expected source key, got %v", err)
	}
	result, err := ParseCodexSessionFile(SourceFile{Path: path, SourceKey: sourceKey})
	if err != nil {
		t.Fatalf("expected parse to succeed, got %v", err)
	}
	if len(result.Events) != 1 {
		t.Fatalf("expected one usage event, got %d", len(result.Events))
	}

	event := result.Events[0]
	if event.Model != "gpt-5.3-codex" {
		t.Fatalf("expected normalized model, got %q", event.Model)
	}
	if event.CostStatus != CostStatusEstimated || event.EstimatedCostMicros == nil {
		t.Fatalf("expected estimated cost from payload.info model, got %#v", event)
	}
	if event.OccurredAtUnixMS != 1751846400000 {
		t.Fatalf("expected microsecond timestamp to convert to millis, got %d", event.OccurredAtUnixMS)
	}
}

func TestParseCodexSessionFileUsesFloatTimestamp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	writeTestFile(t, path, `{"type":"event_msg","payload":{"type":"token_count","info":{"model":"gpt-5.3-codex","total_token_usage":{"input_tokens":10,"output_tokens":3}}},"timestamp":1751846400.123}`)

	sourceKey, err := SourceKey(path)
	if err != nil {
		t.Fatalf("expected source key, got %v", err)
	}
	result, err := ParseCodexSessionFile(SourceFile{Path: path, SourceKey: sourceKey})
	if err != nil {
		t.Fatalf("expected parse to succeed, got %v", err)
	}
	if len(result.Events) != 1 {
		t.Fatalf("expected one usage event, got %d", len(result.Events))
	}
	if result.Events[0].OccurredAtUnixMS != 1751846400123 {
		t.Fatalf("expected fractional seconds timestamp to convert to millis, got %d", result.Events[0].OccurredAtUnixMS)
	}
}

func TestParseCodexSessionFileEventIDIgnoresSourcePath(t *testing.T) {
	content := strings.Join([]string{
		`{"type":"session_meta","session_id":"session-1"}`,
		`{"type":"turn_context","model":"gpt-5.3-codex"}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":10,"cached_input_tokens":2,"output_tokens":3,"total_tokens":13}}}}`,
	}, "\n")
	firstPath := filepath.Join(t.TempDir(), "first", "session.jsonl")
	secondPath := filepath.Join(t.TempDir(), "second", "session.jsonl")
	writeTestFile(t, firstPath, content)
	writeTestFile(t, secondPath, content)

	firstSourceKey, err := SourceKey(firstPath)
	if err != nil {
		t.Fatalf("expected first source key, got %v", err)
	}
	secondSourceKey, err := SourceKey(secondPath)
	if err != nil {
		t.Fatalf("expected second source key, got %v", err)
	}
	if firstSourceKey == secondSourceKey {
		t.Fatalf("expected different source keys for different paths")
	}

	first, err := ParseCodexSessionFile(SourceFile{Path: firstPath, SourceKey: firstSourceKey})
	if err != nil {
		t.Fatalf("expected first parse to succeed, got %v", err)
	}
	second, err := ParseCodexSessionFile(SourceFile{Path: secondPath, SourceKey: secondSourceKey})
	if err != nil {
		t.Fatalf("expected second parse to succeed, got %v", err)
	}
	if len(first.Events) != 1 || len(second.Events) != 1 {
		t.Fatalf("expected one event from each file, got first=%d second=%d", len(first.Events), len(second.Events))
	}
	if first.Events[0].ID != second.Events[0].ID {
		t.Fatalf("expected copied session events to keep the same ID, got %q and %q", first.Events[0].ID, second.Events[0].ID)
	}
	if first.Events[0].SourceKey == second.Events[0].SourceKey {
		t.Fatalf("expected source keys to remain path-specific cursor keys")
	}
}

func TestParseCodexSessionFileFallbackSessionIDIncludesSourceKey(t *testing.T) {
	content := `{"type":"event_msg","payload":{"type":"token_count","info":{"model":"gpt-5.3-codex","total_token_usage":{"input_tokens":10,"cached_input_tokens":2,"output_tokens":3,"total_tokens":13}}}}`
	firstPath := filepath.Join(t.TempDir(), "2026", "07", "06", "session.jsonl")
	secondPath := filepath.Join(t.TempDir(), "2026", "07", "07", "session.jsonl")
	writeTestFile(t, firstPath, content)
	writeTestFile(t, secondPath, content)

	firstSourceKey, err := SourceKey(firstPath)
	if err != nil {
		t.Fatalf("expected first source key, got %v", err)
	}
	secondSourceKey, err := SourceKey(secondPath)
	if err != nil {
		t.Fatalf("expected second source key, got %v", err)
	}

	first, err := ParseCodexSessionFile(SourceFile{Path: firstPath, SourceKey: firstSourceKey})
	if err != nil {
		t.Fatalf("expected first parse to succeed, got %v", err)
	}
	second, err := ParseCodexSessionFile(SourceFile{Path: secondPath, SourceKey: secondSourceKey})
	if err != nil {
		t.Fatalf("expected second parse to succeed, got %v", err)
	}
	if len(first.Events) != 1 || len(second.Events) != 1 {
		t.Fatalf("expected one event from each file, got first=%d second=%d", len(first.Events), len(second.Events))
	}
	if first.Events[0].SessionID != "session" || second.Events[0].SessionID != "session" {
		t.Fatalf("expected stored fallback session ids to stay readable, got first=%q second=%q", first.Events[0].SessionID, second.Events[0].SessionID)
	}
	if first.Events[0].ID == second.Events[0].ID {
		t.Fatalf("expected missing-session fallback events from different files not to collide")
	}
}

func TestParseCodexSessionFileCountsInvalidAndUnsupportedLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	writeTestFile(t, path, strings.Join([]string{
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1,"output_tokens":1}}}}`,
		`not-json`,
		`[]`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":-1,"output_tokens":1}}}}`,
	}, "\n"))

	sourceKey, err := SourceKey(path)
	if err != nil {
		t.Fatalf("expected source key, got %v", err)
	}
	result, err := ParseCodexSessionFile(SourceFile{Path: path, SourceKey: sourceKey})
	if err != nil {
		t.Fatalf("expected parse to succeed, got %v", err)
	}
	if len(result.Events) != 1 {
		t.Fatalf("expected one valid event, got %d", len(result.Events))
	}
	if result.InvalidLines != 1 || result.UnsupportedLines != 2 {
		t.Fatalf("expected invalid=1 unsupported=2, got invalid=%d unsupported=%d", result.InvalidLines, result.UnsupportedLines)
	}
}

func TestParseCodexSessionFilePrefersKnownUsagePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	writeTestFile(t, path, `{"a":{"total_token_usage":{"input_tokens":999,"output_tokens":999}},"payload":{"type":"token_count","info":{"model":"gpt-5.3-codex","total_token_usage":{"input_tokens":7,"output_tokens":3}}}}`)

	sourceKey, err := SourceKey(path)
	if err != nil {
		t.Fatalf("expected source key, got %v", err)
	}
	result, err := ParseCodexSessionFile(SourceFile{Path: path, SourceKey: sourceKey})
	if err != nil {
		t.Fatalf("expected parse to succeed, got %v", err)
	}
	if len(result.Events) != 1 {
		t.Fatalf("expected one usage event, got %d", len(result.Events))
	}
	event := result.Events[0]
	if event.InputTokens != 7 || event.OutputTokens != 3 || event.TotalTokens != 10 {
		t.Fatalf("expected parser to prefer payload.info total_token_usage, got %#v", event)
	}
}

func TestParseCodexSessionFileIgnoresNestedFakeUsageObject(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	writeTestFile(t, path, `{"type":"event_msg","payload":{"type":"tool_call","arguments":{"total_token_usage":{"input_tokens":999,"output_tokens":999}}}}`)

	sourceKey, err := SourceKey(path)
	if err != nil {
		t.Fatalf("expected source key, got %v", err)
	}
	result, err := ParseCodexSessionFile(SourceFile{Path: path, SourceKey: sourceKey})
	if err != nil {
		t.Fatalf("expected parse to succeed, got %v", err)
	}
	if len(result.Events) != 0 || result.InvalidLines != 0 || result.UnsupportedLines != 0 {
		t.Fatalf("expected nested fake usage to be ignored, got %#v", result)
	}
}

func TestParseCodexSessionFileSkipsOversizedLinesAndContinues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	writeTestFile(t, path, strings.Join([]string{
		`{"total_token_usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12}}`,
		strings.Repeat("x", 129),
		`{"total_token_usage":{"input_tokens":15,"output_tokens":3,"total_tokens":18}}`,
	}, "\n"))

	sourceKey, err := SourceKey(path)
	if err != nil {
		t.Fatalf("expected source key, got %v", err)
	}
	result, err := parseCodexSessionFile(SourceFile{Path: path, SourceKey: sourceKey}, 128)
	if err != nil {
		t.Fatalf("expected parse to skip oversized line and continue, got %v", err)
	}
	if result.InvalidLines != 1 || result.UnsupportedLines != 0 {
		t.Fatalf("expected one invalid oversized line, got %#v", result)
	}
	if len(result.Events) != 2 {
		t.Fatalf("expected valid lines before and after oversized line to import, got %d", len(result.Events))
	}
	second := result.Events[1]
	if second.InputTokens != 5 || second.OutputTokens != 1 || second.TotalTokens != 6 {
		t.Fatalf("unexpected second event after oversized line: %#v", second)
	}
}

func TestReadCodexSessionLineReturnsUnderlyingErrorOnOversizedRead(t *testing.T) {
	wantErr := errors.New("read failed")
	reader := bufio.NewReaderSize(&failingReader{
		data: []byte(strings.Repeat("x", 129)),
		err:  wantErr,
	}, 256)

	_, tooLong, err := readCodexSessionLine(reader, 128)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected underlying read error, got %v", err)
	}
	if tooLong {
		t.Fatalf("expected underlying read error not to be reported as oversized line")
	}
}

func TestListCodexSessionFilesReturnsJSONLFiles(t *testing.T) {
	root := t.TempDir()
	sessionDir := filepath.Join(root, "sessions", "2026", "07", "06")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatalf("expected session dir setup to succeed, got %v", err)
	}
	writeTestFile(t, filepath.Join(sessionDir, "a.jsonl"), "{}")
	writeTestFile(t, filepath.Join(sessionDir, "ignored.txt"), "{}")

	files, err := ListCodexSessionFiles(root)
	if err != nil {
		t.Fatalf("expected list to succeed, got %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected one jsonl file, got %d", len(files))
	}
	if files[0].SourceKey == "" || files[0].SizeBytes == 0 {
		t.Fatalf("expected source metadata, got %#v", files[0])
	}
}

type failingReader struct {
	data []byte
	err  error
	read bool
}

func (reader *failingReader) Read(p []byte) (int, error) {
	if reader.read {
		return 0, reader.err
	}
	reader.read = true
	return copy(p, reader.data), reader.err
}

func writeTestFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("expected parent dir setup to succeed, got %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("expected write to succeed, got %v", err)
	}
}
