package usage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/bootstrap"
	grokconfig "github.com/strahe/profiledeck/internal/grokbuild/config"
	grokpreset "github.com/strahe/profiledeck/internal/grokbuild/preset"
	profilesruntime "github.com/strahe/profiledeck/internal/runtime"
	"github.com/strahe/profiledeck/internal/store"
)

type grokBuildUsageTestEnvironment struct {
	runtime *profilesruntime.Service
	service *Service
}

func TestUsageSyncGrokBuildImportsIncrementallyAcrossRestart(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	grokHome := t.TempDir()
	path := writeGrokBuildUsageFixture(
		t,
		grokHome,
		"workspace-a",
		"session-a",
		syntheticGrokBuildUsageLine(
			"session-a",
			"prompt-a",
			"grok-build-latest",
			1_750_000_000,
			TokenCounts{InputTokens: 100, CachedInputTokens: 40, OutputTokens: 20, TotalTokens: 120},
		),
	)
	environment := newGrokBuildUsageTestEnvironment(t, configDir, grokHome)
	if _, err := bootstrap.NewService(environment.runtime, nil, nil).Initialize(ctx); err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}

	first, err := environment.service.SyncGrokBuild(ctx)
	if err != nil {
		t.Fatalf("first Grok Build sync: %v", err)
	}
	if first.ProviderID != grokconfig.ProviderID ||
		first.Source != SourceGrokBuildSessionJSONL ||
		first.ScannedFiles != 1 ||
		first.ImportedEvents != 1 {
		t.Fatalf("first sync = %#v", first)
	}
	second, err := environment.service.SyncGrokBuild(ctx)
	if err != nil || second.SkippedUnchangedFiles != 1 || second.ImportedEvents != 0 {
		t.Fatalf("unchanged sync = %#v, err = %v", second, err)
	}

	appendAppUsageFixture(t, path, syntheticGrokBuildUsageLine(
		"session-a",
		"prompt-b",
		"grok-4.5",
		1_750_000_001,
		TokenCounts{InputTokens: 50, CachedInputTokens: 10, OutputTokens: 5, TotalTokens: 55},
	))
	incremental, err := environment.service.SyncGrokBuild(ctx)
	if err != nil || incremental.ImportedEvents != 1 || incremental.SkippedDuplicateEvents != 0 {
		t.Fatalf("incremental sync = %#v, err = %v", incremental, err)
	}

	restarted := newGrokBuildUsageTestEnvironment(t, configDir, grokHome)
	afterRestart, err := restarted.service.SyncGrokBuild(ctx)
	if err != nil || afterRestart.SkippedUnchangedFiles != 1 || afterRestart.ImportedEvents != 0 {
		t.Fatalf("post-restart sync = %#v, err = %v", afterRestart, err)
	}
	summary, err := restarted.service.Summary(ctx, UsageSummaryRequest{ProviderID: grokconfig.ProviderID})
	if err != nil {
		t.Fatalf("Grok Build summary: %v", err)
	}
	if summary.EventCount != 2 ||
		summary.InputTokens != 150 ||
		summary.CachedInputTokens != 50 ||
		summary.OutputTokens != 25 ||
		summary.TotalTokens != 175 ||
		summary.CostStatus != CostStatusEstimated.String() ||
		summary.EstimatedCostUSD == nil {
		t.Fatalf("summary = %#v", summary)
	}

	db, err := restarted.runtime.StoreFactory().OpenHealthy(ctx, true)
	if err != nil {
		t.Fatalf("open Store: %v", err)
	}
	defer db.Close()
	provider, err := db.GetProvider(ctx, grokconfig.ProviderID)
	if err != nil {
		t.Fatalf("read provisioned Provider: %v", err)
	}
	metadata, err := grokpreset.DecodeProviderMetadata(provider.MetadataJSON)
	if err != nil || metadata.GrokHome != grokHome {
		t.Fatalf("Provider metadata = %#v, err = %v", metadata, err)
	}
}

