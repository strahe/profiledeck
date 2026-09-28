package usage

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/strahe/profiledeck/internal/apperror"
	claudeconfig "github.com/strahe/profiledeck/internal/claudecode/config"
	"github.com/strahe/profiledeck/internal/store"
)

const (
	claudeCodeFileUnavailableMessage = "A Claude Code session file could not be imported. It will be checked again after the file changes or when you sync manually."
	claudeCodeHistoryChangedMessage  = "A Claude Code session file changed before the saved import point. It will be checked again after the file changes or when you sync manually."
	claudeCodeFactConflictMessage    = "A Claude Code session file conflicts with previously imported usage. It will be checked again after the file changes or when you sync manually."
)

type claudeCodeIntegration struct {
	claudeDir   string
	provisioner ProviderProvisioner
}

func NewClaudeCodeIntegrationWithProvisioner(
	claudeDir string,
	provisioner ProviderProvisioner,
) Integration {
	return claudeCodeIntegration{claudeDir: claudeDir, provisioner: provisioner}
}

func (claudeCodeIntegration) ProviderID() string {
	return claudeconfig.ProviderID
}

func (claudeCodeIntegration) SourceIDs() []string {
	return []string{SourceClaudeCodeSessionJSONL}
}

func (claudeCodeIntegration) PricingInfo() UsagePricingInfo {
	return UsagePricingInfo{
		Basis:               "anthropic-standard-api",
		SourceURL:           "https://platform.claude.com/docs/en/about-claude/pricing",
		VerifiedAt:          "2026-09-28",
		HistoricalRepricing: false,
	}
}

