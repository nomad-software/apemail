package engine_test

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"

	"github.com/nomad-software/apemail/internal/account"
	"github.com/nomad-software/apemail/internal/crypt"
	"github.com/nomad-software/apemail/internal/crypt/crypttest"
	"github.com/nomad-software/apemail/internal/engine"
	"github.com/nomad-software/apemail/internal/remote/condstoretest"
	"github.com/nomad-software/apemail/internal/remote/remotetest"
	"github.com/nomad-software/apemail/internal/send/sendtest"
	"github.com/nomad-software/apemail/internal/store"
)

// listener keeps what the engine tells it.
type listener struct {
	mu      sync.Mutex
	folders []store.Folder
	indexes map[string]*store.Index
	// history is every index the engine has shown, in order.
	history []*store.Index
	errs    []error
	// unread are the counts of unread messages the engine gave, by
	// folder.
	unread map[string]int
	// statuses are what the engine said it was doing, in order.
	statuses []string
}

func (l *listener) FoldersChanged(_ string, folders []store.Folder) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.folders = folders
}

func (l *listener) FolderChanged(_ string, ix *store.Index) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.indexes[ix.Folder] = ix
	l.history = append(l.history, ix)
}

func (l *listener) Status(_ string, text string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.statuses = append(l.statuses, text)
}

func (l *listener) UnreadCounts(_ string, unread map[string]int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.unread = unread
}

func (l *listener) Failed(_ string, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.errs = append(l.errs, err)
}

// index returns the last index of the folder the listener was given.
func (l *listener) index(folder string) *store.Index {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.indexes[folder]
}

// wait waits for the condition to hold, failing the test if it does not
// within a few seconds.
func wait(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// recv returns what is sent on the channel, failing the test if nothing is
// within a few seconds, so that a hang fails the test rather than holds it up
// for ever.
func recv[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(10 * time.Second):
		t.Fatal("waiting for the engine")
	}
	var zero T
	return zero
}

// result runs an operation of the engine and returns the error it ends
// with.
func result(t *testing.T, op func(done func(error))) error {
	t.Helper()
	done := make(chan error, 1)
	op(func(err error) { done <- err })
	return recv(t, done)
}

// kept says if the message is kept in the local copy.
func kept(st *store.Store, folder string, uid uint32) bool {
	_, err := st.Message(folder, uid)
	return err == nil
}

// listed says if the engine has listed the folder.
func listed(e *engine.Engine, folder string) bool {
	return slices.ContainsFunc(e.Folders(), func(f store.Folder) bool { return f.Name == folder })
}

// subjects returns the subjects of the messages of an index.
func subjects(ix *store.Index) string {
	if ix == nil {
		return ""
	}
	var s []string
	for _, m := range ix.Messages {
		s = append(s, m.Subject)
	}
	return strings.Join(s, ",")
}

func start(t *testing.T, s *remotetest.Server, change ...func(*account.Account)) (*engine.Engine, *listener, *store.Store) {
	t.Helper()
	st := openStore(t)
	e, l := startOn(t, st, s.Account(), change...)
	return e, l, st
}

