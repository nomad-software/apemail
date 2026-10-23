package ui

import (
	"errors"
	"strings"

	qt "github.com/mappu/miqt/qt6"

	"github.com/nomad-software/apemail/internal/addressbook"
)

// bookWindow is the window of the address book, which lists the contacts
// and adds, changes and removes them.
type bookWindow struct {
	*qt.QWidget
	w *Window

	table                 *qt.QTableWidget
	edit, remove, writeTo *qt.QPushButton
	// contacts are those listed, in the order of the rows.
	contacts []addressbook.Contact
	// dialog is the contact dialog opened last, which tests answer.
	dialog *contactDialog
}

// showAddressBook opens the window of the address book, or brings it to the
// front if it is open. It belongs to the main window, so that it closes with
// it.
func (w *Window) showAddressBook() {
	if w.bookDialog != nil {
		w.bookDialog.Raise()
		w.bookDialog.ActivateWindow()
		return
	}
	if w.contacts.book == nil {
		return
	}
	b := &bookWindow{QWidget: qt.NewQWidget(w.QWidget), w: w}
	b.SetWindowFlags(qt.Window)
	b.SetAttribute(qt.WA_DeleteOnClose)
	b.SetWindowTitle("Address Book")
	layout := qt.NewQHBoxLayout(b.QWidget)

	b.table = qt.NewQTableWidget3(0, 2)
	b.table.SetHorizontalHeaderLabels([]string{"Name", "Email"})
	b.table.SetSelectionBehavior(qt.QAbstractItemView__SelectRows)
	b.table.SetSelectionMode(qt.QAbstractItemView__SingleSelection)
	b.table.SetEditTriggers(qt.QAbstractItemView__NoEditTriggers)
	b.table.VerticalHeader().SetVisible(false)
	b.table.HorizontalHeader().SetStretchLastSection(true)
	b.table.SetColumnWidth(0, 220)
	b.table.OnItemSelectionChanged(b.updateButtons)
	b.table.OnCellDoubleClicked(func(row, _ int) { b.writeAndClose(row) })
	layout.AddWidget(b.table.QWidget)

	buttons := qt.NewQVBoxLayout2()
	button := func(text string, clicked func()) *qt.QPushButton {
		p := qt.NewQPushButton3(text)
		p.OnClicked(clicked)
		buttons.AddWidget(p.QWidget)
		return p
	}
	button("New…", b.newContact)
	b.edit = button("Edit…", b.editSelected)
	b.remove = button("Remove", b.removeSelected)
	b.writeTo = button("Write", b.writeSelected)
	buttons.AddStretch()
	button("Close", func() { b.Close() })
	layout.AddLayout(buttons.QLayout)

	// Delete removes the contact selected, as it deletes a message in the
	// main window.
	del := qt.NewQAction2("Remove")
	del.SetShortcut(qt.NewQKeySequence2("Del"))
	del.SetShortcutContext(qt.WidgetShortcut)
	del.OnTriggered(b.removeSelected)
	b.table.QWidget.AddAction(del)
	escape := qt.NewQAction2("Close")
	escape.SetShortcut(qt.NewQKeySequence2("Esc"))
	escape.OnTriggered(func() { b.Close() })
	b.QWidget.AddAction(escape)

	b.OnDestroyed(func() {
		if w.bookDialog == b {
			w.bookDialog = nil
		}
	})
	w.bookDialog = b
	b.show(w.contacts.book.Contacts)
	b.Resize(640, 420)
	b.Show()
}

// show lists the contacts, keeping the one selected selected.
func (b *bookWindow) show(contacts []addressbook.Contact) {
	selected, _ := b.selected()
	b.contacts = contacts
	b.table.SetRowCount(len(contacts))
	row := -1
	for i, c := range contacts {
		b.table.SetItem(i, 0, qt.NewQTableWidgetItem2(c.Name))
		b.table.SetItem(i, 1, qt.NewQTableWidgetItem2(c.Email))
		if selected.Email != "" && strings.EqualFold(c.Email, selected.Email) {
			row = i
		}
	}
	if row >= 0 {
		b.table.SelectRow(row)
	} else {
		b.table.ClearSelection()
	}
	b.updateButtons()
}

