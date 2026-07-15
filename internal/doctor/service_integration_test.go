package doctor_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/strahe/profiledeck/internal/agent"
	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/codex"
	codexadapter "github.com/strahe/profiledeck/internal/codex/adapter"
	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	"github.com/strahe/profiledeck/internal/doctor"
	"github.com/strahe/profiledeck/internal/profile"
	"github.com/strahe/profiledeck/internal/profiletarget"
	"github.com/strahe/profiledeck/internal/provider"
	profilesruntime "github.com/strahe/profiledeck/internal/runtime"
	"github.com/strahe/profiledeck/internal/store"
	"github.com/strahe/profiledeck/internal/switching"
	switchplan "github.com/strahe/profiledeck/internal/switching/plan"
	switchtarget "github.com/strahe/profiledeck/internal/switching/target"
)

type doctorTestApplication struct {
	runtime   *profilesruntime.Service
	doctor    *doctor.Service
	providers *provider.Service
	profiles  *profile.Service
	targets   *profiletarget.Service
	switching *switching.Service
	codex     *codex.Service
}

func newDoctorTestApplication(t *testing.T, configDir, codexDir string) *doctorTestApplication {
	t.Helper()
	runtimeService, err := profilesruntime.NewService(configDir)
	if err != nil {
		t.Fatalf("create runtime service: %v", err)
	}
	registry := agent.BuiltinRegistry()
	agentService := agent.NewService(registry, runtimeService.StoreFactory(), agent.AccessUnrestricted)
	dependencies := switching.NewDependencies(
		switchtarget.MustRegistry(switchtarget.FileBackend{}),
		switchplan.MustRegistry(switchplan.GenericAdapter{}, codexadapter.Adapter{}),
	)
	switchingService := switching.NewService(runtimeService.Paths(), runtimeService.StoreFactory(), agentService, dependencies)
	codexService := codex.NewService(runtimeService, switchingService, switchingService, agentService, codexDir)
	doctorService := doctor.NewService(
		runtimeService,
		agentService,
		[]doctor.ProviderCheck{{AgentID: agent.Codex, Check: codexService.HealthCheck}},
		func(ctx context.Context, db *store.Store, paths profilesruntime.Paths, operation store.Operation) (string, string, string) {
			inspection := switchingService.InspectRecoveryFromOperation(ctx, db, paths, operation)
			return inspection.Status, inspection.Action, inspection.Reason
		},
		codexService.SensitivePaths,
	)
	return &doctorTestApplication{
		runtime: runtimeService, doctor: doctorService,
		providers: provider.NewService(runtimeService.StoreFactory(), switchingService, agentService, registry),
		profiles:  profile.NewService(runtimeService.StoreFactory(), switchingService),
		targets: profiletarget.NewService(
			runtimeService.StoreFactory(), switchingService, agentService, registry, codexService.ReservedPaths,
		),
		switching: switchingService, codex: codexService,
	}
}

