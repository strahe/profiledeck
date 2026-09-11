package usage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/strahe/profiledeck/internal/apperror"
	grokconfig "github.com/strahe/profiledeck/internal/grokbuild/config"
	grokpreset "github.com/strahe/profiledeck/internal/grokbuild/preset"
	"github.com/strahe/profiledeck/internal/store"
)

const (
	grokBuildFileUnavailableMessage = "A Grok Build session file could not be imported. It will be checked again after the file changes or when you sync manually."
	grokBuildHistoryChangedMessage  = "A Grok Build session file changed before the saved import point. It will be checked again after the file changes or when you sync manually."
	grokBuildFactConflictMessage    = "A Grok Build session file conflicts with previously imported usage. It will be checked again after the file changes or when you sync manually."
)

type grokBuildIntegration struct {
	grokHome    string
	provisioner ProviderProvisioner
}

func NewGrokBuildIntegration(grokHome string) Integration {
	return NewGrokBuildIntegrationWithProvisioner(
		grokHome,
		grokBuildProviderProvisioner{grokHome: grokHome},
	)
}

func NewGrokBuildIntegrationWithProvisioner(
	grokHome string,
	provisioner ProviderProvisioner,
) Integration {
	return grokBuildIntegration{grokHome: grokHome, provisioner: provisioner}
}

func (grokBuildIntegration) ProviderID() string {
	return grokconfig.ProviderID
}

func (grokBuildIntegration) SourceIDs() []string {
	return []string{SourceGrokBuildSessionJSONL}
}

func (grokBuildIntegration) PricingInfo() UsagePricingInfo {
	return UsagePricingInfo{
		Basis:               GrokBuildPricingBasis,
		SourceURL:           GrokBuildPricingSource,
		VerifiedAt:          GrokBuildPricingVerified,
		HistoricalRepricing: false,
	}
}