// selected returns the contact that is selected, if one is.
func (b *bookWindow) selected() (addressbook.Contact, bool) {
	row := b.table.CurrentRow()
	if row < 0 || row >= len(b.contacts) || !b.table.SelectionModel().IsRowSelected(row) {
		return addressbook.Contact{}, false
	}
	return b.contacts[row], true
}

// updateButtons enables the buttons that act on the contact selected, if
// one is.
func (b *bookWindow) updateButtons() {
	_, ok := b.selected()
	b.edit.SetEnabled(ok)
	b.remove.SetEnabled(ok)
	b.writeTo.SetEnabled(ok)
}

// newContact asks for a contact to add.
func (b *bookWindow) newContact() {
	b.dialog = newContactDialog(b.QWidget, "New Contact", addressbook.Contact{}, func(c addressbook.Contact) error {
		return b.w.changeAddressBook(func(book *addressbook.Book) (bool, error) {
			if book.Has(c.Email) {
				return false, errors.New(c.Email + " is in the address book already")
			}
			return book.Add(c)
		})
	})
}

// editSelected asks for the contact selected to be changed.
func (b *bookWindow) editSelected() {
	old, ok := b.selected()
	if !ok {
		return
	}
	b.dialog = newContactDialog(b.QWidget, "Edit Contact", old, func(c addressbook.Contact) error {
		return b.w.changeAddressBook(func(book *addressbook.Book) (bool, error) {
			if err := book.Change(old.Email, c); err != nil {
				return false, err
			}
			return true, nil
		})
	})
}

// removeSelected takes the contact selected out of the address book, once
// the user agrees, as it cannot be undone.
func (b *bookWindow) removeSelected() {
	c, ok := b.selected()
	if !ok || !confirm(b.QWidget, "Remove Contact", "Remove "+contactText(c)+" from the address book?") {
		return
	}
	if err := b.w.changeAddressBook(func(book *addressbook.Book) (bool, error) { return book.Remove(c.Email), nil }); err != nil {
		warning(b.QWidget, "Remove Contact", err.Error())
	}
}

// writeSelected opens a window to write to the contact selected.
func (b *bookWindow) writeSelected() {
	if c, ok := b.selected(); ok {
		b.w.composeNew(contactText(c), "")
	}
}

// writeAndClose closes the address book and opens a window to write to the
// contact of the row, as double clicking it does.
func (b *bookWindow) writeAndClose(row int) {
	if row < 0 || row >= len(b.contacts) {
		return
	}
	c := b.contacts[row]
	b.Close()
	b.w.composeNew(contactText(c), "")
}

// contactDialog asks for the name and address of a contact.
type contactDialog struct {
	*qt.QDialog
	name, email *qt.QLineEdit
	ok          func()
}

// newContactDialog asks for a contact, starting with c, and calls accepted
// with it when OK is pressed. The dialog stays open, saying what is wrong,
// if accepted returns an error.
func newContactDialog(parent *qt.QWidget, title string, c addressbook.Contact, accepted func(addressbook.Contact) error) *contactDialog {
	d := &contactDialog{QDialog: qt.NewQDialog(parent)}
	d.SetWindowTitle(title)
	layout := qt.NewQVBoxLayout(d.QWidget)
	form := qt.NewQFormLayout2()
	d.name = qt.NewQLineEdit3(c.Name)
	d.email = qt.NewQLineEdit3(c.Email)
	d.email.SetPlaceholderText("name@example.com")
	form.AddRow3("Name:", d.name.QWidget)
	form.AddRow3("Email:", d.email.QWidget)
	layout.AddLayout(form.QLayout)

	buttons := qt.NewQDialogButtonBox5(qt.QDialogButtonBox__Ok|qt.QDialogButtonBox__Cancel, qt.Horizontal)
	layout.AddWidget(buttons.QWidget)
	buttons.OnRejected(d.Reject)
	d.ok = func() {
		if err := accepted(addressbook.Contact{Name: d.name.Text(), Email: d.email.Text()}); err != nil {
			warning(d.QWidget, title, err.Error())
			return
		}
		d.Accept()
	}
	buttons.OnAccepted(d.ok)
	fitDialog(d.QDialog, 400)
	openDialog(d.QDialog)
	return d
}
