package ui

import (
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/nomad-software/qute"
	"github.com/nomad-software/qute/qtcore"
	"github.com/nomad-software/qute/qtwidgets"
	"github.com/nomad-software/qute/qutetest"

	"github.com/nomad-software/apemail/internal/account"
	"github.com/nomad-software/apemail/internal/addressbook"
	"github.com/nomad-software/apemail/internal/engine"
	"github.com/nomad-software/apemail/internal/message"
	"github.com/nomad-software/apemail/internal/remote/remotetest"
	"github.com/nomad-software/apemail/internal/send/sendtest"
)

// TestInBackground checks that what is done away from the UI's thread is
// answered on it while the widget it is for is there, and not once the
// widget has been destroyed, as a dialog answered meanwhile has been.
func TestInBackground(t *testing.T) {
	for _, destroy := range []bool{false, true} {
		release := make(chan struct{})
		worked, answered := false, false
		var w *qtwidgets.QWidget
		qutetest.Run(t, func() {
			w = qtwidgets.NewQWidget(nil)
			// A widget within it goes with it.
			inner := qtwidgets.NewQWidget(w)
			inBackground(inner, func() {
				<-release
				worked = true
			}, func() { answered = worked })
			if destroy {
				w.Delete()
			}
		})
		close(release)
		// Whatever is posted to the UI's thread is run by the time
		// something posted after it is.
		time.Sleep(50 * time.Millisecond)
		qutetest.Run(t, func() {})
		qutetest.Run(t, func() {
			if answered == destroy {
				t.Errorf("destroyed %v: answered %v", destroy, answered)
			}
			if !destroy {
				w.Delete()
			}
		})
	}
}

// findButton returns the push button within the widget with the text.
func findButton(t *testing.T, within qtwidgets.QWidgetLike, text string) *qtwidgets.QPushButton {
	t.Helper()
	for _, w := range qtwidgets.QApplicationAllWidgets() {
		if w.Inherits("QPushButton") && within.AsQWidget().IsAncestorOf(w) {
			if b := qtwidgets.QPushButtonCast(w); b.Text() == text {
				return b
			}
		}
	}
	t.Fatalf("there is no %q button", text)
	return nil
}

// TestAccountDialogClosedWhileTesting checks that an answer from the server
// that arrives after the account dialog was closed, and deleted with its
// widgets, is not shown in it.
func TestAccountDialogClosedWhileTesting(t *testing.T) {
	// A server that says nothing until the test lets it go.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		if c, err := ln.Accept(); err == nil {
			accepted <- c
		}
	}()
	a := account.New()
	addr := ln.Addr().(*net.TCPAddr)
	a.Incoming = account.Server{Host: "127.0.0.1", Port: addr.Port, Security: account.SecurityNone, Auth: account.AuthPassword, Username: "u", Password: "p"}
	// Each server has a button to test it, and either is the one found.
	a.Outgoing = a.Incoming

	var d *qtwidgets.QDialog
	gone := false
	qutetest.Run(t, func() {
		parent := qtwidgets.NewQWidget(nil)
		t.Cleanup(func() { qute.PostSync(parent.Delete) })
		d = newAccountDialog(parent, a, false, nil, func(account.Account) {})
		d.OnDestroyed(func(*qtcore.QObject) { gone = true })
		findButton(t, d, "Test Connection").Click()
	})
	var conn net.Conn
	select {
	case conn = <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("the server was not reached")
	}
	qutetest.Run(t, func() { d.Reject() })
	wait(t, "the dialog deleted", func() bool { return gone })
	// The test fails now, and its answer has no dialog to be shown in.
	conn.Close()
	time.Sleep(100 * time.Millisecond)
	qutetest.Run(t, func() {})
}

