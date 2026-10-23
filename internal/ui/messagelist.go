package ui

import (
	"cmp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	qt "github.com/mappu/miqt/qt6"

	"github.com/nomad-software/apemail/internal/store"
)

// The columns of the message list.
const (
	colSubject = iota
	colCorrespondent
	colDate
	colCount
)

// defaultCorrespondentWidth and defaultDateWidth are the widths of the
// columns of who a message is from (or to) and its date until they are
// dragged to others.
const (
	defaultCorrespondentWidth = 220
	defaultDateWidth          = 150
	// minColumnWidth is the narrowest a column is made by dragging the
	// border of one beside it.
	minColumnWidth = 40
)

// messageList lists the messages of a folder. Its model gives the view only
// the rows it draws, so that a folder of tens of thousands of messages is
// shown at once.
type messageList struct {
	*qt.QTreeView
	model *qt.QAbstractTableModel

	// account and folder are where the messages listed are, which a drag
	// of them says.
	account, folder string
	// rows are the messages in the order they are shown, and row the row
	// of each, by UID.
	rows []store.Summary
	row  map[uint32]int
	// sent says the folder holds mail from the user, whose list shows who
	// it went to rather than who it is from.
	sent bool
	// uidValidity is that of the folder that is listed.
	uidValidity uint32
	// sortColumn and sortOrder are how the rows are sorted.
	sortColumn int
	sortOrder  qt.SortOrder

	bold, normal *qt.QFont

	// selection are the UIDs of the messages that are selected, in the
	// order of their rows, worked out when they are first asked for after
	// the selection changes; selectionKnown says if they have been.
	selection      []uint32
	selectionKnown bool
	// next is the message to select once those selected have gone from
	// the list, as when they are deleted, so that the next can be deleted
	// with another click; 0 if there is none.
	next uint32

	// header is the header of the columns, and fitting says the widths of
	// its columns are being changed to fit, not dragged.
	header  *qt.QHeaderView
	fitting bool

	// selected is called when the selection changes, opened when a message
	// is double clicked, and contextMenu for the menu of the selection.
	selected    func()
	opened      func(uid uint32)
	contextMenu func()
}

