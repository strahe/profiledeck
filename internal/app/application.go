// Package app is the production composition root and typed service facade.
package app

import (
	"context"
	"fmt"

	"github.com/strahe/profiledeck/internal/agent"
	"github.com/strahe/profiledeck/internal/antigravity"
	agyadapter "github.com/strahe/profiledeck/internal/antigravity/adapter"
	agyprofile "github.com/strahe/profiledeck/internal/antigravity/profile"
	"github.com/strahe/profiledeck/internal/appbackup"
	"github.com/strahe/profiledeck/internal/bootstrap"
	"github.com/strahe/profiledeck/internal/claudecode"
	claudeadapter "github.com/strahe/profiledeck/internal/claudecode/adapter"
	claudeprofile "github.com/strahe/profiledeck/internal/claudecode/profile"
	claudetarget "github.com/strahe/profiledeck/internal/claudecode/target"
	"github.com/strahe/profiledeck/internal/codex"
	codexadapter "github.com/strahe/profiledeck/internal/codex/adapter"
	codexprofile "github.com/strahe/profiledeck/internal/codex/profile"
	"github.com/strahe/profiledeck/internal/doctor"
	"github.com/strahe/profiledeck/internal/grokbuild"
	grokadapter "github.com/strahe/profiledeck/internal/grokbuild/adapter"
	grokconfig "github.com/strahe/profiledeck/internal/grokbuild/config"
	grokcoordination "github.com/strahe/profiledeck/internal/grokbuild/coordination"
	grokprofile "github.com/strahe/profiledeck/internal/grokbuild/profile"
	"github.com/strahe/profiledeck/internal/profile"
	"github.com/strahe/profiledeck/internal/profiletarget"
	"github.com/strahe/profiledeck/internal/provider"
	"github.com/strahe/profiledeck/internal/providercoord"
	"github.com/strahe/profiledeck/internal/recoverycleanup"
	runtimeservice "github.com/strahe/profiledeck/internal/runtime"
	"github.com/strahe/profiledeck/internal/settings"
	"github.com/strahe/profiledeck/internal/store"
	"github.com/strahe/profiledeck/internal/switching"
	switchplan "github.com/strahe/profiledeck/internal/switching/plan"
	switchtarget "github.com/strahe/profiledeck/internal/switching/target"
	"github.com/strahe/profiledeck/internal/usage"
)

type Config struct {
	ConfigDir   string
	CodexDir    string
	GrokHome    string
	AgentAccess agent.AccessMode
}

// Dependencies is an immutable set of registries used for explicit test or
// alternate-environment composition.
type Dependencies struct {
	agents     agent.Registry
	switching  switching.Dependencies
	configured bool
}

func NewDependencies(agents agent.Registry, switchingDependencies switching.Dependencies) Dependencies {
	return Dependencies{agents: agents, switching: switchingDependencies, configured: true}
}

type Application struct {
	runtime     *runtimeservice.Service
	dataLease   *runtimeservice.DataLease
	backups     *appbackup.Service
	bootstrap   *bootstrap.Service
	agents      *agent.Service
	providers   *provider.Service
	profiles    *profile.Service
	targets     *profiletarget.Service
	switching   *switching.Service
	doctor      *doctor.Service
	usage       *usage.Service
	settings    *settings.Service
	codex       *codex.Service
	grokBuild   *grokbuild.Service
	antigravity *antigravity.Service
	claudeCode  *claudecode.Service
}

func New(config Config) (*Application, error) {
	home, err := grokconfig.ResolveHome(config.GrokHome)
	if err != nil {
		return nil, err
	}
	config.GrokHome = home.Dir
	return NewWithDependencies(config, defaultDependencies(home))
}

