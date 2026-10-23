package ui

import (
	"os"
	"runtime"
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
	// The settings are in tabs, as triggerapi's are: what ApeMail keeps
	// and where, and how it looks.
	tabs := qt.NewQTabWidget2()
	layout.AddWidget(tabs.QWidget)
	page := func(title string) *qt.QVBoxLayout {
		w := qt.NewQWidget2()
		tabs.AddTab(w, title)
		return qt.NewQVBoxLayout(w)
	}
	// Each heading is a group of its own, with a note on what it is for.
	group := func(page *qt.QVBoxLayout, title, note string) *qt.QFormLayout {
		box := qt.NewQGroupBox3(title)
		form := qt.NewQFormLayout(box.QWidget)
		label := qt.NewQLabel3(note)
		label.SetWordWrap(true)
		form.AddRowWithWidget(label.QWidget)
		page.AddWidget(box.QWidget)
		return form
	}

	general := page("General")
	encryption := group(general, "Encryption", "Your accounts, their passwords and the copy of your mail kept on this computer are encrypted "+
		"with a pair of SSH keys (ed25519 or RSA), using age. Without the private key none of it can be read.")
	encryption.AddRow4("Public key:", browse(publicField, "Public Key", home+"/.ssh", false).QLayout)
	encryption.AddRow4("Private key:", browse(privateField, "Private Key", home+"/.ssh", false).QLayout)

	accounts := group(general, "Accounts", "Changing the location moves the accounts, and the copy of their mail, to it; "+
		"it must be empty or not exist yet. The settings themselves are kept in "+settingsPathHint()+".")
	accounts.AddRow4("Location:", browse(dataField, "Location of Accounts and Mail", home, true).QLayout)
	general.AddStretch()

	look := page("User interface")
	fonts := group(look, "Fonts", "The code font is the one the source of a message is shown in. "+
		"Any font can be chosen; a monospaced one keeps the source in columns.")
	code := newFontField(current)
	fonts.AddRow4("Code font:", code.QHBoxLayout.QLayout)
	themeGroup := group(look, "Theme", "Follow the system looks as the desktop's theme does, and changes with it. "+
		"HTML messages are always shown on a white page, as they are written to be seen.")
	theme := newThemeField(current.Theme)
	themeGroup.AddRow3("Theme:", theme.QWidget)
	look.AddStretch()

	buttons := qt.NewQDialogButtonBox5(qt.QDialogButtonBox__Ok|qt.QDialogButtonBox__Cancel, qt.Horizontal)
	layout.AddWidget(buttons.QWidget)
	buttons.OnRejected(d.Reject)
	buttons.OnAccepted(func() {
		changed := current
		changed.PublicKey = strings.TrimSpace(publicField.Text())
		changed.PrivateKey = strings.TrimSpace(privateField.Text())
		changed.DataDir = strings.TrimSpace(dataField.Text())
		changed.CodeFont, changed.CodeFontSize = code.chosen()
		changed.Theme = currentData(theme)
		if changed.PublicKey == "" || changed.PrivateKey == "" {
			warning(d.QWidget, "Settings", "Both keys are needed.")
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

// fontField chooses the code font: its family, of any there are, and its
// size. What is not changed is kept as the settings have
// it, so that a family or size they leave to the system stays so, as the
// system's font need not be one listed. Default goes back to the system's.
type fontField struct {
	*qt.QHBoxLayout
	familyBox *qt.QFontComboBox
	sizeBox   *qt.QSpinBox
	reset     *qt.QPushButton
	// family and size are what is chosen, empty for the system's.
	family string
	size   int
}

// newFontField starts with the font the settings choose.
func newFontField(config settings.Settings) *fontField {
	f := &fontField{QHBoxLayout: qt.NewQHBoxLayout2(), familyBox: qt.NewQFontComboBox2(), sizeBox: qt.NewQSpinBox2()}
	f.sizeBox.SetRange(6, 72)
	f.sizeBox.SetSuffix(" pt")
	f.reset = qt.NewQPushButton3("Default")
	f.reset.SetToolTip("The system's monospaced font")
	shown := codeFont(config)
	f.show(shown)
	runtime.KeepAlive(shown)
	f.family, f.size = config.CodeFont, config.CodeFontSize
	// Only what the user changes is chosen: these are connected once the
	// font is shown.
	f.familyBox.OnCurrentFontChanged(func(font *qt.QFont) { f.family = font.Family() })
	f.sizeBox.OnValueChanged(func(size int) { f.size = size })
	f.reset.OnClicked(func() {
		system := codeFont(settings.Settings{})
		f.show(system)
		runtime.KeepAlive(system)
		f.family, f.size = "", 0
	})
	f.AddWidget2(f.familyBox.QWidget, 1)
	f.AddWidget(f.sizeBox.QWidget)
	f.AddWidget(f.reset.QWidget)
	return f
}

// show puts the font in the boxes.
func (f *fontField) show(font *qt.QFont) {
	f.familyBox.SetCurrentFont(font)
	size := font.PointSize()
	if size <= 0 {
		// A font sized in pixels has no size in points.
		app := qt.QApplication_Font()
		size = app.PointSize()
		runtime.KeepAlive(app)
	}
	f.sizeBox.SetValue(size)
}

// chosen returns the family and size chosen, each empty for the system's.
func (f *fontField) chosen() (family string, size int) {
	return f.family, f.size
}

// newThemeField chooses the theme, starting with the one named.
func newThemeField(current string) *qt.QComboBox {
	box := qt.NewQComboBox2()
	for _, t := range themes {
		box.AddItem3(t.label, qt.NewQVariant11(t.name))
		if t.name == current {
			box.SetCurrentIndex(box.Count() - 1)
		}
	}
	return box
}
