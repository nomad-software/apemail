package ui

import (
	"slices"
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"
	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/mainthread"

	"github.com/nomad-software/apemail/internal/remote"
	"github.com/nomad-software/apemail/internal/remote/remotetest"
)

// asked has confirm give the answer until the test ends, without a dialog
// being shown, and returns the questions it is asked.
func asked(t *testing.T, yes bool) *[]string {
	var questions []string
	old := confirm
	confirm = func(_ *qt.QWidget, _, text string) bool {
		questions = append(questions, text)
		return yes
	}
	t.Cleanup(func() { confirm = old })
	return &questions
}

// menuAction returns the action of the menu with the text, or nil.
func menuAction(menu *qt.QMenu, text string) *qt.QAction {
	for _, a := range menu.Actions() {
		if a.Text() == text {
			return a
		}
	}
	return nil
}

// trigger triggers the action of the side bar's menu for the account, if
// name is empty, or the folder, and then deletes the menu.
func trigger(t *testing.T, w *Window, name, text string) {
	t.Helper()
	mainthread.Wait(func() {
		folder := name
		menu := w.sidebarMenu(w.account, name, folder)
		defer menu.DeleteLater()
		a := menuAction(menu, text)
		if a == nil {
			t.Fatalf("the menu of %q has no %q", name, text)
		}
		a.Trigger()
	})
}

// onServer returns the names of the folders on the server.
func onServer(t *testing.T, s *remotetest.Server) []string {
	t.Helper()
	list, err := s.Client(t).List("", "*", nil).Collect()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, l := range list {
		names = append(names, l.Mailbox)
	}
	slices.Sort(names)
	return names
}

func TestSidebarMenus(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Trash", "Work")
	w := newTestWindow(t, s)
	wait(t, "the folders", func() bool { return w.folders.folders[w.account]["Work"] != nil })
	mainthread.Wait(func() {
		account := w.sidebarMenu(w.account, "", "")
		defer account.DeleteLater()
		for _, text := range []string{"New Folder…", "Remove Account…", "Account Settings…"} {
			if menuAction(account, text) == nil {
				t.Errorf("the account's menu has no %q", text)
			}
		}
		if menuAction(account, "Delete Folder…") != nil {
			t.Error("the account's menu has Delete Folder")
		}
		for folder, enabled := range map[string]bool{"Work": true, "INBOX": false, "Trash": false} {
			menu := w.sidebarMenu(w.account, folder, folder)
			del := menuAction(menu, "Delete Folder…")
			if del == nil || del.IsEnabled() != enabled {
				t.Errorf("%s: Delete Folder is %v", folder, del != nil && del.IsEnabled())
			}
			for _, text := range []string{"New Folder…", "Remove Account…"} {
				if menuAction(menu, text) != nil {
					t.Errorf("%s: the folder's menu has %q", folder, text)
				}
			}
			menu.DeleteLater()
		}
	})
}

// TestDeleteFolderAsks checks that deleting a folder from its menu asks
// first, saying how many messages it and the folders within it hold, and
// that nothing is deleted if the user says no.
func TestDeleteFolderAsks(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Projects", "Projects/Engine", "Work")
	s.Append(t, "Projects", "a")
	s.Append(t, "Projects/Engine", "b")
	s.Append(t, "Projects/Engine", "c")
	s.Append(t, "Work", "d")
	w := newTestWindow(t, s)
	wait(t, "the folders", func() bool { return w.folders.folders[w.account]["Projects"] != nil })

	questions := asked(t, false)
	trigger(t, w, "Projects", "Delete Folder…")
	wait(t, "the question", func() bool { return len(*questions) == 1 })
	q := (*questions)[0]
	for _, want := range []string{"Projects", "It holds 1 message", "Projects/Engine: 2 messages", "3 messages in all", "cannot be undone"} {
		if !strings.Contains(q, want) {
			t.Errorf("the question does not say %q:\n%s", want, q)
		}
	}
	if got := onServer(t, s); !slices.Equal(got, []string{"INBOX", "Projects", "Projects/Engine", "Work"}) {
		t.Errorf("the server has %q", got)
	}
	if msgs := s.Messages(t, "Projects/Engine"); len(msgs) != 2 {
		t.Errorf("Projects/Engine = %+v", msgs)
	}
	if w := warnings(); w != "" {
		t.Errorf("warned%s", w)
	}
}

