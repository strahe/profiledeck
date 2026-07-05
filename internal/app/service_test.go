package app

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStatusBeforeInitReportsUninitializedWithoutCreatingFiles(t *testing.T) {
	ctx := context.Background()
	configDir := filepath.Join(t.TempDir(), "config")

	result, err := Status(ctx, StatusRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected status before init to succeed, got %v", err)
	}
	if result.Initialized {
		t.Fatalf("expected status before init to report uninitialized")
	}
	if result.SchemaHealthy {
		t.Fatalf("expected schema before init to be unhealthy")
	}
	if _, err := os.Stat(configDir); !os.IsNotExist(err) {
		t.Fatalf("expected status not to create config dir, stat error: %v", err)
	}
}

func TestInitCreatesRuntimeAndDatabase(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()

	result, err := Init(ctx, InitRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	if !result.Initialized || !result.SchemaHealthy {
		t.Fatalf("expected initialized healthy result, got %#v", result)
	}
	if result.MigrationsApplied != 1 {
		t.Fatalf("expected first init to apply one migration, got %d", result.MigrationsApplied)
	}

	for _, path := range []string{
		result.RuntimeRoot,
		filepath.Join(result.RuntimeRoot, "backups"),
		filepath.Join(result.RuntimeRoot, "exports"),
		filepath.Join(result.RuntimeRoot, "logs"),
		filepath.Join(result.RuntimeRoot, "locks"),
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("expected runtime path %s to exist, got %v", path, err)
		}
		if !info.IsDir() {
			t.Fatalf("expected runtime path %s to be a directory", path)
		}
	}
	if info, err := os.Stat(result.DatabasePath); err != nil {
		t.Fatalf("expected database to exist, got %v", err)
	} else if info.IsDir() {
		t.Fatalf("expected database path to be a file")
	}
}

func TestInitIsIdempotent(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()

	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected first init to succeed, got %v", err)
	}
	result, err := Init(ctx, InitRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected second init to succeed, got %v", err)
	}
	if result.MigrationsApplied != 0 {
		t.Fatalf("expected second init to apply no migrations, got %d", result.MigrationsApplied)
	}
}

func TestStatusAfterInitReportsHealthy(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()

	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	result, err := Status(ctx, StatusRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected status after init to succeed, got %v", err)
	}
	if !result.Initialized || !result.SchemaHealthy {
		t.Fatalf("expected initialized healthy status, got %#v", result)
	}
	if result.PendingOperations != 0 || result.FailedOperations != 0 {
		t.Fatalf("expected no operations, got pending=%d failed=%d", result.PendingOperations, result.FailedOperations)
	}
}

func TestStatusWithMissingSchemaReportsUnhealthy(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	dbPath := filepath.Join(configDir, "profiledeck", "profiledeck.db")

	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		t.Fatalf("expected database dir setup to succeed, got %v", err)
	}
	if file, err := os.Create(dbPath); err != nil {
		t.Fatalf("expected empty database file setup to succeed, got %v", err)
	} else if err := file.Close(); err != nil {
		t.Fatalf("expected empty database file close to succeed, got %v", err)
	}

	result, err := Status(ctx, StatusRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected status to succeed for missing schema, got %v", err)
	}
	if !result.Initialized {
		t.Fatalf("expected existing database file to report initialized")
	}
	if result.SchemaHealthy {
		t.Fatalf("expected missing schema to be unhealthy")
	}
}

func TestStatusWithCorruptDatabaseReturnsStructuredError(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	dbPath := filepath.Join(configDir, "profiledeck", "profiledeck.db")

	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		t.Fatalf("expected database dir setup to succeed, got %v", err)
	}
	if err := os.WriteFile(dbPath, []byte("not a sqlite database"), 0o600); err != nil {
		t.Fatalf("expected corrupt database setup to succeed, got %v", err)
	}

	_, err := Status(ctx, StatusRequest{ConfigDir: configDir})
	if err == nil {
		t.Fatalf("expected corrupt database status to fail")
	}

	var appErr *AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected AppError, got %T", err)
	}
	if appErr.Code != ErrorStoreStatusFailed && appErr.Code != ErrorStoreOpenFailed {
		t.Fatalf("expected structured store error, got %s", appErr.Code)
	}
}

