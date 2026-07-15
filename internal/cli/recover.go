package cli

import (
	"context"
	"fmt"
	"io"

	urfavecli "github.com/urfave/cli/v3"

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/switching"
)

func newRecoverCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:      "recover",
		Usage:     "Resolve an incomplete profile switch",
		ArgsUsage: "<switch-operation-id>",
		Flags: []urfavecli.Flag{
			boolFlag(yesFlagName, "Confirm operation recovery"),
			boolFlag(jsonFlagName, "Write JSON output"),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			operationID, err := singleIDArg(cmd, apperror.RecoveryUnsupported)
			if err != nil {
				return err
			}
			application, err := applicationFor(cmd)
			if err != nil {
				return err
			}
			result, err := application.Switching().RecoverOperation(ctx, switching.RecoverOperationParams{
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

func writeRecoverResult(w io.Writer, result switching.RecoverOperationResult) error {
	if _, err := fmt.Fprintf(w,
		"Operation resolved\nsource_operation: %s\nrecovery_operation: %s\naction: %s\nprovider: %s\nprofile: %s\nrestored_profile: %s\nchanges: restore=%d remove=%d noop=%d\n",
		result.SourceOperationID,
		result.RecoveryOperationID,
		result.Action,
		result.ProviderID,
		result.ProfileID,
		result.RestoredProfileID,
		result.Counts.Restore,
		result.Counts.Remove,
		result.Counts.Noop,
	); err != nil {
		return err
	}
	if !result.RecoveryCleanupCompleted {
		_, err := fmt.Fprintln(w, "warning: Operation recovery files could not be removed.")
		return err
	}
	return nil
}
