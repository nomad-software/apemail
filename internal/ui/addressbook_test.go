package ui

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"
	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/mainthread"

	"github.com/nomad-software/apemail/internal/account"
	"github.com/nomad-software/apemail/internal/addressbook"
	"github.com/nomad-software/apemail/internal/crypt"
	"github.com/nomad-software/apemail/internal/remote/remotetest"
	"github.com/nomad-software/apemail/internal/send/sendtest"
)

// waitForBook waits for the window to have read its address book.
func waitForBook(t *testing.T, w *Window) {
	t.Helper()
	wait(t, "the address book", func() bool { return w.contacts.book != nil })
}

// savedBook waits for the address book to be saved and reads it as it is on
// disk.
func savedBook(t *testing.T, w *Window) *addressbook.Book {
	t.Helper()
	w.contacts.pending.Wait()
	var path string
	var keys *crypt.Keys
	mainthread.Wait(func() { path, keys = addressbook.Path(w.accountsDir()), w.keys })
	b, err := addressbook.Load(path, keys)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// addContact puts a contact in the window's address book.
func addContact(t *testing.T, w *Window, name, email string) {
	t.Helper()
	mainthread.Wait(func() {
		if err := w.changeAddressBook(func(b *addressbook.Book) (bool, error) {
			return b.Add(addressbook.Contact{Name: name, Email: email})
		}); err != nil {
			t.Fatal(err)
		}
	})
}

// emails returns the addresses of the contacts.
func emails(contacts []addressbook.Contact) []string {
	var list []string
	for _, c := range contacts {
		list = append(list, c.Email)
	}
	return list
}

// TestAddSender checks that the button beside the sender of the message
// shown puts them in the address book, once, encrypted, and that it is
// enabled only while they are not in it.
func TestAddSender(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	s.Append(t, "INBOX", "Bernoulli numbers")
	before := s.Messages(t, "INBOX")
	w := newTestWindow(t, s)
	waitForBook(t, w)
	showFirst(t, w)

	mainthread.Wait(func() {
		if w.view.addSender == nil || !w.view.addSender.IsEnabled() {
			t.Fatal("the sender cannot be added")
		}
		w.view.addSender.Click()
		if w.view.addSender.IsEnabled() {
			t.Error("the sender can be added again")
		}
		// Were it clicked again, nothing would change.
		w.view.addContacts(w.view.msg.From)
		if got := w.contacts.model.StringList(); !slices.Equal(got, []string{"Alice <alice@example.com>"}) {
			t.Errorf("completions = %q", got)
		}
	})
	b := savedBook(t, w)
	if len(b.Contacts) != 1 || b.Contacts[0] != (addressbook.Contact{Name: "Alice", Email: "alice@example.com"}) {
		t.Errorf("saved %+v", b.Contacts)
	}
	path := mainthread.Wait2(func() string { return addressbook.Path(w.accountsDir()) })
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("alice")) {
		t.Error("the address book was saved unencrypted")
	}
	// It is in the accounts' folder, so that it moves with them, but not
	// taken for an account.
	if rel, err := filepath.Rel(mainthread.Wait2(w.accountsDir), path); err != nil || strings.HasPrefix(rel, "..") {
		t.Errorf("the address book is at %s", path)
	}
	if accounts, err := account.List(mainthread.Wait2(w.accountsDir), mainthread.Wait2(func() *crypt.Keys { return w.keys })); err != nil || len(accounts) != 1 {
		t.Errorf("accounts = %+v, %v", accounts, err)
	}

	// The button of a sender already in the book is not enabled.
	mainthread.Wait(func() { w.list.ClearSelection() })
	wait(t, "nothing shown", func() bool { return w.view.msg == nil })
	showFirst(t, w)
	mainthread.Wait(func() {
		if w.view.addSender.IsEnabled() {
			t.Error("a sender in the address book can be added")
		}
	})
	if after := s.Messages(t, "INBOX"); len(after) != len(before) {
		t.Errorf("the inbox changed from %+v to %+v", before, after)
	}
}

