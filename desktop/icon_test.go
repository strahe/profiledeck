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

func TestMenuBarTemplateIconIs44x44WithAlpha(t *testing.T) {
	t.Parallel()
	icon, err := png.Decode(bytes.NewReader(menuBarTemplateIcon))
	if err != nil {
		t.Fatalf("decode menu bar template icon: %v", err)
	}
	bounds := icon.Bounds()
	// Menu-bar templates are drawn at 22pt; we ship one @2x PNG (22×2 = 44px)
	// because SetTemplateIcon accepts a single image buffer.
	if bounds.Dx() != 44 || bounds.Dy() != 44 {
		t.Fatalf("menu bar template dimensions = %dx%d, want 44x44 (22pt @2x)", bounds.Dx(), bounds.Dy())
	}
	var transparent, opaqueBlack int
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, a := icon.At(x, y).RGBA()
			if a < 256 {
				transparent++
				continue
			}
			if r>>8 < 32 && g>>8 < 32 && b>>8 < 32 {
				opaqueBlack++
			}
		}
	}
	if transparent == 0 {
		t.Fatal("menu bar template needs transparent pixels for macOS template rendering")
	}
	if opaqueBlack == 0 {
		t.Fatal("menu bar template needs opaque black glyph pixels")
	}
}
