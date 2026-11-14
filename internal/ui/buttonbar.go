package ui

import (
	"github.com/nomad-software/qute/qtcore"
	"github.com/nomad-software/qute/qtgui"
	"github.com/nomad-software/qute/qtwidgets"
)

// buttonBarLine is the opacity, out of 255, of the line along the bottom of
// a bar of buttons, in the colour of the text so that it shows in every theme.
const buttonBarLine = 60

// buttonBar is a window's bar of large buttons, which draws a line along its
// bottom: the style draws none for a bar outside a main window's toolbar
// area.
type buttonBar struct {
	qtwidgets.QToolBarBase
}

// PaintEvent overrides QWidget::paintEvent.
func (b *buttonBar) PaintEvent(ev *qtgui.QPaintEvent) {
	b.Super().PaintEvent(ev)
	c := b.Palette().Color(qtgui.QPaletteWindowText)
	c.A = buttonBarLine
	qtgui.PaintOn(b, func(p *qtgui.QPainter) {
		p.FillRect(qtcore.QRect{Y: b.Height() - 1, Width: b.Width(), Height: 1}, c)
	})
}

// newButtonBar makes a bar of large buttons with their text under them, to
// go above content in the window, the two making its central widget. The bar
// is in the window rather than in its toolbar area, which some desktops'
// styles (Breeze) draw as one with the title bar.
func newButtonBar(window *qtwidgets.QMainWindow, content qtwidgets.QWidgetLike) *qtwidgets.QToolBar {
	central := qtwidgets.NewQWidget(window)
	layout := qtwidgets.NewQVBoxLayout(central)
	layout.SetContentsMargins(qtcore.QMargins{})
	layout.SetSpacing(0)
	bar := qtwidgets.NewQToolBarWithTitleWith(&buttonBar{}, "Buttons", central)
	bar.SetMovable(false)
	bar.SetFloatable(false)
	bar.SetToolButtonStyle(qtcore.ToolButtonTextUnderIcon)
	bar.SetIconSize(qtcore.QSize{Width: 32, Height: 32})
	layout.AddWidget(bar)
	layout.AddWidgetFull(content, 1, 0)
	window.SetCentralWidget(central)
	return bar
}
