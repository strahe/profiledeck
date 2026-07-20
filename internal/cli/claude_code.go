package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	urfavecli "github.com/urfave/cli/v3"

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/claudecode"
)

func newClaudeCodeCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "claude-code", Usage: "Manage official Claude Code subscription Profiles",
		Commands: []*urfavecli.Command{newClaudeCodeDetectCommand(), newClaudeCodeProfileCommand()},
	}
}

func newClaudeCodeDetectCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "detect", Usage: "Detect the official Claude Code subscription login",
		Flags: []urfavecli.Flag{boolFlag(jsonFlagName, "Write JSON output")},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			application, err := applicationFor(cmd)
			if err != nil {
				return err
			}
			result, err := application.ClaudeCode().Detect(ctx, claudecode.ClaudeCodeDetectRequest{})
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			return writeClaudeCodeDetect(outputWriter(cmd), result)
		},
	}
}

func newClaudeCodeProfileCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "profile", Usage: "Manage official Claude Code subscription Profiles",
		Commands: []*urfavecli.Command{
			newClaudeCodeProfileCreateCommand(), newClaudeCodeProfileListCommand(), newClaudeCodeProfileShowCommand(),
			newClaudeCodeProfileUpdateCommand(), newClaudeCodeProfileSaveCurrentCommand(), newProfileDeleteCommand(),
		},
	}
}

func newClaudeCodeProfileCreateCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "create", Usage: "Save and activate the current Claude Code subscription login as a Profile", ArgsUsage: "<profile-id>",
		Flags: []urfavecli.Flag{
			stringFlag(nameFlagName, "Profile display name"), stringFlag(descriptionFlagName, "Profile description"),
			boolFlag(jsonFlagName, "Write JSON output"),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			profileID, err := singleIDArg(cmd, apperror.ProfileInvalid)
			if err != nil {
				return err
			}
			application, err := applicationFor(cmd)
			if err != nil {
				return err
			}
			result, err := application.ClaudeCode().CreateProfile(ctx, claudecode.CreateClaudeCodeProfileRequest{
				ProfileID: profileID,
				Name:      stringFlagPtr(cmd, nameFlagName), Description: stringFlagPtr(cmd, descriptionFlagName),
			})
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			return writeClaudeCodeProfileSave(outputWriter(cmd), "Claude Code Profile created", result)
		},
	}
}

func newClaudeCodeProfileListCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "list", Usage: "List stored Claude Code Profiles",
		Flags: []urfavecli.Flag{boolFlag(jsonFlagName, "Write JSON output")},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			application, err := applicationFor(cmd)
			if err != nil {
				return err
			}
			result, err := application.ClaudeCode().ListProfiles(ctx)
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			return writeClaudeCodeProfileList(outputWriter(cmd), result)
		},
	}
}

func newClaudeCodeProfileShowCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "show", Usage: "Show a stored Claude Code Profile", ArgsUsage: "<profile-id>",
		Flags: []urfavecli.Flag{boolFlag(jsonFlagName, "Write JSON output")},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			profileID, err := singleIDArg(cmd, apperror.ProfileInvalid)
			if err != nil {
				return err
			}
			application, err := applicationFor(cmd)
			if err != nil {
				return err
			}
			result, err := application.ClaudeCode().GetProfile(ctx, claudecode.GetClaudeCodeProfileRequest{ProfileID: profileID})
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			return writeClaudeCodeProfileDetail(outputWriter(cmd), result)
		},
	}
}

func newClaudeCodeProfileUpdateCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "update", Usage: "Update Claude Code Profile details", ArgsUsage: "<profile-id>",
		Flags: []urfavecli.Flag{
			stringFlag(nameFlagName, "Profile display name"), stringFlag(descriptionFlagName, "Profile description"),
			boolFlag(jsonFlagName, "Write JSON output"),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			profileID, err := singleIDArg(cmd, apperror.ProfileInvalid)
			if err != nil {
				return err
			}
			application, err := applicationFor(cmd)
			if err != nil {
				return err
			}
			result, err := application.ClaudeCode().UpdateProfile(ctx, claudecode.UpdateClaudeCodeProfileRequest{
				ProfileID: profileID,
				Name:      stringFlagPtr(cmd, nameFlagName), Description: stringFlagPtr(cmd, descriptionFlagName),
			})
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			return writeClaudeCodeProfileDetail(outputWriter(cmd), result)
		},
	}
}

func newClaudeCodeProfileSaveCurrentCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "save-current", Usage: "Save the current Claude Code subscription login into the active Profile",
		Flags: []urfavecli.Flag{
			boolFlag(yesFlagName, "Confirm updating a login shared by multiple Profiles"),
			boolFlag(jsonFlagName, "Write JSON output"),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			application, err := applicationFor(cmd)
			if err != nil {
				return err
			}
			result, err := application.ClaudeCode().SaveActiveProfile(ctx, claudecode.SaveActiveClaudeCodeProfileRequest{ConfirmShared: cmd.Bool(yesFlagName)})
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			return writeClaudeCodeProfileSave(outputWriter(cmd), "Claude Code Profile updated", result)
		},
	}
}

func writeClaudeCodeDetect(w io.Writer, result claudecode.ClaudeCodeDetectResult) error {
	if _, err := fmt.Fprintf(w, "Claude Code\nlogin: %s\nprovider: %s\nenabled: %t\ncompatible: %t\nexpires: %s\n",
		result.CredentialStatus, result.ProviderID, result.ProviderEnabled, result.ProviderCompatible, formatClaudeCodeExpiry(result.ExpiresAtUnixMS)); err != nil {
		return err
	}
	if result.KeychainAuthorizationRequired {
		if _, err := fmt.Fprintln(w, "keychain authorization required: true"); err != nil {
			return err
		}
	}
	for _, hint := range result.ObservedAuthOverrideHints {
		if _, err := fmt.Fprintf(w, "observed auth override hint: %s\n", hint); err != nil {
			return err
		}
	}
	return writeWarnings(w, result.Warnings)
}

func writeClaudeCodeProfileList(w io.Writer, result claudecode.ClaudeCodeProfileListResult) error {
	if len(result.Profiles) == 0 {
		_, err := fmt.Fprintln(w, "No Claude Code Profiles")
		return err
	}
	if _, err := fmt.Fprintf(w, "Claude Code Profiles\ncount: %d\n", len(result.Profiles)); err != nil {
		return err
	}
	for _, summary := range result.Profiles {
		if _, err := fmt.Fprintf(w, "- %s name: %s active: %t login: %s expires: %s references: %d updated: %d\n",
			summary.Profile.ID, summary.Profile.Name, summary.Active, summary.CredentialStatus,
			formatClaudeCodeExpiry(summary.ExpiresAtUnixMS), summary.CredentialReferenceCount, summary.UpdatedAtUnixMS); err != nil {
			return err
		}
		if err := writeWarnings(w, summary.Warnings); err != nil {
			return err
		}
	}
	return nil
}

func writeClaudeCodeProfileDetail(w io.Writer, detail claudecode.ClaudeCodeProfileDetail) error {
	summary := detail.Summary
	if _, err := fmt.Fprintf(w, "Claude Code Profile\nprofile: %s\nname: %s\nactive: %t\nlogin: %s\nexpires: %s\nlogin references: %d\nupdated: %d\n",
		summary.Profile.ID, summary.Profile.Name, summary.Active, summary.CredentialStatus,
		formatClaudeCodeExpiry(summary.ExpiresAtUnixMS), summary.CredentialReferenceCount, summary.UpdatedAtUnixMS); err != nil {
		return err
	}
	return writeWarnings(w, summary.Warnings)
}

func writeClaudeCodeProfileSave(w io.Writer, title string, result claudecode.ClaudeCodeProfileSaveResult) error {
	if _, err := fmt.Fprintf(w, "%s\noperation: %s\nprovider: %s\nprofile: %s\nlogin: %s\nexpires: %s\nlogin references: %d\n",
		title, result.OperationID, result.Summary.ProviderID, result.Summary.Profile.ID, result.Summary.CredentialStatus,
		formatClaudeCodeExpiry(result.Summary.ExpiresAtUnixMS), result.Summary.CredentialReferenceCount); err != nil {
		return err
	}
	warnings := append([]string{}, result.Warnings...)
	warnings = append(warnings, result.Summary.Warnings...)
	return writeWarnings(w, warnings)
}

func formatClaudeCodeExpiry(unixMS int64) string {
	if unixMS <= 0 {
		return "unknown"
	}
	return time.UnixMilli(unixMS).UTC().Format(time.RFC3339)
}
