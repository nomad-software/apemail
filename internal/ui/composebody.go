package ui

import (
	"strings"

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
		// The space of the separator is kept in the plain text of the
		// message too, which a browser leaves out at the end of a line.
		sig := ""
		if signature.HTML != "" {
			sig = `<p style="white-space: pre-wrap">-- </p>` + signature.HTML
		}
		if below {
			c.body.setHTML(quoteHTML + writingHTML() + sig)
		} else {
			c.body.setHTML(writingHTML() + sig + quoteHTML)
		}
	} else {
		writing := strings.Repeat("\n", writingLines)
		sig := ""
		if signature.Text != "" {
			sig = "-- \n" + signature.Text
		}
		if below {
			c.body.setText(quoteText+"\n"+writing+sig, len([]rune(quoteText))+1)
		} else {
			quote := ""
			if quoteText != "" {
				quote = "\n\n" + quoteText
			}
			c.body.setText(writing+sig+quote, 0)
		}
	}
}

// writingHTML returns writingLines empty lines of HTML, the cursor to go in
// the first. They are plain blocks of their own, so that what is written is
// neither part of a quote or list beside them nor formatted as the
// signature is, which keeps its own formatting.
func writingHTML() string {
	return `<div id="` + cursorMark + `"><br></div>` + strings.Repeat("<div><br></div>", writingLines-1)
}
