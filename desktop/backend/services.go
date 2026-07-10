package backend

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/strahe/profiledeck/internal/app"
	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
)

type AppService struct {
	info       app.Info
	env        Environment
	mu         sync.RWMutex
	startupErr error
	changes    *ChangeNotifier
}

type CodexService struct {
	env     Environment
	changes *ChangeNotifier
}

type ProfileService struct {
	env Environment
}

type SwitchService struct {
	env     Environment
	changes *ChangeNotifier
}

type DoctorService struct {
	env     Environment
	changes *ChangeNotifier
}

type BackupService struct {
	env     Environment
	changes *ChangeNotifier
}

type UsageService struct {
	env     Environment
	changes *ChangeNotifier
}

type SettingsService struct {
	env Environment
}

type Services struct {
	App      *AppService
	Codex    *CodexService
	Profile  *ProfileService
	Switch   *SwitchService
	Doctor   *DoctorService
	Backup   *BackupService
	Usage    *UsageService
	Settings *SettingsService
	changes  *ChangeNotifier
}

type DashboardResult struct {
	Info            app.Info                      `json:"info"`
	Environment     Environment                   `json:"environment"`
	Status          app.StatusResult              `json:"status"`
	Doctor          *app.DoctorResult             `json:"doctor,omitempty"`
	Providers       []app.Provider                `json:"providers"`
	Profiles        []app.Profile                 `json:"profiles"`
	ActiveStates    []app.ActiveProviderState     `json:"active_states"`
	CodexProfiles   *app.CodexProfileListResult   `json:"codex_profiles,omitempty"`
	CodexConfigSets *app.CodexConfigSetListResult `json:"codex_config_sets,omitempty"`
	Usage           *app.UsageSummaryResult       `json:"usage,omitempty"`
	StartupError    *DesktopError                 `json:"startup_error,omitempty"`
	GeneratedAt     int64                         `json:"generated_at_unix_ms"`
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

func NewServices(info app.Info, env Environment, startupErr error) Services {
	changes := NewChangeNotifier()
	return Services{
		App:      &AppService{info: info, env: env, startupErr: startupErr, changes: changes},
		Codex:    &CodexService{env: env, changes: changes},
		Profile:  &ProfileService{env: env},
		Switch:   &SwitchService{env: env, changes: changes},
		Doctor:   &DoctorService{env: env, changes: changes},
		Backup:   &BackupService{env: env, changes: changes},
		Usage:    &UsageService{env: env, changes: changes},
		Settings: &SettingsService{env: env},
		changes:  changes,
	}
}

func (s Services) SubscribeChanges(listener func(DesktopChangeEvent)) func() {
	return s.changes.Subscribe(listener)
}

func Bootstrap(ctx context.Context, env Environment) error {
	// Desktop startup may create ProfileDeck runtime state, but it must not touch
	// Codex or any other target tool files; target writes stay in switch/rollback.
	_, err := app.Init(ctx, app.InitRequest{ConfigDir: env.ConfigDir})
	return err
}

func (s *AppService) Info(ctx context.Context) app.Info {
	return s.info
}

func (s *AppService) Environment(ctx context.Context) Environment {
	return s.env
}

func (s *AppService) Initialize(ctx context.Context) (app.InitResult, error) {
	result, err := app.Init(ctx, app.InitRequest{ConfigDir: s.env.ConfigDir})
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
		Providers:    []app.Provider{},
		Profiles:     []app.Profile{},
		ActiveStates: []app.ActiveProviderState{},
		GeneratedAt:  time.Now().UnixMilli(),
	}
	s.mu.RLock()
	startupErr := s.startupErr
	s.mu.RUnlock()
	if startupErr != nil {
		result.StartupError = FormatDesktopErrorPtr(startupErr)
	}

	status, err := app.Status(ctx, app.StatusRequest{ConfigDir: s.env.ConfigDir})
	if err != nil {
		return result, err
	}
	result.Status = status
	if !status.Initialized || !status.SchemaHealthy {
		return result, nil
	}

	doctor, err := app.Doctor(ctx, app.DoctorRequest{ConfigDir: s.env.ConfigDir})
	if err != nil {
		return result, err
	}
	result.Doctor = &doctor

	providers, err := app.ListProviders(ctx, app.ListProvidersRequest{ConfigDir: s.env.ConfigDir, IncludeDisabled: true})
	if err != nil {
		return result, err
	}
	result.Providers = providers

	profiles, err := app.ListProfiles(ctx, app.ListProfilesRequest{ConfigDir: s.env.ConfigDir})
	if err != nil {
		return result, err
	}
	result.Profiles = profiles

	activeStates, err := app.ListActiveProviderStates(ctx, app.ListActiveProviderStatesRequest{ConfigDir: s.env.ConfigDir})
	if err != nil {
		return result, err
	}
	result.ActiveStates = activeStates

	if codexProfiles, err := app.ListCodexProfiles(ctx, app.ListCodexProfilesRequest{ConfigDir: s.env.ConfigDir}); err == nil {
		result.CodexProfiles = &codexProfiles
	}
	if codexConfigSets, err := app.ListCodexConfigSets(ctx, app.ListCodexConfigSetsRequest{ConfigDir: s.env.ConfigDir}); err == nil {
		result.CodexConfigSets = &codexConfigSets
	}

	usage, err := app.UsageSummary(ctx, app.UsageSummaryRequest{ConfigDir: s.env.ConfigDir, ProviderID: codexconfig.ProviderID})
	if err == nil {
		result.Usage = &usage
	}
	return result, nil
}

