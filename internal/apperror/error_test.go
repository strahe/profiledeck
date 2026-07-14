package apperror

import (
	"errors"
	"strings"
	"testing"
)

func TestAppErrorIncludesCodeAndMessage(t *testing.T) {
	err := New(InvalidRuntimePath, "runtime path is invalid")

	got := err.Error()
	if !strings.Contains(got, string(InvalidRuntimePath)) {
		t.Fatalf("expected error to contain code %q, got %q", InvalidRuntimePath, got)
	}
	if !strings.Contains(got, "runtime path is invalid") {
		t.Fatalf("expected error to contain message, got %q", got)
	}
}

func TestAppErrorWrapsCause(t *testing.T) {
	cause := errors.New("root cause")
	err := Wrap(CommandFailed, "command failed", cause)

	if !errors.Is(err, cause) {
		t.Fatalf("expected wrapped error to preserve cause")
	}
}

func TestAppErrorIncludesCauseInMessage(t *testing.T) {
	cause := errors.New("permission denied")
	err := Wrap(RuntimeInitFailed, "failed to initialize runtime directories", cause)

	got := err.Error()
	for _, want := range []string{
		string(RuntimeInitFailed),
		"failed to initialize runtime directories",
		"permission denied",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected error to contain %q, got %q", want, got)
		}
	}
}

func TestAppErrorDetails(t *testing.T) {
	err := New(InvalidRuntimePath, "runtime path is invalid").WithDetail("path", "")

	if got := err.Details["path"]; got != "" {
		t.Fatalf("expected detail to be recorded, got %#v", got)
	}
}
