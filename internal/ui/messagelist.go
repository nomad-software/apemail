package ui

import (
	"cmp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nomad-software/qute/qtcore"
	"github.com/nomad-software/qute/qtgui"
	"github.com/nomad-software/qute/qtwidgets"

	"github.com/nomad-software/apemail/internal/icons"
	"github.com/nomad-software/apemail/internal/store"
)

// The columns of the message list.
const (
	colAttachment = iota
	colSubject
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
	// attachmentWidth is the width of the column of paperclips, which is
	// not dragged.
	attachmentWidth = 28
)

// messageList lists the messages of a folder. Its model gives the view only
// the rows it draws, so that a folder of tens of thousands of messages is
// shown at once.
type messageList struct {
	qtwidgets.QTreeViewBase
	model *listModel

	// account and folder are where the messages listed are, which a drag
	// of them says.
	account, folder string
	// all are the messages of the folder in the order they are sorted in,
	// and at where each is among them, by UID.
	all []store.Summary
	at  map[uint32]int
	// filter is the words the messages shown must have in their subject,
	// sender or recipients, in lower case, and none for every message.
	filter []string
	// rows are the messages shown, in the order they are shown, and row the
	// row of each, by UID. They are all and at themselves unless there is a
	// filter. A message that is filtered out has no row, so it cannot be
	// selected, and nothing done to a selection touches it.
	rows []store.Summary
	row  map[uint32]int
	// sent says the folder holds mail from the user, whose list shows who
	// it went to rather than who it is from.
	sent bool
	// uidValidity is that of the folder that is listed, and 0 until one is
	// (a server's is never 0).
	uidValidity uint32
	// sortColumn and sortOrder are how the rows are sorted.
	sortColumn int
	sortOrder  qtcore.SortOrder

	// boldValue and flaggedValue are what the list shows unread messages
	// in, a bold font, and flagged ones in, red, attachmentValue the
	// paperclip of those with attachments, and noValue what a cell with
	// nothing to show has, made once.
	boldValue, flaggedValue, attachmentValue, noValue *qtcore.QVariant

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
	header  *listHeader
	fitting bool

	// selected is called when the selection changes, opened when a message
	// is double clicked, contextMenu for the menu of the selection, and
	// changed when what is listed has changed.
	selected    func()
	opened      func(uid uint32)
	contextMenu func()
	changed     func()
}

func newMessageList() *messageList {
	l := &messageList{
		sortColumn: colDate,
		sortOrder:  qtcore.DescendingOrder,
		noValue:    qtcore.NewQVariant(),
	}
	qtwidgets.NewQTreeViewWith(l, nil)
	l.model = &listModel{list: l}
	qtcore.NewQAbstractTableModelWith(l.model, l)
	bold := qtgui.NewQFont()
	bold.SetBold(true)
	l.boldValue = qtgui.NewQVariantOf(bold)
	l.flaggedValue = qtgui.NewQVariantOf(qtgui.NewQBrushWithColor(parseColour("#d03030")))
	l.attachmentValue = qtgui.NewQVariantOf(icon(icons.Attachment))

	l.SetModel(l.model)
	l.SetDragEnabled(true)
	l.SetDragDropMode(qtwidgets.QAbstractItemViewDragOnly)
	l.SetDefaultDropAction(qtcore.MoveAction)
	l.SetRootIsDecorated(false)
	l.SetUniformRowHeights(true)
	l.SetAllColumnsShowFocus(true)
	l.SetSelectionMode(qtwidgets.QAbstractItemViewExtendedSelection)
	l.SetSelectionBehavior(qtwidgets.QAbstractItemViewSelectRows)
	l.makeHeader()
	l.SetSortingEnabled(true)
	l.SortByColumn(colDate, qtcore.DescendingOrder)
	l.setColumnWidths(0, 0)

	l.SelectionModel().OnSelectionChanged(func(_, _ *qtcore.QItemSelection) {
		l.selectionKnown = false
		// The user chose other messages, so the next of those that were
		// selected is not wanted. The list's own changes are not
		// announced.
		if l.selected != nil {
			l.next = 0
			l.selected()
		}
	})
	l.OnDoubleClicked(func(index *qtcore.QModelIndex) {
		if index.IsValid() && index.Row() < len(l.rows) && l.opened != nil {
			l.opened(l.rows[index.Row()].UID)
		}
	})
	l.SetContextMenuPolicy(qtcore.CustomContextMenu)
	l.OnCustomContextMenuRequested(func(qtcore.QPoint) {
		if l.contextMenu != nil && len(l.selectedUIDs()) > 0 {
			l.contextMenu()
		}
	})
	return l
}

