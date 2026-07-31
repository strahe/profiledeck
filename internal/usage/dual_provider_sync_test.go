package usage

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/strahe/profiledeck/internal/bootstrap"
	grokconfig "github.com/strahe/profiledeck/internal/grokbuild/config"
	profilesruntime "github.com/strahe/profiledeck/internal/runtime"
)

func TestUsageSyncSerializesConcurrentCodexAndGrokBuild(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	grokHome := t.TempDir()

	writeAppUsageFile(t, filepath.Join(codexDir, "sessions", "2026", "07", "01", "codex.jsonl"), strings.Join([]string{
		`{"type":"session_meta","session_id":"dual-codex-session"}`,
		`{"type":"turn_context","model":"gpt-5.3-codex"}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":20,"output_tokens":10,"total_tokens":110}}}}`,
	}, "\n"))
	writeGrokBuildUsageFixture(
		t,
		grokHome,
		"workspace-dual",
		"session-dual",
		syntheticGrokBuildUsageLine(
			"session-dual",
			"prompt-dual",
			"grok-build-latest",
			1_750_000_000,
			TokenCounts{InputTokens: 100, CachedInputTokens: 20, OutputTokens: 10, TotalTokens: 110},
		),
	)

	runtimeService, err := profilesruntime.NewService(configDir)
	if err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	service := NewService(
		runtimeService.StoreFactory(),
		MustRegistry(
			NewCodexIntegration(codexDir),
			NewGrokBuildIntegration(grokHome),
		),
	)
	if _, err := bootstrap.NewService(runtimeService, nil, nil).Initialize(ctx); err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}

	start := make(chan struct{})
	errorsByRun := make(chan error, 2)
	results := make(chan UsageSyncResult, 2)
	var wait sync.WaitGroup
	for _, run := range []func(context.Context) (UsageSyncResult, error){
		service.SyncCodex,
		service.SyncGrokBuild,
	} {
		wait.Add(1)
		go func(syncFn func(context.Context) (UsageSyncResult, error)) {
			defer wait.Done()
			<-start
			result, err := syncFn(ctx)
			errorsByRun <- err
			if err == nil {
				results <- result
			}
		}(run)
	}
	close(start)
	wait.Wait()
	close(errorsByRun)
	close(results)

	for err := range errorsByRun {
		if err != nil {
			t.Fatalf("concurrent dual-provider sync: %v", err)
		}
	}
	var codexImported, grokImported int64
	for result := range results {
		switch result.ProviderID {
		case ProviderCodex:
			codexImported += result.ImportedEvents
		case grokconfig.ProviderID:
			grokImported += result.ImportedEvents
		default:
			t.Fatalf("unexpected provider in result: %#v", result)
		}
	}
	if codexImported != 1 || grokImported != 1 {
		t.Fatalf("imported codex=%d grok=%d, want 1 each", codexImported, grokImported)
	}

	codexSummary, err := service.Summary(ctx, UsageSummaryRequest{ProviderID: ProviderCodex})
	if err != nil || codexSummary.EventCount != 1 {
		t.Fatalf("codex summary=%#v err=%v", codexSummary, err)
	}
	grokSummary, err := service.Summary(ctx, UsageSummaryRequest{ProviderID: grokconfig.ProviderID})
	if err != nil || grokSummary.EventCount != 1 {
		t.Fatalf("grok summary=%#v err=%v", grokSummary, err)
	}
}
