package ui

import (
	"errors"
	"net"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nomad-software/qute"
	"github.com/nomad-software/qute/qtcore"
	"github.com/nomad-software/qute/qtgui"
	"github.com/nomad-software/qute/qtwebenginecore"
	"github.com/nomad-software/qute/qtwidgets"
	"github.com/nomad-software/qute/qutetest"

	"github.com/nomad-software/apemail/internal/message"
	"github.com/nomad-software/apemail/internal/settings"
)

// newShownView makes a message view of the size, shown, which is deleted
// when the test ends.
func newShownView(t *testing.T, width, height int) *messageView {
	var v *messageView
	qutetest.Run(t, func() {
		v = newMessageView()
		v.Resize(width, height)
		v.Show()
	})
	t.Cleanup(func() { qutetest.Run(t, func() { v.DeleteLater() }) })
	return v
}

// showWhole shows the message in the view and waits for its page to load.
func showWhole(t *testing.T, v *messageView, m *message.Message) {
	t.Helper()
	qutetest.Run(t, func() { v.show(m, "INBOX", 1, 1) })
	loadedWhole(t, v)
}

// loadedWhole waits for the page of what the view shows to have loaded.
func loadedWhole(t *testing.T, v *messageView) {
	t.Helper()
	wait(t, "the message to load", func() bool { return v.loaded == v.serial })
}

// pageText returns the text of the page the view shows, once it has loaded,
// as selecting it all and copying it gives it. Only a page with the focus
// selects, so it is given it meanwhile, and then the widget that had it
// before.
func pageText(t *testing.T, v *messageView) string {
	t.Helper()
	loadedWhole(t, v)
	var focused *qtwidgets.QWidget
	qutetest.Run(t, func() {
		focused = qtwidgets.QApplicationFocusWidget()
		v.web.SetFocus()
		v.page.TriggerAction(qtwebenginecore.QWebEnginePageSelectAll)
	})
	var text string
	wait(t, "the text to be selected", func() bool {
		text = v.web.SelectedText()
		return v.web.HasSelection()
	})
	qutetest.Run(t, func() {
		v.page.TriggerAction(qtwebenginecore.QWebEnginePageUnselect)
		if focused != nil {
			focused.SetFocus()
		}
	})
	return text
}

// htmlMessageOf returns a message of the HTML.
func htmlMessageOf(html string) *message.Message {
	return &message.Message{Subject: "s", HTML: html}
}

// clickAt clicks the body of the view at the point, as the user does.
func clickAt(v *messageView, x, y int) {
	clickWith(v, x, y, qtcore.LeftButton)
}

// clickWith clicks the body of the view at the point with the button.
func clickWith(v *messageView, x, y int, button qtcore.MouseButton) {
	clickPage(v.web, x, y, button)
}

// clickAtPage clicks a page at the point, as the user does.
func clickAtPage(web *hostView, x, y int) {
	clickPage(web, x, y, qtcore.LeftButton)
}

// clickPage clicks a page at the point with the button.
func clickPage(web *hostView, x, y int, button qtcore.MouseButton) {
	qutetest.ClickAt(web.FocusProxy(), button, qtcore.QPoint{X: x, Y: y})
}

