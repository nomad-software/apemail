package ui

import (
	"runtime"
	"testing"

	"github.com/emersion/go-imap/v2"
	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/mainthread"

	"github.com/nomad-software/apemail/internal/account"
	"github.com/nomad-software/apemail/internal/icons"
	"github.com/nomad-software/apemail/internal/message"
	"github.com/nomad-software/apemail/internal/remote/remotetest"
	"github.com/nomad-software/apemail/internal/settings"
)

// windowColour returns the colour of windows in the palette of the
// application.
func windowColour() string {
	palette := qt.QGuiApplication_Palette()
	defer runtime.KeepAlive(palette)
	return palette.Color(qt.QPalette__Active, qt.QPalette__Window).Name()
}

// drawnIn returns the colour most of the pixels of the icon that are drawn
// in full are, at 32 pixels.
func drawnIn(ic *qt.QIcon) string {
	pixmap := ic.Pixmap2(32, 32)
	img := pixmap.ToImage()
	runtime.KeepAlive(pixmap)
	count := map[uint]int{}
	for y := range img.Height() {
		for x := range img.Width() {
			if c := img.Pixel(x, y); c>>24 == 0xff {
				count[c&0xffffff]++
			}
		}
	}
	best, most := uint(0), 0
	for c, n := range count {
		if n > most {
			best, most = c, n
		}
	}
	return qt.NewQColor4(best).Name()
}

// useTheme puts the theme into effect until the test ends, when the
// system's is put back.
func useTheme(t *testing.T, name string) {
	t.Helper()
	mainthread.Wait(func() { applyTheme(name) })
	t.Cleanup(func() { mainthread.Wait(func() { applyTheme("") }) })
}

// TestThemes checks the light and dark themes, that the system's is put
// back as it was, and that the icons are drawn in the colours of the theme
// in effect, without being made again.
func TestThemes(t *testing.T) {
	var system string
	mainthread.Wait(func() { system = windowColour() })
	var ic *qt.QIcon
	mainthread.Wait(func() { ic = icon(icons.Trash) })

	useTheme(t, settings.ThemeDark)
	mainthread.Wait(func() {
		if got := qt.QApplication_Style().Name(); got != "fusion" {
			t.Errorf("style = %q", got)
		}
		if got := windowColour(); got != "#2d2d2d" {
			t.Errorf("dark window = %s", got)
		}
		if got := drawnIn(ic); got != "#e6e6e6" {
			t.Errorf("an icon in the dark theme is drawn in %s", got)
		}
	})

	mainthread.Wait(func() { applyTheme(settings.ThemeLight) })
	mainthread.Wait(func() {
		// The light theme's own colours, not Fusion's standard
		// palette, which is dark where the desktop's colour scheme is.
		if got := windowColour(); got != "#efefef" {
			t.Errorf("light window = %s", got)
		}
		if got := drawnIn(ic); got != "#1a1a1a" {
			t.Errorf("an icon in the light theme is drawn in %s", got)
		}
	})

	mainthread.Wait(func() { applyTheme("") })
	mainthread.Wait(func() {
		if got := qt.QApplication_Style().Name(); got != systemStyle {
			t.Errorf("style = %q, not the system's %q", got, systemStyle)
		}
		if got := windowColour(); got != system {
			t.Errorf("system window = %s, not %s as it was", got, system)
		}
	})
}

// TestIconModes checks that a disabled icon is drawn in the colour of
// disabled text, and a selected one in that of selected text.
func TestIconModes(t *testing.T) {
	useTheme(t, settings.ThemeDark)
	mainthread.Wait(func() {
		for mode, want := range map[qt.QIcon__Mode]string{
			qt.QIcon__Normal:   "#e6e6e6",
			qt.QIcon__Disabled: "#7a7a7a",
			qt.QIcon__Selected: "#ffffff",
		} {
			pixmap := icon(icons.Inbox).Pixmap5(qt.NewQSize2(32, 32), mode)
			img := pixmap.ToImage()
			runtime.KeepAlive(pixmap)
			found := false
			for y := range img.Height() {
				for x := range img.Width() {
					if c := img.Pixel(x, y); c>>24 == 0xff && qt.NewQColor4(c&0xffffff).Name() == want {
						found = true
					}
				}
			}
			if !found {
				t.Errorf("mode %d is not drawn in %s", mode, want)
			}
		}
	})
}

// TestHTMLOnPaper checks that in a dark theme an HTML message is shown on a
// white page, as it is written to be seen, and plain text in the theme.
func TestHTMLOnPaper(t *testing.T) {
	useTheme(t, settings.ThemeDark)
	base := func(v *messageView) string {
		return v.browser.Palette().Color(qt.QPalette__Active, qt.QPalette__Base).Name()
	}
	mainthread.Wait(func() {
		v := newMessageView()
		defer v.DeleteLater()
		v.show(&message.Message{HTML: "<p>Hello</p>"}, "INBOX", 1)
		if got := base(v); got != "#ffffff" {
			t.Errorf("HTML is shown on %s", got)
		}
		v.show(&message.Message{Text: "Hello"}, "INBOX", 2)
		if got := base(v); got != "#1e1e1e" {
			t.Errorf("text is shown on %s", got)
		}
	})
}