// TestUnreadableBook checks that an address book which cannot be read is
// neither used nor saved over.
func TestUnreadableBook(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	s.Append(t, "INBOX", "Bernoulli numbers")
	w := newTestWindow(t, s)
	waitForBook(t, w)
	path := mainthread.Wait2(func() string { return addressbook.Path(w.accountsDir()) })
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not encrypted"), 0o600); err != nil {
		t.Fatal(err)
	}
	mainthread.Wait(w.loadAddressBook)
	wait(t, "the error", func() bool { return strings.Contains(w.StatusBar().CurrentMessage(), "address book could not be read") })
	showFirst(t, w)
	mainthread.Wait(func() {
		if w.contacts.book != nil || w.addressBook.IsEnabled() || w.view.addSender.IsEnabled() {
			t.Error("an address book that could not be read is used")
		}
		if err := w.addContacts(w.view.msg.From); err == nil {
			t.Error("a contact was added to an address book that could not be read")
		}
	})
	w.contacts.pending.Wait()
	if data, _ := os.ReadFile(path); string(data) != "not encrypted" {
		t.Errorf("the address book was saved over: %q", data)
	}
}

// TestReplyAddsRecipients checks that sending a reply puts whoever it went
// to in the address book, without those already in it again, and that a
// message which is not a reply puts no one in it.
func TestReplyAddsRecipients(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Sent")
	s.Append(t, "INBOX", "Bernoulli numbers")
	smtp := sendtest.Start(t, false)
	w := newTestWindow(t, s, func(a *account.Account) { a.Outgoing = smtp.Account })
	waitForBook(t, w)
	addContact(t, w, "Grace Hopper", "grace@example.com")
	showFirst(t, w)

	var c *composer
	mainthread.Wait(func() {
		w.replyTo(false)
		for c = range composers {
		}
		c.to.SetText(c.to.Text() + ", GRACE@example.com")
		c.send()
	})
	wait(t, "the reply to be sent", func() bool { return !composers[c] })
	if got := emails(savedBook(t, w).Contacts); !slices.Equal(got, []string{"alice@example.com", "grace@example.com"}) {
		t.Errorf("contacts = %q", got)
	}

	c = openComposer(t, w)
	mainthread.Wait(func() {
		c.subject.SetText("Not a reply")
		c.send()
	})
	wait(t, "the message to be sent", func() bool { return !composers[c] })
	if got := emails(savedBook(t, w).Contacts); !slices.Equal(got, []string{"alice@example.com", "grace@example.com"}) {
		t.Errorf("contacts = %q", got)
	}
	if n := len(smtp.Messages()); n != 2 {
		t.Errorf("%d messages were sent", n)
	}
}

// TestCompleteAddress checks that the address being typed in To, Cc or Bcc
// is completed from the address book, leaving the others as they are.
func TestCompleteAddress(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	w := newTestWindow(t, s)
	waitForBook(t, w)
	wait(t, "the account", func() bool { return len(w.engines) == 1 })
	addContact(t, w, "Hopper, Grace", "grace@example.com")
	addContact(t, w, "Alan Turing", "alan@example.com")
	c := openComposer(t, w)
	defer mainthread.Wait(func() {
		c.body.Document().SetModifiedWithBool(false)
		c.Close()
	})

	mainthread.Wait(func() {
		if len(c.completers) != 3 {
			t.Fatalf("%d fields are completed", len(c.completers))
		}
		to := c.completers[0]
		// Any part of the name or the address finds a contact.
		for _, typed := range []string{"gra", "HOPP", "example.com"} {
			c.to.SetText("bob@example.com, " + typed)
			to.suggest()
			want := 1
			if typed == "example.com" {
				want = 2
			}
			if got := to.completer.CompletionCount(); got != want {
				t.Errorf("%q found %d", typed, got)
			}
		}
		c.to.SetText("bob@example.com, gra")
		to.suggest()
		to.insert(to.completer.CurrentCompletion())
		if got := c.to.Text(); got != `bob@example.com, "Hopper, Grace" <grace@example.com>, ` {
			t.Errorf("to = %q", got)
		}
		if c.to.CursorPosition() != len(c.to.Text()) {
			t.Errorf("cursor at %d", c.to.CursorPosition())
		}
		d, err := c.draft()
		if err != nil {
			t.Fatal(err)
		}
		if rcpts, err := d.Recipients(); err != nil || !slices.Equal(rcpts, []string{"bob@example.com", "grace@example.com"}) {
			t.Errorf("recipients = %q, %v", rcpts, err)
		}

		// Nothing typed, or nothing found, shows nothing.
		c.to.SetText("bob@example.com, ")
		to.suggest()
		if to.completer.Popup().IsVisible() {
			t.Error("completions are shown for nothing")
		}
		c.to.SetText("nobody")
		to.suggest()
		if to.completer.Popup().IsVisible() {
			t.Error("completions are shown for what is in none")
		}
	})
}

