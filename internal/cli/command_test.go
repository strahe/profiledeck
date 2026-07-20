package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	keyring "github.com/zalando/go-keyring"

	"github.com/strahe/profiledeck/internal/antigravity"
	agyconfig "github.com/strahe/profiledeck/internal/antigravity/config"
	"github.com/strahe/profiledeck/internal/app"
	"github.com/strahe/profiledeck/internal/appbackup"
	"github.com/strahe/profiledeck/internal/apperror"
	claudecodeconfig "github.com/strahe/profiledeck/internal/claudecode/config"
	"github.com/strahe/profiledeck/internal/codex"
	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	"github.com/strahe/profiledeck/internal/doctor"
	"github.com/strahe/profiledeck/internal/profile"
	"github.com/strahe/profiledeck/internal/profiletarget"
	"github.com/strahe/profiledeck/internal/provider"
	"github.com/strahe/profiledeck/internal/recoverycleanup"
	profilesruntime "github.com/strahe/profiledeck/internal/runtime"
	"github.com/strahe/profiledeck/internal/store"
	"github.com/strahe/profiledeck/internal/switching"
	"github.com/strahe/profiledeck/internal/usage"
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
	if cmd.Command("backup") == nil {
		t.Fatalf("expected backup subcommand")
	}
	if cmd.Command("codex") == nil {
		t.Fatalf("expected codex subcommand")
	}
	if antigravity := cmd.Command("antigravity"); antigravity == nil || antigravity.Command("detect") == nil || antigravity.Command("profile") == nil ||
		antigravity.Command("profile").Command("delete") == nil {
		t.Fatalf("expected Antigravity commands")
	}
	if claudeCode := cmd.Command("claude-code"); claudeCode == nil || claudeCode.Command("detect") == nil || claudeCode.Command("profile") == nil ||
		claudeCode.Command("profile").Command("create") == nil || claudeCode.Command("profile").Command("list") == nil ||
		claudeCode.Command("profile").Command("show") == nil || claudeCode.Command("profile").Command("update") == nil ||
		claudeCode.Command("profile").Command("save-current") == nil || claudeCode.Command("profile").Command("delete") == nil {
		t.Fatalf("expected Claude Code commands")
	}
	if cmd.Command("claude") != nil {
		t.Fatalf("Claude Code must not register a generic claude alias")
	}
	if codex := cmd.Command("codex"); codex == nil || codex.Command("profile") == nil ||
		codex.Command("profile").Command("list") == nil ||
		codex.Command("profile").Command("show") == nil ||
		codex.Command("profile").Command("create") == nil ||
		codex.Command("profile").Command("fork") == nil ||
		codex.Command("profile").Command("save-current") == nil ||
		codex.Command("profile").Command("set-config") == nil ||
		codex.Command("profile").Command("export") == nil ||
		codex.Command("profile").Command("delete") == nil ||
		codex.Command("profile").Command("import") == nil ||
		codex.Command("profile").Command("import").Command("inspect") == nil ||
		codex.Command("profile").Command("import").Command("apply") == nil ||
		codex.Command("profile").Command("sync") != nil ||
		codex.Command("config-set") == nil {
		t.Fatalf("expected Codex profile and Config Set commands")
	}
	if cmd.Command("doctor") == nil || cmd.Command("doctor").Command("retry-cleanup") == nil {
		t.Fatalf("expected doctor subcommand")
	}
	if cmd.Command("init") == nil {
		t.Fatalf("expected init subcommand")
	}
	if cmd.Command("status") == nil {
		t.Fatalf("expected status subcommand")
	}
	if cmd.Command("switch") == nil {
		t.Fatalf("expected switch subcommand")
	}
	if cmd.Command("usage") == nil {
		t.Fatalf("expected usage subcommand")
	}
	if cmd.Command("plan") == nil {
		t.Fatalf("expected plan subcommand")
	}
	if cmd.Command("provider") == nil {
		t.Fatalf("expected provider subcommand")
	}
	if cmd.Command("profile") == nil {
		t.Fatalf("expected profile subcommand")
	}
	if cmd.Command("recover") == nil {
		t.Fatalf("expected recover subcommand")
	}
	if cmd.Command("rollback") != nil {
		t.Fatalf("rollback command must not be registered")
	}
}

