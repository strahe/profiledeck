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

const usageKeySize = 32

// UsageUnknownModelKey is the persisted label for absent or unsafe models.
const UsageUnknownModelKey = "unknown"

var (
	ErrUsageCursorConflict   = errors.New("usage import cursor conflict")
	ErrUsageFactConflict     = errors.New("usage fact conflict")
	ErrUsageIdentityRevision = errors.New("usage identity revision requires migration")
	ErrUsageProviderDisabled = errors.New("usage provider is disabled")
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

type UsageSource struct {
	ID                    int64
	ProviderID            string
	SourceKey             string
	IdentityRevision      int64
	SyncGeneration        int64
	LastCompletedAtUnixMS int64
	TrackedUnits          int64
	InvalidRecords        int64
	UnsupportedRecords    int64
}

type CreateUsageFactParams struct {
	EventKey            UsageKey
	SourceID            int64
	SessionKey          string
	ModelKey            string
	OccurredAtUnixMS    int64
	InputTokens         int64
	CachedInputTokens   int64
	OutputTokens        int64
	TotalTokens         int64
	EstimatedCostMicros *int64
	CostStatus          UsageCostStatus
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
	ID                int64
	InputTokens       int64
	CachedInputTokens int64
	OutputTokens      int64
	TotalTokens       int64
}

type UpdateUsageFactCostParams struct {
	ID                  int64
	EstimatedCostMicros int64
	CostStatus          UsageCostStatus
}

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
	// Reserve SQLite's single writer before reading enabled state so a concurrent
	// Provider update is ordered before or after this usage write, never between.
	if _, err := s.executor().ExecContext(ctx, `
		UPDATE providers SET enabled = enabled WHERE id = ?
	`, providerID); err != nil {
		return err
	}
	return s.requireUsageProviderWritable(ctx, providerID)
}

func (s *Store) requireUsageProviderWritable(ctx context.Context, providerID string) error {
	var enabled int
	err := s.executor().QueryRowContext(ctx, `SELECT enabled FROM providers WHERE id = ?`, providerID).Scan(&enabled)
	if errors.Is(err, sql.ErrNoRows) {
		// Integration registration owns sync capability. A missing Provider row
		// means there is no persisted disablement, not that the Integration is absent.
		return nil
	}
	if err != nil {
		return err
	}
	if enabled != 1 {
		return ErrUsageProviderDisabled
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
			last_completed_at_unix_ms, tracked_units, invalid_records, unsupported_records
		FROM usage_sources
		WHERE provider_id = ? AND source_key = ?
	`, providerID, sourceKey)
	return scanUsageSource(row)
}

func (s *Store) getUsageSourceByID(ctx context.Context, sourceID int64) (UsageSource, error) {
	row := s.executor().QueryRowContext(ctx, `
		SELECT id, provider_id, source_key, identity_revision, sync_generation,
			last_completed_at_unix_ms, tracked_units, invalid_records, unsupported_records
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
	insertStmt, err := s.executor().PrepareContext(ctx, `
		INSERT INTO usage_facts (
			event_key, source_id, session_id, model_id, occurred_at_unix_ms,
			input_tokens, cached_input_tokens, output_tokens, total_tokens,
			estimated_cost_micros, cost_status
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(event_key) DO NOTHING
	`)
	if err != nil {
		return UsageInsertResult{}, err
	}
	defer insertStmt.Close()

	canonicalObservationStmt, err := s.executor().PrepareContext(ctx, `
		UPDATE usage_facts
		SET model_id = ?, occurred_at_unix_ms = ?
		WHERE id = ? AND ? > 0 AND (occurred_at_unix_ms = 0 OR occurred_at_unix_ms > ?)
	`)
	if err != nil {
		return UsageInsertResult{}, err
	}
	defer canonicalObservationStmt.Close()

	costUpgradeStmt, err := s.executor().PrepareContext(ctx, `
		UPDATE usage_facts
		SET estimated_cost_micros = ?, cost_status = ?
		WHERE id = ? AND cost_status = ?
	`)
	if err != nil {
		return UsageInsertResult{}, err
	}
	defer costUpgradeStmt.Close()

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

		var existingID, existingSourceID int64
		var existingSessionID sql.NullInt64
		var inputTokens, cachedInputTokens, outputTokens, totalTokens int64
		if err := s.executor().QueryRowContext(ctx, `
			SELECT id, source_id, session_id,
				input_tokens, cached_input_tokens, output_tokens, total_tokens
			FROM usage_facts
			WHERE event_key = ?
		`, fact.EventKey).Scan(
			&existingID,
			&existingSourceID,
			&existingSessionID,
			&inputTokens,
			&cachedInputTokens,
			&outputTokens,
			&totalTokens,
		); err != nil {
			return UsageInsertResult{}, err
		}
		if existingSourceID != fact.SourceID || !sameUsageDimensionID(existingSessionID, sessionID) ||
			inputTokens != fact.InputTokens || cachedInputTokens != fact.CachedInputTokens ||
			outputTokens != fact.OutputTokens || totalTokens != fact.TotalTokens {
			return UsageInsertResult{}, ErrUsageFactConflict
		}

		// Fork copies can carry a different timestamp, model spelling, or pricing
		// classification. Keep the earliest dated observation; an undated copy
		// cannot replace a known time. Pricing remains monotonic so observation order
		// cannot erase an already classified historical cost.
		if _, err := canonicalObservationStmt.ExecContext(
			ctx,
			modelID,
			fact.OccurredAtUnixMS,
			existingID,
			fact.OccurredAtUnixMS,
			fact.OccurredAtUnixMS,
		); err != nil {
			return UsageInsertResult{}, err
		}
		if status != UsageCostStatusUnknown {
			if _, err := costUpgradeStmt.ExecContext(
				ctx,
				cost,
				status,
				existingID,
				UsageCostStatusUnknown,
			); err != nil {
				return UsageInsertResult{}, err
			}
		}
		result.Duplicates++
	}
	return result, nil
}

