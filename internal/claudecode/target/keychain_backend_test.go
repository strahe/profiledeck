package target_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/strahe/profiledeck/internal/apperror"
	claudekeychain "github.com/strahe/profiledeck/internal/claudecode/keychain"
	claudetarget "github.com/strahe/profiledeck/internal/claudecode/target"
	"github.com/strahe/profiledeck/internal/switching"
	switchtarget "github.com/strahe/profiledeck/internal/switching/target"
)

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

func TestClaudeCodeKeychainBackendPassiveReadDoesNotRequestAuthenticationUI(t *testing.T) {
	reference := []byte("authorization-required")
	driver := &fakeClaudeCodeKeychainDriver{
		references: []claudekeychain.Reference{{Persistent: reference, Service: "Claude Code-credentials", Account: "tester"}},
		items: map[string]claudekeychain.Item{
			string(reference): {Service: "Claude Code-credentials", Account: "tester", Data: []byte("secret")},
		},
		requireFindInteraction: true,
	}
	backend := claudetarget.NewBackend(driver)
	spec := claudetarget.KeychainSpec{ID: "auth", Service: "Claude Code-credentials", Account: "tester"}
	if _, err := backend.InspectWithInteraction(context.Background(), spec, false); !claudetarget.IsAuthorizationRequired(err) {
		t.Fatalf("passive InspectWithInteraction() error = %v", err)
	}
	if len(driver.findAllowInteractions) != 1 || driver.findAllowInteractions[0] || len(driver.readAllowInteractions) != 0 {
		t.Fatalf("passive interaction flags: find=%#v read=%#v", driver.findAllowInteractions, driver.readAllowInteractions)
	}
	snapshot, err := backend.InspectWithInteraction(context.Background(), spec, true)
	if err != nil || !snapshot.Exists {
		t.Fatalf("authorized InspectWithInteraction() = %#v, error = %v", snapshot, err)
	}
	if len(driver.findAllowInteractions) != 2 || !driver.findAllowInteractions[1] || len(driver.readAllowInteractions) != 1 || !driver.readAllowInteractions[0] {
		t.Fatalf("authorized interaction flags: find=%#v read=%#v", driver.findAllowInteractions, driver.readAllowInteractions)
	}
}

func (driver *fakeClaudeCodeKeychainDriver) Update(reference, data []byte) error {
	if driver.updateErr != nil {
		return driver.updateErr
	}
	driver.updates = append(driver.updates, fakeClaudeCodeKeychainUpdate{reference: append([]byte{}, reference...), data: append([]byte{}, data...)})
	item, ok := driver.items[string(reference)]
	if !ok {
		return claudekeychain.ErrNotFound
	}
	item.Data = append([]byte{}, data...)
	driver.items[string(reference)] = item
	driver.updated = true
	return nil
}

func TestClaudeCodeKeychainBackendRequiresOneMatchingReference(t *testing.T) {
	spec := claudetarget.KeychainSpec{ID: "auth", Service: "Claude Code-credentials", Account: "tester"}
	driver := &fakeClaudeCodeKeychainDriver{items: map[string]claudekeychain.Item{}}
	backend := claudetarget.NewBackend(driver)
	snapshot, err := backend.Inspect(context.Background(), spec)
	if err != nil || snapshot.Exists {
		t.Fatalf("zero-reference Inspect() = %#v, error = %v", snapshot, err)
	}
	driver = &fakeClaudeCodeKeychainDriver{
		references: []claudekeychain.Reference{
			{Persistent: []byte("one"), Service: spec.Service, Account: spec.Account},
			{Persistent: []byte("two"), Service: spec.Service, Account: spec.Account},
		},
		items: map[string]claudekeychain.Item{},
	}
	backend = claudetarget.NewBackend(driver)
	if _, err := backend.Inspect(context.Background(), spec); !isErrorCode(err, apperror.ClaudeCodeInvalid) {
		t.Fatalf("multiple-reference Inspect() error = %v", err)
	}
	driver = &fakeClaudeCodeKeychainDriver{
		references: []claudekeychain.Reference{{Persistent: []byte("one"), Service: "other", Account: spec.Account}},
		items:      map[string]claudekeychain.Item{},
	}
	backend = claudetarget.NewBackend(driver)
	if _, err := backend.Inspect(context.Background(), spec); !isErrorCode(err, apperror.TargetChanged) {
		t.Fatalf("attribute-mismatch Inspect() error = %v", err)
	}
}