func TestUsageSyncGrokBuildImportsCurrentSessionFormat(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	grokHome := t.TempDir()
	fixture, err := os.ReadFile(filepath.Join("testdata", "grok-build-v1.0.25", "valid.jsonl"))
	if err != nil {
		t.Fatalf("read current Grok Build fixture: %v", err)
	}
	writeGrokBuildUsageFixture(t, grokHome, "workspace-current", "session-current", string(fixture))
	environment := newGrokBuildUsageTestEnvironment(t, configDir, grokHome)
	if _, err := bootstrap.NewService(environment.runtime, nil, nil).Initialize(ctx); err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}

	result, err := environment.service.SyncGrokBuild(ctx)
	if err != nil || result.ImportedEvents != 1 || result.InvalidLines != 0 || len(result.Errors) != 0 {
		t.Fatalf("current Grok Build sync = %#v, err = %v", result, err)
	}
	report, err := environment.service.Report(ctx, UsageReportRequest{
		ProviderID: grokconfig.ProviderID,
		Range:      UsageRangeAll,
	})
	if err != nil || report.Summary.EventCount != 1 || report.Summary.TotalTokens != 1_100 ||
		report.Summary.CostStatus != CostStatusPartial.String() ||
		report.Summary.KnownEstimatedCostUSD != "0.002300" || report.Summary.PartialCostEventCount != 1 ||
		report.Summary.KnownReportedCostUSD != "0.0000012345" ||
		report.Summary.ReportedCostStatus != ReportedCostStatusReported.String() ||
		report.Summary.ReportedCostEventCount != 1 || report.Summary.ReportedCostCoverage != 1 {
		t.Fatalf("current Grok Build report = %#v, err = %v", report, err)
	}
}

func TestUsageSyncGrokBuildParserUpgradeBackfillsReportedCost(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	grokHome := t.TempDir()
	fixture, err := os.ReadFile(filepath.Join("testdata", "grok-build-v1.0.25", "valid.jsonl"))
	if err != nil {
		t.Fatalf("read current Grok Build fixture: %v", err)
	}
	writeGrokBuildUsageFixture(t, grokHome, "workspace-upgrade", "session-upgrade", string(fixture))
	environment := newGrokBuildUsageTestEnvironment(t, configDir, grokHome)
	initialized, err := bootstrap.NewService(environment.runtime, nil, nil).Initialize(ctx)
	if err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}
	if _, err := environment.service.SyncGrokBuild(ctx); err != nil {
		t.Fatalf("initial Grok Build sync: %v", err)
	}

	rawDB, err := sql.Open("sqlite", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("open usage database: %v", err)
	}
	if _, err := rawDB.ExecContext(ctx, `
		UPDATE usage_facts SET reported_cost_usd_ticks = NULL, reported_cost_status = 0;
		UPDATE grok_build_usage_import_files SET parser_revision = ?
	`, GrokBuildUsageParserRevision-1); err != nil {
		_ = rawDB.Close()
		t.Fatalf("downgrade reported cost fixture: %v", err)
	}
	if err := rawDB.Close(); err != nil {
		t.Fatalf("close usage database: %v", err)
	}

	result, err := environment.service.SyncGrokBuild(ctx)
	if err != nil || result.SkippedDuplicateEvents != 1 || result.InvalidLines != 0 {
		t.Fatalf("parser upgrade sync = %#v, err = %v, cause = %v", result, err, errors.Unwrap(err))
	}
	summary, err := environment.service.Summary(ctx, UsageSummaryRequest{ProviderID: grokconfig.ProviderID})
	if err != nil || summary.ReportedCostUSD == nil || *summary.ReportedCostUSD != "0.0000012345" ||
		summary.ReportedCostStatus != ReportedCostStatusReported.String() {
		t.Fatalf("backfilled reported cost summary = %#v, err = %v", summary, err)
	}
}

func TestUsageSyncGrokBuildSameRevisionBackfillsReportedCost(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	grokHome := t.TempDir()
	line := syntheticGrokBuildUsageLine(
		"session-enriched",
		"prompt-enriched",
		"grok-4.6-build",
		1_750_000_000,
		TokenCounts{InputTokens: 100, CachedInputTokens: 40, OutputTokens: 20, TotalTokens: 120},
	)
	path := writeGrokBuildUsageFixture(t, grokHome, "workspace-enriched", "session-enriched", line)
	environment := newGrokBuildUsageTestEnvironment(t, configDir, grokHome)
	if _, err := bootstrap.NewService(environment.runtime, nil, nil).Initialize(ctx); err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}
	if _, err := environment.service.SyncGrokBuild(ctx); err != nil {
		t.Fatalf("initial Grok Build sync: %v", err)
	}
	before, err := environment.service.Summary(ctx, UsageSummaryRequest{ProviderID: grokconfig.ProviderID})
	if err != nil || before.ReportedCostUSD != nil || before.ReportedCostStatus != ReportedCostStatusUnknown.String() {
		t.Fatalf("initial reported cost summary = %#v, err = %v", before, err)
	}

	enriched := strings.ReplaceAll(line, `"costIsPartial":false`, `"costIsPartial":false,"costUsdTicks":5452000000`)
	if strings.Count(enriched, `"costUsdTicks"`) != 2 {
		t.Fatalf("reported cost fixture was not enriched: %s", enriched)
	}
	writeAppUsageFile(t, path, enriched)
	result, err := environment.service.SyncGrokBuild(ctx)
	if err != nil || result.ImportedEvents != 0 || result.SkippedDuplicateEvents != 1 || len(result.Errors) != 0 {
		t.Fatalf("same-revision cost backfill = %#v, err = %v", result, err)
	}
	after, err := environment.service.Summary(ctx, UsageSummaryRequest{ProviderID: grokconfig.ProviderID})
	if err != nil || after.ReportedCostUSD == nil || *after.ReportedCostUSD != "0.5452000000" ||
		after.ReportedCostStatus != ReportedCostStatusReported.String() {
		t.Fatalf("backfilled reported cost summary = %#v, err = %v", after, err)
	}
}

