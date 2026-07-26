package releaseartifact

import (
	"reflect"
	"testing"
)

func TestContractUsesTheSameAssetsForStableAndBeta(t *testing.T) {
	t.Parallel()
	stable, err := NewContract("1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	beta, err := NewContract("1.2.3-beta.4")
	if err != nil {
		t.Fatal(err)
	}
	if stable.Channel != ChannelStable || beta.Channel != ChannelBeta {
		t.Fatalf("channels = %q, %q", stable.Channel, beta.Channel)
	}
	if stable.LinuxUpdaterEntry != LinuxUpdaterEntry || beta.LinuxUpdaterEntry != LinuxUpdaterEntry {
		t.Fatalf("Linux updater entries = %q, %q", stable.LinuxUpdaterEntry, beta.LinuxUpdaterEntry)
	}
	if stable.LinuxPackageVersion != "1.2.3" || stable.LinuxPackageRelease != "1" ||
		stable.LinuxDEBVersion != "1.2.3-1" || stable.LinuxRPMVersion != "1.2.3" {
		t.Fatalf("stable package versions = %q, %q, %q",
			stable.LinuxPackageVersion, stable.LinuxDEBVersion, stable.LinuxRPMVersion)
	}
	if beta.LinuxPackageVersion != "1.2.3~beta.4" ||
		beta.LinuxDEBVersion != "1.2.3~beta.4-1" || beta.LinuxRPMVersion != "1.2.3~beta.4" {
		t.Fatalf("beta package versions = %q, %q, %q",
			beta.LinuxPackageVersion, beta.LinuxDEBVersion, beta.LinuxRPMVersion)
	}
	if len(stable.PublicAssets) != 7 || len(beta.PublicAssets) != 7 {
		t.Fatalf("asset counts = %d, %d", len(stable.PublicAssets), len(beta.PublicAssets))
	}
	stableRoles := make([]string, 0, len(stable.PublicAssets))
	betaRoles := make([]string, 0, len(beta.PublicAssets))
	for _, asset := range stable.PublicAssets {
		stableRoles = append(stableRoles, asset.Role)
	}
	for _, asset := range beta.PublicAssets {
		betaRoles = append(betaRoles, asset.Role)
	}
	if !reflect.DeepEqual(stableRoles, betaRoles) {
		t.Fatalf("stable roles %v differ from beta roles %v", stableRoles, betaRoles)
	}
}

func TestContractNamesAndUpdaterTargets(t *testing.T) {
	t.Parallel()
	contract, err := NewContract("1.2.3-beta.4")
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]string{
		RoleMacOSUpdater:   "ProfileDeck_1.2.3-beta.4_macos_universal.zip",
		RoleMacOSInstaller: "ProfileDeck_1.2.3-beta.4_macos_universal.dmg",
		RoleLinuxUpdater:   "ProfileDeck_1.2.3-beta.4_linux_amd64.tar.gz",
		RoleLinuxDEB:       "ProfileDeck_1.2.3-beta.4_linux_amd64.deb",
		RoleLinuxRPM:       "ProfileDeck_1.2.3-beta.4_linux_amd64.rpm",
		RoleManifest:       ManifestName,
		RoleChecksums:      ChecksumsName,
	}
	for role, name := range expected {
		got, err := contract.AssetName(role)
		if err != nil || got != name {
			t.Fatalf("asset %s = %q, %v; want %q", role, got, err, name)
		}
	}
	for _, test := range []struct {
		platform string
		arch     string
		role     string
		filetype string
		payload  string
		kind     string
		want     string
	}{
		{
			PlatformDarwin, ArchARM64, RoleMacOSUpdater, "zip",
			MacOSUpdaterEntry, PayloadAppBundle, expected[RoleMacOSUpdater],
		},
		{
			PlatformDarwin, ArchAMD64, RoleMacOSUpdater, "zip",
			MacOSUpdaterEntry, PayloadAppBundle, expected[RoleMacOSUpdater],
		},
		{
			PlatformLinux, ArchAMD64, RoleLinuxUpdater, "gz",
			LinuxUpdaterEntry, PayloadExecutable, expected[RoleLinuxUpdater],
		},
	} {
		target, err := ResolveUpdateTarget(test.platform, test.arch)
		if err != nil {
			t.Fatalf("ResolveUpdateTarget(%s, %s): %v", test.platform, test.arch, err)
		}
		if target.Platform != test.platform || target.Arch != test.arch ||
			target.Role != test.role || target.Filetype != test.filetype ||
			target.PayloadName != test.payload || target.PayloadKind != test.kind {
			t.Fatalf("ResolveUpdateTarget(%s, %s) = %#v", test.platform, test.arch, target)
		}
		got, err := UpdaterName(contract.Version, test.platform, test.arch)
		if err != nil || got != test.want {
			t.Fatalf("UpdaterName(%s, %s) = %q, %v; want %q", test.platform, test.arch, got, err, test.want)
		}
	}
	if _, err := ResolveUpdateTarget(PlatformLinux, ArchARM64); err == nil {
		t.Fatal("unsupported update target was accepted")
	}
	if _, err := UpdaterName(contract.Version, PlatformLinux, ArchARM64); err == nil {
		t.Fatal("unsupported updater target was accepted")
	}
}

func TestParseVersionRejectsUnsupportedForms(t *testing.T) {
	t.Parallel()
	for _, value := range []string{
		"",
		"dev",
		"v1.2.3",
		"1.2",
		"1.2.3-alpha.1",
		"1.2.3-beta.0",
		"01.2.3",
		"1.02.3",
		"1.2.03",
		"999999999999999999999999999999999999.2.3",
	} {
		if _, err := ParseVersion(value); err == nil {
			t.Fatalf("ParseVersion(%q) succeeded", value)
		}
	}
}
