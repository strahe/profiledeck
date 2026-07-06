package targetfs

import "fmt"

type Kind string

const (
	KindLockHeld      Kind = "lock_held"
	KindLockFailed    Kind = "lock_failed"
	KindTargetChanged Kind = "target_changed"
	KindUnsupported   Kind = "unsupported"
	KindBackupInvalid Kind = "backup_invalid"
	KindBackupFailed  Kind = "backup_failed"
	KindWriteFailed   Kind = "write_failed"
)

type Error struct {
	Kind    Kind
	Message string
	Cause   error
	Details map[string]any
}

func NewError(kind Kind, message string) *Error {
	return &Error{
		Kind:    kind,
		Message: message,
	}
}

func WrapError(kind Kind, message string, cause error) *Error {
	return &Error{
		Kind:    kind,
		Message: message,
		Cause:   cause,
	}
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	message := ""
	if e.Kind == "" {
		message = e.Message
	} else if e.Message == "" {
		message = string(e.Kind)
	} else {
		message = fmt.Sprintf("%s: %s", e.Kind, e.Message)
	}
	if e.Cause == nil {
		return message
	}
	if message == "" {
		return e.Cause.Error()
	}
	return fmt.Sprintf("%s: %v", message, e.Cause)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func (e *Error) WithDetail(key string, value any) *Error {
	if e.Details == nil {
		e.Details = make(map[string]any)
	}
	e.Details[key] = value
	return e
}
