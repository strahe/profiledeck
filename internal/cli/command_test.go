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
	"time"

	keyring "github.com/zalando/go-keyring"

	agyconfig "github.com/strahe/profiledeck/internal/antigravity/config"
	"github.com/strahe/profiledeck/internal/app"
	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	"github.com/strahe/profiledeck/internal/store"
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
	if antigravity := cmd.Command("antigravity"); antigravity == nil || antigravity.Command("detect") == nil || antigravity.Command("profile") == nil {
		t.Fatalf("expected Antigravity commands")
	}
	if codex := cmd.Command("codex"); codex == nil || codex.Command("profile") == nil ||
		codex.Command("profile").Command("list") == nil ||
		codex.Command("profile").Command("show") == nil ||
		codex.Command("profile").Command("create") == nil ||
		codex.Command("profile").Command("fork") == nil ||
		codex.Command("profile").Command("save-current") == nil ||
		codex.Command("profile").Command("set-config") == nil ||
		codex.Command("profile").Command("export") == nil ||
		codex.Command("profile").Command("import") == nil ||
		codex.Command("profile").Command("import").Command("inspect") == nil ||
		codex.Command("profile").Command("import").Command("apply") == nil ||
		codex.Command("profile").Command("sync") != nil ||
		codex.Command("config-set") == nil {
		t.Fatalf("expected Codex profile and Config Set commands")
	}
	if cmd.Command("doctor") == nil {
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
	if cmd.Command("rollback") == nil {
		t.Fatalf("expected rollback subcommand")
	}
}

