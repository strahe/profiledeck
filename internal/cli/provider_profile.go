package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	urfavecli "github.com/urfave/cli/v3"

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/profile"
	"github.com/strahe/profiledeck/internal/provider"
)

const (
	adapterFlagName      = "adapter"
	allFlagName          = "all"
	descriptionFlagName  = "description"
	disabledFlagName     = "disabled"
	enabledFlagName      = "enabled"
	metadataJSONFlagName = "metadata-json"
	nameFlagName         = "name"
	yesFlagName          = "yes"
)

func newProviderCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:  "provider",
		Usage: "Manage AI tool providers",
		Commands: []*urfavecli.Command{
			newProviderListCommand(),
			newProviderShowCommand(),
			newProviderCreateCommand(),
			newProviderUpdateCommand(),
			newProviderDeleteCommand(),
		},
	}
}

func newProviderListCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:  "list",
		Usage: "List providers",
		Flags: []urfavecli.Flag{
			boolFlag(allFlagName, "Include disabled providers"),
			boolFlag(jsonFlagName, "Write JSON output"),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			application, err := applicationFor(cmd)
			if err != nil {
				return err
			}
			result, err := application.Providers().List(ctx, provider.ListRequest{IncludeDisabled: cmd.Bool(allFlagName)})
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			return writeProviderList(outputWriter(cmd), result)
		},
	}
}

func newProviderShowCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:      "show",
		Usage:     "Show a provider",
		ArgsUsage: "<id>",
		Flags: []urfavecli.Flag{
			boolFlag(jsonFlagName, "Write JSON output"),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			id, err := singleIDArg(cmd, apperror.ProviderInvalid)
			if err != nil {
				return err
			}
			application, err := applicationFor(cmd)
			if err != nil {
				return err
			}
			result, err := application.Providers().Get(ctx, id)
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			return writeProvider(outputWriter(cmd), result)
		},
	}
}

func newProviderCreateCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:      "create",
		Usage:     "Create a provider",
		ArgsUsage: "<id>",
		Flags: []urfavecli.Flag{
			stringFlag(nameFlagName, "Provider display name"),
			stringFlag(adapterFlagName, "Provider adapter ID"),
			boolFlag(disabledFlagName, "Create the provider disabled"),
			stringFlag(metadataJSONFlagName, "Provider metadata JSON object"),
			boolFlag(jsonFlagName, "Write JSON output"),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			id, err := singleIDArg(cmd, apperror.ProviderInvalid)
			if err != nil {
				return err
			}
			enabled := !cmd.Bool(disabledFlagName)
			application, err := applicationFor(cmd)
			if err != nil {
				return err
			}
			result, err := application.Providers().Create(ctx, provider.CreateRequest{
				ID:           id,
				Name:         cmd.String(nameFlagName),
				AdapterID:    cmd.String(adapterFlagName),
				Enabled:      &enabled,
				MetadataJSON: stringFlagPtr(cmd, metadataJSONFlagName),
			})
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			return writeProvider(outputWriter(cmd), result)
		},
	}
}

func newProviderUpdateCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:      "update",
		Usage:     "Update a provider",
		ArgsUsage: "<id>",
		Flags: []urfavecli.Flag{
			stringFlag(nameFlagName, "Provider display name"),
			stringFlag(adapterFlagName, "Provider adapter ID"),
			boolFlag(enabledFlagName, "Enable the provider"),
			boolFlag(disabledFlagName, "Disable the provider"),
			stringFlag(metadataJSONFlagName, "Provider metadata JSON object"),
			boolFlag(jsonFlagName, "Write JSON output"),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			id, err := singleIDArg(cmd, apperror.ProviderInvalid)
			if err != nil {
				return err
			}
			enabled, err := enabledFlagPtr(cmd)
			if err != nil {
				return err
			}
			application, err := applicationFor(cmd)
			if err != nil {
				return err
			}
			result, err := application.Providers().Update(ctx, provider.UpdateRequest{
				ID:           id,
				Name:         stringFlagPtr(cmd, nameFlagName),
				AdapterID:    stringFlagPtr(cmd, adapterFlagName),
				Enabled:      enabled,
				MetadataJSON: stringFlagPtr(cmd, metadataJSONFlagName),
			})
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			return writeProvider(outputWriter(cmd), result)
		},
	}
}

func newProviderDeleteCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:      "delete",
		Usage:     "Delete a provider",
		ArgsUsage: "<id>",
		Flags: []urfavecli.Flag{
			boolFlag(yesFlagName, "Confirm deletion"),
			boolFlag(jsonFlagName, "Write JSON output"),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			id, err := singleIDArg(cmd, apperror.ProviderInvalid)
			if err != nil {
				return err
			}
			application, err := applicationFor(cmd)
			if err != nil {
				return err
			}
			result, err := application.Providers().Delete(ctx, id, cmd.Bool(yesFlagName))
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			_, err = fmt.Fprintf(outputWriter(cmd), "Deleted provider %s\n", result.ID)
			return err
		},
	}
}

func newProfileCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:  "profile",
		Usage: "Manage ProfileDeck profiles",
		Commands: []*urfavecli.Command{
			newProfileTargetCommand(),
			newProfileListCommand(),
			newProfileShowCommand(),
			newProfileCreateCommand(),
			newProfileUpdateCommand(),
			newProfileDeleteCommand(),
		},
	}
}

func newProfileListCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:  "list",
		Usage: "List profiles",
		Flags: []urfavecli.Flag{
			boolFlag(jsonFlagName, "Write JSON output"),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			application, err := applicationFor(cmd)
			if err != nil {
				return err
			}
			result, err := application.Profiles().List(ctx)
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			return writeProfileList(outputWriter(cmd), result)
		},
	}
}

func newProfileShowCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:      "show",
		Usage:     "Show a profile",
		ArgsUsage: "<id>",
		Flags: []urfavecli.Flag{
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
			result, err := application.Profiles().Get(ctx, id)
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			return writeProfile(outputWriter(cmd), result)
		},
	}
}

func newProfileCreateCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:      "create",
		Usage:     "Create a profile",
		ArgsUsage: "<id>",
		Flags: []urfavecli.Flag{
			stringFlag(nameFlagName, "Profile display name"),
			stringFlag(descriptionFlagName, "Profile description"),
			stringFlag(metadataJSONFlagName, "Profile metadata JSON object"),
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
			result, err := application.Profiles().Create(ctx, profile.CreateRequest{
				ID:           id,
				Name:         cmd.String(nameFlagName),
				Description:  cmd.String(descriptionFlagName),
				MetadataJSON: stringFlagPtr(cmd, metadataJSONFlagName),
			})
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			return writeProfile(outputWriter(cmd), result)
		},
	}
}

func newProfileUpdateCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:      "update",
		Usage:     "Update a profile",
		ArgsUsage: "<id>",
		Flags: []urfavecli.Flag{
			stringFlag(nameFlagName, "Profile display name"),
			stringFlag(descriptionFlagName, "Profile description"),
			stringFlag(metadataJSONFlagName, "Profile metadata JSON object"),
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
			result, err := application.Profiles().Update(ctx, profile.UpdateRequest{
				ID:           id,
				Name:         stringFlagPtr(cmd, nameFlagName),
				Description:  stringFlagPtr(cmd, descriptionFlagName),
				MetadataJSON: stringFlagPtr(cmd, metadataJSONFlagName),
			})
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			return writeProfile(outputWriter(cmd), result)
		},
	}
}

func newProfileDeleteCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:      "delete",
		Usage:     "Delete a profile",
		ArgsUsage: "<id>",
		Flags: []urfavecli.Flag{
			boolFlag(yesFlagName, "Confirm deletion"),
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
			result, err := application.Profiles().Delete(ctx, id, cmd.Bool(yesFlagName))
			if err != nil {
				return err
			}
			if cmd.Bool(jsonFlagName) {
				return writeJSON(outputWriter(cmd), result)
			}
			_, err = fmt.Fprintf(outputWriter(cmd), "Deleted profile %s\n", result.ID)
			return err
		},
	}
}

func boolFlag(name, usage string) urfavecli.Flag {
	return &urfavecli.BoolFlag{
		Name:  name,
		Usage: usage,
	}
}

func stringFlag(name, usage string) urfavecli.Flag {
	return &urfavecli.StringFlag{
		Name:  name,
		Usage: usage,
		Config: urfavecli.StringConfig{
			TrimSpace: true,
		},
	}
}

func singleIDArg(cmd *urfavecli.Command, code apperror.Code) (string, error) {
	if cmd.Args().Len() != 1 {
		return "", apperror.New(code, "exactly one id argument is required")
	}
	return cmd.Args().First(), nil
}

func stringFlagPtr(cmd *urfavecli.Command, name string) *string {
	if !cmd.IsSet(name) {
		return nil
	}
	value := cmd.String(name)
	return &value
}

func enabledFlagPtr(cmd *urfavecli.Command) (*bool, error) {
	return enabledFlagPtrWithCode(cmd, apperror.ProviderInvalid)
}

func enabledFlagPtrWithCode(cmd *urfavecli.Command, code apperror.Code) (*bool, error) {
	if cmd.IsSet(enabledFlagName) && cmd.IsSet(disabledFlagName) {
		return nil, apperror.New(code, "cannot set both enabled and disabled")
	}
	if cmd.IsSet(enabledFlagName) {
		value := true
		return &value, nil
	}
	if cmd.IsSet(disabledFlagName) {
		value := false
		return &value, nil
	}
	return nil, nil
}

func writeProviderList(w io.Writer, providers []provider.Provider) error {
	if len(providers) == 0 {
		_, err := fmt.Fprintln(w, "No providers")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 8, 2, ' ', 0)
	for _, provider := range providers {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\tenabled=%t\n", provider.ID, provider.Name, provider.AdapterID, provider.Enabled); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func writeProvider(w io.Writer, provider provider.Provider) error {
	metadata, err := compactJSON(provider.Metadata)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(
		w,
		"id: %s\nname: %s\nadapter: %s\nenabled: %t\nmetadata: %s\ncreated_at_unix_ms: %d\nupdated_at_unix_ms: %d\n",
		provider.ID,
		provider.Name,
		provider.AdapterID,
		provider.Enabled,
		metadata,
		provider.CreatedAtUnixMS,
		provider.UpdatedAtUnixMS,
	)
	return err
}

func writeProfileList(w io.Writer, profiles []profile.Profile) error {
	if len(profiles) == 0 {
		_, err := fmt.Fprintln(w, "No profiles")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 8, 2, ' ', 0)
	for _, profile := range profiles {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\n", profile.ID, profile.Name, profile.Description); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func writeProfile(w io.Writer, profile profile.Profile) error {
	metadata, err := compactJSON(profile.Metadata)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(
		w,
		"id: %s\nname: %s\ndescription: %s\nmetadata: %s\ncreated_at_unix_ms: %d\nupdated_at_unix_ms: %d\n",
		profile.ID,
		profile.Name,
		profile.Description,
		metadata,
		profile.CreatedAtUnixMS,
		profile.UpdatedAtUnixMS,
	)
	return err
}

func compactJSON(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
}