func TestAddressAt(t *testing.T) {
	for _, tc := range []struct {
		text, chosen string
		cursor       int
		want         string
		wantCursor   int
	}{
		{"gr", "Grace <g@x>", 2, "Grace <g@x>, ", 13},
		{"a@x, gr", "Grace <g@x>", 7, "a@x, Grace <g@x>, ", 18},
		{"a@x; gr", "Grace <g@x>", 7, "a@x; Grace <g@x>, ", 18},
		// The address in the middle is replaced, and the cursor goes
		// after it.
		{"a@x, gr, b@x", "Grace <g@x>", 7, "a@x, Grace <g@x>, b@x", 16},
		// A comma inside quotes does not end an address.
		{`"Hopper, G" <h@x>, al`, "Alan <a@x>", 21, `"Hopper, G" <h@x>, Alan <a@x>, `, 31},
		{`"Hop\"per, G" <h@x>, al`, "Alan <a@x>", 23, `"Hop\"per, G" <h@x>, Alan <a@x>, `, 33},
	} {
		got, cursor := completeAddress(tc.text, tc.cursor, tc.chosen)
		if got != tc.want || cursor != tc.wantCursor {
			t.Errorf("completing %q at %d = %q at %d, want %q at %d", tc.text, tc.cursor, got, cursor, tc.want, tc.wantCursor)
		}
	}

	// Qt counts UTF-16 code units, which a character outside the basic
	// plane takes two of.
	text := "😀 <s@x>, gr"
	if got := byteOffset(text, utf16Len(text)); got != len(text) {
		t.Errorf("the end is at %d, not %d", got, len(text))
	}
	if got := byteOffset(text, 2); got != len("😀") {
		t.Errorf("after the emoji is %d", got)
	}
	if utf16Len("😀é") != 3 {
		t.Errorf("utf16Len = %d", utf16Len("😀é"))
	}
}

