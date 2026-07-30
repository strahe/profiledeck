package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"testing"
)

func TestGrokBuildUsageForkCanonicalizationIsImportOrderIndependent(t *testing.T) {
	observations := []struct {
		session string
		time    int64
	}{
		{session: grokBuildTestSession("z"), time: 0},
		{session: grokBuildTestSession("m"), time: 2_000},
		{session: grokBuildTestSession("a"), time: 2_000},
		{session: grokBuildTestSession("y"), time: 3_000},
	}
	for _, test := range []struct {
		name  string
		order []int
	}{
		{name: "forward", order: []int{0, 1, 2, 3}},
		{name: "reverse", order: []int{3, 2, 1, 0}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			db := migratedTestStore(t, ctx)
			defer closeTestStore(t, db)
			createUsageProviderFixture(t, ctx, db, "grok-build")
			source, err := db.BeginUsageSync(ctx, "grok-build", "grok-build-session-jsonl", 1)
			if err != nil {
				t.Fatalf("begin sync: %v", err)
			}
			eventKey := testUsageKey("copied-fork-event")
			for position, index := range test.order {
				observation := observations[index]
				fact := CreateUsageFactParams{
					EventKey:         eventKey,
					SourceID:         source.ID,
					SessionKey:       observation.session,
					ModelKey:         "grok-build-latest",
					OccurredAtUnixMS: observation.time,
					InputTokens:      10,
					OutputTokens:     2,
					TotalTokens:      12,
					CostStatus:       UsageCostStatusUnknown,
				}
				result, err := db.CommitGrokBuildUsageImport(
					ctx,
					testGrokBuildUsageImport(source, CommitGrokBuildUsageImportParams{
						ProviderID: "grok-build",
						Facts:      []CreateUsageFactParams{fact},
						File: GrokBuildUsageImportFile{
							SourceID:         source.ID,
							FileKey:          testUsageKey("fork-file-" + observation.session),
							ImportedFacts:    1,
							ParserRevision:   1,
							IdentityRevision: 1,
							EventDigest:      testUsageKey("fork-digest-" + observation.session),
						},
					}),
				)
				if err != nil {
					t.Fatalf("commit observation %d: %v", position, err)
				}
				if position == 0 && result.Inserted != 1 ||
					position > 0 && result.Duplicates != 1 {
					t.Fatalf("observation %d result = %#v", position, result)
				}
			}

			var sessionKey string
			var occurredAt int64
			if err := db.executor().QueryRowContext(ctx, `
				SELECT sessions.session_key, facts.occurred_at_unix_ms
				FROM usage_facts AS facts
				JOIN usage_sessions AS sessions
					ON sessions.source_id = facts.source_id AND sessions.id = facts.session_id
				WHERE facts.event_key = ?
			`, eventKey).Scan(&sessionKey, &occurredAt); err != nil {
				t.Fatalf("read canonical observation: %v", err)
			}
			wantSession := observations[1].session
			if observations[2].session < wantSession {
				wantSession = observations[2].session
			}
			if sessionKey != wantSession || occurredAt != 2_000 {
				t.Fatalf("canonical observation = (%q, %d)", sessionKey, occurredAt)
			}
		})
	}
}