func TestProviderProfileCommandsRequireInitializedStore(t *testing.T) {
	ctx := context.Background()
	configDir := filepath.Join(t.TempDir(), "config")

	_, err := ListProviders(ctx, ListProvidersRequest{ConfigDir: configDir})
	assertAppErrorCode(t, err, ErrorStoreNotInitialized)

	_, err = CreateProfile(ctx, CreateProfileRequest{
		ConfigDir: configDir,
		ID:        "profile-a",
		Name:      "Profile A",
	})
	assertAppErrorCode(t, err, ErrorStoreNotInitialized)
	if _, err := os.Stat(configDir); !os.IsNotExist(err) {
		t.Fatalf("expected CRUD commands not to create config dir, stat error: %v", err)
	}
}

func TestProviderProfileCommandsRejectUnhealthySchema(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	dbPath := filepath.Join(configDir, "profiledeck", "profiledeck.db")

	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		t.Fatalf("expected database dir setup to succeed, got %v", err)
	}
	if file, err := os.Create(dbPath); err != nil {
		t.Fatalf("expected empty database file setup to succeed, got %v", err)
	} else if err := file.Close(); err != nil {
		t.Fatalf("expected empty database file close to succeed, got %v", err)
	}

	_, err := ListProfiles(ctx, ListProfilesRequest{ConfigDir: configDir})
	assertAppErrorCode(t, err, ErrorStoreSchemaInvalid)
}

func TestProviderAndProfileValidation(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	_, err := CreateProvider(ctx, CreateProviderRequest{
		ConfigDir: configDir,
		ID:        "ProviderA",
		Name:      "Provider A",
		AdapterID: "adapter-a",
	})
	assertAppErrorCode(t, err, ErrorProviderInvalid)

	_, err = CreateProvider(ctx, CreateProviderRequest{
		ConfigDir:    configDir,
		ID:           "provider-a",
		Name:         "Provider A",
		AdapterID:    "adapter-a",
		MetadataJSON: stringPtr(`[]`),
	})
	assertAppErrorCode(t, err, ErrorProviderInvalid)

	for _, raw := range []string{
		`{"openai_api_key":"secret"}`,
		`{"apiKey":"secret"}`,
		`{"openaiApiKey":"secret"}`,
		`{"accessToken":"secret"}`,
		`{"my_secret":"secret"}`,
		`{"secret_key":"secret"}`,
		`{"nested":{"refreshToken":"secret"}}`,
		`{"items":[{"password":"secret"}]}`,
	} {
		_, err = CreateProvider(ctx, CreateProviderRequest{
			ConfigDir:    configDir,
			ID:           "provider-a",
			Name:         "Provider A",
			AdapterID:    "adapter-a",
			MetadataJSON: &raw,
		})
		assertAppErrorCode(t, err, ErrorProviderInvalid)
	}

	_, err = CreateProvider(ctx, CreateProviderRequest{
		ConfigDir:    configDir,
		ID:           "provider-a",
		Name:         "Provider A",
		AdapterID:    "adapter-a",
		MetadataJSON: stringPtr(`{"blob":"` + strings.Repeat("x", 64*1024) + `"}`),
	})
	assertAppErrorCode(t, err, ErrorProviderInvalid)

	profile, err := CreateProfile(ctx, CreateProfileRequest{
		ConfigDir:    configDir,
		ID:           "profile-a",
		Name:         "Profile A",
		MetadataJSON: stringPtr(`{"max_tokens":100,"nested":{"token_budget":1000}}`),
	})
	if err != nil {
		t.Fatalf("expected token budget metadata to be accepted, got %v", err)
	}
	if profile.Metadata["max_tokens"] == nil {
		t.Fatalf("expected metadata to be returned as object, got %#v", profile.Metadata)
	}
}