func (s *CodexService) Detect(ctx context.Context) (app.CodexDetectResult, error) {
	return app.CodexDetect(ctx, app.CodexDetectRequest{ConfigDir: s.env.ConfigDir, CodexDir: s.env.CodexDir})
}

func (s *CodexService) ListProfiles(ctx context.Context) (app.CodexProfileListResult, error) {
	return app.ListCodexProfiles(ctx, app.ListCodexProfilesRequest{ConfigDir: s.env.ConfigDir})
}

func (s *CodexService) ShowProfile(ctx context.Context, profileID string) (app.CodexProfileDetail, error) {
	return app.GetCodexProfile(ctx, app.GetCodexProfileRequest{ConfigDir: s.env.ConfigDir, ProfileID: profileID})
}

func (s *CodexService) CreateProfile(ctx context.Context, req CreateCodexProfileRequest) (app.CodexProfileSaveResult, error) {
	// Raw desired files and hidden credentials must stay behind the Desktop service boundary.
	result, err := app.CreateCodexProfile(ctx, app.CreateCodexProfileRequest{
		ConfigDir:               s.env.ConfigDir,
		CodexDir:                s.env.CodexDir,
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

func (s *CodexService) ForkProfile(ctx context.Context, req ForkCodexProfileRequest) (app.CodexProfileSaveResult, error) {
	result, err := app.ForkCodexProfile(ctx, app.ForkCodexProfileRequest{
		ConfigDir:               s.env.ConfigDir,
		CodexDir:                s.env.CodexDir,
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

func (s *CodexService) SaveActiveProfileState(ctx context.Context) (app.CodexProfileStateSaveResult, error) {
	result, err := app.SaveActiveCodexProfileState(ctx, app.SaveActiveCodexProfileStateRequest{ConfigDir: s.env.ConfigDir, CodexDir: s.env.CodexDir})
	s.notifyMutationResult(DesktopChangeCodexProfileChanged, "codex.saveActiveProfileState", codexconfig.ProviderID, result.ProfileID, result.OperationID, err)
	return result, err
}

func (s *CodexService) SetProfileConfig(ctx context.Context, req UpdateCodexProfileConfigSetRequest) (app.CodexProfileDetail, error) {
	result, err := app.UpdateCodexProfileConfigSet(ctx, app.UpdateCodexProfileConfigSetRequest{ConfigDir: s.env.ConfigDir, ProfileID: req.ProfileID, ConfigSetID: req.ConfigSetID})
	s.notifyMutationResult(DesktopChangeCodexProfileChanged, "codex.setProfileConfig", codexconfig.ProviderID, strings.TrimSpace(req.ProfileID), "", err)
	return result, err
}

func (s *CodexService) ListConfigSets(ctx context.Context) (app.CodexConfigSetListResult, error) {
	return app.ListCodexConfigSets(ctx, app.ListCodexConfigSetsRequest{ConfigDir: s.env.ConfigDir})
}

func (s *CodexService) ShowConfigSet(ctx context.Context, configSetID string) (app.CodexConfigSet, error) {
	return app.GetCodexConfigSet(ctx, app.GetCodexConfigSetRequest{ConfigDir: s.env.ConfigDir, ConfigSetID: configSetID})
}

func (s *CodexService) CreateConfigSet(ctx context.Context, req CreateCodexConfigSetRequest) (app.CodexConfigSet, error) {
	result, err := app.CreateCodexConfigSet(ctx, app.CreateCodexConfigSetRequest{ConfigDir: s.env.ConfigDir, CodexDir: s.env.CodexDir, ConfigSetID: req.ConfigSetID, Name: req.Name, Description: req.Description})
	s.notifyMutationResult(DesktopChangeCodexConfigSetChanged, "codex.createConfigSet", codexconfig.ProviderID, "", "", err)
	return result, err
}

func (s *CodexService) CopyConfigSet(ctx context.Context, req CopyCodexConfigSetRequest) (app.CodexConfigSet, error) {
	result, err := app.CopyCodexConfigSet(ctx, app.CopyCodexConfigSetRequest{ConfigDir: s.env.ConfigDir, SourceConfigSetID: req.SourceConfigSetID, ConfigSetID: req.ConfigSetID, Name: req.Name, Description: req.Description})
	s.notifyMutationResult(DesktopChangeCodexConfigSetChanged, "codex.copyConfigSet", codexconfig.ProviderID, "", "", err)
	return result, err
}

func (s *CodexService) UpdateConfigSet(ctx context.Context, req UpdateCodexConfigSetRequest) (app.CodexConfigSet, error) {
	result, err := app.UpdateCodexConfigSet(ctx, app.UpdateCodexConfigSetRequest{ConfigDir: s.env.ConfigDir, ConfigSetID: req.ConfigSetID, Name: req.Name, Description: req.Description})
	s.notifyMutationResult(DesktopChangeCodexConfigSetChanged, "codex.updateConfigSet", codexconfig.ProviderID, "", "", err)
	return result, err
}

func (s *CodexService) DeleteConfigSet(ctx context.Context, configSetID string) error {
	err := app.DeleteCodexConfigSet(ctx, app.DeleteCodexConfigSetRequest{ConfigDir: s.env.ConfigDir, ConfigSetID: configSetID})
	s.notifyMutationResult(DesktopChangeCodexConfigSetChanged, "codex.deleteConfigSet", codexconfig.ProviderID, "", "", err)
	return err
}

func (s *CodexService) UpdateProfileMetadata(ctx context.Context, req UpdateCodexProfileMetadataRequest) (app.Profile, error) {
	profileID := strings.TrimSpace(req.ProfileID)
	if _, err := app.GetCodexProfile(ctx, app.GetCodexProfileRequest{ConfigDir: s.env.ConfigDir, ProfileID: profileID}); err != nil {
		s.notifyMutationResult(DesktopChangeCodexProfileChanged, "codex.updateProfileMetadata", codexconfig.ProviderID, profileID, "", err)
		return app.Profile{}, err
	}

	result, err := app.UpdateProfile(ctx, app.UpdateProfileRequest{
		ConfigDir:   s.env.ConfigDir,
		ID:          profileID,
		Name:        req.Name,
		Description: req.Description,
	})
	s.notifyMutationResult(DesktopChangeCodexProfileChanged, "codex.updateProfileMetadata", codexconfig.ProviderID, profileID, "", err)
	return result, err
}

func (s *ProfileService) ListProviders(ctx context.Context) ([]app.Provider, error) {
	return app.ListProviders(ctx, app.ListProvidersRequest{ConfigDir: s.env.ConfigDir, IncludeDisabled: true})
}

func (s *ProfileService) ListProfiles(ctx context.Context) ([]app.Profile, error) {
	return app.ListProfiles(ctx, app.ListProfilesRequest{ConfigDir: s.env.ConfigDir})
}

func (s *ProfileService) ListTargets(ctx context.Context, profileID string, providerID string) ([]app.ProfileTarget, error) {
	return app.ListProfileTargets(ctx, app.ListProfileTargetsRequest{
		ConfigDir:       s.env.ConfigDir,
		ProfileID:       profileID,
		ProviderID:      providerID,
		IncludeDisabled: true,
	})
}

func (s *SwitchService) BuildPlan(ctx context.Context, providerID string, profileID string) (app.SwitchPlan, error) {
	return app.BuildPlan(ctx, app.BuildPlanRequest{
		ConfigDir:  s.env.ConfigDir,
		ProviderID: providerID,
		ProfileID:  profileID,
	})
}

func (s *SwitchService) Apply(ctx context.Context, req SwitchApplyRequest) (app.ApplySwitchResult, error) {
	fingerprint := strings.TrimSpace(req.ExpectedPlanFingerprint)
	if fingerprint == "" {
		return app.ApplySwitchResult{}, app.NewError(app.ErrorConfirmationRequired, "desktop switch apply requires a confirmed plan fingerprint")
	}
	result, err := app.ApplySwitch(ctx, app.ApplySwitchRequest{
		ConfigDir:               s.env.ConfigDir,
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
	return result, err
}

func (s *DoctorService) Run(ctx context.Context) (app.DoctorResult, error) {
	return app.Doctor(ctx, app.DoctorRequest{ConfigDir: s.env.ConfigDir})
}

func (s *DoctorService) RepairLock(ctx context.Context, confirm bool) (app.DoctorRepairLockResult, error) {
	result, err := app.RepairDoctorLock(ctx, app.DoctorRepairLockRequest{ConfigDir: s.env.ConfigDir, Confirm: confirm})
	if err != nil || result.Repaired {
		s.notifyMutationResult(DesktopChangeLockRepaired, "doctor.repairLock", "", "", "", err)
	}
	return result, err
}

func (s *BackupService) ListBackups(ctx context.Context) (app.ListBackupsResult, error) {
	return app.ListBackups(ctx, app.ListBackupsRequest{ConfigDir: s.env.ConfigDir})
}

func (s *BackupService) ShowBackup(ctx context.Context, backupID string) (app.BackupDetail, error) {
	return app.ShowBackup(ctx, app.ShowBackupRequest{ConfigDir: s.env.ConfigDir, BackupID: backupID})
}

func (s *BackupService) ApplyRollback(ctx context.Context, backupID string, confirm bool) (app.ApplyRollbackResult, error) {
	result, err := app.ApplyRollback(ctx, app.ApplyRollbackRequest{
		ConfigDir: s.env.ConfigDir,
		BackupID:  backupID,
		Confirm:   confirm,
	})
	s.notifyMutationResult(DesktopChangeRollbackApplied, "backup.applyRollback", result.ProviderID, result.ProfileID, result.OperationID, err)
	return result, err
}

func (s *BackupService) RecoverFailedSwitch(ctx context.Context, operationID string, confirm bool) (app.RecoverFailedSwitchResult, error) {
	result, err := app.RecoverFailedSwitch(ctx, app.RecoverFailedSwitchParams{
		ConfigDir:   s.env.ConfigDir,
		OperationID: operationID,
		Confirm:     confirm,
	})
	resultOperationID := result.OperationID
	if resultOperationID == "" {
		resultOperationID = strings.TrimSpace(operationID)
	}
	s.notifyMutationResult(DesktopChangeSwitchRecovered, "backup.recoverFailedSwitch", result.ProviderID, result.ProfileID, resultOperationID, err)
	return result, err
}

func (s *UsageService) SyncCodex(ctx context.Context) (app.UsageSyncResult, error) {
	result, err := app.UsageSyncCodex(ctx, app.UsageSyncCodexRequest{ConfigDir: s.env.ConfigDir, CodexDir: s.env.CodexDir})
	providerID := result.ProviderID
	if providerID == "" {
		providerID = codexconfig.ProviderID
	}
	s.notifyMutationResult(DesktopChangeUsageSynced, "usage.syncCodex", providerID, "", "", err)
	return result, err
}

func (s *UsageService) Summary(ctx context.Context, providerID string) (app.UsageSummaryResult, error) {
	if providerID == "" {
		providerID = codexconfig.ProviderID
	}
	return app.UsageSummary(ctx, app.UsageSummaryRequest{ConfigDir: s.env.ConfigDir, ProviderID: providerID})
}

func (s *SettingsService) Get(ctx context.Context) (app.DesktopSettings, error) {
	return app.GetDesktopSettings(ctx, app.DesktopSettingsRequest{ConfigDir: s.env.ConfigDir})
}

func (s *SettingsService) Update(ctx context.Context, req app.UpdateDesktopSettingsRequest) (app.DesktopSettings, error) {
	req.ConfigDir = s.env.ConfigDir
	return app.UpdateDesktopSettings(ctx, req)
}

func (s *AppService) notifyMutationResult(kind string, source string, providerID string, profileID string, operationID string, err error) {
	notifyMutationResult(s.changes, kind, source, providerID, profileID, operationID, err)
}

func (s *CodexService) notifyMutationResult(kind string, source string, providerID string, profileID string, operationID string, err error) {
	notifyMutationResult(s.changes, kind, source, providerID, profileID, operationID, err)
}

func (s *SwitchService) notifyMutationResult(kind string, source string, providerID string, profileID string, operationID string, err error) {
	notifyMutationResult(s.changes, kind, source, providerID, profileID, operationID, err)
}

func (s *DoctorService) notifyMutationResult(kind string, source string, providerID string, profileID string, operationID string, err error) {
	notifyMutationResult(s.changes, kind, source, providerID, profileID, operationID, err)
}

func (s *BackupService) notifyMutationResult(kind string, source string, providerID string, profileID string, operationID string, err error) {
	notifyMutationResult(s.changes, kind, source, providerID, profileID, operationID, err)
}

func (s *UsageService) notifyMutationResult(kind string, source string, providerID string, profileID string, operationID string, err error) {
	notifyMutationResult(s.changes, kind, source, providerID, profileID, operationID, err)
}

func notifyMutationResult(changes *ChangeNotifier, kind string, source string, providerID string, profileID string, operationID string, err error) {
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
		event.ConfigSetsChanged = strings.Contains(source, "createProfile") || strings.Contains(source, "forkProfile") || strings.Contains(source, "saveActiveProfileState") || strings.Contains(source, "setProfileConfig")
		event.ActiveStateChanged = strings.Contains(source, "createProfile")
	case DesktopChangeCodexConfigSetChanged:
		event.ConfigSetsChanged = true
	case DesktopChangeSwitchApplied, DesktopChangeRollbackApplied, DesktopChangeSwitchRecovered:
		event.ProfileChanged = providerID == codexconfig.ProviderID
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

func FormatDesktopError(err error) DesktopError {
	if err == nil {
		return DesktopError{}
	}
	if errors.Is(err, context.Canceled) {
		return DesktopError{Code: "CANCELED", Message: "operation canceled"}
	}
	var appErr *app.AppError
	if errors.As(err, &appErr) {
		return DesktopError{
			Code:    string(appErr.Code),
			Message: appErr.Message,
			Details: appErr.Details,
		}
	}
	// Unknown errors can include local paths or driver internals. Keep the
	// desktop boundary structured without exposing raw error text.
	return DesktopError{Code: "DESKTOP_ERROR", Message: "desktop operation failed"}
}

func FormatDesktopErrorPtr(err error) *DesktopError {
	if err == nil {
		return nil
	}
	payload := FormatDesktopError(err)
	return &payload
}