func TestGrokBuildUsageConflictAndCursorCASRollBackWholeFile(t *testing.T) {
	ctx := context.Background()
	db := migratedTestStore(t, ctx)
	defer closeTestStore(t, db)
	createUsageProviderFixture(t, ctx, db, "grok-build")
	source, err := db.BeginUsageSync(ctx, "grok-build", "grok-build-session-jsonl", 1)
	if err != nil {
		t.Fatalf("begin sync: %v", err)
	}
	base := CreateUsageFactParams{
		EventKey: testUsageKey("grok-base"), SourceID: source.ID,
		SessionKey: grokBuildTestSession("a"), ModelKey: "grok-build-latest",
		InputTokens: 10, OutputTokens: 2, TotalTokens: 12,
		CostStatus: UsageCostStatusUnknown,
	}
	baseFile := GrokBuildUsageImportFile{
		SourceID: source.ID, FileKey: testUsageKey("grok-base-file"), ImportedFacts: 1,
		ParserRevision: 1, IdentityRevision: 1, EventDigest: testUsageKey("grok-base-digest"),
	}
	directSession := base
	directSession.SessionKey = "raw-session-identifier"
	directSessionFile := baseFile
	directSessionFile.FileKey = testUsageKey("grok-direct-session-file")
	directSessionFile.EventDigest = testUsageKey("grok-direct-session-digest")
	if _, err := db.CommitGrokBuildUsageImport(ctx, testGrokBuildUsageImport(
		source,
		CommitGrokBuildUsageImportParams{
			ProviderID: "grok-build",
			Facts:      []CreateUsageFactParams{directSession},
			File:       directSessionFile,
		},
	)); err == nil {
		t.Fatal("Grok import accepted a direct session identifier")
	}
	if _, err := db.GetGrokBuildUsageImportFile(ctx, source.ID, directSessionFile.FileKey); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invalid direct session advanced a cursor: %v", err)
	}
	if _, err := db.CommitGrokBuildUsageImport(ctx, testGrokBuildUsageImport(
		source,
		CommitGrokBuildUsageImportParams{
			ProviderID: "grok-build",
			Facts:      []CreateUsageFactParams{base},
			File:       baseFile,
		},
	)); err != nil {
		t.Fatalf("commit base file: %v", err)
	}

	newFact := base
	newFact.EventKey = testUsageKey("grok-must-roll-back")
	conflict := base
	conflict.InputTokens++
	conflict.TotalTokens++
	conflictFile := GrokBuildUsageImportFile{
		SourceID: source.ID, FileKey: testUsageKey("grok-conflict-file"), ImportedFacts: 2,
		ParserRevision: 1, IdentityRevision: 1, EventDigest: testUsageKey("grok-conflict-digest"),
	}
	if _, err := db.CommitGrokBuildUsageImport(ctx, testGrokBuildUsageImport(
		source,
		CommitGrokBuildUsageImportParams{
			ProviderID: "grok-build",
			Facts:      []CreateUsageFactParams{newFact, conflict},
			File:       conflictFile,
		},
	)); !errors.Is(err, ErrUsageFactConflict) {
		t.Fatalf("conflicting file error = %v", err)
	}
	if _, err := db.GetGrokBuildUsageImportFile(ctx, source.ID, conflictFile.FileKey); !errors.Is(err, ErrNotFound) {
		t.Fatalf("conflicting file advanced its cursor: %v", err)
	}
	assertGrokBuildUsageFactCount(t, ctx, db, 1)

	current, err := db.GetGrokBuildUsageImportFile(ctx, source.ID, baseFile.FileKey)
	if err != nil {
		t.Fatalf("read base cursor: %v", err)
	}
	firstAppend := base
	firstAppend.EventKey = testUsageKey("grok-first-append")
	firstDesired := current
	firstDesired.SizeBytes++
	firstDesired.ImportedFacts++
	firstDesired.EventDigest = testUsageKey("grok-first-append-digest")
	if _, err := db.CommitGrokBuildUsageImport(ctx, testGrokBuildUsageImport(
		source,
		CommitGrokBuildUsageImportParams{
			ProviderID: "grok-build",
			Facts:      []CreateUsageFactParams{firstAppend},
			File:       firstDesired,
			Expected:   &current,
		},
	)); err != nil {
		t.Fatalf("commit first append: %v", err)
	}

	staleAppend := base
	staleAppend.EventKey = testUsageKey("grok-stale-append")
	staleDesired := firstDesired
	staleDesired.EventDigest = testUsageKey("grok-stale-append-digest")
	if _, err := db.CommitGrokBuildUsageImport(ctx, testGrokBuildUsageImport(
		source,
		CommitGrokBuildUsageImportParams{
			ProviderID: "grok-build",
			Facts:      []CreateUsageFactParams{staleAppend},
			File:       staleDesired,
			Expected:   &current,
		},
	)); !errors.Is(err, ErrUsageCursorConflict) {
		t.Fatalf("stale cursor error = %v", err)
	}
	assertGrokBuildUsageFactCount(t, ctx, db, 2)
}

