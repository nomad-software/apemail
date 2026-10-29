package ui

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode"

	"github.com/emersion/go-imap/v2"
	gosmtp "github.com/emersion/go-smtp"
	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/mainthread"
	"github.com/mappu/miqt/qt6/webengine"

	"github.com/nomad-software/apemail/internal/account"
	"github.com/nomad-software/apemail/internal/message"
	"github.com/nomad-software/apemail/internal/remote/remotetest"
	"github.com/nomad-software/apemail/internal/send/sendtest"
	"github.com/nomad-software/apemail/internal/settings"
)

func TestReply(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Sent")
	s.Append(t, "INBOX", "Bernoulli numbers")
	w := newTestWindow(t, s)
	wait(t, "the inbox", func() bool { return len(w.list.rows) == 1 && w.folder == "INBOX" })
	mainthread.Wait(func() {
		clickRow(w.list, 0)
	})
	wait(t, "the message", func() bool { return w.view.msg != nil })

	var c *composer
	mainthread.Wait(func() {
		w.replyTo(false)
		for c = range composers {
		}
	})
	if c == nil {
		t.Fatal("no window was opened")
	}
	mainthread.Wait(func() {
		if got := c.to.Text(); got != "Alice <alice@example.com>" {
			t.Errorf("to = %q", got)
		}
		if got := c.subject.Text(); got != "Re: Bernoulli numbers" {
			t.Errorf("subject = %q", got)
		}
	})
	if _, body := bodyOf(t, c); !strings.Contains(body, "Alice <alice@example.com> wrote:\n> The body of Bernoulli numbers.") {
		t.Errorf("body = %q", body)
	}
	if d := draftOf(t, c); d.InReplyTo == "" || d.From.Address != "ada@example.com" {
		t.Errorf("draft = %+v", d)
	}
	mainthread.Wait(func() {
		if !c.Close() {
			t.Error("the window did not close")
		}
		if len(composers) != 0 {
			t.Error("the window is still listed")
		}
	})
}

func TestBold(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	w := newTestWindow(t, s, func(a *account.Account) { a.ComposeHTML = true })
	wait(t, "the account", func() bool { return len(w.engines) == 1 })

	c := openComposer(t, w)
	// Triggering the button, as clicking it or its shortcut does, makes
	// what is typed next bold, and it shows that it is.
	mainthread.Wait(c.bold.Trigger)
	typeInBody(t, c, "strong")
	wait(t, "bold", func() bool { return c.bold.IsChecked() })
	bodyHas(t, c, "<b>strong</b>")
}

// TestShortcutsHaveModifiers checks that no shortcut is a character key on
// its own, which is too easily pressed by mistake. Function keys and special
// keys, as F5 and Delete, can be used alone.
func TestShortcutsHaveModifiers(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	w := newTestWindow(t, s, func(a *account.Account) { a.ComposeHTML = true })
	wait(t, "the account", func() bool { return len(w.engines) == 1 })

	mainthread.Wait(func() {
		actions := []*qt.QAction{w.getMail, w.write, w.reply, w.replyAll, w.forward, w.archive, w.del,
			w.markRead, w.markUnread, w.viewSource, w.addressBook, w.editAccount, w.removeAccount, w.updateCounts, w.syncAll}
		actions = append(actions, w.MenuBar().Actions()...)
		w.composeNew("", "")
		for c := range composers {
			actions = append(actions, c.bar.QWidget.Actions()...)
			actions = append(actions, c.attachList.QWidget.Actions()...)
			actions = append(actions, c.QWidget.Actions()...)
			e := c.edit
			actions = append(actions, c.closeItem, e.undo, e.redo, e.cut, e.copy, e.paste, e.pastePlain, e.del, e.selectAll, c.receipt, c.dsn)
			defer c.Close()
		}
		for _, a := range actions {
			for _, k := range a.Shortcuts() {
				key := k.ToString()
				// A character key is named by the character; function
				// and special keys by a word, as F5 and Del.
				if !strings.Contains(key, "+") && len([]rune(key)) == 1 {
					t.Errorf("%q has the character key %q on its own", a.Text(), key)
				}
			}
		}
		if got := w.archive.Shortcut().ToString(); got != "Ctrl+Shift+A" {
			t.Errorf("archive = %q", got)
		}
		if got := w.del.Shortcut().ToString(); got != "Del" {
			t.Errorf("delete = %q", got)
		}
		if got := w.getMail.Shortcut().ToString(); got != "F5" {
			t.Errorf("get mail = %q", got)
		}
		// Ctrl+A is left to select every message.
		for _, a := range actions {
			if a.Shortcut().ToString() == "Ctrl+A" {
				t.Errorf("%q takes Ctrl+A from Select All", a.Text())
			}
		}
	})
}

func TestReplacedDraftsGoToTrash(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Drafts", "Trash")
	w := newTestWindow(t, s)
	wait(t, "the account", func() bool { return len(w.engines) == 1 })
	asked := answer(t, false)

	var c *composer
	mainthread.Wait(func() {
		w.composeNew("bob@example.com", "Plans")
		for c = range composers {
		}
		c.saveDraft(nil)
	})
	wait(t, "the first draft", func() bool { return len(c.saved) == 1 })
	typeInBody(t, c, "Changed")
	mainthread.Wait(func() { c.saveDraft(nil) })
	wait(t, "the second draft", func() bool { return len(c.saved) == 2 })

	// Closing the window keeps the latest draft, and moves the one it
	// replaced to the trash without asking.
	mainthread.Wait(func() { c.Close() })
	eventually(t, "the replaced draft in the trash", func() bool { return len(s.Messages(t, "Trash")) == 1 })
	if msgs := s.Messages(t, "Drafts"); len(msgs) != 1 {
		t.Errorf("drafts = %+v", msgs)
	}
	if *asked != 0 {
		t.Errorf("asked %d times", *asked)
	}
}

