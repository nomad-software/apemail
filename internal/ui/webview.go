package ui

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"runtime"
	"strings"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/webengine"

	"github.com/nomad-software/apemail/internal/images"
	"github.com/nomad-software/apemail/internal/message"
)

// Messages are shown, and written (editor.go), by Qt WebEngine, the web
// browser Chromium within the program, so that HTML looks as its sender
// meant it to. It runs no scripts, and is kept from the internet:
//
//   - What a host (webHost: the message view, or the body of a message
//     being written) shows is served to it from memory, by a scheme of the
//     program's own (apemail://p<id>-<serial>/...), and the parts it shows
//     by cid:, both by serveWeb. Its profile is off the record, so nothing
//     of it is written to disk.
//   - Every request it makes passes through interceptWeb, which refuses
//     all but those and data:. An image on the web is refused too, unless
//     its host has it (webImage): the view remembers it, until the user has
//     the images loaded, and the body fetches it if the account loads
//     them; internal/images fetches them, with its rules, and they are then
//     served by the program in their place (apemail://.../image).
//   - A link is not followed in it: clicking one in a message shown opens
//     it in the web browser, or a new message for mailto:
//     (messageView.navigate), and in a body nothing at all.
//   - It has no network at all: it is given a proxy that is not there
//     (webFlags), as some of what Chromium does goes past the
//     interceptor, as a connection made ahead for <link rel="preconnect">.
//     Nor does it use the graphics card.
const webScheme = "apemail"

// webFlags are given to Chromium, after those the user gives it, so that
// they win (chromiumFlags). Every connection it would make goes to a proxy
// on a port of this computer that is closed (the discard port), loopback
// included, which it otherwise goes to directly: it never needs the
// network, as the program serves it all it shows. It draws without the
// graphics card, which mail does not need, so that nothing a message holds
// reaches the card's driver, and none of the drivers that leave a page blank
// or flickering, or that it cannot share pictures with (GBM), are used.
const webFlags = "--proxy-server=http://127.0.0.1:9 --proxy-bypass-list=<-loopback> --disable-gpu"

// chromiumFlags returns the flags Chromium is given: those the user gives
// it, and then webFlags.
func chromiumFlags(user string) string {
	return strings.TrimSpace(user + " " + webFlags)
}

// csp is the policy every message is shown with, which refuses what
// interceptWeb would, in case it is asked for some way that it does not
// see: nothing but images, styles of its own and fonts it carries, no
// scripts, no forms, and nothing taken as the base of its addresses.
const csp = "default-src 'none'; img-src apemail: cid: data: http: https:; style-src 'unsafe-inline' apemail: cid:; " +
	"font-src apemail: cid: data:; base-uri 'none'; form-action 'none'"

// PrepareWeb registers the schemes messages are served by, and gives
// Chromium its flags, which has to be done before the application is made,
// as has sharing OpenGL contexts, which WebEngine needs.
func PrepareWeb() {
	os.Setenv("QTWEBENGINE_CHROMIUM_FLAGS", chromiumFlags(os.Getenv("QTWEBENGINE_CHROMIUM_FLAGS")))
	for name, syntax := range map[string]webengine.QWebEngineUrlScheme__Syntax{
		webScheme: webengine.QWebEngineUrlScheme__Host,
		"cid":     webengine.QWebEngineUrlScheme__Path,
	} {
		s := webengine.NewQWebEngineUrlScheme2([]byte(name))
		s.SetSyntax(syntax)
		webengine.QWebEngineUrlScheme_RegisterScheme(s)
		s.Delete()
	}
	qt.QCoreApplication_SetAttribute(qt.AA_ShareOpenGLContexts)
}

var (
	// profile is the one the pages of every view are made in, once one is.
	profile *webengine.QWebEngineProfile
	// webPages are what show pages, by their id, which is in the address of
	// each page they show; lastPage is the id given last.
	webPages = map[int]webHost{}
	lastPage int
)

