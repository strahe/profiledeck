package main

import (
	"fmt"
	"io"
	"sort"
)

const (
	releaseContractSchemaVersion = 1
	productName                  = "ProfileDeck"
	macOSPlatform                = "macos"
	assetRoleUpdater             = "updater"
	assetRoleSignature           = "signature"
	assetRoleInstaller           = "installer"
)

type releaseContract struct {
	SchemaVersion int                       `json:"schema_version"`
	Product       string                    `json:"product"`
	Version       string                    `json:"version"`
	ShortVersion  string                    `json:"short_version"`
	Tag           string                    `json:"tag"`
	Channel       string                    `json:"channel"`
	Platforms     []releasePlatformContract `json:"platforms"`
	PublicAssets  []releaseAssetSpec        `json:"public_assets"`
}

type releasePlatformContract struct {
	ID         string             `json:"id"`
	Assets     []releaseAssetSpec `json:"assets"`
	AssetNames map[string]string  `json:"asset_names"`
}

func platformAssetSpecs(platform string, version releaseVersion) ([]releaseAssetSpec, error) {
	if !platformNamePattern.MatchString(platform) {
		return nil, fmt.Errorf("release platform is invalid")
	}
	var specs []releaseAssetSpec
	switch platform {
	case macOSPlatform:
		specs = []releaseAssetSpec{
			{Name: updaterZIPName(version), Role: assetRoleUpdater},
			{Name: updaterSignatureName(version), Role: assetRoleSignature},
			{Name: installerDMGName(version), Role: assetRoleInstaller},
		}
	default:
		return nil, fmt.Errorf("unsupported release platform %q", platform)
	}
	sort.Slice(specs, func(left, right int) bool { return specs[left].Name < specs[right].Name })
	return specs, nil
}

func buildReleaseContract(version releaseVersion, platforms string) (releaseContract, error) {
	definitions, err := parseReleasePlatforms(platforms, version)
	if err != nil {
		return releaseContract{}, err
	}
	contract := releaseContract{
		SchemaVersion: releaseContractSchemaVersion,
		Product:       productName,
		Version:       version.String(),
		ShortVersion:  version.short(),
		Tag:           version.tag(),
		Channel:       version.channel(),
	}
	for _, definition := range definitions {
		assetNames := make(map[string]string, len(definition.Specs))
		for _, asset := range definition.Specs {
			assetNames[asset.Role] = asset.Name
		}
		contract.Platforms = append(contract.Platforms, releasePlatformContract{
			ID: definition.Name, Assets: definition.Specs, AssetNames: assetNames,
		})
		contract.PublicAssets = append(contract.PublicAssets, definition.Specs...)
	}
	contract.PublicAssets = append(contract.PublicAssets, releaseAssetSpec{Name: "SHA256SUMS", Role: "checksums"})
	sort.Slice(contract.PublicAssets, func(left, right int) bool {
		return contract.PublicAssets[left].Name < contract.PublicAssets[right].Name
	})
	return contract, nil
}

func writeContractField(writer io.Writer, contract releaseContract, field string) error {
	var values []string
	switch field {
	case "product":
		values = []string{contract.Product}
	case "version":
		values = []string{contract.Version}
	case "short-version":
		values = []string{contract.ShortVersion}
	case "tag":
		values = []string{contract.Tag}
	case "channel":
		values = []string{contract.Channel}
	case "public-assets":
		for _, asset := range contract.PublicAssets {
			values = append(values, asset.Name)
		}
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
