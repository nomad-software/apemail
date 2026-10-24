package engine_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"

	"github.com/nomad-software/apemail/internal/account"
	"github.com/nomad-software/apemail/internal/engine"
	"github.com/nomad-software/apemail/internal/remote"
	"github.com/nomad-software/apemail/internal/remote/remotetest"
	"github.com/nomad-software/apemail/internal/store"
)

// serverFolders returns the names of the folders on the server, as another
// client lists them.
func serverFolders(t *testing.T, s *remotetest.Server) []string {
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

// listedFolders returns the names of the folders the engine lists.
func listedFolders(e *engine.Engine) []string {
	var names []string
	for _, f := range e.Folders() {
		names = append(names, f.Name)
	}
	slices.Sort(names)
	return names
}

// folderContents returns what FolderContents gives for the folder.
func folderContents(t *testing.T, e *engine.Engine, folder string) ([]remote.FolderStatus, error) {
	t.Helper()
	type answer struct {
		contents []remote.FolderStatus
		err      error
	}
	got := make(chan answer, 1)
	e.FolderContents(folder, func(c []remote.FolderStatus, err error) { got <- answer{c, err} })
	a := recv(t, got)
	return a.contents, a.err
}

// sent reports if a command with the name was sent.
func sent(commands []string, name string) bool {
	return slices.ContainsFunc(commands, func(c string) bool { return strings.EqualFold(c, name) })
}

// started returns an engine of an account on the server whose folders have
// been listed.
func started(t *testing.T, s *remotetest.Server, change ...func(*account.Account)) (*engine.Engine, *listener, *store.Store) {
	t.Helper()
	e, l, st := start(t, s, change...)
	e.Refresh("")
	wait(t, "the folders", func() bool { return len(e.Folders()) > 0 })
	return e, l, st
}

func TestCreateFolder(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Work")
	e, _, _ := started(t, s)

	if err := result(t, func(done func(error)) { e.CreateFolder("", " Projects ", done) }); err != nil {
		t.Fatal(err)
	}
	if err := result(t, func(done func(error)) { e.CreateFolder("Work", "2026", done) }); err != nil {
		t.Fatal(err)
	}
	want := []string{"INBOX", "Projects", "Work", "Work/2026"}
	if got := serverFolders(t, s); !slices.Equal(got, want) {
		t.Errorf("the server has %q", got)
	}
	// They are listed at once, not when the folders are next listed.
	if got := listedFolders(e); !slices.Equal(got, want) {
		t.Errorf("the engine lists %q", got)
	}

	// What cannot be made is not asked for.
	s.Commands()
	for _, c := range []struct{ parent, name string }{
		{"", ""},
		{"", "  "},
		{"", "Work"},
		{"Work", "2026"},
		{"", "A/B"},
	} {
		if err := result(t, func(done func(error)) { e.CreateFolder(c.parent, c.name, done) }); err == nil {
			t.Errorf("%q within %q was made", c.name, c.parent)
		}
	}
	if sent(s.Commands(), "CREATE") {
		t.Error("the server was asked to make a folder it should not have been")
	}
	if got := serverFolders(t, s); !slices.Equal(got, want) {
		t.Errorf("the server has %q", got)
	}
}

// TestDeleteFolder checks that a folder is deleted with the folders within
// it and their mail, the deepest first, and that the folders beside it, one
// whose name starts the same included, and their mail are left alone.
func TestDeleteFolder(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove),
		"Projects", "Projects/Engine", "Projects/Engine/Old", "Projects/Mill", "Projectsx", "Work")
	s.Append(t, "Projects", "a")
	s.Append(t, "Projects/Engine", "b")
	s.Append(t, "Projects/Engine", "c")
	s.Append(t, "Projects/Engine/Old", "d")
	s.Append(t, "Projectsx", "e")
	s.Append(t, "Work", "f")
	e, l, st := started(t, s)

	// A message downloaded is kept, and is to be thrown away with its
	// folder.
	e.Open("Projects/Engine")
	wait(t, "the folder", func() bool { ix := l.index("Projects/Engine"); return ix != nil && len(ix.Messages) == 2 })
	uid := l.index("Projects/Engine").Messages[0].UID
	got := make(chan error, 1)
	e.Message("Projects/Engine", uid, func(_ []byte, err error) { got <- err })
	if err := recv(t, got); err != nil {
		t.Fatal(err)
	}
	if !kept(st, "Projects/Engine", uid) {
		t.Fatal("the message was not kept")
	}

	contents, err := folderContents(t, e, "Projects")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	counts := map[string]uint32{}
	for _, c := range contents {
		names = append(names, c.Name)
		counts[c.Name] = c.Messages
	}
	// The deepest first, and the folder itself last.
	if !slices.Equal(names, []string{"Projects/Engine/Old", "Projects/Engine", "Projects/Mill", "Projects"}) {
		t.Errorf("the folders are %q", names)
	}
	if counts["Projects"] != 1 || counts["Projects/Engine"] != 2 || counts["Projects/Engine/Old"] != 1 || counts["Projects/Mill"] != 0 {
		t.Errorf("the counts are %v", counts)
	}

	s.Commands()
	if err := result(t, func(done func(error)) { e.DeleteFolder(contents, done) }); err != nil {
		t.Fatal(err)
	}
	want := []string{"INBOX", "Projectsx", "Work"}
	if got := serverFolders(t, s); !slices.Equal(got, want) {
		t.Errorf("the server has %q", got)
	}
	if got := listedFolders(e); !slices.Equal(got, want) {
		t.Errorf("the engine lists %q", got)
	}
	if msgs := s.Messages(t, "Projectsx"); len(msgs) != 1 {
		t.Errorf("Projectsx = %+v", msgs)
	}
	if msgs := s.Messages(t, "Work"); len(msgs) != 1 {
		t.Errorf("Work = %+v", msgs)
	}
	if kept(st, "Projects/Engine", uid) {
		t.Error("the local copy of the folder was kept")
	}
	// No message was expunged, from these or any other folder.
	commands := s.Commands()
	for _, c := range []string{"EXPUNGE", "UID EXPUNGE", "CLOSE", "STORE", "UID STORE"} {
		if sent(commands, c) {
			t.Errorf("%s was sent: %q", c, commands)
		}
	}
	deletes := 0
	for _, c := range commands {
		if c == "DELETE" {
			deletes++
		}
	}
	if deletes != 4 {
		t.Errorf("%d folders were deleted, not 4: %q", deletes, commands)
	}
}

