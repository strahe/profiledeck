package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	usageKeySize            = 32
	maxUsageParserStateSize = 1024 * 1024
)

// UsageUnknownModelKey is the persisted label for absent or unsafe models.
const UsageUnknownModelKey = "unknown"

var (
	ErrUsageCursorConflict   = errors.New("usage import cursor conflict")
	ErrUsageFactConflict     = errors.New("usage fact conflict")
	ErrUsageIdentityRevision = errors.New("usage identity revision requires migration")
	ErrUsageProviderMissing  = errors.New("usage provider is missing")
	ErrUsageSyncSuperseded   = errors.New("usage sync was superseded")
)

type UsageKey [usageKeySize]byte

func (key UsageKey) String() string {
	return hex.EncodeToString(key[:])
}

func (key UsageKey) MarshalText() ([]byte, error) {
	encoded := make([]byte, hex.EncodedLen(usageKeySize))
	hex.Encode(encoded, key[:])
	return encoded, nil
}

func (key *UsageKey) UnmarshalText(text []byte) error {
	if key == nil || len(text) != hex.EncodedLen(usageKeySize) {
		return errors.New("usage key text is invalid")
	}
	var decoded UsageKey
	if _, err := hex.Decode(decoded[:], text); err != nil {
		return errors.New("usage key text is invalid")
	}
	*key = decoded
	return nil
}

func (key UsageKey) IsZero() bool {
	return key == UsageKey{}
}

func (key UsageKey) Value() (driver.Value, error) {
	return key[:], nil
}

func (key *UsageKey) Scan(value any) error {
	encoded, ok := value.([]byte)
	if !ok || len(encoded) != usageKeySize {
		return errors.New("persisted usage key is invalid")
	}
	copy(key[:], encoded)
	return nil
}

type UsageCostStatus int64

const (
	// These values are persisted in usage_facts and require a migration to change.
	UsageCostStatusUnknown   UsageCostStatus = 0
	UsageCostStatusEstimated UsageCostStatus = 1
	UsageCostStatusPartial   UsageCostStatus = 2
)

func (status UsageCostStatus) String() string {
	switch status {
	case UsageCostStatusUnknown:
		return "unknown"
	case UsageCostStatusEstimated:
		return "estimated"
	case UsageCostStatusPartial:
		return "partial"
	default:
		return ""
	}
}

func (status UsageCostStatus) valid() bool {
	return status >= UsageCostStatusUnknown && status <= UsageCostStatusPartial
}

type UsageReportedCostStatus int64

const (
	// These values are persisted in usage_facts and require a migration to change.
	UsageReportedCostStatusUnknown  UsageReportedCostStatus = 0
	UsageReportedCostStatusReported UsageReportedCostStatus = 1
	UsageReportedCostStatusPartial  UsageReportedCostStatus = 2
)

func (status UsageReportedCostStatus) String() string {
	switch status {
	case UsageReportedCostStatusUnknown:
		return "unknown"
	case UsageReportedCostStatusReported:
		return "reported"
	case UsageReportedCostStatusPartial:
		return "partial"
	default:
		return ""
	}
}

type UsageSource struct {
	ID                    int64
	ProviderID            string
	SourceKey             string
	IdentityRevision      int64
	SyncGeneration        int64
	CompletedGeneration   int64
	LastCompletedAtUnixMS int64
	TrackedUnits          int64
	InvalidRecords        int64
	UnsupportedRecords    int64
}

type CreateUsageFactParams struct {
	EventKey                 UsageKey
	SourceID                 int64
	SessionKey               string
	ModelKey                 string
	OccurredAtUnixMS         int64
	InputTokens              int64
	CachedInputTokens        int64
	OutputTokens             int64
	TotalTokens              int64
	EstimatedCostMicros      *int64
	CostStatus               UsageCostStatus
	ReportedCostUSDTicks     *int64
	ReportedCostStatus       UsageReportedCostStatus
	PricingCatalogVersion    *int64
	CacheWriteInputTokens    *int64
	CacheCreationInputTokens *int64
}

type UsageInsertResult struct {
	Inserted   int
	Duplicates int
}

type InsertUsageFactsParams struct {
	SourceID   int64
	Generation int64
	Facts      []CreateUsageFactParams
}

type CompleteUsageSyncParams struct {
	SourceID          int64
	Generation        int64
	CompletedAtUnixMS int64
	Finalization      UsageSyncFinalization
	Observations      []UsageImportObservation
}

type UsageImportObservationStatus string

const (
	UsageImportObservationHistoryChanged UsageImportObservationStatus = "history_changed"
	UsageImportObservationUnavailable    UsageImportObservationStatus = "unavailable"
	UsageImportObservationFactConflict   UsageImportObservationStatus = "fact_conflict"
)

type UsageImportObservation struct {
	SourceID        int64
	FileKey         UsageKey
	MetadataDigest  UsageKey
	ParserRevision  int64
	Status          UsageImportObservationStatus
	UpdatedAtUnixMS int64
}