func TestMetadataOutputRedactsSensitiveKeys(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	initResult, err := Init(ctx, InitRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	sqlDB, err := sql.Open("sqlite", initResult.DatabasePath)
	if err != nil {
		t.Fatalf("expected sqlite open to succeed, got %v", err)
	}
	defer sqlDB.Close()
	_, err = sqlDB.ExecContext(ctx, `
		INSERT INTO providers (id, name, adapter_id, metadata_json, created_at_unix_ms, updated_at_unix_ms)
		VALUES (
			'provider-a',
			'Provider A',
			'adapter-a',
			'{"apiKey":"raw-key","my_secret":"raw-secret","max_tokens":100,"safe":"ok","nested":{"authorization":"Bearer raw"},"items":[{"refreshToken":"raw-refresh"}]}',
			1,
			1
		)
	`)
	if err != nil {
		t.Fatalf("expected provider setup to succeed, got %v", err)
	}

	provider, err := GetProvider(ctx, GetProviderRequest{
		ConfigDir: configDir,
		ID:        "provider-a",
	})
	if err != nil {
		t.Fatalf("expected provider get to succeed, got %v", err)
	}

	for _, key := range []string{"apiKey", "my_secret", "max_tokens"} {
		if got := provider.Metadata[key]; got != redactedValue {
			t.Fatalf("expected %s to be redacted, got %#v", key, got)
		}
	}
	if got := provider.Metadata["safe"]; got != "ok" {
		t.Fatalf("expected safe metadata to remain visible, got %#v", got)
	}
	nested := provider.Metadata["nested"].(map[string]any)
	if got := nested["authorization"]; got != redactedValue {
		t.Fatalf("expected nested authorization to be redacted, got %#v", got)
	}
	items := provider.Metadata["items"].([]any)
	first := items[0].(map[string]any)
	if got := first["refreshToken"]; got != redactedValue {
		t.Fatalf("expected array refreshToken to be redacted, got %#v", got)
	}
}

func TestProviderProfileAppCRUD(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := Init(ctx, InitRequest{ConfigDir: configDir}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	disabled := false
	provider, err := CreateProvider(ctx, CreateProviderRequest{
		ConfigDir: configDir,
		ID:        "provider-b",
		Name:      "Provider B",
		AdapterID: "adapter-b",
		Enabled:   &disabled,
	})
	if err != nil {
		t.Fatalf("expected provider create to succeed, got %v", err)
	}
	if provider.Enabled {
		t.Fatalf("expected provider to be disabled")
	}

	enabledProviders, err := ListProviders(ctx, ListProvidersRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected provider list to succeed, got %v", err)
	}
	if len(enabledProviders) != 0 {
		t.Fatalf("expected disabled provider to be hidden by default, got %#v", enabledProviders)
	}

	enabled := true
	provider, err = UpdateProvider(ctx, UpdateProviderRequest{
		ConfigDir: configDir,
		ID:        "provider-b",
		Enabled:   &enabled,
	})
	if err != nil {
		t.Fatalf("expected provider update to succeed, got %v", err)
	}
	if !provider.Enabled {
		t.Fatalf("expected provider to be enabled")
	}

	_, err = UpdateProvider(ctx, UpdateProviderRequest{
		ConfigDir: configDir,
		ID:        "provider-b",
	})
	assertAppErrorCode(t, err, ErrorProviderInvalid)

	result, err := DeleteProvider(ctx, DeleteProviderRequest{
		ConfigDir: configDir,
		ID:        "provider-b",
		Confirm:   true,
	})
	if err != nil {
		t.Fatalf("expected provider delete to succeed, got %v", err)
	}
	if !result.Deleted || result.ID != "provider-b" {
		t.Fatalf("unexpected delete result: %#v", result)
	}
}

func TestProfileDeleteConfirmationAndInUseProtection(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	initResult, err := Init(ctx, InitRequest{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	if _, err := CreateProfile(ctx, CreateProfileRequest{
		ConfigDir: configDir,
		ID:        "profile-a",
		Name:      "Profile A",
	}); err != nil {
		t.Fatalf("expected profile create to succeed, got %v", err)
	}

	_, err = DeleteProfile(ctx, DeleteProfileRequest{
		ConfigDir: configDir,
		ID:        "profile-a",
	})
	assertAppErrorCode(t, err, ErrorConfirmationRequired)

	sqlDB, err := sql.Open("sqlite", initResult.DatabasePath)
	if err != nil {
		t.Fatalf("expected sqlite open to succeed, got %v", err)
	}
	defer sqlDB.Close()
	_, err = sqlDB.ExecContext(ctx, `
		INSERT INTO operations (id, operation_type, status, profile_id, created_at_unix_ms, updated_at_unix_ms)
		VALUES ('operation-a', 'switch', 'applied', 'profile-a', 1, 1)
	`)
	if err != nil {
		t.Fatalf("expected operation setup to succeed, got %v", err)
	}

	_, err = DeleteProfile(ctx, DeleteProfileRequest{
		ConfigDir: configDir,
		ID:        "profile-a",
		Confirm:   true,
	})
	assertAppErrorCode(t, err, ErrorProfileInUse)
}

func assertAppErrorCode(t *testing.T, err error, code ErrorCode) {
	t.Helper()

	if err == nil {
		t.Fatalf("expected error code %s, got nil", code)
	}
	var appErr *AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected AppError code %s, got %T: %v", code, err, err)
	}
	if appErr.Code != code {
		t.Fatalf("expected error code %s, got %s: %v", code, appErr.Code, err)
	}
}

func stringPtr(value string) *string {
	return &value
}
