package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/strahe/profiledeck/internal/app"
	urfavecli "github.com/urfave/cli/v3"
)

const codexDirFlagName = "codex-dir"

func newUsageCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:  "usage",
		Usage: "Import and summarize local token usage",
		Commands: []*urfavecli.Command{
			newUsageSyncCommand(),
			newUsageSummaryCommand(),
		},
	}
}

func newUsageSyncCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:  "sync",
		Usage: "Import local token usage",
		Commands: []*urfavecli.Command{
			newUsageSyncCodexCommand(),
		},
	}
}

func newUsageSyncCodexCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:  "codex",
		Usage: "Import Codex local session usage",
		Flags: []urfavecli.Flag{
			stringFlag(codexDirFlagName, "Codex config directory"),
			boolFlag(jsonFlagName, "Write JSON output"),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			result, err := app.UsageSyncCodex(ctx, app.UsageSyncCodexRequest{
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
			return writeUsageSyncResult(w, result)
		},
	}
}

func newUsageSummaryCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:  "summary",
		Usage: "Print local token usage summary",
		Flags: []urfavecli.Flag{
			stringFlag(providerFlagName, "Usage provider id"),
			boolFlag(jsonFlagName, "Write JSON output"),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			result, err := app.UsageSummary(ctx, app.UsageSummaryRequest{
				ConfigDir:  configDirValue(cmd),
				ProviderID: cmd.String(providerFlagName),
			})
			if err != nil {
				return err
			}
			w := outputWriter(cmd)
			if cmd.Bool(jsonFlagName) {
				return writeJSON(w, result)
			}
			return writeUsageSummary(w, result)
		},
	}
}

func writeUsageSyncResult(w io.Writer, result app.UsageSyncResult) error {
	if _, err := fmt.Fprintf(
		w,
		"Usage sync\nprovider: %s\nsource: %s\nscanned files: %d\nskipped unchanged files: %d\nimported events: %d\nskipped duplicate events: %d\nunsupported lines: %d\ninvalid lines: %d\nerrors: %d\n",
		result.ProviderID,
		result.Source,
		result.ScannedFiles,
		result.SkippedUnchangedFiles,
		result.ImportedEvents,
		result.SkippedDuplicateEvents,
		result.UnsupportedLines,
		result.InvalidLines,
		len(result.Errors),
	); err != nil {
		return err
	}
	if len(result.Errors) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(w, "error details:"); err != nil {
		return err
	}
	for _, item := range result.Errors {
		fileName := item.FileName
		if fileName == "" {
			fileName = "unknown"
		}
		if _, err := fmt.Fprintf(w, "- file: %s source_key: %s error: %s\n", fileName, item.SourceKey, item.Message); err != nil {
			return err
		}
	}
	return nil
}

func writeUsageSummary(w io.Writer, result app.UsageSummaryResult) error {
	cost := "unknown"
	if result.EstimatedCostUSD != nil {
		cost = *result.EstimatedCostUSD
	}
	sources := strings.Join(result.Sources, ",")
	_, err := fmt.Fprintf(
		w,
		"Usage summary\nprovider: %s\nsource: %s\nsources: %s\nevents: %d\ninput tokens: %d\ncached input tokens: %d\noutput tokens: %d\ntotal tokens: %d\ncost status: %s\nestimated cost usd: %s\nunknown cost events: %d\n",
		result.ProviderID,
		result.Source,
		sources,
		result.EventCount,
		result.InputTokens,
		result.CachedInputTokens,
		result.OutputTokens,
		result.TotalTokens,
		result.CostStatus,
		cost,
		result.UnknownCostEventCount,
	)
	return err
}