// TestComposeSizeIsKept checks that a window to write a message in opens the
// size the last one was left.
func TestComposeSizeIsKept(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	w := newTestWindow(t, s)
	wait(t, "the account", func() bool { return len(w.engines) == 1 })

	mainthread.Wait(func() {
		w.composeNew("", "")
		for c := range composers {
			c.Resize(720, 540)
			c.Close()
		}
	})
	if config := settings.Load(w.files.Settings); config.ComposeWidth != 720 || config.ComposeHeight != 540 {
		t.Fatalf("saved = %dx%d", config.ComposeWidth, config.ComposeHeight)
	}

	mainthread.Wait(func() {
		w.composeNew("", "")
		for c := range composers {
			if c.Width() != 720 || c.Height() != 540 {
				t.Errorf("size = %dx%d", c.Width(), c.Height())
			}
			c.Close()
		}
	})
}

// openComposer opens a window to write a new message in and returns it.
func openComposer(t *testing.T, w *Window) *composer {
	t.Helper()
	var c *composer
	mainthread.Wait(func() {
		w.composeNew("bob@example.com", "Pictures")
		for c = range composers {
		}
	})
	if c == nil {
		t.Fatal("no window was opened")
	}
	// It is closed when the test ends, as nothing had been written in it,
	// so that no question is left open that no one answers.
	t.Cleanup(func() { closeUnchanged(c) })
	bodyLoaded(t, c)
	return c
}

// bodyLoaded waits for the body of the window to have loaded, and its
// cursor to have been put where the message is written: the script is
// asked to put it there once the page has loaded, and what is asked of it
// is done in turn, so once it has said what the body holds it has done
// that. The page's own actions, as Paste, are not done in turn with it.
func bodyLoaded(t *testing.T, c *composer) {
	t.Helper()
	wait(t, "the body to load", func() bool { return c.body.loaded })
	bodyOf(t, c)
}

// bodyOf returns what the body of the window holds, as HTML and as plain
// text, once it has said.
func bodyOf(t *testing.T, c *composer) (html, text string) {
	t.Helper()
	done := false
	mainthread.Wait(func() { c.body.content(func(h, x string) { html, text, done = h, x, true }) })
	wait(t, "the body to say what it holds", func() bool { return done })
	return html, text
}

