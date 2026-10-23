package ui

import (
	"runtime"
	"strings"

	qt "github.com/mappu/miqt/qt6"

	"github.com/nomad-software/apemail/internal/account"
	"github.com/nomad-software/apemail/internal/message"
)

// writingLines is how many lines are left empty for the message to be
// written in, above the signature, the cursor on the first, so that it is
// not written up against the signature.
const writingLines = 3

// setBody puts the signature of the account in the body, below lines left
// empty for the message, and the quote if there is one, above or below
// where the reply is written as the account says. The cursor is put where
// the message is written.
func (c *composer) setBody(a account.Account, quoteText, quoteHTML string) {
	signature, err := message.LoadSignature(a.Signature, a.SignatureFile)
	if err != nil {
		// The message is written without it, rather than not at all.
		c.StatusBar().ShowMessage2(err.Error(), 15000)
	}
	below := a.QuoteBelow && quoteText != ""
	if c.html {
		for _, img := range signature.Images {
			c.addImage(img)
		}
		sig := ""
		if signature.HTML != "" {
			sig = "<p>-- </p>" + signature.HTML
		}
		// The message is written in the first block after the quote, if
		// it is below it, which is counted from the quote alone, and
		// otherwise in the first.
		at := 0
		if below {
			c.body.SetHtml(quoteHTML)
			at = c.body.Document().BlockCount()
			c.body.SetHtml(quoteHTML + sig)
		} else {
			c.body.SetHtml(sig + quoteHTML)
		}
		c.insertWritingLines(at)
	} else {
		writing := strings.Repeat("\n", writingLines)
		sig := ""
		if signature.Text != "" {
			sig = "-- \n" + signature.Text
		}
		if below {
			c.body.SetPlainText(quoteText + "\n" + writing + sig)
			c.setCursor(len([]rune(quoteText)) + 1)
		} else {
			quote := ""
			if quoteText != "" {
				quote = "\n\n" + quoteText
			}
			c.body.SetPlainText(writing + sig + quote)
			c.setCursor(0)
		}
	}
	c.body.Document().SetModifiedWithBool(false)
}

// insertWritingLines puts writingLines empty blocks in an HTML body, as the
// block numbered at, and puts the cursor in the first. They are put in as
// blocks, as HTML of empty paragraphs comes to nothing, and made plain, so
// that what is written is neither part of a quote or list beside it nor
// formatted as the signature is, which keeps its own formatting.
func (c *composer) insertWritingLines(at int) {
	doc := c.body.Document()
	cursor := c.body.TextCursor()
	defer runtime.KeepAlive(cursor)
	block := doc.FindBlockByNumber(at)
	defer runtime.KeepAlive(block)
	switch {
	case at == 0:
		// What is there is moved down, a block at a time.
		cursor.SetPosition(0)
	case block.IsValid():
		// The blocks go after the one before it.
		cursor.SetPosition(block.Position() - 1)
	default:
		cursor.MovePosition(qt.QTextCursor__End)
	}
	for range writingLines {
		cursor.InsertBlock()
	}
	plain := qt.NewQTextBlockFormat()
	defer plain.Delete()
	chars := qt.NewQTextCharFormat()
	defer chars.Delete()
	for i := at; i < at+writingLines; i++ {
		b := doc.FindBlockByNumber(i)
		if list := b.TextList(); list != nil {
			list.Remove(b)
		}
		line := qt.NewQTextCursor4(b)
		line.SetBlockFormat(plain)
		line.SetBlockCharFormat(chars)
		line.Delete()
		runtime.KeepAlive(b)
	}
	first := doc.FindBlockByNumber(at)
	c.setCursor(first.Position())
	runtime.KeepAlive(first)
}

// setCursor puts the cursor at a position of the body.
func (c *composer) setCursor(position int) {
	cursor := c.body.TextCursor()
	cursor.SetPosition(position)
	c.body.SetTextCursor(cursor)
	runtime.KeepAlive(cursor)
}
