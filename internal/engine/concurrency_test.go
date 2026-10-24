package engine_test

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"

	"github.com/nomad-software/apemail/internal/account"
	"github.com/nomad-software/apemail/internal/engine"
	"github.com/nomad-software/apemail/internal/remote/remotetest"
	"github.com/nomad-software/apemail/internal/send/sendtest"
	"github.com/nomad-software/apemail/internal/store"
)

// closing is an engine whose posts are checked: nothing is to be posted once
// Close has returned, as by then the local copy may be being moved, and the
// UI has forgotten the engine.
type closing struct {
	*engine.Engine
	l      *listener
	st     *store.Store
	closed atomic.Bool
	late   atomic.Int32
}

// startClosing starts the engine of an account on the server, as startOn
// does, with its posts checked.
func startClosing(t *testing.T, s *remotetest.Server, change ...func(*account.Account)) *closing {
	t.Helper()
	acct := account.New()
	acct.Email = "user@example.com"
	acct.Incoming = s.Account()
	acct.Outgoing = account.Server{Host: "127.0.0.1", Port: 1, Security: account.SecurityNone, Auth: account.AuthPassword}
	for _, f := range change {
		f(&acct)
	}
	c := &closing{l: &listener{indexes: map[string]*store.Index{}}, st: openStore(t)}
	c.Engine = engine.New(acct, c.st, c.l, func(f func()) {
		if c.closed.Load() {
			c.late.Add(1)
		}
		f()
	})
	t.Cleanup(c.Engine.Close)
	c.Start()
	return c
}

// close closes the engine, and checks a while later that nothing was posted
// after it returned.
func (c *closing) close(t *testing.T) {
	t.Helper()
	c.Close()
	c.closed.Store(true)
	time.Sleep(100 * time.Millisecond)
	if n := c.late.Load(); n > 0 {
		t.Errorf("%d things were posted after the engine was closed", n)
	}
}

// TestQueuedOpsAnsweredOnClose checks that what was asked of the op worker
// and had not started when the engine closed is answered, as it is when the
// server cannot be reached: a draft is kept in the outbox rather than lost,
// and every caller hears back, before Close returns.
func TestQueuedOpsAnsweredOnClose(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Drafts", "Trash")
	s.Append(t, "INBOX", "first")
	c := startClosing(t, s)
	wait(t, "the inbox", func() bool { return subjects(c.l.index("INBOX")) == "first" })
	uid := c.l.index("INBOX").Messages[0].UID

	// The op worker is held downloading the message, so what follows
	// waits behind it.
	held, release := s.Hold(t, "BODY.PEEK[]")
	defer release()
	got := make(chan error, 1)
	c.Message("INBOX", uid, func(_ []byte, err error) { got <- err })
	select {
	case <-held:
	case <-time.After(5 * time.Second):
		t.Fatal("the message was not asked for")
	}
	drafted := make(chan error, 1)
	c.SaveDraft([]byte("Subject: unsaved\r\n\r\nx"), func(_ engine.Saved, err error) { drafted <- err })
	flagged := make(chan error, 1)
	c.SetFlag("INBOX", []uint32{uid}, `\Flagged`, true, func(err error) { flagged <- err })
	trashed := make(chan error, 1)
	c.Trash("INBOX", []uint32{uid}, func(err error) { trashed <- err })

	c.close(t)
	for what, ch := range map[string]chan error{"the download": got, "the draft": drafted, "the flag": flagged, "the move": trashed} {
		select {
		case err := <-ch:
			if err == nil {
				t.Errorf("%s was done after the engine closed", what)
			}
		default:
			t.Errorf("%s was not answered by the time Close returned", what)
		}
	}
	pending, err := c.st.Pending()
	if err != nil || len(pending) != 1 || !strings.Contains(string(pending[0].Raw), "unsaved") || pending[0].Use != "drafts" {
		t.Errorf("pending = %+v, %v", pending, err)
	}
	release()
	// The server was asked for nothing more: the message is as it was.
	if msgs := s.Messages(t, "INBOX"); len(msgs) != 1 || len(msgs[0].Flags) != 0 {
		t.Errorf("inbox = %+v", msgs)
	}
}