// bodyHas waits for the HTML of the body to have the markup.
func bodyHas(t *testing.T, c *composer, markup string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		html, _ := bodyOf(t, c)
		if strings.Contains(html, markup) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the body has no %s: %s", markup, html)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// draftOf returns the message the window has written, once its body has
// said what it holds.
func draftOf(t *testing.T, c *composer) *message.Draft {
	t.Helper()
	var d *message.Draft
	var err error
	done := false
	mainthread.Wait(func() { c.draft(func(got *message.Draft, e error) { d, err, done = got, e, true }) })
	wait(t, "the draft", func() bool { return done })
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// typeInBody types the text in the body of the window where its cursor is,
// a key at a time, as the user does, and waits for the body to have it.
func typeInBody(t *testing.T, c *composer, text string) {
	t.Helper()
	_, before := bodyOf(t, c)
	mainthread.Wait(func() {
		c.ActivateWindow()
		c.body.web.SetFocus()
		target := c.body.web.FocusProxy()
		for _, r := range text {
			for _, typ := range []qt.QEvent__Type{qt.QEvent__KeyPress, qt.QEvent__KeyRelease} {
				e := qt.NewQKeyEvent3(typ, int(unicode.ToUpper(r)), qt.NoModifier, string(r))
				qt.QCoreApplication_SendEvent(target.QObject, e.QEvent)
				e.Delete()
			}
		}
	})
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, now := bodyOf(t, c); strings.Count(now, text) > strings.Count(before, text) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%q was not typed in the body", text)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// closeUnchanged closes the window, if it is open, as if nothing had been
// written in it, so that it does not ask what to do with it.
func closeUnchanged(c *composer) {
	mainthread.Wait(func() {
		if composers[c] {
			c.body.modified = false
			c.Close()
		}
	})
}

// pngImage returns a PNG of a width.
func pngImage(t *testing.T, width int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, width, 10))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestLists(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	w := newTestWindow(t, s, func(a *account.Account) { a.ComposeHTML = true })
	wait(t, "the account", func() bool { return len(w.engines) == 1 })
	c := openComposer(t, w)
	typeInBody(t, c, "one")
	buttons := func(bullets, numbers bool) func() bool {
		return func() bool { return c.bullets.IsChecked() == bullets && c.numbers.IsChecked() == numbers }
	}

	mainthread.Wait(c.bullets.Trigger)
	bodyHas(t, c, "<ul><li>one")
	wait(t, "bullets checked", buttons(true, false))

	// Numbers turn the list into a numbered one.
	mainthread.Wait(c.numbers.Trigger)
	bodyHas(t, c, "<ol><li>one")
	wait(t, "numbers checked", buttons(false, true))

	// Pressing it again takes the paragraph out of the list.
	mainthread.Wait(c.numbers.Trigger)
	wait(t, "no list checked", buttons(false, false))
	if html, _ := bodyOf(t, c); strings.Contains(html, "<ol") || strings.Contains(html, "<ul") || !strings.Contains(html, "one") {
		t.Errorf("html = %s", html)
	}
}

func TestInsertImage(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	w := newTestWindow(t, s, func(a *account.Account) { a.ComposeHTML = true })
	wait(t, "the account", func() bool { return len(w.engines) == 1 })
	c := openComposer(t, w)

	mainthread.Wait(func() {
		// Something that is not an image is refused.
		if err := c.putImage("notes.png", []byte("not a picture")); err == nil || len(c.images) != 0 {
			t.Errorf("err = %v, images = %d", err, len(c.images))
		}
		if err := c.putImage("wide.png", pngImage(t, 900)); err != nil {
			t.Fatal(err)
		}
	})
	bodyHas(t, c, "<img")
	d := draftOf(t, c)
	if len(d.Images) != 1 || d.Images[0].ContentType != "image/png" || len(d.Attachments) != 0 {
		t.Fatalf("images = %+v, attachments = %d", d.Images, len(d.Attachments))
	}
	// It is shown inline, no wider than maxImageWidth.
	if !strings.Contains(d.HTML, `src="cid:`+d.Images[0].ContentID+`"`) || !strings.Contains(d.HTML, `width="600"`) {
		t.Errorf("html = %s", d.HTML)
	}
	if strings.ContainsRune(d.Text, '\uFFFC') {
		t.Errorf("text = %q", d.Text)
	}

	// An image taken out of the message does not go with it.
	mainthread.Wait(func() { c.body.setHTML("<p>No pictures</p>") })
	bodyLoaded(t, c)
	if d := draftOf(t, c); len(d.Images) != 0 {
		t.Errorf("images = %+v", d.Images)
	}
}

// TestPicturesShown checks that a picture put in the message is shown in
// it, as the body asks for it by cid:, and nothing it does not have.
func TestPicturesShown(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	w := newTestWindow(t, s, func(a *account.Account) { a.ComposeHTML = true })
	wait(t, "the account", func() bool { return len(w.engines) == 1 })
	c := openComposer(t, w)
	mainthread.Wait(func() {
		if err := c.putImage("red.png", redImage(t)); err != nil {
			t.Fatal(err)
		}
	})
	wait(t, "the picture", func() bool { return pixelsOf(c.body.web.QWidget, 0xff0000) >= 60*40 })
	if _, _, ok := webContent("cid:other", mainthread.Wait2(func() string { return c.body.pageURL("edit") })); ok {
		t.Error("a picture the message does not have is served")
	}
}

// TestImageInDraft checks that an image put in a message is saved with the
// draft, as part of its HTML, and is still there when the draft is continued.
func TestImageInDraft(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Drafts")
	w := newTestWindow(t, s, func(a *account.Account) { a.ComposeHTML = true })
	wait(t, "the account", func() bool { return len(w.engines) == 1 })
	c := openComposer(t, w)

	mainthread.Wait(func() {
		if err := c.putImage("dot.png", pngImage(t, 10)); err != nil {
			t.Fatal(err)
		}
	})
	bodyHas(t, c, "<img")
	mainthread.Wait(func() { c.saveDraft(nil) })
	wait(t, "the draft", func() bool { return len(c.saved) == 1 })
	mainthread.Wait(func() { c.Close() })

	drafts := s.Messages(t, "Drafts")
	if len(drafts) != 1 {
		t.Fatalf("drafts = %+v", drafts)
	}
	m, err := message.Parse(drafts[0].Raw)
	if err != nil {
		t.Fatal(err)
	}
	if images := m.Images(); len(images) != 1 || !bytes.Equal(images[0].Data, pngImage(t, 10)) || len(m.Files()) != 0 {
		t.Fatalf("images = %d, files = %d in\n%s", len(images), len(m.Files()), drafts[0].Raw)
	}

	var again *composer
	mainthread.Wait(func() {
		w.composeDraft(w.engines[w.order[0]], m, "Drafts", drafts[0].UID)
		for again = range composers {
		}
	})
	bodyLoaded(t, again)
	d := draftOf(t, again)
	if len(d.Images) != 1 || d.Images[0].ContentID != m.Images()[0].ContentID || len(d.Attachments) != 0 {
		t.Errorf("images = %+v, attachments = %d", d.Images, len(d.Attachments))
	}
	// The window has the picture to show.
	if _, _, ok := webContent("cid:"+m.Images()[0].ContentID, mainthread.Wait2(func() string { return again.body.pageURL("edit") })); !ok {
		t.Error("the image is not shown")
	}
	closeUnchanged(again)

	// A message forwarded keeps the pictures it shows, not as attachments.
	var fwd *composer
	mainthread.Wait(func() {
		w.view.msg = m
		w.forwardMessage()
		for fwd = range composers {
		}
	})
	bodyLoaded(t, fwd)
	if d := draftOf(t, fwd); len(d.Images) != 1 || len(d.Attachments) != 0 {
		t.Errorf("images = %d, attachments = %d", len(d.Images), len(d.Attachments))
	}
	closeUnchanged(fwd)
}

// focus gives a field the focus, in a window shown, as clicking in it does.
func focus(w *qt.QWidget) {
	w.Window().ActivateWindow()
	w.SetFocus()
}

// TestEditMenu checks that the Edit menu acts on the field or body last in
// focus, and enables only what it can do.
func TestEditMenu(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	w := newTestWindow(t, s, func(a *account.Account) { a.ComposeHTML = true })
	wait(t, "the account", func() bool { return len(w.engines) == 1 })
	c := openComposer(t, w)

	mainthread.Wait(func() {
		clipboard := qt.QGuiApplication_Clipboard()
		// choose opens the menu, which enables what can be done, and
		// chooses an item of it.
		choose := func(a *qt.QAction) {
			c.updateEditMenu()
			a.Trigger()
		}

		focus(c.subject.QWidget)
		c.subject.SetText("Hello world")
		c.subject.Deselect()
		c.updateEditMenu()
		if c.edit.cut.IsEnabled() || c.edit.copy.IsEnabled() || c.edit.del.IsEnabled() {
			t.Error("cut, copy or delete is enabled with nothing selected")
		}
		choose(c.edit.selectAll)
		c.updateEditMenu()
		if !c.edit.cut.IsEnabled() {
			t.Error("cut is not enabled with the subject selected")
		}
		choose(c.edit.cut)
		if c.subject.Text() != "" || clipboard.Text() != "Hello world" {
			t.Errorf("cut: subject %q, clipboard %q", c.subject.Text(), clipboard.Text())
		}
		c.updateEditMenu()
		if !c.edit.undo.IsEnabled() {
			t.Error("undo is not enabled after a cut")
		}
		choose(c.edit.undo)
		if c.subject.Text() != "Hello world" {
			t.Errorf("undo: subject %q", c.subject.Text())
		}
		choose(c.edit.redo)
		if c.subject.Text() != "" {
			t.Errorf("redo: subject %q", c.subject.Text())
		}

	})

	// The body is acted on once it has the focus.
	mainthread.Wait(func() {
		c.body.setHTML("")
		focus(c.body.web.QWidget)
	})
	bodyLoaded(t, c)
	wait(t, "the body to have the focus", func() bool { return c.editor == editor(c.body) })
	clipboard := mainthread.Wait2(qt.QGuiApplication_Clipboard)
	textIs := func(what, want string) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			if _, text := bodyOf(t, c); strings.TrimSpace(text) == want {
				return
			}
			if time.Now().After(deadline) {
				_, text := bodyOf(t, c)
				t.Fatalf("%s: body %q, want %q", what, text, want)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	mainthread.Wait(func() {
		clipboard.SetText("pasted")
		c.updateEditMenu()
		c.edit.paste.Trigger()
	})
	textIs("paste", "pasted")
	mainthread.Wait(c.edit.selectAll.Trigger)
	wait(t, "the body selected", func() bool { c.updateEditMenu(); return c.edit.copy.IsEnabled() && c.edit.del.IsEnabled() })
	mainthread.Wait(func() {
		clipboard.Clear()
		c.edit.copy.Trigger()
	})
	wait(t, "the copy", func() bool { return clipboard.Text() == "pasted" })
	mainthread.Wait(c.edit.del.Trigger)
	textIs("delete", "")
	wait(t, "undo", func() bool { c.updateEditMenu(); return c.edit.undo.IsEnabled() })
	mainthread.Wait(c.edit.undo.Trigger)
	textIs("undo", "pasted")

	// Pasting without formatting pastes the text, not the markup.
	mainthread.Wait(func() {
		c.edit.selectAll.Trigger()
		mime := qt.NewQMimeData()
		mime.SetHtml("<b>bold</b>")
		mime.SetText("bold")
		clipboard.SetMimeData(mime)
		c.edit.pastePlain.Trigger()
	})
	textIs("paste without formatting", "bold")
	if html, _ := bodyOf(t, c); strings.Contains(html, "<b>") {
		t.Errorf("paste without formatting: %s", html)
	}
	closeUnchanged(c)
}

// TestSendOptions checks that a return receipt and delivery status
// notifications are asked for when, and only when, the Options menu says.
func TestSendOptions(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Sent")
	smtp := sendtest.Start(t, true)
	w := newTestWindow(t, s, func(a *account.Account) { a.Outgoing = smtp.Account })
	wait(t, "the account", func() bool { return len(w.engines) == 1 })

	for _, on := range []bool{true, false} {
		c := openComposer(t, w)
		mainthread.Wait(func() {
			c.receipt.SetChecked(on)
			c.dsn.SetChecked(on)
			c.send()
		})
		wait(t, "the window closed", func() bool { return !composers[c] })
	}

	msgs := smtp.Messages()
	if len(msgs) != 2 {
		t.Fatalf("the server was given %d messages", len(msgs))
	}
	asked, unasked := msgs[0], msgs[1]
	if !strings.Contains(string(asked.Data), "Disposition-Notification-To: \"Ada Lovelace\" <ada@example.com>") {
		t.Errorf("no return receipt in\n%s", asked.Data)
	}
	if asked.Return != gosmtp.DSNReturnHeaders || len(asked.Notify) != 1 || !slices.Equal(asked.Notify[0], []gosmtp.DSNNotify{gosmtp.DSNNotifySuccess, gosmtp.DSNNotifyFailure}) {
		t.Errorf("return = %q, notify = %v", asked.Return, asked.Notify)
	}
	if strings.Contains(string(unasked.Data), "Disposition-Notification-To") || unasked.Return != "" || unasked.Notify[0] != nil {
		t.Errorf("asked for unasked: return = %q, notify = %v\n%s", unasked.Return, unasked.Notify, unasked.Data)
	}
}

// TestReturnReceiptInDraft checks that a draft continued asks for a return
// receipt if it did when it was saved.
func TestReturnReceiptInDraft(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Drafts")
	w := newTestWindow(t, s)
	wait(t, "the account", func() bool { return len(w.engines) == 1 })
	c := openComposer(t, w)
	mainthread.Wait(func() {
		c.receipt.SetChecked(true)
		c.saveDraft(nil)
	})
	wait(t, "the draft", func() bool { return len(c.saved) == 1 })
	mainthread.Wait(func() { c.Close() })

	drafts := s.Messages(t, "Drafts")
	if len(drafts) != 1 {
		t.Fatalf("drafts = %+v", drafts)
	}
	m, err := message.Parse(drafts[0].Raw)
	if err != nil {
		t.Fatal(err)
	}
	mainthread.Wait(func() {
		w.composeDraft(w.engines[w.order[0]], m, "Drafts", drafts[0].UID)
		for c = range composers {
		}
		defer c.Close()
		if !c.receipt.IsChecked() {
			t.Error("the return receipt was lost")
		}
	})
}

// TestMenus checks the menus of the window: Format, with the buttons which
// format text, only for a message written as HTML.
func TestMenus(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	for _, html := range []bool{true, false} {
		w := newTestWindow(t, s, func(a *account.Account) { a.ComposeHTML = html })
		wait(t, "the account", func() bool { return len(w.engines) == 1 })
		c := openComposer(t, w)
		mainthread.Wait(func() {
			var titles []string
			for _, a := range c.MenuBar().Actions() {
				titles = append(titles, strings.ReplaceAll(a.Text(), "&", ""))
			}
			want := []string{"File", "Edit", "Options"}
			if html {
				want = []string{"File", "Edit", "Format", "Options"}
			}
			if !slices.Equal(titles, want) {
				t.Fatalf("html %v: menus %v", html, titles)
			}
			if !html {
				return
			}
			format := c.formatMenu
			var items []string
			for _, a := range format.Actions() {
				items = append(items, a.Text())
			}
			if !slices.Equal(items, []string{"Bold", "Italic", "Underline", "Bullets", "Numbers"}) {
				t.Errorf("format = %v", items)
			}
			// The item is the button: choosing it makes text bold, and
			// both show it.
			format.Actions()[0].Trigger()
		})
		if html {
			typeInBody(t, c, "strong")
			wait(t, "bold", func() bool { return c.bold.IsChecked() })
			bodyHas(t, c, "<b>strong</b>")
		}
		closeUnchanged(c)
	}
}

// TestCloseItem checks that File > Close, which Escape is the shortcut of,
// closes the window to write a message in.
func TestCloseItem(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	w := newTestWindow(t, s)
	wait(t, "the account", func() bool { return len(w.engines) == 1 })
	c := openComposer(t, w)
	mainthread.Wait(func() {
		// The item is in the menu it was made by, the first.
		file := qt.UnsafeNewQMenu(c.closeItem.Parent().UnsafePointer())
		if c.MenuBar().Actions()[0].UnsafePointer() != file.MenuAction().UnsafePointer() || len(file.Actions()) != 1 {
			t.Fatal("Close is not the File menu's one item")
		}
		if got := strings.ReplaceAll(c.closeItem.Text(), "&", ""); got != "Close" {
			t.Errorf("item = %q", got)
		}
		if got := c.closeItem.Shortcut().ToString(); got != "Esc" {
			t.Errorf("shortcut = %q", got)
		}
		c.closeItem.Trigger()
	})
	wait(t, "the window closed", func() bool { return !composers[c] })
}

// signatureFile writes an HTML signature with an image beside it, and
// returns its path.
func signatureFile(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "logo.png"), pngImage(t, 20), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "signature.html")
	if err := os.WriteFile(path, []byte(`<html><body><p><b>Ada Lovelace</b></p><img src="logo.png"></body></html>`), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestHTMLSignatureFile checks that a message written as HTML is signed with
// the HTML of the account's signature file, its image going with it.
func TestHTMLSignatureFile(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	path := signatureFile(t)
	w := newTestWindow(t, s, func(a *account.Account) {
		a.ComposeHTML = true
		a.Signature = "Not this"
		a.SignatureFile = path
	})
	wait(t, "the account", func() bool { return len(w.engines) == 1 })
	c := openComposer(t, w)
	html, text := bodyOf(t, c)
	// The separator keeps its space in the plain text too.
	if !strings.Contains(text, "-- \n") || !strings.Contains(text, "Ada Lovelace") || strings.Contains(text, "Not this") {
		t.Errorf("body = %q", text)
	}
	if !strings.Contains(html, "<b>Ada Lovelace</b>") {
		t.Errorf("the signature is not bold: %s", html)
	}
	d := draftOf(t, c)
	if len(d.Images) != 1 || d.Images[0].Filename != "logo.png" || !strings.Contains(d.HTML, "cid:"+d.Images[0].ContentID) {
		t.Errorf("images = %+v in %s", d.Images, d.HTML)
	}
}

// TestHTMLSignatureAsText checks that a message written as text is signed
// with the text of an HTML signature file.
func TestHTMLSignatureAsText(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	path := signatureFile(t)
	w := newTestWindow(t, s, func(a *account.Account) { a.SignatureFile = path })
	wait(t, "the account", func() bool { return len(w.engines) == 1 })
	c := openComposer(t, w)
	if _, text := bodyOf(t, c); !strings.HasSuffix(text, "\n-- \nAda Lovelace") || strings.Contains(text, "<") {
		t.Errorf("body = %q", text)
	}
	if d := draftOf(t, c); len(d.Images) != 0 || d.HTML != "" {
		t.Errorf("images = %+v, html = %q", d.Images, d.HTML)
	}
}

// TestMissingSignatureFile checks that a message is written without the
// signature, and says why, if its file cannot be read.
func TestMissingSignatureFile(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	w := newTestWindow(t, s, func(a *account.Account) { a.SignatureFile = filepath.Join(t.TempDir(), "gone.txt") })
	wait(t, "the account", func() bool { return len(w.engines) == 1 })
	c := openComposer(t, w)
	if _, text := bodyOf(t, c); strings.Contains(text, "-- ") {
		t.Errorf("body = %q", text)
	}
	mainthread.Wait(func() {
		if msg := c.StatusBar().CurrentMessage(); !strings.Contains(msg, "reading the signature") {
			t.Errorf("status = %q", msg)
		}
	})
}

func TestCheckSignatureFile(t *testing.T) {
	if err := checkSignatureFile(""); err == nil {
		t.Error("no file was taken")
	}
	if err := checkSignatureFile(filepath.Join(t.TempDir(), "gone.txt")); err == nil {
		t.Error("a missing file was taken")
	}
	if err := checkSignatureFile(signatureFile(t)); err != nil {
		t.Error(err)
	}
}

// TestRoomAboveSignature checks that the message is written on an empty
// line with more empty below it before the signature, whether it is written
// as text or HTML, with no quote, above the quote or below it, and that the
// lines to write in are plain blocks of their own, neither in the quote's
// list nor centred as the signature is. What is typed first is written
// where the cursor starts.
func TestRoomAboveSignature(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	quoteText := "Alice wrote:\n> Hello"
	// The quote ends in a list, which the lines to write in are not to
	// join.
	quoteHTML := "<p>Alice wrote:</p><blockquote><ul><li>Hello</li></ul></blockquote>"
	// The HTML signature is centred, which it is to stay, and the lines
	// to write in not to be.
	sigFile := filepath.Join(t.TempDir(), "sig.html")
	if err := os.WriteFile(sigFile, []byte(`<p align="center">Ada</p>`), 0o600); err != nil {
		t.Fatal(err)
	}
	lines := "<div>X</div>" + strings.Repeat("<div><br></div>", writingLines-1)
	for _, html := range []bool{false, true} {
		w := newTestWindow(t, s, func(a *account.Account) {
			a.ComposeHTML = html
			a.Signature = "Ada"
			if html {
				a.SignatureFile = sigFile
			}
		})
		wait(t, "the account", func() bool { return len(w.engines) == 1 })
		for _, quote := range []string{"none", "above", "below"} {
			name := fmt.Sprintf("html %v, quote %s", html, quote)
			c := openComposer(t, w)
			mainthread.Wait(func() {
				a := w.engines[w.order[0]].Account()
				a.QuoteBelow = quote == "below"
				if quote == "none" {
					c.setBody(a, "", "")
				} else {
					c.setBody(a, quoteText, quoteHTML)
				}
			})
			bodyLoaded(t, c)
			if c.body.modified {
				t.Errorf("%s: the body is taken as changed", name)
			}
			typeInBody(t, c, "X")
			if !mainthread.Wait2(func() bool { return c.body.modified }) {
				t.Errorf("%s: typing is not taken as a change", name)
			}
			body, text := bodyOf(t, c)
			if html {
				want := map[string]string{
					"none":  "<body>" + lines + `<p style="white-space: pre-wrap">-- </p><p align="center">Ada</p></body>`,
					"above": "<body>" + lines + `<p style="white-space: pre-wrap">-- </p><p align="center">Ada</p>` + quoteHTML,
					"below": quoteHTML + lines + `<p style="white-space: pre-wrap">-- </p><p align="center">Ada</p></body>`,
				}[quote]
				if !strings.Contains(body, want) {
					t.Errorf("%s: the body is\n%s\nwant within it\n%s", name, body, want)
				}
			} else {
				want := map[string]string{
					"none":  "X\n\n\n-- \nAda",
					"above": "X\n\n\n-- \nAda\n\nAlice wrote:\n> Hello",
					"below": "Alice wrote:\n> Hello\nX\n\n\n-- \nAda",
				}[quote]
				if text != want {
					t.Errorf("%s: the body is %q, want %q", name, text, want)
				}
			}
			closeUnchanged(c)
		}
	}
}

// TestRoomAboveCentredQuote checks that the lines to write in above a quote
// that begins centred are not centred, and the quote stays so.
func TestRoomAboveCentredQuote(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	w := newTestWindow(t, s, func(a *account.Account) { a.ComposeHTML = true })
	wait(t, "the account", func() bool { return len(w.engines) == 1 })
	c := openComposer(t, w)
	quote := `<p align="center">Alice wrote:</p><p>Hello</p>`
	mainthread.Wait(func() { c.setBody(w.engines[w.order[0]].Account(), "Alice wrote:", quote) })
	bodyLoaded(t, c)
	typeInBody(t, c, "X")
	bodyHas(t, c, "<body><div>X</div>"+strings.Repeat("<div><br></div>", writingLines-1)+quote)
}

// TestReplyQuickly checks that a reply to a message which nests tables a
// dozen deep, as newsletters do, opens at once with the message quoted:
// Qt's own editor took a minute to lay one out.
func TestReplyQuickly(t *testing.T) {
	cell := `<table width="100%"><tbody><tr><td align="center" style="padding: 10px">`
	var b strings.Builder
	for range 3 {
		b.WriteString(strings.Repeat(cell, 16))
		b.WriteString("<p>Your booking details. " + strings.Repeat("Some words to lay out. ", 50) + "</p>")
		b.WriteString(strings.Repeat("</td></tr></tbody></table>", 16))
	}
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	s.AppendRaw(t, "INBOX", "From: a@example.com\r\nSubject: Booking\r\nContent-Type: text/html; charset=utf-8\r\n\r\n"+b.String()+"\r\n", imap.FlagSeen)
	w := newTestWindow(t, s, func(a *account.Account) { a.ComposeHTML = true })
	wait(t, "the inbox", func() bool { return w.folder != "" && len(w.list.rows) == 1 })
	mainthread.Wait(func() { clickRow(w.list, 0) })
	wait(t, "the message", func() bool { return w.view.msg != nil })

	start := time.Now()
	var c *composer
	mainthread.Wait(func() {
		w.replyTo(false)
		for c = range composers {
		}
	})
	bodyLoaded(t, c)
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("the reply took %v to open", took)
	}
	if _, text := bodyOf(t, c); !strings.Contains(text, "Your booking details.") {
		t.Errorf("the quote is %q", text)
	}
	closeUnchanged(c)
}

// TestQuoteKeptOffline checks that a message quoted in the body runs none of
// its scripts, loads nothing from the internet, makes no connection to the
// server it names, and follows none of its links when they are clicked.
func TestQuoteKeptOffline(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	var connections atomic.Int32
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			connections.Add(1)
			c.Close()
		}
	}()
	var opened []string
	open := openURL
	openURL = func(link string) { opened = append(opened, link) }
	t.Cleanup(func() { openURL = open })

	r := "http://" + ln.Addr().String()
	quote := `<a href="` + r + `/link" style="display: block; height: 100px">A link</a>` +
		`<link rel="preconnect" href="` + r + `/preconnect"><link rel="stylesheet" href="` + r + `/style.css">` +
		`<img src="` + r + `/image.png"><div style="background: url(` + r + `/background.png)">Background</div>` +
		`<p id="p">Quoted</p><script>document.getElementById("p").textContent = "Ran"</script>`
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	w := newTestWindow(t, s, func(a *account.Account) { a.ComposeHTML = true })
	wait(t, "the account", func() bool { return len(w.engines) == 1 })
	c := openComposer(t, w)
	mainthread.Wait(func() { c.body.setHTML(quote) })
	bodyLoaded(t, c)
	mainthread.Wait(func() {
		if c.body.page.Settings().TestAttribute(webengine.QWebEngineSettings__JavascriptEnabled) {
			t.Error("JavaScript is on")
		}
		clickAtPage(c.body.web, 50, 50)
	})
	time.Sleep(time.Second)
	if _, text := bodyOf(t, c); !strings.Contains(text, "Quoted") || strings.Contains(text, "Ran") {
		t.Errorf("the body is %q", text)
	}
	if n := connections.Load(); n != 0 {
		t.Errorf("%d connections were made to the server", n)
	}
	mainthread.Wait(func() {
		if len(opened) != 0 {
			t.Errorf("opened %q", opened)
		}
		if got := encoded(c.body.page.Url()); got != c.body.pageURL("edit") {
			t.Errorf("the body went to %s", got)
		}
	})
}

