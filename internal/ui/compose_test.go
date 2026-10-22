package ui

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"
	gosmtp "github.com/emersion/go-smtp"
	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/mainthread"

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
		w.list.SetCurrentIndex(w.list.model.Index(0, 0, qt.NewQModelIndex()))
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
		body := c.body.ToPlainText()
		if !strings.Contains(body, "Alice <alice@example.com> wrote:\n> The body of Bernoulli numbers.") {
			t.Errorf("body = %q", body)
		}
		d, err := c.draft()
		if err != nil {
			t.Fatal(err)
		}
		if d.InReplyTo == "" || d.From.Address != "ada@example.com" {
			t.Errorf("draft = %+v", d)
		}
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

	mainthread.Wait(func() {
		w.composeNew("bob@example.com", "Formatting")
		var c *composer
		for c = range composers {
		}
		c.body.SetPlainText("")
		// Triggering the button, as clicking it or its shortcut does,
		// makes what is typed next bold.
		c.bold.Trigger()
		c.body.InsertPlainText("strong")
		if !strings.Contains(c.body.ToHtml(), "font-weight:700") {
			t.Errorf("html = %s", c.body.ToHtml())
		}
		c.body.Document().SetModifiedWithBool(false)
		c.Close()
	})
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
			w.markRead, w.markUnread, w.viewSource, w.editAccount, w.removeAccount, w.updateCounts, w.syncAll}
		actions = append(actions, w.MenuBar().Actions()...)
		w.composeNew("", "")
		for c := range composers {
			actions = append(actions, c.bar.QWidget.Actions()...)
			actions = append(actions, c.attachList.QWidget.Actions()...)
			actions = append(actions, c.QWidget.Actions()...)
			e := c.edit
			actions = append(actions, e.undo, e.redo, e.cut, e.copy, e.paste, e.pastePlain, e.del, e.selectAll, c.receipt, c.dsn)
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
	mainthread.Wait(func() {
		c.body.SetPlainText("Changed")
		c.saveDraft(nil)
	})
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
	return c
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

	mainthread.Wait(func() {
		defer c.Close()
		c.body.SetPlainText("one")
		listStyle := func() qt.QTextListFormat__Style {
			if l := c.body.TextCursor().CurrentList(); l != nil {
				return l.Format().Style()
			}
			return 0
		}

		c.bullets.Trigger()
		if listStyle() != qt.QTextListFormat__ListDisc || !c.bullets.IsChecked() || c.numbers.IsChecked() {
			t.Errorf("bullets: style %v, buttons %v %v", listStyle(), c.bullets.IsChecked(), c.numbers.IsChecked())
		}
		if !strings.Contains(c.body.ToHtml(), "<ul") {
			t.Errorf("html = %s", c.body.ToHtml())
		}

		// Numbers turn the list into a numbered one.
		c.numbers.Trigger()
		if listStyle() != qt.QTextListFormat__ListDecimal || c.bullets.IsChecked() || !c.numbers.IsChecked() {
			t.Errorf("numbers: style %v, buttons %v %v", listStyle(), c.bullets.IsChecked(), c.numbers.IsChecked())
		}
		if !strings.Contains(c.body.ToHtml(), "<ol") {
			t.Errorf("html = %s", c.body.ToHtml())
		}

		// Pressing it again takes the paragraph out of the list.
		c.numbers.Trigger()
		if listStyle() != 0 || c.numbers.IsChecked() {
			t.Errorf("no list: style %v", listStyle())
		}
		if html := c.body.ToHtml(); strings.Contains(html, "<ol") || strings.Contains(html, "-qt-list-indent") {
			t.Errorf("html = %s", html)
		}
		c.body.Document().SetModifiedWithBool(false)
	})
}