// TestNothingFetched checks that a message which links to a server in every
// way a page can has nothing at all fetched from it, not even a connection
// made to it (as <link rel="preconnect"> made, past the interceptor): images, backgrounds, stylesheets, fonts, frames, media,
// objects, scripts, prefetching, refreshing, pings and a base for its
// addresses. Of those, the images are remembered, to be loaded if the user
// asks, and nothing else.
func TestNothingFetched(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	var connections atomic.Int32
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			connections.Add(1)
			c.Close()
		}
	}()
	r := "http://" + ln.Addr().String()
	page := `<!DOCTYPE html><html><head>` +
		`<base href="` + r + `/base/">` +
		`<meta http-equiv="refresh" content="0; url=` + r + `/refresh">` +
		`<link rel="preconnect" href="` + r + `/preconnect"><link rel="dns-prefetch" href="` + r + `/dns">` +
		`<link rel="prefetch" href="` + r + `/prefetch"><link rel="preload" as="image" href="` + r + `/preload.png">` +
		`<link rel="stylesheet" href="` + r + `/style.css"><link rel="icon" href="` + r + `/icon.png">` +
		`<style>@import url(` + r + `/import.css); @font-face { font-family: f; src: url(` + r + `/font.woff) }` +
		` p { font-family: f } div { background: url(` + r + `/background.png) } li { list-style-image: url(` + r + `/bullet.png) }</style>` +
		`<script src="` + r + `/script.js"></script></head><body>` +
		`<p>Hello</p><div>A background</div><ul><li>An item</li></ul>` +
		`<img src="` + r + `/image.png"><img src="relative.png"><img srcset="` + r + `/srcset.png 2x">` +
		`<iframe src="` + r + `/frame"></iframe><object data="` + r + `/object"></object><embed src="` + r + `/embed">` +
		`<video src="` + r + `/video" poster="` + r + `/poster.png" preload="auto"></video><audio src="` + r + `/audio" preload="auto"></audio>` +
		`<a href="` + r + `/link" ping="` + r + `/ping">A link</a>` +
		`<script>new Image().src = "` + r + `/by-script.png"</script>` +
		`</body></html>`
	v := newShownView(t, 600, 800)
	showWhole(t, v, htmlMessageOf(page))
	// What is asked for after the page has loaded is asked for in this time.
	time.Sleep(time.Second)
	if n := connections.Load(); n != 0 {
		t.Errorf("%d connections were made to the server", n)
	}
	qutetest.Run(t, func() {
		if !strings.HasPrefix(encoded(v.page.Url()), webScheme+":") {
			t.Errorf("the page went to %s", encoded(v.page.Url()))
		}
		for _, src := range v.wanted {
			if strings.HasSuffix(src, ".css") || strings.HasSuffix(src, ".woff") || strings.HasSuffix(src, ".js") || strings.Contains(src, "by-script") {
				t.Errorf("%s is remembered as an image", src)
			}
		}
		if !slices.Contains(v.wanted, r+"/image.png") || !slices.Contains(v.wanted, r+"/background.png") {
			t.Errorf("the images remembered are %q", v.wanted)
		}
	})
}

// TestChromiumFlags checks that Chromium is given the flags that keep it
// off the network and the graphics card, after those the user gives it,
// so that they win, and that the program is started with them.
func TestChromiumFlags(t *testing.T) {
	for _, user := range []string{"", "--proxy-server=http://example.com:80 --enable-gpu"} {
		got := chromiumFlags(user)
		if !strings.HasPrefix(got, user) || !strings.HasSuffix(got, webFlags) {
			t.Errorf("for %q Chromium is given %q", user, got)
		}
	}
	for _, flag := range []string{"--proxy-server=http://127.0.0.1:9", "--proxy-bypass-list=<-loopback>", "--disable-gpu"} {
		if !slices.Contains(strings.Fields(webFlags), flag) {
			t.Errorf("Chromium is not given %s", flag)
		}
	}
	// TestMain prepares WebEngine as main does.
	if got := os.Getenv("QTWEBENGINE_CHROMIUM_FLAGS"); !strings.HasSuffix(got, webFlags) {
		t.Errorf("the program is started with %q", got)
	}
}

// TestJavaScriptOff checks that the scripts of a message are not run, and
// what it shows for want of them is.
func TestJavaScriptOff(t *testing.T) {
	v := newShownView(t, 600, 400)
	qutetest.Run(t, func() {
		if v.page.Settings().TestAttribute(qtwebenginecore.QWebEngineSettingsJavascriptEnabled) {
			t.Error("JavaScript is on")
		}
	})
	showWhole(t, v, htmlMessageOf(`<p id="p">Before</p><script>document.getElementById("p").textContent = "After"</script>`+
		`<noscript>No scripts</noscript><img src="cid:none" onerror="document.body.textContent = 'Erred'">`))
	text := pageText(t, v)
	if !strings.Contains(text, "Before") || !strings.Contains(text, "No scripts") || strings.Contains(text, "After") || strings.Contains(text, "Erred") {
		t.Errorf("shown %q", text)
	}
}

