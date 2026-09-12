package usage

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestParseGrokBuildSyntheticFixture(t *testing.T) {
	result, err := ParseGrokBuildSessionFile(grokBuildTestSourceFile(
		t,
		filepath.Join("testdata", "grok-build-v0.2.114", "valid.jsonl"),
	))
	if err != nil {
		t.Fatalf("parse synthetic Grok Build fixture: %v", err)
	}
	if result.InvalidLines != 0 || result.UnsupportedLines != 3 {
		t.Fatalf("fixture counters = %#v", result)
	}
	if len(result.Events) != 3 {
		t.Fatalf("fixture events = %d, want 3", len(result.Events))
	}

	first := result.Events[0]
	if first.Model != "grok-build-latest" ||
		first.OccurredAtUnixMS != 1_750_000_000_000 ||
		first.InputTokens != 100 ||
		first.CachedInputTokens != 40 ||
		first.OutputTokens != 20 ||
		first.TotalTokens != 120 {
		t.Fatalf("first event = %#v", first)
	}
	if first.SessionID == "synthetic-session-a" ||
		!strings.HasPrefix(first.SessionID, "derived-") ||
		strings.Contains(first.SessionID, "synthetic-session") {
		t.Fatalf("session identifier was not irreversibly derived: %q", first.SessionID)
	}
	if first.EstimatedCostMicros == nil || *first.EstimatedCostMicros != 252 ||
		first.CostStatus != CostStatusEstimated {
		t.Fatalf("first event cost = %#v", first)
	}

	known, unknown := result.Events[1], result.Events[2]
	if known.Model != "grok-4.5" || known.OccurredAtUnixMS != 0 ||
		known.EstimatedCostMicros == nil || *known.EstimatedCostMicros != 504 {
		t.Fatalf("known undated event = %#v", known)
	}
	if unknown.Model != "synthetic-unknown-model" ||
		unknown.OccurredAtUnixMS != 0 ||
		unknown.EstimatedCostMicros != nil ||
		unknown.CostStatus != CostStatusUnknown {
		t.Fatalf("unknown-model event = %#v", unknown)
	}
	if known.SessionID != unknown.SessionID {
		t.Fatalf("one terminal produced different session keys: %q != %q", known.SessionID, unknown.SessionID)
	}
}

func TestParseGrokBuildQuarantinesMalformedFixtures(t *testing.T) {
	_, err := ParseGrokBuildSessionFile(grokBuildTestSourceFile(
		t,
		filepath.Join("testdata", "grok-build-v0.2.114", "malformed.jsonl"),
	))
	if err == nil {
		t.Fatal("malformed fixture unexpectedly parsed")
	}
}

func TestParseGrokBuildAcceptsAdditiveFields(t *testing.T) {
	result, err := ParseGrokBuildSessionFile(grokBuildTestSourceFile(
		t,
		filepath.Join("testdata", "grok-build-v0.2.114", "format-drift.jsonl"),
	))
	if err != nil || len(result.Events) != 1 {
		t.Fatalf("additive field fixture = %#v, err = %v", result, err)
	}
}

func TestParseGrokBuildCurrentSessionFixture(t *testing.T) {
	result, err := ParseGrokBuildSessionFile(grokBuildTestSourceFile(
		t,
		filepath.Join("testdata", "grok-build-v1.0.25", "valid.jsonl"),
	))
	if err != nil || result.InvalidLines != 0 || result.UnsupportedLines != 0 || len(result.Events) != 1 {
		t.Fatalf("current Grok Build fixture = %#v, err = %v", result, err)
	}
	event := result.Events[0]
	if event.Model != "grok-4.6-build" || event.InputTokens != 1_000 ||
		event.CachedInputTokens != 200 || event.OutputTokens != 100 ||
		event.TotalTokens != 1_100 || event.EstimatedCostMicros == nil ||
		*event.EstimatedCostMicros != 2_300 || event.CostStatus != CostStatusPartial {
		t.Fatalf("current Grok Build event = %#v", event)
	}
}