func TestDisabledAgentSkipsProviderHealthCheckButKeepsOperationInspection(t *testing.T) {
	ctx := context.Background()
	runtimeService, err := profilesruntime.NewService(t.TempDir())
	if err != nil {
		t.Fatalf("create runtime Service: %v", err)
	}
	if _, err := runtimeService.Init(ctx); err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}
	agentService := agent.NewService(
		agent.BuiltinRegistry(), runtimeService.StoreFactory(), agent.AccessDesktopPreferences,
	)
	if _, err := agentService.SetEnabled(ctx, agent.Codex, false); err != nil {
		t.Fatalf("disable Codex Agent: %v", err)
	}

	db, err := runtimeService.StoreFactory().OpenHealthy(ctx, false)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if _, err := db.CreatePendingSwitchOperation(ctx, store.CreateSwitchOperationParams{
		ID: "switch-disabled-agent", ProfileID: "profile-a",
		MetadataJSON: `{"checkpoint":"planned","provider_id":"codex","profile_id":"profile-a"}`,
	}); err != nil {
		_ = db.Close()
		t.Fatalf("create pending operation: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	providerCheckCalls := 0
	service := doctor.NewService(
		runtimeService,
		agentService,
		[]doctor.ProviderCheck{{
			AgentID: agent.Codex,
			Check: func(context.Context, *store.Store) ([]doctor.Finding, error) {
				providerCheckCalls++
				return []doctor.Finding{{ID: "unexpected-provider-check", Level: doctor.LevelError}}, nil
			},
		}},
		nil,
		nil,
	)
	result, err := service.Run(ctx)
	if err != nil {
		t.Fatalf("run Doctor: %v", err)
	}
	if providerCheckCalls != 0 {
		t.Fatalf("disabled Agent provider checker ran %d times", providerCheckCalls)
	}
	if len(result.Operations) != 1 || result.Operations[0].ID != "switch-disabled-agent" {
		t.Fatalf("generic operation inspection was skipped: %#v", result.Operations)
	}
}

func (application *doctorTestApplication) Runtime() *profilesruntime.Service {
	return application.runtime
}
func (application *doctorTestApplication) Doctor() *doctor.Service      { return application.doctor }
func (application *doctorTestApplication) Providers() *provider.Service { return application.providers }
func (application *doctorTestApplication) Profiles() *profile.Service   { return application.profiles }
func (application *doctorTestApplication) Targets() *profiletarget.Service {
	return application.targets
}

func (application *doctorTestApplication) Switching() *switching.Service {
	return application.switching
}
func (application *doctorTestApplication) Codex() *codex.Service { return application.codex }

func openHealthyStore(ctx context.Context, configDir string, readOnly bool) (*store.Store, error) {
	runtimeService, err := profilesruntime.NewService(configDir)
	if err != nil {
		return nil, err
	}
	return runtimeService.StoreFactory().OpenHealthy(ctx, readOnly)
}

func resolveRuntime(configDir string) (string, profilesruntime.Paths, error) {
	return profilesruntime.ResolveConfig(configDir)
}

func openWritableAppTestStore(t *testing.T, ctx context.Context, databasePath string) *store.Store {
	t.Helper()
	db, err := store.Open(ctx, databasePath, false)
	if err != nil {
		t.Fatalf("open writable store: %v", err)
	}
	return db
}

func openAppTestStore(t *testing.T, ctx context.Context, databasePath string) *store.Store {
	t.Helper()
	db, err := store.Open(ctx, databasePath, true)
	if err != nil {
		t.Fatalf("open read-only store: %v", err)
	}
	return db
}

func createGenericProviderAndProfile(t *testing.T, ctx context.Context, configDir string, enabled bool) {
	t.Helper()
	application := newDoctorTestApplication(t, configDir, "")
	if _, err := application.Providers().Create(ctx, provider.CreateRequest{
		ID: "provider-a", Name: "Provider A", AdapterID: "generic", Enabled: &enabled,
	}); err != nil {
		t.Fatalf("create Provider: %v", err)
	}
	if _, err := application.Profiles().Create(ctx, profile.CreateRequest{ID: "profile-a", Name: "Profile A"}); err != nil {
		t.Fatalf("create Profile: %v", err)
	}
}

func createProfileTargetForRecovery(t *testing.T, ctx context.Context, configDir, profileID, targetID, path, content string) {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"content": content})
	if err != nil {
		t.Fatalf("encode target content: %v", err)
	}
	if _, err := newDoctorTestApplication(t, configDir, "").Targets().Create(ctx, profiletarget.CreateProfileTargetRequest{
		ProfileID: profileID, ProviderID: "provider-a", TargetID: targetID,
		Path: path, Format: profiletarget.FormatText, Strategy: profiletarget.StrategyReplaceFile, ValueJSON: string(raw),
	}); err != nil {
		t.Fatalf("create recovery target %s: %v", targetID, err)
	}
}

func setupCodexSwitchProfiles(t *testing.T, independentConfig bool) (context.Context, string, string) {
	t.Helper()
	ctx := context.Background()
	configDir := t.TempDir()
	codexDir := t.TempDir()
	application := newDoctorTestApplication(t, configDir, codexDir)
	if _, err := application.Runtime().Init(ctx); err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}
	writeCodexProfileFixture(t, codexDir, "model = \"first\"\n", `{"tokens":{"account_id":"first","access_token":"first-token"}}`)
	if _, err := application.Codex().CreateProfile(ctx, codex.CreateCodexProfileRequest{ProfileID: "first"}); err != nil {
		t.Fatalf("create first Codex Profile: %v", err)
	}
	writeCodexProfileFixture(t, codexDir, "model = \"second\"\n", `{"tokens":{"account_id":"second","access_token":"second-token"}}`)
	request := codex.CreateCodexProfileRequest{ProfileID: "second"}
	if independentConfig {
		request.NewConfigSetID = "second-config"
	}
	if _, err := application.Codex().CreateProfile(ctx, request); err != nil {
		t.Fatalf("create second Codex Profile: %v", err)
	}
	return ctx, configDir, codexDir
}

func writeCodexProfileFixture(t *testing.T, codexDir, config, auth string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(codexDir, codexconfig.ConfigFileName), []byte(config), 0o600); err != nil {
		t.Fatalf("write Codex config fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, codexconfig.AuthFileName), []byte(auth), 0o600); err != nil {
		t.Fatalf("write Codex auth fixture: %v", err)
	}
}

func singleOperationIDByTypeStatus(t *testing.T, databasePath, operationType, status string) string {
	t.Helper()
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("open operation database: %v", err)
	}
	defer db.Close()
	rows, err := db.Query(`
		SELECT id FROM operations
		WHERE operation_type = ? AND status = ?
		ORDER BY created_at_unix_ms ASC, id ASC
	`, operationType, status)
	if err != nil {
		t.Fatalf("query operations: %v", err)
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan operation ID: %v", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read operation rows: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("expected one %s %s operation, got %v", operationType, status, ids)
	}
	return ids[0]
}

func assertAppErrorCode(t *testing.T, err error, code apperror.Code) {
	t.Helper()
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != code {
		t.Fatalf("error = %v, want code %q", err, code)
	}
}
