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
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"github.com/uptrace/bun/migrate"
	sqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/strahe/profiledeck/internal/store/migrations"
	"github.com/strahe/profiledeck/internal/targetformat"
)

const (
	sqliteDriverName             = "sqlite"
	sqliteBusyTimeout            = 5 * time.Second
	sqliteMigrationMaxAttempts   = 3
	sqliteMigrationRetryBaseWait = 25 * time.Millisecond

	OperationTypeSwitch      = "switch"
	OperationTypeRecovery    = "recovery"
	OperationTypeImport      = "import"
	OperationTypeMaintenance = "maintenance"

	OperationStatusPending = "pending"
	OperationStatusFailed  = "failed"
	OperationStatusApplied = "applied"

	// Stable baseline versions are historical integrity-contract inputs.
	// Future writers may advance only through an append-only migration.
	stableBaselineOperationMetadataSchemaVersion = 1
	stableBaselineProviderSettingsSchemaVersion  = 1

	OperationMetadataSchemaVersion = stableBaselineOperationMetadataSchemaVersion
	ProviderSettingsSchemaVersion  = stableBaselineProviderSettingsSchemaVersion

	recoveryCleanupStateKey = "recovery.cleanup_required"

	maxProviderCredentialPayloadBytes = 16 * 1024 * 1024
	maxProviderConfigSetPayloadBytes  = 16 * 1024 * 1024
)

var (
	ErrUnsupportedSchema       = errors.New("application database schema contains unknown migrations")
	ErrInvalidMigrationHistory = errors.New("application database migration history is invalid")
	ErrInvalidSystemState      = errors.New("application database system state is invalid")
	ErrQuickCheckFailed        = errors.New("sqlite quick check failed")
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
	accessLease   *accessLease
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
	MetadataJSON    string
	CreatedAtUnixMS int64
	UpdatedAtUnixMS int64
}

type CreateProviderParams struct {
	ID           string
	Name         string
	AdapterID    string
	MetadataJSON string
}

type UpdateProviderParams struct {
	ID           string
	Name         *string
	AdapterID    *string
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

// ProfileTarget stores one generic file target. Enabled controls only its
// participation in switches; it is not Provider or Desktop Agent state.
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
	ID                    string
	ProviderID            string
	OperationType         string
	Status                string
	SourceOperationID     string
	MetadataSchemaVersion int
	MetadataJSON          string
	ErrorCode             string
	ErrorMessage          string
	ResolutionKind        string
	ResolvedAtUnixMS      int64
	CreatedAtUnixMS       int64
	UpdatedAtUnixMS       int64
}