// openStore opens an empty local copy.
func openStore(t *testing.T) *store.Store {
	t.Helper()
	public, private := crypttest.Pair(t, t.TempDir(), "id", "")
	keys, err := crypt.Open(public, private, nil)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(t.TempDir(), keys)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// startOn starts the engine of an account on the incoming server, with the
// local copy st, as the account is changed to be.
func startOn(t *testing.T, st *store.Store, incoming account.Server, change ...func(*account.Account)) (*engine.Engine, *listener) {
	t.Helper()
	acct := account.New()
	acct.Email = "user@example.com"
	acct.Incoming = incoming
	acct.Outgoing = account.Server{Host: "127.0.0.1", Port: 1, Security: account.SecurityNone, Auth: account.AuthPassword}
	for _, f := range change {
		f(&acct)
	}
	l := &listener{indexes: map[string]*store.Index{}}
	e := engine.New(acct, st, l, func(f func()) { f() })
	t.Cleanup(e.Close)
	e.Start()
	return e, l
}

func TestSyncAndOpen(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove, imap.CapIdle), "Trash", "Sent")
	s.Append(t, "INBOX", "first")
	s.Append(t, "INBOX", "second")
	e, l, st := start(t, s)

	wait(t, "the inbox", func() bool { return subjects(l.index("INBOX")) == "first,second" })
	if got := e.SpecialFolder(engine.Trash); got != "Trash" {
		t.Errorf("trash = %q", got)
	}

	// Opening a message downloads it, without marking it as read, and
	// keeps it.
	ix := l.index("INBOX")
	uid := ix.Messages[0].UID
	got := make(chan string, 1)
	e.Message("INBOX", uid, func(raw []byte, err error) {
		if err != nil {
			t.Error(err)
		}
		got <- string(raw)
	})
	if raw := recv(t, got); !strings.Contains(raw, "The body of first.") {
		t.Errorf("message = %q", raw)
	}
	if !kept(st, "INBOX", uid) {
		t.Error("the message was not kept")
	}
	if msgs := s.Messages(t, "INBOX"); len(msgs[0].Flags) != 0 {
		t.Errorf("flags = %v", msgs[0].Flags)
	}

	// New mail is seen as it arrives.
	s.Append(t, "INBOX", "third")
	wait(t, "the new message", func() bool { return subjects(l.index("INBOX")) == "first,second,third" })

	// Moving a message to the trash takes it out of the inbox here and
	// there.
	if err := result(t, func(done func(error)) { e.Move("INBOX", []uint32{uid}, "Trash", done) }); err != nil {
		t.Fatal(err)
	}
	if subjects(l.index("INBOX")) != "second,third" || kept(st, "INBOX", uid) {
		t.Errorf("inbox = %s", subjects(l.index("INBOX")))
	}
	if msgs := s.Messages(t, "Trash"); len(msgs) != 1 || msgs[0].Subject != "first" {
		t.Errorf("trash = %+v", msgs)
	}

	// Marking a message as read.
	if err := result(t, func(done func(error)) {
		e.SetFlag("INBOX", []uint32{l.index("INBOX").Messages[0].UID}, `\Seen`, true, done)
	}); err != nil {
		t.Fatal(err)
	}
	if !l.index("INBOX").Messages[0].Has(`\Seen`) {
		t.Error("the index was not marked as read")
	}
}

func TestOutbox(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus), "Sent")
	e, _, st := start(t, s)
	wait(t, "the folders", func() bool { return listed(e, "Sent") })

	// The outgoing server of the test cannot be reached, so the mail is
	// kept to be sent later.
	if err := result(t, func(done func(error)) { e.Send([]byte("Subject: x\r\n\r\nx"), []string{"bob@example.com"}, true, done) }); err == nil || !strings.Contains(err.Error(), "when it can be") {
		t.Errorf("err = %v", err)
	}
	if pending, _ := st.Pending(); len(pending) != 1 || pending[0].Use != "sent" || !pending[0].DSN {
		t.Errorf("pending = %+v", pending)
	}
}

// TestSendReceipt checks that a return receipt is sent, and is not kept in
// the sent folder as mail the user wrote is.
func TestSendReceipt(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus), "Sent")
	smtp := sendtest.Start(t, true)
	e, _, st := start(t, s, func(a *account.Account) { a.Outgoing = smtp.Account })
	wait(t, "the folders", func() bool { return listed(e, "Sent") })

	receipt := []byte("Subject: Return Receipt (displayed) - x\r\n\r\nx")
	if err := result(t, func(done func(error)) { e.SendReceipt(receipt, []string{"bob@example.com"}, done) }); err != nil {
		t.Fatal(err)
	}
	if msgs := smtp.Messages(); len(msgs) != 1 || strings.TrimSpace(string(msgs[0].Data)) != string(receipt) || msgs[0].Return != "" {
		t.Errorf("sent %+v", msgs)
	}
	// A message sent afterwards is kept, and only it: had the receipt
	// been kept, it would be there before it.
	if err := result(t, func(done func(error)) {
		e.Send([]byte("Subject: Mine\r\n\r\nx"), []string{"bob@example.com"}, false, done)
	}); err != nil {
		t.Fatal(err)
	}
	wait(t, "the copy of the message", func() bool { return len(s.Messages(t, "Sent")) == 1 })
	if msgs := s.Messages(t, "Sent"); msgs[0].Subject != "Mine" {
		t.Errorf("sent folder %+v", msgs)
	}
	if pending, _ := st.Pending(); len(pending) != 0 {
		t.Errorf("pending = %+v", pending)
	}
}

