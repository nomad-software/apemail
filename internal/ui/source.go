package ui

import (
	"strings"

	qt "github.com/mappu/miqt/qt6"

	"github.com/nomad-software/apemail/internal/settings"
)

// sourceWindow is a window that shows the source of a message: the message
// as the server has it, as plain text, so nothing in it is rendered or
// fetched.
type sourceWindow struct {
	*qt.QPlainTextEdit
}

// sources are the windows of the sources of messages that are open.
var sources = map[*sourceWindow]bool{}

// showMessageSource opens a window with the source of the message that is shown.
func (w *Window) showMessageSource() {
	e := w.engines[w.account]
	m := w.view.msg
	if e == nil || m == nil {
		return
	}
	subject := m.Subject
	if subject == "" {
		subject = "(no subject)"
	}
	e.Message(w.view.folder, w.view.uid, func(raw []byte, err error) {
		if err != nil {
			w.showError(err)
			return
		}
		w.showSource(subject, raw)
	})
}

// showSource opens a window showing the source of a message with the
// subject. It belongs to the main window, so that it closes with it.
func (w *Window) showSource(subject string, raw []byte) *sourceWindow {
	s := &sourceWindow{QPlainTextEdit: qt.NewQPlainTextEdit(w.QWidget)}
	s.SetWindowFlags(qt.Window)
	s.SetAttribute(qt.WA_DeleteOnClose)
	s.SetWindowTitle("Source of " + subject)
	s.SetReadOnly(true)
	s.SetFont(codeFont(w.config))
	// A message need not be UTF-8: what is not is shown as replacement
	// characters rather than not at all.
	s.SetPlainText(strings.ToValidUTF8(strings.ReplaceAll(string(raw), "\r\n", "\n"), "�"))
	s.Resize(800, 600)

	escape := qt.NewQAction2("Close")
	escape.SetShortcut(qt.NewQKeySequence2("Esc"))
	escape.OnTriggered(func() { s.Close() })
	s.QWidget.AddAction(escape)
	s.OnDestroyed(func() { delete(sources, s) })
	sources[s] = true
	s.Show()
	return s
}

// codeFont returns the code font the settings choose, or the system's
// monospaced font, in the family or size they leave empty.
func codeFont(config settings.Settings) *qt.QFont {
	font := qt.QFontDatabase_SystemFont(qt.QFontDatabase__FixedFont)
	if config.CodeFont != "" {
		font.SetFamily(config.CodeFont)
	}
	if config.CodeFontSize > 0 {
		font.SetPointSize(config.CodeFontSize)
	}
	return font
}

// setSourceFont shows the source windows that are open in the code font of
// the settings.
func (w *Window) setSourceFont() {
	for s := range sources {
		s.SetFont(codeFont(w.config))
	}
}
