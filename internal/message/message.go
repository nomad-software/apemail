// Package message reads messages for showing, and writes those that are
// composed. It has no dependency on the UI.
package message

import (
	"bytes"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	gomessage "github.com/emersion/go-message"
	_ "github.com/emersion/go-message/charset"
	"github.com/emersion/go-message/mail"
)

// maxPartSize limits how much of a part is read, so that a message cannot
// take all the memory there is.
const maxPartSize = 64 << 20

// Attachment is a part of a message that is not its text: a file, or an
// image an HTML message shows.
type Attachment struct {
	Filename    string
	ContentType string
	// ContentID is how an HTML message refers to the part, as cid:ID.
	ContentID string
	// Inline says the part is shown in the message rather than attached.
	Inline bool
	Data   []byte
}

// Message is a message read for showing.
type Message struct {
	Subject   string
	Date      time.Time
	MessageID string
	// References are the IDs of the messages this one follows, oldest
	// first.
	References []string

	From    []*mail.Address
	ReplyTo []*mail.Address
	To      []*mail.Address
	Cc      []*mail.Address
	// Bcc is only in a draft, kept to be finished.
	Bcc []*mail.Address
	// ReceiptTo are the addresses the sender asks for a return receipt to
	// be sent to, when the message is read (Disposition-Notification-To,
	// RFC 8098). There are none if none is asked for.
	ReceiptTo []*mail.Address

	// Text and HTML are the plain text and HTML versions of the body. A
	// message can have either, or both.
	Text string
	HTML string

	Attachments []Attachment
}

// Parse reads a message as the server sent it.
func Parse(raw []byte) (*Message, error) {
	r, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil && !gomessage.IsUnknownCharset(err) && !gomessage.IsUnknownEncoding(err) {
		return nil, fmt.Errorf("reading the message: %w", err)
	}
	defer r.Close()

	m := &Message{}
	h := r.Header
	m.Subject, _ = h.Subject()
	m.Date, _ = h.Date()
	m.MessageID, _ = h.MessageID()
	m.References, _ = h.MsgIDList("References")
	if len(m.References) == 0 {
		m.References, _ = h.MsgIDList("In-Reply-To")
	}
	m.From = addressList(h, "From")
	m.ReplyTo = addressList(h, "Reply-To")
	m.To = addressList(h, "To")
	m.Cc = addressList(h, "Cc")
	m.Bcc = addressList(h, "Bcc")
	for _, a := range addressList(h, "Disposition-Notification-To") {
		// What cannot be read as an address cannot be sent to.
		if a.Address != "" {
			m.ReceiptTo = append(m.ReceiptTo, a)
		}
	}

	for {
		p, err := r.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			if gomessage.IsUnknownCharset(err) || gomessage.IsUnknownEncoding(err) {
				continue
			}
			// What has been read is shown, rather than nothing.
			if m.Text == "" && m.HTML == "" {
				m.Text = "This message could not be read: " + err.Error()
			}
			break
		}
		data, err := io.ReadAll(io.LimitReader(p.Body, maxPartSize))
		if err != nil && !gomessage.IsUnknownCharset(err) {
			continue
		}

		switch ph := p.Header.(type) {
		case *mail.InlineHeader:
			t, params, _ := ph.ContentType()
			if t == "" {
				t = "text/plain"
			}
			switch {
			case t == "text/plain" && m.Text == "":
				m.Text = flowed(unixLines(string(data)), params)
			case t == "text/html" && m.HTML == "":
				m.HTML = string(data)
			case t == "text/plain" || t == "text/html":
				// A second body is kept, as some mailers split one
				// into several parts.
				if t == "text/plain" {
					m.Text += "\n" + unixLines(string(data))
				} else {
					m.HTML += string(data)
				}
			default:
				m.Attachments = append(m.Attachments, Attachment{
					Filename:    partName(ph.Header, params, len(m.Attachments)),
					ContentType: t,
					ContentID:   contentID(ph.Header),
					Inline:      true,
					Data:        data,
				})
			}
		case *mail.AttachmentHeader:
			t, params, _ := ph.ContentType()
			name, _ := ph.Filename()
			if name == "" {
				name = partName(ph.Header, params, len(m.Attachments))
			}
			m.Attachments = append(m.Attachments, Attachment{
				Filename:    name,
				ContentType: t,
				ContentID:   contentID(ph.Header),
				Data:        data,
			})
		}
	}
	m.HTML = flatTables(withoutQtMarker(m.HTML))
	return m, nil
}

