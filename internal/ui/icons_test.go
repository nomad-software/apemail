package ui

import (
	"runtime"
	"testing"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/mainthread"

	"github.com/nomad-software/apemail/internal/icons"
)

// TestIcons checks that every icon is drawn, in the colour of the text, and
// lasts once the garbage collector has run, which frees the pixmaps drawn
// for it.
func TestIcons(t *testing.T) {
	mainthread.Wait(func() {
		for _, name := range icons.Names {
			icon(name)
		}
	})
	runtime.GC()
	runtime.GC()
	mainthread.Wait(func() {
		text := qt.QGuiApplication_Palette().Color(qt.QPalette__Active, qt.QPalette__WindowText).Rgb()
		for _, name := range icons.Names {
			ic := icon(name)
			if ic.IsNull() {
				t.Errorf("%s is not drawn", name)
				continue
			}
			for _, size := range []int{16, 32} {
				img := ic.Pixmap2(size, size).ToImage()
				drawn, other := 0, 0
				for y := range img.Height() {
					for x := range img.Width() {
						c := img.Pixel(x, y)
						switch {
						case c>>24 == 0:
						case c&0xffffff == text&0xffffff:
							drawn++
						default:
							// Edges that are smoothed are partly
							// transparent, in the same colour.
							if c>>24 == 0xff {
								other++
							}
						}
					}
				}
				if drawn == 0 || other > 0 {
					t.Errorf("%s at %d: %d pixels in the text's colour, %d opaque in another", name, size, drawn, other)
				}
			}
		}
		if !icon("nothing").IsNull() {
			t.Error("an icon that is not there was drawn")
		}
	})
}