// TestReceiptOutbox checks that a return receipt that cannot be sent waits
// in the outbox, to go nowhere but to its recipients.
func TestReceiptOutbox(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus), "Sent")
	e, _, st := start(t, s)
	wait(t, "the folders", func() bool { return listed(e, "Sent") })
	if err := result(t, func(done func(error)) {
		e.SendReceipt([]byte("Subject: x\r\n\r\nx"), []string{"bob@example.com"}, done)
	}); err == nil || !strings.Contains(err.Error(), "when it can be") {
		t.Errorf("err = %v", err)
	}
	if pending, _ := st.Pending(); len(pending) != 1 || pending[0].Use != "" || pending[0].Folder != "" || pending[0].DSN {
		t.Errorf("pending = %+v", pending)
	}
}

// TestFlagSetWhileSyncing checks that a flag the user sets while the folder
// is being synchronised is kept, rather than undone by the flags the sync
// read from the server before it was set.
func TestFlagSetWhileSyncing(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus))
	s.Append(t, "INBOX", "first")
	e, l, _ := start(t, s)
	wait(t, "the inbox", func() bool { return subjects(l.index("INBOX")) == "first" })
	uid := l.index("INBOX").Messages[0].UID

	// The sync reads the flags of the first message, and is held as it
	// asks for the headers of the second, which is new.
	held, release := s.Hold(t, "ENVELOPE")
	s.Append(t, "INBOX", "second")
	e.Refresh("INBOX")
	select {
	case <-held:
	case <-time.After(5 * time.Second):
		t.Fatal("the sync did not ask for the new message")
	}
	if err := result(t, func(done func(error)) { e.SetFlag("INBOX", []uint32{uid}, `\Seen`, true, done) }); err != nil {
		t.Fatal(err)
	}
	release()

	wait(t, "the second message", func() bool { return subjects(l.index("INBOX")) == "first,second" })
	// Once shown as read, it is never shown unread again, not even until
	// a later sync puts it right.
	l.mu.Lock()
	defer l.mu.Unlock()
	read := false
	for _, ix := range l.history {
		if ix.Folder != "INBOX" || len(ix.Messages) == 0 || ix.Messages[0].UID != uid {
			continue
		}
		if ix.Messages[0].Has(`\Seen`) {
			read = true
		} else if read {
			t.Fatalf("the flag set was undone: %+v", ix.Messages)
		}
	}
	if !read {
		t.Error("the message was never shown as read")
	}
}

// TestOnlyWhatIsAsked checks that starting checks only the inbox, and that
// Get Mail checks only the folder it is given: a large folder costs nothing
// until it is looked at.
func TestOnlyWhatIsAsked(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus), "Sent", "Trash")
	s.Append(t, "INBOX", "new")
	s.Append(t, "Sent", "mine")
	s.Append(t, "Trash", "old")
	e, l, _ := start(t, s)

	wait(t, "the inbox", func() bool { return subjects(l.index("INBOX")) == "new" && listed(e, "Trash") })
	e.Refresh("Trash")
	wait(t, "the trash", func() bool { return subjects(l.index("Trash")) == "old" })
	// The syncs are done in turn, so Sent would have been by now.
	if ix := l.index("Sent"); ix != nil {
		t.Errorf("sent was synchronised: %+v", ix)
	}
	// Each sync lists its folder's messages with a FETCH, which nothing
	// else sends; watching the inbox opens it too, but fetches nothing.
	synced := 0
	for _, c := range s.Commands() {
		if c == "FETCH" {
			synced++
		}
	}
	if synced != 2 {
		t.Errorf("%d folders were synchronised, not the inbox and the trash", synced)
	}
}

// savedIndex returns the index of the folder as it is kept on disk.
func savedIndex(t *testing.T, st *store.Store, folder string) *store.Index {
	t.Helper()
	ix, err := st.Index(folder)
	if err != nil {
		t.Fatal(err)
	}
	return ix
}

// TestIndexSavedLater checks that a change to an index is saved a little
// after it is made, rather than at once, so that a run of changes to a
// large folder is saved once, and that it is shown at once all the same.
func TestIndexSavedLater(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus))
	s.Append(t, "INBOX", "first")
	e, l, st := start(t, s)
	wait(t, "the index saved", func() bool { return subjects(savedIndex(t, st, "INBOX")) == "first" })
	uid := l.index("INBOX").Messages[0].UID

	if err := result(t, func(done func(error)) { e.SetFlag("INBOX", []uint32{uid}, `\Seen`, true, done) }); err != nil {
		t.Fatal(err)
	}
	if !l.index("INBOX").Messages[0].Has(`\Seen`) {
		t.Error("the change was not shown at once")
	}
	if savedIndex(t, st, "INBOX").Messages[0].Has(`\Seen`) {
		t.Error("the index was saved at once")
	}
	wait(t, "the change saved", func() bool { return savedIndex(t, st, "INBOX").Messages[0].Has(`\Seen`) })
}

