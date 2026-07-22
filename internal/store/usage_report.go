package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

type UsageSummary struct {
	ProviderID              string
	Sources                 []string
	EventCount              int64
	InputTokens             int64
	CachedInputTokens       int64
	OutputTokens            int64
	TotalTokens             int64
	EstimatedCostMicros     int64
	UnknownCostEvents       int64
	PartialCostEvents       int64
	EstimatedCostEventCount int64
}

type UsageReportQuery struct {
	ProviderID  string
	StartUnixMS *int64
	EndUnixMS   int64
	Buckets     []UsageTimeBucket
}

type UsageTimeBucket struct {
	StartUnixMS int64
	EndUnixMS   int64
}

type UsageAggregate struct {
	EventCount              int64
	SessionCount            int64
	FreshInputTokens        int64
	InputTokens             int64
	CachedInputTokens       int64
	OutputTokens            int64
	TotalTokens             int64
	EstimatedCostMicros     int64
	EstimatedTokenCount     int64
	UnknownCostEvents       int64
	EstimatedCostEventCount int64
	PartialCostEventCount   int64
	UndatedEventCount       int64
}

type UsageTrendAggregate struct {
	BucketIndex int
	UsageAggregate
}

type UsageModelAggregate struct {
	Model string
	UsageAggregate
}

type UsageImportSummary struct {
	TrackedFiles       int64
	LastSyncedAtUnixMS int64
	InvalidLines       int64
	UnsupportedLines   int64
}

type UsageReportSnapshot struct {
	Sources       []string
	Summary       UsageAggregate
	Trend         []UsageTrendAggregate
	Models        []UsageModelAggregate
	ImportSummary UsageImportSummary
}

type usageProviderSources struct {
	IDs         []int64
	FactSources []string
}

func (s *Store) UsageSummary(ctx context.Context, providerID string) (UsageSummary, error) {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return UsageSummary{}, errors.New("usage provider id is required")
	}
	// Sources and aggregates must share one snapshot so an import cannot become
	// visible between the two reads.
	return withUsageTransactionResult(ctx, s, func(txStore *Store) (UsageSummary, error) {
		return txStore.usageSummary(ctx, providerID)
	})
}

func (s *Store) usageSummary(ctx context.Context, providerID string) (UsageSummary, error) {
	sources, err := s.queryUsageProviderSources(ctx, providerID)
	if err != nil {
		return UsageSummary{}, err
	}
	summary := UsageSummary{ProviderID: providerID, Sources: sources.FactSources}
	if len(sources.IDs) == 0 {
		return summary, nil
	}
	aggregate, err := s.queryUsageFactAggregate(ctx, sources.IDs, nil, 0)
	if err != nil {
		return UsageSummary{}, err
	}
	summary.EventCount = aggregate.EventCount
	summary.InputTokens = aggregate.InputTokens
	summary.CachedInputTokens = aggregate.CachedInputTokens
	summary.OutputTokens = aggregate.OutputTokens
	summary.TotalTokens = aggregate.TotalTokens
	summary.EstimatedCostMicros = aggregate.EstimatedCostMicros
	summary.UnknownCostEvents = aggregate.UnknownCostEvents
	summary.PartialCostEvents = aggregate.PartialCostEventCount
	summary.EstimatedCostEventCount = aggregate.EstimatedCostEventCount
	return summary, nil
}

func (s *Store) EarliestDatedUsageUnixMS(ctx context.Context, providerID string) (int64, error) {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return 0, errors.New("usage provider id is required")
	}
	sources, err := s.queryUsageProviderSources(ctx, providerID)
	if err != nil || len(sources.IDs) == 0 {
		return 0, err
	}
	where, args := usageSourceIDWhere("f", sources.IDs)
	var earliest sql.NullInt64
	if err := s.executor().QueryRowContext(ctx, `
		SELECT MIN(f.occurred_at_unix_ms)
		FROM usage_facts f
		WHERE `+where+` AND f.occurred_at_unix_ms > 0
	`, args...).Scan(&earliest); err != nil {
		return 0, err
	}
	if !earliest.Valid {
		return 0, nil
	}
	return earliest.Int64, nil
}

