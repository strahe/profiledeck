package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/strahe/profiledeck/internal/store/migrations"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"github.com/uptrace/bun/migrate"
	_ "modernc.org/sqlite"
)

const (
	sqliteDriverName  = "sqlite"
	sqliteBusyTimeout = 5 * time.Second

	OperationStatusPending = "pending"
	OperationStatusFailed  = "failed"
	OperationStatusApplied = "applied"
)

type Store struct {
	db *bun.DB
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
}

var initialIndexSpecs = []indexSpec{
	{name: "idx_providers_adapter_id", table: "providers", columns: []string{"adapter_id"}},
	{name: "idx_providers_enabled", table: "providers", columns: []string{"enabled"}},
	{name: "idx_operations_status", table: "operations", columns: []string{"status"}},
	{name: "idx_operations_operation_type", table: "operations", columns: []string{"operation_type"}},
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
		db: bun.NewDB(sqlDB, sqlitedialect.New()),
	}, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
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

	return true, nil
}

func (s *Store) indexSchemaHealthy(ctx context.Context, spec indexSpec) (bool, error) {
	exists, err := s.indexExistsOnTable(ctx, spec.table, spec.name)
	if err != nil {
		return false, err
	}
	if !exists {
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

func (s *Store) indexExistsOnTable(ctx context.Context, table string, index string) (bool, error) {
	rows, err := s.db.DB.QueryContext(ctx, fmt.Sprintf("PRAGMA index_list(%s)", quoteSQLiteIdentifier(table)))
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var seq int
		var name string
		var unique int
		var origin string
		var partial int
		if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			return false, err
		}
		if name == index {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return false, nil
}

func (s *Store) indexColumns(ctx context.Context, index string) ([]string, error) {
	rows, err := s.db.DB.QueryContext(ctx, fmt.Sprintf("PRAGMA index_info(%s)", quoteSQLiteIdentifier(index)))
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
	rows, err := s.db.DB.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", quoteSQLiteIdentifier(table)))
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
	err := s.db.DB.QueryRowContext(
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
	err := s.db.DB.QueryRowContext(
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
	err := s.db.DB.QueryRowContext(
		ctx,
		"SELECT COUNT(1) FROM operations WHERE status = ?",
		status,
	).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
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
