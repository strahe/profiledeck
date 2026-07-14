package profile

import (
	"errors"
	"testing"

	"github.com/strahe/profiledeck/internal/apperror"
	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	codexpreset "github.com/strahe/profiledeck/internal/codex/preset"
	"github.com/strahe/profiledeck/internal/store"
)

func TestFullProfileTargetsRequiresTypedConfigAndCredentialBindings(t *testing.T) {
	configValue, err := codexpreset.ConfigSetBindingValueJSON("config-work")
	if err != nil {
		t.Fatal(err)
	}
	configMetadata, err := codexpreset.TargetMetadataJSON(codexconfig.TargetID, codexpreset.TargetModeConfigSet)
	if err != nil {
		t.Fatal(err)
	}
	authValue, err := codexpreset.CredentialBindingValueJSON("credential-work")
	if err != nil {
		t.Fatal(err)
	}
	authMetadata, err := codexpreset.TargetMetadataJSON(codexconfig.AuthTargetID, codexpreset.TargetModeCredential)
	if err != nil {
		t.Fatal(err)
	}
	targets := []store.ProfileTarget{
		{ProfileID: "work", ProviderID: codexconfig.ProviderID, TargetID: codexconfig.TargetID, ValueJSON: configValue, MetadataJSON: configMetadata},
		{ProfileID: "work", ProviderID: codexconfig.ProviderID, TargetID: codexconfig.AuthTargetID, ValueJSON: authValue, MetadataJSON: authMetadata},
	}

	configTarget, authTarget, err := FullProfileTargets("work", targets)
	if err != nil {
		t.Fatal(err)
	}
	if configTarget.TargetID != codexconfig.TargetID || authTarget.TargetID != codexconfig.AuthTargetID {
		t.Fatalf("unexpected full profile targets: %#v %#v", configTarget, authTarget)
	}

	_, _, err = FullProfileTargets("work", targets[:1])
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != apperror.CodexInvalid {
		t.Fatalf("expected invalid full-profile error, got %v", err)
	}
}
