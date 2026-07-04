package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/strahe/profiledeck/internal/app"
)

func TestNewCommandBuildsRootCommand(t *testing.T) {
	cmd := NewCommand(app.DefaultInfo())

	if cmd.Name != app.CLIName {
		t.Fatalf("expected command name %q, got %q", app.CLIName, cmd.Name)
	}
	if cmd.Version != app.DefaultVersion {
		t.Fatalf("expected version %q, got %q", app.DefaultVersion, cmd.Version)
	}
	if cmd.Command("version") == nil {
		t.Fatalf("expected version subcommand")
	}
}

func TestRootHelp(t *testing.T) {
	var out bytes.Buffer
	cmd := NewCommand(app.DefaultInfo())
	cmd.Writer = &out

	if err := cmd.Run(context.Background(), []string{app.CLIName, "--help"}); err != nil {
		t.Fatalf("expected help to succeed, got %v", err)
	}
	if !strings.Contains(out.String(), app.CLIName) {
		t.Fatalf("expected help output to contain %q, got %q", app.CLIName, out.String())
	}
}

func TestRootVersionFlag(t *testing.T) {
	var out bytes.Buffer
	cmd := NewCommand(app.DefaultInfo())
	cmd.Writer = &out

	if err := cmd.Run(context.Background(), []string{app.CLIName, "--version"}); err != nil {
		t.Fatalf("expected version flag to succeed, got %v", err)
	}
	if !strings.Contains(out.String(), app.DefaultVersion) {
		t.Fatalf("expected version output to contain %q, got %q", app.DefaultVersion, out.String())
	}
}

func TestVersionCommand(t *testing.T) {
	var out bytes.Buffer
	cmd := NewCommand(app.DefaultInfo())
	cmd.Writer = &out

	if err := cmd.Run(context.Background(), []string{app.CLIName, "version"}); err != nil {
		t.Fatalf("expected version command to succeed, got %v", err)
	}
	if !strings.Contains(out.String(), app.DefaultVersion) {
		t.Fatalf("expected version output to contain %q, got %q", app.DefaultVersion, out.String())
	}
	if !strings.Contains(out.String(), app.UnknownBuildValue) {
		t.Fatalf("expected version output to contain %q, got %q", app.UnknownBuildValue, out.String())
	}
}