func TestBackgroundGrokBuildSyncRetriesObservationAfterParserUpgrade(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	grokHome := t.TempDir()
	fixture, err := os.ReadFile(filepath.Join("testdata", "grok-build-v1.0.25", "valid.jsonl"))
	if err != nil {
		t.Fatalf("read current Grok Build fixture: %v", err)
	}
	broken := strings.Replace(string(fixture), `"cacheCreationTokens":50`, `"cacheCreationTokens":"50"`, 1)
	path := writeGrokBuildUsageFixture(t, grokHome, "workspace-broken", "session-broken", broken)
	environment := newGrokBuildUsageTestEnvironment(t, configDir, grokHome)
	initialized, err := bootstrap.NewService(environment.runtime, nil, nil).Initialize(ctx)
	if err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}
	first, err := environment.service.SyncGrokBuild(ctx)
	if err != nil || len(first.Errors) != 1 {
		t.Fatalf("initial broken Grok Build sync = %#v, err = %v", first, err)
	}
	additive, err := os.ReadFile(filepath.Join("testdata", "grok-build-v0.2.114", "format-drift.jsonl"))
	if err != nil {
		t.Fatalf("read additive Grok Build fixture: %v", err)
	}
	writeAppUsageFile(t, path, string(additive))
	files, err := ListGrokBuildSessionFilesContext(ctx, grokHome)
	if err != nil || len(files) != 1 {
		t.Fatalf("list rewritten Grok Build fixture: files=%#v, err=%v", files, err)
	}

	rawDB, err := sql.Open("sqlite", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("open usage database: %v", err)
	}
	if _, err := rawDB.ExecContext(ctx, `
		UPDATE usage_import_observations
		SET parser_revision = 0, metadata_digest = ?
	`, files[0].MetadataDigest); err != nil {
		_ = rawDB.Close()
		t.Fatalf("downgrade observation fixture: %v", err)
	}
	if err := rawDB.Close(); err != nil {
		t.Fatalf("close usage database: %v", err)
	}

	observer := &usageSyncReadObserver{}
	retried, err := environment.service.sync(ctx, UsageSyncRequest{ProviderID: grokconfig.ProviderID}, SyncOptions{
		ProvisionMode: SyncExistingProvider,
		Observer:      observer,
	})
	if err != nil || !retried.Performed || retried.Result.ImportedEvents != 1 ||
		len(retried.Result.Errors) != 0 || observer.opened.Load() != 1 {
		t.Fatalf("parser-upgrade retry = %#v, err = %v, opens = %d", retried, err, observer.opened.Load())
	}

	rawDB, err = sql.Open("sqlite", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("reopen usage database: %v", err)
	}
	defer rawDB.Close()
	var observations int64
	if err := rawDB.QueryRowContext(ctx, `SELECT COUNT(1) FROM usage_import_observations`).Scan(&observations); err != nil {
		t.Fatalf("read retried observations: %v", err)
	}
	if observations != 0 {
		t.Fatalf("retried observations = %d, want 0", observations)
	}
	summary, err := environment.service.Summary(ctx, UsageSummaryRequest{ProviderID: grokconfig.ProviderID})
	if err != nil || summary.EventCount != 1 || summary.TotalTokens != 2 {
		t.Fatalf("parser-upgrade summary = %#v, err = %v", summary, err)
	}
}

