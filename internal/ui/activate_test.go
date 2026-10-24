package ui

import (
	"os"
	"testing"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/mainthread"
)

// TestActivate checks that the window is brought back when the program is
// started again, from being minimised or hidden, keeping it maximised if it
// was, with the desktop's token in the environment for Qt.
func TestActivate(t *testing.T) {
	t.Setenv("XDG_ACTIVATION_TOKEN", "")
	mainthread.Wait(func() {
		w := NewWindow(Files{Settings: t.TempDir() + "/settings.json", Accounts: t.TempDir()})
		defer w.DeleteLater()

		w.SetWindowState(qt.WindowMaximized | qt.WindowMinimized)
		w.Show()
		w.Activate("token-1")
		if w.IsMinimized() || !w.IsMaximized() || !w.IsVisible() {
			t.Errorf("minimised: state = %v, visible %v", w.WindowState(), w.IsVisible())
		}
		if got := os.Getenv("XDG_ACTIVATION_TOKEN"); got != "token-1" {
			t.Errorf("token = %q", got)
		}

		w.Hide()
		w.Activate("")
		if !w.IsVisible() {
			t.Error("hidden: not shown")
		}
	})
}