func TestParseGrokBuildTerminalValidationFailsClosed(t *testing.T) {
	base := `{"timestamp":1,"method":"_x.ai/session/update","params":{"sessionId":"session","update":{"sessionUpdate":"turn_completed","prompt_id":"prompt","stop_reason":"end_turn","agent_result":"discard me","usage":{"inputTokens":10,"outputTokens":2,"totalTokens":12,"cachedReadTokens":1,"reasoningTokens":1,"modelCalls":1,"apiDurationMs":5,"costUsdTicks":7,"costIsPartial":false,"modelUsage":{"grok-build-latest":{"inputTokens":10,"outputTokens":2,"totalTokens":12,"cachedReadTokens":1,"reasoningTokens":1,"modelCalls":1,"apiDurationMs":5,"costUsdTicks":7,"costIsPartial":false}},"numTurns":1}},"_meta":{}}}`
	tests := map[string]string{
		"unexpected terminal location": strings.Replace(base, `"_x.ai/session/update"`, `"session/update"`, 1),
		"inconsistent total":           strings.Replace(base, `"totalTokens":12`, `"totalTokens":13`, 1),
		"invalid agent result":         strings.Replace(base, `"agent_result":"discard me"`, `"agent_result":{"secret":"discard me"}`, 1),
		"invalid cost shape":           strings.Replace(base, `"costUsdTicks":7`, `"costUsdTicks":"7"`, 1),
		"invalid cache creation shape": strings.Replace(base, `"cachedReadTokens":1`, `"cachedReadTokens":1,"cacheCreationTokens":"1"`, 1),
		"invalid metadata":             strings.Replace(base, `"_meta":{}`, `"_meta":[]`, 1),
		"missing timestamp":            strings.Replace(base, `"timestamp":1,`, "", 1),
		"null timestamp":               strings.Replace(base, `"timestamp":1`, `"timestamp":null`, 1),
		"duplicate normalized model": strings.Replace(
			base,
			`"grok-build-latest":{"inputTokens":10,"outputTokens":2,"totalTokens":12,"cachedReadTokens":1,"reasoningTokens":1,"modelCalls":1,"apiDurationMs":5,"costUsdTicks":7,"costIsPartial":false}`,
			`"grok-build-latest":{"inputTokens":5,"outputTokens":1,"totalTokens":6,"cachedReadTokens":0,"reasoningTokens":0,"modelCalls":1,"apiDurationMs":2,"costIsPartial":false},"GROK-BUILD-LATEST":{"inputTokens":5,"outputTokens":1,"totalTokens":6,"cachedReadTokens":1,"reasoningTokens":1,"modelCalls":0,"apiDurationMs":3,"costIsPartial":false}`,
			1,
		),
		fmt.Sprintf("timestamp overflow %d", uint64(math.MaxInt64/1000)+1): strings.Replace(
			base,
			`"timestamp":1`,
			fmt.Sprintf(`"timestamp":%d`, uint64(math.MaxInt64/1000)+1),
			1,
		),
		"cache creation mismatch": func() string {
			line := strings.Replace(
				base,
				`"cachedReadTokens":1`,
				`"cachedReadTokens":1,"cacheCreationTokens":2`,
				1,
			)
			return strings.Replace(
				line,
				`"cachedReadTokens":1,"reasoningTokens"`,
				`"cachedReadTokens":1,"cacheCreationTokens":1,"reasoningTokens"`,
				1,
			)
		}(),
		"cache creation overflow": strings.Replace(
			base,
			`"cachedReadTokens":1`,
			fmt.Sprintf(`"cachedReadTokens":1,"cacheCreationTokens":%d`, uint64(math.MaxInt64)+1),
			1,
		),
	}
	for name, line := range tests {
		t.Run(name, func(t *testing.T) {
			if _, _, err := parseGrokBuildSessionLine([]byte(line)); err == nil {
				t.Fatal("invalid terminal unexpectedly parsed")
			}
		})
	}
}

