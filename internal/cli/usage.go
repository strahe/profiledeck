package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	urfavecli "github.com/urfave/cli/v3"

	"github.com/strahe/profiledeck/internal/usage"
)

const (
	codexDirFlagName   = "codex-dir"
	usageRangeFlagName = "range"
)

func newUsageCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:  "usage",
		Usage: "Import and summarize local token usage",
		Commands: []*urfavecli.Command{
			newUsageSyncCommand(),
			newUsageSummaryCommand(),
			newUsageReportCommand(),
		},
	}
}

func newUsageReportCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name:  "report",
		Usage: "Analyze local token usage by time and model",
		Flags: []urfavecli.Flag{
			stringFlag(providerFlagName, "Usage provider id"),
			&urfavecli.StringFlag{Name: usageRangeFlagName, Value: string(usage.UsageRange7Days), Usage: "Time range: today, 7d, 30d, or all"},
			boolFlag(jsonFlagName, "Write JSON output"),
		},
		Action: func(ctx context.Context, cmd *urfavecli.Command) error {
			application, err := applicationFor(cmd)
			if err != nil {
				return err
			}
			result, err := application.Usage().Report(ctx, usage.UsageReportRequest{
				ProviderID: cmd.String(providerFlagName),
				Range:      usage.UsageRangePreset(cmd.String(usageRangeFlagName)),
			})
			if err != nil {
				return err
			}
			w := outputWriter(cmd)
			if cmd.Bool(jsonFlagName) {
				return writeJSON(w, result)
			}
			return writeUsageReport(w, result)
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
			application, err := applicationFor(cmd)
			if err != nil {
				return err
			}
			result, err := application.Usage().SyncCodex(ctx)
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
			application, err := applicationFor(cmd)
			if err != nil {
				return err
			}
			result, err := application.Usage().Summary(ctx, usage.UsageSummaryRequest{
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

func writeUsageSyncResult(w io.Writer, result usage.UsageSyncResult) error {
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

func writeUsageSummary(w io.Writer, result usage.UsageSummaryResult) error {
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

func writeUsageReport(w io.Writer, result usage.UsageReportResult) error {
	lastSync := "never"
	if result.Import.LastSyncedAtUnixMS > 0 {
		lastSync = time.UnixMilli(result.Import.LastSyncedAtUnixMS).Format(time.RFC3339)
	}
	if _, err := fmt.Fprintf(
		w,
		"Usage report\nprovider: %s\nrange: %s\ntime zone: %s\nevents: %d\nsessions: %d\nfresh input tokens: %d\ncached input tokens: %d\noutput tokens: %d\ntotal tokens: %d\ncache hit rate: %.1f%%\nknown API-equivalent estimated cost usd: %s\ncost status: %s\npricing coverage: %.1f%%\nundated events: %d\ntracked files: %d\nlast sync: %s\ninvalid lines: %d\nunsupported lines: %d\npricing basis: %s\n\nTrend\n",
		result.ProviderID,
		result.Range.Preset,
		result.Range.TimeZone,
		result.Summary.EventCount,
		result.Summary.SessionCount,
		result.Summary.FreshInputTokens,
		result.Summary.CachedInputTokens,
		result.Summary.OutputTokens,
		result.Summary.TotalTokens,
		result.Summary.CacheHitRate*100,
		result.Summary.KnownEstimatedCostUSD,
		result.Summary.CostStatus,
		result.Summary.PricingCoverage*100,
		result.Summary.UndatedEventCount,
		result.Import.TrackedFiles,
		lastSync,
		result.Import.InvalidLines,
		result.Import.UnsupportedLines,
		result.Pricing.Basis,
	); err != nil {
		return err
	}

	table := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(table, "bucket\tfresh input\tcached input\toutput\ttotal"); err != nil {
		return err
	}
	for _, point := range result.Trend {
		if _, err := fmt.Fprintf(
			table,
			"%s\t%d\t%d\t%d\t%d\n",
			usageBucketLabel(result.Range, point.StartUnixMS),
			point.Summary.FreshInputTokens,
			point.Summary.CachedInputTokens,
			point.Summary.OutputTokens,
			point.Summary.TotalTokens,
		); err != nil {
			return err
		}
	}
	if err := table.Flush(); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "\nModels"); err != nil {
		return err
	}
	table = tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(table, "model\tsessions\ttokens\tcache hit\tknown cost usd\tstatus"); err != nil {
		return err
	}
	for _, model := range result.Models {
		if _, err := fmt.Fprintf(
			table,
			"%s\t%d\t%d\t%.1f%%\t%s\t%s\n",
			model.Model,
			model.Summary.SessionCount,
			model.Summary.TotalTokens,
			model.Summary.CacheHitRate*100,
			model.Summary.KnownEstimatedCostUSD,
			model.Summary.CostStatus,
		); err != nil {
			return err
		}
	}
	return table.Flush()
}

func usageBucketLabel(resolved usage.UsageResolvedRange, unixMS int64) string {
	location := time.Local
	if resolved.TimeZone != "" {
		if parsed, err := time.LoadLocation(resolved.TimeZone); err == nil {
			location = parsed
		}
	}
	value := time.UnixMilli(unixMS).In(location)
	switch resolved.BucketUnit {
	case "hour":
		return value.Format("2006-01-02 15:04 MST")
	case "month":
		return value.Format("2006-01")
	case "year":
		return value.Format("2006")
	default:
		return value.Format("2006-01-02")
	}
}
