package ui

import (
	"slices"
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/nomad-software/qute/qtcore"
	"github.com/nomad-software/qute/qtgui"
	"github.com/nomad-software/qute/qtwidgets"
	"github.com/nomad-software/qute/qutetest"

	"github.com/nomad-software/apemail/internal/remote/remotetest"
	"github.com/nomad-software/apemail/internal/store"
)

// filterFolder returns a folder whose messages each have what is looked for
// in another header.
func filterFolder() *store.Index {
	return &store.Index{Folder: "INBOX", UIDValidity: 1, Messages: []store.Summary{
		{UID: 1, Subject: "Quarterly REPORT", From: "a@example.com", To: "me@example.com"},
		{UID: 2, Subject: "Lunch", From: `"Report Bot" <bot@example.com>`, To: "me@example.com"},
		{UID: 3, Subject: "Hello", From: "a@example.com", To: "reports@example.com"},
		{UID: 4, Subject: "Other", From: "b@example.com", To: "me@example.com"},
	}}
}

// shownUIDs returns the UIDs of the rows of the list, smallest first.
func shownUIDs(l *messageList) []uint32 {
	var uids []uint32
	for _, r := range l.rows {
		uids = append(uids, r.UID)
	}
	slices.Sort(uids)
	return uids
}

// TestFilterHeaders checks what the filter looks at, the subject, sender and
// recipients, whatever the case, and that every word must be in them.
func TestFilterHeaders(t *testing.T) {
	withList(t, func(l *messageList) {
		l.setIndex(filterFolder(), false)
		for text, want := range map[string][]uint32{
			"report":      {1, 2, 3},
			"  rePORT ":   {1, 2, 3},
			"lunch":       {2},
			"b@example":   {4},
			"reports@":    {3},
			"nothing":     nil,
			"quarterly r": {1},
			// Every word must match, in any order, each in any header.
			"report quarterly": {1},
			"bot lunch":        {2},
			"hello reports":    {3},
			"reports a@":       {3},
			"lunch other":      nil,
			"report nothing":   nil,
			"":                 {1, 2, 3, 4},
			"   ":              {1, 2, 3, 4},
		} {
			l.setFilter(text)
			if got := shownUIDs(l); !slices.Equal(got, want) {
				t.Errorf("filter %q shows %v, want %v", text, got, want)
			}
			if got := l.model.RowCount(qtcore.NewQModelIndex()); got != len(want) {
				t.Errorf("filter %q: the model has %d rows", text, got)
			}
		}
	})
}

// TestFilterOrder checks that what is shown stays in the order of the sort,
// that sorting while filtered keeps to the filter, and that taking the filter
// away shows the sort made meanwhile.
func TestFilterOrder(t *testing.T) {
	withList(t, func(l *messageList) {
		l.setIndex(filterFolder(), false)
		l.setFilter("report")
		l.SortByColumn(colSubject, qtcore.AscendingOrder)
		subjects := func() (got []string) {
			for _, r := range l.rows {
				got = append(got, r.Subject)
			}
			return got
		}
		if got, want := subjects(), []string{"Hello", "Lunch", "Quarterly REPORT"}; !slices.Equal(got, want) {
			t.Errorf("filtered = %v", got)
		}
		l.setFilter("")
		if got, want := subjects(), []string{"Hello", "Lunch", "Other", "Quarterly REPORT"}; !slices.Equal(got, want) {
			t.Errorf("unfiltered = %v", got)
		}
	})
}