func TestClaudeCodeKeychainBackendReadsRawDataAndUpdatesExactReference(t *testing.T) {
	reference := []byte{0, 1, 2, 255}
	driver := &fakeClaudeCodeKeychainDriver{
		references: []claudekeychain.Reference{{Persistent: reference, Service: "Claude Code-credentials", Account: "tester"}},
		items:      map[string]claudekeychain.Item{string(reference): {Service: "Claude Code-credentials", Account: "tester", Data: []byte(`{"raw":true}`)}},
	}
	backend := claudetarget.NewBackend(driver)
	spec := claudetarget.KeychainSpec{ID: "auth", Service: "Claude Code-credentials", Account: "tester", Label: "Claude Code login"}
	snapshot, err := backend.Inspect(context.Background(), spec)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if snapshot.Content != `{"raw":true}` {
		t.Fatalf("Inspect() content = %q; raw Keychain data must not be Base64 wrapped", snapshot.Content)
	}
	if err := backend.Apply(context.Background(), spec, snapshot, `{"raw":false}`, 0, false); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if len(driver.updates) != 1 || !bytes.Equal(driver.updates[0].reference, reference) || string(driver.updates[0].data) != `{"raw":false}` {
		t.Fatalf("unexpected exact-reference update: %#v", driver.updates)
	}
}