// TestAddressBookWindow checks the address book's place in the button bar
// and the menu, and that its window adds, changes and removes contacts,
// asking before one is removed, and writes to them.
func TestAddressBookWindow(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	w := newTestWindow(t, s)
	wait(t, "the account", func() bool { return len(w.engines) == 1 })
	waitForBook(t, w)

	mainthread.Wait(func() {
		// It is between Archive and Delete, with a separator each
		// side.
		actions := w.buttons.QWidget.Actions()
		i := slices.IndexFunc(actions, func(a *qt.QAction) bool { return a.UnsafePointer() == w.addressBook.UnsafePointer() })
		if i < 2 || i+2 >= len(actions) ||
			actions[i-2].UnsafePointer() != w.archive.UnsafePointer() || !actions[i-1].IsSeparator() ||
			!actions[i+1].IsSeparator() || actions[i+2].UnsafePointer() != w.del.UnsafePointer() {
			t.Error("the address book is not between Archive and Delete")
		}
		if got := w.addressBook.Shortcut().ToString(); got != "Ctrl+Shift+B" {
			t.Errorf("shortcut = %q", got)
		}
		if !w.addressBook.IsEnabled() {
			t.Fatal("the address book cannot be opened")
		}
		w.addressBook.Trigger()
		if w.bookDialog == nil {
			t.Fatal("the address book did not open")
		}
	})
	b := mainthread.Wait2(func() *bookWindow { return w.bookDialog })

	answerContact := func(open func(), name, email string) {
		t.Helper()
		mainthread.Wait(func() {
			open()
			b.dialog.name.SetText(name)
			b.dialog.email.SetText(email)
			b.dialog.ok()
		})
	}
	answerContact(b.newContact, "Grace Hopper", "grace@example.com")
	answerContact(b.newContact, "", "alan@example.com")
	mainthread.Wait(func() {
		if b.list.TopLevelItemCount() != 2 || b.list.TopLevelItem(0).Text(1) != "alan@example.com" || b.list.TopLevelItem(1).Text(0) != "Grace Hopper" {
			t.Errorf("listed %+v", b.contacts)
		}
		if b.edit.IsEnabled() || b.remove.IsEnabled() {
			t.Error("the buttons are enabled with nothing selected")
		}
		b.list.SetCurrentItem(b.list.TopLevelItem(0))
		if !b.edit.IsEnabled() {
			t.Error("the contact selected cannot be edited")
		}
	})
	answerContact(b.editSelected, "Alan Turing", "alan@example.com")
	if got := savedBook(t, w).Contacts; len(got) != 2 || got[0] != (addressbook.Contact{Name: "Alan Turing", Email: "alan@example.com"}) {
		t.Errorf("saved %+v", got)
	}

	// Removing a contact is asked about, and nothing is removed if the
	// user says no.
	asked := answer(t, false)
	mainthread.Wait(func() {
		b.list.SetCurrentItem(b.list.TopLevelItem(1))
		b.removeSelected()
		if *asked != 1 || len(w.contacts.book.Contacts) != 2 {
			t.Errorf("asked %d, contacts %+v", *asked, w.contacts.book.Contacts)
		}
	})
	answer(t, true)
	mainthread.Wait(func() {
		b.removeSelected()
		if got := emails(w.contacts.book.Contacts); !slices.Equal(got, []string{"alan@example.com"}) {
			t.Errorf("contacts = %q", got)
		}
	})
	if got := emails(savedBook(t, w).Contacts); !slices.Equal(got, []string{"alan@example.com"}) {
		t.Errorf("saved %q", got)
	}

	mainthread.Wait(func() {
		b.list.SetCurrentItem(b.list.TopLevelItem(0))
		b.writeSelected()
		var c *composer
		for c = range composers {
		}
		if c == nil {
			t.Fatal("no window was opened")
		}
		if got := c.to.Text(); got != "Alan Turing <alan@example.com>" {
			t.Errorf("to = %q", got)
		}
		c.body.Document().SetModifiedWithBool(false)
		c.Close()

		// It opens once, and closes with the main window.
		w.showAddressBook()
		if w.bookDialog != b || b.ParentWidget().UnsafePointer() != w.QWidget.UnsafePointer() {
			t.Error("a second address book was opened")
		}
	})
}

// TestAddReplyTo checks that the button adds whoever the message asks
// replies to go to, rather than its sender, and sits beside them.
func TestAddReplyTo(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	s.AppendRaw(t, "INBOX", "From: Alice <alice@example.com>\r\n"+
		"Reply-To: The List <list@example.com>\r\n"+
		"To: user@example.com\r\n"+
		"Subject: Minutes\r\n"+
		"Date: Tue, 03 Jan 2006 09:30:00 +0000\r\n"+
		"Content-Type: text/plain; charset=utf-8\r\n"+
		"\r\n"+
		"The minutes.\r\n")
	w := newTestWindow(t, s)
	waitForBook(t, w)
	showFirst(t, w)

	mainthread.Wait(func() {
		if w.view.addSender == nil || !w.view.addSender.IsEnabled() {
			t.Fatal("the address replies go to cannot be added")
		}
		if got := w.view.addSender.ToolTip(); got != "Add The List <list@example.com> to the address book" {
			t.Errorf("tool tip = %q", got)
		}
		// It is on the row of Reply to, the second of the addresses.
		grid := w.view.headerGrid
		for row := range 3 {
			item := grid.ItemAtPosition(row, 1)
			if item == nil || item.Layout() == nil {
				continue
			}
			beside := false
			for i := range item.Layout().Count() {
				if wd := item.Layout().ItemAt(i).Widget(); wd != nil && wd.UnsafePointer() == w.view.addSender.UnsafePointer() {
					beside = true
				}
			}
			if beside != (row == 2) {
				t.Errorf("the button is beside row %d: %v", row, beside)
			}
		}
		w.view.addSender.Click()
		if w.view.addSender.IsEnabled() {
			t.Error("the address can be added again")
		}
	})
	if got := emails(savedBook(t, w).Contacts); !slices.Equal(got, []string{"list@example.com"}) {
		t.Errorf("contacts = %q", got)
	}
}

