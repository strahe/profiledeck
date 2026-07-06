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
	if cmd.Command("backup") == nil {
		t.Fatalf("expected backup subcommand")
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
	if cmd.Command("plan") == nil {
		t.Fatalf("expected plan subcommand")
	}
	if cmd.Command("provider") == nil {
		t.Fatalf("expected provider subcommand")
	}
	if cmd.Command("profile") == nil {
		t.Fatalf("expected profile subcommand")
	}
	if cmd.Command("rollback") == nil {
		t.Fatalf("expected rollback subcommand")
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

func assertFileString(t *testing.T, path string, expected string) {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected file read to succeed, got %v", err)
	}
	if string(raw) != expected {
		t.Fatalf("expected file %s content %q, got %q", path, expected, string(raw))
	}
}

func insertFailedOperation(t *testing.T, databasePath string, operationID string) {
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