func (integration claudeCodeIntegration) Sync(
	ctx context.Context,
	stores store.Factory,
	options SyncOptions,
) (SyncOutcome, error) {
	_, _, _, priceBackfill, noWorkResult, work, err := integration.preflight(ctx, stores, options)
	if err != nil {
		return SyncOutcome{}, err
	}
	if !work {
		return SyncOutcome{Result: noWorkResult}, nil
	}
	if options.OnWorkDetected != nil {
		options.OnWorkDetected()
	}
	db, err := stores.OpenHealthy(ctx, false)
	if err != nil {
		return SyncOutcome{}, err
	}
	defer db.Close()
	source, err := beginClaudeCodeUsageSync(ctx, db, integration.provisioner, options.ProvisionMode)
	if err != nil {
		return SyncOutcome{}, err
	}
	files, cursors, observed, err := integration.loadSyncSnapshot(ctx, db, source.ID)
	if err != nil {
		return SyncOutcome{}, err
	}
	result := UsageSyncResult{
		ProviderID: claudeconfig.ProviderID,
		Source:     SourceClaudeCodeSessionJSONL,
	}
	discoveredFileKeys := make([]store.UsageKey, 0, len(files))
	observations := make([]store.UsageImportObservation, 0)
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return SyncOutcome{}, apperror.Wrap(
				apperror.UsageImportFailed,
				"usage import canceled",
				err,
			)
		}
		discoveredFileKeys = append(discoveredFileKeys, file.SourceKey)
		result.ScannedFiles++

		cursor, hasCursor := cursors[file.SourceKey]
		if hasCursor && claudeCodeCursorMatchesFile(cursor, file) {
			result.SkippedUnchangedFiles++
			continue
		}
		if observation, ok := observed[file.SourceKey]; ok &&
			observation.MetadataDigest == file.MetadataDigest &&
			observation.ParserRevision == ClaudeCodeUsageParserRevision &&
			!options.ForceObservedRetry {
			result.SkippedUnchangedFiles++
			result.Errors = append(result.Errors, claudeCodeObservationError(observation.Status))
			continue
		}
		if hasCursor && claudeCodeFileIsShorterThanCheckpoint(cursor, file) {
			result.Errors = append(result.Errors, claudeCodeObservationError(store.UsageImportObservationHistoryChanged))
			observations = append(observations, newUsageObservation(source.ID, file, ClaudeCodeUsageParserRevision, store.UsageImportObservationHistoryChanged))
			continue
		}

		parsed, fullParse, err := parseClaudeCodeUsageChange(ctx, file, cursor, hasCursor, options)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return SyncOutcome{}, apperror.Wrap(
					apperror.UsageImportFailed,
					"usage import canceled",
					ctxErr,
				)
			}
			result.InvalidLines++
			result.Errors = append(result.Errors, claudeCodeObservationError(store.UsageImportObservationUnavailable))
			observations = append(observations, newUsageObservation(source.ID, file, ClaudeCodeUsageParserRevision, store.UsageImportObservationUnavailable))
			continue
		}
		eventsToStore := parsed.Events
		replayExistingFacts := false
		importedFacts := int64(len(parsed.Events))
		invalidLines := parsed.InvalidLines
		unsupportedLines := parsed.UnsupportedLines
		if hasCursor {
			if fullParse && !claudeCodeCheckpointPrefixMatches(parsed.Events, cursor) {
				result.Errors = append(result.Errors, claudeCodeObservationError(store.UsageImportObservationHistoryChanged))
				observations = append(observations, newUsageObservation(source.ID, file, ClaudeCodeUsageParserRevision, store.UsageImportObservationHistoryChanged))
				continue
			}
			if fullParse {
				eventsToStore = parsed.Events
				replayExistingFacts = true
				importedFacts = int64(len(parsed.Events))
			} else {
				importedFacts = cursor.ImportedFacts + int64(len(parsed.Events))
				invalidLines += cursor.InvalidLines
				unsupportedLines += cursor.UnsupportedLines
				previousReasons, err := store.DecodeClaudeCodeUsageInvalidReasons(cursor.ParserStateJSON, cursor.InvalidLines)
				if err != nil {
					return SyncOutcome{}, err
				}
				for reason, count := range previousReasons {
					parsed.ClaudeInvalidReasons[reason] += count
				}
			}
		}

		parserState, err := json.Marshal(parsed.ClaudeInvalidReasons)
		if err != nil {
			return SyncOutcome{}, err
		}
		desired := store.ClaudeCodeUsageImportFile{
			SourceID:              source.ID,
			FileKey:               file.SourceKey,
			ModifiedUnixMS:        file.ModifiedUnixMS,
			SizeBytes:             file.SizeBytes,
			ImportedFacts:         importedFacts,
			InvalidLines:          invalidLines,
			UnsupportedLines:      unsupportedLines,
			ParserRevision:        ClaudeCodeUsageParserRevision,
			IdentityRevision:      ClaudeCodeUsageIdentityRevision,
			EventDigest:           parsed.CheckpointEventDigest,
			CheckpointRevision:    usageCheckpointRevision,
			ProcessedBytes:        parsed.ProcessedBytes,
			MetadataDigest:        file.MetadataDigest,
			FileIdentityDigest:    file.FileIdentityDigest,
			BoundaryDigest:        parsed.BoundaryDigest,
			CheckpointEventDigest: parsed.CheckpointEventDigest,
			ParserStateJSON:       string(parserState),
		}
		var expected *store.ClaudeCodeUsageImportFile
		if hasCursor {
			expected = &cursor
		}
		insertResult, err := db.CommitClaudeCodeUsageImport(
			ctx,
			store.CommitClaudeCodeUsageImportParams{
				ProviderID:          claudeconfig.ProviderID,
				Generation:          source.SyncGeneration,
				Facts:               usageEventsToFactParams(source.ID, eventsToStore),
				Price:               claudeCodePricer(pricingSnapshot(ctx)),
				File:                desired,
				Expected:            expected,
				ReplayExistingFacts: replayExistingFacts,
			},
		)
		if errors.Is(err, store.ErrUsageCursorConflict) {
			current, readErr := db.GetClaudeCodeUsageImportFile(ctx, source.ID, file.SourceKey)
			if readErr == nil && sameClaudeCodeUsageImportProgress(current, desired) {
				result.SkippedDuplicateEvents += int64(len(eventsToStore))
				result.UnsupportedLines += unsupportedLines
				continue
			}
			if readErr != nil && !errors.Is(readErr, store.ErrNotFound) {
				return SyncOutcome{}, apperror.Wrap(
					apperror.UsageImportFailed,
					"failed to inspect concurrent usage sync",
					readErr,
				)
			}
			return SyncOutcome{}, err
		}
		if errors.Is(err, store.ErrUsageFactConflict) {
			result.InvalidLines++
			result.Errors = append(result.Errors, claudeCodeObservationError(store.UsageImportObservationFactConflict))
			observations = append(observations, newUsageObservation(source.ID, file, ClaudeCodeUsageParserRevision, store.UsageImportObservationFactConflict))
			continue
		}
		if err != nil {
			return SyncOutcome{}, err
		}
		result.ImportedEvents += int64(insertResult.Inserted)
		result.UpdatedEvents += int64(insertResult.Updated)
		result.SkippedDuplicateEvents += int64(insertResult.Duplicates)
		result.InvalidLines += invalidLines
		result.UnsupportedLines += unsupportedLines
	}

	if err := db.CompleteUsageSync(ctx, store.CompleteUsageSyncParams{
		SourceID:          source.ID,
		Generation:        source.SyncGeneration,
		CompletedAtUnixMS: time.Now().UnixMilli(),
		Finalization: &store.ClaudeCodeUsageSyncFinalization{
			ProviderID:         claudeconfig.ProviderID,
			DiscoveredFileKeys: discoveredFileKeys,
		},
		Observations: observations,
	}); err != nil {
		return SyncOutcome{}, err
	}
	if options.ProvisionMode == SyncProvisionProvider || priceBackfill {
		if err := backfillClaudeCodeUsageCosts(ctx, db); err != nil {
			return SyncOutcome{}, err
		}
	}

	return SyncOutcome{Result: result, Performed: true}, nil
}

