package app

import "fmt"

type ErrorCode string

const (
	ErrorInvalidRuntimePath   ErrorCode = "INVALID_RUNTIME_PATH"
	ErrorCommandFailed        ErrorCode = "COMMAND_FAILED"
	ErrorRuntimeInitFailed    ErrorCode = "RUNTIME_INIT_FAILED"
	ErrorStoreInitFailed      ErrorCode = "STORE_INIT_FAILED"
	ErrorStoreOpenFailed      ErrorCode = "STORE_OPEN_FAILED"
	ErrorStoreMigrationFailed ErrorCode = "STORE_MIGRATION_FAILED"
	ErrorStoreSchemaInvalid   ErrorCode = "STORE_SCHEMA_INVALID"
	ErrorStoreStatusFailed    ErrorCode = "STORE_STATUS_FAILED"
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
