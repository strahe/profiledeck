package backend

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/strahe/profiledeck/internal/agent"
	"github.com/strahe/profiledeck/internal/antigravity"
	agyconfig "github.com/strahe/profiledeck/internal/antigravity/config"
	"github.com/strahe/profiledeck/internal/app"
	"github.com/strahe/profiledeck/internal/appbackup"
	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/claudecode"
	claudecodeconfig "github.com/strahe/profiledeck/internal/claudecode/config"
	"github.com/strahe/profiledeck/internal/codex"
	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	"github.com/strahe/profiledeck/internal/doctor"
	"github.com/strahe/profiledeck/internal/profile"
	"github.com/strahe/profiledeck/internal/profiletarget"
	"github.com/strahe/profiledeck/internal/provider"
	"github.com/strahe/profiledeck/internal/recoverycleanup"
	profilesruntime "github.com/strahe/profiledeck/internal/runtime"
	"github.com/strahe/profiledeck/internal/settings"
	"github.com/strahe/profiledeck/internal/switching"
	"github.com/strahe/profiledeck/internal/usage"
)

type AppService struct {
	application *app.Application
	info        app.Info
	env         Environment
	mu          sync.RWMutex
	startupErr  error
	changes     *ChangeNotifier
}

type CodexService struct {
	application *app.Application
	changes     *ChangeNotifier
	autoSync    *usageAutoSyncRuntime
	quota       *codexQuotaRuntime
	settingsMu  sync.Mutex
}

type AntigravityService struct {
	application *app.Application
	changes     *ChangeNotifier
}

type ClaudeCodeService struct {
	application *app.Application
	changes     *ChangeNotifier
}

type ProfileService struct {
	application *app.Application
}

type SwitchService struct {
	application *app.Application
	changes     *ChangeNotifier
	quota       *codexQuotaRuntime
}

type DoctorService struct {
	application *app.Application
	changes     *ChangeNotifier
	quota       *codexQuotaRuntime
}

type BackupService struct {
	application *app.Application
	changes     *ChangeNotifier
	runtime     *applicationBackupRuntime
	restartMu   sync.RWMutex
	restart     func() error
}

type UsageService struct {
	application *app.Application
	autoSync    *usageAutoSyncRuntime
}

type SettingsService struct {
	application *app.Application
}

type AgentService struct {
	application *app.Application
	changes     *ChangeNotifier
}

type Services struct {
	App         *AppService
	Agent       *AgentService
	Antigravity *AntigravityService
	ClaudeCode  *ClaudeCodeService
	Codex       *CodexService
	Profile     *ProfileService
	Switch      *SwitchService
	Doctor      *DoctorService
	Backup      *BackupService
	Usage       *UsageService
	Settings    *SettingsService
	changes     *ChangeNotifier
	autoSync    *usageAutoSyncRuntime
	quota       *codexQuotaRuntime
	runtimes    *agentRuntimeManager
	backups     *applicationBackupRuntime
}

type DashboardResult struct {
	Info                app.Info                                  `json:"info"`
	Environment         Environment                               `json:"environment"`
	Status              profilesruntime.StatusResult              `json:"status"`
	Agents              []agent.State                             `json:"agents"`
	Doctor              *doctor.DoctorResult                      `json:"doctor,omitempty"`
	Providers           []provider.Provider                       `json:"providers"`
	ActiveStates        []provider.ActiveState                    `json:"active_states"`
	CodexProfiles       *codex.CodexProfileListResult             `json:"codex_profiles,omitempty"`
	CodexConfigSets     *codex.CodexConfigSetListResult           `json:"codex_config_sets,omitempty"`
	AntigravityProfiles *antigravity.AntigravityProfileListResult `json:"antigravity_profiles,omitempty"`
	ClaudeCodeProfiles  *claudecode.ClaudeCodeProfileListResult   `json:"claude_code_profiles,omitempty"`
	Usage               *usage.UsageSummaryResult                 `json:"usage,omitempty"`
	StartupError        *DesktopError                             `json:"startup_error,omitempty"`
	GeneratedAt         int64                                     `json:"generated_at_unix_ms"`
}

type DesktopError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

type SwitchApplyRequest struct {
	ProviderID              string `json:"provider_id"`
	ProfileID               string `json:"profile_id"`
	ExpectedPlanFingerprint string `json:"expected_plan_fingerprint"`
	Confirm                 bool   `json:"confirm"`
}

type CreateCodexProfileRequest struct {
	ProfileID               string  `json:"profile_id"`
	Name                    *string `json:"name,omitempty"`
	Description             *string `json:"description,omitempty"`
	NewConfigSetID          string  `json:"new_config_set_id,omitempty"`
	NewConfigSetName        *string `json:"new_config_set_name,omitempty"`
	NewConfigSetDescription *string `json:"new_config_set_description,omitempty"`
}

type ForkCodexProfileRequest struct {
	SourceProfileID         string  `json:"source_profile_id"`
	ProfileID               string  `json:"profile_id"`
	CredentialBinding       string  `json:"credential_binding"`
	ConfigBinding           string  `json:"config_binding"`
	NewConfigSetID          string  `json:"new_config_set_id,omitempty"`
	NewConfigSetName        *string `json:"new_config_set_name,omitempty"`
	NewConfigSetDescription *string `json:"new_config_set_description,omitempty"`
	Name                    *string `json:"name,omitempty"`
	Description             *string `json:"description,omitempty"`
}

