package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/strahe/profiledeck/internal/app"
	urfavecli "github.com/urfave/cli/v3"
)

const (
	authBindingFlagName = "auth-binding"
	authUpdateFlagName  = "auth-update"
)

func newCodexCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:  "codex",
		Usage: "Manage Codex provider profiles",
		Commands: []*urfavecli.Command{
			newCodexDetectCommand(),
			newCodexProfileCommand(),
		},
	}
}

func newCodexDetectCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:  "detect",
		Usage: "Detect local Codex config state",
		Flags: []urfavecli.Flag{
			stringFlag(codexDirFlagName, "Codex config directory"),
			boolFlag(jsonFlagName, "Write JSON output"),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			result, err := app.CodexDetect(ctx, app.CodexDetectRequest{
				ConfigDir: configDirValue(cmd),
				CodexDir:  cmd.String(codexDirFlagName),
			})
			if err != nil {
				return err
			}
			w := outputWriter(cmd)
			if cmd.Bool(jsonFlagName) {
				return writeJSON(w, result)
			}
			return writeCodexDetect(w, result)
		},
	}
}

func newCodexProfileCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:  "profile",
		Usage: "Manage Codex profiles",
		Commands: []*urfavecli.Command{
			newCodexProfileListCommand(),
			newCodexProfileShowCommand(),
			newCodexProfileCreateCommand(),
			newCodexProfileForkCommand(),
			newCodexProfileSyncCommand(),
		},
	}
}

func newCodexProfileListCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:  "list",
		Usage: "List stored Codex profiles",
		Flags: []urfavecli.Flag{
			boolFlag(jsonFlagName, "Write JSON output"),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			result, err := app.ListCodexProfiles(ctx, app.ListCodexProfilesRequest{
				ConfigDir: configDirValue(cmd),
			})
			if err != nil {
				return err
			}
			w := outputWriter(cmd)
			if cmd.Bool(jsonFlagName) {
				return writeJSON(w, result)
			}
			return writeCodexProfileList(w, result)
		},
	}
}

func newCodexProfileShowCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:      "show",
		Usage:     "Show a stored Codex profile",
		ArgsUsage: "<profile-id>",
		Flags: []urfavecli.Flag{
			boolFlag(jsonFlagName, "Write JSON output"),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			profileID, err := singleIDArg(cmd, app.ErrorProfileInvalid)
			if err != nil {
				return err
			}
			result, err := app.GetCodexProfile(ctx, app.GetCodexProfileRequest{
				ConfigDir: configDirValue(cmd),
				ProfileID: profileID,
			})
			if err != nil {
				return err
			}
			w := outputWriter(cmd)
			if cmd.Bool(jsonFlagName) {
				return writeJSON(w, result)
			}
			return writeCodexProfileDetail(w, result)
		},
	}
}

func newCodexProfileCreateCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:      "create",
		Usage:     "Create a Codex profile from the current Codex config and file auth",
		ArgsUsage: "<profile-id>",
		Flags: []urfavecli.Flag{
			stringFlag(codexDirFlagName, "Codex config directory"),
			stringFlag(nameFlagName, "Profile display name"),
			stringFlag(descriptionFlagName, "Profile description"),
			boolFlag(jsonFlagName, "Write JSON output"),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			profileID, err := singleIDArg(cmd, app.ErrorProfileInvalid)
			if err != nil {
				return err
			}
			result, err := app.CreateCodexProfile(ctx, app.CreateCodexProfileRequest{
				ConfigDir:   configDirValue(cmd),
				CodexDir:    cmd.String(codexDirFlagName),
				ProfileID:   profileID,
				Name:        stringFlagPtr(cmd, nameFlagName),
				Description: stringFlagPtr(cmd, descriptionFlagName),
			})
			if err != nil {
				return err
			}
			w := outputWriter(cmd)
			if cmd.Bool(jsonFlagName) {
				return writeJSON(w, result)
			}
			return writeCodexProfileSave(w, "Codex profile created", result)
		},
	}
}

func newCodexProfileForkCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:      "fork",
		Usage:     "Fork a stored Codex profile",
		ArgsUsage: "<source-profile-id> <new-profile-id>",
		Flags: []urfavecli.Flag{
			stringFlag(authBindingFlagName, "Auth binding for the fork: share-parent or copy-new"),
			stringFlag(codexDirFlagName, "Codex config directory"),
			stringFlag(nameFlagName, "Profile display name"),
			stringFlag(descriptionFlagName, "Profile description"),
			boolFlag(jsonFlagName, "Write JSON output"),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			if cmd.Args().Len() != 2 {
				return app.NewError(app.ErrorProfileInvalid, "expected source profile id and new profile id")
			}
			sourceID, appErr := appValidateCLIID(cmd.Args().Get(0), app.ErrorProfileInvalid)
			if appErr != nil {
				return appErr
			}
			profileID, appErr := appValidateCLIID(cmd.Args().Get(1), app.ErrorProfileInvalid)
			if appErr != nil {
				return appErr
			}
			result, err := app.ForkCodexProfile(ctx, app.ForkCodexProfileRequest{
				ConfigDir:       configDirValue(cmd),
				CodexDir:        cmd.String(codexDirFlagName),
				SourceProfileID: sourceID,
				ProfileID:       profileID,
				AuthBinding:     cmd.String(authBindingFlagName),
				Name:            stringFlagPtr(cmd, nameFlagName),
				Description:     stringFlagPtr(cmd, descriptionFlagName),
			})
			if err != nil {
				return err
			}
			w := outputWriter(cmd)
			if cmd.Bool(jsonFlagName) {
				return writeJSON(w, result)
			}
			return writeCodexProfileSave(w, "Codex profile forked", result)
		},
	}
}

func newCodexProfileSyncCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:      "sync",
		Usage:     "Sync a stored Codex profile from the current Codex config and file auth",
		ArgsUsage: "<profile-id>",
		Flags: []urfavecli.Flag{
			stringFlag(authUpdateFlagName, "Auth update mode for changed shared credentials: update-shared or fork-new"),
			stringFlag(codexDirFlagName, "Codex config directory"),
			boolFlag(jsonFlagName, "Write JSON output"),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			profileID, err := singleIDArg(cmd, app.ErrorProfileInvalid)
			if err != nil {
				return err
			}
			result, err := app.SyncCodexProfile(ctx, app.SyncCodexProfileRequest{
				ConfigDir:  configDirValue(cmd),
				CodexDir:   cmd.String(codexDirFlagName),
				ProfileID:  profileID,
				AuthUpdate: cmd.String(authUpdateFlagName),
			})
			if err != nil {
				return err
			}
			w := outputWriter(cmd)
			if cmd.Bool(jsonFlagName) {
				return writeJSON(w, result)
			}
			return writeCodexProfileSave(w, "Codex profile synced", result)
		},
	}
}

func appValidateCLIID(value string, code app.ErrorCode) (string, *app.AppError) {
	if strings.TrimSpace(value) == "" {
		return "", app.NewError(code, "id is required")
	}
	return strings.TrimSpace(value), nil
}

func writeCodexDetect(w io.Writer, result app.CodexDetectResult) error {
	if _, err := fmt.Fprintf(
		w,
		"Codex detect\nprovider: %s\nadapter: %s\ncodex dir: %s\nconfig: %s\nauth: %s\ncodex dir exists: %t\nconfig status: %s\nauth status: %s\nProfileDeck initialized: %t\nprovider exists: %t\nprovider adapter: %s\nprovider compatible: %t\n",
		result.ProviderID,
		result.AdapterID,
		result.CodexDir,
		result.ConfigPath,
		result.AuthPath,
		result.CodexDirExists,
		result.ConfigStatus,
		result.AuthStatus,
		result.ProfileDeckInitialized,
		result.ProviderExists,
		result.ProviderAdapterID,
		result.ProviderCompatible,
	); err != nil {
		return err
	}
	return writeWarnings(w, result.Warnings)
}

func writeCodexProfileList(w io.Writer, result app.CodexProfileListResult) error {
	if len(result.Profiles) == 0 {
		_, err := fmt.Fprintln(w, "No Codex profiles")
		return err
	}
	if _, err := fmt.Fprintf(w, "Codex profiles\ncount: %d\n", len(result.Profiles)); err != nil {
		return err
	}
	for _, profile := range result.Profiles {
		active := "idle"
		if profile.Active {
			active = "active"
		}
		if _, err := fmt.Fprintf(
			w,
			"- %s name: %s status: %s targets: %d codex_account_id: %s updated: %d\n",
			profile.Profile.ID,
			profile.Profile.Name,
			active,
			profile.TargetCount,
			profile.CodexAccountID,
			profile.UpdatedAtUnixMS,
		); err != nil {
			return err
		}
		for _, warning := range profile.Warnings {
			if _, err := fmt.Fprintf(w, "  warning: %s\n", warning); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeCodexProfileDetail(w io.Writer, detail app.CodexProfileDetail) error {
	summary := detail.Summary
	active := "false"
	if summary.Active {
		active = "true"
	}
	if _, err := fmt.Fprintf(
		w,
		"Codex profile\nprofile: %s\nname: %s\nactive: %s\nactive operation: %s\ntargets: %d\ncodex_account_id: %s\nupdated: %d\n",
		summary.Profile.ID,
		summary.Profile.Name,
		active,
		summary.ActiveOperationID,
		summary.TargetCount,
		summary.CodexAccountID,
		summary.UpdatedAtUnixMS,
	); err != nil {
		return err
	}
	if err := writeWarnings(w, summary.Warnings); err != nil {
		return err
	}
	for _, target := range detail.Targets {
		if _, err := fmt.Fprintf(
			w,
			"target: %s provider: %s format: %s strategy: %s enabled: %t path: %s\nvalue_preview: %s\n",
			target.TargetID,
			target.ProviderID,
			target.Format,
			target.Strategy,
			target.Enabled,
			target.Path,
			target.ValuePreview.Content,
		); err != nil {
			return err
		}
	}
	return nil
}

func writeCodexProfileSave(w io.Writer, title string, result app.CodexProfileSaveResult) error {
	if _, err := fmt.Fprintf(
		w,
		"%s\nprovider: %s\nprofile: %s\nconfig target: %s\nauth target: %s\ncodex dir: %s\nconfig: %s\nauth: %s\n",
		title,
		result.Provider.ID,
		result.Profile.ID,
		result.ConfigTarget.TargetID,
		result.AuthTarget.TargetID,
		result.CodexDir,
		result.ConfigPath,
		result.AuthPath,
	); err != nil {
		return err
	}
	return writeWarnings(w, result.Warnings)
}

func writeWarnings(w io.Writer, warnings []string) error {
	if len(warnings) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(w, "warnings:"); err != nil {
		return err
	}
	for _, warning := range warnings {
		if _, err := fmt.Fprintf(w, "- %s\n", warning); err != nil {
			return err
		}
	}
	return nil
}
