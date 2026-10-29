package ui

import (
	"runtime"

	qt "github.com/mappu/miqt/qt6"
)

// editor is a field of the window, or its body, which the Edit menu acts on.
type editor interface {
	Undo()
	Redo()
	Cut()
	Copy()
	Paste()
	SelectAll()
}

// editActions are the items of the Edit menu.
type editActions struct {
	undo, redo, cut, copy, paste, pastePlain, del, selectAll *qt.QAction
}

// makeMenus makes the menus of the window: Edit, which acts on the field or
// body last in focus, Format and Options.
func (c *composer) makeMenus() {
	c.editor = c.body
	watchFocus()
	// The body's own menu has the Edit menu's items, for the body.
	c.body.web.OnContextMenuEvent(func(_ func(*qt.QContextMenuEvent), e *qt.QContextMenuEvent) {
		c.editor = c.body
		c.updateEditMenu()
		menu := qt.NewQMenu(c.body.web.QWidget)
		for _, a := range []*qt.QAction{c.edit.undo, c.edit.redo, nil, c.edit.cut, c.edit.copy, c.edit.paste, c.edit.pastePlain, c.edit.del, nil, c.edit.selectAll} {
			switch {
			case a == nil:
				menu.AddSeparator()
			case a != c.edit.pastePlain || c.html:
				menu.QWidget.AddAction(a)
			}
		}
		pos := e.GlobalPos()
		menu.ExecWithPos(pos)
		runtime.KeepAlive(pos)
		menu.DeleteLater()
	})

	// Close, as Escape, closes the window as its close button does, asking
	// first about a message that has not been sent.
	file := c.MenuBar().AddMenuWithTitle("&File")
	c.closeItem = file.AddActionWithText("&Close")
	c.closeItem.SetShortcut(qt.NewQKeySequence2("Esc"))
	c.closeItem.OnTriggered(func() { c.Close() })

	edit := c.MenuBar().AddMenuWithTitle("&Edit")
	// The fields and the body take the standard keys themselves, so the
	// items only show them: were they the items' shortcuts, they would
	// also act on the body while the list of attachments has the focus.
	item := func(text string, key qt.QKeySequence__StandardKey, f func()) *qt.QAction {
		if key != qt.QKeySequence__UnknownKey {
			text += "\t" + qt.NewQKeySequence6(key).ToString()
		}
		a := edit.AddActionWithText(text)
		a.OnTriggered(f)
		return a
	}
	c.edit.undo = item("&Undo", qt.QKeySequence__Undo, func() { c.editor.Undo() })
	c.edit.redo = item("&Redo", qt.QKeySequence__Redo, func() { c.editor.Redo() })
	edit.AddSeparator()
	c.edit.cut = item("Cu&t", qt.QKeySequence__Cut, func() { c.editor.Cut() })
	c.edit.copy = item("&Copy", qt.QKeySequence__Copy, func() { c.editor.Copy() })
	c.edit.paste = item("&Paste", qt.QKeySequence__Paste, func() { c.editor.Paste() })
	if c.html {
		// The body cannot paste without formatting by itself, so this
		// item has its shortcut.
		c.edit.pastePlain = item("Paste &Without Formatting", qt.QKeySequence__UnknownKey, c.pastePlain)
		c.edit.pastePlain.SetShortcut(qt.NewQKeySequence2("Ctrl+Shift+V"))
	}
	c.edit.del = item("&Delete", qt.QKeySequence__UnknownKey, c.deleteSelection)
	edit.AddSeparator()
	c.edit.selectAll = item("Select &All", qt.QKeySequence__SelectAll, func() { c.editor.SelectAll() })
	edit.OnAboutToShow(c.updateEditMenu)

	// The Format menu has the buttons which format text, in a message
	// written as HTML, which they share their state and shortcuts with.
	if c.html {
		c.formatMenu = c.MenuBar().AddMenuWithTitle("F&ormat")
		for _, a := range c.formatting {
			c.formatMenu.QWidget.AddAction(a)
		}
	}

	options := c.MenuBar().AddMenuWithTitle("&Options")
	c.receipt = options.AddActionWithText("&Return Receipt")
	c.receipt.SetCheckable(true)
	c.receipt.SetToolTip("Ask whoever reads the message to say they have")
	c.dsn = options.AddActionWithText("&Delivery Status Notification")
	c.dsn.SetCheckable(true)
	c.dsn.SetToolTip("Ask the mail servers to say when the message is delivered, or cannot be, where the outgoing server supports it")
}

// watchingFocus is set once watchFocus has started watching.
var watchingFocus bool

// watchFocus has the window that a field or body which takes the focus is
// in take it for the one its Edit menu acts on, as the menu takes the focus
// while it is open. The body is a page, whose own widget, which takes the
// focus, is Qt WebEngine's, so the program watches where the focus goes
// rather than each widget it goes to.
func watchFocus() {
	if watchingFocus {
		return
	}
	watchingFocus = true
	app := qt.UnsafeNewQApplication(qt.QCoreApplication_Instance().UnsafePointer())
	app.OnFocusChanged(func(_, now *qt.QWidget) {
		if now == nil {
			return
		}
		for c := range composers {
			for _, f := range []*qt.QLineEdit{c.to, c.cc, c.bcc, c.subject} {
				if now.UnsafePointer() == f.UnsafePointer() {
					c.editor = f
				}
			}
			if c.body.web.IsAncestorOf(now) {
				c.editor = c.body
			}
		}
	})
}

// updateEditMenu enables the items of the Edit menu that the field or body
// last in focus can do.
func (c *composer) updateEditMenu() {
	var undo, redo, selected bool
	switch e := c.editor.(type) {
	case *qt.QLineEdit:
		undo, redo, selected = e.IsUndoAvailable(), e.IsRedoAvailable(), e.HasSelectedText()
	case *bodyEditor:
		undo, redo, selected = e.canUndo(), e.canRedo(), e.web.HasSelection()
	}
	c.edit.undo.SetEnabled(undo)
	c.edit.redo.SetEnabled(redo)
	c.edit.cut.SetEnabled(selected)
	c.edit.copy.SetEnabled(selected)
	c.edit.del.SetEnabled(selected)
}

// deleteSelection deletes what is selected in the field or body last in
// focus.
func (c *composer) deleteSelection() {
	switch e := c.editor.(type) {
	case *qt.QLineEdit:
		if e.HasSelectedText() {
			e.Del()
		}
	case *bodyEditor:
		if e.web.HasSelection() {
			e.exec("delete", "")
		}
	}
}

// pastePlain pastes the text of the clipboard without its formatting.
func (c *composer) pastePlain() {
	if c.editor != editor(c.body) {
		c.editor.Paste()
		return
	}
	c.body.pastePlain()
}
