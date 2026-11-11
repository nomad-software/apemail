package ui

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/nomad-software/qute/qtcore"
	"github.com/nomad-software/qute/qtgui"
	"github.com/nomad-software/qute/qtwebenginecore"
	"github.com/nomad-software/qute/qtwebenginewidgets"
	"github.com/nomad-software/qute/qtwidgets"

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
	for name, syntax := range map[string]qtwebenginecore.QWebEngineUrlSchemeSyntax{
		webScheme: qtwebenginecore.QWebEngineUrlSchemeSyntaxHost,
		"cid":     qtwebenginecore.QWebEngineUrlSchemeSyntaxPath,
	} {
		s := qtwebenginecore.NewQWebEngineUrlSchemeWithBytes([]byte(name))
		s.SetSyntax(syntax)
		qtwebenginecore.QWebEngineUrlSchemeRegisterScheme(s)
	}
	qtcore.QCoreApplicationSetAttribute(qtcore.AA_ShareOpenGLContexts)
}

var (
	// profile is the one the pages of every view are made in, once one is,
	// and interceptor what is given every request they make, which the
	// profile does not own.
	profile     *qtwebenginecore.QWebEngineProfile
	interceptor *requestInterceptor
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
	web    *hostView
	page   *hostPage
	id     int
	serial int
	gone   bool
}

// hostPage is the page of a webPage, which asks its host where it may go
// and tells it what is written to its console.
type hostPage struct {
	qtwebenginecore.QWebEnginePageBase
	// navigate reports if the page may go to an address.
	navigate func(address string, typ qtwebenginecore.QWebEnginePageNavigationType, mainFrame bool) bool
	// console is told what is written to the console, if it is set, in
	// place of it being logged.
	console func(msg string)
}

func (p *hostPage) AcceptNavigationRequest(u *qtcore.QUrl, typ qtwebenginecore.QWebEnginePageNavigationType, mainFrame bool) bool {
	if p.navigate == nil {
		return p.Super().AcceptNavigationRequest(u, typ, mainFrame)
	}
	return p.navigate(encoded(u), typ, mainFrame)
}

func (p *hostPage) JavaScriptConsoleMessage(level qtwebenginecore.QWebEnginePageJavaScriptConsoleMessageLevel, msg string, line int, source string) {
	if p.console == nil {
		p.Super().JavaScriptConsoleMessage(level, msg, line, source)
		return
	}
	p.console(msg)
}

// hostView is the view of a webPage, which shows a menu of its host's and
// tells it when the theme has changed.
type hostView struct {
	qtwebenginewidgets.QWebEngineViewBase
	// menu shows the menu of the view at a place on the screen, if it is
	// set, in place of Qt WebEngine's own.
	menu func(at qtcore.QPoint)
	// themed is called when the palette has changed, if it is set.
	themed func()
}

func (v *hostView) ContextMenuEvent(e *qtgui.QContextMenuEvent) {
	if v.menu == nil {
		v.Super().ContextMenuEvent(e)
		return
	}
	v.menu(e.GlobalPos())
}

func (v *hostView) ChangeEvent(e *qtcore.QEvent) {
	v.Super().ChangeEvent(e)
	if e.Type() == qtcore.QEventPaletteChange && v.themed != nil {
		v.themed()
	}
}

