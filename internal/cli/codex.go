package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	urfavecli "github.com/urfave/cli/v3"

	"github.com/strahe/profiledeck/internal/app"
)

const (
	credentialBindingFlagName    = "credential-binding"
	configBindingFlagName        = "config-binding"
	newConfigSetFlagName         = "new-config-set"
	configSetNameFlagName        = "config-set-name"
	configSetDescriptionFlagName = "config-set-description"
)

func newCodexCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "codex", Usage: "Manage Codex profiles and config sets",
		Commands: []*urfavecli.Command{newCodexDetectCommand(), newCodexProfileCommand(), newCodexConfigSetCommand()},
	}
}

func newCodexDetectCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "detect", Usage: "Detect local Codex config state",
		Flags: []urfavecli.Flag{stringFlag(codexDirFlagName, "Codex config directory"), boolFlag(jsonFlagName, "Write JSON output")},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			result, err := app.CodexDetect(ctx, app.CodexDetectRequest{ConfigDir: configDirValue(cmd), CodexDir: cmd.String(codexDirFlagName)})
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			return writeCodexDetect(outputWriter(cmd), result)
		},
	}
}

func newCodexProfileCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "profile", Usage: "Manage Codex profiles",
		Commands: []*urfavecli.Command{
			newCodexProfileListCommand(), newCodexProfileShowCommand(), newCodexProfileCreateCommand(),
			newCodexProfileForkCommand(), newCodexProfileSaveCurrentCommand(), newCodexProfileSetConfigCommand(),
			newCodexProfileExportCommand(), newCodexProfileImportCommand(),
		},
	}
}

func newCodexProfileListCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "list", Usage: "List stored Codex profiles", Flags: []urfavecli.Flag{boolFlag(jsonFlagName, "Write JSON output")},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			result, err := app.ListCodexProfiles(ctx, app.ListCodexProfilesRequest{ConfigDir: configDirValue(cmd)})
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			return writeCodexProfileList(outputWriter(cmd), result)
		},
	}
}

func newCodexProfileShowCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "show", Usage: "Show a stored Codex profile", ArgsUsage: "<profile-id>",
		Flags: []urfavecli.Flag{boolFlag(jsonFlagName, "Write JSON output")},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			profileID, err := singleIDArg(cmd, app.ErrorProfileInvalid)
			if err != nil {
				return err
			}
			result, err := app.GetCodexProfile(ctx, app.GetCodexProfileRequest{ConfigDir: configDirValue(cmd), ProfileID: profileID})
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			return writeCodexProfileDetail(outputWriter(cmd), result)
		},
	}
}

func newCodexProfileCreateCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "create", Usage: "Create and activate a Codex profile from the current working copy", ArgsUsage: "<profile-id>",
		Flags: []urfavecli.Flag{
			stringFlag(codexDirFlagName, "Codex config directory"), stringFlag(nameFlagName, "Profile display name"),
			stringFlag(descriptionFlagName, "Profile description"), stringFlag(newConfigSetFlagName, "Save current config as a new Config Set with this id"),
			stringFlag(configSetNameFlagName, "New Config Set display name"), stringFlag(configSetDescriptionFlagName, "New Config Set description"),
			boolFlag(jsonFlagName, "Write JSON output"),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			profileID, err := singleIDArg(cmd, app.ErrorProfileInvalid)
			if err != nil {
				return err
			}
			result, err := app.CreateCodexProfile(ctx, app.CreateCodexProfileRequest{
				ConfigDir: configDirValue(cmd), CodexDir: cmd.String(codexDirFlagName), ProfileID: profileID,
				Name: stringFlagPtr(cmd, nameFlagName), Description: stringFlagPtr(cmd, descriptionFlagName),
				NewConfigSetID: cmd.String(newConfigSetFlagName), NewConfigSetName: stringFlagPtr(cmd, configSetNameFlagName),
				NewConfigSetDescription: stringFlagPtr(cmd, configSetDescriptionFlagName),
			})
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			return writeCodexProfileSave(outputWriter(cmd), "Codex profile created", result)
		},
	}
}

func newCodexProfileForkCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "fork", Usage: "Fork a Codex profile with explicit resource sharing", ArgsUsage: "<source-profile-id> <new-profile-id>",
		Flags: []urfavecli.Flag{
			stringFlag(credentialBindingFlagName, "Credential binding: share-parent or copy-new"),
			stringFlag(configBindingFlagName, "Config binding: share-parent or copy-new"),
			stringFlag(newConfigSetFlagName, "New Config Set id when config binding is copy-new"),
			stringFlag(configSetNameFlagName, "New Config Set display name"), stringFlag(configSetDescriptionFlagName, "New Config Set description"),
			stringFlag(codexDirFlagName, "Codex config directory"), stringFlag(nameFlagName, "Profile display name"),
			stringFlag(descriptionFlagName, "Profile description"), boolFlag(jsonFlagName, "Write JSON output"),
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
				ConfigDir: configDirValue(cmd), CodexDir: cmd.String(codexDirFlagName), SourceProfileID: sourceID, ProfileID: profileID,
				CredentialBinding: cmd.String(credentialBindingFlagName), ConfigBinding: cmd.String(configBindingFlagName),
				NewConfigSetID: cmd.String(newConfigSetFlagName), NewConfigSetName: stringFlagPtr(cmd, configSetNameFlagName),
				NewConfigSetDescription: stringFlagPtr(cmd, configSetDescriptionFlagName),
				Name:                    stringFlagPtr(cmd, nameFlagName), Description: stringFlagPtr(cmd, descriptionFlagName),
			})
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			return writeCodexProfileSave(outputWriter(cmd), "Codex profile forked", result)
		},
	}
}

func newCodexProfileSaveCurrentCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "save-current", Usage: "Save current Codex auth and config into the active profile",
		Flags: []urfavecli.Flag{stringFlag(codexDirFlagName, "Codex config directory"), boolFlag(jsonFlagName, "Write JSON output")},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			result, err := app.SaveActiveCodexProfileState(ctx, app.SaveActiveCodexProfileStateRequest{ConfigDir: configDirValue(cmd), CodexDir: cmd.String(codexDirFlagName)})
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			_, err = fmt.Fprintf(outputWriter(cmd), "Codex profile state saved\nprofile: %s\nconfig set: %s\ncredential references: %d\n", result.ProfileID, result.ConfigSet.ID, result.CredentialReferenceCount)
			if err != nil {
				return err
			}
			return writeWarnings(outputWriter(cmd), result.Warnings)
		},
	}
}

func newCodexProfileSetConfigCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "set-config", Usage: "Bind an inactive Codex profile to a Config Set", ArgsUsage: "<profile-id> <config-set-id>",
		Flags: []urfavecli.Flag{boolFlag(jsonFlagName, "Write JSON output")},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			if cmd.Args().Len() != 2 {
				return app.NewError(app.ErrorProfileInvalid, "expected profile id and config set id")
			}
			result, err := app.UpdateCodexProfileConfigSet(ctx, app.UpdateCodexProfileConfigSetRequest{
				ConfigDir: configDirValue(cmd), ProfileID: cmd.Args().Get(0), ConfigSetID: cmd.Args().Get(1),
			})
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			return writeCodexProfileDetail(outputWriter(cmd), result)
		},
	}
}

func newCodexConfigSetCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "config-set", Usage: "Manage Codex Config Sets",
		Commands: []*urfavecli.Command{
			newCodexConfigSetListCommand(), newCodexConfigSetShowCommand(), newCodexConfigSetCreateCommand(),
			newCodexConfigSetCopyCommand(), newCodexConfigSetUpdateCommand(), newCodexConfigSetDeleteCommand(),
		},
	}
}

func newCodexConfigSetListCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "list", Usage: "List Codex Config Sets", Flags: []urfavecli.Flag{boolFlag(jsonFlagName, "Write JSON output")},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			result, err := app.ListCodexConfigSets(ctx, app.ListCodexConfigSetsRequest{ConfigDir: configDirValue(cmd)})
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			return writeCodexConfigSetList(outputWriter(cmd), result)
		},
	}
}

func newCodexConfigSetShowCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "show", Usage: "Show a Codex Config Set summary", ArgsUsage: "<config-set-id>",
		Flags: []urfavecli.Flag{boolFlag(jsonFlagName, "Write JSON output")},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			id, err := singleIDArg(cmd, app.ErrorCodexInvalid)
			if err != nil {
				return err
			}
			result, err := app.GetCodexConfigSet(ctx, app.GetCodexConfigSetRequest{ConfigDir: configDirValue(cmd), ConfigSetID: id})
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			return writeCodexConfigSet(outputWriter(cmd), result)
		},
	}
}

func newCodexConfigSetCreateCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "create", Usage: "Create a Config Set from the current config.toml", ArgsUsage: "<config-set-id>",
		Flags: []urfavecli.Flag{stringFlag(codexDirFlagName, "Codex config directory"), stringFlag(nameFlagName, "Config Set name"), stringFlag(descriptionFlagName, "Config Set description"), boolFlag(jsonFlagName, "Write JSON output")},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			id, err := singleIDArg(cmd, app.ErrorCodexInvalid)
			if err != nil {
				return err
			}
			name := cmd.String(nameFlagName)
			if name == "" {
				name = id
			}
			result, err := app.CreateCodexConfigSet(ctx, app.CreateCodexConfigSetRequest{ConfigDir: configDirValue(cmd), CodexDir: cmd.String(codexDirFlagName), ConfigSetID: id, Name: name, Description: cmd.String(descriptionFlagName)})
			if err != nil {
				return err
			}
			return writeCodexConfigSetCommandResult(cmd, result)
		},
	}
}

func newCodexConfigSetCopyCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "copy", Usage: "Copy a Codex Config Set", ArgsUsage: "<source-id> <new-id>",
		Flags: []urfavecli.Flag{stringFlag(nameFlagName, "Config Set name"), stringFlag(descriptionFlagName, "Config Set description"), boolFlag(jsonFlagName, "Write JSON output")},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			if cmd.Args().Len() != 2 {
				return app.NewError(app.ErrorCodexInvalid, "expected source and destination config set ids")
			}
			name := cmd.String(nameFlagName)
			if name == "" {
				name = cmd.Args().Get(1)
			}
			result, err := app.CopyCodexConfigSet(ctx, app.CopyCodexConfigSetRequest{ConfigDir: configDirValue(cmd), SourceConfigSetID: cmd.Args().Get(0), ConfigSetID: cmd.Args().Get(1), Name: name, Description: cmd.String(descriptionFlagName)})
			if err != nil {
				return err
			}
			return writeCodexConfigSetCommandResult(cmd, result)
		},
	}
}

func newCodexConfigSetUpdateCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "update", Usage: "Rename or describe a Codex Config Set", ArgsUsage: "<config-set-id>",
		Flags: []urfavecli.Flag{stringFlag(nameFlagName, "Config Set name"), stringFlag(descriptionFlagName, "Config Set description"), boolFlag(jsonFlagName, "Write JSON output")},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			id, err := singleIDArg(cmd, app.ErrorCodexInvalid)
			if err != nil {
				return err
			}
			result, err := app.UpdateCodexConfigSet(ctx, app.UpdateCodexConfigSetRequest{ConfigDir: configDirValue(cmd), ConfigSetID: id, Name: stringFlagPtr(cmd, nameFlagName), Description: stringFlagPtr(cmd, descriptionFlagName)})
			if err != nil {
				return err
			}
			return writeCodexConfigSetCommandResult(cmd, result)
		},
	}
}

func newCodexConfigSetDeleteCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "delete", Usage: "Delete an unreferenced Codex Config Set", ArgsUsage: "<config-set-id>",
		Flags: []urfavecli.Flag{boolFlag(yesFlagName, "Confirm deletion"), boolFlag(jsonFlagName, "Write JSON output")},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			id, err := singleIDArg(cmd, app.ErrorCodexInvalid)
			if err != nil {
				return err
			}
			if !cmd.Bool(yesFlagName) {
				return app.NewError(app.ErrorConfirmationRequired, "Config Set deletion requires --yes")
			}
			if err := app.DeleteCodexConfigSet(ctx, app.DeleteCodexConfigSetRequest{ConfigDir: configDirValue(cmd), ConfigSetID: id}); err != nil {
				return err
			}
			result := map[string]any{"deleted": true, "config_set_id": id}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			_, err = fmt.Fprintf(outputWriter(cmd), "Codex Config Set deleted\nid: %s\n", id)
			return err
		},
	}
}

