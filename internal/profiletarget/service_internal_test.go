package profiletarget

import (
	"errors"
	"testing"

	"github.com/strahe/profiledeck/internal/apperror"
	"github.com/strahe/profiledeck/internal/store"
)

func TestMapTargetStoreErrorDistinguishesPathOwnership(t *testing.T) {
	err := mapTargetStoreError(store.ErrPathOwned)
	var appErr *apperror.Error
	if !errors.As(err, &appErr) {
		t.Fatalf("expected path ownership store error to map to apperror.Error, got %T: %v", err, err)
	}
	if appErr.Code != apperror.TargetAlreadyExists || appErr.Message != "target path is already owned by another profile target" {
		t.Fatalf("unexpected path ownership app error: %#v", appErr)
	}
}
