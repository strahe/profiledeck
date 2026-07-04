package main

import (
	"context"
	"fmt"
	"os"

	"github.com/strahe/profiledeck/internal/app"
	internalcli "github.com/strahe/profiledeck/internal/cli"
)

var (
	version   = app.DefaultVersion
	commit    = app.UnknownBuildValue
	buildDate = app.UnknownBuildValue
)

func main() {
	info := app.NewInfo(version, commit, buildDate)
	cmd := internalcli.NewCommand(info)

	if err := cmd.Run(context.Background(), os.Args); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