func TestGrokBuildUsageGenerationSupersedesStaleImportsAndFinalization(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "profiledeck.db")
	staleDB := openTestStore(t, ctx, path, false)
	defer closeTestStore(t, staleDB)
	if _, err := staleDB.Migrate(ctx); err != nil {
		t.Fatalf("migrate database: %v", err)
	}
	createUsageProviderFixture(t, ctx, staleDB, "grok-build")
	stale, err := staleDB.BeginUsageSync(ctx, "grok-build", "grok-build-session-jsonl", 1)
	if err != nil {
		t.Fatalf("begin stale sync: %v", err)
	}

	latestDB := openTestStore(t, ctx, path, false)
	defer closeTestStore(t, latestDB)
	latest, err := latestDB.BeginUsageSync(ctx, "grok-build", "grok-build-session-jsonl", 1)
	if err != nil {
		t.Fatalf("begin latest sync: %v", err)
	}
	latestFact := CreateUsageFactParams{
		EventKey: testUsageKey("latest-grok-fact"), SourceID: latest.ID,
		SessionKey: grokBuildTestSession("a"), ModelKey: "grok-build-latest",
		InputTokens: 10, OutputTokens: 2, TotalTokens: 12,
		CostStatus: UsageCostStatusUnknown,
	}
	latestFile := GrokBuildUsageImportFile{
		SourceID: latest.ID, FileKey: testUsageKey("latest-grok-file"), ImportedFacts: 1,
		ParserRevision: 1, IdentityRevision: 1, EventDigest: testUsageKey("latest-grok-digest"),
	}
	if _, err := latestDB.CommitGrokBuildUsageImport(ctx, testGrokBuildUsageImport(
		latest,
		CommitGrokBuildUsageImportParams{
			ProviderID: "grok-build",
			Facts:      []CreateUsageFactParams{latestFact},
			File:       latestFile,
		},
	)); err != nil {
		t.Fatalf("commit latest import: %v", err)
	}
	if err := latestDB.CompleteUsageSync(ctx, CompleteUsageSyncParams{
		SourceID: latest.ID, Generation: latest.SyncGeneration, CompletedAtUnixMS: 20,
		Finalization: &GrokBuildUsageSyncFinalization{
			ProviderID:         "grok-build",
			DiscoveredFileKeys: []UsageKey{latestFile.FileKey},
		},
	}); err != nil {
		t.Fatalf("complete latest sync: %v", err)
	}

	staleFact := latestFact
	staleFact.EventKey = testUsageKey("stale-grok-fact")
	staleFile := latestFile
	staleFile.FileKey = testUsageKey("stale-grok-file")
	staleFile.EventDigest = testUsageKey("stale-grok-digest")
	if _, err := staleDB.CommitGrokBuildUsageImport(ctx, testGrokBuildUsageImport(
		stale,
		CommitGrokBuildUsageImportParams{
			ProviderID: "grok-build",
			Facts:      []CreateUsageFactParams{staleFact},
			File:       staleFile,
		},
	)); !errors.Is(err, ErrUsageSyncSuperseded) {
		t.Fatalf("superseded Grok import error = %v", err)
	}
	if err := staleDB.CompleteUsageSync(ctx, CompleteUsageSyncParams{
		SourceID: stale.ID, Generation: stale.SyncGeneration, CompletedAtUnixMS: 10,
		Finalization: &GrokBuildUsageSyncFinalization{
			ProviderID:         "grok-build",
			DiscoveredFileKeys: []UsageKey{staleFile.FileKey},
		},
	}); !errors.Is(err, ErrUsageSyncSuperseded) {
		t.Fatalf("superseded Grok completion error = %v", err)
	}

	if _, err := latestDB.GetGrokBuildUsageImportFile(ctx, stale.ID, staleFile.FileKey); !errors.Is(err, ErrNotFound) {
		t.Fatalf("superseded Grok sync advanced its cursor: %v", err)
	}
	assertGrokBuildUsageFactCount(t, ctx, latestDB, 1)
	completed, err := latestDB.GetUsageSource(ctx, "grok-build", "grok-build-session-jsonl")
	if err != nil ||
		completed.SyncGeneration != latest.SyncGeneration ||
		completed.LastCompletedAtUnixMS != 20 ||
		completed.TrackedUnits != 1 {
		t.Fatalf("superseded Grok sync changed latest state: source=%#v err=%v", completed, err)
	}
}

