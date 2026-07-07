package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUsageSyncCodexImportsAndSkipsUnchangedFiles(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	writeAppUsageFixture(t, codexDir, strings.Join([]string{
		`{"type":"session_meta","session_id":"session-1"}`,
		`{"type":"turn_context","model":"gpt-5.3-codex"}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":20,"output_tokens":10,"total_tokens":110}}}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":150,"cached_input_tokens":25,"output_tokens":15,"total_tokens":165}}}}`,
	}, "\n"))

	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	first, err := UsageSyncCodex(ctx, UsageSyncCodexRequest{ConfigDir: configDir, CodexDir: codexDir})
	if err != nil {
		t.Fatalf("expected first usage sync to succeed, got %v", err)
	}
	if first.ScannedFiles != 1 || first.ImportedEvents != 2 || first.SkippedDuplicateEvents != 0 || first.SkippedUnchangedFiles != 0 {
		t.Fatalf("unexpected first sync result: %#v", first)
	}

	second, err := UsageSyncCodex(ctx, UsageSyncCodexRequest{ConfigDir: configDir, CodexDir: codexDir})
	if err != nil {
		t.Fatalf("expected second usage sync to succeed, got %v", err)
	}
	if second.ScannedFiles != 1 || second.ImportedEvents != 0 || second.SkippedUnchangedFiles != 1 {
		t.Fatalf("unexpected second sync result: %#v", second)
	}

	summary, err := UsageSummary(ctx, UsageSummaryRequest{ConfigDir: configDir, ProviderID: "codex"})
	if err != nil {
		t.Fatalf("expected usage summary to succeed, got %v", err)
	}
	if summary.EventCount != 2 || summary.InputTokens != 150 || summary.CachedInputTokens != 25 || summary.OutputTokens != 15 || summary.TotalTokens != 165 {
		t.Fatalf("unexpected summary tokens: %#v", summary)
	}
	if summary.CostStatus != "estimated" || summary.EstimatedCostUSD == nil || summary.UnknownCostEventCount != 0 {
		t.Fatalf("expected estimated cost summary, got %#v", summary)
	}
	if summary.Source != "codex-session-jsonl" || strings.Join(summary.Sources, ",") != "codex-session-jsonl" {
		t.Fatalf("unexpected summary sources: %#v", summary)
	}
}

func TestUsageSyncCodexImportsOnlyAppendedEvents(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	path := writeAppUsageFixture(t, codexDir, strings.Join([]string{
		`{"type":"session_meta","session_id":"session-1"}`,
		`{"type":"turn_context","model":"gpt-5.3-codex"}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":20,"output_tokens":10,"total_tokens":110}}}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":150,"cached_input_tokens":25,"output_tokens":15,"total_tokens":165}}}}`,
	}, "\n"))

	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	first, err := UsageSyncCodex(ctx, UsageSyncCodexRequest{ConfigDir: configDir, CodexDir: codexDir})
	if err != nil {
		t.Fatalf("expected first usage sync to succeed, got %v", err)
	}
	if first.ImportedEvents != 2 || first.SkippedDuplicateEvents != 0 {
		t.Fatalf("unexpected first sync result: %#v", first)
	}

	appendAppUsageFixture(t, path, `{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":180,"cached_input_tokens":28,"output_tokens":18,"total_tokens":198}}}}`)
	second, err := UsageSyncCodex(ctx, UsageSyncCodexRequest{ConfigDir: configDir, CodexDir: codexDir})
	if err != nil {
		t.Fatalf("expected second usage sync to succeed, got %v", err)
	}
	if second.ScannedFiles != 1 || second.ImportedEvents != 1 || second.SkippedDuplicateEvents != 0 || second.SkippedUnchangedFiles != 0 {
		t.Fatalf("expected appended sync to store only one new event, got %#v", second)
	}

	summary, err := UsageSummary(ctx, UsageSummaryRequest{ConfigDir: configDir, ProviderID: "codex"})
	if err != nil {
		t.Fatalf("expected usage summary to succeed, got %v", err)
	}
	if summary.EventCount != 3 || summary.InputTokens != 180 || summary.CachedInputTokens != 28 || summary.OutputTokens != 18 || summary.TotalTokens != 198 {
		t.Fatalf("unexpected summary after append sync: %#v", summary)
	}
}

