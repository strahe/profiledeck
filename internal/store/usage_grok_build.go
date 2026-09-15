package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

type GrokBuildUsageImportFile struct {
	SourceID              int64
	FileKey               UsageKey
	ModifiedUnixMS        int64
	SizeBytes             int64
	ImportedFacts         int64
	InvalidLines          int64
	UnsupportedLines      int64
	ParserRevision        int64
	IdentityRevision      int64
	EventDigest           UsageKey
	CheckpointRevision    int64
	ProcessedBytes        int64
	MetadataDigest        UsageKey
	FileIdentityDigest    UsageKey
	BoundaryDigest        UsageKey
	CheckpointEventDigest UsageKey
	ParserStateJSON       string
	UpdatedAtUnixMS       int64
}

type CommitGrokBuildUsageImportParams struct {
	ProviderID          string
	Generation          int64
	Facts               []CreateUsageFactParams
	File                GrokBuildUsageImportFile
	Expected            *GrokBuildUsageImportFile
	ReplayExistingFacts bool
}

type GrokBuildUsageSyncFinalization struct {
	ProviderID         string
	DiscoveredFileKeys []UsageKey
}

func (finalization *GrokBuildUsageSyncFinalization) validateUsageSyncFinalization() error {
	if finalization == nil || strings.TrimSpace(finalization.ProviderID) == "" {
		return errors.New("invalid Grok Build usage sync finalization")
	}
	for _, fileKey := range finalization.DiscoveredFileKeys {
		if fileKey.IsZero() {
			return errors.New("invalid Grok Build usage sync finalization")
		}
	}
	return nil
}

// ValidateGrokBuildUsageImportIdentity rejects checkpoints produced under
// another fact identity revision.
func (s *Store) ValidateGrokBuildUsageImportIdentity(
	ctx context.Context,
	sourceID int64,
	providerID string,
	identityRevision int64,
) error {
	providerID = strings.TrimSpace(providerID)
	if sourceID <= 0 || providerID == "" || identityRevision <= 0 {
		return errors.New("invalid Grok Build usage import identity")
	}
	source, err := s.getUsageSourceByID(ctx, sourceID)
	if err != nil {
		return err
	}
	if source.ProviderID != providerID {
		return errors.New("invalid Grok Build usage import source")
	}
	if source.IdentityRevision != identityRevision {
		return ErrUsageIdentityRevision
	}
	var incompatibleFiles int
	if err := s.executor().QueryRowContext(ctx, `
		SELECT COUNT(1)
		FROM grok_build_usage_import_files
		WHERE source_id = ? AND identity_revision <> ?
	`, source.ID, identityRevision).Scan(&incompatibleFiles); err != nil {
		return err
	}
	if incompatibleFiles > 0 {
		return ErrUsageIdentityRevision
	}
	return nil
}

func (s *Store) GetGrokBuildUsageImportFile(
	ctx context.Context,
	sourceID int64,
	fileKey UsageKey,
) (GrokBuildUsageImportFile, error) {
	row := s.executor().QueryRowContext(ctx, `
		SELECT source_id, file_key, modified_unix_ms, size_bytes, imported_facts,
			invalid_lines, unsupported_lines, parser_revision, identity_revision,
			event_digest, checkpoint_revision, processed_bytes, metadata_digest,
			file_identity_digest, boundary_digest, checkpoint_event_digest,
			parser_state_json, updated_at_unix_ms
		FROM grok_build_usage_import_files
		WHERE source_id = ? AND file_key = ?
	`, sourceID, fileKey)
	cursor, err := scanGrokBuildUsageImportFile(row)
	if errors.Is(err, sql.ErrNoRows) {
		return GrokBuildUsageImportFile{}, ErrNotFound
	}
	return cursor, err
}