func TestGrokBuildEventAndSessionIdentityContracts(t *testing.T) {
	base := GrokBuildEventID(" prompt ", "GROK-4.5-LATEST")
	if base.IsZero() || base != GrokBuildEventID("prompt", "grok-4.5-latest") {
		t.Fatalf("event identity is not normalized: %q", base)
	}
	if base == GrokBuildEventID("other-prompt", "grok-4.5-latest") ||
		base == GrokBuildEventID("prompt", "grok-build-latest") {
		t.Fatal("event identity did not scope prompt and model")
	}
	parent := grokBuildSessionKey("session-parent")
	child := grokBuildSessionKey("session-child")
	if parent == child || parent == "" || child == "" ||
		strings.Contains(parent, "session-parent") ||
		len(parent) != len("derived-")+64 {
		t.Fatalf("derived session keys are invalid: parent=%q child=%q", parent, child)
	}
	event := Event{
		EventKey: base, SessionID: parent, OccurredAtUnixMS: 1,
		InputTokens: 10, CachedInputTokens: 2, OutputTokens: 3, TotalTokens: 13,
	}
	alias := event
	alias.SessionID = child
	alias.OccurredAtUnixMS = 2
	if GrokBuildEventDigest([]Event{event}, 1) != GrokBuildEventDigest([]Event{alias}, 1) {
		t.Fatal("prefix digest included fork-local session or timestamp")
	}
	alias.TotalTokens++
	if GrokBuildEventDigest([]Event{event}, 1) == GrokBuildEventDigest([]Event{alias}, 1) {
		t.Fatal("prefix digest did not cover token values")
	}
}

func TestEstimateGrokBuildCostUsesShortContextStandardPrices(t *testing.T) {
	tokens := TokenCounts{
		InputTokens:       1_000_000,
		CachedInputTokens: 100_000,
		OutputTokens:      1_000_000,
		TotalTokens:       2_000_000,
	}
	for _, model := range []string{
		"grok-4.5",
		"grok-4.5-build",
		"grok-4.5-latest",
		"grok-build-latest",
	} {
		cost, status := EstimateGrokBuildCostMicros(model, tokens)
		if status != CostStatusEstimated || cost == nil || *cost != 7_830_000 {
			t.Fatalf("%s cost = %v, status = %v", model, cost, status)
		}
	}
	if cost, status := EstimateGrokBuildCostMicros("grok-unverified", tokens); cost != nil ||
		status != CostStatusUnknown {
		t.Fatalf("unverified model cost = %v, status = %v", cost, status)
	}
	for _, model := range []string{"grok-4.6", "grok-4.6-build"} {
		cost, status := EstimateGrokBuildCostMicros(model, tokens)
		if status != CostStatusEstimated || cost == nil || *cost != 7_850_000 {
			t.Fatalf("%s cost = %v, status = %v", model, cost, status)
		}
	}
	if cost, status := EstimateGrokBuildCostMicros("grok-4.5", TokenCounts{
		InputTokens:  math.MaxInt64,
		OutputTokens: math.MaxInt64,
	}); cost != nil || status != CostStatusUnknown {
		t.Fatalf("overflow cost = %v, status = %v", cost, status)
	}
	if GrokBuildPricingBasis != "xai-standard-api-short-context" ||
		GrokBuildPricingSource != "https://docs.x.ai/developers/pricing" ||
		GrokBuildPricingVerified == "" {
		t.Fatalf("pricing metadata is incomplete")
	}
}

