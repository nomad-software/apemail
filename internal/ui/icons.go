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
	return makeIcon(name, nil)
}

// tint is the colour an icon is drawn in rather than that of the text: one
// for a light theme and one for a dark, so that it stands out on either.
type tint struct {
	light, dark string
}

// tintedIcon returns the program's own icon with the name, drawn in the
// tint, as icon draws it in the colour of the text, and filled with a pale
// wash of it, as other clients' folders are. Where it is disabled or
// selected it is drawn in the colour icon draws it in, as the tint could not
// be seen on the colour of the selection.
func tintedIcon(name string, t tint) *qt.QIcon {
	return makeIcon(name, &t)
}

// makeIcon returns the icon with the name, in the tint if there is one.
func makeIcon(name string, t *tint) *qt.QIcon {
	data, err := icons.SVG(name)
	if err != nil {
		fmt.Fprintln(os.Stderr, "apemail:", err)
		return qt.NewQIcon()
	}
	return qt.NewQIcon5(newIconEngine(name, data, t))
}

// newIconEngine returns the engine that draws the icon with the name, whose
// SVG is data, in the tint if there is one. Qt asks it for a copy of itself
// where an icon is changed.
func newIconEngine(name string, data []byte, t *tint) *qt.QIconEngine {
	e := qt.NewQIconEngine()
	paint := func(p *qt.QPainter, rect *qt.QRectF, mode qt.QIcon__Mode) {
		p.SetRenderHint(qt.QPainter__Antialiasing)
		colour := iconColour(mode, t)
		if t != nil {
			// The fill of every shape goes first, so that none is
			// drawn over the outline of another.
			svgRenderer(name, data, colour, true).Render2(p, rect)
		}
		svgRenderer(name, data, colour, false).Render2(p, rect)
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
	e.OnClone(func() *qt.QIconEngine { return newIconEngine(name, data, t) })
	return e
}

// iconColour returns the colour an icon is drawn in, in the mode, from the
// palette of the application as it is, and the tint if there is one: that
// of a dark theme where the text is light.
func iconColour(mode qt.QIcon__Mode, t *tint) string {
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
	text := palette.Color(qt.QPalette__Active, qt.QPalette__WindowText)
	switch {
	case t == nil:
		return text.Name()
	case text.Lightness() > 127:
		return t.dark
	}
	return t.light
}

// fillOpacity is how much of the colour of a tinted icon its fill has.
const fillOpacity = "0.2"

// svgRenderers hold the SVG of each icon read for each colour it has been
// drawn in, and of its fill, as reading it each time it is drawn would be
// slow.
var svgRenderers = map[string]*svg.QSvgRenderer{}

// svgRenderer returns the renderer of the icon with the name, whose SVG is
// data, in the colour: of its outline, or, if fill, of its fill alone. The
// icons set their fill and outline on the root, so that is where both are
// changed.
func svgRenderer(name string, data []byte, colour string, fill bool) *svg.QSvgRenderer {
	key := name + " " + colour
	if fill {
		key += " fill"
	}
	r, ok := svgRenderers[key]
	if !ok {
		if fill {
			data = bytes.Replace(data, []byte(`fill="none"`), []byte(`fill="`+colour+`" fill-opacity="`+fillOpacity+`"`), 1)
			data = bytes.Replace(data, []byte(`stroke="currentColor"`), []byte(`stroke="none"`), 1)
		}
		r = svg.NewQSvgRenderer3(bytes.ReplaceAll(data, []byte("currentColor"), []byte(colour)))
		svgRenderers[key] = r
	}
	return r
}