func TestBackgroundGrokBuildSyncNoopAndBoundedAppend(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	grokHome := t.TempDir()
	benign := `{"timestamp":1,"method":"session/update","params":{"sessionId":"session-tail","update":{"sessionUpdate":"agent_message_chunk"}}}` + "\n"
	path := writeGrokBuildUsageFixture(
		t,
		grokHome,
		"workspace-tail",
		"session-tail",
		strings.Repeat(benign, 20_000)+syntheticGrokBuildUsageLine(
			"session-tail",
			"prompt-a",
			"grok-build-latest",
			1_750_000_000,
			TokenCounts{InputTokens: 100, CachedInputTokens: 20, OutputTokens: 10, TotalTokens: 110},
		),
	)
	environment := newGrokBuildUsageTestEnvironment(t, configDir, grokHome)
	if _, err := bootstrap.NewService(environment.runtime, nil, nil).Initialize(ctx); err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}
	if _, err := environment.service.SyncGrokBuild(ctx); err != nil {
		t.Fatalf("initial Grok Build sync: %v", err)
	}

	idleObserver := &usageSyncReadObserver{}
	idle, err := environment.service.sync(ctx, UsageSyncRequest{ProviderID: grokconfig.ProviderID}, SyncOptions{
		ProvisionMode: SyncExistingProvider,
		Observer:      idleObserver,
	})
	if err != nil || idle.Performed || idle.Result.SkippedUnchangedFiles != 1 {
		t.Fatalf("idle Grok Build outcome = %#v, err = %v", idle, err)
	}
	if idleObserver.opened.Load() != 0 || idleObserver.bytes.Load() != 0 {
		t.Fatalf("idle Grok Build sync read content: opens=%d bytes=%d", idleObserver.opened.Load(), idleObserver.bytes.Load())
	}

	appendedLine := syntheticGrokBuildUsageLine(
		"session-tail",
		"prompt-b",
		"grok-4.5",
		1_750_000_001,
		TokenCounts{InputTokens: 50, CachedInputTokens: 10, OutputTokens: 5, TotalTokens: 55},
	)
	appendAppUsageFixture(t, path, appendedLine)
	appendObserver := &usageSyncReadObserver{}
	appended, err := environment.service.sync(ctx, UsageSyncRequest{ProviderID: grokconfig.ProviderID}, SyncOptions{
		ProvisionMode: SyncExistingProvider,
		Observer:      appendObserver,
	})
	if err != nil || !appended.Performed || appended.Result.ImportedEvents != 1 {
		t.Fatalf("Grok Build tail outcome = %#v, err = %v", appended, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat Grok Build fixture: %v", err)
	}
	maxRead := usageBoundaryBytes + int64(len(appendedLine)+1)
	if appendObserver.opened.Load() != 1 || appendObserver.bytes.Load() > maxRead {
		t.Fatalf("Grok Build append read was not bounded: opens=%d bytes=%d size=%d", appendObserver.opened.Load(), appendObserver.bytes.Load(), info.Size())
	}
}

func TestBackgroundGrokBuildSyncKeepsCursorCreatedAfterPreflight(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	grokHome := t.TempDir()
	oldPath := writeGrokBuildUsageFixture(
		t,
		grokHome,
		"workspace-old",
		"session-old",
		syntheticGrokBuildUsageLine(
			"session-old",
			"prompt-old",
			"grok-build-latest",
			1_750_000_000,
			TokenCounts{InputTokens: 10, OutputTokens: 2, TotalTokens: 12},
		),
	)
	environment := newGrokBuildUsageTestEnvironment(t, configDir, grokHome)
	if _, err := bootstrap.NewService(environment.runtime, nil, nil).Initialize(ctx); err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}
	if _, err := environment.service.SyncGrokBuild(ctx); err != nil {
		t.Fatalf("initial Grok Build sync: %v", err)
	}
	if err := os.Remove(oldPath); err != nil {
		t.Fatalf("remove old Grok Build fixture: %v", err)
	}

	var second BackgroundSyncOutcome
	var secondErr error
	newPath := filepath.Join(grokHome, "sessions", "workspace-new", "session-new", "updates.jsonl")
	first, err := environment.service.SyncProviderBackground(ctx, grokconfig.ProviderID, func() {
		writeAppUsageFile(t, newPath, syntheticGrokBuildUsageLine(
			"session-new",
			"prompt-new",
			"grok-build-latest",
			1_750_000_001,
			TokenCounts{InputTokens: 20, OutputTokens: 4, TotalTokens: 24},
		))
		other := newGrokBuildUsageTestEnvironment(t, configDir, grokHome)
		second, secondErr = other.service.SyncProviderBackground(ctx, grokconfig.ProviderID, nil)
	})
	if err != nil {
		t.Fatalf("first background Grok Build sync: %v", err)
	}
	if secondErr != nil || !second.Performed || second.Result.ImportedEvents != 1 {
		t.Fatalf("second background Grok Build sync = %#v, err = %v", second, secondErr)
	}
	if !first.Performed {
		t.Fatalf("first background Grok Build sync was not performed: %#v", first)
	}

	fileKey, err := SourceKey(newPath)
	if err != nil {
		t.Fatalf("derive new Grok Build file key: %v", err)
	}
	db, err := environment.runtime.StoreFactory().OpenHealthy(ctx, true)
	if err != nil {
		t.Fatalf("open Store: %v", err)
	}
	defer db.Close()
	source, err := db.GetUsageSource(ctx, grokconfig.ProviderID, SourceGrokBuildSessionJSONL)
	if err != nil {
		t.Fatalf("read Grok Build usage source: %v", err)
	}
	if _, err := db.GetGrokBuildUsageImportFile(ctx, source.ID, fileKey); err != nil {
		t.Fatalf("new Grok Build cursor was removed by stale finalization: %v", err)
	}
	summary, err := environment.service.Summary(ctx, UsageSummaryRequest{ProviderID: grokconfig.ProviderID})
	if err != nil || summary.EventCount != 2 || summary.TotalTokens != 36 {
		t.Fatalf("Grok Build summary after stale-snapshot race = %#v, err = %v", summary, err)
	}
}

