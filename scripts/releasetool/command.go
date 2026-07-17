package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type commandRunner interface {
	run(context.Context, string, ...string) ([]byte, error)
}

type systemCommandRunner struct{}

func (systemCommandRunner) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = strings.TrimSpace(stdout.String())
		}
		if detail == "" {
			return nil, fmt.Errorf("%s failed: %w", name, err)
		}
		return nil, fmt.Errorf("%s failed: %s: %w", name, detail, err)
	}
	if stdout.Len() == 0 && stderr.Len() > 0 {
		return stderr.Bytes(), nil
	}
	return stdout.Bytes(), nil
}

func runVisible(ctx context.Context, name string, args ...string) error {
	command := exec.CommandContext(ctx, name, args...)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.Stdin = os.Stdin
	if err := command.Run(); err != nil {
		return fmt.Errorf("%s failed: %w", name, err)
	}
	return nil
}

func requireCommands(names ...string) error {
	for _, name := range names {
		if _, err := exec.LookPath(name); err != nil {
			return fmt.Errorf("required command is unavailable: %s", name)
		}
	}
	return nil
}