func TestInsertImage(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	w := newTestWindow(t, s, func(a *account.Account) { a.ComposeHTML = true })
	wait(t, "the account", func() bool { return len(w.engines) == 1 })
	c := openComposer(t, w)

	mainthread.Wait(func() {
		defer c.Close()
		// Something that is not an image is refused.
		if err := c.putImage("notes.png", []byte("not a picture")); err == nil || len(c.images) != 0 {
			t.Errorf("err = %v, images = %d", err, len(c.images))
		}

		if err := c.putImage("wide.png", pngImage(t, 900)); err != nil {
			t.Fatal(err)
		}
		d, err := c.draft()
		if err != nil {
			t.Fatal(err)
		}
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
		c.body.SetHtml("<p>No pictures</p>")
		if d, _ := c.draft(); len(d.Images) != 0 {
			t.Errorf("images = %+v", d.Images)
		}
		c.body.Document().SetModifiedWithBool(false)
	})
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
	if images := m.Images(); len(images) != 1 || !bytes.Equal(images[0].Data, pngImage(t, 10)) || len(m.Files()) != 0 {
		t.Fatalf("images = %d, files = %d in\n%s", len(images), len(m.Files()), drafts[0].Raw)
	}

	var again *composer
	mainthread.Wait(func() {
		w.composeDraft(w.engines[w.order[0]], m, "Drafts", drafts[0].UID)
		for again = range composers {
		}
		defer again.Close()
		d, err := again.draft()
		if err != nil {
			t.Fatal(err)
		}
		if len(d.Images) != 1 || d.Images[0].ContentID != m.Images()[0].ContentID || len(d.Attachments) != 0 {
			t.Errorf("images = %+v, attachments = %d", d.Images, len(d.Attachments))
		}
		// The window has the picture to show.
		url := qt.NewQUrl3("cid:" + m.Images()[0].ContentID)
		if again.body.Document().Resource(int(qt.QTextDocument__ImageResource), url).IsNull() {
			t.Error("the image is not shown")
		}
		again.body.Document().SetModifiedWithBool(false)
	})

	// A message forwarded keeps the pictures it shows, not as attachments.
	mainthread.Wait(func() {
		w.view.msg = m
		w.forwardMessage()
		var fwd *composer
		for fwd = range composers {
		}
		defer fwd.Close()
		d, err := fwd.draft()
		if err != nil {
			t.Fatal(err)
		}
		if len(d.Images) != 1 || len(d.Attachments) != 0 {
			t.Errorf("images = %d, attachments = %d", len(d.Images), len(d.Attachments))
		}
		fwd.body.Document().SetModifiedWithBool(false)
	})
}

// focus gives a field or the body the focus, as clicking in it does.
func focus(w *qt.QWidget) {
	e := qt.NewQFocusEvent(qt.QEvent__FocusIn)
	defer e.Delete()
	qt.QCoreApplication_SendEvent(w.QObject, e.QEvent)
}

// TestEditMenu checks that the Edit menu acts on the field or body last in
// focus, and enables only what it can do.
func TestEditMenu(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	w := newTestWindow(t, s, func(a *account.Account) { a.ComposeHTML = true })
	wait(t, "the account", func() bool { return len(w.engines) == 1 })
	c := openComposer(t, w)

	mainthread.Wait(func() {
		defer c.Close()
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

		// The body is acted on once it has the focus.
		focus(c.body.QWidget)
		c.body.SetPlainText("")
		clipboard.SetText("pasted")
		choose(c.edit.paste)
		if got := c.body.ToPlainText(); got != "pasted" {
			t.Errorf("paste: body %q", got)
		}
		choose(c.edit.selectAll)
		choose(c.edit.copy)
		choose(c.edit.del)
		if c.body.ToPlainText() != "" || clipboard.Text() != "pasted" {
			t.Errorf("copy and delete: body %q, clipboard %q", c.body.ToPlainText(), clipboard.Text())
		}

		// Pasting without formatting pastes the text, not the markup.
		mime := qt.NewQMimeData()
		mime.SetHtml("<b>bold</b>")
		mime.SetText("bold")
		clipboard.SetMimeData(mime)
		choose(c.edit.pastePlain)
		if html := c.body.ToHtml(); !strings.Contains(c.body.ToPlainText(), "bold") || strings.Contains(html, "font-weight:700") {
			t.Errorf("paste without formatting: %s", html)
		}
		c.body.Document().SetModifiedWithBool(false)
	})
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
			defer c.Close()
			var titles []string
			for _, a := range c.MenuBar().Actions() {
				titles = append(titles, strings.ReplaceAll(a.Text(), "&", ""))
			}
			want := []string{"Edit", "Options"}
			if html {
				want = []string{"Edit", "Format", "Options"}
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
			c.body.SetPlainText("")
			format.Actions()[0].Trigger()
			if !c.bold.IsChecked() {
				t.Error("bold is not checked")
			}
			c.body.InsertPlainText("strong")
			if !strings.Contains(c.body.ToHtml(), "font-weight:700") {
				t.Errorf("html = %s", c.body.ToHtml())
			}
			c.body.Document().SetModifiedWithBool(false)
		})
	}
}

