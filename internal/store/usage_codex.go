package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const codexUsageProviderID = "codex"

type CodexUsageImportFile struct {
	SourceID         int64
	FileKey          UsageKey
	ModifiedUnixMS   int64
	SizeBytes        int64
	ImportedFacts    int64
	InvalidLines     int64
	UnsupportedLines int64
	ParserRevision   int64
	IdentityRevision int64
	EventDigest      UsageKey
	UpdatedAtUnixMS  int64
}

type CommitCodexUsageImportParams struct {
	Generation int64
	Facts      []CreateUsageFactParams
	File       CodexUsageImportFile
	Expected   *CodexUsageImportFile
}

type CodexUsageSyncFinalization struct {
	DiscoveredFileKeys []UsageKey
}

func (finalization *CodexUsageSyncFinalization) validateUsageSyncFinalization() error {
	if finalization == nil {
		return errors.New("Codex usage sync finalization is invalid")
	}
	for _, fileKey := range finalization.DiscoveredFileKeys {
		if fileKey.IsZero() {
			return errors.New("Codex usage sync finalization is invalid")
		}
	}
	return nil
}

// ValidateCodexUsageImportIdentity rejects checkpoints produced under another
// fact identity revision.
func (s *Store) ValidateCodexUsageImportIdentity(ctx context.Context, sourceID, identityRevision int64) error {
	if sourceID <= 0 || identityRevision <= 0 {
		return errors.New("Codex usage import identity is invalid")
	}
	source, err := s.getUsageSourceByID(ctx, sourceID)
	if err != nil {
		return err
	}
	if source.ProviderID != codexUsageProviderID {
		return errors.New("Codex usage import source is invalid")
	}
	if source.IdentityRevision != identityRevision {
		return ErrUsageIdentityRevision
	}
	var incompatibleFiles int
	if err := s.executor().QueryRowContext(ctx, `
		SELECT COUNT(1)
		FROM codex_usage_import_files
		WHERE source_id = ? AND identity_revision <> ?
	`, source.ID, identityRevision).Scan(&incompatibleFiles); err != nil {
		return err
	}
	if incompatibleFiles > 0 {
		return ErrUsageIdentityRevision
	}
	return nil
}

func (s *Store) GetCodexUsageImportFile(ctx context.Context, sourceID int64, fileKey UsageKey) (CodexUsageImportFile, error) {
	row := s.executor().QueryRowContext(ctx, `
		SELECT source_id, file_key, modified_unix_ms, size_bytes, imported_facts,
			invalid_lines, unsupported_lines, parser_revision, identity_revision,
			event_digest, updated_at_unix_ms
		FROM codex_usage_import_files
		WHERE source_id = ? AND file_key = ?
	`, sourceID, fileKey)
	cursor, err := scanCodexUsageImportFile(row)
	if errors.Is(err, sql.ErrNoRows) {
		return CodexUsageImportFile{}, ErrNotFound
	}
	return cursor, err
}

func scanCodexUsageImportFile(row rowScanner) (CodexUsageImportFile, error) {
	var cursor CodexUsageImportFile
	if err := row.Scan(
		&cursor.SourceID,
		&cursor.FileKey,
		&cursor.ModifiedUnixMS,
		&cursor.SizeBytes,
		&cursor.ImportedFacts,
		&cursor.InvalidLines,
		&cursor.UnsupportedLines,
		&cursor.ParserRevision,
		&cursor.IdentityRevision,
		&cursor.EventDigest,
		&cursor.UpdatedAtUnixMS,
	); err != nil {
		return CodexUsageImportFile{}, err
	}
	return cursor, nil
}

func (s *Store) CommitCodexUsageImport(ctx context.Context, params CommitCodexUsageImportParams) (UsageInsertResult, error) {
	if err := validateCodexUsageImportBatch(params); err != nil {
		return UsageInsertResult{}, err
	}
	// Facts and their checkpoint advance atomically so a crash cannot skip usage
	// that did not reach durable storage.
	return withUsageTransactionResult(ctx, s, func(txStore *Store) (UsageInsertResult, error) {
		return txStore.commitCodexUsageImport(ctx, params)
	})
}