// TestCloseWhileSavingDraft checks that the window to write a message in
// stays open while a draft of it is being saved, as what the save ends with
// is shown in it, and closes once it has been saved.
func TestCloseWhileSavingDraft(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Drafts")
	w := newTestWindow(t, s)
	wait(t, "the account", func() bool { return len(w.engines) == 1 })
	c := openComposer(t, w)
	held, release := s.Hold(t, "APPEND")
	typeInBody(t, c, "Not finished.")
	qutetest.Run(t, func() { c.saveDraft(nil) })
	select {
	case <-held:
	case <-time.After(5 * time.Second):
		t.Fatal("the draft was not appended")
	}
	qutetest.Run(t, func() {
		// Were it asked, the question would wait for an answer: it
		// is not, as the window does not close while saving.
		c.Close()
		if !composers[c] {
			t.Error("the window closed while the draft was being saved")
		}
	})
	release()
	wait(t, "the draft saved", func() bool { return !c.savingDraft && len(c.saved) == 1 })
	qutetest.Run(t, func() {
		if !strings.Contains(c.StatusBar().CurrentMessage(), "saved") {
			t.Errorf("status = %q", c.StatusBar().CurrentMessage())
		}
		c.Close()
	})
	wait(t, "the window closed", func() bool { return !composers[c] })
	if msgs := s.Messages(t, "Drafts"); len(msgs) != 1 {
		t.Errorf("drafts = %+v", msgs)
	}
}

// TestStatusOfWindow checks that what the window itself is doing is shown,
// as applying the settings is while there are no accounts, and that what an
// engine that has been closed says is not.
func TestStatusOfWindow(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus))
	w := newTestWindow(t, s, func(a *account.Account) { a.CheckOnStart = false })
	wait(t, "the account", func() bool { return len(w.engines) == 1 })
	qutetest.Run(t, func() {
		w.stopAccounts()
		w.setDoing("Applying the settings…")
		if got := w.statusText.Text(); got != "Applying the settings…" {
			t.Errorf("status = %q", got)
		}
		// An engine closed meanwhile is not kept to.
		w.Status("gone", "Checking INBOX…")
		if got := w.statusText.Text(); got != "Applying the settings…" {
			t.Errorf("status = %q", got)
		}
		if _, ok := w.statuses["gone"]; ok {
			t.Error("the status of an engine that has been closed is kept")
		}
		w.setDoing("")
		if got := w.statusText.Text(); got != "" {
			t.Errorf("status = %q", got)
		}
	})
}

// TestManyAddressBookSaves checks that changes made one after another
// faster than the address book is saved are all saved, the last of them
// last: each save is made away from the UI's thread, and one left behind
// by a later one is not made over it.
func TestManyAddressBookSaves(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus))
	w := newTestWindow(t, s, func(a *account.Account) { a.CheckOnStart = false })
	waitForBook(t, w)
	const n = 30
	qutetest.Run(t, func() {
		for i := range n {
			if err := w.changeAddressBook(func(b *addressbook.Book) (bool, error) {
				return b.Add(addressbook.Contact{Email: fmt.Sprintf("c%02d@example.com", i)})
			}); err != nil {
				t.Fatal(err)
			}
		}
		// And one taken out again, so that the last save is not
		// the largest.
		if err := w.changeAddressBook(func(b *addressbook.Book) (bool, error) { return b.Remove("c00@example.com"), nil }); err != nil {
			t.Fatal(err)
		}
	})
	if got := savedBook(t, w).Contacts; len(got) != n-1 || got[0].Email != "c01@example.com" {
		t.Errorf("saved %d: %+v", len(got), got)
	}
}

// TestOtherMessageChosenWhileDownloading checks that a message that arrives
// after another was chosen is not shown over it.
func TestOtherMessageChosenWhileDownloading(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus))
	s.Append(t, "INBOX", "first")
	s.Append(t, "INBOX", "second")
	w := newTestWindow(t, s)
	wait(t, "the messages", func() bool { return len(w.list.rows) == 2 })
	held, release := s.Hold(t, "BODY.PEEK[]")
	qutetest.Run(t, func() { clickRow(w.list, 0) })
	select {
	case <-held:
	case <-time.After(5 * time.Second):
		t.Fatal("the message was not asked for")
	}
	var want string
	qutetest.Run(t, func() {
		want = w.list.rows[1].Subject
		clickRow(w.list, 1)
	})
	release()
	wait(t, "the second message", func() bool { return w.view.msg != nil && w.view.msg.Subject == want })
	// What was asked for first has arrived by now, and is not shown.
	time.Sleep(100 * time.Millisecond)
	qutetest.Run(t, func() {
		if w.view.msg.Subject != want {
			t.Errorf("shown %q, not %q", w.view.msg.Subject, want)
		}
	})
}

