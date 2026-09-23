package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	urfavecli "github.com/urfave/cli/v3"

	"github.com/strahe/profiledeck/internal/pricing"
)

func newUsagePricingCommand() *urfavecli.Command {
	return &urfavecli.Command{
		Name: "pricing", Usage: "Check prices used for local usage estimates",
		Commands: []*urfavecli.Command{
			{
				Name: "status", Usage: "Show price update status", Flags: []urfavecli.Flag{boolFlag(jsonFlagName, "Write JSON output")},
				Action: func(ctx context.Context, cmd *urfavecli.Command) error {
					application, err := applicationFor(cmd)
					if err != nil {
						return err
					}
					status, err := application.Pricing().Status(ctx)
					if err != nil {
						return err
					}
					return writePricingStatus(outputWriter(cmd), status, cmd.Bool(jsonFlagName), "")
				},
			},
			{
				Name: "check", Usage: "Check for an updated price list", Flags: []urfavecli.Flag{boolFlag(jsonFlagName, "Write JSON output")},
				Action: func(ctx context.Context, cmd *urfavecli.Command) error {
					application, err := applicationFor(cmd)
					if err != nil {
						return err
					}
					previous, err := application.Pricing().Status(ctx)
					if err != nil {
						return err
					}
					status, err := application.Pricing().Check(ctx, true)
					if err != nil {
						return err
					}
					message := "Prices are up to date."
					if status.CatalogVersion > previous.CatalogVersion {
						message = "Prices updated."
					}
					return writePricingStatus(outputWriter(cmd), status, cmd.Bool(jsonFlagName), message)
				},
			},
			{
				Name: "auto", Usage: "Turn automatic price checks on or off", ArgsUsage: "<on|off>",
				Action: func(ctx context.Context, cmd *urfavecli.Command) error {
					value := cmd.Args().First()
					if value != "on" && value != "off" {
						return fmt.Errorf("choose on or off")
					}
					application, err := applicationFor(cmd)
					if err != nil {
						return err
					}
					status, err := application.Pricing().SetAutomatic(ctx, value == "on")
					if err != nil {
						return err
					}
					return writePricingStatus(outputWriter(cmd), status, false, "")
				},
			},
		},
	}
}

func writePricingStatus(w io.Writer, status pricing.Status, asJSON bool, checkMessage string) error {
	if asJSON {
		return writeJSON(w, status)
	}
	message := "Using the price list included with this version."
	if status.LastUpdatedAtUnixMS > 0 {
		message = "Prices last updated " + formatPricingTime(status.LastUpdatedAtUnixMS)
	}
	if checkMessage != "" {
		message = checkMessage
	}
	if status.LastError != "" {
		message = "Could not check prices. Usage estimates remain available; try again later."
	}
	_, err := fmt.Fprintf(w, "%s\nversion: %d\nautomatic checks: %t\nlast checked: %s\n", message,
		status.CatalogVersion, status.Automatic, formatPricingTime(status.LastCheckedAtUnixMS))
	return err
}

func formatPricingTime(value int64) string {
	if value <= 0 {
		return "never"
	}
	return time.UnixMilli(value).Local().Format(time.RFC3339)
}
