package cli

import (
	"context"
	"fmt"
	"io"

	urfavecli "github.com/urfave/cli/v3"

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/switching"
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
			providerID, profileID, err := twoIDArgs(cmd, apperror.ProviderInvalid)
			if err != nil {
				return err
			}
			application, err := applicationFor(cmd)
			if err != nil {
				return err
			}

			w := outputWriter(cmd)
			if !cmd.Bool(yesFlagName) {
				result, err := application.Switching().BuildPlan(ctx, switching.BuildPlanRequest{
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
				return apperror.New(apperror.ConfirmationRequired, "switch apply requires --yes")
			}

			result, err := application.Switching().Apply(ctx, switching.ApplySwitchRequest{
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

func writeSwitchResult(w io.Writer, result switching.ApplySwitchResult) error {
	if _, err := fmt.Fprintf(
		w,
		"Switch applied\noperation: %s\nprovider: %s (%s)\nprofile: %s (%s)\nplan_fingerprint: %s\nchanges: create=%d update=%d noop=%d\n",
		result.OperationID,
		result.Provider.ID,
		result.Provider.Name,
		result.Profile.ID,
		result.Profile.Name,
		result.PlanFingerprint,
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
	if !result.RecoveryCleanupCompleted {
		_, err := fmt.Fprintln(
			w,
			"warning: The switch was applied, but temporary recovery files still need cleanup. Run `profiledeck doctor retry-cleanup --yes`; this does not change tool sign-ins or settings.",
		)
		return err
	}
	return nil
}