// TestPartsShown checks that the images a message carries (cid:) are shown,
// and nothing that another view shows, or that the view showed before.
func TestPartsShown(t *testing.T) {
	red := redImage(t)
	other := newShownView(t, 300, 200)
	showWhole(t, other, &message.Message{
		HTML:        `<img src="cid:pic" width="60" height="40">`,
		Attachments: []message.Attachment{{Filename: "pic.png", ContentType: "image/png", ContentID: "pic", Inline: true, Data: red}},
	})
	var stolen, before string
	qutetest.Run(t, func() {
		other.images["https://example.com/a.png"] = red
		stolen = other.pageURL("image?src=https%3A%2F%2Fexample.com%2Fa.png")
		before = other.pageURL("message")
	})

	v := newShownView(t, 300, 200)
	showWhole(t, v, htmlMessageOf(`<p>Nothing red</p><img src="`+stolen+`" width="60" height="40"><iframe src="`+before+`"></iframe>`))
	time.Sleep(500 * time.Millisecond)
	qutetest.Run(t, func() {
		if n := redPixels(v); n != 0 {
			t.Errorf("%d red pixels of another view are shown", n)
		}
		if n := redPixels(other); n < 60*40 {
			t.Errorf("%d red pixels of the image the message carries are shown", n)
		}
	})

	// What was shown before is served no more.
	qutetest.Run(t, func() { other.show(htmlMessageOf("<p>Another</p>"), "INBOX", 1, 2) })
	if _, _, ok := webContent(before, ""); ok {
		t.Error("the page shown before is still served")
	}
	if _, _, ok := webContent("cid:pic", before); ok {
		t.Error("a part of the message shown before is still served")
	}
}

// TestRequests checks what a page may ask for: the page of the message its
// view shows, though the page shown before is named as the first party, as
// it is while that is still loading, which was refused and nothing shown;
// what it asks of its own host, and not of another view's or of a message
// shown before; the parts the message carries; and no file or address on
// the web but the images the user has had loaded, served in their place.
func TestRequests(t *testing.T) {
	v, other := newShownView(t, 300, 200), newShownView(t, 300, 200)
	qutetest.Run(t, func() {
		before := v.pageURL("message")
		v.show(htmlMessageOf("<p>Hello</p>"), "INBOX", 1, 1)
		page := v.pageURL("message")
		v.images["https://example.com/a.png"] = []byte("PNG")
		mainFrame, image, frame := qtwebenginecore.QWebEngineUrlRequestInfoResourceTypeMainFrame, qtwebenginecore.QWebEngineUrlRequestInfoResourceTypeImage, qtwebenginecore.QWebEngineUrlRequestInfoResourceTypeSubFrame
		for _, c := range []struct {
			address, from string
			kind          qtwebenginecore.QWebEngineUrlRequestInfoResourceType
			allow         bool
			to            string
		}{
			{page, page, mainFrame, true, ""},
			{page, before, mainFrame, true, ""},
			{before, before, mainFrame, false, ""},
			// Another view's page is let through, for that view, which
			// alone goes to it (messageView.navigate).
			{other.pageURL("message"), page, mainFrame, true, ""},
			{other.pageURL("message"), page, frame, false, ""},
			{other.pageURL("image?src=x"), page, image, false, ""},
			{v.pageURL("image?src=x"), page, image, true, ""},
			{"cid:a", page, image, true, ""},
			{"cid:a", before, image, false, ""},
			{"data:image/png;base64,AA==", page, image, true, ""},
			{"file:///etc/passwd", page, image, false, ""},
			{"https://example.com/a.png", page, image, false, v.pageURL("image?src=https%3A%2F%2Fexample.com%2Fa.png")},
			{"https://example.com/a.png", before, image, false, ""},
			{"https://example.com/b.png", page, image, false, ""},
			{"https://example.com/a.css", page, qtwebenginecore.QWebEngineUrlRequestInfoResourceTypeStylesheet, false, ""},
		} {
			if allow, to := webRequest(c.address, c.from, c.kind); allow != c.allow || to != c.to {
				t.Errorf("%s for %s (%d): let through %v, sent to %q", c.address, c.from, c.kind, allow, to)
			}
		}
		if !slices.Equal(v.wanted, []string{"https://example.com/b.png"}) {
			t.Errorf("the images remembered are %q", v.wanted)
		}
	})
}

