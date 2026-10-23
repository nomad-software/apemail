package ui

import (
	"slices"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/mappu/miqt/qt6/mainthread"

	"github.com/nomad-software/apemail/internal/remote/remotetest"
	"github.com/nomad-software/apemail/internal/store"
)

// threeMessages puts First, Second and Third in the folder, which the list
// shows newest first: Third, Second, First.
func threeMessages(t *testing.T, s *remotetest.Server, folder string) {
	t.Helper()
	day := time.Date(2006, 1, 2, 15, 4, 5, 0, time.UTC)
	for i, subject := range []string{"First", "Second", "Third"} {
		s.AppendDated(t, folder, subject, day.Add(time.Duration(i)*24*time.Hour))
	}
}

// selectRow selects the row of the list and waits for its message to be
// shown.
func selectRow(t *testing.T, w *Window, row int) {
	t.Helper()
	mainthread.Wait(func() { clickRow(w.list, row) })
	wait(t, "the message", func() bool { return w.view.msg != nil })
}

// shownAfter waits for the list to have the subjects, and returns the
// subject of the message shown, if one is, once it is.
func shownAfter(t *testing.T, w *Window, subjects ...string) string {
	t.Helper()
	wait(t, "the list", func() bool {
		var got []string
		for _, r := range w.list.rows {
			got = append(got, r.Subject)
		}
		return slices.Equal(got, subjects)
	})
	shown := ""
	wait(t, "what is shown", func() bool {
		uids := w.list.selectedUIDs()
		if len(uids) == 0 {
			return w.view.msg == nil
		}
		if len(uids) != 1 || w.view.msg == nil || w.view.uid != uids[0] {
			return false
		}
		shown = w.view.msg.Subject
		return true
	})
	return shown
}

// TestDeleteSelectsNext checks that once a message is deleted the one after
// it is selected and shown, so that pressing Delete again deletes it, and
// that once there is none after, the one before is.
func TestDeleteSelectsNext(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Trash")
	threeMessages(t, s, "INBOX")
	w := newTestWindow(t, s)
	wait(t, "the inbox", func() bool { return len(w.list.rows) == 3 && w.folder == "INBOX" })
	asked := answer(t, false)

	selectRow(t, w, 1)
	mainthread.Wait(w.deleteMessages)
	if got := shownAfter(t, w, "Third", "First"); got != "First" {
		t.Errorf("shown %q after deleting Second", got)
	}
	mainthread.Wait(w.deleteMessages)
	if got := shownAfter(t, w, "Third"); got != "Third" {
		t.Errorf("shown %q after deleting First", got)
	}
	mainthread.Wait(w.deleteMessages)
	if got := shownAfter(t, w); got != "" {
		t.Errorf("shown %q with nothing left", got)
	}
	if *asked != 0 {
		t.Errorf("asked %d times", *asked)
	}
	if msgs := s.Messages(t, "Trash"); len(msgs) != 3 {
		t.Errorf("trash = %+v", msgs)
	}
}

// TestDeleteForGoodSelectsNext checks the same in the trash, where the user
// is asked, and that nothing else is selected if they say no.
func TestDeleteForGoodSelectsNext(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Trash")
	threeMessages(t, s, "Trash")
	w := newTestWindow(t, s)
	wait(t, "the inbox", func() bool { return w.folder == "INBOX" })
	mainthread.Wait(func() { w.folders.selectFolder(w.order[0], "Trash") })
	wait(t, "the trash", func() bool { return w.folder == "Trash" && len(w.list.rows) == 3 })

	selectRow(t, w, 0)
	answer(t, false)
	mainthread.Wait(w.deleteMessages)
	if got := shownAfter(t, w, "Third", "Second", "First"); got != "Third" {
		t.Errorf("shown %q after saying no", got)
	}
	if w.list.next != 0 {
		t.Error("a message is to be selected after saying no")
	}

	answer(t, true)
	mainthread.Wait(w.deleteMessages)
	if got := shownAfter(t, w, "Second", "First"); got != "Second" {
		t.Errorf("shown %q after deleting Third", got)
	}
	if msgs := s.Messages(t, "Trash"); len(msgs) != 2 {
		t.Errorf("trash = %+v", msgs)
	}
}

// TestArchiveSelectsNext checks that archiving selects the next message as
// deleting does.
func TestArchiveSelectsNext(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Archive")
	threeMessages(t, s, "INBOX")
	w := newTestWindow(t, s)
	wait(t, "the inbox", func() bool { return len(w.list.rows) == 3 && w.folder == "INBOX" })

	// Two selected together: the one after the last of them is next.
	selectRow(t, w, 0)
	mainthread.Wait(func() {
		addRow(w.list, 1)
	})
	wait(t, "two selected", func() bool { return len(w.list.selectedUIDs()) == 2 })
	mainthread.Wait(w.archiveMessages)
	if got := shownAfter(t, w, "First"); got != "First" {
		t.Errorf("shown %q after archiving", got)
	}
}

// TestChosenSelectionIsKept checks that a message the user chooses while a
// delete is on its way is kept, rather than the one after those deleted.
func TestChosenSelectionIsKept(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Trash")
	threeMessages(t, s, "INBOX")
	w := newTestWindow(t, s)
	wait(t, "the inbox", func() bool { return len(w.list.rows) == 3 && w.folder == "INBOX" })

	selectRow(t, w, 0)
	mainthread.Wait(func() {
		w.list.selectNextLater()
		if w.list.next != w.list.rows[1].UID {
			t.Errorf("next = %d", w.list.next)
		}
		clickRow(w.list, 2)
		if w.list.next != 0 {
			t.Error("the next message is kept after another was chosen")
		}
	})
}

// TestFoldersListedAgainKeepSelection checks that listing the folders again,
// as archiving into a folder made for it does, leaves the folder and the
// message shown as they were, and that a folder that has gone is no longer
// listed.
func TestFoldersListedAgainKeepSelection(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Projects")
	threeMessages(t, s, "INBOX")
	w := newTestWindow(t, s)
	wait(t, "the inbox", func() bool { return len(w.list.rows) == 3 && w.folder == "INBOX" })
	selectRow(t, w, 1)

	mainthread.Wait(func() {
		id := w.order[0]
		folders := w.engines[id].Folders()
		w.FoldersChanged(id, folders)
		if w.folder != "INBOX" || len(w.list.rows) != 3 || w.view.msg == nil || w.view.msg.Subject != "Second" {
			t.Errorf("after listing again: folder %q, %d rows, shown %v", w.folder, len(w.list.rows), w.view.msg != nil)
		}
		if uids := w.list.selectedUIDs(); len(uids) != 1 || uids[0] != w.list.rows[1].UID {
			t.Errorf("selected %v", uids)
		}

		// Without Projects, which is selected, the account is.
		w.folders.selectFolder(id, "Projects")
		var without []store.Folder
		for _, f := range folders {
			if f.Name != "Projects" {
				without = append(without, f)
			}
		}
		w.FoldersChanged(id, without)
		if w.account != id || w.folder != "" {
			t.Errorf("listing %q of %q after its folder went", w.folder, w.account)
		}
	})
}
