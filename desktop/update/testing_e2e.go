//go:build updatee2e

package update

import "github.com/wailsapp/wails/v3/pkg/updater"

// ConfigureForE2E connects the real service to a local signed feed and a
// headless Wails updater. It is excluded from production builds.
func ConfigureForE2E(service *Service, provider *StrictProvider, publicKey []byte, engine *updater.Updater, version string) {
	service.mu.Lock()
	defer service.mu.Unlock()
	service.provider = provider
	service.publicKey = append([]byte(nil), publicKey...)
	service.engine = engine
	service.status.Configured = true
	service.status.State = StateIdle
	service.status.CurrentVersion = version
}