type UpdateCodexProfileConfigSetRequest struct {
	ProfileID   string `json:"profile_id"`
	ConfigSetID string `json:"config_set_id"`
}

type CreateCodexConfigSetRequest struct {
	ConfigSetID string `json:"config_set_id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type CopyCodexConfigSetRequest struct {
	SourceConfigSetID string `json:"source_config_set_id"`
	ConfigSetID       string `json:"config_set_id"`
	Name              string `json:"name"`
	Description       string `json:"description,omitempty"`
}

type UpdateCodexConfigSetRequest struct {
	ConfigSetID string  `json:"config_set_id"`
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
}

type UpdateCodexProfileMetadataRequest struct {
	ProfileID   string  `json:"profile_id"`
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
}

type CreateAntigravityProfileRequest struct {
	ProfileID   string  `json:"profile_id"`
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
}

type UpdateAntigravityProfileRequest struct {
	ProfileID   string  `json:"profile_id"`
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
}

type CreateClaudeCodeProfileRequest struct {
	ProfileID   string  `json:"profile_id"`
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
}

type UpdateClaudeCodeProfileRequest struct {
	ProfileID   string  `json:"profile_id"`
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
}

type ExportCodexProfilesRequest struct {
	ProfileIDs []string `json:"profile_ids,omitempty"`
	OutputPath string   `json:"output_path"`
	Overwrite  bool     `json:"overwrite"`
}

type ApplyCodexProfileImportRequest struct {
	InputPath               string `json:"input_path"`
	ExpectedPlanFingerprint string `json:"expected_plan_fingerprint"`
	Confirm                 bool   `json:"confirm"`
}

func NewServices(application *app.Application, info app.Info, env Environment, startupErr error) Services {
	changes := NewChangeNotifier()
	autoSync := newUsageAutoSyncRuntime(application.Codex().GetSettings, application.Usage().SyncCodex)
	quota := newCodexQuotaRuntime(application.Codex().ListAutomationTargets, application.Codex().RunCredentialJob)
	backups := newApplicationBackupRuntime(
		application.Settings().Get,
		application.Backups().CreateAutomaticIfDue,
		func(_ *appbackup.BackupDetail, err error) {
			notifyMutationResult(changes, DesktopChangeApplicationBackupChanged, "backup.createAutomatic", "", "", "", err)
		},
	)
	runtimes := newAgentRuntimeManager(application.Agents())
	services := Services{
		App:         &AppService{application: application, info: info, env: env, startupErr: startupErr, changes: changes},
		Agent:       &AgentService{application: application, changes: changes},
		Antigravity: &AntigravityService{application: application, changes: changes},
		ClaudeCode:  &ClaudeCodeService{application: application, changes: changes},
		Codex:       &CodexService{application: application, changes: changes, autoSync: autoSync, quota: quota},
		Profile:     &ProfileService{application: application},
		Switch:      &SwitchService{application: application, changes: changes, quota: quota},
		Doctor:      &DoctorService{application: application, changes: changes, quota: quota},
		Backup:      &BackupService{application: application, changes: changes, runtime: backups},
		Usage:       &UsageService{application: application, autoSync: autoSync},
		Settings:    &SettingsService{application: application},
		changes:     changes,
		autoSync:    autoSync,
		quota:       quota,
		runtimes:    runtimes,
		backups:     backups,
	}
	runtimes.Register(agent.Codex, autoSync)
	runtimes.Register(agent.Codex, quota)
	return services
}

func (s Services) SubscribeChanges(listener func(DesktopChangeEvent)) func() {
	return s.changes.Subscribe(listener)
}

func (s Services) StartUsageAutoSync(ctx context.Context, emitter func(UsageAutoSyncStatus)) {
	s.autoSync.SetEmitter(emitter)
	s.runtimes.Activate(ctx, agent.Codex, s.autoSync)
}

func (s Services) StopUsageAutoSync() {
	s.runtimes.Deactivate(agent.Codex, s.autoSync)
}

func (s Services) StartCodexQuotaRuntime(ctx context.Context, emitter func(CodexQuotaRuntimeStatus)) {
	s.quota.SetEmitter(emitter)
	s.runtimes.Activate(ctx, agent.Codex, s.quota)
}

func (s Services) StopCodexQuotaRuntime() {
	s.runtimes.Deactivate(agent.Codex, s.quota)
}

func (s Services) StartApplicationBackups(ctx context.Context) {
	s.backups.Start(ctx)
}

func (s Services) StopApplicationBackups() {
	s.backups.Stop()
}

func Bootstrap(ctx context.Context, application *app.Application) error {
	// Desktop startup may create ProfileDeck runtime state, but it must not touch
	// Codex or any other target tool files; target writes stay in switch/recovery.
	_, err := application.Initialize(ctx)
	return err
}

func (s *AppService) Info(_ context.Context) app.Info {
	return s.info
}

func (s *AppService) Environment(_ context.Context) Environment {
	return s.env
}

func (s *AppService) Initialize(ctx context.Context) (profilesruntime.InitResult, error) {
	result, err := s.application.Initialize(ctx)
	if err == nil {
		s.mu.Lock()
		s.startupErr = nil
		s.mu.Unlock()
	}
	s.notifyMutationResult(DesktopChangeInitialized, "app.initialize", "", "", "", err)
	return result, err
}

func (s *AppService) Dashboard(ctx context.Context) (DashboardResult, error) {
	result := DashboardResult{
		Info:         s.info,
		Environment:  s.env,
		Agents:       []agent.State{},
		Providers:    []provider.Provider{},
		ActiveStates: []provider.ActiveState{},
		GeneratedAt:  time.Now().UnixMilli(),
	}
	s.mu.RLock()
	startupErr := s.startupErr
	s.mu.RUnlock()
	if startupErr != nil {
		result.StartupError = FormatDesktopErrorPtr(startupErr)
	}

	status, err := s.application.Runtime().Status(ctx)
	if err != nil {
		if startupErr != nil {
			// Startup recovery must remain reachable even when the database is too
			// damaged for the normal status and dashboard queries.
			return result, nil
		}
		return result, err
	}
	result.Status = status
	if !status.Initialized || !status.SchemaHealthy {
		return result, nil
	}

	doctorResult, err := s.application.Doctor().Run(ctx)
	if err != nil {
		return result, err
	}
	result.Doctor = &doctorResult

	agents, err := s.application.Agents().List(ctx)
	if err != nil {
		return result, err
	}
	result.Agents = agents

	providers, err := s.application.Providers().List(ctx, provider.ListRequest{IncludeDisabled: true})
	if err != nil {
		return result, err
	}
	result.Providers = providers

	activeStates, err := s.application.Providers().ListActiveStates(ctx)
	if err != nil {
		return result, err
	}
	result.ActiveStates = activeStates

	if agentEnabled(agents, agent.Codex) {
		if codexProfiles, listErr := s.application.Codex().ListProfiles(ctx); listErr == nil {
			result.CodexProfiles = &codexProfiles
		}
		if codexConfigSets, listErr := s.application.Codex().ListConfigSets(ctx); listErr == nil {
			result.CodexConfigSets = &codexConfigSets
		}
		if usageSummary, summaryErr := s.application.Usage().Summary(ctx, usage.UsageSummaryRequest{ProviderID: codexconfig.ProviderID}); summaryErr == nil {
			result.Usage = &usageSummary
		}
	}
	if agentEnabled(agents, agent.Antigravity) {
		if antigravityProfiles, listErr := s.application.Antigravity().ListProfiles(ctx); listErr == nil {
			result.AntigravityProfiles = &antigravityProfiles
		}
	}
	if agentEnabled(agents, agent.ClaudeCode) {
		if claudeCodeProfiles, listErr := s.application.ClaudeCode().ListProfiles(ctx); listErr == nil {
			result.ClaudeCodeProfiles = &claudeCodeProfiles
		}
	}
	return result, nil
}

func (s *AgentService) List(ctx context.Context) ([]agent.State, error) {
	return s.application.Agents().List(ctx)
}

func (s *AgentService) SetEnabled(ctx context.Context, id string, enabled bool) (agent.State, error) {
	state, err := s.application.Agents().SetEnabled(ctx, agent.ID(strings.TrimSpace(id)), enabled)
	if err == nil {
		s.changes.Notify(DesktopChangeEvent{
			Kind: DesktopChangeAgentStateChanged, Source: "agent.setEnabled", Status: DesktopChangeStatusSuccess,
			AgentID: state.Manifest.ID, AgentEnabled: &state.Enabled,
		})
	}
	return state, err
}

func agentEnabled(states []agent.State, id agent.ID) bool {
	for _, state := range states {
		if state.Manifest.ID == id {
			return state.Enabled
		}
	}
	return false
}

func (s *AntigravityService) Detect(ctx context.Context) (antigravity.AntigravityDetectResult, error) {
	return s.application.Antigravity().Detect(ctx)
}

func (s *AntigravityService) ListProfiles(ctx context.Context) (antigravity.AntigravityProfileListResult, error) {
	return s.application.Antigravity().ListProfiles(ctx)
}

func (s *AntigravityService) ShowProfile(ctx context.Context, profileID string) (antigravity.AntigravityProfileDetail, error) {
	return s.application.Antigravity().GetProfile(ctx, antigravity.GetAntigravityProfileRequest{ProfileID: profileID})
}

func (s *AntigravityService) ReadProfileQuota(ctx context.Context, profileID string) (antigravity.AntigravityProfileQuota, error) {
	return s.application.Antigravity().ReadProfileQuota(ctx, profileID)
}

func (s *AntigravityService) CreateProfile(ctx context.Context, req CreateAntigravityProfileRequest) (antigravity.AntigravityProfileSaveResult, error) {
	result, err := s.application.Antigravity().CreateProfile(ctx, antigravity.CreateAntigravityProfileRequest{
		ProfileID: req.ProfileID, Name: req.Name, Description: req.Description,
	})
	profileID := result.Summary.Profile.ID
	if profileID == "" {
		profileID = strings.TrimSpace(req.ProfileID)
	}
	s.notifyMutationResult(DesktopChangeAntigravityProfileChanged, "antigravity.createProfile", agyconfig.ProviderID, profileID, result.OperationID, err)
	return result, err
}

func (s *AntigravityService) UpdateProfile(ctx context.Context, req UpdateAntigravityProfileRequest) (antigravity.AntigravityProfileDetail, error) {
	result, err := s.application.Antigravity().UpdateProfile(ctx, antigravity.UpdateAntigravityProfileRequest{
		ProfileID: req.ProfileID, Name: req.Name, Description: req.Description,
	})
	profileID := result.Summary.Profile.ID
	if profileID == "" {
		profileID = strings.TrimSpace(req.ProfileID)
	}
	s.notifyMutationResult(DesktopChangeAntigravityProfileChanged, "antigravity.updateProfile", agyconfig.ProviderID, profileID, "", err)
	return result, err
}

func (s *AntigravityService) SaveCurrent(ctx context.Context) (antigravity.AntigravityProfileSaveResult, error) {
	result, err := s.application.Antigravity().SaveActiveProfile(ctx)
	s.notifyMutationResult(DesktopChangeAntigravityProfileChanged, "antigravity.saveCurrent", agyconfig.ProviderID, result.Summary.Profile.ID, result.OperationID, err)
	return result, err
}

func (s *ClaudeCodeService) Detect(ctx context.Context) (claudecode.ClaudeCodeDetectResult, error) {
	return s.application.ClaudeCode().Detect(ctx, claudecode.ClaudeCodeDetectRequest{})
}

func (s *ClaudeCodeService) AuthorizeKeychain(ctx context.Context) (claudecode.ClaudeCodeDetectResult, error) {
	return s.application.ClaudeCode().Detect(ctx, claudecode.ClaudeCodeDetectRequest{AllowKeychainInteraction: true})
}

func (s *ClaudeCodeService) ListProfiles(ctx context.Context) (claudecode.ClaudeCodeProfileListResult, error) {
	return s.application.ClaudeCode().ListProfiles(ctx)
}

func (s *ClaudeCodeService) ShowProfile(ctx context.Context, profileID string) (claudecode.ClaudeCodeProfileDetail, error) {
	return s.application.ClaudeCode().GetProfile(ctx, claudecode.GetClaudeCodeProfileRequest{ProfileID: profileID})
}

func (s *ClaudeCodeService) CreateProfile(ctx context.Context, req CreateClaudeCodeProfileRequest) (claudecode.ClaudeCodeProfileSaveResult, error) {
	result, err := s.application.ClaudeCode().CreateProfile(ctx, claudecode.CreateClaudeCodeProfileRequest{
		ProfileID: req.ProfileID, Name: req.Name, Description: req.Description,
	})
	profileID := result.Summary.Profile.ID
	if profileID == "" {
		profileID = strings.TrimSpace(req.ProfileID)
	}
	s.notifyMutationResult(DesktopChangeClaudeCodeProfileChanged, "claude-code.createProfile", claudecodeconfig.ProviderID, profileID, result.OperationID, err)
	return result, err
}

func (s *ClaudeCodeService) UpdateProfile(ctx context.Context, req UpdateClaudeCodeProfileRequest) (claudecode.ClaudeCodeProfileDetail, error) {
	result, err := s.application.ClaudeCode().UpdateProfile(ctx, claudecode.UpdateClaudeCodeProfileRequest{
		ProfileID: req.ProfileID, Name: req.Name, Description: req.Description,
	})
	profileID := result.Summary.Profile.ID
	if profileID == "" {
		profileID = strings.TrimSpace(req.ProfileID)
	}
	s.notifyMutationResult(DesktopChangeClaudeCodeProfileChanged, "claude-code.updateProfile", claudecodeconfig.ProviderID, profileID, "", err)
	return result, err
}

func (s *ClaudeCodeService) SaveCurrent(ctx context.Context, confirmShared bool) (claudecode.ClaudeCodeProfileSaveResult, error) {
	result, err := s.application.ClaudeCode().SaveActiveProfile(ctx, claudecode.SaveActiveClaudeCodeProfileRequest{ConfirmShared: confirmShared})
	s.notifyMutationResult(DesktopChangeClaudeCodeProfileChanged, "claude-code.saveCurrent", claudecodeconfig.ProviderID, result.Summary.Profile.ID, result.OperationID, err)
	return result, err
}

func (s *CodexService) Detect(ctx context.Context) (codex.CodexDetectResult, error) {
	return s.application.Codex().Detect(ctx)
}

func (s *CodexService) ListProfiles(ctx context.Context) (codex.CodexProfileListResult, error) {
	return s.application.Codex().ListProfiles(ctx)
}

func (s *CodexService) ShowProfile(ctx context.Context, profileID string) (codex.CodexProfileDetail, error) {
	return s.application.Codex().GetProfile(ctx, profileID)
}

func (s *CodexService) GetSettings(ctx context.Context) (codex.CodexSettings, error) {
	return s.application.Codex().GetSettings(ctx)
}

func (s *CodexService) UpdateSettings(ctx context.Context, req codex.UpdateCodexSettingsRequest) (codex.CodexSettings, error) {
	// Persistence and both in-process schedulers advance together so a slower
	// older request cannot restore stale runtime intervals after a newer write.
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	settings, err := s.application.Codex().UpdateSettings(ctx, req)
	if err != nil {
		return codex.CodexSettings{}, err
	}
	if req.UsageSyncIntervalSeconds != nil {
		s.autoSync.SetInterval(settings.UsageSyncIntervalSeconds)
	}
	if err := s.quota.Reload(ctx); err != nil {
		// Persistence already committed. Surface scheduler reload failures through
		// its redacted runtime status instead of misreporting the save as failed.
		s.quota.recordRuntimeError(err)
	}
	return settings, nil
}

func (s *CodexService) ReadProfileQuota(ctx context.Context, profileID string) (codex.CodexProfileQuota, error) {
	return s.quota.ReadProfileQuota(ctx, profileID)
}

func (s *CodexService) QuotaRuntimeStatus(ctx context.Context) (CodexQuotaRuntimeStatus, error) {
	if err := s.application.Agents().RequireAgent(ctx, agent.Codex); err != nil {
		return CodexQuotaRuntimeStatus{}, err
	}
	return s.quota.Status(), nil
}

func (s *CodexService) CreateProfile(ctx context.Context, req CreateCodexProfileRequest) (codex.CodexProfileSaveResult, error) {
	// Raw desired files and hidden credentials must stay behind the Desktop service boundary.
	result, err := s.application.Codex().CreateProfile(ctx, codex.CreateCodexProfileRequest{
		ProfileID:               req.ProfileID,
		Name:                    req.Name,
		Description:             req.Description,
		NewConfigSetID:          req.NewConfigSetID,
		NewConfigSetName:        req.NewConfigSetName,
		NewConfigSetDescription: req.NewConfigSetDescription,
	})
	profileID := result.Profile.ID
	if profileID == "" {
		profileID = strings.TrimSpace(req.ProfileID)
	}
	s.notifyMutationResult(DesktopChangeCodexProfileChanged, "codex.createProfile", codexconfig.ProviderID, profileID, "", err)
	return result, err
}

func (s *CodexService) ForkProfile(ctx context.Context, req ForkCodexProfileRequest) (codex.CodexProfileSaveResult, error) {
	result, err := s.application.Codex().ForkProfile(ctx, codex.ForkCodexProfileRequest{
		SourceProfileID:         req.SourceProfileID,
		ProfileID:               req.ProfileID,
		CredentialBinding:       req.CredentialBinding,
		ConfigBinding:           req.ConfigBinding,
		NewConfigSetID:          req.NewConfigSetID,
		NewConfigSetName:        req.NewConfigSetName,
		NewConfigSetDescription: req.NewConfigSetDescription,
		Name:                    req.Name,
		Description:             req.Description,
	})
	profileID := result.Profile.ID
	if profileID == "" {
		profileID = strings.TrimSpace(req.ProfileID)
	}
	s.notifyMutationResult(DesktopChangeCodexProfileChanged, "codex.forkProfile", codexconfig.ProviderID, profileID, "", err)
	return result, err
}

func (s *CodexService) SaveActiveProfileState(ctx context.Context) (codex.CodexProfileStateSaveResult, error) {
	result, err := s.application.Codex().SaveActiveProfileState(ctx)
	s.notifyMutationResult(DesktopChangeCodexProfileChanged, "codex.saveActiveProfileState", codexconfig.ProviderID, result.ProfileID, result.OperationID, err)
	return result, err
}

func (s *CodexService) SetProfileConfig(ctx context.Context, req UpdateCodexProfileConfigSetRequest) (codex.CodexProfileDetail, error) {
	result, err := s.application.Codex().UpdateProfileConfigSet(ctx, codex.UpdateCodexProfileConfigSetRequest{ProfileID: req.ProfileID, ConfigSetID: req.ConfigSetID})
	s.notifyMutationResult(DesktopChangeCodexProfileChanged, "codex.setProfileConfig", codexconfig.ProviderID, strings.TrimSpace(req.ProfileID), "", err)
	return result, err
}

func (s *CodexService) ListConfigSets(ctx context.Context) (codex.CodexConfigSetListResult, error) {
	return s.application.Codex().ListConfigSets(ctx)
}

func (s *CodexService) ShowConfigSet(ctx context.Context, configSetID string) (codex.CodexConfigSet, error) {
	return s.application.Codex().GetConfigSet(ctx, configSetID)
}

func (s *CodexService) CreateConfigSet(ctx context.Context, req CreateCodexConfigSetRequest) (codex.CodexConfigSet, error) {
	result, err := s.application.Codex().CreateConfigSet(ctx, codex.CreateCodexConfigSetRequest{ConfigSetID: req.ConfigSetID, Name: req.Name, Description: req.Description})
	s.notifyMutationResult(DesktopChangeCodexConfigSetChanged, "codex.createConfigSet", codexconfig.ProviderID, "", "", err)
	return result, err
}

func (s *CodexService) CopyConfigSet(ctx context.Context, req CopyCodexConfigSetRequest) (codex.CodexConfigSet, error) {
	result, err := s.application.Codex().CopyConfigSet(ctx, codex.CopyCodexConfigSetRequest{SourceConfigSetID: req.SourceConfigSetID, ConfigSetID: req.ConfigSetID, Name: req.Name, Description: req.Description})
	s.notifyMutationResult(DesktopChangeCodexConfigSetChanged, "codex.copyConfigSet", codexconfig.ProviderID, "", "", err)
	return result, err
}

func (s *CodexService) UpdateConfigSet(ctx context.Context, req UpdateCodexConfigSetRequest) (codex.CodexConfigSet, error) {
	result, err := s.application.Codex().UpdateConfigSet(ctx, codex.UpdateCodexConfigSetRequest{ConfigSetID: req.ConfigSetID, Name: req.Name, Description: req.Description})
	s.notifyMutationResult(DesktopChangeCodexConfigSetChanged, "codex.updateConfigSet", codexconfig.ProviderID, "", "", err)
	return result, err
}

func (s *CodexService) DeleteConfigSet(ctx context.Context, configSetID string) error {
	err := s.application.Codex().DeleteConfigSet(ctx, configSetID)
	s.notifyMutationResult(DesktopChangeCodexConfigSetChanged, "codex.deleteConfigSet", codexconfig.ProviderID, "", "", err)
	return err
}

func (s *CodexService) UpdateProfileMetadata(ctx context.Context, req UpdateCodexProfileMetadataRequest) (profile.Profile, error) {
	profileID := strings.TrimSpace(req.ProfileID)
	if _, err := s.application.Codex().GetProfile(ctx, profileID); err != nil {
		s.notifyMutationResult(DesktopChangeCodexProfileChanged, "codex.updateProfileMetadata", codexconfig.ProviderID, profileID, "", err)
		return profile.Profile{}, err
	}

	result, err := s.application.Profiles().Update(ctx, profile.UpdateRequest{
		ID:          profileID,
		Name:        req.Name,
		Description: req.Description,
	})
	s.notifyMutationResult(DesktopChangeCodexProfileChanged, "codex.updateProfileMetadata", codexconfig.ProviderID, profileID, "", err)
	return result, err
}

func (s *CodexService) ExportProfiles(ctx context.Context, req ExportCodexProfilesRequest) (codex.CodexProfileExportResult, error) {
	return s.application.Codex().ExportProfiles(ctx, codex.ExportCodexProfilesRequest{
		ProfileIDs: req.ProfileIDs,
		OutputPath: req.OutputPath, Overwrite: req.Overwrite,
	})
}

func (s *CodexService) InspectProfileImport(ctx context.Context, inputPath string) (codex.CodexProfileImportPlan, error) {
	return s.application.Codex().InspectProfileImport(ctx, codex.InspectCodexProfileImportRequest{
		InputPath: inputPath,
	})
}

func (s *CodexService) ApplyProfileImport(ctx context.Context, req ApplyCodexProfileImportRequest) (codex.CodexProfileImportResult, error) {
	result, err := s.application.Codex().ImportProfiles(ctx, codex.ImportCodexProfilesRequest{
		InputPath:               req.InputPath,
		ExpectedPlanFingerprint: req.ExpectedPlanFingerprint, Confirm: req.Confirm,
	})
	if err == nil && result.Changed {
		s.notifyMutationResult(DesktopChangeCodexProfileChanged, "codex.importProfiles", codexconfig.ProviderID, "", result.OperationID, nil)
	}
	return result, err
}

func (s *ProfileService) ListProviders(ctx context.Context) ([]provider.Provider, error) {
	return s.application.Providers().List(ctx, provider.ListRequest{IncludeDisabled: true})
}

func (s *ProfileService) ListProfiles(ctx context.Context) ([]profile.Profile, error) {
	return s.application.Profiles().List(ctx)
}

func (s *ProfileService) ListTargets(ctx context.Context, profileID, providerID string) ([]profiletarget.ProfileTarget, error) {
	return s.application.Targets().List(ctx, profiletarget.ListProfileTargetsRequest{
		ProfileID:       profileID,
		ProviderID:      providerID,
		IncludeDisabled: true,
	})
}

func (s *SwitchService) BuildPlan(ctx context.Context, providerID, profileID string) (switching.SwitchPlan, error) {
	return s.application.Switching().BuildPlan(ctx, switching.BuildPlanRequest{
		ProviderID: providerID,
		ProfileID:  profileID,
	})
}

func (s *SwitchService) Apply(ctx context.Context, req SwitchApplyRequest) (switching.ApplySwitchResult, error) {
	fingerprint := strings.TrimSpace(req.ExpectedPlanFingerprint)
	if fingerprint == "" {
		return switching.ApplySwitchResult{}, apperror.New(apperror.ConfirmationRequired, "desktop switch apply requires a confirmed plan fingerprint")
	}
	result, err := s.application.Switching().Apply(ctx, switching.ApplySwitchRequest{
		ProviderID:              req.ProviderID,
		ProfileID:               req.ProfileID,
		Confirm:                 req.Confirm,
		ExpectedPlanFingerprint: fingerprint,
	})
	providerID := result.Provider.ID
	if providerID == "" {
		providerID = strings.TrimSpace(req.ProviderID)
	}
	profileID := result.Profile.ID
	if profileID == "" {
		profileID = strings.TrimSpace(req.ProfileID)
	}
	s.notifyMutationResult(DesktopChangeSwitchApplied, "switch.apply", providerID, profileID, result.OperationID, err)
	if recoveryCleanupRequiredError(err) || (err == nil && !result.RecoveryCleanupCompleted) {
		notifyRecoveryCleanupChanged(
			s.changes,
			"switch.recoveryCleanup",
			providerID,
			profileID,
			result.OperationID,
			false,
		)
	}
	return result, err
}

func (s *DoctorService) Run(ctx context.Context) (doctor.DoctorResult, error) {
	return s.application.Doctor().Run(ctx)
}

func (s *DoctorService) RepairLock(ctx context.Context, confirm bool) (doctor.DoctorRepairLockResult, error) {
	result, err := s.application.Doctor().RepairLock(ctx, confirm)
	if err != nil || result.Repaired {
		s.notifyMutationResult(DesktopChangeLockRepaired, "doctor.repairLock", "", "", "", err)
	}
	return result, err
}

func (s *DoctorService) RetryRecoveryCleanup(ctx context.Context, confirm bool) (recoverycleanup.RetryRecoveryCleanupResult, error) {
	result, err := s.application.Doctor().RetryRecoveryCleanup(ctx, confirm)
	notifyRecoveryCleanupChanged(
		s.changes,
		"doctor.retryRecoveryCleanup",
		"",
		"",
		"",
		err == nil && result.RecoveryCleanupCompleted,
	)
	return result, err
}

func (s *BackupService) Create(ctx context.Context) (appbackup.BackupDetail, error) {
	result, err := s.application.Backups().Create(ctx, appbackup.CreateRequest{Kind: appbackup.KindManual, Reason: appbackup.ReasonManual})
	s.notifyMutationResult("backup.create", err)
	return result, err
}

func (s *BackupService) List(ctx context.Context) (appbackup.ListResult, error) {
	return s.application.Backups().List(ctx)
}

func (s *BackupService) Show(ctx context.Context, backupID string) (appbackup.BackupDetail, error) {
	return s.application.Backups().Show(ctx, backupID)
}

func (s *BackupService) Export(ctx context.Context, req appbackup.ExportRequest) (appbackup.ExportResult, error) {
	return s.application.Backups().Export(ctx, req)
}

func (s *BackupService) PreviewRestore(ctx context.Context, source appbackup.RestoreSource) (appbackup.RestorePreview, error) {
	return s.application.Backups().PreviewRestore(ctx, source)
}

func (s *BackupService) Restore(ctx context.Context, req appbackup.RestoreRequest) (appbackup.RestoreResult, error) {
	result, err := s.application.Backups().Restore(ctx, req)
	if err != nil {
		s.notifyMutationResult("backup.restore", err)
		return appbackup.RestoreResult{}, err
	}
	s.notifyMutationResult("backup.restore", nil)
	s.restartMu.RLock()
	restart := s.restart
	s.restartMu.RUnlock()
	if restart == nil {
		return result, apperror.New(apperror.ApplicationRestartFailed, "application data was restored, but ProfileDeck could not restart automatically")
	}
	if err := restart(); err != nil {
		return result, apperror.New(apperror.ApplicationRestartFailed, "application data was restored, but ProfileDeck could not restart automatically")
	}
	return result, nil
}

func (s *BackupService) Delete(ctx context.Context, req appbackup.DeleteRequest) error {
	err := s.application.Backups().Delete(ctx, req)
	s.notifyMutationResult("backup.delete", err)
	return err
}

func (s *BackupService) KeyStatus(ctx context.Context) (appbackup.KeyStatus, error) {
	return s.application.Backups().KeyStatus(ctx)
}

func (s *BackupService) ExportKey(ctx context.Context, req appbackup.ExportKeyRequest) (appbackup.ExportKeyResult, error) {
	return s.application.Backups().ExportKey(ctx, req)
}

func (s *BackupService) ImportKey(ctx context.Context, req appbackup.ImportKeyRequest) (appbackup.ImportKeyResult, error) {
	result, err := s.application.Backups().ImportKey(ctx, req)
	s.notifyMutationResult("backup.key.import", err)
	return result, err
}

func (s *BackupService) SetAutomatic(ctx context.Context, enabled bool) (settings.Desktop, error) {
	result, err := s.application.Settings().SetAutomaticBackups(ctx, enabled)
	if err == nil && enabled {
		s.runtime.Wake()
	}
	s.notifyMutationResult("backup.setAutomatic", err)
	return result, err
}

func (s *BackupService) setRestarter(restart func() error) {
	s.restartMu.Lock()
	s.restart = restart
	s.restartMu.Unlock()
}

// ConfigureBackupRestarter keeps the process callback out of generated Wails
// bindings while allowing the Desktop composition root to provide it.
func ConfigureBackupRestarter(service *BackupService, restart func() error) {
	if service != nil {
		service.setRestarter(restart)
	}
}

func (s *DoctorService) InspectRecovery(ctx context.Context, operationID string) (switching.RecoveryInspection, error) {
	return s.application.Switching().InspectRecovery(ctx, operationID)
}

func (s *DoctorService) RecoverOperation(ctx context.Context, operationID string, confirm bool) (switching.RecoverOperationResult, error) {
	result, err := s.application.Switching().RecoverOperation(ctx, switching.RecoverOperationParams{
		OperationID: operationID, Confirm: confirm,
	})
	resultOperationID := result.RecoveryOperationID
	if resultOperationID == "" {
		resultOperationID = strings.TrimSpace(operationID)
	}
	s.notifyMutationResult(DesktopChangeSwitchRecovered, "doctor.recoverOperation", result.ProviderID, result.ProfileID, resultOperationID, err)
	if recoveryCleanupRequiredError(err) || (err == nil && !result.RecoveryCleanupCompleted) {
		notifyRecoveryCleanupChanged(
			s.changes,
			"doctor.recoveryCleanup",
			result.ProviderID,
			result.ProfileID,
			resultOperationID,
			false,
		)
	}
	if err == nil && result.ProviderID == codexconfig.ProviderID {
		reloadCodexQuotaRuntime(s.quota)
	}
	return result, err
}

func (s *UsageService) Summary(ctx context.Context, providerID string) (usage.UsageSummaryResult, error) {
	if providerID == "" {
		providerID = codexconfig.ProviderID
	}
	return s.application.Usage().Summary(ctx, usage.UsageSummaryRequest{ProviderID: providerID})
}

func (s *UsageService) AutoSyncStatus(ctx context.Context) (UsageAutoSyncStatus, error) {
	if err := s.application.Agents().RequireAgent(ctx, agent.Codex); err != nil {
		return UsageAutoSyncStatus{}, err
	}
	return s.autoSync.Status(), nil
}

func (s *UsageService) Report(ctx context.Context, providerID, rangeValue string) (usage.UsageReportResult, error) {
	if providerID == "" {
		providerID = codexconfig.ProviderID
	}
	return s.application.Usage().Report(ctx, usage.UsageReportRequest{
		ProviderID: providerID,
		Range:      usage.UsageRangePreset(rangeValue),
	})
}

func (s *SettingsService) Get(ctx context.Context) (settings.Desktop, error) {
	return s.application.Settings().Get(ctx)
}

func (s *SettingsService) Update(ctx context.Context, req settings.UpdateRequest) (settings.Desktop, error) {
	return s.application.Settings().Update(ctx, req)
}

func (s *AppService) notifyMutationResult(kind, source, providerID, profileID, operationID string, err error) {
	notifyMutationResult(s.changes, kind, source, providerID, profileID, operationID, err)
}

func (s *CodexService) notifyMutationResult(kind, source, providerID, profileID, operationID string, err error) {
	notifyMutationResult(s.changes, kind, source, providerID, profileID, operationID, err)
	if err == nil && kind == DesktopChangeCodexProfileChanged {
		reloadCodexQuotaRuntime(s.quota)
	}
}

func (s *AntigravityService) notifyMutationResult(kind, source, providerID, profileID, operationID string, err error) {
	notifyMutationResult(s.changes, kind, source, providerID, profileID, operationID, err)
}

func (s *ClaudeCodeService) notifyMutationResult(kind, source, providerID, profileID, operationID string, err error) {
	notifyMutationResult(s.changes, kind, source, providerID, profileID, operationID, err)
}

func (s *SwitchService) notifyMutationResult(kind, source, providerID, profileID, operationID string, err error) {
	notifyMutationResult(s.changes, kind, source, providerID, profileID, operationID, err)
	if err == nil && providerID == codexconfig.ProviderID {
		reloadCodexQuotaRuntime(s.quota)
	}
}

func (s *DoctorService) notifyMutationResult(kind, source, providerID, profileID, operationID string, err error) {
	notifyMutationResult(s.changes, kind, source, providerID, profileID, operationID, err)
}

func (s *BackupService) notifyMutationResult(source string, err error) {
	notifyMutationResult(s.changes, DesktopChangeApplicationBackupChanged, source, "", "", "", err)
}

func reloadCodexQuotaRuntime(runtime *codexQuotaRuntime) {
	if runtime == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := runtime.Reload(ctx); err != nil {
		runtime.recordRuntimeError(err)
	}
}

func notifyMutationResult(changes *ChangeNotifier, kind, source, providerID, profileID, operationID string, err error) {
	event := DesktopChangeEvent{
		Kind:        kind,
		Source:      source,
		Status:      DesktopChangeStatusSuccess,
		ProviderID:  providerID,
		ProfileID:   profileID,
		OperationID: operationID,
	}
	switch kind {
	case DesktopChangeCodexProfileChanged:
		event.ProfileChanged = true
		event.ConfigSetsChanged = strings.Contains(source, "createProfile") || strings.Contains(source, "forkProfile") || strings.Contains(source, "saveActiveProfileState") || strings.Contains(source, "setProfileConfig") || strings.Contains(source, "importProfiles")
		event.ActiveStateChanged = strings.Contains(source, "createProfile")
	case DesktopChangeCodexConfigSetChanged:
		event.ConfigSetsChanged = true
	case DesktopChangeAntigravityProfileChanged:
		event.ProfileChanged = true
		event.ActiveStateChanged = strings.Contains(source, "createProfile")
	case DesktopChangeSwitchApplied, DesktopChangeSwitchRecovered:
		event.ProfileChanged = providerID == codexconfig.ProviderID || providerID == agyconfig.ProviderID
		event.ConfigSetsChanged = providerID == codexconfig.ProviderID
		event.ActiveStateChanged = true
	}
	if err != nil {
		if errors.Is(err, context.Canceled) {
			event.Status = DesktopChangeStatusCanceled
		} else {
			event.Status = DesktopChangeStatusFailure
		}
		event.Error = FormatDesktopErrorPtr(err)
	}
	changes.Notify(event)
}

func recoveryCleanupRequiredError(err error) bool {
	var appErr *apperror.Error
	return errors.As(err, &appErr) && appErr.Code == apperror.OperationRecoveryCleanupRequired
}

func notifyRecoveryCleanupChanged(
	changes *ChangeNotifier,
	source string,
	providerID string,
	profileID string,
	operationID string,
	completed bool,
) {
	status := DesktopChangeStatusFailure
	if completed {
		status = DesktopChangeStatusSuccess
	}
	changes.Notify(DesktopChangeEvent{
		Kind: DesktopChangeRecoveryCleanupChanged, Source: source, Status: status,
		ProviderID: providerID, ProfileID: profileID, OperationID: operationID,
	})
}

func FormatDesktopError(err error) DesktopError {
	if err == nil {
		return DesktopError{}
	}
	if errors.Is(err, context.Canceled) {
		return DesktopError{Code: "CANCELED", Message: "operation canceled"}
	}
	publicErr := apperror.Public(err)
	return DesktopError{
		Code:    string(publicErr.Code),
		Message: publicErr.Message,
		Details: desktopErrorDetails(err),
	}
}

func desktopErrorDetails(err error) map[string]any {
	var appErr *apperror.Error
	if !errors.As(err, &appErr) {
		return nil
	}
	reason, _ := appErr.Details["reason"].(string)
	allowed := appErr.Code == apperror.ConfirmationRequired && reason == "replace_required" ||
		appErr.Code == apperror.ExportFailed && reason == "exists"
	if !allowed {
		return nil
	}
	// These stable reasons drive explicit confirmation flows in the frontend;
	// paths and arbitrary backend diagnostics never cross the Desktop boundary.
	return map[string]any{"reason": reason}
}

func FormatDesktopErrorPtr(err error) *DesktopError {
	if err == nil {
		return nil
	}
	payload := FormatDesktopError(err)
	return &payload
}
