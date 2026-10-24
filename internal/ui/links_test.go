package ui

import (
	"runtime"
	"slices"
	"testing"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/mainthread"

	"github.com/nomad-software/apemail/internal/message"
)

// clickLink clicks the link to href that the view shows, as the user does,
// and reports if it found one.
func clickLink(v *messageView, href string) bool {
	vp := v.browser.Viewport()
	for y := 0; y < vp.Height(); y += 2 {
		for x := 0; x < vp.Width(); x += 3 {
			at := qt.NewQPoint2(x, y)
			found := v.browser.AnchorAt(at) == href
			at.Delete()
			if !found {
				continue
			}
			p := qt.NewQPointF3(float64(x), float64(y))
			defer p.Delete()
			global := vp.MapToGlobal(p)
			defer runtime.KeepAlive(global)
			for _, typ := range []qt.QEvent__Type{qt.QEvent__MouseButtonPress, qt.QEvent__MouseButtonRelease} {
				buttons := qt.LeftButton
				if typ == qt.QEvent__MouseButtonRelease {
					buttons = qt.NoButton
				}
				e := qt.NewQMouseEvent2(typ, p, global, qt.LeftButton, buttons, qt.NoModifier)
				qt.QCoreApplication_SendEvent(vp.QObject, e.QEvent)
				e.Delete()
			}
			return true
		}
	}
	return false
}

// TestLinksOpen checks that the links of an HTML message open in the web
// browser when clicked, and mail links a new message: those written as
// links, with spaces around their address or without a scheme, and the
// addresses only written in the text.
func TestLinksOpen(t *testing.T) {
	var opened []string
	open := openURL
	openURL = func(link string) { opened = append(opened, link) }
	t.Cleanup(func() { openURL = open })

	m, err := message.Parse([]byte("From: a@example.com\r\nSubject: s\r\nContent-Type: text/html; charset=utf-8\r\n\r\n" +
		`<p><a href="https://example.com/a?x=1&amp;y=2">a link</a></p>` +
		"<p><a href=\"  https://example.com/b\n\">spaced</a></p>" +
		`<p><a href="www.example.com/c">no scheme</a></p>` +
		`<p>Written https://example.com/d in the text</p>` +
		`<p>Write to someone@example.com today</p>`))
	if err != nil {
		t.Fatal(err)
	}
	mainthread.Wait(func() {
		v := newMessageView()
		defer v.DeleteLater()
		var wrote []string
		v.compose = func(to, _ string) { wrote = append(wrote, to) }
		v.Resize(800, 600)
		v.Show()
		v.show(m, "INBOX", 1)
		qt.QCoreApplication_ProcessEvents()
		for _, href := range []string{"https://example.com/a?x=1&y=2", "https://example.com/b", "http://www.example.com/c", "https://example.com/d", "mailto:someone@example.com"} {
			if !clickLink(v, href) {
				t.Errorf("no link to %q is shown", href)
			}
		}
		want := []string{"https://example.com/a?x=1&y=2", "https://example.com/b", "http://www.example.com/c", "https://example.com/d"}
		if !slices.Equal(opened, want) {
			t.Errorf("opened %q, want %q", opened, want)
		}
		if !slices.Equal(wrote, []string{"someone@example.com"}) {
			t.Errorf("wrote to %q", wrote)
		}
	})
}