func newMessageList() *messageList {
	l := &messageList{
		QTreeView:  qt.NewQTreeView2(),
		model:      qt.NewQAbstractTableModel(),
		sortColumn: colDate,
		sortOrder:  qt.DescendingOrder,
		normal:     qt.NewQFont(),
	}
	l.bold = qt.NewQFont()
	l.bold.SetBold(true)

	l.model.OnRowCount(func(parent *qt.QModelIndex) int {
		if parent.IsValid() {
			return 0
		}
		return len(l.rows)
	})
	l.model.OnColumnCount(func(parent *qt.QModelIndex) int {
		if parent.IsValid() {
			return 0
		}
		return colCount
	})
	l.model.OnData(l.data)
	l.model.OnHeaderData(func(super func(int, qt.Orientation, int) *qt.QVariant, section int, orientation qt.Orientation, role int) *qt.QVariant {
		if orientation != qt.Horizontal || role != int(qt.DisplayRole) {
			return super(section, orientation, role)
		}
		switch section {
		case colSubject:
			return qt.NewQVariant11("Subject")
		case colCorrespondent:
			if l.sent {
				return qt.NewQVariant11("Recipient")
			}
			return qt.NewQVariant11("From")
		case colDate:
			return qt.NewQVariant11("Date")
		}
		return qt.NewQVariant()
	})
	l.model.OnSort(func(_ func(int, qt.SortOrder), column int, order qt.SortOrder) {
		l.sortColumn, l.sortOrder = column, order
		l.resetKeepingSelection(l.rows)
	})

	// Messages are dragged to a folder of the tree to move them there.
	l.model.OnFlags(func(super func(*qt.QModelIndex) qt.ItemFlag, index *qt.QModelIndex) qt.ItemFlag {
		flags := super(index)
		if index.IsValid() {
			flags |= qt.ItemIsDragEnabled
		}
		return flags
	})
	// The drag is made here rather than by the model's mimeData, which miqt
	// hands a list of indexes that it frees while they are in use, and
	// cannot make of more than 65,535: dragging a large selection would
	// crash.
	l.OnStartDrag(func(func(qt.DropAction), qt.DropAction) {
		d := l.draggedSelection()
		if len(d.UIDs) == 0 {
			return
		}
		drag := qt.NewQDrag(l.QObject)
		drag.SetMimeData(d.encode())
		drag.Exec2(qt.MoveAction, qt.MoveAction)
	})

	l.SetModel(l.model.QAbstractItemModel)
	l.SetDragEnabled(true)
	l.SetDragDropMode(qt.QAbstractItemView__DragOnly)
	l.SetDefaultDropAction(qt.MoveAction)
	l.SetRootIsDecorated(false)
	l.SetUniformRowHeights(true)
	l.SetAllColumnsShowFocus(true)
	l.SetSelectionMode(qt.QAbstractItemView__ExtendedSelection)
	l.SetSelectionBehavior(qt.QAbstractItemView__SelectRows)
	l.makeHeader()
	l.SetSortingEnabled(true)
	l.SortByColumn(colDate, qt.DescendingOrder)
	l.setColumnWidths(0, 0)

	l.SelectionModel().OnSelectionChanged(func(_, _ *qt.QItemSelection) {
		l.selectionKnown = false
		// The user chose other messages, so the next of those that were
		// selected is not wanted. The list's own changes are not
		// announced.
		if l.selected != nil {
			l.next = 0
			l.selected()
		}
	})
	l.OnDoubleClicked(func(index *qt.QModelIndex) {
		if index.IsValid() && index.Row() < len(l.rows) && l.opened != nil {
			l.opened(l.rows[index.Row()].UID)
		}
	})
	l.SetContextMenuPolicy(qt.CustomContextMenu)
	l.OnCustomContextMenuRequested(func(*qt.QPoint) {
		if l.contextMenu != nil && len(l.selectedUIDs()) > 0 {
			l.contextMenu()
		}
	})
	return l
}

// data returns what the view shows of a cell.
func (l *messageList) data(index *qt.QModelIndex, role int) *qt.QVariant {
	row := index.Row()
	if !index.IsValid() || row >= len(l.rows) {
		return qt.NewQVariant()
	}
	m := l.rows[row]
	switch role {
	case int(qt.DisplayRole):
		switch index.Column() {
		case colSubject:
			if m.Subject == "" {
				return qt.NewQVariant11("(no subject)")
			}
			return qt.NewQVariant11(m.Subject)
		case colCorrespondent:
			if l.sent {
				return qt.NewQVariant11(shortAddresses(m.To))
			}
			return qt.NewQVariant11(shortAddresses(m.From))
		case colDate:
			return qt.NewQVariant11(formatDate(m.Date))
		}
	case int(qt.FontRole):
		if !m.Has(`\Seen`) {
			return l.bold.ToQVariant()
		}
	case int(qt.ToolTipRole):
		switch index.Column() {
		case colCorrespondent:
			if l.sent {
				return qt.NewQVariant11(m.To)
			}
			return qt.NewQVariant11(m.From)
		case colDate:
			return qt.NewQVariant11(m.Date.Local().Format("Monday 2 January 2006 15:04"))
		}
	case int(qt.ForegroundRole):
		if m.Has(`\Flagged`) {
			return qt.NewQBrush3(qt.NewQColor6("#d03030")).ToQVariant()
		}
	}
	return qt.NewQVariant()
}

// shortAddresses returns the names of the addresses, or the addresses of
// those without names.
func shortAddresses(list string) string {
	var names []string
	for _, a := range splitAddresses(list) {
		a = strings.TrimSpace(a)
		if i := strings.Index(a, " <"); i > 0 && strings.HasSuffix(a, ">") {
			a = strings.Trim(a[:i], `"`)
		}
		names = append(names, a)
	}
	return strings.Join(names, ", ")
}

