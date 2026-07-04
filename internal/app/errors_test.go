package app

import (
	"errors"
	"strings"
	"testing"
)

func TestAppErrorIncludesCodeAndMessage(t *testing.T) {
	err := NewError(ErrorInvalidRuntimePath, "runtime path is invalid")

	got := err.Error()
	if !strings.Contains(got, string(ErrorInvalidRuntimePath)) {
		t.Fatalf("expected error to contain code %q, got %q", ErrorInvalidRuntimePath, got)
	}
	if !strings.Contains(got, "runtime path is invalid") {
		t.Fatalf("expected error to contain message, got %q", got)
	}
}

func TestAppErrorWrapsCause(t *testing.T) {
	cause := errors.New("root cause")
	err := WrapError(ErrorCommandFailed, "command failed", cause)

	if !errors.Is(err, cause) {
		t.Fatalf("expected wrapped error to preserve cause")
	}
}

func TestAppErrorDetails(t *testing.T) {
	err := NewError(ErrorInvalidRuntimePath, "runtime path is invalid").WithDetail("path", "")

	if got := err.Details["path"]; got != "" {
		t.Fatalf("expected detail to be recorded, got %#v", got)
	}
}
