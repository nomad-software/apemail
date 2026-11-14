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

	"github.com/nomad-software/qute/qtcore"
	"github.com/nomad-software/qute/qtwidgets"

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
	host     *qtwidgets.QLineEdit
	port     *qtwidgets.QSpinBox
	username *qtwidgets.QLineEdit
	password *qtwidgets.QLineEdit
	security *qtwidgets.QComboBox
	auth     *qtwidgets.QComboBox
	cert     *qtwidgets.QLineEdit
	key      *qtwidgets.QLineEdit
	result   *qtwidgets.QLabel
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
func newServerFields(s account.Server, ports map[account.Security]int, test func(account.Server) error) (qtwidgets.QWidgetLike, *qtwidgets.QFormLayout, *serverFields) {
	page := qtwidgets.NewQWidget(nil)
	form := qtwidgets.NewQFormLayout(page)
	f := &serverFields{
		host:     qtwidgets.NewQLineEdit(nil),
		port:     qtwidgets.NewQSpinBox(nil),
		username: qtwidgets.NewQLineEdit(nil),
		password: qtwidgets.NewQLineEdit(nil),
		security: qtwidgets.NewQComboBox(nil),
		auth:     qtwidgets.NewQComboBox(nil),
		cert:     qtwidgets.NewQLineEdit(nil),
		key:      qtwidgets.NewQLineEdit(nil),
		result:   qtwidgets.NewQLabel(nil),
		ports:    ports,
	}
	f.port.SetRange(1, 65535)
	f.password.SetEchoMode(qtwidgets.QLineEditPassword)
	for _, sec := range account.Securities {
		f.security.AddItemFull(sec.String(), qtcore.NewQVariantWithString(string(sec)))
	}
	for _, a := range account.Auths {
		f.auth.AddItemFull(a.String(), qtcore.NewQVariantWithString(string(a)))
	}
	f.cert.SetPlaceholderText("Path of the PEM file of the certificate")
	f.key.SetPlaceholderText("Path of the PEM file of its key")
	f.result.SetWordWrap(true)
	f.result.SetTextInteractionFlags(qtcore.TextSelectableByMouse)

	form.AddRowText("Server:", f.host)
	form.AddRowText("Port:", f.port)
	form.AddRowText("Connection security:", f.security)
	form.AddRowText("Authentication:", f.auth)
	form.AddRowText("User name:", f.username)
	form.AddRowText("Password:", f.password)
	form.AddRowText("Client certificate:", f.cert)
	form.AddRowText("Certificate key:", f.key)
	testButton := qtwidgets.NewQPushButtonWithText("Test Connection", nil)
	form.AddRowText("", testButton)
	form.AddRowText("", f.result)

	f.set(s)
	f.security.OnCurrentIndexChanged(func(int) {
		// The port follows the security while it is a usual one.
		if slices.Contains([]int{ports[account.SecurityNone], ports[account.SecurityStartTLS], ports[account.SecurityTLS]}, f.port.Value()) {
			f.port.SetValue(ports[f.server().Security])
		}
		f.update()
	})
	f.auth.OnCurrentIndexChanged(func(int) { f.update() })
	testButton.OnClicked(func(bool) {
		s := f.server()
		f.result.SetText("Testing…")
		testButton.SetEnabled(false)
		var err error
		inBackground(page, func() { err = test(s) }, func() {
			testButton.SetEnabled(true)
			if err != nil {
				f.result.SetText(failure(err.Error()))
			} else {
				f.result.SetText(success("The connection and login worked."))
			}
		})
	})
	return page, form, f
}

