package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/strahe/profiledeck/internal/store/migrations"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"github.com/uptrace/bun/migrate"
	sqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

const (
	sqliteDriverName  = "sqlite"
	sqliteBusyTimeout = 5 * time.Second

	OperationTypeSwitch   = "switch"
	OperationTypeRollback = "rollback"

	OperationStatusPending = "pending"
	OperationStatusFailed  = "failed"
	OperationStatusApplied = "applied"

	ActiveStateScopeProvider = "provider"

	UsageCostStatusEstimated = "estimated"
	UsageCostStatusUnknown   = "unknown"

	// provider_account_secrets intentionally constrains secret_kind in v1;
	// adding another kind requires a schema migration and explicit validation.
	providerAccountSecretKindCodexAuthJSON = "codex-auth-json"
	maxProviderAccountSecretPayloadBytes   = 16 * 1024 * 1024
)

var (
	ErrAlreadyExists = errors.New("already exists")
	ErrInUse         = errors.New("in use")
	ErrNotFound      = errors.New("not found")
	ErrPathOwned     = errors.New("path owned")
)

const profileTargetPathOwnerMessage = "profile target path is owned by another provider target"

type Store struct {
	db            *bun.DB
	exec          dbExecutor
	transactional bool
}

type dbExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type Provider struct {
	ID              string
	Name            string
	AdapterID       string
	Enabled         bool
	MetadataJSON    string
	CreatedAtUnixMS int64
	UpdatedAtUnixMS int64
}

type CreateProviderParams struct {
	ID           string
	Name         string
	AdapterID    string
	Enabled      bool
	MetadataJSON string
}

type UpdateProviderParams struct {
	ID           string
	Name         *string
	AdapterID    *string
	Enabled      *bool
	MetadataJSON *string
}

type Profile struct {
	ID              string
	Name            string
	Description     string
	MetadataJSON    string
	CreatedAtUnixMS int64
	UpdatedAtUnixMS int64
}

type ProfileTarget struct {
	ProfileID       string
	ProviderID      string
	TargetID        string
	Path            string
	PathKey         string
	Format          string
	Strategy        string
	ValueJSON       string
	Enabled         bool
	MetadataJSON    string
	CreatedAtUnixMS int64
	UpdatedAtUnixMS int64
}

type Operation struct {
	ID              string
	OperationType   string
	Status          string
	ProfileID       string
	MetadataJSON    string
	ErrorCode       string
	ErrorMessage    string
	CreatedAtUnixMS int64
	UpdatedAtUnixMS int64
}

type ActiveState struct {
	ScopeType       string
	ScopeID         string
	ProfileID       string
	OperationID     string
	UpdatedAtUnixMS int64
}

type UsageEvent struct {
	ID                  string
	ProviderID          string
	Source              string
	SourceKey           string
	SessionID           string
	Model               string
	OccurredAtUnixMS    int64
	InputTokens         int64
	CachedInputTokens   int64
	OutputTokens        int64
	TotalTokens         int64
	EstimatedCostMicros *int64
	CostStatus          string
	MetadataJSON        string
	CreatedAtUnixMS     int64
	UpdatedAtUnixMS     int64
}

type UsageImportCursor struct {
	ProviderID       string
	Source           string
	SourceKey        string
	ModifiedUnixMS   int64
	SizeBytes        int64
	ImportedEvents   int64
	InvalidLines     int64
	UnsupportedLines int64
	MetadataJSON     string
	CreatedAtUnixMS  int64
	UpdatedAtUnixMS  int64
}

type ProviderAccountSecret struct {
	ProviderID      string
	AccountID       string
	SecretKind      string
	PayloadJSON     string
	PayloadSHA256   string
	DisplayName     string
	MetadataJSON    string
	CreatedAtUnixMS int64
	UpdatedAtUnixMS int64
}

type CreateProfileParams struct {
	ID           string
	Name         string
	Description  string
	MetadataJSON string
}

type UpdateProfileParams struct {
	ID           string
	Name         *string
	Description  *string
	MetadataJSON *string
}

type CreateProfileTargetParams struct {
	ProfileID    string
	ProviderID   string
	TargetID     string
	Path         string
	PathKey      string
	Format       string
	Strategy     string
	ValueJSON    string
	Enabled      bool
	MetadataJSON string
}

type UpdateProfileTargetParams struct {
	ProfileID    string
	ProviderID   string
	TargetID     string
	Path         *string
	PathKey      *string
	Format       *string
	Strategy     *string
	ValueJSON    *string
	Enabled      *bool
	MetadataJSON *string
}

type CreateSwitchOperationParams struct {
	ID           string
	ProfileID    string
	MetadataJSON string
}

type CreateRollbackOperationParams struct {
	ID           string
	ProfileID    string
	MetadataJSON string
}

type MarkOperationFailedParams struct {
	ID           string
	ErrorCode    string
	ErrorMessage string
	MetadataJSON *string
}

type CompleteSwitchOperationParams struct {
	ID           string
	ProfileID    string
	ProviderID   string
	MetadataJSON string
}

type RollbackActiveStateParams struct {
	ProfileID   string
	OperationID string
}

type CompleteRollbackOperationParams struct {
	ID                  string
	ProfileID           string
	ProviderID          string
	RestoredActiveState *RollbackActiveStateParams
	MetadataJSON        string
}

type CreateUsageEventParams struct {
	ID                  string
	ProviderID          string
	Source              string
	SourceKey           string
	SessionID           string
	Model               string
	OccurredAtUnixMS    int64
	InputTokens         int64
	CachedInputTokens   int64
	OutputTokens        int64
	TotalTokens         int64
	EstimatedCostMicros *int64
	CostStatus          string
	MetadataJSON        string
}

type UsageInsertResult struct {
	Inserted   int
	Duplicates int
}

type UpsertUsageImportCursorParams struct {
	ProviderID       string
	Source           string
	SourceKey        string
	ModifiedUnixMS   int64
	SizeBytes        int64
	ImportedEvents   int64
	InvalidLines     int64
	UnsupportedLines int64
	MetadataJSON     string
}

type UpsertProviderAccountSecretParams struct {
	ProviderID    string
	AccountID     string
	SecretKind    string
	PayloadJSON   string
	PayloadSHA256 string
	DisplayName   string
	MetadataJSON  string
}

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
	EstimatedCostEventCount int64
}

type MigrationResult struct {
	Applied int
}

type Status struct {
	SchemaHealthy     bool
	PendingOperations int
	FailedOperations  int
}

type tableSpec struct {
	name    string
	columns []columnSpec
	checks  []string
}

type columnSpec struct {
	name           string
	columnType     string
	notNull        bool
	primaryKey     bool
	requireDefault bool
	defaultValue   string
}

type columnInfo struct {
	columnType   string
	notNull      bool
	primaryKey   bool
	defaultValue sql.NullString
}

type indexSpec struct {
	name    string
	table   string
	columns []string
	unique  bool
}

type triggerSpec struct {
	name   string
	table  string
	checks []string
}