// splitAddresses splits a list of addresses at the commas that are not
// within quotes.
func splitAddresses(list string) []string {
	var parts []string
	quoted, start := false, 0
	for i, r := range list {
		switch r {
		case '"':
			quoted = !quoted
		case ',':
			if !quoted {
				parts = append(parts, list[start:i])
				start = i + 1
			}
		}
	}
	if rest := strings.TrimSpace(list[start:]); rest != "" {
		parts = append(parts, rest)
	}
	return parts
}

// formatDate returns the date as the list shows it: the full date and time,
// whenever it was.
func formatDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("02/01/2006 15:04")
}

// sorted returns the rows in the order the list is sorted in. What each row
// is sorted by is worked out once, rather than at each comparison, as for a
// large folder that is most of the work.
func (l *messageList) sorted(rows []store.Summary) []store.Summary {
	var keys []string
	switch l.sortColumn {
	case colSubject:
		keys = sortKeys(rows, func(m store.Summary) string { return strings.ToLower(m.Subject) })
	case colCorrespondent:
		keys = sortKeys(rows, func(m store.Summary) string {
			if l.sent {
				return strings.ToLower(shortAddresses(m.To))
			}
			return strings.ToLower(shortAddresses(m.From))
		})
	}
	order := make([]int, len(rows))
	for i := range order {
		order[i] = i
	}
	slices.SortFunc(order, func(i, j int) int {
		var c int
		if keys != nil {
			c = cmp.Compare(keys[i], keys[j])
		} else {
			c = rows[i].Date.Compare(rows[j].Date)
		}
		if c == 0 {
			c = cmp.Compare(rows[i].UID, rows[j].UID)
		}
		if l.sortOrder == qt.DescendingOrder {
			return -c
		}
		return c
	})
	out := make([]store.Summary, len(rows))
	for k, i := range order {
		out[k] = rows[i]
	}
	return out
}

// parallelRows is how many rows there must be for their sort keys to be
// worked out on every processor rather than one.
const parallelRows = 10000

// sortKeys returns the sort key of each row, which a large folder has
// worked out on every processor at once, each taking a part of the rows.
func sortKeys(rows []store.Summary, key func(store.Summary) string) []string {
	keys := make([]string, len(rows))
	parts := 1
	if len(rows) >= parallelRows {
		parts = runtime.GOMAXPROCS(0)
	}
	size := (len(rows) + parts - 1) / parts
	var wg sync.WaitGroup
	for start := 0; start < len(rows); start += size {
		end := min(start+size, len(rows))
		wg.Go(func() {
			for i := start; i < end; i++ {
				keys[i] = key(rows[i])
			}
		})
	}
	wg.Wait()
	return keys
}

// setIndex lists the messages of the index, keeping those that are
// selected.
func (l *messageList) setIndex(ix *store.Index, sent bool) {
	if sent == l.sent && ix.UIDValidity == l.uidValidity && l.update(ix) {
		return
	}
	l.sent = sent
	l.uidValidity = ix.UIDValidity
	l.resetKeepingSelection(ix.Messages)
}

// update shows the messages of the index in place of the rows of the same
// UIDs, if they are the same messages as the list shows, and reports if
// they were. That is so when only their flags have changed, which is most
// changes, and then nothing moves: a message's subject, sender and date
// never change. Only the rows that have changed are drawn again.
func (l *messageList) update(ix *store.Index) bool {
	if len(ix.Messages) != len(l.rows) {
		return false
	}
	// The UIDs are told apart, so the same number of them all shown is the
	// same messages.
	for _, m := range ix.Messages {
		if _, ok := l.row[m.UID]; !ok {
			return false
		}
	}
	first, last := len(l.rows), -1
	for _, m := range ix.Messages {
		i := l.row[m.UID]
		if !slices.Equal(l.rows[i].Flags, m.Flags) {
			first, last = min(first, i), max(last, i)
		}
		l.rows[i] = m
	}
	if last >= 0 {
		l.model.DataChanged(l.model.Index(first, 0, noParent), l.model.Index(last, colCount-1, noParent))
	}
	return true
}

