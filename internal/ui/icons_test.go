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
		palette := qt.QGuiApplication_Palette()
		text := palette.Color(qt.QPalette__Active, qt.QPalette__WindowText).Rgb()
		runtime.KeepAlive(palette)
		for _, name := range icons.Names {
			ic := icon(name)
			if ic.IsNull() {
				t.Errorf("%s is not drawn", name)
				continue
			}
			for _, size := range []int{16, 32} {
				pixmap := ic.Pixmap2(size, size)
				img := pixmap.ToImage()
				runtime.KeepAlive(pixmap)
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

// TestIconsWhileCollecting draws icons over and over while the garbage
// collector runs all the while: the pixmap an icon's engine returns is a Go
// value, which Qt copies only once the engine has returned it.
func TestIconsWhileCollecting(t *testing.T) {
	stop := make(chan struct{})
	collected := make(chan struct{})
	go func() {
		defer close(collected)
		for {
			select {
			case <-stop:
				return
			default:
				runtime.GC()
			}
		}
	}()
	mainthread.Wait(func() {
		ic := icon(icons.Archive)
		for i := range 3000 {
			size := 16 + i%48
			// The pixmap is a Go value too, kept while it is used.
			pixmap := ic.Pixmap2(size, size)
			if pixmap.IsNull() {
				t.Fatalf("no pixmap at %d", size)
			}
			runtime.KeepAlive(pixmap)
		}
	})
	close(stop)
	<-collected
}
