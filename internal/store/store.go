package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"github.com/uptrace/bun/migrate"
	sqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/strahe/profiledeck/internal/store/migrations"
)

const (
	sqliteDriverName  = "sqlite"
	sqliteBusyTimeout = 5 * time.Second

	OperationTypeSwitch      = "switch"
	OperationTypeRollback    = "rollback"
	OperationTypeImport      = "import"
	OperationTypeMaintenance = "maintenance"

	OperationStatusPending = "pending"
	OperationStatusFailed  = "failed"
	OperationStatusApplied = "applied"

	ActiveStateScopeProvider = "provider"

	UsageCostStatusEstimated = "estimated"
	UsageCostStatusPartial   = "partial"
	UsageCostStatusUnknown   = "unknown"

	maxProviderCredentialPayloadBytes = 16 * 1024 * 1024
	maxProviderConfigSetPayloadBytes  = 16 * 1024 * 1024
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
	PrepareContext(ctx context.Context, query string) (*sql.Stmt, error)
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

type ProviderCredential struct {
	ID              string
	ProviderID      string
	CredentialKind  string
	PayloadJSON     string
	PayloadSHA256   string
	MetadataJSON    string
	CreatedAtUnixMS int64
	UpdatedAtUnixMS int64
}

type ProviderConfigSet struct {
	ID              string
	ProviderID      string
	ConfigKind      string
	Name            string
	Description     string
	PayloadText     string
	PayloadSHA256   string
	MetadataJSON    string
	CreatedAtUnixMS int64
	UpdatedAtUnixMS int64
}

type ProfileCredentialBinding struct {
	ProfileID       string
	ProviderID      string
	SlotID          string
	CredentialID    string
	CreatedAtUnixMS int64
	UpdatedAtUnixMS int64
}

type ProfileConfigSetBinding struct {
	ProfileID       string
	ProviderID      string
	SlotID          string
	ConfigSetID     string
	CreatedAtUnixMS int64
	UpdatedAtUnixMS int64
}

type Setting struct {
	Key             string
	ValueJSON       string
	UpdatedAtUnixMS int64
}

type ProviderProfileSetting struct {
	ProfileID                   string
	ProviderID                  string
	QuotaRefreshIntervalSeconds int
	AuthKeepaliveEnabled        bool
	UpdatedAtUnixMS             int64
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

type CreateAppliedMaintenanceOperationParams struct {
	ID           string
	ProfileID    string
	ProviderID   string
	MetadataJSON string
	SetActive    bool
}

type CreateAppliedImportOperationParams struct {
	ID           string
	MetadataJSON string
}

type MarkOperationFailedParams struct {
	ID           string
	ErrorCode    string
	ErrorMessage string
	MetadataJSON *string
}

type CompleteSwitchOperationParams struct {
	ID                string
	ProfileID         string
	ProviderID        string
	MetadataJSON      string
	CredentialUpdates []UpsertProviderCredentialParams
	ConfigSetUpdates  []UpsertProviderConfigSetParams
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

type UsageCostCandidate struct {
	ID                string
	Model             string
	InputTokens       int64
	CachedInputTokens int64
	OutputTokens      int64
	TotalTokens       int64
}

type UpdateUsageEventCostParams struct {
	ID                  string
	EstimatedCostMicros int64
	CostStatus          string
}

type CommitUsageImportParams struct {
	Events []CreateUsageEventParams
	Cursor UpsertUsageImportCursorParams
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

type UpsertProviderCredentialParams struct {
	ID             string
	ProviderID     string
	CredentialKind string
	PayloadJSON    string
	PayloadSHA256  string
	MetadataJSON   string
}

type UpsertProviderConfigSetParams struct {
	ID            string
	ProviderID    string
	ConfigKind    string
	Name          string
	Description   string
	PayloadText   string
	PayloadSHA256 string
	MetadataJSON  string
}

type UpsertProfileCredentialBindingParams struct {
	ProfileID    string
	ProviderID   string
	SlotID       string
	CredentialID string
}

type UpsertProfileConfigSetBindingParams struct {
	ProfileID   string
	ProviderID  string
	SlotID      string
	ConfigSetID string
}

type UpdateProviderConfigSetParams struct {
	ID           string
	Name         *string
	Description  *string
	MetadataJSON *string
}

type UpsertSettingParams struct {
	Key       string
	ValueJSON string
}

type UpsertProviderProfileSettingParams struct {
	ProfileID                   string
	ProviderID                  string
	QuotaRefreshIntervalSeconds int
	AuthKeepaliveEnabled        bool
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
		name: "provider_profile_settings",
		columns: []columnSpec{
			{name: "profile_id", columnType: "TEXT", notNull: true, primaryKey: true},
			{name: "provider_id", columnType: "TEXT", notNull: true, primaryKey: true},
			{name: "quota_refresh_interval_seconds", columnType: "INTEGER", notNull: true, requireDefault: true, defaultValue: "0"},
			{name: "auth_keepalive_enabled", columnType: "INTEGER", notNull: true, requireDefault: true, defaultValue: "0"},
			{name: "updated_at_unix_ms", columnType: "INTEGER", notNull: true},
		},
		checks: []string{
			"CHECK (quota_refresh_interval_seconds IN (0, 300, 600, 1800, 3600))",
			"CHECK (auth_keepalive_enabled IN (0, 1))",
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
			"CHECK (cost_status IN ('estimated', 'partial', 'unknown'))",
			"CHECK ((cost_status IN ('estimated', 'partial') AND estimated_cost_micros IS NOT NULL) OR (cost_status = 'unknown' AND estimated_cost_micros IS NULL))",
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
		name: "provider_credentials",
		columns: []columnSpec{
			{name: "id", columnType: "TEXT", primaryKey: true},
			{name: "provider_id", columnType: "TEXT", notNull: true},
			{name: "credential_kind", columnType: "TEXT", notNull: true},
			{name: "payload_json", columnType: "TEXT", notNull: true},
			{name: "payload_sha256", columnType: "TEXT", notNull: true},
			{name: "metadata_json", columnType: "TEXT", notNull: true, requireDefault: true, defaultValue: "'{}'"},
			{name: "created_at_unix_ms", columnType: "INTEGER", notNull: true},
			{name: "updated_at_unix_ms", columnType: "INTEGER", notNull: true},
		},
	},
	{
		name: "provider_config_sets",
		columns: []columnSpec{
			{name: "id", columnType: "TEXT", primaryKey: true},
			{name: "provider_id", columnType: "TEXT", notNull: true},
			{name: "config_kind", columnType: "TEXT", notNull: true},
			{name: "name", columnType: "TEXT", notNull: true},
			{name: "description", columnType: "TEXT", notNull: true, requireDefault: true, defaultValue: "''"},
			{name: "payload_text", columnType: "TEXT", notNull: true},
			{name: "payload_sha256", columnType: "TEXT", notNull: true},
			{name: "metadata_json", columnType: "TEXT", notNull: true, requireDefault: true, defaultValue: "'{}'"},
			{name: "created_at_unix_ms", columnType: "INTEGER", notNull: true},
			{name: "updated_at_unix_ms", columnType: "INTEGER", notNull: true},
		},
	},
	{
		name: "profile_credential_bindings",
		columns: []columnSpec{
			{name: "profile_id", columnType: "TEXT", notNull: true, primaryKey: true},
			{name: "provider_id", columnType: "TEXT", notNull: true, primaryKey: true},
			{name: "slot_id", columnType: "TEXT", notNull: true, primaryKey: true},
			{name: "credential_id", columnType: "TEXT", notNull: true},
			{name: "created_at_unix_ms", columnType: "INTEGER", notNull: true},
			{name: "updated_at_unix_ms", columnType: "INTEGER", notNull: true},
		},
		checks: []string{
			"FOREIGN KEY (profile_id) REFERENCES profiles(id) ON UPDATE RESTRICT ON DELETE RESTRICT",
			"FOREIGN KEY (provider_id) REFERENCES providers(id) ON UPDATE RESTRICT ON DELETE RESTRICT",
			"FOREIGN KEY (provider_id, credential_id) REFERENCES provider_credentials(provider_id, id) ON UPDATE RESTRICT ON DELETE RESTRICT",
		},
	},
	{
		name: "profile_config_set_bindings",
		columns: []columnSpec{
			{name: "profile_id", columnType: "TEXT", notNull: true, primaryKey: true},
			{name: "provider_id", columnType: "TEXT", notNull: true, primaryKey: true},
			{name: "slot_id", columnType: "TEXT", notNull: true, primaryKey: true},
			{name: "config_set_id", columnType: "TEXT", notNull: true},
			{name: "created_at_unix_ms", columnType: "INTEGER", notNull: true},
			{name: "updated_at_unix_ms", columnType: "INTEGER", notNull: true},
		},
		checks: []string{
			"FOREIGN KEY (profile_id) REFERENCES profiles(id) ON UPDATE RESTRICT ON DELETE RESTRICT",
			"FOREIGN KEY (provider_id) REFERENCES providers(id) ON UPDATE RESTRICT ON DELETE RESTRICT",
			"FOREIGN KEY (provider_id, config_set_id) REFERENCES provider_config_sets(provider_id, id) ON UPDATE RESTRICT ON DELETE RESTRICT",
		},
	},
}

var initialIndexSpecs = []indexSpec{
	{name: "idx_providers_adapter_id", table: "providers", columns: []string{"adapter_id"}},
	{name: "idx_providers_enabled", table: "providers", columns: []string{"enabled"}},
	{name: "idx_provider_profile_settings_provider_id", table: "provider_profile_settings", columns: []string{"provider_id"}},
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
	{name: "idx_usage_events_provider_cost_model_id", table: "usage_events", columns: []string{"provider_id", "cost_status", "model", "id"}},
	{name: "idx_usage_import_cursors_source", table: "usage_import_cursors", columns: []string{"source"}},
	{name: "idx_provider_credentials_provider_id", table: "provider_credentials", columns: []string{"provider_id"}},
	{name: "idx_provider_credentials_kind", table: "provider_credentials", columns: []string{"credential_kind"}},
	{name: "idx_provider_credentials_provider_id_id", table: "provider_credentials", columns: []string{"provider_id", "id"}, unique: true},
	{name: "idx_provider_config_sets_provider_id", table: "provider_config_sets", columns: []string{"provider_id"}},
	{name: "idx_provider_config_sets_kind", table: "provider_config_sets", columns: []string{"config_kind"}},
	{name: "idx_provider_config_sets_provider_id_id", table: "provider_config_sets", columns: []string{"provider_id", "id"}, unique: true},
	{name: "idx_profile_credential_bindings_provider_id", table: "profile_credential_bindings", columns: []string{"provider_id"}},
	{name: "idx_profile_credential_bindings_credential_id", table: "profile_credential_bindings", columns: []string{"credential_id"}},
	{name: "idx_profile_config_set_bindings_provider_id", table: "profile_config_set_bindings", columns: []string{"provider_id"}},
	{name: "idx_profile_config_set_bindings_config_set_id", table: "profile_config_set_bindings", columns: []string{"config_set_id"}},
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
			AND NOT EXISTS (SELECT 1 FROM provider_credentials WHERE provider_id = ?)
			AND NOT EXISTS (SELECT 1 FROM provider_config_sets WHERE provider_id = ?)
			AND NOT EXISTS (SELECT 1 FROM profile_credential_bindings WHERE provider_id = ?)
			AND NOT EXISTS (SELECT 1 FROM profile_config_set_bindings WHERE provider_id = ?)
			AND NOT EXISTS (SELECT 1 FROM provider_profile_settings WHERE provider_id = ?)
			AND NOT EXISTS (SELECT 1 FROM active_states WHERE scope_type = ? AND scope_id = ?)
	`, id, id, id, id, id, id, id, ActiveStateScopeProvider, id)
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
	if !s.transactional {
		return s.WithTransaction(ctx, func(txStore *Store) error {
			return txStore.DeleteProfile(ctx, id)
		})
	}
	// Profile automation is local runtime preference data. Remove it in the
	// same transaction as the Profile so failed/in-use deletes keep settings.
	if _, err := s.executor().ExecContext(ctx, `DELETE FROM provider_profile_settings WHERE profile_id = ?`, id); err != nil {
		return err
	}
	result, err := s.executor().ExecContext(ctx, `
		DELETE FROM profiles
		WHERE id = ?
			AND NOT EXISTS (SELECT 1 FROM active_states WHERE profile_id = ?)
			AND NOT EXISTS (SELECT 1 FROM operations WHERE profile_id = ?)
			AND NOT EXISTS (SELECT 1 FROM profile_targets WHERE profile_id = ?)
			AND NOT EXISTS (SELECT 1 FROM profile_credential_bindings WHERE profile_id = ?)
			AND NOT EXISTS (SELECT 1 FROM profile_config_set_bindings WHERE profile_id = ?)
	`, id, id, id, id, id, id)
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

func (s *Store) ListProfileTargets(ctx context.Context, profileID, providerID string, includeDisabled bool) ([]ProfileTarget, error) {
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

func (s *Store) GetProfileTarget(ctx context.Context, profileID, providerID, targetID string) (ProfileTarget, error) {
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

func (s *Store) DeleteProfileTarget(ctx context.Context, profileID, providerID, targetID string) error {
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

func (s *Store) GetSetting(ctx context.Context, key string) (Setting, error) {
	row := s.executor().QueryRowContext(
		ctx,
		`SELECT key, value_json, updated_at_unix_ms
		FROM settings
		WHERE key = ?`,
		strings.TrimSpace(key),
	)
	setting, err := scanSetting(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Setting{}, ErrNotFound
	}
	return setting, err
}

func (s *Store) DeleteSetting(ctx context.Context, key string) error {
	result, err := s.executor().ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, strings.TrimSpace(key))
	if err != nil {
		return err
	}
	if rows, err := result.RowsAffected(); err != nil {
		return err
	} else if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) GetProviderProfileSetting(ctx context.Context, profileID, providerID string) (ProviderProfileSetting, error) {
	row := s.executor().QueryRowContext(ctx, `
		SELECT profile_id, provider_id, quota_refresh_interval_seconds, auth_keepalive_enabled, updated_at_unix_ms
		FROM provider_profile_settings
		WHERE profile_id = ? AND provider_id = ?
	`, strings.TrimSpace(profileID), strings.TrimSpace(providerID))
	setting, err := scanProviderProfileSetting(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ProviderProfileSetting{}, ErrNotFound
	}
	return setting, err
}

func (s *Store) ListProviderProfileSettings(ctx context.Context, providerID string) ([]ProviderProfileSetting, error) {
	rows, err := s.executor().QueryContext(ctx, `
		SELECT profile_id, provider_id, quota_refresh_interval_seconds, auth_keepalive_enabled, updated_at_unix_ms
		FROM provider_profile_settings
		WHERE provider_id = ?
		ORDER BY profile_id ASC
	`, strings.TrimSpace(providerID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	settings := []ProviderProfileSetting{}
	for rows.Next() {
		setting, err := scanProviderProfileSetting(rows)
		if err != nil {
			return nil, err
		}
		settings = append(settings, setting)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return settings, nil
}

func (s *Store) UpsertProviderProfileSetting(ctx context.Context, params UpsertProviderProfileSettingParams) (ProviderProfileSetting, error) {
	profileID := strings.TrimSpace(params.ProfileID)
	providerID := strings.TrimSpace(params.ProviderID)
	if profileID == "" || providerID == "" {
		return ProviderProfileSetting{}, errors.New("provider profile setting profile_id and provider_id are required")
	}
	switch params.QuotaRefreshIntervalSeconds {
	case 0, 300, 600, 1800, 3600:
	default:
		return ProviderProfileSetting{}, errors.New("provider profile quota refresh interval is unsupported")
	}
	if _, err := s.GetProfile(ctx, profileID); err != nil {
		return ProviderProfileSetting{}, err
	}
	if _, err := s.GetProvider(ctx, providerID); err != nil {
		return ProviderProfileSetting{}, err
	}
	keepalive := 0
	if params.AuthKeepaliveEnabled {
		keepalive = 1
	}
	now := time.Now().UnixMilli()
	_, err := s.executor().ExecContext(ctx, `
		INSERT INTO provider_profile_settings
			(profile_id, provider_id, quota_refresh_interval_seconds, auth_keepalive_enabled, updated_at_unix_ms)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(profile_id, provider_id) DO UPDATE SET
			quota_refresh_interval_seconds = excluded.quota_refresh_interval_seconds,
			auth_keepalive_enabled = excluded.auth_keepalive_enabled,
			updated_at_unix_ms = excluded.updated_at_unix_ms
	`, profileID, providerID, params.QuotaRefreshIntervalSeconds, keepalive, now)
	if err != nil {
		return ProviderProfileSetting{}, err
	}
	return s.GetProviderProfileSetting(ctx, profileID, providerID)
}

func (s *Store) UpsertSetting(ctx context.Context, params UpsertSettingParams) (Setting, error) {
	if err := validateSettingParams(params); err != nil {
		return Setting{}, err
	}
	key := strings.TrimSpace(params.Key)
	now := time.Now().UnixMilli()
	_, err := s.executor().ExecContext(
		ctx,
		`INSERT INTO settings (key, value_json, updated_at_unix_ms)
		VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET
			value_json = excluded.value_json,
			updated_at_unix_ms = excluded.updated_at_unix_ms`,
		key,
		params.ValueJSON,
		now,
	)
	if err != nil {
		return Setting{}, err
	}
	return s.GetSetting(ctx, key)
}

func validateSettingParams(params UpsertSettingParams) error {
	if strings.TrimSpace(params.Key) == "" {
		return fmt.Errorf("setting key is required")
	}
	decoder := json.NewDecoder(strings.NewReader(params.ValueJSON))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return fmt.Errorf("setting value must contain one JSON value")
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func (s *Store) UpsertProviderCredential(ctx context.Context, params UpsertProviderCredentialParams) (ProviderCredential, error) {
	if err := validateProviderCredentialParams(params); err != nil {
		return ProviderCredential{}, err
	}
	id := strings.TrimSpace(params.ID)
	providerID := strings.TrimSpace(params.ProviderID)
	credentialKind := strings.TrimSpace(params.CredentialKind)
	now := time.Now().UnixMilli()
	metadataJSON := params.MetadataJSON
	if metadataJSON == "" {
		metadataJSON = "{}"
	}
	_, err := s.executor().ExecContext(
		ctx,
		`INSERT INTO provider_credentials
			(id, provider_id, credential_kind, payload_json, payload_sha256, metadata_json, created_at_unix_ms, updated_at_unix_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			payload_json = excluded.payload_json,
			payload_sha256 = excluded.payload_sha256,
			metadata_json = excluded.metadata_json,
			updated_at_unix_ms = excluded.updated_at_unix_ms
		WHERE provider_credentials.provider_id = excluded.provider_id
			AND provider_credentials.credential_kind = excluded.credential_kind`,
		id,
		providerID,
		credentialKind,
		params.PayloadJSON,
		strings.ToLower(params.PayloadSHA256),
		metadataJSON,
		now,
		now,
	)
	if err != nil {
		return ProviderCredential{}, err
	}
	credential, err := s.GetProviderCredential(ctx, id)
	if err != nil {
		return ProviderCredential{}, err
	}
	// Credential IDs have one provider/kind identity for their full lifetime;
	// external account metadata must never retarget an existing credential.
	if credential.ProviderID != providerID || credential.CredentialKind != credentialKind {
		return ProviderCredential{}, fmt.Errorf("provider credential identity does not match existing record")
	}
	return credential, nil
}

func (s *Store) CompareAndSwapProviderCredential(ctx context.Context, expectedPayloadSHA256 string, params UpsertProviderCredentialParams) (ProviderCredential, bool, error) {
	if err := validateProviderCredentialParams(params); err != nil {
		return ProviderCredential{}, false, err
	}
	expected := strings.ToLower(strings.TrimSpace(expectedPayloadSHA256))
	if digest, err := hex.DecodeString(expected); err != nil || len(digest) != sha256.Size {
		return ProviderCredential{}, false, errors.New("expected provider credential payload sha256 is invalid")
	}
	now := time.Now().UnixMilli()
	metadataJSON := params.MetadataJSON
	if metadataJSON == "" {
		metadataJSON = "{}"
	}
	result, err := s.executor().ExecContext(ctx, `
		UPDATE provider_credentials SET
			payload_json = ?, payload_sha256 = ?, metadata_json = ?, updated_at_unix_ms = ?
		WHERE id = ? AND provider_id = ? AND credential_kind = ? AND payload_sha256 = ?
	`, params.PayloadJSON, strings.ToLower(params.PayloadSHA256), metadataJSON, now, strings.TrimSpace(params.ID), strings.TrimSpace(params.ProviderID), strings.TrimSpace(params.CredentialKind), expected)
	if err != nil {
		return ProviderCredential{}, false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return ProviderCredential{}, false, err
	}
	if rows == 0 {
		if _, err := s.GetProviderCredential(ctx, params.ID); err != nil {
			return ProviderCredential{}, false, err
		}
		return ProviderCredential{}, false, nil
	}
	credential, err := s.GetProviderCredential(ctx, params.ID)
	return credential, true, err
}

func validateProviderCredentialParams(params UpsertProviderCredentialParams) error {
	if strings.TrimSpace(params.ID) == "" {
		return fmt.Errorf("provider credential id is required")
	}
	if strings.TrimSpace(params.ProviderID) == "" {
		return fmt.Errorf("provider credential provider_id is required")
	}
	if strings.TrimSpace(params.CredentialKind) == "" {
		return fmt.Errorf("provider credential kind is required")
	}
	if len(params.PayloadJSON) > maxProviderCredentialPayloadBytes {
		return fmt.Errorf("provider credential payload is too large")
	}
	digest, err := hex.DecodeString(params.PayloadSHA256)
	if err != nil || len(digest) != sha256.Size {
		return fmt.Errorf("provider credential payload sha256 is invalid")
	}
	actual := sha256.Sum256([]byte(params.PayloadJSON))
	if !strings.EqualFold(params.PayloadSHA256, hex.EncodeToString(actual[:])) {
		return fmt.Errorf("provider credential payload sha256 does not match payload")
	}
	decoder := json.NewDecoder(strings.NewReader(params.PayloadJSON))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	if _, ok := value.(map[string]any); !ok {
		return fmt.Errorf("provider credential payload must be a JSON object")
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return fmt.Errorf("provider credential payload must contain one JSON object")
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func (s *Store) GetProviderCredential(ctx context.Context, id string) (ProviderCredential, error) {
	row := s.executor().QueryRowContext(
		ctx,
		`SELECT id, provider_id, credential_kind, payload_json, payload_sha256, metadata_json, created_at_unix_ms, updated_at_unix_ms
		FROM provider_credentials
		WHERE id = ?`,
		id,
	)
	credential, err := scanProviderCredential(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ProviderCredential{}, ErrNotFound
	}
	return credential, err
}

func (s *Store) ListProviderCredentials(ctx context.Context, providerID string) ([]ProviderCredential, error) {
	rows, err := s.executor().QueryContext(
		ctx,
		`SELECT id, provider_id, credential_kind, payload_json, payload_sha256, metadata_json, created_at_unix_ms, updated_at_unix_ms
		FROM provider_credentials
		WHERE provider_id = ?
		ORDER BY id ASC`,
		providerID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	credentials := []ProviderCredential{}
	for rows.Next() {
		credential, err := scanProviderCredential(rows)
		if err != nil {
			return nil, err
		}
		credentials = append(credentials, credential)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return credentials, nil
}

func (s *Store) UpsertProviderConfigSet(ctx context.Context, params UpsertProviderConfigSetParams) (ProviderConfigSet, error) {
	if err := validateProviderConfigSetParams(params); err != nil {
		return ProviderConfigSet{}, err
	}
	id := strings.TrimSpace(params.ID)
	now := time.Now().UnixMilli()
	metadataJSON := params.MetadataJSON
	if metadataJSON == "" {
		metadataJSON = "{}"
	}
	_, err := s.executor().ExecContext(
		ctx,
		`INSERT INTO provider_config_sets
			(id, provider_id, config_kind, name, description, payload_text, payload_sha256, metadata_json, created_at_unix_ms, updated_at_unix_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			description = excluded.description,
			payload_text = excluded.payload_text,
			payload_sha256 = excluded.payload_sha256,
			metadata_json = excluded.metadata_json,
			updated_at_unix_ms = excluded.updated_at_unix_ms
		WHERE provider_config_sets.provider_id = excluded.provider_id
			AND provider_config_sets.config_kind = excluded.config_kind`,
		id,
		strings.TrimSpace(params.ProviderID),
		strings.TrimSpace(params.ConfigKind),
		strings.TrimSpace(params.Name),
		strings.TrimSpace(params.Description),
		params.PayloadText,
		strings.ToLower(params.PayloadSHA256),
		metadataJSON,
		now,
		now,
	)
	if err != nil {
		return ProviderConfigSet{}, err
	}
	configSet, err := s.GetProviderConfigSet(ctx, id)
	if err != nil {
		return ProviderConfigSet{}, err
	}
	// A Config Set ID has one provider/kind identity for its full lifetime;
	// capture updates may change only its metadata and payload.
	if configSet.ProviderID != strings.TrimSpace(params.ProviderID) || configSet.ConfigKind != strings.TrimSpace(params.ConfigKind) {
		return ProviderConfigSet{}, fmt.Errorf("provider config set identity does not match existing record")
	}
	return configSet, nil
}

func validateProviderConfigSetParams(params UpsertProviderConfigSetParams) error {
	if strings.TrimSpace(params.ID) == "" {
		return fmt.Errorf("provider config set id is required")
	}
	if strings.TrimSpace(params.ProviderID) == "" {
		return fmt.Errorf("provider config set provider_id is required")
	}
	if strings.TrimSpace(params.ConfigKind) == "" {
		return fmt.Errorf("provider config set kind is required")
	}
	if strings.TrimSpace(params.Name) == "" {
		return fmt.Errorf("provider config set name is required")
	}
	if len(params.PayloadText) > maxProviderConfigSetPayloadBytes {
		return fmt.Errorf("provider config set payload is too large")
	}
	digest, err := hex.DecodeString(params.PayloadSHA256)
	if err != nil || len(digest) != sha256.Size {
		return fmt.Errorf("provider config set payload sha256 is invalid")
	}
	actual := sha256.Sum256([]byte(params.PayloadText))
	if !strings.EqualFold(params.PayloadSHA256, hex.EncodeToString(actual[:])) {
		return fmt.Errorf("provider config set payload sha256 does not match payload")
	}
	metadataJSON := params.MetadataJSON
	if metadataJSON == "" {
		metadataJSON = "{}"
	}
	if err := validateJSONObject(metadataJSON, "provider config set metadata"); err != nil {
		return err
	}
	return nil
}

func (s *Store) GetProviderConfigSet(ctx context.Context, id string) (ProviderConfigSet, error) {
	row := s.executor().QueryRowContext(
		ctx,
		`SELECT id, provider_id, config_kind, name, description, payload_text, payload_sha256, metadata_json, created_at_unix_ms, updated_at_unix_ms
		FROM provider_config_sets
		WHERE id = ?`,
		strings.TrimSpace(id),
	)
	configSet, err := scanProviderConfigSet(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ProviderConfigSet{}, ErrNotFound
	}
	return configSet, err
}

func (s *Store) ListProviderConfigSets(ctx context.Context, providerID, configKind string) ([]ProviderConfigSet, error) {
	query := `SELECT id, provider_id, config_kind, name, description, payload_text, payload_sha256, metadata_json, created_at_unix_ms, updated_at_unix_ms
		FROM provider_config_sets
		WHERE provider_id = ?`
	args := []any{strings.TrimSpace(providerID)}
	if strings.TrimSpace(configKind) != "" {
		query += " AND config_kind = ?"
		args = append(args, strings.TrimSpace(configKind))
	}
	query += " ORDER BY name COLLATE NOCASE ASC, id ASC"
	rows, err := s.executor().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	configSets := []ProviderConfigSet{}
	for rows.Next() {
		configSet, err := scanProviderConfigSet(rows)
		if err != nil {
			return nil, err
		}
		configSets = append(configSets, configSet)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return configSets, nil
}

func (s *Store) UpsertProfileCredentialBinding(ctx context.Context, params UpsertProfileCredentialBindingParams) (ProfileCredentialBinding, error) {
	profileID := strings.TrimSpace(params.ProfileID)
	providerID := strings.TrimSpace(params.ProviderID)
	slotID := strings.TrimSpace(params.SlotID)
	credentialID := strings.TrimSpace(params.CredentialID)
	if profileID == "" || providerID == "" || slotID == "" || credentialID == "" {
		return ProfileCredentialBinding{}, errors.New("profile credential binding fields are required")
	}
	now := time.Now().UnixMilli()
	_, err := s.executor().ExecContext(ctx, `
		INSERT INTO profile_credential_bindings
			(profile_id, provider_id, slot_id, credential_id, created_at_unix_ms, updated_at_unix_ms)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(profile_id, provider_id, slot_id) DO UPDATE SET
			credential_id = excluded.credential_id,
			updated_at_unix_ms = excluded.updated_at_unix_ms
	`, profileID, providerID, slotID, credentialID, now, now)
	if err != nil {
		return ProfileCredentialBinding{}, err
	}
	return s.GetProfileCredentialBinding(ctx, profileID, providerID, slotID)
}

func (s *Store) GetProfileCredentialBinding(ctx context.Context, profileID, providerID, slotID string) (ProfileCredentialBinding, error) {
	row := s.executor().QueryRowContext(ctx, `
		SELECT profile_id, provider_id, slot_id, credential_id, created_at_unix_ms, updated_at_unix_ms
		FROM profile_credential_bindings
		WHERE profile_id = ? AND provider_id = ? AND slot_id = ?
	`, profileID, providerID, slotID)
	var binding ProfileCredentialBinding
	err := row.Scan(&binding.ProfileID, &binding.ProviderID, &binding.SlotID, &binding.CredentialID, &binding.CreatedAtUnixMS, &binding.UpdatedAtUnixMS)
	if errors.Is(err, sql.ErrNoRows) {
		return ProfileCredentialBinding{}, ErrNotFound
	}
	return binding, err
}

func (s *Store) ListProfileCredentialBindings(ctx context.Context, profileID, providerID string) ([]ProfileCredentialBinding, error) {
	query := `SELECT profile_id, provider_id, slot_id, credential_id, created_at_unix_ms, updated_at_unix_ms
		FROM profile_credential_bindings WHERE profile_id = ?`
	args := []any{profileID}
	if providerID != "" {
		query += " AND provider_id = ?"
		args = append(args, providerID)
	}
	query += " ORDER BY provider_id ASC, slot_id ASC"
	rows, err := s.executor().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	bindings := []ProfileCredentialBinding{}
	for rows.Next() {
		var binding ProfileCredentialBinding
		if err := rows.Scan(&binding.ProfileID, &binding.ProviderID, &binding.SlotID, &binding.CredentialID, &binding.CreatedAtUnixMS, &binding.UpdatedAtUnixMS); err != nil {
			return nil, err
		}
		bindings = append(bindings, binding)
	}
	return bindings, rows.Err()
}

func (s *Store) ListProfileCredentialBindingsByProvider(ctx context.Context, providerID string) ([]ProfileCredentialBinding, error) {
	rows, err := s.executor().QueryContext(ctx, `
		SELECT profile_id, provider_id, slot_id, credential_id, created_at_unix_ms, updated_at_unix_ms
		FROM profile_credential_bindings WHERE provider_id = ?
		ORDER BY profile_id ASC, slot_id ASC
	`, providerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	bindings := []ProfileCredentialBinding{}
	for rows.Next() {
		var binding ProfileCredentialBinding
		if err := rows.Scan(&binding.ProfileID, &binding.ProviderID, &binding.SlotID, &binding.CredentialID, &binding.CreatedAtUnixMS, &binding.UpdatedAtUnixMS); err != nil {
			return nil, err
		}
		bindings = append(bindings, binding)
	}
	return bindings, rows.Err()
}

func (s *Store) DeleteProfileCredentialBinding(ctx context.Context, profileID, providerID, slotID string) error {
	result, err := s.executor().ExecContext(ctx, `DELETE FROM profile_credential_bindings WHERE profile_id = ? AND provider_id = ? AND slot_id = ?`, profileID, providerID, slotID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) CountProviderCredentialReferences(ctx context.Context, credentialID string) (int, error) {
	var count int
	err := s.executor().QueryRowContext(ctx, `SELECT COUNT(1) FROM profile_credential_bindings WHERE credential_id = ?`, strings.TrimSpace(credentialID)).Scan(&count)
	return count, err
}

func (s *Store) UpsertProfileConfigSetBinding(ctx context.Context, params UpsertProfileConfigSetBindingParams) (ProfileConfigSetBinding, error) {
	profileID := strings.TrimSpace(params.ProfileID)
	providerID := strings.TrimSpace(params.ProviderID)
	slotID := strings.TrimSpace(params.SlotID)
	configSetID := strings.TrimSpace(params.ConfigSetID)
	if profileID == "" || providerID == "" || slotID == "" || configSetID == "" {
		return ProfileConfigSetBinding{}, errors.New("profile Config Set binding fields are required")
	}
	now := time.Now().UnixMilli()
	_, err := s.executor().ExecContext(ctx, `
		INSERT INTO profile_config_set_bindings
			(profile_id, provider_id, slot_id, config_set_id, created_at_unix_ms, updated_at_unix_ms)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(profile_id, provider_id, slot_id) DO UPDATE SET
			config_set_id = excluded.config_set_id,
			updated_at_unix_ms = excluded.updated_at_unix_ms
	`, profileID, providerID, slotID, configSetID, now, now)
	if err != nil {
		return ProfileConfigSetBinding{}, err
	}
	return s.GetProfileConfigSetBinding(ctx, profileID, providerID, slotID)
}

func (s *Store) GetProfileConfigSetBinding(ctx context.Context, profileID, providerID, slotID string) (ProfileConfigSetBinding, error) {
	row := s.executor().QueryRowContext(ctx, `
		SELECT profile_id, provider_id, slot_id, config_set_id, created_at_unix_ms, updated_at_unix_ms
		FROM profile_config_set_bindings
		WHERE profile_id = ? AND provider_id = ? AND slot_id = ?
	`, profileID, providerID, slotID)
	var binding ProfileConfigSetBinding
	err := row.Scan(&binding.ProfileID, &binding.ProviderID, &binding.SlotID, &binding.ConfigSetID, &binding.CreatedAtUnixMS, &binding.UpdatedAtUnixMS)
	if errors.Is(err, sql.ErrNoRows) {
		return ProfileConfigSetBinding{}, ErrNotFound
	}
	return binding, err
}

func (s *Store) ListProfileConfigSetBindings(ctx context.Context, profileID, providerID string) ([]ProfileConfigSetBinding, error) {
	query := `SELECT profile_id, provider_id, slot_id, config_set_id, created_at_unix_ms, updated_at_unix_ms
		FROM profile_config_set_bindings WHERE profile_id = ?`
	args := []any{profileID}
	if providerID != "" {
		query += " AND provider_id = ?"
		args = append(args, providerID)
	}
	query += " ORDER BY provider_id ASC, slot_id ASC"
	rows, err := s.executor().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	bindings := []ProfileConfigSetBinding{}
	for rows.Next() {
		var binding ProfileConfigSetBinding
		if err := rows.Scan(&binding.ProfileID, &binding.ProviderID, &binding.SlotID, &binding.ConfigSetID, &binding.CreatedAtUnixMS, &binding.UpdatedAtUnixMS); err != nil {
			return nil, err
		}
		bindings = append(bindings, binding)
	}
	return bindings, rows.Err()
}

func (s *Store) ListProfileConfigSetBindingsByProvider(ctx context.Context, providerID string) ([]ProfileConfigSetBinding, error) {
	rows, err := s.executor().QueryContext(ctx, `
		SELECT profile_id, provider_id, slot_id, config_set_id, created_at_unix_ms, updated_at_unix_ms
		FROM profile_config_set_bindings WHERE provider_id = ?
		ORDER BY profile_id ASC, slot_id ASC
	`, providerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	bindings := []ProfileConfigSetBinding{}
	for rows.Next() {
		var binding ProfileConfigSetBinding
		if err := rows.Scan(&binding.ProfileID, &binding.ProviderID, &binding.SlotID, &binding.ConfigSetID, &binding.CreatedAtUnixMS, &binding.UpdatedAtUnixMS); err != nil {
			return nil, err
		}
		bindings = append(bindings, binding)
	}
	return bindings, rows.Err()
}

func (s *Store) DeleteProfileConfigSetBinding(ctx context.Context, profileID, providerID, slotID string) error {
	result, err := s.executor().ExecContext(ctx, `DELETE FROM profile_config_set_bindings WHERE profile_id = ? AND provider_id = ? AND slot_id = ?`, profileID, providerID, slotID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) UpdateProviderConfigSet(ctx context.Context, params UpdateProviderConfigSetParams) (ProviderConfigSet, error) {
	assignments := []string{}
	args := []any{}
	if params.Name != nil {
		name := strings.TrimSpace(*params.Name)
		if name == "" {
			return ProviderConfigSet{}, fmt.Errorf("provider config set name is required")
		}
		assignments = append(assignments, "name = ?")
		args = append(args, name)
	}
	if params.Description != nil {
		assignments = append(assignments, "description = ?")
		args = append(args, strings.TrimSpace(*params.Description))
	}
	if params.MetadataJSON != nil {
		if err := validateJSONObject(*params.MetadataJSON, "provider config set metadata"); err != nil {
			return ProviderConfigSet{}, err
		}
		assignments = append(assignments, "metadata_json = ?")
		args = append(args, *params.MetadataJSON)
	}
	if len(assignments) == 0 {
		return s.GetProviderConfigSet(ctx, params.ID)
	}
	assignments = append(assignments, "updated_at_unix_ms = ?")
	args = append(args, time.Now().UnixMilli(), strings.TrimSpace(params.ID))
	result, err := s.executor().ExecContext(
		ctx,
		`UPDATE provider_config_sets SET `+strings.Join(assignments, ", ")+` WHERE id = ?`,
		args...,
	)
	if err != nil {
		return ProviderConfigSet{}, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return ProviderConfigSet{}, err
	}
	if rows == 0 {
		return ProviderConfigSet{}, ErrNotFound
	}
	return s.GetProviderConfigSet(ctx, params.ID)
}

func (s *Store) CountProviderConfigSetReferences(ctx context.Context, id string) (int, error) {
	var count int
	err := s.executor().QueryRowContext(ctx, `
		SELECT COUNT(1) FROM profile_config_set_bindings WHERE config_set_id = ?
	`, strings.TrimSpace(id)).Scan(&count)
	return count, err
}

func (s *Store) DeleteProviderConfigSet(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	result, err := s.executor().ExecContext(
		ctx,
		`DELETE FROM provider_config_sets
		WHERE id = ?
			AND NOT EXISTS (
				SELECT 1 FROM profile_config_set_bindings AS binding
				WHERE binding.provider_id = provider_config_sets.provider_id
					AND binding.config_set_id = provider_config_sets.id
			)`,
		id,
	)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		if _, getErr := s.GetProviderConfigSet(ctx, id); errors.Is(getErr, ErrNotFound) {
			return ErrNotFound
		} else if getErr != nil {
			return getErr
		}
		return ErrInUse
	}
	return nil
}

func validateJSONObject(valueJSON, label string) error {
	decoder := json.NewDecoder(strings.NewReader(valueJSON))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	if _, ok := value.(map[string]any); !ok {
		return fmt.Errorf("%s must be a JSON object", label)
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return fmt.Errorf("%s must contain one JSON object", label)
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
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

func (s *Store) CreateAppliedMaintenanceOperation(ctx context.Context, params CreateAppliedMaintenanceOperationParams) (Operation, error) {
	now := time.Now().UnixMilli()
	_, err := s.executor().ExecContext(
		ctx,
		`INSERT INTO operations
			(id, operation_type, status, profile_id, metadata_json, created_at_unix_ms, updated_at_unix_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		params.ID,
		OperationTypeMaintenance,
		OperationStatusApplied,
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
	if params.SetActive {
		// Maintenance capture adopts the current working copy. Its operation and
		// active state must be committed by the caller's surrounding transaction.
		_, err = s.executor().ExecContext(
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
			return Operation{}, err
		}
	}
	return s.GetOperation(ctx, params.ID)
}

func (s *Store) CreateAppliedImportOperation(ctx context.Context, params CreateAppliedImportOperationParams) (Operation, error) {
	now := time.Now().UnixMilli()
	_, err := s.executor().ExecContext(
		ctx,
		`INSERT INTO operations
			(id, operation_type, status, profile_id, metadata_json, created_at_unix_ms, updated_at_unix_ms)
		VALUES (?, ?, ?, '', ?, ?, ?)`,
		params.ID,
		OperationTypeImport,
		OperationStatusApplied,
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

func (s *Store) UpdateOperationMetadata(ctx context.Context, id, metadataJSON string) error {
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
	for _, update := range params.CredentialUpdates {
		if err := validateProviderCredentialParams(update); err != nil {
			return err
		}
	}
	for _, update := range params.ConfigSetUpdates {
		if err := validateProviderConfigSetParams(update); err != nil {
			return err
		}
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
	txStore := &Store{db: s.db, exec: tx, transactional: true}
	for _, update := range params.CredentialUpdates {
		if _, err := txStore.UpsertProviderCredential(ctx, update); err != nil {
			return err
		}
	}
	for _, update := range params.ConfigSetUpdates {
		if _, err := txStore.UpsertProviderConfigSet(ctx, update); err != nil {
			return err
		}
	}

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

func (s *Store) GetActiveState(ctx context.Context, scopeType, scopeID string) (ActiveState, error) {
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
	if s.transactional {
		return s.insertUsageEvents(ctx, events)
	}

	var result UsageInsertResult
	err := s.WithTransaction(ctx, func(txStore *Store) error {
		var insertErr error
		result, insertErr = txStore.insertUsageEvents(ctx, events)
		return insertErr
	})
	return result, err
}

func (s *Store) insertUsageEvents(ctx context.Context, events []CreateUsageEventParams) (UsageInsertResult, error) {
	now := time.Now().UnixMilli()
	stmt, err := s.executor().PrepareContext(
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
	canonicalStmt, err := s.executor().PrepareContext(
		ctx,
		`UPDATE usage_events
		SET source_key = ?, model = ?, occurred_at_unix_ms = ?, estimated_cost_micros = ?,
			cost_status = ?, metadata_json = ?, updated_at_unix_ms = ?
		WHERE id = ? AND provider_id = ? AND source = ?
			AND ? > 0
			AND (occurred_at_unix_ms = 0 OR occurred_at_unix_ms > ?)`,
	)
	if err != nil {
		return UsageInsertResult{}, err
	}
	defer canonicalStmt.Close()

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
		rows, err := insert.RowsAffected()
		if err != nil {
			return UsageInsertResult{}, err
		}
		if rows > 0 {
			result.Inserted++
			continue
		}

		// Fork copies can rewrite timestamps and model labels. Keep fields from the
		// earliest dated observation without inserting another usage event.
		update, err := canonicalStmt.ExecContext(
			ctx,
			event.SourceKey,
			event.Model,
			event.OccurredAtUnixMS,
			estimatedCost,
			event.CostStatus,
			metadataJSON,
			now,
			event.ID,
			event.ProviderID,
			event.Source,
			event.OccurredAtUnixMS,
			event.OccurredAtUnixMS,
		)
		if err != nil {
			return UsageInsertResult{}, err
		}
		if _, err := update.RowsAffected(); err != nil {
			return UsageInsertResult{}, err
		}
	}
	result.Duplicates = len(events) - result.Inserted
	return result, nil
}

func (s *Store) CommitUsageImport(ctx context.Context, params CommitUsageImportParams) (UsageInsertResult, error) {
	if s.transactional {
		result, err := s.InsertUsageEvents(ctx, params.Events)
		if err != nil {
			return UsageInsertResult{}, err
		}
		if err := s.UpsertUsageImportCursor(ctx, params.Cursor); err != nil {
			return UsageInsertResult{}, err
		}
		return result, nil
	}

	var result UsageInsertResult
	// A cursor may advance only with the events it describes, otherwise a crash
	// could permanently skip usage that never reached the database.
	err := s.WithTransaction(ctx, func(txStore *Store) error {
		var importErr error
		result, importErr = txStore.CommitUsageImport(ctx, params)
		return importErr
	})
	return result, err
}

func (s *Store) GetUsageImportCursor(ctx context.Context, providerID, source, sourceKey string) (UsageImportCursor, error) {
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

func (s *Store) TouchUsageImportCursor(ctx context.Context, providerID, source, sourceKey string) error {
	result, err := s.executor().ExecContext(
		ctx,
		`UPDATE usage_import_cursors
		SET updated_at_unix_ms = ?
		WHERE provider_id = ? AND source = ? AND source_key = ?`,
		time.Now().UnixMilli(),
		providerID,
		source,
		sourceKey,
	)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ListUnknownUsageCostCandidates(ctx context.Context, providerID string, models []string, afterID string, limit int) ([]UsageCostCandidate, error) {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return nil, errors.New("usage provider id is required")
	}
	if len(models) == 0 || len(models) > 32 {
		return nil, errors.New("usage cost candidate models are invalid")
	}
	if limit <= 0 || limit > 1_000 {
		return nil, errors.New("usage cost candidate limit is invalid")
	}

	normalizedModels := make([]string, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" {
			return nil, errors.New("usage cost candidate model is required")
		}
		if _, ok := seen[model]; ok {
			continue
		}
		seen[model] = struct{}{}
		normalizedModels = append(normalizedModels, model)
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(normalizedModels)), ",")
	args := make([]any, 0, len(normalizedModels)+4)
	args = append(args, providerID, UsageCostStatusUnknown)
	for _, model := range normalizedModels {
		args = append(args, model)
	}
	args = append(args, afterID, limit)
	rows, err := s.executor().QueryContext(
		ctx,
		`SELECT id, model, input_tokens, cached_input_tokens, output_tokens, total_tokens
		FROM usage_events
		WHERE provider_id = ? AND cost_status = ? AND model IN (`+placeholders+`) AND id > ?
		ORDER BY id ASC
		LIMIT ?`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	candidates := make([]UsageCostCandidate, 0)
	for rows.Next() {
		var candidate UsageCostCandidate
		if err := rows.Scan(
			&candidate.ID,
			&candidate.Model,
			&candidate.InputTokens,
			&candidate.CachedInputTokens,
			&candidate.OutputTokens,
			&candidate.TotalTokens,
		); err != nil {
			return nil, err
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return candidates, nil
}

func (s *Store) UpdateUnknownUsageEventCosts(ctx context.Context, updates []UpdateUsageEventCostParams) (int, error) {
	if len(updates) == 0 {
		return 0, nil
	}
	if s.transactional {
		return s.updateUnknownUsageEventCosts(ctx, updates)
	}

	var updated int
	err := s.WithTransaction(ctx, func(txStore *Store) error {
		var updateErr error
		updated, updateErr = txStore.updateUnknownUsageEventCosts(ctx, updates)
		return updateErr
	})
	return updated, err
}

func (s *Store) updateUnknownUsageEventCosts(ctx context.Context, updates []UpdateUsageEventCostParams) (int, error) {
	stmt, err := s.executor().PrepareContext(
		ctx,
		`UPDATE usage_events
		SET estimated_cost_micros = ?, cost_status = ?, updated_at_unix_ms = ?
		WHERE id = ? AND cost_status = ?`,
	)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	now := time.Now().UnixMilli()
	updated := 0
	for _, item := range updates {
		if strings.TrimSpace(item.ID) == "" || item.EstimatedCostMicros < 0 ||
			(item.CostStatus != UsageCostStatusEstimated && item.CostStatus != UsageCostStatusPartial) {
			return 0, errors.New("usage event cost update is invalid")
		}
		result, err := stmt.ExecContext(
			ctx,
			item.EstimatedCostMicros,
			item.CostStatus,
			now,
			item.ID,
			UsageCostStatusUnknown,
		)
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
			COALESCE(SUM(CASE WHEN cost_status = ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN cost_status = ? THEN 1 ELSE 0 END), 0)
		FROM usage_events
		WHERE provider_id = ?`,
		UsageCostStatusUnknown,
		UsageCostStatusPartial,
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
		&summary.PartialCostEvents,
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

func (s *Store) EarliestDatedUsageUnixMS(ctx context.Context, providerID string) (int64, error) {
	var earliest sql.NullInt64
	err := s.executor().QueryRowContext(
		ctx,
		`SELECT MIN(occurred_at_unix_ms)
		FROM usage_events
		WHERE provider_id = ? AND occurred_at_unix_ms > 0`,
		providerID,
	).Scan(&earliest)
	if err != nil {
		return 0, err
	}
	if !earliest.Valid {
		return 0, nil
	}
	return earliest.Int64, nil
}

func (s *Store) UsageReport(ctx context.Context, query UsageReportQuery) (UsageReportSnapshot, error) {
	if strings.TrimSpace(query.ProviderID) == "" {
		return UsageReportSnapshot{}, errors.New("usage provider id is required")
	}
	if query.StartUnixMS != nil && query.EndUnixMS <= *query.StartUnixMS {
		return UsageReportSnapshot{}, errors.New("usage report range is invalid")
	}
	if len(query.Buckets) > 512 {
		return UsageReportSnapshot{}, errors.New("usage report has too many buckets")
	}
	for _, bucket := range query.Buckets {
		if bucket.EndUnixMS <= bucket.StartUnixMS {
			return UsageReportSnapshot{}, errors.New("usage report bucket is invalid")
		}
	}

	if !s.transactional {
		var snapshot UsageReportSnapshot
		// Every aggregate in one report must observe the same event/cursor state,
		// even when a concurrent importer commits between individual queries.
		err := s.WithTransaction(ctx, func(txStore *Store) error {
			var reportErr error
			snapshot, reportErr = txStore.UsageReport(ctx, query)
			return reportErr
		})
		return snapshot, err
	}

	summary, err := s.queryUsageAggregate(ctx, query.ProviderID, query.StartUnixMS, query.EndUnixMS)
	if err != nil {
		return UsageReportSnapshot{}, err
	}
	if query.StartUnixMS != nil {
		if err := s.executor().QueryRowContext(
			ctx,
			"SELECT COUNT(1) FROM usage_events WHERE provider_id = ? AND occurred_at_unix_ms = 0",
			query.ProviderID,
		).Scan(&summary.UndatedEventCount); err != nil {
			return UsageReportSnapshot{}, err
		}
	}
	models, err := s.queryUsageModels(ctx, query.ProviderID, query.StartUnixMS, query.EndUnixMS)
	if err != nil {
		return UsageReportSnapshot{}, err
	}
	trend := make([]UsageTrendAggregate, 0, len(query.Buckets))
	for index, bucket := range query.Buckets {
		start := bucket.StartUnixMS
		aggregate, err := s.queryUsageAggregate(ctx, query.ProviderID, &start, bucket.EndUnixMS)
		if err != nil {
			return UsageReportSnapshot{}, err
		}
		trend = append(trend, UsageTrendAggregate{BucketIndex: index, UsageAggregate: aggregate})
	}
	sources, err := s.usageSummarySources(ctx, query.ProviderID)
	if err != nil {
		return UsageReportSnapshot{}, err
	}
	importSummary, err := s.queryUsageImportSummary(ctx, query.ProviderID)
	if err != nil {
		return UsageReportSnapshot{}, err
	}
	return UsageReportSnapshot{
		Sources:       sources,
		Summary:       summary,
		Trend:         trend,
		Models:        models,
		ImportSummary: importSummary,
	}, nil
}

func (s *Store) queryUsageAggregate(ctx context.Context, providerID string, startUnixMS *int64, endUnixMS int64) (UsageAggregate, error) {
	where, args := usageReportWhere(providerID, startUnixMS, endUnixMS)
	row := s.executor().QueryRowContext(ctx, usageAggregateSelect+" FROM usage_events WHERE "+where, args...)
	return scanUsageAggregate(row)
}

func (s *Store) queryUsageModels(ctx context.Context, providerID string, startUnixMS *int64, endUnixMS int64) ([]UsageModelAggregate, error) {
	where, args := usageReportWhere(providerID, startUnixMS, endUnixMS)
	rows, err := s.executor().QueryContext(
		ctx,
		`SELECT `+usageReportModelExpression+`,`+usageAggregateColumns+`
		FROM usage_events
		WHERE `+where+`
		GROUP BY `+usageReportModelExpression+`
		ORDER BY COALESCE(SUM(total_tokens), 0) DESC, `+usageReportModelExpression+` ASC`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	models := make([]UsageModelAggregate, 0)
	for rows.Next() {
		var item UsageModelAggregate
		if err := rows.Scan(
			&item.Model,
			&item.EventCount,
			&item.SessionCount,
			&item.FreshInputTokens,
			&item.InputTokens,
			&item.CachedInputTokens,
			&item.OutputTokens,
			&item.TotalTokens,
			&item.EstimatedCostMicros,
			&item.EstimatedTokenCount,
			&item.UnknownCostEvents,
			&item.EstimatedCostEventCount,
			&item.PartialCostEventCount,
			&item.UndatedEventCount,
		); err != nil {
			return nil, err
		}
		models = append(models, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return models, nil
}

func (s *Store) queryUsageImportSummary(ctx context.Context, providerID string) (UsageImportSummary, error) {
	var summary UsageImportSummary
	err := s.executor().QueryRowContext(
		ctx,
		`SELECT
			COUNT(1),
			COALESCE(MAX(updated_at_unix_ms), 0),
			COALESCE(SUM(invalid_lines), 0),
			COALESCE(SUM(unsupported_lines), 0)
		FROM usage_import_cursors
		WHERE provider_id = ?`,
		providerID,
	).Scan(
		&summary.TrackedFiles,
		&summary.LastSyncedAtUnixMS,
		&summary.InvalidLines,
		&summary.UnsupportedLines,
	)
	return summary, err
}

const usageAggregateSelect = "SELECT " + usageAggregateColumns

// Model strings cross CLI/Desktop output boundaries. Group malformed or
// unreasonably large persisted values under a safe label rather than echoing
// arbitrary session content.
const usageReportModelExpression = `CASE
	WHEN length(model) BETWEEN 1 AND 200
		AND model NOT GLOB '*[^A-Za-z0-9._:/@-]*'
	THEN model
	ELSE 'unknown'
END`

const usageAggregateColumns = `
	COUNT(1),
	COUNT(DISTINCT NULLIF(session_id, '')),
	COALESCE(SUM(CASE
		WHEN cached_input_tokens <= input_tokens THEN input_tokens - cached_input_tokens
		ELSE input_tokens
	END), 0),
	COALESCE(SUM(input_tokens), 0),
	COALESCE(SUM(cached_input_tokens), 0),
	COALESCE(SUM(output_tokens), 0),
	COALESCE(SUM(total_tokens), 0),
	COALESCE(SUM(CASE WHEN estimated_cost_micros IS NULL THEN 0 ELSE estimated_cost_micros END), 0),
	COALESCE(SUM(CASE WHEN cost_status IN ('estimated', 'partial') THEN total_tokens ELSE 0 END), 0),
	COALESCE(SUM(CASE WHEN cost_status = 'unknown' THEN 1 ELSE 0 END), 0),
	COALESCE(SUM(CASE WHEN cost_status = 'estimated' THEN 1 ELSE 0 END), 0),
	COALESCE(SUM(CASE WHEN cost_status = 'partial' THEN 1 ELSE 0 END), 0),
	COALESCE(SUM(CASE WHEN occurred_at_unix_ms = 0 THEN 1 ELSE 0 END), 0)`

func usageReportWhere(providerID string, startUnixMS *int64, endUnixMS int64) (string, []any) {
	where := "provider_id = ?"
	args := []any{providerID}
	if startUnixMS != nil {
		where += " AND occurred_at_unix_ms >= ? AND occurred_at_unix_ms < ?"
		args = append(args, *startUnixMS, endUnixMS)
	}
	return where, args
}

func scanUsageAggregate(row rowScanner) (UsageAggregate, error) {
	var aggregate UsageAggregate
	err := row.Scan(
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
	)
	return aggregate, err
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

func (s *Store) indexExistsOnTable(ctx context.Context, table, index string) (bool, bool, error) {
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

	var credentialBindingCount int
	if err := s.executor().QueryRowContext(ctx, "SELECT COUNT(1) FROM profile_credential_bindings WHERE profile_id = ?", profileID).Scan(&credentialBindingCount); err != nil {
		return 0, err
	}
	var configSetBindingCount int
	if err := s.executor().QueryRowContext(ctx, "SELECT COUNT(1) FROM profile_config_set_bindings WHERE profile_id = ?", profileID).Scan(&configSetBindingCount); err != nil {
		return 0, err
	}

	return activeStateCount + operationCount + targetCount + credentialBindingCount + configSetBindingCount, nil
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

	var settingCount int
	if err := s.executor().QueryRowContext(ctx, "SELECT COUNT(1) FROM provider_profile_settings WHERE provider_id = ?", providerID).Scan(&settingCount); err != nil {
		return 0, err
	}

	var credentialBindingCount int
	if err := s.executor().QueryRowContext(ctx, "SELECT COUNT(1) FROM profile_credential_bindings WHERE provider_id = ?", providerID).Scan(&credentialBindingCount); err != nil {
		return 0, err
	}
	var configSetBindingCount int
	if err := s.executor().QueryRowContext(ctx, "SELECT COUNT(1) FROM profile_config_set_bindings WHERE provider_id = ?", providerID).Scan(&configSetBindingCount); err != nil {
		return 0, err
	}
	var credentialCount int
	if err := s.executor().QueryRowContext(ctx, "SELECT COUNT(1) FROM provider_credentials WHERE provider_id = ?", providerID).Scan(&credentialCount); err != nil {
		return 0, err
	}
	var configSetCount int
	if err := s.executor().QueryRowContext(ctx, "SELECT COUNT(1) FROM provider_config_sets WHERE provider_id = ?", providerID).Scan(&configSetCount); err != nil {
		return 0, err
	}

	return targetCount + activeStateCount + operationCount + settingCount + credentialCount + configSetCount + credentialBindingCount + configSetBindingCount, nil
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

func scanProviderProfileSetting(row rowScanner) (ProviderProfileSetting, error) {
	var setting ProviderProfileSetting
	var keepalive int
	if err := row.Scan(
		&setting.ProfileID,
		&setting.ProviderID,
		&setting.QuotaRefreshIntervalSeconds,
		&keepalive,
		&setting.UpdatedAtUnixMS,
	); err != nil {
		return ProviderProfileSetting{}, err
	}
	setting.AuthKeepaliveEnabled = keepalive != 0
	return setting, nil
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

func scanProviderCredential(row rowScanner) (ProviderCredential, error) {
	var credential ProviderCredential
	if err := row.Scan(
		&credential.ID,
		&credential.ProviderID,
		&credential.CredentialKind,
		&credential.PayloadJSON,
		&credential.PayloadSHA256,
		&credential.MetadataJSON,
		&credential.CreatedAtUnixMS,
		&credential.UpdatedAtUnixMS,
	); err != nil {
		return ProviderCredential{}, err
	}
	return credential, nil
}

func scanProviderConfigSet(row rowScanner) (ProviderConfigSet, error) {
	var configSet ProviderConfigSet
	if err := row.Scan(
		&configSet.ID,
		&configSet.ProviderID,
		&configSet.ConfigKind,
		&configSet.Name,
		&configSet.Description,
		&configSet.PayloadText,
		&configSet.PayloadSHA256,
		&configSet.MetadataJSON,
		&configSet.CreatedAtUnixMS,
		&configSet.UpdatedAtUnixMS,
	); err != nil {
		return ProviderConfigSet{}, err
	}
	return configSet, nil
}

func scanSetting(row rowScanner) (Setting, error) {
	var setting Setting
	if err := row.Scan(
		&setting.Key,
		&setting.ValueJSON,
		&setting.UpdatedAtUnixMS,
	); err != nil {
		return Setting{}, err
	}
	return setting, nil
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
	q.Add("_pragma", "foreign_keys(1)")
	u.RawQuery = q.Encode()

	return u.String()
}
