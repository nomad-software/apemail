package ui

import (
	"testing"

	"github.com/mappu/miqt/qt6/mainthread"
)

func TestAppIcon(t *testing.T) {
	mainthread.Wait(func() {
		icon := appIcon()
		if icon.IsNull() {
			t.Error("the program has no icon")
		}
		// It is loaded once.
		if appIcon() != icon {
			t.Error("the icon is loaded again")
		}
	})
}