// TestLinksOpen checks that the links of a message open in the web browser
// when clicked, as do those to open in a window of their own, and mail links
// a new message; that a link to a place in the message goes there, and that
// a form is not sent; and that the message stays shown.
func TestLinksOpen(t *testing.T) {
	var opened []string
	open := openURL
	openURL = func(link string) { opened = append(opened, link) }
	t.Cleanup(func() { openURL = open })

	block := `style="display: block; height: 40px"`
	v := newShownView(t, 600, 600)
	var wrote []string
	qutetest.Run(t, func() { v.compose = func(to, _ string) { wrote = append(wrote, to) } })
	showWhole(t, v, htmlMessageOf(`<body style="margin: 0">`+
		`<a `+block+` href="https://example.com/a?x=1&amp;y=2">a link</a>`+
		`<a `+block+` href="https://example.com/b" target="_blank">a window</a>`+
		`<a `+block+` href="mailto:someone@example.com">mail</a>`+
		`<a `+block+` href="#end">further down</a>`+
		`<form action="https://example.com/form"><input type="submit" `+block+` value="Send"></form>`+
		`<p id="end">The end</p></body>`))
	for i := range 5 {
		qutetest.Run(t, func() { clickAt(v, 100, 20+40*i) })
		time.Sleep(200 * time.Millisecond)
	}
	want := []string{"https://example.com/a?x=1&y=2", "https://example.com/b"}
	eventually(t, "the links to open", func() bool { return onUI(func() bool { return len(opened) == 2 && len(wrote) == 1 }) })
	qutetest.Run(t, func() {
		if !slices.Equal(opened, want) {
			t.Errorf("opened %q, want %q", opened, want)
		}
		if !slices.Equal(wrote, []string{"someone@example.com"}) {
			t.Errorf("wrote to %q", wrote)
		}
		if page, _, _ := strings.Cut(encoded(v.page.Url()), "#"); page != v.pageURL("message") {
			t.Errorf("the page went to %s", encoded(v.page.Url()))
		}
	})
}

// TestFollowsTheme checks that a message is shown in the colours of the
// theme where it gives none of its own, its own where it does, and again
// in those of another theme when it changes: at once, or, if the view is
// not seen, once it is.
func TestFollowsTheme(t *testing.T) {
	v := newShownView(t, 400, 300)
	pixel := func() string {
		pixmap := v.web.Grab()
		img := pixmap.ToImage()
		return colourOf(img.Pixel(img.Width()-5, img.Height()-5) & 0xffffff).Name()
	}
	useTheme(t, settings.ThemeDark)
	showWhole(t, v, htmlMessageOf("<p>Hello</p>"))
	wait(t, "the page in the dark theme", func() bool { return pixel() == "#1e1e1e" })
	textIn := func() string {
		palette := qtgui.QGuiApplicationPalette()
		return "color: " + palette.GroupColor(qtgui.QPaletteActive, qtgui.QPaletteText).Name()
	}
	qutetest.Run(t, func() {
		if !strings.Contains(v.html, textIn()) {
			t.Errorf("served %s", v.html)
		}
	})

	showWhole(t, v, htmlMessageOf(`<body style="background: #00ff00">Green</body>`))
	wait(t, "the message's own background", func() bool { return pixel() == "#00ff00" })

	showWhole(t, v, &message.Message{Text: "Hello"})
	wait(t, "text in the dark theme", func() bool { return pixel() == "#1e1e1e" })
	qutetest.Run(t, func() { applyTheme(settings.ThemeLight) })
	wait(t, "text in the light theme", func() bool { return pixel() != "#1e1e1e" })
	qutetest.Run(t, func() {
		if !strings.Contains(v.html, textIn()) {
			t.Errorf("served %s", v.html)
		}
	})

	qutetest.Run(t, func() {
		v.Hide()
		applyTheme(settings.ThemeDark)
	})
	wait(t, "the hidden view to be left", func() bool { return v.stale })
	qute.PostSync(v.Show)
	wait(t, "the view seen in the dark theme", func() bool { return !v.stale && pixel() == "#1e1e1e" })
}