// TestBodyFollowsTheme checks that the body takes the colours of a theme
// when it changes, at once and without losing what is written.
func TestBodyFollowsTheme(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	w := newTestWindow(t, s, func(a *account.Account) { a.ComposeHTML = true })
	wait(t, "the account", func() bool { return len(w.engines) == 1 })
	c := openComposer(t, w)
	typeInBody(t, c, "Kept")
	useTheme(t, settings.ThemeDark)
	wait(t, "the body in the dark theme", func() bool { return pixelsOf(c.body.web.QWidget, 0x1e1e1e) > 0 })
	if _, text := bodyOf(t, c); !strings.Contains(text, "Kept") {
		t.Errorf("the body is %q", text)
	}
}

// pixelsOf counts the pixels of the colour the widget shows.
func pixelsOf(w *qt.QWidget, colour uint) int {
	return mainthread.Wait2(func() int {
		pixmap := w.Grab()
		img := pixmap.ToImage()
		runtime.KeepAlive(pixmap)
		n := 0
		for y := range img.Height() {
			for x := range img.Width() {
				if img.Pixel(x, y)&0xffffff == colour {
					n++
				}
			}
		}
		return n
	})
}

// replyWindow shows the window, opens the message in the inbox, which shows
// the image of the server at /a.png, and returns the window and the
// message.
func replyWindow(t *testing.T, images *imageServer, always bool) *Window {
	t.Helper()
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	s.AppendRaw(t, "INBOX", "From: a@example.com\r\nSubject: Pictures\r\nContent-Type: text/html; charset=utf-8\r\n\r\n"+
		imageMessage(images, "/a.png").HTML+"\r\n", imap.FlagSeen)
	w := newTestWindow(t, s, func(a *account.Account) {
		a.ComposeHTML = true
		a.LoadRemoteImages = always
	})
	// The message asks for its images only where it is seen.
	mainthread.Wait(w.Show)
	wait(t, "the inbox", func() bool { return w.folder != "" && len(w.list.rows) == 1 })
	mainthread.Wait(func() { clickRow(w.list, 0) })
	wait(t, "the message", func() bool { return w.view.msg != nil })
	return w
}

