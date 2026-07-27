// Package releaseartifact defines ProfileDeck's public release versions and asset names.
package releaseartifact

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
)

const (
	SchemaVersion  = 1
	Product        = "ProfileDeck"
	PackageRelease = 1

	ChannelStable = "stable"
	ChannelBeta   = "beta"

	PlatformDarwin = "darwin"
	PlatformLinux  = "linux"
	ArchARM64      = "arm64"
	ArchAMD64      = "amd64"

	DesktopExecutableName = "profiledeck"
	MacOSUpdaterEntry     = Product + ".app"
	LinuxUpdaterEntry     = DesktopExecutableName
	PayloadAppBundle      = "app-bundle"
	PayloadExecutable     = "executable"

	RoleMacOSUpdater   = "macos-updater"
	RoleMacOSInstaller = "macos-installer"
	RoleLinuxUpdater   = "linux-updater"
	RoleLinuxDEB       = "linux-deb"
	RoleLinuxRPM       = "linux-rpm"
	RoleManifest       = "manifest"
	RoleChecksums      = "checksums"

	ManifestName  = "updates.json"
	ChecksumsName = "SHA256SUMS"
)

var versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-beta\.([1-9][0-9]*))?$`)

type Version struct {
	major int
	minor int
	patch int
	beta  int
}

func ParseVersion(value string) (Version, error) {
	matches := versionPattern.FindStringSubmatch(value)
	if matches == nil {
		return Version{}, fmt.Errorf("version must be X.Y.Z or X.Y.Z-beta.N")
	}
	components := make([]int, 4)
	for index := 1; index <= 4; index++ {
		if matches[index] == "" {
			continue
		}
		component, err := strconv.Atoi(matches[index])
		if err != nil {
			return Version{}, fmt.Errorf("version component is too large")
		}
		components[index-1] = component
	}
	return Version{
		major: components[0],
		minor: components[1],
		patch: components[2],
		beta:  components[3],
	}, nil
}

func (version Version) String() string {
	base := version.Short()
	if version.beta > 0 {
		return fmt.Sprintf("%s-beta.%d", base, version.beta)
	}
	return base
}

func (version Version) Short() string {
	return fmt.Sprintf("%d.%d.%d", version.major, version.minor, version.patch)
}

func (version Version) Tag() string {
	return "v" + version.String()
}

func (version Version) Channel() string {
	if version.beta > 0 {
		return ChannelBeta
	}
	return ChannelStable
}

func (version Version) LinuxPackageVersion() string {
	value := version.Short()
	if version.beta > 0 {
		return fmt.Sprintf("%s~beta.%d", value, version.beta)
	}
	return value
}

type Asset struct {
	Name string `json:"name"`
	Role string `json:"role"`
}

type UpdateTarget struct {
	Platform    string
	Arch        string
	Role        string
	Filetype    string
	PayloadName string
	PayloadKind string
}

type Contract struct {
	SchemaVersion       int     `json:"schema_version"`
	Product             string  `json:"product"`
	Version             string  `json:"version"`
	ShortVersion        string  `json:"short_version"`
	Tag                 string  `json:"tag"`
	Channel             string  `json:"channel"`
	LinuxUpdaterEntry   string  `json:"linux_updater_entry"`
	LinuxPackageVersion string  `json:"linux_package_version"`
	LinuxPackageRelease string  `json:"linux_package_release"`
	LinuxDEBVersion     string  `json:"linux_deb_version"`
	LinuxRPMVersion     string  `json:"linux_rpm_version"`
	PublicAssets        []Asset `json:"public_assets"`
	UpdateAssets        []Asset `json:"update_assets"`
}

func NewContract(value string) (Contract, error) {
	version, err := ParseVersion(value)
	if err != nil {
		return Contract{}, err
	}
	macOSUpdater := Asset{
		Name: fmt.Sprintf("%s_%s_macos_universal.zip", Product, version),
		Role: RoleMacOSUpdater,
	}
	linuxUpdater := Asset{
		Name: fmt.Sprintf("%s_%s_linux_amd64.tar.gz", Product, version),
		Role: RoleLinuxUpdater,
	}
	linuxPackageVersion := version.LinuxPackageVersion()
	contract := Contract{
		SchemaVersion:       SchemaVersion,
		Product:             Product,
		Version:             version.String(),
		ShortVersion:        version.Short(),
		Tag:                 version.Tag(),
		Channel:             version.Channel(),
		LinuxUpdaterEntry:   LinuxUpdaterEntry,
		LinuxPackageVersion: linuxPackageVersion,
		LinuxPackageRelease: strconv.Itoa(PackageRelease),
		LinuxDEBVersion:     fmt.Sprintf("%s-%d", linuxPackageVersion, PackageRelease),
		LinuxRPMVersion:     linuxPackageVersion,
		UpdateAssets:        []Asset{macOSUpdater, linuxUpdater},
		PublicAssets: []Asset{
			macOSUpdater,
			{
				Name: fmt.Sprintf("%s_%s_macos_universal.dmg", Product, version),
				Role: RoleMacOSInstaller,
			},
			linuxUpdater,
			{
				Name: fmt.Sprintf("%s_%s_linux_amd64.deb", Product, version),
				Role: RoleLinuxDEB,
			},
			{
				Name: fmt.Sprintf("%s_%s_linux_amd64.rpm", Product, version),
				Role: RoleLinuxRPM,
			},
			{Name: ManifestName, Role: RoleManifest},
			{Name: ChecksumsName, Role: RoleChecksums},
		},
	}
	sort.Slice(contract.PublicAssets, func(left, right int) bool {
		return contract.PublicAssets[left].Name < contract.PublicAssets[right].Name
	})
	sort.Slice(contract.UpdateAssets, func(left, right int) bool {
		return contract.UpdateAssets[left].Name < contract.UpdateAssets[right].Name
	})
	return contract, nil
}

func (contract Contract) AssetName(role string) (string, error) {
	for _, asset := range contract.PublicAssets {
		if asset.Role == role {
			return asset.Name, nil
		}
	}
	return "", fmt.Errorf("asset role %q is not present in the release contract", role)
}

func ResolveUpdateTarget(platform, arch string) (UpdateTarget, error) {
	switch {
	case platform == PlatformDarwin && (arch == ArchARM64 || arch == ArchAMD64):
		return UpdateTarget{
			Platform:    PlatformDarwin,
			Arch:        arch,
			Role:        RoleMacOSUpdater,
			Filetype:    "zip",
			PayloadName: MacOSUpdaterEntry,
			PayloadKind: PayloadAppBundle,
		}, nil
	case platform == PlatformLinux && arch == ArchAMD64:
		return UpdateTarget{
			Platform:    PlatformLinux,
			Arch:        ArchAMD64,
			Role:        RoleLinuxUpdater,
			Filetype:    "gz",
			PayloadName: LinuxUpdaterEntry,
			PayloadKind: PayloadExecutable,
		}, nil
	default:
		return UpdateTarget{}, fmt.Errorf("updates are not supported for %s/%s", platform, arch)
	}
}

func UpdaterName(version, platform, arch string) (string, error) {
	contract, err := NewContract(version)
	if err != nil {
		return "", err
	}
	target, err := ResolveUpdateTarget(platform, arch)
	if err != nil {
		return "", err
	}
	return contract.AssetName(target.Role)
}
