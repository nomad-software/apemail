package message_test

import (
	"strings"
	"testing"

	"github.com/emersion/go-message/mail"

	"github.com/nomad-software/apemail/internal/message"
)

const multipart = "From: =?utf-8?q?J=C3=B6rg?= <jorg@example.com>\r\n" +
	"To: Me <me@example.com>, Bob <bob@example.com>\r\n" +
	"Cc: Carol <carol@example.com>\r\n" +
	"Subject: =?iso-8859-1?q?Gr=FC=DFe?=\r\n" +
	"Message-Id: <abc@example.com>\r\n" +
	"References: <older@example.com>\r\n" +
	"Date: Mon, 02 Jan 2006 15:04:05 +0000\r\n" +
	"MIME-Version: 1.0\r\n" +
	"Content-Type: multipart/mixed; boundary=outer\r\n" +
	"\r\n" +
	"--outer\r\n" +
	"Content-Type: multipart/alternative; boundary=inner\r\n" +
	"\r\n" +
	"--inner\r\n" +
	"Content-Type: text/plain; charset=iso-8859-1\r\n" +
	"Content-Transfer-Encoding: quoted-printable\r\n" +
	"\r\n" +
	"Sch=F6n\r\n" +
	"--inner\r\n" +
	"Content-Type: text/html; charset=utf-8\r\n" +
	"\r\n" +
	"<p>Sch\xc3\xb6n <img src=\"cid:pic@x\"></p>\r\n" +
	"--inner--\r\n" +
	"--outer\r\n" +
	"Content-Type: image/png\r\n" +
	"Content-Id: <pic@x>\r\n" +
	"Content-Disposition: inline\r\n" +
	"Content-Transfer-Encoding: base64\r\n" +
	"\r\n" +
	"iVBORw0K\r\n" +
	"--outer\r\n" +
	"Content-Type: application/pdf\r\n" +
	"Content-Disposition: attachment; filename=\"report.pdf\"\r\n" +
	"\r\n" +
	"%PDF\r\n" +
	"--outer--\r\n"

func TestParse(t *testing.T) {
	m, err := message.Parse([]byte(multipart))
	if err != nil {
		t.Fatal(err)
	}
	if m.Subject != "Grüße" {
		t.Errorf("subject = %q", m.Subject)
	}
	if got := message.FormatAddresses(m.From); got != "Jörg <jorg@example.com>" {
		t.Errorf("from = %q", got)
	}
	if strings.TrimSpace(m.Text) != "Schön" {
		t.Errorf("text = %q", m.Text)
	}
	if !strings.Contains(m.HTML, "Schön") {
		t.Errorf("html = %q", m.HTML)
	}
	if a, ok := m.Inline("pic@x"); !ok || a.ContentType != "image/png" || len(a.Data) == 0 {
		t.Errorf("inline = %+v, %v", a, ok)
	}
	files := m.Files()
	if len(files) != 1 || files[0].Filename != "report.pdf" {
		t.Errorf("files = %+v", files)
	}
	if m.MessageID != "abc@example.com" || len(m.References) != 1 {
		t.Errorf("ids = %q, %q", m.MessageID, m.References)
	}
}

func TestReply(t *testing.T) {
	m, err := message.Parse([]byte(multipart))
	if err != nil {
		t.Fatal(err)
	}
	to, cc := m.ReplyRecipients(false, "me@example.com")
	if to != "Jörg <jorg@example.com>" || cc != "" {
		t.Errorf("reply = %q, %q", to, cc)
	}
	to, cc = m.ReplyRecipients(true, "ME@example.com")
	if to != "Jörg <jorg@example.com>" || cc != "Bob <bob@example.com>, Carol <carol@example.com>" {
		t.Errorf("reply all = %q, %q", to, cc)
	}
	inReplyTo, refs := m.ReplyReferences()
	if inReplyTo != "abc@example.com" || strings.Join(refs, " ") != "older@example.com abc@example.com" {
		t.Errorf("references = %q, %q", inReplyTo, refs)
	}
	if message.ReplySubject("Re: x") != "Re: x" || message.ReplySubject("x") != "Re: x" || message.ForwardSubject("x") != "Fwd: x" {
		t.Error("subjects")
	}
	if got := message.QuoteText("a\n> b\n"); got != "> a\n>> b" {
		t.Errorf("quote = %q", got)
	}
}