// TestDoctypeFirst checks that the page of a message that has a doctype
// keeps it first, with what may come before it, so that it is laid out as
// it asks, and that the message is otherwise served as it is.
func TestDoctypeFirst(t *testing.T) {
	qutetest.Run(t, func() {
		head := messagePage("")
		for _, c := range []struct{ doctype, rest string }{
			{"<!-- a -->\r\n<!DOCTYPE html PUBLIC \"-//W3C//DTD XHTML 1.0 Transitional//EN\">", "<html><body>Hello"},
			{" <!doctype html>", "<p>Hello</p>"},
			{"", "<html><body>Hello"},
			{"", "<p>a</p><!doctype html>"},
		} {
			if got, want := messagePage(c.doctype+c.rest), c.doctype+head+c.rest; got != want {
				t.Errorf("served %q, want %q", got, want)
			}
		}
	})
}

// TestFocusStays checks that showing a message leaves the focus where it
// was, as in the list, where Delete deletes it.
func TestFocusStays(t *testing.T) {
	var window *qtwidgets.QWidget
	var field *qtwidgets.QLineEdit
	var v *messageView
	qutetest.Run(t, func() {
		window = qtwidgets.NewQWidget(nil)
		layout := qtwidgets.NewQVBoxLayout(window)
		field = qtwidgets.NewQLineEdit(nil)
		layout.AddWidget(field)
		v = newMessageView()
		layout.AddWidget(v)
		window.Resize(400, 400)
		window.Show()
		window.ActivateWindow()
		field.SetFocus()
	})
	t.Cleanup(func() { qutetest.Run(t, func() { window.DeleteLater() }) })
	showWhole(t, v, htmlMessageOf(`<p>Hello <input autofocus></p>`))
	time.Sleep(300 * time.Millisecond)
	qutetest.Run(t, func() {
		if !field.HasFocus() {
			t.Error("showing a message took the focus")
		}
	})
}

// TestContextMenu checks the menu of a message's body away from a link: to
// copy the text, once some is selected, and to select it all.
func TestContextMenu(t *testing.T) {
	v := newShownView(t, 400, 300)
	showWhole(t, v, htmlMessageOf("<p>Hello</p>"))
	texts := func() (names []string, copyOn bool) {
		menu := v.contextMenu()
		defer menu.DeleteLater()
		for _, a := range menu.Actions() {
			if a.IsSeparator() {
				continue
			}
			names = append(names, a.Text())
			if a.Text() == "Copy" {
				copyOn = a.IsEnabled()
			}
		}
		return names, copyOn
	}
	qutetest.Run(t, func() {
		names, copyOn := texts()
		if !slices.Equal(names, []string{"Copy", "Select All"}) || copyOn {
			t.Errorf("the menu has %q, Copy enabled %v", names, copyOn)
		}
		v.page.TriggerAction(qtwebenginecore.QWebEnginePageSelectAll)
	})
	wait(t, "the text to be selected", func() bool { return v.web.HasSelection() })
	qutetest.Run(t, func() {
		if _, copyOn := texts(); !copyOn {
			t.Error("Copy is not enabled with text selected")
		}
	})
}

