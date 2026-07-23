//go:build updatee2e

package update

import "github.com/wailsapp/wails/v3/pkg/updater"

// ConfigureForE2E connects the real service to a local GitHub-compatible
// server and a headless Wails updater. It is excluded from production builds.
func ConfigureForE2E(service *Service, engine *updater.Updater, version, baseURL string) error {
	provider, err := newGitHubProvider(version, githubProviderOptions{
		Repository: "test/profiledeck",
		BaseURL:    baseURL,
	})
	if err != nil {
		return err
	}
	if err := engine.Init(updater.Config{
		CurrentVersion: version,
		Providers:      []updater.Provider{provider},
		PublicKey:      append([]byte(nil), service.publicKey...),
		Platform:       UpdatePlatform,
		Arch:           "arm64",
		Window:         updater.WindowNone,
	}); err != nil {
		return err
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	service.provider = provider
	service.engine = engine
	service.status.Configured = true
	service.status.State = StateIdle
	service.status.CurrentVersion = version
	return nil
}