func (integration claudeCodeIntegration) loadSyncSnapshot(
	ctx context.Context,
	db *store.Store,
	sourceID int64,
) ([]SourceFile, map[store.UsageKey]store.ClaudeCodeUsageImportFile, map[store.UsageKey]store.UsageImportObservation, error) {
	files, err := ListClaudeCodeSessionFilesContext(ctx, integration.claudeDir)
	if err != nil {
		return nil, nil, nil, apperror.Wrap(apperror.UsageImportFailed, "failed to list Claude Code session files", err)
	}
	cursorRows, err := db.ListClaudeCodeUsageImportFiles(ctx, sourceID)
	if err != nil {
		return nil, nil, nil, err
	}
	observationRows, err := db.ListUsageImportObservations(ctx, sourceID)
	if err != nil {
		return nil, nil, nil, err
	}
	return files, claudeCodeCursorMap(cursorRows), observationMap(observationRows), nil
}

func (integration claudeCodeIntegration) preflight(
	ctx context.Context,
	stores store.Factory,
	options SyncOptions,
) (
	[]SourceFile,
	map[store.UsageKey]store.ClaudeCodeUsageImportFile,
	map[store.UsageKey]store.UsageImportObservation,
	bool,
	UsageSyncResult,
	bool,
	error,
) {
	if options.ProvisionMode == SyncProvisionProvider {
		files, err := ListClaudeCodeSessionFilesContext(ctx, integration.claudeDir)
		if err != nil {
			return nil, nil, nil, false, UsageSyncResult{}, false, apperror.Wrap(
				apperror.UsageImportFailed,
				"failed to list Claude Code session files",
				err,
			)
		}
		result := UsageSyncResult{
			ProviderID:   claudeconfig.ProviderID,
			Source:       SourceClaudeCodeSessionJSONL,
			ScannedFiles: int64(len(files)),
		}
		return files, nil, nil, true, result, true, nil
	}
	db, err := stores.OpenHealthy(ctx, true)
	if err != nil {
		return nil, nil, nil, false, UsageSyncResult{}, false, err
	}
	defer db.Close()
	if integration.provisioner == nil {
		return nil, nil, nil, false, UsageSyncResult{}, false,
			errors.New("usage Provider provisioner for Claude Code is required")
	}
	if err := integration.provisioner.Ensure(ctx, db, SyncExistingProvider); err != nil {
		return nil, nil, nil, false, UsageSyncResult{}, false, err
	}
	source, err := db.GetUsageSource(ctx, claudeconfig.ProviderID, SourceClaudeCodeSessionJSONL)
	if errors.Is(err, store.ErrNotFound) {
		files, listErr := ListClaudeCodeSessionFilesContext(ctx, integration.claudeDir)
		if listErr != nil {
			return nil, nil, nil, false, UsageSyncResult{}, false, apperror.Wrap(
				apperror.UsageImportFailed,
				"failed to list Claude Code session files",
				listErr,
			)
		}
		result := UsageSyncResult{
			ProviderID:   claudeconfig.ProviderID,
			Source:       SourceClaudeCodeSessionJSONL,
			ScannedFiles: int64(len(files)),
		}
		return files, nil, nil, false, result, true, nil
	}
	if err != nil {
		return nil, nil, nil, false, UsageSyncResult{}, false, err
	}
	if source.IdentityRevision != ClaudeCodeUsageIdentityRevision {
		return nil, nil, nil, false, UsageSyncResult{}, false, store.ErrUsageIdentityRevision
	}
	cursorRows, err := db.ListClaudeCodeUsageImportFiles(ctx, source.ID)
	if err != nil {
		return nil, nil, nil, false, UsageSyncResult{}, false, err
	}
	observationRows, err := db.ListUsageImportObservations(ctx, source.ID)
	if err != nil {
		return nil, nil, nil, false, UsageSyncResult{}, false, err
	}
	unknownModels, err := db.ListUnknownUsageCostModels(ctx, claudeconfig.ProviderID)
	if err != nil {
		return nil, nil, nil, false, UsageSyncResult{}, false, err
	}
	files, err := ListClaudeCodeSessionFilesContext(ctx, integration.claudeDir)
	if err != nil {
		return nil, nil, nil, false, UsageSyncResult{}, false, apperror.Wrap(
			apperror.UsageImportFailed,
			"failed to list Claude Code session files",
			err,
		)
	}
	result := UsageSyncResult{
		ProviderID:   claudeconfig.ProviderID,
		Source:       SourceClaudeCodeSessionJSONL,
		ScannedFiles: int64(len(files)),
	}
	cursors := claudeCodeCursorMap(cursorRows)
	observations := observationMap(observationRows)
	catalog := pricingSnapshot(ctx)
	priceBackfill, err := hasPriceableUnknownUsageModel(ctx, db, unknownModels, catalog, claudeconfig.ProviderID)
	if err != nil {
		return nil, nil, nil, false, UsageSyncResult{}, false, err
	}
	work := source.SyncGeneration != source.CompletedGeneration || priceBackfill
	discovered := make(map[store.UsageKey]struct{}, len(files))
	for _, file := range files {
		discovered[file.SourceKey] = struct{}{}
		if cursor, ok := cursors[file.SourceKey]; ok {
			if cursor.IdentityRevision != ClaudeCodeUsageIdentityRevision {
				return nil, nil, nil, false, UsageSyncResult{}, false, store.ErrUsageIdentityRevision
			}
			if claudeCodeCursorMatchesFile(cursor, file) {
				result.SkippedUnchangedFiles++
				continue
			}
		}
		if observation, ok := observations[file.SourceKey]; ok &&
			observation.MetadataDigest == file.MetadataDigest &&
			observation.ParserRevision == ClaudeCodeUsageParserRevision &&
			!options.ForceObservedRetry {
			result.SkippedUnchangedFiles++
			result.Errors = append(result.Errors, claudeCodeObservationError(observation.Status))
			continue
		}
		work = true
	}
	for key := range cursors {
		if _, ok := discovered[key]; !ok {
			work = true
		}
	}
	for key := range observations {
		if _, ok := discovered[key]; !ok {
			work = true
		}
	}
	return files, cursors, observations, priceBackfill, result, work, nil
}

