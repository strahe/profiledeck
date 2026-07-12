package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	urfavecli "github.com/urfave/cli/v3"

	"github.com/strahe/profiledeck/internal/app"
)

func newAntigravityCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:     "antigravity",
		Aliases:  []string{"agy"},
		Usage:    "Manage Antigravity agy v2 profiles",
		Commands: []*urfavecli.Command{newAntigravityDetectCommand(), newAntigravityProfileCommand()},
	}
}

func newAntigravityDetectCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "detect", Usage: "Detect the Antigravity agy v2 login",
		Flags: []urfavecli.Flag{boolFlag(jsonFlagName, "Write JSON output")},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			result, err := app.AntigravityDetect(ctx, app.AntigravityDetectRequest{ConfigDir: configDirValue(cmd)})
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			return writeAntigravityDetect(outputWriter(cmd), result)
		},
	}
}

func newAntigravityProfileCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "profile", Usage: "Manage Antigravity agy v2 profiles",
		Commands: []*urfavecli.Command{
			newAntigravityProfileListCommand(), newAntigravityProfileShowCommand(),
			newAntigravityProfileCreateCommand(), newAntigravityProfileUpdateCommand(),
			newAntigravityProfileSaveCurrentCommand(),
		},
	}
}

func newAntigravityProfileListCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "list", Usage: "List stored Antigravity profiles",
		Flags: []urfavecli.Flag{boolFlag(jsonFlagName, "Write JSON output")},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			result, err := app.ListAntigravityProfiles(ctx, app.ListAntigravityProfilesRequest{ConfigDir: configDirValue(cmd)})
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			return writeAntigravityProfileList(outputWriter(cmd), result)
		},
	}
}

func newAntigravityProfileShowCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "show", Usage: "Show a stored Antigravity profile", ArgsUsage: "<profile-id>",
		Flags: []urfavecli.Flag{boolFlag(jsonFlagName, "Write JSON output")},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			profileID, err := singleIDArg(cmd, app.ErrorProfileInvalid)
			if err != nil {
				return err
			}
			result, err := app.GetAntigravityProfile(ctx, app.GetAntigravityProfileRequest{ConfigDir: configDirValue(cmd), ProfileID: profileID})
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			return writeAntigravityProfileDetail(outputWriter(cmd), result)
		},
	}
}

func newAntigravityProfileCreateCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "create", Usage: "Save and activate the current agy v2 login as a Profile", ArgsUsage: "<profile-id>",
		Flags: []urfavecli.Flag{
			stringFlag(nameFlagName, "Profile display name"), stringFlag(descriptionFlagName, "Profile description"),
			boolFlag(jsonFlagName, "Write JSON output"),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			profileID, err := singleIDArg(cmd, app.ErrorProfileInvalid)
			if err != nil {
				return err
			}
			result, err := app.CreateAntigravityProfile(ctx, app.CreateAntigravityProfileRequest{
				ConfigDir: configDirValue(cmd), ProfileID: profileID,
				Name: stringFlagPtr(cmd, nameFlagName), Description: stringFlagPtr(cmd, descriptionFlagName),
			})
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			return writeAntigravityProfileSave(outputWriter(cmd), "Antigravity profile created", result)
		},
	}
}

func newAntigravityProfileUpdateCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "update", Usage: "Update Antigravity profile details", ArgsUsage: "<profile-id>",
		Flags: []urfavecli.Flag{
			stringFlag(nameFlagName, "Profile display name"), stringFlag(descriptionFlagName, "Profile description"),
			boolFlag(jsonFlagName, "Write JSON output"),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			profileID, err := singleIDArg(cmd, app.ErrorProfileInvalid)
			if err != nil {
				return err
			}
			result, err := app.UpdateAntigravityProfile(ctx, app.UpdateAntigravityProfileRequest{
				ConfigDir: configDirValue(cmd), ProfileID: profileID,
				Name: stringFlagPtr(cmd, nameFlagName), Description: stringFlagPtr(cmd, descriptionFlagName),
			})
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			return writeAntigravityProfileDetail(outputWriter(cmd), result)
		},
	}
}

func newAntigravityProfileSaveCurrentCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "save-current", Usage: "Save the current agy v2 login into the active Antigravity Profile",
		Flags: []urfavecli.Flag{boolFlag(jsonFlagName, "Write JSON output")},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			result, err := app.SaveActiveAntigravityProfile(ctx, app.SaveActiveAntigravityProfileRequest{ConfigDir: configDirValue(cmd)})
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			return writeAntigravityProfileSave(outputWriter(cmd), "Antigravity profile updated", result)
		},
	}
}

func writeAntigravityDetect(w io.Writer, result app.AntigravityDetectResult) error {
	if _, err := fmt.Fprintf(w, "Antigravity agy v2\nlogin: %s\nprovider: %s\ncompatible: %t\n", result.CredentialStatus, result.ProviderID, result.ProviderCompatible); err != nil {
		return err
	}
	return writeWarnings(w, result.Warnings)
}

func writeAntigravityProfileList(w io.Writer, result app.AntigravityProfileListResult) error {
	if len(result.Profiles) == 0 {
		_, err := fmt.Fprintln(w, "No Antigravity profiles")
		return err
	}
	if _, err := fmt.Fprintf(w, "Antigravity profiles\ncount: %d\n", len(result.Profiles)); err != nil {
		return err
	}
	for _, profile := range result.Profiles {
		status := "idle"
		if profile.Active {
			status = "active"
		}
		if _, err := fmt.Fprintf(w, "- %s name: %s status: %s expires: %s references: %d updated: %d\n",
			profile.Profile.ID, profile.Profile.Name, status, formatAntigravityExpiry(profile.ExpiresAtUnixMS),
			profile.CredentialReferenceCount, profile.UpdatedAtUnixMS); err != nil {
			return err
		}
		if err := writeWarnings(w, profile.Warnings); err != nil {
			return err
		}
	}
	return nil
}

func writeAntigravityProfileDetail(w io.Writer, detail app.AntigravityProfileDetail) error {
	summary := detail.Summary
	if _, err := fmt.Fprintf(w, "Antigravity profile\nprofile: %s\nname: %s\nactive: %t\nexpires: %s\nlogin references: %d\nupdated: %d\n",
		summary.Profile.ID, summary.Profile.Name, summary.Active, formatAntigravityExpiry(summary.ExpiresAtUnixMS),
		summary.CredentialReferenceCount, summary.UpdatedAtUnixMS); err != nil {
		return err
	}
	return writeWarnings(w, summary.Warnings)
}

func writeAntigravityProfileSave(w io.Writer, title string, result app.AntigravityProfileSaveResult) error {
	if _, err := fmt.Fprintf(w, "%s\noperation: %s\nprovider: %s\nprofile: %s\nexpires: %s\nlogin references: %d\n",
		title, result.OperationID, result.Summary.ProviderID, result.Summary.Profile.ID,
		formatAntigravityExpiry(result.Summary.ExpiresAtUnixMS), result.Summary.CredentialReferenceCount); err != nil {
		return err
	}
	return writeWarnings(w, result.Warnings)
}

func formatAntigravityExpiry(unixMS int64) string {
	if unixMS <= 0 {
		return "unknown"
	}
	return time.UnixMilli(unixMS).UTC().Format(time.RFC3339)
}