// UsageSyncFinalization is sealed so provider-specific checkpoints remain
// owned by Store while sharing the generation-gated completion transaction.
type UsageSyncFinalization interface {
	validateUsageSyncFinalization() error
	applyUsageSyncFinalization(context.Context, *Store, UsageSource) (usageSyncFinalizationResult, error)
}

type usageSyncFinalizationResult struct {
	trackedUnits       int64
	invalidRecords     int64
	unsupportedRecords int64
}

// StaticUsageSyncFinalization records counters for an Integration that has no
// provider-specific checkpoint work to complete.
type StaticUsageSyncFinalization struct {
	TrackedUnits       int64
	InvalidRecords     int64
	UnsupportedRecords int64
}

func (finalization *StaticUsageSyncFinalization) validateUsageSyncFinalization() error {
	if finalization == nil || finalization.TrackedUnits < 0 || finalization.InvalidRecords < 0 || finalization.UnsupportedRecords < 0 {
		return errors.New("usage sync finalization is invalid")
	}
	return nil
}

func (finalization *StaticUsageSyncFinalization) applyUsageSyncFinalization(
	context.Context,
	*Store,
	UsageSource,
) (usageSyncFinalizationResult, error) {
	if err := finalization.validateUsageSyncFinalization(); err != nil {
		return usageSyncFinalizationResult{}, err
	}
	return usageSyncFinalizationResult{
		trackedUnits:       finalization.TrackedUnits,
		invalidRecords:     finalization.InvalidRecords,
		unsupportedRecords: finalization.UnsupportedRecords,
	}, nil
}

type UsageUnknownCostModel struct {
	SourceID int64
	ModelID  int64
	Model    string
}

type UsageFactCostCandidate struct {
	ID                       int64
	OccurredAtUnixMS         int64
	InputTokens              int64
	CachedInputTokens        int64
	CacheWriteInputTokens    *int64
	CacheCreationInputTokens *int64
	OutputTokens             int64
	TotalTokens              int64
}

type UpdateUsageFactCostParams struct {
	ID                    int64
	EstimatedCostMicros   int64
	CostStatus            UsageCostStatus
	PricingCatalogVersion *int64
}

type usageFactSessionPolicy int

const (
	usageFactSessionStrict usageFactSessionPolicy = iota
	usageFactSessionCanonicalAlias
)

func withUsageTransactionResult[T any](
	ctx context.Context,
	store *Store,
	operation func(*Store) (T, error),
) (T, error) {
	if store.transactional {
		return operation(store)
	}
	var result T
	err := store.WithTransaction(ctx, func(txStore *Store) error {
		var operationErr error
		result, operationErr = operation(txStore)
		return operationErr
	})
	return result, err
}

func (s *Store) BeginUsageSync(ctx context.Context, providerID, sourceKey string, identityRevision int64) (UsageSource, error) {
	providerID = strings.TrimSpace(providerID)
	sourceKey = strings.TrimSpace(sourceKey)
	if providerID == "" || sourceKey == "" || identityRevision <= 0 {
		return UsageSource{}, errors.New("usage source is invalid")
	}
	return withUsageTransactionResult(ctx, s, func(txStore *Store) (UsageSource, error) {
		return txStore.beginUsageSync(ctx, providerID, sourceKey, identityRevision)
	})
}

func (s *Store) beginUsageSync(ctx context.Context, providerID, sourceKey string, identityRevision int64) (UsageSource, error) {
	if err := s.reserveUsageProviderStateForWrite(ctx, providerID); err != nil {
		return UsageSource{}, err
	}
	source, err := s.prepareUsageSource(ctx, providerID, sourceKey, identityRevision)
	if err != nil {
		return UsageSource{}, err
	}
	return s.advanceUsageSyncGeneration(ctx, source.ID)
}

func (s *Store) reserveUsageProviderStateForWrite(ctx context.Context, providerID string) error {
	// Reserve SQLite's single writer before creating or advancing a source. This
	// orders Provider deletion before or after the whole source transaction.
	result, err := s.executor().ExecContext(ctx, `
		UPDATE providers SET id = id WHERE id = ?
	`, providerID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrUsageProviderMissing
	}
	return nil
}

func (s *Store) prepareUsageSource(ctx context.Context, providerID, sourceKey string, identityRevision int64) (UsageSource, error) {
	if _, err := s.executor().ExecContext(ctx, `
		INSERT INTO usage_sources (provider_id, source_key, identity_revision)
		VALUES (?, ?, ?)
		ON CONFLICT(provider_id, source_key) DO NOTHING
	`, providerID, sourceKey, identityRevision); err != nil {
		return UsageSource{}, err
	}

	source, err := s.getUsageSource(ctx, providerID, sourceKey)
	if err != nil {
		return UsageSource{}, err
	}
	// Runtime sync must never reinterpret stored fact identity; revisions move
	// only through an explicit database migration.
	if source.IdentityRevision != identityRevision {
		return UsageSource{}, ErrUsageIdentityRevision
	}
	return source, nil
}