func NewWithDependencies(config Config, dependencies Dependencies) (*Application, error) {
	if !dependencies.configured {
		return nil, fmt.Errorf("application dependencies are required")
	}
	home, err := grokconfig.ResolveHome(config.GrokHome)
	if err != nil {
		return nil, err
	}
	// Freeze service path resolution even if the process environment or working
	// directory later changes. Production dependencies use this same Home.
	config.GrokHome = home.Dir
	accessMode := config.AgentAccess
	if accessMode == "" {
		accessMode = agent.AccessUnrestricted
	}
	if accessMode != agent.AccessUnrestricted && accessMode != agent.AccessDesktopPreferences {
		return nil, fmt.Errorf("unsupported Agent access mode %q", accessMode)
	}
	usageRegistry, err := usage.NewRegistry(
		usage.NewCodexIntegration(config.CodexDir),
		usage.NewGrokBuildIntegration(config.GrokHome),
	)
	if err != nil {
		return nil, err
	}
	for _, manifest := range dependencies.agents.Manifests() {
		for _, providerID := range manifest.ProviderIDs {
			if _, ok := dependencies.switching.Adapters.ManagedAdapterID(providerID); !ok {
				return nil, fmt.Errorf("agent %q Provider %q has no managed plan adapter", manifest.ID, providerID)
			}
		}
	}
	for _, providerID := range dependencies.switching.Adapters.ManagedProviderIDs() {
		if _, ok := dependencies.agents.AgentForProvider(providerID); !ok {
			return nil, fmt.Errorf("managed plan adapter Provider %q has no owning Agent", providerID)
		}
	}
	for _, providerID := range usageRegistry.ProviderIDs() {
		if _, ok := dependencies.agents.AgentForProvider(providerID); !ok {
			return nil, fmt.Errorf("usage Provider %q has no owning Agent", providerID)
		}
	}

	runtimeService, err := runtimeservice.NewService(config.ConfigDir)
	if err != nil {
		return nil, err
	}
	dataLease, err := runtimeservice.AcquireDataLease(
		runtimeService.Paths().DataLock,
		runtimeService.StoreFactory().AccessGate(),
	)
	if err != nil {
		return nil, err
	}
	runtimeService.AttachDataLease(dataLease)
	stores := runtimeService.StoreFactory()
	cleanupService := recoverycleanup.NewService(runtimeService.Paths())
	runtimeService.AttachRecoveryCleanup(cleanupService)
	agentService := agent.NewService(dependencies.agents, stores, accessMode)
	switchingService := switching.NewService(
		runtimeService.Paths(), stores, agentService, dependencies.switching, cleanupService,
	)

	codexService := codex.NewService(runtimeService, switchingService, switchingService, agentService, config.CodexDir)
	grokBuildService := grokbuild.NewService(runtimeService, switchingService, switchingService, agentService, config.GrokHome)
	antigravityService := antigravity.NewService(
		runtimeService, stores, switchingService, switchingService, agentService, dependencies.switching.Targets,
	)
	claudeCodeService := claudecode.NewService(
		runtimeService, stores, switchingService, agentService, dependencies.switching.Targets,
	)
	profileTargetService := profiletarget.NewService(
		stores, switchingService, agentService, dependencies.agents,
		codexService.ReservedPaths, grokBuildService.ReservedPaths, claudeCodeService.ReservedPaths,
	)
	doctorService := doctor.NewService(
		runtimeService,
		agentService,
		[]doctor.ProviderCheck{
			{AgentID: agent.Codex, Check: codexService.HealthCheck},
			{AgentID: agent.GrokBuild, Check: grokBuildService.HealthCheck},
			{AgentID: agent.Antigravity, Check: antigravityService.HealthCheck},
			{AgentID: agent.ClaudeCode, Check: claudeCodeService.HealthCheck},
		},
		func(ctx context.Context, db *store.Store, paths runtimeservice.Paths, operation store.Operation) (string, string, string) {
			inspection := switchingService.InspectRecoveryFromOperation(ctx, db, paths, operation)
			return inspection.Status, inspection.Action, inspection.Reason
		},
		[]doctor.SensitivePathCheck{
			{Kind: doctor.SensitivePathCodexAuth, List: codexService.SensitivePaths},
			{Kind: doctor.SensitivePathGrokBuildAuth, List: grokBuildService.SensitivePaths},
			{Kind: doctor.SensitivePathClaudeCodeCredential, List: claudeCodeService.SensitivePaths},
		},
		doctor.RecoveryCleanupCoordinator{Cleanup: cleanupService, Locks: switchingService},
	)

	backupService := appbackup.NewService(
		runtimeService.Paths(),
		stores,
		dataLease,
		appbackup.RecoveryCleanupCoordinator{Cleanup: cleanupService, Locks: switchingService},
	)
	profileDeleteRegistry := profile.MustDeleteRegistry(
		codexprofile.DeleteParticipant{},
		grokprofile.DeleteParticipant{},
		agyprofile.DeleteParticipant{},
		claudeprofile.DeleteParticipant{},
	)
	return &Application{
		runtime: runtimeService, dataLease: dataLease, backups: backupService,
		bootstrap: bootstrap.NewService(
			runtimeService,
			backupService,
			dataLease,
			bootstrap.RecoveryCleanupCoordinator{Cleanup: cleanupService, Locks: switchingService},
		),
		agents:    agentService,
		providers: provider.NewService(stores, switchingService, dependencies.agents),
		profiles:  profile.NewService(stores, switchingService, profileDeleteRegistry),
		targets:   profileTargetService,
		switching: switchingService, doctor: doctorService,
		usage: usage.NewService(stores, usageRegistry), settings: settings.NewService(stores),
		codex: codexService, grokBuild: grokBuildService,
		antigravity: antigravityService, claudeCode: claudeCodeService,
	}, nil
}

