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
)

// Messages are shown by Qt WebEngine, the web browser Chromium within the
// program, so that HTML looks as its sender meant it to. It runs no scripts,
// and is kept from the internet:
//
//   - The message is served to it from memory, by a scheme of the
//     program's own (apemail://v<view>-<serial>/message), and the parts it
//     shows by cid:, both by serveWeb. Its profile is off the record, so
//     nothing of it is written to disk.
//   - Every request it makes passes through interceptWeb, which refuses
//     all but those and data:. An image on the web is refused too, and
//     remembered (messageView.want), until the user has the images
//     loaded: internal/images fetches them, with its rules, and they are
//     then served by the program in their place (apemail://.../image).
//   - A link is not followed in it: clicking one opens it in the web
//     browser, or a new message for mailto: (messageView.navigate).
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
	// webViews are the message views, by their id, which is in the
	// address of each page they show; lastView is the id given last.
	webViews = map[int]*messageView{}
	lastView int
)

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

// pageHost is the host of the page of the message a view shows, which
// changes with each one it shows.
func pageHost(view, serial int) string {
	return fmt.Sprintf("v%d-%d", view, serial)
}

// pageOf returns the view an address of the program's own scheme is of, and
// the serial of the message, if it is one.
func pageOf(address string) (*messageView, int) {
	u, err := url.Parse(address)
	if err != nil || u.Scheme != webScheme {
		return nil, 0
	}
	var id, serial int
	if _, err := fmt.Sscanf(u.Host, "v%d-%d", &id, &serial); err != nil || pageHost(id, serial) != u.Host {
		return nil, 0
	}
	return webViews[id], serial
}

// shownBy returns the view an address of the program's own scheme is of,
// if it is of the message the view shows.
func shownBy(address string) *messageView {
	v, serial := pageOf(address)
	if v == nil || v.gone || serial != v.serial {
		return nil
	}
	return v
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
// instead, if any: the page of the message a view shows, the parts it shows
// by cid:, data:, and the images on the web the user has had loaded, which
// are served by the program. The images on the web it asks for are
// remembered, to be loaded if the user asks.
func webRequest(address, from string, kind webengine.QWebEngineUrlRequestInfo__ResourceType) (allow bool, to string) {
	scheme, _, _ := strings.Cut(address, ":")
	v := shownBy(from)
	switch strings.ToLower(scheme) {
	case "data":
		return true, ""
	case "cid":
		return v != nil, ""
	case webScheme:
		page := shownBy(address)
		if page == nil {
			return false, ""
		}
		// The page of the message a view shows, whatever is named as the
		// first party, as it is the page shown before while that is
		// still loading, and what it asks of its own host: no message may
		// show what another view does.
		if kind == webengine.QWebEngineUrlRequestInfo__ResourceTypeMainFrame {
			return address == page.pageURL("message"), ""
		}
		return hostOf(address) == hostOf(from), ""
	case "http", "https":
		if v != nil && kind == webengine.QWebEngineUrlRequestInfo__ResourceTypeImage && images.Web(address) {
			if _, ok := v.images[address]; ok {
				return false, v.pageURL("image?src=" + url.QueryEscape(address))
			}
			v.want(address)
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
// cid:, if it is of the message its view shows.
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
// the address from, and its type: the page of the message, an image of the
// web the user has had loaded, or a part the message carries (cid:).
func webContent(address, from string) ([]byte, string, bool) {
	u, err := url.Parse(address)
	if err != nil {
		return nil, "", false
	}
	if u.Scheme == "cid" {
		v := shownBy(from)
		if v == nil || v.msg == nil {
			return nil, "", false
		}
		id, _ := url.PathUnescape(u.Opaque)
		a, ok := v.msg.Inline(id)
		return a.Data, a.ContentType, ok
	}
	v := shownBy(address)
	if v == nil {
		return nil, "", false
	}
	switch u.Path {
	case "/message":
		return []byte(v.html), "text/html; charset=utf-8", true
	case "/image":
		data, ok := v.images[u.Query().Get("src")]
		return data, http.DetectContentType(data), ok
	}
	return nil, "", false
}

// newWebView makes the view that shows the body of messages for v, in the
// profile every view shares, in the fonts of the program.
func (v *messageView) newWebView() {
	lastView++
	v.id = lastView
	webViews[v.id] = v
	v.page = webengine.NewQWebEnginePage4(webProfile(), v.QObject)
	v.page.OnAcceptNavigationRequest(func(_ func(*qt.QUrl, webengine.QWebEnginePage__NavigationType, bool) bool, u *qt.QUrl, typ webengine.QWebEnginePage__NavigationType, mainFrame bool) bool {
		return v.navigate(encoded(u), typ, mainFrame)
	})
	// A link to open in a window of its own (target="_blank").
	v.page.OnNewWindowRequested(func(r *webengine.QWebEngineNewWindowRequest) {
		v.follow(encoded(r.RequestedUrl()))
	})
	v.page.OnLoadFinished(func(ok bool) {
		if v.gone || shownBy(encoded(v.page.Url())) != v {
			return
		}
		v.loaded = v.serial
		v.imagesWanted()
	})

	s := v.page.Settings()
	font := qt.QApplication_Font()
	info := qt.NewQFontInfo(font)
	px := info.PixelSize()
	for _, which := range []webengine.QWebEngineSettings__FontFamily{webengine.QWebEngineSettings__StandardFont, webengine.QWebEngineSettings__SansSerifFont} {
		s.SetFontFamily(which, font.Family())
	}
	runtime.KeepAlive(font)
	info.Delete()
	fixed := qt.QFontDatabase_SystemFont(qt.QFontDatabase__FixedFont)
	s.SetFontFamily(webengine.QWebEngineSettings__FixedFont, fixed.Family())
	runtime.KeepAlive(fixed)
	s.SetFontSize(webengine.QWebEngineSettings__DefaultFontSize, px)
	s.SetFontSize(webengine.QWebEngineSettings__DefaultFixedFontSize, px)

	v.web = webengine.NewQWebEngineView4(v.page)
	v.web.OnContextMenuEvent(func(_ func(*qt.QContextMenuEvent), e *qt.QContextMenuEvent) {
		menu := v.contextMenu()
		pos := e.GlobalPos()
		menu.ExecWithPos(pos)
		runtime.KeepAlive(pos)
		menu.DeleteLater()
	})
}

// pageURL returns the address of what is at the path in the page of the
// message shown.
func (v *messageView) pageURL(path string) string {
	return webScheme + "://" + pageHost(v.id, v.serial) + "/" + path
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

// webPage returns the page that shows the HTML: as it is, after the policy
// it is shown with (csp) and the colours of the theme, which it takes for
// its text and links where it gives none of its own. The colour behind it
// is the page's (messageView.restyle), and not set in CSS, so that the
// background a message gives its body, by its style or bgcolor, still fills
// the page; nor is color-scheme, as Chromium paints the page its own dark
// colour for a dark one.
func webPage(body string) string {
	palette := qt.QGuiApplication_Palette()
	colour := func(role qt.QPalette__ColorRole) string { return palette.Color(qt.QPalette__Active, role).Name() }
	head := `<meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="` + csp + `">` +
		`<style>:where(html) { color: ` + colour(qt.QPalette__Text) + ` }` +
		` :where(a:link) { color: ` + colour(qt.QPalette__Link) + ` }</style>`
	runtime.KeepAlive(palette)
	at := 0
	if m := doctype.FindStringIndex(body); m != nil {
		at = m[1]
	}
	return body[:at] + head + body[at:]
}
