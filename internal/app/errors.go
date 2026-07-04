package app

import "fmt"

type ErrorCode string

const (
	ErrorInvalidRuntimePath ErrorCode = "INVALID_RUNTIME_PATH"
	ErrorCommandFailed      ErrorCode = "COMMAND_FAILED"
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
	if e.Code == "" {
		return e.Message
	}
	if e.Message == "" {
		return string(e.Code)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
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
