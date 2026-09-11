package usage

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/strahe/profiledeck/internal/apperror"
	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	codexpreset "github.com/strahe/profiledeck/internal/codex/preset"
	"github.com/strahe/profiledeck/internal/store"
)

const codexHistoryChangedMessage = "This Codex session file changed before the saved import point, so it was skipped to protect existing usage history."

const codexFactConflictMessage = "This Codex session file conflicts with previously imported usage, so it was skipped."

type codexIntegration struct {
	codexDir    string
	provisioner ProviderProvisioner
}

func NewCodexIntegration(codexDir string) Integration {
	return NewCodexIntegrationWithProvisioner(codexDir, codexProviderProvisioner{codexDir: codexDir})
}

func NewCodexIntegrationWithProvisioner(codexDir string, provisioner ProviderProvisioner) Integration {
	return codexIntegration{codexDir: codexDir, provisioner: provisioner}
}

func (codexIntegration) ProviderID() string {
	return ProviderCodex
}

func (codexIntegration) SourceIDs() []string {
	return []string{SourceCodexSessionJSONL}
}

func (codexIntegration) PricingInfo() UsagePricingInfo {
	return UsagePricingInfo{
		Basis:               PricingBasis,
		SourceURL:           PricingSourceURL,
		VerifiedAt:          PricingVerifiedAt,
		HistoricalRepricing: false,
	}
}

