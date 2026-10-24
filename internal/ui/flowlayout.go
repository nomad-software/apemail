package ui

import (
	"runtime"

	qt "github.com/mappu/miqt/qt6"
)

// flowLayout lays its items out in a row and wraps them onto the next row
// where the row is full, as words are, so that a message with many
// attachments is shown on as many rows as they need rather than widening the
// window. Qt has no such layout of its own. It asks for no more width than
// its margins: an item wider than the row is cut down to it.
type flowLayout struct {
	*qt.QLayout
	items []*qt.QLayoutItem
	// sizes are the sizes last returned to Qt, which copies them only once
	// the override has returned; they are deleted on the next call.
	sizes struct{ hint, minimum *qt.QSize }
}

func newFlowLayout(parent *qt.QWidget) *flowLayout {
	l := &flowLayout{QLayout: qt.NewQLayout(parent)}
	l.SetSpacing(4)
	l.OnAddItem(func(item *qt.QLayoutItem) { l.items = append(l.items, item) })
	l.OnCount(func() int { return len(l.items) })
	l.OnItemAt(func(i int) *qt.QLayoutItem {
		if i < 0 || i >= len(l.items) {
			return nil
		}
		return l.items[i]
	})
	l.OnTakeAt(func(i int) *qt.QLayoutItem {
		if i < 0 || i >= len(l.items) {
			return nil
		}
		item := l.items[i]
		l.items = append(l.items[:i], l.items[i+1:]...)
		return item
	})
	l.OnExpandingDirections(func(func() qt.Orientation) qt.Orientation { return 0 })
	l.OnHasHeightForWidth(func(func() bool) bool { return true })
	l.OnHeightForWidth(func(_ func(int) int, width int) int { return l.place(width, nil) })
	l.OnMinimumHeightForWidth(func(_ func(int) int, width int) int { return l.place(width, nil) })
	l.OnSizeHint(func() *qt.QSize { return giveSize(&l.sizes.hint, l.size()) })
	l.OnMinimumSize(func(func() *qt.QSize) *qt.QSize { return giveSize(&l.sizes.minimum, l.size()) })
	l.OnSetGeometry(func(super func(*qt.QRect), r *qt.QRect) {
		super(r)
		l.place(r.Width(), r)
	})
	return l
}

// addWidget adds w, shown at once: Qt shows a widget added to one shown only
// later, and until then the rows have no room for it.
func (l *flowLayout) addWidget(w *qt.QWidget) {
	l.AddWidget(w)
	w.Show()
}

// giveSize returns s, which a constructor made, for an override to return,
// deleting the one last returned, kept in last, as returned.give does for a
// QVariant.
func giveSize(last **qt.QSize, s *qt.QSize) *qt.QSize {
	if *last != nil {
		(*last).Delete()
	}
	*last = s
	return s
}

// size is the size the layout asks for: its margins, and the height of a
// row, as heightForWidth gives the height the items need.
func (l *flowLayout) size() *qt.QSize {
	m := l.ContentsMargins()
	defer runtime.KeepAlive(m)
	return qt.NewQSize2(m.Left()+m.Right(), l.rowHeight()+m.Top()+m.Bottom())
}

// rowHeight is the height of the tallest item.
func (l *flowLayout) rowHeight() int {
	h := 0
	for _, item := range l.items {
		s := item.SizeHint()
		h = max(h, s.Height())
		runtime.KeepAlive(s)
	}
	return h
}

// heightOfRows is the height that rows of the tallest item take.
func (l *flowLayout) heightOfRows(rows int) int {
	m := l.ContentsMargins()
	defer runtime.KeepAlive(m)
	return rows*l.rowHeight() + (rows-1)*l.Spacing() + m.Top() + m.Bottom()
}

// place works out where each item goes in a width, and returns the height
// they take. With a rectangle, the layout's, it puts them there.
func (l *flowLayout) place(width int, r *qt.QRect) int {
	m := l.ContentsMargins()
	defer runtime.KeepAlive(m)
	left, top := m.Left(), m.Top()
	if r != nil {
		left += r.X()
		top += r.Y()
	}
	right := left + max(width-m.Left()-m.Right(), 1)
	spacing := l.Spacing()

	// Each row is found first, so that its items can be centred on its
	// height.
	type placed struct {
		item *qt.QLayoutItem
		x, w int
		h    int
	}
	var row []placed
	x, y, rowHeight := left, top, 0
	flush := func() {
		for _, p := range row {
			if r != nil {
				g := qt.NewQRect4(p.x, y+(rowHeight-p.h)/2, p.w, p.h)
				p.item.SetGeometry(g)
				g.Delete()
			}
		}
		row = row[:0]
	}
	for _, item := range l.items {
		if item.IsEmpty() {
			continue
		}
		s := item.SizeHint()
		w, h := min(s.Width(), right-left), s.Height()
		runtime.KeepAlive(s)
		if x > left && x+w > right {
			flush()
			x, y, rowHeight = left, y+rowHeight+spacing, 0
		}
		row = append(row, placed{item, x, w, h})
		x += w + spacing
		rowHeight = max(rowHeight, h)
	}
	flush()
	return y + rowHeight - top + m.Top() + m.Bottom()
}