func (s *Store) commitCodexUsageImport(ctx context.Context, params CommitCodexUsageImportParams) (UsageInsertResult, error) {
	// Generation owns sync-run writes; cursor CAS independently protects the
	// file state read by the current generation.
	source, err := s.reserveCurrentUsageSyncForWrite(ctx, params.File.SourceID, params.Generation)
	if err != nil {
		return UsageInsertResult{}, err
	}
	if source.ProviderID != codexUsageProviderID {
		return UsageInsertResult{}, errors.New("Codex usage import source is invalid")
	}
	result, err := s.insertUsageFacts(ctx, params.Facts)
	if err != nil {
		return UsageInsertResult{}, err
	}
	if err := s.upsertCodexUsageImportFileCAS(ctx, params.File, params.Expected); err != nil {
		return UsageInsertResult{}, err
	}
	return result, nil
}

func validateCodexUsageImportBatch(params CommitCodexUsageImportParams) error {
	// A checkpoint may describe only the same source and exactly the facts in
	// this transaction, otherwise it could skip history that was never stored.
	if params.Generation <= 0 {
		return errors.New("Codex usage sync generation is invalid")
	}
	if err := validateCodexUsageImportFile(params.File); err != nil {
		return err
	}
	for _, fact := range params.Facts {
		if fact.SourceID != params.File.SourceID {
			return errors.New("Codex usage facts must belong to the import source")
		}
	}
	if params.Expected == nil {
		if params.File.ImportedFacts != int64(len(params.Facts)) {
			return errors.New("Codex usage import progress does not match its facts")
		}
		return nil
	}
	if err := validateCodexUsageImportFile(*params.Expected); err != nil {
		return err
	}
	if params.Expected.ImportedFacts < 0 || params.File.ImportedFacts < params.Expected.ImportedFacts ||
		params.File.ImportedFacts-params.Expected.ImportedFacts != int64(len(params.Facts)) {
		return errors.New("Codex usage import progress does not match its facts")
	}
	return nil
}

func (s *Store) upsertCodexUsageImportFileCAS(ctx context.Context, file CodexUsageImportFile, expected *CodexUsageImportFile) error {
	if err := validateCodexUsageImportFile(file); err != nil {
		return err
	}
	source, err := s.getUsageSourceByID(ctx, file.SourceID)
	if err != nil {
		return err
	}
	if source.IdentityRevision != file.IdentityRevision {
		return ErrUsageIdentityRevision
	}

	updatedAt := time.Now().UnixMilli()
	if expected == nil {
		_, err := s.executor().ExecContext(ctx, `
			INSERT INTO codex_usage_import_files (
				source_id, file_key, modified_unix_ms, size_bytes, imported_facts,
				invalid_lines, unsupported_lines, parser_revision, identity_revision,
				event_digest, updated_at_unix_ms
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`,
			file.SourceID,
			file.FileKey,
			file.ModifiedUnixMS,
			file.SizeBytes,
			file.ImportedFacts,
			file.InvalidLines,
			file.UnsupportedLines,
			file.ParserRevision,
			file.IdentityRevision,
			file.EventDigest,
			updatedAt,
		)
		if isSQLiteConstraintError(err) {
			return ErrUsageCursorConflict
		}
		return err
	}
	if expected.SourceID != file.SourceID || expected.FileKey != file.FileKey {
		return ErrUsageCursorConflict
	}
	if updatedAt <= expected.UpdatedAtUnixMS {
		updatedAt = expected.UpdatedAtUnixMS + 1
	}
	update, err := s.executor().ExecContext(ctx, `
		UPDATE codex_usage_import_files
		SET modified_unix_ms = ?, size_bytes = ?, imported_facts = ?,
			invalid_lines = ?, unsupported_lines = ?, parser_revision = ?,
			identity_revision = ?, event_digest = ?, updated_at_unix_ms = ?
		WHERE source_id = ? AND file_key = ?
			AND modified_unix_ms = ? AND size_bytes = ? AND imported_facts = ?
			AND invalid_lines = ? AND unsupported_lines = ? AND parser_revision = ?
			AND identity_revision = ? AND event_digest = ? AND updated_at_unix_ms = ?
	`,
		file.ModifiedUnixMS,
		file.SizeBytes,
		file.ImportedFacts,
		file.InvalidLines,
		file.UnsupportedLines,
		file.ParserRevision,
		file.IdentityRevision,
		file.EventDigest,
		updatedAt,
		file.SourceID,
		file.FileKey,
		expected.ModifiedUnixMS,
		expected.SizeBytes,
		expected.ImportedFacts,
		expected.InvalidLines,
		expected.UnsupportedLines,
		expected.ParserRevision,
		expected.IdentityRevision,
		expected.EventDigest,
		expected.UpdatedAtUnixMS,
	)
	if err != nil {
		return err
	}
	rows, err := update.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return ErrUsageCursorConflict
	}
	return nil
}

