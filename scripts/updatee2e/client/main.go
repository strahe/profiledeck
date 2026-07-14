//go:build updatee2e

package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"

	"github.com/wailsapp/wails/v3/pkg/updater"

	desktopupdate "github.com/strahe/profiledeck/desktop/update"
	coreapp "github.com/strahe/profiledeck/internal/app"
	"github.com/strahe/profiledeck/internal/store"
)

var (
	version   = "dev"
	feedURL   string
	publicKey string
	configDir string
	marker    string
)

func main() {
	updater.HandleHelperMode()
	if version == "0.1.0-alpha.2" {
		finishUpdatedLaunch()
		return
	}
	if err := runUpdate(); err != nil {
		_ = os.WriteFile(marker, []byte("error: "+err.Error()), 0o600)
		os.Exit(1)
	}
}

func runUpdate() error {
	ctx := context.Background()
	application, err := coreapp.New(coreapp.Config{ConfigDir: configDir})
	if err != nil {
		return err
	}
	if _, err := application.Runtime().Init(ctx); err != nil {
		return err
	}
	db, err := application.Runtime().StoreFactory().OpenHealthy(ctx, false)
	if err != nil {
		return err
	}
	if _, err := db.UpsertSetting(ctx, store.UpsertSettingParams{Key: "update.e2e", ValueJSON: `"preserve"`}); err != nil {
		_ = db.Close()
		return err
	}
	if err := db.Close(); err != nil {
		return err
	}
	key, err := base64.StdEncoding.DecodeString(publicKey)
	if err != nil {
		return err
	}
	provider, err := desktopupdate.NewStrictProvider(desktopupdate.ProviderConfig{
		FeedURL: feedURL, PublicKey: key, AllowTestSource: true,
	})
	if err != nil {
		return err
	}
	host := headlessHost{}
	engine := updater.New(host)
	if err := engine.Init(updater.Config{
		CurrentVersion: version,
		Providers:      []updater.Provider{provider},
		PublicKey:      key,
		Platform:       desktopupdate.UpdatePlatform,
		Arch:           desktopupdate.UpdateArch,
		Channel:        desktopupdate.UpdateChannel,
		Window:         updater.WindowNone,
	}); err != nil {
		return err
	}
	service := desktopupdate.NewService(application, desktopupdate.BuildConfig{CurrentVersion: version})
	desktopupdate.ConfigureForE2E(service, provider, key, engine, version)
	status := service.CheckAndDownload(ctx)
	if status.State != desktopupdate.StateReady {
		return fmt.Errorf("update did not become ready: %#v", status)
	}
	return service.Restart(ctx)
}

func finishUpdatedLaunch() {
	ctx := context.Background()
	if err := verifyRunningBundle(); err != nil {
		writeResult(err)
		return
	}
	application, err := coreapp.New(coreapp.Config{ConfigDir: configDir})
	if err != nil {
		writeResult(err)
		return
	}
	if _, err := application.Runtime().Init(ctx); err != nil {
		writeResult(err)
		return
	}
	setting, err := readE2ESetting(ctx, application.Runtime().StoreFactory())
	if err != nil || setting != `"preserve"` {
		writeResult(fmt.Errorf("application data was not preserved: value=%q err=%v", setting, err))
		return
	}
	backups, err := filepath.Glob(filepath.Join(application.Runtime().Paths().UpdateBackups, "*.db"))
	if err != nil || len(backups) == 0 {
		writeResult(fmt.Errorf("update snapshot missing: %v", err))
		return
	}
	sort.Strings(backups)
	snapshotSetting, err := readE2ESetting(ctx, store.NewFactory(backups[len(backups)-1]))
	if err != nil || snapshotSetting != `"preserve"` {
		writeResult(fmt.Errorf("update snapshot is invalid: value=%q err=%v", snapshotSetting, err))
		return
	}
	if info, err := os.Stat(backups[len(backups)-1]); err != nil || info.Mode().Perm() != 0o600 {
		writeResult(fmt.Errorf("update snapshot is not private: info=%v err=%v", info, err))
		return
	}
	writeResult(nil)
}

func verifyRunningBundle() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	bundle := filepath.Clean(executable)
	for current := bundle; current != filepath.Dir(current); current = filepath.Dir(current) {
		if filepath.Ext(current) != ".app" {
			continue
		}
		if output, err := exec.Command("codesign", "--verify", "--deep", "--strict", current).CombinedOutput(); err != nil {
			return fmt.Errorf("updated bundle signature failed: %s: %w", output, err)
		}
		return nil
	}
	return errors.New("updated executable is not inside an application bundle")
}

func readE2ESetting(ctx context.Context, factory store.Factory) (string, error) {
	db, err := factory.OpenHealthy(ctx, true)
	if err != nil {
		return "", err
	}
	defer db.Close()
	setting, err := db.GetSetting(ctx, "update.e2e")
	if err != nil {
		return "", err
	}
	return setting.ValueJSON, nil
}

func writeResult(err error) {
	result := "ok: " + version
	if err != nil {
		result = "error: " + err.Error()
	}
	_ = os.WriteFile(marker, []byte(result), 0o600)
}

type headlessHost struct{}

func (headlessHost) Emit(string, ...any) bool                              { return false }
func (headlessHost) OnEvent(string, func(any)) func()                      { return func() {} }
func (headlessHost) OpenWindow(updater.WindowOptions) updater.WindowHandle { return headlessWindow{} }
func (headlessHost) Quit()                                                 { os.Exit(0) }

type headlessWindow struct{}

func (headlessWindow) EmitEvent(string, ...any) bool { return false }
func (headlessWindow) Show()                         {}
func (headlessWindow) Close()                        {}
