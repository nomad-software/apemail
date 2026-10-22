package ui

import (
	"os"
	"strings"

	qt "github.com/mappu/miqt/qt6"

	"github.com/nomad-software/apemail/internal/settings"
)

// settingsDialogWidth is the narrowest the settings dialog is, so that its
// paths and notes have room.
const settingsDialogWidth = 620

// newSettingsDialog shows the settings, and calls accepted with them if
// they are. defaultDir is where the accounts are kept unless the settings
// name another folder.
func newSettingsDialog(parent *qt.QWidget, current settings.Settings, defaultDir string, accepted func(settings.Settings)) *qt.QDialog {
	d := qt.NewQDialog(parent)
	d.SetWindowTitle("Settings")
	layout := qt.NewQVBoxLayout(d.QWidget)

	home, _ := os.UserHomeDir()
	public, private := current.PublicKey, current.PrivateKey
	if public == "" && private == "" {
		// The usual keys are suggested if there are some.
		for _, name := range []string{"id_ed25519", "id_rsa"} {
			p := home + "/.ssh/" + name
			if _, err := os.Stat(p + ".pub"); err == nil {
				public, private = "~/.ssh/"+name+".pub", "~/.ssh/"+name
				break
			}
		}
	}
	publicField := qt.NewQLineEdit3(public)
	privateField := qt.NewQLineEdit3(private)
	dataField := qt.NewQLineEdit3(current.DataDir)
	dataField.SetPlaceholderText(defaultDir)
	dataField.SetToolTip("Empty for the default place: " + defaultDir)
	browse := func(field *qt.QLineEdit, title, start string, folder bool) *qt.QHBoxLayout {
		row := qt.NewQHBoxLayout2()
		row.AddWidget(field.QWidget)
		b := qt.NewQPushButton3("Browse…")
		b.OnClicked(func() {
			var path string
			if folder {
				path = qt.QFileDialog_GetExistingDirectory3(d.QWidget, title, start)
			} else {
				path = qt.QFileDialog_GetOpenFileName3(d.QWidget, title, start)
			}
			if path != "" {
				field.SetText(path)
			}
		})
		row.AddWidget(b.QWidget)
		return row
	}
	// Each heading is a group of its own, with a note on what it is for.
	group := func(title, note string) *qt.QFormLayout {
		box := qt.NewQGroupBox3(title)
		form := qt.NewQFormLayout(box.QWidget)
		label := qt.NewQLabel3(note)
		label.SetWordWrap(true)
		form.AddRowWithWidget(label.QWidget)
		layout.AddWidget(box.QWidget)
		return form
	}

	encryption := group("Encryption", "Your accounts, their passwords and the copy of your mail kept on this computer are encrypted "+
		"with a pair of SSH keys (ed25519 or RSA), using age. Without the private key none of it can be read.")
	encryption.AddRow4("Public key:", browse(publicField, "Public Key", home+"/.ssh", false).QLayout)
	encryption.AddRow4("Private key:", browse(privateField, "Private Key", home+"/.ssh", false).QLayout)

	accounts := group("Accounts", "Changing the location moves the accounts, and the copy of their mail, to it; "+
		"it must be empty or not exist yet. The settings themselves are kept in "+settingsPathHint()+".")
	accounts.AddRow4("Location:", browse(dataField, "Location of Accounts and Mail", home, true).QLayout)
	layout.AddStretch()

	buttons := qt.NewQDialogButtonBox5(qt.QDialogButtonBox__Ok|qt.QDialogButtonBox__Cancel, qt.Horizontal)
	layout.AddWidget(buttons.QWidget)
	buttons.OnRejected(d.Reject)
	buttons.OnAccepted(func() {
		changed := current
		changed.PublicKey = strings.TrimSpace(publicField.Text())
		changed.PrivateKey = strings.TrimSpace(privateField.Text())
		changed.DataDir = strings.TrimSpace(dataField.Text())
		if changed.PublicKey == "" || changed.PrivateKey == "" {
			qt.QMessageBox_Warning(d.QWidget, "Settings", "Both keys are needed.")
			return
		}
		d.Accept()
		accepted(changed)
	})
	fitDialog(d, settingsDialogWidth)
	openDialog(d)
	return d
}

// settingsPathHint says where the settings file is kept.
func settingsPathHint() string {
	if path, err := settings.Path(); err == nil {
		return path
	}
	return "the current folder"
}
