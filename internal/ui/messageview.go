package ui

import (
	"context"
	"encoding/base64"
	"fmt"
	"html"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/emersion/go-message/mail"
	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/mainthread"

	"github.com/nomad-software/apemail/internal/icons"
	"github.com/nomad-software/apemail/internal/images"
	"github.com/nomad-software/apemail/internal/message"
	"github.com/nomad-software/apemail/internal/preview"
)

// messageView shows a message: its headers, its attachments and its body.
//
// HTML is shown by a QTextBrowser, which fetches nothing from the internet
// itself and runs no scripts, so images a message links to are not loaded,
// and cannot tell the sender that it was read, unless the user asks: Load
// Remote Images, or the account's setting to load them always. Then they are
// fetched away from the UI's thread, and given to the browser when it asks
// for an image, and for nothing else. Images the message carries itself
// (cid: and data:) are shown; nothing else is loaded, local files included.
type messageView struct {
	*qt.QWidget

	headers     *qt.QWidget
	headerGrid  *qt.QGridLayout
	attachments *qt.QWidget
	attachBox   *qt.QHBoxLayout
	browser     *qt.QTextBrowser

	// msg is the message that is shown, if one is, and folder and uid
	// where it is.
	msg    *message.Message
	folder string
	uid    uint32

	// loadImages is the button that loads the images the message links to
	// on the internet, if it links to any that are not loaded.
	loadImages *qt.QPushButton
	// images are those loaded, by their address in the message, and
	// imageAddresses the address in the message of each, by the address
	// the browser asks for, which Qt writes its own way.
	images         map[string][]byte
	imageAddresses map[string]string
	// inBrowser is the button which shows the message in the web browser,
	// if it is HTML, and pages the messages being served to it, which stop
	// being served when the view is destroyed, if their time is not up.
	inBrowser *qt.QPushButton
	pages     []*preview.Page
	// stopImages stops the images being loaded, if they are.
	stopImages context.CancelFunc
	// loadAlways reports if the account shown has its images loaded when
	// a message is shown.
	loadAlways func() bool
	// gone is set when the view is destroyed, so that images that arrive
	// afterwards are not shown in it.
	gone bool

	// addSender is the button which puts whoever replies to the message
	// go to in the address book, if a message is shown: beside Reply to,
	// if it has one, or beside From.
	addSender *qt.QToolButton

	// compose is called to write to an address that is clicked, as
	// mailto:.
	compose func(to, subject string)
	// inBook reports if an address is in the address book, and addContacts
	// puts addresses in it.
	inBook      func(email string) bool
	addContacts func(addrs []*mail.Address)
}

func newMessageView() *messageView {
	v := &messageView{QWidget: qt.NewQWidget2()}
	layout := qt.NewQVBoxLayout(v.QWidget)
	layout.SetContentsMargins(0, 0, 0, 0)
	layout.SetSpacing(0)

	v.headers = qt.NewQWidget2()
	v.headers.SetAutoFillBackground(true)
	v.headers.SetBackgroundRole(qt.QPalette__AlternateBase)
	v.headerGrid = qt.NewQGridLayout(v.headers)
	v.headerGrid.SetContentsMargins(8, 6, 8, 6)
	v.headerGrid.SetHorizontalSpacing(8)
	v.headerGrid.SetVerticalSpacing(2)
	v.headerGrid.SetColumnStretch(1, 1)
	layout.AddWidget(v.headers)

	v.attachments = qt.NewQWidget2()
	v.attachBox = qt.NewQHBoxLayout(v.attachments)
	v.attachBox.SetContentsMargins(8, 2, 8, 4)
	layout.AddWidget(v.attachments)

	v.browser = qt.NewQTextBrowser2()
	v.browser.SetOpenLinks(false)
	v.browser.SetSearchPaths(nil)
	var resource returned
	v.browser.OnLoadResource(func(_ func(int, *qt.QUrl) *qt.QVariant, typeVal int, name *qt.QUrl) *qt.QVariant {
		return resource.give(v.resource(typeVal, name.ToString()))
	})
	v.browser.OnAnchorClicked(func(link *qt.QUrl) { v.follow(link.ToString()) })
	layout.AddWidget(v.browser.QWidget)
	v.OnDestroyed(func() {
		v.gone = true
		for _, p := range v.pages {
			p.Close()
		}
	})

	v.clear()
	return v
}

// resource returns what the message carries for a URL in its HTML: a part
// for cid:, the data of data:, an image from the internet that has been
// loaded, and nothing for anything else. It is a value
// a constructor made, which returned deletes once Qt has copied it.
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
	default:
		if data, ok := v.images[v.imageAddresses[name]]; ok {
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
		openURL(link)
	}
}

// openURL opens an address in the desktop's web browser. Tests replace it,
// so that no browser opens.
var openURL = func(link string) {
	u := qt.NewQUrl3(link)
	qt.QDesktopServices_OpenUrl(u)
	u.Delete()
}

