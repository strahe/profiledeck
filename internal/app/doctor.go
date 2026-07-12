package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	goruntime "runtime"
	"strconv"
	"strings"

	agyauth "github.com/strahe/profiledeck/internal/antigravity/auth"
	agyconfig "github.com/strahe/profiledeck/internal/antigravity/config"
	codexconfig "github.com/strahe/profiledeck/internal/codex/config"
	codexpreset "github.com/strahe/profiledeck/internal/codex/preset"
	"github.com/strahe/profiledeck/internal/runtime"
	"github.com/strahe/profiledeck/internal/store"
	"github.com/strahe/profiledeck/internal/targetfs"
)

const (
	DoctorLevelOK      = "OK"
	DoctorLevelWarning = "WARNING"
	DoctorLevelError   = "ERROR"

	doctorPIDStateAlive       = "alive"
	doctorPIDStateDead        = "dead"
	doctorPIDStateUnknown     = "unknown"
	doctorPIDStateUnavailable = "unavailable"

	doctorOSLockStateHeld        = "held"
	doctorOSLockStateFree        = "free"
	doctorOSLockStateUnknown     = "unknown"
	doctorOSLockStateUnavailable = "unavailable"
)

var (
	profileDeckOperationIDPrefixes = []string{"switch-", "rollback-", "codex-", "antigravity-", "profile-target-", "provider-"}
	maintenanceLockOwnerPrefixes   = []string{"codex-", "antigravity-", "profile-target-", "provider-"}
)

type DoctorRequest struct {
	ConfigDir string
}

type DoctorResult struct {
	ConfigDir    string            `json:"config_dir"`
	RuntimeRoot  string            `json:"runtime_root"`
	DatabasePath string            `json:"database_path"`
	OverallLevel string            `json:"overall_level"`
	Findings     []DoctorFinding   `json:"findings"`
	Operations   []DoctorOperation `json:"operations"`
	Lock         DoctorLock        `json:"lock"`
}