// TestFilterFollowsFolder checks that a change to the folder is seen through
// a filter: flags change in place, drawing the row shown rather than where the
// message is among them all, the message filtered out is changed too, and new
// messages are filtered.
func TestFilterFollowsFolder(t *testing.T) {
	withList(t, func(l *messageList) {
		ix := filterFolder()
		l.setIndex(ix, false)
		l.setFilter("lunch")

		resets, drawn := 0, -1
		l.model.OnModelReset(func() { resets++ })
		l.model.OnDataChanged(func(from, _ *qtcore.QModelIndex, _ []int) { drawn = from.Row() })
		changed := *ix
		changed.Messages = slices.Clone(ix.Messages)
		changed.Messages[1].Flags = []string{`\Seen`}
		changed.Messages[3].Flags = []string{`\Flagged`}
		l.setIndex(&changed, false)
		if resets != 0 || drawn != 0 || !l.rows[0].Has(`\Seen`) {
			t.Errorf("%d resets, row %d drawn, row 0 %+v", resets, drawn, l.rows[0])
		}

		// A change only to one filtered out draws nothing.
		drawn = -1
		again := changed
		again.Messages = slices.Clone(changed.Messages)
		again.Messages[3].Flags = nil
		l.setIndex(&again, false)
		if resets != 0 || drawn != -1 {
			t.Errorf("%d resets, row %d drawn", resets, drawn)
		}

		l.setFilter("")
		if s, _ := l.summary(4); s.Has(`\Flagged`) {
			t.Errorf("the message filtered out kept its old flags: %+v", s)
		}
		if s, _ := l.summary(2); !s.Has(`\Seen`) {
			t.Errorf("the message shown lost its flags: %+v", s)
		}

		l.setFilter("lunch")
		more := again
		more.Messages = append(slices.Clone(again.Messages),
			store.Summary{UID: 5, Subject: "Lunch again"},
			store.Summary{UID: 6, Subject: "Supper"})
		l.setIndex(&more, false)
		if got := shownUIDs(l); !slices.Equal(got, []uint32{2, 5}) {
			t.Errorf("shown = %v", got)
		}
		if shown, total, ok := l.listed(); shown != 2 || total != 6 || !ok {
			t.Errorf("listed = %d, %d, %v", shown, total, ok)
		}
	})
}

// TestFilterKeepsSelection checks that a message selected and still shown
// stays selected, in its new row, and that one filtered out does not.
func TestFilterKeepsSelection(t *testing.T) {
	withList(t, func(l *messageList) {
		l.setIndex(filterFolder(), false)
		clickRow(l, l.row[2])
		addRow(l, l.row[4])
		l.setFilter("lunch")
		if got := l.selectedUIDs(); !slices.Equal(got, []uint32{2}) {
			t.Errorf("selected = %v", got)
		}
		l.setFilter("")
		if got := l.selectedUIDs(); !slices.Equal(got, []uint32{2}) {
			t.Errorf("selected once the filter went = %v", got)
		}
	})
}

// TestFilterProtectsHidden checks, on the server, that deleting what is
// selected while filtered leaves the messages filtered out alone, whether one
// message or all those shown, and that the message selected next is one that
// is shown.
func TestFilterProtectsHidden(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Trash")
	for _, subject := range []string{"Keep one", "Drop one", "Keep two", "Drop two", "Drop three"} {
		s.Append(t, "INBOX", subject)
	}
	w := newTestWindow(t, s)
	wait(t, "the inbox", func() bool { return len(w.list.rows) == 5 && w.folder == "INBOX" })

	qutetest.Run(t, func() {
		// A message selected before the filter hides it is not deleted.
		for i, r := range w.list.rows {
			if r.Subject == "Keep one" {
				clickRow(w.list, i)
			}
		}
		w.filter.SetText("drop")
		if len(w.list.rows) != 3 {
			t.Errorf("%d rows", len(w.list.rows))
		}
		if got := w.list.selectedUIDs(); len(got) != 0 {
			t.Errorf("selected while hidden: %v", got)
		}
		clickRow(w.list, 0)
		w.deleteMessages()
	})
	wait(t, "the next to be selected", func() bool {
		return len(w.list.all) == 4 && len(w.list.rows) == 2 && len(w.list.selectedUIDs()) == 1
	})
	qutetest.Run(t, func() {
		next, _ := w.list.summary(w.list.selectedUIDs()[0])
		if !strings.HasPrefix(next.Subject, "Drop") {
			t.Errorf("selected next: %q", next.Subject)
		}
		w.list.SelectAll()
		w.deleteMessages()
	})
	wait(t, "the messages to go", func() bool { return len(w.list.all) == 2 })

	var left []string
	for _, m := range s.Messages(t, "INBOX") {
		left = append(left, m.Subject)
	}
	slices.Sort(left)
	if !slices.Equal(left, []string{"Keep one", "Keep two"}) {
		t.Errorf("inbox = %v", left)
	}
	if trash := s.Messages(t, "Trash"); len(trash) != 3 {
		t.Errorf("trash = %+v", trash)
	}
	qutetest.Run(t, func() {
		if len(w.list.rows) != 0 || len(w.list.selectedUIDs()) != 0 {
			t.Errorf("rows %+v, selected %v", w.list.rows, w.list.selectedUIDs())
		}
	})
}