// TestImagesOfAnotherMessage checks that the images of a message that
// arrive once another is shown are not shown with it, and that the button
// of the other is left as it is.
func TestImagesOfAnotherMessage(t *testing.T) {
	s := newImageServer(t)
	s.hold = make(chan struct{})
	var v *messageView
	qutetest.Run(t, func() {
		v = newMessageView()
		v.show(imageMessage(s, "/a.png"), "INBOX", 1, 1)
		v.loadImages.Click()
	})
	t.Cleanup(func() { qutetest.Run(t, func() { v.DeleteLater() }) })
	eventually(t, "the image to be asked for", func() bool { return s.count("/a.png") == 1 })

	// The images arrive while the UI's thread is busy, and are shown
	// after the other message is.
	other := imageMessage(s, "/b.png")
	qutetest.Run(t, func() {
		close(s.hold)
		time.Sleep(300 * time.Millisecond)
		v.show(other, "INBOX", 1, 2)
	})
	time.Sleep(100 * time.Millisecond)
	qutetest.Run(t, func() {
		if len(v.images) != 0 || v.msg != other {
			t.Errorf("the images of the first are kept with the other: %d", len(v.images))
		}
		if !v.loadImages.IsEnabled() || v.loadImages.Text() != "Load Remote Images" {
			t.Errorf("the other's button says %q", v.loadImages.Text())
		}
	})
}

// TestImagesAfterViewGone checks that images that arrive once the view has
// been destroyed, as when the window closes, are not shown in it.
func TestImagesAfterViewGone(t *testing.T) {
	s := newImageServer(t)
	s.hold = make(chan struct{})
	var v *messageView
	qutetest.Run(t, func() {
		v = newMessageView()
		v.show(imageMessage(s, "/a.png"), "INBOX", 1, 1)
		v.loadImages.Click()
	})
	eventually(t, "the image to be asked for", func() bool { return s.count("/a.png") == 1 })
	qutetest.Run(t, func() { v.Delete() })
	close(s.hold)
	time.Sleep(200 * time.Millisecond)
	// Had the view been touched, the test would have crashed.
	qutetest.Run(t, func() {
		if !v.gone {
			t.Error("the view does not know it is gone")
		}
	})
}

// TestCloseWhileBodyAnswers checks that the window to write a message in
// does not close while it waits for its body to say what it holds, to send
// it, and is sent once it has, as what was written then.
func TestCloseWhileBodyAnswers(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Sent")
	smtp := sendtest.Start(t, true)
	w := newTestWindow(t, s, func(a *account.Account) { a.Outgoing = smtp.Account })
	wait(t, "the account", func() bool { return len(w.engines) == 1 })
	c := openComposer(t, w)
	typeInBody(t, c, "Written")
	qutetest.Run(t, func() {
		// Were it to close, it would not ask about what is written.
		c.body.unmodified()
		c.send()
		// The body answers on a later turn of the event loop.
		if c.Close() || !composers[c] {
			t.Error("the window closed while the body was asked what it holds")
		}
	})
	wait(t, "the window closed", func() bool { return !composers[c] })
	if msgs := smtp.Messages(); len(msgs) != 1 || !strings.Contains(string(msgs[0].Data), "Written") {
		t.Errorf("the server was given %+v", msgs)
	}
}

// TestDraftBeforeBodyLoads checks that what the body holds, asked for
// before it has loaded, is told once it has, as it is then.
func TestDraftBeforeBodyLoads(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	w := newTestWindow(t, s, func(a *account.Account) { a.Signature = "Ada" })
	wait(t, "the account", func() bool { return len(w.engines) == 1 })
	var c *composer
	var text string
	done := false
	qutetest.Run(t, func() {
		w.composeNew("bob@example.com", "Early")
		for c = range composers {
		}
		if c.body.loaded {
			t.Fatal("the body loaded at once")
		}
		c.draft(func(d *message.Draft, err error) {
			if err != nil {
				t.Error(err)
			}
			text, done = d.Text, true
		})
	})
	t.Cleanup(func() { closeUnchanged(c) })
	wait(t, "the draft", func() bool { return done })
	if !strings.HasSuffix(text, "-- \nAda") {
		t.Errorf("the draft is %q", text)
	}
}