func TestCLICommandsIgnoreDesktopAgentPreferences(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := runCLI(t, "--config-dir", configDir, "init", "--json"); err != nil {
		t.Fatalf("initialize CLI runtime: %v", err)
	}
	application, err := app.New(app.Config{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("create unrestricted Application: %v", err)
	}
	defer application.Close()
	db, err := application.Runtime().StoreFactory().OpenHealthy(ctx, false)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if _, err := db.UpsertSetting(ctx, store.UpsertSettingParams{
		Key: "desktop.agent.codex.enabled", ValueJSON: "false",
	}); err != nil {
		_ = db.Close()
		t.Fatalf("save Desktop Agent preference: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	raw, err := runCLI(
		t, "--config-dir", configDir, "codex", "detect", "--codex-dir", t.TempDir(), "--json",
	)
	if err != nil {
		t.Fatalf("Desktop Agent preference disabled CLI Codex command: %v", err)
	}
	var result codex.CodexDetectResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil || result.ProviderID != codexconfig.ProviderID {
		t.Fatalf("unexpected Codex detect result: raw=%q result=%#v err=%v", raw, result, err)
	}
}

func TestWritePlanUsesSafeTargetLabelWithoutPath(t *testing.T) {
	var output bytes.Buffer
	err := writePlan(&output, switching.SwitchPlan{
		Provider: switching.PlanProvider{ID: agyconfig.ProviderID, Name: "Antigravity"},
		Profile:  switching.PlanProfile{ID: "work", Name: "Work"},
		Operations: []switching.PlanOperation{{
			TargetID: "auth", BackendID: "keyring", TargetLabel: "Antigravity login",
			Action: "update", StatusReason: "target_different_content",
		}},
	})
	if err != nil {
		t.Fatalf("expected plan output to succeed, got %v", err)
	}
	if !strings.Contains(output.String(), "Antigravity login") {
		t.Fatalf("expected safe target label in plan output, got %q", output.String())
	}
}

func TestWriteBackupDetailUsesApplicationBackupMetadata(t *testing.T) {
	var output bytes.Buffer
	err := writeBackupDetail(&output, appbackup.BackupDetail{
		BackupSummary: appbackup.BackupSummary{ID: "manual-test", Kind: appbackup.KindManual},
		Reason:        appbackup.ReasonManual, FormatVersion: 1,
	}, "Application backup")
	if err != nil {
		t.Fatalf("expected backup output to succeed, got %v", err)
	}
	if !strings.Contains(output.String(), "manual-test") || strings.Contains(output.String(), "path") {
		t.Fatalf("unexpected application backup output: %q", output.String())
	}
}

func TestWriteBackupListReportsIncompleteAutomaticCleanup(t *testing.T) {
	var output bytes.Buffer
	err := writeBackupList(&output, appbackup.ListResult{
		Backups:                  []appbackup.BackupSummary{{ID: "auto-test", Kind: appbackup.KindAutomatic}},
		AutomaticCleanupRequired: true,
	})
	if err != nil {
		t.Fatalf("expected backup list output to succeed, got %v", err)
	}
	if !strings.Contains(output.String(), "ProfileDeck kept extra automatic backups") {
		t.Fatalf("backup cleanup warning is missing: %q", output.String())
	}
}

func TestAntigravityProfileCLIUsesAntigravityKeyringState(t *testing.T) {
	keyring.MockInit()
	t.Cleanup(keyring.MockInit)
	configDir := t.TempDir()
	payload := `{"token":{"access_token":"cli-access-secret","token_type":"Bearer","refresh_token":"cli-refresh-secret","expiry":"2026-07-12T04:00:00Z"},"auth_method":"consumer"}`
	if err := keyring.Set(agyconfig.KeyringService, agyconfig.KeyringAccount, payload); err != nil {
		t.Fatalf("expected mock keyring setup to succeed, got %v", err)
	}
	if _, err := runCLI(t, "--config-dir", configDir, "init", "--json"); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	createdRaw, err := runCLI(t, "--config-dir", configDir, "agy", "profile", "create", "work", "--name", "Work", "--json")
	if err != nil {
		t.Fatalf("expected Antigravity profile create to succeed, got %v", err)
	}
	var created antigravity.AntigravityProfileSaveResult
	if err := json.Unmarshal([]byte(createdRaw), &created); err != nil {
		t.Fatalf("expected Antigravity create JSON, got %q: %v", createdRaw, err)
	}
	if created.Summary.Profile.ID != "work" || !created.Summary.Active {
		t.Fatalf("unexpected Antigravity create result: %#v", created)
	}
	listed, err := runCLI(t, "--config-dir", configDir, "antigravity", "profile", "list", "--json")
	if err != nil {
		t.Fatalf("expected Antigravity list to succeed, got %v", err)
	}
	humanList, err := runCLI(t, "--config-dir", configDir, "antigravity", "profile", "list")
	if err != nil {
		t.Fatalf("expected Antigravity human-readable list to succeed, got %v", err)
	}
	humanDetail, err := runCLI(t, "--config-dir", configDir, "antigravity", "profile", "show", "work")
	if err != nil {
		t.Fatalf("expected Antigravity human-readable detail to succeed, got %v", err)
	}
	for _, output := range []string{humanList, humanDetail} {
		if strings.Contains(output, "expires:") {
			t.Fatalf("expected short-lived token expiry to stay out of human-readable login output, got %q", output)
		}
	}
	for _, secret := range []string{"cli-access-secret", "cli-refresh-secret", "access_token", "refresh_token"} {
		if strings.Contains(createdRaw, secret) || strings.Contains(listed, secret) || strings.Contains(humanList, secret) || strings.Contains(humanDetail, secret) {
			t.Fatalf("expected Antigravity output to hide %q", secret)
		}
	}
}

func TestClaudeCodeProfileCLIUsesOfficialLoginWithoutExposingTokens(t *testing.T) {
	configDir := t.TempDir()
	credentialPath := filepath.Join(t.TempDir(), claudecodeconfig.CredentialsFile)
	payload := `{"claudeAiOauth":{"accessToken":"cli-claude-access-secret","refreshToken":"cli-claude-refresh-secret","subscriptionType":"max","expiresAt":4102444800000}}`
	if err := os.WriteFile(credentialPath, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runCLI(t, "--config-dir", configDir, "init", "--json"); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	fingerprint := sha256.Sum256([]byte(claudecodeconfig.StorageFile + "\x00" + credentialPath + "\x00\x00"))
	metadata, err := json.Marshal(map[string]any{
		"preset": claudecodeconfig.PresetName, "preset_version": claudecodeconfig.PresetVersion,
		"storage": claudecodeconfig.StorageFile, "path": credentialPath,
		"locator_fingerprint": fmt.Sprintf("%x", fingerprint),
	})
	if err != nil {
		t.Fatal(err)
	}
	metadataJSON := string(metadata)
	application, err := app.New(app.Config{ConfigDir: configDir})
	if err != nil {
		t.Fatal(err)
	}
	db, err := application.Runtime().StoreFactory().OpenHealthy(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateProvider(context.Background(), store.CreateProviderParams{
		ID: claudecodeconfig.ProviderID, Name: claudecodeconfig.ProviderName, Enabled: true,
		AdapterID: claudecodeconfig.AdapterID, MetadataJSON: metadataJSON,
	}); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	created, err := runCLI(t, "--config-dir", configDir, "claude-code", "profile", "create", "work", "--json")
	if err != nil {
		t.Fatalf("expected Claude Code Profile create to succeed, got %v", err)
	}
	listed, err := runCLI(t, "--config-dir", configDir, "claude-code", "profile", "list", "--json")
	if err != nil {
		t.Fatalf("expected Claude Code Profile list to succeed, got %v", err)
	}
	planned, err := runCLI(t, "--config-dir", configDir, "plan", claudecodeconfig.ProviderID, "work", "--json")
	if err != nil {
		t.Fatalf("expected Claude Code plan to succeed, got %v", err)
	}
	for boundary, output := range map[string]string{"create": created, "list": listed, "plan": planned} {
		for _, secret := range []string{"cli-claude-access-secret", "cli-claude-refresh-secret", "accessToken", "refreshToken"} {
			if strings.Contains(output, secret) {
				t.Fatalf("Claude Code CLI %s output exposed %q: %s", boundary, secret, output)
			}
		}
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

	var result profilesruntime.StatusResult
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
	if _, err := os.Stat(filepath.Join(configDir, "profiledeck", "profiledeck.db")); !os.IsNotExist(err) {
		t.Fatalf("status before init created an application database: %v", err)
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

	var result profilesruntime.InitResult
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
	var secondInit profilesruntime.InitResult
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

	var status profilesruntime.StatusResult
	decodeCLIJSON(t, statusOut.Bytes(), &status)
	if !status.Initialized || !status.SchemaHealthy {
		t.Fatalf("expected status after init to be initialized and healthy, got %#v", status)
	}
	if status.PendingOperations != 0 || status.FailedOperations != 0 {
		t.Fatalf("expected no operations, got pending=%d failed=%d", status.PendingOperations, status.FailedOperations)
	}
}

func TestCLIRejectsUnsupportedSchemaAndKeepsDoctorAvailable(t *testing.T) {
	configDir := t.TempDir()
	if _, err := runCLI(t, "--config-dir", configDir, "init", "--json"); err != nil {
		t.Fatalf("initialize CLI runtime: %v", err)
	}
	databasePath := filepath.Join(configDir, "profiledeck", "profiledeck.db")
	sqlDB, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("open CLI database: %v", err)
	}
	unknownName := "209912310001"
	_, insertErr := sqlDB.Exec(`
		INSERT INTO bun_migrations (name, group_id, migrated_at)
		VALUES (?, 99, CURRENT_TIMESTAMP)
	`, unknownName)
	closeErr := sqlDB.Close()
	if err := errors.Join(insertErr, closeErr); err != nil {
		t.Fatalf("insert unsupported migration: %v", err)
	}

	_, err = runCLI(t, "--config-dir", configDir, "init", "--json")
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != apperror.StoreSchemaUnsupported {
		t.Fatalf("CLI init error = %v, want %s", err, apperror.StoreSchemaUnsupported)
	}
	if strings.Contains(err.Error(), unknownName) {
		t.Fatalf("CLI init exposed migration name: %v", err)
	}

	doctorOutput, err := runCLI(t, "--config-dir", configDir, "doctor")
	if err != nil {
		t.Fatalf("run CLI Doctor after startup rejection: %v", err)
	}
	for _, expected := range []string{"database_schema_unsupported", "update ProfileDeck"} {
		if !strings.Contains(doctorOutput, expected) {
			t.Fatalf("Doctor output missing %q: %q", expected, doctorOutput)
		}
	}
	if strings.Contains(doctorOutput, unknownName) {
		t.Fatalf("CLI Doctor exposed migration name: %q", doctorOutput)
	}
}

func TestUsageSyncCodexAndSummaryJSON(t *testing.T) {
	configDir := t.TempDir()
	codexDir := t.TempDir()
	writeCLIUsageFixture(t, codexDir, strings.Join([]string{
		`{"type":"session_meta","session_id":"session-1"}`,
		`{"type":"turn_context","model":"gpt-5.3-codex"}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":10,"cached_input_tokens":2,"output_tokens":3,"total_tokens":13},"prompt":"SECRET_PROMPT","api_key":"SECRET_KEY"}}}`,
	}, "\n"))

	if _, err := runCLI(t, "--config-dir", configDir, "init", "--json"); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	syncOut, err := runCLI(t, "--config-dir", configDir, "usage", "sync", "codex", "--codex-dir", codexDir, "--json")
	if err != nil {
		t.Fatalf("expected usage sync to succeed, got %v", err)
	}
	for _, forbidden := range []string{"SECRET_PROMPT", "SECRET_KEY", codexDir} {
		if strings.Contains(syncOut, forbidden) {
			t.Fatalf("expected usage sync output to exclude %q, got %q", forbidden, syncOut)
		}
	}
	var syncResult usage.UsageSyncResult
	decodeCLIJSON(t, []byte(syncOut), &syncResult)
	if syncResult.ImportedEvents != 1 || syncResult.ScannedFiles != 1 {
		t.Fatalf("unexpected usage sync result: %#v", syncResult)
	}

	summaryOut, err := runCLI(t, "--config-dir", configDir, "usage", "summary", "--provider", "codex", "--json")
	if err != nil {
		t.Fatalf("expected usage summary to succeed, got %v", err)
	}
	for _, forbidden := range []string{"SECRET_PROMPT", "SECRET_KEY", codexDir} {
		if strings.Contains(summaryOut, forbidden) {
			t.Fatalf("expected usage summary output to exclude %q, got %q", forbidden, summaryOut)
		}
	}
	var summary usage.UsageSummaryResult
	decodeCLIJSON(t, []byte(summaryOut), &summary)
	if summary.EventCount != 1 || summary.InputTokens != 10 || summary.CachedInputTokens != 2 || summary.OutputTokens != 3 || summary.TotalTokens != 13 {
		t.Fatalf("unexpected usage summary: %#v", summary)
	}
	if summary.CostStatus != "estimated" || summary.EstimatedCostUSD == nil {
		t.Fatalf("expected estimated cost summary, got %#v", summary)
	}

	reportOut, err := runCLI(t, "--config-dir", configDir, "usage", "report", "--json")
	if err != nil {
		t.Fatalf("expected default usage report to succeed, got %v", err)
	}
	for _, forbidden := range []string{"SECRET_PROMPT", "SECRET_KEY", codexDir} {
		if strings.Contains(reportOut, forbidden) {
			t.Fatalf("expected usage report output to exclude %q, got %q", forbidden, reportOut)
		}
	}
	var report usage.UsageReportResult
	decodeCLIJSON(t, []byte(reportOut), &report)
	if report.Range.Preset != usage.UsageRange7Days || report.Summary.EventCount != 0 || report.Summary.UndatedEventCount != 1 || len(report.Trend) != 7 {
		t.Fatalf("unexpected default usage report: %#v", report)
	}
	humanReport, err := runCLI(t, "--config-dir", configDir, "usage", "report", "--range", "all")
	if err != nil {
		t.Fatalf("expected human all-time usage report, got %v", err)
	}
	for _, expected := range []string{"Usage report", "Trend", "Models", "gpt-5.3-codex", "API"} {
		if !strings.Contains(humanReport, expected) {
			t.Fatalf("expected human report to contain %q, got %q", expected, humanReport)
		}
	}
	if _, err := runCLI(t, "--config-dir", configDir, "usage", "report", "--range", "14d"); err == nil {
		t.Fatalf("expected invalid usage report range to fail")
	}
}

func TestUsageSyncCodexDefaultsToCodexHomeEnv(t *testing.T) {
	configDir := t.TempDir()
	codexDir := t.TempDir()
	t.Setenv("CODEX_HOME", codexDir)
	writeCLIUsageFixture(t, codexDir, strings.Join([]string{
		`{"type":"session_meta","session_id":"session-env"}`,
		`{"type":"turn_context","model":"gpt-5.3-codex"}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":5,"output_tokens":2,"total_tokens":7}}}}`,
	}, "\n"))

	if _, err := runCLI(t, "--config-dir", configDir, "init", "--json"); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	out, err := runCLI(t, "--config-dir", configDir, "usage", "sync", "codex", "--json")
	if err != nil {
		t.Fatalf("expected usage sync with CODEX_HOME to succeed, got %v", err)
	}
	var result usage.UsageSyncResult
	decodeCLIJSON(t, []byte(out), &result)
	if result.ScannedFiles != 1 || result.ImportedEvents != 1 {
		t.Fatalf("expected CODEX_HOME usage file to be imported, got %#v", result)
	}
}

func TestUsageBucketLabelDistinguishesRepeatedDSTHour(t *testing.T) {
	location, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Skipf("timezone data unavailable: %v", err)
	}
	resolved := usage.UsageResolvedRange{BucketUnit: "hour", TimeZone: location.String()}
	first := usageBucketLabel(resolved, time.Date(2026, time.November, 1, 8, 30, 0, 0, time.UTC).UnixMilli())
	second := usageBucketLabel(resolved, time.Date(2026, time.November, 1, 9, 30, 0, 0, time.UTC).UnixMilli())
	if first == second || !strings.Contains(first, "PDT") || !strings.Contains(second, "PST") {
		t.Fatalf("expected repeated hour labels to include distinct zones, first=%q second=%q", first, second)
	}
}

func TestCodexDetectAndProfileCreatePlanSwitchJSON(t *testing.T) {
	configDir := t.TempDir()
	codexDir := t.TempDir()
	configPath := filepath.Join(codexDir, codexconfig.ConfigFileName)
	authPath := filepath.Join(codexDir, codexconfig.AuthFileName)
	desiredConfig := strings.Join([]string{
		`model = "gpt-5.3-codex"`,
		`model_provider = "openai"`,
		`approval_policy = "never"`,
		`api_key = "raw-secret"`,
		``,
	}, "\n")
	if err := os.WriteFile(configPath, []byte(desiredConfig), 0o600); err != nil {
		t.Fatalf("expected Codex config setup to succeed, got %v", err)
	}
	if err := os.WriteFile(authPath, []byte(`{"tokens":{"account_id":"remote-work","access_token":"auth-secret"}}`), 0o600); err != nil {
		t.Fatalf("expected Codex auth setup to succeed, got %v", err)
	}

	out, err := runCLI(t, "--config-dir", configDir, "codex", "detect", "--codex-dir", codexDir, "--json")
	if err != nil {
		t.Fatalf("expected codex detect before init to succeed, got %v", err)
	}
	var detect codex.CodexDetectResult
	decodeCLIJSON(t, []byte(out), &detect)
	if detect.ProfileDeckInitialized || detect.ConfigStatus != "valid" {
		t.Fatalf("unexpected detect result before init: %#v", detect)
	}

	if _, err := runCLI(t, "--config-dir", configDir, "init", "--json"); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	out, err = runCLI(t,
		"--config-dir", configDir,
		"codex", "profile", "create",
		"work",
		"--codex-dir", codexDir,
		"--name", "Work",
		"--json",
	)
	if err != nil {
		t.Fatalf("expected codex profile create to succeed, got %v", err)
	}
	var createResult codex.CodexProfileSaveResult
	decodeCLIJSON(t, []byte(out), &createResult)
	if createResult.Provider.ID != codexconfig.ProviderID || createResult.Profile.ID != "work" || createResult.ConfigSet.ID != "shared" {
		t.Fatalf("unexpected codex profile create result: %#v", createResult)
	}
	if err := os.WriteFile(configPath, []byte(`model = "old-model"`+"\n"+`api_key = "live-secret"`+"\n"), 0o600); err != nil {
		t.Fatalf("expected live config mutation to succeed, got %v", err)
	}

	out, err = runCLI(t, "--config-dir", configDir, "plan", codexconfig.ProviderID, "work", "--json")
	if err != nil {
		t.Fatalf("expected codex plan to succeed, got %v", err)
	}
	var plan switching.SwitchPlan
	decodeCLIJSON(t, []byte(out), &plan)
	if len(plan.Operations) != 2 || len(plan.StateCaptures) != 1 || !hasCLIPlanOperation(plan.Operations, codexconfig.TargetID, "noop") {
		t.Fatalf("unexpected codex plan: %#v", plan)
	}
	if strings.Contains(out, "raw-secret") || strings.Contains(out, "live-secret") {
		t.Fatalf("expected codex plan JSON to redact existing secret-looking values, got %q", out)
	}

	out, err = runCLI(t, "--config-dir", configDir, "switch", codexconfig.ProviderID, "work", "--yes", "--json")
	if err != nil {
		t.Fatalf("expected codex switch to succeed, got %v", err)
	}
	var switchResult switching.ApplySwitchResult
	decodeCLIJSON(t, []byte(out), &switchResult)
	if switchResult.Status != store.OperationStatusApplied || switchResult.Counts.Noop != 2 {
		t.Fatalf("unexpected codex switch result: %#v", switchResult)
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("expected Codex config read to succeed, got %v", err)
	}
	if !strings.Contains(string(raw), `model = "old-model"`) {
		t.Fatalf("expected active working copy to be checked in and retained, got %q", string(raw))
	}
}

func TestCodexProfileCreateCLIJSONRedactsAuth(t *testing.T) {
	configDir := t.TempDir()
	codexDir := t.TempDir()
	configPath := filepath.Join(codexDir, codexconfig.ConfigFileName)
	authPath := filepath.Join(codexDir, codexconfig.AuthFileName)
	desiredConfig := `model = "gpt-5.3-codex"` + "\n"
	desiredAuth := `{"tokens":{"account_id":"Team/Shared","access_token":"desired-secret","refresh_token":"desired-refresh"}}`
	if err := os.WriteFile(configPath, []byte(desiredConfig), 0o600); err != nil {
		t.Fatalf("expected Codex config setup to succeed, got %v", err)
	}
	if err := os.WriteFile(authPath, []byte(desiredAuth), 0o600); err != nil {
		t.Fatalf("expected Codex auth setup to succeed, got %v", err)
	}
	if _, err := runCLI(t, "--config-dir", configDir, "init", "--json"); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	out, err := runCLI(t,
		"--config-dir", configDir,
		"codex", "profile", "create", "work",
		"--codex-dir", codexDir,
		"--json",
	)
	if err != nil {
		t.Fatalf("expected codex profile create to succeed, got %v", err)
	}
	if strings.Contains(out, "desired-secret") || strings.Contains(out, "desired-refresh") {
		t.Fatalf("expected create output to redact raw auth, got %q", out)
	}
	var created codex.CodexProfileSaveResult
	decodeCLIJSON(t, []byte(out), &created)
	if created.Profile.ID != "work" || created.Summary.CredentialID == "" || created.ConfigSet.ID != "shared" {
		t.Fatalf("unexpected create result: %#v", created)
	}

	_, err = runCLI(t, "--config-dir", configDir, "codex", "account", "list", "--json")
	if err == nil {
		t.Fatalf("expected removed account command to fail")
	}

	if err := os.WriteFile(configPath, []byte(`model = "other"`+"\n"), 0o600); err != nil {
		t.Fatalf("expected config mutation to succeed, got %v", err)
	}
	if err := os.WriteFile(authPath, []byte(`{"unknown_auth_shape":"live-secret"}`), 0o600); err != nil {
		t.Fatalf("expected auth mutation to succeed, got %v", err)
	}
	out, err = runCLI(t, "--config-dir", configDir, "plan", codexconfig.ProviderID, "work", "--json")
	if err != nil {
		t.Fatalf("expected codex plan to succeed, got %v", err)
	}
	if strings.Contains(out, "desired-secret") || strings.Contains(out, "live-secret") {
		t.Fatalf("expected plan output to redact raw auth, got %q", out)
	}
	out, err = runCLI(t, "--config-dir", configDir, "switch", codexconfig.ProviderID, "work", "--yes", "--json")
	if err != nil {
		t.Fatalf("expected codex switch to succeed, got %v", err)
	}
	if strings.Contains(out, "desired-secret") || strings.Contains(out, "live-secret") {
		t.Fatalf("expected switch output to redact raw auth, got %q", out)
	}
	if got := string(mustReadFile(t, authPath)); got != desiredAuth {
		t.Fatalf("expected switch to restore created auth, got %q", got)
	}
	if got := string(mustReadFile(t, configPath)); !strings.Contains(got, `model = "other"`) {
		t.Fatalf("expected valid config working copy to be retained, got %q", got)
	}
}

func TestCodexProfileCreateRejectsExtraArgs(t *testing.T) {
	configDir := t.TempDir()
	codexDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(codexDir, codexconfig.ConfigFileName), []byte(`model = "gpt-5.3-codex"`+"\n"), 0o600); err != nil {
		t.Fatalf("expected Codex config setup to succeed, got %v", err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, codexconfig.AuthFileName), []byte(`{"tokens":{"account_id":"auth-account","access_token":"desired-secret"}}`), 0o600); err != nil {
		t.Fatalf("expected Codex auth setup to succeed, got %v", err)
	}
	if _, err := runCLI(t, "--config-dir", configDir, "init", "--json"); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	_, err := runCLI(t,
		"--config-dir", configDir,
		"codex", "profile", "create", "team-zhu",
		"--codex-dir", codexDir,
		"--json",
	)
	if err != nil {
		t.Fatalf("expected codex profile create to succeed, got %v", err)
	}

	_, err = runCLI(t,
		"--config-dir", configDir,
		"codex", "profile", "create", "team-zhu", "team-zhu",
		"--codex-dir", codexDir,
		"--json",
	)
	assertCLIAppErrorCode(t, err, apperror.ProfileInvalid)
}

func TestCodexProfileCreateForkAndSaveCurrentCLI(t *testing.T) {
	configDir := t.TempDir()
	codexDir := t.TempDir()
	writeCLICodexProfileFixture(t, codexDir, `model = "gpt-5.3-codex"`+"\n", `{"tokens":{"account_id":"same-account","access_token":"token-1"}}`)
	if _, err := runCLI(t, "--config-dir", configDir, "init", "--json"); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	out, err := runCLI(t,
		"--config-dir", configDir,
		"codex", "profile", "create", "work",
		"--codex-dir", codexDir,
		"--name", "Work",
		"--json",
	)
	if err != nil {
		t.Fatalf("expected codex profile create to succeed, got %v", err)
	}
	var created codex.CodexProfileSaveResult
	decodeCLIJSON(t, []byte(out), &created)
	if created.Profile.ID != "work" || created.Summary.CredentialID == "" {
		t.Fatalf("unexpected create result: %#v", created)
	}
	if strings.Contains(out, "token-1") {
		t.Fatalf("expected create output to redact auth payload, got %q", out)
	}

	out, err = runCLI(t,
		"--config-dir", configDir,
		"codex", "profile", "fork", "work", "shared-child",
		"--codex-dir", codexDir,
		"--credential-binding", codex.CodexForkBindingShareParent,
		"--config-binding", codex.CodexForkBindingCopyNew,
		"--new-config-set", "shared-child-config",
		"--json",
	)
	if err != nil {
		t.Fatalf("expected codex profile fork to succeed, got %v", err)
	}
	var forked codex.CodexProfileSaveResult
	decodeCLIJSON(t, []byte(out), &forked)
	if forked.Profile.ID != "shared-child" {
		t.Fatalf("unexpected fork result: %#v", forked)
	}

	writeCLICodexProfileFixture(t, codexDir, `model = "gpt-5-mini"`+"\n", `{"tokens":{"account_id":"same-account","access_token":"token-2"}}`)
	out, err = runCLI(t,
		"--config-dir", configDir,
		"codex", "profile", "save-current",
		"--codex-dir", codexDir,
		"--json",
	)
	if err != nil {
		t.Fatalf("expected codex profile save-current to succeed, got %v", err)
	}
	var saved codex.CodexProfileStateSaveResult
	decodeCLIJSON(t, []byte(out), &saved)
	if saved.ProfileID != "work" {
		t.Fatalf("unexpected save-current result: %#v", saved)
	}
	if strings.Contains(out, "token-2") {
		t.Fatalf("expected save-current output to redact auth payload, got %q", out)
	}

	if _, err := runCLI(t, "--config-dir", configDir, "switch", codexconfig.ProviderID, "shared-child", "--yes", "--json"); err != nil {
		t.Fatalf("expected shared-child switch to succeed, got %v", err)
	}
	if got := string(mustReadFile(t, filepath.Join(codexDir, codexconfig.AuthFileName))); !strings.Contains(got, "token-2") {
		t.Fatalf("expected shared credential update to affect fork, got %q", got)
	}
}

func TestCodexProfileExportImportCLI(t *testing.T) {
	sourceConfigDir := t.TempDir()
	codexDir := t.TempDir()
	rawAuthSecret := "private-auth-test-value"
	rawConfigSecret := "private-config-test-value"
	writeCLICodexProfileFixture(t, codexDir,
		"model = \"gpt-test\"\napi_key = \""+rawConfigSecret+"\"\n",
		`{"tokens":{"account_id":"test-account","access_token":"`+rawAuthSecret+`"}}`,
	)
	if _, err := runCLI(t, "--config-dir", sourceConfigDir, "init", "--json"); err != nil {
		t.Fatal(err)
	}
	if _, err := runCLI(t, "--config-dir", sourceConfigDir, "codex", "profile", "create", "work", "--codex-dir", codexDir, "--json"); err != nil {
		t.Fatal(err)
	}

	bundlePath := filepath.Join(t.TempDir(), "profiles.json")
	out, err := runCLI(t, "--config-dir", sourceConfigDir, "codex", "profile", "export", "--output", bundlePath, "--json")
	if err != nil {
		t.Fatalf("expected CLI export to succeed, got %v", err)
	}
	if strings.Contains(out, rawAuthSecret) || strings.Contains(out, rawConfigSecret) {
		t.Fatalf("expected export command output to contain metadata only, got %q", out)
	}
	var exported codex.CodexProfileExportResult
	decodeCLIJSON(t, []byte(out), &exported)
	if exported.Path != bundlePath || exported.ProfileCount != 1 || exported.SHA256 == "" {
		t.Fatalf("unexpected export result: %#v", exported)
	}
	bundleRaw := string(mustReadFile(t, bundlePath))
	if !strings.Contains(bundleRaw, rawAuthSecret) || !strings.Contains(bundleRaw, rawConfigSecret) {
		t.Fatalf("expected explicitly sensitive bundle to preserve auth and config payloads")
	}
	if _, err := runCLI(t, "--config-dir", sourceConfigDir, "codex", "profile", "export", "--output", bundlePath, "--json"); err == nil {
		t.Fatalf("expected export overwrite to require --force")
	}

	targetConfigDir := t.TempDir()
	if _, err := runCLI(t, "--config-dir", targetConfigDir, "init", "--json"); err != nil {
		t.Fatal(err)
	}
	out, err = runCLI(t, "--config-dir", targetConfigDir, "codex", "profile", "import", "inspect", bundlePath, "--codex-dir", codexDir, "--json")
	if err != nil {
		t.Fatalf("expected CLI import inspection to succeed, got %v", err)
	}
	if strings.Contains(out, rawAuthSecret) || strings.Contains(out, rawConfigSecret) {
		t.Fatalf("expected import plan output to contain metadata only, got %q", out)
	}
	var plan codex.CodexProfileImportPlan
	decodeCLIJSON(t, []byte(out), &plan)
	if !plan.CanApply || plan.PlanFingerprint == "" {
		t.Fatalf("unexpected import plan: %#v", plan)
	}
	_, err = runCLI(t, "--config-dir", targetConfigDir, "codex", "profile", "import", "apply", bundlePath, "--codex-dir", codexDir, "--plan-fingerprint", plan.PlanFingerprint, "--json")
	assertCLIAppErrorCode(t, err, apperror.ConfirmationRequired)
	out, err = runCLI(t, "--config-dir", targetConfigDir, "codex", "profile", "import", "apply", bundlePath, "--codex-dir", codexDir, "--plan-fingerprint", plan.PlanFingerprint, "--yes", "--json")
	if err != nil {
		t.Fatalf("expected CLI import apply to succeed, got %v", err)
	}
	if strings.Contains(out, rawAuthSecret) || strings.Contains(out, rawConfigSecret) {
		t.Fatalf("expected import result output to contain metadata only, got %q", out)
	}
	var imported codex.CodexProfileImportResult
	decodeCLIJSON(t, []byte(out), &imported)
	if !imported.Changed || imported.OperationID == "" {
		t.Fatalf("unexpected import result: %#v", imported)
	}
	listOut, err := runCLI(t, "--config-dir", targetConfigDir, "codex", "profile", "list", "--json")
	if err != nil || !strings.Contains(listOut, `"id":"work"`) {
		t.Fatalf("expected imported profile in target store, output=%q err=%v", listOut, err)
	}
}

func TestCodexConfigSetAndProfileBindingCLI(t *testing.T) {
	configDir := t.TempDir()
	codexDir := t.TempDir()
	writeCLICodexProfileFixture(t, codexDir, "model = \"gpt-5\"\napi_key = \"config-secret\"\n", `{"tokens":{"account_id":"work","access_token":"auth-secret"}}`)
	if _, err := runCLI(t, "--config-dir", configDir, "init", "--json"); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	if _, err := runCLI(t, "--config-dir", configDir, "codex", "profile", "create", "work", "--codex-dir", codexDir, "--json"); err != nil {
		t.Fatalf("expected profile create to succeed, got %v", err)
	}
	out, err := runCLI(t, "--config-dir", configDir, "codex", "config-set", "create", "other", "--codex-dir", codexDir, "--name", "Other", "--json")
	if err != nil {
		t.Fatalf("expected Config Set create to succeed, got %v", err)
	}
	if strings.Contains(out, "config-secret") {
		t.Fatalf("expected Config Set output not to expose raw TOML, got %q", out)
	}
	if _, err := runCLI(t, "--config-dir", configDir, "codex", "profile", "fork", "work", "child", "--codex-dir", codexDir, "--credential-binding", codex.CodexForkBindingCopyNew, "--config-binding", codex.CodexForkBindingShareParent, "--json"); err != nil {
		t.Fatalf("expected child fork to succeed, got %v", err)
	}
	out, err = runCLI(t, "--config-dir", configDir, "codex", "profile", "set-config", "child", "other", "--json")
	if err != nil {
		t.Fatalf("expected inactive profile rebind to succeed, got %v", err)
	}
	var detail codex.CodexProfileDetail
	decodeCLIJSON(t, []byte(out), &detail)
	if detail.Summary.ConfigSetID != "other" {
		t.Fatalf("expected child to use other Config Set, got %#v", detail.Summary)
	}
	if _, err := runCLI(t, "--config-dir", configDir, "codex", "config-set", "copy", "other", "temporary", "--name", "Temporary", "--json"); err != nil {
		t.Fatalf("expected Config Set copy to succeed, got %v", err)
	}
	if _, err := runCLI(t, "--config-dir", configDir, "codex", "config-set", "update", "temporary", "--name", "Renamed", "--json"); err != nil {
		t.Fatalf("expected Config Set update to succeed, got %v", err)
	}
	out, err = runCLI(t, "--config-dir", configDir, "codex", "config-set", "show", "temporary", "--json")
	if err != nil {
		t.Fatalf("expected Config Set show to succeed, got %v", err)
	}
	var shown codex.CodexConfigSet
	decodeCLIJSON(t, []byte(out), &shown)
	if shown.Name != "Renamed" || strings.Contains(out, "config-secret") {
		t.Fatalf("unexpected Config Set summary output: %q", out)
	}
	if _, err := runCLI(t, "--config-dir", configDir, "codex", "config-set", "delete", "temporary", "--yes", "--json"); err != nil {
		t.Fatalf("expected unreferenced Config Set delete to succeed, got %v", err)
	}
}

func TestCodexProfileListAndShowCLI(t *testing.T) {
	configDir := t.TempDir()
	codexDir := t.TempDir()
	configPath := filepath.Join(codexDir, codexconfig.ConfigFileName)
	authPath := filepath.Join(codexDir, codexconfig.AuthFileName)
	rawConfigSecret := "config-secret"
	rawAuthSecret := "auth-secret"
	if err := os.WriteFile(configPath, []byte(`model = "gpt-5-codex"`+"\n"+`api_key = "`+rawConfigSecret+`"`+"\n"), 0o600); err != nil {
		t.Fatalf("expected Codex config setup to succeed, got %v", err)
	}
	if err := os.WriteFile(authPath, []byte(`{"tokens":{"account_id":"remote-work","access_token":"`+rawAuthSecret+`"}}`), 0o600); err != nil {
		t.Fatalf("expected Codex auth setup to succeed, got %v", err)
	}
	if _, err := runCLI(t, "--config-dir", configDir, "init", "--json"); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	if _, err := runCLI(t,
		"--config-dir", configDir,
		"codex", "profile", "create", "work",
		"--codex-dir", codexDir,
		"--json",
	); err != nil {
		t.Fatalf("expected codex profile create to succeed, got %v", err)
	}
	out, err := runCLI(t, "--config-dir", configDir, "codex", "profile", "list", "--json")
	if err != nil {
		t.Fatalf("expected codex profile list to succeed, got %v", err)
	}
	if strings.Contains(out, rawConfigSecret) || strings.Contains(out, rawAuthSecret) {
		t.Fatalf("expected codex profile list output to redact raw secrets, got %q", out)
	}
	var list codex.CodexProfileListResult
	decodeCLIJSON(t, []byte(out), &list)
	if len(list.Profiles) != 1 {
		t.Fatalf("expected one Codex profile, got %#v", list)
	}
	byID := map[string]codex.CodexProfileSummary{}
	for _, summary := range list.Profiles {
		byID[summary.Profile.ID] = summary
	}
	if byID["work"].CodexAccountID != "remote-work" {
		t.Fatalf("unexpected created profile summary: %#v", byID["work"])
	}

	humanOut, err := runCLI(t, "--config-dir", configDir, "codex", "profile", "list")
	if err != nil {
		t.Fatalf("expected codex profile human list to succeed, got %v", err)
	}
	if !strings.Contains(humanOut, "Codex profiles") || strings.Contains(humanOut, rawConfigSecret) || strings.Contains(humanOut, rawAuthSecret) {
		t.Fatalf("unexpected codex profile human list output: %q", humanOut)
	}

	out, err = runCLI(t, "--config-dir", configDir, "codex", "profile", "show", "work", "--json")
	if err != nil {
		t.Fatalf("expected codex profile show to succeed, got %v", err)
	}
	if strings.Contains(out, rawConfigSecret) || strings.Contains(out, rawAuthSecret) || strings.Contains(out, "access_token") {
		t.Fatalf("expected codex profile show output to redact raw secrets, got %q", out)
	}
	var detail codex.CodexProfileDetail
	decodeCLIJSON(t, []byte(out), &detail)
	if detail.Summary.Profile.ID != "work" || detail.Login == nil || detail.ConfigSet == nil {
		t.Fatalf("unexpected codex profile detail: %#v", detail)
	}
	humanOut, err = runCLI(t, "--config-dir", configDir, "codex", "profile", "show", "work")
	if err != nil {
		t.Fatalf("expected codex profile human show to succeed, got %v", err)
	}
	if !strings.Contains(humanOut, "Codex profile") || strings.Contains(humanOut, rawConfigSecret) || strings.Contains(humanOut, rawAuthSecret) {
		t.Fatalf("unexpected codex profile human show output: %q", humanOut)
	}

	if _, err := runCLI(t, "--config-dir", configDir, "profile", "create", "generic", "--name", "Generic", "--json"); err != nil {
		t.Fatalf("expected generic profile create to succeed, got %v", err)
	}
	_, err = runCLI(t, "--config-dir", configDir, "codex", "profile", "show", "generic", "--json")
	assertCLIAppErrorCode(t, err, apperror.ProfileNotFound)
}

func TestCodexProfileRemovedCommandsFail(t *testing.T) {
	profile := NewCommand(app.DefaultInfo()).Command("codex").Command("profile")
	for _, command := range []string{"capture", "set"} {
		if profile.Command(command) != nil {
			t.Fatalf("expected codex profile %s command to be removed", command)
		}
	}
}

func TestUsageSyncCodexHumanOutputReportsOversizedLinesWithoutBlockingFile(t *testing.T) {
	configDir := t.TempDir()
	codexDir := t.TempDir()
	writeCLIUsageFixture(t, codexDir, strings.Repeat("x", 16*1024*1024+1))

	if _, err := runCLI(t, "--config-dir", configDir, "init", "--json"); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	out, err := runCLI(t, "--config-dir", configDir, "usage", "sync", "codex", "--codex-dir", codexDir)
	if err != nil {
		t.Fatalf("expected usage sync to succeed with invalid line accounting, got %v", err)
	}
	for _, want := range []string{"invalid lines: 1", "errors: 0"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected usage sync output to contain %q, got %q", want, out)
		}
	}
	for _, forbidden := range []string{"error details:", "token too long"} {
		if strings.Contains(out, forbidden) {
			t.Fatalf("expected usage sync output to exclude %q, got %q", forbidden, out)
		}
	}
	if strings.Contains(out, codexDir) {
		t.Fatalf("expected usage sync output to exclude full codex dir, got %q", out)
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
	var createdProvider provider.Provider
	decodeCLIJSON(t, []byte(out), &createdProvider)
	if createdProvider.ID != "provider-b" || createdProvider.Enabled || createdProvider.Metadata["tier"] != "paid" {
		t.Fatalf("unexpected created provider: %#v", createdProvider)
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
	var providers []provider.Provider
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
	decodeCLIJSON(t, []byte(out), &createdProvider)
	if !createdProvider.Enabled {
		t.Fatalf("expected provider to be enabled after update")
	}

	_, err = runCLI(t, "--config-dir", configDir, "provider", "update", "provider-b", "--enabled", "--disabled", "--json")
	assertCLIAppErrorCode(t, err, apperror.ProviderInvalid)

	_, err = runCLI(t, "--config-dir", configDir, "provider", "delete", "provider-b", "--json")
	assertCLIAppErrorCode(t, err, apperror.ConfirmationRequired)

	out, err = runCLI(t, "--config-dir", configDir, "provider", "delete", "provider-b", "--yes", "--json")
	if err != nil {
		t.Fatalf("expected provider delete to succeed, got %v", err)
	}
	var deleted provider.DeleteResult
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
	var createdProfile profile.Profile
	decodeCLIJSON(t, []byte(out), &createdProfile)
	if createdProfile.ID != "profile-a" || createdProfile.Metadata["max_tokens"] == nil {
		t.Fatalf("unexpected created profile: %#v", createdProfile)
	}

	_, err = runCLI(t, "--config-dir", configDir, "profile", "update", "profile-a", "--json")
	assertCLIAppErrorCode(t, err, apperror.ProfileInvalid)

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
	decodeCLIJSON(t, []byte(out), &createdProfile)
	if createdProfile.Description != "Updated profile" || createdProfile.Metadata["mode"] != "work" {
		t.Fatalf("unexpected updated profile: %#v", createdProfile)
	}

	out, err = runCLI(t, "--config-dir", configDir, "profile", "list", "--json")
	if err != nil {
		t.Fatalf("expected profile list to succeed, got %v", err)
	}
	var profiles []profile.Profile
	decodeCLIJSON(t, []byte(out), &profiles)
	if profileIDs(profiles) != "profile-a,profile-b" {
		t.Fatalf("expected id-sorted profiles, got %#v", profiles)
	}

	out, err = runCLI(t, "--config-dir", configDir, "profile", "delete", "profile-b", "--yes", "--json")
	if err != nil {
		t.Fatalf("expected profile delete to succeed, got %v", err)
	}
	var deleted profile.DeleteResult
	decodeCLIJSON(t, []byte(out), &deleted)
	if !deleted.Deleted || deleted.ID != "profile-b" {
		t.Fatalf("unexpected delete result: %#v", deleted)
	}
	assertNoTargetToolConfigCreated(t, configDir)
}

func TestAgentProfileDeleteCommandsDeleteTheGlobalProfile(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	if _, err := runCLI(t, "--config-dir", configDir, "init", "--json"); err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}
	for _, profileID := range []string{"from-codex", "from-antigravity", "from-claude"} {
		if _, err := runCLI(t, "--config-dir", configDir, "profile", "create", profileID, "--name", profileID, "--json"); err != nil {
			t.Fatalf("create global Profile %q: %v", profileID, err)
		}
	}

	application, err := app.New(app.Config{ConfigDir: configDir})
	if err != nil {
		t.Fatalf("create application: %v", err)
	}
	db, err := application.Runtime().StoreFactory().OpenHealthy(ctx, false)
	if err != nil {
		application.Close()
		t.Fatalf("open store: %v", err)
	}
	if _, err := db.CreateProvider(ctx, store.CreateProviderParams{
		ID: claudecodeconfig.ProviderID, Name: claudecodeconfig.ProviderName,
		AdapterID: claudecodeconfig.AdapterID, Enabled: true, MetadataJSON: `{}`,
	}); err != nil {
		_ = db.Close()
		application.Close()
		t.Fatalf("create Claude Code Provider: %v", err)
	}
	payload := `{"claudeAiOauth":{"accessToken":"private"}}`
	if _, err := db.UpsertProviderCredential(ctx, store.UpsertProviderCredentialParams{
		ID: "claude-only-login", ProviderID: claudecodeconfig.ProviderID, CredentialKind: claudecodeconfig.CredentialKind,
		PayloadJSON: payload, PayloadSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(payload))), MetadataJSON: `{}`,
	}); err != nil {
		_ = db.Close()
		application.Close()
		t.Fatalf("create Claude Code credential: %v", err)
	}
	if _, err := db.UpsertProfileCredentialBinding(ctx, store.UpsertProfileCredentialBindingParams{
		ProfileID: "from-codex", ProviderID: claudecodeconfig.ProviderID,
		SlotID: claudecodeconfig.CredentialSlot, CredentialID: "claude-only-login",
	}); err != nil {
		_ = db.Close()
		application.Close()
		t.Fatalf("bind Claude Code-only data: %v", err)
	}
	if err := db.Close(); err != nil {
		application.Close()
		t.Fatalf("close store: %v", err)
	}
	application.Close()

	_, err = runCLI(t, "--config-dir", configDir, "codex", "profile", "delete", "from-codex", "--json")
	assertCLIAppErrorCode(t, err, apperror.ConfirmationRequired)
	for _, test := range []struct {
		agent     string
		profileID string
	}{
		{agent: "codex", profileID: "from-codex"},
		{agent: "antigravity", profileID: "from-antigravity"},
		{agent: "claude-code", profileID: "from-claude"},
	} {
		out, err := runCLI(t, "--config-dir", configDir, test.agent, "profile", "delete", test.profileID, "--yes", "--json")
		if err != nil {
			t.Fatalf("%s Profile delete: %v", test.agent, err)
		}
		var result profile.DeleteResult
		decodeCLIJSON(t, []byte(out), &result)
		if !result.Deleted || result.ID != test.profileID {
			t.Fatalf("%s delete result = %#v", test.agent, result)
		}
	}
	if _, err := runCLI(t, "--config-dir", configDir, "profile", "show", "from-codex", "--json"); err == nil {
		t.Fatal("Codex alias left the global Profile behind")
	}
	if _, err := runCLI(t, "--config-dir", configDir, "profile", "create", "human", "--name", "Human", "--json"); err != nil {
		t.Fatalf("create human-output Profile: %v", err)
	}
	if out, err := runCLI(t, "--config-dir", configDir, "codex", "profile", "delete", "human", "--yes"); err != nil || out != "Deleted profile human\n" {
		t.Fatalf("human delete output = %q, err=%v", out, err)
	}
	assertNoTargetToolConfigCreated(t, configDir)
}

func TestProfileTargetAndPlanCLIFlow(t *testing.T) {
	configDir := t.TempDir()
	if _, err := runCLI(t, "--config-dir", configDir, "init", "--json"); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	if _, err := runCLI(t,
		"--config-dir", configDir,
		"provider", "create", "provider-a",
		"--name", "Provider A",
		"--adapter", "generic",
		"--json",
	); err != nil {
		t.Fatalf("expected provider create to succeed, got %v", err)
	}
	if _, err := runCLI(t,
		"--config-dir", configDir,
		"profile", "create", "profile-a",
		"--name", "Profile A",
		"--json",
	); err != nil {
		t.Fatalf("expected profile create to succeed, got %v", err)
	}

	targetPath := filepath.Join(t.TempDir(), "target.env")
	out, err := runCLI(t,
		"--config-dir", configDir,
		"profile", "target", "add", "profile-a", "target-a",
		"--provider", "provider-a",
		"--path", targetPath,
		"--format", "env",
		"--strategy", "replace-file",
		"--value-json", `{"content":"OPENAI_API_KEY=raw-key\nsafe=value"}`,
		"--json",
	)
	if err != nil {
		t.Fatalf("expected profile target add to succeed, got %v", err)
	}
	if strings.Contains(out, "raw-key") || !strings.Contains(out, "[REDACTED]") {
		t.Fatalf("expected target add JSON to redact raw key, got %q", out)
	}
	var target profiletarget.ProfileTarget
	decodeCLIJSON(t, []byte(out), &target)
	if target.TargetID != "target-a" || target.ValuePreview.Content == "" {
		t.Fatalf("unexpected target add result: %#v", target)
	}

	out, err = runCLI(t, "--config-dir", configDir, "profile", "target", "list", "profile-a", "--provider", "provider-a", "--json")
	if err != nil {
		t.Fatalf("expected profile target list to succeed, got %v", err)
	}
	if strings.Contains(out, "raw-key") {
		t.Fatalf("expected target list JSON to redact raw key, got %q", out)
	}
	var targets []profiletarget.ProfileTarget
	decodeCLIJSON(t, []byte(out), &targets)
	if len(targets) != 1 || targets[0].TargetID != "target-a" {
		t.Fatalf("unexpected target list result: %#v", targets)
	}

	humanOut, err := runCLI(t, "--config-dir", configDir, "profile", "target", "show", "profile-a", "provider-a", "target-a")
	if err != nil {
		t.Fatalf("expected profile target show to succeed, got %v", err)
	}
	if strings.Contains(humanOut, "raw-key") || !strings.Contains(humanOut, "[REDACTED]") {
		t.Fatalf("expected target show human output to redact raw key, got %q", humanOut)
	}

	out, err = runCLI(t, "--config-dir", configDir, "plan", "provider-a", "profile-a", "--json")
	if err != nil {
		t.Fatalf("expected plan JSON to succeed, got %v", err)
	}
	if strings.Contains(out, "raw-key") {
		t.Fatalf("expected plan JSON to redact raw key, got %q", out)
	}
	var plan switching.SwitchPlan
	decodeCLIJSON(t, []byte(out), &plan)
	if !plan.ReadOnly || len(plan.Operations) != 1 || plan.Operations[0].Action != "create" {
		t.Fatalf("unexpected plan result: %#v", plan)
	}
	if _, err := os.Stat(targetPath); !os.IsNotExist(err) {
		t.Fatalf("expected plan not to create target file, stat error: %v", err)
	}

	humanOut, err = runCLI(t, "--config-dir", configDir, "switch", "provider-a", "profile-a")
	if err == nil {
		t.Fatalf("expected unconfirmed switch to fail")
	}
	assertCLIAppErrorCode(t, err, apperror.ConfirmationRequired)
	if !strings.Contains(humanOut, "Switch plan") || !strings.Contains(humanOut, "plan_fingerprint:") {
		t.Fatalf("expected unconfirmed switch to print plan, got %q", humanOut)
	}
	if strings.Contains(humanOut, "raw-key") {
		t.Fatalf("expected unconfirmed switch plan to redact raw key, got %q", humanOut)
	}
	if _, err := os.Stat(targetPath); !os.IsNotExist(err) {
		t.Fatalf("expected unconfirmed switch not to create target file, stat error: %v", err)
	}

	out, err = runCLI(t,
		"--config-dir", configDir,
		"switch", "provider-a", "profile-a",
		"--yes",
		"--plan-fingerprint", plan.PlanFingerprint,
		"--json",
	)
	if err != nil {
		t.Fatalf("expected confirmed switch JSON to succeed, got %v", err)
	}
	if strings.Contains(out, "raw-key") {
		t.Fatalf("expected switch JSON to exclude raw key, got %q", out)
	}
	var switchResult switching.ApplySwitchResult
	decodeCLIJSON(t, []byte(out), &switchResult)
	if switchResult.Status != "applied" || switchResult.Counts.Create != 1 || switchResult.OperationID == "" || !switchResult.RecoveryCleanupCompleted {
		t.Fatalf("unexpected switch result: %#v", switchResult)
	}
	rawTarget, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("expected switched target read to succeed, got %v", err)
	}
	if !strings.Contains(string(rawTarget), "raw-key") {
		t.Fatalf("expected target file to contain raw managed content, got %q", rawTarget)
	}

	if _, err := runCLI(t, "--config-dir", configDir, "profile", "target", "update", "profile-a", "provider-a", "target-a", "--enabled", "--disabled", "--json"); err != nil {
		assertCLIAppErrorCode(t, err, apperror.TargetInvalid)
	} else {
		t.Fatalf("expected enabled/disabled conflict to fail")
	}
	out, err = runCLI(t, "--config-dir", configDir, "profile", "target", "delete", "profile-a", "provider-a", "target-a", "--yes", "--json")
	if err != nil {
		t.Fatalf("expected profile target delete to succeed, got %v", err)
	}
	var deleted profiletarget.DeleteResult
	decodeCLIJSON(t, []byte(out), &deleted)
	if !deleted.Deleted || deleted.ID != "target-a" {
		t.Fatalf("unexpected target delete result: %#v", deleted)
	}
}

