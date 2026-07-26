package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/strahe/profiledeck/internal/releaseartifact"
)

func runContract(args []string, stdout io.Writer) error {
	flags := newFlagSet("contract")
	version := flags.String("version", "", "release version")
	field := flags.String("field", "", "single contract field")
	if err := flags.Parse(args); err != nil {
		return err
	}
	contract, err := releaseartifact.NewContract(*version)
	if err != nil {
		return err
	}
	if *field != "" {
		return writeContractField(stdout, contract, *field)
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(contract)
}

func writeContractField(writer io.Writer, contract releaseartifact.Contract, field string) error {
	var values []string
	switch {
	case field == "product":
		values = []string{contract.Product}
	case field == "version":
		values = []string{contract.Version}
	case field == "short-version":
		values = []string{contract.ShortVersion}
	case field == "tag":
		values = []string{contract.Tag}
	case field == "channel":
		values = []string{contract.Channel}
	case field == "linux-updater-entry":
		values = []string{contract.LinuxUpdaterEntry}
	case field == "linux-package-version":
		values = []string{contract.LinuxPackageVersion}
	case field == "linux-deb-version":
		values = []string{contract.LinuxDEBVersion}
	case field == "linux-rpm-version":
		values = []string{contract.LinuxRPMVersion}
	case field == "linux-package-release":
		values = []string{contract.LinuxPackageRelease}
	case field == "public-assets":
		for _, asset := range contract.PublicAssets {
			values = append(values, asset.Name)
		}
	case field == "update-assets":
		for _, asset := range contract.UpdateAssets {
			values = append(values, asset.Name)
		}
	case strings.HasPrefix(field, "asset."):
		name, err := contract.AssetName(strings.TrimPrefix(field, "asset."))
		if err != nil {
			return err
		}
		values = []string{name}
	default:
		return fmt.Errorf("unknown contract field %q", field)
	}
	for _, value := range values {
		if _, err := fmt.Fprintln(writer, value); err != nil {
			return fmt.Errorf("write contract field: %w", err)
		}
	}
	return nil
}
