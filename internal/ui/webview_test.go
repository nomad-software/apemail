package ui

import (
	"errors"
	"net"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/mainthread"
	"github.com/mappu/miqt/qt6/webengine"

	"github.com/nomad-software/apemail/internal/message"
	"github.com/nomad-software/apemail/internal/settings"
)

// newShownView makes a message view of the size, shown, which is deleted
// when the test ends.
func newShownView(t *testing.T, width, height int) *messageView {
	var v *messageView
	mainthread.Wait(func() {
		v = newMessageView()
		v.Resize(width, height)
		v.Show()
	})
	t.Cleanup(func() { mainthread.Wait(func() { v.DeleteLater() }) })
	return v
}

// showWhole shows the message in the view and waits for its page to load.
func showWhole(t *testing.T, v *messageView, m *message.Message) {
	t.Helper()
	mainthread.Wait(func() { v.show(m, "INBOX", 1) })
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
	var focused *qt.QWidget
	mainthread.Wait(func() {
		focused = qt.QApplication_FocusWidget()
		v.web.SetFocus()
		v.page.TriggerAction(webengine.QWebEnginePage__SelectAll, false)
	})
	var text string
	wait(t, "the text to be selected", func() bool {
		text = v.web.SelectedText()
		return v.web.HasSelection()
	})
	mainthread.Wait(func() {
		v.page.TriggerAction(webengine.QWebEnginePage__Unselect, false)
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
	clickWith(v, x, y, qt.LeftButton)
}

// clickWith clicks the body of the view at the point with the button.
func clickWith(v *messageView, x, y int, button qt.MouseButton) {
	p := qt.NewQPointF3(float64(x), float64(y))
	defer p.Delete()
	target := v.web.FocusProxy()
	for _, typ := range []qt.QEvent__Type{qt.QEvent__MouseButtonPress, qt.QEvent__MouseButtonRelease} {
		buttons := button
		if typ == qt.QEvent__MouseButtonRelease {
			buttons = qt.NoButton
		}
		e := qt.NewQMouseEvent(typ, p, button, buttons, qt.NoModifier)
		qt.QCoreApplication_SendEvent(target.QObject, e.QEvent)
		e.Delete()
	}
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
	mainthread.Wait(func() {
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
	mainthread.Wait(func() {
		if v.page.Settings().TestAttribute(webengine.QWebEngineSettings__JavascriptEnabled) {
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
	mainthread.Wait(func() {
		other.images["https://example.com/a.png"] = red
		stolen = other.pageURL("image?src=https%3A%2F%2Fexample.com%2Fa.png")
		before = other.pageURL("message")
	})

	v := newShownView(t, 300, 200)
	showWhole(t, v, htmlMessageOf(`<p>Nothing red</p><img src="`+stolen+`" width="60" height="40"><iframe src="`+before+`"></iframe>`))
	time.Sleep(500 * time.Millisecond)
	mainthread.Wait(func() {
		if n := redPixels(v); n != 0 {
			t.Errorf("%d red pixels of another view are shown", n)
		}
		if n := redPixels(other); n < 60*40 {
			t.Errorf("%d red pixels of the image the message carries are shown", n)
		}
	})

	// What was shown before is served no more.
	mainthread.Wait(func() { other.show(htmlMessageOf("<p>Another</p>"), "INBOX", 2) })
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
	mainthread.Wait(func() {
		before := v.pageURL("message")
		v.show(htmlMessageOf("<p>Hello</p>"), "INBOX", 1)
		page := v.pageURL("message")
		v.images["https://example.com/a.png"] = []byte("PNG")
		mainFrame, image, frame := webengine.QWebEngineUrlRequestInfo__ResourceTypeMainFrame, webengine.QWebEngineUrlRequestInfo__ResourceTypeImage, webengine.QWebEngineUrlRequestInfo__ResourceTypeSubFrame
		for _, c := range []struct {
			address, from string
			kind          webengine.QWebEngineUrlRequestInfo__ResourceType
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
			{"https://example.com/a.css", page, webengine.QWebEngineUrlRequestInfo__ResourceTypeStylesheet, false, ""},
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
	mainthread.Wait(func() { v.compose = func(to, _ string) { wrote = append(wrote, to) } })
	showWhole(t, v, htmlMessageOf(`<body style="margin: 0">`+
		`<a `+block+` href="https://example.com/a?x=1&amp;y=2">a link</a>`+
		`<a `+block+` href="https://example.com/b" target="_blank">a window</a>`+
		`<a `+block+` href="mailto:someone@example.com">mail</a>`+
		`<a `+block+` href="#end">further down</a>`+
		`<form action="https://example.com/form"><input type="submit" `+block+` value="Send"></form>`+
		`<p id="end">The end</p></body>`))
	for i := range 5 {
		mainthread.Wait(func() { clickAt(v, 100, 20+40*i) })
		time.Sleep(200 * time.Millisecond)
	}
	want := []string{"https://example.com/a?x=1&y=2", "https://example.com/b"}
	eventually(t, "the links to open", func() bool { return mainthread.Wait2(func() bool { return len(opened) == 2 && len(wrote) == 1 }) })
	mainthread.Wait(func() {
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
		runtime.KeepAlive(pixmap)
		return qt.NewQColor4(img.Pixel(img.Width()-5, img.Height()-5) & 0xffffff).Name()
	}
	useTheme(t, settings.ThemeDark)
	showWhole(t, v, htmlMessageOf("<p>Hello</p>"))
	wait(t, "the page in the dark theme", func() bool { return pixel() == "#1e1e1e" })
	textIn := func() string {
		palette := qt.QGuiApplication_Palette()
		defer runtime.KeepAlive(palette)
		return "color: " + palette.Color(qt.QPalette__Active, qt.QPalette__Text).Name()
	}
	mainthread.Wait(func() {
		if !strings.Contains(v.html, textIn()) {
			t.Errorf("served %s", v.html)
		}
	})

	showWhole(t, v, htmlMessageOf(`<body style="background: #00ff00">Green</body>`))
	wait(t, "the message's own background", func() bool { return pixel() == "#00ff00" })

	showWhole(t, v, &message.Message{Text: "Hello"})
	wait(t, "text in the dark theme", func() bool { return pixel() == "#1e1e1e" })
	mainthread.Wait(func() { applyTheme(settings.ThemeLight) })
	wait(t, "text in the light theme", func() bool { return pixel() != "#1e1e1e" })
	mainthread.Wait(func() {
		if !strings.Contains(v.html, textIn()) {
			t.Errorf("served %s", v.html)
		}
	})

	mainthread.Wait(func() {
		v.Hide()
		applyTheme(settings.ThemeDark)
	})
	wait(t, "the hidden view to be left", func() bool { return v.stale })
	mainthread.Wait(v.Show)
	wait(t, "the view seen in the dark theme", func() bool { return !v.stale && pixel() == "#1e1e1e" })
}

// TestDoctypeFirst checks that the page of a message that has a doctype
// keeps it first, with what may come before it, so that it is laid out as
// it asks, and that the message is otherwise served as it is.
func TestDoctypeFirst(t *testing.T) {
	mainthread.Wait(func() {
		head := webPage("")
		for _, c := range []struct{ doctype, rest string }{
			{"<!-- a -->\r\n<!DOCTYPE html PUBLIC \"-//W3C//DTD XHTML 1.0 Transitional//EN\">", "<html><body>Hello"},
			{" <!doctype html>", "<p>Hello</p>"},
			{"", "<html><body>Hello"},
			{"", "<p>a</p><!doctype html>"},
		} {
			if got, want := webPage(c.doctype+c.rest), c.doctype+head+c.rest; got != want {
				t.Errorf("served %q, want %q", got, want)
			}
		}
	})
}

// TestFocusStays checks that showing a message leaves the focus where it
// was, as in the list, where Delete deletes it.
func TestFocusStays(t *testing.T) {
	var window *qt.QWidget
	var field *qt.QLineEdit
	var v *messageView
	mainthread.Wait(func() {
		window = qt.NewQWidget2()
		layout := qt.NewQVBoxLayout(window)
		field = qt.NewQLineEdit2()
		layout.AddWidget(field.QWidget)
		v = newMessageView()
		layout.AddWidget(v.QWidget)
		window.Resize(400, 400)
		window.Show()
		window.ActivateWindow()
		field.SetFocus()
	})
	t.Cleanup(func() { mainthread.Wait(func() { window.DeleteLater() }) })
	showWhole(t, v, htmlMessageOf(`<p>Hello <input autofocus></p>`))
	time.Sleep(300 * time.Millisecond)
	mainthread.Wait(func() {
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
	mainthread.Wait(func() {
		names, copyOn := texts()
		if !slices.Equal(names, []string{"Copy", "Select All"}) || copyOn {
			t.Errorf("the menu has %q, Copy enabled %v", names, copyOn)
		}
		v.page.TriggerAction(webengine.QWebEnginePage__SelectAll, false)
	})
	wait(t, "the text to be selected", func() bool { return v.web.HasSelection() })
	mainthread.Wait(func() {
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
	var menu *qt.QMenu
	for _, item := range []string{"Open Link", "Copy Link Address"} {
		mainthread.Start(func() { clickWith(v, 50, 50, qt.RightButton) })
		wait(t, "the menu", func() bool {
			if w := qt.QApplication_ActivePopupWidget(); w != nil {
				menu = qt.UnsafeNewQMenu(w.UnsafePointer())
			}
			return qt.QApplication_ActivePopupWidget() != nil
		})
		mainthread.Wait(func() {
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
		wait(t, "the menu to close", func() bool { return qt.QApplication_ActivePopupWidget() == nil })
	}
	mainthread.Wait(func() {
		if !slices.Equal(opened, []string{"https://example.com/a"}) {
			t.Errorf("opened %q", opened)
		}
		if got := qt.QGuiApplication_Clipboard().Text(); got != "https://example.com/a" {
			t.Errorf("copied %q", got)
		}
	})
}

// TestFailedShown checks that what kept a message from being shown is
// shown in its place, as it is written.
func TestFailedShown(t *testing.T) {
	v := newShownView(t, 400, 300)
	mainthread.Wait(func() { v.failed(errors.New("the server said <b>no</b> & left")) })
	if got := pageText(t, v); got != "the server said <b>no</b> & left" {
		t.Errorf("shown %q", got)
	}
}

// redPixels counts the red pixels the body of the view shows.
func redPixels(v *messageView) int {
	pixmap := v.web.Grab()
	img := pixmap.ToImage()
	runtime.KeepAlive(pixmap)
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
