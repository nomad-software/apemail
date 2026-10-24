package ui

import (
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/mappu/miqt/qt6/mainthread"

	"github.com/nomad-software/apemail/internal/remote/remotetest"
)

// TestFolderTitle checks that the path of the folder listed, as the side
// bar shows it, and how many messages it has are shown above the list, and
// that nothing is where no folder is listed.
func TestFolderTitle(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Projects", "Projects/Engine", "Trash")
	for _, subject := range []string{"One", "Two", "Three"} {
		s.AppendRaw(t, "INBOX", "From: a@example.com\r\nSubject: "+subject+"\r\n\r\nHello.\r\n")
	}
	s.AppendRaw(t, "Projects/Engine", "From: a@example.com\r\nSubject: Gears\r\n\r\nHello.\r\n")
	w := newTestWindow(t, s)
	title := func() string {
		if !w.folderTitle.IsVisibleTo(w.QWidget) {
			return ""
		}
		return w.folderTitle.Text()
	}
	shows := func(want ...string) func() bool {
		return func() bool {
			got := title()
			for _, part := range want {
				if !strings.Contains(got, part) {
					return false
				}
			}
			return got != ""
		}
	}
	wait(t, "the inbox counted", shows(">Inbox<", "3 messages"))

	// The count follows the messages as they go.
	mainthread.Wait(func() {
		clickRow(w.list, 0)
		w.deleteMessages()
	})
	wait(t, "the inbox counted again", shows(">Inbox<", "2 messages"))

	// A folder within another is named with those it is within, as the
	// side bar names them.
	mainthread.Wait(func() { w.folders.selectFolder(w.account, "Projects/Engine") })
	wait(t, "the folder counted", shows(">Projects/Engine<", "1 message"))
	mainthread.Wait(func() {
		if strings.Contains(title(), "1 messages") {
			t.Errorf("title = %q", title())
		}
	})
	mainthread.Wait(func() { w.folders.selectFolder(w.account, "Trash") })
	wait(t, "the trash counted", shows(">Trash<", "1 message"))

	// The account itself lists nothing.
	mainthread.Wait(func() {
		w.folders.SetCurrentItem(w.folders.accounts[w.account])
		if w.folder != "" {
			t.Fatalf("listing %q", w.folder)
		}
		if w.folderTitle.IsVisibleTo(w.QWidget) {
			t.Errorf("a title is shown with no folder: %q", w.folderTitle.Text())
		}
	})
}

// TestMessageCount checks how a number of messages is said.
func TestMessageCount(t *testing.T) {
	for n, want := range map[int]string{0: "0 messages", 1: "1 message", 2: "2 messages", 100000: "100000 messages"} {
		if got := messageCount(n); got != want {
			t.Errorf("messageCount(%d) = %q", n, got)
		}
	}
}
