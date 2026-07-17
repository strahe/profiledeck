package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type notarizationResult struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

func parseNotarizationResult(content []byte) (notarizationResult, error) {
	var result notarizationResult
	decoder := json.NewDecoder(bytes.NewReader(content))
	if err := decoder.Decode(&result); err != nil {
		return notarizationResult{}, fmt.Errorf("decode notarytool response: %w", err)
	}
	if result.ID == "" || result.Status == "" {
		return notarizationResult{}, fmt.Errorf("notarytool response is missing an ID or status")
	}
	return result, nil
}

func notarize(
	ctx context.Context,
	runner commandRunner,
	input string,
	profile string,
	keychain string,
) (notarizationResult, error) {
	if input == "" || profile == "" {
		return notarizationResult{}, fmt.Errorf("notarization input and profile are required")
	}
	args := []string{"notarytool", "submit", input}
	args = append(args, notaryCredentialArgs(profile, keychain)...)
	args = append(
		args,
		"--wait",
		"--output-format",
		"json",
	)
	output, err := runner.run(ctx, "xcrun", args...)
	if err != nil {
		return notarizationResult{}, err
	}
	result, err := parseNotarizationResult(output)
	if err != nil {
		return notarizationResult{}, err
	}
	if result.Status != "Accepted" {
		logArgs := []string{"notarytool", "log", result.ID}
		logArgs = append(logArgs, notaryCredentialArgs(profile, keychain)...)
		logOutput, logErr := runner.run(ctx, "xcrun", logArgs...)
		if len(logOutput) > 0 {
			fmt.Fprintln(os.Stderr, string(logOutput))
		}
		if logErr != nil {
			fmt.Fprintf(os.Stderr, "releasetool: unable to retrieve notarization log: %v\n", logErr)
		}
		return result, fmt.Errorf(
			"notarization was not accepted for %s: %s",
			filepath.Base(input),
			result.Status,
		)
	}
	fmt.Printf("Notarization accepted for %s: %s\n", filepath.Base(input), result.ID)
	return result, nil
}

func notaryCredentialArgs(profile, keychain string) []string {
	args := []string{"--keychain-profile", profile}
	if keychain != "" {
		args = append(args, "--keychain", keychain)
	}
	return args
}

func writeInfoPlist(
	templatePath string,
	outputPath string,
	version releaseVersion,
	buildNumber int,
) error {
	content, err := os.ReadFile(templatePath)
	if err != nil {
		return fmt.Errorf("read Info.plist template: %w", err)
	}
	rendered := strings.ReplaceAll(string(content), "@SHORT_VERSION@", version.short())
	rendered = strings.ReplaceAll(rendered, "@BUILD_NUMBER@", fmt.Sprintf("%d", buildNumber))
	if strings.Contains(rendered, "@SHORT_VERSION@") || strings.Contains(rendered, "@BUILD_NUMBER@") {
		return fmt.Errorf("render Info.plist: template placeholders were not fully replaced")
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return fmt.Errorf("create Info.plist directory: %w", err)
	}
	if err := os.WriteFile(outputPath, []byte(rendered), 0o644); err != nil {
		return fmt.Errorf("write Info.plist: %w", err)
	}
	return nil
}