// TestLinkMenu checks that the menu of a link, opened by clicking it with
// the right button, opens it and copies its address.
func TestLinkMenu(t *testing.T) {
	var opened []string
	open := openURL
	openURL = func(link string) { opened = append(opened, link) }
	t.Cleanup(func() { openURL = open })

	v := newShownView(t, 400, 300)
	showWhole(t, v, htmlMessageOf(`<body style="margin: 0"><a style="display: block; height: 100px" href="https://example.com/a">A link</a></body>`))
	// The menu runs a loop of its own until it is closed, in which what is
	// asked of the UI's thread is done.
	var menu *qtwidgets.QMenu
	for _, item := range []string{"Open Link", "Copy Link Address"} {
		qute.Post(func() { clickWith(v, 50, 50, qtcore.RightButton) })
		wait(t, "the menu", func() bool {
			if w := qtwidgets.QApplicationActivePopupWidget(); w != nil {
				menu = qtwidgets.QMenuCast(w)
			}
			return qtwidgets.QApplicationActivePopupWidget() != nil
		})
		qutetest.Run(t, func() {
			var names []string
			for _, a := range menu.Actions() {
				if !a.IsSeparator() {
					names = append(names, a.Text())
				}
			}
			if !slices.Equal(names, []string{"Open Link", "Copy Link Address", "Copy", "Select All"}) {
				t.Errorf("the menu has %q", names)
			}
			for _, a := range menu.Actions() {
				if a.Text() == item {
					a.Trigger()
				}
			}
			menu.Close()
		})
		wait(t, "the menu to close", func() bool { return qtwidgets.QApplicationActivePopupWidget() == nil })
	}
	qutetest.Run(t, func() {
		if !slices.Equal(opened, []string{"https://example.com/a"}) {
			t.Errorf("opened %q", opened)
		}
		if got := qtgui.QGuiApplicationClipboard().Text(); got != "https://example.com/a" {
			t.Errorf("copied %q", got)
		}
	})
}

// TestFailedShown checks that what kept a message from being shown is
// shown in its place, as it is written.
func TestFailedShown(t *testing.T) {
	v := newShownView(t, 400, 300)
	qutetest.Run(t, func() { v.failed(errors.New("the server said <b>no</b> & left")) })
	if got := pageText(t, v); got != "the server said <b>no</b> & left" {
		t.Errorf("shown %q", got)
	}
}

// redPixels counts the red pixels the body of the view shows.
func redPixels(v *messageView) int {
	pixmap := v.web.Grab()
	img := pixmap.ToImage()
	n := 0
	for y := range img.Height() {
		for x := range img.Width() {
			if img.Pixel(x, y)&0xffffff == 0xff0000 {
				n++
			}
		}
	}
	return n
}

// TestProfileOffTheRecord checks that the profile messages are shown and
// written in is the program's own, not Qt WebEngine's default one, and keeps
// nothing on disk: it is off the record, with no cache and no cookies kept,
// and every page is in it.
func TestProfileOffTheRecord(t *testing.T) {
	qutetest.Run(t, func() {
		v := newMessageView()
		defer v.DeleteLater()
		if profile == nil || !profile.IsOffTheRecord() {
			t.Fatal("the profile is not off the record")
		}
		if got := profile.HttpCacheType(); got != qtwebenginecore.QWebEngineProfileNoCache {
			t.Errorf("the cache is %v", got)
		}
		if got := profile.PersistentCookiesPolicy(); got != qtwebenginecore.QWebEngineProfileNoPersistentCookies {
			t.Errorf("cookies are kept: %v", got)
		}
		if v.page.Profile().Ptr() != profile.Ptr() {
			t.Error("the page of a view is in another profile")
		}
		if profile.Ptr() == qtwebenginecore.QWebEngineProfileDefaultProfile().Ptr() {
			t.Error("the pages are in Qt WebEngine's default profile")
		}
	})
}
