package claudecode

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"github.com/strahe/profiledeck/internal/agent"
	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/bootstrap"
	claudeadapter "github.com/strahe/profiledeck/internal/claudecode/adapter"
	claudekeychain "github.com/strahe/profiledeck/internal/claudecode/keychain"
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

const (
	planActionCreate      = "create"
	planActionUpdate      = "update"
	planActionNoop        = "noop"
	planActionUnsupported = "unsupported"

	planReasonTargetMissing       = "target_missing"
	planReasonTargetModeDifferent = "target_mode_different"
	planReasonTargetIsSymlink     = "target_is_symlink"
)

type claudeCodeTestEnvironment struct {
	runtime    *profilesruntime.Service
	claudeCode *Service
	providers  *provider.Service
	profiles   *profile.Service
	targets    *profiletarget.Service
	switching  *switching.Service
	doctor     *doctor.Service
}

func newClaudeCodeTestEnvironment(t *testing.T, configDir string, customBackends ...switchtarget.Backend) *claudeCodeTestEnvironment {
	t.Helper()
	runtimeService, err := profilesruntime.NewService(configDir)
	if err != nil {
		t.Fatalf("expected runtime service, got %v", err)
	}
	agentRegistry := agent.BuiltinRegistry()
	agentService := agent.NewService(agentRegistry, runtimeService.StoreFactory(), agent.AccessUnrestricted)
	backends := map[string]switchtarget.Backend{switchtarget.BackendFile: switchtarget.FileBackend{}}
	for _, backend := range customBackends {
		backends[backend.ID()] = backend
	}
	backendList := make([]switchtarget.Backend, 0, len(backends))
	for _, id := range []string{switchtarget.BackendFile, switchtarget.BackendClaudeCodeKeychain} {
		if backend, ok := backends[id]; ok {
			backendList = append(backendList, backend)
		}
	}
	targetRegistry := switchtarget.MustRegistry(backendList...)
	adapterRegistry := switchplan.MustRegistry(switchplan.GenericAdapter{}, claudeadapter.Adapter{})
	switchingService := switching.NewService(
		runtimeService.Paths(), runtimeService.StoreFactory(), agentService,
		switching.NewDependencies(targetRegistry, adapterRegistry),
	)
	claudeCodeService := NewService(
		runtimeService, runtimeService.StoreFactory(), switchingService, agentService, targetRegistry,
	)
	doctorService := doctor.NewService(
		runtimeService,
		agentService,
		[]doctor.ProviderCheck{{AgentID: agent.ClaudeCode, Check: claudeCodeService.HealthCheck}},
		func(ctx context.Context, db *store.Store, paths profilesruntime.Paths, operation store.Operation) (string, string, string) {
			inspection := switchingService.InspectRecoveryFromOperation(ctx, db, paths, operation)
			return inspection.Status, inspection.Action, inspection.Reason
		},
		[]doctor.SensitivePathCheck{{Kind: doctor.SensitivePathClaudeCodeCredential, List: claudeCodeService.SensitivePaths}},
	)
	return &claudeCodeTestEnvironment{
		runtime: runtimeService, claudeCode: claudeCodeService,
		providers: provider.NewService(runtimeService.StoreFactory(), switchingService, agentService, agentRegistry),
		profiles:  profile.NewService(runtimeService.StoreFactory(), switchingService, profile.DeleteRegistry{}),
		targets: profiletarget.NewService(
			runtimeService.StoreFactory(), switchingService, agentService, agentRegistry, claudeCodeService.ReservedPaths,
		),
		switching: switchingService, doctor: doctorService,
	}
}

func initClaudeCodeTestRuntime(ctx context.Context, configDir string) (profilesruntime.InitResult, error) {
	runtimeService, err := profilesruntime.NewService(configDir)
	if err != nil {
		return profilesruntime.InitResult{}, err
	}
	return bootstrap.NewService(runtimeService, nil, nil).Initialize(ctx)
}

