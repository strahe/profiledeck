package config

const (
	ProviderID     = "claude-code"
	ProviderName   = "Claude Code"
	AdapterID      = "claude-code-official-oauth"
	PresetName     = "claude-code"
	PresetVersion  = 1
	CredentialKind = "claude-code-subscription-oauth"
	CredentialSlot = "auth"
	TargetID       = "auth"

	StorageKeychain = "keychain"
	StorageFile     = "file"

	KeychainService = "Claude Code-credentials"
	CredentialsFile = ".credentials.json"
)

type Locator struct {
	Storage string
	Path    string
	Service string
	Account string
}
