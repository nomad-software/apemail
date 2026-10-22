package message_test

import (
	"bytes"
	"io"
	"strings"
	"testing"

	gomessage "github.com/emersion/go-message"
	"github.com/emersion/go-message/mail"

	"github.com/nomad-software/apemail/internal/message"
)

const askingReceipt = "From: Bob <bob@example.com>\r\n" +
	"To: Ada <ada@example.com>\r\n" +
	"Subject: Plans\r\n" +
	"Message-Id: <plans@example.com>\r\n" +
	"References: <older@example.com>\r\n" +
	"Date: Mon, 02 Jan 2006 15:04:05 +0000\r\n" +
	"Disposition-Notification-To: Bob <receipts@example.com>\r\n" +
	"Content-Type: text/plain\r\n" +
	"\r\n" +
	"Read me.\r\n"

func TestReceiptTo(t *testing.T) {
	m, err := message.Parse([]byte(askingReceipt))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.ReceiptTo) != 1 || m.ReceiptTo[0].Address != "receipts@example.com" {
		t.Errorf("receipt to %v", m.ReceiptTo)
	}
	// What cannot be read as an address is not asked for.
	m, _ = message.Parse([]byte(strings.Replace(askingReceipt, "Bob <receipts@example.com>", "not an address", 1)))
	if len(m.ReceiptTo) != 0 {
		t.Errorf("receipt to %v", m.ReceiptTo)
	}
}

// TestReceipt checks that a return receipt is a report of the message being
// displayed (RFC 8098), sent to whom it was asked for by, about that
// message.
func TestReceipt(t *testing.T) {
	m, err := message.Parse([]byte(askingReceipt))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := m.Receipt(&mail.Address{Name: "Ada", Address: "ada@example.com"})
	if err != nil {
		t.Fatal(err)
	}

	r, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	h := r.Header
	if to, _ := h.AddressList("To"); len(to) != 1 || to[0].Address != "receipts@example.com" {
		t.Errorf("to = %v", to)
	}
	if from, _ := h.AddressList("From"); len(from) != 1 || from[0].Address != "ada@example.com" {
		t.Errorf("from = %v", from)
	}
	if subject, _ := h.Subject(); subject != "Return Receipt (displayed) - Plans" {
		t.Errorf("subject = %q", subject)
	}
	if ids, _ := h.MsgIDList("In-Reply-To"); len(ids) != 1 || ids[0] != "plans@example.com" {
		t.Errorf("in reply to %v", ids)
	}
	if ids, _ := h.MsgIDList("References"); len(ids) != 2 || ids[1] != "plans@example.com" {
		t.Errorf("references %v", ids)
	}
	if t2, params, _ := h.ContentType(); t2 != "multipart/report" || params["report-type"] != "disposition-notification" {
		t.Errorf("content type %s %v", t2, params)
	}

	var types []string
	var report string
	e, err := gomessage.Read(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	mr := e.MultipartReader()
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		pt, _, _ := p.Header.ContentType()
		types = append(types, pt)
		body, _ := io.ReadAll(p.Body)
		if pt == "message/disposition-notification" {
			report = string(body)
		} else if !strings.Contains(string(body), "has been displayed") {
			t.Errorf("text = %q", body)
		}
	}
	if strings.Join(types, " ") != "text/plain message/disposition-notification" {
		t.Errorf("parts = %v", types)
	}
	for _, field := range []string{
		"Reporting-UA: apemail",
		"Final-Recipient: rfc822;ada@example.com",
		"Original-Message-ID: <plans@example.com>",
		"Disposition: manual-action/MDN-sent-manually; displayed",
	} {
		if !strings.Contains(report, field+"\r\n") {
			t.Errorf("no %q in\n%s", field, report)
		}
	}
}

func TestReceiptNotAskedFor(t *testing.T) {
	m, err := message.Parse([]byte(strings.Replace(askingReceipt, "Disposition-Notification-To: Bob <receipts@example.com>\r\n", "", 1)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Receipt(&mail.Address{Address: "ada@example.com"}); err == nil {
		t.Error("a receipt was made that was not asked for")
	}
}
