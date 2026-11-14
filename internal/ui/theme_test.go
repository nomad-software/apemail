package ui

import (
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/nomad-software/qute"
	"github.com/nomad-software/qute/qtcore"
	"github.com/nomad-software/qute/qtgui"
	"github.com/nomad-software/qute/qtwidgets"
	"github.com/nomad-software/qute/qutetest"

	"github.com/nomad-software/apemail/internal/account"
	"github.com/nomad-software/apemail/internal/engine"
	"github.com/nomad-software/apemail/internal/icons"
	"github.com/nomad-software/apemail/internal/remote/remotetest"
	"github.com/nomad-software/apemail/internal/settings"
	"github.com/nomad-software/apemail/internal/store"
)

// windowColour returns the colour of windows in the palette of the
// application.
func windowColour() string {
	palette := qtgui.QGuiApplicationPalette()
	return palette.GroupColor(qtgui.QPaletteActive, qtgui.QPaletteWindow).Name()
}

// windowTextColour returns the colour of text in the palette of the
// application.
func windowTextColour() string {
	palette := qtgui.QGuiApplicationPalette()
	return palette.GroupColor(qtgui.QPaletteActive, qtgui.QPaletteWindowText).Name()
}

// drawnIn returns the colour most of the pixels of the icon that are drawn
// in full are, at 32 pixels.
func drawnIn(ic *qtgui.QIcon) string {
	return drawnInMode(ic, qtgui.QIconNormal)
}

// drawnInMode returns the colour drawnIn does, of the icon in the mode.
func drawnInMode(ic *qtgui.QIcon, mode qtgui.QIconMode) string {
	size := qtcore.QSize{Width: 32, Height: 32}
	pixmap := ic.PixmapFull(size, mode, qtgui.QIconOff)
	img := pixmap.ToImage()
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
	return colourOf(best).Name()
}

