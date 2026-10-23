package ui

import (
	"bytes"
	"fmt"
	"os"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/svg"

	"github.com/nomad-software/apemail/internal/icons"
)

// iconSizes are the sizes each icon is drawn at, so that Qt has one to hand
// for a menu, the side bar and the button bars, at any scale of the screen.
var iconSizes = []int{16, 22, 24, 32, 48, 64, 96, 128}

// iconCache holds the icons drawn, by name.
var iconCache = map[string]*qt.QIcon{}

// icon returns the program's own icon with the name, drawn in the colour of
// the text of the window, so that it shows in a dark theme as in a light
// one. Qt greys it itself where it is disabled.
func icon(name string) *qt.QIcon {
	if ic := iconCache[name]; ic != nil {
		return ic
	}
	ic := qt.NewQIcon()
	iconCache[name] = ic
	data, err := icons.SVG(name)
	if err != nil {
		fmt.Fprintln(os.Stderr, "apemail:", err)
		return ic
	}
	text := qt.QGuiApplication_Palette().Color(qt.QPalette__Active, qt.QPalette__WindowText).Name()
	r := svg.NewQSvgRenderer3(bytes.ReplaceAll(data, []byte("currentColor"), []byte(text)))
	defer r.Delete()
	for _, size := range iconSizes {
		img := qt.NewQImage3(size, size, qt.QImage__Format_ARGB32_Premultiplied)
		img.Fill2(qt.Transparent)
		p := qt.NewQPainter2(img.QPaintDevice)
		p.SetRenderHint(qt.QPainter__Antialiasing)
		r.Render(p)
		p.End()
		p.Delete()
		// The icon keeps a copy of the picture. The pixmap is a value
		// miqt frees itself, once it is no longer used, so it is not
		// deleted here, which would free it twice.
		ic.AddPixmap(qt.QPixmap_FromImage(img))
		img.Delete()
	}
	return ic
}
