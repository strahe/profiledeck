package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	urfavecli "github.com/urfave/cli/v3"

	"github.com/strahe/profiledeck/internal/app"
)

const (
	configDirFlagName = "config-dir"
	jsonFlagName      = "json"
	providerFlagName  = "provider"
)

func NewCommand(info app.Info) *urfavecli.Command {
	return &urfavecli.Command{
		Name:    info.CLIName,
		Usage:   "Switch AI coding tool provider profiles safely",
		Version: info.Version,
		Flags: []urfavecli.Flag{
			&urfavecli.StringFlag{
				Name:  configDirFlagName,
				Usage: "Use a custom ProfileDeck config directory",
				Config: urfavecli.StringConfig{
					TrimSpace: true,
				},
			},
		},
		Commands: []*urfavecli.Command{
			newAntigravityCommand(),
			newBackupCommand(),
			newCodexCommand(),
			newDoctorCommand(),
			newInitCommand(),
			newPlanCommand(),
			newProviderCommand(),
			newProfileCommand(),
			newRecoverCommand(),
			newRollbackCommand(),
			newStatusCommand(),
			newSwitchCommand(),
			newUsageCommand(),
			newVersionCommand(info),
		},
	}
}

func newInitCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:  "init",
		Usage: "Initialize the ProfileDeck application store",
		Flags: []urfavecli.Flag{
			&urfavecli.BoolFlag{
				Name:  jsonFlagName,
				Usage: "Write JSON output",
			},
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			result, err := app.Init(ctx, app.InitRequest{
				ConfigDir: configDirValue(cmd),
			})
			if err != nil {
				return err
			}

			w := outputWriter(cmd)
			if cmd.Bool(jsonFlagName) {
				return writeJSON(w, result)
			}

			_, err = fmt.Fprintf(
				w,
				"ProfileDeck initialized\nconfig dir: %s\nruntime root: %s\ndatabase: %s\nschema: healthy\nmigrations applied: %d\n",
				result.ConfigDir,
				result.RuntimeRoot,
				result.DatabasePath,
				result.MigrationsApplied,
			)
			return err
		},
	}
}

func newStatusCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:  "status",
		Usage: "Print ProfileDeck application store status",
		Flags: []urfavecli.Flag{
			&urfavecli.BoolFlag{
				Name:  jsonFlagName,
				Usage: "Write JSON output",
			},
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			result, err := app.Status(ctx, app.StatusRequest{
				ConfigDir: configDirValue(cmd),
			})
			if err != nil {
				return err
			}

			w := outputWriter(cmd)
			if cmd.Bool(jsonFlagName) {
				return writeJSON(w, result)
			}

			schema := "unhealthy"
			if result.SchemaHealthy {
				schema = "healthy"
			}
			_, err = fmt.Fprintf(
				w,
				"ProfileDeck status\nconfig dir: %s\nruntime root: %s\ndatabase: %s\ninitialized: %t\nschema: %s\npending operations: %d\nfailed operations: %d\n",
				result.ConfigDir,
				result.RuntimeRoot,
				result.DatabasePath,
				result.Initialized,
				schema,
				result.PendingOperations,
				result.FailedOperations,
			)
			return err
		},
	}
}

func newVersionCommand(info app.Info) *urfavecli.Command {
	return &urfavecli.Command{
		Name:  "version",
		Usage: "Print version information",
		Action: func(_ context.Context, cmd *urfavecli.Command) error {
			_, err := fmt.Fprintf(outputWriter(cmd), "%s %s\ncommit: %s\nbuilt: %s\n", info.CLIName, info.Version, info.Commit, info.BuildDate)
			return err
		},
	}
}

func configDirValue(cmd *urfavecli.Command) string {
	if cmd == nil {
		return ""
	}
	if value := cmd.String(configDirFlagName); value != "" {
		return value
	}
	if root := cmd.Root(); root != nil {
		return root.String(configDirFlagName)
	}
	return ""
}

func writeJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

func outputWriter(cmd *urfavecli.Command) io.Writer {
	if cmd != nil {
		if root := cmd.Root(); root != nil && root.Writer != nil {
			return root.Writer
		}
		if cmd.Writer != nil {
			return cmd.Writer
		}
	}
	return os.Stdout
}