// TestThemeSetting checks that the theme chosen in the settings is put into
// effect at once, in the compose windows open too, kept, and put into effect
// when the program starts.
func TestThemeSetting(t *testing.T) {
	t.Cleanup(func() { mainthread.Wait(func() { applyTheme("") }) })
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	w := newTestWindow(t, s, func(a *account.Account) { a.ComposeHTML = true })
	wait(t, "the account", func() bool { return len(w.engines) == 1 })
	c := openComposer(t, w)
	defer mainthread.Wait(func() {
		c.body.Document().SetModifiedWithBool(false)
		c.Close()
	})

	mainthread.Wait(func() {
		// The settings offer the system's first, as it is.
		f := newThemeField(w.config.Theme)
		defer f.DeleteLater()
		if f.CurrentText() != "Follow the system" || f.Count() != 3 {
			t.Errorf("field = %q of %d", f.CurrentText(), f.Count())
		}

		changed := w.config
		changed.Theme = settings.ThemeDark
		w.applyInterface(changed)
		if got := windowColour(); got != "#2d2d2d" {
			t.Errorf("window = %s", got)
		}
		// The HTML written is on a white page, with the theme's scroll
		// bars.
		p := c.body.Palette()
		if p.Color(qt.QPalette__Active, qt.QPalette__Base).Name() != "#ffffff" || p.Color(qt.QPalette__Active, qt.QPalette__Window).Name() != "#2d2d2d" {
			t.Errorf("compose page %s, window %s", p.Color(qt.QPalette__Active, qt.QPalette__Base).Name(), p.Color(qt.QPalette__Active, qt.QPalette__Window).Name())
		}
		if saved := settings.Load(w.files.Settings); saved.Theme != settings.ThemeDark {
			t.Errorf("saved %q", saved.Theme)
		}
		if f := newThemeField(w.config.Theme); currentData(f) != settings.ThemeDark {
			t.Errorf("field = %q", currentData(f))
		}
	})

	// A window started with the setting is dark from the start.
	mainthread.Wait(func() { applyTheme("") })
	mainthread.Wait(func() {
		other := NewWindow(w.files)
		defer other.DeleteLater()
		if got := windowColour(); got != "#2d2d2d" {
			t.Errorf("a window started dark has a window of %s", got)
		}
	})
}

// TestThemesAreWhole checks that each theme sets every colour Fusion draws
// with, light ones in the light theme and dark in the dark, so that none is
// left to the desktop, whose colour scheme may be the other.
func TestThemesAreWhole(t *testing.T) {
	roles := []qt.QPalette__ColorRole{
		qt.QPalette__Window, qt.QPalette__WindowText, qt.QPalette__Base, qt.QPalette__AlternateBase,
		qt.QPalette__Text, qt.QPalette__PlaceholderText, qt.QPalette__Button, qt.QPalette__ButtonText,
		qt.QPalette__BrightText, qt.QPalette__ToolTipBase, qt.QPalette__ToolTipText, qt.QPalette__Highlight,
		qt.QPalette__HighlightedText, qt.QPalette__Link, qt.QPalette__LinkVisited,
		qt.QPalette__Light, qt.QPalette__Midlight, qt.QPalette__Mid, qt.QPalette__Dark, qt.QPalette__Shadow,
	}
	lightness := func(colour string) int {
		c := qt.NewQColor6(colour)
		defer c.Delete()
		return c.Lightness()
	}
	for name, cs := range map[string]colours{"light": lightColours, "dark": darkColours} {
		for _, role := range roles {
			if _, ok := cs.roles[role]; !ok {
				t.Errorf("%s does not set role %d", name, role)
			}
		}
		// The page and the window are light in the light theme and dark
		// in the dark, with text that stands out on them.
		mainthread.Wait(func() {
			for _, pair := range [][2]qt.QPalette__ColorRole{
				{qt.QPalette__Window, qt.QPalette__WindowText},
				{qt.QPalette__Base, qt.QPalette__Text},
				{qt.QPalette__Button, qt.QPalette__ButtonText},
			} {
				back, text := lightness(cs.roles[pair[0]]), lightness(cs.roles[pair[1]])
				if light := back > 128; light != (name == "light") {
					t.Errorf("%s: background %s is the wrong way", name, cs.roles[pair[0]])
				}
				if d := back - text; d < 128 && d > -128 {
					t.Errorf("%s: text %s does not stand out on %s", name, cs.roles[pair[1]], cs.roles[pair[0]])
				}
			}
		})
	}
}