func TestSwitchCLIRejectsStalePlanFingerprint(t *testing.T) {
	configDir := t.TempDir()
	if _, err := runCLI(t, "--config-dir", configDir, "init", "--json"); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	if _, err := runCLI(t,
		"--config-dir", configDir,
		"provider", "create", "provider-a",
		"--name", "Provider A",
		"--adapter", "generic",
		"--json",
	); err != nil {
		t.Fatalf("expected provider create to succeed, got %v", err)
	}
	if _, err := runCLI(t,
		"--config-dir", configDir,
		"profile", "create", "profile-a",
		"--name", "Profile A",
		"--json",
	); err != nil {
		t.Fatalf("expected profile create to succeed, got %v", err)
	}

	targetPath := filepath.Join(t.TempDir(), "target.txt")
	if _, err := runCLI(t,
		"--config-dir", configDir,
		"profile", "target", "add", "profile-a", "target-a",
		"--provider", "provider-a",
		"--path", targetPath,
		"--format", "text",
		"--strategy", "replace-file",
		"--value-json", `{"content":"managed\n"}`,
		"--json",
	); err != nil {
		t.Fatalf("expected profile target add to succeed, got %v", err)
	}

	out, err := runCLI(t, "--config-dir", configDir, "plan", "provider-a", "profile-a", "--json")
	if err != nil {
		t.Fatalf("expected plan to succeed, got %v", err)
	}
	var plan switching.SwitchPlan
	decodeCLIJSON(t, []byte(out), &plan)
	if err := os.WriteFile(targetPath, []byte("external\n"), 0o600); err != nil {
		t.Fatalf("expected external write to succeed, got %v", err)
	}

	_, err = runCLI(t,
		"--config-dir", configDir,
		"switch", "provider-a", "profile-a",
		"--yes",
		"--plan-fingerprint", plan.PlanFingerprint,
		"--json",
	)
	assertCLIAppErrorCode(t, err, apperror.TargetChanged)
	raw, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("expected target read to succeed, got %v", err)
	}
	if string(raw) != "external\n" {
		t.Fatalf("expected stale switch not to overwrite target, got %q", raw)
	}
}