func (integration codexIntegration) Sync(
	ctx context.Context,
	stores store.Factory,
	options SyncOptions,
) (SyncOutcome, error) {
	files, cursors, observed, priceBackfill, noWorkResult, work, err := integration.preflight(ctx, stores, options)
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
	source, err := beginCodexUsageSync(ctx, db, integration.provisioner, options.ProvisionMode)
	if err != nil {
		return SyncOutcome{}, err
	}
	if cursors == nil {
		cursorRows, err := db.ListCodexUsageImportFiles(ctx, source.ID)
		if err != nil {
			return SyncOutcome{}, err
		}
		cursors = codexCursorMap(cursorRows)
	}
	if observed == nil {
		observationRows, err := db.ListUsageImportObservations(ctx, source.ID)
		if err != nil {
			return SyncOutcome{}, err
		}
		observed = observationMap(observationRows)
	}
	result := UsageSyncResult{ProviderID: ProviderCodex, Source: SourceCodexSessionJSONL}
	discoveredFileKeys := make([]store.UsageKey, 0, len(files))
	observations := make([]store.UsageImportObservation, 0)
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return SyncOutcome{}, apperror.Wrap(apperror.UsageImportFailed, "usage import canceled", err)
		}
		discoveredFileKeys = append(discoveredFileKeys, file.SourceKey)
		result.ScannedFiles++
		cursor, hasCursor := cursors[file.SourceKey]
		if hasCursor && codexCursorMatchesFile(cursor, file) {
			result.SkippedUnchangedFiles++
			continue
		}
		if observation, ok := observed[file.SourceKey]; ok &&
			observation.MetadataDigest == file.MetadataDigest && !options.ForceObservedRetry {
			result.SkippedUnchangedFiles++
			result.Errors = append(result.Errors, codexObservationError(file, observation.Status))
			continue
		}
		if hasCursor && codexFileIsShorterThanCheckpoint(cursor, file) {
			result.Errors = append(result.Errors, codexObservationError(file, store.UsageImportObservationHistoryChanged))
			observations = append(observations, newUsageObservation(source.ID, file, store.UsageImportObservationHistoryChanged))
			continue
		}

		parsed, fullParse, err := parseCodexUsageChange(ctx, file, cursor, hasCursor, options)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return SyncOutcome{}, apperror.Wrap(apperror.UsageImportFailed, "usage import canceled", ctxErr)
			}
			result.Errors = append(result.Errors, codexObservationError(file, store.UsageImportObservationUnavailable))
			observations = append(observations, newUsageObservation(source.ID, file, store.UsageImportObservationUnavailable))
			continue
		}
		eventsToStore := parsed.Events
		importedFacts := int64(len(parsed.Events))
		invalidLines := parsed.InvalidLines
		unsupportedLines := parsed.UnsupportedLines
		if hasCursor {
			if fullParse && !codexCheckpointPrefixMatches(parsed.Events, cursor) {
				result.Errors = append(result.Errors, codexObservationError(file, store.UsageImportObservationHistoryChanged))
				observations = append(observations, newUsageObservation(source.ID, file, store.UsageImportObservationHistoryChanged))
				continue
			}
			if fullParse {
				eventsToStore = parsed.Events[cursor.ImportedFacts:]
				importedFacts = int64(len(parsed.Events))
			} else {
				importedFacts = cursor.ImportedFacts + int64(len(parsed.Events))
				invalidLines += cursor.InvalidLines
				unsupportedLines += cursor.UnsupportedLines
			}
		}
		desired := store.CodexUsageImportFile{
			SourceID:              source.ID,
			FileKey:               file.SourceKey,
			ModifiedUnixMS:        file.ModifiedUnixMS,
			SizeBytes:             file.SizeBytes,
			ImportedFacts:         importedFacts,
			InvalidLines:          invalidLines,
			UnsupportedLines:      unsupportedLines,
			ParserRevision:        CodexUsageParserRevision,
			IdentityRevision:      CodexUsageIdentityRevision,
			EventDigest:           parsed.CheckpointEventDigest,
			CheckpointRevision:    usageCheckpointRevision,
			ProcessedBytes:        parsed.ProcessedBytes,
			MetadataDigest:        file.MetadataDigest,
			FileIdentityDigest:    file.FileIdentityDigest,
			BoundaryDigest:        parsed.BoundaryDigest,
			CheckpointEventDigest: parsed.CheckpointEventDigest,
			ParserStateJSON:       parsed.ParserStateJSON,
		}
		var expected *store.CodexUsageImportFile
		if hasCursor {
			expected = &cursor
		}
		insertResult, err := db.CommitCodexUsageImport(ctx, store.CommitCodexUsageImportParams{
			Generation: source.SyncGeneration,
			Facts:      usageEventsToFactParams(source.ID, eventsToStore),
			File:       desired,
			Expected:   expected,
		})
		if errors.Is(err, store.ErrUsageCursorConflict) {
			current, readErr := db.GetCodexUsageImportFile(ctx, source.ID, file.SourceKey)
			if readErr == nil && sameCodexUsageImportProgress(current, desired) {
				result.SkippedDuplicateEvents += int64(len(eventsToStore))
				result.InvalidLines += invalidLines
				result.UnsupportedLines += unsupportedLines
				continue
			}
			if readErr != nil && !errors.Is(readErr, store.ErrNotFound) {
				return SyncOutcome{}, apperror.Wrap(apperror.UsageImportFailed, "failed to inspect concurrent usage sync", readErr)
			}
			return SyncOutcome{}, err
		}
		if errors.Is(err, store.ErrUsageFactConflict) {
			result.Errors = append(result.Errors, codexObservationError(file, store.UsageImportObservationFactConflict))
			observations = append(observations, newUsageObservation(source.ID, file, store.UsageImportObservationFactConflict))
			continue
		}
		if err != nil {
			return SyncOutcome{}, err
		}
		result.ImportedEvents += int64(insertResult.Inserted)
		result.SkippedDuplicateEvents += int64(insertResult.Duplicates)
		result.InvalidLines += invalidLines
		result.UnsupportedLines += unsupportedLines
	}

	if err := db.CompleteUsageSync(ctx, store.CompleteUsageSyncParams{
		SourceID:          source.ID,
		Generation:        source.SyncGeneration,
		CompletedAtUnixMS: time.Now().UnixMilli(),
		Finalization: &store.CodexUsageSyncFinalization{
			DiscoveredFileKeys: discoveredFileKeys,
		},
		Observations: observations,
	}); err != nil {
		return SyncOutcome{}, err
	}
	if options.ProvisionMode == SyncProvisionProvider || priceBackfill {
		if err := backfillPartialUsageCosts(ctx, db); err != nil {
			return SyncOutcome{}, apperror.Wrap(apperror.UsageImportFailed, "failed to update usage pricing", err)
		}
	}
	return SyncOutcome{Result: result, Performed: true}, nil
}