func (s *Store) UsageReport(ctx context.Context, query UsageReportQuery) (UsageReportSnapshot, error) {
	if err := validateUsageReportQuery(query); err != nil {
		return UsageReportSnapshot{}, err
	}
	// All report aggregates share one read transaction so concurrent imports
	// cannot produce a mixed-version report.
	return withUsageTransactionResult(ctx, s, func(txStore *Store) (UsageReportSnapshot, error) {
		return txStore.usageReport(ctx, query)
	})
}

func (s *Store) usageReport(ctx context.Context, query UsageReportQuery) (UsageReportSnapshot, error) {
	sources, err := s.queryUsageProviderSources(ctx, strings.TrimSpace(query.ProviderID))
	if err != nil {
		return UsageReportSnapshot{}, err
	}
	if len(sources.IDs) == 0 {
		return UsageReportSnapshot{
			Trend: zeroUsageTrend(len(query.Buckets)),
		}, nil
	}
	summary, err := s.queryUsageFactAggregate(ctx, sources.IDs, query.StartUnixMS, query.EndUnixMS)
	if err != nil {
		return UsageReportSnapshot{}, err
	}
	if query.StartUnixMS != nil {
		summary.UndatedEventCount, err = s.queryUsageUndatedFactCount(ctx, sources.IDs)
		if err != nil {
			return UsageReportSnapshot{}, err
		}
	}
	models, err := s.queryUsageFactModels(ctx, sources.IDs, query.StartUnixMS, query.EndUnixMS)
	if err != nil {
		return UsageReportSnapshot{}, err
	}
	trend, err := s.queryUsageFactTrend(ctx, sources.IDs, query.Buckets)
	if err != nil {
		return UsageReportSnapshot{}, err
	}
	importSummary, err := s.queryUsageSourceSummary(ctx, sources.IDs)
	if err != nil {
		return UsageReportSnapshot{}, err
	}
	return UsageReportSnapshot{
		Sources:       sources.FactSources,
		Summary:       summary,
		Trend:         trend,
		Models:        models,
		ImportSummary: importSummary,
	}, nil
}

func validateUsageReportQuery(query UsageReportQuery) error {
	if strings.TrimSpace(query.ProviderID) == "" {
		return errors.New("usage provider id is required")
	}
	if query.StartUnixMS != nil && query.EndUnixMS <= *query.StartUnixMS {
		return errors.New("usage report range is invalid")
	}
	if len(query.Buckets) > 512 {
		return errors.New("usage report has too many buckets")
	}
	for _, bucket := range query.Buckets {
		if bucket.StartUnixMS < 0 || bucket.EndUnixMS <= bucket.StartUnixMS {
			return errors.New("usage report bucket is invalid")
		}
	}
	return nil
}

func (s *Store) queryUsageProviderSources(ctx context.Context, providerID string) (usageProviderSources, error) {
	rows, err := s.executor().QueryContext(ctx, `
		SELECT s.id, s.source_key, EXISTS (
			SELECT 1 FROM usage_facts f WHERE f.source_id = s.id LIMIT 1
		)
		FROM usage_sources s
		WHERE s.provider_id = ?
		ORDER BY s.source_key ASC
	`, providerID)
	if err != nil {
		return usageProviderSources{}, err
	}
	defer rows.Close()

	var result usageProviderSources
	for rows.Next() {
		var id int64
		var sourceKey string
		var hasFacts bool
		if err := rows.Scan(&id, &sourceKey, &hasFacts); err != nil {
			return usageProviderSources{}, err
		}
		result.IDs = append(result.IDs, id)
		if hasFacts {
			result.FactSources = append(result.FactSources, sourceKey)
		}
	}
	return result, rows.Err()
}

