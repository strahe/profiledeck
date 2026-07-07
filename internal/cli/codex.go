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
	modelFlagName         = "model"
	modelProviderFlagName = "model-provider"
	openAIBaseURLFlagName = "openai-base-url"
	accountFlagName       = "account"
	authFileFlagName      = "auth-file"
	outputFlagName        = "output"
	forceFlagName         = "force"
)

func newCodexCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:  "codex",
		Usage: "Manage Codex provider profiles",
		Commands: []*urfavecli.Command{
			newCodexAccountCommand(),
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
			newCodexProfileCaptureCommand(),
			newCodexProfileSetCommand(),
		},
	}
}

func newCodexProfileCaptureCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:      "capture",
		Usage:     "Capture full Codex config and file auth for a profile",
		ArgsUsage: "<profile-id>",
		Flags: []urfavecli.Flag{
			stringFlag(accountFlagName, "Local ProfileDeck Codex account id; defaults to profile id"),
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
			result, err := app.CodexProfileCapture(ctx, app.CodexProfileCaptureRequest{
				ConfigDir:   configDirValue(cmd),
				CodexDir:    cmd.String(codexDirFlagName),
				ProfileID:   profileID,
				AccountID:   cmd.String(accountFlagName),
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
			return writeCodexProfileCapture(w, result)
		},
	}
}

func newCodexProfileSetCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:  "set",
		Usage: "Set the full managed Codex config for a profile",
		Description: "Writes a complete ProfileDeck desired state for the managed Codex keys. " +
			"Omitting --openai-base-url removes ProfileDeck's managed base URL from Codex config on switch.",
		ArgsUsage: "<profile-id>",
		Flags: []urfavecli.Flag{
			stringFlag(modelFlagName, "Codex model"),
			stringFlag(modelProviderFlagName, "Codex model provider ID"),
			stringFlag(openAIBaseURLFlagName, "OpenAI-compatible API base URL; omit to remove managed value"),
			stringFlag(accountFlagName, "Existing local ProfileDeck Codex account id to bind"),
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
			result, err := app.CodexProfileSet(ctx, app.CodexProfileSetRequest{
				ConfigDir:     configDirValue(cmd),
				CodexDir:      cmd.String(codexDirFlagName),
				ProfileID:     profileID,
				Model:         cmd.String(modelFlagName),
				ModelProvider: cmd.String(modelProviderFlagName),
				OpenAIBaseURL: stringFlagPtr(cmd, openAIBaseURLFlagName),
				AccountID:     cmd.String(accountFlagName),
				Name:          stringFlagPtr(cmd, nameFlagName),
				Description:   stringFlagPtr(cmd, descriptionFlagName),
			})
			if err != nil {
				return err
			}
			w := outputWriter(cmd)
			if cmd.Bool(jsonFlagName) {
				return writeJSON(w, result)
			}
			return writeCodexProfileSet(w, result)
		},
	}
}

func newCodexAccountCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:  "account",
		Usage: "Manage stored Codex file-auth accounts",
		Commands: []*urfavecli.Command{
			newCodexAccountListCommand(),
			newCodexAccountShowCommand(),
			newCodexAccountExportCommand(),
			newCodexAccountImportCommand(),
		},
	}
}

func newCodexAccountListCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:  "list",
		Usage: "List stored Codex accounts without raw auth",
		Flags: []urfavecli.Flag{
			boolFlag(jsonFlagName, "Write JSON output"),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			result, err := app.CodexAccountList(ctx, app.CodexAccountListRequest{
				ConfigDir: configDirValue(cmd),
			})
			if err != nil {
				return err
			}
			w := outputWriter(cmd)
			if cmd.Bool(jsonFlagName) {
				return writeJSON(w, result)
			}
			return writeCodexAccountList(w, result)
		},
	}
}

func newCodexAccountShowCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:      "show",
		Usage:     "Show stored Codex account metadata without raw auth",
		ArgsUsage: "<account-id>",
		Flags: []urfavecli.Flag{
			boolFlag(jsonFlagName, "Write JSON output"),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			accountID, err := singleIDArg(cmd, app.ErrorCodexInvalid)
			if err != nil {
				return err
			}
			result, err := app.CodexAccountShow(ctx, app.CodexAccountShowRequest{
				ConfigDir: configDirValue(cmd),
				AccountID: accountID,
			})
			if err != nil {
				return err
			}
			w := outputWriter(cmd)
			if cmd.Bool(jsonFlagName) {
				return writeJSON(w, result)
			}
			return writeCodexAccount(w, result)
		},
	}
}

func newCodexAccountExportCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:      "export",
		Usage:     "Export raw Codex auth JSON to a 0600 file",
		ArgsUsage: "<account-id>",
		Flags: []urfavecli.Flag{
			stringFlag(outputFlagName, "Output auth JSON path"),
			boolFlag(forceFlagName, "Overwrite output path if it exists"),
			boolFlag(jsonFlagName, "Write JSON output"),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			accountID, err := singleIDArg(cmd, app.ErrorCodexInvalid)
			if err != nil {
				return err
			}
			result, err := app.CodexAccountExport(ctx, app.CodexAccountExportRequest{
				ConfigDir: configDirValue(cmd),
				AccountID: accountID,
				Output:    cmd.String(outputFlagName),
				Force:     cmd.Bool(forceFlagName),
			})
			if err != nil {
				return err
			}
			w := outputWriter(cmd)
			if cmd.Bool(jsonFlagName) {
				return writeJSON(w, result)
			}
			return writeCodexAccountExport(w, result)
		},
	}
}

func newCodexAccountImportCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:      "import",
		Usage:     "Import raw Codex auth JSON from a file",
		ArgsUsage: "<account-id>",
		Flags: []urfavecli.Flag{
			stringFlag(authFileFlagName, "Input auth JSON path"),
			stringFlag(nameFlagName, "Account display name"),
			boolFlag(jsonFlagName, "Write JSON output"),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			accountID, err := singleIDArg(cmd, app.ErrorCodexInvalid)
			if err != nil {
				return err
			}
			result, err := app.CodexAccountImport(ctx, app.CodexAccountImportRequest{
				ConfigDir: configDirValue(cmd),
				AccountID: accountID,
				AuthFile:  cmd.String(authFileFlagName),
				Name:      stringFlagPtr(cmd, nameFlagName),
			})
			if err != nil {
				return err
			}
			w := outputWriter(cmd)
			if cmd.Bool(jsonFlagName) {
				return writeJSON(w, result)
			}
			return writeCodexAccount(w, result)
		},
	}
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

func writeCodexProfileSet(w io.Writer, result app.CodexProfileSetResult) error {
	if _, err := fmt.Fprintf(
		w,
		"Codex profile set\nprovider: %s\nprofile: %s\ntarget: %s\nauth target: %s\ncodex dir: %s\nconfig: %s\nauth: %s\nmanaged keys: %s\n",
		result.Provider.ID,
		result.Profile.ID,
		result.Target.TargetID,
		optionalTargetID(result.AuthTarget),
		result.CodexDir,
		result.ConfigPath,
		result.AuthPath,
		strings.Join(result.ManagedKeys, ","),
	); err != nil {
		return err
	}
	return writeWarnings(w, result.Warnings)
}

func writeCodexProfileCapture(w io.Writer, result app.CodexProfileCaptureResult) error {
	if _, err := fmt.Fprintf(
		w,
		"Codex profile captured\nprovider: %s\nprofile: %s\naccount: %s\nconfig target: %s\nauth target: %s\ncodex dir: %s\nconfig: %s\nauth: %s\nauth sha256: %s\n",
		result.Provider.ID,
		result.Profile.ID,
		result.Account.AccountID,
		result.ConfigTarget.TargetID,
		result.AuthTarget.TargetID,
		result.CodexDir,
		result.ConfigPath,
		result.AuthPath,
		result.Account.PayloadSHA256,
	); err != nil {
		return err
	}
	return writeWarnings(w, result.Warnings)
}

func writeCodexAccountList(w io.Writer, accounts []app.CodexAccount) error {
	if _, err := fmt.Fprintf(w, "Codex accounts\ncount: %d\n", len(accounts)); err != nil {
		return err
	}
	for _, account := range accounts {
		if _, err := fmt.Fprintf(w, "- %s name: %s sha256: %s updated: %d\n", account.AccountID, account.DisplayName, account.PayloadSHA256, account.UpdatedAtUnixMS); err != nil {
			return err
		}
	}
	return nil
}

func writeCodexAccount(w io.Writer, account app.CodexAccount) error {
	_, err := fmt.Fprintf(
		w,
		"Codex account\nprovider: %s\naccount: %s\nkind: %s\nname: %s\nsha256: %s\ncreated: %d\nupdated: %d\n",
		account.ProviderID,
		account.AccountID,
		account.SecretKind,
		account.DisplayName,
		account.PayloadSHA256,
		account.CreatedAtUnixMS,
		account.UpdatedAtUnixMS,
	)
	return err
}

func writeCodexAccountExport(w io.Writer, result app.CodexAccountExportResult) error {
	_, err := fmt.Fprintf(
		w,
		"Codex account exported\nprovider: %s\naccount: %s\noutput: %s\nsha256: %s\n",
		result.ProviderID,
		result.AccountID,
		result.Output,
		result.PayloadSHA256,
	)
	return err
}

func optionalTargetID(target *app.ProfileTarget) string {
	if target == nil {
		return ""
	}
	return target.TargetID
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