func writeCodexConfigSetCommandResult(cmd *urfavecli.Command, result app.CodexConfigSet) error {
	if cmd.Bool(jsonFlagName) {
		return writeJSON(outputWriter(cmd), result)
	}
	return writeCodexConfigSet(outputWriter(cmd), result)
}

func appValidateCLIID(value string, code app.ErrorCode) (string, *app.AppError) {
	if strings.TrimSpace(value) == "" {
		return "", app.NewError(code, "id is required")
	}
	return strings.TrimSpace(value), nil
}

func writeCodexDetect(w io.Writer, result app.CodexDetectResult) error {
	if _, err := fmt.Fprintf(w, "Codex detect\nprovider: %s\nadapter: %s\ncodex dir: %s\nconfig: %s\nauth: %s\ncodex dir exists: %t\nconfig status: %s\nauth status: %s\nProfileDeck initialized: %t\nprovider exists: %t\nprovider adapter: %s\nprovider compatible: %t\n",
		result.ProviderID, result.AdapterID, result.CodexDir, result.ConfigPath, result.AuthPath, result.CodexDirExists,
		result.ConfigStatus, result.AuthStatus, result.ProfileDeckInitialized, result.ProviderExists, result.ProviderAdapterID, result.ProviderCompatible); err != nil {
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
		status := "idle"
		if profile.Active {
			status = "active"
		}
		if _, err := fmt.Fprintf(w, "- %s name: %s status: %s account: %s config_set: %s updated: %d\n",
			profile.Profile.ID, profile.Profile.Name, status, profile.CodexAccountID, profile.ConfigSetName, profile.UpdatedAtUnixMS); err != nil {
			return err
		}
		if err := writeWarnings(w, profile.Warnings); err != nil {
			return err
		}
	}
	return nil
}

func writeCodexProfileDetail(w io.Writer, detail app.CodexProfileDetail) error {
	summary := detail.Summary
	if _, err := fmt.Fprintf(w, "Codex profile\nprofile: %s\nname: %s\nactive: %t\naccount: %s\nconfig set: %s (%s)\nmodel: %s\nprovider: %s\nupdated: %d\n",
		summary.Profile.ID, summary.Profile.Name, summary.Active, summary.CodexAccountID, summary.ConfigSetName, summary.ConfigSetID,
		summary.Model, summary.ModelProvider, summary.UpdatedAtUnixMS); err != nil {
		return err
	}
	return writeWarnings(w, summary.Warnings)
}

func writeCodexProfileSave(w io.Writer, title string, result app.CodexProfileSaveResult) error {
	if _, err := fmt.Fprintf(w, "%s\noperation: %s\nprovider: %s\nprofile: %s\nconfig set: %s\ncodex dir: %s\nconfig: %s\nauth: %s\n",
		title, result.OperationID, result.Provider.ID, result.Profile.ID, result.ConfigSet.ID, result.CodexDir, result.ConfigPath, result.AuthPath); err != nil {
		return err
	}
	return writeWarnings(w, result.Warnings)
}

func writeCodexConfigSetList(w io.Writer, result app.CodexConfigSetListResult) error {
	if len(result.ConfigSets) == 0 {
		_, err := fmt.Fprintln(w, "No Codex Config Sets")
		return err
	}
	for _, configSet := range result.ConfigSets {
		if err := writeCodexConfigSet(w, configSet); err != nil {
			return err
		}
	}
	return nil
}

func writeCodexConfigSet(w io.Writer, result app.CodexConfigSet) error {
	_, err := fmt.Fprintf(w, "Codex Config Set\nid: %s\nname: %s\nactive: %t\nreferences: %d\nmodel: %s\nprovider: %s\nupdated: %d\n",
		result.ID, result.Name, result.Active, result.ReferenceCount, result.Model, result.ModelProvider, result.UpdatedAtUnixMS)
	return err
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