func (s *Store) advanceUsageSyncGeneration(ctx context.Context, sourceID int64) (UsageSource, error) {
	if _, err := s.executor().ExecContext(ctx, `
		UPDATE usage_sources
		SET sync_generation = sync_generation + 1
		WHERE id = ?
	`, sourceID); err != nil {
		return UsageSource{}, err
	}
	return s.getUsageSourceByID(ctx, sourceID)
}

func (s *Store) GetUsageSource(ctx context.Context, providerID, sourceKey string) (UsageSource, error) {
	return s.getUsageSource(ctx, strings.TrimSpace(providerID), strings.TrimSpace(sourceKey))
}

func (s *Store) getUsageSource(ctx context.Context, providerID, sourceKey string) (UsageSource, error) {
	row := s.executor().QueryRowContext(ctx, `
		SELECT id, provider_id, source_key, identity_revision, sync_generation,
			completed_generation, last_completed_at_unix_ms, tracked_units,
			invalid_records, unsupported_records
		FROM usage_sources
		WHERE provider_id = ? AND source_key = ?
	`, providerID, sourceKey)
	return scanUsageSource(row)
}

func (s *Store) getUsageSourceByID(ctx context.Context, sourceID int64) (UsageSource, error) {
	row := s.executor().QueryRowContext(ctx, `
		SELECT id, provider_id, source_key, identity_revision, sync_generation,
			completed_generation, last_completed_at_unix_ms, tracked_units,
			invalid_records, unsupported_records
		FROM usage_sources
		WHERE id = ?
	`, sourceID)
	return scanUsageSource(row)
}

func scanUsageSource(row rowScanner) (UsageSource, error) {
	var source UsageSource
	if err := row.Scan(
		&source.ID,
		&source.ProviderID,
		&source.SourceKey,
		&source.IdentityRevision,
		&source.SyncGeneration,
		&source.CompletedGeneration,
		&source.LastCompletedAtUnixMS,
		&source.TrackedUnits,
		&source.InvalidRecords,
		&source.UnsupportedRecords,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return UsageSource{}, ErrNotFound
		}
		return UsageSource{}, err
	}
	return source, nil
}

// InsertUsageFacts atomically inserts idempotent facts for the current source
// generation. It does not advance a checkpoint or complete the generation.
func (s *Store) InsertUsageFacts(ctx context.Context, params InsertUsageFactsParams) (UsageInsertResult, error) {
	if err := validateUsageFactBatch(params); err != nil {
		return UsageInsertResult{}, err
	}
	if len(params.Facts) == 0 {
		return UsageInsertResult{}, nil
	}
	return withUsageTransactionResult(ctx, s, func(txStore *Store) (UsageInsertResult, error) {
		return txStore.insertUsageFactsForWrite(ctx, params)
	})
}

func validateUsageFactBatch(params InsertUsageFactsParams) error {
	if params.SourceID <= 0 || params.Generation <= 0 {
		return errors.New("usage fact batch is invalid")
	}
	for _, fact := range params.Facts {
		if fact.SourceID != params.SourceID {
			return errors.New("usage facts must belong to the sync source")
		}
	}
	return nil
}

func (s *Store) insertUsageFactsForWrite(ctx context.Context, params InsertUsageFactsParams) (UsageInsertResult, error) {
	if _, err := s.reserveCurrentUsageSyncForWrite(ctx, params.SourceID, params.Generation); err != nil {
		return UsageInsertResult{}, err
	}
	return s.insertUsageFacts(ctx, params.Facts)
}

func (s *Store) insertUsageFacts(ctx context.Context, facts []CreateUsageFactParams) (UsageInsertResult, error) {
	return s.insertUsageFactsWithSessionPolicy(ctx, facts, usageFactSessionStrict)
}