func (s *Store) queryUsageFactAggregate(
	ctx context.Context,
	sourceIDs []int64,
	startUnixMS *int64,
	endUnixMS int64,
) (UsageAggregate, error) {
	where, args := usageSourceIDWhere("f", sourceIDs)
	if startUnixMS != nil {
		where += " AND f.occurred_at_unix_ms >= ? AND f.occurred_at_unix_ms < ?"
		args = append(args, *startUnixMS, endUnixMS)
	}
	undatedExpression := "COALESCE(SUM(CASE WHEN f.occurred_at_unix_ms = 0 THEN 1 ELSE 0 END), 0)"
	if startUnixMS != nil {
		undatedExpression = "0"
	}
	row := s.executor().QueryRowContext(ctx, `
		SELECT `+usageFactAggregateColumns("f", undatedExpression)+`
		FROM usage_facts f
		WHERE `+where,
		args...,
	)
	return scanUsageAggregate(row)
}

func (s *Store) queryUsageUndatedFactCount(ctx context.Context, sourceIDs []int64) (int64, error) {
	undatedWhere, undatedArgs := usageSourceIDWhere("u", sourceIDs)
	var count int64
	if err := s.executor().QueryRowContext(ctx, `
		SELECT COUNT(1) FROM usage_facts u
		WHERE `+undatedWhere+` AND u.occurred_at_unix_ms = 0
	`, undatedArgs...).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Store) queryUsageFactModels(ctx context.Context, sourceIDs []int64, startUnixMS *int64, endUnixMS int64) ([]UsageModelAggregate, error) {
	where, args := usageSourceIDWhere("f", sourceIDs)
	if startUnixMS != nil {
		where += " AND f.occurred_at_unix_ms >= ? AND f.occurred_at_unix_ms < ?"
		args = append(args, *startUnixMS, endUnixMS)
	}
	rows, err := s.executor().QueryContext(ctx, `
		SELECT m.model_key, `+usageFactAggregateColumns(
		"f",
		"COALESCE(SUM(CASE WHEN f.occurred_at_unix_ms = 0 THEN 1 ELSE 0 END), 0)",
	)+`
		FROM usage_facts f
		JOIN usage_models m ON m.id = f.model_id AND m.source_id = f.source_id
		WHERE `+where+`
		GROUP BY m.model_key
		ORDER BY COALESCE(SUM(f.total_tokens), 0) DESC, m.model_key ASC
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	models := make([]UsageModelAggregate, 0)
	for rows.Next() {
		var item UsageModelAggregate
		if err := scanUsageModelAggregate(rows, &item); err != nil {
			return nil, err
		}
		models = append(models, item)
	}
	return models, rows.Err()
}

func (s *Store) queryUsageFactTrend(ctx context.Context, sourceIDs []int64, buckets []UsageTimeBucket) ([]UsageTrendAggregate, error) {
	if len(buckets) == 0 {
		return []UsageTrendAggregate{}, nil
	}
	values := make([]string, 0, len(buckets))
	args := make([]any, 0, len(buckets)*3+len(sourceIDs))
	for index, bucket := range buckets {
		values = append(values, "(?, ?, ?)")
		args = append(args, index, bucket.StartUnixMS, bucket.EndUnixMS)
	}
	sourceWhere, sourceArgs := usageSourceIDWhere("f", sourceIDs)
	args = append(args, sourceArgs...)
	rows, err := s.executor().QueryContext(ctx, `
		WITH buckets(bucket_index, start_unix_ms, end_unix_ms) AS (
			VALUES `+strings.Join(values, ", ")+`
		)
		SELECT b.bucket_index, `+usageFactAggregateColumns(
		"f",
		"0",
	)+`
		FROM buckets b
		LEFT JOIN usage_facts f
			ON `+sourceWhere+`
			AND f.occurred_at_unix_ms >= b.start_unix_ms
			AND f.occurred_at_unix_ms < b.end_unix_ms
		GROUP BY b.bucket_index
		ORDER BY b.bucket_index ASC
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	trend := make([]UsageTrendAggregate, 0, len(buckets))
	for rows.Next() {
		var point UsageTrendAggregate
		if err := scanUsageTrendAggregate(rows, &point); err != nil {
			return nil, err
		}
		trend = append(trend, point)
	}
	return trend, rows.Err()
}

func (s *Store) queryUsageSourceSummary(ctx context.Context, sourceIDs []int64) (UsageImportSummary, error) {
	where, args := usagePrimaryIDWhere("s", sourceIDs)
	var summary UsageImportSummary
	err := s.executor().QueryRowContext(ctx, `
		SELECT COALESCE(SUM(s.tracked_units), 0),
			COALESCE(MAX(s.last_completed_at_unix_ms), 0),
			COALESCE(SUM(s.invalid_records), 0),
			COALESCE(SUM(s.unsupported_records), 0)
		FROM usage_sources s
		WHERE `+where,
		args...,
	).Scan(
		&summary.TrackedFiles,
		&summary.LastSyncedAtUnixMS,
		&summary.InvalidLines,
		&summary.UnsupportedLines,
	)
	return summary, err
}

func usageSourceIDWhere(alias string, sourceIDs []int64) (string, []any) {
	return usageIDWhere(alias+".source_id", sourceIDs)
}

func usagePrimaryIDWhere(alias string, sourceIDs []int64) (string, []any) {
	return usageIDWhere(alias+".id", sourceIDs)
}

func usageIDWhere(column string, sourceIDs []int64) (string, []any) {
	if len(sourceIDs) == 0 {
		return "1 = 0", nil
	}
	args := make([]any, len(sourceIDs))
	for index, sourceID := range sourceIDs {
		args[index] = sourceID
	}
	return fmt.Sprintf("%s IN (%s)", column, sqlPlaceholders(len(sourceIDs))), args
}

func usageFactAggregateColumns(alias, undatedExpression string) string {
	return fmt.Sprintf(`
		COUNT(%[1]s.id),
		COUNT(DISTINCT %[1]s.session_id),
		COALESCE(SUM(%[1]s.input_tokens - %[1]s.cached_input_tokens), 0),
		COALESCE(SUM(%[1]s.input_tokens), 0),
		COALESCE(SUM(%[1]s.cached_input_tokens), 0),
		COALESCE(SUM(%[1]s.output_tokens), 0),
		COALESCE(SUM(%[1]s.total_tokens), 0),
		COALESCE(SUM(COALESCE(%[1]s.estimated_cost_micros, 0)), 0),
		COALESCE(SUM(CASE WHEN %[1]s.cost_status IN (%[2]d, %[3]d) THEN %[1]s.total_tokens ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN %[1]s.cost_status = %[4]d THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN %[1]s.cost_status = %[2]d THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN %[1]s.cost_status = %[3]d THEN 1 ELSE 0 END), 0),
		%[5]s`,
		alias,
		UsageCostStatusEstimated,
		UsageCostStatusPartial,
		UsageCostStatusUnknown,
		undatedExpression,
	)
}

func scanUsageAggregate(row rowScanner) (UsageAggregate, error) {
	var aggregate UsageAggregate
	err := row.Scan(usageAggregateScanTargets(&aggregate)...)
	return aggregate, err
}

func usageAggregateScanTargets(aggregate *UsageAggregate) []any {
	return []any{
		&aggregate.EventCount,
		&aggregate.SessionCount,
		&aggregate.FreshInputTokens,
		&aggregate.InputTokens,
		&aggregate.CachedInputTokens,
		&aggregate.OutputTokens,
		&aggregate.TotalTokens,
		&aggregate.EstimatedCostMicros,
		&aggregate.EstimatedTokenCount,
		&aggregate.UnknownCostEvents,
		&aggregate.EstimatedCostEventCount,
		&aggregate.PartialCostEventCount,
		&aggregate.UndatedEventCount,
	}
}

func scanUsageModelAggregate(row rowScanner, item *UsageModelAggregate) error {
	targets := append([]any{&item.Model}, usageAggregateScanTargets(&item.UsageAggregate)...)
	return row.Scan(targets...)
}

func scanUsageTrendAggregate(row rowScanner, point *UsageTrendAggregate) error {
	targets := append([]any{&point.BucketIndex}, usageAggregateScanTargets(&point.UsageAggregate)...)
	return row.Scan(targets...)
}

func zeroUsageTrend(count int) []UsageTrendAggregate {
	trend := make([]UsageTrendAggregate, count)
	for index := range trend {
		trend[index].BucketIndex = index
	}
	return trend
}