// reply opens a window to reply to the message shown, and returns it once
// its body has loaded.
func reply(t *testing.T, w *Window) *composer {
	t.Helper()
	var c *composer
	mainthread.Wait(func() {
		w.replyTo(false)
		for c = range composers {
		}
	})
	t.Cleanup(func() { closeUnchanged(c) })
	bodyLoaded(t, c)
	return c
}

// TestReplyShowsImages checks that a reply shows the images of the message
// quoted that the message view has loaded, as the account loads them when
// a message is shown, without their being fetched again.
func TestReplyShowsImages(t *testing.T) {
	images := newImageServer(t)
	w := replyWindow(t, images, true)
	wait(t, "the images of the message", func() bool { return len(w.view.images) == 1 })
	c := reply(t, w)
	wait(t, "the image in the reply", func() bool { return pixelsOf(c.body.web.QWidget, 0xff0000) >= 60*40 })
	if n := images.count("/a.png"); n != 1 {
		t.Errorf("the image was fetched %d times", n)
	}
	if n := images.count("/style.css"); n != 0 {
		t.Errorf("the stylesheet was fetched %d times", n)
	}
}

// TestReplyImagesFollowAccount checks that a reply loads no image of the web
// if the account does not load them when a message is shown, and shows those
// the user has had loaded in the message view.
func TestReplyImagesFollowAccount(t *testing.T) {
	images := newImageServer(t)
	w := replyWindow(t, images, false)
	c := reply(t, w)
	time.Sleep(time.Second)
	if n := pixelsOf(c.body.web.QWidget, 0xff0000); n != 0 {
		t.Errorf("%d red pixels are shown", n)
	}
	if n := images.count(""); n != 0 {
		t.Fatalf("%d things were fetched", n)
	}
	closeUnchanged(c)

	buttonShown(t, w.view)
	mainthread.Wait(w.view.loadImages.Click)
	wait(t, "the images of the message", func() bool { return len(w.view.images) == 1 })
	c = reply(t, w)
	wait(t, "the image in the reply", func() bool { return pixelsOf(c.body.web.QWidget, 0xff0000) >= 60*40 })
	if n := images.count("/a.png"); n != 1 {
		t.Errorf("the image was fetched %d times", n)
	}
}