func (s *Store) insertUsageFactsWithSessionPolicy(
	ctx context.Context,
	facts []CreateUsageFactParams,
	sessionPolicy usageFactSessionPolicy,
) (UsageInsertResult, error) {
	if sessionPolicy != usageFactSessionStrict && sessionPolicy != usageFactSessionCanonicalAlias {
		return UsageInsertResult{}, errors.New("usage fact session policy is invalid")
	}
	insertStmt, err := s.executor().PrepareContext(ctx, `
		INSERT INTO usage_facts (
			event_key, source_id, session_id, model_id, occurred_at_unix_ms,
			input_tokens, cached_input_tokens, output_tokens, total_tokens,
			estimated_cost_micros, cost_status, reported_cost_usd_ticks, reported_cost_status,
			pricing_catalog_version, cache_write_input_tokens, cache_creation_input_tokens
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(event_key) DO NOTHING
	`)
	if err != nil {
		return UsageInsertResult{}, err
	}
	defer insertStmt.Close()

	canonicalObservationStmt, err := s.executor().PrepareContext(ctx, `
		UPDATE usage_facts
		SET session_id = ?, model_id = ?, occurred_at_unix_ms = ?
		WHERE id = ?
	`)
	if err != nil {
		return UsageInsertResult{}, err
	}
	defer canonicalObservationStmt.Close()

	costUpgradeStmt, err := s.executor().PrepareContext(ctx, `
		UPDATE usage_facts
		SET estimated_cost_micros = ?, cost_status = ?, pricing_catalog_version = ?
		WHERE id = ? AND cost_status = ?
	`)
	if err != nil {
		return UsageInsertResult{}, err
	}
	defer costUpgradeStmt.Close()

	reportedCostUpgradeStmt, err := s.executor().PrepareContext(ctx, `
		UPDATE usage_facts
		SET reported_cost_usd_ticks = ?, reported_cost_status = ?
		WHERE id = ? AND reported_cost_status = ?
	`)
	if err != nil {
		return UsageInsertResult{}, err
	}
	defer reportedCostUpgradeStmt.Close()

	sessionIDs := make(map[string]int64)
	modelIDs := make(map[string]int64)
	result := UsageInsertResult{}
	for _, fact := range facts {
		if err := validateUsageFact(fact); err != nil {
			return UsageInsertResult{}, err
		}
		sessionID, err := s.resolveUsageSessionID(ctx, fact.SourceID, fact.SessionKey, sessionIDs)
		if err != nil {
			return UsageInsertResult{}, err
		}
		modelKey := NormalizeUsageModelKey(fact.ModelKey)
		modelID, err := s.resolveUsageModelID(ctx, fact.SourceID, modelKey, modelIDs)
		if err != nil {
			return UsageInsertResult{}, err
		}
		status, cost, err := usageCostStorageValues(fact.CostStatus, fact.EstimatedCostMicros)
		if err != nil {
			return UsageInsertResult{}, err
		}
		reportedStatus, reportedCost, err := usageReportedCostStorageValues(
			fact.ReportedCostStatus,
			fact.ReportedCostUSDTicks,
		)
		if err != nil {
			return UsageInsertResult{}, err
		}

		insert, err := insertStmt.ExecContext(
			ctx,
			fact.EventKey,
			fact.SourceID,
			nullableUsageDimensionID(sessionID),
			modelID,
			fact.OccurredAtUnixMS,
			fact.InputTokens,
			fact.CachedInputTokens,
			fact.OutputTokens,
			fact.TotalTokens,
			cost,
			status,
			reportedCost,
			reportedStatus,
			fact.PricingCatalogVersion,
			fact.CacheWriteInputTokens,
			fact.CacheCreationInputTokens,
		)
		if err != nil {
			return UsageInsertResult{}, err
		}
		rows, err := insert.RowsAffected()
		if err != nil {
			return UsageInsertResult{}, err
		}
		if rows > 0 {
			result.Inserted++
			continue
		}

		var existingID, existingSourceID, existingOccurredAtUnixMS int64
		var existingSessionID, existingReportedCost sql.NullInt64
		var existingSessionKey string
		var inputTokens, cachedInputTokens, outputTokens, totalTokens, existingReportedStatus int64
		if err := s.executor().QueryRowContext(ctx, `
			SELECT facts.id, facts.source_id, facts.session_id,
				COALESCE(sessions.session_key, ''), facts.occurred_at_unix_ms,
				facts.input_tokens, facts.cached_input_tokens, facts.output_tokens, facts.total_tokens,
				facts.reported_cost_usd_ticks, facts.reported_cost_status
			FROM usage_facts AS facts
			LEFT JOIN usage_sessions AS sessions
				ON sessions.source_id = facts.source_id AND sessions.id = facts.session_id
			WHERE facts.event_key = ?
		`, fact.EventKey).Scan(
			&existingID,
			&existingSourceID,
			&existingSessionID,
			&existingSessionKey,
			&existingOccurredAtUnixMS,
			&inputTokens,
			&cachedInputTokens,
			&outputTokens,
			&totalTokens,
			&existingReportedCost,
			&existingReportedStatus,
		); err != nil {
			return UsageInsertResult{}, err
		}
		sessionMatches := sameUsageDimensionID(existingSessionID, sessionID)
		if existingSourceID != fact.SourceID ||
			(sessionPolicy == usageFactSessionStrict && !sessionMatches) ||
			inputTokens != fact.InputTokens || cachedInputTokens != fact.CachedInputTokens ||
			outputTokens != fact.OutputTokens || totalTokens != fact.TotalTokens {
			return UsageInsertResult{}, ErrUsageFactConflict
		}
		if existingReportedStatus != int64(UsageReportedCostStatusUnknown) &&
			reportedStatus != UsageReportedCostStatusUnknown &&
			(!existingReportedCost.Valid || fact.ReportedCostUSDTicks == nil ||
				existingReportedCost.Int64 != *fact.ReportedCostUSDTicks ||
				existingReportedStatus != int64(reportedStatus)) {
			return UsageInsertResult{}, ErrUsageFactConflict
		}

		replaceObservation := fact.OccurredAtUnixMS > 0 &&
			(existingOccurredAtUnixMS == 0 || fact.OccurredAtUnixMS < existingOccurredAtUnixMS)
		if sessionPolicy == usageFactSessionCanonicalAlias {
			if fact.SessionKey == "" || existingSessionKey == "" {
				return UsageInsertResult{}, ErrUsageFactConflict
			}
			replaceObservation = canonicalUsageObservationBefore(
				fact.OccurredAtUnixMS,
				fact.SessionKey,
				existingOccurredAtUnixMS,
				existingSessionKey,
			)
		}
		if replaceObservation {
			// Grok Build forks copy completed turns into a new session. Keep one
			// deterministic observation so file discovery order cannot change
			// report grouping, while cost classification remains monotonic.
			if _, err := canonicalObservationStmt.ExecContext(
				ctx,
				nullableUsageDimensionID(sessionID),
				modelID,
				fact.OccurredAtUnixMS,
				existingID,
			); err != nil {
				return UsageInsertResult{}, err
			}
		}
		if sessionPolicy == usageFactSessionStrict && status != UsageCostStatusUnknown {
			if _, err := costUpgradeStmt.ExecContext(
				ctx,
				cost,
				status,
				fact.PricingCatalogVersion,
				existingID,
				UsageCostStatusUnknown,
			); err != nil {
				return UsageInsertResult{}, err
			}
		}
		if reportedStatus != UsageReportedCostStatusUnknown {
			if _, err := reportedCostUpgradeStmt.ExecContext(
				ctx,
				reportedCost,
				reportedStatus,
				existingID,
				UsageReportedCostStatusUnknown,
			); err != nil {
				return UsageInsertResult{}, err
			}
		}
		result.Duplicates++
	}
	return result, nil
}

