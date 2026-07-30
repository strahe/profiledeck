package adapter

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/strahe/profiledeck/internal/apperror"
	grokconfig "github.com/strahe/profiledeck/internal/grokbuild/config"
	switchtarget "github.com/strahe/profiledeck/internal/switching/target"
)

func TestResolveTargetSpecAcceptsOnlyManagedFileTargets(t *testing.T) {
	t.Parallel()
	for _, targetID := range []string{grokconfig.AuthTargetID, grokconfig.ConfigTargetID} {
		spec, err := (Adapter{}).ResolveTargetSpec(
			grokconfig.ProviderID,
			targetID,
			switchtarget.BackendFile,
			filepath.Join(t.TempDir(), targetID),
			targetID,
		)
		if err != nil {
			t.Fatalf("ResolveTargetSpec(%q): %v", targetID, err)
		}
		if spec.BackendID() != switchtarget.BackendFile {
			t.Fatalf("ResolveTargetSpec(%q) backend = %q", targetID, spec.BackendID())
		}
	}

	for _, testCase := range []struct {
		name      string
		targetID  string
		backendID string
	}{
		{name: "unknown target", targetID: "other", backendID: switchtarget.BackendFile},
		{name: "missing backend", targetID: grokconfig.AuthTargetID},
		{name: "non-file backend", targetID: grokconfig.AuthTargetID, backendID: "keyring"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := (Adapter{}).ResolveTargetSpec(
				grokconfig.ProviderID,
				testCase.targetID,
				testCase.backendID,
				filepath.Join(t.TempDir(), "target"),
				testCase.targetID,
			)
			var appErr *apperror.Error
			if !errors.As(err, &appErr) || appErr.Code != apperror.RecoveryUnsupported {
				t.Fatalf("ResolveTargetSpec error = %v", err)
			}
		})
	}
}
