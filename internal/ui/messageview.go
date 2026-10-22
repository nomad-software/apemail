package ui

import (
	"encoding/base64"
	"fmt"
	"html"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	qt "github.com/mappu/miqt/qt6"

	"github.com/nomad-software/apemail/internal/message"
)

// messageView shows a message: its headers, its attachments and its body.
//
// HTML is shown by a QTextBrowser, which fetches nothing from the internet,
// so images a message links to are never loaded and cannot tell the sender
// that it was read. Images the message carries itself (cid: and data:) are
// shown; nothing else is loaded, local files included.
type messageView struct {
	*qt.QWidget

	headers     *qt.QLabel
	attachments *qt.QWidget
	attachBox   *qt.QHBoxLayout
	browser     *qt.QTextBrowser

	// msg is the message that is shown, if one is, and folder and uid
	// where it is.
	msg    *message.Message
	folder string
	uid    uint32

	// compose is called to write to an address that is clicked, as
	// mailto:.
	compose func(to, subject string)
}

func newMessageView() *messageView {
	v := &messageView{QWidget: qt.NewQWidget2()}
	layout := qt.NewQVBoxLayout(v.QWidget)
	layout.SetContentsMargins(0, 0, 0, 0)
	layout.SetSpacing(0)

	v.headers = qt.NewQLabel2()
	v.headers.SetTextFormat(qt.RichText)
	v.headers.SetWordWrap(true)
	v.headers.SetTextInteractionFlags(qt.TextSelectableByMouse)
	v.headers.SetContentsMargins(8, 6, 8, 6)
	v.headers.SetAutoFillBackground(true)
	v.headers.SetBackgroundRole(qt.QPalette__AlternateBase)
	layout.AddWidget(v.headers.QWidget)

	v.attachments = qt.NewQWidget2()
	v.attachBox = qt.NewQHBoxLayout(v.attachments)
	v.attachBox.SetContentsMargins(8, 2, 8, 4)
	layout.AddWidget(v.attachments)

	v.browser = qt.NewQTextBrowser2()
	v.browser.SetOpenLinks(false)
	v.browser.SetSearchPaths(nil)
	v.browser.OnLoadResource(func(_ func(int, *qt.QUrl) *qt.QVariant, typeVal int, name *qt.QUrl) *qt.QVariant {
		return v.resource(typeVal, name.ToString())
	})
	v.browser.OnAnchorClicked(func(link *qt.QUrl) { v.follow(link.ToString()) })
	layout.AddWidget(v.browser.QWidget)

	v.clear()
	return v
}

// resource returns what the message carries for a URL in its HTML: a part
// for cid:, the data of data:, and nothing for anything else.
func (v *messageView) resource(typeVal int, name string) *qt.QVariant {
	if v.msg == nil || typeVal != int(qt.QTextDocument__ImageResource) {
		return qt.NewQVariant()
	}
	switch {
	case strings.HasPrefix(strings.ToLower(name), "cid:"):
		id, _ := url.PathUnescape(name[len("cid:"):])
		if a, ok := v.msg.Inline(id); ok {
			return qt.NewQVariant12(a.Data)
		}
	case strings.HasPrefix(strings.ToLower(name), "data:"):
		if data, ok := dataURL(name); ok {
			return qt.NewQVariant12(data)
		}
	}
	return qt.NewQVariant()
}

// dataURL returns the data of a data: URL that is base64.
func dataURL(u string) ([]byte, bool) {
	meta, payload, ok := strings.Cut(u[len("data:"):], ",")
	if !ok || !strings.HasSuffix(strings.ToLower(meta), ";base64") {
		return nil, false
	}
	data, err := base64.StdEncoding.DecodeString(payload)
	return data, err == nil
}

// follow opens a link that is clicked: mail to an address in a new
// message, and anything else on the internet in the browser.
func (v *messageView) follow(link string) {
	u, err := url.Parse(link)
	if err != nil {
		return
	}
	switch strings.ToLower(u.Scheme) {
	case "mailto":
		if v.compose != nil {
			v.compose(u.Opaque, u.Query().Get("subject"))
		}
	case "http", "https":
		qt.QDesktopServices_OpenUrl(qt.NewQUrl3(link))
	}
}

// clear shows nothing.
func (v *messageView) clear() {
	v.msg, v.folder, v.uid = nil, "", 0
	v.headers.SetText("")
	v.headers.SetVisible(false)
	v.setAttachments(nil)
	v.browser.SetHtml("")
}

// loading says a message is being fetched.
func (v *messageView) loading() {
	v.clear()
	v.browser.SetHtml("<p style=\"color: gray\">Loading…</p>")
}

