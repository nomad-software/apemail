package ui

import (
	"testing"

	"github.com/nomad-software/qute"
	"github.com/nomad-software/qute/qtwidgets"
	"github.com/nomad-software/qute/qutetest"
)

// answerDialog has f answer the dialog that is next opened and waited for,
// as the dialogs that ask for files and passwords are: it is called once the
// dialog is shown, with it, and is to close it.
func answerDialog(f func(d *qtwidgets.QWidget)) {
	qute.Post(func() { f(qtwidgets.QApplicationActiveModalWidget()) })
}

// TestAskHidden checks that a passphrase is asked for in a field that does
// not show it, and that it is an error to give none.
func TestAskHidden(t *testing.T) {
	qutetest.Run(t, func() {
		parent := qtwidgets.NewQWidget(nil)
		defer parent.DeleteLater()

		answerDialog(func(w *qtwidgets.QWidget) {
			d := qtwidgets.QInputDialogCast(w)
			if d == nil {
				t.Error("no dialog is open")
				w.Close()
				return
			}
			if d.WindowTitle() != "Passphrase" || d.LabelText() != "For the key:" || d.TextEchoMode() != qtwidgets.QLineEditPassword {
				t.Errorf("%q, %q, echo mode %v", d.WindowTitle(), d.LabelText(), d.TextEchoMode())
			}
			d.SetTextValue("secret")
			d.Accept()
		})
		if got, err := askHidden(parent, "Passphrase", "For the key:"); got != "secret" || err != nil {
			t.Errorf("given %q, %v", got, err)
		}

		answerDialog(func(w *qtwidgets.QWidget) { qtwidgets.QInputDialogCast(w).Reject() })
		if got, err := askHidden(parent, "Passphrase", "For the key:"); got != "" || err == nil {
			t.Errorf("cancelled: %q, %v", got, err)
		}
	})
}