func TestApplicationBackupCLIJSONFlow(t *testing.T) {
	keyring.MockInit()
	t.Cleanup(keyring.MockInit)
	configDir := t.TempDir()
	if _, err := runCLI(t, "--config-dir", configDir, "init", "--json"); err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}
	if _, err := runCLI(t, "--config-dir", configDir, "profile", "create", "before", "--name", "Before", "--json"); err != nil {
		t.Fatalf("create pre-backup Profile: %v", err)
	}

	createOut, err := runCLI(t, "--config-dir", configDir, "backup", "create", "--json")
	if err != nil {
		t.Fatalf("create application backup: %v", err)
	}
	var created appbackup.BackupDetail
	decodeCLIJSON(t, []byte(createOut), &created)
	if created.ID == "" || created.Kind != appbackup.KindManual || created.Reason != appbackup.ReasonManual {
		t.Fatalf("unexpected created backup: %#v", created)
	}

	listOut, err := runCLI(t, "--config-dir", configDir, "backup", "list", "--json")
	if err != nil {
		t.Fatalf("list application backups: %v", err)
	}
	var listed appbackup.ListResult
	decodeCLIJSON(t, []byte(listOut), &listed)
	if len(listed.Backups) != 1 || listed.Backups[0].ID != created.ID {
		t.Fatalf("unexpected backup list: %#v", listed)
	}
	showOut, err := runCLI(t, "--config-dir", configDir, "backup", "show", created.ID, "--json")
	if err != nil {
		t.Fatalf("show application backup: %v", err)
	}
	var shown appbackup.BackupDetail
	decodeCLIJSON(t, []byte(showOut), &shown)
	if shown.ID != created.ID || shown.FormatVersion != 1 {
		t.Fatalf("unexpected backup detail: %#v", shown)
	}

	exportPath := filepath.Join(t.TempDir(), "exported.profiledeck-backup")
	if _, err := runCLI(t, "--config-dir", configDir, "backup", "export", created.ID, "--output", exportPath, "--json"); err != nil {
		t.Fatalf("export application backup: %v", err)
	}
	if info, err := os.Stat(exportPath); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("exported backup is invalid: info=%#v error=%v", info, err)
	}
	if _, err := runCLI(t, "--config-dir", configDir, "profile", "create", "after", "--name", "After", "--json"); err != nil {
		t.Fatalf("create post-backup Profile: %v", err)
	}

	previewOut, err := runCLI(t, "--config-dir", configDir, "backup", "restore", created.ID, "--json")
	assertCLIAppErrorCode(t, err, apperror.ConfirmationRequired)
	var preview appbackup.RestorePreview
	decodeCLIJSON(t, []byte(previewOut), &preview)
	if preview.Backup.ID != created.ID || preview.Fingerprint == "" {
		t.Fatalf("unexpected restore preview: %#v", preview)
	}
	restoreOut, err := runCLI(t, "--config-dir", configDir, "backup", "restore", created.ID, "--yes", "--json")
	if err != nil {
		t.Fatalf("restore application backup: %v", err)
	}
	var restored appbackup.RestoreResult
	decodeCLIJSON(t, []byte(restoreOut), &restored)
	if restored.Backup.ID != created.ID || restored.SafetyBackup == nil || !restored.RestartRequired {
		t.Fatalf("unexpected restore result: %#v", restored)
	}
	if _, err := runCLI(t, "--config-dir", configDir, "profile", "show", "after", "--json"); err == nil {
		t.Fatal("post-backup Profile remained after restore")
	} else {
		assertCLIAppErrorCode(t, err, apperror.ProfileNotFound)
	}

	_, err = runCLI(t, "--config-dir", configDir, "backup", "delete", created.ID)
	assertCLIAppErrorCode(t, err, apperror.ConfirmationRequired)
	if _, err := runCLI(t, "--config-dir", configDir, "backup", "delete", created.ID, "--yes"); err != nil {
		t.Fatalf("delete manual backup: %v", err)
	}

	keyStatusOut, err := runCLI(t, "--config-dir", configDir, "backup", "key", "status", "--json")
	if err != nil {
		t.Fatalf("read recovery key status: %v", err)
	}
	var keyStatus appbackup.KeyStatus
	decodeCLIJSON(t, []byte(keyStatusOut), &keyStatus)
	if !keyStatus.Available || keyStatus.Recipient == "" {
		t.Fatalf("unexpected key status: %#v", keyStatus)
	}
	keyPath := filepath.Join(t.TempDir(), "recovery-key.txt")
	if _, err := runCLI(t, "--config-dir", configDir, "backup", "key", "export", "--output", keyPath, "--yes", "--json"); err != nil {
		t.Fatalf("export recovery key: %v", err)
	}
	if _, err := runCLI(t, "--config-dir", configDir, "backup", "key", "import", "--file", keyPath, "--yes", "--json"); err != nil {
		t.Fatalf("import identical recovery key: %v", err)
	}
}

