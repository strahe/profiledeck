package cli

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/strahe/profiledeck/internal/app"
)

func TestNewCommandBuildsRootCommand(t *testing.T) {
	cmd := NewCommand(app.DefaultInfo())

	if cmd.Name != app.CLIName {
		t.Fatalf("expected command name %q, got %q", app.CLIName, cmd.Name)
	}
	if cmd.Version != app.DefaultVersion {
		t.Fatalf("expected version %q, got %q", app.DefaultVersion, cmd.Version)
	}
	if cmd.Command("version") == nil {
		t.Fatalf("expected version subcommand")
	}
	if cmd.Command("init") == nil {
		t.Fatalf("expected init subcommand")
	}
	if cmd.Command("status") == nil {
		t.Fatalf("expected status subcommand")
	}
	if cmd.Command("provider") == nil {
		t.Fatalf("expected provider subcommand")
	}
	if cmd.Command("profile") == nil {
		t.Fatalf("expected profile subcommand")
	}
}

func TestRootHelp(t *testing.T) {
	var out bytes.Buffer
	cmd := NewCommand(app.DefaultInfo())
	cmd.Writer = &out

	if err := cmd.Run(context.Background(), []string{app.CLIName, "--help"}); err != nil {
		t.Fatalf("expected help to succeed, got %v", err)
	}
	if !strings.Contains(out.String(), app.CLIName) {
		t.Fatalf("expected help output to contain %q, got %q", app.CLIName, out.String())
	}
}

func TestRootVersionFlag(t *testing.T) {
	var out bytes.Buffer
	cmd := NewCommand(app.DefaultInfo())
	cmd.Writer = &out

	if err := cmd.Run(context.Background(), []string{app.CLIName, "--version"}); err != nil {
		t.Fatalf("expected version flag to succeed, got %v", err)
	}
	if !strings.Contains(out.String(), app.DefaultVersion) {
		t.Fatalf("expected version output to contain %q, got %q", app.DefaultVersion, out.String())
	}
}

func TestVersionCommand(t *testing.T) {
	var out bytes.Buffer
	cmd := NewCommand(app.DefaultInfo())
	cmd.Writer = &out

	if err := cmd.Run(context.Background(), []string{app.CLIName, "version"}); err != nil {
		t.Fatalf("expected version command to succeed, got %v", err)
	}
	if !strings.Contains(out.String(), app.DefaultVersion) {
		t.Fatalf("expected version output to contain %q, got %q", app.DefaultVersion, out.String())
	}
	if !strings.Contains(out.String(), app.UnknownBuildValue) {
		t.Fatalf("expected version output to contain %q, got %q", app.UnknownBuildValue, out.String())
	}
}

