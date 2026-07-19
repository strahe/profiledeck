// Package apperror defines stable application error codes and structured errors.
package apperror

import (
	"errors"
	"fmt"
)

// Code identifies a recoverable application error category.
type Code string

const (
	InvalidRuntimePath               Code = "INVALID_RUNTIME_PATH"
	CommandFailed                    Code = "COMMAND_FAILED"
	RuntimeInitFailed                Code = "RUNTIME_INIT_FAILED"
	StoreInitFailed                  Code = "STORE_INIT_FAILED"
	StoreOpenFailed                  Code = "STORE_OPEN_FAILED"
	StoreMigrationFailed             Code = "STORE_MIGRATION_FAILED"
	StoreNotInitialized              Code = "STORE_NOT_INITIALIZED"
	StoreSchemaInvalid               Code = "STORE_SCHEMA_INVALID"
	StoreSchemaUnsupported           Code = "STORE_SCHEMA_UNSUPPORTED"
	StoreStatusFailed                Code = "STORE_STATUS_FAILED"
	ConfirmationRequired             Code = "CONFIRMATION_REQUIRED"
	OperationCreateFailed            Code = "OPERATION_CREATE_FAILED"
	OperationUpdateFailed            Code = "OPERATION_UPDATE_FAILED"
	OperationRecoveryCleanupRequired Code = "OPERATION_RECOVERY_CLEANUP_REQUIRED"
	ProviderAlreadyExists            Code = "PROVIDER_EXISTS"
	ProviderInUse                    Code = "PROVIDER_IN_USE"
	ProviderInvalid                  Code = "PROVIDER_INVALID"
	ProviderNotFound                 Code = "PROVIDER_NOT_FOUND"
	ProfileAlreadyExists             Code = "PROFILE_EXISTS"
	ProfileInUse                     Code = "PROFILE_IN_USE"
	ProfileInvalid                   Code = "PROFILE_INVALID"
	ProfileNotFound                  Code = "PROFILE_NOT_FOUND"
	PlanBuildFailed                  Code = "PLAN_BUILD_FAILED"
	AdapterNotFound                  Code = "ADAPTER_NOT_FOUND"
	AgentDisabled                    Code = "agent_disabled"
	ProviderDisabled                 Code = "PROVIDER_DISABLED"
	TargetInvalid                    Code = "TARGET_INVALID"
	TargetAlreadyExists              Code = "TARGET_EXISTS"
	TargetNotFound                   Code = "TARGET_NOT_FOUND"
	TargetReadFailed                 Code = "TARGET_READ_FAILED"
	LockAcquireFailed                Code = "LOCK_ACQUIRE_FAILED"
	SwitchPlanUnsupported            Code = "SWITCH_PLAN_UNSUPPORTED"
	TargetChanged                    Code = "TARGET_CHANGED"
	BackupFailed                     Code = "BACKUP_FAILED"
	BackupInvalid                    Code = "BACKUP_INVALID"
	BackupSchemaUnsupported          Code = "BACKUP_SCHEMA_UNSUPPORTED"
	BackupNotFound                   Code = "BACKUP_NOT_FOUND"
	RestoreFailed                    Code = "RESTORE_FAILED"
	ApplicationRestartFailed         Code = "APPLICATION_RESTART_FAILED"
	RecoveryUnsupported              Code = "RECOVERY_UNSUPPORTED"
	TargetWriteFailed                Code = "TARGET_WRITE_FAILED"
	LockRepairUnsafe                 Code = "LOCK_REPAIR_UNSAFE"
	UsageInvalid                     Code = "USAGE_INVALID"
	UsageImportFailed                Code = "USAGE_IMPORT_FAILED"
	CodexInvalid                     Code = "CODEX_INVALID"
	AntigravityInvalid               Code = "ANTIGRAVITY_INVALID"
	ClaudeCodeInvalid                Code = "CLAUDE_CODE_INVALID"
	SettingInvalid                   Code = "SETTING_INVALID"
	ExportFailed                     Code = "EXPORT_FAILED"
	ImportInvalid                    Code = "IMPORT_INVALID"
	ImportConflict                   Code = "IMPORT_CONFLICT"
	ImportPlanChanged                Code = "IMPORT_PLAN_CHANGED"
	UpdateChannelBusy                Code = "UPDATE_CHANNEL_BUSY"
	UpdateNotReady                   Code = "UPDATE_NOT_READY"
	UpdateRestartFailed              Code = "UPDATE_RESTART_FAILED"
)