// viewInBrowser shows the message in the web browser, as it is meant to
// look, with the images it carries: it is served from this computer for a
// while (preview.Serve) rather than written to a file, which would hold it
// unencrypted.
func (v *messageView) viewInBrowser() {
	if v.msg == nil {
		return
	}
	parts := map[string]preview.Part{}
	for _, a := range v.msg.Attachments {
		if a.ContentID != "" {
			parts[a.ContentID] = preview.Part{ContentType: a.ContentType, Data: a.Data}
		}
	}
	page, err := preview.Serve(v.msg.SourceHTML, parts)
	if err != nil {
		warning(v.QWidget, "View in Browser", "The message could not be shown in the browser: "+err.Error())
		return
	}
	v.pages = append(v.pages, page)
	openURL(page.URL)
}

// clear shows nothing.
func (v *messageView) clear() {
	v.msg, v.folder, v.uid = nil, "", 0
	v.addSender, v.loadImages, v.inBrowser = nil, nil, nil
	v.forgetImages()
	clearLayout(v.headerGrid.QLayout)
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
	v.forgetImages()
	v.msg, v.folder, v.uid = m, folder, uid

	v.showHeaders(m)

	v.setAttachments(m.Files())

	if m.HTML == "" {
		v.browser.SetHtml(textToHTML(m.Text))
		return
	}
	v.showHTML()
	if v.loadImages != nil && v.loadAlways != nil && v.loadAlways() {
		// The button is shown only if some images cannot be loaded, to
		// try them again, rather than for the moment they take.
		v.loadImages.SetVisible(false)
		v.loadRemoteImages()
	}
}

// showHTML shows the HTML of the message, with the images from the internet
// that have been loaded, and small icons in place of the others.
func (v *messageView) showHTML() {
	v.browser.SetHtml(message.SmallRemoteImages(v.msg.HTML, func(src string) bool {
		_, ok := v.images[src]
		return ok
	}))
}

// webImages returns the addresses of the images the message links to on
// the internet.
func webImages(m *message.Message) []string {
	return slices.DeleteFunc(message.RemoteImages(m.HTML), func(src string) bool { return !images.Web(src) })
}

// loadRemoteImages loads the images the message shown links to on the
// internet, away from the UI's thread, and shows them once they are. Those
// that cannot be loaded are left as they were, and the button tries them
// again.
func (v *messageView) loadRemoteImages() {
	m, button := v.msg, v.loadImages
	if m == nil || button == nil {
		return
	}
	v.stopLoading()
	ctx, stop := context.WithCancel(context.Background())
	v.stopImages = stop
	button.SetEnabled(false)
	button.SetText("Loading Images…")
	srcs := webImages(m)
	go func() {
		got := images.Fetch(ctx, srcs)
		mainthread.Start(func() {
			// Another message may be shown by now, or none.
			if v.gone || v.msg != m || ctx.Err() != nil {
				return
			}
			stop()
			v.stopImages = nil
			for src, data := range got {
				v.images[src] = data
				// The browser asks for the image by the address as Qt
				// writes it.
				u := qt.NewQUrl3(src)
				v.imageAddresses[u.ToString()] = src
				u.Delete()
			}
			bar := v.browser.VerticalScrollBar()
			at := bar.Value()
			v.showHTML()
			bar.SetValue(at)
			if missing := len(srcs) - len(v.images); missing > 0 {
				button.SetVisible(true)
				button.SetEnabled(true)
				button.SetText("Load Remote Images")
				button.SetToolTip(fmt.Sprintf("%d of %d images could not be loaded. Try again", missing, len(srcs)))
			} else {
				button.SetVisible(false)
			}
		})
	}()
}

// stopLoading stops the images being loaded, if they are.
func (v *messageView) stopLoading() {
	if v.stopImages != nil {
		v.stopImages()
		v.stopImages = nil
	}
}

// forgetImages stops the images being loaded, and forgets those that were,
// as another message is shown.
func (v *messageView) forgetImages() {
	v.stopLoading()
	v.images, v.imageAddresses = map[string][]byte{}, map[string]string{}
}

