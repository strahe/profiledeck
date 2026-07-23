package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestReleaseContractDescribesVersionAndPublicAssets(t *testing.T) {
	t.Parallel()
	version, _ := parseReleaseVersion("1.2.3-beta.4")
	contract, err := buildReleaseContract(version, macOSPlatform)
	if err != nil {
		t.Fatal(err)
	}
	if contract.SchemaVersion != 1 || contract.Product != productName || contract.Tag != "v1.2.3-beta.4" ||
		contract.Channel != "beta" || contract.ShortVersion != "1.2.3" {
		t.Fatalf("unexpected contract: %#v", contract)
	}
	if len(contract.Platforms) != 1 || len(contract.Platforms[0].Assets) != 3 || len(contract.PublicAssets) != 4 {
		t.Fatalf("unexpected contract assets: %#v", contract)
	}
	var output bytes.Buffer
	if err := writeContractField(&output, contract, "public-assets"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{updaterZIPName(version), updaterSignatureName(version), installerDMGName(version), "SHA256SUMS"} {
		if !strings.Contains(output.String(), name+"\n") {
			t.Fatalf("public asset output missing %s: %q", name, output.String())
		}
	}
}

func TestContractCommandEmitsStableVersionedJSON(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	if err := run([]string{"contract", "--version", "1.2.3", "--platforms", macOSPlatform}, &output); err != nil {
		t.Fatal(err)
	}
	var contract releaseContract
	if err := json.Unmarshal(output.Bytes(), &contract); err != nil {
		t.Fatalf("decode contract JSON: %v: %s", err, output.String())
	}
	if contract.SchemaVersion != releaseContractSchemaVersion || contract.Version != "1.2.3" ||
		contract.ShortVersion != "1.2.3" || contract.Tag != "v1.2.3" || contract.Channel != "stable" {
		t.Fatalf("unexpected stable contract: %#v", contract)
	}
	if len(contract.Platforms) != 1 {
		t.Fatalf("stable contract platforms: %#v", contract.Platforms)
	}
	if got := contract.Platforms[0].AssetNames[assetRoleUpdater]; got != "ProfileDeck_1.2.3_macos_universal.zip" {
		t.Fatalf("updater asset name = %q", got)
	}
}

func TestRequireNewerVersionUsesReleaseOrdering(t *testing.T) {
	t.Parallel()
	candidate, _ := parseReleaseVersion("1.2.3")
	if err := requireNewerVersion(candidate, strings.NewReader("v1.2.3-beta.8\nnot-a-release\n")); err != nil {
		t.Fatal(err)
	}
	if err := requireNewerVersion(candidate, strings.NewReader("v1.2.3\n")); err == nil {
		t.Fatal("equal published version was accepted")
	}
	beta, _ := parseReleaseVersion("1.2.3-beta.9")
	if err := requireNewerVersion(beta, strings.NewReader("v1.2.3\n")); err == nil {
		t.Fatal("beta after stable release was accepted")
	}
}