// TestIndexSavedOnClose checks that changes not yet saved are saved when the
// engine is closed.
func TestIndexSavedOnClose(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus))
	s.Append(t, "INBOX", "first")
	e, l, st := start(t, s)
	wait(t, "the inbox", func() bool { return subjects(l.index("INBOX")) == "first" })
	uid := l.index("INBOX").Messages[0].UID
	if err := result(t, func(done func(error)) { e.SetFlag("INBOX", []uint32{uid}, `\Flagged`, true, done) }); err != nil {
		t.Fatal(err)
	}
	e.Close()
	if ix := savedIndex(t, st, "INBOX"); len(ix.Messages) != 1 || !ix.Messages[0].Has(`\Flagged`) {
		t.Errorf("saved %+v", ix.Messages)
	}
}

// TestCondStoreKept checks that the count of changes a sync found is kept
// with the index, so that once the client starts again it asks a server
// with CONDSTORE only for what has changed since, and that a flag the user
// sets is seen by the next sync as a change.
func TestCondStoreKept(t *testing.T) {
	s := condstoretest.Start(t)
	s.Append("one")
	two := s.Append("two")
	st := openStore(t)
	e, l := startOn(t, st, s.Account())
	wait(t, "the inbox", func() bool { return subjects(l.index("INBOX")) == "one,two" })
	e.Close()
	if savedIndex(t, st, "INBOX").HighestModSeq == 0 {
		t.Fatal("the count of changes was not kept")
	}

	// Started again, with a flag set elsewhere, only it is fetched.
	s.SetFlags(two, `\Seen`)
	s.Commands()
	e, l = startOn(t, st, s.Account())
	wait(t, "the flag", func() bool {
		ix := l.index("INBOX")
		return ix != nil && len(ix.Messages) == 2 && ix.Messages[1].Has(`\Seen`)
	})
	for _, c := range s.Commands() {
		if strings.HasPrefix(c, "FETCH 1:*") && !strings.Contains(c, "CHANGEDSINCE") {
			t.Errorf("every message was fetched: %s", c)
		}
	}

	// The user's own change is one too.
	if err := result(t, func(done func(error)) { e.SetFlag("INBOX", []uint32{two}, `\Flagged`, true, done) }); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(s.Flags(two), `\Flagged`) {
		t.Fatalf("the server has %v", s.Flags(two))
	}
	s.SetFlags(two, `\Seen`)
	e.Refresh("")
	wait(t, "the flag taken off elsewhere", func() bool { return !l.index("INBOX").Messages[1].Has(`\Flagged`) })
}

// TestGoneFolderForgotten checks that the local copy of a folder that has
// gone from the server is thrown away, and that no index of it is saved
// after, even one that had changed and was waiting to be.
func TestGoneFolderForgotten(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus), "Old")
	s.Append(t, "Old", "kept")
	e, l, st := start(t, s)
	e.Refresh("Old")
	wait(t, "the old folder", func() bool { return subjects(l.index("Old")) == "kept" })
	uid := l.index("Old").Messages[0].UID
	// The change waits to be saved.
	if err := result(t, func(done func(error)) { e.SetFlag("Old", []uint32{uid}, `\Flagged`, true, done) }); err != nil {
		t.Fatal(err)
	}

	c := s.Client(t)
	if err := c.Delete("Old").Wait(); err != nil {
		t.Fatal(err)
	}
	e.Refresh("")
	wait(t, "the folder gone", func() bool { return !listed(e, "Old") })
	e.Close()
	if ix := savedIndex(t, st, "Old"); len(ix.Messages) != 0 {
		t.Errorf("the index of the folder that went was kept: %+v", ix.Messages)
	}
}

// noCheck has an account not check for mail when it starts, so that a test
// sees only what it asks for.
func noCheck(a *account.Account) { a.CheckOnStart = false }

