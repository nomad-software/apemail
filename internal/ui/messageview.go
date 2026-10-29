package ui

import (
	"context"
	"fmt"
	"html"
	"maps"
	"net/url"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/emersion/go-message/mail"
	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/mainthread"
	"github.com/mappu/miqt/qt6/webengine"

	"github.com/nomad-software/apemail/internal/icons"
	"github.com/nomad-software/apemail/internal/images"
	"github.com/nomad-software/apemail/internal/message"
)

// messageView shows a message: its headers, its attachments and its body.
//
// The body is shown by Qt WebEngine, which runs no scripts and fetches
// nothing from the internet (webview.go), so images a message links to are
// not loaded, and cannot tell the sender that it was read, unless the user
// asks: Load Remote Images, or the account's setting to load them always.
// Then they are fetched away from the UI's thread, and served to the page in
// place of those it asked for. Images the message carries itself (cid: and
// data:) are shown; nothing else is loaded, local files included.
type messageView struct {
	*qt.QWidget

	headers    *qt.QWidget
	headerGrid *qt.QGridLayout
	// attachments holds a button for each of files, the attachments shown,
	// laid out by attachBox, in attachScroll, which scrolls them once they
	// take more than attachRows rows. attachPane holds attachScroll and
	// saveAll, which saves every one.
	attachments  *qt.QWidget
	attachBox    *flowLayout
	attachScroll *qt.QScrollArea
	attachPane   *qt.QWidget
	saveAll      *qt.QPushButton
	files        []message.Attachment

	// web shows page, the body of the message. id is the view's in
	// webViews, and serial that of what it shows, which changes each time
	// it shows something else, so that what was asked for by what it
	// showed before is not served. body is what it shows, and html the page
	// served for it, in the colours of the theme; loaded is the serial of
	// the page last loaded whole. stale is set when it is to be shown
	// again in the colours of the theme, once the view is seen.
	web    *webengine.QWebEngineView
	page   *webengine.QWebEnginePage
	id     int
	serial int
	body   string
	html   string
	loaded int
	stale  bool

	// msg is the message that is shown, if one is, and folder and uid
	// where it is.
	msg    *message.Message
	folder string
	uid    uint32

	// loadImages is the button that loads the images the message asks for
	// on the internet, shown once it has asked for some that are not
	// loaded.
	loadImages *qt.QPushButton
	// wanted are the images on the web the page has asked for, in the order
	// it did, tried those that have been fetched, and images those that
	// were, by their address. allowImages is set once the user has had them
	// loaded, or if the account says to, after which those the page asks for
	// are fetched as it does.
	wanted      []string
	tried       map[string]bool
	images      map[string][]byte
	allowImages bool
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

	layout.AddWidget(v.makeAttachments())

	v.newWebView()
	layout.AddWidget(v.web.QWidget)
	// A change of theme is shown in the message too, once it is seen.
	v.OnChangeEvent(func(super func(*qt.QEvent), e *qt.QEvent) {
		super(e)
		if e.Type() == qt.QEvent__PaletteChange {
			v.restyle()
		}
	})
	v.OnShowEvent(func(super func(*qt.QShowEvent), e *qt.QShowEvent) {
		super(e)
		if v.stale {
			v.stale = false
			v.page.TriggerAction(webengine.QWebEnginePage__Reload, false)
		}
	})
	v.OnDestroyed(func() {
		v.gone = true
		v.stopLoading()
		delete(webViews, v.id)
	})

	v.clear()
	return v
}

// render shows the HTML as the body.
func (v *messageView) render(body string) {
	v.serial++
	v.body, v.stale = body, false
	v.restyle()
	u := qt.NewQUrl3(v.pageURL("message"))
	v.page.Load(u)
	u.Delete()
}

