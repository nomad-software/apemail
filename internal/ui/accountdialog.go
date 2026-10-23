package ui

import (
	"context"
	"errors"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"slices"
	"strings"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/mainthread"

	"github.com/nomad-software/apemail/internal/account"
	"github.com/nomad-software/apemail/internal/autoconfig"
	"github.com/nomad-software/apemail/internal/homedir"
	"github.com/nomad-software/apemail/internal/message"
	"github.com/nomad-software/apemail/internal/remote"
	"github.com/nomad-software/apemail/internal/send"
	"github.com/nomad-software/apemail/internal/store"
)

// serverFields are the fields of the settings of a server.
type serverFields struct {
	host     *qt.QLineEdit
	port     *qt.QSpinBox
	username *qt.QLineEdit
	password *qt.QLineEdit
	security *qt.QComboBox
	auth     *qt.QComboBox
	cert     *qt.QLineEdit
	key      *qt.QLineEdit
	result   *qt.QLabel
	// ports are the usual ports of each kind of security.
	ports map[account.Security]int
}

// signatureFilter is the filter of the file dialog that picks a file to sign
// messages with.
const signatureFilter = "Signatures (*.txt *.html *.htm);;All files (*)"

// checkSignatureFile reports what is wrong with the file of a signature, if
// anything is: it must be named, and be one a signature can be read from.
func checkSignatureFile(path string) error {
	if path == "" {
		return errors.New("choose the file of the signature, or write the signature instead")
	}
	_, err := message.LoadSignature("", path)
	return err
}

// accountDialogWidth is the narrowest the account dialog is, so that its
// five tabs and the fields of the servers have room.
const accountDialogWidth = 760

// newServerFields makes the fields of a server, in a page with a button that
// tests them with test.
func newServerFields(s account.Server, ports map[account.Security]int, test func(account.Server) error) (*qt.QWidget, *qt.QFormLayout, *serverFields) {
	page := qt.NewQWidget2()
	form := qt.NewQFormLayout(page)
	f := &serverFields{
		host:     qt.NewQLineEdit2(),
		port:     qt.NewQSpinBox2(),
		username: qt.NewQLineEdit2(),
		password: qt.NewQLineEdit2(),
		security: qt.NewQComboBox2(),
		auth:     qt.NewQComboBox2(),
		cert:     qt.NewQLineEdit2(),
		key:      qt.NewQLineEdit2(),
		result:   qt.NewQLabel2(),
		ports:    ports,
	}
	f.port.SetRange(1, 65535)
	f.password.SetEchoMode(qt.QLineEdit__Password)
	for _, sec := range account.Securities {
		f.security.AddItem3(sec.String(), qt.NewQVariant11(string(sec)))
	}
	for _, a := range account.Auths {
		f.auth.AddItem3(a.String(), qt.NewQVariant11(string(a)))
	}
	f.cert.SetPlaceholderText("Path of the PEM file of the certificate")
	f.key.SetPlaceholderText("Path of the PEM file of its key")
	f.result.SetWordWrap(true)
	f.result.SetTextInteractionFlags(qt.TextSelectableByMouse)

	form.AddRow3("Server:", f.host.QWidget)
	form.AddRow3("Port:", f.port.QWidget)
	form.AddRow3("Connection security:", f.security.QWidget)
	form.AddRow3("Authentication:", f.auth.QWidget)
	form.AddRow3("User name:", f.username.QWidget)
	form.AddRow3("Password:", f.password.QWidget)
	form.AddRow3("Client certificate:", f.cert.QWidget)
	form.AddRow3("Certificate key:", f.key.QWidget)
	testButton := qt.NewQPushButton3("Test Connection")
	form.AddRow3("", testButton.QWidget)
	form.AddRow3("", f.result.QWidget)

	f.set(s)
	f.security.OnCurrentIndexChanged(func(int) {
		// The port follows the security while it is a usual one.
		if slices.Contains([]int{ports[account.SecurityNone], ports[account.SecurityStartTLS], ports[account.SecurityTLS]}, f.port.Value()) {
			f.port.SetValue(ports[f.server().Security])
		}
		f.update()
	})
	f.auth.OnCurrentIndexChanged(func(int) { f.update() })
	testButton.OnClicked(func() {
		s := f.server()
		f.result.SetText("Testing…")
		testButton.SetEnabled(false)
		go func() {
			err := test(s)
			mainthread.Start(func() {
				testButton.SetEnabled(true)
				if err != nil {
					f.result.SetText(failure(err.Error()))
				} else {
					f.result.SetText(success("The connection and login worked."))
				}
			})
		}()
	})
	return page, form, f
}

