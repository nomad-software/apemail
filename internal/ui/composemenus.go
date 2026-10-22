package ui

import qt "github.com/mappu/miqt/qt6"

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
	for _, f := range []*qt.QLineEdit{c.to, c.cc, c.bcc, c.subject} {
		f.OnFocusInEvent(func(super func(*qt.QFocusEvent), e *qt.QFocusEvent) {
			c.editor = f
			super(e)
		})
	}
	c.body.OnFocusInEvent(func(super func(*qt.QFocusEvent), e *qt.QFocusEvent) {
		c.editor = c.body
		super(e)
	})

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

// updateEditMenu enables the items of the Edit menu that the field or body
// last in focus can do.
func (c *composer) updateEditMenu() {
	var undo, redo, selected bool
	switch e := c.editor.(type) {
	case *qt.QLineEdit:
		undo, redo, selected = e.IsUndoAvailable(), e.IsRedoAvailable(), e.HasSelectedText()
	case *qt.QTextEdit:
		doc := e.Document()
		undo, redo, selected = doc.IsUndoAvailable(), doc.IsRedoAvailable(), e.TextCursor().HasSelection()
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
	case *qt.QTextEdit:
		e.TextCursor().RemoveSelectedText()
	}
}

// pastePlain pastes the text of the clipboard without its formatting.
func (c *composer) pastePlain() {
	if c.editor != editor(c.body) {
		c.editor.Paste()
		return
	}
	c.body.InsertPlainText(qt.QGuiApplication_Clipboard().Text())
}
