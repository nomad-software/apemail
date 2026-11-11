package ui

import (
	"os"
	"strings"

	"github.com/nomad-software/qute/qtcore"
	"github.com/nomad-software/qute/qtgui"
	"github.com/nomad-software/qute/qtwidgets"

	"github.com/nomad-software/apemail/internal/settings"
)

// settingsDialogWidth is the narrowest the settings dialog is, so that its
// paths and notes have room.
const settingsDialogWidth = 620

// newSettingsDialog shows the settings, and calls accepted with them if
// they are. defaultDir is where the accounts are kept unless the settings
// name another folder.
func newSettingsDialog(parent qtwidgets.QWidgetLike, current settings.Settings, defaultDir string, accepted func(settings.Settings)) *qtwidgets.QDialog {
	d := qtwidgets.NewQDialog(parent)
	d.SetWindowTitle("Settings")
	layout := qtwidgets.NewQVBoxLayout(d)

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
	publicField := qtwidgets.NewQLineEditWithText(public, nil)
	privateField := qtwidgets.NewQLineEditWithText(private, nil)
	dataField := qtwidgets.NewQLineEditWithText(current.DataDir, nil)
	dataField.SetPlaceholderText(defaultDir)
	dataField.SetToolTip("Empty for the default place: " + defaultDir)
	browse := func(field *qtwidgets.QLineEdit, title, start string, folder bool) *qtwidgets.QHBoxLayout {
		row := qtwidgets.NewQHBoxLayout(nil)
		row.AddWidget(field)
		b := qtwidgets.NewQPushButtonWithText("Browse…", nil)
		b.OnClicked(func(bool) {
			var path string
			if folder {
				path = qtwidgets.QFileDialogGetExistingDirectoryFull(d, title, start, qtwidgets.QFileDialogShowDirsOnly)
			} else {
				path, _ = qtwidgets.QFileDialogGetOpenFileNameFull(d, title, start, "", "", 0)
			}
			if path != "" {
				field.SetText(path)
			}
		})
		row.AddWidget(b)
		return row
	}
	// The settings are in tabs, as triggerapi's are: what ApeMail keeps
	// and where, and how it looks.
	tabs := qtwidgets.NewQTabWidget(nil)
	layout.AddWidget(tabs)
	page := func(title string) *qtwidgets.QVBoxLayout {
		w := qtwidgets.NewQWidget(nil)
		tabs.AddTab(w, title)
		return qtwidgets.NewQVBoxLayout(w)
	}
	// Each heading is a group of its own, with a note on what it is for.
	group := func(page *qtwidgets.QVBoxLayout, title, note string) *qtwidgets.QFormLayout {
		box := qtwidgets.NewQGroupBoxWithTitle(title, nil)
		form := qtwidgets.NewQFormLayout(box)
		label := qtwidgets.NewQLabelWithText(note, nil)
		label.SetWordWrap(true)
		form.AddRowWidget(label)
		page.AddWidget(box)
		return form
	}

	general := page("General")
	encryption := group(general, "Encryption", "Your accounts, their passwords and the copy of your mail kept on this computer are encrypted "+
		"with a pair of SSH keys (ed25519 or RSA), using age. Without the private key none of it can be read.")
	encryption.AddRowTextLayout("Public key:", browse(publicField, "Public Key", home+"/.ssh", false))
	encryption.AddRowTextLayout("Private key:", browse(privateField, "Private Key", home+"/.ssh", false))

	accounts := group(general, "Accounts", "Changing the location moves the accounts, and the copy of their mail, to it; "+
		"it must be empty or not exist yet. The settings themselves are kept in "+settingsPathHint()+".")
	accounts.AddRowTextLayout("Location:", browse(dataField, "Location of Accounts and Mail", home, true))
	general.AddStretch()

	look := page("User interface")
	fonts := group(look, "Fonts", "The code font is the one the source of a message is shown in. "+
		"Any font can be chosen; a monospaced one keeps the source in columns.")
	code := newFontField(current)
	fonts.AddRowTextLayout("Code font:", code.QHBoxLayout)
	themeGroup := group(look, "Theme", "Follow the system looks as the desktop's theme does, and changes with it. "+
		"Messages are shown and written in the colours of the theme too, where they give none of their own.")
	theme := newThemeField(current.Theme)
	themeGroup.AddRowText("Theme:", theme)
	look.AddStretch()

	buttons := qtwidgets.NewQDialogButtonBoxWithButtonsOriented(qtwidgets.QDialogButtonBoxOk|qtwidgets.QDialogButtonBoxCancel, qtcore.Horizontal, nil)
	layout.AddWidget(buttons)
	buttons.OnRejected(d.Reject)
	buttons.OnAccepted(func() {
		changed := current
		changed.PublicKey = strings.TrimSpace(publicField.Text())
		changed.PrivateKey = strings.TrimSpace(privateField.Text())
		changed.DataDir = strings.TrimSpace(dataField.Text())
		changed.CodeFont, changed.CodeFontSize = code.chosen()
		changed.Theme = currentData(theme)
		if changed.PublicKey == "" || changed.PrivateKey == "" {
			warning(d, "Settings", "Both keys are needed.")
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
// size. What is not changed is kept as the settings have it, so that a
// family or size they leave to the system stays so, as the system's font
// need not be one listed. Default goes back to the system's.
type fontField struct {
	*qtwidgets.QHBoxLayout
	familyBox *qtwidgets.QFontComboBox
	sizeBox   *qtwidgets.QSpinBox
	reset     *qtwidgets.QPushButton
	// family and size are what is chosen, empty for the system's.
	family string
	size   int
}

// newFontField starts with the font the settings choose.
func newFontField(config settings.Settings) *fontField {
	f := &fontField{QHBoxLayout: qtwidgets.NewQHBoxLayout(nil), familyBox: qtwidgets.NewQFontComboBox(nil), sizeBox: qtwidgets.NewQSpinBox(nil)}
	f.sizeBox.SetRange(6, 72)
	f.sizeBox.SetSuffix(" pt")
	f.reset = qtwidgets.NewQPushButtonWithText("Default", nil)
	f.reset.SetToolTip("The system's monospaced font")
	shown := codeFont(config)
	f.show(shown)
	f.family, f.size = config.CodeFont, config.CodeFontSize
	// Only what the user changes is chosen: these are connected once the
	// font is shown.
	f.familyBox.OnCurrentFontChanged(func(font *qtgui.QFont) { f.family = font.Family() })
	f.sizeBox.OnValueChanged(func(size int) { f.size = size })
	f.reset.OnClicked(func(bool) {
		system := codeFont(settings.Settings{})
		f.show(system)
		f.family, f.size = "", 0
	})
	f.AddWidgetFull(f.familyBox, 1, 0)
	f.AddWidget(f.sizeBox)
	f.AddWidget(f.reset)
	return f
}

// show puts the font in the boxes.
func (f *fontField) show(font *qtgui.QFont) {
	f.familyBox.SetCurrentFont(font)
	size := font.PointSize()
	if size <= 0 {
		// A font sized in pixels has no size in points.
		app := qtwidgets.QApplicationFont()
		size = app.PointSize()
	}
	f.sizeBox.SetValue(size)
}

// chosen returns the family and size chosen, each empty for the system's.
func (f *fontField) chosen() (family string, size int) {
	return f.family, f.size
}

// newThemeField chooses the theme, starting with the one named.
func newThemeField(current string) *qtwidgets.QComboBox {
	box := qtwidgets.NewQComboBox(nil)
	for _, t := range themes {
		box.AddItemFull(t.label, qtcore.NewQVariantWithString(t.name))
		if t.name == current {
			box.SetCurrentIndex(box.Count() - 1)
		}
	}
	return box
}
