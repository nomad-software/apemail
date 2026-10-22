package appicon

import (
	"bytes"
	"image/png"
	"testing"
)

// The desktop entry is written with the icon as it is, so it must be a PNG
// of the size the desktop expects.
func TestPNG(t *testing.T) {
	img, err := png.Decode(bytes.NewReader(PNG))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 256 || b.Dy() != 256 {
		t.Errorf("the icon is %v, not 256×256", b)
	}
}