// TestNothingAfterClose checks that what is asked of an engine once it is
// closed does nothing: no goroutine starts, nothing reaches the server or
// the local copy, and nothing is posted.
func TestNothingAfterClose(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Drafts", "Trash")
	s.Append(t, "INBOX", "first")
	c := startClosing(t, s)
	wait(t, "the inbox", func() bool { return subjects(c.l.index("INBOX")) == "first" })
	uid := c.l.index("INBOX").Messages[0].UID
	c.close(t)
	s.Commands()

	// Each answer would be posted, which is counted.
	answered := func(string) {}
	c.Start()
	c.Open("INBOX")
	c.Refresh("")
	c.Message("INBOX", uid, func([]byte, error) { answered("Message") })
	c.SetFlag("INBOX", []uint32{uid}, `\Seen`, true, func(error) { answered("SetFlag") })
	c.Trash("INBOX", []uint32{uid}, func(error) { answered("Trash") })
	c.SaveDraft([]byte("Subject: x\r\n\r\nx"), func(engine.Saved, error) { answered("SaveDraft") })
	c.Send([]byte("Subject: x\r\n\r\nx"), []string{"bob@example.com"}, false, func(error) { answered("Send") })
	c.SyncAll(func(error) { answered("SyncAll") })
	c.UpdateCounts(func(error) { answered("UpdateCounts") })
	if c.SyncingAll() {
		t.Error("synchronising every folder after closing")
	}
	// What a closed engine waits for is nothing, so it closes again at
	// once.
	c.Close()
	time.Sleep(100 * time.Millisecond)
	if n := c.late.Load(); n > 0 {
		t.Errorf("%d things were posted", n)
	}
	if cmds := s.Commands(); len(cmds) > 0 {
		t.Errorf("the server was asked %v", cmds)
	}
	if pending, _ := c.st.Pending(); len(pending) != 0 {
		t.Errorf("pending = %+v", pending)
	}
}

// TestCloseWhileBusy closes engines while every kind of work is being done
// at once, from several goroutines, so that the race detector sees them
// together: nothing is posted after Close returns, and every message that
// was said to be kept for later is in the outbox, each in a file of its own.
func TestCloseWhileBusy(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove, imap.CapIdle), "Drafts", "Sent", "Trash", "Work")
	for i := range 20 {
		s.Append(t, "INBOX", fmt.Sprintf("message %d", i))
		s.Append(t, "Work", fmt.Sprintf("work %d", i))
	}
	for round := range 8 {
		c := startClosing(t, s)
		wait(t, "the inbox", func() bool { return c.l.index("INBOX") != nil && len(c.l.index("INBOX").Messages) == 20 })
		ix := c.l.index("INBOX")

		var kept atomic.Int32
		keptFor := func(err error) {
			if err != nil && strings.Contains(err.Error(), "when it can be") {
				kept.Add(1)
			}
		}
		var wg sync.WaitGroup
		for g := range 4 {
			wg.Go(func() {
				for i := range 5 {
					uid := ix.Messages[(g*5+i)%len(ix.Messages)].UID
					c.Open("Work")
					c.Refresh("")
					c.Message("INBOX", uid, func([]byte, error) {})
					c.SetFlag("INBOX", []uint32{uid}, `\Seen`, i%2 == 0, func(error) {})
					c.SaveDraft([]byte(fmt.Sprintf("Subject: draft %d %d\r\n\r\nx", g, i)), func(_ engine.Saved, err error) { keptFor(err) })
					c.Send([]byte(fmt.Sprintf("Subject: mail %d %d\r\n\r\nx", g, i)), []string{"bob@example.com"}, false, keptFor)
					c.SyncAll(func(error) {})
					c.UpdateCounts(func(error) {})
					_ = c.Folders()
					_ = c.SpecialFolder(engine.Trash)
				}
			})
		}
		// Closing part way through, at a different point each round.
		time.Sleep(time.Duration(round*15) * time.Millisecond)
		c.close(t)
		wg.Wait()
		pending, err := c.st.Pending()
		if err != nil {
			t.Fatal(err)
		}
		if int(kept.Load()) != len(pending) {
			t.Errorf("round %d: %d were said to be kept, and the outbox has %d", round, kept.Load(), len(pending))
		}
		ids := map[string]bool{}
		for _, p := range pending {
			if ids[p.ID] {
				t.Errorf("round %d: two in the outbox are %s", round, p.ID)
			}
			ids[p.ID] = true
		}
	}
}