func canonicalUsageObservationBefore(
	candidateTime int64,
	candidateSession string,
	existingTime int64,
	existingSession string,
) bool {
	switch {
	case candidateTime > 0 && existingTime == 0:
		return true
	case candidateTime == 0 && existingTime > 0:
		return false
	case candidateTime > 0 && existingTime > 0 && candidateTime != existingTime:
		return candidateTime < existingTime
	default:
		return candidateSession < existingSession
	}
}

func validateUsageFact(fact CreateUsageFactParams) error {
	if fact.EventKey.IsZero() || fact.SourceID <= 0 || fact.OccurredAtUnixMS < 0 || fact.InputTokens < 0 ||
		fact.CachedInputTokens < 0 || fact.CachedInputTokens > fact.InputTokens ||
		fact.OutputTokens < 0 || fact.TotalTokens < 0 {
		return errors.New("usage fact is invalid")
	}
	if fact.PricingCatalogVersion != nil && *fact.PricingCatalogVersion <= 0 ||
		fact.CacheWriteInputTokens != nil && (*fact.CacheWriteInputTokens < 0 || *fact.CacheWriteInputTokens > fact.InputTokens) ||
		fact.CacheCreationInputTokens != nil && (*fact.CacheCreationInputTokens < 0 || *fact.CacheCreationInputTokens > fact.InputTokens) {
		return errors.New("usage fact pricing is invalid")
	}
	if fact.SessionKey != "" && !validUsageSessionKey(fact.SessionKey) {
		return errors.New("usage session key is invalid")
	}
	return nil
}

func (s *Store) resolveUsageSessionID(ctx context.Context, sourceID int64, sessionKey string, cache map[string]int64) (int64, error) {
	if sessionKey == "" {
		return 0, nil
	}
	return s.resolveUsageDimensionID(
		ctx,
		sourceID,
		sessionKey,
		cache,
		`
		INSERT INTO usage_sessions (source_id, session_key)
		VALUES (?, ?)
		ON CONFLICT(source_id, session_key) DO NOTHING
		`,
		`SELECT id FROM usage_sessions WHERE source_id = ? AND session_key = ?`,
	)
}

func (s *Store) resolveUsageModelID(ctx context.Context, sourceID int64, modelKey string, cache map[string]int64) (int64, error) {
	return s.resolveUsageDimensionID(
		ctx,
		sourceID,
		modelKey,
		cache,
		`
		INSERT INTO usage_models (source_id, model_key)
		VALUES (?, ?)
		ON CONFLICT(source_id, model_key) DO NOTHING
		`,
		`SELECT id FROM usage_models WHERE source_id = ? AND model_key = ?`,
	)
}

func (s *Store) resolveUsageDimensionID(
	ctx context.Context,
	sourceID int64,
	key string,
	cache map[string]int64,
	insertQuery string,
	selectQuery string,
) (int64, error) {
	cacheKey := fmt.Sprintf("%d\x00%s", sourceID, key)
	if id, ok := cache[cacheKey]; ok {
		return id, nil
	}
	if _, err := s.executor().ExecContext(ctx, insertQuery, sourceID, key); err != nil {
		return 0, err
	}
	var id int64
	if err := s.executor().QueryRowContext(ctx, selectQuery, sourceID, key).Scan(&id); err != nil {
		return 0, err
	}
	cache[cacheKey] = id
	return id, nil
}

func nullableUsageDimensionID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

func sameUsageDimensionID(existing sql.NullInt64, expected int64) bool {
	if expected == 0 {
		return !existing.Valid
	}
	return existing.Valid && existing.Int64 == expected
}