func TestWritePlanUsesSafeTargetLabelWithoutPath(t *testing.T) {
	var output bytes.Buffer
	err := writePlan(&output, app.SwitchPlan{
		Provider: app.PlanProvider{ID: agyconfig.ProviderID, Name: "Antigravity"},
		Profile:  app.PlanProfile{ID: "work", Name: "Work"},
		Operations: []app.PlanOperation{{
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

func TestWriteBackupDetailUsesSafeTargetLabelWithoutPath(t *testing.T) {
	var output bytes.Buffer
	err := writeBackupDetail(&output, app.BackupDetail{
		BackupSummary: app.BackupSummary{BackupID: "switch-test", ProviderID: agyconfig.ProviderID},
		Entries: []app.BackupEntrySummary{{
			TargetID: "auth", BackendID: "keyring", TargetLabel: "Antigravity login", Action: "update", Existed: true,
		}},
	})
	if err != nil {
		t.Fatalf("expected backup output to succeed, got %v", err)
	}
	if !strings.Contains(output.String(), "Antigravity login") {
		t.Fatalf("expected safe target label in backup output, got %q", output.String())
	}
}

func TestAntigravityProfileCLIUsesAgyV2KeyringState(t *testing.T) {
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
	var created app.AntigravityProfileSaveResult
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
	for _, secret := range []string{"cli-access-secret", "cli-refresh-secret", "access_token", "refresh_token"} {
		if strings.Contains(createdRaw, secret) || strings.Contains(listed, secret) {
			t.Fatalf("expected Antigravity CLI output to hide %q", secret)
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
	var syncResult app.UsageSyncResult
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
	var summary app.UsageSummaryResult
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
	var report app.UsageReportResult
	decodeCLIJSON(t, []byte(reportOut), &report)
	if report.Range.Preset != app.UsageRange7Days || report.Summary.EventCount != 0 || report.Summary.UndatedEventCount != 1 || len(report.Trend) != 7 {
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
	var result app.UsageSyncResult
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
	resolved := app.UsageResolvedRange{BucketUnit: "hour", TimeZone: location.String()}
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
	var detect app.CodexDetectResult
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
	var createResult app.CodexProfileSaveResult
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
	var plan app.SwitchPlan
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
	var switchResult app.ApplySwitchResult
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
	var created app.CodexProfileSaveResult
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
	assertCLIAppErrorCode(t, err, app.ErrorProfileInvalid)
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
	var created app.CodexProfileSaveResult
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
		"--credential-binding", app.CodexForkBindingShareParent,
		"--config-binding", app.CodexForkBindingCopyNew,
		"--new-config-set", "shared-child-config",
		"--json",
	)
	if err != nil {
		t.Fatalf("expected codex profile fork to succeed, got %v", err)
	}
	var forked app.CodexProfileSaveResult
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
	var saved app.CodexProfileStateSaveResult
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
	var exported app.CodexProfileExportResult
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
	var plan app.CodexProfileImportPlan
	decodeCLIJSON(t, []byte(out), &plan)
	if !plan.CanApply || plan.PlanFingerprint == "" {
		t.Fatalf("unexpected import plan: %#v", plan)
	}
	_, err = runCLI(t, "--config-dir", targetConfigDir, "codex", "profile", "import", "apply", bundlePath, "--codex-dir", codexDir, "--plan-fingerprint", plan.PlanFingerprint, "--json")
	assertCLIAppErrorCode(t, err, app.ErrorConfirmationRequired)
	out, err = runCLI(t, "--config-dir", targetConfigDir, "codex", "profile", "import", "apply", bundlePath, "--codex-dir", codexDir, "--plan-fingerprint", plan.PlanFingerprint, "--yes", "--json")
	if err != nil {
		t.Fatalf("expected CLI import apply to succeed, got %v", err)
	}
	if strings.Contains(out, rawAuthSecret) || strings.Contains(out, rawConfigSecret) {
		t.Fatalf("expected import result output to contain metadata only, got %q", out)
	}
	var imported app.CodexProfileImportResult
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
	if _, err := runCLI(t, "--config-dir", configDir, "codex", "profile", "fork", "work", "child", "--codex-dir", codexDir, "--credential-binding", app.CodexForkBindingCopyNew, "--config-binding", app.CodexForkBindingShareParent, "--json"); err != nil {
		t.Fatalf("expected child fork to succeed, got %v", err)
	}
	out, err = runCLI(t, "--config-dir", configDir, "codex", "profile", "set-config", "child", "other", "--json")
	if err != nil {
		t.Fatalf("expected inactive profile rebind to succeed, got %v", err)
	}
	var detail app.CodexProfileDetail
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
	var shown app.CodexConfigSet
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
	var list app.CodexProfileListResult
	decodeCLIJSON(t, []byte(out), &list)
	if len(list.Profiles) != 1 {
		t.Fatalf("expected one Codex profile, got %#v", list)
	}
	byID := map[string]app.CodexProfileSummary{}
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
	var detail app.CodexProfileDetail
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
	assertCLIAppErrorCode(t, err, app.ErrorProfileNotFound)
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
	var target app.ProfileTarget
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
	var targets []app.ProfileTarget
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
	var plan app.SwitchPlan
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
	assertCLIAppErrorCode(t, err, app.ErrorConfirmationRequired)
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
	var switchResult app.ApplySwitchResult
	decodeCLIJSON(t, []byte(out), &switchResult)
	if switchResult.Status != "applied" || switchResult.Counts.Create != 1 || switchResult.OperationID == "" || switchResult.BackupPath == "" {
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
		assertCLIAppErrorCode(t, err, app.ErrorTargetInvalid)
	} else {
		t.Fatalf("expected enabled/disabled conflict to fail")
	}
	out, err = runCLI(t, "--config-dir", configDir, "profile", "target", "delete", "profile-a", "provider-a", "target-a", "--yes", "--json")
	if err != nil {
		t.Fatalf("expected profile target delete to succeed, got %v", err)
	}
	var deleted app.DeleteResult
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
	var plan app.SwitchPlan
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
	assertCLIAppErrorCode(t, err, app.ErrorTargetChanged)
	raw, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("expected target read to succeed, got %v", err)
	}
	if string(raw) != "external\n" {
		t.Fatalf("expected stale switch not to overwrite target, got %q", raw)
	}
}

func TestRollbackCLIJSONFlow(t *testing.T) {
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
	if err := os.WriteFile(targetPath, []byte("OLD=value\n"), 0o600); err != nil {
		t.Fatalf("expected old target setup to succeed, got %v", err)
	}
	if _, err := runCLI(t,
		"--config-dir", configDir,
		"profile", "target", "add", "profile-a", "target-a",
		"--provider", "provider-a",
		"--path", targetPath,
		"--format", "text",
		"--strategy", "replace-file",
		"--value-json", `{"content":"OPENAI_API_KEY=raw-key\n"}`,
		"--json",
	); err != nil {
		t.Fatalf("expected profile target add to succeed, got %v", err)
	}

	switchOut, err := runCLI(t, "--config-dir", configDir, "switch", "provider-a", "profile-a", "--yes", "--json")
	if err != nil {
		t.Fatalf("expected switch to succeed, got %v", err)
	}
	if strings.Contains(switchOut, "raw-key") {
		t.Fatalf("expected switch output to exclude raw key, got %q", switchOut)
	}
	var switchResult app.ApplySwitchResult
	decodeCLIJSON(t, []byte(switchOut), &switchResult)
	if switchResult.OperationID == "" || switchResult.Counts.Update != 1 {
		t.Fatalf("unexpected switch result: %#v", switchResult)
	}

	_, err = runCLI(t, "--config-dir", configDir, "rollback", switchResult.OperationID, "--json")
	assertCLIAppErrorCode(t, err, app.ErrorConfirmationRequired)
	assertFileString(t, targetPath, "OPENAI_API_KEY=raw-key\n")

	listOut, err := runCLI(t, "--config-dir", configDir, "backup", "list", "--json")
	if err != nil {
		t.Fatalf("expected backup list to succeed, got %v", err)
	}
	if strings.Contains(listOut, "raw-key") {
		t.Fatalf("expected backup list output to exclude raw key, got %q", listOut)
	}
	var listResult app.ListBackupsResult
	decodeCLIJSON(t, []byte(listOut), &listResult)
	if len(listResult.Backups) != 1 || !listResult.Backups[0].RollbackSupported {
		t.Fatalf("unexpected backup list result: %#v", listResult)
	}

	showOut, err := runCLI(t, "--config-dir", configDir, "backup", "show", switchResult.OperationID, "--json")
	if err != nil {
		t.Fatalf("expected backup show to succeed, got %v", err)
	}
	if strings.Contains(showOut, "raw-key") {
		t.Fatalf("expected backup show output to exclude raw key, got %q", showOut)
	}
	var detail app.BackupDetail
	decodeCLIJSON(t, []byte(showOut), &detail)
	if !detail.RollbackSupported || len(detail.Entries) != 1 {
		t.Fatalf("unexpected backup detail: %#v", detail)
	}

	rollbackOut, err := runCLI(t, "--config-dir", configDir, "rollback", switchResult.OperationID, "--yes", "--json")
	if err != nil {
		t.Fatalf("expected rollback to succeed, got %v", err)
	}
	if strings.Contains(rollbackOut, "raw-key") {
		t.Fatalf("expected rollback output to exclude raw key, got %q", rollbackOut)
	}
	var rollbackResult app.ApplyRollbackResult
	decodeCLIJSON(t, []byte(rollbackOut), &rollbackResult)
	if rollbackResult.Status != "applied" || rollbackResult.SourceOperationID != switchResult.OperationID || rollbackResult.Counts.Restore != 1 {
		t.Fatalf("unexpected rollback result: %#v", rollbackResult)
	}
	assertFileString(t, targetPath, "OLD=value\n")
}

func TestRecoverCLIJSONAndHumanFlow(t *testing.T) {
	configDir := t.TempDir()
	initOut, err := runCLI(t, "--config-dir", configDir, "init", "--json")
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	var initResult app.InitResult
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
	assertCLIAppErrorCode(t, err, app.ErrorTargetWriteFailed)
	assertFileString(t, firstPath, "first\n")
	failedSwitchID := singleCLIOperationIDByTypeStatus(t, initResult.DatabasePath, store.OperationTypeSwitch, store.OperationStatusFailed)

	_, err = runCLI(t, "--config-dir", configDir, "recover", failedSwitchID, "--json")
	assertCLIAppErrorCode(t, err, app.ErrorConfirmationRequired)

	recoverOut, err := runCLI(t, "--config-dir", configDir, "recover", failedSwitchID, "--yes", "--json")
	if err != nil {
		t.Fatalf("expected recovery JSON to succeed, got %v", err)
	}
	var recovery app.RecoverFailedSwitchResult
	decodeCLIJSON(t, []byte(recoverOut), &recovery)
	if recovery.OperationType != store.OperationTypeRollback || recovery.RollbackKind != "failed_switch_recovery" || recovery.SourceOperationID != failedSwitchID || recovery.Counts.Remove != 1 {
		t.Fatalf("unexpected recovery result: %#v", recovery)
	}
	if _, err := os.Stat(firstPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected first target to be removed, got %v", err)
	}

	humanOut, err := runCLI(t, "--config-dir", configDir, "recover", failedSwitchID, "--yes")
	if err != nil {
		t.Fatalf("expected idempotent recovery human output to succeed, got %v", err)
	}
	for _, expected := range []string{"Recovery applied", "rollback_kind: failed_switch_recovery", "operation_type: rollback"} {
		if !strings.Contains(humanOut, expected) {
			t.Fatalf("expected recovery human output to contain %q, got %q", expected, humanOut)
		}
	}
}

func TestRecoverHumanOutputIncludesWarnings(t *testing.T) {
	var out bytes.Buffer
	err := writeRecoverResult(&out, app.RecoverFailedSwitchResult{
		OperationID:       "rollback-recovery",
		OperationType:     store.OperationTypeRollback,
		RollbackKind:      "failed_switch_recovery",
		Status:            "applied",
		SourceOperationID: "switch-failed",
		ProviderID:        "provider-a",
		ProfileID:         "profile-a",
		Counts:            app.RollbackCounts{Noop: 1},
		BackupPath:        "/tmp/profiledeck-backup",
		Warnings:          []string{"target already restored"},
	})
	if err != nil {
		t.Fatalf("expected recovery output to succeed, got %v", err)
	}
	if !strings.Contains(out.String(), "warning: target already restored") {
		t.Fatalf("expected recovery output to include warning, got %q", out.String())
	}
}

func TestBackupShowMissingReturnsNotFound(t *testing.T) {
	configDir := t.TempDir()
	if _, err := runCLI(t, "--config-dir", configDir, "init", "--json"); err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}

	_, err := runCLI(t, "--config-dir", configDir, "backup", "show", "missing-backup", "--json")
	assertCLIAppErrorCode(t, err, app.ErrorBackupNotFound)
}

func TestDoctorCLIJSONAndHumanOutput(t *testing.T) {
	configDir := filepath.Join(t.TempDir(), "config")
	out, err := runCLI(t, "--config-dir", configDir, "doctor", "--json")
	if err != nil {
		t.Fatalf("expected doctor before init to succeed, got %v", err)
	}
	var result app.DoctorResult
	decodeCLIJSON(t, []byte(out), &result)
	if result.OverallLevel != app.DoctorLevelWarning || len(result.Findings) != 1 {
		t.Fatalf("unexpected doctor before init result: %#v", result)
	}
	if _, err := os.Stat(configDir); !os.IsNotExist(err) {
		t.Fatalf("expected doctor not to create config dir, got %v", err)
	}

	initOut, err := runCLI(t, "--config-dir", configDir, "init", "--json")
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	var initResult app.InitResult
	decodeCLIJSON(t, []byte(initOut), &initResult)
	insertFailedOperation(t, initResult.DatabasePath, "switch-cli-failed")

	out, err = runCLI(t, "--config-dir", configDir, "doctor", "--json")
	if err != nil {
		t.Fatalf("expected doctor with failed operation to succeed, got %v", err)
	}
	decodeCLIJSON(t, []byte(out), &result)
	if result.OverallLevel != app.DoctorLevelError || len(result.Operations) != 1 || result.Operations[0].ID != "switch-cli-failed" {
		t.Fatalf("unexpected doctor failed operation result: %#v", result)
	}

	humanOut, err := runCLI(t, "--config-dir", configDir, "doctor")
	if err != nil {
		t.Fatalf("expected doctor human output to succeed, got %v", err)
	}
	for _, expected := range []string{"database:", "operations:", "lock:", "TARGET_WRITE_FAILED", "write failed"} {
		if !strings.Contains(humanOut, expected) {
			t.Fatalf("expected human doctor output to contain %q, got %q", expected, humanOut)
		}
	}
}

func TestDoctorHumanOperationsUseStableColumns(t *testing.T) {
	var out bytes.Buffer
	err := writeDoctorOperations(&out, []app.DoctorOperation{
		{
			Level:         app.DoctorLevelError,
			ID:            "switch-pending",
			OperationType: "switch",
			Status:        "pending",
			Checkpoint:    "planned",
			Reason:        "pending_operation_without_active_lock",
		},
		{
			Level:         app.DoctorLevelError,
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
		"error_message=-",
		"error_code=TARGET_WRITE_FAILED",
		"error_message=write failed",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("expected doctor operation output to contain %q, got %q", expected, output)
		}
	}
}

func TestDoctorRepairLockCLIFlow(t *testing.T) {
	configDir := t.TempDir()
	initOut, err := runCLI(t, "--config-dir", configDir, "init", "--json")
	if err != nil {
		t.Fatalf("expected init to succeed, got %v", err)
	}
	var initResult app.InitResult
	decodeCLIJSON(t, []byte(initOut), &initResult)
	insertFailedOperation(t, initResult.DatabasePath, "switch-cli-stale")
	lockPath := filepath.Join(initResult.RuntimeRoot, "locks", "switch.lock")
	if err := os.WriteFile(lockPath, []byte("switch-cli-stale\npid=999999999\ncreated_at_unix_ms=1\n"), 0o600); err != nil {
		t.Fatalf("expected stale lock setup to succeed, got %v", err)
	}

	_, err = runCLI(t, "--config-dir", configDir, "doctor", "repair-lock", "--json")
	assertCLIAppErrorCode(t, err, app.ErrorConfirmationRequired)

	out, err := runCLI(t, "--config-dir", configDir, "doctor", "repair-lock", "--yes", "--json")
	if err != nil {
		t.Fatalf("expected doctor repair-lock to succeed, got %v", err)
	}
	var repair app.DoctorRepairLockResult
	decodeCLIJSON(t, []byte(out), &repair)
	if !repair.Repaired || repair.Path != lockPath {
		t.Fatalf("unexpected repair result: %#v", repair)
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("expected lock file to be removed, got %v", err)
	}
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

func hasCLIPlanOperation(operations []app.PlanOperation, targetID, action string) bool {
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