// TestDoubleClickContact checks that double clicking a contact closes the
// address book and opens a window to write to them.
func TestDoubleClickContact(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	w := newTestWindow(t, s)
	wait(t, "the account", func() bool { return len(w.engines) == 1 })
	waitForBook(t, w)
	addContact(t, w, "Grace Hopper", "grace@example.com")
	addContact(t, w, "Alan Turing", "alan@example.com")

	mainthread.Wait(func() {
		w.showAddressBook()
		b := w.bookDialog
		// The second row, as the view reports a double click on it.
		b.list.ItemDoubleClicked(b.list.TopLevelItem(1), 0)
		var c *composer
		for c = range composers {
		}
		if c == nil {
			t.Fatal("no window was opened")
		}
		defer c.Close()
		if got := c.to.Text(); got != "Grace Hopper <grace@example.com>" {
			t.Errorf("to = %q", got)
		}
		if b.IsVisible() {
			t.Error("the address book is still open")
		}
	})
	wait(t, "the address book to go", func() bool { return w.bookDialog == nil })
}

// TestSortContacts checks that clicking a column's header sorts the contacts
// by it, either way, and that what each row does is the contact it shows.
func TestSortContacts(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	w := newTestWindow(t, s)
	wait(t, "the account", func() bool { return len(w.engines) == 1 })
	waitForBook(t, w)
	addContact(t, w, "Grace Hopper", "hopper@example.com")
	addContact(t, w, "Alan Turing", "turing@example.com")
	addContact(t, w, "Ada Lovelace", "Zed@example.com")

	mainthread.Wait(func() {
		w.showAddressBook()
		b := w.bookDialog
		defer b.Close()
		listed := func() []string {
			var got []string
			for i := range b.list.TopLevelItemCount() {
				got = append(got, b.list.TopLevelItem(i).Text(1))
			}
			return got
		}
		header := b.list.Header()
		if got := listed(); !slices.Equal(got, []string{"Zed@example.com", "turing@example.com", "hopper@example.com"}) {
			t.Errorf("by name: %q", got)
		}
		if !header.IsSortIndicatorShown() || header.SortIndicatorSection() != 0 || header.SortIndicatorOrder() != qt.AscendingOrder {
			t.Error("the header does not show the list sorted by name")
		}

		// The contact selected stays selected as the order changes.
		b.list.SetCurrentItem(b.list.TopLevelItem(1))
		header.SetSortIndicator(1, qt.AscendingOrder)
		if got := listed(); !slices.Equal(got, []string{"hopper@example.com", "turing@example.com", "Zed@example.com"}) {
			t.Errorf("by address: %q", got)
		}
		if c, ok := b.selected(); !ok || c.Email != "turing@example.com" {
			t.Errorf("selected %+v, %v", c, ok)
		}
		header.SetSortIndicator(1, qt.DescendingOrder)
		if got := listed(); !slices.Equal(got, []string{"Zed@example.com", "turing@example.com", "hopper@example.com"}) {
			t.Errorf("by address, descending: %q", got)
		}
		b.list.SetCurrentItem(b.list.TopLevelItem(2))
		if c, _ := b.selected(); c.Email != "hopper@example.com" {
			t.Errorf("the last row is %+v", c)
		}

		// A contact added is put in the order chosen.
		if err := w.changeAddressBook(func(book *addressbook.Book) (bool, error) {
			return book.Add(addressbook.Contact{Name: "Charles Babbage", Email: "babbage@example.com"})
		}); err != nil {
			t.Fatal(err)
		}
		if got := listed(); !slices.Equal(got, []string{"Zed@example.com", "turing@example.com", "hopper@example.com", "babbage@example.com"}) {
			t.Errorf("after adding: %q", got)
		}
	})
}
