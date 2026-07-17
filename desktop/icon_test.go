package main

import (
	"bytes"
	"image/png"
	"testing"
)

func TestApplicationIconIsValidSquarePNG(t *testing.T) {
	t.Parallel()
	icon, err := png.Decode(bytes.NewReader(appIcon))
	if err != nil {
		t.Fatalf("decode application icon: %v", err)
	}
	bounds := icon.Bounds()
	if bounds.Dx() != bounds.Dy() || bounds.Dx() < 512 {
		t.Fatalf("application icon dimensions = %dx%d, want a square icon of at least 512px", bounds.Dx(), bounds.Dy())
	}
}
