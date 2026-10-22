package message

import (
	"errors"
	"fmt"
	"strings"
	"time"

	gomessage "github.com/emersion/go-message"
	"github.com/emersion/go-message/mail"
)

// Receipt returns the return receipt the sender of m asks for (RFC 8098),
// from the address of whoever it was sent to, saying it has been shown to
// them: a report of a part of text, for people, and a part of fields, for
// programs. It is sent by hand, as the user is asked first.
func (m *Message) Receipt(from *mail.Address) ([]byte, error) {
	if len(m.ReceiptTo) == 0 {
		return nil, errors.New("the message asks for no return receipt")
	}
	if from == nil || from.Address == "" {
		return nil, errors.New("the receipt has no sender")
	}
	h, err := newHeader(from, "Return Receipt (displayed) - "+m.Subject, time.Time{})
	if err != nil {
		return nil, err
	}
	h.SetAddressList("To", m.ReceiptTo)
	if m.MessageID != "" {
		h.SetMsgIDList("In-Reply-To", []string{m.MessageID})
		h.SetMsgIDList("References", append(append([]string(nil), m.References...), m.MessageID))
	}

	var text strings.Builder
	text.WriteString("The message")
	if !m.Date.IsZero() {
		text.WriteString(" sent on " + m.Date.Local().Format("Mon, 02 Jan 2006 15:04:05 -0700"))
	}
	fmt.Fprintf(&text, " to %s with the subject %q has been displayed.\r\n", from.Address, m.Subject)
	text.WriteString("This is no guarantee that the message has been read or understood.\r\n")

	fields := []string{
		"Reporting-UA: apemail",
		"Final-Recipient: rfc822;" + from.Address,
	}
	if m.MessageID != "" {
		fields = append(fields, "Original-Message-ID: <"+m.MessageID+">")
	}
	fields = append(fields, "Disposition: manual-action/MDN-sent-manually; displayed")
	var report gomessage.Header
	report.SetContentType("message/disposition-notification", nil)

	root := multipart("multipart/report", map[string]string{"report-type": "disposition-notification"}, []part{
		textPart("text/plain", text.String()),
		{header: report, body: []byte(strings.Join(fields, "\r\n") + "\r\n")},
	})
	return writeMessage(h, root)
}