// webHost is what shows pages by the program's own scheme: a message view,
// or the body of a message being written.
type webHost interface {
	// base returns its page.
	base() *webPage
	// serve returns what is at an address of the page it shows, and its
	// type.
	serve(u *url.URL) ([]byte, string, bool)
	// part returns a part the page shows by cid:.
	part(id string) (message.Attachment, bool)
	// webImage returns the address of the program's own that an image on
	// the web the page asks for is served at instead, or nothing if it is
	// refused.
	webImage(address string) string
}

// webPage is a page of Qt WebEngine, in the profile every one shares, and
// the view that shows it, in the fonts of the program. id is its own in
// webPages, and serial that of what it shows, which changes each time it
// shows something else, so that what was asked for by what it showed
// before is not served. gone is set once it is destroyed.
type webPage struct {
	web    *webengine.QWebEngineView
	page   *webengine.QWebEnginePage
	id     int
	serial int
	gone   bool
}

// newWebPage makes the page and view of the host, which the parent owns,
// with the fixed font for text in place of the program's own if fixed is
// set. It is forgotten once the parent is destroyed.
func newWebPage(h webHost, parent *qt.QObject, fixed bool) *webPage {
	lastPage++
	p := &webPage{id: lastPage}
	webPages[p.id] = h
	p.page = webengine.NewQWebEnginePage4(webProfile(), parent)

	s := p.page.Settings()
	font := qt.QApplication_Font()
	if fixed {
		font = qt.QFontDatabase_SystemFont(qt.QFontDatabase__FixedFont)
	}
	info := qt.NewQFontInfo(font)
	px := info.PixelSize()
	info.Delete()
	for _, which := range []webengine.QWebEngineSettings__FontFamily{webengine.QWebEngineSettings__StandardFont, webengine.QWebEngineSettings__SansSerifFont} {
		s.SetFontFamily(which, font.Family())
	}
	runtime.KeepAlive(font)
	system := qt.QFontDatabase_SystemFont(qt.QFontDatabase__FixedFont)
	s.SetFontFamily(webengine.QWebEngineSettings__FixedFont, system.Family())
	runtime.KeepAlive(system)
	s.SetFontSize(webengine.QWebEngineSettings__DefaultFontSize, px)
	s.SetFontSize(webengine.QWebEngineSettings__DefaultFixedFontSize, px)

	p.web = webengine.NewQWebEngineView4(p.page)
	parent.OnDestroyed(func() {
		p.gone = true
		delete(webPages, p.id)
	})
	return p
}

// pageURL returns the address of what is at the path in the page shown.
func (p *webPage) pageURL(path string) string {
	return webScheme + "://" + pageHost(p.id, p.serial) + "/" + path
}

// load shows what is at the path of a page of the program's own, which is
// a new one.
func (p *webPage) load(path string) {
	p.serial++
	u := qt.NewQUrl3(p.pageURL(path))
	p.page.Load(u)
	u.Delete()
}

// paint gives the page the colour of the theme's base, behind what it
// shows.
func (p *webPage) paint() {
	palette := qt.QGuiApplication_Palette()
	base := palette.Color(qt.QPalette__Active, qt.QPalette__Base)
	p.page.SetBackgroundColor(base)
	runtime.KeepAlive(palette)
}