// resetKeepingSelection shows the rows, sorted, selecting again the messages
// that were selected, and the current one.
func (l *messageList) resetKeepingSelection(rows []store.Summary) {
	selected := map[uint32]bool{}
	for _, uid := range l.selectedUIDs() {
		selected[uid] = true
	}
	var current uint32
	if cur := l.CurrentIndex(); cur.IsValid() && cur.Row() < len(l.rows) {
		current = l.rows[cur.Row()].UID
	}

	sm := l.SelectionModel()
	// The selection is put back without each row of it being announced.
	blocked := l.selected
	l.selected = nil
	l.model.BeginResetModel()
	l.rows = l.sorted(rows)
	l.row = make(map[uint32]int, len(l.rows))
	for i, r := range l.rows {
		l.row[r.UID] = i
	}
	l.model.EndResetModel()
	// A reset empties the selection without saying so.
	l.selectionKnown = false

	// Rows that were selected are selected again a run at a time, as a
	// large selection a row at a time takes a long while.
	selection := qt.NewQItemSelection2()
	defer selection.Delete()
	reselected := 0
	for i := 0; i < len(l.rows); i++ {
		if l.rows[i].UID == current {
			sm.SetCurrentIndex(l.model.Index(i, 0, noParent), qt.QItemSelectionModel__NoUpdate)
		}
		if !selected[l.rows[i].UID] {
			continue
		}
		j := i
		for j+1 < len(l.rows) && selected[l.rows[j+1].UID] {
			j++
			if l.rows[j].UID == current {
				sm.SetCurrentIndex(l.model.Index(j, 0, noParent), qt.QItemSelectionModel__NoUpdate)
			}
		}
		selection.Select(l.model.Index(i, 0, noParent), l.model.Index(j, colCount-1, noParent))
		reselected += j - i + 1
		i = j
	}
	if reselected > 0 {
		sm.Select2(selection, qt.QItemSelectionModel__Select|qt.QItemSelectionModel__Rows)
	}
	// Once the messages selected have all gone, which can be a few at a
	// time, as archiving moves them to a folder for each year, the one
	// after them is selected in their place.
	if len(selected) > 0 && reselected == 0 {
		if i, ok := l.row[l.next]; ok {
			index := l.model.Index(i, 0, noParent)
			sm.SetCurrentIndex(index, qt.QItemSelectionModel__ClearAndSelect|qt.QItemSelectionModel__Rows)
			l.ScrollTo(index, qt.QAbstractItemView__EnsureVisible)
			l.selectionKnown = false
		}
		l.next = 0
	}
	l.selected = blocked
	// A message that was selected and is gone changes what is shown.
	if reselected != len(selected) && l.selected != nil {
		l.selected()
	}
}

// selectNextLater has the list select, once the messages selected have gone
// from it, the first message after them that is not selected, or, if they
// are the last, the one before them.
func (l *messageList) selectNextLater() {
	l.next = 0
	uids := l.selectedUIDs()
	if len(uids) == 0 {
		return
	}
	selected := map[uint32]bool{}
	for _, uid := range uids {
		selected[uid] = true
	}
	first, last := l.row[uids[0]], l.row[uids[len(uids)-1]]
	for i := last + 1; i < len(l.rows); i++ {
		if !selected[l.rows[i].UID] {
			l.next = l.rows[i].UID
			return
		}
	}
	for i := first - 1; i >= 0; i-- {
		if !selected[l.rows[i].UID] {
			l.next = l.rows[i].UID
			return
		}
	}
}

// draggedSelection returns the messages a drag from the list carries: those
// that are selected, and where they are.
func (l *messageList) draggedSelection() draggedMessages {
	return draggedMessages{Account: l.account, Folder: l.folder, UIDs: l.selectedUIDs()}
}

// noParent is the parent of the rows of the list, which have none.
var noParent = qt.NewQModelIndex()