func TestListGrokBuildSessionFilesUsesExactRegularFileLayout(t *testing.T) {
	home := t.TempDir()
	first := filepath.Join(home, "sessions", "a-workspace", "session-a", "updates.jsonl")
	second := filepath.Join(home, "sessions", "z-workspace", "session-z", "updates.jsonl")
	writeTestFile(t, first, "{}")
	writeTestFile(t, second, "{}")
	writeTestFile(t, filepath.Join(home, "sessions", "a-workspace", "subagents", "updates.jsonl"), "{}")
	writeTestFile(t, filepath.Join(home, "sessions", "a-workspace", "session-a", "subagents", "child", "updates.jsonl"), "{}")
	writeTestFile(t, filepath.Join(home, "sessions", "updates.jsonl"), "{}")

	if runtime.GOOS != "windows" {
		linkDir := filepath.Join(home, "sessions", "a-workspace", "linked-session")
		if err := os.MkdirAll(linkDir, 0o700); err != nil {
			t.Fatalf("create symlink fixture directory: %v", err)
		}
		if err := os.Symlink(first, filepath.Join(linkDir, "updates.jsonl")); err != nil {
			t.Fatalf("create file symlink fixture: %v", err)
		}
		if err := os.Symlink(
			filepath.Join(home, "sessions", "a-workspace"),
			filepath.Join(home, "sessions", "linked-workspace"),
		); err != nil {
			t.Fatalf("create directory symlink fixture: %v", err)
		}
	}

	files, err := ListGrokBuildSessionFiles(home)
	if err != nil {
		t.Fatalf("list Grok Build sessions: %v", err)
	}
	if len(files) != 2 || files[0].Path != first || files[1].Path != second {
		t.Fatalf("session files = %#v", files)
	}
}

func TestParseGrokBuildRejectsChangedAndOversizedFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "updates.jsonl")
	writeTestFile(t, path, `{"timestamp":0,"method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"user_message_chunk"}}}`)
	source := grokBuildTestSourceFile(t, path)
	stale := source
	stale.SizeBytes++
	if _, err := ParseGrokBuildSessionFile(stale); err == nil {
		t.Fatal("stale file metadata unexpectedly accepted")
	}

	writeTestFile(t, path, strings.Repeat("x", 65))
	source = grokBuildTestSourceFile(t, path)
	if _, err := parseGrokBuildSessionFile(context.Background(), source, 64); err == nil {
		t.Fatal("oversized record unexpectedly accepted")
	}
}

func TestParseGrokBuildRejectsAppendAndReplacementDuringRead(t *testing.T) {
	const original = `{"timestamp":0,"method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"user_message_chunk"}}}`
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, string)
	}{
		{
			name: "append",
			mutate: func(t *testing.T, path string) {
				t.Helper()
				handle, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
				if err != nil {
					t.Fatalf("open append fixture: %v", err)
				}
				if _, err := handle.WriteString("\n" + original); err != nil {
					_ = handle.Close()
					t.Fatalf("append fixture: %v", err)
				}
				if err := handle.Close(); err != nil {
					t.Fatalf("close append fixture: %v", err)
				}
			},
		},
		{
			name: "replacement",
			mutate: func(t *testing.T, path string) {
				t.Helper()
				replacement := path + ".replacement"
				writeTestFile(t, replacement, original)
				if err := os.Rename(replacement, path); err != nil {
					t.Fatalf("replace fixture: %v", err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "updates.jsonl")
			writeTestFile(t, path, original)
			source := grokBuildTestSourceFile(t, path)
			if _, err := parseGrokBuildSessionFileWithOpenHook(
				context.Background(),
				source,
				maxGrokBuildSessionLineBytes,
				func() { test.mutate(t, path) },
			); err == nil {
				t.Fatal("file mutation during read unexpectedly accepted")
			}
		})
	}
}

func TestParseGrokBuildHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ParseGrokBuildSessionFileContext(
		ctx,
		grokBuildTestSourceFile(
			t,
			filepath.Join("testdata", "grok-build-v0.2.114", "valid.jsonl"),
		),
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("parse error = %v, want context cancellation", err)
	}
}

func grokBuildTestSourceFile(t *testing.T, path string) SourceFile {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("stat fixture %q: %v", path, err)
	}
	key, err := SourceKey(path)
	if err != nil {
		t.Fatalf("derive source key %q: %v", path, err)
	}
	return SourceFile{
		Path:           path,
		SourceKey:      key,
		ModifiedUnixMS: info.ModTime().UnixMilli(),
		SizeBytes:      info.Size(),
	}
}
