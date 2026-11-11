package ui

import (
	"bytes"
	"fmt"
	"os"

	"github.com/nomad-software/qute/qtcore"
	"github.com/nomad-software/qute/qtgui"
	"github.com/nomad-software/qute/qtsvg"

	"github.com/nomad-software/apemail/internal/icons"
)

// icon returns the program's own icon with the name. It is drawn each time
// it is shown, in the colours of the palette as it then is, so that it
// follows a change of theme: in the colour of the text, of disabled text
// where it is disabled, and of selected text where it is selected.
func icon(name string) *qtgui.QIcon {
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
func tintedIcon(name string, t tint) *qtgui.QIcon {
	return makeIcon(name, &t)
}

// makeIcon returns the icon with the name, in the tint if there is one.
func makeIcon(name string, t *tint) *qtgui.QIcon {
	data, err := icons.SVG(name)
	if err != nil {
		fmt.Fprintln(os.Stderr, "apemail:", err)
		return qtgui.NewQIcon()
	}
	return qtgui.NewQIconWithIconEngine(newIconEngine(name, data, t))
}

// iconEngine draws the icon with the name, whose SVG is data, in the tint
// if there is one.
type iconEngine struct {
	qtgui.QIconEngineBase
	name string
	data []byte
	tint *tint
}

// newIconEngine returns the engine that draws the icon with the name, whose
// SVG is data, in the tint if there is one. The icon it is given to owns it.
func newIconEngine(name string, data []byte, t *tint) *iconEngine {
	e := &iconEngine{name: name, data: data, tint: t}
	qtgui.NewQIconEngineWith(e)
	return e
}

// Paint draws the icon.
func (e *iconEngine) Paint(p *qtgui.QPainter, rect qtcore.QRect, mode qtgui.QIconMode, _ qtgui.QIconState) {
	e.paint(p, qtcore.QRectF{X: float64(rect.X), Y: float64(rect.Y), Width: float64(rect.Width), Height: float64(rect.Height)}, mode)
}

// Pixmap returns the icon drawn at the size. Qt's own pixmap of an engine is
// not cleared before it is painted, so the icon is drawn on one that is
// transparent.
func (e *iconEngine) Pixmap(size qtcore.QSize, mode qtgui.QIconMode, _ qtgui.QIconState) *qtgui.QPixmap {
	img := qtgui.NewQImageWithSize(size, qtgui.QImageFormat_ARGB32_Premultiplied)
	img.FillColor(qtgui.QColor{})
	p := qtgui.NewQPainterOn(img)
	e.paint(p, qtcore.QRectF{Width: float64(size.Width), Height: float64(size.Height)}, mode)
	p.End()
	return qtgui.QPixmapFromImage(img)
}

// Clone returns a copy of the engine, which Qt asks for where an icon is
// changed.
func (e *iconEngine) Clone() *qtgui.QIconEngine {
	return newIconEngine(e.name, e.data, e.tint).QIconEngine
}

// paint draws the icon in the rectangle.
func (e *iconEngine) paint(p *qtgui.QPainter, rect qtcore.QRectF, mode qtgui.QIconMode) {
	p.SetRenderHint(qtgui.QPainterAntialiasing)
	colour := iconColour(mode, e.tint)
	if e.tint != nil {
		// The fill of every shape goes first, so that none is drawn over
		// the outline of another.
		svgRenderer(e.name, e.data, colour, true).RenderWithRectF(p, rect)
	}
	svgRenderer(e.name, e.data, colour, false).RenderWithRectF(p, rect)
}

// iconColour returns the colour an icon is drawn in, in the mode, from the
// palette of the application as it is, and the tint if there is one: that
// of a dark theme where the text is light.
func iconColour(mode qtgui.QIconMode, t *tint) string {
	palette := qtgui.QGuiApplicationPalette()
	switch mode {
	case qtgui.QIconDisabled:
		return palette.GroupColor(qtgui.QPaletteDisabled, qtgui.QPaletteWindowText).Name()
	case qtgui.QIconSelected:
		return palette.GroupColor(qtgui.QPaletteActive, qtgui.QPaletteHighlightedText).Name()
	}
	text := palette.GroupColor(qtgui.QPaletteActive, qtgui.QPaletteWindowText)
	switch {
	case t == nil:
		return text.Name()
	case lightnessOf(text) > 127:
		return t.dark
	}
	return t.light
}

// lightnessOf returns how light the colour is, from 0 to 255, as Qt reckons
// it: half way between its strongest and its weakest part.
func lightnessOf(c qtgui.QColor) int {
	return (int(max(c.R, c.G, c.B)) + int(min(c.R, c.G, c.B))) / 2
}

// fillOpacity is how much of the colour of a tinted icon its fill has.
const fillOpacity = "0.2"

// svgRenderers hold the SVG of each icon read for each colour it has been
// drawn in, and of its fill, as reading it each time it is drawn would be
// slow.
var svgRenderers = map[string]*qtsvg.QSvgRenderer{}

// svgRenderer returns the renderer of the icon with the name, whose SVG is
// data, in the colour: of its outline, or, if fill, of its fill alone. The
// icons set their fill and outline on the root, so that is where both are
// changed.
func svgRenderer(name string, data []byte, colour string, fill bool) *qtsvg.QSvgRenderer {
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
		r = qtsvg.NewQSvgRendererWithBytes(bytes.ReplaceAll(data, []byte("currentColor"), []byte(colour)), nil)
		svgRenderers[key] = r
	}
	return r
}
