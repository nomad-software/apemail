package ui

import (
	"testing"

	"github.com/nomad-software/qute/qutetest"
)

func TestAppIcon(t *testing.T) {
	qutetest.Run(t, func() {
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