func TestUsageSyncCodexDoesNotDoubleCountCopiedSessionFiles(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	firstCodexDir := t.TempDir()
	secondCodexDir := t.TempDir()
	content := strings.Join([]string{
		`{"type":"session_meta","session_id":"session-1"}`,
		`{"type":"turn_context","model":"gpt-5.3-codex"}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":20,"output_tokens":10,"total_tokens":110}}}}`,
	}, "\n")
	writeAppUsageFixture(t, firstCodexDir, content)
	writeAppUsageFixture(t, secondCodexDir, content)

	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	first, err := UsageSyncCodex(ctx, UsageSyncCodexRequest{ConfigDir: configDir, CodexDir: firstCodexDir})
	if err != nil {
		t.Fatalf("expected first usage sync to succeed, got %v", err)
	}
	if first.ImportedEvents != 1 || first.SkippedDuplicateEvents != 0 {
		t.Fatalf("unexpected first sync result: %#v", first)
	}

	second, err := UsageSyncCodex(ctx, UsageSyncCodexRequest{ConfigDir: configDir, CodexDir: secondCodexDir})
	if err != nil {
		t.Fatalf("expected copied usage sync to succeed, got %v", err)
	}
	if second.ImportedEvents != 0 || second.SkippedDuplicateEvents != 1 {
		t.Fatalf("expected copied session to dedupe existing event, got %#v", second)
	}

	summary, err := UsageSummary(ctx, UsageSummaryRequest{ConfigDir: configDir, ProviderID: "codex"})
	if err != nil {
		t.Fatalf("expected usage summary to succeed, got %v", err)
	}
	if summary.EventCount != 1 || summary.InputTokens != 100 || summary.CachedInputTokens != 20 || summary.OutputTokens != 10 || summary.TotalTokens != 110 {
		t.Fatalf("expected copied session not to double count usage, got %#v", summary)
	}
}

func TestUsageSummaryReportsUnknownCostForUnknownModel(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	writeAppUsageFixture(t, codexDir, strings.Join([]string{
		`{"type":"session_meta","session_id":"session-1"}`,
		`{"type":"turn_context","model":"unknown-model"}`,
		`{"type":"event_msg","payload":{"type":"token_count","last_token_usage":{"input_tokens":5,"output_tokens":2}}}`,
	}, "\n"))

	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	if _, err := UsageSyncCodex(ctx, UsageSyncCodexRequest{ConfigDir: configDir, CodexDir: codexDir}); err != nil {
		t.Fatalf("expected usage sync to succeed, got %v", err)
	}

	summary, err := UsageSummary(ctx, UsageSummaryRequest{ConfigDir: configDir, ProviderID: "codex"})
	if err != nil {
		t.Fatalf("expected usage summary to succeed, got %v", err)
	}
	if summary.EventCount != 1 || summary.InputTokens != 5 || summary.OutputTokens != 2 {
		t.Fatalf("unexpected summary tokens: %#v", summary)
	}
	if summary.CostStatus != "unknown" || summary.EstimatedCostUSD != nil || summary.UnknownCostEventCount != 1 {
		t.Fatalf("expected unknown cost summary, got %#v", summary)
	}
}

func TestSummarySourceReflectsAggregatedSources(t *testing.T) {
	if got := summarySource(nil); got != "" {
		t.Fatalf("expected empty source for no sources, got %q", got)
	}
	if got := summarySource([]string{"codex-session-jsonl"}); got != "codex-session-jsonl" {
		t.Fatalf("expected single source, got %q", got)
	}
	if got := summarySource([]string{"codex-archive-jsonl", "codex-session-jsonl"}); got != "multiple" {
		t.Fatalf("expected multiple source marker, got %q", got)
	}
}

func writeAppUsageFixture(t *testing.T, codexDir string, content string) string {
	t.Helper()
	path := filepath.Join(codexDir, "sessions", "2026", "07", "06", "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("expected fixture dir setup to succeed, got %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("expected fixture write to succeed, got %v", err)
	}
	return path
}

func appendAppUsageFixture(t *testing.T, path string, line string) {
	t.Helper()
	handle, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("expected fixture append open to succeed, got %v", err)
	}
	defer handle.Close()
	if _, err := handle.WriteString("\n" + line); err != nil {
		t.Fatalf("expected fixture append write to succeed, got %v", err)
	}
}
