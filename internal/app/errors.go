package app

import "fmt"

type ErrorCode string

const (
	ErrorInvalidRuntimePath    ErrorCode = "INVALID_RUNTIME_PATH"
	ErrorCommandFailed         ErrorCode = "COMMAND_FAILED"
	ErrorRuntimeInitFailed     ErrorCode = "RUNTIME_INIT_FAILED"
	ErrorStoreInitFailed       ErrorCode = "STORE_INIT_FAILED"
	ErrorStoreOpenFailed       ErrorCode = "STORE_OPEN_FAILED"
	ErrorStoreMigrationFailed  ErrorCode = "STORE_MIGRATION_FAILED"
	ErrorStoreNotInitialized   ErrorCode = "STORE_NOT_INITIALIZED"
	ErrorStoreSchemaInvalid    ErrorCode = "STORE_SCHEMA_INVALID"
	ErrorStoreStatusFailed     ErrorCode = "STORE_STATUS_FAILED"
	ErrorConfirmationRequired  ErrorCode = "CONFIRMATION_REQUIRED"
	ErrorOperationCreateFailed ErrorCode = "OPERATION_CREATE_FAILED"
	ErrorOperationUpdateFailed ErrorCode = "OPERATION_UPDATE_FAILED"
	ErrorProviderAlreadyExists ErrorCode = "PROVIDER_EXISTS"
	ErrorProviderInUse         ErrorCode = "PROVIDER_IN_USE"
	ErrorProviderInvalid       ErrorCode = "PROVIDER_INVALID"
	ErrorProviderNotFound      ErrorCode = "PROVIDER_NOT_FOUND"
	ErrorProfileAlreadyExists  ErrorCode = "PROFILE_EXISTS"
	ErrorProfileInUse          ErrorCode = "PROFILE_IN_USE"
	ErrorProfileInvalid        ErrorCode = "PROFILE_INVALID"
	ErrorProfileNotFound       ErrorCode = "PROFILE_NOT_FOUND"
	ErrorPlanBuildFailed       ErrorCode = "PLAN_BUILD_FAILED"
	ErrorAdapterNotFound       ErrorCode = "ADAPTER_NOT_FOUND"
	ErrorProviderDisabled      ErrorCode = "PROVIDER_DISABLED"
	ErrorTargetInvalid         ErrorCode = "TARGET_INVALID"
	ErrorTargetAlreadyExists   ErrorCode = "TARGET_EXISTS"
	ErrorTargetNotFound        ErrorCode = "TARGET_NOT_FOUND"
	ErrorTargetReadFailed      ErrorCode = "TARGET_READ_FAILED"
	ErrorLockAcquireFailed     ErrorCode = "LOCK_ACQUIRE_FAILED"
	ErrorSwitchPlanUnsupported ErrorCode = "SWITCH_PLAN_UNSUPPORTED"
	ErrorTargetChanged         ErrorCode = "TARGET_CHANGED"
	ErrorBackupFailed          ErrorCode = "BACKUP_FAILED"
	ErrorBackupInvalid         ErrorCode = "BACKUP_INVALID"
	ErrorBackupNotFound        ErrorCode = "BACKUP_NOT_FOUND"
	ErrorRollbackUnsupported   ErrorCode = "ROLLBACK_UNSUPPORTED"
	ErrorRecoveryUnsupported   ErrorCode = "RECOVERY_UNSUPPORTED"
	ErrorTargetWriteFailed     ErrorCode = "TARGET_WRITE_FAILED"
	ErrorLockRepairUnsafe      ErrorCode = "LOCK_REPAIR_UNSAFE"
	ErrorUsageInvalid          ErrorCode = "USAGE_INVALID"
	ErrorUsageImportFailed     ErrorCode = "USAGE_IMPORT_FAILED"
	ErrorCodexInvalid          ErrorCode = "CODEX_INVALID"
	ErrorAntigravityInvalid    ErrorCode = "ANTIGRAVITY_INVALID"
	ErrorSettingInvalid        ErrorCode = "SETTING_INVALID"
	ErrorExportFailed          ErrorCode = "EXPORT_FAILED"
	ErrorImportInvalid         ErrorCode = "IMPORT_INVALID"
	ErrorImportConflict        ErrorCode = "IMPORT_CONFLICT"
	ErrorImportPlanChanged     ErrorCode = "IMPORT_PLAN_CHANGED"
)

type AppError struct {
	Code    ErrorCode
	Message string
	Cause   error
	Details map[string]any
}

func NewError(code ErrorCode, message string) *AppError {
	return &AppError{
		Code:    code,
		Message: message,
	}
}

func WrapError(code ErrorCode, message string, cause error) *AppError {
	return &AppError{
		Code:    code,
		Message: message,
		Cause:   cause,
	}
}

func (e *AppError) Error() string {
	if e == nil {
		return "<nil>"
	}
	message := ""
	if e.Code == "" {
		message = e.Message
	} else if e.Message == "" {
		message = string(e.Code)
	} else {
		message = fmt.Sprintf("%s: %s", e.Code, e.Message)
	}

	if e.Cause == nil {
		return message
	}
	if message == "" {
		return e.Cause.Error()
	}
	return fmt.Sprintf("%s: %v", message, e.Cause)
}

func (e *AppError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func (e *AppError) WithDetail(key string, value any) *AppError {
	if e.Details == nil {
		e.Details = make(map[string]any)
	}
	e.Details[key] = value
	return e
}