// TestBodyImagesAfterClose checks that images fetched for the body of a
// message being written that arrive once its window has closed are not
// shown in it.
func TestBodyImagesAfterClose(t *testing.T) {
	images := newImageServer(t)
	images.hold = make(chan struct{})
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	w := newTestWindow(t, s, func(a *account.Account) {
		a.ComposeHTML = true
		a.LoadRemoteImages = true
	})
	wait(t, "the account", func() bool { return len(w.engines) == 1 })
	var c *composer
	qutetest.Run(t, func() {
		w.composeDraft(w.engines[w.order[0]], imageMessage(images, "/a.png"), engine.Saved{Folder: "Drafts", UID: 1})
		for c = range composers {
		}
	})
	eventually(t, "the image to be asked for", func() bool { return images.count("/a.png") == 1 })
	body := c.body
	closeUnchanged(c)
	wait(t, "the body destroyed", func() bool { return body.gone })
	close(images.hold)
	time.Sleep(200 * time.Millisecond)
	// Had the body been touched, the test would have crashed.
	qutetest.Run(t, func() {
		if len(body.webImages) != 0 {
			t.Errorf("%d images were kept by the body gone", len(body.webImages))
		}
	})
}

// TestTypedWhileSavingDraft checks that what is typed while a draft is being
// saved is not taken as saved when it has been, as it is not in the draft:
// closing the window then asks what to do with it.
func TestTypedWhileSavingDraft(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Drafts")
	w := newTestWindow(t, s)
	wait(t, "the account", func() bool { return len(w.engines) == 1 })
	c := openComposer(t, w)
	typeInBody(t, c, "Saved.")
	held, release := s.Hold(t, "APPEND")
	qutetest.Run(t, func() { c.saveDraft(nil) })
	select {
	case <-held:
	case <-time.After(5 * time.Second):
		t.Fatal("the draft was not appended")
	}
	typeInBody(t, c, " Not saved.")
	release()
	wait(t, "the draft saved", func() bool { return !c.savingDraft && len(c.saved) == 1 })
	if !onUI(c.body.modified) {
		t.Error("what was typed while saving is taken as saved")
	}
	asked := answerSave(t, qtwidgets.QMessageBoxDiscard)
	qutetest.Run(t, func() { c.Close() })
	if *asked != 1 {
		t.Errorf("asked %d times on closing", *asked)
	}
}

// TestFieldEditedIsChange checks that what is typed in a field, as the
// subject, is a change that closing the window asks about, as what is
// written in the body is, while what the program puts in one is not.
func TestFieldEditedIsChange(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Drafts")
	w := newTestWindow(t, s)
	wait(t, "the account", func() bool { return len(w.engines) == 1 })
	c := openComposer(t, w)
	qutetest.Run(t, func() {
		c.to.SetText("bob@example.com")
		if c.body.modified() {
			t.Error("an address the program put in is taken as a change")
		}
		qutetest.Type(c.subject, "Plans")
		if !c.body.modified() {
			t.Error("the subject typed is not taken as a change")
		}
	})
	closeUnchanged(c)
}

// TestSendWhileSavingDraft checks that a message is not sent while a draft
// of it is being saved, as the window would stay open once it was sent, to
// be sent again, and that it is sent once the draft has been saved.
func TestSendWhileSavingDraft(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Drafts", "Sent")
	smtp := sendtest.Start(t, true)
	w := newTestWindow(t, s, func(a *account.Account) { a.Outgoing = smtp.Account })
	wait(t, "the account", func() bool { return len(w.engines) == 1 })
	c := openComposer(t, w)
	qutetest.Run(t, func() {
		c.to.SetText("bob@example.com")
		c.subject.SetText("Plans")
	})
	held, release := s.Hold(t, "APPEND")
	qutetest.Run(t, func() { c.saveDraft(nil) })
	select {
	case <-held:
	case <-time.After(5 * time.Second):
		t.Fatal("the draft was not appended")
	}
	qutetest.Run(t, func() { c.send() })
	time.Sleep(200 * time.Millisecond)
	if msgs := smtp.Messages(); len(msgs) != 0 {
		t.Errorf("sent while the draft was saved: %d", len(msgs))
	}
	release()
	wait(t, "the draft saved", func() bool { return !c.savingDraft })
	qutetest.Run(t, func() { c.send() })
	wait(t, "the window closed", func() bool { return !composers[c] })
	if msgs := smtp.Messages(); len(msgs) != 1 {
		t.Errorf("the server was given %d messages", len(msgs))
	}
}