// webProfile returns the profile pages are made in, which it makes the
// first time.
func webProfile() *webengine.QWebEngineProfile {
	if profile != nil {
		return profile
	}
	profile = webengine.NewQWebEngineProfile()
	profile.SetHttpCacheType(webengine.QWebEngineProfile__NoCache)
	profile.SetPersistentCookiesPolicy(webengine.QWebEngineProfile__NoPersistentCookies)
	s := profile.Settings()
	for attr, on := range map[webengine.QWebEngineSettings__WebAttribute]bool{
		webengine.QWebEngineSettings__JavascriptEnabled:                 false,
		webengine.QWebEngineSettings__JavascriptCanOpenWindows:          false,
		webengine.QWebEngineSettings__JavascriptCanAccessClipboard:      false,
		webengine.QWebEngineSettings__LocalStorageEnabled:               false,
		webengine.QWebEngineSettings__LocalContentCanAccessRemoteUrls:   false,
		webengine.QWebEngineSettings__LocalContentCanAccessFileUrls:     false,
		webengine.QWebEngineSettings__PluginsEnabled:                    false,
		webengine.QWebEngineSettings__PdfViewerEnabled:                  false,
		webengine.QWebEngineSettings__WebGLEnabled:                      false,
		webengine.QWebEngineSettings__ScreenCaptureEnabled:              false,
		webengine.QWebEngineSettings__HyperlinkAuditingEnabled:          false,
		webengine.QWebEngineSettings__DnsPrefetchEnabled:                false,
		webengine.QWebEngineSettings__AllowRunningInsecureContent:       false,
		webengine.QWebEngineSettings__AllowGeolocationOnInsecureOrigins: false,
		webengine.QWebEngineSettings__ErrorPageEnabled:                  false,
		webengine.QWebEngineSettings__AutoLoadIconsForPage:              false,
		// Dropping a file on the message would open it in its place.
		webengine.QWebEngineSettings__NavigateOnDropEnabled: false,
		// Showing a message leaves the focus in the list, where Delete
		// deletes it.
		webengine.QWebEngineSettings__FocusOnNavigationEnabled: false,
	} {
		s.SetAttribute(attr, on)
	}

	handler := webengine.NewQWebEngineUrlSchemeHandler2(profile.QObject)
	handler.OnRequestStarted(serveWeb)
	profile.InstallUrlSchemeHandler([]byte(webScheme), handler)
	profile.InstallUrlSchemeHandler([]byte("cid"), handler)
	interceptor := webengine.NewQWebEngineUrlRequestInterceptor2(profile.QObject)
	interceptor.OnInterceptRequest(interceptWeb)
	profile.SetUrlRequestInterceptor(interceptor)
	return profile
}

// encoded returns the address, encoded as it is sent. The address is a
// value miqt frees itself, so it is kept while it is read.
func encoded(u *qt.QUrl) string {
	s := string(u.ToEncoded())
	runtime.KeepAlive(u)
	return s
}

// pageHost is the host of a page of the program's own, which changes with
// each one its host shows.
func pageHost(id, serial int) string {
	return fmt.Sprintf("p%d-%d", id, serial)
}

// shownBy returns the host an address of the program's own scheme is of,
// if it is of what the host shows now.
func shownBy(address string) webHost {
	u, err := url.Parse(address)
	if err != nil || u.Scheme != webScheme {
		return nil
	}
	var id, serial int
	if _, err := fmt.Sscanf(u.Host, "p%d-%d", &id, &serial); err != nil || pageHost(id, serial) != u.Host {
		return nil
	}
	h := webPages[id]
	if h == nil || h.base().gone || h.base().serial != serial {
		return nil
	}
	return h
}

// interceptWeb refuses every request a page makes but for what the message
// it shows carries (webRequest), and serves the images on the web the user
// has had loaded in place of those it asks for.
func interceptWeb(info *webengine.QWebEngineUrlRequestInfo) {
	allow, to := webRequest(encoded(info.RequestUrl()), encoded(info.FirstPartyUrl()), info.ResourceType())
	switch {
	case to != "":
		u := qt.NewQUrl3(to)
		info.Redirect(u)
		u.Delete()
	case !allow:
		info.Block(true)
	}
}

