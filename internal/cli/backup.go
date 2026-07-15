package cli

import (
	"context"
	"fmt"
	"io"
	"text/tabwriter"

	urfavecli "github.com/urfave/cli/v3"

	"github.com/strahe/profiledeck/internal/appbackup"
	"github.com/strahe/profiledeck/internal/apperror"
)

const (
	backupFileFlagName = "file"
	replaceFlagName    = "replace"
)

func newBackupCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:  "backup",
		Usage: "Back up and restore ProfileDeck application data",
		Commands: []*urfavecli.Command{
			newBackupCreateCommand(),
			newBackupListCommand(),
			newBackupShowCommand(),
			newBackupExportCommand(),
			newBackupRestoreCommand(),
			newBackupDeleteCommand(),
			newBackupKeyCommand(),
		},
	}
}

func newBackupCreateCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "create", Usage: "Create an encrypted application backup",
		Flags: []urfavecli.Flag{boolFlag(jsonFlagName, "Write JSON output")},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			application, err := applicationFor(cmd)
			if err != nil {
				return err
			}
			result, err := application.Backups().Create(ctx, appbackup.CreateRequest{
				Kind: appbackup.KindManual, Reason: appbackup.ReasonManual,
			})
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			return writeBackupDetail(outputWriter(cmd), result, "Application backup created")
		},
	}
}

func newBackupListCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "list", Usage: "List application backups",
		Flags: []urfavecli.Flag{boolFlag(jsonFlagName, "Write JSON output")},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			application, err := applicationFor(cmd)
			if err != nil {
				return err
			}
			result, err := application.Backups().List(ctx)
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			return writeBackupList(outputWriter(cmd), result)
		},
	}
}

func newBackupShowCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "show", Usage: "Show an application backup", ArgsUsage: "<backup-id>",
		Flags: []urfavecli.Flag{boolFlag(jsonFlagName, "Write JSON output")},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			backupID, err := singleIDArg(cmd, apperror.BackupInvalid)
			if err != nil {
				return err
			}
			application, err := applicationFor(cmd)
			if err != nil {
				return err
			}
			result, err := application.Backups().Show(ctx, backupID)
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			return writeBackupDetail(outputWriter(cmd), result, "Application backup")
		},
	}
}

func newBackupExportCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "export", Usage: "Copy an encrypted application backup to a private file", ArgsUsage: "<backup-id>",
		Flags: []urfavecli.Flag{
			requiredStringFlag(outputFlagName, "Write the encrypted backup to this file"),
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
			result, err := application.Backups().Export(ctx, appbackup.ExportRequest{
				BackupID: backupID, OutputPath: cmd.String(outputFlagName),
			})
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			_, err = fmt.Fprintf(outputWriter(cmd), "Application backup exported\nbackup: %s\noutput: %s\n", result.Backup.ID, result.Path)
			return err
		},
	}
}

func newBackupRestoreCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "restore", Usage: "Restore ProfileDeck application data", ArgsUsage: "[<backup-id>]",
		Flags: []urfavecli.Flag{
			stringFlag(backupFileFlagName, "Restore from an exported encrypted backup file"),
			boolFlag(yesFlagName, "Confirm application data restore"),
			boolFlag(jsonFlagName, "Write JSON output"),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			source, err := backupRestoreSource(cmd)
			if err != nil {
				return err
			}
			application, err := applicationFor(cmd)
			if err != nil {
				return err
			}
			preview, err := application.Backups().PreviewRestore(ctx, source)
			if err != nil {
				return err
			}
			if !cmd.Bool(yesFlagName) {
				if cmd.Bool(jsonFlagName) {
					if err := writeJSON(outputWriter(cmd), preview); err != nil {
						return err
					}
				} else if err := writeRestorePreview(outputWriter(cmd), preview); err != nil {
					return err
				}
				return apperror.New(apperror.ConfirmationRequired, "application data restore requires --yes")
			}
			result, err := application.Backups().Restore(ctx, appbackup.RestoreRequest{
				Source: source, ExpectedFingerprint: preview.Fingerprint, Confirm: true,
			})
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			return writeRestoreResult(outputWriter(cmd), result)
		},
	}
}

func newBackupDeleteCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "delete", Usage: "Delete an application backup", ArgsUsage: "<backup-id>",
		Flags: []urfavecli.Flag{boolFlag(yesFlagName, "Confirm application backup deletion")},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			backupID, err := singleIDArg(cmd, apperror.BackupInvalid)
			if err != nil {
				return err
			}
			application, err := applicationFor(cmd)
			if err != nil {
				return err
			}
			if err := application.Backups().Delete(ctx, appbackup.DeleteRequest{
				BackupID: backupID, Confirm: cmd.Bool(yesFlagName),
			}); err != nil {
				return err
			}
			_, err = fmt.Fprintf(outputWriter(cmd), "Application backup deleted\nbackup: %s\n", backupID)
			return err
		},
	}
}

func newBackupKeyCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "key", Usage: "Manage the application backup recovery key",
		Commands: []*urfavecli.Command{
			{
				Name: "status", Usage: "Show recovery key status",
				Flags:  []urfavecli.Flag{boolFlag(jsonFlagName, "Write JSON output")},
				Action: backupKeyStatusAction,
			},
			{
				Name: "export", Usage: "Export the recovery key to a private file",
				Flags: []urfavecli.Flag{
					requiredStringFlag(outputFlagName, "Write the recovery key to this file"),
					boolFlag(yesFlagName, "Confirm recovery key export"),
					boolFlag(jsonFlagName, "Write JSON output"),
				},
				Action: backupKeyExportAction,
			},
			{
				Name: "import", Usage: "Import a recovery key from a private file",
				Flags: []urfavecli.Flag{
					requiredStringFlag(backupFileFlagName, "Read the recovery key from this file"),
					boolFlag(replaceFlagName, "Replace a different current recovery key"),
					boolFlag(yesFlagName, "Confirm recovery key import"),
					boolFlag(jsonFlagName, "Write JSON output"),
				},
				Action: backupKeyImportAction,
			},
		},
	}
}

