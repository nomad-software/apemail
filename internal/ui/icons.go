package ui

import (
	"bytes"
	"fmt"
	"os"
	"runtime"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/svg"

	"github.com/nomad-software/apemail/internal/icons"
)

// icon returns the program's own icon with the name. It is drawn each time
// it is shown, in the colours of the palette as it then is, so that it
// follows a change of theme: in the colour of the text, of disabled text
// where it is disabled, and of selected text where it is selected.
func icon(name string) *qt.QIcon {
	data, err := icons.SVG(name)
	if err != nil {
		fmt.Fprintln(os.Stderr, "apemail:", err)
		return qt.NewQIcon()
	}
	return qt.NewQIcon5(newIconEngine(name, data))
}

// newIconEngine returns the engine that draws the icon with the name, whose
// SVG is data. Qt asks it for a copy of itself where an icon is changed.
func newIconEngine(name string, data []byte) *qt.QIconEngine {
	e := qt.NewQIconEngine()
	paint := func(p *qt.QPainter, rect *qt.QRectF, mode qt.QIcon__Mode) {
		p.SetRenderHint(qt.QPainter__Antialiasing)
		svgRenderer(name, data, iconColour(mode)).Render2(p, rect)
	}
	e.OnPaint(func(p *qt.QPainter, rect *qt.QRect, mode qt.QIcon__Mode, _ qt.QIcon__State) {
		r := qt.NewQRectF5(rect)
		defer r.Delete()
		paint(p, r, mode)
	})
	// Qt's own pixmap of an engine is not cleared before it is painted,
	// so the icon is drawn on one that is transparent. miqt hands Qt the
	// pixmap returned, which Qt copies once this returns: it is kept until
	// the next is asked for, by when it has been, as otherwise the garbage
	// collector could free it first.
	var last *qt.QPixmap
	e.OnPixmap(func(_ func(*qt.QSize, qt.QIcon__Mode, qt.QIcon__State) *qt.QPixmap, size *qt.QSize, mode qt.QIcon__Mode, _ qt.QIcon__State) *qt.QPixmap {
		img := qt.NewQImage3(size.Width(), size.Height(), qt.QImage__Format_ARGB32_Premultiplied)
		defer img.Delete()
		img.Fill2(qt.Transparent)
		p := qt.NewQPainter2(img.QPaintDevice)
		r := qt.NewQRectF4(0, 0, float64(size.Width()), float64(size.Height()))
		paint(p, r, mode)
		r.Delete()
		p.End()
		p.Delete()
		last = qt.QPixmap_FromImage(img)
		return last
	})
	e.OnClone(func() *qt.QIconEngine { return newIconEngine(name, data) })
	return e
}

// iconColour returns the colour an icon is drawn in, in the mode, from the
// palette of the application as it is.
func iconColour(mode qt.QIcon__Mode) string {
	// The palette is a copy, which the garbage collector may free once
	// it is no longer used, and the colours it gives are its own: it is
	// kept until they are read.
	palette := qt.QGuiApplication_Palette()
	defer runtime.KeepAlive(palette)
	switch mode {
	case qt.QIcon__Disabled:
		return palette.Color(qt.QPalette__Disabled, qt.QPalette__WindowText).Name()
	case qt.QIcon__Selected:
		return palette.Color(qt.QPalette__Active, qt.QPalette__HighlightedText).Name()
	}
	return palette.Color(qt.QPalette__Active, qt.QPalette__WindowText).Name()
}

// svgRenderers hold the SVG of each icon read for each colour it has been
// drawn in, as reading it each time it is drawn would be slow.
var svgRenderers = map[string]*svg.QSvgRenderer{}

// svgRenderer returns the renderer of the icon with the name, whose SVG is
// data, in the colour.
func svgRenderer(name string, data []byte, colour string) *svg.QSvgRenderer {
	key := name + " " + colour
	r, ok := svgRenderers[key]
	if !ok {
		r = svg.NewQSvgRenderer3(bytes.ReplaceAll(data, []byte("currentColor"), []byte(colour)))
		svgRenderers[key] = r
	}
	return r
}
