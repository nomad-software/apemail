package remote_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"

	"github.com/nomad-software/apemail/internal/remote"
	"github.com/nomad-software/apemail/internal/remote/remotetest"
	"github.com/nomad-software/apemail/internal/store"
)

// dial connects to the server, and closes the connection when the test ends.
func dial(t *testing.T, s *remotetest.Server, writable bool) *remote.Conn {
	t.Helper()
	c, err := remote.Dial(s.Account(), remote.Options{Writable: writable})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// changing are the commands which change a mailbox.
var changing = []string{"SELECT", "STORE", "UID STORE", "EXPUNGE", "UID EXPUNGE", "MOVE", "UID MOVE", "COPY", "UID COPY", "APPEND", "CREATE", "DELETE", "RENAME", "CLOSE"}

// unchanged fails the test if a command which changes a mailbox was sent.
func unchanged(t *testing.T, commands []string) {
	t.Helper()
	for _, c := range commands {
		if slices.Contains(changing, c) {
			t.Errorf("%s was sent, in %v", c, commands)
		}
	}
}

func TestSyncChangesNothing(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(), "Sent")
	s.Append(t, "INBOX", "first")
	s.Append(t, "INBOX", "second", imap.FlagSeen)
	before := s.Messages(t, "INBOX")
	s.Commands()

	c := dial(t, s, true)
	folders, err := c.Folders()
	if err != nil {
		t.Fatal(err)
	}
	if len(folders) != 2 || folders[0].Name != "INBOX" || folders[1].Name != "Sent" {
		t.Errorf("folders = %+v", folders)
	}

	ix := &store.Index{Folder: "INBOX"}
	u, err := c.Sync(ix, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !u.Reset || len(u.Added) != 2 {
		t.Fatalf("update = %+v", u)
	}
	subjects := []string{u.Added[0].Subject, u.Added[1].Subject}
	slices.Sort(subjects)
	if !slices.Equal(subjects, []string{"first", "second"}) {
		t.Errorf("subjects = %v", subjects)
	}

	// Downloading the messages does not mark them as read.
	var got []string
	err = c.Fetch("INBOX", u.UIDValidity, []uint32{u.Added[0].UID, u.Added[1].UID}, func(uid uint32, raw []byte) error {
		got = append(got, string(raw))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !strings.Contains(got[0]+got[1], "The body of first.") {
		t.Errorf("fetched = %q", got)
	}

	commands := s.Commands()
	if !slices.Contains(commands, "EXAMINE") || !slices.Contains(commands, "UID FETCH") {
		t.Errorf("commands = %v", commands)
	}
	unchanged(t, commands)
	after := s.Messages(t, "INBOX")
	if !slices.EqualFunc(before, after, func(a, b remotetest.Message) bool {
		return a.UID == b.UID && slices.Equal(a.Flags, b.Flags)
	}) {
		t.Errorf("the mailbox changed from %+v to %+v", before, after)
	}
}

func TestSyncFindsChanges(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Archive")
	s.Append(t, "INBOX", "first")
	s.Append(t, "INBOX", "second")

	c := dial(t, s, true)
	ix := &store.Index{Folder: "INBOX"}
	u, err := c.Sync(ix, nil)
	if err != nil {
		t.Fatal(err)
	}
	ix.UIDValidity = u.UIDValidity
	for _, m := range u.Added {
		ix.Put(m)
	}

	// Another client reads one, moves the other away and adds a third.
	other := dial(t, s, true)
	first, second := ix.Messages[0].UID, ix.Messages[1].UID
	if err := other.SetFlags("INBOX", ix.UIDValidity, []uint32{first}, true, `\Seen`); err != nil {
		t.Fatal(err)
	}
	if err := other.Move("INBOX", ix.UIDValidity, []uint32{second}, "Archive"); err != nil {
		t.Fatal(err)
	}
	s.Append(t, "INBOX", "third")

	u, err = c.Sync(ix, nil)
	if err != nil {
		t.Fatal(err)
	}
	if u.Reset {
		t.Error("the folder was reset")
	}
	if len(u.Added) != 1 || u.Added[0].Subject != "third" {
		t.Errorf("added = %+v", u.Added)
	}
	if !slices.Equal(u.Removed, []uint32{second}) {
		t.Errorf("removed = %v", u.Removed)
	}
	if !slices.Contains(u.Flags[first], `\Seen`) {
		t.Errorf("flags = %v", u.Flags)
	}
	if archived := s.Messages(t, "Archive"); len(archived) != 1 || archived[0].Subject != "second" {
		t.Errorf("archive = %+v", archived)
	}
}

func TestReadOnly(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Trash")
	s.Append(t, "INBOX", "first")
	c := dial(t, s, false)
	u, err := c.Sync(&store.Index{Folder: "INBOX"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	uids := []uint32{u.Added[0].UID}
	s.Commands()

	checks := map[string]error{
		"flags":         c.SetFlags("INBOX", u.UIDValidity, uids, true, `\Seen`),
		"move":          c.Move("INBOX", u.UIDValidity, uids, "Trash"),
		"create":        c.Create("New", ""),
		"delete folder": c.DeleteFolder("Trash"),
	}
	_, _, checks["append"] = c.Append("INBOX", []byte("Subject: x\r\n\r\nx"), nil)
	for name, err := range checks {
		if !errors.Is(err, remote.ErrReadOnly) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	unchanged(t, s.Commands())
	if msgs := s.Messages(t, "INBOX"); len(msgs) != 1 || len(msgs[0].Flags) != 0 {
		t.Errorf("inbox = %+v", msgs)
	}
}

func TestMoveOnlyExpungesItsOwn(t *testing.T) {
	// Without MOVE, a move is a copy and then an expunge of the
	// originals, by UID.
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus), "Trash")
	s.Append(t, "INBOX", "keep", imap.FlagDeleted)
	s.Append(t, "INBOX", "delete")
	c := dial(t, s, true)
	u, err := c.Sync(&store.Index{Folder: "INBOX"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var uid uint32
	for _, m := range u.Added {
		if m.Subject == "delete" {
			uid = m.UID
		}
	}
	if err := c.Move("INBOX", u.UIDValidity, []uint32{uid}, "Trash"); err != nil {
		t.Fatal(err)
	}
	if msgs := s.Messages(t, "Trash"); len(msgs) != 1 || msgs[0].Subject != "delete" {
		t.Errorf("trash = %+v", msgs)
	}
	// The message another client marked as deleted is still there.
	msgs := s.Messages(t, "INBOX")
	if len(msgs) != 1 || msgs[0].Subject != "keep" {
		t.Errorf("inbox = %+v", msgs)
	}
}

func TestRefusesUnsafeExpunge(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(), "Trash")
	s.Append(t, "INBOX", "first")
	c := dial(t, s, true)
	u, err := c.Sync(&store.Index{Folder: "INBOX"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	uids := []uint32{u.Added[0].UID}
	s.Commands()

	if err := c.Delete("INBOX", u.UIDValidity, uids); err == nil || !strings.Contains(err.Error(), "UIDPLUS") {
		t.Errorf("delete: err = %v", err)
	}
	if err := c.Move("INBOX", u.UIDValidity, uids, "Trash"); err == nil || !strings.Contains(err.Error(), "UIDPLUS") {
		t.Errorf("move: err = %v", err)
	}
	for _, c := range s.Commands() {
		if c != "SELECT" {
			t.Errorf("%s was sent", c)
		}
	}
	if msgs := s.Messages(t, "INBOX"); len(msgs) != 1 {
		t.Errorf("inbox = %+v", msgs)
	}
}

func TestStaleUIDValidity(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Trash")
	s.Append(t, "INBOX", "first")
	c := dial(t, s, true)
	u, err := c.Sync(&store.Index{Folder: "INBOX"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	uids := []uint32{u.Added[0].UID}
	if err := c.Move("INBOX", u.UIDValidity+1, uids, "Trash"); err == nil || !strings.Contains(err.Error(), "numbered anew") {
		t.Errorf("err = %v", err)
	}
	if msgs := s.Messages(t, "INBOX"); len(msgs) != 1 {
		t.Errorf("inbox = %+v", msgs)
	}
}

func TestNoDeletedFlag(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus))
	s.Append(t, "INBOX", "first")
	c := dial(t, s, true)
	u, err := c.Sync(&store.Index{Folder: "INBOX"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetFlags("INBOX", u.UIDValidity, []uint32{u.Added[0].UID}, true, `\deleted`); err == nil {
		t.Error(`\Deleted was set`)
	}
}

func TestCramMD5(t *testing.T) {
	// The example of RFC 2195.
	a := remote.NewCramMD5Client("tim", "tanstaaftanstaaf")
	mech, ir, err := a.Start()
	if mech != "CRAM-MD5" || ir != nil || err != nil {
		t.Fatalf("start = %q, %q, %v", mech, ir, err)
	}
	resp, err := a.Next([]byte("<1896.697170952@postoffice.reston.mci.net>"))
	if err != nil || string(resp) != "tim b913a602c7eda7a495b4e6e7334d3890" {
		t.Errorf("response = %q, %v", resp, err)
	}
}

func TestDeleteOnlyExpungesItsOwn(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus))
	s.Append(t, "INBOX", "keep", imap.FlagDeleted)
	s.Append(t, "INBOX", "delete")
	c := dial(t, s, true)
	u, err := c.Sync(&store.Index{Folder: "INBOX"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var uid uint32
	for _, m := range u.Added {
		if m.Subject == "delete" {
			uid = m.UID
		}
	}
	if err := c.Delete("INBOX", u.UIDValidity, []uint32{uid}); err != nil {
		t.Fatal(err)
	}
	// The message another client marked as deleted is still there.
	if msgs := s.Messages(t, "INBOX"); len(msgs) != 1 || msgs[0].Subject != "keep" {
		t.Errorf("inbox = %+v", msgs)
	}
}

func TestCreate(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(), "Existing")
	c := dial(t, s, true)
	if err := c.Create("Archive", `\Archive`); err != nil {
		t.Fatal(err)
	}
	// A folder that is already there is not an error.
	if err := c.Create("Existing", ""); err != nil {
		t.Errorf("existing: %v", err)
	}
	folders, err := c.Folders()
	if err != nil {
		t.Fatal(err)
	}
	if len(folders) != 3 {
		t.Errorf("folders = %+v", folders)
	}
	if err := dial(t, s, false).Create("Other", ""); !errors.Is(err, remote.ErrReadOnly) {
		t.Errorf("read only: %v", err)
	}
	if c.PersonalPrefix() != "" {
		t.Errorf("prefix = %q", c.PersonalPrefix())
	}
}

// TestUnread checks that the unread messages of a folder are counted
// without the folder being opened or anything changed.
func TestUnread(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(), "Trash")
	s.Append(t, "Trash", "read", imap.FlagSeen)
	s.Append(t, "Trash", "unread")
	s.Append(t, "Trash", "unread too")
	s.Commands()
	c := dial(t, s, false)
	unread, err := c.Unread("Trash", "INBOX")
	if err != nil || unread["Trash"] != 2 || unread["INBOX"] != 0 || len(unread) != 2 {
		t.Errorf("unread = %v, %v", unread, err)
	}
	// A folder that is not there is said to be, and the others are
	// counted all the same.
	unread, err = c.Unread("Missing", "Trash")
	if err == nil || !strings.Contains(err.Error(), "Missing") || unread["Trash"] != 2 {
		t.Errorf("with a missing folder: %v, %v", unread, err)
	}
	commands := s.Commands()
	if slices.Contains(commands, "EXAMINE") || slices.Contains(commands, "SELECT") || !slices.Contains(commands, "STATUS") {
		t.Errorf("commands = %v", commands)
	}
	unchanged(t, commands)
}

// TestSyncWhileAnotherExpunges checks that a sync which asks for every
// message while another client has just expunged one, which the server has
// yet to report, finds only that one gone. Asked for by number, the server
// answers with nothing at all, and every message was taken for gone.
func TestSyncWhileAnotherExpunges(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Trash")
	for _, subject := range []string{"first", "second", "third"} {
		s.Append(t, "INBOX", subject)
	}
	c := dial(t, s, false)
	ix := &store.Index{Folder: "INBOX"}
	u, err := c.Sync(ix, nil)
	if err != nil {
		t.Fatal(err)
	}
	ix.UIDValidity = u.UIDValidity
	for _, m := range u.Added {
		ix.Put(m)
	}
	first := ix.Messages[0].UID

	// The sync waits once it has opened the folder, before it asks for
	// every message, while another client moves the first away.
	held, release := s.Hold(t, "FETCH 1:*")
	done := make(chan struct{})
	go func() {
		defer close(done)
		u, err = c.Sync(ix, nil)
	}()
	<-held
	other := dial(t, s, true)
	if err := other.Move("INBOX", ix.UIDValidity, []uint32{first}, "Trash"); err != nil {
		t.Fatal(err)
	}
	release()
	<-done
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(u.Removed, []uint32{first}) || len(u.Added) != 0 {
		t.Errorf("removed %v, added %+v; only %d went", u.Removed, u.Added, first)
	}
	if len(u.Flags) != 2 {
		t.Errorf("flags of %d messages: %v", len(u.Flags), u.Flags)
	}
}

// TestStatus checks that folders are counted without being opened, and
// that a folder that holds only others is not asked about.
func TestStatus(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Work")
	s.Append(t, "Work", "a")
	s.Append(t, "Work", "b")
	c := dial(t, s, false)
	s.Commands()
	got, err := c.Status([]store.Folder{{Name: "Work"}, {Name: "Holder", Attrs: []string{`\Noselect`}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "Work" || !got[0].Selectable || got[0].Messages != 2 || got[0].UIDNext == 0 || got[0].UIDValidity == 0 {
		t.Errorf("Work = %+v", got)
	}
	if got[1] != (remote.FolderStatus{Name: "Holder"}) {
		t.Errorf("Holder = %+v", got[1])
	}
	commands := s.Commands()
	if !slices.Equal(commands, []string{"STATUS"}) {
		t.Errorf("commands = %v", commands)
	}
	unchanged(t, commands)

	if _, err := c.Status([]store.Folder{{Name: "Missing"}}); err == nil {
		t.Error("a folder that is not there was counted")
	}
}

// TestDeleteFolder checks that a folder is deleted with its mail, that the
// folder the connection has open is left by opening the inbox read-only,
// never by CLOSE, and that the inbox is never deleted.
func TestDeleteFolder(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Old", "Work")
	s.Append(t, "Old", "a", imap.FlagDeleted)
	s.Append(t, "Work", "b", imap.FlagDeleted)
	c := dial(t, s, true)
	// Work is opened to be changed, as setting a flag does.
	u, err := c.Sync(&store.Index{Folder: "Work"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetFlags("Work", u.UIDValidity, []uint32{u.Added[0].UID}, true, `\Seen`); err != nil {
		t.Fatal(err)
	}
	s.Commands()

	if err := c.DeleteFolder("Old"); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteFolder("INBOX"); err == nil {
		t.Error("the inbox was deleted")
	}
	commands := s.Commands()
	if !slices.Equal(commands, []string{"EXAMINE", "DELETE"}) {
		t.Errorf("commands = %v", commands)
	}
	folders, err := c.Folders()
	if err != nil {
		t.Fatal(err)
	}
	if len(folders) != 2 || folders[0].Name != "INBOX" || folders[1].Name != "Work" {
		t.Errorf("folders = %+v", folders)
	}
	// The message marked as deleted in the folder that was open is still
	// there: it was not expunged by leaving it.
	if msgs := s.Messages(t, "Work"); len(msgs) != 1 {
		t.Errorf("Work = %+v", msgs)
	}

	if err := c.DeleteFolder("Missing"); err == nil {
		t.Error("a folder that is not there was deleted")
	}
}
