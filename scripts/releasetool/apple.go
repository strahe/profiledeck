package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	notaryUUIDPattern = regexp.MustCompile(
		`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`,
	)
	notaryDeveloperIDPattern         = regexp.MustCompile(`Developer ID Application:[^;,\r\n"]+`)
	notaryTeamIDPattern              = regexp.MustCompile(`\b[A-Z0-9]{10}\b`)
	notaryEmailPattern               = regexp.MustCompile(`\b[^\s@]+@[^\s@]+\.[^\s@]+\b`)
	notaryBearerPattern              = regexp.MustCompile(`(?i)\bbearer\s+[^\s,;]+`)
	notaryAbsolutePathPattern        = regexp.MustCompile(`/[^,;\r\n]+`)
	notarySensitiveAssignmentPattern = regexp.MustCompile(
		`(?i)\b[A-Z0-9_]*(api_?key|token|secret|password|authorization|bearer)[A-Z0-9_]*\s*[:=]\s*[^,;\r\n]+`,
	)
)

type notarizationResult struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type notarizationLog struct {
	Status string              `json:"status"`
	Issues []notarizationIssue `json:"issues"`
}

type notarizationIssue struct {
	Severity     string `json:"severity"`
	Code         string `json:"code"`
	Path         string `json:"path"`
	Message      string `json:"message"`
	Architecture string `json:"architecture"`
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
	return notarizeWithWriters(ctx, runner, input, profile, keychain, os.Stdout, os.Stderr)
}

func notarizeWithWriters(
	ctx context.Context,
	runner commandRunner,
	input string,
	profile string,
	keychain string,
	stdout io.Writer,
	stderr io.Writer,
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
		return notarizationResult{}, fmt.Errorf(
			"could not submit %s for notarization; verify the notary credentials and try again",
			filepath.Base(input),
		)
	}
	result, err := parseNotarizationResult(output)
	if err != nil {
		return notarizationResult{}, err
	}
	if result.Status != "Accepted" {
		logArgs := []string{"notarytool", "log", result.ID}
		logArgs = append(logArgs, notaryCredentialArgs(profile, keychain)...)
		logOutput, logErr := runner.run(ctx, "xcrun", logArgs...)
		var writeErr error
		if logErr != nil || len(logOutput) == 0 {
			_, writeErr = fmt.Fprintln(stderr, "Notarization details could not be retrieved safely.")
		} else if log, parseErr := parseNotarizationLog(logOutput); parseErr != nil {
			_, writeErr = fmt.Fprintln(stderr, "Notarization details were returned in an unsupported format.")
		} else {
			writeErr = writeSanitizedNotarizationLog(stderr, filepath.Base(input), log)
		}
		if writeErr != nil {
			return result, fmt.Errorf(
				"notarization was not accepted for %s; the safe issue summary could not be written",
				filepath.Base(input),
			)
		}
		return result, fmt.Errorf(
			"notarization was not accepted for %s (%s); review the safe issue summary above",
			filepath.Base(input),
			sanitizeNotarizationText(result.Status, 80),
		)
	}
	if _, err := fmt.Fprintf(stdout, "Notarization accepted for %s.\n", filepath.Base(input)); err != nil {
		return result, fmt.Errorf(
			"notarization was accepted for %s, but the result could not be written",
			filepath.Base(input),
		)
	}
	return result, nil
}

func parseNotarizationLog(content []byte) (notarizationLog, error) {
	var log notarizationLog
	if err := json.Unmarshal(content, &log); err != nil {
		return notarizationLog{}, fmt.Errorf("decode notarization log: %w", err)
	}
	if strings.TrimSpace(log.Status) == "" && len(log.Issues) == 0 {
		return notarizationLog{}, fmt.Errorf("notarization log is missing a status and issues")
	}
	return log, nil
}

func writeSanitizedNotarizationLog(writer io.Writer, input string, log notarizationLog) error {
	status := sanitizeNotarizationText(log.Status, 80)
	if status == "" {
		status = "Unavailable"
	}
	if _, err := fmt.Fprintf(writer, "Notarization status for %s: %s\n", input, status); err != nil {
		return err
	}
	for _, issue := range log.Issues {
		fields := make([]string, 0, 4)
		for _, field := range []string{issue.Severity, issue.Code, issue.Architecture} {
			if value := sanitizeNotarizationText(field, 80); value != "" {
				fields = append(fields, value)
			}
		}
		if base := filepath.Base(strings.TrimSpace(issue.Path)); base != "." && base != "" {
			fields = append(fields, sanitizeNotarizationText(base, 120))
		}
		label := strings.Join(fields, ", ")
		if label == "" {
			label = "Issue"
		}
		message := issue.Message
		if path := strings.TrimSpace(issue.Path); path != "" {
			message = strings.ReplaceAll(message, issue.Path, filepath.Base(path))
		}
		message = sanitizeNotarizationText(message, 300)
		if message == "" {
			message = "No safe details were provided."
		}
		if _, err := fmt.Fprintf(writer, "- %s: %s\n", label, message); err != nil {
			return err
		}
	}
	return nil
}

func sanitizeNotarizationText(value string, limit int) string {
	value = notaryUUIDPattern.ReplaceAllString(value, "[REDACTED]")
	value = notaryDeveloperIDPattern.ReplaceAllString(value, "[REDACTED]")
	value = notaryTeamIDPattern.ReplaceAllString(value, "[REDACTED]")
	value = notaryEmailPattern.ReplaceAllString(value, "[REDACTED]")
	value = notaryBearerPattern.ReplaceAllString(value, "bearer [REDACTED]")
	value = notarySensitiveAssignmentPattern.ReplaceAllStringFunc(value, func(match string) string {
		separator := strings.IndexAny(match, ":=")
		if separator < 0 {
			return "[REDACTED]"
		}
		return strings.TrimSpace(match[:separator]) + "=[REDACTED]"
	})
	value = notaryAbsolutePathPattern.ReplaceAllStringFunc(value, filepath.Base)
	value = strings.Join(strings.Fields(value), " ")
	if limit < 1 {
		return ""
	}
	runes := []rune(value)
	if len(runes) > limit {
		value = string(runes[:limit-1]) + "…"
	}
	return value
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