// TestUpdateCounts checks that the unread messages of every folder are
// counted without any folder being opened or anything fetched.
func TestUpdateCounts(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus), "Sent", "Trash")
	s.Append(t, "INBOX", "new")
	s.Append(t, "Trash", "old", imap.FlagSeen)
	s.Append(t, "Trash", "unread")
	s.Append(t, "Trash", "unread too")
	e, l, _ := start(t, s, noCheck)
	s.Commands()

	if err := result(t, func(done func(error)) { e.UpdateCounts(done) }); err != nil {
		t.Fatal(err)
	}
	wait(t, "the counts", func() bool {
		l.mu.Lock()
		defer l.mu.Unlock()
		return l.unread != nil
	})
	l.mu.Lock()
	unread := l.unread
	l.mu.Unlock()
	if len(unread) != 3 || unread["INBOX"] != 1 || unread["Trash"] != 2 || unread["Sent"] != 0 {
		t.Errorf("unread = %v", unread)
	}
	for _, c := range s.Commands() {
		if c == "EXAMINE" || c == "SELECT" || strings.Contains(c, "FETCH") {
			t.Errorf("%s was sent", c)
		}
	}
	if l.index("Trash") != nil {
		t.Error("the trash was synchronised")
	}
}

// lastStatus returns what the engine last said it was doing.
func lastStatus(l *listener) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.statuses) == 0 {
		return ""
	}
	return l.statuses[len(l.statuses)-1]
}

// firstShown returns the folders in the order the engine first showed an
// index of each.
func firstShown(l *listener) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var order []string
	for _, ix := range l.history {
		if !slices.Contains(order, ix.Folder) {
			order = append(order, ix.Folder)
		}
	}
	return order
}

// TestSyncAll checks that every folder is synchronised, the inbox first and
// the trash last, and no message downloaded.
func TestSyncAll(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus), "Trash", "Projects", "Sent", "Drafts")
	s.Append(t, "INBOX", "new")
	s.Append(t, "Projects", "plans")
	s.Append(t, "Trash", "old")
	e, l, st := start(t, s, noCheck)

	if err := result(t, func(done func(error)) { e.SyncAll(done) }); err != nil {
		t.Fatal(err)
	}
	order := firstShown(l)
	if len(order) != 5 || order[0] != "INBOX" || order[4] != "Trash" || order[3] != "Projects" {
		t.Errorf("synchronised in the order %v", order)
	}
	if subjects(l.index("Projects")) != "plans" || subjects(l.index("Trash")) != "old" {
		t.Errorf("projects %q, trash %q", subjects(l.index("Projects")), subjects(l.index("Trash")))
	}
	if kept(st, "Trash", l.index("Trash").Messages[0].UID) {
		t.Error("a message was downloaded")
	}
	if e.SyncingAll() {
		t.Error("still synchronising")
	}
}

// TestStopSyncAll checks that synchronising every folder does not hold up
// checking a folder meanwhile, is not started twice, and stops at once when
// it is stopped, leaving the folder it was at as it was.
func TestStopSyncAll(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus), "Trash")
	s.Append(t, "Trash", "old")
	e, l, _ := start(t, s, noCheck)

	// The inbox is empty, so the first headers asked for are the trash's.
	held, release := s.Hold(t, "ENVELOPE")
	done := make(chan error, 2)
	e.SyncAll(func(err error) { done <- err })
	select {
	case <-held:
	case <-time.After(5 * time.Second):
		t.Fatal("the trash was not reached")
	}
	if !e.SyncingAll() {
		t.Error("not synchronising")
	}
	e.SyncAll(func(err error) { done <- err })

	// Mail is checked for meanwhile, even after the trash, which is being
	// synchronised already, is asked for.
	e.Refresh("Trash")
	s.Append(t, "INBOX", "arrived")
	e.Refresh("INBOX")
	wait(t, "the new mail", func() bool { return subjects(l.index("INBOX")) == "arrived" })

	// What it is doing is shown beside what else is being done.
	if last := lastStatus(l); !strings.Contains(last, "Every folder, 2 of 2: ") {
		t.Errorf("status %q", last)
	}

	e.StopSyncAll()
	if err := recv(t, done); !errors.Is(err, engine.ErrStopped) {
		t.Errorf("err = %v", err)
	}
	release()
	wait(t, "the status cleared", func() bool { return !strings.Contains(lastStatus(l), "Every folder") })
	if e.SyncingAll() {
		t.Error("still synchronising")
	}
	select {
	case err := <-done:
		t.Errorf("a second sync of every folder ran: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	if ix := l.index("Trash"); ix != nil && len(ix.Messages) != 0 {
		t.Errorf("the trash was synchronised: %+v", ix.Messages)
	}
}

