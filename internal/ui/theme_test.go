package ui

import (
	"runtime"
	"testing"

	"github.com/emersion/go-imap/v2"
	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/mainthread"

	"github.com/nomad-software/apemail/internal/account"
	"github.com/nomad-software/apemail/internal/engine"
	"github.com/nomad-software/apemail/internal/icons"
	"github.com/nomad-software/apemail/internal/message"
	"github.com/nomad-software/apemail/internal/remote/remotetest"
	"github.com/nomad-software/apemail/internal/settings"
	"github.com/nomad-software/apemail/internal/store"
)

// windowColour returns the colour of windows in the palette of the
// application.
func windowColour() string {
	palette := qt.QGuiApplication_Palette()
	defer runtime.KeepAlive(palette)
	return palette.Color(qt.QPalette__Active, qt.QPalette__Window).Name()
}

// windowTextColour returns the colour of text in the palette of the
// application.
func windowTextColour() string {
	palette := qt.QGuiApplication_Palette()
	defer runtime.KeepAlive(palette)
	return palette.Color(qt.QPalette__Active, qt.QPalette__WindowText).Name()
}

// drawnIn returns the colour most of the pixels of the icon that are drawn
// in full are, at 32 pixels.
func drawnIn(ic *qt.QIcon) string {
	return drawnInMode(ic, qt.QIcon__Normal)
}

// drawnInMode returns the colour drawnIn does, of the icon in the mode.
func drawnInMode(ic *qt.QIcon, mode qt.QIcon__Mode) string {
	size := qt.NewQSize2(32, 32)
	defer size.Delete()
	pixmap := ic.Pixmap5(size, mode)
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

// washed returns how many pixels of the icon, at 32 pixels, are a pale
// wash of the colour: as opaque as the fill of a tinted icon, and of the
// colour but for what is lost to rounding at so little opacity.
func washed(ic *qt.QIcon, colour string) int {
	want := qt.NewQColor6(colour)
	defer want.Delete()
	near := func(c uint) bool {
		for i, w := range []int{want.Red(), want.Green(), want.Blue()} {
			if d := int(c>>(16-8*i)&0xff) - w; d < -8 || d > 8 {
				return false
			}
		}
		return true
	}
	pixmap := ic.Pixmap2(32, 32)
	img := pixmap.ToImage().ConvertToFormat(qt.QImage__Format_ARGB32)
	runtime.KeepAlive(pixmap)
	n := 0
	for y := range img.Height() {
		for x := range img.Width() {
			c := img.Pixel(x, y)
			if alpha := c >> 24; alpha >= 45 && alpha <= 58 && near(c) {
				n++
			}
		}
	}
	return n
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

// TestMessagesInTheme checks that in a dark theme HTML messages are shown in
// the theme, as plain text is, rather than on a white page.
func TestMessagesInTheme(t *testing.T) {
	useTheme(t, settings.ThemeDark)
	base := func(v *messageView) string {
		return v.browser.Palette().Color(qt.QPalette__Active, qt.QPalette__Base).Name()
	}
	mainthread.Wait(func() {
		v := newMessageView()
		defer v.DeleteLater()
		v.show(&message.Message{HTML: "<p>Hello</p>"}, "INBOX", 1)
		if got := base(v); got != "#1e1e1e" {
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
		// The HTML written is in the theme too, once the change of
		// palette, which is posted to the windows open, is delivered.
		qt.QCoreApplication_ProcessEvents()
		p := c.body.Palette()
		if got := p.Color(qt.QPalette__Active, qt.QPalette__Base).Name(); got != "#1e1e1e" {
			t.Errorf("compose page %s", got)
		}
		runtime.KeepAlive(p)
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

// TestSidebarIcons checks that the icons of the side bar are drawn in their
// colours, those of a dark theme in the dark one, and a selected folder's
// in that of selected text, as its colour could not be seen on the
// selection.
func TestSidebarIcons(t *testing.T) {
	folders := map[string]engine.Special{"Sent": engine.Sent, "Drafts": engine.Drafts, "Trash": engine.Trash, "Archive": engine.Archive}
	for theme, want := range map[string]map[string]string{
		settings.ThemeLight: {"INBOX": "#1a73e8", "Drafts": "#8e44ad", "Sent": "#188038", "Archive": "#a0622d", "Work": "#c99400", "Trash": "#6b6b6b"},
		settings.ThemeDark:  {"INBOX": "#6ea8fe", "Drafts": "#c084fc", "Sent": "#5cc77d", "Archive": "#d9a066", "Work": "#f2c94c", "Trash": "#a8a8a8"},
	} {
		useTheme(t, theme)
		mainthread.Wait(func() {
			for name, colour := range want {
				if got := drawnIn(folderIcon(store.Folder{Name: name}, folders)); got != colour {
					t.Errorf("%s: %s is drawn in %s, not %s", theme, name, got, colour)
				}
			}
			if got := drawnIn(sidebarIcon(icons.Account)); got != want["INBOX"] {
				t.Errorf("%s: the account is drawn in %s", theme, got)
			}
			if got := drawnInMode(sidebarIcon(icons.Inbox), qt.QIcon__Selected); got != "#ffffff" {
				t.Errorf("%s: a selected folder is drawn in %s", theme, got)
			}
			// Inside the outline each is filled with a wash of its
			// colour, and an icon in the colour of the text is not: a
			// few pixels of the edge of its outline are as faint.
			for name, colour := range want {
				if n := washed(folderIcon(store.Folder{Name: name}, folders), colour); n < 50 {
					t.Errorf("%s: %s is filled in %d pixels", theme, name, n)
				}
			}
			if n := washed(icon(icons.Trash), windowTextColour()); n > 20 {
				t.Errorf("%s: an untinted icon is filled (%d pixels)", theme, n)
			}
		})
	}
}