// TestStatusEndsIdle checks that once everything the engine was doing at
// once has finished, the last status it gave is that it is doing nothing:
// what each part says is given in the order they said it, so that an older
// status never arrives after a newer one. The UI's thread takes a while to
// get to what is posted, which is what lets one overtake another.
func TestStatusEndsIdle(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus), "Sent", "Trash", "Work")
	for i := range 10 {
		s.Append(t, "Work", fmt.Sprintf("work %d", i))
	}
	acct := account.New()
	acct.Incoming = s.Account()
	l := &listener{indexes: map[string]*store.Index{}}
	var n atomic.Uint32
	e := engine.New(acct, openStore(t), l, func(f func()) {
		// Every third post waits a little, as the UI's thread does
		// while it is busy.
		if n.Add(1)%3 == 0 {
			time.Sleep(time.Millisecond)
		}
		f()
	})
	t.Cleanup(e.Close)
	for round := range 20 {
		var wg sync.WaitGroup
		wg.Add(3)
		e.SyncAll(func(error) { wg.Done() })
		e.UpdateCounts(func(error) { wg.Done() })
		e.Message("Work", 1, func([]byte, error) { wg.Done() })
		e.Refresh("Work")
		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()
		recv(t, done)
		// The sync asked for by Refresh has no answer to wait for, but
		// is soon done; a status overtaken stays for good.
		wait(t, fmt.Sprintf("round %d to end idle", round), func() bool { return lastStatus(l) == "" })
	}
}

// TestManyOutboxAtOnce checks that mail sent from several windows at once
// while the outgoing server cannot be reached is all kept, each in a file of
// its own, rather than one over another.
func TestManyOutboxAtOnce(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus), "Sent")
	e, _, st := start(t, s, noCheck)
	const n = 20
	errs := make(chan error, n)
	for i := range n {
		e.Send([]byte(fmt.Sprintf("Subject: %d\r\n\r\nx", i)), []string{"bob@example.com"}, false, func(err error) { errs <- err })
	}
	for range n {
		if err := recv(t, errs); err == nil || !strings.Contains(err.Error(), "when it can be") {
			t.Errorf("err = %v", err)
		}
	}
	pending, err := st.Pending()
	if err != nil || len(pending) != n {
		t.Fatalf("the outbox has %d, not %d: %v", len(pending), n, err)
	}
	subjects := map[string]bool{}
	for _, p := range pending {
		subjects[string(p.Raw)] = true
	}
	if len(subjects) != n {
		t.Errorf("the outbox has %d different messages, not %d", len(subjects), n)
	}
}

// TestNewMailWhileIdling checks that mail arriving in the inbox while the
// engine idles there is seen without being asked for.
func TestNewMailWhileIdling(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapIdle))
	s.Append(t, "INBOX", "first")
	_, l, _ := start(t, s)
	wait(t, "the inbox", func() bool { return subjects(l.index("INBOX")) == "first" })
	wait(t, "the engine idling", func() bool {
		for _, cmd := range s.Commands() {
			if cmd == "IDLE" {
				return true
			}
		}
		return false
	})
	s.Append(t, "INBOX", "second")
	wait(t, "the new mail", func() bool { return subjects(l.index("INBOX")) == "first,second" })
	// It is seen again once the engine has gone back to idling.
	s.Append(t, "INBOX", "third")
	wait(t, "more new mail", func() bool { return subjects(l.index("INBOX")) == "first,second,third" })
	if errs := l.errors(); len(errs) > 0 {
		t.Errorf("errors: %v", errs)
	}
}