// webRequest returns if a request for the address, of the kind, made for
// the page at from, is let through, or else the address it is sent to
// instead, if any: the page a host shows, the parts it shows by cid:,
// data:, and the images on the web its host serves (webImage).
func webRequest(address, from string, kind webengine.QWebEngineUrlRequestInfo__ResourceType) (allow bool, to string) {
	scheme, _, _ := strings.Cut(address, ":")
	h := shownBy(from)
	switch strings.ToLower(scheme) {
	case "data":
		return true, ""
	case "cid":
		return h != nil, ""
	case webScheme:
		// The page a host shows, whatever is named as the first party, as
		// it is the page shown before while that is still loading, and
		// what it asks of its own host: no page may show what another
		// does. Each host goes only to its own page (navigate).
		if kind == webengine.QWebEngineUrlRequestInfo__ResourceTypeMainFrame {
			return shownBy(address) != nil, ""
		}
		return shownBy(address) != nil && hostOf(address) == hostOf(from), ""
	case "http", "https":
		if h != nil && kind == webengine.QWebEngineUrlRequestInfo__ResourceTypeImage && images.Web(address) {
			if to := h.webImage(address); to != "" {
				return false, to
			}
		}
	}
	return false, ""
}

// hostOf returns the host of the address, if it is of the program's own
// scheme.
func hostOf(address string) string {
	u, err := url.Parse(address)
	if err != nil || u.Scheme != webScheme {
		return ""
	}
	return u.Host
}

// serveWeb serves what a page asks for by the program's own scheme or by
// cid:, if it is of what its host shows.
func serveWeb(job *webengine.QWebEngineUrlRequestJob) {
	data, kind, ok := webContent(encoded(job.RequestUrl()), encoded(job.Initiator()))
	if !ok {
		job.Fail(webengine.QWebEngineUrlRequestJob__UrlNotFound)
		return
	}
	// The job deletes the buffer when it is done with it.
	buf := qt.NewQBuffer2(job.QObject)
	buf.SetData(data)
	job.Reply([]byte(kind), buf.QIODevice)
}

// webContent returns what is served at an address, asked for by a page at
// the address from, and its type: what its host serves, or a part it
// carries (cid:).
func webContent(address, from string) ([]byte, string, bool) {
	u, err := url.Parse(address)
	if err != nil {
		return nil, "", false
	}
	if u.Scheme == "cid" {
		h := shownBy(from)
		if h == nil {
			return nil, "", false
		}
		id, _ := url.PathUnescape(u.Opaque)
		a, ok := h.part(id)
		return a.Data, a.ContentType, ok
	}
	h := shownBy(address)
	if h == nil {
		return nil, "", false
	}
	return h.serve(u)
}

// newWebView makes the view that shows the body of messages for v.
func (v *messageView) newWebView() {
	v.webPage = newWebPage(v, v.QObject, false)
	v.page.OnAcceptNavigationRequest(func(_ func(*qt.QUrl, webengine.QWebEnginePage__NavigationType, bool) bool, u *qt.QUrl, typ webengine.QWebEnginePage__NavigationType, mainFrame bool) bool {
		return v.navigate(encoded(u), typ, mainFrame)
	})
	// A link to open in a window of its own (target="_blank").
	v.page.OnNewWindowRequested(func(r *webengine.QWebEngineNewWindowRequest) {
		v.follow(encoded(r.RequestedUrl()))
	})
	v.page.OnLoadFinished(func(bool) {
		if v.gone || shownBy(encoded(v.page.Url())) != webHost(v) {
			return
		}
		v.loaded = v.serial
		v.imagesWanted()
	})
	v.web.OnContextMenuEvent(func(_ func(*qt.QContextMenuEvent), e *qt.QContextMenuEvent) {
		menu := v.contextMenu()
		pos := e.GlobalPos()
		menu.ExecWithPos(pos)
		runtime.KeepAlive(pos)
		menu.DeleteLater()
	})
}

// base returns the page of the view.
func (v *messageView) base() *webPage { return v.webPage }

// serve returns the page of the message shown, or an image of the web the
// user has had loaded.
func (v *messageView) serve(u *url.URL) ([]byte, string, bool) {
	switch u.Path {
	case "/message":
		return []byte(v.html), "text/html; charset=utf-8", true
	case "/image":
		data, ok := v.images[u.Query().Get("src")]
		return data, http.DetectContentType(data), ok
	}
	return nil, "", false
}