// set fills the fields in from the server.
func (f *serverFields) set(s account.Server) {
	f.host.SetText(s.Host)
	f.port.SetValue(s.Port)
	f.username.SetText(s.Username)
	f.password.SetText(s.Password)
	if i := f.security.FindData(qt.NewQVariant11(string(s.Security))); i >= 0 {
		f.security.SetCurrentIndex(i)
	}
	if i := f.auth.FindData(qt.NewQVariant11(string(s.Auth))); i >= 0 {
		f.auth.SetCurrentIndex(i)
	}
	f.cert.SetText(s.ClientCert)
	f.key.SetText(s.ClientKey)
	f.update()
}

// update enables the fields that the kind of authentication uses, and warns
// of a password that would be sent as it is.
func (f *serverFields) update() {
	s := f.server()
	certs := s.Auth == account.AuthTLSCert
	f.cert.SetEnabled(certs)
	f.key.SetEnabled(certs)
	f.password.SetEnabled(!certs)
	if s.Security == account.SecurityNone && s.Auth == account.AuthPassword {
		f.result.SetText(failure("Without connection security the password is sent as it is."))
	} else {
		f.result.SetText("")
	}
}

// server returns the server the fields describe.
func (f *serverFields) server() account.Server {
	return account.Server{
		Host:       strings.TrimSpace(f.host.Text()),
		Port:       f.port.Value(),
		Username:   strings.TrimSpace(f.username.Text()),
		Password:   f.password.Text(),
		Security:   account.Security(f.security.CurrentData().ToString()),
		Auth:       account.Auth(f.auth.CurrentData().ToString()),
		ClientCert: strings.TrimSpace(f.cert.Text()),
		ClientKey:  strings.TrimSpace(f.key.Text()),
	}
}

// failure and success return a message for a label, in red or green.
func failure(text string) string {
	return "<span style=\"color: #c03030\">" + html.EscapeString(text) + "</span>"
}

func success(text string) string {
	return "<span style=\"color: #208020\">" + html.EscapeString(text) + "</span>"
}

