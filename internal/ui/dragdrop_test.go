package ui

import (
	"runtime"
	"testing"

	"github.com/emersion/go-imap/v2"
	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/mainthread"

	"github.com/nomad-software/apemail/internal/remote/remotetest"
)

func TestDragToFolder(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Projects")
	s.Append(t, "INBOX", "Plans")
	w := newTestWindow(t, s)
	wait(t, "the inbox", func() bool { return len(w.list.rows) == 1 && w.folder == "INBOX" })

	var d draggedMessages
	mainthread.Wait(func() {
		// What the list gives a drag of the selection says where the
		// message is.
		index := w.list.model.Index(0, 0, noParent)
		if w.list.model.Flags(index)&qt.ItemIsDragEnabled == 0 {
			t.Error("the message cannot be dragged")
		}
		w.list.SetCurrentIndex(index)
		runtime.KeepAlive(index)
		var ok bool
		d, ok = decodeDragged(w.list.draggedSelection().encode())
		if !ok || d.Account != w.account || d.Folder != "INBOX" || len(d.UIDs) != 1 {
			t.Fatalf("dragged = %+v, %v", d, ok)
		}

		// Folders take drops; accounts do not.
		account := w.folders.accounts[w.account]
		if account.Flags()&qt.ItemIsDropEnabled != 0 {
			t.Error("an account takes drops")
		}
		projects := w.folders.folders[w.account]["Projects"]
		if projects.Flags()&qt.ItemIsDropEnabled == 0 {
			t.Error("a folder does not take drops")
		}

		// Nowhere but another folder of the account will do.
		for _, c := range []struct{ id, folder string }{{"other", "Projects"}, {w.account, "INBOX"}, {w.account, ""}} {
			if w.folders.dropped(d, c.id, c.folder) {
				t.Errorf("dropped on %q of %q", c.folder, c.id)
			}
		}
		if !w.folders.dropped(d, w.account, "Projects") {
			t.Error("the drop was refused")
		}
	})
	wait(t, "the message to go", func() bool { return len(w.list.rows) == 0 })
	if msgs := s.Messages(t, "Projects"); len(msgs) != 1 || msgs[0].Subject != "Plans" {
		t.Errorf("projects = %+v", msgs)
	}
}