// NormalizeUsageModelKey returns a safe, trimmed storage label and maps empty
// or unsafe labels to UsageUnknownModelKey.
func NormalizeUsageModelKey(modelKey string) string {
	modelKey = strings.TrimSpace(modelKey)
	if len(modelKey) == 0 || len(modelKey) > 200 {
		return UsageUnknownModelKey
	}
	for _, character := range modelKey {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' {
			continue
		}
		switch character {
		case '.', '_', '-', ':', '/', '@':
			continue
		default:
			return UsageUnknownModelKey
		}
	}
	return modelKey
}

func validUsageSessionKey(sessionKey string) bool {
	return len(sessionKey) >= 1 && len(sessionKey) <= 256
}

func usageCostStorageValues(status UsageCostStatus, estimatedCostMicros *int64) (UsageCostStatus, any, error) {
	switch status {
	case UsageCostStatusUnknown:
		if estimatedCostMicros != nil {
			return 0, nil, errors.New("unknown usage cost must not include an estimate")
		}
		return UsageCostStatusUnknown, nil, nil
	case UsageCostStatusEstimated:
		if estimatedCostMicros == nil || *estimatedCostMicros < 0 {
			return 0, nil, errors.New("estimated usage cost is invalid")
		}
		return UsageCostStatusEstimated, *estimatedCostMicros, nil
	case UsageCostStatusPartial:
		if estimatedCostMicros == nil || *estimatedCostMicros < 0 {
			return 0, nil, errors.New("partial usage cost is invalid")
		}
		return UsageCostStatusPartial, *estimatedCostMicros, nil
	default:
		return 0, nil, errors.New("usage cost status is invalid")
	}
}

func usageReportedCostStorageValues(
	status UsageReportedCostStatus,
	reportedCostUSDTicks *int64,
) (UsageReportedCostStatus, any, error) {
	switch status {
	case UsageReportedCostStatusUnknown:
		if reportedCostUSDTicks != nil {
			return 0, nil, errors.New("unknown reported usage cost must not include ticks")
		}
		return UsageReportedCostStatusUnknown, nil, nil
	case UsageReportedCostStatusReported, UsageReportedCostStatusPartial:
		if reportedCostUSDTicks == nil || *reportedCostUSDTicks <= 0 {
			return 0, nil, errors.New("reported usage cost is invalid")
		}
		return status, *reportedCostUSDTicks, nil
	default:
		return 0, nil, errors.New("reported usage cost status is invalid")
	}
}

func (s *Store) CompleteUsageSync(ctx context.Context, params CompleteUsageSyncParams) error {
	if err := validateUsageSyncCompletion(params); err != nil {
		return err
	}
	if s.transactional {
		return s.completeUsageSync(ctx, params)
	}
	return s.WithTransaction(ctx, func(txStore *Store) error {
		return txStore.completeUsageSync(ctx, params)
	})
}

func (s *Store) completeUsageSync(ctx context.Context, params CompleteUsageSyncParams) error {
	source, err := s.reserveCurrentUsageSyncForWrite(ctx, params.SourceID, params.Generation)
	if err != nil {
		return err
	}
	result, err := params.Finalization.applyUsageSyncFinalization(ctx, s, source)
	if err != nil {
		return err
	}
	if result.trackedUnits < 0 || result.invalidRecords < 0 || result.unsupportedRecords < 0 {
		return errors.New("usage sync finalization is invalid")
	}
	if err := s.upsertUsageImportObservations(ctx, source, params.Observations); err != nil {
		return err
	}
	return s.updateUsageSyncCompletion(ctx, params, result)
}

func (s *Store) reserveCurrentUsageSyncForWrite(ctx context.Context, sourceID, generation int64) (UsageSource, error) {
	// Reserve SQLite's writer and validate generation in one statement so a
	// newer sync cannot begin between the ownership check and the guarded write.
	gate, err := s.executor().ExecContext(ctx, `
		UPDATE usage_sources SET sync_generation = sync_generation
		WHERE id = ? AND sync_generation = ?
	`, sourceID, generation)
	if err != nil {
		return UsageSource{}, err
	}
	matched, err := gate.RowsAffected()
	if err != nil {
		return UsageSource{}, err
	}
	if matched != 1 {
		return UsageSource{}, ErrUsageSyncSuperseded
	}
	return s.getUsageSourceByID(ctx, sourceID)
}

func validateUsageSyncCompletion(params CompleteUsageSyncParams) error {
	if params.SourceID <= 0 || params.Generation <= 0 || params.CompletedAtUnixMS < 0 || params.Finalization == nil {
		return errors.New("usage sync completion is invalid")
	}
	if err := params.Finalization.validateUsageSyncFinalization(); err != nil {
		return err
	}
	for _, observation := range params.Observations {
		if observation.SourceID != params.SourceID || observation.FileKey.IsZero() ||
			observation.MetadataDigest.IsZero() || observation.ParserRevision <= 0 ||
			!observation.Status.valid() ||
			observation.UpdatedAtUnixMS < 0 {
			return errors.New("usage import observation is invalid")
		}
	}
	return nil
}