var initialTableSpecs = []tableSpec{
	{
		name: "providers",
		columns: []columnSpec{
			{name: "id", columnType: "TEXT", primaryKey: true},
			{name: "name", columnType: "TEXT", notNull: true},
			{name: "adapter_id", columnType: "TEXT", notNull: true},
			{name: "enabled", columnType: "INTEGER", notNull: true, requireDefault: true, defaultValue: "1"},
			{name: "metadata_json", columnType: "TEXT", notNull: true, requireDefault: true, defaultValue: "'{}'"},
			{name: "created_at_unix_ms", columnType: "INTEGER", notNull: true},
			{name: "updated_at_unix_ms", columnType: "INTEGER", notNull: true},
		},
		checks: []string{
			"CHECK (enabled IN (0, 1))",
		},
	},
	{
		name: "profiles",
		columns: []columnSpec{
			{name: "id", columnType: "TEXT", primaryKey: true},
			{name: "name", columnType: "TEXT", notNull: true},
			{name: "description", columnType: "TEXT", notNull: true, requireDefault: true, defaultValue: "''"},
			{name: "metadata_json", columnType: "TEXT", notNull: true, requireDefault: true, defaultValue: "'{}'"},
			{name: "created_at_unix_ms", columnType: "INTEGER", notNull: true},
			{name: "updated_at_unix_ms", columnType: "INTEGER", notNull: true},
		},
	},
	{
		name: "settings",
		columns: []columnSpec{
			{name: "key", columnType: "TEXT", primaryKey: true},
			{name: "value_json", columnType: "TEXT", notNull: true},
			{name: "updated_at_unix_ms", columnType: "INTEGER", notNull: true},
		},
	},
	{
		name: "active_states",
		columns: []columnSpec{
			{name: "scope_type", columnType: "TEXT", notNull: true, primaryKey: true},
			{name: "scope_id", columnType: "TEXT", notNull: true, primaryKey: true},
			{name: "profile_id", columnType: "TEXT", notNull: true, requireDefault: true, defaultValue: "''"},
			{name: "operation_id", columnType: "TEXT", notNull: true, requireDefault: true, defaultValue: "''"},
			{name: "updated_at_unix_ms", columnType: "INTEGER", notNull: true},
		},
	},
	{
		name: "operations",
		columns: []columnSpec{
			{name: "id", columnType: "TEXT", primaryKey: true},
			{name: "operation_type", columnType: "TEXT", notNull: true},
			{name: "status", columnType: "TEXT", notNull: true},
			{name: "profile_id", columnType: "TEXT", notNull: true, requireDefault: true, defaultValue: "''"},
			{name: "metadata_json", columnType: "TEXT", notNull: true, requireDefault: true, defaultValue: "'{}'"},
			{name: "error_code", columnType: "TEXT", notNull: true, requireDefault: true, defaultValue: "''"},
			{name: "error_message", columnType: "TEXT", notNull: true, requireDefault: true, defaultValue: "''"},
			{name: "created_at_unix_ms", columnType: "INTEGER", notNull: true},
			{name: "updated_at_unix_ms", columnType: "INTEGER", notNull: true},
		},
		checks: []string{
			"CHECK (operation_type IN ('switch', 'rollback', 'import', 'maintenance'))",
			"CHECK (status IN ('pending', 'failed', 'applied'))",
		},
	},
	{
		name: "profile_targets",
		columns: []columnSpec{
			{name: "profile_id", columnType: "TEXT", notNull: true, primaryKey: true},
			{name: "provider_id", columnType: "TEXT", notNull: true, primaryKey: true},
			{name: "target_id", columnType: "TEXT", notNull: true, primaryKey: true},
			{name: "path", columnType: "TEXT", notNull: true},
			{name: "path_key", columnType: "TEXT", notNull: true},
			{name: "format", columnType: "TEXT", notNull: true},
			{name: "strategy", columnType: "TEXT", notNull: true},
			{name: "value_json", columnType: "TEXT", notNull: true},
			{name: "enabled", columnType: "INTEGER", notNull: true, requireDefault: true, defaultValue: "1"},
			{name: "metadata_json", columnType: "TEXT", notNull: true, requireDefault: true, defaultValue: "'{}'"},
			{name: "created_at_unix_ms", columnType: "INTEGER", notNull: true},
			{name: "updated_at_unix_ms", columnType: "INTEGER", notNull: true},
		},
		checks: []string{
			"CHECK (format IN ('text', 'json', 'toml', 'env'))",
			"CHECK (strategy IN ('replace-file', 'json-merge', 'toml-merge', 'env-merge'))",
			"CHECK (enabled IN (0, 1))",
		},
	},
	{
		name: "usage_events",
		columns: []columnSpec{
			{name: "id", columnType: "TEXT", primaryKey: true},
			{name: "provider_id", columnType: "TEXT", notNull: true},
			{name: "source", columnType: "TEXT", notNull: true},
			{name: "source_key", columnType: "TEXT", notNull: true},
			{name: "session_id", columnType: "TEXT", notNull: true, requireDefault: true, defaultValue: "''"},
			{name: "model", columnType: "TEXT", notNull: true, requireDefault: true, defaultValue: "''"},
			{name: "occurred_at_unix_ms", columnType: "INTEGER", notNull: true, requireDefault: true, defaultValue: "0"},
			{name: "input_tokens", columnType: "INTEGER", notNull: true, requireDefault: true, defaultValue: "0"},
			{name: "cached_input_tokens", columnType: "INTEGER", notNull: true, requireDefault: true, defaultValue: "0"},
			{name: "output_tokens", columnType: "INTEGER", notNull: true, requireDefault: true, defaultValue: "0"},
			{name: "total_tokens", columnType: "INTEGER", notNull: true, requireDefault: true, defaultValue: "0"},
			{name: "estimated_cost_micros", columnType: "INTEGER"},
			{name: "cost_status", columnType: "TEXT", notNull: true},
			{name: "metadata_json", columnType: "TEXT", notNull: true, requireDefault: true, defaultValue: "'{}'"},
			{name: "created_at_unix_ms", columnType: "INTEGER", notNull: true},
			{name: "updated_at_unix_ms", columnType: "INTEGER", notNull: true},
		},
		checks: []string{
			"CHECK (input_tokens >= 0)",
			"CHECK (cached_input_tokens >= 0)",
			"CHECK (output_tokens >= 0)",
			"CHECK (total_tokens >= 0)",
			"CHECK (estimated_cost_micros IS NULL OR estimated_cost_micros >= 0)",
			"CHECK (cost_status IN ('estimated', 'unknown'))",
			"CHECK ((cost_status = 'estimated' AND estimated_cost_micros IS NOT NULL) OR (cost_status = 'unknown' AND estimated_cost_micros IS NULL))",
		},
	},
	{
		name: "usage_import_cursors",
		columns: []columnSpec{
			{name: "provider_id", columnType: "TEXT", notNull: true, primaryKey: true},
			{name: "source", columnType: "TEXT", notNull: true, primaryKey: true},
			{name: "source_key", columnType: "TEXT", notNull: true, primaryKey: true},
			{name: "modified_unix_ms", columnType: "INTEGER", notNull: true, requireDefault: true, defaultValue: "0"},
			{name: "size_bytes", columnType: "INTEGER", notNull: true, requireDefault: true, defaultValue: "0"},
			{name: "imported_events", columnType: "INTEGER", notNull: true, requireDefault: true, defaultValue: "0"},
			{name: "invalid_lines", columnType: "INTEGER", notNull: true, requireDefault: true, defaultValue: "0"},
			{name: "unsupported_lines", columnType: "INTEGER", notNull: true, requireDefault: true, defaultValue: "0"},
			{name: "metadata_json", columnType: "TEXT", notNull: true, requireDefault: true, defaultValue: "'{}'"},
			{name: "created_at_unix_ms", columnType: "INTEGER", notNull: true},
			{name: "updated_at_unix_ms", columnType: "INTEGER", notNull: true},
		},
		checks: []string{
			"CHECK (size_bytes >= 0)",
			"CHECK (imported_events >= 0)",
			"CHECK (invalid_lines >= 0)",
			"CHECK (unsupported_lines >= 0)",
		},
	},
	{
		name: "provider_account_secrets",
		columns: []columnSpec{
			{name: "provider_id", columnType: "TEXT", notNull: true, primaryKey: true},
			{name: "account_id", columnType: "TEXT", notNull: true, primaryKey: true},
			{name: "secret_kind", columnType: "TEXT", notNull: true},
			{name: "payload_json", columnType: "TEXT", notNull: true},
			{name: "payload_sha256", columnType: "TEXT", notNull: true},
			{name: "display_name", columnType: "TEXT", notNull: true, requireDefault: true, defaultValue: "''"},
			{name: "metadata_json", columnType: "TEXT", notNull: true, requireDefault: true, defaultValue: "'{}'"},
			{name: "created_at_unix_ms", columnType: "INTEGER", notNull: true},
			{name: "updated_at_unix_ms", columnType: "INTEGER", notNull: true},
		},
		checks: []string{
			"CHECK (secret_kind IN ('codex-auth-json'))",
		},
	},
}