// newAccountDialog shows the settings of an account, and calls saved with
// them if they are accepted. folders are the folders of the mailbox, which
// the folders for operations are chosen from.
func newAccountDialog(parent *qt.QWidget, a account.Account, isNew bool, folders []string, saved func(account.Account)) *qt.QDialog {
	d := qt.NewQDialog(parent)
	if isNew {
		d.SetWindowTitle("New Account")
	} else {
		d.SetWindowTitle("Account Settings: " + a.Title())
	}
	layout := qt.NewQVBoxLayout(d.QWidget)
	tabs := qt.NewQTabWidget2()
	layout.AddWidget(tabs.QWidget)

	// Identity.
	identityPage := qt.NewQWidget2()
	identityForm := qt.NewQFormLayout(identityPage)
	name := qt.NewQLineEdit3(a.Name)
	email := qt.NewQLineEdit3(a.Email)
	lookup := qt.NewQPushButton3("Look Up Settings")
	lookup.SetToolTip("Find the settings of the servers from the email address, as other clients do")
	emailRow := qt.NewQHBoxLayout2()
	emailRow.AddWidget(email.QWidget)
	emailRow.AddWidget(lookup.QWidget)
	lookupResult := qt.NewQLabel2()
	lookupResult.SetWordWrap(true)
	replyTo := qt.NewQLineEdit3(a.ReplyTo)
	signature := qt.NewQPlainTextEdit2()
	signature.SetPlainText(a.Signature)
	// A signature can be a file instead, of text or HTML, as other clients
	// have. The text is kept for when it is not.
	signatureFromFile := qt.NewQCheckBox3("Use the signature from a file instead")
	signatureFile := qt.NewQLineEdit3(a.SignatureFile)
	signatureFile.SetPlaceholderText("A file of text, or of HTML (.html or .htm)")
	browseSignature := qt.NewQPushButton3("Browse…")
	signatureFileRow := qt.NewQHBoxLayout2()
	signatureFileRow.AddWidget(signatureFile.QWidget)
	signatureFileRow.AddWidget(browseSignature.QWidget)
	fromFile := func(on bool) {
		signature.SetEnabled(!on)
		signatureFile.SetEnabled(on)
		browseSignature.SetEnabled(on)
	}
	signatureFromFile.SetChecked(a.SignatureFile != "")
	fromFile(a.SignatureFile != "")
	signatureFromFile.OnToggled(fromFile)
	browseSignature.OnClicked(func() {
		dir := filepath.Dir(homedir.Expand(signatureFile.Text()))
		if signatureFile.Text() == "" {
			dir, _ = os.UserHomeDir()
		}
		path := qt.QFileDialog_GetOpenFileName4(d.QWidget, "Signature File", dir, signatureFilter)
		if path != "" {
			signatureFile.SetText(path)
		}
	})
	identityForm.AddRow3("Your name:", name.QWidget)
	identityForm.AddRow4("Email address:", emailRow.QLayout)
	identityForm.AddRow3("", lookupResult.QWidget)
	identityForm.AddRow3("Reply-to address:", replyTo.QWidget)
	identityForm.AddRow3("Signature:", signature.QWidget)
	identityForm.AddRow3("", signatureFromFile.QWidget)
	identityForm.AddRow4("Signature file:", signatureFileRow.QLayout)
	tabs.AddTab(identityPage, "Identity")

	// Servers.
	incomingPage, _, incoming := newServerFields(a.Incoming,
		map[account.Security]int{account.SecurityNone: 143, account.SecurityStartTLS: 143, account.SecurityTLS: 993},
		func(s account.Server) error {
			c, err := remote.Dial(s, remote.Options{})
			if err != nil {
				return err
			}
			return c.Close()
		})
	tabs.AddTab(incomingPage, "Incoming (IMAP)")

	outgoingPage, outgoingForm, outgoing := newServerFields(a.Outgoing,
		map[account.Security]int{account.SecurityNone: 25, account.SecurityStartTLS: 587, account.SecurityTLS: 465},
		send.Test)
	outgoingName := qt.NewQLineEdit3(a.OutgoingName)
	outgoingName.SetPlaceholderText("A name to know it by")
	outgoingForm.InsertRow3(0, "Description:", outgoingName.QWidget)
	tabs.AddTab(outgoingPage, "Outgoing (SMTP)")

	// Folders.
	foldersPage := qt.NewQWidget2()
	foldersForm := qt.NewQFormLayout(foldersPage)
	folderOf := func(c *qt.QComboBox) string {
		text := strings.TrimSpace(c.CurrentText())
		if text == "Automatic" {
			return ""
		}
		return text
	}
	// fillFolders gives a choice of folder the folders of the mailbox, and
	// Automatic, keeping what is chosen.
	fillFolders := func(c *qt.QComboBox, folders []string) {
		current := folderOf(c)
		c.Clear()
		c.AddItem3("Automatic", qt.NewQVariant11(""))
		for _, f := range folders {
			c.AddItem3(f, qt.NewQVariant11(f))
		}
		if current == "" {
			c.SetCurrentIndex(0)
		} else {
			c.SetCurrentText(current)
		}
	}
	folderCombo := func(current string) *qt.QComboBox {
		c := qt.NewQComboBox2()
		c.SetEditable(true)
		c.SetCurrentText(current)
		fillFolders(c, folders)
		return c
	}
	sent := folderCombo(a.Folders.Sent)
	drafts := folderCombo(a.Folders.Drafts)
	trash := folderCombo(a.Folders.Trash)
	archive := folderCombo(a.Folders.Archive)
	readFolders := qt.NewQPushButton3("Read Folders from Server")
	readFolders.SetToolTip("List the folders of the mailbox with the settings of the incoming server, to choose from")
	readResult := qt.NewQLabel2()
	readResult.SetWordWrap(true)
	readFolders.OnClicked(func() {
		s := incoming.server()
		readResult.SetText("Reading the folders…")
		readFolders.SetEnabled(false)
		go func() {
			var names []string
			c, err := remote.Dial(s, remote.Options{})
			if err == nil {
				var list []store.Folder
				list, err = c.Folders()
				c.Close()
				for _, f := range list {
					if f.Selectable() {
						names = append(names, f.Name)
					}
				}
			}
			mainthread.Start(func() {
				readFolders.SetEnabled(true)
				if err != nil {
					readResult.SetText(failure(err.Error()))
					return
				}
				for _, c := range []*qt.QComboBox{sent, drafts, trash, archive} {
					fillFolders(c, names)
				}
				readResult.SetText(fmt.Sprintf("%d folders were found.", len(names)))
			})
		}()
	})
	foldersNote := qt.NewQLabel3("Automatic uses the folder the server marks for the purpose (RFC 6154), or one with a usual name for it, " +
		"as Archive or Archives. If there is none, it is made on the server, as Sent, Drafts, Trash or Archive, the first time it is needed.")
	foldersNote.SetWordWrap(true)
	foldersForm.AddRow3("Sent mail:", sent.QWidget)
	foldersForm.AddRow3("Drafts:", drafts.QWidget)
	foldersForm.AddRow3("Deleted mail (trash):", trash.QWidget)
	foldersForm.AddRow3("Archive:", archive.QWidget)
	archiveBy := qt.NewQComboBox2()
	for _, by := range account.ArchiveBys {
		archiveBy.AddItem3(by.String(), qt.NewQVariant11(string(by)))
	}
	archiveBy.SetCurrentIndex(archiveBy.FindData(qt.NewQVariant11(string(a.ArchiveBy))))
	archiveBy.SetToolTip("As other clients do: Archive/2026, or Archive/2026/2026-03, by when each message was sent")
	foldersForm.AddRow3("Archive into:", archiveBy.QWidget)
	foldersForm.AddRow3("", readFolders.QWidget)
	foldersForm.AddRow3("", readResult.QWidget)
	foldersForm.AddRow3("", foldersNote.QWidget)
	tabs.AddTab(foldersPage, "Folders")

	// Composition and synchronisation.
	optionsPage := qt.NewQWidget2()
	optionsForm := qt.NewQFormLayout(optionsPage)
	format := qt.NewQComboBox2()
	format.AddItems([]string{"Plain text", "HTML"})
	if a.ComposeHTML {
		format.SetCurrentIndex(1)
	}
	quote := qt.NewQComboBox2()
	quote.AddItems([]string{"Start my reply above the quote", "Start my reply below the quote"})
	if a.QuoteBelow {
		quote.SetCurrentIndex(1)
	}
	checkOnStart := qt.NewQCheckBox3("Check for new messages when ApeMail starts")
	checkOnStart.SetChecked(a.CheckOnStart)
	syncNote := qt.NewQLabel3("New mail is seen as it arrives in the inbox (IMAP IDLE) while ApeMail is running. " +
		"Any other folder is checked when it is opened, or by Get Mail while it is selected. " +
		"A message is downloaded only when it is opened.")
	syncNote.SetWordWrap(true)
	optionsForm.AddRow3("Compose messages in:", format.QWidget)
	optionsForm.AddRow3("When replying:", quote.QWidget)
	optionsForm.AddRow3("", checkOnStart.QWidget)
	optionsForm.AddRow3("", syncNote.QWidget)
	tabs.AddTab(optionsPage, "Composition && Sync")

	lookup.OnClicked(func() {
		address := strings.TrimSpace(email.Text())
		lookupResult.SetText(html.EscapeString("Looking up the settings of " + address + "…"))
		lookup.SetEnabled(false)
		go func() {
			config, err := autoconfig.Lookup(context.Background(), address)
			mainthread.Start(func() {
				lookup.SetEnabled(true)
				if err != nil {
					lookupResult.SetText(failure(err.Error() + ". The servers can be filled in by hand."))
					return
				}
				// What has been typed of the passwords is kept.
				in, out := config.Incoming, config.Outgoing
				in.Password = incoming.password.Text()
				incoming.set(in)
				if out.Host != "" {
					out.Password = outgoing.password.Text()
					outgoing.set(out)
				}
				lookupResult.SetText(success("Found in " + config.Source + ". Check the servers, and enter the passwords."))
			})
		}()
	})

	buttons := qt.NewQDialogButtonBox5(qt.QDialogButtonBox__Ok|qt.QDialogButtonBox__Cancel, qt.Horizontal)
	layout.AddWidget(buttons.QWidget)
	buttons.OnRejected(d.Reject)
	buttons.OnAccepted(func() {
		changed := a
		changed.Name = strings.TrimSpace(name.Text())
		changed.Email = strings.TrimSpace(email.Text())
		changed.ReplyTo = strings.TrimSpace(replyTo.Text())
		changed.Signature = signature.ToPlainText()
		changed.SignatureFile = ""
		if signatureFromFile.IsChecked() {
			changed.SignatureFile = strings.TrimSpace(signatureFile.Text())
			if err := checkSignatureFile(changed.SignatureFile); err != nil {
				warning(d.QWidget, "Account", err.Error())
				return
			}
		}
		changed.Incoming = incoming.server()
		changed.Outgoing = outgoing.server()
		changed.OutgoingName = strings.TrimSpace(outgoingName.Text())
		changed.Folders = account.Folders{
			Sent:    folderOf(sent),
			Drafts:  folderOf(drafts),
			Trash:   folderOf(trash),
			Archive: folderOf(archive),
		}
		changed.ComposeHTML = format.CurrentIndex() == 1
		changed.QuoteBelow = quote.CurrentIndex() == 1
		changed.ArchiveBy = account.ArchiveBy(archiveBy.CurrentData().ToString())
		changed.CheckOnStart = checkOnStart.IsChecked()
		if err := changed.Check(); err != nil {
			warning(d.QWidget, "Account", err.Error())
			return
		}
		d.Accept()
		saved(changed)
	})
	fitDialog(d, accountDialogWidth)
	openDialog(d)
	return d
}
