package ui

import (
	"errors"
	"fmt"

	"github.com/emersion/go-message/mail"

	"github.com/nomad-software/apemail/internal/message"
	"github.com/nomad-software/apemail/internal/send"
)

// mdnSent is the keyword a message is marked with once the return receipt it
// asks for has been sent or refused (RFC 3503), here or by another client,
// so that the user is asked only once.
const mdnSent = "$MDNSent"

// askReceipt asks the user whether to send the return receipt the message
// shown asks for, and sends it if they agree. The user is not asked about
// their own mail, in the sent and drafts folders, nor about a message they
// have been asked about already.
func (w *Window) askReceipt(m *message.Message, uid uint32) {
	e := w.engines[w.account]
	s, ok := w.list.summary(uid)
	if e == nil || !ok || len(m.ReceiptTo) == 0 || s.Has(mdnSent) || w.list.sent {
		return
	}
	folder := w.folder
	subject := m.Subject
	if subject == "" {
		subject = "(no subject)"
	}
	question := fmt.Sprintf("%s asks to be told that you have read “%s”.\n\nSend a return receipt?",
		message.FormatAddresses(m.ReceiptTo), subject)
	if !confirm(w.QWidget, "Return Receipt", question) {
		e.SetFlag(folder, []uint32{uid}, mdnSent, true, w.showError)
		return
	}

	a := e.Account()
	raw, err := m.Receipt(&mail.Address{Name: a.Name, Address: a.Email})
	if err != nil {
		w.showError(fmt.Errorf("the return receipt could not be made: %w", err))
		return
	}
	var to []string
	for _, r := range m.ReceiptTo {
		to = append(to, r.Address)
	}
	e.SendReceipt(raw, to, func(err error) {
		if err != nil && !errors.Is(err, send.ErrUnreachable) {
			// The message is left unmarked, so the user is asked again.
			w.showError(fmt.Errorf("the return receipt could not be sent: %w", err))
			return
		}
		// A receipt waiting in the outbox is as good as sent; the error
		// says it will be.
		w.showError(err)
		e.SetFlag(folder, []uint32{uid}, mdnSent, true, w.showError)
	})
}