func TestBuildPlain(t *testing.T) {
	d := &message.Draft{
		From:    &mail.Address{Name: "Me", Address: "me@example.com"},
		To:      "Bob <bob@example.com>; carol@example.com",
		Bcc:     "secret@example.com, bob@example.com",
		Subject: "Grüße",
		Text:    "Hello",
	}
	raw, err := d.Build(false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "secret@example.com") {
		t.Error("the Bcc header was written")
	}
	m, err := message.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if m.Subject != "Grüße" || strings.TrimSpace(m.Text) != "Hello" || m.HTML != "" || len(m.To) != 2 {
		t.Errorf("message = %+v", m)
	}
	rcpts, err := d.Recipients()
	if err != nil || strings.Join(rcpts, ",") != "bob@example.com,carol@example.com,secret@example.com" {
		t.Errorf("recipients = %q, %v", rcpts, err)
	}
	if raw, _ := d.Build(true); !strings.Contains(string(raw), "secret@example.com") {
		t.Error("the Bcc header of a draft was not written")
	}
}

func TestBuildHTMLWithAttachment(t *testing.T) {
	d := &message.Draft{
		From:        &mail.Address{Address: "me@example.com"},
		To:          "bob@example.com",
		Subject:     "Report",
		Text:        "See attached",
		HTML:        "<p>See <b>attached</b></p>",
		InReplyTo:   "abc@example.com",
		References:  []string{"abc@example.com"},
		Attachments: []message.Attachment{{Filename: "a.txt", ContentType: "text/plain", Data: []byte("data")}},
	}
	raw, err := d.Build(false)
	if err != nil {
		t.Fatal(err)
	}
	m, err := message.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(m.Text) != "See attached" || !strings.Contains(m.HTML, "<b>attached</b>") {
		t.Errorf("bodies = %q, %q", m.Text, m.HTML)
	}
	if files := m.Files(); len(files) != 1 || files[0].Filename != "a.txt" || string(files[0].Data) != "data" {
		t.Errorf("files = %+v", files)
	}
	if !strings.Contains(string(raw), "In-Reply-To: <abc@example.com>") {
		t.Errorf("raw = %s", raw)
	}
}

// TestBuildHTMLWithImages checks that the images an HTML message shows go
// with its HTML, as multipart/related, and are read back as such rather than
// as attachments.
func TestBuildHTMLWithImages(t *testing.T) {
	d := &message.Draft{
		From:        &mail.Address{Address: "me@example.com"},
		To:          "bob@example.com",
		Subject:     "Holiday",
		Text:        "Look",
		HTML:        `<p>Look <img src="cid:beach@apemail"></p>`,
		Images:      []message.Attachment{{Filename: "beach.png", ContentType: "image/png", ContentID: "beach@apemail", Data: []byte("png")}},
		Attachments: []message.Attachment{{Filename: "plan.txt", ContentType: "text/plain", Data: []byte("plan")}},
	}
	raw, err := d.Build(false)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"multipart/mixed", "multipart/alternative", "multipart/related", "Content-Id: <beach@apemail>", "Content-Disposition: inline"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("no %q in\n%s", want, raw)
		}
	}
	m, err := message.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if img, ok := m.Inline("beach@apemail"); !ok || string(img.Data) != "png" || img.ContentType != "image/png" {
		t.Errorf("image = %+v, %v", img, ok)
	}
	if files := m.Files(); len(files) != 1 || files[0].Filename != "plan.txt" {
		t.Errorf("files = %+v", files)
	}
	if strings.TrimSpace(m.Text) != "Look" {
		t.Errorf("text = %q", m.Text)
	}

	// Without attachments, the related part is the HTML alternative of
	// the message itself.
	d.Attachments = nil
	raw, err = d.Build(false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "multipart/mixed") || !strings.Contains(string(raw), "multipart/related") {
		t.Errorf("raw = %s", raw)
	}
	if m, _ := message.Parse(raw); len(m.Files()) != 0 {
		t.Errorf("files = %+v", m.Files())
	}
}

// TestReturnReceipt checks that a return receipt is asked for, of the
// sender, and is read back from a draft.
func TestReturnReceipt(t *testing.T) {
	d := &message.Draft{From: &mail.Address{Name: "Me", Address: "me@example.com"}, To: "bob@example.com", Text: "Hi"}
	raw, err := d.Build(false)
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := message.Parse(raw); len(m.ReceiptTo) != 0 || strings.Contains(string(raw), "Disposition-Notification-To") {
		t.Errorf("a receipt was asked for unasked:\n%s", raw)
	}

	d.ReturnReceipt = true
	if raw, err = d.Build(false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "Disposition-Notification-To: \"Me\" <me@example.com>") {
		t.Errorf("raw = %s", raw)
	}
	if m, _ := message.Parse(raw); len(m.ReceiptTo) != 1 || m.ReceiptTo[0].Address != "me@example.com" {
		t.Error("the receipt was not read back")
	}
}