func (s *Store) updateUsageSyncCompletion(
	ctx context.Context,
	params CompleteUsageSyncParams,
	result usageSyncFinalizationResult,
) error {
	completedAt := params.CompletedAtUnixMS
	if completedAt == 0 {
		completedAt = time.Now().UnixMilli()
	}
	update, err := s.executor().ExecContext(ctx, `
		UPDATE usage_sources
		SET completed_generation = ?, last_completed_at_unix_ms = ?, tracked_units = ?,
			invalid_records = ?, unsupported_records = ?
		WHERE id = ? AND sync_generation = ?
	`, params.Generation, completedAt, result.trackedUnits, result.invalidRecords, result.unsupportedRecords, params.SourceID, params.Generation)
	if err != nil {
		return err
	}
	affected, err := update.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrUsageSyncSuperseded
	}
	return nil
}

func (s *Store) ListUsageImportObservations(ctx context.Context, sourceID int64) ([]UsageImportObservation, error) {
	if sourceID <= 0 {
		return nil, errors.New("usage import observation query is invalid")
	}
	rows, err := s.executor().QueryContext(ctx, `
		SELECT source_id, file_key, metadata_digest, parser_revision, status,
			updated_at_unix_ms
		FROM usage_import_observations
		WHERE source_id = ?
		ORDER BY file_key
	`, sourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	observations := make([]UsageImportObservation, 0)
	for rows.Next() {
		var observation UsageImportObservation
		if err := rows.Scan(
			&observation.SourceID,
			&observation.FileKey,
			&observation.MetadataDigest,
			&observation.ParserRevision,
			&observation.Status,
			&observation.UpdatedAtUnixMS,
		); err != nil {
			return nil, err
		}
		observations = append(observations, observation)
	}
	return observations, rows.Err()
}

func (s *Store) upsertUsageImportObservations(
	ctx context.Context,
	source UsageSource,
	observations []UsageImportObservation,
) error {
	if len(observations) == 0 {
		return nil
	}
	stmt, err := s.executor().PrepareContext(ctx, `
		INSERT INTO usage_import_observations (
			source_id, file_key, metadata_digest, parser_revision, status,
			updated_at_unix_ms
		) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(source_id, file_key) DO UPDATE SET
			metadata_digest = excluded.metadata_digest,
			parser_revision = excluded.parser_revision,
			status = excluded.status,
			updated_at_unix_ms = excluded.updated_at_unix_ms
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	now := time.Now().UnixMilli()
	for _, observation := range observations {
		if observation.SourceID != source.ID || observation.FileKey.IsZero() ||
			observation.MetadataDigest.IsZero() || observation.ParserRevision <= 0 ||
			!observation.Status.valid() ||
			observation.UpdatedAtUnixMS < 0 {
			return errors.New("usage import observation is invalid")
		}
		updatedAt := observation.UpdatedAtUnixMS
		if updatedAt == 0 {
			updatedAt = now
		}
		if _, err := stmt.ExecContext(
			ctx,
			observation.SourceID,
			observation.FileKey,
			observation.MetadataDigest,
			observation.ParserRevision,
			observation.Status,
			updatedAt,
		); err != nil {
			return err
		}
	}
	return nil
}

func (status UsageImportObservationStatus) valid() bool {
	switch status {
	case UsageImportObservationHistoryChanged,
		UsageImportObservationUnavailable,
		UsageImportObservationFactConflict:
		return true
	default:
		return false
	}
}

func (s *Store) deleteUsageImportObservation(ctx context.Context, sourceID int64, fileKey UsageKey) error {
	_, err := s.executor().ExecContext(ctx, `
		DELETE FROM usage_import_observations WHERE source_id = ? AND file_key = ?
	`, sourceID, fileKey)
	return err
}

func (s *Store) deleteMissingUsageImportObservations(
	ctx context.Context,
	sourceID int64,
	discovered map[UsageKey]struct{},
) error {
	rows, err := s.executor().QueryContext(ctx, `
		SELECT file_key FROM usage_import_observations WHERE source_id = ?
	`, sourceID)
	if err != nil {
		return err
	}
	var stale []UsageKey
	for rows.Next() {
		var fileKey UsageKey
		if err := rows.Scan(&fileKey); err != nil {
			_ = rows.Close()
			return err
		}
		if _, ok := discovered[fileKey]; !ok {
			stale = append(stale, fileKey)
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, fileKey := range stale {
		if err := s.deleteUsageImportObservation(ctx, sourceID, fileKey); err != nil {
			return err
		}
	}
	return nil
}

// ListUnknownUsageCostModels returns one dimension row per model that still has
// unknown costs. The owning Integration decides which labels it can price.
func (s *Store) ListUnknownUsageCostModels(ctx context.Context, providerID string) ([]UsageUnknownCostModel, error) {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return nil, errors.New("usage cost model query is invalid")
	}
	rows, err := s.executor().QueryContext(ctx, `
		SELECT m.source_id, m.id, m.model_key
		FROM usage_models m
		JOIN usage_sources s ON s.id = m.source_id
		WHERE s.provider_id = ? AND EXISTS (
			SELECT 1
			FROM usage_facts f INDEXED BY idx_usage_facts_source_cost_model_id
			WHERE f.source_id = m.source_id AND f.cost_status = ? AND f.model_id = m.id
			LIMIT 1
		)
		ORDER BY m.source_id ASC, m.id ASC
	`, providerID, UsageCostStatusUnknown)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	models := make([]UsageUnknownCostModel, 0)
	for rows.Next() {
		var model UsageUnknownCostModel
		if err := rows.Scan(&model.SourceID, &model.ModelID, &model.Model); err != nil {
			return nil, err
		}
		models = append(models, model)
	}
	return models, rows.Err()
}

func (s *Store) HasUnknownUsageFactCostInPeriod(ctx context.Context, sourceID, modelID, fromUnixMS, untilUnixMS int64) (bool, error) {
	if sourceID <= 0 || modelID <= 0 || untilUnixMS <= fromUnixMS {
		return false, errors.New("usage cost period query is invalid")
	}
	var found int
	err := s.executor().QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM usage_facts INDEXED BY idx_usage_facts_source_cost_model_id
			WHERE source_id = ? AND cost_status = ? AND model_id = ?
				AND occurred_at_unix_ms >= ? AND occurred_at_unix_ms < ?
			LIMIT 1
		)
	`, sourceID, UsageCostStatusUnknown, modelID, fromUnixMS, untilUnixMS).Scan(&found)
	return found != 0, err
}

// ListUnknownUsageFactCostCandidates pages facts for one model dimension so
// unsupported models never cause every historical fact to be read again.
func (s *Store) ListUnknownUsageFactCostCandidates(
	ctx context.Context,
	providerID string,
	sourceID int64,
	modelID int64,
	afterID int64,
	limit int,
) ([]UsageFactCostCandidate, error) {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" || sourceID <= 0 || modelID <= 0 || afterID < 0 || limit <= 0 || limit > 1_000 {
		return nil, errors.New("usage cost candidate query is invalid")
	}

	rows, err := s.executor().QueryContext(ctx, `
		SELECT f.id, f.occurred_at_unix_ms, f.input_tokens, f.cached_input_tokens,
			f.cache_write_input_tokens, f.cache_creation_input_tokens, f.output_tokens, f.total_tokens
		FROM usage_facts f INDEXED BY idx_usage_facts_source_cost_model_id
		JOIN usage_sources s ON s.id = f.source_id
		WHERE s.provider_id = ? AND f.source_id = ? AND f.cost_status = ?
			AND f.model_id = ? AND f.id > ?
		ORDER BY f.id ASC
		LIMIT ?
	`, providerID, sourceID, UsageCostStatusUnknown, modelID, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	candidates := make([]UsageFactCostCandidate, 0)
	for rows.Next() {
		var candidate UsageFactCostCandidate
		if err := rows.Scan(
			&candidate.ID,
			&candidate.OccurredAtUnixMS,
			&candidate.InputTokens,
			&candidate.CachedInputTokens,
			&candidate.CacheWriteInputTokens,
			&candidate.CacheCreationInputTokens,
			&candidate.OutputTokens,
			&candidate.TotalTokens,
		); err != nil {
			return nil, err
		}
		candidates = append(candidates, candidate)
	}
	return candidates, rows.Err()
}

func (s *Store) UpdateUnknownUsageFactCosts(ctx context.Context, providerID string, updates []UpdateUsageFactCostParams) (int, error) {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return 0, errors.New("usage fact cost update is invalid")
	}
	if len(updates) == 0 {
		return 0, nil
	}
	return withUsageTransactionResult(ctx, s, func(txStore *Store) (int, error) {
		return txStore.updateUnknownUsageFactCosts(ctx, providerID, updates)
	})
}

func (s *Store) updateUnknownUsageFactCosts(ctx context.Context, providerID string, updates []UpdateUsageFactCostParams) (int, error) {
	if err := s.reserveUsageProviderStateForWrite(ctx, providerID); err != nil {
		return 0, err
	}
	stmt, err := s.executor().PrepareContext(ctx, `
		UPDATE usage_facts
		SET estimated_cost_micros = ?, cost_status = ?, pricing_catalog_version = ?
		WHERE id = ? AND cost_status = ? AND source_id IN (
			SELECT id FROM usage_sources WHERE provider_id = ?
		)
	`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	updated := 0
	for _, item := range updates {
		if item.ID <= 0 || item.EstimatedCostMicros < 0 || !item.CostStatus.valid() || item.CostStatus == UsageCostStatusUnknown {
			return 0, errors.New("usage fact cost update is invalid")
		}
		result, err := stmt.ExecContext(ctx, item.EstimatedCostMicros, item.CostStatus, item.PricingCatalogVersion, item.ID, UsageCostStatusUnknown, providerID)
		if err != nil {
			return 0, err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return 0, err
		}
		updated += int(rows)
	}
	return updated, nil
}

func sqlPlaceholders(count int) string {
	if count <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", count), ",")
}
