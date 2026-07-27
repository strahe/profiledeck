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

const (
	dryRunFlagName          = "dry-run"
	planFingerprintFlagName = "plan-fingerprint"
)

func newSwitchCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:      "switch",
		Usage:     "Preview or apply a profile switch",
		ArgsUsage: "<provider-id> <profile-id>",
		Flags: []urfavecli.Flag{
			boolFlag(dryRunFlagName, "Preview the switch without making changes"),
			boolFlag(yesFlagName, "Confirm switch apply"),
			stringFlag(planFingerprintFlagName, "Require the applied switch to match a preview fingerprint"),
			boolFlag(jsonFlagName, "Write JSON output"),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			// Mode selection must be explicit before ProfileDeck opens application state.
			dryRun := cmd.Bool(dryRunFlagName)
			apply := cmd.Bool(yesFlagName)
			if dryRun && apply {
				return apperror.New(apperror.ConfirmationRequired, "--dry-run and --yes cannot be used together")
			}
			if cmd.IsSet(planFingerprintFlagName) && !apply {
				return apperror.New(apperror.ConfirmationRequired, "--plan-fingerprint can only be used with --yes")
			}
			if !dryRun && !apply {
				return apperror.New(apperror.ConfirmationRequired, "switch requires either --dry-run to preview or --yes to apply")
			}

			providerID, profileID, err := twoIDArgs(cmd, apperror.ProviderInvalid)
			if err != nil {
				return err
			}
			application, err := applicationFor(cmd)
			if err != nil {
				return err
			}

			w := outputWriter(cmd)
			if dryRun {
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
				return nil
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

func writePlan(w io.Writer, plan switching.SwitchPlan) error {
	if _, err := fmt.Fprintf(
		w,
		"Switch plan\nprovider: %s (%s)\nprofile: %s (%s)\nplan_fingerprint: %s\noperations: %d\n",
		plan.Provider.ID,
		plan.Provider.Name,
		plan.Profile.ID,
		plan.Profile.Name,
		plan.PlanFingerprint,
		len(plan.Operations),
	); err != nil {
		return err
	}
	if len(plan.Operations) > 0 {
		tw := tabwriter.NewWriter(w, 0, 8, 2, ' ', 0)
		for _, op := range plan.Operations {
			location := op.Path
			if location == "" {
				location = op.TargetLabel
			}
			if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", op.TargetID, op.Action, op.StatusReason, location); err != nil {
				return err
			}
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	for _, warning := range plan.Warnings {
		if _, err := fmt.Fprintf(w, "warning: %s\n", warning); err != nil {
			return err
		}
	}
	return nil
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
			"warning: The switch was applied, but temporary recovery files still need cleanup. Run `profiledeck-cli doctor retry-cleanup --yes`; this does not change tool sign-ins or settings.",
		)
		return err
	}
	return nil
}