// washed returns how many pixels of the icon, at 32 pixels, are a pale
// wash of the colour: as opaque as the fill of a tinted icon, and of the
// colour but for what is lost to rounding at so little opacity.
func washed(ic *qtgui.QIcon, colour string) int {
	want := parseColour(colour)
	near := func(c uint) bool {
		for i, w := range []int{int(want.R), int(want.G), int(want.B)} {
			if d := int(c>>(16-8*i)&0xff) - w; d < -8 || d > 8 {
				return false
			}
		}
		return true
	}
	pixmap := ic.Pixmap(qtcore.QSize{Width: 32, Height: 32})
	img := pixmap.ToImage().ConvertToFormat(qtgui.QImageFormat_ARGB32)
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

// themeBase returns the name of the style the application's is: that of the
// style a proxy style is over, or its own.
func themeBase() string {
	style := qtwidgets.QApplicationStyle()
	if style.Inherits("QProxyStyle") {
		return qtwidgets.QProxyStyleCast(style).BaseStyle().Name()
	}
	return style.Name()
}

// boxEdge returns how much the edge of the box of a check box that is not
// checked stands out from the box: the most that the lightness of a pixel
// of the box, edge and all, differs from that of the box within it.
func boxEdge() int {
	box := qtwidgets.NewQCheckBoxWithText("Ticked", nil)
	defer box.Delete()
	box.AdjustSize()
	pixmap := box.Grab()
	img := pixmap.ToImage()
	palette := qtgui.QGuiApplicationPalette()
	base := palette.GroupColor(qtgui.QPaletteActive, qtgui.QPaletteBase).Lightness()
	// The box is at the left, before the space and the text.
	width := box.Style().PixelMetricFull(qtwidgets.QStylePM_IndicatorWidth, nil, box)
	most := 0
	for y := range img.Height() {
		for x := range width {
			most = max(most, abs(colourOf(img.Pixel(x, y)).Lightness()-base))
		}
	}
	return most
}

// abs returns the size of n.
func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// TestCheckBoxEdges checks that the boxes of check boxes have an edge that
// shows in the light and dark themes, which Fusion's own barely does.
func TestCheckBoxEdges(t *testing.T) {
	for _, theme := range []string{settings.ThemeLight, settings.ThemeDark} {
		useTheme(t, theme)
		qutetest.Run(t, func() {
			if got := boxEdge(); got < 80 {
				t.Errorf("%s: the edge of a check box stands out from it by %d of 255", theme, got)
			}
		})
	}
}

// useTheme puts the theme into effect until the test ends, when the
// system's is put back.
func useTheme(t *testing.T, name string) {
	t.Helper()
	qutetest.Run(t, func() { applyTheme(name) })
	t.Cleanup(func() { qutetest.Run(t, func() { applyTheme("") }) })
}

// TestThemes checks the light and dark themes, that the system's is put
// back as it was, and that the icons are drawn in the colours of the theme
// in effect, without being made again.
func TestThemes(t *testing.T) {
	var system string
	qutetest.Run(t, func() { system = windowColour() })
	var ic *qtgui.QIcon
	qutetest.Run(t, func() { ic = icon(icons.Trash) })

	useTheme(t, settings.ThemeDark)
	qutetest.Run(t, func() {
		if got := themeBase(); got != "fusion" {
			t.Errorf("style = %q", got)
		}
		if got := windowColour(); got != "#2d2d2d" {
			t.Errorf("dark window = %s", got)
		}
		if got := drawnIn(ic); got != "#e6e6e6" {
			t.Errorf("an icon in the dark theme is drawn in %s", got)
		}
	})

	qutetest.Run(t, func() { applyTheme(settings.ThemeLight) })
	qutetest.Run(t, func() {
		// The light theme's own colours, not Fusion's standard
		// palette, which is dark where the desktop's colour scheme is.
		if got := windowColour(); got != "#efefef" {
			t.Errorf("light window = %s", got)
		}
		if got := drawnIn(ic); got != "#1a1a1a" {
			t.Errorf("an icon in the light theme is drawn in %s", got)
		}
	})

	qutetest.Run(t, func() { applyTheme("") })
	qutetest.Run(t, func() {
		if got := qtwidgets.QApplicationStyle().Name(); got != systemStyle {
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
	qutetest.Run(t, func() {
		for mode, want := range map[qtgui.QIconMode]string{
			qtgui.QIconNormal:   "#e6e6e6",
			qtgui.QIconDisabled: "#7a7a7a",
			qtgui.QIconSelected: "#ffffff",
		} {
			pixmap := icon(icons.Inbox).PixmapFull(qtcore.QSize{Width: 32, Height: 32}, mode, qtgui.QIconOff)
			img := pixmap.ToImage()
			found := false
			for y := range img.Height() {
				for x := range img.Width() {
					if c := img.Pixel(x, y); c>>24 == 0xff && colourOf(c&0xffffff).Name() == want {
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

// TestThemeSetting checks that the theme chosen in the settings is put into
// effect at once, in the compose windows open too, kept, and put into effect
// when the program starts.
func TestThemeSetting(t *testing.T) {
	t.Cleanup(func() { qutetest.Run(t, func() { applyTheme("") }) })
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	w := newTestWindow(t, s, func(a *account.Account) { a.ComposeHTML = true })
	wait(t, "the account", func() bool { return len(w.engines) == 1 })
	c := openComposer(t, w)
	defer closeUnchanged(c)

	qutetest.Run(t, func() {
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
		qtcore.QCoreApplicationProcessEvents()
		if got := c.body.page.BackgroundColor(); got.Name() != "#1e1e1e" {
			t.Errorf("compose page %s", got.Name())
		}
		if saved, _ := settings.Load(w.files.Settings); saved.Theme != settings.ThemeDark {
			t.Errorf("saved %q", saved.Theme)
		}
		if f := newThemeField(w.config.Theme); currentData(f) != settings.ThemeDark {
			t.Errorf("field = %q", currentData(f))
		}
	})

	// A window started with the setting is dark from the start.
	qutetest.Run(t, func() { applyTheme("") })
	qutetest.Run(t, func() {
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
	roles := []qtgui.QPaletteColorRole{
		qtgui.QPaletteWindow, qtgui.QPaletteWindowText, qtgui.QPaletteBase, qtgui.QPaletteAlternateBase,
		qtgui.QPaletteText, qtgui.QPalettePlaceholderText, qtgui.QPaletteButton, qtgui.QPaletteButtonText,
		qtgui.QPaletteBrightText, qtgui.QPaletteToolTipBase, qtgui.QPaletteToolTipText, qtgui.QPaletteHighlight,
		qtgui.QPaletteHighlightedText, qtgui.QPaletteLink, qtgui.QPaletteLinkVisited,
		qtgui.QPaletteLight, qtgui.QPaletteMidlight, qtgui.QPaletteMid, qtgui.QPaletteDark, qtgui.QPaletteShadow,
	}
	lightness := func(colour string) int { return parseColour(colour).Lightness() }
	for name, cs := range map[string]colours{"light": lightColours, "dark": darkColours} {
		for _, role := range roles {
			if _, ok := cs.roles[role]; !ok {
				t.Errorf("%s does not set role %d", name, role)
			}
		}
		// The page and the window are light in the light theme and dark
		// in the dark, with text that stands out on them.
		qutetest.Run(t, func() {
			for _, pair := range [][2]qtgui.QPaletteColorRole{
				{qtgui.QPaletteWindow, qtgui.QPaletteWindowText},
				{qtgui.QPaletteBase, qtgui.QPaletteText},
				{qtgui.QPaletteButton, qtgui.QPaletteButtonText},
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
		qutetest.Run(t, func() {
			for name, colour := range want {
				if got := drawnIn(folderIcon(store.Folder{Name: name}, folders)); got != colour {
					t.Errorf("%s: %s is drawn in %s, not %s", theme, name, got, colour)
				}
			}
			if got := drawnIn(sidebarIcon(icons.Account)); got != want["INBOX"] {
				t.Errorf("%s: the account is drawn in %s", theme, got)
			}
			if got := drawnInMode(sidebarIcon(icons.Inbox), qtgui.QIconSelected); got != "#ffffff" {
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

// selectionDrawn returns how many pixels of the rectangle of the view are
// in the colour of the selection, and how many in that of selected text.
// Fusion shades the selection of a tree a little from top to bottom, so a
// colour near either is taken for it.
func selectionDrawn(view qtwidgets.QAbstractItemViewLike, r qtcore.QRect) (fill, text int) {
	v := view.AsQAbstractItemView()
	img := v.Viewport().Grab().ToImage()
	palette := qtgui.QGuiApplicationPalette()
	highlight := palette.GroupColor(qtgui.QPaletteActive, qtgui.QPaletteHighlight)
	highlighted := palette.GroupColor(qtgui.QPaletteActive, qtgui.QPaletteHighlightedText)
	for y := r.Y; y < r.Y+r.Height; y++ {
		for x := r.X; x < r.X+r.Width; x++ {
			switch c := colourOf(img.Pixel(x, y)); {
			case near(c, highlight):
				fill++
			case near(c, highlighted):
				text++
			}
		}
	}
	return fill, text
}

// near reports whether two colours differ by no more than a little in each
// of red, green and blue.
func near(a, b qtgui.QColor) bool {
	const little = 24
	return abs(int(a.R)-int(b.R)) <= little && abs(int(a.G)-int(b.G)) <= little && abs(int(a.B)-int(b.B)) <= little
}

// TestSelectionInThemes checks that in the light and dark themes the folder
// and the message selected are drawn filled with the colour of the
// selection, with their text on it, as Fusion draws them. The style of the
// themes draws Fusion's items through its own, which once handed Fusion
// what describes an item without the item, and drew each selected row
// blank.
func TestSelectionInThemes(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove, imap.CapIdle), "Archive", "Drafts", "Sent", "Trash")
	s.Append(t, "INBOX", "Analytical engine", imap.FlagSeen)
	s.Append(t, "INBOX", "Bernoulli numbers", imap.FlagSeen)
	for _, theme := range []string{settings.ThemeLight, settings.ThemeDark} {
		t.Run(theme, func(t *testing.T) {
			useTheme(t, theme)
			w := newTestWindow(t, s)
			qute.PostSync(w.Show)
			wait(t, "the inbox", func() bool { return w.folder == "INBOX" && len(w.list.rows) == 2 })
			qutetest.Run(t, func() {
				clickRow(w.list, 0)
				views := map[string]struct {
					view qtwidgets.QAbstractItemViewLike
					rect qtcore.QRect
				}{
					"the folder":  {w.folders, w.folders.VisualItemRect(w.folders.CurrentItem())},
					"the message": {w.list, w.list.VisualRect(w.list.model.Index(0, 1))},
				}
				for what, v := range views {
					fill, text := selectionDrawn(v.view, v.rect)
					if area := v.rect.Width * v.rect.Height; fill < area/2 || text == 0 {
						t.Errorf("%s selected: %d of %d pixels in the colour of the selection, %d in that of its text", what, fill, area, text)
					}
				}
			})
		})
	}
}
