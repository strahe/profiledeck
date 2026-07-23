package usage

import (
	"context"
	"errors"
	"testing"

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/bootstrap"
	profilesruntime "github.com/strahe/profiledeck/internal/runtime"
	"github.com/strahe/profiledeck/internal/store"
)

type registryTestIntegration struct {
	provider string
	sources  []string
	called   bool
	syncErr  error
}

func (integration *registryTestIntegration) ProviderID() string {
	return integration.provider
}

func (integration *registryTestIntegration) SourceIDs() []string {
	return append([]string(nil), integration.sources...)
}

func (integration *registryTestIntegration) Sync(context.Context, store.Factory, SyncProvisionMode) (UsageSyncResult, error) {
	integration.called = true
	if integration.syncErr != nil {
		return UsageSyncResult{}, integration.syncErr
	}
	return UsageSyncResult{ProviderID: integration.provider, Source: integration.sources[0]}, nil
}

func (*registryTestIntegration) PricingInfo() UsagePricingInfo {
	return UsagePricingInfo{Basis: "test"}
}

func TestUsageRegistryRejectsInvalidAndDuplicateOwnership(t *testing.T) {
	tests := []struct {
		name         string
		integrations []Integration
	}{
		{name: "nil integration", integrations: []Integration{nil}},
		{name: "empty provider", integrations: []Integration{&registryTestIntegration{sources: []string{"source"}}}},
		{name: "empty sources", integrations: []Integration{&registryTestIntegration{provider: "provider"}}},
		{name: "duplicate provider", integrations: []Integration{
			&registryTestIntegration{provider: "provider", sources: []string{"source-a"}},
			&registryTestIntegration{provider: "provider", sources: []string{"source-b"}},
		}},
		{name: "duplicate source in integration", integrations: []Integration{
			&registryTestIntegration{provider: "provider", sources: []string{"source", "source"}},
		}},
		{name: "duplicate source across integrations", integrations: []Integration{
			&registryTestIntegration{provider: "provider-a", sources: []string{"source"}},
			&registryTestIntegration{provider: "provider-b", sources: []string{"source"}},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewRegistry(test.integrations...); err == nil {
				t.Fatal("invalid Usage Integration registry was accepted")
			}
		})
	}
}

func TestUsageServiceDispatchesRegisteredIntegrationAndRejectsUnsupportedProvider(t *testing.T) {
	ctx := context.Background()
	runtimeService, err := profilesruntime.NewService(t.TempDir())
	if err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	if _, err := bootstrap.NewService(runtimeService, nil, nil).Initialize(ctx); err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}
	integration := &registryTestIntegration{provider: "test-provider", sources: []string{"test-source"}}
	registry := MustRegistry(integration)
	service := NewService(runtimeService.StoreFactory(), registry)

	result, err := service.Sync(ctx, UsageSyncRequest{ProviderID: "test-provider"})
	if err != nil || !integration.called || result.ProviderID != "test-provider" {
		t.Fatalf("registered integration was not dispatched: result=%#v called=%v err=%v", result, integration.called, err)
	}
	_, err = service.Sync(ctx, UsageSyncRequest{ProviderID: "missing"})
	assertAppErrorCode(t, err, apperror.UsageInvalid)
}

func TestUsageServiceMapsIntegrationErrors(t *testing.T) {
	ctx := context.Background()
	runtimeService, err := profilesruntime.NewService(t.TempDir())
	if err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	if _, err := bootstrap.NewService(runtimeService, nil, nil).Initialize(ctx); err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}
	preserved := apperror.New(apperror.UsageInvalid, "Usage request is invalid")
	tests := []struct {
		name string
		err  error
		code apperror.Code
	}{
		{name: "missing Provider", err: store.ErrUsageProviderMissing, code: apperror.ProviderNotFound},
		{name: "identity revision", err: store.ErrUsageIdentityRevision, code: apperror.UsageMigrationRequired},
		{name: "cursor conflict", err: store.ErrUsageCursorConflict, code: apperror.UsageSyncConflict},
		{name: "superseded sync", err: store.ErrUsageSyncSuperseded, code: apperror.UsageSyncConflict},
		{name: "generic Integration error", err: errors.New("raw usage failure"), code: apperror.UsageImportFailed},
		{name: "existing application error", err: preserved, code: apperror.UsageInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			integration := &registryTestIntegration{
				provider: "test-provider",
				sources:  []string{"test-source"},
				syncErr:  test.err,
			}
			service := NewService(runtimeService.StoreFactory(), MustRegistry(integration))
			_, err := service.Sync(ctx, UsageSyncRequest{ProviderID: integration.provider})
			assertAppErrorCode(t, err, test.code)
			if !errors.Is(err, test.err) {
				t.Fatalf("mapped error does not preserve cause %v: %v", test.err, err)
			}
		})
	}
}
