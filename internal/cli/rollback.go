package cli

import (
	"context"
	"fmt"
	"io"
	"text/tabwriter"

	urfavecli "github.com/urfave/cli/v3"

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/switching"
)

func newBackupCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:  "backup",
		Usage: "Inspect ProfileDeck backups",
		Commands: []*urfavecli.Command{
			newBackupListCommand(),
			newBackupShowCommand(),
		},
	}
}

func newBackupListCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:  "list",
		Usage: "List backups",
		Flags: []urfavecli.Flag{
			boolFlag(jsonFlagName, "Write JSON output"),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			application, err := applicationFor(cmd)
			if err != nil {
				return err
			}
			result, err := application.Switching().ListBackups(ctx)
			if err != nil {
				return err
			}
			w := outputWriter(cmd)
			if cmd.Bool(jsonFlagName) {
				return writeJSON(w, result)
			}
			return writeBackupList(w, result)
		},
	}
}

func newBackupShowCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:      "show",
		Usage:     "Show a backup",
		ArgsUsage: "<backup-id>",
		Flags: []urfavecli.Flag{
			boolFlag(jsonFlagName, "Write JSON output"),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			backupID, err := singleIDArg(cmd, apperror.BackupInvalid)
			if err != nil {
				return err
			}
			application, err := applicationFor(cmd)
			if err != nil {
				return err
			}
			result, err := application.Switching().ShowBackup(ctx, backupID)
			if err != nil {
				return err
			}
			w := outputWriter(cmd)
			if cmd.Bool(jsonFlagName) {
				return writeJSON(w, result)
			}
			return writeBackupDetail(w, result)
		},
	}
}

func newRollbackCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:      "rollback",
		Usage:     "Rollback an applied switch backup",
		ArgsUsage: "<backup-id>",
		Flags: []urfavecli.Flag{
			boolFlag(yesFlagName, "Confirm rollback apply"),
			boolFlag(jsonFlagName, "Write JSON output"),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			backupID, err := singleIDArg(cmd, apperror.BackupInvalid)
			if err != nil {
				return err
			}
			application, err := applicationFor(cmd)
			if err != nil {
				return err
			}
			result, err := application.Switching().Rollback(ctx, switching.ApplyRollbackRequest{
				BackupID: backupID,
				Confirm:  cmd.Bool(yesFlagName),
			})
			if err != nil {
				return err
			}
			w := outputWriter(cmd)
			if cmd.Bool(jsonFlagName) {
				return writeJSON(w, result)
			}
			return writeRollbackResult(w, result)
		},
	}
}

func writeBackupList(w io.Writer, result switching.ListBackupsResult) error {
	if len(result.Backups) == 0 {
		_, err := fmt.Fprintln(w, "No backups")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 8, 2, ' ', 0)
	for _, backup := range result.Backups {
		supported := "unsupported"
		if backup.RollbackSupported {
			supported = "rollback-supported"
		}
		if !backup.Valid {
			supported = "invalid"
		}
		if _, err := fmt.Fprintf(
			tw,
			"%s\t%s\t%s\t%s\tentries=%d\n",
			backup.BackupID,
			backup.OperationType,
			backup.OperationStatus,
			supported,
			backup.EntryCount,
		); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func writeBackupDetail(w io.Writer, detail switching.BackupDetail) error {
	supported := "false"
	if detail.RollbackSupported {
		supported = "true"
	}
	if _, err := fmt.Fprintf(
		w,
		"Backup %s\noperation: %s\noperation_type: %s\nstatus: %s\nprovider: %s\nprofile: %s\nrollback_supported: %s\n",
		detail.BackupID,
		detail.OperationID,
		detail.OperationType,
		detail.OperationStatus,
		detail.ProviderID,
		detail.ProfileID,
		supported,
	); err != nil {
		return err
	}
	if detail.InvalidReason != "" {
		if _, err := fmt.Fprintf(w, "invalid_reason: %s\n", detail.InvalidReason); err != nil {
			return err
		}
	}
	if detail.UnsupportedReason != "" {
		if _, err := fmt.Fprintf(w, "unsupported_reason: %s\n", detail.UnsupportedReason); err != nil {
			return err
		}
	}
	if len(detail.Entries) == 0 {
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 8, 2, ' ', 0)
	for _, entry := range detail.Entries {
		location := entry.Path
		if location == "" {
			location = entry.TargetLabel
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\texisted=%t\t%s\n", entry.TargetID, entry.Action, entry.Existed, location); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func writeRollbackResult(w io.Writer, result switching.ApplyRollbackResult) error {
	if _, err := fmt.Fprintf(
		w,
		"Rollback applied\noperation: %s\nsource_operation: %s\nprovider: %s\nprofile: %s\nrestored_profile: %s\nbackup: %s\nchanges: restore=%d remove=%d noop=%d\n",
		result.OperationID,
		result.SourceOperationID,
		result.ProviderID,
		result.ProfileID,
		result.RestoredProfileID,
		result.BackupPath,
		result.Counts.Restore,
		result.Counts.Remove,
		result.Counts.Noop,
	); err != nil {
		return err
	}
	for _, warning := range result.Warnings {
		if _, err := fmt.Fprintf(w, "warning: %s\n", warning); err != nil {
			return err
		}
	}
	return nil
}