// failed says a message could not be shown.
func (v *messageView) failed(err error) {
	v.clear()
	v.browser.SetHtml("<p style=\"color: #c03030\">" + html.EscapeString(err.Error()) + "</p>")
}

// show shows the message, which is in the folder with the UID.
func (v *messageView) show(m *message.Message, folder string, uid uint32) {
	v.msg, v.folder, v.uid = m, folder, uid

	var h strings.Builder
	subject := m.Subject
	if subject == "" {
		subject = "(no subject)"
	}
	fmt.Fprintf(&h, "<div style=\"font-size: large; font-weight: bold\">%s</div><table>", html.EscapeString(subject))
	row := func(name, value string) {
		if value != "" {
			fmt.Fprintf(&h, "<tr><td style=\"color: gray; padding-right: 8px\">%s</td><td>%s</td></tr>", name, html.EscapeString(value))
		}
	}
	row("From", message.FormatAddresses(m.From))
	if len(m.ReplyTo) > 0 && message.FormatAddresses(m.ReplyTo) != message.FormatAddresses(m.From) {
		row("Reply to", message.FormatAddresses(m.ReplyTo))
	}
	row("To", message.FormatAddresses(m.To))
	row("Cc", message.FormatAddresses(m.Cc))
	if !m.Date.IsZero() {
		row("Date", m.Date.Local().Format("Monday 2 January 2006 15:04"))
	}
	h.WriteString("</table>")
	v.headers.SetText(h.String())
	v.headers.SetVisible(true)

	v.setAttachments(m.Files())

	if m.HTML != "" {
		v.browser.SetHtml(m.HTML)
	} else {
		v.browser.SetHtml(textToHTML(m.Text))
	}
}

// links finds web and mail addresses in text.
var links = regexp.MustCompile(`(?i)\b(https?://[^\s<>"]+[^\s<>".,;:!?)\]'])|\b([a-z0-9._%+-]+@[a-z0-9.-]+\.[a-z]{2,})\b`)

// textToHTML returns plain text as HTML that shows it as it is, in a fixed
// width font as it was likely written in, with its links made links.
func textToHTML(text string) string {
	var b strings.Builder
	b.WriteString("<pre style=\"white-space: pre-wrap; font-family: monospace\">")
	last := 0
	for _, m := range links.FindAllStringIndex(text, -1) {
		b.WriteString(html.EscapeString(text[last:m[0]]))
		link := text[m[0]:m[1]]
		href := link
		if !strings.Contains(strings.ToLower(link), "://") {
			href = "mailto:" + link
		}
		fmt.Fprintf(&b, "<a href=\"%s\">%s</a>", html.EscapeString(href), html.EscapeString(link))
		last = m[1]
	}
	b.WriteString(html.EscapeString(text[last:]))
	b.WriteString("</pre>")
	return b.String()
}

// setAttachments shows a button for each attachment, which saves it.
func (v *messageView) setAttachments(files []message.Attachment) {
	for v.attachBox.Count() > 0 {
		item := v.attachBox.TakeAt(0)
		if w := item.Widget(); w != nil {
			w.DeleteLater()
		}
	}
	v.attachments.SetVisible(len(files) > 0)
	if len(files) == 0 {
		return
	}
	label := qt.NewQLabel3(fmt.Sprintf("%d attachment%s:", len(files), plural(len(files))))
	v.attachBox.AddWidget(label.QWidget)
	for _, a := range files {
		b := qt.NewQPushButton3(fmt.Sprintf("%s (%s)", a.Filename, size(len(a.Data))))
		b.SetIcon(icon(qt.QStyle__SP_FileIcon, "mail-attachment"))
		b.SetFlat(true)
		b.SetToolTip("Save " + a.Filename)
		b.OnClicked(func() { v.save(a) })
		v.attachBox.AddWidget(b.QWidget)
	}
	v.attachBox.AddStretch()
}

// save asks where to save an attachment and saves it there.
func (v *messageView) save(a message.Attachment) {
	home, _ := os.UserHomeDir()
	name := filepath.Base(a.Filename)
	if name == "." || name == "/" || name == "" {
		name = "attachment"
	}
	path := qt.QFileDialog_GetSaveFileName3(v.QWidget, "Save Attachment", filepath.Join(home, "Downloads", name))
	if path == "" {
		return
	}
	if err := os.WriteFile(path, a.Data, 0o600); err != nil {
		qt.QMessageBox_Warning(v.QWidget, "Save Attachment", err.Error())
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// size returns a number of bytes as a person reads it.
func size(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.0f KB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
}