// qtMarker is the tag with which Qt marks HTML written by its own editor,
// as ApeMail's: <meta name="qrichtext" content="1">.
var qtMarker = regexp.MustCompile(`(?i)<meta\s[^>]*name\s*=\s*["']?qrichtext\b[^>]*>`)

// withoutQtMarker returns the HTML without Qt's marker. Wherever it is,
// as in a message another client quotes, Qt keeps every space and line
// break of the HTML, so the indenting of the HTML around it shows as empty
// lines, at the top of the message and between its paragraphs. It is for
// Qt alone, so it is left out of what is sent as well as what is shown.
func withoutQtMarker(html string) string {
	return qtMarker.ReplaceAllString(html, "")
}

// addressList returns the addresses of a header, or, if they cannot be
// parsed, the header as one address so that it is still seen.
func addressList(h mail.Header, key string) []*mail.Address {
	list, err := h.AddressList(key)
	if err == nil {
		return list
	}
	if v, _ := h.Text(key); v != "" {
		return []*mail.Address{{Name: v}}
	}
	return nil
}

// contentID returns the Content-ID of a part without its angle brackets.
func contentID(h gomessage.Header) string {
	id := strings.TrimSpace(h.Get("Content-Id"))
	return strings.TrimSuffix(strings.TrimPrefix(id, "<"), ">")
}

// partName returns a name for a part that has none.
func partName(h gomessage.Header, params map[string]string, n int) string {
	if name := params["name"]; name != "" {
		return filepath.Base(name)
	}
	_, dparams, _ := h.ContentDisposition()
	if name := dparams["filename"]; name != "" {
		return filepath.Base(name)
	}
	t, _, _ := h.ContentType()
	ext := ""
	if exts, _ := mime.ExtensionsByType(t); len(exts) > 0 {
		ext = exts[0]
	}
	return fmt.Sprintf("part%d%s", n+1, ext)
}

// unixLines returns text with its lines ended by LF alone. Mail ends them
// with CRLF, and Qt takes the CR and the LF each for the end of a line, which
// would show every line break twice.
func unixLines(text string) string {
	return strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
}

// flowed joins the lines of text that is format=flowed (RFC 3676), which
// end in a space where the sender's mailer wrapped them. Its lines end in LF.
func flowed(text string, params map[string]string) string {
	if !strings.EqualFold(params["format"], "flowed") {
		return text
	}
	delsp := strings.EqualFold(params["delsp"], "yes")
	lines := strings.Split(text, "\n")
	var out strings.Builder
	for i, line := range lines {
		// A space that was added in front of a line is not part of it.
		line = strings.TrimPrefix(line, " ")
		if line != "-- " && strings.HasSuffix(line, " ") && i < len(lines)-1 {
			if delsp {
				line = line[:len(line)-1]
			}
			out.WriteString(line)
			continue
		}
		out.WriteString(line)
		if i < len(lines)-1 {
			out.WriteByte('\n')
		}
	}
	return out.String()
}

// Body returns the body as plain text: the text version, or the HTML
// version with its markup taken out.
func (m *Message) Body() string {
	if strings.TrimSpace(m.Text) != "" || m.HTML == "" {
		return m.Text
	}
	return HTMLToText(m.HTML)
}

// Inline returns the part an HTML message refers to as cid:id.
func (m *Message) Inline(id string) (Attachment, bool) {
	for _, a := range m.Attachments {
		if a.ContentID != "" && strings.EqualFold(a.ContentID, id) {
			return a, true
		}
	}
	return Attachment{}, false
}

// Files returns the attachments that are files rather than parts an HTML
// message shows.
func (m *Message) Files() []Attachment {
	var files []Attachment
	for _, a := range m.Attachments {
		if !m.shows(a) {
			files = append(files, a)
		}
	}
	return files
}

// Images returns the parts an HTML message shows, as cid:ID, rather than
// attaches.
func (m *Message) Images() []Attachment {
	var images []Attachment
	for _, a := range m.Attachments {
		if m.shows(a) {
			images = append(images, a)
		}
	}
	return images
}

// shows says if the HTML of the message shows a part.
func (m *Message) shows(a Attachment) bool {
	return a.Inline && a.ContentID != "" && m.HTML != "" && strings.Contains(m.HTML, "cid:"+a.ContentID)
}

// FormatAddresses returns the addresses as they are written in a header.
func FormatAddresses(list []*mail.Address) string {
	parts := make([]string, 0, len(list))
	for _, a := range list {
		parts = append(parts, FormatAddress(a))
	}
	return strings.Join(parts, ", ")
}