func parseClaudeCodeUsageChange(
	ctx context.Context,
	file SourceFile,
	cursor store.ClaudeCodeUsageImportFile,
	hasCursor bool,
	options SyncOptions,
) (checkpointParseResult, bool, error) {
	if hasCursor && cursor.CheckpointRevision == usageCheckpointRevision &&
		cursor.ParserRevision == ClaudeCodeUsageParserRevision &&
		cursor.IdentityRevision == ClaudeCodeUsageIdentityRevision &&
		!cursor.FileIdentityDigest.IsZero() &&
		cursor.FileIdentityDigest == file.FileIdentityDigest &&
		file.SizeBytes > cursor.SizeBytes {
		parsed, parseErr := parseClaudeCodeCheckpointFile(
			ctx,
			file,
			cursor.ProcessedBytes,
			cursor.CheckpointEventDigest,
			cursor.BoundaryDigest,
			options.fileSystem,
			options.Observer,
		)
		if parseErr == nil {
			return parsed, false, nil
		}
		if !errors.Is(parseErr, errUsageBoundaryChanged) {
			return checkpointParseResult{}, false, parseErr
		}
	}
	parsed, err := parseClaudeCodeCheckpointFile(
		ctx,
		file,
		0,
		store.UsageKey{},
		store.UsageKey{},
		options.fileSystem,
		options.Observer,
	)
	return parsed, true, err
}