// set fills the fields in from the server.
func (f *serverFields) set(s account.Server) {
	f.host.SetText(s.Host)
	f.username.SetText(s.Username)
	f.password.SetText(s.Password)
	if i := f.security.FindData(qtcore.NewQVariantWithString(string(s.Security))); i >= 0 {
		f.security.SetCurrentIndex(i)
	}
	// The port is set after the security, whose change gives a usual port
	// the one usual for it, which would take the place of the one given.
	f.port.SetValue(s.Port)
	if i := f.auth.FindData(qtcore.NewQVariantWithString(string(s.Auth))); i >= 0 {
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
		Security:   account.Security(currentData(f.security)),
		Auth:       account.Auth(currentData(f.auth)),
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
func newAccountDialog(parent qtwidgets.QWidgetLike, a account.Account, isNew bool, folders []string, saved func(account.Account)) *qtwidgets.QDialog {
	d := qtwidgets.NewQDialog(parent)
	if isNew {
		d.SetWindowTitle("New Account")
	} else {
		d.SetWindowTitle("Account Settings: " + a.Title())
	}
	layout := qtwidgets.NewQVBoxLayout(d)
	tabs := qtwidgets.NewQTabWidget(nil)
	layout.AddWidget(tabs)

	// Identity.
	identityPage := qtwidgets.NewQWidget(nil)
	identityForm := qtwidgets.NewQFormLayout(identityPage)
	name := qtwidgets.NewQLineEditWithText(a.Name, nil)
	email := qtwidgets.NewQLineEditWithText(a.Email, nil)
	lookup := qtwidgets.NewQPushButtonWithText("Look Up Settings", nil)
	lookup.SetToolTip("Find the settings of the servers from the email address, as other clients do")
	emailRow := qtwidgets.NewQHBoxLayout(nil)
	emailRow.AddWidget(email)
	emailRow.AddWidget(lookup)
	lookupResult := qtwidgets.NewQLabel(nil)
	lookupResult.SetWordWrap(true)
	replyTo := qtwidgets.NewQLineEditWithText(a.ReplyTo, nil)
	signature := qtwidgets.NewQPlainTextEdit(nil)
	signature.SetPlainText(a.Signature)
	// A signature can be a file instead, of text or HTML, as other clients
	// have. The text is kept for when it is not.
	signatureFromFile := qtwidgets.NewQCheckBoxWithText("Use the signature from a file instead", nil)
	signatureFile := qtwidgets.NewQLineEditWithText(a.SignatureFile, nil)
	signatureFile.SetPlaceholderText("A file of text, or of HTML (.html or .htm)")
	browseSignature := qtwidgets.NewQPushButtonWithText("Browse…", nil)
	signatureFileRow := qtwidgets.NewQHBoxLayout(nil)
	signatureFileRow.AddWidget(signatureFile)
	signatureFileRow.AddWidget(browseSignature)
	fromFile := func(on bool) {
		signature.SetEnabled(!on)
		signatureFile.SetEnabled(on)
		browseSignature.SetEnabled(on)
	}
	signatureFromFile.SetChecked(a.SignatureFile != "")
	fromFile(a.SignatureFile != "")
	signatureFromFile.OnToggled(fromFile)
	browseSignature.OnClicked(func(bool) {
		dir := filepath.Dir(homedir.Expand(signatureFile.Text()))
		if signatureFile.Text() == "" {
			dir, _ = os.UserHomeDir()
		}
		path, _ := qtwidgets.QFileDialogGetOpenFileNameFull(d, "Signature File", dir, signatureFilter, "", 0)
		if path != "" {
			signatureFile.SetText(path)
		}
	})
	identityForm.AddRowText("Your name:", name)
	identityForm.AddRowTextLayout("Email address:", emailRow)
	identityForm.AddRowText("", lookupResult)
	identityForm.AddRowText("Reply-to address:", replyTo)
	identityForm.AddRowText("Signature:", signature)
	identityForm.AddRowText("", signatureFromFile)
	identityForm.AddRowTextLayout("Signature file:", signatureFileRow)
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
	outgoingName := qtwidgets.NewQLineEditWithText(a.OutgoingName, nil)
	outgoingName.SetPlaceholderText("A name to know it by")
	outgoingForm.InsertRowText(0, "Description:", outgoingName)
	tabs.AddTab(outgoingPage, "Outgoing (SMTP)")

	// Folders.
	foldersPage := qtwidgets.NewQWidget(nil)
	foldersForm := qtwidgets.NewQFormLayout(foldersPage)
	folderOf := func(c *qtwidgets.QComboBox) string {
		text := strings.TrimSpace(c.CurrentText())
		if text == "Automatic" {
			return ""
		}
		return text
	}
	// fillFolders gives a choice of folder the folders of the mailbox, and
	// Automatic, keeping what is chosen.
	fillFolders := func(c *qtwidgets.QComboBox, folders []string) {
		current := folderOf(c)
		c.Clear()
		c.AddItemFull("Automatic", qtcore.NewQVariantWithString(""))
		for _, f := range folders {
			c.AddItemFull(f, qtcore.NewQVariantWithString(f))
		}
		if current == "" {
			c.SetCurrentIndex(0)
		} else {
			c.SetCurrentText(current)
		}
	}
	folderCombo := func(current string) *qtwidgets.QComboBox {
		c := qtwidgets.NewQComboBox(nil)
		c.SetEditable(true)
		c.SetCurrentText(current)
		fillFolders(c, folders)
		return c
	}
	sent := folderCombo(a.Folders.Sent)
	drafts := folderCombo(a.Folders.Drafts)
	trash := folderCombo(a.Folders.Trash)
	archive := folderCombo(a.Folders.Archive)
	readFolders := qtwidgets.NewQPushButtonWithText("Read Folders from Server", nil)
	readFolders.SetToolTip("List the folders of the mailbox with the settings of the incoming server, to choose from")
	readResult := qtwidgets.NewQLabel(nil)
	readResult.SetWordWrap(true)
	readFolders.OnClicked(func(bool) {
		s := incoming.server()
		readResult.SetText("Reading the folders…")
		readFolders.SetEnabled(false)
		var names []string
		var err error
		inBackground(d, func() {
			var c *remote.Conn
			c, err = remote.Dial(s, remote.Options{})
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
		}, func() {
			readFolders.SetEnabled(true)
			if err != nil {
				readResult.SetText(failure(err.Error()))
				return
			}
			for _, c := range []*qtwidgets.QComboBox{sent, drafts, trash, archive} {
				fillFolders(c, names)
			}
			readResult.SetText(fmt.Sprintf("%d folders were found.", len(names)))
		})
	})
	foldersNote := qtwidgets.NewQLabelWithText("Automatic uses the folder the server marks for the purpose (RFC 6154), or one with a usual name for it, "+
		"as Archive or Archives. If there is none, it is made on the server, as Sent, Drafts, Trash or Archive, the first time it is needed. "+
		"The side bar shows each by what it is for, whatever the server calls it: an archive called Archives is shown as Archive.", nil)
	foldersNote.SetWordWrap(true)
	foldersForm.AddRowText("Sent mail:", sent)
	foldersForm.AddRowText("Drafts:", drafts)
	foldersForm.AddRowText("Deleted mail (trash):", trash)
	foldersForm.AddRowText("Archive:", archive)
	archiveBy := qtwidgets.NewQComboBox(nil)
	for _, by := range account.ArchiveBys {
		archiveBy.AddItemFull(by.String(), qtcore.NewQVariantWithString(string(by)))
	}
	archiveBy.SetCurrentIndex(archiveBy.FindData(qtcore.NewQVariantWithString(string(a.ArchiveBy))))
	archiveBy.SetToolTip("As other clients do: Archive/2026, or Archive/2026/2026-03, by when each message was sent")
	foldersForm.AddRowText("Archive into:", archiveBy)
	foldersForm.AddRowText("", readFolders)
	foldersForm.AddRowText("", readResult)
	foldersForm.AddRowText("", foldersNote)
	tabs.AddTab(foldersPage, "Folders")

	// Composition and synchronisation.
	optionsPage := qtwidgets.NewQWidget(nil)
	optionsForm := qtwidgets.NewQFormLayout(optionsPage)
	format := qtwidgets.NewQComboBox(nil)
	format.AddItems([]string{"Plain text", "HTML"})
	if a.ComposeHTML {
		format.SetCurrentIndex(1)
	}
	quote := qtwidgets.NewQComboBox(nil)
	quote.AddItems([]string{"Start my reply above the quote", "Start my reply below the quote"})
	if a.QuoteBelow {
		quote.SetCurrentIndex(1)
	}
	remoteImages := qtwidgets.NewQCheckBoxWithText("Load images from the internet when a message is shown", nil)
	remoteImages.SetChecked(a.LoadRemoteImages)
	remoteImagesNote := qtwidgets.NewQLabelWithText("Loading them tells the sender that the message was read, when, and where from. "+
		"Without this, they are loaded only when Load Remote Images is pressed above a message. Only images are loaded.", nil)
	remoteImagesNote.SetWordWrap(true)
	checkOnStart := qtwidgets.NewQCheckBoxWithText("Check for new messages when ApeMail starts", nil)
	checkOnStart.SetChecked(a.CheckOnStart)
	syncNote := qtwidgets.NewQLabelWithText("New mail is seen as it arrives in the inbox (IMAP IDLE) while ApeMail is running. "+
		"Any other folder is checked when it is opened, or by Get Mail while it is selected. "+
		"A message is downloaded only when it is opened.", nil)
	syncNote.SetWordWrap(true)
	optionsForm.AddRowText("Compose messages in:", format)
	optionsForm.AddRowText("When replying:", quote)
	optionsForm.AddRowText("Images:", remoteImages)
	optionsForm.AddRowText("", remoteImagesNote)
	optionsForm.AddRowText("", checkOnStart)
	optionsForm.AddRowText("", syncNote)
	tabs.AddTab(optionsPage, "Composition && Sync")

	lookup.OnClicked(func(bool) {
		address := strings.TrimSpace(email.Text())
		lookupResult.SetText(html.EscapeString("Looking up the settings of " + address + "…"))
		lookup.SetEnabled(false)
		var config *autoconfig.Config
		var err error
		inBackground(d, func() { config, err = autoconfig.Lookup(context.Background(), address) }, func() {
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
	})

	buttons := qtwidgets.NewQDialogButtonBoxWithButtonsOriented(qtwidgets.QDialogButtonBoxOk|qtwidgets.QDialogButtonBoxCancel, qtcore.Horizontal, nil)
	layout.AddWidget(buttons)
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
				warning(d, "Account", err.Error())
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
		changed.ArchiveBy = account.ArchiveBy(currentData(archiveBy))
		changed.CheckOnStart = checkOnStart.IsChecked()
		changed.LoadRemoteImages = remoteImages.IsChecked()
		if err := changed.Check(); err != nil {
			warning(d, "Account", err.Error())
			return
		}
		d.Accept()
		saved(changed)
	})
	fitDialog(d, accountDialogWidth)
	openDialog(d)
	return d
}