func defaultDependencies(homes ...grokconfig.Home) Dependencies {
	home, _ := grokconfig.ResolveHome("")
	if len(homes) > 0 {
		home = homes[0]
	}
	targets := switchtarget.MustRegistry(
		switchtarget.FileBackend{},
		switchtarget.NewKeyringBackend(switchtarget.SystemKeyringClient{}),
		claudetarget.NewSystemBackend(),
	)
	adapters := switchplan.MustRegistry(
		switchplan.GenericAdapter{},
		codexadapter.Adapter{},
		grokadapter.Adapter{},
		agyadapter.Adapter{},
		claudeadapter.Adapter{},
	)
	coordinators := providercoord.MustRegistry(providercoord.Registration{
		ProviderID: grokconfig.ProviderID, Coordinator: grokcoordination.NewCoordinator(home),
	})
	return NewDependencies(
		agent.BuiltinRegistry(),
		switching.NewDependencies(targets, adapters, switching.WithProviderCoordinators(coordinators)),
	)
}

func (application *Application) Runtime() *runtimeservice.Service  { return application.runtime }
func (application *Application) Backups() *appbackup.Service       { return application.backups }
func (application *Application) Agents() *agent.Service            { return application.agents }
func (application *Application) Providers() *provider.Service      { return application.providers }
func (application *Application) Profiles() *profile.Service        { return application.profiles }
func (application *Application) Targets() *profiletarget.Service   { return application.targets }
func (application *Application) Switching() *switching.Service     { return application.switching }
func (application *Application) Doctor() *doctor.Service           { return application.doctor }
func (application *Application) Usage() *usage.Service             { return application.usage }
func (application *Application) Settings() *settings.Service       { return application.settings }
func (application *Application) Codex() *codex.Service             { return application.codex }
func (application *Application) GrokBuild() *grokbuild.Service     { return application.grokBuild }
func (application *Application) Antigravity() *antigravity.Service { return application.antigravity }
func (application *Application) ClaudeCode() *claudecode.Service   { return application.claudeCode }

func (application *Application) Initialize(ctx context.Context) (runtimeservice.InitResult, error) {
	return application.bootstrap.Initialize(ctx)
}

func (application *Application) Close() {
	if application == nil || application.dataLease == nil {
		return
	}
	application.dataLease.Close()
}