func TestTrash(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Trash")
	s.Append(t, "INBOX", "first")
	e, l, _ := start(t, s)
	wait(t, "the inbox", func() bool { return subjects(l.index("INBOX")) == "first" })
	uids := []uint32{l.index("INBOX").Messages[0].UID}

	if err := result(t, func(done func(error)) { e.Trash("INBOX", uids, done) }); err != nil {
		t.Fatal(err)
	}
	wait(t, "the trash", func() bool { return subjects(l.index("Trash")) == "first" })

	// Mail outside the trash is not deleted for good.
	s.Append(t, "INBOX", "second")
	e.Refresh("INBOX")
	wait(t, "the inbox", func() bool { return subjects(l.index("INBOX")) == "second" })
	if err := result(t, func(done func(error)) { e.Delete("INBOX", []uint32{l.index("INBOX").Messages[0].UID}, done) }); err == nil {
		t.Error("mail outside the trash was deleted for good")
	}

	// Mail in the trash is.
	if err := result(t, func(done func(error)) { e.Delete("Trash", []uint32{l.index("Trash").Messages[0].UID}, done) }); err != nil {
		t.Fatal(err)
	}
	if msgs := s.Messages(t, "Trash"); len(msgs) != 0 {
		t.Errorf("trash = %+v", msgs)
	}
	if msgs := s.Messages(t, "INBOX"); len(msgs) != 1 {
		t.Errorf("inbox = %+v", msgs)
	}
}

func TestMissingFoldersAreMade(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	s.Append(t, "INBOX", "first")
	s.Append(t, "INBOX", "second")
	e, l, _ := start(t, s)
	wait(t, "the inbox", func() bool { return subjects(l.index("INBOX")) == "first,second" })
	if got := e.FolderFor(engine.Archive); got != "Archive" {
		t.Errorf("archive = %q", got)
	}
	ix := l.index("INBOX")
	first, second := ix.Messages[0].UID, ix.Messages[1].UID

	if err := result(t, func(done func(error)) { e.Archive("INBOX", []uint32{first}, done) }); err != nil {
		t.Fatal(err)
	}
	if err := result(t, func(done func(error)) { e.Trash("INBOX", []uint32{second}, done) }); err != nil {
		t.Fatal(err)
	}
	saved := make(chan engine.Saved, 1)
	e.SaveDraft([]byte("Subject: draft\r\n\r\nx"), func(s engine.Saved, err error) {
		if err != nil {
			t.Error(err)
		}
		saved <- s
	})
	if s := recv(t, saved); s.Folder != "Drafts" || s.UID == 0 {
		t.Errorf("saved = %+v", s)
	}

	// Archived mail goes in the folder for the year it was sent in.
	for folder, subject := range map[string]string{"Archive/2006": "first", "Trash": "second", "Drafts": "draft"} {
		if msgs := s.Messages(t, folder); len(msgs) != 1 || msgs[0].Subject != subject {
			t.Errorf("%s = %+v", folder, msgs)
		}
	}
	if msgs := s.Messages(t, "INBOX"); len(msgs) != 0 {
		t.Errorf("inbox = %+v", msgs)
	}
	// The folders are listed at once.
	for _, name := range []string{"Archive", "Trash", "Drafts"} {
		if !slices.ContainsFunc(e.Folders(), func(f store.Folder) bool { return f.Name == name }) {
			t.Errorf("%s is not listed", name)
		}
	}
}

func TestUsualNamesAreUsed(t *testing.T) {
	// A server that marks no folder with its use has them found by their
	// usual names, rather than others being made.
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Archives", "Deleted Items")
	s.Append(t, "INBOX", "first")
	s.Append(t, "INBOX", "second")
	e, l, _ := start(t, s)
	wait(t, "the folders", func() bool { return len(e.Folders()) == 3 && subjects(l.index("INBOX")) == "first,second" })
	ix := l.index("INBOX")

	if err := result(t, func(done func(error)) { e.Archive("INBOX", []uint32{ix.Messages[0].UID}, done) }); err != nil {
		t.Fatal(err)
	}
	if err := result(t, func(done func(error)) { e.Trash("INBOX", []uint32{ix.Messages[1].UID}, done) }); err != nil {
		t.Fatal(err)
	}
	if msgs := s.Messages(t, "Archives/2006"); len(msgs) != 1 {
		t.Errorf("archives = %+v", msgs)
	}
	if msgs := s.Messages(t, "Deleted Items"); len(msgs) != 1 {
		t.Errorf("deleted items = %+v", msgs)
	}
	// Only the folder for the year was made.
	if len(e.Folders()) != 4 || !slices.ContainsFunc(e.Folders(), func(f store.Folder) bool { return f.Name == "Archives/2006" }) {
		t.Errorf("folders = %+v", e.Folders())
	}
}