func TestUsageSyncGrokBuildBackfillsNewlyRecognizedUnknownModels(t *testing.T) {
	ctx := context.Background()
	environment := newGrokBuildUsageTestEnvironment(t, t.TempDir(), t.TempDir())
	if _, err := bootstrap.NewService(environment.runtime, nil, nil).Initialize(ctx); err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}
	if _, err := environment.service.SyncGrokBuild(ctx); err != nil {
		t.Fatalf("provision Grok Build usage source: %v", err)
	}

	db, err := environment.runtime.StoreFactory().OpenHealthy(ctx, false)
	if err != nil {
		t.Fatalf("open Store: %v", err)
	}
	source, err := db.BeginUsageSync(
		ctx,
		grokconfig.ProviderID,
		SourceGrokBuildSessionJSONL,
		GrokBuildUsageIdentityRevision,
	)
	if err != nil {
		_ = db.Close()
		t.Fatalf("begin historical sync: %v", err)
	}
	if _, err := db.InsertUsageFacts(ctx, store.InsertUsageFactsParams{
		SourceID:   source.ID,
		Generation: source.SyncGeneration,
		Facts: []store.CreateUsageFactParams{{
			EventKey:     usageTestEventKey("grok-4.6-build-historical"),
			SourceID:     source.ID,
			ModelKey:     "grok-4.6-build",
			InputTokens:  1_000_000,
			OutputTokens: 1_000_000,
			TotalTokens:  2_000_000,
			CostStatus:   store.UsageCostStatusUnknown,
		}},
	}); err != nil {
		_ = db.Close()
		t.Fatalf("seed unknown historical cost: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close Store: %v", err)
	}

	if _, err := environment.service.SyncGrokBuild(ctx); err != nil {
		t.Fatalf("backfill Grok Build usage price: %v", err)
	}
	summary, err := environment.service.Summary(ctx, UsageSummaryRequest{
		ProviderID: grokconfig.ProviderID,
	})
	if err != nil {
		t.Fatalf("read backfilled summary: %v", err)
	}
	if summary.CostStatus != CostStatusEstimated.String() ||
		summary.EstimatedCostUSD == nil ||
		*summary.EstimatedCostUSD != "8.000000" {
		t.Fatalf("backfilled summary = %#v", summary)
	}
}

func TestUsageSyncGrokBuildConcurrentRunsRemainIdempotent(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	grokHome := t.TempDir()
	environment := newGrokBuildUsageTestEnvironment(t, configDir, grokHome)
	if _, err := bootstrap.NewService(environment.runtime, nil, nil).Initialize(ctx); err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}
	if _, err := environment.service.SyncGrokBuild(ctx); err != nil {
		t.Fatalf("provision Grok Build usage source: %v", err)
	}
	writeGrokBuildUsageFixture(
		t,
		grokHome,
		"workspace-concurrent",
		"session-concurrent",
		syntheticGrokBuildUsageLine(
			"session-concurrent",
			"prompt-concurrent",
			"grok-build-latest",
			1_750_000_000,
			TokenCounts{InputTokens: 100, CachedInputTokens: 20, OutputTokens: 10, TotalTokens: 110},
		),
	)

	services := []*Service{
		newGrokBuildUsageTestEnvironment(t, configDir, grokHome).service,
		newGrokBuildUsageTestEnvironment(t, configDir, grokHome).service,
	}
	start := make(chan struct{})
	errorsByRun := make(chan error, len(services))
	var wait sync.WaitGroup
	for _, service := range services {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, err := service.SyncGrokBuild(ctx)
			errorsByRun <- err
		}()
	}
	close(start)
	wait.Wait()
	close(errorsByRun)

	var succeeded, superseded int
	for err := range errorsByRun {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, store.ErrUsageSyncSuperseded), errors.Is(err, store.ErrUsageCursorConflict):
			assertAppErrorCode(t, err, apperror.UsageSyncConflict)
			superseded++
		default:
			t.Fatalf("unexpected concurrent Grok sync error: %v", err)
		}
	}
	if succeeded == 0 || succeeded+superseded != len(services) {
		t.Fatalf("concurrent Grok sync outcomes succeeded=%d superseded=%d", succeeded, superseded)
	}

	summary, err := environment.service.Summary(ctx, UsageSummaryRequest{ProviderID: grokconfig.ProviderID})
	if err != nil {
		t.Fatalf("read concurrent Grok summary: %v", err)
	}
	if summary.EventCount != 1 || summary.TotalTokens != 110 {
		t.Fatalf("concurrent Grok sync was not idempotent: %#v", summary)
	}
}

