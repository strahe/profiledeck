package cli

import (
	"context"
	"fmt"
	"io"
	"text/tabwriter"

	urfavecli "github.com/urfave/cli/v3"

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/profiletarget"
	"github.com/strahe/profiledeck/internal/switching"
)

const (
	formatFlagName    = "format"
	pathFlagName      = "path"
	strategyFlagName  = "strategy"
	valueJSONFlagName = "value-json"
)

func newPlanCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:      "plan",
		Usage:     "Build a read-only switch plan",
		ArgsUsage: "<provider-id> <profile-id>",
		Flags: []urfavecli.Flag{
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
			result, err := application.Switching().BuildPlan(ctx, switching.BuildPlanRequest{
				ProviderID: providerID,
				ProfileID:  profileID,
			})
			if err != nil {
				return err
			}

			w := outputWriter(cmd)
			if cmd.Bool(jsonFlagName) {
				return writeJSON(w, result)
			}
			return writePlan(w, result)
		},
	}
}

func newProfileTargetCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:  "target",
		Usage: "Manage profile target files",
		Commands: []*urfavecli.Command{
			newProfileTargetAddCommand(),
			newProfileTargetListCommand(),
			newProfileTargetShowCommand(),
			newProfileTargetUpdateCommand(),
			newProfileTargetDeleteCommand(),
		},
	}
}

func newProfileTargetAddCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:      "add",
		Usage:     "Add a target file to a profile",
		ArgsUsage: "<profile-id> <target-id>",
		Flags: []urfavecli.Flag{
			stringFlag(providerFlagName, "Provider id"),
			stringFlag(pathFlagName, "Absolute target file path"),
			stringFlag(formatFlagName, "Target file format"),
			stringFlag(strategyFlagName, "Target patch strategy"),
			stringFlag(valueJSONFlagName, "Target value JSON object"),
			boolFlag(disabledFlagName, "Create the profile target disabled"),
			stringFlag(metadataJSONFlagName, "Profile target metadata JSON object"),
			boolFlag(jsonFlagName, "Write JSON output"),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			profileID, targetID, err := twoIDArgs(cmd, apperror.TargetInvalid)
			if err != nil {
				return err
			}
			enabled := !cmd.Bool(disabledFlagName)
			application, err := applicationFor(cmd)
			if err != nil {
				return err
			}
			result, err := application.Targets().Create(ctx, profiletarget.CreateProfileTargetRequest{
				ProfileID:    profileID,
				ProviderID:   cmd.String(providerFlagName),
				TargetID:     targetID,
				Path:         cmd.String(pathFlagName),
				Format:       cmd.String(formatFlagName),
				Strategy:     cmd.String(strategyFlagName),
				ValueJSON:    cmd.String(valueJSONFlagName),
				Enabled:      &enabled,
				MetadataJSON: stringFlagPtr(cmd, metadataJSONFlagName),
			})
			if err != nil {
				return err
			}
			w := outputWriter(cmd)
			if cmd.Bool(jsonFlagName) {
				return writeJSON(w, result)
			}
			return writeProfileTarget(w, result)
		},
	}
}

func newProfileTargetListCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:      "list",
		Usage:     "List profile targets",
		ArgsUsage: "<profile-id>",
		Flags: []urfavecli.Flag{
			stringFlag(providerFlagName, "Filter by provider id"),
			boolFlag(allFlagName, "Include disabled profile targets"),
			boolFlag(jsonFlagName, "Write JSON output"),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			profileID, err := singleIDArg(cmd, apperror.TargetInvalid)
			if err != nil {
				return err
			}
			application, err := applicationFor(cmd)
			if err != nil {
				return err
			}
			result, err := application.Targets().List(ctx, profiletarget.ListProfileTargetsRequest{
				ProfileID:       profileID,
				ProviderID:      cmd.String(providerFlagName),
				IncludeDisabled: cmd.Bool(allFlagName),
			})
			if err != nil {
				return err
			}
			w := outputWriter(cmd)
			if cmd.Bool(jsonFlagName) {
				return writeJSON(w, result)
			}
			return writeProfileTargetList(w, result)
		},
	}
}

func newProfileTargetShowCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:      "show",
		Usage:     "Show a profile target",
		ArgsUsage: "<profile-id> <provider-id> <target-id>",
		Flags: []urfavecli.Flag{
			boolFlag(jsonFlagName, "Write JSON output"),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			profileID, providerID, targetID, err := threeIDArgs(cmd, apperror.TargetInvalid)
			if err != nil {
				return err
			}
			application, err := applicationFor(cmd)
			if err != nil {
				return err
			}
			result, err := application.Targets().Get(ctx, profiletarget.GetProfileTargetRequest{
				ProfileID:  profileID,
				ProviderID: providerID,
				TargetID:   targetID,
			})
			if err != nil {
				return err
			}
			w := outputWriter(cmd)
			if cmd.Bool(jsonFlagName) {
				return writeJSON(w, result)
			}
			return writeProfileTarget(w, result)
		},
	}
}

func newProfileTargetUpdateCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:      "update",
		Usage:     "Update a profile target",
		ArgsUsage: "<profile-id> <provider-id> <target-id>",
		Flags: []urfavecli.Flag{
			stringFlag(pathFlagName, "Absolute target file path"),
			stringFlag(formatFlagName, "Target file format"),
			stringFlag(strategyFlagName, "Target patch strategy"),
			stringFlag(valueJSONFlagName, "Target value JSON object"),
			boolFlag(enabledFlagName, "Enable the profile target"),
			boolFlag(disabledFlagName, "Disable the profile target"),
			stringFlag(metadataJSONFlagName, "Profile target metadata JSON object"),
			boolFlag(jsonFlagName, "Write JSON output"),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			profileID, providerID, targetID, err := threeIDArgs(cmd, apperror.TargetInvalid)
			if err != nil {
				return err
			}
			enabled, err := enabledFlagPtrWithCode(cmd, apperror.TargetInvalid)
			if err != nil {
				return err
			}
			application, err := applicationFor(cmd)
			if err != nil {
				return err
			}
			result, err := application.Targets().Update(ctx, profiletarget.UpdateProfileTargetRequest{
				ProfileID:    profileID,
				ProviderID:   providerID,
				TargetID:     targetID,
				Path:         stringFlagPtr(cmd, pathFlagName),
				Format:       stringFlagPtr(cmd, formatFlagName),
				Strategy:     stringFlagPtr(cmd, strategyFlagName),
				ValueJSON:    stringFlagPtr(cmd, valueJSONFlagName),
				Enabled:      enabled,
				MetadataJSON: stringFlagPtr(cmd, metadataJSONFlagName),
			})
			if err != nil {
				return err
			}
			w := outputWriter(cmd)
			if cmd.Bool(jsonFlagName) {
				return writeJSON(w, result)
			}
			return writeProfileTarget(w, result)
		},
	}
}

func newProfileTargetDeleteCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:      "delete",
		Usage:     "Delete a profile target",
		ArgsUsage: "<profile-id> <provider-id> <target-id>",
		Flags: []urfavecli.Flag{
			boolFlag(yesFlagName, "Confirm deletion"),
			boolFlag(jsonFlagName, "Write JSON output"),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			profileID, providerID, targetID, err := threeIDArgs(cmd, apperror.TargetInvalid)
			if err != nil {
				return err
			}
			application, err := applicationFor(cmd)
			if err != nil {
				return err
			}
			result, err := application.Targets().Delete(ctx, profiletarget.DeleteProfileTargetRequest{
				ProfileID:  profileID,
				ProviderID: providerID,
				TargetID:   targetID,
				Confirm:    cmd.Bool(yesFlagName),
			})
			if err != nil {
				return err
			}
			w := outputWriter(cmd)
			if cmd.Bool(jsonFlagName) {
				return writeJSON(w, result)
			}
			_, err = fmt.Fprintf(w, "Deleted profile target %s\n", result.ID)
			return err
		},
	}
}

func twoIDArgs(cmd *urfavecli.Command, code apperror.Code) (string, string, error) {
	if cmd.Args().Len() != 2 {
		return "", "", apperror.New(code, "exactly two id arguments are required")
	}
	return cmd.Args().Get(0), cmd.Args().Get(1), nil
}

func threeIDArgs(cmd *urfavecli.Command, code apperror.Code) (string, string, string, error) {
	if cmd.Args().Len() != 3 {
		return "", "", "", apperror.New(code, "exactly three id arguments are required")
	}
	return cmd.Args().Get(0), cmd.Args().Get(1), cmd.Args().Get(2), nil
}

func writeProfileTargetList(w io.Writer, targets []profiletarget.ProfileTarget) error {
	if len(targets) == 0 {
		_, err := fmt.Fprintln(w, "No profile targets")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 8, 2, ' ', 0)
	for _, target := range targets {
		if _, err := fmt.Fprintf(
			tw,
			"%s\t%s\t%s\t%s\tenabled=%t\t%s\n",
			target.TargetID,
			target.ProviderID,
			target.Format,
			target.Strategy,
			target.Enabled,
			target.Path,
		); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func writeProfileTarget(w io.Writer, target profiletarget.ProfileTarget) error {
	metadata, err := compactJSON(target.Metadata)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(
		w,
		"profile_id: %s\nprovider_id: %s\ntarget_id: %s\npath: %s\nformat: %s\nstrategy: %s\nenabled: %t\nvalue_preview: %s\nvalue_preview_truncated: %t\nmetadata: %s\ncreated_at_unix_ms: %d\nupdated_at_unix_ms: %d\n",
		target.ProfileID,
		target.ProviderID,
		target.TargetID,
		target.Path,
		target.Format,
		target.Strategy,
		target.Enabled,
		target.ValuePreview.Content,
		target.ValuePreview.Truncated,
		metadata,
		target.CreatedAtUnixMS,
		target.UpdatedAtUnixMS,
	)
	return err
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
