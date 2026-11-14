package ui

import (
	"testing"

	"github.com/nomad-software/qute"
	"github.com/nomad-software/qute/qtcore"
	"github.com/nomad-software/qute/qtwidgets"
	"github.com/nomad-software/qute/qutetest"

	"github.com/nomad-software/apemail/internal/account"
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

// TestServerFieldsSetPort checks that the port given with the servers, as
// Look Up Settings finds, is the one filled in, though it is a usual port
// for other security than the fields had, which a change of the security
// alone would give the port usual for it.
func TestServerFieldsSetPort(t *testing.T) {
	qutetest.Run(t, func() {
		ports := map[account.Security]int{account.SecurityNone: 25, account.SecurityStartTLS: 587, account.SecurityTLS: 465}
		page, _, f := newServerFields(account.Server{Host: "smtp.example.com", Port: 465, Security: account.SecurityTLS, Auth: account.AuthPassword}, ports, nil)
		defer page.AsQWidget().DeleteLater()
		f.set(account.Server{Host: "smtp.example.com", Port: 25, Security: account.SecurityStartTLS, Auth: account.AuthPassword})
		if s := f.server(); s.Port != 25 || s.Security != account.SecurityStartTLS {
			t.Errorf("server = %+v", s)
		}
		// Changing the security by hand still changes a usual port.
		f.security.SetCurrentIndex(f.security.FindData(qtcore.NewQVariantWithString(string(account.SecurityTLS))))
		if s := f.server(); s.Port != 465 {
			t.Errorf("port = %d", s.Port)
		}
	})
}