type DoctorFinding struct {
	ID      string         `json:"id"`
	Level   string         `json:"level"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

type DoctorOperation struct {
	ID                string `json:"id"`
	OperationType     string `json:"operation_type"`
	Status            string `json:"status"`
	Checkpoint        string `json:"checkpoint,omitempty"`
	ProviderID        string `json:"provider_id,omitempty"`
	ProfileID         string `json:"profile_id,omitempty"`
	BackupPath        string `json:"backup_path,omitempty"`
	BackupID          string `json:"backup_id,omitempty"`
	SourceOperationID string `json:"source_operation_id,omitempty"`
	RollbackKind      string `json:"rollback_kind,omitempty"`
	RecoveryStatus    string `json:"recovery_status,omitempty"`
	RecoveryReason    string `json:"recovery_reason,omitempty"`
	ErrorCode         string `json:"error_code,omitempty"`
	ErrorMessage      string `json:"error_message,omitempty"`
	UpdatedAtUnixMS   int64  `json:"updated_at_unix_ms"`
	Level             string `json:"level"`
	Reason            string `json:"reason"`
}

type DoctorLock struct {
	Path            string `json:"path"`
	Exists          bool   `json:"exists"`
	Owner           string `json:"owner,omitempty"`
	OperationID     string `json:"operation_id,omitempty"`
	PID             int    `json:"pid,omitempty"`
	PIDState        string `json:"pid_state"`
	CreatedAtUnixMS int64  `json:"created_at_unix_ms,omitempty"`
	OperationStatus string `json:"operation_status,omitempty"`
	OSLockState     string `json:"os_lock_state"`
	StaleCandidate  bool   `json:"stale_candidate"`
	Repairable      bool   `json:"repairable"`
	Level           string `json:"level"`
	Reason          string `json:"reason"`

	contentSHA256 string
}

type DoctorRepairLockRequest struct {
	ConfigDir string
	Confirm   bool
}

type DoctorRepairLockResult struct {
	Path     string `json:"path"`
	Repaired bool   `json:"repaired"`
	Reason   string `json:"reason"`
}

type doctorDatabaseState struct {
	db      *store.Store
	healthy bool
}

type doctorOperationMetadata struct {
	Checkpoint         string `json:"checkpoint"`
	ProviderID         string `json:"provider_id"`
	ProfileID          string `json:"profile_id"`
	BackupPath         string `json:"backup_path"`
	CurrentBackupPath  string `json:"current_backup_path"`
	SourceOperationID  string `json:"source_operation_id"`
	BackupID           string `json:"backup_id"`
	RollbackKind       string `json:"rollback_kind"`
	metadataDecodeFail bool
}

func Doctor(ctx context.Context, req DoctorRequest) (DoctorResult, error) {
	configDir, paths, err := resolveRuntime(req.ConfigDir)
	if err != nil {
		return DoctorResult{}, err
	}

	result := DoctorResult{
		ConfigDir:    configDir,
		RuntimeRoot:  paths.Root,
		DatabasePath: paths.Database,
		OverallLevel: DoctorLevelOK,
		Findings:     []DoctorFinding{},
		Operations:   []DoctorOperation{},
	}

	dbState, operations, dbFindings := inspectDoctorDatabase(ctx, paths.Database)
	result.Findings = append(result.Findings, dbFindings...)
	if dbState.db != nil {
		defer dbState.db.Close()
	}
	result.Findings = append(result.Findings, inspectSensitivePathPermissions(ctx, paths, dbState)...)
	result.Findings = append(result.Findings, inspectCodexDomainHealth(ctx, dbState)...)
	result.Findings = append(result.Findings, inspectAntigravityDomainHealth(ctx, dbState)...)

	result.Lock = inspectDoctorLock(ctx, paths.Lock, dbState)
	result.Operations = doctorOperations(ctx, dbState, paths, operations, result.Lock)
	result.OverallLevel = doctorOverallLevel(result)
	return result, nil
}

func inspectAntigravityDomainHealth(ctx context.Context, dbState doctorDatabaseState) []DoctorFinding {
	if !dbState.healthy || dbState.db == nil {
		return nil
	}
	provider, err := dbState.db.GetProvider(ctx, agyconfig.ProviderID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return []DoctorFinding{{ID: "antigravity_provider_check_failed", Level: DoctorLevelWarning, Message: "failed to inspect Antigravity provider"}}
	}
	findings := []DoctorFinding{}
	if err := validateAntigravityProvider(provider); err != nil {
		findings = append(findings, DoctorFinding{
			ID: "antigravity_agy_v2_invalid", Level: DoctorLevelError,
			Message: "Antigravity provider is not compatible with agy v2",
		})
	}
	bindings, err := dbState.db.ListProfileCredentialBindingsByProvider(ctx, agyconfig.ProviderID)
	if err != nil {
		return append(findings, DoctorFinding{ID: "antigravity_binding_check_failed", Level: DoctorLevelWarning, Message: "failed to inspect Antigravity profile bindings"})
	}
	grouped := map[string][]store.ProfileCredentialBinding{}
	for _, binding := range bindings {
		grouped[binding.ProfileID] = append(grouped[binding.ProfileID], binding)
	}
	configBindings, err := dbState.db.ListProfileConfigSetBindingsByProvider(ctx, agyconfig.ProviderID)
	if err != nil {
		return append(findings, DoctorFinding{ID: "antigravity_binding_check_failed", Level: DoctorLevelWarning, Message: "failed to inspect Antigravity profile bindings"})
	}
	configBindingCounts := map[string]int{}
	for _, binding := range configBindings {
		configBindingCounts[binding.ProfileID]++
		if _, ok := grouped[binding.ProfileID]; !ok {
			grouped[binding.ProfileID] = nil
		}
	}
	if active, activeErr := dbState.db.GetActiveState(ctx, store.ActiveStateScopeProvider, agyconfig.ProviderID); activeErr == nil {
		if _, ok := grouped[active.ProfileID]; !ok {
			grouped[active.ProfileID] = nil
		}
	} else if activeErr != nil && !errors.Is(activeErr, store.ErrNotFound) {
		findings = append(findings, DoctorFinding{ID: "antigravity_active_state_check_failed", Level: DoctorLevelWarning, Message: "failed to inspect active Antigravity Profile"})
	}
	for profileID, profileBindings := range grouped {
		if _, err := dbState.db.GetProfile(ctx, profileID); err != nil {
			findings = append(findings, DoctorFinding{
				ID: "antigravity_profile_missing", Level: DoctorLevelError,
				Message: "Antigravity binding references a missing Profile", Details: map[string]any{"profile_id": profileID},
			})
			continue
		}
		if len(profileBindings) != 1 || profileBindings[0].SlotID != agyconfig.CredentialSlot || configBindingCounts[profileID] != 0 {
			findings = append(findings, DoctorFinding{
				ID: "antigravity_login_binding_invalid", Level: DoctorLevelError,
				Message: "Antigravity Profile bindings are missing or unsupported", Details: map[string]any{"profile_id": profileID},
			})
			continue
		}
		if _, err := requireAntigravityCredential(ctx, dbState.db, profileBindings[0].CredentialID); err != nil {
			findings = append(findings, DoctorFinding{
				ID: "antigravity_login_state_invalid", Level: DoctorLevelError,
				Message: "Antigravity Profile references missing or invalid login state", Details: map[string]any{"profile_id": profileID},
			})
		}
	}

	snapshot, err := targetBackends[targetBackendKeyring].Inspect(ctx, antigravityTargetSpec())
	if err != nil {
		return append(findings, DoctorFinding{
			ID: "antigravity_login_unavailable", Level: DoctorLevelWarning,
			Message: "Antigravity login could not be read from the system credential store",
		})
	}
	if !snapshot.Exists {
		return append(findings, DoctorFinding{
			ID: "antigravity_login_missing", Level: DoctorLevelWarning,
			Message: "Antigravity is not signed in with agy v2",
		})
	}
	if _, _, err := agyauth.Normalize([]byte(snapshot.Content)); err != nil {
		findings = append(findings, DoctorFinding{
			ID: "antigravity_login_invalid", Level: DoctorLevelError,
			Message: "Antigravity login is not compatible with agy v2",
		})
	}
	return findings
}

func inspectCodexDomainHealth(ctx context.Context, dbState doctorDatabaseState) []DoctorFinding {
	if !dbState.healthy || dbState.db == nil {
		return nil
	}
	provider, err := dbState.db.GetProvider(ctx, codexconfig.ProviderID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return []DoctorFinding{{ID: "codex_provider_check_failed", Level: DoctorLevelWarning, Message: "failed to inspect Codex provider"}}
	}
	findings := []DoctorFinding{}
	metadata, err := codexpreset.DecodeProviderMetadata(provider.MetadataJSON)
	if provider.AdapterID != codexconfig.AdapterID || err != nil || !metadata.Compatible() {
		findings = append(findings, DoctorFinding{
			ID: "codex_preset_v2_invalid", Level: DoctorLevelError,
			Message: "Codex provider is not compatible with preset v2",
		})
	}
	credentialBindings, err := dbState.db.ListProfileCredentialBindingsByProvider(ctx, codexconfig.ProviderID)
	if err != nil {
		return append(findings, DoctorFinding{ID: "codex_binding_check_failed", Level: DoctorLevelWarning, Message: "failed to inspect Codex profile bindings"})
	}
	configBindings, err := dbState.db.ListProfileConfigSetBindingsByProvider(ctx, codexconfig.ProviderID)
	if err != nil {
		return append(findings, DoctorFinding{ID: "codex_binding_check_failed", Level: DoctorLevelWarning, Message: "failed to inspect Codex profile bindings"})
	}
	credentialBindingsByProfile := map[string][]store.ProfileCredentialBinding{}
	configBindingsByProfile := map[string][]store.ProfileConfigSetBinding{}
	profileIDs := map[string]struct{}{}
	for _, binding := range credentialBindings {
		credentialBindingsByProfile[binding.ProfileID] = append(credentialBindingsByProfile[binding.ProfileID], binding)
		profileIDs[binding.ProfileID] = struct{}{}
	}
	for _, binding := range configBindings {
		configBindingsByProfile[binding.ProfileID] = append(configBindingsByProfile[binding.ProfileID], binding)
		profileIDs[binding.ProfileID] = struct{}{}
	}
	if active, activeErr := dbState.db.GetActiveState(ctx, store.ActiveStateScopeProvider, codexconfig.ProviderID); activeErr == nil {
		profileIDs[active.ProfileID] = struct{}{}
	} else if !errors.Is(activeErr, store.ErrNotFound) {
		findings = append(findings, DoctorFinding{ID: "codex_active_state_check_failed", Level: DoctorLevelWarning, Message: "failed to inspect active Codex Profile"})
	}
	for profileID := range profileIDs {
		if _, err := dbState.db.GetProfile(ctx, profileID); err != nil {
			findings = append(findings, DoctorFinding{
				ID: "codex_profile_missing", Level: DoctorLevelError,
				Message: "Codex binding references a missing Profile", Details: map[string]any{"profile_id": profileID},
			})
			continue
		}
		profileConfigBindings := configBindingsByProfile[profileID]
		if len(profileConfigBindings) == 0 {
			findings = append(findings, DoctorFinding{
				ID: "codex_config_binding_missing", Level: DoctorLevelError,
				Message: "Codex profile config binding is missing", Details: map[string]any{"profile_id": profileID},
			})
		} else if len(profileConfigBindings) != 1 || profileConfigBindings[0].SlotID != codexpreset.ConfigSetSlotUserConfig {
			findings = append(findings, DoctorFinding{
				ID: "codex_config_binding_invalid", Level: DoctorLevelError,
				Message: "Codex profile config binding is invalid", Details: map[string]any{"profile_id": profileID},
			})
		} else if _, err := requireCodexConfigSet(ctx, dbState.db, profileConfigBindings[0].ConfigSetID); err != nil {
			findings = append(findings, DoctorFinding{
				ID: "codex_config_set_invalid", Level: DoctorLevelError,
				Message: "Codex profile references a missing or invalid config set",
				Details: map[string]any{"profile_id": profileID, "config_set_id": profileConfigBindings[0].ConfigSetID},
			})
		}
		profileCredentialBindings := credentialBindingsByProfile[profileID]
		if len(profileCredentialBindings) == 0 {
			findings = append(findings, DoctorFinding{
				ID: "codex_login_binding_missing", Level: DoctorLevelError,
				Message: "Codex profile login binding is missing", Details: map[string]any{"profile_id": profileID},
			})
		} else if len(profileCredentialBindings) != 1 || profileCredentialBindings[0].SlotID != codexpreset.CredentialSlotAuth {
			findings = append(findings, DoctorFinding{
				ID: "codex_login_binding_invalid", Level: DoctorLevelError,
				Message: "Codex profile login binding is invalid", Details: map[string]any{"profile_id": profileID},
			})
		} else if _, err := requireCodexAuthCredential(ctx, dbState.db, profileCredentialBindings[0].CredentialID); err != nil {
			findings = append(findings, DoctorFinding{
				ID: "codex_login_state_invalid", Level: DoctorLevelError,
				Message: "Codex profile references missing or invalid login state", Details: map[string]any{"profile_id": profileID},
			})
		}
	}
	configSets, err := dbState.db.ListProviderConfigSets(ctx, codexconfig.ProviderID, codexpreset.ConfigSetKindTOML)
	if err != nil {
		return append(findings, DoctorFinding{ID: "codex_config_set_check_failed", Level: DoctorLevelWarning, Message: "failed to inspect Codex config sets"})
	}
	for _, configSet := range configSets {
		if _, err := requireCodexConfigSet(ctx, dbState.db, configSet.ID); err != nil {
			findings = append(findings, DoctorFinding{
				ID: "codex_config_set_invalid", Level: DoctorLevelError,
				Message: "Codex config set payload is invalid", Details: map[string]any{"config_set_id": configSet.ID},
			})
		}
	}
	return findings
}

func RepairDoctorLock(ctx context.Context, req DoctorRepairLockRequest) (DoctorRepairLockResult, error) {
	if !req.Confirm {
		return DoctorRepairLockResult{}, NewError(ErrorConfirmationRequired, "doctor lock repair requires confirmation")
	}
	result, err := Doctor(ctx, DoctorRequest{ConfigDir: req.ConfigDir})
	if err != nil {
		return DoctorRepairLockResult{}, err
	}
	if !result.Lock.Repairable {
		return DoctorRepairLockResult{}, NewError(ErrorLockRepairUnsafe, "lock is not safe to repair").
			WithDetail("reason", result.Lock.Reason).
			WithDetail("path", result.Lock.Path)
	}
	if result.Lock.contentSHA256 == "" {
		return DoctorRepairLockResult{}, NewError(ErrorLockRepairUnsafe, "lock content hash is unavailable").
			WithDetail("path", result.Lock.Path)
	}
	if err := targetfs.RemoveStaleLockFile(result.Lock.Path, result.Lock.contentSHA256); err != nil {
		return DoctorRepairLockResult{}, lockRepairUnsafeError(result.Lock.Path, err)
	}
	return DoctorRepairLockResult{
		Path:     result.Lock.Path,
		Repaired: true,
		Reason:   result.Lock.Reason,
	}, nil
}

func inspectDoctorDatabase(ctx context.Context, databasePath string) (doctorDatabaseState, []store.Operation, []DoctorFinding) {
	if _, err := os.Stat(databasePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return doctorDatabaseState{}, nil, []DoctorFinding{{
				ID:      "database_not_initialized",
				Level:   DoctorLevelWarning,
				Message: "application database is not initialized",
			}}
		}
		return doctorDatabaseState{}, nil, []DoctorFinding{{
			ID:      "database_inspect_failed",
			Level:   DoctorLevelError,
			Message: "failed to inspect application database",
			Details: map[string]any{"error": err.Error()},
		}}
	}

	db, err := store.Open(ctx, databasePath, true)
	if err != nil {
		return doctorDatabaseState{}, nil, []DoctorFinding{{
			ID:      "database_open_failed",
			Level:   DoctorLevelError,
			Message: "failed to open application database",
			Details: map[string]any{"error": err.Error()},
		}}
	}

	status, err := db.Status(ctx)
	if err != nil {
		_ = db.Close()
		return doctorDatabaseState{}, nil, []DoctorFinding{{
			ID:      "database_status_failed",
			Level:   DoctorLevelError,
			Message: "failed to inspect application database",
			Details: map[string]any{"error": err.Error()},
		}}
	}
	if !status.SchemaHealthy {
		_ = db.Close()
		return doctorDatabaseState{}, nil, []DoctorFinding{{
			ID:      "database_schema_unhealthy",
			Level:   DoctorLevelError,
			Message: "application database schema is not healthy",
		}}
	}

	operations, err := db.ListIncompleteOperations(ctx)
	if err != nil {
		_ = db.Close()
		return doctorDatabaseState{}, nil, []DoctorFinding{{
			ID:      "operation_list_failed",
			Level:   DoctorLevelError,
			Message: "failed to list incomplete operations",
			Details: map[string]any{"error": err.Error()},
		}}
	}

	return doctorDatabaseState{db: db, healthy: true}, operations, []DoctorFinding{{
		ID:      "database_healthy",
		Level:   DoctorLevelOK,
		Message: "application database is healthy",
	}}
}

func inspectSensitivePathPermissions(ctx context.Context, paths runtime.Paths, dbState doctorDatabaseState) []DoctorFinding {
	if goruntime.GOOS == "windows" {
		return nil
	}
	findings := []DoctorFinding{}
	findings = append(findings, inspectPathPermission(paths.Database, 0o600, "database_permissions_weak", "application database file permissions are wider than 0600")...)
	findings = append(findings, inspectPathPermission(paths.Backups, 0o700, "backups_permissions_weak", "backup directory permissions are wider than 0700")...)

	if !dbState.healthy || dbState.db == nil {
		return findings
	}
	targets, err := allStoredCodexBindingTargets(ctx, dbState.db)
	if err != nil {
		findings = append(findings, DoctorFinding{
			ID:      "codex_auth_target_permission_check_failed",
			Level:   DoctorLevelWarning,
			Message: "failed to inspect Codex auth target permissions",
			Details: map[string]any{"error": err.Error()},
		})
		return findings
	}
	seen := map[string]struct{}{}
	for _, target := range targets {
		if target.TargetID != codexconfig.AuthTargetID || target.Path == "" {
			continue
		}
		if _, ok := seen[target.Path]; ok {
			continue
		}
		seen[target.Path] = struct{}{}
		findings = append(findings, inspectPathPermission(target.Path, 0o600, "codex_auth_target_permissions_weak", "Codex auth target file permissions are wider than 0600")...)
	}
	return findings
}

func inspectPathPermission(path string, want os.FileMode, id, message string) []DoctorFinding {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return []DoctorFinding{{
			ID:      id + "_inspect_failed",
			Level:   DoctorLevelWarning,
			Message: "failed to inspect sensitive path permissions",
			Details: map[string]any{"path": path, "error": err.Error()},
		}}
	}
	if info.Mode().Perm() == want {
		return nil
	}
	return []DoctorFinding{{
		ID:      id,
		Level:   DoctorLevelWarning,
		Message: message,
		Details: map[string]any{
			"path": path,
			"mode": fileModeString(info.Mode()),
			"want": fileModeString(want),
		},
	}}
}

func inspectDoctorLock(ctx context.Context, lockPath string, dbState doctorDatabaseState) DoctorLock {
	lock := DoctorLock{
		Path:        lockPath,
		PIDState:    doctorPIDStateUnavailable,
		OSLockState: doctorOSLockStateUnavailable,
		Level:       DoctorLevelOK,
		Reason:      "lock_file_missing",
	}

	raw, err := os.ReadFile(lockPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return lock
		}
		lock.Exists = true
		lock.Level = DoctorLevelWarning
		lock.Reason = "lock_file_read_failed"
		return lock
	}
	lock.Exists = true
	sum := sha256.Sum256(raw)
	lock.contentSHA256 = hex.EncodeToString(sum[:])

	parseErr := populateDoctorLockFields(&lock, string(raw))
	if lock.PID > 0 {
		lock.PIDState = inspectProcess(lock.PID)
	} else {
		lock.PIDState = doctorPIDStateUnknown
	}

	probe, probeErr := targetfs.ProbeLock(lockPath)
	switch {
	case probeErr != nil:
		lock.OSLockState = doctorOSLockStateUnknown
	case probe.Unsupported:
		lock.OSLockState = doctorOSLockStateUnavailable
	case probe.Exists && probe.Held:
		lock.OSLockState = doctorOSLockStateHeld
	case probe.Exists:
		lock.OSLockState = doctorOSLockStateFree
	default:
		lock.OSLockState = doctorOSLockStateUnknown
	}

	if lock.OperationID != "" && dbState.healthy {
		operation, err := dbState.db.GetOperation(ctx, lock.OperationID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				lock.OperationStatus = "missing"
			} else {
				lock.OperationStatus = "unknown"
			}
		} else {
			lock.OperationStatus = operation.Status
		}
	}

	classifyDoctorLock(&lock, parseErr, probeErr, dbState.healthy)
	return lock
}

func populateDoctorLockFields(lock *DoctorLock, raw string) error {
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return errors.New("missing owner")
	}
	lock.Owner = strings.TrimSpace(lines[0])
	if isProfileDeckOperationID(lock.Owner) {
		lock.OperationID = lock.Owner
	}

	var pidParsed bool
	var createdParsed bool
	for _, line := range lines[1:] {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch key {
		case "pid":
			pid, err := strconv.Atoi(value)
			if err != nil {
				return err
			}
			lock.PID = pid
			pidParsed = true
		case "created_at_unix_ms":
			created, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return err
			}
			lock.CreatedAtUnixMS = created
			createdParsed = true
		}
	}
	if !pidParsed {
		return errors.New("missing pid")
	}
	if !createdParsed {
		return errors.New("missing created_at_unix_ms")
	}
	return nil
}

func classifyDoctorLock(lock *DoctorLock, parseErr, probeErr error, dbHealthy bool) {
	if probeErr != nil {
		lock.Level = DoctorLevelWarning
		lock.Reason = "lock_probe_failed"
		return
	}
	if lock.OSLockState == doctorOSLockStateHeld {
		lock.Level = DoctorLevelWarning
		lock.Reason = "lock_may_be_active"
		return
	}
	// The OS lock is the cross-process safety primitive; a free lock can
	// outweigh a stale diagnostic PID that has been reused by the OS.
	if lock.OSLockState != doctorOSLockStateFree {
		lock.Level = DoctorLevelWarning
		if lock.PIDState == doctorPIDStateAlive {
			lock.Reason = "lock_may_be_active"
		} else {
			lock.Reason = "os_lock_not_free"
		}
		return
	}
	if !dbHealthy {
		lock.Level = DoctorLevelWarning
		lock.Reason = "database_unavailable"
		return
	}
	if parseErr != nil {
		lock.Level = DoctorLevelWarning
		lock.Reason = "malformed_lock_file"
		lock.StaleCandidate = true
		lock.Repairable = true
		return
	}
	if lock.OperationID == "" {
		lock.Level = DoctorLevelWarning
		lock.Reason = "owner_not_profiledeck_operation"
		return
	}
	if lock.OperationStatus == "missing" && isMaintenanceLockOwner(lock.Owner) {
		// Maintenance owners serialize database or tool-owned refresh work but do
		// not need switch recovery once their OS lock is free.
		lock.Level = DoctorLevelOK
		lock.Reason = "maintenance_lock_residue"
		lock.Repairable = true
		return
	}
	switch lock.OperationStatus {
	case store.OperationStatusFailed, "missing":
		lock.Level = DoctorLevelError
		lock.Reason = "stale_lock_candidate"
		lock.StaleCandidate = true
		lock.Repairable = true
	case store.OperationStatusPending:
		lock.Level = DoctorLevelWarning
		lock.Reason = "pending_operation"
	case store.OperationStatusApplied:
		lock.Level = DoctorLevelOK
		lock.Reason = "applied_operation_lock_residue"
		lock.Repairable = true
	default:
		lock.Level = DoctorLevelWarning
		lock.Reason = "operation_status_unknown"
	}
}

func doctorOperations(ctx context.Context, dbState doctorDatabaseState, paths runtime.Paths, operations []store.Operation, lock DoctorLock) []DoctorOperation {
	result := make([]DoctorOperation, 0, len(operations))
	for _, operation := range operations {
		result = append(result, doctorOperation(ctx, dbState, paths, operation, lock))
	}
	return result
}

func doctorOperation(ctx context.Context, dbState doctorDatabaseState, paths runtime.Paths, operation store.Operation, lock DoctorLock) DoctorOperation {
	metadata := parseDoctorOperationMetadata(operation.MetadataJSON)
	profileID := metadata.ProfileID
	if profileID == "" {
		profileID = operation.ProfileID
	}
	backupPath := metadata.BackupPath
	if backupPath == "" {
		backupPath = metadata.CurrentBackupPath
	}

	result := DoctorOperation{
		ID:                operation.ID,
		OperationType:     operation.OperationType,
		Status:            operation.Status,
		Checkpoint:        metadata.Checkpoint,
		ProviderID:        metadata.ProviderID,
		ProfileID:         profileID,
		BackupPath:        backupPath,
		BackupID:          metadata.BackupID,
		SourceOperationID: metadata.SourceOperationID,
		RollbackKind:      metadata.RollbackKind,
		ErrorCode:         operation.ErrorCode,
		ErrorMessage:      redactSensitiveText(operation.ErrorMessage),
		UpdatedAtUnixMS:   operation.UpdatedAtUnixMS,
	}

	switch operation.Status {
	case store.OperationStatusFailed:
		result.Level = DoctorLevelError
		result.Reason = "failed_operation"
	case store.OperationStatusPending:
		if lock.OperationID == operation.ID && doctorLockMayBeActive(lock) {
			result.Level = DoctorLevelWarning
			result.Reason = "operation_may_be_in_progress"
		} else {
			result.Level = DoctorLevelError
			result.Reason = "pending_operation_without_active_lock"
		}
	default:
		result.Level = DoctorLevelWarning
		result.Reason = "unexpected_operation_status"
	}
	if metadata.metadataDecodeFail {
		result.Checkpoint = ""
		result.ProviderID = ""
		result.BackupPath = ""
		result.BackupID = ""
		result.SourceOperationID = ""
		result.RollbackKind = ""
		result.Reason = result.Reason + "_metadata_invalid"
	}
	if operation.OperationType == store.OperationTypeSwitch && operation.Status == store.OperationStatusFailed && !metadata.metadataDecodeFail && dbState.healthy && dbState.db != nil {
		inspection := inspectFailedSwitchRecovery(ctx, dbState.db, paths, operation)
		result.RecoveryStatus = inspection.Status
		result.RecoveryReason = inspection.Reason
	}
	return result
}

func doctorLockMayBeActive(lock DoctorLock) bool {
	if lock.OSLockState == doctorOSLockStateHeld {
		return true
	}
	if lock.OSLockState == doctorOSLockStateFree {
		return false
	}
	return lock.PIDState == doctorPIDStateAlive
}

func parseDoctorOperationMetadata(raw string) doctorOperationMetadata {
	var decoded map[string]any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return doctorOperationMetadata{metadataDecodeFail: true}
	}
	return doctorOperationMetadata{
		Checkpoint:        stringMapValue(decoded, "checkpoint"),
		ProviderID:        stringMapValue(decoded, "provider_id"),
		ProfileID:         stringMapValue(decoded, "profile_id"),
		BackupPath:        stringMapValue(decoded, "backup_path"),
		CurrentBackupPath: stringMapValue(decoded, "current_backup_path"),
		SourceOperationID: stringMapValue(decoded, "source_operation_id"),
		BackupID:          stringMapValue(decoded, "backup_id"),
		RollbackKind:      stringMapValue(decoded, "rollback_kind"),
	}
}

func stringMapValue(value map[string]any, key string) string {
	raw, ok := value[key]
	if !ok {
		return ""
	}
	text, ok := raw.(string)
	if !ok {
		return ""
	}
	return text
}

func isProfileDeckOperationID(value string) bool {
	for _, prefix := range profileDeckOperationIDPrefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func isMaintenanceLockOwner(value string) bool {
	for _, prefix := range maintenanceLockOwnerPrefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func doctorOverallLevel(result DoctorResult) string {
	level := DoctorLevelOK
	for _, finding := range result.Findings {
		level = maxDoctorLevel(level, finding.Level)
	}
	for _, operation := range result.Operations {
		level = maxDoctorLevel(level, operation.Level)
	}
	level = maxDoctorLevel(level, result.Lock.Level)
	return level
}

func maxDoctorLevel(left, right string) string {
	if doctorLevelRank(right) > doctorLevelRank(left) {
		return right
	}
	return left
}

func doctorLevelRank(level string) int {
	switch level {
	case DoctorLevelError:
		return 2
	case DoctorLevelWarning:
		return 1
	default:
		return 0
	}
}

func lockRepairUnsafeError(path string, err error) *AppError {
	var targetErr *targetfs.Error
	if errors.As(err, &targetErr) {
		appErr := WrapError(ErrorLockRepairUnsafe, targetErr.Message, err).WithDetail("path", path)
		for key, value := range targetErr.Details {
			appErr = appErr.WithDetail(key, value)
		}
		return appErr
	}
	return WrapError(ErrorLockRepairUnsafe, "failed to repair lock safely", err).WithDetail("path", path)
}