// StartDrag starts a drag of the messages selected, which are dragged to a
// folder of the tree to move them there. The drag is made here rather than
// from the model's mimeData, which would be handed an index for every cell
// of a large selection.
func (l *messageList) StartDrag(qtcore.DropActions) {
	d := l.draggedSelection()
	if len(d.UIDs) == 0 {
		return
	}
	drag := qtgui.NewQDrag(l)
	drag.SetMimeData(d.encode())
	drag.ExecWithDropAction(qtcore.MoveAction, qtcore.MoveAction)
}

// listModel is the model of a messageList, which tells the view what the
// rows it draws show.
type listModel struct {
	qtcore.QAbstractTableModelBase
	list *messageList
}

func (m *listModel) RowCount(parent *qtcore.QModelIndex) int {
	if parent.IsValid() {
		return 0
	}
	return len(m.list.rows)
}

func (m *listModel) ColumnCount(parent *qtcore.QModelIndex) int {
	if parent.IsValid() {
		return 0
	}
	return colCount
}

func (m *listModel) HeaderData(section int, orientation qtcore.Orientations, role int) *qtcore.QVariant {
	if orientation == qtcore.Horizontal && section == colAttachment && role == int(qtcore.DecorationRole) {
		return m.list.attachmentValue
	}
	if orientation != qtcore.Horizontal || role != int(qtcore.DisplayRole) {
		return m.Super().HeaderData(section, orientation, role)
	}
	switch section {
	case colSubject:
		return qtcore.NewQVariantWithString("Subject")
	case colCorrespondent:
		if m.list.sent {
			return qtcore.NewQVariantWithString("Recipient")
		}
		return qtcore.NewQVariantWithString("From")
	case colDate:
		return qtcore.NewQVariantWithString("Date")
	}
	return m.list.noValue
}

func (m *listModel) Sort(column int, order qtcore.SortOrder) {
	l := m.list
	l.sortColumn, l.sortOrder = column, order
	l.resetKeepingSelection(l.all)
}

// Flags lets the messages be dragged.
func (m *listModel) Flags(index *qtcore.QModelIndex) qtcore.ItemFlags {
	flags := m.Super().Flags(index)
	if index.IsValid() {
		flags |= qtcore.ItemIsDragEnabled
	}
	return flags
}