func validateUsageFact(fact CreateUsageFactParams) error {
	if fact.EventKey.IsZero() || fact.SourceID <= 0 || fact.OccurredAtUnixMS < 0 || fact.InputTokens < 0 ||
		fact.CachedInputTokens < 0 || fact.CachedInputTokens > fact.InputTokens ||
		fact.OutputTokens < 0 || fact.TotalTokens < 0 {
		return errors.New("usage fact is invalid")
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
	source, err := s.getUsageSourceByID(ctx, sourceID)
	if err != nil {
		return UsageSource{}, err
	}
	if matched != 1 {
		return UsageSource{}, ErrUsageSyncSuperseded
	}
	if err := s.requireUsageProviderWritable(ctx, source.ProviderID); err != nil {
		return UsageSource{}, err
	}
	return source, nil
}

func validateUsageSyncCompletion(params CompleteUsageSyncParams) error {
	if params.SourceID <= 0 || params.Generation <= 0 || params.CompletedAtUnixMS < 0 || params.Finalization == nil {
		return errors.New("usage sync completion is invalid")
	}
	if err := params.Finalization.validateUsageSyncFinalization(); err != nil {
		return err
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
		SET last_completed_at_unix_ms = ?, tracked_units = ?,
			invalid_records = ?, unsupported_records = ?
		WHERE id = ? AND sync_generation = ?
	`, completedAt, result.trackedUnits, result.invalidRecords, result.unsupportedRecords, params.SourceID, params.Generation)
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
		SELECT f.id, f.input_tokens, f.cached_input_tokens, f.output_tokens, f.total_tokens
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
			&candidate.InputTokens,
			&candidate.CachedInputTokens,
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
		SET estimated_cost_micros = ?, cost_status = ?
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
		result, err := stmt.ExecContext(ctx, item.EstimatedCostMicros, item.CostStatus, item.ID, UsageCostStatusUnknown, providerID)
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