func TestRecoverCLIJSONAndHumanFlow(t *testing.T) {
	configDir := t.TempDir()
	initOut, err := runCLI(t, "--config-dir", configDir, "init", "--json")
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	var initResult profilesruntime.InitResult
	decodeCLIJSON(t, []byte(initOut), &initResult)
	if _, err := runCLI(t,
		"--config-dir", configDir,
		"provider", "create", "provider-a",
		"--name", "Provider A",
		"--adapter", "generic",
		"--json",
	); err != nil {
		t.Fatalf("expected provider create to succeed, got %v", err)
	}
	if _, err := runCLI(t,
		"--config-dir", configDir,
		"profile", "create", "profile-a",
		"--name", "Profile A",
		"--json",
	); err != nil {
		t.Fatalf("expected profile create to succeed, got %v", err)
	}

	dir := t.TempDir()
	firstPath := filepath.Join(dir, "target-a.txt")
	secondPath := filepath.Join(dir, "missing", "target-b.txt")
	for _, target := range []struct {
		id      string
		path    string
		content string
	}{
		{id: "target-a", path: firstPath, content: "first\n"},
		{id: "target-b", path: secondPath, content: "second\n"},
	} {
		if _, err := runCLI(t,
			"--config-dir", configDir,
			"profile", "target", "add", "profile-a", target.id,
			"--provider", "provider-a",
			"--path", target.path,
			"--format", "text",
			"--strategy", "replace-file",
			"--value-json", `{"content":"`+strings.ReplaceAll(target.content, "\n", `\n`)+`"}`,
			"--json",
		); err != nil {
			t.Fatalf("expected profile target %s create to succeed, got %v", target.id, err)
		}
	}

	_, err = runCLI(t, "--config-dir", configDir, "switch", "provider-a", "profile-a", "--yes", "--json")
	assertCLIAppErrorCode(t, err, apperror.TargetWriteFailed)
	assertFileString(t, firstPath, "first\n")
	failedSwitchID := singleCLIOperationIDByTypeStatus(t, initResult.DatabasePath, store.OperationTypeSwitch, store.OperationStatusFailed)

	_, err = runCLI(t, "--config-dir", configDir, "recover", failedSwitchID, "--json")
	assertCLIAppErrorCode(t, err, apperror.ConfirmationRequired)

	recoverOut, err := runCLI(t, "--config-dir", configDir, "recover", failedSwitchID, "--yes", "--json")
	if err != nil {
		t.Fatalf("expected recovery JSON to succeed, got %v", err)
	}
	var recovery switching.RecoverOperationResult
	decodeCLIJSON(t, []byte(recoverOut), &recovery)
	if recovery.Action != switching.RecoveryActionRestore || recovery.RecoveryOperationID == "" || recovery.SourceOperationID != failedSwitchID || recovery.Counts.Remove != 1 {
		t.Fatalf("unexpected recovery result: %#v", recovery)
	}
	if _, err := os.Stat(firstPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected first target to be removed, got %v", err)
	}

	var humanOut bytes.Buffer
	if err := writeRecoverResult(&humanOut, recovery); err != nil {
		t.Fatalf("write recovery human output: %v", err)
	}
	for _, expected := range []string{"Operation resolved", "action: restore", "changes: restore=0 remove=1 noop=1"} {
		if !strings.Contains(humanOut.String(), expected) {
			t.Fatalf("expected recovery human output to contain %q, got %q", expected, humanOut.String())
		}
	}
}