func TestClaudeCodeKeychainBackendRejectsAmbiguousReplacementAndChangedContent(t *testing.T) {
	reference := []byte("first")
	driver := &fakeClaudeCodeKeychainDriver{
		references: []claudekeychain.Reference{{Persistent: reference, Service: "Claude Code-credentials", Account: "tester"}},
		items:      map[string]claudekeychain.Item{string(reference): {Service: "Claude Code-credentials", Account: "tester", Data: []byte("before")}},
	}
	backend := claudetarget.NewBackend(driver)
	spec := claudetarget.KeychainSpec{ID: "auth", Service: "Claude Code-credentials", Account: "tester"}
	snapshot, err := backend.Inspect(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	driver.references = append(driver.references, claudekeychain.Reference{Persistent: []byte("second"), Service: spec.Service, Account: spec.Account})
	if err := backend.Verify(context.Background(), spec, snapshot); !isErrorCode(err, apperror.TargetChanged) {
		t.Fatalf("ambiguous replacement Verify() error = %v", err)
	}
	driver.references = []claudekeychain.Reference{{Persistent: []byte("replacement"), Service: spec.Service, Account: spec.Account}}
	driver.items["replacement"] = claudekeychain.Item{Service: spec.Service, Account: spec.Account, Data: []byte("before")}
	if err := backend.Verify(context.Background(), spec, snapshot); !isErrorCode(err, apperror.TargetChanged) {
		t.Fatalf("recreated item Verify() error = %v", err)
	}
	driver.references = driver.references[:0]
	driver.references = append(driver.references, claudekeychain.Reference{Persistent: reference, Service: spec.Service, Account: spec.Account})
	item := driver.items[string(reference)]
	item.Data = []byte("changed")
	driver.items[string(reference)] = item
	if err := backend.Apply(context.Background(), spec, snapshot, "desired", 0, false); !isErrorCode(err, apperror.TargetChanged) {
		t.Fatalf("changed content Apply() error = %v", err)
	}
}

func TestClaudeCodeKeychainBackendRejectsDeletedReferenceAndPostVerifyFailure(t *testing.T) {
	reference := []byte("original")
	driver := &fakeClaudeCodeKeychainDriver{
		references: []claudekeychain.Reference{{Persistent: reference, Service: "Claude Code-credentials", Account: "tester"}},
		items:      map[string]claudekeychain.Item{string(reference): {Service: "Claude Code-credentials", Account: "tester", Data: []byte("before")}},
	}
	backend := claudetarget.NewBackend(driver)
	spec := claudetarget.KeychainSpec{ID: "auth", Service: "Claude Code-credentials", Account: "tester"}
	snapshot, _ := backend.Inspect(context.Background(), spec)
	delete(driver.items, string(reference))
	if err := backend.Apply(context.Background(), spec, snapshot, "desired", 0, false); !isErrorCode(err, apperror.TargetChanged) {
		t.Fatalf("deleted reference Apply() error = %v", err)
	}
	driver.items[string(reference)] = claudekeychain.Item{Service: spec.Service, Account: spec.Account, Data: []byte("before")}
	driver.updateErr = errors.New("write failed with raw-access-secret")
	if err := backend.Apply(context.Background(), spec, snapshot, "desired", 0, false); !isErrorCode(err, apperror.TargetWriteFailed) {
		t.Fatalf("update failure Apply() error = %v", err)
	} else if strings.Contains(err.Error(), "raw-access-secret") {
		t.Fatalf("Keychain driver error leaked through app boundary: %v", err)
	}
	driver.updateErr = nil
	driver.postReadErr = errors.New("post-read failed")
	if err := backend.Apply(context.Background(), spec, snapshot, "desired", 0, false); !isErrorCode(err, apperror.TargetWriteFailed) {
		t.Fatalf("post-update read failure Apply() error = %v", err)
	}
}

func TestClaudeCodeKeychainBackupKeepsReferenceOutOfPublicSummary(t *testing.T) {
	reference := []byte("private-reference")
	driver := &fakeClaudeCodeKeychainDriver{
		references: []claudekeychain.Reference{{Persistent: reference, Service: "Claude Code-credentials", Account: "tester"}},
		items:      map[string]claudekeychain.Item{string(reference): {Service: "Claude Code-credentials", Account: "tester", Data: []byte("secret")}},
	}
	backend := claudetarget.NewBackend(driver)
	spec := claudetarget.KeychainSpec{ID: "auth", Service: "Claude Code-credentials", Account: "tester"}
	snapshot, _ := backend.Inspect(context.Background(), spec)
	destination := filepath.Join(t.TempDir(), "credential.bak")
	hash, err := backend.Backup(context.Background(), spec, snapshot, destination)
	if err != nil || hash != switchtarget.SHA256String("secret") {
		t.Fatalf("Backup() hash = %q, error = %v", hash, err)
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatalf("backup stat error = %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("backup mode = %v", info.Mode().Perm())
	}
	if _, err := backend.Backup(context.Background(), spec, snapshot, destination); !isErrorCode(err, apperror.BackupFailed) {
		t.Fatalf("Backup() overwrote an existing destination: %v", err)
	}
	public := switching.BackupEntrySummary{TargetID: "auth", BackendID: switchtarget.BackendClaudeCodeKeychain}
	if encoded := mustJSON(t, public); stringContains(encoded, "private-reference") || stringContains(encoded, "private_locator") {
		t.Fatalf("public backup summary leaked private reference: %s", encoded)
	}
}

func TestClaudeCodeKeychainDriverHasNoCreateOrDeleteAPI(t *testing.T) {
	typeOfDriver := reflect.TypeOf((*claudekeychain.Driver)(nil)).Elem()
	methods := map[string]bool{}
	for index := 0; index < typeOfDriver.NumMethod(); index++ {
		methods[typeOfDriver.Method(index).Name] = true
	}
	if len(methods) != 3 || !methods["Find"] || !methods["Read"] || !methods["Update"] || methods["Add"] || methods["Delete"] {
		t.Fatalf("unexpected Keychain driver API: %#v", methods)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func stringContains(value []byte, text string) bool { return bytes.Contains(value, []byte(text)) }

func isErrorCode(err error, code apperror.Code) bool {
	var appErr *apperror.Error
	return errors.As(err, &appErr) && appErr.Code == code
}