// errors returns the errors the listener was given.
func (l *listener) errors() []error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]error(nil), l.errs...)
}

// TestOutboxSentWhileSending checks that the outbox is sent on Get Mail,
// each message once, with a copy in the sent folder, and the drafts kept
// while offline put in the drafts folder, while other mail is sent at the
// same time from other goroutines.
func TestOutboxSentWhileSending(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus), "Sent", "Drafts")
	smtp := sendtest.Start(t, true)
	st := openStore(t)
	const kept, sent = 5, 5
	for i := range kept {
		if _, err := st.AddPending(store.Pending{Kind: store.PendingSend, Use: "sent", From: "user@example.com", To: []string{"bob@example.com"},
			Raw: []byte(fmt.Sprintf("Subject: kept %d\r\n\r\nx", i))}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.AddPending(store.Pending{Kind: store.PendingAppend, Use: "drafts", Flags: []string{`\Draft`}, Raw: []byte("Subject: offline draft\r\n\r\nx")}); err != nil {
		t.Fatal(err)
	}
	e, _ := startOn(t, st, s.Account(), noCheck, func(a *account.Account) { a.Outgoing = smtp.Account })

	errs := make(chan error, sent)
	e.Refresh("")
	for i := range sent {
		go e.Send([]byte(fmt.Sprintf("Subject: new %d\r\n\r\nx", i)), []string{"bob@example.com"}, false, func(err error) { errs <- err })
	}
	for range sent {
		if err := recv(t, errs); err != nil {
			t.Error(err)
		}
	}
	wait(t, "the outbox empty", func() bool {
		pending, _ := st.Pending()
		return len(pending) == 0
	})
	wait(t, "the copies", func() bool { return len(s.Messages(t, "Sent")) == kept+sent })
	count := map[string]int{}
	for _, m := range smtp.Messages() {
		count[strings.SplitN(string(m.Data), "\r\n", 2)[0]]++
	}
	if len(count) != kept+sent {
		t.Errorf("sent %v", count)
	}
	for subject, n := range count {
		if n != 1 {
			t.Errorf("%s was sent %d times", subject, n)
		}
	}
	if drafts := s.Messages(t, "Drafts"); len(drafts) != 1 || drafts[0].Subject != "offline draft" {
		t.Errorf("drafts = %+v", drafts)
	}
}

// TestServerRestarted checks that when the server drops every connection,
// as one that restarts does, the engine carries on without an error: the
// watcher connects again and has the inbox checked for what arrived while
// it was away, and a sync on a connection that has gone is tried again on
// another rather than failing.
func TestServerRestarted(t *testing.T) {
	t.Cleanup(engine.SetWatchRetry(50 * time.Millisecond))
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapIdle), "Work")
	s.Append(t, "INBOX", "first")
	s.Append(t, "Work", "work")
	e, l, _ := start(t, s)
	wait(t, "the inbox", func() bool { return subjects(l.index("INBOX")) == "first" })
	e.Refresh("Work")
	wait(t, "the folder", func() bool { return subjects(l.index("Work")) == "work" })
	idling := func() bool {
		for _, cmd := range s.Commands() {
			if cmd == "IDLE" {
				return true
			}
		}
		return false
	}
	wait(t, "the engine idling", idling)

	s.Restart(t)
	s.Append(t, "INBOX", "while away")
	wait(t, "the mail that arrived while away", func() bool { return subjects(l.index("INBOX")) == "first,while away" })
	wait(t, "the engine idling again", idling)
	s.Append(t, "INBOX", "after")
	wait(t, "the mail that arrived after", func() bool { return subjects(l.index("INBOX")) == "first,while away,after" })

	// The connection of the syncs has gone again; the next is made anew.
	s.Restart(t)
	s.Append(t, "Work", "more work")
	e.Refresh("Work")
	wait(t, "the folder again", func() bool { return subjects(l.index("Work")) == "work,more work" })
	if errs := l.errors(); len(errs) > 0 {
		t.Errorf("errors: %v", errs)
	}
}