func TestRecoverHumanOutputWarnsWhenCleanupFails(t *testing.T) {
	var out bytes.Buffer
	err := writeRecoverResult(&out, switching.RecoverOperationResult{
		RecoveryOperationID: "recovery-attempt",
		SourceOperationID:   "switch-failed",
		Action:              switching.RecoveryActionRestore,
		Status:              "resolved",
		ProviderID:          "provider-a",
		ProfileID:           "profile-a",
		Counts:              switching.RecoveryCounts{Noop: 1},
	})
	if err != nil {
		t.Fatalf("expected recovery output to succeed, got %v", err)
	}
	if !strings.Contains(out.String(), "temporary recovery files still need cleanup") ||
		!strings.Contains(out.String(), "does not change tool sign-ins or settings") {
		t.Fatalf("expected recovery output to include warning, got %q", out.String())
	}
}

func TestBackupShowMissingReturnsNotFound(t *testing.T) {
	configDir := t.TempDir()
	if _, err := runCLI(t, "--config-dir", configDir, "init", "--json"); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	_, err := runCLI(t, "--config-dir", configDir, "backup", "show", "manual-20260101-000000Z", "--json")
	assertCLIAppErrorCode(t, err, apperror.BackupNotFound)
}

func TestDoctorCLIJSONAndHumanOutput(t *testing.T) {
	configDir := filepath.Join(t.TempDir(), "config")
	out, err := runCLI(t, "--config-dir", configDir, "doctor", "--json")
	if err != nil {
		t.Fatalf("expected doctor before init to succeed, got %v", err)
	}
	var result doctor.DoctorResult
	decodeCLIJSON(t, []byte(out), &result)
	if result.OverallLevel != doctor.LevelWarning || len(result.Findings) != 1 {
		t.Fatalf("unexpected doctor before init result: %#v", result)
	}
	if _, err := os.Stat(filepath.Join(configDir, "profiledeck", "profiledeck.db")); !os.IsNotExist(err) {
		t.Fatalf("doctor before init created an application database: %v", err)
	}

	initOut, err := runCLI(t, "--config-dir", configDir, "init", "--json")
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	var initResult profilesruntime.InitResult
	decodeCLIJSON(t, []byte(initOut), &initResult)
	insertFailedOperation(t, initResult.DatabasePath, "switch-cli-failed")

	out, err = runCLI(t, "--config-dir", configDir, "doctor", "--json")
	if err != nil {
		t.Fatalf("expected doctor with failed operation to succeed, got %v", err)
	}
	decodeCLIJSON(t, []byte(out), &result)
	if result.OverallLevel != doctor.LevelError || len(result.Operations) != 1 || result.Operations[0].ID != "switch-cli-failed" {
		t.Fatalf("unexpected doctor failed operation result: %#v", result)
	}

	humanOut, err := runCLI(t, "--config-dir", configDir, "doctor")
	if err != nil {
		t.Fatalf("expected doctor human output to succeed, got %v", err)
	}
	for _, expected := range []string{"database:", "operations:", "lock:", "TARGET_WRITE_FAILED"} {
		if !strings.Contains(humanOut, expected) {
			t.Fatalf("expected human doctor output to contain %q, got %q", expected, humanOut)
		}
	}
	if strings.Contains(humanOut, "write failed") {
		t.Fatalf("expected persisted error message to stay private, got %q", humanOut)
	}
}

