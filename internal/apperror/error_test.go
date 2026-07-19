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

func TestAppErrorExcludesCauseFromMessage(t *testing.T) {
	cause := errors.New("permission denied SECRET_PATH")
	err := Wrap(RuntimeInitFailed, "failed to initialize runtime directories", cause)

	got := err.Error()
	for _, want := range []string{
		string(RuntimeInitFailed),
		"failed to initialize runtime directories",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected error to contain %q, got %q", want, got)
		}
	}
	if strings.Contains(got, "permission denied") || strings.Contains(got, "SECRET_PATH") {
		t.Fatalf("expected error to exclude its cause, got %q", got)
	}
	if !errors.Is(err, cause) {
		t.Fatal("expected safe rendering to preserve the cause chain")
	}
}

func TestPublicNormalizesUnknownAndUnstableErrors(t *testing.T) {
	for _, err := range []error{
		errors.New("open /private/secret: permission denied"),
		New(Code("PRIVATE_FAILURE"), "SECRET_MESSAGE"),
	} {
		publicErr := Public(err)
		if publicErr.Code != CommandFailed || strings.Contains(publicErr.Error(), "secret") || strings.Contains(publicErr.Error(), "SECRET") {
			t.Fatalf("unexpected public error: %#v", publicErr)
		}
		if !errors.Is(publicErr, err) {
			t.Fatal("expected public error to retain its private cause")
		}
	}
}

func TestPublicPreservesKnownCodeAndSafeMessageWithoutDetails(t *testing.T) {
	original := Wrap(TargetWriteFailed, "target could not be updated", errors.New("private cause")).
		WithDetail("path", "/private/target")

	publicErr := Public(original)
	if publicErr.Code != TargetWriteFailed || publicErr.Message != "target could not be updated" {
		t.Fatalf("unexpected public error: %#v", publicErr)
	}
	if len(publicErr.Details) != 0 {
		t.Fatalf("expected public normalization to omit details, got %#v", publicErr.Details)
	}
	if !errors.Is(publicErr, original) {
		t.Fatal("expected public error to retain the original error")
	}
}

func TestAppErrorDetails(t *testing.T) {
	err := New(InvalidRuntimePath, "runtime path is invalid").WithDetail("path", "")

	if got := err.Details["path"]; got != "" {
		t.Fatalf("expected detail to be recorded, got %#v", got)
	}
}