func openHealthyStore(ctx context.Context, configDir string, readOnly bool) (*store.Store, error) {
	runtimeService, err := profilesruntime.NewService(configDir)
	if err != nil {
		return nil, err
	}
	return runtimeService.StoreFactory().OpenHealthy(ctx, readOnly)
}

func assertErrorCode(t *testing.T, err error, code apperror.Code) {
	t.Helper()
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != code {
		t.Fatalf("expected error code %q, got %v", code, err)
	}
}

func hasDoctorFinding(findings []doctor.Finding, id string) bool {
	for _, finding := range findings {
		if finding.ID == id {
			return true
		}
	}
	return false
}

func hasDoctorFindingForProfile(findings []doctor.Finding, id, profileID string) bool {
	for _, finding := range findings {
		if finding.ID == id && finding.Details["profile_id"] == profileID {
			return true
		}
	}
	return false
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("expected JSON encoding, got %v", err)
	}
	return raw
}

func singleOperationIDByTypeStatus(t *testing.T, databasePath, operationType, status string) string {
	t.Helper()
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("expected sqlite open, got %v", err)
	}
	defer db.Close()
	rows, err := db.Query(`
		SELECT id
		FROM operations
		WHERE operation_type = ? AND status = ?
		ORDER BY created_at_unix_ms ASC, id ASC
	`, operationType, status)
	if err != nil {
		t.Fatalf("expected operation query, got %v", err)
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("expected operation id, got %v", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("expected operation rows, got %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("expected one %s %s operation, got %d: %v", operationType, status, len(ids), ids)
	}
	return ids[0]
}

type fakeClaudeCodeKeychainDriver struct {
	references             []claudekeychain.Reference
	items                  map[string]claudekeychain.Item
	updates                []fakeClaudeCodeKeychainUpdate
	findErr                error
	readErr                error
	updateErr              error
	updated                bool
	postReadErr            error
	requireFindInteraction bool
	requireReadInteraction bool
	findAllowInteractions  []bool
	readAllowInteractions  []bool
}

type fakeClaudeCodeKeychainUpdate struct {
	reference []byte
	data      []byte
}

func (driver *fakeClaudeCodeKeychainDriver) Find(_, _ string, allowInteraction bool) ([]claudekeychain.Reference, error) {
	driver.findAllowInteractions = append(driver.findAllowInteractions, allowInteraction)
	if driver.requireFindInteraction && !allowInteraction {
		return nil, claudekeychain.ErrInteractionRequired
	}
	return driver.references, driver.findErr
}

func (driver *fakeClaudeCodeKeychainDriver) Read(reference []byte, allowInteraction bool) (claudekeychain.Item, error) {
	driver.readAllowInteractions = append(driver.readAllowInteractions, allowInteraction)
	if driver.requireReadInteraction && !allowInteraction {
		return claudekeychain.Item{}, claudekeychain.ErrInteractionRequired
	}
	if driver.updated && driver.postReadErr != nil {
		return claudekeychain.Item{}, driver.postReadErr
	}
	if driver.readErr != nil {
		return claudekeychain.Item{}, driver.readErr
	}
	item, ok := driver.items[string(reference)]
	if !ok {
		return claudekeychain.Item{}, claudekeychain.ErrNotFound
	}
	return item, nil
}

func (driver *fakeClaudeCodeKeychainDriver) Update(reference, data []byte) error {
	if driver.updateErr != nil {
		return driver.updateErr
	}
	driver.updates = append(driver.updates, fakeClaudeCodeKeychainUpdate{
		reference: append([]byte(nil), reference...), data: append([]byte(nil), data...),
	})
	item, ok := driver.items[string(reference)]
	if !ok {
		return claudekeychain.ErrNotFound
	}
	item.Data = append([]byte(nil), data...)
	driver.items[string(reference)] = item
	driver.updated = true
	return nil
}
