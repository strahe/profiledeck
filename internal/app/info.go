package app

const (
	ProductName       = "ProfileDeck"
	CLIName           = "profiledeck"
	DefaultVersion    = "dev"
	UnknownBuildValue = "unknown"
)

type Info struct {
	ProductName string
	CLIName     string
	Version     string
	Commit      string
	BuildDate   string
}

func DefaultInfo() Info {
	return NewInfo(DefaultVersion, UnknownBuildValue, UnknownBuildValue)
}

func NewInfo(version, commit, buildDate string) Info {
	if version == "" {
		version = DefaultVersion
	}
	if commit == "" {
		commit = UnknownBuildValue
	}
	if buildDate == "" {
		buildDate = UnknownBuildValue
	}

	return Info{
		ProductName: ProductName,
		CLIName:     CLIName,
		Version:     version,
		Commit:      commit,
		BuildDate:   buildDate,
	}
}
