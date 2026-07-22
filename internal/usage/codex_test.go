package usage

import (
	"bufio"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseCodexSessionFileHonorsCanceledContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	writeTestFile(t, path, `{"type":"event_msg","payload":{"type":"token_count"}}`)
	sourceKey, err := SourceKey(path)
	if err != nil {
		t.Fatalf("expected source key, got %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = ParseCodexSessionFileContext(ctx, SourceFile{Path: path, SourceKey: sourceKey})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled parse, got %v", err)
	}
}

func TestListCodexSessionFilesHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := ListCodexSessionFilesContext(ctx, t.TempDir())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled discovery, got %v", err)
	}
}

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
	second := result.Events[1]
	if second.InputTokens != 50 || second.CachedInputTokens != 5 || second.OutputTokens != 5 || second.TotalTokens != 55 {
		t.Fatalf("unexpected second delta counts: %#v", second)
	}
}

func TestParseCodexSessionFileStoresGPT56BaseCostAsPartial(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	writeTestFile(t, path, strings.Join([]string{
		`{"type":"session_meta","session_id":"session-1"}`,
		`{"type":"turn_context","model":"gpt-5.6-sol"}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1000000,"cached_input_tokens":100000,"output_tokens":1000000,"total_tokens":2000000}}}}`,
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
	if event.CostStatus != CostStatusPartial || event.EstimatedCostMicros == nil || *event.EstimatedCostMicros != 34_550_000 {
		t.Fatalf("expected GPT-5.6 base cost to remain explicitly partial, got %#v", event)
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

func TestParseCodexSessionFileMapsMissingModelToUnknown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	writeTestFile(t, path, `{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":5,"output_tokens":2}}}}`)

	sourceKey, err := SourceKey(path)
	if err != nil {
		t.Fatalf("expected source key, got %v", err)
	}
	result, err := ParseCodexSessionFile(SourceFile{Path: path, SourceKey: sourceKey})
	if err != nil || len(result.Events) != 1 {
		t.Fatalf("expected one usage event, result=%#v err=%v", result, err)
	}
	if event := result.Events[0]; event.Model != "unknown" || event.CostStatus != CostStatusUnknown {
		t.Fatalf("expected missing model to use unknown storage semantics, got %#v", event)
	}
}

func TestParseCodexSessionFileUsesTokenCountInfoModel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	writeTestFile(t, path, `{"type":"event_msg","payload":{"type":"token_count","info":{"model_name":"OpenAI/GPT-5.3-Codex-2026-07-06","total_token_usage":{"input_tokens":10,"cached_input_tokens":2,"output_tokens":3}}},"timestamp":1751846400000000}`)

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
	if event.Model != "OpenAI/GPT-5.3-Codex-2026-07-06" {
		t.Fatalf("expected exact safe model name for analysis, got %q", event.Model)
	}
	if event.CostStatus != CostStatusUnknown || event.EstimatedCostMicros != nil {
		t.Fatalf("expected unknown cost for an unlisted model alias, got %#v", event)
	}
	wantID := EventID(
		ProviderCodex,
		SourceCodexSessionJSONL,
		1,
		storedSessionID(eventIdentitySessionID("session", sourceKey, false)),
		event.Model,
		TokenCounts{InputTokens: 10, CachedInputTokens: 2, OutputTokens: 3, TotalTokens: 13},
	)
	if event.EventKey != wantID {
		t.Fatalf("expected exact model storage not to change stable event identity, got %q want %q", event.EventKey, wantID)
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
	if first.Events[0].EventKey != second.Events[0].EventKey {
		t.Fatalf("expected copied session events to keep the same key, got %q and %q", first.Events[0].EventKey, second.Events[0].EventKey)
	}
}

func TestParseCodexSessionFileStableIDMatchesForkedHistory(t *testing.T) {
	root := t.TempDir()
	parentPath := filepath.Join(root, "parent", "session.jsonl")
	forkPath := filepath.Join(root, "fork", "session.jsonl")
	writeTestFile(t, parentPath, strings.Join([]string{
		`{"type":"session_meta","payload":{"id":"session-parent"}}`,
		`{"type":"turn_context","model":"openai/gpt-5.3-codex-2026-07-01"}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":20,"output_tokens":10,"total_tokens":110}}},"timestamp":"2026-07-01T00:00:00Z"}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":150,"cached_input_tokens":25,"output_tokens":15,"total_tokens":165}}},"timestamp":"2026-07-01T00:01:00Z"}`,
	}, "\n"))
	writeTestFile(t, forkPath, strings.Join([]string{
		`{"type":"session_meta","payload":{"id":"session-child","forked_from_id":"session-parent"}}`,
		`{"type":"session_meta","payload":{"id":"session-parent"}}`,
		`{"type":"turn_context","model":"gpt-5.3-codex"}`,
		`{"type":"response_item","payload":{"type":"message"}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":20,"output_tokens":10,"total_tokens":110}}},"timestamp":"2026-07-02T00:00:00Z"}`,
		`{"type":"session_meta","payload":{"id":"session-parent"}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":150,"cached_input_tokens":25,"output_tokens":15,"total_tokens":165}}},"timestamp":"2026-07-02T00:00:00Z"}`,
		`{"type":"session_meta","payload":{"id":"session-child","forked_from_id":"session-parent"}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":30,"cached_input_tokens":5,"output_tokens":3,"total_tokens":33}}},"timestamp":"2026-07-02T00:02:00Z"}`,
	}, "\n"))

	parent := parseUsageFixture(t, parentPath)
	fork := parseUsageFixture(t, forkPath)
	if len(parent.Events) != 2 || len(fork.Events) != 3 {
		t.Fatalf("unexpected parent/fork event counts: parent=%d fork=%d", len(parent.Events), len(fork.Events))
	}
	for index := range parent.Events {
		if parent.Events[index].EventKey.IsZero() || parent.Events[index].EventKey != fork.Events[index].EventKey {
			t.Fatalf("expected fork history event %d to retain its stable ID, parent=%#v fork=%#v", index, parent.Events[index], fork.Events[index])
		}
	}
	if fork.Events[2].SessionID != "session-child" || fork.Events[2].EventKey == parent.Events[0].EventKey {
		t.Fatalf("expected post-fork child usage to remain distinct, got %#v", fork.Events[2])
	}
}

func TestParseCodexSessionFileStableIDSeparatesIndependentSessions(t *testing.T) {
	root := t.TempDir()
	content := func(sessionID string) string {
		return strings.Join([]string{
			`{"type":"session_meta","payload":{"id":"` + sessionID + `"}}`,
			`{"type":"turn_context","model":"gpt-5.3-codex"}`,
			`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":10,"cached_input_tokens":2,"output_tokens":3,"total_tokens":13}}},"timestamp":"2026-07-01T00:00:00Z"}`,
		}, "\n")
	}
	firstPath := filepath.Join(root, "first", "session.jsonl")
	secondPath := filepath.Join(root, "second", "session.jsonl")
	writeTestFile(t, firstPath, content("session-first"))
	writeTestFile(t, secondPath, content("session-second"))

	first := parseUsageFixture(t, firstPath)
	second := parseUsageFixture(t, secondPath)
	if len(first.Events) != 1 || len(second.Events) != 1 {
		t.Fatalf("expected one event per independent session")
	}
	if first.Events[0].EventKey == second.Events[0].EventKey {
		t.Fatalf("expected independent sessions with identical timestamps and tokens to stay distinct")
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
	if !strings.HasPrefix(first.Events[0].SessionID, "derived-") || !strings.HasPrefix(second.Events[0].SessionID, "derived-") || first.Events[0].SessionID == second.Events[0].SessionID {
		t.Fatalf("expected distinct derived fallback session ids, got first=%q second=%q", first.Events[0].SessionID, second.Events[0].SessionID)
	}
	if first.Events[0].EventKey == second.Events[0].EventKey {
		t.Fatalf("expected missing-session fallback events from different files not to collide")
	}
}

func parseUsageFixture(t *testing.T, path string) FileParseResult {
	t.Helper()
	sourceKey, err := SourceKey(path)
	if err != nil {
		t.Fatalf("expected fixture source key, got %v", err)
	}
	result, err := ParseCodexSessionFile(SourceFile{Path: path, SourceKey: sourceKey})
	if err != nil {
		t.Fatalf("expected fixture parse, got %v", err)
	}
	return result
}

func TestParseCodexSessionFileCountsInvalidAndUnsupportedLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	writeTestFile(t, path, strings.Join([]string{
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1,"output_tokens":1}}}}`,
		`not-json`,
		`[]`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":-1,"output_tokens":1}}}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1,"cached_input_tokens":2,"output_tokens":1}}}}`,
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
	if result.InvalidLines != 1 || result.UnsupportedLines != 3 {
		t.Fatalf("expected invalid=1 unsupported=3, got invalid=%d unsupported=%d", result.InvalidLines, result.UnsupportedLines)
	}
}

func TestParseCodexSessionFileSanitizesPersistedLabelsWithoutChangingUsage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	writeTestFile(t, path, strings.Join([]string{
		`{"type":"session_meta","session_id":"SECRET SESSION VALUE"}`,
		`{"type":"turn_context","model":"SECRET MODEL VALUE"}`,
		`{"type":"SECRET EVENT VALUE","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":10,"output_tokens":2}}}}`,
	}, "\n"))
	sourceKey, err := SourceKey(path)
	if err != nil {
		t.Fatalf("expected source key, got %v", err)
	}
	result, err := ParseCodexSessionFile(SourceFile{Path: path, SourceKey: sourceKey})
	if err != nil || len(result.Events) != 1 {
		t.Fatalf("expected one sanitized usage event, result=%#v err=%v", result, err)
	}
	event := result.Events[0]
	if !strings.HasPrefix(event.SessionID, "derived-") || event.Model != "unknown" || event.InputTokens != 10 || event.OutputTokens != 2 {
		t.Fatalf("unexpected sanitized usage event: %#v", event)
	}
	for _, value := range []string{event.SessionID, event.Model} {
		if strings.Contains(strings.ToLower(value), "secret") {
			t.Fatalf("expected persisted labels to exclude raw unsafe content, got %q", value)
		}
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
	result, err := parseCodexSessionFile(context.Background(), SourceFile{Path: path, SourceKey: sourceKey}, 128)
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

	_, tooLong, err := readCodexSessionLine(context.Background(), reader, 128)
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
	outside := filepath.Join(t.TempDir(), "outside.jsonl")
	writeTestFile(t, outside, "{}")
	if err := os.Symlink(outside, filepath.Join(sessionDir, "ignored-link.jsonl")); err != nil {
		t.Logf("symlink fixture unavailable: %v", err)
	}

	files, err := ListCodexSessionFiles(root)
	if err != nil {
		t.Fatalf("expected list to succeed, got %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected one jsonl file, got %d", len(files))
	}
	if files[0].SourceKey.IsZero() || files[0].SizeBytes == 0 {
		t.Fatalf("expected source metadata, got %#v", files[0])
	}
}

func TestListCodexSessionFilesIncludesOnlyFlatArchivedJSONL(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "sessions", "2026", "07", "06", "active.jsonl"), "{}")
	writeTestFile(t, filepath.Join(root, "archived_sessions", "archived.jsonl"), "{}")
	writeTestFile(t, filepath.Join(root, "archived_sessions", "ignored.txt"), "{}")
	writeTestFile(t, filepath.Join(root, "archived_sessions", "nested", "ignored.jsonl"), "{}")

	files, err := ListCodexSessionFiles(root)
	if err != nil {
		t.Fatalf("expected active and archived file discovery, got %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected one active and one flat archived JSONL file, got %#v", files)
	}
	if filepath.Base(files[0].Path) != "archived.jsonl" || filepath.Base(files[1].Path) != "active.jsonl" {
		t.Fatalf("expected stable path ordering across roots, got %#v", files)
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

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("expected parent dir setup to succeed, got %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("expected write to succeed, got %v", err)
	}
}