func validateCodexUsageImportFile(file CodexUsageImportFile) error {
	if file.SourceID <= 0 || file.FileKey.IsZero() || file.EventDigest.IsZero() ||
		file.ModifiedUnixMS < 0 || file.SizeBytes < 0 || file.ImportedFacts < 0 ||
		file.InvalidLines < 0 || file.UnsupportedLines < 0 || file.ParserRevision <= 0 || file.IdentityRevision <= 0 ||
		file.UpdatedAtUnixMS < 0 {
		return errors.New("Codex usage import file is invalid")
	}
	return nil
}

func (finalization *CodexUsageSyncFinalization) applyUsageSyncFinalization(
	ctx context.Context,
	store *Store,
	source UsageSource,
) (usageSyncFinalizationResult, error) {
	if err := finalization.validateUsageSyncFinalization(); err != nil {
		return usageSyncFinalizationResult{}, err
	}
	if source.ProviderID != codexUsageProviderID {
		return usageSyncFinalizationResult{}, errors.New("Codex usage sync finalization is invalid")
	}

	discovered := make(map[UsageKey]struct{}, len(finalization.DiscoveredFileKeys))
	for _, key := range finalization.DiscoveredFileKeys {
		discovered[key] = struct{}{}
	}
	rows, err := store.executor().QueryContext(ctx, `
		SELECT file_key FROM codex_usage_import_files WHERE source_id = ?
	`, source.ID)
	if err != nil {
		return usageSyncFinalizationResult{}, err
	}
	var staleKeys []UsageKey
	for rows.Next() {
		var fileKey UsageKey
		if err := rows.Scan(&fileKey); err != nil {
			if closeErr := rows.Close(); closeErr != nil {
				return usageSyncFinalizationResult{}, closeErr
			}
			return usageSyncFinalizationResult{}, err
		}
		if _, ok := discovered[fileKey]; !ok {
			staleKeys = append(staleKeys, fileKey)
		}
	}
	if err := rows.Close(); err != nil {
		return usageSyncFinalizationResult{}, err
	}
	for _, fileKey := range staleKeys {
		if _, err := store.executor().ExecContext(ctx, `
			DELETE FROM codex_usage_import_files WHERE source_id = ? AND file_key = ?
		`, source.ID, fileKey); err != nil {
			return usageSyncFinalizationResult{}, err
		}
	}

	var result usageSyncFinalizationResult
	if err := store.executor().QueryRowContext(ctx, `
		SELECT COUNT(1), COALESCE(SUM(invalid_lines), 0), COALESCE(SUM(unsupported_lines), 0)
		FROM codex_usage_import_files
		WHERE source_id = ?
	`, source.ID).Scan(&result.trackedUnits, &result.invalidRecords, &result.unsupportedRecords); err != nil {
		return usageSyncFinalizationResult{}, err
	}
	return result, nil
}
