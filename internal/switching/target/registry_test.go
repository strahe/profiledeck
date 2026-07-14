package target

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/strahe/profiledeck/internal/apperror"
)

func TestRegistryInspectAllRejectsInvalidAndDuplicateTargetIDs(t *testing.T) {
	registry := MustRegistry(FileBackend{})

	_, err := registry.InspectAll(context.Background(), []Spec{
		FileSpec{ID: "duplicate", Path: filepath.Join(t.TempDir(), "first")},
		FileSpec{ID: "duplicate", Path: filepath.Join(t.TempDir(), "second")},
	})
	assertTargetErrorCode(t, err, apperror.PlanBuildFailed)

	_, err = registry.InspectAll(context.Background(), []Spec{nil})
	assertTargetErrorCode(t, err, apperror.PlanBuildFailed)

	_, err = registry.InspectAll(context.Background(), []Spec{
		FileSpec{ID: " ", Path: filepath.Join(t.TempDir(), "invalid")},
	})
	assertTargetErrorCode(t, err, apperror.PlanBuildFailed)
}

func TestRegistryInspectAllRejectsDuplicateLocatorsWithoutLeakingFingerprint(t *testing.T) {
	registry := MustRegistry(FileBackend{})
	path := filepath.Join(t.TempDir(), "shared.json")

	_, err := registry.InspectAll(context.Background(), []Spec{
		FileSpec{ID: "first", Path: path, NeedsContent: true},
		FileSpec{ID: "second", Path: path, NeedsContent: true},
	})
	assertTargetErrorCode(t, err, apperror.PlanBuildFailed)
	if strings.Contains(err.Error(), SHA256String(BackendFile+"\x00"+path)) {
		t.Fatalf("duplicate locator error leaked locator fingerprint: %v", err)
	}
}

func assertTargetErrorCode(t *testing.T, err error, code apperror.Code) {
	t.Helper()
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != code {
		t.Fatalf("error = %v, want code %q", err, code)
	}
}
