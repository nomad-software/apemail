package ui

import (
	"slices"
	"testing"

	"github.com/emersion/go-imap/v2"
	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/mainthread"

	"github.com/nomad-software/apemail/internal/remote/remotetest"
	"github.com/nomad-software/apemail/internal/settings"
)

// someFont returns a family the font box lists, other than the first, so
// that choosing it is a change. It is called on the UI's thread, which
// t.Fatal and t.Skip would end, so the test fails without one and goes on.
func someFont(t *testing.T, f *fontField) string {
	t.Helper()
	if f.familyBox.Count() < 2 {
		t.Error("fewer than two fonts are installed")
		return ""
	}
	if f.familyBox.CurrentIndex() == 1 {
		return f.familyBox.ItemText(0)
	}
	return f.familyBox.ItemText(1)
}

// TestFontField checks that the font field keeps what the settings leave to
// the system unless it is changed, chooses what is changed, and goes back
// to the system's with Default.
func TestFontField(t *testing.T) {
	mainthread.Wait(func() {
		f := newFontField(settings.Settings{})
		if family, size := f.chosen(); family != "" || size != 0 {
			t.Errorf("unchanged = %q, %d", family, size)
		}
		if f.sizeBox.Value() <= 0 {
			t.Errorf("size shown = %d", f.sizeBox.Value())
		}

		// Any font can be chosen, not only those that are monospaced.
		if f.familyBox.FontFilters() != qt.QFontComboBox__AllFonts {
			t.Errorf("filters = %v", f.familyBox.FontFilters())
		}
		proportional := ""
		for i := range f.familyBox.Count() {
			if name := f.familyBox.ItemText(i); !qt.QFontDatabase_IsFixedPitch(name) {
				proportional = name
				break
			}
		}
		if proportional == "" {
			t.Error("no font that is not monospaced is offered")
		} else {
			f.familyBox.SetCurrentFont(qt.NewQFont2(proportional))
			if got, _ := f.chosen(); got != proportional {
				t.Errorf("chose %q, not %q", got, proportional)
			}
		}

		family := someFont(t, f)
		f.familyBox.SetCurrentFont(qt.NewQFont2(family))
		f.sizeBox.SetValue(14)
		if got, size := f.chosen(); got != family || size != 14 {
			t.Errorf("chosen = %q, %d, want %q, 14", got, size, family)
		}

		// The font of the settings is shown, and kept if not changed.
		g := newFontField(settings.Settings{CodeFont: family, CodeFontSize: 14})
		if g.familyBox.CurrentFont().Family() != family || g.sizeBox.Value() != 14 {
			t.Errorf("shown %q, %d", g.familyBox.CurrentFont().Family(), g.sizeBox.Value())
		}
		if got, size := g.chosen(); got != family || size != 14 {
			t.Errorf("kept = %q, %d", got, size)
		}
		g.reset.Click()
		if got, size := g.chosen(); got != "" || size != 0 {
			t.Errorf("after Default = %q, %d", got, size)
		}
	})
}

// TestSourceFont checks that the source window is shown in the code font of
// the settings, which a change to them puts into effect at once, in
// a window that is open, and saves.
func TestSourceFont(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	w := newTestWindow(t, s)
	wait(t, "the account", func() bool { return len(w.engines) == 1 })

	mainthread.Wait(func() {
		src := w.showSource("A message", []byte("Subject: A message\r\n\r\nBody\r\n"))
		defer src.Close()
		system := qt.QFontDatabase_SystemFont(qt.QFontDatabase__FixedFont)
		if src.Font().Family() != system.Family() {
			t.Errorf("font = %q, want the system's %q", src.Font().Family(), system.Family())
		}

		family := someFont(t, newFontField(w.config))
		changed := w.config
		changed.CodeFont, changed.CodeFontSize = family, 15
		w.applyInterface(changed)
		if src.Font().Family() != family || src.Font().PointSize() != 15 {
			t.Errorf("open window = %q %d", src.Font().Family(), src.Font().PointSize())
		}
		later := w.showSource("Another", []byte("Subject: Another\r\n\r\nBody\r\n"))
		defer later.Close()
		if later.Font().Family() != family || later.Font().PointSize() != 15 {
			t.Errorf("new window = %q %d", later.Font().Family(), later.Font().PointSize())
		}
		if saved := settings.Load(w.files.Settings); saved.CodeFont != family || saved.CodeFontSize != 15 {
			t.Errorf("saved %q %d", saved.CodeFont, saved.CodeFontSize)
		}
	})
}

// TestSettingsTabs checks that the settings are in tabs, as triggerapi's
// are, with the code font on a tab of its own.
func TestSettingsTabs(t *testing.T) {
	mainthread.Wait(func() {
		parent := qt.NewQWidget2()
		defer parent.DeleteLater()
		d := newSettingsDialog(parent, settings.Settings{}, "/accounts", func(settings.Settings) {})
		defer d.Reject()
		var tabs *qt.QTabWidget
		for _, child := range d.Children() {
			if child.MetaObject().ClassName() == "QTabWidget" {
				tabs = qt.UnsafeNewQTabWidget(child.UnsafePointer())
			}
		}
		if tabs == nil {
			t.Fatal("the settings have no tabs")
		}
		var titles []string
		for i := range tabs.Count() {
			titles = append(titles, tabs.TabText(i))
		}
		if !slices.Equal(titles, []string{"General", "User interface"}) {
			t.Errorf("tabs = %q", titles)
		}
		// The code font and the theme are on the second, and nothing
		// else is.
		var fonts []string
		for _, child := range tabs.Widget(1).Children() {
			if child.MetaObject().ClassName() == "QGroupBox" {
				fonts = append(fonts, qt.UnsafeNewQGroupBox(child.UnsafePointer()).Title())
			}
		}
		if !slices.Equal(fonts, []string{"Fonts", "Theme"}) {
			t.Errorf("groups of User interface = %q", fonts)
		}
	})
}
