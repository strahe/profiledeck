package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/strahe/profiledeck/internal/app"
	urfavecli "github.com/urfave/cli/v3"
)

const planFingerprintFlagName = "plan-fingerprint"

func newSwitchCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:      "switch",
		Usage:     "Apply a profile switch",
		ArgsUsage: "<provider-id> <profile-id>",
		Flags: []urfavecli.Flag{
			boolFlag(yesFlagName, "Confirm switch apply"),
			stringFlag(planFingerprintFlagName, "Require a matching read-only plan fingerprint"),
			boolFlag(jsonFlagName, "Write JSON output"),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			providerID, profileID, err := twoIDArgs(cmd, app.ErrorProviderInvalid)
			if err != nil {
				return err
			}

			w := outputWriter(cmd)
			if !cmd.Bool(yesFlagName) {
				result, err := app.BuildPlan(ctx, app.BuildPlanRequest{
					ConfigDir:  configDirValue(cmd),
					ProviderID: providerID,
					ProfileID:  profileID,
				})
				if err != nil {
					return err
				}
				if cmd.Bool(jsonFlagName) {
					if err := writeJSON(w, result); err != nil {
						return err
					}
				} else if err := writePlan(w, result); err != nil {
					return err
				}
				return app.NewError(app.ErrorConfirmationRequired, "switch apply requires --yes")
			}

			result, err := app.ApplySwitch(ctx, app.ApplySwitchRequest{
				ConfigDir:               configDirValue(cmd),
				ProviderID:              providerID,
				ProfileID:               profileID,
				Confirm:                 true,
				ExpectedPlanFingerprint: cmd.String(planFingerprintFlagName),
			})
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(w, result)
			}
			return writeSwitchResult(w, result)
		},
	}
}

func writeSwitchResult(w io.Writer, result app.ApplySwitchResult) error {
	if _, err := fmt.Fprintf(
		w,
		"Switch applied\noperation: %s\nprovider: %s (%s)\nprofile: %s (%s)\nplan_fingerprint: %s\nbackup: %s\nchanges: create=%d update=%d noop=%d\n",
		result.OperationID,
		result.Provider.ID,
		result.Provider.Name,
		result.Profile.ID,
		result.Profile.Name,
		result.PlanFingerprint,
		result.BackupPath,
		result.Counts.Create,
		result.Counts.Update,
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