// TestDeleteFolderLeavesItOpen checks that the folder the connection has
// open is left before it is deleted, by opening the inbox read-only, and
// never by CLOSE, which would expunge it.
func TestDeleteFolderLeavesItOpen(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Old")
	s.Append(t, "Old", "a")
	e, l, _ := started(t, s)
	e.Open("Old")
	wait(t, "the folder", func() bool { ix := l.index("Old"); return ix != nil && len(ix.Messages) == 1 })
	// Setting a flag has the connection open the folder.
	if err := result(t, func(done func(error)) {
		e.SetFlag("Old", []uint32{l.index("Old").Messages[0].UID}, string(imap.FlagFlagged), true, done)
	}); err != nil {
		t.Fatal(err)
	}

	contents, err := folderContents(t, e, "Old")
	if err != nil {
		t.Fatal(err)
	}
	s.Commands()
	if err := result(t, func(done func(error)) { e.DeleteFolder(contents, done) }); err != nil {
		t.Fatal(err)
	}
	// It is counted again, left for the inbox, and deleted.
	if commands := s.Commands(); !slices.Equal(commands, []string{"STATUS", "EXAMINE", "DELETE"}) {
		t.Errorf("the commands were %q", commands)
	}
	if got := serverFolders(t, s); !slices.Equal(got, []string{"INBOX"}) {
		t.Errorf("the server has %q", got)
	}
}

// TestDeleteFolderChanged checks that nothing is deleted if the folder has
// changed since the user was shown what it holds: a message has arrived or
// gone, or a folder has been made within it.
func TestDeleteFolderChanged(t *testing.T) {
	for name, change := range map[string]func(t *testing.T, s *remotetest.Server){
		"arrived": func(t *testing.T, s *remotetest.Server) { s.Append(t, "Projects/Engine", "new") },
		"made": func(t *testing.T, s *remotetest.Server) {
			if err := s.Client(t).Create("Projects/New", nil).Wait(); err != nil {
				t.Fatal(err)
			}
		},
		"gone": func(t *testing.T, s *remotetest.Server) {
			c := s.Client(t)
			if _, err := c.Select("Projects", nil).Wait(); err != nil {
				t.Fatal(err)
			}
			if _, err := c.Move(imap.SeqSetNum(1), "INBOX").Wait(); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Projects", "Projects/Engine")
			s.Append(t, "Projects", "a")
			s.Append(t, "Projects/Engine", "b")
			e, _, _ := started(t, s)
			contents, err := folderContents(t, e, "Projects")
			if err != nil {
				t.Fatal(err)
			}
			before := serverFolders(t, s)

			change(t, s)
			s.Commands()
			if err := result(t, func(done func(error)) { e.DeleteFolder(contents, done) }); err == nil {
				t.Fatal("the folder was deleted")
			}
			if sent(s.Commands(), "DELETE") {
				t.Error("the server was asked to delete a folder")
			}
			if got := serverFolders(t, s); !slices.Equal(got, before) && name != "made" {
				t.Errorf("the server has %q", got)
			}
			if msgs := s.Messages(t, "Projects/Engine"); len(msgs) == 0 {
				t.Error("the mail within was deleted")
			}
		})
	}
}

// TestDeleteFolderKept checks that the inbox, the folders the account uses,
// and those that hold one, are not deleted, nor asked about.
func TestDeleteFolderKept(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove),
		"Trash", "Mail", "Mail/Kept", "Projects")
	e, _, _ := started(t, s, func(a *account.Account) { a.Folders.Archive = "Mail/Kept" })
	for _, folder := range []string{"INBOX", "Trash", "Mail/Kept", "Mail"} {
		if _, err := folderContents(t, e, folder); err == nil {
			t.Errorf("%s can be deleted", folder)
		}
		if err := e.Deletable(folder); err == nil && folder != "Mail" {
			t.Errorf("%s is deletable", folder)
		}
	}
	if err := e.Deletable("Projects"); err != nil {
		t.Errorf("Projects is not deletable: %v", err)
	}
	// Even if asked to with what was not given by FolderContents.
	s.Commands()
	for _, folder := range []string{"INBOX", "Trash"} {
		if err := result(t, func(done func(error)) {
			e.DeleteFolder([]remote.FolderStatus{{Name: folder, Selectable: true}}, done)
		}); err == nil {
			t.Errorf("%s was deleted", folder)
		}
	}
	if sent(s.Commands(), "DELETE") {
		t.Error("the server was asked to delete a folder")
	}
	if got := serverFolders(t, s); !slices.Equal(got, []string{"INBOX", "Mail", "Mail/Kept", "Projects", "Trash"}) {
		t.Errorf("the server has %q", got)
	}
}
