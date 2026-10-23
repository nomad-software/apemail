package ui

import (
	"strings"
	"unicode/utf16"

	qt "github.com/mappu/miqt/qt6"
)

// addressCompleter offers the contacts of the address book for the address
// being typed in a field of addresses. A field holds several, so it is the
// one the cursor is in that is completed, rather than the whole field as a
// QLineEdit's own completer would.
type addressCompleter struct {
	field     *qt.QLineEdit
	completer *qt.QCompleter
}

// newAddressCompleter completes the addresses typed in the field from the
// model, which has a contact as written in the field in each row. Any part
// of a name or address finds it.
func newAddressCompleter(field *qt.QLineEdit, model *qt.QStringListModel) *addressCompleter {
	a := &addressCompleter{field: field, completer: qt.NewQCompleter5(model.QAbstractItemModel, field.QObject)}
	a.completer.SetWidget(field.QWidget)
	a.completer.SetCompletionMode(qt.QCompleter__PopupCompletion)
	a.completer.SetCaseSensitivity(qt.CaseInsensitive)
	a.completer.SetFilterMode(qt.MatchContains)
	field.OnTextEdited(func(string) { a.suggest() })
	a.completer.OnActivated(a.insert)
	return a
}

// suggest shows the contacts that have what is typed of the address the
// cursor is in.
func (a *addressCompleter) suggest() {
	text := a.field.Text()
	cursor := byteOffset(text, a.field.CursorPosition())
	start, _ := addressAt(text, cursor)
	typed := strings.TrimSpace(text[start:cursor])
	if typed == "" {
		a.completer.Popup().Hide()
		return
	}
	a.completer.SetCompletionPrefix(typed)
	if a.completer.CompletionCount() == 0 {
		a.completer.Popup().Hide()
		return
	}
	a.completer.Complete()
}

// insert puts the contact chosen in place of the address the cursor is in.
func (a *addressCompleter) insert(chosen string) {
	text := a.field.Text()
	text, cursor := completeAddress(text, byteOffset(text, a.field.CursorPosition()), chosen)
	a.field.SetText(text)
	a.field.SetCursorPosition(utf16Len(text[:cursor]))
}

// addressAt returns where the address that the byte offset cursor is in
// starts and ends in a list of them, which commas or semicolons separate
// outside quotes, as ParseAddresses reads it.
func addressAt(text string, cursor int) (start, end int) {
	quoted, escaped := false, false
	for i := 0; i < len(text); i++ {
		switch ch := text[i]; {
		case escaped:
			escaped = false
		case quoted && ch == '\\':
			escaped = true
		case ch == '"':
			quoted = !quoted
		case !quoted && (ch == ',' || ch == ';'):
			if i >= cursor {
				return start, i
			}
			start = i + 1
		}
	}
	return start, len(text)
}

// completeAddress returns the list of addresses with the one the byte offset
// cursor is in replaced by chosen, and the byte offset the cursor goes to:
// after it, or, as the last is followed by a comma ready for the next, at
// the end.
func completeAddress(text string, cursor int, chosen string) (string, int) {
	start, end := addressAt(text, cursor)
	before, after := text[:start], text[end:]
	if before != "" {
		before += " "
	}
	if strings.TrimSpace(after) == "" {
		text = before + chosen + ", "
		return text, len(text)
	}
	return before + chosen + after, len(before) + len(chosen)
}

// byteOffset returns the byte offset in text of a position Qt gives, which
// counts UTF-16 code units.
func byteOffset(text string, pos int) int {
	n := 0
	for i, r := range text {
		if n >= pos {
			return i
		}
		n += utf16.RuneLen(r)
	}
	return len(text)
}

// utf16Len returns the length of text as Qt counts it, in UTF-16 code units.
func utf16Len(text string) int {
	n := 0
	for _, r := range text {
		n += utf16.RuneLen(r)
	}
	return n
}
