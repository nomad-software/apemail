package ui

import (
	"errors"
	"slices"
	"strings"

	"github.com/nomad-software/qute/qtcore"
	"github.com/nomad-software/qute/qtgui"
	"github.com/nomad-software/qute/qtwidgets"

	"github.com/nomad-software/apemail/internal/addressbook"
)

// bookWindow is the window of the address book, which lists the contacts
// and adds, changes and removes them.
type bookWindow struct {
	*qtwidgets.QWidget
	w *Window

	list                  *qtwidgets.QTreeWidget
	edit, remove, writeTo *qtwidgets.QPushButton
	// contacts are those listed, in the order of the rows.
	contacts []addressbook.Contact
	// sortColumn and sortOrder are how the rows are sorted, which a click
	// on a column's header changes.
	sortColumn int
	sortOrder  qtcore.SortOrder
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
	b := &bookWindow{QWidget: qtwidgets.NewQWidget(w), w: w}
	b.SetWindowFlags(qtcore.Window)
	b.SetAttribute(qtcore.WA_DeleteOnClose)
	b.SetWindowTitle("Address Book")
	layout := qtwidgets.NewQHBoxLayout(b)

	// A list as the messages are, without a grid, sorted by a click on a
	// column's header. The rows are sorted here rather than by the view,
	// so that they stay in the order of contacts.
	b.list = qtwidgets.NewQTreeWidget(nil)
	b.list.SetColumnCount(2)
	b.list.SetHeaderLabels([]string{"Name", "Email"})
	b.list.SetRootIsDecorated(false)
	b.list.SetUniformRowHeights(true)
	b.list.SetAllColumnsShowFocus(true)
	b.list.SetSelectionBehavior(qtwidgets.QAbstractItemViewSelectRows)
	b.list.SetSelectionMode(qtwidgets.QAbstractItemViewSingleSelection)
	b.list.SetEditTriggers(qtwidgets.QAbstractItemViewNoEditTriggers)
	b.list.SetColumnWidth(0, 220)
	header := b.list.Header()
	header.SetSectionsMovable(false)
	header.SetSectionsClickable(true)
	header.SetSortIndicatorShown(true)
	header.SetSortIndicator(b.sortColumn, b.sortOrder)
	header.OnSortIndicatorChanged(func(column int, order qtcore.SortOrder) {
		b.sortColumn, b.sortOrder = column, order
		b.show(w.contacts.book.Contacts)
	})
	b.list.OnItemSelectionChanged(b.updateButtons)
	b.list.OnItemDoubleClicked(func(item *qtwidgets.QTreeWidgetItem, _ int) {
		b.writeAndClose(b.list.IndexOfTopLevelItem(item))
	})
	layout.AddWidget(b.list)

	buttons := qtwidgets.NewQVBoxLayout(nil)
	button := func(text string, clicked func()) *qtwidgets.QPushButton {
		p := qtwidgets.NewQPushButtonWithText(text, nil)
		p.OnClicked(func(bool) { clicked() })
		buttons.AddWidget(p)
		return p
	}
	button("New…", b.newContact)
	b.edit = button("Edit…", b.editSelected)
	b.remove = button("Remove", b.removeSelected)
	b.writeTo = button("Write", b.writeSelected)
	buttons.AddStretch()
	button("Close", func() { b.Close() })
	layout.AddLayout(buttons)

	// Delete removes the contact selected, as it deletes a message in the
	// main window.
	del := qtgui.NewQActionWithText("Remove", b)
	del.SetShortcut(qtgui.NewQKeySequenceWithKey("Del"))
	del.SetShortcutContext(qtcore.WidgetShortcut)
	del.OnTriggered(func(bool) { b.removeSelected() })
	b.list.AddAction(del)
	escape := qtgui.NewQActionWithText("Close", b)
	escape.SetShortcut(qtgui.NewQKeySequenceWithKey("Esc"))
	escape.OnTriggered(func(bool) { b.Close() })
	b.AddAction(escape)

	b.OnDestroyed(func(*qtcore.QObject) {
		if w.bookDialog == b {
			w.bookDialog = nil
		}
	})
	w.bookDialog = b
	b.show(w.contacts.book.Contacts)
	b.Resize(640, 420)
	b.Show()
}

