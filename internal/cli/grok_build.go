package cli

import (
	"context"
	"fmt"
	"io"

	urfavecli "github.com/urfave/cli/v3"

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/grokbuild"
)

func newGrokBuildCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "grok-build", Usage: "Manage Grok Build Profiles and Config Sets",
		Commands: []*urfavecli.Command{
			newGrokBuildDetectCommand(), newGrokBuildProfileCommand(), newGrokBuildConfigSetCommand(),
		},
	}
}

func newGrokBuildDetectCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "detect", Usage: "Detect local Grok Build configuration",
		Flags: []urfavecli.Flag{boolFlag(jsonFlagName, "Write JSON output")},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			application, err := applicationFor(cmd)
			if err != nil {
				return err
			}
			result, err := application.GrokBuild().Detect(ctx)
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			return writeGrokBuildDetect(outputWriter(cmd), result)
		},
	}
}

func newGrokBuildProfileCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "profile", Usage: "Manage Grok Build Profiles",
		Commands: []*urfavecli.Command{
			newGrokBuildProfileListCommand(), newGrokBuildProfileShowCommand(),
			newGrokBuildProfileCreateCommand(), newGrokBuildProfileForkCommand(),
			newGrokBuildProfileSaveCurrentCommand(), newGrokBuildProfileSetConfigCommand(),
			newProfileDeleteCommand(),
		},
	}
}

func newGrokBuildProfileListCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "list", Usage: "List Grok Build Profiles",
		Flags: []urfavecli.Flag{boolFlag(jsonFlagName, "Write JSON output")},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			application, err := applicationFor(cmd)
			if err != nil {
				return err
			}
			result, err := application.GrokBuild().ListProfiles(ctx)
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			return writeGrokBuildProfileList(outputWriter(cmd), result)
		},
	}
}

func newGrokBuildProfileShowCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "show", Usage: "Show a Grok Build Profile", ArgsUsage: "<profile-id>",
		Flags: []urfavecli.Flag{boolFlag(jsonFlagName, "Write JSON output")},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			id, err := singleIDArg(cmd, apperror.ProfileInvalid)
			if err != nil {
				return err
			}
			application, err := applicationFor(cmd)
			if err != nil {
				return err
			}
			result, err := application.GrokBuild().GetProfile(ctx, id)
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			return writeGrokBuildProfileDetail(outputWriter(cmd), result)
		},
	}
}

func newGrokBuildProfileCreateCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "create", Usage: "Create and activate a Grok Build Profile from the current login",
		ArgsUsage: "<profile-id>",
		Flags: []urfavecli.Flag{
			stringFlag(nameFlagName, "Profile display name"),
			stringFlag(descriptionFlagName, "Profile description"),
			stringFlag(newConfigSetFlagName, "Save current settings as a new Config Set with this id"),
			stringFlag(configSetNameFlagName, "New Config Set display name"),
			stringFlag(configSetDescriptionFlagName, "New Config Set description"),
			boolFlag(jsonFlagName, "Write JSON output"),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			id, err := singleIDArg(cmd, apperror.ProfileInvalid)
			if err != nil {
				return err
			}
			application, err := applicationFor(cmd)
			if err != nil {
				return err
			}
			result, err := application.GrokBuild().CreateProfile(ctx, grokbuild.CreateProfileRequest{
				ProfileID: id, Name: stringFlagPtr(cmd, nameFlagName),
				Description:             stringFlagPtr(cmd, descriptionFlagName),
				NewConfigSetID:          cmd.String(newConfigSetFlagName),
				NewConfigSetName:        stringFlagPtr(cmd, configSetNameFlagName),
				NewConfigSetDescription: stringFlagPtr(cmd, configSetDescriptionFlagName),
			})
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			return writeGrokBuildProfileSave(outputWriter(cmd), "Grok Build Profile created", result)
		},
	}
}

func newGrokBuildProfileForkCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "fork", Usage: "Fork a Grok Build Profile with explicit resource sharing",
		ArgsUsage: "<source-profile-id> <new-profile-id>",
		Flags: []urfavecli.Flag{
			stringFlag(credentialBindingFlagName, "Login binding: share-parent or copy-new"),
			stringFlag(configBindingFlagName, "Config Set binding: share-parent or copy-new"),
			stringFlag(newConfigSetFlagName, "New Config Set id when copying settings"),
			stringFlag(configSetNameFlagName, "New Config Set display name"),
			stringFlag(configSetDescriptionFlagName, "New Config Set description"),
			stringFlag(nameFlagName, "Profile display name"),
			stringFlag(descriptionFlagName, "Profile description"),
			boolFlag(jsonFlagName, "Write JSON output"),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			if cmd.Args().Len() != 2 {
				return apperror.New(apperror.ProfileInvalid, "expected source and destination Profile ids")
			}
			application, err := applicationFor(cmd)
			if err != nil {
				return err
			}
			result, err := application.GrokBuild().ForkProfile(ctx, grokbuild.ForkProfileRequest{
				SourceProfileID: cmd.Args().Get(0), ProfileID: cmd.Args().Get(1),
				CredentialBinding:       cmd.String(credentialBindingFlagName),
				ConfigBinding:           cmd.String(configBindingFlagName),
				NewConfigSetID:          cmd.String(newConfigSetFlagName),
				NewConfigSetName:        stringFlagPtr(cmd, configSetNameFlagName),
				NewConfigSetDescription: stringFlagPtr(cmd, configSetDescriptionFlagName),
				Name:                    stringFlagPtr(cmd, nameFlagName),
				Description:             stringFlagPtr(cmd, descriptionFlagName),
			})
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			return writeGrokBuildProfileSave(outputWriter(cmd), "Grok Build Profile forked", result)
		},
	}
}

func newGrokBuildProfileSaveCurrentCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "save-current", Usage: "Save current Grok Build login and settings into the active Profile",
		Flags: []urfavecli.Flag{boolFlag(jsonFlagName, "Write JSON output")},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			application, err := applicationFor(cmd)
			if err != nil {
				return err
			}
			result, err := application.GrokBuild().SaveActiveProfileState(ctx)
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			if _, err := fmt.Fprintf(
				outputWriter(cmd),
				"Grok Build Profile saved\nprofile: %s\nConfig Set: %s\nlogin references: %d\n",
				result.ProfileID, result.ConfigSet.ID, result.CredentialReferenceCount,
			); err != nil {
				return err
			}
			return writeWarnings(outputWriter(cmd), result.Warnings)
		},
	}
}

func newGrokBuildProfileSetConfigCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "set-config", Usage: "Bind an inactive Grok Build Profile to a Config Set",
		ArgsUsage: "<profile-id> <config-set-id>",
		Flags:     []urfavecli.Flag{boolFlag(jsonFlagName, "Write JSON output")},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			if cmd.Args().Len() != 2 {
				return apperror.New(apperror.ProfileInvalid, "expected Profile and Config Set ids")
			}
			application, err := applicationFor(cmd)
			if err != nil {
				return err
			}
			result, err := application.GrokBuild().UpdateProfileConfigSet(ctx, grokbuild.UpdateProfileConfigSetRequest{
				ProfileID: cmd.Args().Get(0), ConfigSetID: cmd.Args().Get(1),
			})
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			return writeGrokBuildProfileDetail(outputWriter(cmd), result)
		},
	}
}

func newGrokBuildConfigSetCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "config-set", Usage: "Manage Grok Build Config Sets",
		Commands: []*urfavecli.Command{
			newGrokBuildConfigSetListCommand(), newGrokBuildConfigSetShowCommand(),
			newGrokBuildConfigSetCreateCommand(), newGrokBuildConfigSetCopyCommand(),
			newGrokBuildConfigSetUpdateCommand(), newGrokBuildConfigSetDeleteCommand(),
		},
	}
}

func newGrokBuildConfigSetListCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "list", Usage: "List Grok Build Config Sets",
		Flags: []urfavecli.Flag{boolFlag(jsonFlagName, "Write JSON output")},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			application, err := applicationFor(cmd)
			if err != nil {
				return err
			}
			result, err := application.GrokBuild().ListConfigSets(ctx)
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			return writeGrokBuildConfigSetList(outputWriter(cmd), result)
		},
	}
}

func newGrokBuildConfigSetShowCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "show", Usage: "Show a Grok Build Config Set summary", ArgsUsage: "<config-set-id>",
		Flags: []urfavecli.Flag{boolFlag(jsonFlagName, "Write JSON output")},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			id, err := singleIDArg(cmd, apperror.GrokBuildInvalid)
			if err != nil {
				return err
			}
			application, err := applicationFor(cmd)
			if err != nil {
				return err
			}
			result, err := application.GrokBuild().GetConfigSet(ctx, id)
			if err != nil {
				return err
			}
			return writeGrokBuildConfigSetResult(cmd, result)
		},
	}
}

func newGrokBuildConfigSetCreateCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "create", Usage: "Create a Config Set from the current config.toml",
		ArgsUsage: "<config-set-id>",
		Flags: []urfavecli.Flag{
			stringFlag(nameFlagName, "Config Set name"),
			stringFlag(descriptionFlagName, "Config Set description"),
			boolFlag(jsonFlagName, "Write JSON output"),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			id, err := singleIDArg(cmd, apperror.GrokBuildInvalid)
			if err != nil {
				return err
			}
			name := cmd.String(nameFlagName)
			if name == "" {
				name = id
			}
			application, err := applicationFor(cmd)
			if err != nil {
				return err
			}
			result, err := application.GrokBuild().CreateConfigSet(ctx, grokbuild.CreateConfigSetRequest{
				ConfigSetID: id, Name: name, Description: cmd.String(descriptionFlagName),
			})
			if err != nil {
				return err
			}
			return writeGrokBuildConfigSetResult(cmd, result)
		},
	}
}

func newGrokBuildConfigSetCopyCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "copy", Usage: "Copy a Grok Build Config Set", ArgsUsage: "<source-id> <new-id>",
		Flags: []urfavecli.Flag{
			stringFlag(nameFlagName, "Config Set name"),
			stringFlag(descriptionFlagName, "Config Set description"),
			boolFlag(jsonFlagName, "Write JSON output"),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			if cmd.Args().Len() != 2 {
				return apperror.New(apperror.GrokBuildInvalid, "expected source and destination Config Set ids")
			}
			name := cmd.String(nameFlagName)
			if name == "" {
				name = cmd.Args().Get(1)
			}
			application, err := applicationFor(cmd)
			if err != nil {
				return err
			}
			result, err := application.GrokBuild().CopyConfigSet(ctx, grokbuild.CopyConfigSetRequest{
				SourceConfigSetID: cmd.Args().Get(0), ConfigSetID: cmd.Args().Get(1),
				Name: name, Description: cmd.String(descriptionFlagName),
			})
			if err != nil {
				return err
			}
			return writeGrokBuildConfigSetResult(cmd, result)
		},
	}
}

func newGrokBuildConfigSetUpdateCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "update", Usage: "Rename or describe a Grok Build Config Set",
		ArgsUsage: "<config-set-id>",
		Flags: []urfavecli.Flag{
			stringFlag(nameFlagName, "Config Set name"),
			stringFlag(descriptionFlagName, "Config Set description"),
			boolFlag(jsonFlagName, "Write JSON output"),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			id, err := singleIDArg(cmd, apperror.GrokBuildInvalid)
			if err != nil {
				return err
			}
			application, err := applicationFor(cmd)
			if err != nil {
				return err
			}
			result, err := application.GrokBuild().UpdateConfigSet(ctx, grokbuild.UpdateConfigSetRequest{
				ConfigSetID: id, Name: stringFlagPtr(cmd, nameFlagName),
				Description: stringFlagPtr(cmd, descriptionFlagName),
			})
			if err != nil {
				return err
			}
			return writeGrokBuildConfigSetResult(cmd, result)
		},
	}
}

func newGrokBuildConfigSetDeleteCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "delete", Usage: "Delete an unreferenced Grok Build Config Set",
		ArgsUsage: "<config-set-id>",
		Flags: []urfavecli.Flag{
			boolFlag(yesFlagName, "Confirm deletion"),
			boolFlag(jsonFlagName, "Write JSON output"),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			id, err := singleIDArg(cmd, apperror.GrokBuildInvalid)
			if err != nil {
				return err
			}
			if !cmd.Bool(yesFlagName) {
				return apperror.New(apperror.ConfirmationRequired, "Config Set deletion requires --yes")
			}
			application, err := applicationFor(cmd)
			if err != nil {
				return err
			}
			if err := application.GrokBuild().DeleteConfigSet(ctx, id); err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), map[string]any{"deleted": true, "config_set_id": id})
			}
			_, err = fmt.Fprintf(outputWriter(cmd), "Grok Build Config Set deleted\nid: %s\n", id)
			return err
		},
	}
}

