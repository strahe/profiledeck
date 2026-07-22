package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestUsageKeyUsesHexTextEncoding(t *testing.T) {
	key := testUsageKey("json-usage-key")
	encoded, err := json.Marshal(struct {
		Key UsageKey `json:"key"`
	}{Key: key})
	if err != nil {
		t.Fatalf("marshal usage key: %v", err)
	}
	want := `{"key":"` + key.String() + `"}`
	if string(encoded) != want {
		t.Fatalf("encoded usage key = %s, want %s", encoded, want)
	}

	var decoded struct {
		Key UsageKey `json:"key"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal usage key: %v", err)
	}
	if decoded.Key != key {
		t.Fatalf("decoded usage key = %s, want %s", decoded.Key, key)
	}

	preserved := testUsageKey("preserved-usage-key")
	before := preserved
	if err := preserved.UnmarshalText([]byte(strings.Repeat("z", 64))); err == nil {
		t.Fatal("expected invalid hex usage key to fail")
	}
	if preserved != before {
		t.Fatal("invalid usage key text mutated its destination")
	}
}

func TestUsageWritesRejectZeroKeys(t *testing.T) {
	ctx := context.Background()
	db := migratedTestStore(t, ctx)
	defer closeTestStore(t, db)
	source, err := db.BeginUsageSync(ctx, "codex", "codex-session-jsonl", 1)
	if err != nil {
		t.Fatalf("begin usage sync: %v", err)
	}

	if _, err := db.InsertUsageFacts(ctx, testUsageFactBatch(source, []CreateUsageFactParams{{
		SourceID: source.ID, ModelKey: "model", TotalTokens: 1, CostStatus: UsageCostStatusUnknown,
	}})); err == nil {
		t.Fatal("expected zero event key to be rejected")
	}

	validFile := CodexUsageImportFile{
		SourceID: source.ID, FileKey: testUsageKey("valid-file"),
		ParserRevision: 1, IdentityRevision: 1, EventDigest: testUsageKey("valid-digest"),
	}
	zeroFileKey := validFile
	zeroFileKey.FileKey = UsageKey{}
	zeroDigest := validFile
	zeroDigest.EventDigest = UsageKey{}
	for name, file := range map[string]CodexUsageImportFile{
		"file key": zeroFileKey,
		"digest":   zeroDigest,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := db.CommitCodexUsageImport(ctx, testCodexUsageImport(source, CommitCodexUsageImportParams{File: file})); err == nil {
				t.Fatal("expected zero key to be rejected")
			}
		})
	}

	if _, err := db.CommitCodexUsageImport(ctx, testCodexUsageImport(source, CommitCodexUsageImportParams{File: validFile})); err != nil {
		t.Fatalf("commit valid cursor: %v", err)
	}
	if err := db.CompleteUsageSync(ctx, CompleteUsageSyncParams{
		SourceID: source.ID, Generation: source.SyncGeneration,
		Finalization: &CodexUsageSyncFinalization{DiscoveredFileKeys: []UsageKey{{}}},
	}); err == nil {
		t.Fatal("zero discovered key completed without error")
	}
	if _, err := db.GetCodexUsageImportFile(ctx, source.ID, validFile.FileKey); err != nil {
		t.Fatalf("invalid finalization removed cursor: %v", err)
	}
	unchanged, err := db.GetUsageSource(ctx, "codex", "codex-session-jsonl")
	if err != nil || unchanged.LastCompletedAtUnixMS != 0 {
		t.Fatalf("invalid finalization changed source: source=%#v err=%v", unchanged, err)
	}
}

func TestUsageSchemaRejectsZeroKeys(t *testing.T) {
	ctx := context.Background()
	db := migratedTestStore(t, ctx)
	defer closeTestStore(t, db)
	source, err := db.BeginUsageSync(ctx, "codex", "codex-session-jsonl", 1)
	if err != nil {
		t.Fatalf("begin usage sync: %v", err)
	}
	model, err := db.executor().ExecContext(ctx, `
		INSERT INTO usage_models (source_id, model_key) VALUES (?, ?)
	`, source.ID, "model")
	if err != nil {
		t.Fatalf("create model fixture: %v", err)
	}
	modelID, err := model.LastInsertId()
	if err != nil {
		t.Fatalf("read model fixture ID: %v", err)
	}
	if _, err := db.executor().ExecContext(ctx, `
		INSERT INTO usage_facts (event_key, source_id, model_id, cost_status)
		VALUES (zeroblob(32), ?, ?, 0)
	`, source.ID, modelID); err == nil {
		t.Fatal("expected schema to reject zero event key")
	}
	if _, err := db.executor().ExecContext(ctx, `
		INSERT INTO codex_usage_import_files (
			source_id, file_key, parser_revision, identity_revision, event_digest, updated_at_unix_ms
		) VALUES (?, zeroblob(32), 1, 1, ?, 0)
	`, source.ID, testUsageKey("schema-digest")); err == nil {
		t.Fatal("expected schema to reject zero file key")
	}
	if _, err := db.executor().ExecContext(ctx, `
		INSERT INTO codex_usage_import_files (
			source_id, file_key, parser_revision, identity_revision, event_digest, updated_at_unix_ms
		) VALUES (?, ?, 1, 1, zeroblob(32), 0)
	`, source.ID, testUsageKey("schema-file")); err == nil {
		t.Fatal("expected schema to reject zero event digest")
	}
}
