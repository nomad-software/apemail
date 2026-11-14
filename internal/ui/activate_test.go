package ui

import (
	"os"
	"strings"
	"testing"

	"github.com/nomad-software/qute/qtcore"
	"github.com/nomad-software/qute/qutetest"
)

// TestActivate checks that the window is brought back when the program is
// started again, from being minimised or hidden, keeping it maximised if it
// was, with the desktop's token in the environment for Qt.
func TestActivate(t *testing.T) {
	t.Setenv("XDG_ACTIVATION_TOKEN", "")
	qutetest.Run(t, func() {
		w := NewWindow(Files{Settings: t.TempDir() + "/settings.json", Accounts: t.TempDir()})
		defer w.DeleteLater()

		w.SetWindowState(qtcore.WindowMaximized | qtcore.WindowMinimized)
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

// TestSettingsUnreadable checks that settings that cannot be read, as with a
// mistake made editing them by hand, are kept rather than saved over when
// the window closes, as they name the keys, and that the window says so.
func TestSettingsUnreadable(t *testing.T) {
	path := t.TempDir() + "/settings.json"
	broken := `{"publicKey": "~/.ssh/id.pub",}`
	if err := os.WriteFile(path, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	qutetest.Run(t, func() {
		w := NewWindow(Files{Settings: path, Accounts: t.TempDir()})
		if w.configErr == nil || !strings.Contains(w.configErr.Error(), path+".bad") {
			t.Errorf("err = %v", w.configErr)
		}
		w.Close()
		w.DeleteLater()
	})
	if kept, err := os.ReadFile(path + ".bad"); err != nil || string(kept) != broken {
		t.Errorf("kept %q, %v", kept, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the settings were not saved anew: %v", err)
	}
}