func TestUsageSyncGrokBuildTruncationPreservesCommittedFactsAndCursor(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	grokHome := t.TempDir()
	path := writeGrokBuildUsageFixture(
		t,
		grokHome,
		"workspace-truncated",
		"session-truncated",
		syntheticGrokBuildUsageLine(
			"session-truncated",
			"prompt-truncated",
			"grok-build-latest",
			1_750_000_000,
			TokenCounts{InputTokens: 100, CachedInputTokens: 20, OutputTokens: 10, TotalTokens: 110},
		),
	)
	environment := newGrokBuildUsageTestEnvironment(t, configDir, grokHome)
	if _, err := bootstrap.NewService(environment.runtime, nil, nil).Initialize(ctx); err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}
	if _, err := environment.service.SyncGrokBuild(ctx); err != nil {
		t.Fatalf("initial Grok sync: %v", err)
	}
	db, err := environment.runtime.StoreFactory().OpenHealthy(ctx, false)
	if err != nil {
		t.Fatalf("open Store: %v", err)
	}
	defer db.Close()
	source, err := db.GetUsageSource(ctx, grokconfig.ProviderID, SourceGrokBuildSessionJSONL)
	if err != nil {
		t.Fatalf("read usage source: %v", err)
	}
	fileKey, err := SourceKey(path)
	if err != nil {
		t.Fatalf("derive truncated file key: %v", err)
	}
	before, err := db.GetGrokBuildUsageImportFile(ctx, source.ID, fileKey)
	if err != nil {
		t.Fatalf("read initial cursor: %v", err)
	}

	writeAppUsageFile(t, path, "")
	result, err := environment.service.SyncGrokBuild(ctx)
	if err != nil || len(result.Errors) != 1 {
		t.Fatalf("truncated sync = %#v, err = %v", result, err)
	}
	after, err := db.GetGrokBuildUsageImportFile(ctx, source.ID, fileKey)
	if err != nil {
		t.Fatalf("read cursor after truncation: %v", err)
	}
	if after != before {
		t.Fatalf("truncation changed cursor: before=%#v after=%#v", before, after)
	}
	summary, err := db.UsageSummary(ctx, grokconfig.ProviderID)
	if err != nil || summary.EventCount != 1 || summary.TotalTokens != 110 {
		t.Fatalf("truncation changed committed facts: summary=%#v err=%v", summary, err)
	}
}

