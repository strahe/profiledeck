package cli

import (
	"context"
	"fmt"
	"io"

	urfavecli "github.com/urfave/cli/v3"

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/codex"
)

const (
	outputFlagName = "output"
	forceFlagName  = "force"
)

func newCodexProfileExportCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "export", Usage: "Export sensitive Codex Profile backups", ArgsUsage: "[profile-id ...]",
		Flags: []urfavecli.Flag{
			&urfavecli.StringFlag{Name: outputFlagName, Usage: "Write the sensitive bundle to this path", Required: true, Config: urfavecli.StringConfig{TrimSpace: true}},
			boolFlag(forceFlagName, "Replace an existing regular output file"),
			boolFlag(jsonFlagName, "Write JSON metadata output"),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			application, err := applicationFor(cmd)
			if err != nil {
				return err
			}
			result, err := application.Codex().ExportProfiles(ctx, codex.ExportCodexProfilesRequest{
				ProfileIDs: cmd.Args().Slice(),
				OutputPath: cmd.String(outputFlagName), Overwrite: cmd.Bool(forceFlagName),
			})
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			return writeCodexProfileExportResult(outputWriter(cmd), result)
		},
	}
}

func newCodexProfileImportCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "import", Usage: "Inspect and apply sensitive Codex Profile backups",
		Commands: []*urfavecli.Command{newCodexProfileImportInspectCommand(), newCodexProfileImportApplyCommand()},
	}
}

func newCodexProfileImportInspectCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "inspect", Usage: "Validate a Codex Profile bundle and report conflicts", ArgsUsage: "<bundle-path>",
		Flags: []urfavecli.Flag{stringFlag(codexDirFlagName, "Codex config directory"), boolFlag(jsonFlagName, "Write JSON output")},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			path, err := singlePathArg(cmd)
			if err != nil {
				return err
			}
			application, err := applicationFor(cmd)
			if err != nil {
				return err
			}
			result, err := application.Codex().InspectProfileImport(ctx, codex.InspectCodexProfileImportRequest{InputPath: path})
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			return writeCodexProfileImportPlan(outputWriter(cmd), result)
		},
	}
}

func newCodexProfileImportApplyCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "apply", Usage: "Apply a reviewed Codex Profile import plan", ArgsUsage: "<bundle-path>",
		Flags: []urfavecli.Flag{
			stringFlag(codexDirFlagName, "Codex config directory"),
			stringFlag(planFingerprintFlagName, "Require a matching import plan fingerprint"),
			boolFlag(yesFlagName, "Confirm import apply"),
			boolFlag(jsonFlagName, "Write JSON output"),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			path, err := singlePathArg(cmd)
			if err != nil {
				return err
			}
			application, err := applicationFor(cmd)
			if err != nil {
				return err
			}
			result, err := application.Codex().ImportProfiles(ctx, codex.ImportCodexProfilesRequest{
				InputPath:               path,
				ExpectedPlanFingerprint: cmd.String(planFingerprintFlagName), Confirm: cmd.Bool(yesFlagName),
			})
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			return writeCodexProfileImportResult(outputWriter(cmd), result)
		},
	}
}

func singlePathArg(cmd *urfavecli.Command) (string, error) {
	if cmd.Args().Len() != 1 {
		return "", apperror.New(apperror.ImportInvalid, "exactly one bundle path is required")
	}
	return cmd.Args().Get(0), nil
}

func writeCodexProfileExportResult(w io.Writer, result codex.CodexProfileExportResult) error {
	if _, err := fmt.Fprintf(w,
		"Codex Profiles exported\npath: %s\nsha256: %s\nmode: %s\nprofiles: %d\ncredentials: %d\nconfig_sets: %d\n",
		result.Path, result.SHA256, result.FileMode, result.ProfileCount, result.CredentialCount, result.ConfigSetCount,
	); err != nil {
		return err
	}
	return writeWarnings(w, result.Warnings)
}

func writeCodexProfileImportPlan(w io.Writer, plan codex.CodexProfileImportPlan) error {
	if _, err := fmt.Fprintf(w,
		"Codex Profile import plan\npath: %s\nfile_sha256: %s\nplan_fingerprint: %s\ncodex_dir: %s\nprofiles: %d\ncredentials: %d\nconfig_sets: %d\nactions: create=%d unchanged=%d conflict=%d\ncan_apply: %t\nno_changes: %t\n",
		plan.Path, plan.FileSHA256, plan.PlanFingerprint, plan.CodexDir,
		plan.ProfileCount, plan.CredentialCount, plan.ConfigSetCount,
		plan.Counts.Create, plan.Counts.Unchanged, plan.Counts.Conflict, plan.CanApply, plan.NoChanges,
	); err != nil {
		return err
	}
	for _, item := range plan.Items {
		name := ""
		if item.Name != "" {
			name = " name=" + item.Name
		}
		reason := ""
		if item.Reason != "" {
			reason = " reason=" + item.Reason
		}
		if _, err := fmt.Fprintf(w, "- %s %s action=%s%s%s\n", item.Kind, item.ID, item.Action, name, reason); err != nil {
			return err
		}
	}
	return writeWarnings(w, plan.Warnings)
}

func writeCodexProfileImportResult(w io.Writer, result codex.CodexProfileImportResult) error {
	status := "no changes"
	if result.Changed {
		status = "applied"
	}
	if _, err := fmt.Fprintf(w,
		"Codex Profiles import %s\noperation: %s\nfile_sha256: %s\nplan_fingerprint: %s\nprofiles: %d\ncredentials: %d\nconfig_sets: %d\nactions: create=%d unchanged=%d conflict=%d\n",
		status, result.OperationID, result.FileSHA256, result.PlanFingerprint,
		result.ProfileCount, result.CredentialCount, result.ConfigSetCount,
		result.Counts.Create, result.Counts.Unchanged, result.Counts.Conflict,
	); err != nil {
		return err
	}
	return writeWarnings(w, result.Warnings)
}