// TestFilterField checks the field: that it filters as it is typed in, that
// the title says how many are shown, that its clear button shows only when
// there is something to clear, that clearing it or choosing another folder
// closes it, and that it is closed where no folder is listed.
func TestFilterField(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Other", "Trash")
	s.Append(t, "INBOX", "Apples")
	s.Append(t, "INBOX", "Pears")
	s.Append(t, "INBOX", "Plums")
	s.Append(t, "Other", "Figs")
	w := newTestWindow(t, s)
	wait(t, "the inbox", func() bool { return len(w.list.rows) == 3 && w.folder == "INBOX" })

	var clearButton *qtgui.QAction
	qutetest.Run(t, func() {
		clearButton = w.filter.Actions()[0]
		if w.filter.IsVisibleTo(w) || clearButton.IsVisible() {
			t.Errorf("before it is asked for, field shown %v, clear shown %v", w.filter.IsVisibleTo(w), clearButton.IsVisible())
		}
		w.openFilter()
		if !w.filter.IsVisibleTo(w) {
			t.Errorf("the field is not shown when asked for")
		}
		w.filter.SetText("p")
		if got := w.folderTitle.Text(); !strings.Contains(got, "3 messages") || strings.Contains(got, " of ") {
			t.Errorf("all match, title = %q", got)
		}
		w.filter.SetText("plu")
		if got := w.folderTitle.Text(); !strings.Contains(got, "1 of 3 messages") {
			t.Errorf("title = %q", got)
		}
		if len(w.list.rows) != 1 || w.list.rows[0].Subject != "Plums" || !clearButton.IsVisible() {
			t.Errorf("rows %+v, clear shown %v", w.list.rows, clearButton.IsVisible())
		}
		clearButton.Trigger()
		if w.filter.Text() != "" || len(w.list.rows) != 3 || clearButton.IsVisible() {
			t.Errorf("cleared: %q, rows %+v, clear shown %v", w.filter.Text(), w.list.rows, clearButton.IsVisible())
		}
		if w.filter.IsVisibleTo(w) {
			t.Errorf("the field is shown once cleared")
		}
		w.openFilter()

		w.filter.SetText("plu")
		w.folders.selectFolder(w.account, "Other")
		if w.filter.IsVisibleTo(w) {
			t.Errorf("the field is shown in another folder")
		}
		if w.filter.Text() != "" || len(w.list.filter) != 0 {
			t.Errorf("another folder kept the filter: %q, %q", w.filter.Text(), w.list.filter)
		}
	})
	wait(t, "the other folder", func() bool { return len(w.list.rows) == 1 && w.list.rows[0].Subject == "Figs" })

	qutetest.Run(t, func() {
		w.openFilter()
		w.folders.SetCurrentItem(w.folders.accounts[w.account])
		if w.filter.IsVisibleTo(w) {
			t.Errorf("the field is shown with no folder")
		}
	})
}

