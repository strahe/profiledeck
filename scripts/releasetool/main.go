package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

type command func([]string, io.Reader, io.Writer) error

var commands = map[string]command{
	"contract":        commandWithoutInput(runContract),
	"finalize":        commandWithoutInput(runFinalize),
	"notes-start-tag": runNotesStartTag,
}

func main() {
	if err := runWithInput(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "releasetool: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	return runWithInput(args, nil, stdout)
}

func runWithInput(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("a command is required")
	}
	runCommand, ok := commands[args[0]]
	if !ok {
		return fmt.Errorf("unknown command %q", args[0])
	}
	return runCommand(args[1:], stdin, stdout)
}

func commandWithoutInput(runCommand func([]string, io.Writer) error) command {
	return func(args []string, _ io.Reader, stdout io.Writer) error {
		return runCommand(args, stdout)
	}
}

func newFlagSet(name string) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	return flags
}