func TestDoctorHumanOperationsUseStableColumns(t *testing.T) {
	var out bytes.Buffer
	err := writeDoctorOperations(&out, []doctor.DoctorOperation{
		{
			Level:         doctor.LevelError,
			ID:            "switch-pending",
			OperationType: "switch",
			Status:        "pending",
			Checkpoint:    "planned",
			Reason:        "pending_operation_without_active_lock",
		},
		{
			Level:         doctor.LevelError,
			ID:            "switch-failed",
			OperationType: "switch",
			Status:        "failed",
			Checkpoint:    "backed_up",
			ErrorCode:     "TARGET_WRITE_FAILED",
			ErrorMessage:  "write failed",
			Reason:        "failed_operation",
		},
	})
	if err != nil {
		t.Fatalf("expected doctor operation output to succeed, got %v", err)
	}
	output := out.String()
	for _, expected := range []string{
		"error_code=-",
		"error_code=TARGET_WRITE_FAILED",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("expected doctor operation output to contain %q, got %q", expected, output)
		}
	}
	if strings.Contains(output, "error_message") || strings.Contains(output, "write failed") {
		t.Fatalf("expected persisted error messages to stay private, got %q", output)
	}
}

func TestDoctorRepairLockCLIFlow(t *testing.T) {
	configDir := t.TempDir()
	initOut, err := runCLI(t, "--config-dir", configDir, "init", "--json")
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	var initResult profilesruntime.InitResult
	decodeCLIJSON(t, []byte(initOut), &initResult)
	insertFailedOperation(t, initResult.DatabasePath, "switch-cli-stale")
	lockPath := filepath.Join(initResult.RuntimeRoot, "locks", "switch.lock")
	if err := os.WriteFile(lockPath, []byte("switch-cli-stale\npid=999999999\ncreated_at_unix_ms=1\n"), 0o600); err != nil {
		t.Fatalf("expected stale lock setup to succeed, got %v", err)
	}

	_, err = runCLI(t, "--config-dir", configDir, "doctor", "repair-lock", "--json")
	assertCLIAppErrorCode(t, err, apperror.ConfirmationRequired)

	out, err := runCLI(t, "--config-dir", configDir, "doctor", "repair-lock", "--yes", "--json")
	if err != nil {
		t.Fatalf("expected doctor repair-lock to succeed, got %v", err)
	}
	var repair doctor.DoctorRepairLockResult
	decodeCLIJSON(t, []byte(out), &repair)
	if !repair.Repaired || repair.Path != lockPath {
		t.Fatalf("unexpected repair result: %#v", repair)
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("expected lock file to be removed, got %v", err)
	}
}