func TestUsageSyncGrokBuildCanonicalizesForksAndQuarantinesConflicts(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	grokHome := t.TempDir()
	tokens := TokenCounts{InputTokens: 100, CachedInputTokens: 25, OutputTokens: 20, TotalTokens: 120}
	parentPath := writeGrokBuildUsageFixture(
		t,
		grokHome,
		"a-workspace",
		"parent",
		syntheticGrokBuildUsageLine("session-parent", "shared-prompt", "grok-build-latest", 2_000, tokens),
	)
	writeGrokBuildUsageFixture(
		t,
		grokHome,
		"b-workspace",
		"child",
		syntheticGrokBuildUsageLine("session-child", "shared-prompt", "grok-build-latest", 1_000, tokens),
	)
	environment := newGrokBuildUsageTestEnvironment(t, configDir, grokHome)
	if _, err := bootstrap.NewService(environment.runtime, nil, nil).Initialize(ctx); err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}
	result, err := environment.service.SyncGrokBuild(ctx)
	if err != nil {
		t.Fatalf("sync fork fixtures: %v", err)
	}
	if result.ImportedEvents != 1 || result.SkippedDuplicateEvents != 1 || len(result.Errors) != 0 {
		t.Fatalf("fork sync = %#v", result)
	}
	db, err := environment.runtime.StoreFactory().OpenHealthy(ctx, false)
	if err != nil {
		t.Fatalf("open Store: %v", err)
	}
	source, err := db.GetUsageSource(ctx, grokconfig.ProviderID, SourceGrokBuildSessionJSONL)
	if err != nil {
		_ = db.Close()
		t.Fatalf("read source: %v", err)
	}
	eventKey := GrokBuildEventID("shared-prompt", "grok-build-latest")
	if eventKey.IsZero() {
		_ = db.Close()
		t.Fatal("shared event key is empty")
	}
	snapshot, err := db.UsageReport(ctx, store.UsageReportQuery{
		ProviderID: grokconfig.ProviderID,
	})
	if err != nil {
		_ = db.Close()
		t.Fatalf("read fork report: %v", err)
	}
	earliest, err := db.EarliestDatedUsageUnixMS(ctx, grokconfig.ProviderID)
	if err != nil {
		_ = db.Close()
		t.Fatalf("read earliest Grok usage: %v", err)
	}
	if snapshot.Summary.EventCount != 1 ||
		snapshot.Summary.SessionCount != 1 ||
		earliest != 1_000_000 {
		_ = db.Close()
		t.Fatalf("canonical fork report = %#v, earliest = %d", snapshot.Summary, earliest)
	}

	conflictPath := writeGrokBuildUsageFixture(
		t,
		grokHome,
		"c-workspace",
		"conflict",
		syntheticGrokBuildUsageLine(
			"session-conflict",
			"shared-prompt",
			"grok-build-latest",
			3_000,
			TokenCounts{InputTokens: 101, CachedInputTokens: 25, OutputTokens: 20, TotalTokens: 121},
		),
	)
	conflictKey, err := SourceKey(conflictPath)
	if err != nil {
		_ = db.Close()
		t.Fatalf("derive conflict source key: %v", err)
	}
	conflicted, err := environment.service.SyncGrokBuild(ctx)
	if err != nil {
		_ = db.Close()
		t.Fatalf("conflicting file should be isolated, not abort sync: %v", err)
	}
	if len(conflicted.Errors) != 1 ||
		conflicted.Errors[0].FileName != "" ||
		conflicted.Errors[0].SourceKey != "" ||
		conflicted.InvalidLines != 1 {
		_ = db.Close()
		t.Fatalf("conflict result = %#v", conflicted)
	}
	encoded, err := json.Marshal(conflicted.Errors[0])
	if err != nil ||
		strings.Contains(string(encoded), "file_name") ||
		strings.Contains(string(encoded), "source_key") ||
		strings.Contains(string(encoded), filepath.Base(conflictPath)) {
		_ = db.Close()
		t.Fatalf("conflict error leaked an identifier: %s, err = %v", encoded, err)
	}
	if _, err := db.GetGrokBuildUsageImportFile(ctx, source.ID, conflictKey); !errors.Is(err, store.ErrNotFound) {
		_ = db.Close()
		t.Fatalf("conflicting file advanced cursor: %v", err)
	}
	summary, err := db.UsageSummary(ctx, grokconfig.ProviderID)
	if err != nil || summary.EventCount != 1 {
		_ = db.Close()
		t.Fatalf("conflicting file changed facts: summary=%#v err=%v", summary, err)
	}

	parentKey, err := SourceKey(parentPath)
	if err != nil {
		_ = db.Close()
		t.Fatalf("derive parent source key: %v", err)
	}
	parentCursor, err := db.GetGrokBuildUsageImportFile(ctx, source.ID, parentKey)
	if err != nil {
		_ = db.Close()
		t.Fatalf("read parent cursor: %v", err)
	}
	writeAppUsageFile(t, parentPath, syntheticGrokBuildUsageLine(
		"session-parent",
		"shared-prompt",
		"grok-build-latest",
		2_000,
		TokenCounts{InputTokens: 102, CachedInputTokens: 25, OutputTokens: 20, TotalTokens: 122},
	))
	drifted, err := environment.service.SyncGrokBuild(ctx)
	if err != nil || len(drifted.Errors) == 0 {
		_ = db.Close()
		t.Fatalf("rewritten history result = %#v, err = %v", drifted, err)
	}
	unchangedCursor, err := db.GetGrokBuildUsageImportFile(ctx, source.ID, parentKey)
	if err != nil {
		_ = db.Close()
		t.Fatalf("read unchanged parent cursor: %v", err)
	}
	if unchangedCursor != parentCursor {
		_ = db.Close()
		t.Fatalf("rewritten history changed cursor: before=%#v after=%#v", parentCursor, unchangedCursor)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close Store: %v", err)
	}
}