func (integration grokBuildIntegration) Sync(
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
	source, err := beginGrokBuildUsageSync(ctx, db, integration.provisioner, options.ProvisionMode)
	if err != nil {
		return SyncOutcome{}, err
	}
	if cursors == nil {
		cursorRows, err := db.ListGrokBuildUsageImportFiles(ctx, source.ID)
		if err != nil {
			return SyncOutcome{}, err
		}
		cursors = grokBuildCursorMap(cursorRows)
	}
	if observed == nil {
		observationRows, err := db.ListUsageImportObservations(ctx, source.ID)
		if err != nil {
			return SyncOutcome{}, err
		}
		observed = observationMap(observationRows)
	}
	result := UsageSyncResult{
		ProviderID: grokconfig.ProviderID,
		Source:     SourceGrokBuildSessionJSONL,
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
		if hasCursor && grokBuildCursorMatchesFile(cursor, file) {
			result.SkippedUnchangedFiles++
			continue
		}
		if observation, ok := observed[file.SourceKey]; ok &&
			observation.MetadataDigest == file.MetadataDigest && !options.ForceObservedRetry {
			result.SkippedUnchangedFiles++
			result.Errors = append(result.Errors, grokBuildObservationError(observation.Status))
			continue
		}
		if hasCursor && grokBuildFileIsShorterThanCheckpoint(cursor, file) {
			result.Errors = append(result.Errors, grokBuildObservationError(store.UsageImportObservationHistoryChanged))
			observations = append(observations, newUsageObservation(source.ID, file, store.UsageImportObservationHistoryChanged))
			continue
		}

		parsed, fullParse, err := parseGrokBuildUsageChange(ctx, file, cursor, hasCursor, options)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return SyncOutcome{}, apperror.Wrap(
					apperror.UsageImportFailed,
					"usage import canceled",
					ctxErr,
				)
			}
			result.InvalidLines++
			result.Errors = append(result.Errors, grokBuildObservationError(store.UsageImportObservationUnavailable))
			observations = append(observations, newUsageObservation(source.ID, file, store.UsageImportObservationUnavailable))
			continue
		}
		eventsToStore := parsed.Events
		importedFacts := int64(len(parsed.Events))
		invalidLines := parsed.InvalidLines
		unsupportedLines := parsed.UnsupportedLines
		if hasCursor {
			if fullParse && !grokBuildCheckpointPrefixMatches(parsed.Events, cursor) {
				result.Errors = append(result.Errors, grokBuildObservationError(store.UsageImportObservationHistoryChanged))
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

		desired := store.GrokBuildUsageImportFile{
			SourceID:              source.ID,
			FileKey:               file.SourceKey,
			ModifiedUnixMS:        file.ModifiedUnixMS,
			SizeBytes:             file.SizeBytes,
			ImportedFacts:         importedFacts,
			InvalidLines:          invalidLines,
			UnsupportedLines:      unsupportedLines,
			ParserRevision:        GrokBuildUsageParserRevision,
			IdentityRevision:      GrokBuildUsageIdentityRevision,
			EventDigest:           parsed.CheckpointEventDigest,
			CheckpointRevision:    usageCheckpointRevision,
			ProcessedBytes:        parsed.ProcessedBytes,
			MetadataDigest:        file.MetadataDigest,
			FileIdentityDigest:    file.FileIdentityDigest,
			BoundaryDigest:        parsed.BoundaryDigest,
			CheckpointEventDigest: parsed.CheckpointEventDigest,
			ParserStateJSON:       "{}",
		}
		var expected *store.GrokBuildUsageImportFile
		if hasCursor {
			expected = &cursor
		}
		insertResult, err := db.CommitGrokBuildUsageImport(
			ctx,
			store.CommitGrokBuildUsageImportParams{
				ProviderID: grokconfig.ProviderID,
				Generation: source.SyncGeneration,
				Facts:      usageEventsToFactParams(source.ID, eventsToStore),
				File:       desired,
				Expected:   expected,
			},
		)
		if errors.Is(err, store.ErrUsageCursorConflict) {
			current, readErr := db.GetGrokBuildUsageImportFile(ctx, source.ID, file.SourceKey)
			if readErr == nil && sameGrokBuildUsageImportProgress(current, desired) {
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
			result.Errors = append(result.Errors, grokBuildObservationError(store.UsageImportObservationFactConflict))
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
		Finalization: &store.GrokBuildUsageSyncFinalization{
			ProviderID:         grokconfig.ProviderID,
			DiscoveredFileKeys: discoveredFileKeys,
		},
		Observations: observations,
	}); err != nil {
		return SyncOutcome{}, err
	}
	if options.ProvisionMode == SyncProvisionProvider || priceBackfill {
		if err := backfillUnknownUsageCosts(
			ctx,
			db,
			grokconfig.ProviderID,
			grokBuildPriceCatalog.Supports,
			EstimateGrokBuildCostMicros,
		); err != nil {
			return SyncOutcome{}, apperror.Wrap(
				apperror.UsageImportFailed,
				"failed to update usage pricing",
				err,
			)
		}
	}
	return SyncOutcome{Result: result, Performed: true}, nil
}

func (integration grokBuildIntegration) preflight(
	ctx context.Context,
	stores store.Factory,
	options SyncOptions,
) (
	[]SourceFile,
	map[store.UsageKey]store.GrokBuildUsageImportFile,
	map[store.UsageKey]store.UsageImportObservation,
	bool,
	UsageSyncResult,
	bool,
	error,
) {
	if options.ProvisionMode == SyncProvisionProvider {
		files, err := ListGrokBuildSessionFilesContext(ctx, integration.grokHome)
		if err != nil {
			return nil, nil, nil, false, UsageSyncResult{}, false, apperror.Wrap(
				apperror.UsageImportFailed,
				"failed to list Grok Build session files",
				err,
			)
		}
		result := UsageSyncResult{
			ProviderID:   grokconfig.ProviderID,
			Source:       SourceGrokBuildSessionJSONL,
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
			errors.New("usage Provider provisioner for Grok Build is required")
	}
	if err := integration.provisioner.Ensure(ctx, db, SyncExistingProvider); err != nil {
		return nil, nil, nil, false, UsageSyncResult{}, false, err
	}
	source, err := db.GetUsageSource(ctx, grokconfig.ProviderID, SourceGrokBuildSessionJSONL)
	if errors.Is(err, store.ErrNotFound) {
		files, listErr := ListGrokBuildSessionFilesContext(ctx, integration.grokHome)
		if listErr != nil {
			return nil, nil, nil, false, UsageSyncResult{}, false, apperror.Wrap(
				apperror.UsageImportFailed,
				"failed to list Grok Build session files",
				listErr,
			)
		}
		result := UsageSyncResult{
			ProviderID:   grokconfig.ProviderID,
			Source:       SourceGrokBuildSessionJSONL,
			ScannedFiles: int64(len(files)),
		}
		return files, nil, nil, false, result, true, nil
	}
	if err != nil {
		return nil, nil, nil, false, UsageSyncResult{}, false, err
	}
	if source.IdentityRevision != GrokBuildUsageIdentityRevision {
		return nil, nil, nil, false, UsageSyncResult{}, false, store.ErrUsageIdentityRevision
	}
	cursorRows, err := db.ListGrokBuildUsageImportFiles(ctx, source.ID)
	if err != nil {
		return nil, nil, nil, false, UsageSyncResult{}, false, err
	}
	observationRows, err := db.ListUsageImportObservations(ctx, source.ID)
	if err != nil {
		return nil, nil, nil, false, UsageSyncResult{}, false, err
	}
	unknownModels, err := db.ListUnknownUsageCostModels(ctx, grokconfig.ProviderID)
	if err != nil {
		return nil, nil, nil, false, UsageSyncResult{}, false, err
	}
	files, err := ListGrokBuildSessionFilesContext(ctx, integration.grokHome)
	if err != nil {
		return nil, nil, nil, false, UsageSyncResult{}, false, apperror.Wrap(
			apperror.UsageImportFailed,
			"failed to list Grok Build session files",
			err,
		)
	}
	result := UsageSyncResult{
		ProviderID:   grokconfig.ProviderID,
		Source:       SourceGrokBuildSessionJSONL,
		ScannedFiles: int64(len(files)),
	}
	cursors := grokBuildCursorMap(cursorRows)
	observations := observationMap(observationRows)
	priceBackfill := hasSupportedUnknownUsageModel(unknownModels, grokBuildPriceCatalog.Supports)
	work := source.SyncGeneration != source.CompletedGeneration || priceBackfill
	discovered := make(map[store.UsageKey]struct{}, len(files))
	for _, file := range files {
		discovered[file.SourceKey] = struct{}{}
		if cursor, ok := cursors[file.SourceKey]; ok {
			if cursor.IdentityRevision != GrokBuildUsageIdentityRevision {
				return nil, nil, nil, false, UsageSyncResult{}, false, store.ErrUsageIdentityRevision
			}
			if grokBuildCursorMatchesFile(cursor, file) {
				result.SkippedUnchangedFiles++
				continue
			}
		}
		if observation, ok := observations[file.SourceKey]; ok &&
			observation.MetadataDigest == file.MetadataDigest && !options.ForceObservedRetry {
			result.SkippedUnchangedFiles++
			result.Errors = append(result.Errors, grokBuildObservationError(observation.Status))
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

func parseGrokBuildUsageChange(
	ctx context.Context,
	file SourceFile,
	cursor store.GrokBuildUsageImportFile,
	hasCursor bool,
	options SyncOptions,
) (checkpointParseResult, bool, error) {
	if hasCursor && cursor.CheckpointRevision == usageCheckpointRevision &&
		cursor.ParserRevision == GrokBuildUsageParserRevision &&
		cursor.IdentityRevision == GrokBuildUsageIdentityRevision &&
		!cursor.FileIdentityDigest.IsZero() &&
		cursor.FileIdentityDigest == file.FileIdentityDigest &&
		cursor.ParserStateJSON == "{}" && file.SizeBytes > cursor.SizeBytes {
		parsed, parseErr := parseGrokBuildCheckpointFile(
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
	parsed, err := parseGrokBuildCheckpointFile(
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

func grokBuildCheckpointPrefixMatches(events []Event, cursor store.GrokBuildUsageImportFile) bool {
	if cursor.ImportedFacts < 0 || cursor.ImportedFacts > int64(len(events)) ||
		cursor.IdentityRevision != GrokBuildUsageIdentityRevision {
		return false
	}
	if cursor.CheckpointRevision == usageCheckpointRevision {
		return checkpointEventDigest(grokconfig.ProviderID, events, cursor.ImportedFacts) == cursor.CheckpointEventDigest
	}
	return cursor.CheckpointRevision == 0 &&
		GrokBuildEventDigest(events, cursor.ImportedFacts) == cursor.EventDigest
}

func grokBuildCursorMatchesFile(cursor store.GrokBuildUsageImportFile, file SourceFile) bool {
	if cursor.ParserRevision != GrokBuildUsageParserRevision ||
		cursor.IdentityRevision != GrokBuildUsageIdentityRevision {
		return false
	}
	if cursor.CheckpointRevision == usageCheckpointRevision {
		return cursor.MetadataDigest == file.MetadataDigest
	}
	return cursor.CheckpointRevision == 0 &&
		cursor.ModifiedUnixMS == file.ModifiedUnixMS && cursor.SizeBytes == file.SizeBytes
}

func grokBuildFileIsShorterThanCheckpoint(cursor store.GrokBuildUsageImportFile, file SourceFile) bool {
	if cursor.CheckpointRevision == usageCheckpointRevision {
		return file.SizeBytes < cursor.ProcessedBytes
	}
	return file.SizeBytes < cursor.SizeBytes
}

func grokBuildCursorMap(rows []store.GrokBuildUsageImportFile) map[store.UsageKey]store.GrokBuildUsageImportFile {
	result := make(map[store.UsageKey]store.GrokBuildUsageImportFile, len(rows))
	for _, row := range rows {
		result[row.FileKey] = row
	}
	return result
}

func grokBuildObservationError(status store.UsageImportObservationStatus) UsageImportError {
	message := grokBuildFileUnavailableMessage
	switch status {
	case store.UsageImportObservationHistoryChanged:
		message = grokBuildHistoryChangedMessage
	case store.UsageImportObservationFactConflict:
		message = grokBuildFactConflictMessage
	}
	return UsageImportError{Message: message}
}

func beginGrokBuildUsageSync(
	ctx context.Context,
	db *store.Store,
	provisioner ProviderProvisioner,
	mode SyncProvisionMode,
) (store.UsageSource, error) {
	var source store.UsageSource
	err := db.WithTransaction(ctx, func(txStore *store.Store) error {
		if provisioner == nil {
			return errors.New("usage Provider provisioner for Grok Build is required")
		}
		if err := provisioner.Ensure(ctx, txStore, mode); err != nil {
			return err
		}
		var err error
		source, err = txStore.BeginUsageSync(
			ctx,
			grokconfig.ProviderID,
			SourceGrokBuildSessionJSONL,
			GrokBuildUsageIdentityRevision,
		)
		if err != nil {
			return err
		}
		return txStore.ValidateGrokBuildUsageImportIdentity(
			ctx,
			source.ID,
			grokconfig.ProviderID,
			GrokBuildUsageIdentityRevision,
		)
	})
	return source, err
}

type grokBuildProviderProvisioner struct {
	grokHome string
}

func (provisioner grokBuildProviderProvisioner) Ensure(
	ctx context.Context,
	db *store.Store,
	mode SyncProvisionMode,
) error {
	home, err := grokconfig.ResolveHome(provisioner.grokHome)
	if err != nil {
		return err
	}
	var provider store.Provider
	if mode == SyncProvisionProvider {
		metadataJSON, err := grokpreset.ProviderMetadataJSON(home)
		if err != nil {
			return err
		}
		provider, _, err = db.CreateProviderIfMissing(ctx, store.CreateProviderParams{
			ID:           grokconfig.ProviderID,
			Name:         grokpreset.ProviderName,
			AdapterID:    grokconfig.AdapterID,
			MetadataJSON: metadataJSON,
		})
		if err != nil {
			return err
		}
	} else {
		provider, err = db.GetProvider(ctx, grokconfig.ProviderID)
		if errors.Is(err, store.ErrNotFound) {
			return store.ErrUsageProviderMissing
		}
		if err != nil {
			return err
		}
	}
	if provider.AdapterID != grokconfig.AdapterID {
		return fmt.Errorf("usage Provider adapter does not match the Grok Build integration")
	}
	metadata, err := grokpreset.DecodeProviderMetadata(provider.MetadataJSON)
	if err != nil || !metadata.Compatible() {
		return fmt.Errorf("usage Provider locator for Grok Build is invalid")
	}
	if metadata.GrokHome != home.Dir ||
		metadata.ConfigPath != home.ConfigPath ||
		metadata.AuthPath != home.AuthPath {
		return fmt.Errorf("usage Provider locator does not match the configured Grok Home")
	}
	return nil
}

func sameGrokBuildUsageImportProgress(
	left store.GrokBuildUsageImportFile,
	right store.GrokBuildUsageImportFile,
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