// restyle makes the page of what is shown in the colours of the theme,
// and shows it again if it was shown: now if the view is seen, or when it
// is, as every page reloading at once is slow.
func (v *messageView) restyle() {
	v.html = webPage(v.body)
	palette := qt.QGuiApplication_Palette()
	base := palette.Color(qt.QPalette__Active, qt.QPalette__Base)
	v.page.SetBackgroundColor(base)
	runtime.KeepAlive(palette)
	switch {
	case v.loaded != v.serial:
		// It is being loaded, as it is now.
	case v.IsVisible():
		v.page.TriggerAction(webengine.QWebEnginePage__Reload, false)
	default:
		v.stale = true
	}
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
			to, _ := url.PathUnescape(u.Opaque)
			v.compose(to, u.Query().Get("subject"))
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

// clear shows nothing.
func (v *messageView) clear() {
	v.showOnly("")
}

// loading says a message is being fetched.
func (v *messageView) loading() {
	v.showOnly("<p style=\"color: gray\">Loading…</p>")
}

// failed says a message could not be shown.
func (v *messageView) failed(err error) {
	v.showOnly("<p style=\"color: #c03030\">" + html.EscapeString(err.Error()) + "</p>")
}

// showOnly shows the HTML in place of a message, with no headers or
// attachments.
func (v *messageView) showOnly(body string) {
	v.msg, v.folder, v.uid = nil, "", 0
	v.addSender, v.loadImages = nil, nil
	v.forgetImages()
	clearLayout(v.headerGrid.QLayout)
	v.headers.SetVisible(false)
	v.setAttachments(nil)
	v.render(body)
}

// show shows the message, which is in the folder with the UID.
func (v *messageView) show(m *message.Message, folder string, uid uint32) {
	v.forgetImages()
	v.msg, v.folder, v.uid = m, folder, uid
	v.allowImages = m.HTML != "" && v.loadAlways != nil && v.loadAlways()

	v.showHeaders(m)

	v.setAttachments(m.Files())

	if m.HTML == "" {
		v.render(textToHTML(m.Text))
		return
	}
	v.render(m.HTML)
}

// want remembers an image on the web the page asks for, which is not
// loaded.
func (v *messageView) want(address string) {
	if !slices.Contains(v.wanted, address) {
		v.wanted = append(v.wanted, address)
	}
	v.imagesWanted()
}

// missing returns the images on the web the page has asked for that are
// not loaded, and untried those of them that have not been fetched, no more
// than are fetched for a message.
func (v *messageView) missing() (missing, untried []string) {
	for _, src := range v.wanted {
		if _, ok := v.images[src]; ok {
			continue
		}
		missing = append(missing, src)
		if !v.tried[src] && len(v.tried)+len(untried) < images.MaxImages {
			untried = append(untried, src)
		}
	}
	return missing, untried
}

// imagesWanted fetches the images the page has asked for and not been
// given, if the user has had them loaded, once it has loaded and if they
// are not being fetched, and otherwise shows the button that loads them.
func (v *messageView) imagesWanted() {
	if _, untried := v.missing(); v.allowImages && v.loaded == v.serial && v.stopImages == nil && len(untried) > 0 {
		v.fetchImages(untried)
		return
	}
	v.showImagesButton()
}

// showImagesButton shows the button that loads images if the page asks for
// some that are not loaded, and is not having them loaded already: enabled
// unless they are being, and saying how many could not be loaded, to try
// them again, once some were tried.
func (v *messageView) showImagesButton() {
	b := v.loadImages
	if b == nil {
		return
	}
	missing, untried := v.missing()
	switch {
	case v.stopImages != nil:
		// It stays hidden while the images load if they were loaded
		// without asking, and shown if it was pressed.
		b.SetEnabled(false)
		b.SetText("Loading Images…")
	case len(missing) == 0, v.allowImages && len(untried) > 0:
		b.SetVisible(false)
	default:
		b.SetVisible(true)
		b.SetEnabled(true)
		b.SetText("Load Remote Images")
		if failed := len(missing) - len(untried); failed > 0 {
			b.SetToolTip(fmt.Sprintf("%d of %d images could not be loaded. Try again", failed, len(v.wanted)))
		} else {
			b.SetToolTip("Load the images this message shows from the internet, which tells the sender that it was read")
		}
	}
}

// loadRemoteImages loads the images the page has asked for that are not
// loaded, those that could not be before too, and those it asks for later.
func (v *messageView) loadRemoteImages() {
	v.allowImages = true
	if missing, _ := v.missing(); len(missing) > 0 {
		v.stopLoading()
		v.fetchImages(missing)
	}
}

// fetchImages fetches the images away from the UI's thread, and shows the
// message again with them once they are, for the page to be given those
// that could be fetched.
func (v *messageView) fetchImages(srcs []string) {
	m, serial := v.msg, v.serial
	ctx, stop := context.WithCancel(context.Background())
	v.stopImages = stop
	for _, src := range srcs {
		v.tried[src] = true
	}
	v.showImagesButton()
	go func() {
		got := images.Fetch(ctx, srcs)
		mainthread.Start(func() {
			// Another message may be shown by now, or none.
			if v.gone || v.msg != m || v.serial != serial || ctx.Err() != nil {
				return
			}
			stop()
			v.stopImages = nil
			maps.Copy(v.images, got)
			if len(got) > 0 {
				v.page.TriggerAction(webengine.QWebEnginePage__Reload, false)
			}
			v.showImagesButton()
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
// and that the page asked for, as another message is shown.
func (v *messageView) forgetImages() {
	v.stopLoading()
	v.wanted, v.tried, v.images, v.allowImages = nil, map[string]bool{}, map[string][]byte{}, false
}

// showHeaders shows the subject of the message, and who it is from and to
// and when, with a button beside the sender to put them in the address book.
func (v *messageView) showHeaders(m *message.Message) {
	clearLayout(v.headerGrid.QLayout)
	// The button of the message shown before is gone with the headers.
	v.loadImages = nil
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
	// The files themselves are below the headers.
	if files := len(m.Files()); files > 0 {
		row("Attachments", strconv.Itoa(files))
	}
	// The button that loads the images an HTML message links to goes at
	// the bottom right of the headers, in a column of its own beside them,
	// shown once the message asks for some.
	if m.HTML != "" {
		v.loadImages = qt.NewQPushButton3("Load Remote Images")
		v.loadImages.SetIcon(icon(icons.Image))
		v.loadImages.SetVisible(false)
		v.loadImages.OnClicked(v.loadRemoteImages)
		v.headerGrid.AddWidget5(v.loadImages.QWidget, 0, 2, n+1, 1, qt.AlignRight|qt.AlignBottom)
	}
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

// textToHTML returns plain text as HTML that shows it as it is, in a fixed
// width font as it was likely written in, with its links made links.
func textToHTML(text string) string {
	return "<pre style=\"white-space: pre-wrap; font-family: monospace\">" + message.Linked(text) + "</pre>"
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