func TestFindSpecial(t *testing.T) {
	folders := []store.Folder{
		{Name: "INBOX", Delim: "."},
		{Name: "INBOX.Archive", Delim: "."},
		{Name: "Old mail", Delim: ".", Attrs: []string{`\Archive`}},
		{Name: "INBOX.Sent Items", Delim: "."},
		{Name: "Bin", Delim: ".", Attrs: []string{`\Noselect`}},
	}
	// The folder the server marks wins over one with a usual name.
	if got := engine.FindSpecial(folders, engine.Archive); got != "Old mail" {
		t.Errorf("archive = %q", got)
	}
	// A name within another folder is found.
	if got := engine.FindSpecial(folders, engine.Sent); got != "INBOX.Sent Items" {
		t.Errorf("sent = %q", got)
	}
	// A folder that cannot hold messages is not.
	if got := engine.FindSpecial(folders, engine.Trash); got != "" {
		t.Errorf("trash = %q", got)
	}
}

func TestArchiveByYear(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	s.AppendDated(t, "INBOX", "old", time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC))
	s.AppendDated(t, "INBOX", "new", time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC))
	s.AppendDated(t, "INBOX", "newer", time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC))
	e, l, _ := start(t, s)
	wait(t, "the inbox", func() bool { ix := l.index("INBOX"); return ix != nil && len(ix.Messages) == 3 })
	var uids []uint32
	for _, m := range l.index("INBOX").Messages {
		uids = append(uids, m.UID)
	}

	if err := result(t, func(done func(error)) { e.Archive("INBOX", uids, done) }); err != nil {
		t.Fatal(err)
	}
	// The server of the tests separates folders with /.
	if msgs := s.Messages(t, "Archive/2024"); len(msgs) != 1 || msgs[0].Subject != "old" {
		t.Errorf("2024 = %+v", msgs)
	}
	if msgs := s.Messages(t, "Archive/2026"); len(msgs) != 2 {
		t.Errorf("2026 = %+v", msgs)
	}
	if msgs := s.Messages(t, "INBOX"); len(msgs) != 0 {
		t.Errorf("inbox = %+v", msgs)
	}
	if !e.InArchive("Archive/2026") || !e.InArchive("Archive") || e.InArchive("Archives") || e.InArchive("INBOX") {
		t.Error("in archive")
	}

	// Mail in the archive is not archived again.
	wait(t, "the year folder", func() bool {
		return slices.ContainsFunc(e.Folders(), func(f store.Folder) bool { return f.Name == "Archive/2024" })
	})
	e.Refresh("Archive/2024")
	wait(t, "the 2024 index", func() bool { ix := l.index("Archive/2024"); return ix != nil && len(ix.Messages) == 1 })
	if err := result(t, func(done func(error)) {
		e.Archive("Archive/2024", []uint32{l.index("Archive/2024").Messages[0].UID}, done)
	}); err == nil {
		t.Error("archived mail was archived again")
	}
}

func TestArchiveFolder(t *testing.T) {
	date := time.Date(2026, 3, 9, 12, 0, 0, 0, time.Local)
	for _, c := range []struct {
		delim string
		by    account.ArchiveBy
		want  string
	}{
		{".", account.ArchiveYearly, "INBOX.Archive.2026"},
		{".", account.ArchiveMonthly, "INBOX.Archive.2026.2026-03"},
		{".", account.ArchiveSingle, "INBOX.Archive"},
		// A server without folders within folders has everything in
		// the archive.
		{"", account.ArchiveYearly, "INBOX.Archive"},
	} {
		if got := engine.ArchiveFolder("INBOX.Archive", c.delim, c.by, date); got != c.want {
			t.Errorf("%q, %q = %q, want %q", c.delim, c.by, got, c.want)
		}
	}
}