// show lists the contacts in the order chosen, keeping the one selected
// selected.
func (b *bookWindow) show(contacts []addressbook.Contact) {
	selected, _ := b.selected()
	b.contacts = b.sorted(contacts)
	// Clearing the list announces a change of selection, which would ask
	// of rows that are not there yet.
	b.list.BlockSignals(true)
	b.list.Clear()
	var current *qtwidgets.QTreeWidgetItem
	for _, c := range b.contacts {
		item := qtwidgets.NewQTreeWidgetItemWithStrings([]string{c.Name, c.Email})
		b.list.AddTopLevelItem(item)
		if selected.Email != "" && strings.EqualFold(c.Email, selected.Email) {
			current = item
		}
	}
	b.list.BlockSignals(false)
	if current != nil {
		b.list.SetCurrentItem(current)
	} else {
		b.list.ClearSelection()
	}
	b.updateButtons()
}

// sorted returns the contacts in the order of the column chosen. The book
// keeps them in that of names (those without one by their address), and
// addresses are sorted whatever their case, as the book is.
func (b *bookWindow) sorted(contacts []addressbook.Contact) []addressbook.Contact {
	out := slices.Clone(contacts)
	if b.sortColumn == 1 {
		slices.SortStableFunc(out, func(x, y addressbook.Contact) int {
			return strings.Compare(strings.ToLower(x.Email), strings.ToLower(y.Email))
		})
	}
	if b.sortOrder == qtcore.DescendingOrder {
		slices.Reverse(out)
	}
	return out
}

// selected returns the contact that is selected, if one is.
func (b *bookWindow) selected() (addressbook.Contact, bool) {
	item := b.list.CurrentItem()
	if item == nil || !item.IsSelected() {
		return addressbook.Contact{}, false
	}
	row := b.list.IndexOfTopLevelItem(item)
	if row < 0 || row >= len(b.contacts) {
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
	b.dialog = newContactDialog(b, "New Contact", addressbook.Contact{}, func(c addressbook.Contact) error {
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
	b.dialog = newContactDialog(b, "Edit Contact", old, func(c addressbook.Contact) error {
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
	if !ok || !confirm(b, "Remove Contact", "Remove "+contactText(c)+" from the address book?") {
		return
	}
	if err := b.w.changeAddressBook(func(book *addressbook.Book) (bool, error) { return book.Remove(c.Email), nil }); err != nil {
		warning(b, "Remove Contact", err.Error())
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
	*qtwidgets.QDialog
	name, email *qtwidgets.QLineEdit
	ok          func()
}

// newContactDialog asks for a contact, starting with c, and calls accepted
// with it when OK is pressed. The dialog stays open, saying what is wrong,
// if accepted returns an error.
func newContactDialog(parent qtwidgets.QWidgetLike, title string, c addressbook.Contact, accepted func(addressbook.Contact) error) *contactDialog {
	d := &contactDialog{QDialog: qtwidgets.NewQDialog(parent)}
	d.SetWindowTitle(title)
	layout := qtwidgets.NewQVBoxLayout(d)
	form := qtwidgets.NewQFormLayout(nil)
	d.name = qtwidgets.NewQLineEditWithText(c.Name, nil)
	d.email = qtwidgets.NewQLineEditWithText(c.Email, nil)
	d.email.SetPlaceholderText("name@example.com")
	form.AddRowText("Name:", d.name)
	form.AddRowText("Email:", d.email)
	layout.AddLayout(form)

	buttons := qtwidgets.NewQDialogButtonBoxWithButtonsOriented(qtwidgets.QDialogButtonBoxOk|qtwidgets.QDialogButtonBoxCancel, qtcore.Horizontal, nil)
	layout.AddWidget(buttons)
	buttons.OnRejected(d.Reject)
	d.ok = func() {
		if err := accepted(addressbook.Contact{Name: d.name.Text(), Email: d.email.Text()}); err != nil {
			warning(d, title, err.Error())
			return
		}
		d.Accept()
	}
	buttons.OnAccepted(d.ok)
	fitDialog(d, 400)
	openDialog(d)
	return d
}