func (s *Store) ListGrokBuildUsageImportFiles(
	ctx context.Context,
	sourceID int64,
) ([]GrokBuildUsageImportFile, error) {
	if sourceID <= 0 {
		return nil, errors.New("invalid Grok Build usage import query")
	}
	rows, err := s.executor().QueryContext(ctx, `
		SELECT source_id, file_key, modified_unix_ms, size_bytes, imported_facts,
			invalid_lines, unsupported_lines, parser_revision, identity_revision,
			event_digest, checkpoint_revision, processed_bytes, metadata_digest,
			file_identity_digest, boundary_digest, checkpoint_event_digest,
			parser_state_json, updated_at_unix_ms
		FROM grok_build_usage_import_files
		WHERE source_id = ?
		ORDER BY file_key
	`, sourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	files := make([]GrokBuildUsageImportFile, 0)
	for rows.Next() {
		file, err := scanGrokBuildUsageImportFile(rows)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, rows.Err()
}

func scanGrokBuildUsageImportFile(row rowScanner) (GrokBuildUsageImportFile, error) {
	var cursor GrokBuildUsageImportFile
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
		&cursor.CheckpointRevision,
		&cursor.ProcessedBytes,
		&cursor.MetadataDigest,
		&cursor.FileIdentityDigest,
		&cursor.BoundaryDigest,
		&cursor.CheckpointEventDigest,
		&cursor.ParserStateJSON,
		&cursor.UpdatedAtUnixMS,
	); err != nil {
		return GrokBuildUsageImportFile{}, err
	}
	return cursor, nil
}

func (s *Store) CommitGrokBuildUsageImport(
	ctx context.Context,
	params CommitGrokBuildUsageImportParams,
) (UsageInsertResult, error) {
	if err := validateGrokBuildUsageImportBatch(params); err != nil {
		return UsageInsertResult{}, err
	}
	// Facts and the file checkpoint share one transaction so quarantined or
	// interrupted files never advance past facts that were not stored.
	return withUsageTransactionResult(ctx, s, func(txStore *Store) (UsageInsertResult, error) {
		return txStore.commitGrokBuildUsageImport(ctx, params)
	})
}

func (s *Store) commitGrokBuildUsageImport(
	ctx context.Context,
	params CommitGrokBuildUsageImportParams,
) (UsageInsertResult, error) {
	source, err := s.reserveCurrentUsageSyncForWrite(ctx, params.File.SourceID, params.Generation)
	if err != nil {
		return UsageInsertResult{}, err
	}
	if source.ProviderID != params.ProviderID {
		return UsageInsertResult{}, errors.New("invalid Grok Build usage import source")
	}
	result, err := s.insertUsageFactsWithSessionPolicy(
		ctx,
		params.Facts,
		usageFactSessionCanonicalAlias,
	)
	if err != nil {
		return UsageInsertResult{}, err
	}
	if err := s.upsertGrokBuildUsageImportFileCAS(ctx, params.File, params.Expected); err != nil {
		return UsageInsertResult{}, err
	}
	if err := s.deleteUsageImportObservation(ctx, params.File.SourceID, params.File.FileKey); err != nil {
		return UsageInsertResult{}, err
	}
	return result, nil
}

func validateGrokBuildUsageImportBatch(params CommitGrokBuildUsageImportParams) error {
	if strings.TrimSpace(params.ProviderID) == "" || params.Generation <= 0 {
		return errors.New("invalid Grok Build usage sync generation")
	}
	if err := validateGrokBuildUsageImportFile(params.File); err != nil {
		return err
	}
	for _, fact := range params.Facts {
		if fact.SourceID != params.File.SourceID {
			return errors.New("usage facts must belong to the Grok Build import source")
		}
		// Reject direct upstream identifiers at the persistence boundary so a
		// future importer cannot accidentally bypass the session privacy contract.
		if !validGrokBuildDerivedSessionKey(fact.SessionKey) {
			return errors.New("usage sessions for Grok Build must use derived identifiers")
		}
	}
	if params.Expected == nil {
		if params.File.ImportedFacts != int64(len(params.Facts)) {
			return errors.New("usage import progress does not match its Grok Build facts")
		}
		return nil
	}
	if err := validateGrokBuildUsageImportFile(*params.Expected); err != nil {
		return err
	}
	if params.ReplayExistingFacts {
		if params.File.ParserRevision <= params.Expected.ParserRevision ||
			params.File.ImportedFacts != int64(len(params.Facts)) {
			return errors.New("invalid Grok Build usage parser replay")
		}
		return nil
	}
	if params.File.ImportedFacts < params.Expected.ImportedFacts ||
		params.File.ImportedFacts-params.Expected.ImportedFacts != int64(len(params.Facts)) {
		return errors.New("usage import progress does not match its Grok Build facts")
	}
	return nil
}

func validGrokBuildDerivedSessionKey(value string) bool {
	const prefix = "derived-"
	if len(value) != len(prefix)+hex.EncodedLen(sha256.Size) ||
		!strings.HasPrefix(value, prefix) {
		return false
	}
	encoded := strings.TrimPrefix(value, prefix)
	if encoded != strings.ToLower(encoded) {
		return false
	}
	_, err := hex.DecodeString(encoded)
	return err == nil
}

func (s *Store) upsertGrokBuildUsageImportFileCAS(
	ctx context.Context,
	file GrokBuildUsageImportFile,
	expected *GrokBuildUsageImportFile,
) error {
	if err := validateGrokBuildUsageImportFile(file); err != nil {
		return err
	}
	if file.CheckpointRevision == 0 && strings.TrimSpace(file.ParserStateJSON) == "" {
		file.ParserStateJSON = "{}"
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
			INSERT INTO grok_build_usage_import_files (
				source_id, file_key, modified_unix_ms, size_bytes, imported_facts,
				invalid_lines, unsupported_lines, parser_revision, identity_revision,
				event_digest, checkpoint_revision, processed_bytes, metadata_digest,
				file_identity_digest, boundary_digest, checkpoint_event_digest,
				parser_state_json, updated_at_unix_ms
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
			file.CheckpointRevision,
			file.ProcessedBytes,
			file.MetadataDigest,
			file.FileIdentityDigest,
			file.BoundaryDigest,
			file.CheckpointEventDigest,
			file.ParserStateJSON,
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
		UPDATE grok_build_usage_import_files
		SET modified_unix_ms = ?, size_bytes = ?, imported_facts = ?,
			invalid_lines = ?, unsupported_lines = ?, parser_revision = ?,
			identity_revision = ?, event_digest = ?, checkpoint_revision = ?,
			processed_bytes = ?, metadata_digest = ?, file_identity_digest = ?,
			boundary_digest = ?, checkpoint_event_digest = ?, parser_state_json = ?,
			updated_at_unix_ms = ?
		WHERE source_id = ? AND file_key = ?
			AND modified_unix_ms = ? AND size_bytes = ? AND imported_facts = ?
			AND invalid_lines = ? AND unsupported_lines = ? AND parser_revision = ?
			AND identity_revision = ? AND event_digest = ? AND checkpoint_revision = ?
			AND processed_bytes = ? AND metadata_digest = ? AND file_identity_digest = ?
			AND boundary_digest = ? AND checkpoint_event_digest = ? AND parser_state_json = ?
			AND updated_at_unix_ms = ?
	`,
		file.ModifiedUnixMS,
		file.SizeBytes,
		file.ImportedFacts,
		file.InvalidLines,
		file.UnsupportedLines,
		file.ParserRevision,
		file.IdentityRevision,
		file.EventDigest,
		file.CheckpointRevision,
		file.ProcessedBytes,
		file.MetadataDigest,
		file.FileIdentityDigest,
		file.BoundaryDigest,
		file.CheckpointEventDigest,
		file.ParserStateJSON,
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
		expected.CheckpointRevision,
		expected.ProcessedBytes,
		expected.MetadataDigest,
		expected.FileIdentityDigest,
		expected.BoundaryDigest,
		expected.CheckpointEventDigest,
		expected.ParserStateJSON,
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

func validateGrokBuildUsageImportFile(file GrokBuildUsageImportFile) error {
	if file.SourceID <= 0 || file.FileKey.IsZero() || file.EventDigest.IsZero() ||
		file.ModifiedUnixMS < 0 || file.SizeBytes < 0 || file.ImportedFacts < 0 ||
		file.InvalidLines < 0 || file.UnsupportedLines < 0 ||
		file.ParserRevision <= 0 || file.IdentityRevision <= 0 ||
		file.UpdatedAtUnixMS < 0 || file.CheckpointRevision < 0 ||
		file.ProcessedBytes < 0 || file.ProcessedBytes > file.SizeBytes {
		return errors.New("invalid Grok Build usage import file")
	}
	if file.CheckpointRevision == 0 {
		if file.ProcessedBytes != 0 || !file.MetadataDigest.IsZero() ||
			!file.FileIdentityDigest.IsZero() || !file.BoundaryDigest.IsZero() ||
			!file.CheckpointEventDigest.IsZero() || file.ParserStateJSON != "" && file.ParserStateJSON != "{}" {
			return errors.New("invalid Grok Build legacy checkpoint")
		}
		return nil
	}
	if file.CheckpointRevision != 1 || file.MetadataDigest.IsZero() ||
		file.BoundaryDigest.IsZero() || file.CheckpointEventDigest.IsZero() ||
		file.ParserStateJSON != "{}" {
		return errors.New("invalid Grok Build checkpoint")
	}
	return nil
}

func (finalization *GrokBuildUsageSyncFinalization) applyUsageSyncFinalization(
	ctx context.Context,
	store *Store,
	source UsageSource,
) (usageSyncFinalizationResult, error) {
	if err := finalization.validateUsageSyncFinalization(); err != nil {
		return usageSyncFinalizationResult{}, err
	}
	if source.ProviderID != finalization.ProviderID {
		return usageSyncFinalizationResult{}, errors.New("invalid Grok Build usage sync finalization")
	}

	discovered := make(map[UsageKey]struct{}, len(finalization.DiscoveredFileKeys))
	for _, key := range finalization.DiscoveredFileKeys {
		discovered[key] = struct{}{}
	}
	rows, err := store.executor().QueryContext(ctx, `
		SELECT file_key FROM grok_build_usage_import_files WHERE source_id = ?
	`, source.ID)
	if err != nil {
		return usageSyncFinalizationResult{}, err
	}
	var staleKeys []UsageKey
	for rows.Next() {
		var fileKey UsageKey
		if err := rows.Scan(&fileKey); err != nil {
			_ = rows.Close()
			return usageSyncFinalizationResult{}, err
		}
		if _, ok := discovered[fileKey]; !ok {
			staleKeys = append(staleKeys, fileKey)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return usageSyncFinalizationResult{}, err
	}
	if err := rows.Close(); err != nil {
		return usageSyncFinalizationResult{}, err
	}
	for _, fileKey := range staleKeys {
		if _, err := store.executor().ExecContext(ctx, `
			DELETE FROM grok_build_usage_import_files WHERE source_id = ? AND file_key = ?
		`, source.ID, fileKey); err != nil {
			return usageSyncFinalizationResult{}, err
		}
	}
	if err := store.deleteMissingUsageImportObservations(ctx, source.ID, discovered); err != nil {
		return usageSyncFinalizationResult{}, err
	}

	var result usageSyncFinalizationResult
	if err := store.executor().QueryRowContext(ctx, `
		SELECT COUNT(1), COALESCE(SUM(invalid_lines), 0), COALESCE(SUM(unsupported_lines), 0)
		FROM grok_build_usage_import_files
		WHERE source_id = ?
	`, source.ID).Scan(
		&result.trackedUnits,
		&result.invalidRecords,
		&result.unsupportedRecords,
	); err != nil {
		return usageSyncFinalizationResult{}, err
	}
	return result, nil
}