const publicCommandFailedMessage = "ProfileDeck could not complete this command"

// Error carries a stable code, safe message, optional cause, and safe details.
type Error struct {
	Code    Code
	Message string
	Cause   error
	Details map[string]any
}

// New constructs an application error without a cause.
func New(code Code, message string) *Error {
	return &Error{Code: code, Message: message}
}

// Wrap constructs an application error that retains a cause for errors.Is/As.
func Wrap(code Code, message string, cause error) *Error {
	return &Error{Code: code, Message: message, Cause: cause}
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	// Causes can contain paths, secrets, or driver diagnostics. Keep them on the
	// unwrap chain and render only the explicitly safe application contract.
	if !KnownCode(e.Code) {
		return fmt.Sprintf("%s: %s", CommandFailed, publicCommandFailedMessage)
	}
	if e.Message == "" {
		return string(e.Code)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// Public normalizes an arbitrary error into the safe application contract.
// Details are deliberately excluded because each output boundary owns its
// explicit allowlist.
func Public(err error) *Error {
	if err == nil {
		return nil
	}
	var appErr *Error
	if errors.As(err, &appErr) && KnownCode(appErr.Code) {
		return Wrap(appErr.Code, appErr.Message, err)
	}
	return Wrap(CommandFailed, publicCommandFailedMessage, err)
}

// KnownCode reports whether code is part of ProfileDeck's stable error set.
func KnownCode(code Code) bool {
	switch code {
	case InvalidRuntimePath,
		CommandFailed,
		RuntimeInitFailed,
		StoreInitFailed,
		StoreOpenFailed,
		StoreMigrationFailed,
		StoreNotInitialized,
		StoreSchemaInvalid,
		StoreSchemaUnsupported,
		StoreStatusFailed,
		ConfirmationRequired,
		OperationCreateFailed,
		OperationUpdateFailed,
		OperationRecoveryCleanupRequired,
		ProviderAlreadyExists,
		ProviderInUse,
		ProviderInvalid,
		ProviderNotFound,
		ProfileAlreadyExists,
		ProfileInUse,
		ProfileInvalid,
		ProfileNotFound,
		PlanBuildFailed,
		AdapterNotFound,
		AgentDisabled,
		ProviderDisabled,
		TargetInvalid,
		TargetAlreadyExists,
		TargetNotFound,
		TargetReadFailed,
		LockAcquireFailed,
		SwitchPlanUnsupported,
		TargetChanged,
		BackupFailed,
		BackupInvalid,
		BackupSchemaUnsupported,
		BackupNotFound,
		RestoreFailed,
		ApplicationRestartFailed,
		RecoveryUnsupported,
		TargetWriteFailed,
		LockRepairUnsafe,
		UsageInvalid,
		UsageImportFailed,
		CodexInvalid,
		AntigravityInvalid,
		ClaudeCodeInvalid,
		SettingInvalid,
		ExportFailed,
		ImportInvalid,
		ImportConflict,
		ImportPlanChanged,
		UpdateChannelBusy,
		UpdateNotReady,
		UpdateRestartFailed:
		return true
	default:
		return false
	}
}

// WithDetail attaches safe diagnostic data to the error.
func (e *Error) WithDetail(key string, value any) *Error {
	if e.Details == nil {
		e.Details = make(map[string]any)
	}
	e.Details[key] = value
	return e
}