// part returns a part the message shown carries.
func (v *messageView) part(id string) (message.Attachment, bool) {
	if v.msg == nil {
		return message.Attachment{}, false
	}
	return v.msg.Inline(id)
}

// webImage returns where an image of the web that the user has had loaded
// is served, and otherwise remembers it, to be loaded if the user asks.
func (v *messageView) webImage(address string) string {
	if _, ok := v.images[address]; ok {
		return v.pageURL("image?src=" + url.QueryEscape(address))
	}
	v.want(address)
	return ""
}

// navigate reports if the page may go to an address: only to the message
// shown, and to a place within it that a link goes to. Any other link that
// is clicked is followed outside (follow).
func (v *messageView) navigate(address string, typ webengine.QWebEnginePage__NavigationType, mainFrame bool) bool {
	page, _, _ := strings.Cut(address, "#")
	ours := mainFrame && page == v.pageURL("message")
	if typ == webengine.QWebEnginePage__NavigationTypeLinkClicked {
		if !ours {
			v.follow(address)
		}
		return ours
	}
	return ours && typ != webengine.QWebEnginePage__NavigationTypeFormSubmitted
}

// contextMenu returns the menu of the message's body: to open and copy the
// link it is on, if it is on one, and to copy and select the text.
func (v *messageView) contextMenu() *qt.QMenu {
	menu := qt.NewQMenu(v.web.QWidget)
	link := ""
	if r := v.web.LastContextMenuRequest(); r != nil {
		link = encoded(r.LinkUrl())
	}
	if link != "" {
		menu.AddActionWithText("Open Link").OnTriggered(func() { v.follow(link) })
		menu.AddActionWithText("Copy Link Address").OnTriggered(func() { qt.QGuiApplication_Clipboard().SetText(link) })
		menu.AddSeparator()
	}
	copyText := menu.AddActionWithText("Copy")
	copyText.SetEnabled(v.web.HasSelection())
	copyText.OnTriggered(func() { v.page.TriggerAction(webengine.QWebEnginePage__Copy, false) })
	menu.AddActionWithText("Select All").OnTriggered(func() { v.page.TriggerAction(webengine.QWebEnginePage__SelectAll, false) })
	return menu
}

// doctype finds the doctype at the start of a page, with the comments and
// spaces before it, which has to stay first for the page to be laid out as
// it asks.
var doctype = regexp.MustCompile(`(?is)^(?:\s|<!--.*?-->)*<!doctype[^>]*>`)

// pageHead is what goes before what a page shows: the policy it is shown
// with (csp) and the colours of the theme (themeCSS), in a style the editor
// changes as the theme does.
func pageHead() string {
	return `<meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="` + csp + `">` +
		`<style id="apemail-theme">` + themeCSS() + `</style>`
}

// themeCSS is the style of the colours of the theme, which a page takes
// for its text and links where it gives none of its own. The colour behind
// it is the page's (webPage.paint), and not set in CSS, so that the
// background a message gives its body, by its style or bgcolor, still fills
// the page; nor is color-scheme, as Chromium paints the page its own dark
// colour for a dark one.
func themeCSS() string {
	palette := qt.QGuiApplication_Palette()
	defer runtime.KeepAlive(palette)
	colour := func(role qt.QPalette__ColorRole) string { return palette.Color(qt.QPalette__Active, role).Name() }
	return `:where(html) { color: ` + colour(qt.QPalette__Text) + ` } :where(a:link) { color: ` + colour(qt.QPalette__Link) + ` }`
}

// messagePage returns the page that shows the HTML of a message: as it is,
// after pageHead.
func messagePage(body string) string {
	at := 0
	if m := doctype.FindStringIndex(body); m != nil {
		at = m[1]
	}
	return body[:at] + pageHead() + body[at:]
}
