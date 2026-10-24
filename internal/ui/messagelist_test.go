package ui

import (
	"fmt"
	"runtime"
	"slices"
	"testing"
	"time"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/mainthread"

	"github.com/nomad-software/apemail/internal/store"
)

// folderOf returns the index of a folder of n messages, much as a real one
// would be, with every message read.
func folderOf(n int) *store.Index {
	ix := &store.Index{Folder: "INBOX", UIDValidity: 1}
	d := time.Date(2016, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 1; i <= n; i++ {
		ix.Messages = append(ix.Messages, store.Summary{
			UID:     uint32(i),
			Subject: fmt.Sprintf("Re: About the quarterly report number %d", (i*7919)%977),
			From:    fmt.Sprintf(`"Someone %03d" <someone%d@example.com>`, (i*31)%331, i),
			To:      fmt.Sprintf(`"Recipient %03d" <r%d@example.com>`, (i*17)%257, i),
			// Out of the order of the UIDs, as mail moved in is.
			Date:        d.Add(time.Duration((i*104729)%n) * time.Minute),
			Flags:       []string{`\Seen`},
			Attachments: i%5 == 0,
		})
	}
	return ix
}

// withList runs f with a message list of its own, on the UI's thread.
func withList(f func(l *messageList)) {
	mainthread.Wait(func() {
		l := newMessageList()
		defer l.Delete()
		l.Resize(1000, 600)
		f(l)
	})
}

// TestSorted checks the order of the list in every column, both ways, in
// a folder small enough to work out its keys on one processor and one
// large enough to work them out on every one.
func TestSorted(t *testing.T) {
	for _, n := range []int{50, parallelRows + 7} {
		ix := folderOf(n)
		for _, sent := range []bool{false, true} {
			for _, col := range []int{colAttachment, colSubject, colCorrespondent, colDate} {
				for _, order := range []qt.SortOrder{qt.AscendingOrder, qt.DescendingOrder} {
					withList(func(l *messageList) {
						l.sent, l.sortColumn, l.sortOrder = sent, col, order
						rows := l.sorted(ix.Messages)
						if len(rows) != n {
							t.Fatalf("%d rows of %d", len(rows), n)
						}
						for i := 1; i < len(rows); i++ {
							c := compareRows(rows[i-1], rows[i], col, sent)
							if order == qt.DescendingOrder {
								c = -c
							}
							if c > 0 {
								t.Fatalf("%d rows, sent %v, column %d, order %v: row %d %+v before %+v", n, sent, col, order, i, rows[i-1], rows[i])
							}
						}
					})
				}
			}
		}
	}
}

// compareRows compares two rows as the list sorts them, the slow way.
func compareRows(a, b store.Summary, col int, sent bool) int {
	var c int
	switch col {
	case colAttachment:
		c = attachments(a) - attachments(b)
		if c == 0 {
			c = a.Date.Compare(b.Date)
		}
	case colSubject:
		c = compareStrings(a.Subject, b.Subject)
	case colCorrespondent:
		if sent {
			c = compareStrings(shortAddresses(a.To), shortAddresses(b.To))
		} else {
			c = compareStrings(shortAddresses(a.From), shortAddresses(b.From))
		}
	default:
		c = a.Date.Compare(b.Date)
	}
	if c == 0 && a.UID != b.UID {
		if a.UID < b.UID {
			return -1
		}
		return 1
	}
	return c
}

// compareStrings compares strings without regard to case.
func compareStrings(a, b string) int {
	return slices.Compare([]byte(lower(a)), []byte(lower(b)))
}

func lower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

// TestAttachmentColumn checks that the list shows a paperclip, in a column of
// its own headed by one, beside each message with attachments, and nothing
// beside one without.
func TestAttachmentColumn(t *testing.T) {
	withList(func(l *messageList) {
		l.setIndex(folderOf(10), false)
		decorated := func(v *qt.QVariant) bool {
			defer runtime.KeepAlive(v)
			return v.IsValid()
		}
		if !decorated(l.model.HeaderData(colAttachment, qt.Horizontal, int(qt.DecorationRole))) {
			t.Error("the column has no paperclip")
		}
		for row, m := range l.rows {
			index := l.model.Index(row, colAttachment, noParent)
			if got := decorated(l.model.Data(index, int(qt.DecorationRole))); got != m.Attachments {
				t.Errorf("message %d, with attachments %v, has a paperclip: %v", m.UID, m.Attachments, got)
			}
			runtime.KeepAlive(index)
			index = l.model.Index(row, colSubject, noParent)
			if decorated(l.model.Data(index, int(qt.DecorationRole))) {
				t.Errorf("message %d has a paperclip by its subject", m.UID)
			}
			runtime.KeepAlive(index)
		}
	})
}

// TestFlagsUpdatedInPlace checks that when only the flags of the messages
// have changed the rows are changed where they are, without the list being
// made again, and the selection and current message are kept.
func TestFlagsUpdatedInPlace(t *testing.T) {
	ix := folderOf(100)
	withList(func(l *messageList) {
		l.setIndex(ix, false)
		resets := 0
		l.model.OnModelReset(func() { resets++ })
		before := slices.Clone(l.rows)
		clickRow(l, 5)
		addRow(l, 7)
		selected := l.selectedUIDs()

		changed := *ix
		changed.Messages = slices.Clone(ix.Messages)
		uid := l.rows[3].UID
		i, _ := changed.Find(uid)
		changed.Messages[i].Flags = []string{`\Flagged`}
		l.setIndex(&changed, false)

		if resets != 0 {
			t.Errorf("the list was made again %d times", resets)
		}
		for i := range l.rows {
			if l.rows[i].UID != before[i].UID {
				t.Fatalf("row %d moved", i)
			}
		}
		if l.rows[3].Has(`\Seen`) || !l.rows[3].Has(`\Flagged`) {
			t.Errorf("row 3 = %+v", l.rows[3])
		}
		if got := l.selectedUIDs(); !slices.Equal(got, selected) || l.CurrentIndex().Row() != 5 {
			t.Errorf("selected %v, was %v; current row %d", got, selected, l.CurrentIndex().Row())
		}

		// A new message makes the list again.
		changed.Messages = append(changed.Messages, store.Summary{UID: 1000, Subject: "new"})
		l.setIndex(&changed, false)
		if resets != 1 || len(l.rows) != 101 {
			t.Errorf("%d resets, %d rows", resets, len(l.rows))
		}
	})
}

// TestAttachmentsUpdatedInPlace checks that a message found to have
// attachments, as those of an index saved before it said are, has its row
// drawn again where it is, or the list sorted again if it is sorted by them.
func TestAttachmentsUpdatedInPlace(t *testing.T) {
	ix := folderOf(20)
	for i := range ix.Messages {
		ix.Messages[i].Attachments = false
	}
	withList(func(l *messageList) {
		l.setIndex(ix, false)
		resets, drawn := 0, -1
		l.model.OnModelReset(func() { resets++ })
		l.model.OnDataChanged(func(from, to *qt.QModelIndex) {
			if from.Row() == to.Row() {
				drawn = from.Row()
			}
		})
		changed := *ix
		changed.Messages = slices.Clone(ix.Messages)
		uid := l.rows[4].UID
		i, _ := changed.Find(uid)
		changed.Messages[i].Attachments = true
		l.setIndex(&changed, false)
		if resets != 0 || drawn != 4 || !l.rows[4].Attachments {
			t.Errorf("%d resets, row %d drawn, row 4 %+v", resets, drawn, l.rows[4])
		}

		// Sorted by attachments, the message goes to the top.
		l.SortByColumn(colAttachment, qt.DescendingOrder)
		again := changed
		again.Messages = slices.Clone(changed.Messages)
		j, _ := again.Find(l.rows[10].UID)
		again.Messages[j].Attachments = true
		moved := l.rows[10].UID
		l.setIndex(&again, false)
		if top := []uint32{l.rows[0].UID, l.rows[1].UID}; !slices.Contains(top, moved) || !slices.Contains(top, uid) {
			t.Errorf("sorted by attachments, the top is %v, not %d and %d", top, uid, moved)
		}
	})
}

// TestLargeSelectionKept checks that a selection of every message of a
// large folder is kept, and quickly, when the list is made again.
func TestLargeSelectionKept(t *testing.T) {
	ix := folderOf(20000)
	withList(func(l *messageList) {
		l.setIndex(ix, false)
		l.SelectAll()
		start := time.Now()
		more := *ix
		more.Messages = append(slices.Clone(ix.Messages), store.Summary{UID: 30000, Subject: "new"})
		l.setIndex(&more, false)
		if took := time.Since(start); took > 5*time.Second {
			t.Errorf("making the list again took %v", took)
		}
		if got := len(l.selectedUIDs()); got != 20000 {
			t.Errorf("%d selected", got)
		}
	})
}

// TestSelectionBeyondMiqt checks that a selection larger than miqt can hand
// over as a list of indexes (65,535), with the garbage collector running,
// which frees the indexes miqt's lists are made of, is read and dragged
// whole. It crashed before.
func TestSelectionBeyondMiqt(t *testing.T) {
	ix := folderOf(70000)
	withList(func(l *messageList) {
		l.account, l.folder = "account", "INBOX"
		l.setIndex(ix, false)
		l.SelectAll()
		runtime.GC()
		uids := l.selectedUIDs()
		runtime.GC()
		if len(uids) != 70000 {
			t.Fatalf("%d selected", len(uids))
		}
		for i, uid := range uids {
			if uid != l.rows[i].UID {
				t.Fatalf("selected %d is %d, row %d is %d", i, uid, i, l.rows[i].UID)
			}
		}
		d, ok := decodeDragged(l.draggedSelection().encode())
		if !ok || len(d.UIDs) != 70000 || d.Folder != "INBOX" {
			t.Errorf("dragged %d from %q, %v", len(d.UIDs), d.Folder, ok)
		}
	})
}

// TestSelectionFollows checks that the selection the list keeps follows the
// user's, a new list, and messages that go.
func TestSelectionFollows(t *testing.T) {
	ix := folderOf(10)
	withList(func(l *messageList) {
		l.setIndex(ix, false)
		if got := l.selectedUIDs(); len(got) != 0 {
			t.Errorf("selected %v at first", got)
		}
		clickRow(l, 2)
		if got := l.selectedUIDs(); !slices.Equal(got, []uint32{l.rows[2].UID}) {
			t.Errorf("selected %v", got)
		}
		addRow(l, 4)
		want := []uint32{l.rows[2].UID, l.rows[4].UID}
		if got := l.selectedUIDs(); !slices.Equal(got, want) {
			t.Errorf("selected %v, want %v", got, want)
		}
		// What is returned is the caller's to change.
		l.selectedUIDs()[0] = 0
		if got := l.selectedUIDs(); !slices.Equal(got, want) {
			t.Errorf("selected %v after changing a copy", got)
		}

		// A message that was selected goes.
		gone := l.rows[4].UID
		less := &store.Index{Folder: ix.Folder, UIDValidity: ix.UIDValidity}
		for _, m := range ix.Messages {
			if m.UID != gone {
				less.Messages = append(less.Messages, m)
			}
		}
		l.setIndex(less, false)
		if got := l.selectedUIDs(); !slices.Equal(got, want[:1]) {
			t.Errorf("selected %v once %d went", got, gone)
		}
		l.clear()
		if got := l.selectedUIDs(); len(got) != 0 {
			t.Errorf("selected %v once cleared", got)
		}
	})
}

// benchmarkSetIndex measures the list being given a folder of 100,000
// messages, sorted by the column.
func benchmarkSetIndex(b *testing.B, col int) {
	ix := folderOf(100000)
	withList(func(l *messageList) {
		l.sortColumn = col
		for b.Loop() {
			l.clear()
			l.setIndex(ix, false)
		}
	})
}

func BenchmarkSetIndexByDate100k(b *testing.B)   { benchmarkSetIndex(b, colDate) }
func BenchmarkSetIndexBySender100k(b *testing.B) { benchmarkSetIndex(b, colCorrespondent) }

// BenchmarkFlagChange100k measures one message of a folder of 100,000
// being marked read.
func BenchmarkFlagChange100k(b *testing.B) {
	ix := folderOf(100000)
	withList(func(l *messageList) {
		l.setIndex(ix, false)
		changed := *ix
		changed.Messages = slices.Clone(ix.Messages)
		seen := false
		for b.Loop() {
			seen = !seen
			if seen {
				changed.Messages[500].Flags = nil
			} else {
				changed.Messages[500].Flags = []string{`\Seen`}
			}
			l.setIndex(&changed, false)
		}
	})
}
