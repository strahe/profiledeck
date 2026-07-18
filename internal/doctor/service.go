package doctor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	goruntime "runtime"
	"strconv"
	"strings"

	"github.com/strahe/profiledeck/internal/agent"
	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/profiletarget"
	"github.com/strahe/profiledeck/internal/runtime"
	"github.com/strahe/profiledeck/internal/store"
	"github.com/strahe/profiledeck/internal/targetfs"
)

const (
	doctorPIDStateAlive       = "alive"
	doctorPIDStateDead        = "dead"
	doctorPIDStateUnknown     = "unknown"
	doctorPIDStateUnavailable = "unavailable"

	doctorOSLockStateHeld        = "held"
	doctorOSLockStateFree        = "free"
	doctorOSLockStateUnknown     = "unknown"
	doctorOSLockStateUnavailable = "unavailable"
)

type DoctorResult struct {
	ConfigDir    string            `json:"config_dir"`
	RuntimeRoot  string            `json:"runtime_root"`
	DatabasePath string            `json:"database_path"`
	OverallLevel string            `json:"overall_level"`
	Findings     []Finding         `json:"findings"`
	Operations   []DoctorOperation `json:"operations"`
	Lock         DoctorLock        `json:"lock"`
}

type DoctorOperation struct {
	ID              string `json:"id"`
	OperationType   string `json:"operation_type"`
	Status          string `json:"status"`
	Checkpoint      string `json:"checkpoint,omitempty"`
	ProviderID      string `json:"provider_id,omitempty"`
	ProfileID       string `json:"profile_id,omitempty"`
	RecoveryStatus  string `json:"recovery_status,omitempty"`
	RecoveryAction  string `json:"recovery_action,omitempty"`
	RecoveryReason  string `json:"recovery_reason,omitempty"`
	ErrorCode       string `json:"error_code,omitempty"`
	ErrorMessage    string `json:"error_message,omitempty"`
	UpdatedAtUnixMS int64  `json:"updated_at_unix_ms"`
	Level           string `json:"level"`
	Reason          string `json:"reason"`
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

type DoctorRepairLockResult struct {
	Path     string `json:"path"`
	Repaired bool   `json:"repaired"`
	Reason   string `json:"reason"`
}

// ProviderCheck binds one provider health check to its owning Agent. Disabled
// Desktop Agents are skipped while generic database and recovery checks remain.
type ProviderCheck struct {
	AgentID agent.ID
	Check   func(context.Context, *store.Store) ([]Finding, error)
}

// RecoveryInspector evaluates an unresolved switch without mutating state.
type RecoveryInspector func(context.Context, *store.Store, runtime.Paths, store.Operation) (status, action, reason string)

// SensitivePathLister returns compiled-in Provider working paths that remain
// safety-relevant even when their Desktop Agent is disabled.
type SensitivePathLister func(context.Context, *store.Store) ([]string, error)

type Service struct {
	runtime           *runtime.Service
	policy            agent.Policy
	providerChecks    []ProviderCheck
	recoveryInspector RecoveryInspector
	sensitivePaths    SensitivePathLister
}

func NewService(
	runtimeService *runtime.Service,
	policy agent.Policy,
	providerChecks []ProviderCheck,
	recoveryInspector RecoveryInspector,
	sensitivePaths SensitivePathLister,
) *Service {
	checks := append([]ProviderCheck(nil), providerChecks...)
	return &Service{
		runtime: runtimeService, policy: policy, providerChecks: checks,
		recoveryInspector: recoveryInspector, sensitivePaths: sensitivePaths,
	}
}

func (service *Service) runProviderChecks(ctx context.Context, dbState doctorDatabaseState) ([]Finding, error) {
	if !dbState.healthy || dbState.db == nil {
		return nil, nil
	}
	findings := []Finding{}
	for _, check := range service.providerChecks {
		if check.Check == nil {
			continue
		}
		if service.policy != nil {
			var err error
			if policy, ok := service.policy.(agent.StorePolicy); ok {
				err = policy.RequireAgentWithStore(ctx, dbState.db, check.AgentID)
			} else {
				err = service.policy.RequireAgent(ctx, check.AgentID)
			}
			if err != nil {
				var appErr *apperror.Error
				if errors.As(err, &appErr) && appErr.Code == apperror.AgentDisabled {
					continue
				}
				return nil, err
			}
		}
		result, err := check.Check(ctx, dbState.db)
		if err != nil {
			return nil, err
		}
		findings = append(findings, result...)
	}
	return findings, nil
}

type doctorDatabaseState struct {
	db      *store.Store
	healthy bool
}

type doctorOperationMetadata struct {
	Checkpoint         string
	ProviderID         string
	ProfileID          string
	metadataDecodeFail bool
}

func (service *Service) Run(ctx context.Context) (DoctorResult, error) {
	paths := service.runtime.Paths()
	result := DoctorResult{
		ConfigDir:    service.runtime.ConfigDir(),
		RuntimeRoot:  paths.Root,
		DatabasePath: paths.Database,
		OverallLevel: LevelOK,
		Findings:     []Finding{},
		Operations:   []DoctorOperation{},
	}

	dbState, operations, dbFindings := inspectDoctorDatabase(ctx, service.runtime.StoreFactory())
	result.Findings = append(result.Findings, dbFindings...)
	if dbState.db != nil {
		defer dbState.db.Close()
	}
	result.Findings = append(result.Findings, service.inspectSensitivePathPermissions(ctx, paths, dbState)...)
	providerFindings, err := service.runProviderChecks(ctx, dbState)
	if err != nil {
		return DoctorResult{}, apperror.Wrap(apperror.StoreStatusFailed, "failed to run provider health checks", err)
	}
	result.Findings = append(result.Findings, providerFindings...)

	result.Lock = inspectDoctorLock(ctx, paths.Lock, dbState)
	result.Operations = service.doctorOperations(ctx, dbState, paths, operations, result.Lock)
	result.OverallLevel = doctorOverallLevel(result)
	return result, nil
}

func (service *Service) RepairLock(ctx context.Context, confirm bool) (DoctorRepairLockResult, error) {
	if !confirm {
		return DoctorRepairLockResult{}, apperror.New(apperror.ConfirmationRequired, "doctor lock repair requires confirmation")
	}
	result, err := service.Run(ctx)
	if err != nil {
		return DoctorRepairLockResult{}, err
	}
	if !result.Lock.Repairable {
		return DoctorRepairLockResult{}, apperror.New(apperror.LockRepairUnsafe, "lock is not safe to repair").
			WithDetail("reason", result.Lock.Reason).
			WithDetail("path", result.Lock.Path)
	}
	if result.Lock.contentSHA256 == "" {
		return DoctorRepairLockResult{}, apperror.New(apperror.LockRepairUnsafe, "lock content hash is unavailable").
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

func inspectDoctorDatabase(ctx context.Context, stores store.Factory) (doctorDatabaseState, []store.Operation, []Finding) {
	databasePath := stores.DatabasePath()
	if _, err := os.Stat(databasePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return doctorDatabaseState{}, nil, []Finding{{
				ID:      "database_not_initialized",
				Level:   LevelWarning,
				Message: "application database is not initialized",
			}}
		}
		return doctorDatabaseState{}, nil, []Finding{{
			ID:      "database_inspect_failed",
			Level:   LevelError,
			Message: "failed to inspect application database",
			Details: map[string]any{"error": err.Error()},
		}}
	}

	db, err := stores.Open(ctx, true)
	if err != nil {
		return doctorDatabaseState{}, nil, []Finding{{
			ID:      "database_open_failed",
			Level:   LevelError,
			Message: "failed to open application database",
			Details: map[string]any{"error": err.Error()},
		}}
	}

	report, err := db.InspectIntegrity(ctx, store.IntegrityAppliedBaseline)
	if err != nil {
		_ = db.Close()
		if errors.Is(err, store.ErrUnsupportedSchema) {
			return doctorDatabaseState{}, nil, []Finding{{
				ID:      "database_schema_unsupported",
				Level:   LevelError,
				Message: "this ProfileDeck version cannot open the existing local data; update ProfileDeck and try again",
			}}
		}
		if errors.Is(err, store.ErrInvalidMigrationHistory) {
			return doctorDatabaseState{}, nil, []Finding{{
				ID:      "database_schema_unhealthy",
				Level:   LevelError,
				Message: "application database structure is invalid",
			}}
		}
		return doctorDatabaseState{}, nil, []Finding{{
			ID:      "database_status_failed",
			Level:   LevelError,
			Message: "failed to inspect application database",
			Details: map[string]any{"error": err.Error()},
		}}
	}
	if !report.Healthy {
		_ = db.Close()
		return doctorDatabaseState{}, nil, integrityFindings(report)
	}
	if !report.Migration.Current {
		_ = db.Close()
		return doctorDatabaseState{}, nil, []Finding{{
			ID:      "database_upgrade_required",
			Level:   LevelWarning,
			Message: "ProfileDeck local data must be updated before other database checks can run",
		}}
	}
	report, err = db.InspectIntegrity(ctx, store.IntegrityCurrentBaseline)
	if err != nil || !report.Healthy {
		_ = db.Close()
		if err != nil {
			return doctorDatabaseState{}, nil, []Finding{{
				ID:      "database_status_failed",
				Level:   LevelError,
				Message: "failed to inspect application database",
			}}
		}
		return doctorDatabaseState{}, nil, integrityFindings(report)
	}

	operations, err := db.ListIncompleteOperations(ctx)
	if err != nil {
		_ = db.Close()
		return doctorDatabaseState{}, nil, []Finding{{
			ID:      "operation_list_failed",
			Level:   LevelError,
			Message: "failed to list incomplete operations",
			Details: map[string]any{"error": err.Error()},
		}}
	}

	return doctorDatabaseState{db: db, healthy: true}, operations, []Finding{{
		ID:      "database_healthy",
		Level:   LevelOK,
		Message: "application database is healthy",
	}}
}

func integrityFindings(report store.IntegrityReport) []Finding {
	findings := make([]Finding, 0, len(report.Issues))
	for _, issue := range report.Issues {
		finding := Finding{Level: LevelError, Details: map[string]any{"count": issue.Count}}
		switch issue.Kind {
		case store.IntegrityIssueQuickCheck:
			finding.ID = "database_quick_check_failed"
			finding.Message = "local application data is damaged"
		case store.IntegrityIssueForeignKeys:
			finding.ID = "database_foreign_key_check_failed"
			finding.Message = "local application data contains broken relationships"
		case store.IntegrityIssueSchema:
			finding.ID = "database_schema_unhealthy"
			finding.Message = "application database structure is not healthy"
		case store.IntegrityIssueJSON:
			finding.ID = "database_json_invalid"
			finding.Message = "local application data contains invalid structured values"
		case store.IntegrityIssueReferences:
			finding.ID = "database_references_invalid"
			finding.Message = "local application data contains invalid references"
		default:
			continue
		}
		findings = append(findings, finding)
	}
	if len(findings) == 0 {
		return []Finding{{
			ID: "database_schema_unhealthy", Level: LevelError,
			Message: "application database integrity could not be verified",
		}}
	}
	return findings
}

func (service *Service) inspectSensitivePathPermissions(ctx context.Context, paths runtime.Paths, dbState doctorDatabaseState) []Finding {
	if goruntime.GOOS == "windows" {
		return nil
	}
	findings := []Finding{}
	findings = append(findings, inspectPathPermission(paths.Database, 0o600, "database_permissions_weak", "application database file permissions are wider than 0600")...)
	findings = append(findings, inspectPathPermission(paths.Backups, 0o700, "backups_permissions_weak", "backup directory permissions are wider than 0700")...)

	if !dbState.healthy || dbState.db == nil {
		return findings
	}
	if service.sensitivePaths == nil {
		return findings
	}
	pathsToInspect, err := service.sensitivePaths(ctx, dbState.db)
	if err != nil {
		findings = append(findings, Finding{
			ID:      "codex_auth_target_permission_check_failed",
			Level:   LevelWarning,
			Message: "failed to inspect Codex auth target permissions",
			Details: map[string]any{"error": err.Error()},
		})
		return findings
	}
	seen := map[string]struct{}{}
	for _, path := range pathsToInspect {
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		findings = append(findings, inspectPathPermission(path, 0o600, "codex_auth_target_permissions_weak", "Codex auth target file permissions are wider than 0600")...)
	}
	return findings
}

func inspectPathPermission(path string, want os.FileMode, id, message string) []Finding {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return []Finding{{
			ID:      id + "_inspect_failed",
			Level:   LevelWarning,
			Message: "failed to inspect sensitive path permissions",
			Details: map[string]any{"path": path, "error": err.Error()},
		}}
	}
	if info.Mode().Perm() == want {
		return nil
	}
	return []Finding{{
		ID:      id,
		Level:   LevelWarning,
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
		Level:       LevelOK,
		Reason:      "lock_file_missing",
	}

	raw, err := os.ReadFile(lockPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return lock
		}
		lock.Exists = true
		lock.Level = LevelWarning
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
		lock.Level = LevelWarning
		lock.Reason = "lock_probe_failed"
		return
	}
	if lock.OSLockState == doctorOSLockStateHeld {
		lock.Level = LevelWarning
		lock.Reason = "lock_may_be_active"
		return
	}
	// The OS lock is the cross-process safety primitive; a free lock can
	// outweigh a stale diagnostic PID that has been reused by the OS.
	if lock.OSLockState != doctorOSLockStateFree {
		lock.Level = LevelWarning
		if lock.PIDState == doctorPIDStateAlive {
			lock.Reason = "lock_may_be_active"
		} else {
			lock.Reason = "os_lock_not_free"
		}
		return
	}
	if !dbHealthy {
		lock.Level = LevelWarning
		lock.Reason = "database_unavailable"
		return
	}
	if parseErr != nil {
		lock.Level = LevelWarning
		lock.Reason = "malformed_lock_file"
		lock.StaleCandidate = true
		lock.Repairable = true
		return
	}
	if lock.OperationID == "" {
		lock.Level = LevelWarning
		lock.Reason = "owner_not_profiledeck_operation"
		return
	}
	if lock.OperationStatus == "missing" && isMaintenanceLockOwner(lock.Owner) {
		// Maintenance owners serialize database or tool-owned refresh work but do
		// not need switch recovery once their OS lock is free.
		lock.Level = LevelOK
		lock.Reason = "maintenance_lock_residue"
		lock.Repairable = true
		return
	}
	switch lock.OperationStatus {
	case store.OperationStatusFailed, "missing":
		lock.Level = LevelError
		lock.Reason = "stale_lock_candidate"
		lock.StaleCandidate = true
		lock.Repairable = true
	case store.OperationStatusPending:
		lock.Level = LevelWarning
		lock.Reason = "pending_operation"
	case store.OperationStatusApplied:
		lock.Level = LevelOK
		lock.Reason = "applied_operation_lock_residue"
		lock.Repairable = true
	default:
		lock.Level = LevelWarning
		lock.Reason = "operation_status_unknown"
	}
}

func (service *Service) doctorOperations(ctx context.Context, dbState doctorDatabaseState, paths runtime.Paths, operations []store.Operation, lock DoctorLock) []DoctorOperation {
	result := make([]DoctorOperation, 0, len(operations))
	for _, operation := range operations {
		if operation.OperationType != store.OperationTypeSwitch {
			continue
		}
		result = append(result, service.doctorOperation(ctx, dbState, paths, operation, lock))
	}
	return result
}

func (service *Service) doctorOperation(ctx context.Context, dbState doctorDatabaseState, paths runtime.Paths, operation store.Operation, lock DoctorLock) DoctorOperation {
	metadata := parseDoctorOperationMetadata(operation.MetadataJSON)
	profileID := metadata.ProfileID
	if profileID == "" {
		profileID = operation.ProfileID
	}
	result := DoctorOperation{
		ID: operation.ID, OperationType: operation.OperationType, Status: operation.Status,
		Checkpoint: metadata.Checkpoint, ProviderID: metadata.ProviderID, ProfileID: profileID,
		ErrorCode: operation.ErrorCode, ErrorMessage: profiletarget.RedactSensitiveText(operation.ErrorMessage),
		UpdatedAtUnixMS: operation.UpdatedAtUnixMS,
	}

	switch operation.Status {
	case store.OperationStatusFailed:
		result.Level = LevelError
		result.Reason = "failed_operation"
	case store.OperationStatusPending:
		if lock.OperationID == operation.ID && doctorLockMayBeActive(lock) {
			result.Level = LevelWarning
			result.Reason = "operation_may_be_in_progress"
		} else {
			result.Level = LevelError
			result.Reason = "pending_operation_without_active_lock"
		}
	default:
		result.Level = LevelWarning
		result.Reason = "unexpected_operation_status"
	}
	if metadata.metadataDecodeFail {
		result.Checkpoint = ""
		result.ProviderID = ""
		result.Reason = result.Reason + "_metadata_invalid"
	}
	if operation.OperationType == store.OperationTypeSwitch && !metadata.metadataDecodeFail && dbState.healthy && dbState.db != nil && service.recoveryInspector != nil {
		result.RecoveryStatus, result.RecoveryAction, result.RecoveryReason = service.recoveryInspector(ctx, dbState.db, paths, operation)
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
		Checkpoint: stringMapValue(decoded, "checkpoint"),
		ProviderID: stringMapValue(decoded, "provider_id"),
		ProfileID:  stringMapValue(decoded, "profile_id"),
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
	return strings.HasPrefix(value, "switch-") || strings.HasPrefix(value, "recovery-") || isGeneratedOperationID(value)
}

func isMaintenanceLockOwner(value string) bool {
	if strings.HasPrefix(value, "switch-") || strings.HasPrefix(value, "recovery-") {
		return false
	}
	return isGeneratedOperationID(value)
}

func isGeneratedOperationID(value string) bool {
	randomSeparator := strings.LastIndexByte(value, '-')
	if randomSeparator <= 0 || randomSeparator == len(value)-1 {
		return false
	}
	timestampSeparator := strings.LastIndexByte(value[:randomSeparator], '-')
	if timestampSeparator <= 0 || timestampSeparator == randomSeparator-1 {
		return false
	}
	random := value[randomSeparator+1:]
	if len(random) != 12 {
		return false
	}
	if _, err := hex.DecodeString(random); err != nil {
		return false
	}
	timestamp, err := strconv.ParseInt(value[timestampSeparator+1:randomSeparator], 10, 64)
	return err == nil && timestamp > 0
}

func doctorOverallLevel(result DoctorResult) string {
	levels := make([]string, 0, len(result.Findings)+len(result.Operations)+1)
	for _, finding := range result.Findings {
		levels = append(levels, finding.Level)
	}
	for _, operation := range result.Operations {
		levels = append(levels, operation.Level)
	}
	levels = append(levels, result.Lock.Level)
	return OverallLevel(levels...)
}

func lockRepairUnsafeError(path string, err error) *apperror.Error {
	var targetErr *targetfs.Error
	if errors.As(err, &targetErr) {
		appErr := apperror.Wrap(apperror.LockRepairUnsafe, targetErr.Message, err).WithDetail("path", path)
		for key, value := range targetErr.Details {
			appErr = appErr.WithDetail(key, value)
		}
		return appErr
	}
	return apperror.Wrap(apperror.LockRepairUnsafe, "failed to repair lock safely", err).WithDetail("path", path)
}

func fileModeString(mode os.FileMode) string {
	if mode == 0 {
		return ""
	}
	return fmt.Sprintf("%#o", mode.Perm())
}
