package ui

import (
	"github.com/nomad-software/qute/qtcore"
	"github.com/nomad-software/qute/qtwidgets"
)

// flowLayout lays its items out in a row and wraps them onto the next row
// where the row is full, as words are, so that a message with many
// attachments is shown on as many rows as they need rather than widening the
// window. Qt has no such layout of its own. It asks for no more width than
// its margins: an item wider than the row is cut down to it.
type flowLayout struct {
	qtwidgets.QLayoutBase
	items []*qtwidgets.QLayoutItem
}

func newFlowLayout(parent qtwidgets.QWidgetLike) *flowLayout {
	l := &flowLayout{}
	qtwidgets.NewQLayoutWith(l, parent)
	l.SetSpacing(4)
	return l
}

// What Qt asks of a layout.

func (l *flowLayout) AddItem(item *qtwidgets.QLayoutItem) { l.items = append(l.items, item) }

func (l *flowLayout) Count() int { return len(l.items) }

func (l *flowLayout) ItemAt(i int) *qtwidgets.QLayoutItem {
	if i < 0 || i >= len(l.items) {
		return nil
	}
	return l.items[i]
}

func (l *flowLayout) TakeAt(i int) *qtwidgets.QLayoutItem {
	if i < 0 || i >= len(l.items) {
		return nil
	}
	item := l.items[i]
	l.items = append(l.items[:i], l.items[i+1:]...)
	return item
}

func (l *flowLayout) ExpandingDirections() qtcore.Orientations { return 0 }

func (l *flowLayout) HasHeightForWidth() bool { return true }

func (l *flowLayout) HeightForWidth(width int) int { return l.place(width, nil) }

func (l *flowLayout) MinimumHeightForWidth(width int) int { return l.place(width, nil) }

// SizeHint is the size the layout asks for: its margins, and the height of a
// row, as HeightForWidth gives the height the items need.
func (l *flowLayout) SizeHint() qtcore.QSize {
	m := l.ContentsMargins()
	return qtcore.QSize{Width: m.Left + m.Right, Height: l.rowHeight() + m.Top + m.Bottom}
}

func (l *flowLayout) MinimumSize() qtcore.QSize { return l.SizeHint() }

func (l *flowLayout) SetGeometry(r qtcore.QRect) {
	l.Super().SetGeometry(r)
	l.place(r.Width, &r)
}

// addWidget adds w, shown at once: Qt shows a widget added to one shown only
// later, and until then the rows have no room for it.
func (l *flowLayout) addWidget(w qtwidgets.QWidgetLike) {
	l.AddWidget(w)
	w.AsQWidget().Show()
}

// rowHeight is the height of the tallest item.
func (l *flowLayout) rowHeight() int {
	h := 0
	for _, item := range l.items {
		h = max(h, item.SizeHint().Height)
	}
	return h
}

// heightOfRows is the height that rows of the tallest item take.
func (l *flowLayout) heightOfRows(rows int) int {
	m := l.ContentsMargins()
	return rows*l.rowHeight() + (rows-1)*l.Spacing() + m.Top + m.Bottom
}

// place works out where each item goes in a width, and returns the height
// they take. With a rectangle, the layout's, it puts them there.
func (l *flowLayout) place(width int, r *qtcore.QRect) int {
	m := l.ContentsMargins()
	left, top := m.Left, m.Top
	if r != nil {
		left += r.X
		top += r.Y
	}
	right := left + max(width-m.Left-m.Right, 1)
	spacing := l.Spacing()

	// Each row is found first, so that its items can be centred on its
	// height.
	type placed struct {
		item *qtwidgets.QLayoutItem
		x, w int
		h    int
	}
	var row []placed
	x, y, rowHeight := left, top, 0
	flush := func() {
		for _, p := range row {
			if r != nil {
				p.item.SetGeometry(qtcore.QRect{X: p.x, Y: y + (rowHeight-p.h)/2, Width: p.w, Height: p.h})
			}
		}
		row = row[:0]
	}
	for _, item := range l.items {
		if item.IsEmpty() {
			continue
		}
		s := item.SizeHint()
		w, h := min(s.Width, right-left), s.Height
		if x > left && x+w > right {
			flush()
			x, y, rowHeight = left, y+rowHeight+spacing, 0
		}
		row = append(row, placed{item, x, w, h})
		x += w + spacing
		rowHeight = max(rowHeight, h)
	}
	flush()
	return y + rowHeight - top + m.Top + m.Bottom
}