// clear empties the list.
func (l *messageList) clear() {
	blocked := l.selected
	l.selected = nil
	l.model.BeginResetModel()
	l.rows, l.row = nil, nil
	l.uidValidity = 0
	l.next = 0
	l.model.EndResetModel()
	l.selectionKnown = false
	l.selected = blocked
}

// selectedUIDs returns the UIDs of the messages that are selected, in the
// order they are shown.
func (l *messageList) selectedUIDs() []uint32 {
	if !l.selectionKnown {
		// Each row is asked about, as miqt's SelectedRows frees the
		// indexes it returns while they are in use, and cannot return
		// more than 65,535.
		l.selection = nil
		sm := l.SelectionModel()
		if sm.HasSelection() {
			for i, r := range l.rows {
				if sm.IsRowSelected(i) {
					l.selection = append(l.selection, r.UID)
				}
			}
		}
		l.selectionKnown = true
	}
	return slices.Clone(l.selection)
}

// summary returns what the list shows of the message with the UID.
func (l *messageList) summary(uid uint32) (store.Summary, bool) {
	if i, ok := l.row[uid]; ok {
		return l.rows[i], true
	}
	return store.Summary{}, false
}

// makeHeader makes the header of the columns, which fill the width of the
// list as those of other clients do: dragging the border between two
// columns trades width between them, the right edge of the date trades it with the
// subject, and the subject takes what the list gains or loses in width.
// Every border can be dragged, so the date can be made wider by dragging
// the border on its left, rather than only by its right edge against the
// side of the list.
func (l *messageList) makeHeader() {
	l.header = qt.NewQHeaderView(qt.Horizontal)
	l.header.SetDefaultAlignment(qt.AlignLeft | qt.AlignVCenter)
	l.header.SetStretchLastSection(false)
	// The trade is between columns in their order, so they stay in it.
	l.header.SetSectionsMovable(false)
	l.header.SetMinimumSectionSize(minColumnWidth)
	l.SetHeader(l.header)
	for col := range colCount {
		l.header.SetSectionResizeMode2(col, qt.QHeaderView__Interactive)
	}
	l.header.OnSectionResized(func(col, oldSize, newSize int) {
		if l.fitting {
			return
		}
		l.fitting = true
		defer func() { l.fitting = false }()
		next := (col + 1) % colCount
		width := l.header.SectionSize(next) - (newSize - oldSize)
		if width < minColumnWidth {
			// The column beside it can give no more.
			l.header.ResizeSection(col, newSize-(minColumnWidth-width))
			width = minColumnWidth
		}
		l.header.ResizeSection(next, width)
	})
	l.header.OnResizeEvent(func(super func(*qt.QResizeEvent), e *qt.QResizeEvent) {
		super(e)
		l.fitSubject()
	})
}

// fitSubject gives the subject the width the other columns leave.
func (l *messageList) fitSubject() {
	l.fitting = true
	defer func() { l.fitting = false }()
	width := l.header.Width() - l.header.SectionSize(colCorrespondent) - l.header.SectionSize(colDate)
	l.header.ResizeSection(colSubject, max(width, minColumnWidth))
}

// setColumnWidths gives the columns of who a message is from (or to) and of
// its date their widths, or their default widths where a width is zero, and
// the subject the rest. The columns are the same whatever folder is listed,
// so every folder has these widths.
func (l *messageList) setColumnWidths(correspondent, date int) {
	if correspondent <= 0 {
		correspondent = defaultCorrespondentWidth
	}
	if date <= 0 {
		date = defaultDateWidth
	}
	l.fitting = true
	l.header.ResizeSection(colCorrespondent, max(correspondent, minColumnWidth))
	l.header.ResizeSection(colDate, max(date, minColumnWidth))
	l.fitting = false
	l.fitSubject()
}

// columnWidths returns the widths of the columns of who a message is from
// (or to) and of its date.
func (l *messageList) columnWidths() (correspondent, date int) {
	return l.header.SectionSize(colCorrespondent), l.header.SectionSize(colDate)
}