func backupKeyStatusAction(ctx context.Context, cmd *urfavecli.Command) error {
	application, err := applicationFor(cmd)
	if err != nil {
		return err
	}
	result, err := application.Backups().KeyStatus(ctx)
	if err != nil {
		return err
	}
	if cmd.Bool(jsonFlagName) {
		return writeJSON(outputWriter(cmd), result)
	}
	if !result.Available {
		_, err = fmt.Fprintln(outputWriter(cmd), "Application backup recovery key is not available")
		return err
	}
	_, err = fmt.Fprintf(outputWriter(cmd), "Application backup recovery key is available\nrecipient: %s\n", result.Recipient)
	return err
}

func backupKeyExportAction(ctx context.Context, cmd *urfavecli.Command) error {
	application, err := applicationFor(cmd)
	if err != nil {
		return err
	}
	result, err := application.Backups().ExportKey(ctx, appbackup.ExportKeyRequest{
		OutputPath: cmd.String(outputFlagName), Confirm: cmd.Bool(yesFlagName),
	})
	if err != nil {
		return err
	}
	if cmd.Bool(jsonFlagName) {
		return writeJSON(outputWriter(cmd), result)
	}
	_, err = fmt.Fprintf(outputWriter(cmd), "Recovery key exported\noutput: %s\nrecipient: %s\n", result.Path, result.Recipient)
	return err
}

func backupKeyImportAction(ctx context.Context, cmd *urfavecli.Command) error {
	application, err := applicationFor(cmd)
	if err != nil {
		return err
	}
	result, err := application.Backups().ImportKey(ctx, appbackup.ImportKeyRequest{
		InputPath: cmd.String(backupFileFlagName), Replace: cmd.Bool(replaceFlagName), Confirm: cmd.Bool(yesFlagName),
	})
	if err != nil {
		return err
	}
	if cmd.Bool(jsonFlagName) {
		return writeJSON(outputWriter(cmd), result)
	}
	_, err = fmt.Fprintf(outputWriter(cmd), "Recovery key imported\nrecipient: %s\nchanged: %t\n", result.Recipient, result.Changed)
	return err
}

func backupRestoreSource(cmd *urfavecli.Command) (appbackup.RestoreSource, error) {
	filePath := cmd.String(backupFileFlagName)
	if cmd.Args().Len() > 1 || (cmd.Args().Len() == 1 && filePath != "") || (cmd.Args().Len() == 0 && filePath == "") {
		return appbackup.RestoreSource{}, apperror.New(apperror.BackupInvalid, "provide one backup id or --file")
	}
	if filePath != "" {
		return appbackup.RestoreSource{FilePath: filePath}, nil
	}
	return appbackup.RestoreSource{BackupID: cmd.Args().First()}, nil
}

func writeBackupList(w io.Writer, result appbackup.ListResult) error {
	if len(result.Backups) == 0 {
		if _, err := fmt.Fprintln(w, "No application backups"); err != nil {
			return err
		}
	} else {
		tw := tabwriter.NewWriter(w, 0, 8, 2, ' ', 0)
		for _, backup := range result.Backups {
			if _, err := fmt.Fprintf(tw, "%s\t%s\tcreated=%d\tsize=%d\n", backup.ID, backup.Kind, backup.CreatedAtUnixMS, backup.SizeBytes); err != nil {
				return err
			}
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	if result.AutomaticCleanupRequired {
		_, err := fmt.Fprintln(w, "warning: More than 10 automatic backups are stored. Delete backups you no longer need.")
		return err
	}
	return nil
}

func writeBackupDetail(w io.Writer, detail appbackup.BackupDetail, title string) error {
	_, err := fmt.Fprintf(w, "%s\nbackup: %s\nkind: %s\nreason: %s\ncreated: %d\nsize: %d\nformat: %d\n",
		title, detail.ID, detail.Kind, detail.Reason, detail.CreatedAtUnixMS, detail.SizeBytes, detail.FormatVersion)
	return err
}

func writeRestorePreview(w io.Writer, preview appbackup.RestorePreview) error {
	if err := writeBackupDetail(w, preview.Backup, "Application restore preview"); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "current_database_healthy: %t\nschema_upgrade_required: %t\n",
		preview.CurrentDatabaseHealthy, preview.SchemaUpgradeRequired)
	return err
}

func writeRestoreResult(w io.Writer, result appbackup.RestoreResult) error {
	if _, err := fmt.Fprintf(w, "Application data restored\nbackup: %s\nrestart_required: %t\n", result.Backup.ID, result.RestartRequired); err != nil {
		return err
	}
	if result.SafetyBackup != nil {
		if _, err := fmt.Fprintf(w, "safety_backup: %s\n", result.SafetyBackup.ID); err != nil {
			return err
		}
	} else if result.SafetyBackupSkipped {
		if _, err := fmt.Fprintln(w, "warning: The damaged current database could not be backed up before restore."); err != nil {
			return err
		}
	}
	if !result.RecoveryCleanupCompleted {
		_, err := fmt.Fprintln(w, "warning: Old operation recovery files could not be removed.")
		return err
	}
	return nil
}

func requiredStringFlag(name, usage string) *urfavecli.StringFlag {
	return &urfavecli.StringFlag{
		Name: name, Usage: usage, Required: true,
		Config: urfavecli.StringConfig{TrimSpace: true},
	}
}