// TestFilterShortcut checks that Ctrl+F is in a menu of the bar, shows the
// field with the keyboard in it and what is there selected to be typed over,
// that Escape closes it, showing every message again with the keyboard back in
// the list, and that Ctrl+F does nothing where no folder is listed.
func TestFilterShortcut(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Trash")
	s.Append(t, "INBOX", "Apples")
	w := newTestWindow(t, s)
	wait(t, "the inbox", func() bool { return len(w.list.rows) == 1 && w.folder == "INBOX" })

	qutetest.Run(t, func() {
		w.Resize(1280, 800)
		w.Show()
		w.ActivateWindow()
		qtcore.QCoreApplicationProcessEvents()
		if got := w.filterMessages.Shortcut().ToString(); got != "Ctrl+F" {
			t.Errorf("shortcut = %q", got)
		}
		inMenu := false
		for _, o := range w.filterMessages.AssociatedObjects() {
			inMenu = inMenu || o.Inherits("QMenu")
		}
		if !inMenu {
			t.Errorf("it is in no menu")
		}
		w.list.SetFocus()
		w.filter.SetText("apples")
		if w.filter.IsVisible() {
			t.Errorf("the field is shown before Ctrl+F")
		}
		w.filterMessages.Trigger()
		if !w.filter.HasFocus() {
			t.Errorf("the field does not have the focus")
		}
		if got := w.filter.SelectedText(); got != "apples" {
			t.Errorf("selected = %q", got)
		}
		if !w.filterMessages.IsEnabled() {
			t.Errorf("disabled with a folder listed")
		}

		qtcore.QCoreApplicationSendEvent(w.filter, qtgui.NewQKeyEvent(qtcore.QEventKeyPress, int(qtcore.Key_Escape), qtcore.NoModifier))
		if w.filter.IsVisible() || w.filter.Text() != "" || len(w.list.filter) != 0 {
			t.Errorf("after Escape the field is shown %v with %q", w.filter.IsVisible(), w.filter.Text())
		}
		if !w.list.HasFocus() {
			t.Errorf("after Escape the keyboard is not in the list")
		}

		w.folders.SetCurrentItem(w.folders.accounts[w.account])
		if w.filterMessages.IsEnabled() {
			t.Errorf("enabled with no folder listed")
		}
	})
}

// TestFilterHalfRow checks that the field takes half of the row it shares with
// the title, whatever the width of the window, and gives way to a title too
// long for its half.
func TestFilterHalfRow(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Trash")
	s.Append(t, "INBOX", "Apples")
	w := newTestWindow(t, s)
	wait(t, "the inbox", func() bool { return w.folder == "INBOX" && len(w.list.rows) == 1 })

	qutetest.Run(t, func() {
		w.Show()
		w.openFilter()
		for _, width := range []int{1000, 1280, 1600} {
			w.Resize(width, 800)
			qtcore.QCoreApplicationProcessEvents()
			field, title := w.filter.Width(), w.folderTitle.Width()
			if field-title > 1 || title-field > 1 {
				t.Errorf("at %d the field is %d wide and the title %d", width, field, title)
			}
		}
		half := w.filter.Width()
		w.folderTitle.SetText(strings.Repeat("A long name ", 20))
		qtcore.QCoreApplicationProcessEvents()
		if got := w.filter.Width(); got >= half {
			t.Errorf("beside a long title the field is %d wide, was %d", got, half)
		}
	})
}

// TestFilterKeepsDeleteKey checks that Delete in the field is the field's, to
// edit what is typed, and not the shortcut that deletes the messages selected.
// Qt asks the widget with the focus first, by this event, and a shortcut only
// fires if it is ignored.
func TestFilterKeepsDeleteKey(t *testing.T) {
	withList(t, func(l *messageList) {
		window := qtwidgets.NewQWidget(nil)
		defer window.DeleteLater()
		w := &Window{list: l}
		w.makeFilter(window)
		w.filter.SetText("text")
		for _, key := range []qtcore.Key{qtcore.Key_Delete, qtcore.Key_Backspace} {
			e := qtgui.NewQKeyEvent(qtcore.QEventShortcutOverride, int(key), qtcore.NoModifier)
			e.Ignore()
			qtcore.QCoreApplicationSendEvent(w.filter, e)
			if !e.IsAccepted() {
				t.Errorf("key %v is left to the shortcuts", key)
			}
		}
	})
}

// BenchmarkFilter100k measures a key typed in the filter with a folder of
// 100,000 messages listed, when every one of them matches, which costs the
// most, as each gets a row.
func BenchmarkFilter100k(b *testing.B) {
	ix := folderOf(100000)
	withList(b, func(l *messageList) {
		l.setIndex(ix, false)
		filters := []string{"example", "example.com"}
		i := 0
		for b.Loop() {
			l.setFilter(filters[i%2])
			i++
		}
	})
}
