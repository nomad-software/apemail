package ui

import (
	"errors"
	"strings"
	"testing"

	"github.com/mappu/miqt/qt6/mainthread"
)

// TestWarningsAreTold checks that a warning in a test is kept, rather than
// shown in a dialog that would wait for ever, and told by a wait that fails.
func TestWarningsAreTold(t *testing.T) {
	forgetWarnings()
	defer forgetWarnings()
	mainthread.Wait(func() {
		w := NewWindow(Files{Settings: t.TempDir() + "/settings.json", Accounts: t.TempDir()})
		defer w.DeleteLater()
		w.warn("Delete", errors.New("the folder has gone"))
	})
	if got := warnings(); !strings.Contains(got, "Delete: the folder has gone") {
		t.Errorf("warnings = %q", got)
	}
}
