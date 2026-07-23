//go:build updatee2e

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/updater"

	desktopupdate "github.com/strahe/profiledeck/desktop/update"
	coreapp "github.com/strahe/profiledeck/internal/app"
	"github.com/strahe/profiledeck/internal/appbackup"
	"github.com/strahe/profiledeck/internal/store"
)

var (
	version               = "dev"
	githubBaseURL         string
	configDir             string
	marker                string
	updatePublicKeyBase64 string
)

func main() {
	updater.HandleHelperMode()
	if version == "0.1.0-beta.2" {
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
	defer application.Close()
	if _, err := application.Initialize(ctx); err != nil {
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
	host := &headlessHost{}
	engine := updater.New(host)
	service := desktopupdate.NewService(ctx, application, desktopupdate.BuildConfig{
		CurrentVersion:  version,
		PublicKeyBase64: updatePublicKeyBase64,
	})
	if err := desktopupdate.ConfigureForE2E(service, engine, version, githubBaseURL); err != nil {
		return err
	}
	status := service.CheckAndDownload(ctx)
	if status.State != desktopupdate.StateReady {
		return fmt.Errorf("update did not become ready: %#v: %s", status, host.LastError())
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
	defer application.Close()
	if _, err := application.Initialize(ctx); err != nil {
		writeResult(err)
		return
	}
	setting, err := readE2ESetting(ctx, application.Runtime().StoreFactory())
	if err != nil || setting != `"preserve"` {
		writeResult(fmt.Errorf("application data was not preserved: value=%q err=%v", setting, err))
		return
	}
	backups, err := application.Backups().List(ctx)
	if err != nil || len(backups.Backups) == 0 {
		writeResult(fmt.Errorf("update application backup missing: %v", err))
		return
	}
	backup := backups.Backups[0]
	detail, err := application.Backups().Show(ctx, backup.ID)
	if err != nil || detail.Reason != appbackup.ReasonBeforeUpdate {
		writeResult(fmt.Errorf("update application backup is invalid: detail=%#v err=%v", detail, err))
		return
	}
	preview, err := application.Backups().PreviewRestore(ctx, appbackup.RestoreSource{BackupID: backup.ID})
	if err != nil || preview.Fingerprint == "" {
		writeResult(fmt.Errorf("update application backup could not be verified: err=%v", err))
		return
	}
	backupPath := filepath.Join(application.Runtime().Paths().Backups, backup.ID+appbackup.Extension)
	if info, err := os.Stat(backupPath); err != nil || info.Mode().Perm() != 0o600 {
		writeResult(fmt.Errorf("update application backup is not private: info=%v err=%v", info, err))
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

type headlessHost struct {
	mu        sync.Mutex
	lastError string
}

func (host *headlessHost) Emit(name string, data ...any) bool {
	if name == updater.EventError && len(data) > 0 {
		host.mu.Lock()
		host.lastError = fmt.Sprint(data[0])
		host.mu.Unlock()
	}
	return false
}

func (host *headlessHost) LastError() string {
	host.mu.Lock()
	defer host.mu.Unlock()
	return host.lastError
}

func (*headlessHost) OnEvent(string, func(any)) func()                      { return func() {} }
func (*headlessHost) OpenWindow(updater.WindowOptions) updater.WindowHandle { return headlessWindow{} }
func (*headlessHost) Quit()                                                 { os.Exit(0) }

type headlessWindow struct{}

func (headlessWindow) EmitEvent(string, ...any) bool { return false }
func (headlessWindow) Show()                         {}
func (headlessWindow) Close()                        {}