// newWebPage makes the page and view of the host, which the parent owns,
// with the fixed font for text in place of the program's own if fixed is
// set. It is forgotten once the parent is destroyed.
func newWebPage(h webHost, parent qtwidgets.QWidgetLike, fixed bool) *webPage {
	lastPage++
	p := &webPage{id: lastPage, page: &hostPage{}, web: &hostView{}}
	webPages[p.id] = h
	qtwebenginecore.NewQWebEnginePageWithProfileWith(p.page, webProfile(), parent.AsQWidget())

	s := p.page.Settings()
	font := qtwidgets.QApplicationFont()
	if fixed {
		font = qtgui.QFontDatabaseGetSystemFont(qtgui.QFontDatabaseFixedFont)
	}
	px := qtgui.NewQFontInfoWithFont(font).PixelSize()
	for _, which := range []qtwebenginecore.QWebEngineSettingsFontFamily{qtwebenginecore.QWebEngineSettingsStandardFont, qtwebenginecore.QWebEngineSettingsSansSerifFont} {
		s.SetFontFamily(which, font.Family())
	}
	system := qtgui.QFontDatabaseGetSystemFont(qtgui.QFontDatabaseFixedFont)
	s.SetFontFamily(qtwebenginecore.QWebEngineSettingsFixedFont, system.Family())
	s.SetFontSize(qtwebenginecore.QWebEngineSettingsDefaultFontSize, px)
	s.SetFontSize(qtwebenginecore.QWebEngineSettingsDefaultFixedFontSize, px)

	qtwebenginewidgets.NewQWebEngineViewWith(p.web, parent)
	p.web.SetPage(p.page)
	parent.AsQWidget().OnDestroyed(func(*qtcore.QObject) {
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
	p.page.Load(qtcore.NewQUrlWithUrl(p.pageURL(path)))
}

// paint gives the page the colour of the theme's base, behind what it
// shows.
func (p *webPage) paint() {
	p.page.SetBackgroundColor(qtgui.QGuiApplicationPalette().GroupColor(qtgui.QPaletteActive, qtgui.QPaletteBase))
}

// webProfile returns the profile pages are made in, which it makes the
// first time: one of the program's own, off the record, as it has no name,
// so that nothing of it is written to disk (TestProfileOffTheRecord).
func webProfile() *qtwebenginecore.QWebEngineProfile {
	if profile != nil {
		return profile
	}
	profile = qtwebenginecore.NewQWebEngineProfile(nil)
	profile.SetHttpCacheType(qtwebenginecore.QWebEngineProfileNoCache)
	profile.SetPersistentCookiesPolicy(qtwebenginecore.QWebEngineProfileNoPersistentCookies)
	s := profile.Settings()
	for attr, on := range map[qtwebenginecore.QWebEngineSettingsWebAttribute]bool{
		qtwebenginecore.QWebEngineSettingsJavascriptEnabled:                 false,
		qtwebenginecore.QWebEngineSettingsJavascriptCanOpenWindows:          false,
		qtwebenginecore.QWebEngineSettingsJavascriptCanAccessClipboard:      false,
		qtwebenginecore.QWebEngineSettingsLocalStorageEnabled:               false,
		qtwebenginecore.QWebEngineSettingsLocalContentCanAccessRemoteUrls:   false,
		qtwebenginecore.QWebEngineSettingsLocalContentCanAccessFileUrls:     false,
		qtwebenginecore.QWebEngineSettingsPluginsEnabled:                    false,
		qtwebenginecore.QWebEngineSettingsPdfViewerEnabled:                  false,
		qtwebenginecore.QWebEngineSettingsWebGLEnabled:                      false,
		qtwebenginecore.QWebEngineSettingsScreenCaptureEnabled:              false,
		qtwebenginecore.QWebEngineSettingsHyperlinkAuditingEnabled:          false,
		qtwebenginecore.QWebEngineSettingsDnsPrefetchEnabled:                false,
		qtwebenginecore.QWebEngineSettingsAllowRunningInsecureContent:       false,
		qtwebenginecore.QWebEngineSettingsAllowGeolocationOnInsecureOrigins: false,
		qtwebenginecore.QWebEngineSettingsErrorPageEnabled:                  false,
		qtwebenginecore.QWebEngineSettingsAutoLoadIconsForPage:              false,
		// Dropping a file on the message would open it in its place.
		qtwebenginecore.QWebEngineSettingsNavigateOnDropEnabled: false,
		// Showing a message leaves the focus in the list, where Delete
		// deletes it.
		qtwebenginecore.QWebEngineSettingsFocusOnNavigationEnabled: false,
	} {
		s.SetAttribute(attr, on)
	}

	handler := &schemeHandler{}
	qtwebenginecore.NewQWebEngineUrlSchemeHandlerWith(handler, profile)
	profile.InstallUrlSchemeHandler([]byte(webScheme), handler)
	profile.InstallUrlSchemeHandler([]byte("cid"), handler)
	interceptor = &requestInterceptor{}
	qtwebenginecore.NewQWebEngineUrlRequestInterceptorWith(interceptor)
	profile.SetUrlRequestInterceptor(interceptor)
	return profile
}

// schemeHandler serves what pages ask for by the program's own scheme and by
// cid: (serveWeb).
type schemeHandler struct {
	qtwebenginecore.QWebEngineUrlSchemeHandlerBase
}

func (*schemeHandler) RequestStarted(job *qtwebenginecore.QWebEngineUrlRequestJob) { serveWeb(job) }

// requestInterceptor is given every request a page makes (interceptWeb).
type requestInterceptor struct {
	qtwebenginecore.QWebEngineUrlRequestInterceptorBase
}

func (*requestInterceptor) InterceptRequest(info *qtwebenginecore.QWebEngineUrlRequestInfo) {
	interceptWeb(info)
}

// encoded returns the address, encoded as it is sent.
func encoded(u *qtcore.QUrl) string {
	return string(u.ToEncoded())
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
func interceptWeb(info *qtwebenginecore.QWebEngineUrlRequestInfo) {
	allow, to := webRequest(encoded(info.RequestUrl()), encoded(info.FirstPartyUrl()), info.ResourceType())
	switch {
	case to != "":
		info.Redirect(qtcore.NewQUrlWithUrl(to))
	case !allow:
		info.Block(true)
	}
}

// webRequest returns if a request for the address, of the kind, made for
// the page at from, is let through, or else the address it is sent to
// instead, if any: the page a host shows, the parts it shows by cid:,
// data:, and the images on the web its host serves (webImage).
func webRequest(address, from string, kind qtwebenginecore.QWebEngineUrlRequestInfoResourceType) (allow bool, to string) {
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
		if kind == qtwebenginecore.QWebEngineUrlRequestInfoResourceTypeMainFrame {
			return shownBy(address) != nil, ""
		}
		return shownBy(address) != nil && hostOf(address) == hostOf(from), ""
	case "http", "https":
		if h != nil && kind == qtwebenginecore.QWebEngineUrlRequestInfoResourceTypeImage && images.Web(address) {
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
func serveWeb(job *qtwebenginecore.QWebEngineUrlRequestJob) {
	data, kind, ok := webContent(encoded(job.RequestUrl()), encoded(job.Initiator()))
	if !ok {
		job.Fail(qtwebenginecore.QWebEngineUrlRequestJobUrlNotFound)
		return
	}
	// The job deletes the buffer when it is done with it.
	buf := qtcore.NewQBuffer(job)
	buf.SetData(data)
	job.Reply([]byte(kind), buf)
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
	v.webPage = newWebPage(v, v, false)
	v.page.navigate = v.navigate
	// A link to open in a window of its own (target="_blank").
	v.page.OnNewWindowRequested(func(r *qtwebenginecore.QWebEngineNewWindowRequest) {
		v.follow(encoded(r.RequestedUrl()))
	})
	v.page.OnLoadFinished(func(bool) {
		if v.gone || shownBy(encoded(v.page.Url())) != webHost(v) {
			return
		}
		v.loaded = v.serial
		v.imagesWanted()
	})
	v.web.menu = func(at qtcore.QPoint) {
		menu := v.contextMenu()
		menu.ExecAt(at)
		menu.DeleteLater()
	}
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
func (v *messageView) navigate(address string, typ qtwebenginecore.QWebEnginePageNavigationType, mainFrame bool) bool {
	page, _, _ := strings.Cut(address, "#")
	ours := mainFrame && page == v.pageURL("message")
	if typ == qtwebenginecore.QWebEnginePageNavigationTypeLinkClicked {
		if !ours {
			v.follow(address)
		}
		return ours
	}
	return ours && typ != qtwebenginecore.QWebEnginePageNavigationTypeFormSubmitted
}

// contextMenu returns the menu of the message's body: to open and copy the
// link it is on, if it is on one, and to copy and select the text.
func (v *messageView) contextMenu() *qtwidgets.QMenu {
	menu := qtwidgets.NewQMenu(v.web)
	link := ""
	if r := v.web.LastContextMenuRequest(); r != nil {
		link = encoded(r.LinkUrl())
	}
	if link != "" {
		menu.AddActionText("Open Link").OnTriggered(func(bool) { v.follow(link) })
		menu.AddActionText("Copy Link Address").OnTriggered(func(bool) { qtgui.QGuiApplicationClipboard().SetText(link) })
		menu.AddSeparator()
	}
	copyText := menu.AddActionText("Copy")
	copyText.SetEnabled(v.web.HasSelection())
	copyText.OnTriggered(func(bool) { v.page.TriggerAction(qtwebenginecore.QWebEnginePageCopy) })
	menu.AddActionText("Select All").OnTriggered(func(bool) { v.page.TriggerAction(qtwebenginecore.QWebEnginePageSelectAll) })
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
	palette := qtgui.QGuiApplicationPalette()
	colour := func(role qtgui.QPaletteColorRole) string {
		return palette.GroupColor(qtgui.QPaletteActive, role).Name()
	}
	return `:where(html) { color: ` + colour(qtgui.QPaletteText) + ` } :where(a:link) { color: ` + colour(qtgui.QPaletteLink) + ` }`
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