func TestBadAddress(t *testing.T) {
	d := &message.Draft{From: &mail.Address{Address: "me@example.com"}, To: "not an address"}
	if _, err := d.Build(false); err == nil {
		t.Error("a bad address was accepted")
	}
}

func TestHTMLToText(t *testing.T) {
	got := message.HTMLToText("<html><head><style>p{}</style></head><body><p>One &amp; two</p><p>Three<br>four</p></body></html>")
	if got != "One & two\n\nThree\nfour" {
		t.Errorf("text = %q", got)
	}
}

func TestFlowed(t *testing.T) {
	raw := "Content-Type: text/plain; format=flowed\r\n\r\nA long \r\nline.\r\nNext\r\n"
	m, err := message.Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(m.Text, "A long line.\nNext") {
		t.Errorf("text = %q", m.Text)
	}
}

// TestFormatAddressQuotes checks that a name which would be read as more
// than one address, or not as a name, is quoted, so that a field it is put
// in is read back as it was.
func TestFormatAddressQuotes(t *testing.T) {
	for _, a := range []*mail.Address{
		{Name: "Hopper, Grace", Address: "grace@example.com"},
		{Name: `Grace "Amazing" Hopper`, Address: "grace@example.com"},
		{Name: `Back\slash <x>`, Address: "grace@example.com"},
		{Name: "Jörg", Address: "jorg@example.com"},
	} {
		text := message.FormatAddress(a)
		got, err := message.ParseAddresses(text + ", other@example.com")
		if err != nil {
			t.Errorf("%q: %v", text, err)
			continue
		}
		if len(got) != 2 || got[0].Name != a.Name || got[0].Address != a.Address {
			t.Errorf("%q was read as %+v", text, got)
		}
	}
	if got := message.FormatAddress(&mail.Address{Name: "Grace Hopper", Address: "grace@example.com"}); got != "Grace Hopper <grace@example.com>" {
		t.Errorf("a plain name was quoted: %q", got)
	}
}

// TestParseLineEnds checks that the text of a message has its lines ended
// by LF alone, as a page takes a CR for another line, whether they end in
// CRLF, as mail does, or a CR alone, and in a second text part too.
func TestParseLineEnds(t *testing.T) {
	raw := "From: a@example.com\r\nSubject: s\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=b\r\n\r\n" +
		"--b\r\nContent-Type: text/plain\r\n\r\n\r\nHello,\r\n\r\nThe body.\r\n" +
		"--b\r\nContent-Type: text/plain\r\n\r\nMore\rlines\r\n" +
		"--b--\r\n"
	m, err := message.Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	// The CRLF before a boundary is the boundary's, not the part's.
	if want := "\nHello,\n\nThe body.\nMore\nlines"; m.Text != want {
		t.Errorf("text = %q, want %q", m.Text, want)
	}
}

// TestHTMLAsItIs checks that the HTML of a message is read, and that of a
// draft sent, exactly as it is: nothing in it is rewritten, hidden or
// linked, whatever it holds.
func TestHTMLAsItIs(t *testing.T) {
	body := "<!DOCTYPE html>\r\n<html><head><meta name=\"qrichtext\" content=\"1\"><style>.p { display: none }</style></head>\r\n" +
		"<body><div class=\"p\" style=\"display:none\">Preview</div>" +
		strings.Repeat(`<table width="100%"><tr><td>`, 8) + "See www.example.com or a@example.com" + strings.Repeat("</td></tr></table>", 8) +
		`<img src="https://example.com/a.png" width="600px" height="auto"><a href=" www.example.com/b ">b</a></body></html>`
	m, err := message.Parse([]byte("From: a@example.com\r\nSubject: s\r\nContent-Type: text/html; charset=utf-8\r\n\r\n" + body))
	if err != nil {
		t.Fatal(err)
	}
	if m.HTML != body {
		t.Errorf("read\n%s\nwant\n%s", m.HTML, body)
	}

	html := `<html><head><meta name="qrichtext" content="1" /><meta charset="utf-8" /></head><body><p>foo</p></body></html>`
	d := &message.Draft{From: &mail.Address{Address: "a@example.com"}, To: "b@example.com", Subject: "s", Text: "foo", HTML: html}
	raw, err := d.Build(false)
	if err != nil {
		t.Fatal(err)
	}
	sent, err := message.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if sent.HTML != html {
		t.Errorf("sent %q, want %q", sent.HTML, html)
	}
}
