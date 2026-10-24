package ui

import (
	"slices"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/mappu/miqt/qt6/mainthread"

	"github.com/nomad-software/apemail/internal/account"
	"github.com/nomad-software/apemail/internal/remote/remotetest"
)

// TestFoldersShownByUse checks that the side bar shows a folder for a use
// by the use, whatever the server calls it, so that every account looks
// the same: the archive the account's settings choose, and the sent mail
// found by its usual name. It has their place among the folders, and the
// folders within it stay below it.
func TestFoldersShownByUse(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Archives", "Archives/2026", "Old mail", "Sent Items", "Work")
	w := newTestWindow(t, s, func(a *account.Account) { a.Folders.Archive = "Old mail" })
	wait(t, "the folders", func() bool {
		return mainthread.Wait2(func() bool { return w.folders.folders[w.account]["Work"] != nil })
	})
	mainthread.Wait(func() {
		items := w.folders.folders[w.account]
		for folder, want := range map[string]string{"Old mail": "Archive", "Sent Items": "Sent", "Archives": "Archives", "Archives/2026": "2026", "Work": "Work"} {
			if got := items[folder].Text(0); got != want {
				t.Errorf("%s is shown as %q, not %q", folder, got, want)
			}
		}
		if got := items["Old mail"].ToolTip(0); got != "Old mail" {
			t.Errorf("the archive's tool tip is %q", got)
		}
		if got := w.folders.folderName(w.account, "Old mail"); got != "Archive" {
			t.Errorf("the archive is named %q above the list", got)
		}
		var order []string
		account := w.folders.accounts[w.account]
		for i := range account.ChildCount() {
			order = append(order, account.Child(i).Text(0))
		}
		if !slices.Equal(order, []string{"Inbox", "Sent", "Archive", "Archives", "Work"}) {
			t.Errorf("the folders are in the order %q", order)
		}
	})
}
