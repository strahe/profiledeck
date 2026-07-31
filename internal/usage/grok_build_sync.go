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
	grokBuildFileUnavailableMessage = "A Grok Build session file could not be imported and will be retried."
	grokBuildHistoryChangedMessage  = "A Grok Build session file changed before the saved import point and will be retried."
	grokBuildFactConflictMessage    = "A Grok Build session file conflicts with previously imported usage and will be retried."
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
	mode SyncProvisionMode,
) (UsageSyncResult, error) {
	db, err := stores.OpenHealthy(ctx, false)
	if err != nil {
		return UsageSyncResult{}, err
	}
	defer db.Close()

	source, err := beginGrokBuildUsageSync(ctx, db, integration.provisioner, mode)
	if err != nil {
		return UsageSyncResult{}, err
	}
	files, err := ListGrokBuildSessionFilesContext(ctx, integration.grokHome)
	if err != nil {
		return UsageSyncResult{}, apperror.Wrap(
			apperror.UsageImportFailed,
			"failed to list Grok Build session files",
			err,
		)
	}
	result := UsageSyncResult{
		ProviderID: grokconfig.ProviderID,
		Source:     SourceGrokBuildSessionJSONL,
	}
	discoveredFileKeys := make([]store.UsageKey, 0, len(files))
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return UsageSyncResult{}, apperror.Wrap(
				apperror.UsageImportFailed,
				"usage import canceled",
				err,
			)
		}
		discoveredFileKeys = append(discoveredFileKeys, file.SourceKey)
		result.ScannedFiles++

		cursor, hasCursor, err := grokBuildUsageCursor(ctx, db, source.ID, file.SourceKey)
		if err != nil {
			return UsageSyncResult{}, apperror.Wrap(
				apperror.UsageImportFailed,
				"failed to inspect usage import progress",
				err,
			)
		}
		if hasCursor &&
			cursor.ModifiedUnixMS == file.ModifiedUnixMS &&
			cursor.SizeBytes == file.SizeBytes &&
			cursor.ParserRevision == GrokBuildUsageParserRevision &&
			cursor.IdentityRevision == GrokBuildUsageIdentityRevision {
			result.SkippedUnchangedFiles++
			continue
		}

		parsed, err := ParseGrokBuildSessionFileContext(ctx, file)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return UsageSyncResult{}, apperror.Wrap(
					apperror.UsageImportFailed,
					"usage import canceled",
					ctxErr,
				)
			}
			result.InvalidLines++
			result.Errors = append(result.Errors, UsageImportError{
				Message: grokBuildFileUnavailableMessage,
			})
			continue
		}
		eventsToStore, safe := grokBuildEventsAfterCursor(parsed.Events, cursor, hasCursor, file)
		if !safe {
			result.Errors = append(result.Errors, UsageImportError{
				Message: grokBuildHistoryChangedMessage,
			})
			continue
		}

		desired := store.GrokBuildUsageImportFile{
			SourceID:         source.ID,
			FileKey:          file.SourceKey,
			ModifiedUnixMS:   file.ModifiedUnixMS,
			SizeBytes:        file.SizeBytes,
			ImportedFacts:    int64(len(parsed.Events)),
			InvalidLines:     parsed.InvalidLines,
			UnsupportedLines: parsed.UnsupportedLines,
			ParserRevision:   GrokBuildUsageParserRevision,
			IdentityRevision: GrokBuildUsageIdentityRevision,
			EventDigest:      GrokBuildEventDigest(parsed.Events, int64(len(parsed.Events))),
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
				result.UnsupportedLines += parsed.UnsupportedLines
				continue
			}
			if readErr != nil && !errors.Is(readErr, store.ErrNotFound) {
				return UsageSyncResult{}, apperror.Wrap(
					apperror.UsageImportFailed,
					"failed to inspect concurrent usage sync",
					readErr,
				)
			}
			return UsageSyncResult{}, err
		}
		if errors.Is(err, store.ErrUsageFactConflict) {
			result.InvalidLines++
			result.Errors = append(result.Errors, UsageImportError{
				Message: grokBuildFactConflictMessage,
			})
			continue
		}
		if err != nil {
			return UsageSyncResult{}, err
		}
		result.ImportedEvents += int64(insertResult.Inserted)
		result.SkippedDuplicateEvents += int64(insertResult.Duplicates)
		result.InvalidLines += parsed.InvalidLines
		result.UnsupportedLines += parsed.UnsupportedLines
	}

	if err := db.CompleteUsageSync(ctx, store.CompleteUsageSyncParams{
		SourceID:          source.ID,
		Generation:        source.SyncGeneration,
		CompletedAtUnixMS: time.Now().UnixMilli(),
		Finalization: &store.GrokBuildUsageSyncFinalization{
			ProviderID:         grokconfig.ProviderID,
			DiscoveredFileKeys: discoveredFileKeys,
		},
	}); err != nil {
		return UsageSyncResult{}, err
	}
	if err := backfillUnknownUsageCosts(
		ctx,
		db,
		grokconfig.ProviderID,
		grokBuildPriceCatalog.Supports,
		EstimateGrokBuildCostMicros,
	); err != nil {
		return UsageSyncResult{}, apperror.Wrap(
			apperror.UsageImportFailed,
			"failed to update usage pricing",
			err,
		)
	}
	return result, nil
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

func grokBuildUsageCursor(
	ctx context.Context,
	db *store.Store,
	sourceID int64,
	fileKey store.UsageKey,
) (store.GrokBuildUsageImportFile, bool, error) {
	cursor, err := db.GetGrokBuildUsageImportFile(ctx, sourceID, fileKey)
	if errors.Is(err, store.ErrNotFound) {
		return store.GrokBuildUsageImportFile{}, false, nil
	}
	return cursor, err == nil, err
}

func grokBuildEventsAfterCursor(
	events []Event,
	cursor store.GrokBuildUsageImportFile,
	hasCursor bool,
	file SourceFile,
) ([]Event, bool) {
	if !hasCursor {
		return events, true
	}
	if cursor.IdentityRevision != GrokBuildUsageIdentityRevision ||
		cursor.ImportedFacts < 0 ||
		cursor.ImportedFacts > int64(len(events)) ||
		file.SizeBytes < cursor.SizeBytes {
		return nil, false
	}
	if GrokBuildEventDigest(events, cursor.ImportedFacts) != cursor.EventDigest {
		return nil, false
	}
	return events[cursor.ImportedFacts:], true
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
		left.EventDigest == right.EventDigest
}