func (integration codexIntegration) preflight(
	ctx context.Context,
	stores store.Factory,
	options SyncOptions,
) (
	[]SourceFile,
	map[store.UsageKey]store.CodexUsageImportFile,
	map[store.UsageKey]store.UsageImportObservation,
	bool,
	UsageSyncResult,
	bool,
	error,
) {
	if options.ProvisionMode == SyncProvisionProvider {
		files, err := ListCodexSessionFilesContext(ctx, integration.codexDir)
		if err != nil {
			return nil, nil, nil, false, UsageSyncResult{}, false,
				apperror.Wrap(apperror.UsageImportFailed, "failed to list Codex session files", err)
		}
		result := UsageSyncResult{
			ProviderID:   ProviderCodex,
			Source:       SourceCodexSessionJSONL,
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
			errors.New("usage Provider provisioner for Codex is required")
	}
	if err := integration.provisioner.Ensure(ctx, db, SyncExistingProvider); err != nil {
		return nil, nil, nil, false, UsageSyncResult{}, false, err
	}
	source, err := db.GetUsageSource(ctx, ProviderCodex, SourceCodexSessionJSONL)
	if errors.Is(err, store.ErrNotFound) {
		files, listErr := ListCodexSessionFilesContext(ctx, integration.codexDir)
		if listErr != nil {
			return nil, nil, nil, false, UsageSyncResult{}, false,
				apperror.Wrap(apperror.UsageImportFailed, "failed to list Codex session files", listErr)
		}
		result := UsageSyncResult{
			ProviderID:   ProviderCodex,
			Source:       SourceCodexSessionJSONL,
			ScannedFiles: int64(len(files)),
		}
		return files, nil, nil, false, result, true, nil
	}
	if err != nil {
		return nil, nil, nil, false, UsageSyncResult{}, false, err
	}
	if source.IdentityRevision != CodexUsageIdentityRevision {
		return nil, nil, nil, false, UsageSyncResult{}, false, store.ErrUsageIdentityRevision
	}
	cursorRows, err := db.ListCodexUsageImportFiles(ctx, source.ID)
	if err != nil {
		return nil, nil, nil, false, UsageSyncResult{}, false, err
	}
	observationRows, err := db.ListUsageImportObservations(ctx, source.ID)
	if err != nil {
		return nil, nil, nil, false, UsageSyncResult{}, false, err
	}
	unknownModels, err := db.ListUnknownUsageCostModels(ctx, ProviderCodex)
	if err != nil {
		return nil, nil, nil, false, UsageSyncResult{}, false, err
	}
	files, err := ListCodexSessionFilesContext(ctx, integration.codexDir)
	if err != nil {
		return nil, nil, nil, false, UsageSyncResult{}, false,
			apperror.Wrap(apperror.UsageImportFailed, "failed to list Codex session files", err)
	}
	result := UsageSyncResult{
		ProviderID:   ProviderCodex,
		Source:       SourceCodexSessionJSONL,
		ScannedFiles: int64(len(files)),
	}
	cursors := codexCursorMap(cursorRows)
	observations := observationMap(observationRows)
	priceBackfill := hasSupportedUnknownUsageModel(unknownModels, codexPriceCatalog.Supports)
	work := source.SyncGeneration != source.CompletedGeneration || priceBackfill
	discovered := make(map[store.UsageKey]struct{}, len(files))
	for _, file := range files {
		discovered[file.SourceKey] = struct{}{}
		if cursor, ok := cursors[file.SourceKey]; ok {
			if cursor.IdentityRevision != CodexUsageIdentityRevision {
				return nil, nil, nil, false, UsageSyncResult{}, false, store.ErrUsageIdentityRevision
			}
			if codexCursorMatchesFile(cursor, file) {
				result.SkippedUnchangedFiles++
				continue
			}
		}
		if observation, ok := observations[file.SourceKey]; ok &&
			observation.MetadataDigest == file.MetadataDigest && !options.ForceObservedRetry {
			result.SkippedUnchangedFiles++
			result.Errors = append(result.Errors, codexObservationError(file, observation.Status))
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

func parseCodexUsageChange(
	ctx context.Context,
	file SourceFile,
	cursor store.CodexUsageImportFile,
	hasCursor bool,
	options SyncOptions,
) (checkpointParseResult, bool, error) {
	if hasCursor && cursor.CheckpointRevision == usageCheckpointRevision &&
		cursor.ParserRevision == CodexUsageParserRevision &&
		cursor.IdentityRevision == CodexUsageIdentityRevision &&
		!cursor.FileIdentityDigest.IsZero() &&
		cursor.FileIdentityDigest == file.FileIdentityDigest &&
		file.SizeBytes > cursor.SizeBytes {
		state, err := decodeCodexParserState(cursor.ParserStateJSON)
		if err == nil {
			parsed, parseErr := parseCodexCheckpointFile(
				ctx,
				file,
				cursor.ProcessedBytes,
				state,
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
	}
	parsed, err := parseCodexCheckpointFile(
		ctx,
		file,
		0,
		newCodexParserState(file),
		store.UsageKey{},
		store.UsageKey{},
		options.fileSystem,
		options.Observer,
	)
	return parsed, true, err
}

func codexCheckpointPrefixMatches(events []Event, cursor store.CodexUsageImportFile) bool {
	if cursor.ImportedFacts < 0 || cursor.ImportedFacts > int64(len(events)) ||
		cursor.IdentityRevision != CodexUsageIdentityRevision {
		return false
	}
	if cursor.CheckpointRevision == usageCheckpointRevision {
		return checkpointEventDigest(ProviderCodex, events, cursor.ImportedFacts) == cursor.CheckpointEventDigest
	}
	return cursor.CheckpointRevision == 0 && EventDigest(events, cursor.ImportedFacts) == cursor.EventDigest
}

func codexCursorMatchesFile(cursor store.CodexUsageImportFile, file SourceFile) bool {
	if cursor.ParserRevision != CodexUsageParserRevision ||
		cursor.IdentityRevision != CodexUsageIdentityRevision {
		return false
	}
	if cursor.CheckpointRevision == usageCheckpointRevision {
		return cursor.MetadataDigest == file.MetadataDigest
	}
	return cursor.CheckpointRevision == 0 &&
		cursor.ModifiedUnixMS == file.ModifiedUnixMS && cursor.SizeBytes == file.SizeBytes
}

func codexFileIsShorterThanCheckpoint(cursor store.CodexUsageImportFile, file SourceFile) bool {
	if cursor.CheckpointRevision == usageCheckpointRevision {
		return file.SizeBytes < cursor.ProcessedBytes
	}
	return file.SizeBytes < cursor.SizeBytes
}

func codexCursorMap(rows []store.CodexUsageImportFile) map[store.UsageKey]store.CodexUsageImportFile {
	result := make(map[store.UsageKey]store.CodexUsageImportFile, len(rows))
	for _, row := range rows {
		result[row.FileKey] = row
	}
	return result
}

func observationMap(rows []store.UsageImportObservation) map[store.UsageKey]store.UsageImportObservation {
	result := make(map[store.UsageKey]store.UsageImportObservation, len(rows))
	for _, row := range rows {
		result[row.FileKey] = row
	}
	return result
}

func hasSupportedUnknownUsageModel(
	models []store.UsageUnknownCostModel,
	supports func(string) bool,
) bool {
	for _, model := range models {
		if supports(model.Model) {
			return true
		}
	}
	return false
}

func newUsageObservation(
	sourceID int64,
	file SourceFile,
	status store.UsageImportObservationStatus,
) store.UsageImportObservation {
	return store.UsageImportObservation{
		SourceID:       sourceID,
		FileKey:        file.SourceKey,
		MetadataDigest: file.MetadataDigest,
		Status:         status,
	}
}

func codexObservationError(
	file SourceFile,
	status store.UsageImportObservationStatus,
) UsageImportError {
	message := sanitizedUsageImportError(errors.New("usage file unavailable"))
	switch status {
	case store.UsageImportObservationHistoryChanged:
		message = codexHistoryChangedMessage
	case store.UsageImportObservationFactConflict:
		message = codexFactConflictMessage
	}
	return UsageImportError{
		SourceKey: file.SourceKey.String(),
		FileName:  filepath.Base(file.Path),
		Message:   message,
	}
}

func beginCodexUsageSync(
	ctx context.Context,
	db *store.Store,
	provisioner ProviderProvisioner,
	mode SyncProvisionMode,
) (store.UsageSource, error) {
	var source store.UsageSource
	err := db.WithTransaction(ctx, func(txStore *store.Store) error {
		if provisioner == nil {
			return errors.New("usage Provider provisioner for Codex is required")
		}
		if err := provisioner.Ensure(ctx, txStore, mode); err != nil {
			return err
		}
		var err error
		source, err = txStore.BeginUsageSync(ctx, ProviderCodex, SourceCodexSessionJSONL, CodexUsageIdentityRevision)
		if err != nil {
			return err
		}
		// A stale identity checkpoint must roll back the generation advance so
		// runtime sync cannot reinterpret already persisted event identities.
		return txStore.ValidateCodexUsageImportIdentity(ctx, source.ID, CodexUsageIdentityRevision)
	})
	return source, err
}

type codexProviderProvisioner struct {
	codexDir string
}

func (provisioner codexProviderProvisioner) Ensure(
	ctx context.Context,
	db *store.Store,
	mode SyncProvisionMode,
) error {
	home, err := codexconfig.ResolveHome(provisioner.codexDir)
	if err != nil {
		return err
	}
	var provider store.Provider
	if mode == SyncProvisionProvider {
		metadataJSON, err := codexpreset.ProviderMetadataJSON(home)
		if err != nil {
			return err
		}
		provider, _, err = db.CreateProviderIfMissing(ctx, store.CreateProviderParams{
			ID: ProviderCodex, Name: codexpreset.ProviderName,
			AdapterID: codexconfig.AdapterID, MetadataJSON: metadataJSON,
		})
		if err != nil {
			return err
		}
	} else {
		provider, err = db.GetProvider(ctx, ProviderCodex)
		if errors.Is(err, store.ErrNotFound) {
			return store.ErrUsageProviderMissing
		}
		if err != nil {
			return err
		}
	}
	if provider.AdapterID != codexconfig.AdapterID {
		return fmt.Errorf("Codex usage Provider adapter does not match the configured integration")
	}
	metadata, err := codexpreset.DecodeProviderMetadata(provider.MetadataJSON)
	if err != nil || !metadata.Compatible() {
		return fmt.Errorf("Codex usage Provider locator is invalid")
	}
	if metadata.CodexDir != home.Dir ||
		metadata.ConfigPath != home.ConfigPath ||
		metadata.AuthPath != home.AuthPath {
		return fmt.Errorf("Codex usage Provider locator does not match the configured Codex home")
	}
	return nil
}

func sameCodexUsageImportProgress(left, right store.CodexUsageImportFile) bool {
	return left.SourceID == right.SourceID && left.FileKey == right.FileKey &&
		left.ModifiedUnixMS == right.ModifiedUnixMS && left.SizeBytes == right.SizeBytes &&
		left.ImportedFacts == right.ImportedFacts && left.InvalidLines == right.InvalidLines &&
		left.UnsupportedLines == right.UnsupportedLines && left.ParserRevision == right.ParserRevision &&
		left.IdentityRevision == right.IdentityRevision && left.EventDigest == right.EventDigest &&
		left.CheckpointRevision == right.CheckpointRevision &&
		left.ProcessedBytes == right.ProcessedBytes &&
		left.MetadataDigest == right.MetadataDigest &&
		left.FileIdentityDigest == right.FileIdentityDigest &&
		left.BoundaryDigest == right.BoundaryDigest &&
		left.CheckpointEventDigest == right.CheckpointEventDigest &&
		left.ParserStateJSON == right.ParserStateJSON
}

func usageEventsToFactParams(sourceID int64, events []Event) []store.CreateUsageFactParams {
	facts := make([]store.CreateUsageFactParams, 0, len(events))
	for _, event := range events {
		facts = append(facts, store.CreateUsageFactParams{
			EventKey:            event.EventKey,
			SourceID:            sourceID,
			SessionKey:          event.SessionID,
			ModelKey:            event.Model,
			OccurredAtUnixMS:    event.OccurredAtUnixMS,
			InputTokens:         event.InputTokens,
			CachedInputTokens:   event.CachedInputTokens,
			OutputTokens:        event.OutputTokens,
			TotalTokens:         event.TotalTokens,
			EstimatedCostMicros: event.EstimatedCostMicros,
			CostStatus:          event.CostStatus,
		})
	}
	return facts
}

func backfillPartialUsageCosts(ctx context.Context, db *store.Store) error {
	return backfillUnknownUsageCosts(
		ctx,
		db,
		ProviderCodex,
		codexPriceCatalog.Supports,
		EstimateCostMicros,
	)
}

func backfillUnknownUsageCosts(
	ctx context.Context,
	db *store.Store,
	providerID string,
	supports func(string) bool,
	estimate func(string, TokenCounts) (*int64, store.UsageCostStatus),
) error {
	const batchSize = 256
	models, err := db.ListUnknownUsageCostModels(ctx, providerID)
	if err != nil {
		return err
	}
	for _, model := range models {
		if !supports(model.Model) {
			continue
		}
		var afterID int64
		for {
			candidates, err := db.ListUnknownUsageFactCostCandidates(
				ctx,
				providerID,
				model.SourceID,
				model.ModelID,
				afterID,
				batchSize,
			)
			if err != nil {
				return err
			}
			if len(candidates) == 0 {
				break
			}
			updates := make([]store.UpdateUsageFactCostParams, 0, len(candidates))
			for _, candidate := range candidates {
				cost, status := estimate(model.Model, TokenCounts{
					InputTokens:       candidate.InputTokens,
					CachedInputTokens: candidate.CachedInputTokens,
					OutputTokens:      candidate.OutputTokens,
					TotalTokens:       candidate.TotalTokens,
				})
				if cost == nil || status == CostStatusUnknown {
					continue
				}
				updates = append(updates, store.UpdateUsageFactCostParams{
					ID:                  candidate.ID,
					EstimatedCostMicros: *cost,
					CostStatus:          status,
				})
			}
			// Only unknown facts are eligible, so concurrent import/backfill runs are
			// idempotent and never overwrite an already classified historical fact.
			if _, err := db.UpdateUnknownUsageFactCosts(ctx, providerID, updates); err != nil {
				return err
			}
			afterID = candidates[len(candidates)-1].ID
			if len(candidates) < batchSize {
				break
			}
		}
	}
	return nil
}

func sanitizedUsageImportError(err error) string {
	if err == nil {
		return ""
	}
	return "Codex session file could not be read"
}
