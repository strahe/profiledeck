package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/strahe/profiledeck/internal/app"
	"github.com/strahe/profiledeck/internal/apperror"
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
		writeCommandError(os.Stderr, err)
		os.Exit(1)
	}
}

func writeCommandError(w io.Writer, err error) {
	_, _ = fmt.Fprintln(w, apperror.Public(err))
}