type ActiveState struct {
	ProviderID      string
	ProfileID       string
	Revision        int64
	UpdatedAtUnixMS int64
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

type ProviderSetting struct {
	ProviderID      string
	SchemaVersion   int
	SettingsJSON    string
	UpdatedAtUnixMS int64
}

type ProviderProfileSetting struct {
	ProfileID       string
	ProviderID      string
	SchemaVersion   int
	SettingsJSON    string
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
	ID                    string
	ProviderID            string
	ProfileIDs            []string
	MetadataSchemaVersion int
	MetadataJSON          string
}

type CreateRecoveryOperationParams struct {
	ID                    string
	ProviderID            string
	SourceOperationID     string
	ProfileIDs            []string
	MetadataSchemaVersion int
	MetadataJSON          string
}

type CreateAppliedMaintenanceOperationParams struct {
	ID                    string
	ProviderID            string
	RelatedProfileIDs     []string
	ActiveProfileID       string
	MetadataSchemaVersion int
	MetadataJSON          string
}

type CreateAppliedImportOperationParams struct {
	ID                    string
	ProviderID            string
	ProfileIDs            []string
	MetadataSchemaVersion int
	MetadataJSON          string
}

type MarkOperationFailedParams struct {
	ID           string
	ErrorCode    string
	ErrorMessage string
	MetadataJSON *string
}

type CompleteSwitchOperationParams struct {
	ID                    string
	ProfileID             string
	ProviderID            string
	MetadataSchemaVersion int
	MetadataJSON          string
	CredentialUpdates     []UpsertProviderCredentialParams
	ConfigSetUpdates      []UpsertProviderConfigSetParams
}

type CompleteRecoveryOperationParams struct {
	ID                    string
	SourceOperationID     string
	ProviderID            string
	MetadataSchemaVersion int
	MetadataJSON          string
	ResolutionKind        string
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
	ProviderID   string
	Name         *string
	Description  *string
	MetadataJSON *string
}

type UpsertSettingParams struct {
	Key       string
	ValueJSON string
}

type UpsertProviderSettingParams struct {
	ProviderID    string
	SchemaVersion int
	SettingsJSON  string
}

type UpsertProviderProfileSettingParams struct {
	ProfileID     string
	ProviderID    string
	SchemaVersion int
	SettingsJSON  string
}

type MigrationResult struct {
	Applied int
}

type Status struct {
	SchemaHealthy     bool
	PendingOperations int
	FailedOperations  int
}

// Recovery cleanup is a global safety obligation. Keep its persistence behind
// typed methods so user settings cannot clear or forge this gate.
func (s *Store) RequireRecoveryCleanup(ctx context.Context) error {
	if _, err := s.RecoveryCleanupRequired(ctx); err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	_, err := s.executor().ExecContext(ctx, `
		INSERT INTO system_state (key, value_json, created_at_unix_ms, updated_at_unix_ms)
		VALUES (?, 'true', ?, ?)
		ON CONFLICT(key) DO UPDATE SET
			value_json = 'true',
			updated_at_unix_ms = excluded.updated_at_unix_ms
	`, recoveryCleanupStateKey, now, now)
	return err
}

func (s *Store) RecoveryCleanupRequired(ctx context.Context) (bool, error) {
	rows, err := s.executor().QueryContext(ctx, `SELECT key, value_json FROM system_state ORDER BY key`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	required := false
	for rows.Next() {
		var key, valueJSON string
		if err := rows.Scan(&key, &valueJSON); err != nil {
			return false, err
		}
		var value bool
		if key != recoveryCleanupStateKey || json.Unmarshal([]byte(valueJSON), &value) != nil || !value {
			return false, ErrInvalidSystemState
		}
		required = true
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return required, nil
}

func (s *Store) ClearRecoveryCleanup(ctx context.Context) error {
	if _, err := s.RecoveryCleanupRequired(ctx); err != nil {
		return err
	}
	_, err := s.executor().ExecContext(ctx, `DELETE FROM system_state WHERE key = ?`, recoveryCleanupStateKey)
	return err
}

type columnInfo struct {
	columnType   string
	notNull      bool
	primaryKey   bool
	defaultValue sql.NullString
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
	err := s.db.Close()
	s.accessLease.close()
	return err
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
	return migrateWithRetry(ctx, s.migrateOnce)
}

func migrateWithRetry(
	ctx context.Context,
	migrate func(context.Context) (MigrationResult, error),
) (MigrationResult, error) {
	var result MigrationResult
	var err error
	// Concurrent CLI and Desktop startup can outlive one busy timeout on Windows;
	// retry only SQLite lock errors so migration failures are never masked.
	for attempt := range sqliteMigrationMaxAttempts {
		result, err = migrate(ctx)
		if err == nil || !isSQLiteBusyError(err) || attempt == sqliteMigrationMaxAttempts-1 {
			return result, err
		}
		if err := waitForMigrationRetry(ctx, sqliteMigrationRetryBaseWait<<attempt); err != nil {
			return MigrationResult{}, err
		}
	}
	return result, err
}

func (s *Store) migrateOnce(ctx context.Context) (MigrationResult, error) {
	// Older Bun clients ignore applied migrations that are absent from their
	// registry, so reject them before Bun initializes or changes the schema.
	state, err := s.MigrationState(ctx)
	if err != nil {
		return MigrationResult{}, err
	}
	if state.Applied == 0 {
		if err := s.validateUnmarkedStableBaseline(ctx); err != nil {
			return MigrationResult{}, err
		}
	}
	if !state.HasMigrationTable {
		// Establish Bun's infrastructure as one baseline. This prevents concurrent
		// fresh initialization from exposing the gaps between Bun's Init statements.
		if err := s.initializeMigrationInfrastructure(ctx); err != nil {
			return MigrationResult{}, err
		}
	}
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

func (s *Store) initializeMigrationInfrastructure(ctx context.Context) error {
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		for index, statement := range migrationInfrastructureStatements {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("initialize migration infrastructure statement %d: %w", index+1, err)
			}
		}
		return nil
	})
}

func waitForMigrationRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (s *Store) Status(ctx context.Context) (Status, error) {
	state, err := s.MigrationState(ctx)
	if err != nil {
		return Status{}, err
	}
	if !state.Current {
		return Status{SchemaHealthy: false}, nil
	}
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

// QuickCheck verifies SQLite page and index consistency without exposing
// database content in the returned error.
func (s *Store) QuickCheck(ctx context.Context) error {
	rows, err := s.executor().QueryContext(ctx, "PRAGMA quick_check")
	if err != nil {
		return normalizeQuickCheckError(err)
	}
	defer rows.Close()
	results := 0
	healthy := true
	for rows.Next() {
		var result string
		if err := rows.Scan(&result); err != nil {
			return normalizeQuickCheckError(err)
		}
		results++
		if result != "ok" {
			healthy = false
		}
	}
	if err := rows.Err(); err != nil {
		return normalizeQuickCheckError(err)
	}
	if results != 1 || !healthy {
		return ErrQuickCheckFailed
	}
	return nil
}

// CheckMigrationCompatibility rejects unknown or invalid migration history
// before application data is read or migrated.
// Marked baselines are checked separately by InspectIntegrity, which includes
// QuickCheck. An unmarked database is validated here only before marker adoption.
func (s *Store) CheckMigrationCompatibility(ctx context.Context) error {
	state, err := s.MigrationState(ctx)
	if err != nil {
		return err
	}
	if state.Applied == 0 {
		return s.validateUnmarkedStableBaseline(ctx)
	}
	return nil
}

// Checkpoint folds committed WAL pages into the database before a database
// file set is moved or published.
func (s *Store) Checkpoint(ctx context.Context) error {
	var busy, logPages, checkpointedPages int
	if err := s.executor().QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(
		&busy,
		&logPages,
		&checkpointedPages,
	); err != nil {
		return err
	}
	if busy != 0 {
		return errors.New("sqlite checkpoint is busy")
	}
	return nil
}

func (s *Store) ListProviders(ctx context.Context) ([]Provider, error) {
	rows, err := s.executor().QueryContext(ctx, `
		SELECT id, name, adapter_id, metadata_json, created_at_unix_ms, updated_at_unix_ms
		FROM providers
		ORDER BY id ASC
	`)
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
		`SELECT id, name, adapter_id, metadata_json, created_at_unix_ms, updated_at_unix_ms
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
	_, err := s.executor().ExecContext(
		ctx,
		`INSERT INTO providers
			(id, name, adapter_id, metadata_json, created_at_unix_ms, updated_at_unix_ms)
		VALUES (?, ?, ?, ?, ?, ?)`,
		params.ID,
		params.Name,
		params.AdapterID,
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

// CreateProviderIfMissing is reserved for typed provisioners. A conflicting
// Provider is returned unchanged so the caller can validate its adapter and
// locator instead of silently rebinding it.
func (s *Store) CreateProviderIfMissing(ctx context.Context, params CreateProviderParams) (Provider, bool, error) {
	now := time.Now().UnixMilli()
	result, err := s.executor().ExecContext(
		ctx,
		`INSERT INTO providers
			(id, name, adapter_id, metadata_json, created_at_unix_ms, updated_at_unix_ms)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING`,
		params.ID,
		params.Name,
		params.AdapterID,
		params.MetadataJSON,
		now,
		now,
	)
	if err != nil {
		return Provider{}, false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return Provider{}, false, err
	}
	provider, err := s.GetProvider(ctx, params.ID)
	return provider, rows == 1, err
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
	result, err := s.executor().ExecContext(ctx, `DELETE FROM providers WHERE id = ?`, strings.TrimSpace(id))
	if err != nil {
		if isSQLiteConstraintError(err) {
			return ErrInUse
		}
		return err
	}
	if rows, err := result.RowsAffected(); err == nil && rows == 0 {
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
	result, err := s.executor().ExecContext(ctx, `DELETE FROM profiles WHERE id = ?`, strings.TrimSpace(id))
	if err != nil {
		if isSQLiteConstraintError(err) {
			return ErrInUse
		}
		return err
	}
	if rows, err := result.RowsAffected(); err == nil && rows == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) CreateProfileTarget(ctx context.Context, params CreateProfileTargetParams) (ProfileTarget, error) {
	if !targetformat.BuiltinRegistry().AllowsCanonical(params.Format, params.Strategy) {
		return ProfileTarget{}, errors.New("profile target format and strategy are not registered")
	}
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
	if params.Format != nil || params.Strategy != nil {
		existing, err := s.GetProfileTarget(ctx, params.ProfileID, params.ProviderID, params.TargetID)
		if err != nil {
			return ProfileTarget{}, err
		}
		format := existing.Format
		if params.Format != nil {
			format = *params.Format
		}
		strategy := existing.Strategy
		if params.Strategy != nil {
			strategy = *params.Strategy
		}
		if !targetformat.BuiltinRegistry().AllowsCanonical(format, strategy) {
			return ProfileTarget{}, errors.New("profile target format and strategy are not registered")
		}
	}
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

func (s *Store) DeleteProfileTargetsByProfile(ctx context.Context, profileID string) error {
	_, err := s.executor().ExecContext(ctx, "DELETE FROM profile_targets WHERE profile_id = ?", strings.TrimSpace(profileID))
	return err
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

func (s *Store) ListSettingsByPrefix(ctx context.Context, prefix string) ([]Setting, error) {
	prefix = strings.TrimSpace(prefix)
	rows, err := s.executor().QueryContext(ctx, `
		SELECT key, value_json, updated_at_unix_ms
		FROM settings
		WHERE key LIKE ? ESCAPE '!'
		ORDER BY key ASC
	`, escapeLikePrefix(prefix)+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	settings := []Setting{}
	for rows.Next() {
		setting, err := scanSetting(rows)
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

func escapeLikePrefix(value string) string {
	replacer := strings.NewReplacer(`!`, `!!`, `%`, `!%`, `_`, `!_`)
	return replacer.Replace(value)
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

func (s *Store) GetProviderSetting(ctx context.Context, providerID string) (ProviderSetting, error) {
	row := s.executor().QueryRowContext(ctx, `
		SELECT provider_id, schema_version, settings_json, updated_at_unix_ms
		FROM provider_settings
		WHERE provider_id = ?
	`, strings.TrimSpace(providerID))
	setting, err := scanProviderSetting(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ProviderSetting{}, ErrNotFound
	}
	return setting, err
}

func (s *Store) UpsertProviderSetting(ctx context.Context, params UpsertProviderSettingParams) (ProviderSetting, error) {
	providerID := strings.TrimSpace(params.ProviderID)
	if providerID == "" {
		return ProviderSetting{}, errors.New("provider setting provider_id is required")
	}
	if params.SchemaVersion != ProviderSettingsSchemaVersion {
		return ProviderSetting{}, errors.New("provider setting schema version is unsupported")
	}
	if err := validateJSONObject(params.SettingsJSON, "provider settings"); err != nil {
		return ProviderSetting{}, err
	}
	now := time.Now().UnixMilli()
	_, err := s.executor().ExecContext(ctx, `
		INSERT INTO provider_settings
			(provider_id, schema_version, settings_json, updated_at_unix_ms)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(provider_id) DO UPDATE SET
			schema_version = excluded.schema_version,
			settings_json = excluded.settings_json,
			updated_at_unix_ms = excluded.updated_at_unix_ms
	`, providerID, params.SchemaVersion, params.SettingsJSON, now)
	if err != nil {
		return ProviderSetting{}, err
	}
	return s.GetProviderSetting(ctx, providerID)
}

func (s *Store) GetProviderProfileSetting(ctx context.Context, profileID, providerID string) (ProviderProfileSetting, error) {
	row := s.executor().QueryRowContext(ctx, `
		SELECT profile_id, provider_id, schema_version, settings_json, updated_at_unix_ms
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
		SELECT profile_id, provider_id, schema_version, settings_json, updated_at_unix_ms
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
	if params.SchemaVersion != ProviderSettingsSchemaVersion {
		return ProviderProfileSetting{}, errors.New("provider profile setting schema version is unsupported")
	}
	if err := validateJSONObject(params.SettingsJSON, "provider profile settings"); err != nil {
		return ProviderProfileSetting{}, err
	}
	now := time.Now().UnixMilli()
	_, err := s.executor().ExecContext(ctx, `
		INSERT INTO provider_profile_settings
			(profile_id, provider_id, schema_version, settings_json, updated_at_unix_ms)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(profile_id, provider_id) DO UPDATE SET
			schema_version = excluded.schema_version,
			settings_json = excluded.settings_json,
			updated_at_unix_ms = excluded.updated_at_unix_ms
	`, profileID, providerID, params.SchemaVersion, params.SettingsJSON, now)
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
		ON CONFLICT(provider_id, id) DO UPDATE SET
			name = excluded.name,
			description = excluded.description,
			payload_text = excluded.payload_text,
			payload_sha256 = excluded.payload_sha256,
			metadata_json = excluded.metadata_json,
			updated_at_unix_ms = excluded.updated_at_unix_ms
		WHERE provider_config_sets.config_kind = excluded.config_kind`,
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
	configSet, err := s.GetProviderConfigSet(ctx, params.ProviderID, id)
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

func (s *Store) GetProviderConfigSet(ctx context.Context, providerID, id string) (ProviderConfigSet, error) {
	row := s.executor().QueryRowContext(
		ctx,
		`SELECT id, provider_id, config_kind, name, description, payload_text, payload_sha256, metadata_json, created_at_unix_ms, updated_at_unix_ms
		FROM provider_config_sets
		WHERE provider_id = ? AND id = ?`,
		strings.TrimSpace(providerID),
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

func (s *Store) DeleteProviderCredential(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	// Hidden credentials may be removed only after every typed binding has
	// released them; Profile deletion must never collect a shared login.
	result, err := s.executor().ExecContext(
		ctx,
		`DELETE FROM provider_credentials
		WHERE id = ?
			AND NOT EXISTS (
				SELECT 1 FROM profile_credential_bindings AS binding
				WHERE binding.provider_id = provider_credentials.provider_id
					AND binding.credential_id = provider_credentials.id
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
		if _, getErr := s.GetProviderCredential(ctx, id); errors.Is(getErr, ErrNotFound) {
			return ErrNotFound
		} else if getErr != nil {
			return getErr
		}
		return ErrInUse
	}
	return nil
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

// ListProviderResourceProfileIDs returns every Profile whose typed binding
// observes one of the supplied Provider-owned resources. Operation writers use
// it so shared-resource mutations cannot omit affected Profiles.
func (s *Store) ListProviderResourceProfileIDs(
	ctx context.Context,
	providerID string,
	credentialIDs []string,
	configSetIDs []string,
) ([]string, error) {
	credentialSet := make(map[string]struct{}, len(credentialIDs))
	for _, id := range credentialIDs {
		if id = strings.TrimSpace(id); id != "" {
			credentialSet[id] = struct{}{}
		}
	}
	configSetSet := make(map[string]struct{}, len(configSetIDs))
	for _, id := range configSetIDs {
		if id = strings.TrimSpace(id); id != "" {
			configSetSet[id] = struct{}{}
		}
	}
	profileIDs := []string{}
	if len(credentialSet) > 0 {
		bindings, err := s.ListProfileCredentialBindingsByProvider(ctx, providerID)
		if err != nil {
			return nil, err
		}
		for _, binding := range bindings {
			if _, ok := credentialSet[binding.CredentialID]; ok {
				profileIDs = append(profileIDs, binding.ProfileID)
			}
		}
	}
	if len(configSetSet) > 0 {
		bindings, err := s.ListProfileConfigSetBindingsByProvider(ctx, providerID)
		if err != nil {
			return nil, err
		}
		for _, binding := range bindings {
			if _, ok := configSetSet[binding.ConfigSetID]; ok {
				profileIDs = append(profileIDs, binding.ProfileID)
			}
		}
	}
	return uniqueNonEmptyStrings(profileIDs), nil
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
		return s.GetProviderConfigSet(ctx, params.ProviderID, params.ID)
	}
	assignments = append(assignments, "updated_at_unix_ms = ?")
	args = append(args, time.Now().UnixMilli(), strings.TrimSpace(params.ProviderID), strings.TrimSpace(params.ID))
	result, err := s.executor().ExecContext(
		ctx,
		`UPDATE provider_config_sets SET `+strings.Join(assignments, ", ")+` WHERE provider_id = ? AND id = ?`,
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
	return s.GetProviderConfigSet(ctx, params.ProviderID, params.ID)
}

func (s *Store) CountProviderConfigSetReferences(ctx context.Context, providerID, id string) (int, error) {
	var count int
	err := s.executor().QueryRowContext(ctx, `
		SELECT COUNT(1) FROM profile_config_set_bindings
		WHERE provider_id = ? AND config_set_id = ?
	`, strings.TrimSpace(providerID), strings.TrimSpace(id)).Scan(&count)
	return count, err
}

func (s *Store) DeleteProviderConfigSet(ctx context.Context, providerID, id string) error {
	providerID = strings.TrimSpace(providerID)
	id = strings.TrimSpace(id)
	// Config Sets may outlive one Profile when another binding still shares
	// them, so the final delete repeats the reference check atomically.
	result, err := s.executor().ExecContext(
		ctx,
		`DELETE FROM provider_config_sets
		WHERE provider_id = ? AND id = ?
			AND NOT EXISTS (
				SELECT 1 FROM profile_config_set_bindings AS binding
				WHERE binding.provider_id = provider_config_sets.provider_id
					AND binding.config_set_id = provider_config_sets.id
			)`,
		providerID,
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
		if _, getErr := s.GetProviderConfigSet(ctx, providerID, id); errors.Is(getErr, ErrNotFound) {
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
	return s.createOperation(ctx, createOperationParams{
		ID: params.ID, ProviderID: params.ProviderID, OperationType: OperationTypeSwitch,
		Status: OperationStatusPending, ProfileIDs: params.ProfileIDs,
		MetadataSchemaVersion: params.MetadataSchemaVersion, MetadataJSON: params.MetadataJSON,
	})
}

func (s *Store) CreatePendingRecoveryOperation(ctx context.Context, params CreateRecoveryOperationParams) (Operation, error) {
	return s.createOperation(ctx, createOperationParams{
		ID: params.ID, ProviderID: params.ProviderID, OperationType: OperationTypeRecovery,
		Status: OperationStatusPending, SourceOperationID: params.SourceOperationID,
		ProfileIDs: params.ProfileIDs, MetadataSchemaVersion: params.MetadataSchemaVersion,
		MetadataJSON: params.MetadataJSON,
	})
}

func (s *Store) CreateAppliedMaintenanceOperation(ctx context.Context, params CreateAppliedMaintenanceOperationParams) (Operation, error) {
	relatedProfileIDs := uniqueNonEmptyStrings(params.RelatedProfileIDs)
	activeProfileID := strings.TrimSpace(params.ActiveProfileID)
	if activeProfileID != "" && !containsString(relatedProfileIDs, activeProfileID) {
		return Operation{}, errors.New("active maintenance Profile must be related to the operation")
	}
	var operation Operation
	err := s.withTransactionIfNeeded(ctx, func(tx *Store) error {
		if activeProfileID != "" {
			// Active state and disposable history have different lifecycles.
			// Capture both endpoints before changing active state so deleting
			// either Profile removes the complete maintenance operation.
			previous, err := tx.GetActiveState(ctx, params.ProviderID)
			if err == nil {
				relatedProfileIDs = uniqueNonEmptyStrings(append(relatedProfileIDs, previous.ProfileID))
			} else if !errors.Is(err, ErrNotFound) {
				return err
			}
		}
		var err error
		operation, err = tx.createOperationRecord(ctx, createOperationParams{
			ID: params.ID, ProviderID: params.ProviderID, OperationType: OperationTypeMaintenance,
			Status: OperationStatusApplied, ProfileIDs: relatedProfileIDs,
			MetadataSchemaVersion: params.MetadataSchemaVersion, MetadataJSON: params.MetadataJSON,
		})
		if err != nil {
			return err
		}
		if activeProfileID != "" {
			_, err = tx.SetProviderActiveState(ctx, params.ProviderID, activeProfileID)
		}
		return err
	})
	return operation, err
}

func (s *Store) CreateAppliedImportOperation(ctx context.Context, params CreateAppliedImportOperationParams) (Operation, error) {
	return s.createOperation(ctx, createOperationParams{
		ID: params.ID, ProviderID: params.ProviderID, OperationType: OperationTypeImport,
		Status: OperationStatusApplied, ProfileIDs: params.ProfileIDs,
		MetadataSchemaVersion: params.MetadataSchemaVersion, MetadataJSON: params.MetadataJSON,
	})
}

type createOperationParams struct {
	ID                    string
	ProviderID            string
	OperationType         string
	Status                string
	SourceOperationID     string
	ProfileIDs            []string
	MetadataSchemaVersion int
	MetadataJSON          string
}

func (s *Store) createOperation(ctx context.Context, params createOperationParams) (Operation, error) {
	var operation Operation
	err := s.withTransactionIfNeeded(ctx, func(tx *Store) error {
		var err error
		operation, err = tx.createOperationRecord(ctx, params)
		return err
	})
	return operation, err
}

func (s *Store) createOperationRecord(ctx context.Context, params createOperationParams) (Operation, error) {
	if !s.transactional {
		return Operation{}, errors.New("operation creation requires a transaction")
	}
	if strings.TrimSpace(params.ID) == "" || strings.TrimSpace(params.ProviderID) == "" {
		return Operation{}, errors.New("operation id and provider_id are required")
	}
	if params.MetadataSchemaVersion != OperationMetadataSchemaVersion {
		return Operation{}, errors.New("operation metadata schema version is unsupported")
	}
	profileIDs := uniqueNonEmptyStrings(params.ProfileIDs)
	metadataJSON, err := normalizeOperationMetadataJSON(
		params.MetadataJSON,
		strings.TrimSpace(params.ProviderID),
		profileIDs,
	)
	if err != nil {
		return Operation{}, err
	}
	now := time.Now().UnixMilli()
	var sourceOperationID any
	if source := strings.TrimSpace(params.SourceOperationID); source != "" {
		sourceOperationID = source
	}
	_, err = s.executor().ExecContext(
		ctx,
		`INSERT INTO operations
			(id, provider_id, operation_type, status, source_operation_id,
				metadata_schema_version, metadata_json, created_at_unix_ms, updated_at_unix_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		strings.TrimSpace(params.ID),
		strings.TrimSpace(params.ProviderID),
		params.OperationType,
		params.Status,
		sourceOperationID,
		params.MetadataSchemaVersion,
		metadataJSON,
		now,
		now,
	)
	if err != nil {
		if isSQLiteConstraintError(err) {
			return Operation{}, ErrAlreadyExists
		}
		return Operation{}, err
	}
	if err := s.replaceOperationProfiles(ctx, params.ID, profileIDs); err != nil {
		return Operation{}, err
	}
	return s.GetOperation(ctx, params.ID)
}

func (s *Store) withTransactionIfNeeded(ctx context.Context, fn func(*Store) error) error {
	if s.transactional {
		return fn(s)
	}
	return s.WithTransaction(ctx, fn)
}

func (s *Store) replaceOperationProfiles(ctx context.Context, operationID string, profileIDs []string) error {
	if !s.transactional {
		return errors.New("operation Profile update requires a transaction")
	}
	if _, err := s.executor().ExecContext(ctx, `DELETE FROM operation_profiles WHERE operation_id = ?`, operationID); err != nil {
		return err
	}
	for _, profileID := range uniqueNonEmptyStrings(profileIDs) {
		if _, err := s.executor().ExecContext(
			ctx,
			`INSERT INTO operation_profiles (operation_id, profile_id) VALUES (?, ?)`,
			operationID,
			profileID,
		); err != nil {
			return err
		}
	}
	return nil
}

func uniqueNonEmptyStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func normalizeOperationMetadataJSON(raw, providerID string, profileIDs []string) (string, error) {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return "", errors.New("operation metadata provider_id is required")
	}
	var metadata map[string]any
	decoder := json.NewDecoder(strings.NewReader(raw))
	if err := decoder.Decode(&metadata); err != nil {
		return "", fmt.Errorf("operation metadata must be a JSON object: %w", err)
	}
	if metadata == nil {
		return "", errors.New("operation metadata must be a JSON object")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return "", errors.New("operation metadata must contain one JSON object")
	}
	if value, exists := metadata["provider_id"]; exists {
		stored, ok := value.(string)
		if !ok || stored != providerID {
			return "", errors.New("operation metadata provider_id does not match the operation")
		}
	}
	sortedProfileIDs := uniqueNonEmptyStrings(profileIDs)
	if value, exists := metadata["related_profile_ids"]; exists {
		encoded, err := json.Marshal(value)
		if err != nil {
			return "", errors.New("operation metadata related_profile_ids is invalid")
		}
		var stored []string
		if err := json.Unmarshal(encoded, &stored); err != nil ||
			!equalStrings(uniqueNonEmptyStrings(stored), sortedProfileIDs) {
			return "", errors.New("operation metadata related_profile_ids do not match the operation")
		}
	}
	metadata["provider_id"] = providerID
	metadata["related_profile_ids"] = sortedProfileIDs
	normalized, err := json.Marshal(metadata)
	if err != nil {
		return "", fmt.Errorf("encode operation metadata: %w", err)
	}
	return string(normalized), nil
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func containsString(values []string, value string) bool {
	value = strings.TrimSpace(value)
	index := sort.SearchStrings(values, value)
	return index < len(values) && values[index] == value
}

func (s *Store) operationMetadataContext(ctx context.Context, operationID string) (string, []string, error) {
	operation, err := s.GetOperation(ctx, operationID)
	if err != nil {
		return "", nil, err
	}
	profileIDs, err := s.ListOperationProfileIDs(ctx, operationID)
	if err != nil {
		return "", nil, err
	}
	return operation.ProviderID, profileIDs, nil
}

func (s *Store) GetOperation(ctx context.Context, id string) (Operation, error) {
	row := s.executor().QueryRowContext(
		ctx,
		`SELECT id, provider_id, operation_type, status, source_operation_id,
			metadata_schema_version, metadata_json, error_code, error_message,
			resolution_kind, resolved_at_unix_ms, created_at_unix_ms, updated_at_unix_ms
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
		`SELECT id, provider_id, operation_type, status, source_operation_id,
			metadata_schema_version, metadata_json, error_code, error_message,
			resolution_kind, resolved_at_unix_ms, created_at_unix_ms, updated_at_unix_ms
		FROM operations
		WHERE status IN (?, ?) AND resolved_at_unix_ms = 0
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

// HasUnresolvedSwitchOperation reports whether recovery or an explicit close
// is still required for a root switch operation other than excludeID.
func (s *Store) HasUnresolvedSwitchOperation(ctx context.Context, excludeID string) (bool, error) {
	var exists int
	err := s.executor().QueryRowContext(
		ctx,
		`SELECT EXISTS (
			SELECT 1
			FROM operations
			WHERE operation_type = ?
				AND status IN (?, ?)
				AND resolved_at_unix_ms = 0
				AND id <> ?
		)`,
		OperationTypeSwitch,
		OperationStatusPending,
		OperationStatusFailed,
		excludeID,
	).Scan(&exists)
	return exists != 0, err
}

// RejectPendingSwitchOperation closes a switch that was blocked before any
// recovery point or external target write could be created.
func (s *Store) RejectPendingSwitchOperation(ctx context.Context, id, errorCode, errorMessage, resolutionKind string) error {
	if strings.TrimSpace(resolutionKind) == "" {
		return errors.New("operation resolution kind is required")
	}
	now := time.Now().UnixMilli()
	result, err := s.executor().ExecContext(
		ctx,
		`UPDATE operations
		SET status = ?, error_code = ?, error_message = ?, resolution_kind = ?,
			resolved_at_unix_ms = ?, updated_at_unix_ms = ?
		WHERE id = ? AND operation_type = ? AND status = ? AND resolved_at_unix_ms = 0`,
		OperationStatusFailed,
		errorCode,
		errorMessage,
		resolutionKind,
		now,
		now,
		id,
		OperationTypeSwitch,
		OperationStatusPending,
	)
	if err != nil {
		return err
	}
	if rows, err := result.RowsAffected(); err == nil && rows == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) UpdateOperationMetadata(
	ctx context.Context,
	id string,
	metadataSchemaVersion int,
	metadataJSON string,
	profileIDs []string,
) error {
	if metadataSchemaVersion != OperationMetadataSchemaVersion {
		return errors.New("operation metadata schema version is unsupported")
	}
	return s.withTransactionIfNeeded(ctx, func(tx *Store) error {
		providerID, _, err := tx.operationMetadataContext(ctx, id)
		if err != nil {
			return err
		}
		profileIDs = uniqueNonEmptyStrings(profileIDs)
		normalizedMetadata, err := normalizeOperationMetadataJSON(metadataJSON, providerID, profileIDs)
		if err != nil {
			return err
		}
		result, err := tx.executor().ExecContext(
			ctx,
			`UPDATE operations
			SET metadata_schema_version = ?, metadata_json = ?, updated_at_unix_ms = ?
			WHERE id = ?`,
			metadataSchemaVersion,
			normalizedMetadata,
			time.Now().UnixMilli(),
			id,
		)
		if err != nil {
			return err
		}
		if rows, err := result.RowsAffected(); err == nil && rows == 0 {
			return ErrNotFound
		}
		return tx.replaceOperationProfiles(ctx, id, profileIDs)
	})
}

func (s *Store) MarkOperationFailed(ctx context.Context, params MarkOperationFailedParams) error {
	assignments := []string{
		"status = ?",
		"error_code = ?",
		"error_message = ?",
	}
	args := []any{OperationStatusFailed, params.ErrorCode, params.ErrorMessage}
	if params.MetadataJSON != nil {
		providerID, profileIDs, err := s.operationMetadataContext(ctx, params.ID)
		if err != nil {
			return err
		}
		metadataJSON, err := normalizeOperationMetadataJSON(*params.MetadataJSON, providerID, profileIDs)
		if err != nil {
			return err
		}
		assignments = append(assignments, "metadata_json = ?")
		args = append(args, metadataJSON)
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
	if params.MetadataSchemaVersion != OperationMetadataSchemaVersion {
		return errors.New("operation metadata schema version is unsupported")
	}
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
	_, profileIDs, err := txStore.operationMetadataContext(ctx, params.ID)
	if err != nil {
		return err
	}
	metadataJSON, err := normalizeOperationMetadataJSON(params.MetadataJSON, params.ProviderID, profileIDs)
	if err != nil {
		return err
	}
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
		SET status = ?, metadata_schema_version = ?, metadata_json = ?,
			error_code = '', error_message = '', updated_at_unix_ms = ?
		WHERE id = ? AND provider_id = ? AND operation_type = ?`,
		OperationStatusApplied,
		params.MetadataSchemaVersion,
		metadataJSON,
		now,
		params.ID,
		params.ProviderID,
		OperationTypeSwitch,
	)
	if err != nil {
		return err
	}
	if rows, err := result.RowsAffected(); err == nil && rows == 0 {
		return ErrNotFound
	}

	// The active revision is live concurrency state, not a pointer to audit
	// history. Commit it with captures and the applied operation.
	if _, err := txStore.SetProviderActiveState(ctx, params.ProviderID, params.ProfileID); err != nil {
		return err
	}
	if err := txStore.RequireRecoveryCleanup(ctx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *Store) CompleteRecoveryOperation(ctx context.Context, params CompleteRecoveryOperationParams) error {
	if params.MetadataSchemaVersion != OperationMetadataSchemaVersion {
		return errors.New("operation metadata schema version is unsupported")
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
	_, profileIDs, err := txStore.operationMetadataContext(ctx, params.ID)
	if err != nil {
		return err
	}
	metadataJSON, err := normalizeOperationMetadataJSON(params.MetadataJSON, params.ProviderID, profileIDs)
	if err != nil {
		return err
	}

	now := time.Now().UnixMilli()
	result, err := tx.ExecContext(
		ctx,
		`UPDATE operations
		SET status = ?, metadata_schema_version = ?, metadata_json = ?,
			error_code = '', error_message = '', updated_at_unix_ms = ?
		WHERE id = ? AND provider_id = ? AND operation_type = ? AND source_operation_id = ?`,
		OperationStatusApplied,
		params.MetadataSchemaVersion,
		metadataJSON,
		now,
		params.ID,
		params.ProviderID,
		OperationTypeRecovery,
		params.SourceOperationID,
	)
	if err != nil {
		return err
	}
	if rows, err := result.RowsAffected(); err == nil && rows == 0 {
		return ErrNotFound
	}

	if params.ResolutionKind == "" {
		return errors.New("recovery resolution kind is required")
	}
	result, err = tx.ExecContext(
		ctx,
		`UPDATE operations
		SET resolution_kind = ?, resolved_at_unix_ms = ?, updated_at_unix_ms = ?
		WHERE id = ? AND provider_id = ? AND operation_type = ? AND resolved_at_unix_ms = 0`,
		params.ResolutionKind,
		now,
		now,
		params.SourceOperationID,
		params.ProviderID,
		OperationTypeSwitch,
	)
	if err != nil {
		return err
	}
	if rows, err := result.RowsAffected(); err == nil && rows == 0 {
		return ErrNotFound
	}

	// A failed switch never commits active state. Recovery verifies the captured
	// revision before writes and therefore leaves the live state unchanged.
	if err := txStore.RequireRecoveryCleanup(ctx); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *Store) ResolveOperation(ctx context.Context, id, resolutionKind string) error {
	if strings.TrimSpace(resolutionKind) == "" {
		return errors.New("operation resolution kind is required")
	}
	now := time.Now().UnixMilli()
	result, err := s.executor().ExecContext(
		ctx,
		`UPDATE operations
		SET resolution_kind = ?, resolved_at_unix_ms = ?, updated_at_unix_ms = ?
		WHERE id = ? AND status IN (?, ?) AND resolved_at_unix_ms = 0`,
		resolutionKind,
		now,
		now,
		id,
		OperationStatusPending,
		OperationStatusFailed,
	)
	if err != nil {
		return err
	}
	if rows, err := result.RowsAffected(); err == nil && rows == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ResolveSwitchOperationForCleanup(ctx context.Context, id, resolutionKind string) error {
	if strings.TrimSpace(resolutionKind) == "" {
		return errors.New("operation resolution kind is required")
	}
	return s.WithTransaction(ctx, func(tx *Store) error {
		now := time.Now().UnixMilli()
		result, err := tx.executor().ExecContext(
			ctx,
			`UPDATE operations
			SET resolution_kind = ?, resolved_at_unix_ms = ?, updated_at_unix_ms = ?
			WHERE id = ? AND operation_type = ? AND status IN (?, ?) AND resolved_at_unix_ms = 0`,
			resolutionKind,
			now,
			now,
			id,
			OperationTypeSwitch,
			OperationStatusPending,
			OperationStatusFailed,
		)
		if err != nil {
			return err
		}
		if rows, err := result.RowsAffected(); err == nil && rows == 0 {
			return ErrNotFound
		}
		return tx.RequireRecoveryCleanup(ctx)
	})
}

// PrepareForApplicationRestore severs runtime state that cannot be restored
// without also mutating tool-owned targets.
func (s *Store) PrepareForApplicationRestore(ctx context.Context) error {
	return s.WithTransaction(ctx, func(tx *Store) error {
		if _, err := tx.executor().ExecContext(ctx, `DELETE FROM provider_active_states`); err != nil {
			return err
		}
		now := time.Now().UnixMilli()
		_, err := tx.executor().ExecContext(
			ctx,
			`UPDATE operations
			SET resolution_kind = 'application_restore', resolved_at_unix_ms = ?, updated_at_unix_ms = ?
			WHERE status IN (?, ?) AND resolved_at_unix_ms = 0`,
			now,
			now,
			OperationStatusPending,
			OperationStatusFailed,
		)
		if err != nil {
			return err
		}
		return tx.RequireRecoveryCleanup(ctx)
	})
}

func (s *Store) SetProviderActiveState(ctx context.Context, providerID, profileID string) (ActiveState, error) {
	providerID = strings.TrimSpace(providerID)
	profileID = strings.TrimSpace(profileID)
	if providerID == "" || profileID == "" {
		return ActiveState{}, errors.New("active Provider and Profile ids are required")
	}
	row := s.executor().QueryRowContext(
		ctx,
		`INSERT INTO provider_active_states
			(provider_id, profile_id, revision, updated_at_unix_ms)
		VALUES (?, ?, 1, ?)
		ON CONFLICT(provider_id) DO UPDATE SET
			profile_id = excluded.profile_id,
			revision = provider_active_states.revision + 1,
			updated_at_unix_ms = excluded.updated_at_unix_ms
		RETURNING provider_id, profile_id, revision, updated_at_unix_ms`,
		providerID,
		profileID,
		time.Now().UnixMilli(),
	)
	return scanActiveState(row)
}

func (s *Store) GetActiveState(ctx context.Context, providerID string) (ActiveState, error) {
	row := s.executor().QueryRowContext(
		ctx,
		`SELECT provider_id, profile_id, revision, updated_at_unix_ms
		FROM provider_active_states
		WHERE provider_id = ?`,
		strings.TrimSpace(providerID),
	)
	activeState, err := scanActiveState(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ActiveState{}, ErrNotFound
	}
	return activeState, err
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
	return s.schemaHealthyForApplied(ctx, len(schemaContracts))
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
	if len(columns) != len(spec.columns) {
		return false, nil
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

	if len(spec.checks) == 0 && !spec.strict {
		return true, nil
	}

	createSQL, err := s.tableCreateSQL(ctx, spec.name)
	if err != nil {
		return false, err
	}
	compactCreateSQL := compactSQL(createSQL)
	if spec.strict && !strings.HasSuffix(compactCreateSQL, ")strict") &&
		!strings.HasSuffix(compactCreateSQL, ")strict,withoutrowid") {
		return false, nil
	}
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
		"SELECT COUNT(1) FROM operations WHERE status = ? AND resolved_at_unix_ms = 0",
		status,
	).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Store) CountActiveProfileReferences(ctx context.Context, profileID string) (int, error) {
	var count int
	err := s.executor().QueryRowContext(
		ctx,
		"SELECT COUNT(1) FROM provider_active_states WHERE profile_id = ?",
		strings.TrimSpace(profileID),
	).Scan(&count)
	return count, err
}

func (s *Store) CountUnresolvedProfileOperations(ctx context.Context, profileID string) (int, error) {
	var count int
	err := s.executor().QueryRowContext(
		ctx,
		`SELECT COUNT(1)
		FROM operations AS o
		JOIN operation_profiles AS op ON op.operation_id = o.id
		WHERE op.profile_id = ?
			AND o.status IN (?, ?)
			AND o.resolved_at_unix_ms = 0`,
		strings.TrimSpace(profileID),
		OperationStatusPending,
		OperationStatusFailed,
	).Scan(&count)
	return count, err
}

func (s *Store) CountUnresolvedProviderOperations(ctx context.Context, providerID string) (int, error) {
	var count int
	err := s.executor().QueryRowContext(
		ctx,
		`SELECT COUNT(1)
		FROM operations
		WHERE provider_id = ?
			AND status IN (?, ?)
			AND resolved_at_unix_ms = 0`,
		strings.TrimSpace(providerID),
		OperationStatusPending,
		OperationStatusFailed,
	).Scan(&count)
	return count, err
}

func (s *Store) DeleteResolvedProviderOperations(ctx context.Context, providerID string) error {
	_, err := s.executor().ExecContext(
		ctx,
		`DELETE FROM operations
		WHERE provider_id = ?
			AND NOT (status IN (?, ?) AND resolved_at_unix_ms = 0)`,
		strings.TrimSpace(providerID),
		OperationStatusPending,
		OperationStatusFailed,
	)
	return err
}

func (s *Store) DeleteResolvedProfileOperations(ctx context.Context, profileID string) error {
	_, err := s.executor().ExecContext(
		ctx,
		`DELETE FROM operations
		WHERE id IN (
			SELECT o.id
			FROM operations AS o
			JOIN operation_profiles AS op ON op.operation_id = o.id
			WHERE op.profile_id = ?
				AND NOT (o.status IN (?, ?) AND o.resolved_at_unix_ms = 0)
		)`,
		strings.TrimSpace(profileID),
		OperationStatusPending,
		OperationStatusFailed,
	)
	return err
}

func (s *Store) ListOperationProfileIDs(ctx context.Context, operationID string) ([]string, error) {
	rows, err := s.executor().QueryContext(
		ctx,
		`SELECT profile_id
		FROM operation_profiles
		WHERE operation_id = ?
		ORDER BY profile_id ASC`,
		strings.TrimSpace(operationID),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	profileIDs := []string{}
	for rows.Next() {
		var profileID string
		if err := rows.Scan(&profileID); err != nil {
			return nil, err
		}
		profileIDs = append(profileIDs, profileID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return profileIDs, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanProvider(row rowScanner) (Provider, error) {
	var provider Provider
	if err := row.Scan(
		&provider.ID,
		&provider.Name,
		&provider.AdapterID,
		&provider.MetadataJSON,
		&provider.CreatedAtUnixMS,
		&provider.UpdatedAtUnixMS,
	); err != nil {
		return Provider{}, err
	}
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
	if err := row.Scan(
		&setting.ProfileID,
		&setting.ProviderID,
		&setting.SchemaVersion,
		&setting.SettingsJSON,
		&setting.UpdatedAtUnixMS,
	); err != nil {
		return ProviderProfileSetting{}, err
	}
	return setting, nil
}

func scanProviderSetting(row rowScanner) (ProviderSetting, error) {
	var setting ProviderSetting
	if err := row.Scan(
		&setting.ProviderID,
		&setting.SchemaVersion,
		&setting.SettingsJSON,
		&setting.UpdatedAtUnixMS,
	); err != nil {
		return ProviderSetting{}, err
	}
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
	var sourceOperationID sql.NullString
	if err := row.Scan(
		&operation.ID,
		&operation.ProviderID,
		&operation.OperationType,
		&operation.Status,
		&sourceOperationID,
		&operation.MetadataSchemaVersion,
		&operation.MetadataJSON,
		&operation.ErrorCode,
		&operation.ErrorMessage,
		&operation.ResolutionKind,
		&operation.ResolvedAtUnixMS,
		&operation.CreatedAtUnixMS,
		&operation.UpdatedAtUnixMS,
	); err != nil {
		return Operation{}, err
	}
	operation.SourceOperationID = sourceOperationID.String
	return operation, nil
}

func scanActiveState(row rowScanner) (ActiveState, error) {
	var activeState ActiveState
	if err := row.Scan(
		&activeState.ProviderID,
		&activeState.ProfileID,
		&activeState.Revision,
		&activeState.UpdatedAtUnixMS,
	); err != nil {
		return ActiveState{}, err
	}
	return activeState, nil
}

func isSQLiteConstraintError(err error) bool {
	var sqliteErr *sqlite.Error
	return errors.As(err, &sqliteErr) && sqliteErr.Code()&0xff == sqlite3.SQLITE_CONSTRAINT
}

func isSQLiteBusyError(err error) bool {
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	code := sqliteErr.Code() & 0xff
	return code == sqlite3.SQLITE_BUSY || code == sqlite3.SQLITE_LOCKED
}

func normalizeQuickCheckError(err error) error {
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) {
		return err
	}
	code := sqliteErr.Code() & 0xff
	if code == sqlite3.SQLITE_CORRUPT || code == sqlite3.SQLITE_NOTADB {
		return ErrQuickCheckFailed
	}
	return err
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

	normalizedPath := strings.ReplaceAll(databasePath, `\`, `/`)
	// A drive-letter path must remain a URI path. Without the leading slash,
	// net/url serializes the drive as an authority and SQLite rejects it.
	if len(normalizedPath) >= 2 && normalizedPath[1] == ':' {
		normalizedPath = "/" + normalizedPath
	}
	u := url.URL{
		Scheme: "file",
		Path:   normalizedPath,
	}
	q := u.Query()
	q.Set("mode", mode)
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", sqliteBusyTimeout.Milliseconds()))
	q.Add("_pragma", "foreign_keys(1)")
	u.RawQuery = q.Encode()

	return u.String()
}
