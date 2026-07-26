package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/strahe/profiledeck/internal/releaseartifact"
)

func TestContractCommandEmitsTheSharedContract(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	if err := run([]string{"contract", "--version", "1.2.3-beta.4"}, &output); err != nil {
		t.Fatal(err)
	}
	var contract releaseartifact.Contract
	if err := json.Unmarshal(output.Bytes(), &contract); err != nil {
		t.Fatalf("decode contract: %v", err)
	}
	if contract.Tag != "v1.2.3-beta.4" || contract.Channel != releaseartifact.ChannelBeta ||
		len(contract.PublicAssets) != 7 || len(contract.UpdateAssets) != 2 {
		t.Fatalf("unexpected contract: %#v", contract)
	}
}

func TestContractCommandEmitsFields(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"tag":                   "v1.2.3",
		"channel":               "stable",
		"linux-updater-entry":   releaseartifact.LinuxUpdaterEntry,
		"linux-package-version": "1.2.3",
		"linux-deb-version":     "1.2.3-1",
		"linux-rpm-version":     "1.2.3",
		"linux-package-release": "1",
		"asset.macos-updater":   "ProfileDeck_1.2.3_macos_universal.zip",
		"asset.macos-installer": "ProfileDeck_1.2.3_macos_universal.dmg",
		"asset.linux-updater":   "ProfileDeck_1.2.3_linux_amd64.tar.gz",
		"asset.linux-deb":       "ProfileDeck_1.2.3_linux_amd64.deb",
		"asset.linux-rpm":       "ProfileDeck_1.2.3_linux_amd64.rpm",
		"asset.manifest":        releaseartifact.ManifestName,
		"asset.checksums":       releaseartifact.ChecksumsName,
	}
	for field, want := range tests {
		field, want := field, want
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			var output bytes.Buffer
			if err := run([]string{"contract", "--version", "1.2.3", "--field", field}, &output); err != nil {
				t.Fatal(err)
			}
			if got := strings.TrimSpace(output.String()); got != want {
				t.Fatalf("%s = %q, want %q", field, got, want)
			}
		})
	}
}

func TestReleasetoolRejectsUnknownCommandsAndFields(t *testing.T) {
	t.Parallel()
	if err := run([]string{"unknown"}, &bytes.Buffer{}); err == nil {
		t.Fatal("unknown command was accepted")
	}
	if err := run([]string{"contract", "--version", "1.2.3", "--field", "missing"}, &bytes.Buffer{}); err == nil {
		t.Fatal("unknown contract field was accepted")
	}
}