// Data returns what the view shows of a cell.
func (m *listModel) Data(index *qtcore.QModelIndex, role int) *qtcore.QVariant {
	l := m.list
	row := index.Row()
	if !index.IsValid() || row >= len(l.rows) {
		return l.noValue
	}
	msg := l.rows[row]
	switch role {
	case int(qtcore.DisplayRole):
		switch index.Column() {
		case colSubject:
			if msg.Subject == "" {
				return qtcore.NewQVariantWithString("(no subject)")
			}
			return qtcore.NewQVariantWithString(msg.Subject)
		case colCorrespondent:
			if l.sent {
				return qtcore.NewQVariantWithString(shortAddresses(msg.To))
			}
			return qtcore.NewQVariantWithString(shortAddresses(msg.From))
		case colDate:
			return qtcore.NewQVariantWithString(formatDate(msg.Date))
		}
	case int(qtcore.DecorationRole):
		if index.Column() == colAttachment && msg.Attachments {
			return l.attachmentValue
		}
	case int(qtcore.FontRole):
		if !msg.Has(`\Seen`) {
			return l.boldValue
		}
	case int(qtcore.ToolTipRole):
		switch index.Column() {
		case colCorrespondent:
			if l.sent {
				return qtcore.NewQVariantWithString(msg.To)
			}
			return qtcore.NewQVariantWithString(msg.From)
		case colDate:
			if !msg.Date.IsZero() {
				return qtcore.NewQVariantWithString(msg.Date.Local().Format("Monday 2 January 2006 15:04"))
			}
		}
	case int(qtcore.ForegroundRole):
		if msg.Has(`\Flagged`) {
			return l.flaggedValue
		}
	}
	return l.noValue
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
		switch {
		case keys != nil:
			c = cmp.Compare(keys[i], keys[j])
		case l.sortColumn == colAttachment:
			// Those with attachments, and then by date.
			c = cmp.Compare(attachments(rows[i]), attachments(rows[j]))
			if c == 0 {
				c = rows[i].Date.Compare(rows[j].Date)
			}
		default:
			c = rows[i].Date.Compare(rows[j].Date)
		}
		if c == 0 {
			c = cmp.Compare(rows[i].UID, rows[j].UID)
		}
		if l.sortOrder == qtcore.DescendingOrder {
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

// attachments is 1 for a message with attachments and 0 for one without,
// by which they are sorted.
func attachments(m store.Summary) int {
	if m.Attachments {
		return 1
	}
	return 0
}

// parallelRows is how many rows there must be for their sort keys to be
// worked out on every processor rather than one.
const parallelRows = 10000

// sortKeys returns the sort key of each row.
func sortKeys(rows []store.Summary, key func(store.Summary) string) []string {
	keys := make([]string, len(rows))
	eachRow(len(rows), func(i int) { keys[i] = key(rows[i]) })
	return keys
}

// eachRow calls f with the number of each of n rows, which a large folder has
// done on every processor at once, each taking a part of the rows.
func eachRow(n int, f func(i int)) {
	parts := 1
	if n >= parallelRows {
		parts = runtime.GOMAXPROCS(0)
	}
	size := (n + parts - 1) / parts
	var wg sync.WaitGroup
	for start := 0; start < n; start += size {
		end := min(start+size, n)
		wg.Go(func() {
			for i := start; i < end; i++ {
				f(i)
			}
		})
	}
	wg.Wait()
}

// rowNumbers returns where each of the messages is among them, by UID.
func rowNumbers(rows []store.Summary) map[uint32]int {
	numbers := make(map[uint32]int, len(rows))
	for i, r := range rows {
		numbers[r.UID] = i
	}
	return numbers
}

// matchesFilter reports whether every word of the filter, which is in lower
// case, is in the message's subject, sender or recipients, in any order and
// each in any of them. Nothing else of it is looked at, so that filtering a
// large folder is quick.
func matchesFilter(m store.Summary, filter []string) bool {
	subject, from, to := strings.ToLower(m.Subject), strings.ToLower(m.From), strings.ToLower(m.To)
	for _, word := range filter {
		if !strings.Contains(subject, word) && !strings.Contains(from, word) && !strings.Contains(to, word) {
			return false
		}
	}
	return true
}

// setFilter shows only the messages with every word of the text in their
// subject, sender or recipients, whatever its case, or every message if it has
// no words. The messages are sorted already, so they are not sorted again, and
// the current one, if it is still shown, is kept in sight.
func (l *messageList) setFilter(text string) {
	filter := strings.Fields(strings.ToLower(text))
	if slices.Equal(filter, l.filter) {
		return
	}
	l.filter = filter
	l.relist(l.applyFilter)
	if cur := l.CurrentIndex(); cur.IsValid() {
		l.ScrollTo(cur)
	}
	l.announce()
}

// applyFilter makes the rows those of the messages that the filter lets
// through.
func (l *messageList) applyFilter() {
	if len(l.filter) == 0 {
		l.rows, l.row = l.all, l.at
		return
	}
	matches := make([]bool, len(l.all))
	eachRow(len(l.all), func(i int) { matches[i] = matchesFilter(l.all[i], l.filter) })
	l.rows = nil
	for i, m := range l.all {
		if matches[i] {
			l.rows = append(l.rows, m)
		}
	}
	l.row = rowNumbers(l.rows)
}

// setIndex lists the messages of the index, keeping those that are
// selected.
func (l *messageList) setIndex(ix *store.Index, sent bool) {
	defer l.announce()
	if sent == l.sent && ix.UIDValidity == l.uidValidity && l.update(ix) {
		return
	}
	// A folder numbered anew has other messages by the UIDs of those
	// selected, if any, so none of it is selected.
	renumbered := l.uidValidity != 0 && ix.UIDValidity != l.uidValidity
	deselected := renumbered && len(l.selectedUIDs()) > 0
	if renumbered {
		l.clear()
	}
	l.sent = sent
	l.uidValidity = ix.UIDValidity
	l.resetKeepingSelection(ix.Messages)
	if deselected && l.selected != nil {
		l.selected()
	}
}

// listed reports whether the messages of a folder are listed, how many of
// them are shown, and how many the folder has.
func (l *messageList) listed() (shown, total int, ok bool) {
	return len(l.rows), len(l.all), l.uidValidity != 0
}

// announce says that what is listed has changed.
func (l *messageList) announce() {
	if l.changed != nil {
		l.changed()
	}
}

// update shows the messages of the index in place of the rows of the same
// UIDs, if they are the same messages as the list shows, and reports if
// they were. That is so when only their flags have changed, which is most
// changes, or whether they have attachments, which an index saved before it
// said learns, and then nothing moves: a message's subject, sender and date
// never change, so neither does whether the filter lets it through. A list
// sorted by attachments is sorted again if they have. Only the rows that have
// changed are drawn again.
func (l *messageList) update(ix *store.Index) bool {
	if len(ix.Messages) != len(l.all) {
		return false
	}
	// The UIDs are told apart, so the same number of them, all known, is the
	// same messages.
	for _, m := range ix.Messages {
		i, ok := l.at[m.UID]
		if !ok || l.sortColumn == colAttachment && l.all[i].Attachments != m.Attachments {
			return false
		}
	}
	first, last := len(l.rows), -1
	for _, m := range ix.Messages {
		i := l.at[m.UID]
		changed := !slices.Equal(l.all[i].Flags, m.Flags) || l.all[i].Attachments != m.Attachments
		l.all[i] = m
		if r, ok := l.row[m.UID]; ok {
			l.rows[r] = m
			if changed {
				first, last = min(first, r), max(last, r)
			}
		}
	}
	if last >= 0 {
		from, to := l.model.Index(first, 0), l.model.Index(last, colCount-1)
		l.model.EmitDataChanged(from, to)
	}
	return true
}

// resetKeepingSelection lists the messages, sorted, selecting again those
// that were selected, and the current one.
func (l *messageList) resetKeepingSelection(messages []store.Summary) {
	l.relist(func() {
		l.all = l.sorted(messages)
		l.at = rowNumbers(l.all)
		l.applyFilter()
	})
}

// relist makes the change to the rows, selecting again the messages that were
// selected and are still shown, and the current one.
func (l *messageList) relist(change func()) {
	selected := map[uint32]bool{}
	for _, uid := range l.selectedUIDs() {
		selected[uid] = true
	}
	var current uint32
	cur := l.CurrentIndex()
	if cur.IsValid() && cur.Row() < len(l.rows) {
		current = l.rows[cur.Row()].UID
	}

	sm := l.SelectionModel()
	// The selection is put back without each row of it being announced.
	blocked := l.selected
	l.selected = nil
	l.model.BeginResetModel()
	change()
	l.model.EndResetModel()
	// A reset empties the selection without saying so.
	l.selectionKnown = false

	// Rows that were selected are selected again a run at a time, as a
	// large selection a row at a time takes a long while.
	var selection *qtcore.QItemSelection
	reselected := 0
	for i := 0; i < len(l.rows); i++ {
		if l.rows[i].UID == current {
			l.setCurrentRow(i, qtcore.QItemSelectionModelNoUpdate)
		}
		if !selected[l.rows[i].UID] {
			continue
		}
		j := i
		for j+1 < len(l.rows) && selected[l.rows[j+1].UID] {
			j++
			if l.rows[j].UID == current {
				l.setCurrentRow(j, qtcore.QItemSelectionModelNoUpdate)
			}
		}
		from, to := l.model.Index(i, 0), l.model.Index(j, colCount-1)
		if selection == nil {
			selection = qtcore.NewQItemSelectionWithModelIndex(from, to)
		} else {
			selection.Select(from, to)
		}
		reselected += j - i + 1
		i = j
	}
	if reselected > 0 {
		sm.SelectItemSelection(selection, qtcore.QItemSelectionModelSelect|qtcore.QItemSelectionModelRows)
	}
	// Once the messages selected have all gone, which can be a few at a
	// time, as archiving moves them to a folder for each year, the one
	// after them is selected in their place.
	if len(selected) > 0 && reselected == 0 {
		if i, ok := l.row[l.next]; ok {
			l.setCurrentRow(i, qtcore.QItemSelectionModelClearAndSelect|qtcore.QItemSelectionModelRows)
			index := l.model.Index(i, 0)
			l.ScrollTo(index)
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

// setCurrentRow makes the row the current one, selecting it as the flags
// say.
func (l *messageList) setCurrentRow(row int, flags qtcore.QItemSelectionModelSelectionFlags) {
	index := l.model.Index(row, 0)
	l.SelectionModel().SetCurrentIndex(index, flags)
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
	return draggedMessages{Account: l.account, Folder: l.folder, UIDValidity: l.uidValidity, UIDs: l.selectedUIDs()}
}

// clear empties the list. The filter stays, as the field it comes from does.
func (l *messageList) clear() {
	blocked := l.selected
	l.selected = nil
	l.model.BeginResetModel()
	l.all, l.at = nil, nil
	l.rows, l.row = nil, nil
	l.uidValidity = 0
	l.next = 0
	l.model.EndResetModel()
	l.selectionKnown = false
	l.selected = blocked
	l.announce()
}

// selectedUIDs returns the UIDs of the messages that are selected, in the
// order they are shown.
func (l *messageList) selectedUIDs() []uint32 {
	if !l.selectionKnown {
		// Each row is asked about, rather than a list made of an index
		// for each of what may be 100,000 rows. Rows are selected whole,
		// so one with any of it selected is selected, which is asked
		// rather than whether all of it is, as Qt then asks the model
		// about one cell of the row and not every one.
		l.selection = nil
		sm := l.SelectionModel()
		if sm.HasSelection() {
			for i, r := range l.rows {
				if sm.RowIntersectsSelection(i) {
					l.selection = append(l.selection, r.UID)
				}
			}
		}
		l.selectionKnown = true
	}
	return slices.Clone(l.selection)
}

// summary returns what the list knows of the message with the UID, whether
// or not the filter lets it be shown.
func (l *messageList) summary(uid uint32) (store.Summary, bool) {
	if i, ok := l.at[uid]; ok {
		return l.all[i], true
	}
	return store.Summary{}, false
}

// makeHeader makes the header of the columns, which fill the width of the
// list as those of other clients do: dragging the border between two
// columns trades width between them, the right edge of the date trades it
// with the subject, and the subject takes what the list gains or loses in
// width. Every border but that of the paperclips, which keep their width,
// can be dragged, so the date can be made wider by dragging the border on
// its left, rather than only by its right edge against the side of the
// list.
func (l *messageList) makeHeader() {
	l.header = &listHeader{resized: l.fitSubject}
	qtwidgets.NewQHeaderViewWith(l.header, qtcore.Horizontal, l)
	l.header.SetDefaultAlignment(qtcore.AlignLeft | qtcore.AlignVCenter)
	l.header.SetStretchLastSection(false)
	// The trade is between columns in their order, so they stay in it.
	l.header.SetSectionsMovable(false)
	// Qt holds every column to its minimum, the paperclips too, so the
	// others are held to theirs here.
	l.header.SetMinimumSectionSize(attachmentWidth)
	l.SetHeader(l.header)
	for col := range colCount {
		l.header.SetSectionResizeMode(col, qtwidgets.QHeaderViewInteractive)
	}
	// The paperclips keep their width, and their border is not dragged.
	l.header.SetSectionResizeMode(colAttachment, qtwidgets.QHeaderViewFixed)
	l.fitting = true
	l.header.ResizeSection(colAttachment, attachmentWidth)
	l.fitting = false
	l.header.OnSectionResized(func(col, oldSize, newSize int) {
		if l.fitting {
			return
		}
		l.fitting = true
		defer func() { l.fitting = false }()
		// The date trades with the subject, not the paperclips.
		next := col + 1
		if next == colCount {
			next = colSubject
		}
		if newSize < minColumnWidth {
			l.header.ResizeSection(col, minColumnWidth)
			newSize = minColumnWidth
		}
		width := l.header.SectionSize(next) - (newSize - oldSize)
		if width < minColumnWidth {
			// The column beside it can give no more.
			l.header.ResizeSection(col, newSize-(minColumnWidth-width))
			width = minColumnWidth
		}
		l.header.ResizeSection(next, width)
	})
}

// listHeader is the header of the columns of a messageList, which says when
// its width has changed.
type listHeader struct {
	qtwidgets.QHeaderViewBase
	resized func()
}

func (h *listHeader) ResizeEvent(e *qtgui.QResizeEvent) {
	h.Super().ResizeEvent(e)
	h.resized()
}

// fitSubject gives the subject the width the other columns leave.
func (l *messageList) fitSubject() {
	l.fitting = true
	defer func() { l.fitting = false }()
	width := l.header.Width() - attachmentWidth - l.header.SectionSize(colCorrespondent) - l.header.SectionSize(colDate)
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