func TestDoctorRetryCleanupCLIFlow(t *testing.T) {
	configDir := t.TempDir()
	initOut, err := runCLI(t, "--config-dir", configDir, "init", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var initResult profilesruntime.InitResult
	decodeCLIJSON(t, []byte(initOut), &initResult)
	orphan := filepath.Join(initResult.RuntimeRoot, "recovery", "orphan")
	if err := os.WriteFile(orphan, []byte("orphan"), 0o600); err != nil {
		t.Fatal(err)
	}
	statusOut, err := runCLI(t, "--config-dir", configDir, "status", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var status profilesruntime.StatusResult
	decodeCLIJSON(t, []byte(statusOut), &status)
	if !status.OperationRecoveryCleanupRequired {
		t.Fatalf("status did not report cleanup requirement: %#v", status)
	}
	humanStatus, err := runCLI(t, "--config-dir", configDir, "status")
	if err != nil || !strings.Contains(humanStatus, "temporary recovery cleanup: required") ||
		!strings.Contains(humanStatus, "profiledeck doctor retry-cleanup --yes") {
		t.Fatalf("human status did not explain cleanup restriction: %q, %v", humanStatus, err)
	}
	_, err = runCLI(t, "--config-dir", configDir, "doctor", "retry-cleanup", "--json")
	assertCLIAppErrorCode(t, err, apperror.ConfirmationRequired)

	out, err := runCLI(t, "--config-dir", configDir, "doctor", "retry-cleanup", "--yes", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var result recoverycleanup.RetryRecoveryCleanupResult
	decodeCLIJSON(t, []byte(out), &result)
	if !result.RecoveryCleanupCompleted {
		t.Fatalf("retry result = %#v", result)
	}
	human, err := runCLI(t, "--config-dir", configDir, "doctor", "retry-cleanup", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(human, "Tool sign-ins and settings were not changed") {
		t.Fatalf("retry output missing safety explanation: %q", human)
	}
}

func TestProviderCLIOutputRedactsSensitiveMetadata(t *testing.T) {
	configDir := t.TempDir()
	initOut, err := runCLI(t, "--config-dir", configDir, "init", "--json")
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	var initResult profilesruntime.InitResult
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
	var provider provider.Provider
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

func assertCLIAppErrorCode(t *testing.T, err error, code apperror.Code) {
	t.Helper()

	if err == nil {
		t.Fatalf("expected error code %s, got nil", code)
	}
	var appErr *apperror.Error
	if !errors.As(err, &appErr) {
		t.Fatalf("expected AppError code %s, got %T: %v", code, err, err)
	}
	if appErr.Code != code {
		t.Fatalf("expected error code %s, got %s: %v", code, appErr.Code, err)
	}
}

func assertFileString(t *testing.T, path, expected string) {
	t.Helper()

	raw := mustReadFile(t, path)
	if string(raw) != expected {
		t.Fatalf("expected file %s content %q, got %q", path, expected, string(raw))
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected file read to succeed, got %v", err)
	}
	return raw
}

func insertFailedOperation(t *testing.T, databasePath, operationID string) {
	t.Helper()

	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("expected sqlite open to succeed, got %v", err)
	}
	defer db.Close()
	_, err = db.ExecContext(context.Background(), `
		INSERT INTO operations (id, operation_type, status, profile_id, metadata_json, error_code, error_message, created_at_unix_ms, updated_at_unix_ms)
		VALUES (?, 'switch', 'failed', 'profile-a', '{"checkpoint":"backed_up","provider_id":"provider-a","profile_id":"profile-a"}', 'TARGET_WRITE_FAILED', 'write failed', 1, 1)
	`, operationID)
	if err != nil {
		t.Fatalf("expected failed operation setup to succeed, got %v", err)
	}
}

func writeCLIUsageFixture(t *testing.T, codexDir, content string) {
	t.Helper()
	path := filepath.Join(codexDir, "sessions", "2026", "07", "06", "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("expected usage fixture dir setup to succeed, got %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("expected usage fixture write to succeed, got %v", err)
	}
}

func writeCLICodexProfileFixture(t *testing.T, codexDir, config, auth string) {
	t.Helper()
	if err := os.MkdirAll(codexDir, 0o700); err != nil {
		t.Fatalf("expected Codex fixture dir setup to succeed, got %v", err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, codexconfig.ConfigFileName), []byte(config), 0o600); err != nil {
		t.Fatalf("expected Codex config fixture write to succeed, got %v", err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, codexconfig.AuthFileName), []byte(auth), 0o600); err != nil {
		t.Fatalf("expected Codex auth fixture write to succeed, got %v", err)
	}
}

func hasCLIPlanOperation(operations []switching.PlanOperation, targetID, action string) bool {
	for _, operation := range operations {
		if operation.TargetID == targetID && operation.Action == action {
			return true
		}
	}
	return false
}

func singleCLIOperationIDByTypeStatus(t *testing.T, databasePath, operationType, status string) string {
	t.Helper()

	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("expected sqlite open to succeed, got %v", err)
	}
	defer db.Close()

	rows, err := db.Query(`
		SELECT id
		FROM operations
		WHERE operation_type = ? AND status = ?
		ORDER BY created_at_unix_ms ASC, id ASC
	`, operationType, status)
	if err != nil {
		t.Fatalf("expected operation query to succeed, got %v", err)
	}
	defer rows.Close()

	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("expected operation id scan to succeed, got %v", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("expected operation rows to succeed, got %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("expected one %s %s operation, got %d: %v", operationType, status, len(ids), ids)
	}
	return ids[0]
}

func providerIDs(providers []provider.Provider) string {
	ids := make([]string, 0, len(providers))
	for _, provider := range providers {
		ids = append(ids, provider.ID)
	}
	return strings.Join(ids, ",")
}

func profileIDs(profiles []profile.Profile) string {
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