// TestEscapeCloses checks that Escape closes the window to write a message
// in.
func TestEscapeCloses(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	w := newTestWindow(t, s)
	wait(t, "the account", func() bool { return len(w.engines) == 1 })
	c := openComposer(t, w)
	mainthread.Wait(func() {
		var escape *qt.QAction
		for _, a := range c.QWidget.Actions() {
			if a.Shortcut().ToString() == "Esc" {
				escape = a
			}
		}
		if escape == nil {
			t.Fatal("no action has Escape")
		}
		escape.Trigger()
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
	mainthread.Wait(func() {
		defer c.Close()
		text := c.body.ToPlainText()
		if !strings.Contains(text, "-- \nAda Lovelace") || strings.Contains(text, "Not this") {
			t.Errorf("body = %q", text)
		}
		if !strings.Contains(c.body.ToHtml(), "font-weight:700") {
			t.Errorf("the signature is not bold: %s", c.body.ToHtml())
		}
		d, err := c.draft()
		if err != nil {
			t.Fatal(err)
		}
		if len(d.Images) != 1 || d.Images[0].Filename != "logo.png" || !strings.Contains(d.HTML, "cid:"+d.Images[0].ContentID) {
			t.Errorf("images = %+v in %s", d.Images, d.HTML)
		}
	})
}

// TestHTMLSignatureAsText checks that a message written as text is signed
// with the text of an HTML signature file.
func TestHTMLSignatureAsText(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	path := signatureFile(t)
	w := newTestWindow(t, s, func(a *account.Account) { a.SignatureFile = path })
	wait(t, "the account", func() bool { return len(w.engines) == 1 })
	c := openComposer(t, w)
	mainthread.Wait(func() {
		defer c.Close()
		if text := c.body.ToPlainText(); !strings.HasSuffix(text, "\n-- \nAda Lovelace") || strings.Contains(text, "<") {
			t.Errorf("body = %q", text)
		}
		if d, _ := c.draft(); len(d.Images) != 0 {
			t.Errorf("images = %+v", d.Images)
		}
	})
}

// TestMissingSignatureFile checks that a message is written without the
// signature, and says why, if its file cannot be read.
func TestMissingSignatureFile(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	w := newTestWindow(t, s, func(a *account.Account) { a.SignatureFile = filepath.Join(t.TempDir(), "gone.txt") })
	wait(t, "the account", func() bool { return len(w.engines) == 1 })
	c := openComposer(t, w)
	mainthread.Wait(func() {
		defer c.Close()
		if text := c.body.ToPlainText(); strings.Contains(text, "-- ") {
			t.Errorf("body = %q", text)
		}
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
// as text or HTML, with no quote, above the quote or below it.
func TestRoomAboveSignature(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	w := newTestWindow(t, s)
	wait(t, "the account", func() bool { return len(w.engines) == 1 })
	c := openComposer(t, w)
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
	mainthread.Wait(func() {
		defer c.Close()
		for _, html := range []bool{false, true} {
			for _, quote := range []string{"none", "above", "below"} {
				a := w.engines[w.order[0]].Account()
				a.Signature = "Ada"
				if html {
					a.SignatureFile = sigFile
				}
				a.QuoteBelow = quote == "below"
				c.html = html
				if quote == "none" {
					c.setBody(a, "", "")
				} else {
					c.setBody(a, quoteText, quoteHTML)
				}

				lines := strings.Split(c.body.ToPlainText(), "\n")
				at := c.body.TextCursor().BlockNumber()
				name := fmt.Sprintf("html %v, quote %s", html, quote)
				if at+4 >= len(lines) || c.body.TextCursor().PositionInBlock() != 0 {
					t.Errorf("%s: cursor on line %d of %q", name, at, lines)
					continue
				}
				// The line written on and those below it are empty
				// up to the signature.
				if got := lines[at : at+writingLines]; strings.Join(got, "") != "" {
					t.Errorf("%s: lines %q under the cursor in %q", name, got, lines)
				}
				if lines[at+writingLines] != "-- " || lines[at+writingLines+1] != "Ada" {
					t.Errorf("%s: no signature below the empty lines in %q", name, lines)
				}
				if html {
					doc := c.body.Document()
					for i := at; i < at+writingLines; i++ {
						b := doc.FindBlockByNumber(i)
						f := b.BlockFormat()
						if f.Alignment()&qt.AlignHCenter != 0 || f.Indent() != 0 || f.LeftMargin() != 0 || b.TextList() != nil {
							t.Errorf("%s: line %d to write in is centred, indented or in a list", name, i)
						}
					}
					if f := doc.FindBlockByNumber(at + writingLines + 1).BlockFormat(); f.Alignment()&qt.AlignHCenter == 0 {
						t.Errorf("%s: the signature is no longer centred", name)
					}
				}
				text := strings.Join(lines, "\n")
				switch quote {
				case "above":
					if !strings.HasSuffix(text, "Hello") || at != 0 {
						t.Errorf("%s: %q", name, lines)
					}
				case "below":
					if !strings.HasPrefix(text, "Alice wrote:") || at == 0 || !strings.Contains(lines[at-1], "Hello") {
						t.Errorf("%s: %q, cursor on line %d", name, lines, at)
					}
				}
				if c.body.Document().IsModified() {
					t.Errorf("%s: the body is taken as changed", name)
				}
			}
		}
	})
}

// TestRoomAboveCentredQuote checks that the lines to write in above a quote
// that begins centred are not centred, and the quote stays so.
func TestRoomAboveCentredQuote(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	w := newTestWindow(t, s)
	wait(t, "the account", func() bool { return len(w.engines) == 1 })
	c := openComposer(t, w)
	mainthread.Wait(func() {
		defer c.Close()
		c.html = true
		a := w.engines[w.order[0]].Account()
		c.setBody(a, "Alice wrote:", `<p align="center">Alice wrote:</p><p>Hello</p>`)
		doc := c.body.Document()
		if c.body.TextCursor().BlockNumber() != 0 {
			t.Errorf("cursor on block %d", c.body.TextCursor().BlockNumber())
		}
		for i := range writingLines {
			if b := doc.FindBlockByNumber(i); b.Text() != "" || b.BlockFormat().Alignment()&qt.AlignHCenter != 0 {
				t.Errorf("line %d to write in is %q, centred %v", i, b.Text(), b.BlockFormat().Alignment()&qt.AlignHCenter != 0)
			}
		}
		if b := doc.FindBlockByNumber(writingLines); b.Text() != "Alice wrote:" || b.BlockFormat().Alignment()&qt.AlignHCenter == 0 {
			t.Errorf("the quote begins %q, centred %v", b.Text(), b.BlockFormat().Alignment()&qt.AlignHCenter != 0)
		}
		c.body.Document().SetModifiedWithBool(false)
	})
}
