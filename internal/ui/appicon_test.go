package ui

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/mainthread"

	"github.com/nomad-software/apemail/internal/appicon"
)

func TestAppIcon(t *testing.T) {
	mainthread.Wait(func() {
		icon := appIcon()
		if icon.IsNull() {
			t.Error("the program has no icon")
		}
		// It is loaded once.
		if appIcon() != icon {
			t.Error("the icon is loaded again")
		}
	})
}

// iconsEnv names the folder TestWriteIcons writes the icon of the macOS app
// into. The test is skipped without it; make icons sets it to packaging.
const iconsEnv = "APEMAIL_ICONS"

// macIconSize and macIconMargin are the size of the icon of the macOS app
// and the margin Apple's grid gives the shape of an icon inside it, which
// the icons of the Dock are drawn with: the program's icon fills its square,
// and would be larger than every other in the Dock without it.
const (
	macIconSize   = 1024
	macIconMargin = 100
)

// macIconPNG returns the program's icon as the macOS app has it: scaled
// smoothly into the middle of a transparent square of the size, within the
// margin.
func macIconPNG(size, margin int) []byte {
	icon := qt.NewQImage()
	defer icon.Delete()
	if !icon.LoadFromDataWithData(appicon.PNG) {
		return nil
	}
	inner := size - 2*margin
	// A value miqt frees itself once it is no longer used, which it is
	// kept until it has been drawn.
	scaled := icon.Scaled3(inner, inner, qt.KeepAspectRatio, qt.SmoothTransformation)
	defer runtime.KeepAlive(scaled)

	out := qt.NewQImage3(size, size, qt.QImage__Format_ARGB32_Premultiplied)
	defer out.Delete()
	out.Fill2(qt.Transparent)
	painter := qt.NewQPainter2(out.QPaintDevice)
	painter.SetRenderHint(qt.QPainter__SmoothPixmapTransform)
	painter.DrawImage9(margin+(inner-scaled.Width())/2, margin+(inner-scaled.Height())/2, scaled)
	painter.End()
	painter.Delete()

	buffer := qt.NewQBuffer()
	defer buffer.Delete()
	if !buffer.Open(qt.QIODeviceBase__WriteOnly) || !out.Save4(buffer.QIODevice, "PNG") {
		return nil
	}
	return buffer.Data()
}

// TestWriteIcons draws the icon the macOS app is made from,
// packaging/macos/apemail.png, from which macos.sh makes the .icns of the
// app. The AppImage has the program's own icon as it is. Run make icons
// again whenever the icon changes.
func TestWriteIcons(t *testing.T) {
	dir := os.Getenv(iconsEnv)
	if dir == "" {
		t.Skip(iconsEnv + " is not set")
	}
	var data []byte
	mainthread.Wait(func() { data = macIconPNG(macIconSize, macIconMargin) })
	if data == nil {
		t.Fatal("the icon could not be drawn")
	}
	path := filepath.Join(dir, "macos", "apemail.png")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestMacIcon checks that the icon of the macOS app is of the size macOS
// wants, with its margin clear and the program's icon within it.
func TestMacIcon(t *testing.T) {
	var data []byte
	mainthread.Wait(func() { data = macIconPNG(macIconSize, macIconMargin) })
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil || img.Bounds().Dx() != macIconSize || img.Bounds().Dy() != macIconSize {
		t.Fatalf("icon %v, %v", img, err)
	}
	if _, _, _, a := img.At(macIconMargin/2, macIconSize/2).RGBA(); a != 0 {
		t.Error("the margin is not clear")
	}
	if _, _, _, a := img.At(macIconSize/2, macIconSize/2).RGBA(); a == 0 {
		t.Error("the middle is clear")
	}

	// The icon kept for the app is the one drawn from the icon as it is,
	// so that a new icon is not left out of the app.
	kept, err := os.ReadFile(filepath.Join("..", "..", "packaging", "macos", "apemail.png"))
	if err != nil {
		t.Fatal(err)
	}
	// Another Qt may scale or round a little differently, which is not a
	// different icon.
	keptImg, err := png.Decode(bytes.NewReader(kept))
	if err != nil || keptImg.Bounds() != img.Bounds() {
		t.Fatalf("packaging/macos/apemail.png is %v, %v: run make icons", keptImg, err)
	}
	for y := range macIconSize {
		for x := range macIconSize {
			r1, g1, b1, a1 := img.At(x, y).RGBA()
			r2, g2, b2, a2 := keptImg.At(x, y).RGBA()
			for _, d := range []int{int(r1) - int(r2), int(g1) - int(g2), int(b1) - int(b2), int(a1) - int(a2)} {
				if d < -8*257 || d > 8*257 {
					t.Fatalf("packaging/macos/apemail.png is not drawn from the icon as it is (at %d,%d): run make icons", x, y)
				}
			}
		}
	}
}