// showHeaders shows the subject of the message, and who it is from and to
// and when, with a button beside the sender to put them in the address book.
func (v *messageView) showHeaders(m *message.Message) {
	clearLayout(v.headerGrid.QLayout)
	// The buttons of the message shown before are gone with the headers.
	v.loadImages, v.inBrowser = nil, nil
	subject := m.Subject
	if subject == "" {
		subject = "(no subject)"
	}
	title := qt.NewQLabel3(fmt.Sprintf("<div style=\"font-size: large; font-weight: bold\">%s</div>", html.EscapeString(subject)))
	title.SetTextFormat(qt.RichText)
	title.SetWordWrap(true)
	title.SetTextInteractionFlags(qt.TextSelectableByMouse)
	v.headerGrid.AddWidget3(title.QWidget, 0, 0, 1, 2)

	// A grid does not forget the rows it had, so they are counted here.
	n := 0
	var text *qt.QLabel
	row := func(name, value string) *qt.QHBoxLayout {
		if value == "" {
			return nil
		}
		n++
		label := qt.NewQLabel3(fmt.Sprintf("<span style=\"color: gray\">%s</span>", name))
		label.SetTextFormat(qt.RichText)
		v.headerGrid.AddWidget4(label.QWidget, n, 0, qt.AlignLeft|qt.AlignTop)
		text = qt.NewQLabel3(value)
		text.SetTextFormat(qt.PlainText)
		text.SetWordWrap(true)
		text.SetTextInteractionFlags(qt.TextSelectableByMouse)
		line := qt.NewQHBoxLayout2()
		line.SetSpacing(4)
		line.AddWidget(text.QWidget)
		v.headerGrid.AddLayout(line.QLayout, n, 1)
		return line
	}
	// The button goes beside the addresses it adds.
	addButton := func(line *qt.QHBoxLayout) {
		if line == nil {
			return
		}
		// A label that wraps would be squeezed by the stretch that keeps
		// the button beside it, and a sender is short.
		text.SetWordWrap(false)
		v.addSender = qt.NewQToolButton2()
		v.addSender.SetIcon(icon(icons.AddContact))
		v.addSender.SetAutoRaise(true)
		v.addSender.SetIconSize(qt.NewQSize2(16, 16))
		v.addSender.OnClicked(func() {
			if v.msg != nil && v.addContacts != nil {
				v.addContacts(replier(v.msg))
			}
		})
		line.AddWidget(v.addSender.QWidget)
		line.AddStretch()
		v.showAddButton()
	}
	from := row("From", message.FormatAddresses(m.From))
	if len(m.ReplyTo) > 0 && message.FormatAddresses(m.ReplyTo) != message.FormatAddresses(m.From) {
		addButton(row("Reply to", message.FormatAddresses(m.ReplyTo)))
	} else {
		addButton(from)
	}
	row("To", message.FormatAddresses(m.To))
	row("Cc", message.FormatAddresses(m.Cc))
	if !m.Date.IsZero() {
		row("Date", m.Date.Local().Format("Monday 2 January 2006 15:04"))
	}
	// The buttons for HTML go at the bottom right of the headers, in a
	// column of their own beside them: one that loads the images the
	// message links to, and one that shows it in the web browser.
	buttons := qt.NewQHBoxLayout2()
	buttons.SetSpacing(4)
	if len(webImages(m)) > 0 {
		v.loadImages = qt.NewQPushButton3("Load Remote Images")
		v.loadImages.SetIcon(icon(icons.Image))
		v.loadImages.SetToolTip("Load the images this message shows from the internet, which tells the sender that it was read")
		v.loadImages.OnClicked(v.loadRemoteImages)
		buttons.AddWidget(v.loadImages.QWidget)
	}
	if m.SourceHTML != "" {
		v.inBrowser = qt.NewQPushButton3("View in Browser")
		v.inBrowser.SetIcon(icon(icons.Browser))
		v.inBrowser.SetToolTip("Show this message in the web browser, as it is meant to look, which loads its images from the internet and tells the sender that it was read")
		v.inBrowser.OnClicked(v.viewInBrowser)
		buttons.AddWidget(v.inBrowser.QWidget)
	}
	v.headerGrid.AddLayout4(buttons.QLayout, 0, 2, n+1, 1, qt.AlignRight|qt.AlignBottom)
	v.headers.SetVisible(true)
}

// replier returns whom replies to the message go to: whoever it says, or
// its sender.
func replier(m *message.Message) []*mail.Address {
	if len(m.ReplyTo) > 0 {
		return m.ReplyTo
	}
	return m.From
}

// showAddButton enables the button that adds whoever replies to the message
// go to if they are not in the address book yet.
func (v *messageView) showAddButton() {
	if v.addSender == nil || v.msg == nil {
		return
	}
	missing := false
	addrs := replier(v.msg)
	for _, a := range addrs {
		if a.Address != "" && v.inBook != nil && !v.inBook(a.Address) {
			missing = true
		}
	}
	v.addSender.SetEnabled(missing)
	if missing {
		v.addSender.SetToolTip("Add " + message.FormatAddresses(addrs) + " to the address book")
	} else {
		v.addSender.SetToolTip("In the address book")
	}
}

// clearLayout takes everything out of the layout, and deletes the widgets
// and layouts in it.
func clearLayout(l *qt.QLayout) {
	for l.Count() > 0 {
		item := l.TakeAt(0)
		if w := item.Widget(); w != nil {
			w.DeleteLater()
		}
		if inner := item.Layout(); inner != nil {
			clearLayout(inner)
			inner.DeleteLater()
		} else {
			item.Delete()
		}
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
	clearLayout(v.attachBox.QLayout)
	v.attachments.SetVisible(len(files) > 0)
	if len(files) == 0 {
		return
	}
	label := qt.NewQLabel3(fmt.Sprintf("%d attachment%s:", len(files), plural(len(files))))
	v.attachBox.AddWidget(label.QWidget)
	for _, a := range files {
		b := qt.NewQPushButton3(fmt.Sprintf("%s (%s)", a.Filename, size(len(a.Data))))
		b.SetIcon(icon(icons.Attachment))
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
		warning(v.QWidget, "Save Attachment", err.Error())
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
