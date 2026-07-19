package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/strahe/profiledeck/internal/apperror"
)

func TestWriteCommandErrorUsesSafeApplicationBoundary(t *testing.T) {
	private := "/private/SECRET_DATABASE"
	for _, err := range []error{
		errors.New("open " + private + ": permission denied"),
		apperror.Wrap(apperror.StoreOpenFailed, "local data could not be opened", errors.New(private)),
	} {
		var output bytes.Buffer
		writeCommandError(&output, err)
		if strings.Contains(output.String(), private) {
			t.Fatalf("expected CLI error to omit private cause, got %q", output.String())
		}
		if !strings.Contains(output.String(), "FAILED") {
			t.Fatalf("expected CLI error code, got %q", output.String())
		}
	}
}