var initialIndexSpecs = []indexSpec{
	{name: "idx_providers_adapter_id", table: "providers", columns: []string{"adapter_id"}},
	{name: "idx_providers_enabled", table: "providers", columns: []string{"enabled"}},
	{name: "idx_operations_status", table: "operations", columns: []string{"status"}},
	{name: "idx_operations_operation_type", table: "operations", columns: []string{"operation_type"}},
	{name: "idx_profile_targets_profile_id", table: "profile_targets", columns: []string{"profile_id"}},
	{name: "idx_profile_targets_provider_id", table: "profile_targets", columns: []string{"provider_id"}},
	{name: "idx_profile_targets_enabled", table: "profile_targets", columns: []string{"enabled"}},
	{name: "idx_profile_targets_unique_path", table: "profile_targets", columns: []string{"profile_id", "provider_id", "path_key"}, unique: true},
	{name: "idx_usage_events_provider_id", table: "usage_events", columns: []string{"provider_id"}},
	{name: "idx_usage_events_source", table: "usage_events", columns: []string{"source"}},
	{name: "idx_usage_events_source_key", table: "usage_events", columns: []string{"source_key"}},
	{name: "idx_usage_events_model", table: "usage_events", columns: []string{"model"}},
	{name: "idx_usage_events_occurred_at", table: "usage_events", columns: []string{"occurred_at_unix_ms"}},
	{name: "idx_usage_events_cost_status", table: "usage_events", columns: []string{"cost_status"}},
	{name: "idx_usage_import_cursors_source", table: "usage_import_cursors", columns: []string{"source"}},
	{name: "idx_provider_account_secrets_secret_kind", table: "provider_account_secrets", columns: []string{"secret_kind"}},
}

var initialTriggerSpecs = []triggerSpec{
	{
		name:  "trg_profile_targets_path_owner_insert",
		table: "profile_targets",
		checks: []string{
			"BEFORE INSERT ON profile_targets",
			"path_key = NEW.path_key",
			"provider_id <> NEW.provider_id",
			"target_id <> NEW.target_id",
			"RAISE(ABORT, '" + profileTargetPathOwnerMessage + "')",
		},
	},
	{
		name:  "trg_profile_targets_path_owner_update",
		table: "profile_targets",
		checks: []string{
			"BEFORE UPDATE OF path, path_key, provider_id, target_id ON profile_targets",
			"path_key = NEW.path_key",
			"profile_id = OLD.profile_id",
			"provider_id = OLD.provider_id",
			"target_id = OLD.target_id",
			"provider_id <> NEW.provider_id",
			"target_id <> NEW.target_id",
			"RAISE(ABORT, '" + profileTargetPathOwnerMessage + "')",
		},
	},
}

func Open(ctx context.Context, databasePath string, readOnly bool) (*Store, error) {
	sqlDB, err := sql.Open(sqliteDriverName, sqliteDSN(databasePath, readOnly))
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)

	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}

	return &Store{
		db:   bun.NewDB(sqlDB, sqlitedialect.New()),
		exec: sqlDB,
	}, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	if s.transactional {
		return nil
	}
	return s.db.Close()
}

func (s *Store) WithTransaction(ctx context.Context, fn func(txStore *Store) error) error {
	if s.transactional {
		return errors.New("nested store transactions are not supported")
	}
	tx, err := s.db.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	txStore := &Store{
		db:            s.db,
		exec:          tx,
		transactional: true,
	}
	if err := fn(txStore); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *Store) executor() dbExecutor {
	if s.exec != nil {
		return s.exec
	}
	return s.db.DB
}

func (s *Store) Migrate(ctx context.Context) (MigrationResult, error) {
	migrator := migrate.NewMigrator(
		s.db,
		migrations.Migrations,
		migrate.WithMarkAppliedOnSuccess(true),
		migrate.WithUpsert(true),
	)
	if err := migrator.Init(ctx); err != nil {
		return MigrationResult{}, err
	}
	group, err := migrator.Migrate(ctx)
	if err != nil {
		return MigrationResult{}, err
	}
	return MigrationResult{Applied: len(group.Migrations)}, nil
}

func (s *Store) Status(ctx context.Context) (Status, error) {
	healthy, err := s.schemaHealthy(ctx)
	if err != nil {
		return Status{}, err
	}
	if !healthy {
		return Status{SchemaHealthy: false}, nil
	}

	pending, err := s.countOperations(ctx, OperationStatusPending)
	if err != nil {
		return Status{}, err
	}
	failed, err := s.countOperations(ctx, OperationStatusFailed)
	if err != nil {
		return Status{}, err
	}

	return Status{
		SchemaHealthy:     true,
		PendingOperations: pending,
		FailedOperations:  failed,
	}, nil
}