func claudeCodeCheckpointPrefixMatches(events []Event, cursor store.ClaudeCodeUsageImportFile) bool {
	if cursor.ImportedFacts < 0 || cursor.ImportedFacts > int64(len(events)) ||
		cursor.IdentityRevision != ClaudeCodeUsageIdentityRevision {
		return false
	}
	return cursor.CheckpointRevision == usageCheckpointRevision &&
		checkpointEventDigest(claudeconfig.ProviderID, events, cursor.ImportedFacts) == cursor.CheckpointEventDigest
}

func claudeCodeCursorMatchesFile(cursor store.ClaudeCodeUsageImportFile, file SourceFile) bool {
	if cursor.ParserRevision != ClaudeCodeUsageParserRevision ||
		cursor.IdentityRevision != ClaudeCodeUsageIdentityRevision {
		return false
	}
	if cursor.CheckpointRevision == usageCheckpointRevision {
		return cursor.MetadataDigest == file.MetadataDigest
	}
	return cursor.CheckpointRevision == 0 &&
		cursor.ModifiedUnixMS == file.ModifiedUnixMS && cursor.SizeBytes == file.SizeBytes
}

func claudeCodeFileIsShorterThanCheckpoint(cursor store.ClaudeCodeUsageImportFile, file SourceFile) bool {
	if cursor.CheckpointRevision == usageCheckpointRevision {
		return file.SizeBytes < cursor.ProcessedBytes
	}
	return file.SizeBytes < cursor.SizeBytes
}

func claudeCodeCursorMap(rows []store.ClaudeCodeUsageImportFile) map[store.UsageKey]store.ClaudeCodeUsageImportFile {
	result := make(map[store.UsageKey]store.ClaudeCodeUsageImportFile, len(rows))
	for _, row := range rows {
		result[row.FileKey] = row
	}
	return result
}

func claudeCodeObservationError(status store.UsageImportObservationStatus) UsageImportError {
	message := claudeCodeFileUnavailableMessage
	switch status {
	case store.UsageImportObservationHistoryChanged:
		message = claudeCodeHistoryChangedMessage
	case store.UsageImportObservationFactConflict:
		message = claudeCodeFactConflictMessage
	}
	return UsageImportError{Message: message}
}

func beginClaudeCodeUsageSync(
	ctx context.Context,
	db *store.Store,
	provisioner ProviderProvisioner,
	mode SyncProvisionMode,
) (store.UsageSource, error) {
	var source store.UsageSource
	err := db.WithTransaction(ctx, func(txStore *store.Store) error {
		if provisioner == nil {
			return errors.New("usage Provider provisioner for Claude Code is required")
		}
		if err := provisioner.Ensure(ctx, txStore, mode); err != nil {
			return err
		}
		var err error
		source, err = txStore.BeginUsageSync(
			ctx,
			claudeconfig.ProviderID,
			SourceClaudeCodeSessionJSONL,
			ClaudeCodeUsageIdentityRevision,
		)
		if err != nil {
			return err
		}
		return txStore.ValidateClaudeCodeUsageImportIdentity(
			ctx,
			source.ID,
			claudeconfig.ProviderID,
			ClaudeCodeUsageIdentityRevision,
		)
	})
	return source, err
}

func sameClaudeCodeUsageImportProgress(
	left store.ClaudeCodeUsageImportFile,
	right store.ClaudeCodeUsageImportFile,
) bool {
	return left.SourceID == right.SourceID &&
		left.FileKey == right.FileKey &&
		left.ModifiedUnixMS == right.ModifiedUnixMS &&
		left.SizeBytes == right.SizeBytes &&
		left.ImportedFacts == right.ImportedFacts &&
		left.InvalidLines == right.InvalidLines &&
		left.UnsupportedLines == right.UnsupportedLines &&
		left.ParserRevision == right.ParserRevision &&
		left.IdentityRevision == right.IdentityRevision &&
		left.EventDigest == right.EventDigest &&
		left.CheckpointRevision == right.CheckpointRevision &&
		left.ProcessedBytes == right.ProcessedBytes &&
		left.MetadataDigest == right.MetadataDigest &&
		left.FileIdentityDigest == right.FileIdentityDigest &&
		left.BoundaryDigest == right.BoundaryDigest &&
		left.CheckpointEventDigest == right.CheckpointEventDigest &&
		left.ParserStateJSON == right.ParserStateJSON
}
