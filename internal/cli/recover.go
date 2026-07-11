package cli

import (
	"context"
	"fmt"
	"io"

	urfavecli "github.com/urfave/cli/v3"

	"github.com/strahe/profiledeck/internal/app"
)

func newRecoverCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:      "recover",
		Usage:     "Recover a failed switch from its backup checkpoint",
		ArgsUsage: "<switch-operation-id>",
		Flags: []urfavecli.Flag{
			boolFlag(yesFlagName, "Confirm failed switch recovery"),
			boolFlag(jsonFlagName, "Write JSON output"),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			operationID, err := singleIDArg(cmd, app.ErrorRecoveryUnsupported)
			if err != nil {
				return err
			}
			result, err := app.RecoverFailedSwitch(ctx, app.RecoverFailedSwitchParams{
				ConfigDir:   configDirValue(cmd),
				OperationID: operationID,
				Confirm:     cmd.Bool(yesFlagName),
			})
			if err != nil {
				return err
			}
			w := outputWriter(cmd)
			if cmd.Bool(jsonFlagName) {
				return writeJSON(w, result)
			}
			return writeRecoverResult(w, result)
		},
	}
}

func writeRecoverResult(w io.Writer, result app.RecoverFailedSwitchResult) error {
	if _, err := fmt.Fprintf(
		w,
		"Recovery applied\noperation: %s\nsource_operation: %s\noperation_type: %s\nrollback_kind: %s\nprovider: %s\nprofile: %s\nrestored_profile: %s\nbackup: %s\nchanges: restore=%d remove=%d noop=%d\n",
		result.OperationID,
		result.SourceOperationID,
		result.OperationType,
		result.RollbackKind,
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