func (s *Store) ListProviders(ctx context.Context, includeDisabled bool) ([]Provider, error) {
	query := `
		SELECT id, name, adapter_id, enabled, metadata_json, created_at_unix_ms, updated_at_unix_ms
		FROM providers
	`
	args := []any{}
	if !includeDisabled {
		query += " WHERE enabled = ?"
		args = append(args, 1)
	}
	query += " ORDER BY id ASC"

	rows, err := s.executor().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var providers []Provider
	for rows.Next() {
		provider, err := scanProvider(rows)
		if err != nil {
			return nil, err
		}
		providers = append(providers, provider)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return providers, nil
}

func (s *Store) GetProvider(ctx context.Context, id string) (Provider, error) {
	row := s.executor().QueryRowContext(
		ctx,
		`SELECT id, name, adapter_id, enabled, metadata_json, created_at_unix_ms, updated_at_unix_ms
		FROM providers
		WHERE id = ?`,
		id,
	)
	provider, err := scanProvider(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Provider{}, ErrNotFound
	}
	return provider, err
}

func (s *Store) CreateProvider(ctx context.Context, params CreateProviderParams) (Provider, error) {
	now := time.Now().UnixMilli()
	enabled := 0
	if params.Enabled {
		enabled = 1
	}
	_, err := s.executor().ExecContext(
		ctx,
		`INSERT INTO providers
			(id, name, adapter_id, enabled, metadata_json, created_at_unix_ms, updated_at_unix_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		params.ID,
		params.Name,
		params.AdapterID,
		enabled,
		params.MetadataJSON,
		now,
		now,
	)
	if err != nil {
		if isSQLiteConstraintError(err) {
			return Provider{}, ErrAlreadyExists
		}
		return Provider{}, err
	}
	return s.GetProvider(ctx, params.ID)
}

func (s *Store) UpdateProvider(ctx context.Context, params UpdateProviderParams) (Provider, error) {
	assignments := []string{}
	args := []any{}
	if params.Name != nil {
		assignments = append(assignments, "name = ?")
		args = append(args, *params.Name)
	}
	if params.AdapterID != nil {
		assignments = append(assignments, "adapter_id = ?")
		args = append(args, *params.AdapterID)
	}
	if params.Enabled != nil {
		enabled := 0
		if *params.Enabled {
			enabled = 1
		}
		assignments = append(assignments, "enabled = ?")
		args = append(args, enabled)
	}
	if params.MetadataJSON != nil {
		assignments = append(assignments, "metadata_json = ?")
		args = append(args, *params.MetadataJSON)
	}
	if len(assignments) == 0 {
		return s.GetProvider(ctx, params.ID)
	}
	assignments = append(assignments, "updated_at_unix_ms = ?")
	args = append(args, time.Now().UnixMilli(), params.ID)

	result, err := s.executor().ExecContext(
		ctx,
		`UPDATE providers SET `+strings.Join(assignments, ", ")+` WHERE id = ?`,
		args...,
	)
	if err != nil {
		return Provider{}, err
	}
	if rows, err := result.RowsAffected(); err == nil && rows == 0 {
		return Provider{}, ErrNotFound
	}
	return s.GetProvider(ctx, params.ID)
}

func (s *Store) DeleteProvider(ctx context.Context, id string) error {
	if _, err := s.GetProvider(ctx, id); err != nil {
		return err
	}
	references, err := s.providerReferenceCount(ctx, id)
	if err != nil {
		return err
	}
	if references > 0 {
		return ErrInUse
	}

	result, err := s.executor().ExecContext(ctx, `
		DELETE FROM providers
		WHERE id = ?
			AND NOT EXISTS (SELECT 1 FROM profile_targets WHERE provider_id = ?)
			AND NOT EXISTS (SELECT 1 FROM active_states WHERE scope_type = ? AND scope_id = ?)
	`, id, id, ActiveStateScopeProvider, id)
	if err != nil {
		return err
	}
	if rows, err := result.RowsAffected(); err == nil && rows == 0 {
		if _, getErr := s.GetProvider(ctx, id); errors.Is(getErr, ErrNotFound) {
			return ErrNotFound
		} else if getErr != nil {
			return getErr
		}
		references, refErr := s.providerReferenceCount(ctx, id)
		if refErr != nil {
			return refErr
		}
		if references > 0 {
			return ErrInUse
		}
		return ErrNotFound
	}
	return nil
}

func (s *Store) ListProfiles(ctx context.Context) ([]Profile, error) {
	rows, err := s.executor().QueryContext(
		ctx,
		`SELECT id, name, description, metadata_json, created_at_unix_ms, updated_at_unix_ms
		FROM profiles
		ORDER BY id ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var profiles []Profile
	for rows.Next() {
		profile, err := scanProfile(rows)
		if err != nil {
			return nil, err
		}
		profiles = append(profiles, profile)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return profiles, nil
}

func (s *Store) GetProfile(ctx context.Context, id string) (Profile, error) {
	row := s.executor().QueryRowContext(
		ctx,
		`SELECT id, name, description, metadata_json, created_at_unix_ms, updated_at_unix_ms
		FROM profiles
		WHERE id = ?`,
		id,
	)
	profile, err := scanProfile(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Profile{}, ErrNotFound
	}
	return profile, err
}

func (s *Store) CreateProfile(ctx context.Context, params CreateProfileParams) (Profile, error) {
	now := time.Now().UnixMilli()
	_, err := s.executor().ExecContext(
		ctx,
		`INSERT INTO profiles
			(id, name, description, metadata_json, created_at_unix_ms, updated_at_unix_ms)
		VALUES (?, ?, ?, ?, ?, ?)`,
		params.ID,
		params.Name,
		params.Description,
		params.MetadataJSON,
		now,
		now,
	)
	if err != nil {
		if isSQLiteConstraintError(err) {
			return Profile{}, ErrAlreadyExists
		}
		return Profile{}, err
	}
	return s.GetProfile(ctx, params.ID)
}

func (s *Store) UpdateProfile(ctx context.Context, params UpdateProfileParams) (Profile, error) {
	assignments := []string{}
	args := []any{}
	if params.Name != nil {
		assignments = append(assignments, "name = ?")
		args = append(args, *params.Name)
	}
	if params.Description != nil {
		assignments = append(assignments, "description = ?")
		args = append(args, *params.Description)
	}
	if params.MetadataJSON != nil {
		assignments = append(assignments, "metadata_json = ?")
		args = append(args, *params.MetadataJSON)
	}
	if len(assignments) == 0 {
		return s.GetProfile(ctx, params.ID)
	}
	assignments = append(assignments, "updated_at_unix_ms = ?")
	args = append(args, time.Now().UnixMilli(), params.ID)

	result, err := s.executor().ExecContext(
		ctx,
		`UPDATE profiles SET `+strings.Join(assignments, ", ")+` WHERE id = ?`,
		args...,
	)
	if err != nil {
		return Profile{}, err
	}
	if rows, err := result.RowsAffected(); err == nil && rows == 0 {
		return Profile{}, ErrNotFound
	}
	return s.GetProfile(ctx, params.ID)
}

func (s *Store) DeleteProfile(ctx context.Context, id string) error {
	result, err := s.executor().ExecContext(ctx, `
		DELETE FROM profiles
		WHERE id = ?
			AND NOT EXISTS (SELECT 1 FROM active_states WHERE profile_id = ?)
			AND NOT EXISTS (SELECT 1 FROM operations WHERE profile_id = ?)
			AND NOT EXISTS (SELECT 1 FROM profile_targets WHERE profile_id = ?)
	`, id, id, id, id)
	if err != nil {
		return err
	}
	if rows, err := result.RowsAffected(); err == nil && rows == 0 {
		if _, getErr := s.GetProfile(ctx, id); errors.Is(getErr, ErrNotFound) {
			return ErrNotFound
		} else if getErr != nil {
			return getErr
		}
		references, refErr := s.profileReferenceCount(ctx, id)
		if refErr != nil {
			return refErr
		}
		if references > 0 {
			return ErrInUse
		}
		return ErrNotFound
	}
	return nil
}

func (s *Store) CreateProfileTarget(ctx context.Context, params CreateProfileTargetParams) (ProfileTarget, error) {
	now := time.Now().UnixMilli()
	enabled := 0
	if params.Enabled {
		enabled = 1
	}
	pathKey := params.PathKey
	if pathKey == "" {
		pathKey = params.Path
	}
	_, err := s.executor().ExecContext(
		ctx,
		`INSERT INTO profile_targets
			(profile_id, provider_id, target_id, path, path_key, format, strategy, value_json, enabled, metadata_json, created_at_unix_ms, updated_at_unix_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		params.ProfileID,
		params.ProviderID,
		params.TargetID,
		params.Path,
		pathKey,
		params.Format,
		params.Strategy,
		params.ValueJSON,
		enabled,
		params.MetadataJSON,
		now,
		now,
	)
	if err != nil {
		if isSQLiteConstraintError(err) {
			return ProfileTarget{}, profileTargetConstraintError(err)
		}
		return ProfileTarget{}, err
	}
	return s.GetProfileTarget(ctx, params.ProfileID, params.ProviderID, params.TargetID)
}

func (s *Store) ListProfileTargets(ctx context.Context, profileID string, providerID string, includeDisabled bool) ([]ProfileTarget, error) {
	query := `
		SELECT profile_id, provider_id, target_id, path, path_key, format, strategy, value_json, enabled, metadata_json, created_at_unix_ms, updated_at_unix_ms
		FROM profile_targets
		WHERE profile_id = ?
	`
	args := []any{profileID}
	if providerID != "" {
		query += " AND provider_id = ?"
		args = append(args, providerID)
	}
	if !includeDisabled {
		query += " AND enabled = ?"
		args = append(args, 1)
	}
	query += " ORDER BY provider_id ASC, target_id ASC"

	rows, err := s.executor().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var targets []ProfileTarget
	for rows.Next() {
		target, err := scanProfileTarget(rows)
		if err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return targets, nil
}

func (s *Store) ListProfileTargetsByProvider(ctx context.Context, providerID string) ([]ProfileTarget, error) {
	rows, err := s.executor().QueryContext(
		ctx,
		`SELECT profile_id, provider_id, target_id, path, path_key, format, strategy, value_json, enabled, metadata_json, created_at_unix_ms, updated_at_unix_ms
		FROM profile_targets
		WHERE provider_id = ?
		ORDER BY profile_id ASC, target_id ASC`,
		providerID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var targets []ProfileTarget
	for rows.Next() {
		target, err := scanProfileTarget(rows)
		if err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return targets, nil
}

func (s *Store) ListProfileTargetsByPathKey(ctx context.Context, pathKey string) ([]ProfileTarget, error) {
	rows, err := s.executor().QueryContext(
		ctx,
		`SELECT profile_id, provider_id, target_id, path, path_key, format, strategy, value_json, enabled, metadata_json, created_at_unix_ms, updated_at_unix_ms
		FROM profile_targets
		WHERE path_key = ?
		ORDER BY provider_id ASC, target_id ASC, profile_id ASC`,
		pathKey,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var targets []ProfileTarget
	for rows.Next() {
		target, err := scanProfileTarget(rows)
		if err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return targets, nil
}

func (s *Store) GetProfileTarget(ctx context.Context, profileID string, providerID string, targetID string) (ProfileTarget, error) {
	row := s.executor().QueryRowContext(
		ctx,
		`SELECT profile_id, provider_id, target_id, path, path_key, format, strategy, value_json, enabled, metadata_json, created_at_unix_ms, updated_at_unix_ms
		FROM profile_targets
		WHERE profile_id = ? AND provider_id = ? AND target_id = ?`,
		profileID,
		providerID,
		targetID,
	)
	target, err := scanProfileTarget(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ProfileTarget{}, ErrNotFound
	}
	return target, err
}

func (s *Store) UpdateProfileTarget(ctx context.Context, params UpdateProfileTargetParams) (ProfileTarget, error) {
	assignments := []string{}
	args := []any{}
	if params.Path != nil {
		assignments = append(assignments, "path = ?")
		args = append(args, *params.Path)
		pathKey := *params.Path
		if params.PathKey != nil {
			pathKey = *params.PathKey
		}
		assignments = append(assignments, "path_key = ?")
		args = append(args, pathKey)
	}
	if params.Format != nil {
		assignments = append(assignments, "format = ?")
		args = append(args, *params.Format)
	}
	if params.Strategy != nil {
		assignments = append(assignments, "strategy = ?")
		args = append(args, *params.Strategy)
	}
	if params.ValueJSON != nil {
		assignments = append(assignments, "value_json = ?")
		args = append(args, *params.ValueJSON)
	}
	if params.Enabled != nil {
		enabled := 0
		if *params.Enabled {
			enabled = 1
		}
		assignments = append(assignments, "enabled = ?")
		args = append(args, enabled)
	}
	if params.MetadataJSON != nil {
		assignments = append(assignments, "metadata_json = ?")
		args = append(args, *params.MetadataJSON)
	}
	if len(assignments) == 0 {
		return s.GetProfileTarget(ctx, params.ProfileID, params.ProviderID, params.TargetID)
	}
	assignments = append(assignments, "updated_at_unix_ms = ?")
	args = append(args, time.Now().UnixMilli(), params.ProfileID, params.ProviderID, params.TargetID)

	result, err := s.executor().ExecContext(
		ctx,
		`UPDATE profile_targets SET `+strings.Join(assignments, ", ")+` WHERE profile_id = ? AND provider_id = ? AND target_id = ?`,
		args...,
	)
	if err != nil {
		if isSQLiteConstraintError(err) {
			return ProfileTarget{}, profileTargetConstraintError(err)
		}
		return ProfileTarget{}, err
	}
	if rows, err := result.RowsAffected(); err == nil && rows == 0 {
		return ProfileTarget{}, ErrNotFound
	}
	return s.GetProfileTarget(ctx, params.ProfileID, params.ProviderID, params.TargetID)
}

func (s *Store) DeleteProfileTarget(ctx context.Context, profileID string, providerID string, targetID string) error {
	result, err := s.executor().ExecContext(
		ctx,
		"DELETE FROM profile_targets WHERE profile_id = ? AND provider_id = ? AND target_id = ?",
		profileID,
		providerID,
		targetID,
	)
	if err != nil {
		return err
	}
	if rows, err := result.RowsAffected(); err == nil && rows == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) UpsertProviderAccountSecret(ctx context.Context, params UpsertProviderAccountSecretParams) (ProviderAccountSecret, error) {
	if err := validateProviderAccountSecretParams(params); err != nil {
		return ProviderAccountSecret{}, err
	}
	accountID := strings.TrimSpace(params.AccountID)
	now := time.Now().UnixMilli()
	metadataJSON := params.MetadataJSON
	if metadataJSON == "" {
		metadataJSON = "{}"
	}
	_, err := s.executor().ExecContext(
		ctx,
		`INSERT INTO provider_account_secrets
			(provider_id, account_id, secret_kind, payload_json, payload_sha256, display_name, metadata_json, created_at_unix_ms, updated_at_unix_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(provider_id, account_id) DO UPDATE SET
			secret_kind = excluded.secret_kind,
			payload_json = excluded.payload_json,
			payload_sha256 = excluded.payload_sha256,
			display_name = excluded.display_name,
			metadata_json = excluded.metadata_json,
			updated_at_unix_ms = excluded.updated_at_unix_ms`,
		params.ProviderID,
		accountID,
		params.SecretKind,
		params.PayloadJSON,
		params.PayloadSHA256,
		params.DisplayName,
		metadataJSON,
		now,
		now,
	)
	if err != nil {
		return ProviderAccountSecret{}, err
	}
	return s.GetProviderAccountSecret(ctx, params.ProviderID, accountID)
}

func validateProviderAccountSecretParams(params UpsertProviderAccountSecretParams) error {
	if strings.TrimSpace(params.AccountID) == "" {
		return fmt.Errorf("provider account secret account_id is required")
	}
	if params.SecretKind != providerAccountSecretKindCodexAuthJSON {
		return fmt.Errorf("unsupported provider account secret kind: %s", params.SecretKind)
	}
	if len(params.PayloadJSON) > maxProviderAccountSecretPayloadBytes {
		return fmt.Errorf("provider account secret payload is too large")
	}
	decoder := json.NewDecoder(strings.NewReader(params.PayloadJSON))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("provider account secret payload must be a JSON object")
	}
	tokens, ok := object["tokens"].(map[string]any)
	if !ok {
		return fmt.Errorf("provider account secret payload must contain tokens.account_id")
	}
	payloadAccountID, ok := tokens["account_id"].(string)
	if !ok || strings.TrimSpace(payloadAccountID) == "" {
		return fmt.Errorf("provider account secret payload must contain tokens.account_id")
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return fmt.Errorf("provider account secret payload must contain one JSON object")
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func (s *Store) GetProviderAccountSecret(ctx context.Context, providerID string, accountID string) (ProviderAccountSecret, error) {
	row := s.executor().QueryRowContext(
		ctx,
		`SELECT provider_id, account_id, secret_kind, payload_json, payload_sha256, display_name, metadata_json, created_at_unix_ms, updated_at_unix_ms
		FROM provider_account_secrets
		WHERE provider_id = ? AND account_id = ?`,
		providerID,
		accountID,
	)
	secret, err := scanProviderAccountSecret(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ProviderAccountSecret{}, ErrNotFound
	}
	return secret, err
}

func (s *Store) ListProviderAccountSecrets(ctx context.Context, providerID string) ([]ProviderAccountSecret, error) {
	rows, err := s.executor().QueryContext(
		ctx,
		`SELECT provider_id, account_id, secret_kind, payload_json, payload_sha256, display_name, metadata_json, created_at_unix_ms, updated_at_unix_ms
		FROM provider_account_secrets
		WHERE provider_id = ?
		ORDER BY account_id ASC`,
		providerID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	secrets := []ProviderAccountSecret{}
	for rows.Next() {
		secret, err := scanProviderAccountSecret(rows)
		if err != nil {
			return nil, err
		}
		secrets = append(secrets, secret)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return secrets, nil
}

func (s *Store) CreatePendingSwitchOperation(ctx context.Context, params CreateSwitchOperationParams) (Operation, error) {
	now := time.Now().UnixMilli()
	_, err := s.executor().ExecContext(
		ctx,
		`INSERT INTO operations
			(id, operation_type, status, profile_id, metadata_json, created_at_unix_ms, updated_at_unix_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		params.ID,
		OperationTypeSwitch,
		OperationStatusPending,
		params.ProfileID,
		params.MetadataJSON,
		now,
		now,
	)
	if err != nil {
		if isSQLiteConstraintError(err) {
			return Operation{}, ErrAlreadyExists
		}
		return Operation{}, err
	}
	return s.GetOperation(ctx, params.ID)
}

func (s *Store) CreatePendingRollbackOperation(ctx context.Context, params CreateRollbackOperationParams) (Operation, error) {
	now := time.Now().UnixMilli()
	_, err := s.executor().ExecContext(
		ctx,
		`INSERT INTO operations
			(id, operation_type, status, profile_id, metadata_json, created_at_unix_ms, updated_at_unix_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		params.ID,
		OperationTypeRollback,
		OperationStatusPending,
		params.ProfileID,
		params.MetadataJSON,
		now,
		now,
	)
	if err != nil {
		if isSQLiteConstraintError(err) {
			return Operation{}, ErrAlreadyExists
		}
		return Operation{}, err
	}
	return s.GetOperation(ctx, params.ID)
}

func (s *Store) GetOperation(ctx context.Context, id string) (Operation, error) {
	row := s.executor().QueryRowContext(
		ctx,
		`SELECT id, operation_type, status, profile_id, metadata_json, error_code, error_message, created_at_unix_ms, updated_at_unix_ms
		FROM operations
		WHERE id = ?`,
		id,
	)
	operation, err := scanOperation(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Operation{}, ErrNotFound
	}
	return operation, err
}

func (s *Store) ListIncompleteOperations(ctx context.Context) ([]Operation, error) {
	rows, err := s.executor().QueryContext(
		ctx,
		`SELECT id, operation_type, status, profile_id, metadata_json, error_code, error_message, created_at_unix_ms, updated_at_unix_ms
		FROM operations
		WHERE status IN (?, ?)
		ORDER BY updated_at_unix_ms ASC, id ASC`,
		OperationStatusPending,
		OperationStatusFailed,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	operations := []Operation{}
	for rows.Next() {
		operation, err := scanOperation(rows)
		if err != nil {
			return nil, err
		}
		operations = append(operations, operation)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return operations, nil
}

func (s *Store) UpdateOperationMetadata(ctx context.Context, id string, metadataJSON string) error {
	result, err := s.executor().ExecContext(
		ctx,
		`UPDATE operations
		SET metadata_json = ?, updated_at_unix_ms = ?
		WHERE id = ?`,
		metadataJSON,
		time.Now().UnixMilli(),
		id,
	)
	if err != nil {
		return err
	}
	if rows, err := result.RowsAffected(); err == nil && rows == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) MarkOperationFailed(ctx context.Context, params MarkOperationFailedParams) error {
	assignments := []string{
		"status = ?",
		"error_code = ?",
		"error_message = ?",
	}
	args := []any{OperationStatusFailed, params.ErrorCode, params.ErrorMessage}
	if params.MetadataJSON != nil {
		assignments = append(assignments, "metadata_json = ?")
		args = append(args, *params.MetadataJSON)
	}
	assignments = append(assignments, "updated_at_unix_ms = ?")
	args = append(args, time.Now().UnixMilli(), params.ID)

	result, err := s.executor().ExecContext(
		ctx,
		`UPDATE operations SET `+strings.Join(assignments, ", ")+` WHERE id = ?`,
		args...,
	)
	if err != nil {
		return err
	}
	if rows, err := result.RowsAffected(); err == nil && rows == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) CompleteSwitchOperation(ctx context.Context, params CompleteSwitchOperationParams) error {
	tx, err := s.db.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	now := time.Now().UnixMilli()
	result, err := tx.ExecContext(
		ctx,
		`UPDATE operations
		SET status = ?, profile_id = ?, metadata_json = ?, error_code = '', error_message = '', updated_at_unix_ms = ?
		WHERE id = ? AND operation_type = ?`,
		OperationStatusApplied,
		params.ProfileID,
		params.MetadataJSON,
		now,
		params.ID,
		OperationTypeSwitch,
	)
	if err != nil {
		return err
	}
	if rows, err := result.RowsAffected(); err == nil && rows == 0 {
		return ErrNotFound
	}

	// Completing a switch is a DB-level invariant: the active provider state and
	// applied operation must be committed together.
	_, err = tx.ExecContext(
		ctx,
		`INSERT INTO active_states (scope_type, scope_id, profile_id, operation_id, updated_at_unix_ms)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(scope_type, scope_id) DO UPDATE SET
			profile_id = excluded.profile_id,
			operation_id = excluded.operation_id,
			updated_at_unix_ms = excluded.updated_at_unix_ms`,
		ActiveStateScopeProvider,
		params.ProviderID,
		params.ProfileID,
		params.ID,
		now,
	)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *Store) CompleteRollbackOperation(ctx context.Context, params CompleteRollbackOperationParams) error {
	tx, err := s.db.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	now := time.Now().UnixMilli()
	result, err := tx.ExecContext(
		ctx,
		`UPDATE operations
		SET status = ?, profile_id = ?, metadata_json = ?, error_code = '', error_message = '', updated_at_unix_ms = ?
		WHERE id = ? AND operation_type = ?`,
		OperationStatusApplied,
		params.ProfileID,
		params.MetadataJSON,
		now,
		params.ID,
		OperationTypeRollback,
	)
	if err != nil {
		return err
	}
	if rows, err := result.RowsAffected(); err == nil && rows == 0 {
		return ErrNotFound
	}

	// Completing rollback is atomic with active-state restoration so callers
	// never observe a successful rollback operation with stale active state.
	if params.RestoredActiveState == nil {
		if _, err := tx.ExecContext(
			ctx,
			`DELETE FROM active_states
			WHERE scope_type = ? AND scope_id = ?`,
			ActiveStateScopeProvider,
			params.ProviderID,
		); err != nil {
			return err
		}
	} else {
		_, err = tx.ExecContext(
			ctx,
			`INSERT INTO active_states (scope_type, scope_id, profile_id, operation_id, updated_at_unix_ms)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(scope_type, scope_id) DO UPDATE SET
				profile_id = excluded.profile_id,
				operation_id = excluded.operation_id,
				updated_at_unix_ms = excluded.updated_at_unix_ms`,
			ActiveStateScopeProvider,
			params.ProviderID,
			params.RestoredActiveState.ProfileID,
			params.RestoredActiveState.OperationID,
			now,
		)
		if err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *Store) GetActiveState(ctx context.Context, scopeType string, scopeID string) (ActiveState, error) {
	row := s.executor().QueryRowContext(
		ctx,
		`SELECT scope_type, scope_id, profile_id, operation_id, updated_at_unix_ms
		FROM active_states
		WHERE scope_type = ? AND scope_id = ?`,
		scopeType,
		scopeID,
	)
	activeState, err := scanActiveState(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ActiveState{}, ErrNotFound
	}
	return activeState, err
}

func (s *Store) InsertUsageEvents(ctx context.Context, events []CreateUsageEventParams) (UsageInsertResult, error) {
	if len(events) == 0 {
		return UsageInsertResult{}, nil
	}

	tx, err := s.db.DB.BeginTx(ctx, nil)
	if err != nil {
		return UsageInsertResult{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	now := time.Now().UnixMilli()
	stmt, err := tx.PrepareContext(
		ctx,
		`INSERT INTO usage_events
			(id, provider_id, source, source_key, session_id, model, occurred_at_unix_ms,
			 input_tokens, cached_input_tokens, output_tokens, total_tokens,
			 estimated_cost_micros, cost_status, metadata_json, created_at_unix_ms, updated_at_unix_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING`,
	)
	if err != nil {
		return UsageInsertResult{}, err
	}
	defer stmt.Close()

	result := UsageInsertResult{}
	for _, event := range events {
		metadataJSON := event.MetadataJSON
		if metadataJSON == "" {
			metadataJSON = "{}"
		}
		var estimatedCost any
		if event.EstimatedCostMicros != nil {
			estimatedCost = *event.EstimatedCostMicros
		}

		insert, err := stmt.ExecContext(
			ctx,
			event.ID,
			event.ProviderID,
			event.Source,
			event.SourceKey,
			event.SessionID,
			event.Model,
			event.OccurredAtUnixMS,
			event.InputTokens,
			event.CachedInputTokens,
			event.OutputTokens,
			event.TotalTokens,
			estimatedCost,
			event.CostStatus,
			metadataJSON,
			now,
			now,
		)
		if err != nil {
			return UsageInsertResult{}, err
		}
		if rows, err := insert.RowsAffected(); err == nil && rows > 0 {
			result.Inserted++
		}
	}
	result.Duplicates = len(events) - result.Inserted

	if err := tx.Commit(); err != nil {
		return UsageInsertResult{}, err
	}
	committed = true
	return result, nil
}

func (s *Store) GetUsageImportCursor(ctx context.Context, providerID string, source string, sourceKey string) (UsageImportCursor, error) {
	row := s.executor().QueryRowContext(
		ctx,
		`SELECT provider_id, source, source_key, modified_unix_ms, size_bytes,
			imported_events, invalid_lines, unsupported_lines, metadata_json, created_at_unix_ms, updated_at_unix_ms
		FROM usage_import_cursors
		WHERE provider_id = ? AND source = ? AND source_key = ?`,
		providerID,
		source,
		sourceKey,
	)
	cursor, err := scanUsageImportCursor(row)
	if errors.Is(err, sql.ErrNoRows) {
		return UsageImportCursor{}, ErrNotFound
	}
	return cursor, err
}

func (s *Store) UpsertUsageImportCursor(ctx context.Context, params UpsertUsageImportCursorParams) error {
	now := time.Now().UnixMilli()
	metadataJSON := params.MetadataJSON
	if metadataJSON == "" {
		metadataJSON = "{}"
	}
	_, err := s.executor().ExecContext(
		ctx,
		`INSERT INTO usage_import_cursors
			(provider_id, source, source_key, modified_unix_ms, size_bytes,
			 imported_events, invalid_lines, unsupported_lines, metadata_json, created_at_unix_ms, updated_at_unix_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(provider_id, source, source_key) DO UPDATE SET
			modified_unix_ms = excluded.modified_unix_ms,
			size_bytes = excluded.size_bytes,
			imported_events = excluded.imported_events,
			invalid_lines = excluded.invalid_lines,
			unsupported_lines = excluded.unsupported_lines,
			metadata_json = excluded.metadata_json,
			updated_at_unix_ms = excluded.updated_at_unix_ms`,
		params.ProviderID,
		params.Source,
		params.SourceKey,
		params.ModifiedUnixMS,
		params.SizeBytes,
		params.ImportedEvents,
		params.InvalidLines,
		params.UnsupportedLines,
		metadataJSON,
		now,
		now,
	)
	return err
}

func (s *Store) UsageSummary(ctx context.Context, providerID string) (UsageSummary, error) {
	row := s.executor().QueryRowContext(
		ctx,
		`SELECT
			COUNT(1),
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(cached_input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(total_tokens), 0),
			COALESCE(SUM(CASE WHEN estimated_cost_micros IS NULL THEN 0 ELSE estimated_cost_micros END), 0),
			COALESCE(SUM(CASE WHEN cost_status = ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN cost_status = ? THEN 1 ELSE 0 END), 0)
		FROM usage_events
		WHERE provider_id = ?`,
		UsageCostStatusUnknown,
		UsageCostStatusEstimated,
		providerID,
	)
	summary := UsageSummary{ProviderID: providerID}
	if err := row.Scan(
		&summary.EventCount,
		&summary.InputTokens,
		&summary.CachedInputTokens,
		&summary.OutputTokens,
		&summary.TotalTokens,
		&summary.EstimatedCostMicros,
		&summary.UnknownCostEvents,
		&summary.EstimatedCostEventCount,
	); err != nil {
		return UsageSummary{}, err
	}
	sources, err := s.usageSummarySources(ctx, providerID)
	if err != nil {
		return UsageSummary{}, err
	}
	summary.Sources = sources
	return summary, nil
}

func (s *Store) usageSummarySources(ctx context.Context, providerID string) ([]string, error) {
	rows, err := s.executor().QueryContext(
		ctx,
		`SELECT DISTINCT source
		FROM usage_events
		WHERE provider_id = ?
		ORDER BY source ASC`,
		providerID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sources []string
	for rows.Next() {
		var source string
		if err := rows.Scan(&source); err != nil {
			return nil, err
		}
		sources = append(sources, source)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return sources, nil
}

func (s *Store) CountProviderTargetReferences(ctx context.Context, providerID string) (int, error) {
	var count int
	err := s.executor().QueryRowContext(
		ctx,
		"SELECT COUNT(1) FROM profile_targets WHERE provider_id = ?",
		providerID,
	).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Store) CountProfileTargetReferences(ctx context.Context, profileID string) (int, error) {
	var count int
	err := s.executor().QueryRowContext(
		ctx,
		"SELECT COUNT(1) FROM profile_targets WHERE profile_id = ?",
		profileID,
	).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Store) schemaHealthy(ctx context.Context) (bool, error) {
	ok, err := s.objectExists(ctx, "table", "bun_migrations")
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}

	for _, spec := range initialTableSpecs {
		ok, err := s.tableSchemaHealthy(ctx, spec)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}

	for _, index := range initialIndexSpecs {
		ok, err := s.indexSchemaHealthy(ctx, index)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}

	for _, trigger := range initialTriggerSpecs {
		ok, err := s.triggerSchemaHealthy(ctx, trigger)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}

	return true, nil
}

func (s *Store) indexSchemaHealthy(ctx context.Context, spec indexSpec) (bool, error) {
	exists, unique, err := s.indexExistsOnTable(ctx, spec.table, spec.name)
	if err != nil {
		return false, err
	}
	if !exists {
		return false, nil
	}
	if unique != spec.unique {
		return false, nil
	}

	columns, err := s.indexColumns(ctx, spec.name)
	if err != nil {
		return false, err
	}
	if len(columns) != len(spec.columns) {
		return false, nil
	}
	for i := range spec.columns {
		if columns[i] != spec.columns[i] {
			return false, nil
		}
	}
	return true, nil
}

func (s *Store) tableSchemaHealthy(ctx context.Context, spec tableSpec) (bool, error) {
	ok, err := s.objectExists(ctx, "table", spec.name)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}

	columns, err := s.tableColumns(ctx, spec.name)
	if err != nil {
		return false, err
	}
	for _, want := range spec.columns {
		got, ok := columns[want.name]
		if !ok {
			return false, nil
		}
		if normalizeSQLiteValue(got.columnType) != normalizeSQLiteValue(want.columnType) {
			return false, nil
		}
		if want.notNull && !got.notNull {
			return false, nil
		}
		if want.primaryKey && !got.primaryKey {
			return false, nil
		}
		if want.requireDefault {
			if !got.defaultValue.Valid {
				return false, nil
			}
			if normalizeSQLiteValue(got.defaultValue.String) != normalizeSQLiteValue(want.defaultValue) {
				return false, nil
			}
		}
	}

	if len(spec.checks) == 0 {
		return true, nil
	}

	createSQL, err := s.tableCreateSQL(ctx, spec.name)
	if err != nil {
		return false, err
	}
	compactCreateSQL := compactSQL(createSQL)
	for _, check := range spec.checks {
		if !strings.Contains(compactCreateSQL, compactSQL(check)) {
			return false, nil
		}
	}

	return true, nil
}

func (s *Store) triggerSchemaHealthy(ctx context.Context, spec triggerSpec) (bool, error) {
	var table string
	var sqlText string
	err := s.executor().QueryRowContext(
		ctx,
		"SELECT tbl_name, sql FROM sqlite_master WHERE type = 'trigger' AND name = ?",
		spec.name,
	).Scan(&table, &sqlText)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if table != spec.table {
		return false, nil
	}
	compactSQLText := compactSQL(sqlText)
	for _, check := range spec.checks {
		if !strings.Contains(compactSQLText, compactSQL(check)) {
			return false, nil
		}
	}
	return true, nil
}

func (s *Store) indexExistsOnTable(ctx context.Context, table string, index string) (bool, bool, error) {
	rows, err := s.executor().QueryContext(ctx, fmt.Sprintf("PRAGMA index_list(%s)", quoteSQLiteIdentifier(table)))
	if err != nil {
		return false, false, err
	}
	defer rows.Close()

	for rows.Next() {
		var seq int
		var name string
		var unique int
		var origin string
		var partial int
		if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			return false, false, err
		}
		if name == index {
			return true, unique != 0, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, false, err
	}
	return false, false, nil
}

func (s *Store) indexColumns(ctx context.Context, index string) ([]string, error) {
	rows, err := s.executor().QueryContext(ctx, fmt.Sprintf("PRAGMA index_info(%s)", quoteSQLiteIdentifier(index)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var columns []string
	for rows.Next() {
		var seqno int
		var cid int
		var name sql.NullString
		if err := rows.Scan(&seqno, &cid, &name); err != nil {
			return nil, err
		}
		if !name.Valid {
			return nil, nil
		}
		columns = append(columns, name.String)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return columns, nil
}

func (s *Store) tableColumns(ctx context.Context, table string) (map[string]columnInfo, error) {
	rows, err := s.executor().QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", quoteSQLiteIdentifier(table)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	columns := make(map[string]columnInfo)
	for rows.Next() {
		var cid int
		var name string
		var columnType string
		var notNull int
		var defaultValue sql.NullString
		var primaryKey int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, err
		}
		columns[name] = columnInfo{
			columnType:   columnType,
			notNull:      notNull != 0,
			primaryKey:   primaryKey != 0,
			defaultValue: defaultValue,
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return columns, nil
}

func (s *Store) tableCreateSQL(ctx context.Context, table string) (string, error) {
	var createSQL string
	err := s.executor().QueryRowContext(
		ctx,
		"SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?",
		table,
	).Scan(&createSQL)
	if err != nil {
		return "", err
	}
	return createSQL, nil
}

func (s *Store) objectExists(ctx context.Context, objectType, name string) (bool, error) {
	var count int
	err := s.executor().QueryRowContext(
		ctx,
		"SELECT COUNT(1) FROM sqlite_master WHERE type = ? AND name = ?",
		objectType,
		name,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func quoteSQLiteIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

func normalizeSQLiteValue(value string) string {
	return strings.ToUpper(strings.TrimSpace(value))
}

func compactSQL(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, value)
}

func (s *Store) countOperations(ctx context.Context, status string) (int, error) {
	var count int
	err := s.executor().QueryRowContext(
		ctx,
		"SELECT COUNT(1) FROM operations WHERE status = ?",
		status,
	).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Store) profileReferenceCount(ctx context.Context, profileID string) (int, error) {
	var activeStateCount int
	if err := s.executor().QueryRowContext(
		ctx,
		"SELECT COUNT(1) FROM active_states WHERE profile_id = ?",
		profileID,
	).Scan(&activeStateCount); err != nil {
		return 0, err
	}

	var operationCount int
	if err := s.executor().QueryRowContext(
		ctx,
		"SELECT COUNT(1) FROM operations WHERE profile_id = ?",
		profileID,
	).Scan(&operationCount); err != nil {
		return 0, err
	}

	targetCount, err := s.CountProfileTargetReferences(ctx, profileID)
	if err != nil {
		return 0, err
	}

	return activeStateCount + operationCount + targetCount, nil
}

func (s *Store) providerReferenceCount(ctx context.Context, providerID string) (int, error) {
	targetCount, err := s.CountProviderTargetReferences(ctx, providerID)
	if err != nil {
		return 0, err
	}

	var activeStateCount int
	if err := s.executor().QueryRowContext(
		ctx,
		"SELECT COUNT(1) FROM active_states WHERE scope_type = ? AND scope_id = ?",
		ActiveStateScopeProvider,
		providerID,
	).Scan(&activeStateCount); err != nil {
		return 0, err
	}

	operationCount, err := s.providerOperationReferenceCount(ctx, providerID)
	if err != nil {
		return 0, err
	}

	return targetCount + activeStateCount + operationCount, nil
}

func (s *Store) providerOperationReferenceCount(ctx context.Context, providerID string) (int, error) {
	rows, err := s.executor().QueryContext(ctx, "SELECT metadata_json FROM operations")
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var metadataJSON string
		if err := rows.Scan(&metadataJSON); err != nil {
			return 0, err
		}
		var metadata struct {
			ProviderID string `json:"provider_id"`
		}
		if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
			continue
		}
		if metadata.ProviderID == providerID {
			count++
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	return count, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanProvider(row rowScanner) (Provider, error) {
	var provider Provider
	var enabled int
	if err := row.Scan(
		&provider.ID,
		&provider.Name,
		&provider.AdapterID,
		&enabled,
		&provider.MetadataJSON,
		&provider.CreatedAtUnixMS,
		&provider.UpdatedAtUnixMS,
	); err != nil {
		return Provider{}, err
	}
	provider.Enabled = enabled != 0
	return provider, nil
}

func scanProfile(row rowScanner) (Profile, error) {
	var profile Profile
	if err := row.Scan(
		&profile.ID,
		&profile.Name,
		&profile.Description,
		&profile.MetadataJSON,
		&profile.CreatedAtUnixMS,
		&profile.UpdatedAtUnixMS,
	); err != nil {
		return Profile{}, err
	}
	return profile, nil
}

func scanProfileTarget(row rowScanner) (ProfileTarget, error) {
	var target ProfileTarget
	var enabled int
	if err := row.Scan(
		&target.ProfileID,
		&target.ProviderID,
		&target.TargetID,
		&target.Path,
		&target.PathKey,
		&target.Format,
		&target.Strategy,
		&target.ValueJSON,
		&enabled,
		&target.MetadataJSON,
		&target.CreatedAtUnixMS,
		&target.UpdatedAtUnixMS,
	); err != nil {
		return ProfileTarget{}, err
	}
	target.Enabled = enabled != 0
	return target, nil
}

func scanProviderAccountSecret(row rowScanner) (ProviderAccountSecret, error) {
	var secret ProviderAccountSecret
	if err := row.Scan(
		&secret.ProviderID,
		&secret.AccountID,
		&secret.SecretKind,
		&secret.PayloadJSON,
		&secret.PayloadSHA256,
		&secret.DisplayName,
		&secret.MetadataJSON,
		&secret.CreatedAtUnixMS,
		&secret.UpdatedAtUnixMS,
	); err != nil {
		return ProviderAccountSecret{}, err
	}
	return secret, nil
}

func scanOperation(row rowScanner) (Operation, error) {
	var operation Operation
	if err := row.Scan(
		&operation.ID,
		&operation.OperationType,
		&operation.Status,
		&operation.ProfileID,
		&operation.MetadataJSON,
		&operation.ErrorCode,
		&operation.ErrorMessage,
		&operation.CreatedAtUnixMS,
		&operation.UpdatedAtUnixMS,
	); err != nil {
		return Operation{}, err
	}
	return operation, nil
}

func scanActiveState(row rowScanner) (ActiveState, error) {
	var activeState ActiveState
	if err := row.Scan(
		&activeState.ScopeType,
		&activeState.ScopeID,
		&activeState.ProfileID,
		&activeState.OperationID,
		&activeState.UpdatedAtUnixMS,
	); err != nil {
		return ActiveState{}, err
	}
	return activeState, nil
}

func scanUsageImportCursor(row rowScanner) (UsageImportCursor, error) {
	var cursor UsageImportCursor
	if err := row.Scan(
		&cursor.ProviderID,
		&cursor.Source,
		&cursor.SourceKey,
		&cursor.ModifiedUnixMS,
		&cursor.SizeBytes,
		&cursor.ImportedEvents,
		&cursor.InvalidLines,
		&cursor.UnsupportedLines,
		&cursor.MetadataJSON,
		&cursor.CreatedAtUnixMS,
		&cursor.UpdatedAtUnixMS,
	); err != nil {
		return UsageImportCursor{}, err
	}
	return cursor, nil
}

func isSQLiteConstraintError(err error) bool {
	var sqliteErr *sqlite.Error
	return errors.As(err, &sqliteErr) && sqliteErr.Code()&0xff == sqlite3.SQLITE_CONSTRAINT
}

func profileTargetConstraintError(err error) error {
	if strings.Contains(err.Error(), profileTargetPathOwnerMessage) {
		return ErrPathOwned
	}
	return ErrAlreadyExists
}

func sqliteDSN(databasePath string, readOnly bool) string {
	mode := "rwc"
	if readOnly {
		mode = "ro"
	}

	u := url.URL{
		Scheme: "file",
		Path:   strings.ReplaceAll(databasePath, `\`, `/`),
	}
	q := u.Query()
	q.Set("mode", mode)
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", sqliteBusyTimeout.Milliseconds()))
	u.RawQuery = q.Encode()

	return u.String()
}