func TestStatusJSONBeforeInit(t *testing.T) {
	var out bytes.Buffer
	configDir := filepath.Join(t.TempDir(), "config")
	cmd := NewCommand(app.DefaultInfo())
	cmd.Writer = &out

	if err := cmd.Run(context.Background(), []string{app.CLIName, "--config-dir", configDir, "status", "--json"}); err != nil {
		t.Fatalf("expected status to succeed, got %v", err)
	}

	var result app.StatusResult
	decodeCLIJSON(t, out.Bytes(), &result)
	if result.ConfigDir != configDir {
		t.Fatalf("expected config_dir %q, got %q", configDir, result.ConfigDir)
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

func TestInitJSONCreatesRuntimeAndDatabase(t *testing.T) {
	var out bytes.Buffer
	configDir := t.TempDir()
	cmd := NewCommand(app.DefaultInfo())
	cmd.Writer = &out

	if err := cmd.Run(context.Background(), []string{app.CLIName, "--config-dir", configDir, "init", "--json"}); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	var result app.InitResult
	decodeCLIJSON(t, out.Bytes(), &result)
	if result.ConfigDir != configDir {
		t.Fatalf("expected config_dir %q, got %q", configDir, result.ConfigDir)
	}
	if !result.Initialized || !result.SchemaHealthy {
		t.Fatalf("expected initialized healthy result, got %#v", result)
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
	assertNoTargetToolConfigCreated(t, configDir)
}

func TestInitTwiceAndStatusJSONAfterInit(t *testing.T) {
	configDir := t.TempDir()

	var initOut bytes.Buffer
	initCmd := NewCommand(app.DefaultInfo())
	initCmd.Writer = &initOut
	if err := initCmd.Run(context.Background(), []string{app.CLIName, "--config-dir", configDir, "init", "--json"}); err != nil {
		t.Fatalf("expected first init to succeed, got %v", err)
	}

	var secondInitOut bytes.Buffer
	secondInitCmd := NewCommand(app.DefaultInfo())
	secondInitCmd.Writer = &secondInitOut
	if err := secondInitCmd.Run(context.Background(), []string{app.CLIName, "--config-dir", configDir, "init", "--json"}); err != nil {
		t.Fatalf("expected second init to succeed, got %v", err)
	}
	var secondInit app.InitResult
	decodeCLIJSON(t, secondInitOut.Bytes(), &secondInit)
	if secondInit.MigrationsApplied != 0 {
		t.Fatalf("expected second init to apply no migrations, got %d", secondInit.MigrationsApplied)
	}

	var statusOut bytes.Buffer
	statusCmd := NewCommand(app.DefaultInfo())
	statusCmd.Writer = &statusOut
	if err := statusCmd.Run(context.Background(), []string{app.CLIName, "--config-dir", configDir, "status", "--json"}); err != nil {
		t.Fatalf("expected status to succeed, got %v", err)
	}

	var status app.StatusResult
	decodeCLIJSON(t, statusOut.Bytes(), &status)
	if !status.Initialized || !status.SchemaHealthy {
		t.Fatalf("expected status after init to be initialized and healthy, got %#v", status)
	}
	if status.PendingOperations != 0 || status.FailedOperations != 0 {
		t.Fatalf("expected no operations, got pending=%d failed=%d", status.PendingOperations, status.FailedOperations)
	}
}

func TestProviderCLIJSONFlow(t *testing.T) {
	configDir := t.TempDir()
	if _, err := runCLI(t, "--config-dir", configDir, "init", "--json"); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	out, err := runCLI(t,
		"--config-dir", configDir,
		"provider", "create", "provider-b",
		"--name", "Provider B",
		"--adapter", "adapter-b",
		"--disabled",
		"--metadata-json", `{"tier":"paid"}`,
		"--json",
	)
	if err != nil {
		t.Fatalf("expected disabled provider create to succeed, got %v", err)
	}
	var provider app.Provider
	decodeCLIJSON(t, []byte(out), &provider)
	if provider.ID != "provider-b" || provider.Enabled || provider.Metadata["tier"] != "paid" {
		t.Fatalf("unexpected created provider: %#v", provider)
	}

	if _, err := runCLI(t,
		"--config-dir", configDir,
		"provider", "create", "provider-a",
		"--name", "Provider A",
		"--adapter", "adapter-a",
		"--json",
	); err != nil {
		t.Fatalf("expected enabled provider create to succeed, got %v", err)
	}

	out, err = runCLI(t, "--config-dir", configDir, "provider", "list", "--json")
	if err != nil {
		t.Fatalf("expected provider list to succeed, got %v", err)
	}
	var providers []app.Provider
	decodeCLIJSON(t, []byte(out), &providers)
	if providerIDs(providers) != "provider-a" {
		t.Fatalf("expected default list to include only enabled providers, got %#v", providers)
	}

	out, err = runCLI(t, "--config-dir", configDir, "provider", "list", "--all", "--json")
	if err != nil {
		t.Fatalf("expected provider list --all to succeed, got %v", err)
	}
	decodeCLIJSON(t, []byte(out), &providers)
	if providerIDs(providers) != "provider-a,provider-b" {
		t.Fatalf("expected id-sorted providers, got %#v", providers)
	}

	out, err = runCLI(t, "--config-dir", configDir, "provider", "update", "provider-b", "--enabled", "--json")
	if err != nil {
		t.Fatalf("expected provider update to succeed, got %v", err)
	}
	decodeCLIJSON(t, []byte(out), &provider)
	if !provider.Enabled {
		t.Fatalf("expected provider to be enabled after update")
	}

	_, err = runCLI(t, "--config-dir", configDir, "provider", "update", "provider-b", "--enabled", "--disabled", "--json")
	assertCLIAppErrorCode(t, err, app.ErrorProviderInvalid)

	_, err = runCLI(t, "--config-dir", configDir, "provider", "delete", "provider-b", "--json")
	assertCLIAppErrorCode(t, err, app.ErrorConfirmationRequired)

	out, err = runCLI(t, "--config-dir", configDir, "provider", "delete", "provider-b", "--yes", "--json")
	if err != nil {
		t.Fatalf("expected provider delete to succeed, got %v", err)
	}
	var deleted app.DeleteResult
	decodeCLIJSON(t, []byte(out), &deleted)
	if !deleted.Deleted || deleted.ID != "provider-b" {
		t.Fatalf("unexpected delete result: %#v", deleted)
	}
	assertNoTargetToolConfigCreated(t, configDir)
}

func TestProfileCLIJSONFlow(t *testing.T) {
	configDir := t.TempDir()
	if _, err := runCLI(t, "--config-dir", configDir, "init", "--json"); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	if _, err := runCLI(t,
		"--config-dir", configDir,
		"profile", "create", "profile-b",
		"--name", "Profile B",
		"--description", "Second profile",
		"--json",
	); err != nil {
		t.Fatalf("expected profile-b create to succeed, got %v", err)
	}
	out, err := runCLI(t,
		"--config-dir", configDir,
		"profile", "create", "profile-a",
		"--name", "Profile A",
		"--metadata-json", `{"max_tokens":100}`,
		"--json",
	)
	if err != nil {
		t.Fatalf("expected profile-a create to succeed, got %v", err)
	}
	var profile app.Profile
	decodeCLIJSON(t, []byte(out), &profile)
	if profile.ID != "profile-a" || profile.Metadata["max_tokens"] == nil {
		t.Fatalf("unexpected created profile: %#v", profile)
	}

	_, err = runCLI(t, "--config-dir", configDir, "profile", "update", "profile-a", "--json")
	assertCLIAppErrorCode(t, err, app.ErrorProfileInvalid)

	out, err = runCLI(t,
		"--config-dir", configDir,
		"profile", "update", "profile-a",
		"--description", "Updated profile",
		"--metadata-json", `{"mode":"work"}`,
		"--json",
	)
	if err != nil {
		t.Fatalf("expected profile update to succeed, got %v", err)
	}
	decodeCLIJSON(t, []byte(out), &profile)
	if profile.Description != "Updated profile" || profile.Metadata["mode"] != "work" {
		t.Fatalf("unexpected updated profile: %#v", profile)
	}

	out, err = runCLI(t, "--config-dir", configDir, "profile", "list", "--json")
	if err != nil {
		t.Fatalf("expected profile list to succeed, got %v", err)
	}
	var profiles []app.Profile
	decodeCLIJSON(t, []byte(out), &profiles)
	if profileIDs(profiles) != "profile-a,profile-b" {
		t.Fatalf("expected id-sorted profiles, got %#v", profiles)
	}

	out, err = runCLI(t, "--config-dir", configDir, "profile", "delete", "profile-b", "--yes", "--json")
	if err != nil {
		t.Fatalf("expected profile delete to succeed, got %v", err)
	}
	var deleted app.DeleteResult
	decodeCLIJSON(t, []byte(out), &deleted)
	if !deleted.Deleted || deleted.ID != "profile-b" {
		t.Fatalf("unexpected delete result: %#v", deleted)
	}
	assertNoTargetToolConfigCreated(t, configDir)
}

func TestProviderCLIOutputRedactsSensitiveMetadata(t *testing.T) {
	configDir := t.TempDir()
	initOut, err := runCLI(t, "--config-dir", configDir, "init", "--json")
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	var initResult app.InitResult
	decodeCLIJSON(t, []byte(initOut), &initResult)

	sqlDB, err := sql.Open("sqlite", initResult.DatabasePath)
	if err != nil {
		t.Fatalf("expected sqlite open to succeed, got %v", err)
	}
	defer sqlDB.Close()
	_, err = sqlDB.ExecContext(context.Background(), `
		INSERT INTO providers (id, name, adapter_id, metadata_json, created_at_unix_ms, updated_at_unix_ms)
		VALUES ('provider-secret', 'Provider Secret', 'adapter-secret', '{"apiKey":"raw-key","safe":"ok"}', 1, 1)
	`)
	if err != nil {
		t.Fatalf("expected provider setup to succeed, got %v", err)
	}

	jsonOut, err := runCLI(t, "--config-dir", configDir, "provider", "show", "provider-secret", "--json")
	if err != nil {
		t.Fatalf("expected provider show JSON to succeed, got %v", err)
	}
	if strings.Contains(jsonOut, "raw-key") {
		t.Fatalf("expected JSON output to redact raw key, got %q", jsonOut)
	}
	var provider app.Provider
	decodeCLIJSON(t, []byte(jsonOut), &provider)
	if provider.Metadata["apiKey"] != "[REDACTED]" || provider.Metadata["safe"] != "ok" {
		t.Fatalf("unexpected redacted metadata: %#v", provider.Metadata)
	}

	humanOut, err := runCLI(t, "--config-dir", configDir, "provider", "show", "provider-secret")
	if err != nil {
		t.Fatalf("expected provider show to succeed, got %v", err)
	}
	if strings.Contains(humanOut, "raw-key") || !strings.Contains(humanOut, "[REDACTED]") {
		t.Fatalf("expected human output to redact raw key, got %q", humanOut)
	}
}

func decodeCLIJSON(t *testing.T, raw []byte, target any) {
	t.Helper()

	if err := json.Unmarshal(raw, target); err != nil {
		t.Fatalf("expected parseable JSON output, got %q: %v", string(raw), err)
	}
}

func runCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()

	var out bytes.Buffer
	cmd := NewCommand(app.DefaultInfo())
	cmd.Writer = &out

	argv := append([]string{app.CLIName}, args...)
	err := cmd.Run(context.Background(), argv)
	return out.String(), err
}

func assertCLIAppErrorCode(t *testing.T, err error, code app.ErrorCode) {
	t.Helper()

	if err == nil {
		t.Fatalf("expected error code %s, got nil", code)
	}
	var appErr *app.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected AppError code %s, got %T: %v", code, err, err)
	}
	if appErr.Code != code {
		t.Fatalf("expected error code %s, got %s: %v", code, appErr.Code, err)
	}
}

func providerIDs(providers []app.Provider) string {
	ids := make([]string, 0, len(providers))
	for _, provider := range providers {
		ids = append(ids, provider.ID)
	}
	return strings.Join(ids, ",")
}

func profileIDs(profiles []app.Profile) string {
	ids := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		ids = append(ids, profile.ID)
	}
	return strings.Join(ids, ",")
}

func assertNoTargetToolConfigCreated(t *testing.T, configDir string) {
	t.Helper()

	for _, path := range []string{
		filepath.Join(configDir, ".claude"),
		filepath.Join(configDir, ".codex"),
		filepath.Join(configDir, ".gemini"),
		filepath.Join(configDir, "opencode"),
		filepath.Join(configDir, "claude.json"),
		filepath.Join(configDir, "settings.json"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("expected init not to create target tool config path %s, stat error: %v", path, err)
		}
	}
}