// TestDraftFetchesImages checks that a draft continued, which the message
// view has not loaded the images of, has them fetched and shown if the
// account loads images when a message is shown.
func TestDraftFetchesImages(t *testing.T) {
	images := newImageServer(t)
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	w := newTestWindow(t, s, func(a *account.Account) {
		a.ComposeHTML = true
		a.LoadRemoteImages = true
	})
	wait(t, "the account", func() bool { return len(w.engines) == 1 })
	var c *composer
	mainthread.Wait(func() {
		w.composeDraft(w.engines[w.order[0]], imageMessage(images, "/a.png"), "Drafts", 1)
		for c = range composers {
		}
	})
	t.Cleanup(func() { closeUnchanged(c) })
	wait(t, "the image in the draft", func() bool { return pixelsOf(c.body.web.QWidget, 0xff0000) >= 60*40 })
	if n := images.count("/a.png"); n != 1 {
		t.Errorf("the image was fetched %d times", n)
	}
	// Nothing was typed: the images are not a change to what is written.
	if mainthread.Wait2(func() bool { return c.body.modified }) {
		t.Error("the draft is taken as changed")
	}
}

// TestRoomAroundBody checks that what is written, in HTML or text, is kept
// from the edges of the body, though the message quoted, as newsletters do,
// gives its body no margin or padding and makes it and the page as wide as
// the window, which would apply to the whole of the body written in; and
// that the body is then no wider than the window, which would have it
// scroll from side to side.
func TestRoomAroundBody(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	for _, html := range []bool{true, false} {
		w := newTestWindow(t, s, func(a *account.Account) { a.ComposeHTML = html })
		wait(t, "the account", func() bool { return len(w.engines) == 1 })
		c := openComposer(t, w)
		mainthread.Wait(func() {
			c.setBody(w.engines[w.order[0]].Account(), "Alice wrote:\n> Hello",
				`<style>html, body { margin: 0 !important; padding: 0 !important; width: 100% !important }</style><p>Alice wrote:</p><p>Hello</p>`)
		})
		bodyLoaded(t, c)
		typeInBody(t, c, "MMMM")
		// Nothing is drawn within the room kept at the left and the top.
		edge := func() int {
			return mainthread.Wait2(func() int {
				background := c.body.page.BackgroundColor()
				pixmap := c.body.web.Grab()
				img := pixmap.ToImage()
				runtime.KeepAlive(pixmap)
				n := 0
				for y := range img.Height() {
					for x := range bodyRoom {
						if img.Pixel(x, y)&0xffffff != background.Rgb()&0xffffff {
							n++
						}
					}
				}
				for y := range bodyRoom {
					for x := range img.Width() - 20 {
						if img.Pixel(x, y)&0xffffff != background.Rgb()&0xffffff {
							n++
						}
					}
				}
				return n
			})
		}
		// The text is drawn a moment after it is typed.
		time.Sleep(300 * time.Millisecond)
		if n := edge(); n != 0 {
			t.Errorf("html %v: %d pixels are drawn within %d of the edges", html, n, bodyRoom)
		}
		mainthread.Wait(func() {
			size := c.body.page.ContentsSize()
			defer runtime.KeepAlive(size)
			if wide, room := size.Width(), float64(c.body.web.Width()); wide > room {
				t.Errorf("html %v: the body is %v wide, in %v", html, wide, room)
			}
		})
		closeUnchanged(c)
	}
}
