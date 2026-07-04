package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/strahe/profiledeck/internal/app"
	urfavecli "github.com/urfave/cli/v3"
)

func NewCommand(info app.Info) *urfavecli.Command {
	return &urfavecli.Command{
		Name:    info.CLIName,
		Usage:   "Switch AI coding tool provider profiles safely",
		Version: info.Version,
		Commands: []*urfavecli.Command{
			newVersionCommand(info),
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
