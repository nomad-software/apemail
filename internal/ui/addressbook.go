package ui

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/emersion/go-message/mail"
	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/mainthread"

	"github.com/nomad-software/apemail/internal/addressbook"
	"github.com/nomad-software/apemail/internal/message"
)

// contacts is the address book as the window has it. It is read and saved
// away from the UI's thread, as it is encrypted.
type contacts struct {
	// book is nil until it has been read, and if it could not be, so
	// that nothing is saved over one that is there.
	book *addressbook.Book
	// model has each contact as it is written in a field of addresses,
	// which the compose windows complete what is typed from.
	model *qt.QStringListModel

	// saving is held while the book is saved, so that one save does not
	// overtake another, and saves is the count of them, so that a save
	// left behind by a later one is not made. pending are the saves not
	// yet done, which are waited for before the files are moved.
	saving  sync.Mutex
	saves   atomic.Uint64
	pending sync.WaitGroup
}

// loadAddressBook reads the address book of the accounts' folder with the
// keys, away from the UI's thread.
func (w *Window) loadAddressBook() {
	w.contacts.book = nil
	w.addressBookChanged()
	path, keys := addressbook.Path(w.accountsDir()), w.keys
	if keys == nil {
		return
	}
	go func() {
		b, err := addressbook.Load(path, keys)
		mainthread.Start(func() {
			// What was read with keys or from a folder since changed is
			// not used.
			if keys != w.keys || path != addressbook.Path(w.accountsDir()) {
				return
			}
			if err != nil {
				w.showError(fmt.Errorf("the address book could not be read: %w", err))
				return
			}
			w.contacts.book = b
			w.addressBookChanged()
		})
	}()
}

// closeAddressBook waits for the address book to be saved and puts it away,
// so that nothing is saved into the folder the accounts were in while they
// are moved or encrypted with other keys.
func (w *Window) closeAddressBook() {
	if w.bookDialog != nil {
		w.bookDialog.Close()
	}
	w.contacts.book = nil
	w.addressBookChanged()
	w.contacts.pending.Wait()
}

// saveAddressBook saves a copy of the address book away from the UI's
// thread.
func (w *Window) saveAddressBook() {
	c := &w.contacts
	b, path, keys := c.book.Clone(), addressbook.Path(w.accountsDir()), w.keys
	n := c.saves.Add(1)
	c.pending.Add(1)
	go func() {
		defer c.pending.Done()
		c.saving.Lock()
		defer c.saving.Unlock()
		// A later save has this one's changes, and will make it.
		if c.saves.Load() != n {
			return
		}
		if err := b.Save(path, keys); err != nil {
			mainthread.Start(func() { w.showError(fmt.Errorf("the address book could not be saved: %w", err)) })
		}
	}()
}

// changeAddressBook makes a change to the address book, which reports if it
// changed anything, and saves and shows it if it did.
func (w *Window) changeAddressBook(change func(b *addressbook.Book) (bool, error)) error {
	if w.contacts.book == nil {
		return errors.New("the address book has not been read")
	}
	changed, err := change(w.contacts.book)
	if changed {
		w.saveAddressBook()
		w.addressBookChanged()
	}
	return err
}

// addContacts puts in the address book those of the addresses it does not
// have yet, and the names of those it has without one.
func (w *Window) addContacts(addrs []*mail.Address) error {
	return w.changeAddressBook(func(b *addressbook.Book) (bool, error) {
		changed := false
		var failed []error
		for _, a := range addrs {
			added, err := b.Add(addressbook.Contact{Name: a.Name, Email: a.Address})
			changed = changed || added
			if err != nil {
				failed = append(failed, err)
			}
		}
		return changed, errors.Join(failed...)
	})
}

// inAddressBook reports if the address is in the address book. Until it has
// been read every address is, so that none is offered to be added.
func (w *Window) inAddressBook(email string) bool {
	return w.contacts.book == nil || w.contacts.book.Has(email)
}

// addressBookChanged shows the address book as it now is: in the window of
// it, the completions of the compose windows, and the button that adds the
// sender of the message shown.
func (w *Window) addressBookChanged() {
	var list []string
	var all []addressbook.Contact
	if b := w.contacts.book; b != nil {
		all = b.Contacts
	}
	for _, c := range all {
		list = append(list, contactText(c))
	}
	w.contacts.model.SetStringList(list)
	w.view.showAddButton()
	w.addressBook.SetEnabled(w.contacts.book != nil)
	if w.bookDialog != nil {
		w.bookDialog.show(all)
	}
}

// contactText returns a contact as it is written in a field of addresses.
func contactText(c addressbook.Contact) string {
	return message.FormatAddress(&mail.Address{Name: c.Name, Address: c.Email})
}