func writeGrokBuildDetect(w io.Writer, result grokbuild.DetectResult) error {
	if _, err := fmt.Fprintf(
		w,
		"Grok Build detect\nprovider: %s\nadapter: %s\nHome: %s\nconfig: %s\nauth: %s\nHome exists: %t\nconfig status: %s\nauth status: %s\nfile auth supported: %t\nProfileDeck initialized: %t\nprovider exists: %t\nprovider compatible: %t\n",
		result.ProviderID, result.AdapterID, result.GrokHome, result.ConfigPath, result.AuthPath,
		result.GrokHomeExists, result.ConfigStatus, result.AuthStatus, result.FileAuthSupported,
		result.ProfileDeckInitialized, result.ProviderExists, result.ProviderCompatible,
	); err != nil {
		return err
	}
	return writeWarnings(w, result.Warnings)
}

func writeGrokBuildProfileList(w io.Writer, result grokbuild.ProfileListResult) error {
	if len(result.Profiles) == 0 {
		_, err := fmt.Fprintln(w, "No Grok Build Profiles")
		return err
	}
	for _, value := range result.Profiles {
		status := "idle"
		if value.Active {
			status = "active"
		}
		if _, err := fmt.Fprintf(
			w, "Grok Build Profile\nid: %s\nname: %s\nstatus: %s\nConfig Set: %s\nlogin references: %d\nupdated: %d\n",
			value.Profile.ID, value.Profile.Name, status, value.ConfigSetName,
			value.CredentialReferenceCount, value.UpdatedAtUnixMS,
		); err != nil {
			return err
		}
		if err := writeWarnings(w, value.Warnings); err != nil {
			return err
		}
	}
	return nil
}

func writeGrokBuildProfileDetail(w io.Writer, detail grokbuild.ProfileDetail) error {
	value := detail.Summary
	if _, err := fmt.Fprintf(
		w,
		"Grok Build Profile\nid: %s\nname: %s\nactive: %t\nConfig Set: %s (%s)\nConfig Set references: %d\nlogin references: %d\nupdated: %d\n",
		value.Profile.ID, value.Profile.Name, value.Active, value.ConfigSetName, value.ConfigSetID,
		value.ConfigSetReferenceCount, value.CredentialReferenceCount, value.UpdatedAtUnixMS,
	); err != nil {
		return err
	}
	return writeWarnings(w, value.Warnings)
}

func writeGrokBuildProfileSave(w io.Writer, title string, result grokbuild.ProfileSaveResult) error {
	if _, err := fmt.Fprintf(
		w,
		"%s\noperation: %s\nprovider: %s\nprofile: %s\nConfig Set: %s\nHome: %s\nconfig: %s\nauth: %s\n",
		title, result.OperationID, result.Provider.ID, result.Profile.ID,
		result.ConfigSet.ID, result.GrokHome, result.ConfigPath, result.AuthPath,
	); err != nil {
		return err
	}
	return writeWarnings(w, result.Warnings)
}

func writeGrokBuildConfigSetList(w io.Writer, result grokbuild.ConfigSetListResult) error {
	if len(result.ConfigSets) == 0 {
		_, err := fmt.Fprintln(w, "No Grok Build Config Sets")
		return err
	}
	for _, value := range result.ConfigSets {
		if err := writeGrokBuildConfigSet(w, value); err != nil {
			return err
		}
	}
	return nil
}

func writeGrokBuildConfigSet(w io.Writer, value grokbuild.ConfigSet) error {
	_, err := fmt.Fprintf(
		w,
		"Grok Build Config Set\nid: %s\nname: %s\nactive: %t\nreferences: %d\nupdated: %d\n",
		value.ID, value.Name, value.Active, value.ReferenceCount, value.UpdatedAtUnixMS,
	)
	return err
}

func writeGrokBuildConfigSetResult(cmd *urfavecli.Command, value grokbuild.ConfigSet) error {
	if cmd.Bool(jsonFlagName) {
		return writeJSON(outputWriter(cmd), value)
	}
	return writeGrokBuildConfigSet(outputWriter(cmd), value)
}