// TestDeleteFolderConfirmed checks that a folder is deleted, with those
// within it, once the user agrees, that the side bar no longer shows them,
// and that the other folders and their mail are left alone.
func TestDeleteFolderConfirmed(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Projects", "Projects/Engine", "Work")
	s.Append(t, "Projects/Engine", "b")
	s.Append(t, "Work", "d")
	w := newTestWindow(t, s)
	wait(t, "the folders", func() bool { return w.folders.folders[w.account]["Projects"] != nil })
	// The folder is open, and goes from the list with it.
	mainthread.Wait(func() { w.folders.selectFolder(w.account, "Projects/Engine") })
	wait(t, "the folder to open", func() bool { return w.folder == "Projects/Engine" })

	questions := asked(t, true)
	trigger(t, w, "Projects", "Delete Folder…")
	wait(t, "the folders to go", func() bool {
		items := w.folders.folders[w.account]
		return items["Projects"] == nil && items["Projects/Engine"] == nil
	})
	if len(*questions) != 1 {
		t.Errorf("asked %d times", len(*questions))
	}
	if got := onServer(t, s); !slices.Equal(got, []string{"INBOX", "Work"}) {
		t.Errorf("the server has %q", got)
	}
	if msgs := s.Messages(t, "Work"); len(msgs) != 1 {
		t.Errorf("Work = %+v", msgs)
	}
	if mainthread.Wait2(func() bool { return w.folder == "Projects/Engine" }) {
		t.Error("the folder deleted is still listed")
	}
}

func TestNewFolder(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Work")
	w := newTestWindow(t, s)
	wait(t, "the folders", func() bool { return w.folders.folders[w.account]["Work"] != nil })
	trigger(t, w, "", "New Folder…")

	for _, c := range []struct{ name, within, want string }{
		{"Projects", topLevel, "Projects"},
		{"2026", "Work", "Work/2026"},
	} {
		mainthread.Wait(func() {
			d := w.newFolderDialog(w.engines[w.account])
			d.name.SetText(c.name)
			d.within.SetCurrentIndex(d.within.FindText(c.within))
			d.ok()
		})
		wait(t, "the folder "+c.want, func() bool { return w.folders.folders[w.account][c.want] != nil })
	}
	if got := onServer(t, s); !slices.Equal(got, []string{"INBOX", "Projects", "Work", "Work/2026"}) {
		t.Errorf("the server has %q", got)
	}

	// A folder there already is warned of, and not made again.
	mainthread.Wait(func() {
		d := w.newFolderDialog(w.engines[w.account])
		d.name.SetText("Work")
		d.ok()
	})
	eventually(t, "the warning", func() bool { return strings.Contains(warnings(), "already") })
}

// TestRemoveAccountFromMenu checks that the account's menu removes it only
// once the user agrees, and leaves the server alone.
func TestRemoveAccountFromMenu(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Work")
	s.Append(t, "Work", "d")
	w := newTestWindow(t, s)
	wait(t, "the folders", func() bool { return w.folders.folders[w.account]["Work"] != nil })
	id := mainthread.Wait2(func() string { return w.account })

	questions := asked(t, false)
	trigger(t, w, "", "Remove Account…")
	if len(*questions) != 1 || !strings.Contains((*questions)[0], "not touched") {
		t.Errorf("asked %q", *questions)
	}
	if mainthread.Wait2(func() bool { return w.engines[id] == nil }) {
		t.Fatal("the account was removed when the user said no")
	}

	asked(t, true)
	trigger(t, w, "", "Remove Account…")
	if !mainthread.Wait2(func() bool { return w.engines[id] == nil && w.folders.accounts[id] == nil }) {
		t.Error("the account was not removed")
	}
	if got := onServer(t, s); !slices.Equal(got, []string{"INBOX", "Work"}) {
		t.Errorf("the server has %q", got)
	}
	if msgs := s.Messages(t, "Work"); len(msgs) != 1 {
		t.Errorf("Work = %+v", msgs)
	}
}

func TestDeleteFolderText(t *testing.T) {
	for _, c := range []struct {
		contents []remote.FolderStatus
		want     []string
	}{
		{[]remote.FolderStatus{{Name: "Old", Selectable: true}},
			[]string{"Delete the folder Old from the server for good?", "It is empty. This cannot be undone."}},
		{[]remote.FolderStatus{{Name: "Old", Selectable: true, Messages: 1}},
			[]string{"It holds 1 message, which will be deleted with it. This cannot be undone."}},
		{[]remote.FolderStatus{
			{Name: "P/b/x", Selectable: true, Messages: 4},
			{Name: "P/a", Selectable: true, Messages: 2},
			{Name: "P/b", Selectable: true},
			{Name: "P"},
		}, []string{
			"It holds no mail of its own, and within it are 3 folders:\n    P/a: 2 messages\n    P/b: 0 messages\n    P/b/x: 4 messages\n",
			"6 messages in all will be deleted, with the 4 folders. This cannot be undone.",
		}},
	} {
		got := deleteFolderText(c.contents)
		for _, want := range c.want {
			if !strings.Contains(got, want) {
				t.Errorf("%q does not say %q", got, want)
			}
		}
	}

	// Only so many folders within are named.
	var many []remote.FolderStatus
	for i := range maxListed + 3 {
		many = append(many, remote.FolderStatus{Name: "P/" + string(rune('a'+i)), Selectable: true, Messages: 1})
	}
	many = append(many, remote.FolderStatus{Name: "P", Selectable: true})
	got := deleteFolderText(many)
	if !strings.Contains(got, "and 3 more") || strings.Contains(got, "P/m:") || !strings.Contains(got, "13 messages in all") {
		t.Errorf("%q", got)
	}
}