func TestGrokBuildUsageDoesNotRepriceDuplicatesAndCascadesWithProvider(t *testing.T) {
	ctx := context.Background()
	db := migratedTestStore(t, ctx)
	defer closeTestStore(t, db)
	createUsageProviderFixture(t, ctx, db, "grok-build")
	if _, err := db.UpsertProviderSetting(ctx, UpsertProviderSettingParams{
		ProviderID: "grok-build", SchemaVersion: ProviderSettingsSchemaVersion,
		SettingsJSON: `{"usage_sync_interval_seconds":30}`,
	}); err != nil {
		t.Fatalf("save Provider settings: %v", err)
	}
	source, err := db.BeginUsageSync(ctx, "grok-build", "grok-build-session-jsonl", 1)
	if err != nil {
		t.Fatalf("begin sync: %v", err)
	}
	fact := CreateUsageFactParams{
		EventKey: testUsageKey("grok-price-stability"), SourceID: source.ID,
		SessionKey: grokBuildTestSession("a"), ModelKey: "grok-build-latest",
		InputTokens: 10, OutputTokens: 2, TotalTokens: 12,
		CostStatus: UsageCostStatusUnknown,
	}
	for index := 0; index < 2; index++ {
		next := fact
		if index == 1 {
			cost := int64(32)
			next.EstimatedCostMicros = &cost
			next.CostStatus = UsageCostStatusEstimated
			next.SessionKey = grokBuildTestSession("b")
		}
		if _, err := db.CommitGrokBuildUsageImport(ctx, testGrokBuildUsageImport(
			source,
			CommitGrokBuildUsageImportParams{
				ProviderID: "grok-build",
				Facts:      []CreateUsageFactParams{next},
				File: GrokBuildUsageImportFile{
					SourceID: source.ID, FileKey: testUsageKey("grok-price-file-" + string(rune('a'+index))),
					ImportedFacts: 1, ParserRevision: 1, IdentityRevision: 1,
					EventDigest: testUsageKey("grok-price-digest-" + string(rune('a'+index))),
				},
			},
		)); err != nil {
			t.Fatalf("commit price observation %d: %v", index, err)
		}
	}
	var status UsageCostStatus
	var cost *int64
	if err := db.executor().QueryRowContext(ctx, `
		SELECT cost_status, estimated_cost_micros FROM usage_facts WHERE event_key = ?
	`, fact.EventKey).Scan(&status, &cost); err != nil {
		t.Fatalf("read stored price: %v", err)
	}
	if status != UsageCostStatusUnknown || cost != nil {
		t.Fatalf("duplicate repriced historical fact: status=%v cost=%v", status, cost)
	}

	if err := db.DeleteProvider(ctx, "grok-build"); err != nil {
		t.Fatalf("delete Provider: %v", err)
	}
	if _, err := db.GetUsageSource(ctx, "grok-build", "grok-build-session-jsonl"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Provider deletion retained source: %v", err)
	}
	if _, err := db.GetProviderSetting(ctx, "grok-build"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Provider deletion retained settings: %v", err)
	}
	var cursors int
	if err := db.executor().QueryRowContext(ctx, `
		SELECT COUNT(1) FROM grok_build_usage_import_files
	`).Scan(&cursors); err != nil {
		t.Fatalf("count Grok cursors: %v", err)
	}
	if cursors != 0 {
		t.Fatalf("Provider deletion retained %d Grok cursors", cursors)
	}
	assertGrokBuildUsageFactCount(t, ctx, db, 0)
}

func testGrokBuildUsageImport(
	source UsageSource,
	params CommitGrokBuildUsageImportParams,
) CommitGrokBuildUsageImportParams {
	params.Generation = source.SyncGeneration
	return params
}

func grokBuildTestSession(character string) string {
	digest := sha256.Sum256([]byte(character))
	return "derived-" + hex.EncodeToString(digest[:])
}

func assertGrokBuildUsageFactCount(
	t *testing.T,
	ctx context.Context,
	db *Store,
	want int,
) {
	t.Helper()
	var count int
	if err := db.executor().QueryRowContext(ctx, `
		SELECT COUNT(1) FROM usage_facts
	`).Scan(&count); err != nil {
		t.Fatalf("count usage facts: %v", err)
	}
	if count != want {
		t.Fatalf("usage fact count = %d, want %d", count, want)
	}
}