// FormatAddress returns the address as a person would write it, without the
// encoding a header gives a name that is not ASCII. A name with a comma or
// another character that would end it is quoted, so that ParseAddresses
// reads what is typed or completed in a field of addresses back as it was.
func FormatAddress(a *mail.Address) string {
	switch {
	case a.Address == "":
		return a.Name
	case a.Name == "":
		return a.Address
	}
	name := a.Name
	if strings.ContainsAny(name, `,;<>()[]:@"\`) {
		name = `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(name) + `"`
	}
	return name + " <" + a.Address + ">"
}

// HTMLToText returns roughly the text of HTML, for quoting a message that has
// no text version.
func HTMLToText(s string) string {
	var out strings.Builder
	inTag, inSkip := false, ""
	var tag strings.Builder
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
			tag.Reset()
		case r == '>' && inTag:
			inTag = false
			name := strings.ToLower(strings.Fields(tag.String() + " ")[0])
			switch {
			case inSkip != "":
				if name == "/"+inSkip {
					inSkip = ""
				}
			case name == "style" || name == "script" || name == "head":
				inSkip = name
			case name == "br" || name == "br/" || name == "/p" || name == "/div" || name == "/tr" || name == "/li" || name == "/h1" || name == "/h2" || name == "/h3":
				out.WriteByte('\n')
			case name == "p" || name == "li":
				if out.Len() > 0 {
					out.WriteByte('\n')
				}
			}
		case inTag:
			tag.WriteRune(r)
		case inSkip == "":
			out.WriteRune(r)
		}
	}
	text := html.UnescapeString(out.String())
	// Runs of blank lines that markup leaves are closed up.
	lines := strings.Split(text, "\n")
	var kept []string
	blank := 0
	for _, l := range lines {
		l = strings.TrimRight(l, " \t ")
		if strings.TrimSpace(l) == "" {
			blank++
			if blank > 1 {
				continue
			}
		} else {
			blank = 0
		}
		kept = append(kept, l)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

// Draft is a message being written.
type Draft struct {
	From    *mail.Address
	ReplyTo string
	// To, Cc and Bcc are lists of addresses as they are typed, separated
	// by commas.
	To, Cc, Bcc string
	Subject     string
	// Text is the body as plain text. HTML is the body as HTML, if it was
	// written as HTML, of which Text is then the plain version.
	Text string
	HTML string
	// InReplyTo and References are the IDs of the message replied to and
	// those it follows.
	InReplyTo  string
	References []string

	Attachments []Attachment
	// ReturnReceipt asks whoever reads the message to say so, to the
	// sender.
	ReturnReceipt bool
	// Images are the pictures the HTML shows, each referred to by its
	// ContentID as cid:ID. They go with the HTML, not as attachments.
	Images []Attachment

	// Date is when it was written, which is now if it is zero.
	Date time.Time
}

// ParseAddresses reads a list of addresses as they are typed, separated by
// commas or semicolons.
func ParseAddresses(list string) ([]*mail.Address, error) {
	list = strings.TrimSpace(strings.ReplaceAll(list, ";", ","))
	list = strings.Trim(list, ", ")
	if list == "" {
		return nil, nil
	}
	addrs, err := mail.ParseAddressList(list)
	if err != nil {
		return nil, fmt.Errorf("%q is not a list of addresses: %w", list, err)
	}
	return addrs, nil
}

// Recipients returns the addresses the draft is to be sent to: those of To,
// Cc and Bcc, each once.
func (d *Draft) Recipients() ([]string, error) {
	var rcpts []string
	for _, list := range []string{d.To, d.Cc, d.Bcc} {
		addrs, err := ParseAddresses(list)
		if err != nil {
			return nil, err
		}
		for _, a := range addrs {
			if !slices.ContainsFunc(rcpts, func(r string) bool { return strings.EqualFold(r, a.Address) }) {
				rcpts = append(rcpts, a.Address)
			}
		}
	}
	return rcpts, nil
}

// Build writes the draft as a message. bcc says if the Bcc header is
// written, which it is for a draft kept to be finished later and not for
// one that is sent.
func (d *Draft) Build(bcc bool) ([]byte, error) {
	if d.From == nil || d.From.Address == "" {
		return nil, errors.New("the message has no sender")
	}
	h, err := newHeader(d.From, d.Subject, d.Date)
	if err != nil {
		return nil, err
	}
	if d.ReplyTo != "" {
		addrs, err := ParseAddresses(d.ReplyTo)
		if err != nil {
			return nil, err
		}
		h.SetAddressList("Reply-To", addrs)
	}
	lists := []struct {
		key, value string
	}{{"To", d.To}, {"Cc", d.Cc}}
	if bcc {
		lists = append(lists, struct{ key, value string }{"Bcc", d.Bcc})
	}
	for _, l := range lists {
		addrs, err := ParseAddresses(l.value)
		if err != nil {
			return nil, err
		}
		if len(addrs) > 0 {
			h.SetAddressList(l.key, addrs)
		}
	}
	if d.ReturnReceipt {
		h.SetAddressList("Disposition-Notification-To", []*mail.Address{d.From})
	}
	if d.InReplyTo != "" {
		h.SetMsgIDList("In-Reply-To", []string{d.InReplyTo})
	}
	if len(d.References) > 0 {
		h.SetMsgIDList("References", d.References)
	}
	return writeMessage(h, d.body())
}

// newHeader returns the header every message written has: when it was
// written (now if date is zero), who from, its subject and an ID of its own.
func newHeader(from *mail.Address, subject string, date time.Time) (mail.Header, error) {
	var h mail.Header
	if date.IsZero() {
		date = time.Now()
	}
	h.SetDate(date)
	h.SetAddressList("From", []*mail.Address{from})
	h.SetSubject(subject)
	host := "localhost"
	if _, domain, ok := strings.Cut(from.Address, "@"); ok && domain != "" {
		host = domain
	}
	if err := h.GenerateMessageIDWithHostname(host); err != nil {
		return h, err
	}
	h.Set("MIME-Version", "1.0")
	h.Set("User-Agent", "apemail")
	return h, nil
}

// writeMessage writes a message of the header and the parts of root, whose
// header becomes that of the message.
func writeMessage(h mail.Header, root part) ([]byte, error) {
	for f := root.header.Fields(); f.Next(); {
		h.Set(f.Key(), f.Value())
	}
	var buf bytes.Buffer
	w, err := gomessage.CreateWriter(&buf, h.Header)
	if err != nil {
		return nil, err
	}
	if err := root.write(w); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// part is a part of a message to be written: one with a body, or a
// multipart one made of parts.
type part struct {
	header gomessage.Header
	body   []byte
	parts  []part
}

// body returns the parts of the draft: the text, or the text and the HTML as
// alternatives, the HTML with the images it shows, and then the attachments.
func (d *Draft) body() part {
	text := textPart("text/plain", d.Text)
	body := text
	if d.HTML != "" {
		html := textPart("text/html", withoutQtMarker(d.HTML))
		if len(d.Images) > 0 {
			parts := []part{html}
			for _, img := range d.Images {
				parts = append(parts, filePart(img, "inline"))
			}
			html = multipart("multipart/related", map[string]string{"type": "text/html"}, parts)
		}
		body = multipart("multipart/alternative", nil, []part{text, html})
	}
	if len(d.Attachments) == 0 {
		return body
	}
	parts := []part{body}
	for _, a := range d.Attachments {
		parts = append(parts, filePart(a, "attachment"))
	}
	return multipart("multipart/mixed", nil, parts)
}

// textPart returns a part of text of a type, in UTF-8.
func textPart(t, text string) part {
	var h gomessage.Header
	h.SetContentType(t, map[string]string{"charset": "utf-8"})
	h.Set("Content-Transfer-Encoding", "quoted-printable")
	return part{header: h, body: []byte(text)}
}

// filePart returns a part for a file, shown as the disposition says: inline,
// for an image the HTML shows, or as an attachment.
func filePart(a Attachment, disposition string) part {
	var h gomessage.Header
	t := a.ContentType
	if t == "" {
		t = "application/octet-stream"
	}
	h.SetContentType(t, nil)
	h.SetContentDisposition(disposition, map[string]string{"filename": a.Filename})
	if a.ContentID != "" {
		h.Set("Content-Id", "<"+a.ContentID+">")
	}
	h.Set("Content-Transfer-Encoding", "base64")
	return part{header: h, body: a.Data}
}

// multipart returns a multipart part of a type, made of parts.
func multipart(t string, params map[string]string, parts []part) part {
	var h gomessage.Header
	if params == nil {
		params = map[string]string{}
	}
	h.SetContentType(t, params)
	return part{header: h, parts: parts}
}

// write writes the part to w, which was made for its header.
func (p part) write(w *gomessage.Writer) error {
	if p.parts == nil {
		if _, err := w.Write(p.body); err != nil {
			return err
		}
		return w.Close()
	}
	for _, sub := range p.parts {
		pw, err := w.CreatePart(sub.header)
		if err != nil {
			return err
		}
		if err := sub.write(pw); err != nil {
			return err
		}
	}
	return w.Close()
}