func TestBackgroundGrokBuildSyncDoesNotCreateOrRebindProvider(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	firstHome := t.TempDir()
	environment := newGrokBuildUsageTestEnvironment(t, configDir, firstHome)
	if _, err := bootstrap.NewService(environment.runtime, nil, nil).Initialize(ctx); err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}
	background, err := environment.service.SyncProviderBackground(ctx, grokconfig.ProviderID, nil)
	if err != nil || background.Result.ProviderID != grokconfig.ProviderID || background.Result.ImportedEvents != 0 || background.Performed {
		t.Fatalf("missing Provider background sync = %#v, err = %v", background, err)
	}
	db, err := environment.runtime.StoreFactory().OpenHealthy(ctx, true)
	if err != nil {
		t.Fatalf("open Store: %v", err)
	}
	if _, err := db.GetProvider(ctx, grokconfig.ProviderID); !errors.Is(err, store.ErrNotFound) {
		_ = db.Close()
		t.Fatalf("background sync created Provider: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close Store: %v", err)
	}

	if _, err := environment.service.SyncGrokBuild(ctx); err != nil {
		t.Fatalf("explicit sync should provision Provider: %v", err)
	}
	secondHome := t.TempDir()
	rebound := newGrokBuildUsageTestEnvironment(t, configDir, secondHome)
	if _, err := rebound.service.SyncGrokBuild(ctx); err == nil {
		t.Fatal("explicit sync silently rebound Grok Home")
	}
	db, err = rebound.runtime.StoreFactory().OpenHealthy(ctx, true)
	if err != nil {
		t.Fatalf("reopen Store: %v", err)
	}
	defer db.Close()
	provider, err := db.GetProvider(ctx, grokconfig.ProviderID)
	if err != nil {
		t.Fatalf("read Provider: %v", err)
	}
	metadata, err := grokpreset.DecodeProviderMetadata(provider.MetadataJSON)
	if err != nil || metadata.GrokHome != firstHome {
		t.Fatalf("Provider locator changed: %#v, err = %v", metadata, err)
	}
}

func newGrokBuildUsageTestEnvironment(
	t *testing.T,
	configDir string,
	grokHome string,
) *grokBuildUsageTestEnvironment {
	t.Helper()
	runtimeService, err := profilesruntime.NewService(configDir)
	if err != nil {
		t.Fatalf("create runtime service: %v", err)
	}
	return &grokBuildUsageTestEnvironment{
		runtime: runtimeService,
		service: NewService(
			runtimeService.StoreFactory(),
			MustRegistry(NewGrokBuildIntegration(grokHome)),
		),
	}
}

func writeGrokBuildUsageFixture(
	t *testing.T,
	grokHome string,
	workspace string,
	session string,
	content string,
) string {
	t.Helper()
	path := filepath.Join(grokHome, "sessions", workspace, session, "updates.jsonl")
	writeAppUsageFile(t, path, content)
	return path
}

func syntheticGrokBuildUsageLine(
	sessionID string,
	promptID string,
	model string,
	timestamp uint64,
	tokens TokenCounts,
) string {
	row := map[string]any{
		"inputTokens":         tokens.InputTokens,
		"outputTokens":        tokens.OutputTokens,
		"totalTokens":         tokens.TotalTokens,
		"cachedReadTokens":    tokens.CachedInputTokens,
		"cacheCreationTokens": int64(0),
		"reasoningTokens":     int64(0),
		"modelCalls":          int64(1),
		"apiDurationMs":       int64(1),
		"costIsPartial":       false,
	}
	raw, err := json.Marshal(map[string]any{
		"timestamp": timestamp,
		"method":    grokBuildSessionUpdateMethod,
		"params": map[string]any{
			"sessionId": sessionID,
			"update": map[string]any{
				"sessionUpdate": grokBuildTurnCompleted,
				"prompt_id":     promptID,
				"stop_reason":   "end_turn",
				"usage": map[string]any{
					"inputTokens":         tokens.InputTokens,
					"outputTokens":        tokens.OutputTokens,
					"totalTokens":         tokens.TotalTokens,
					"cachedReadTokens":    tokens.CachedInputTokens,
					"cacheCreationTokens": int64(0),
					"reasoningTokens":     int64(0),
					"modelCalls":          int64(1),
					"apiDurationMs":       int64(1),
					"costIsPartial":       false,
					"modelUsage":          map[string]any{model: row},
					"numTurns":            int64(1),
				},
			},
		},
	})
	if err != nil {
		panic(err)
	}
	return string(raw)
}
