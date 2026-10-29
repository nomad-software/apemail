package ui

import (
	"errors"
	"fmt"
	"html"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"github.com/emersion/go-message/mail"
	qt "github.com/mappu/miqt/qt6"

	"github.com/nomad-software/apemail/internal/account"
	"github.com/nomad-software/apemail/internal/engine"
	"github.com/nomad-software/apemail/internal/icons"
	"github.com/nomad-software/apemail/internal/message"
	"github.com/nomad-software/apemail/internal/send"
)

// maxAttachment is the size of an attachment above which the user is asked
// if it is meant, as most servers refuse messages much bigger.
const maxAttachment = 20 << 20

// composer is a window a message is written in.
type composer struct {
	*qt.QMainWindow
	w *Window

	from                 *qt.QComboBox
	to, cc, bcc, subject *qt.QLineEdit
	// completers complete the addresses typed in To, Cc and Bcc from the
	// address book.
	completers []*addressCompleter
	// body is what is written, as HTML if html is set, and otherwise as
	// plain text.
	body        *bodyEditor
	attachList  *qt.QListWidget
	attachments []message.Attachment
	// images are the pictures put in the HTML, which shows each as
	// cid:ID. Those taken out of it again are left out when it is sent.
	images []message.Attachment
	html   bool
	// bar is the bar of buttons.
	bar *qt.QToolBar
	// editor is the field or body the Edit menu acts on: the one last in
	// focus, as the menu takes the focus while it is open.
	editor editor
	edit   editActions
	// closeItem is File > Close, which Escape is the shortcut of.
	closeItem *qt.QAction
	// receipt and dsn are the options of the Options menu: a return
	// receipt, and delivery status notifications.
	receipt, dsn *qt.QAction
	// bold, italic and underline are the buttons which format text, and
	// bullets and numbers those which make a list, in a message written as
	// HTML, which show the format at the cursor.
	bold, italic, underline, bullets, numbers *qt.QAction
	// formatting are the buttons which format text, which formatMenu has
	// too.
	formatting []*qt.QAction
	formatMenu *qt.QMenu

	// inReplyTo and references are the IDs of the message replied to and
	// those it follows, and repliedFolder and repliedUID where it is, so
	// that it can be marked as answered.
	inReplyTo     string
	references    []string
	repliedFolder string
	repliedUID    uint32

	// saved are the drafts of this message in the drafts folder: those
	// saved from this window, and the one it was opened from.
	saved []engine.Saved
	// sent says the message has been sent, or put in the outbox.
	sent bool
	// sending says it is being sent, while which the window is not used,
	// and savingDraft that a draft of it is being saved. The window does
	// not close while either is, as what they end with is shown in it.
	sending, savingDraft bool
}

// composers are the windows of the messages being written.
var composers = map[*composer]bool{}

// newComposer makes a window to write a message from the account in.
func (w *Window) newComposer(e *engine.Engine) *composer {
	a := e.Account()
	c := &composer{QMainWindow: qt.NewQMainWindow2(), w: w, html: a.ComposeHTML}
	c.SetAttribute(qt.WA_DeleteOnClose)
	c.SetWindowTitle("Write: (no subject)")
	c.Resize(w.config.ComposeWidth, w.config.ComposeHeight)

	central := qt.NewQWidget2()
	layout := qt.NewQVBoxLayout(central)
	form := qt.NewQFormLayout2()

	c.from = qt.NewQComboBox2()
	for _, id := range w.order {
		other := w.engines[id].Account()
		c.from.AddItem3(identity(other), qt.NewQVariant11(other.ID))
		if other.ID == a.ID {
			c.from.SetCurrentIndex(c.from.Count() - 1)
		}
	}
	form.AddRow3("From:", c.from.QWidget)
	c.to = qt.NewQLineEdit2()
	c.cc = qt.NewQLineEdit2()
	c.bcc = qt.NewQLineEdit2()
	c.subject = qt.NewQLineEdit2()
	for _, f := range []*qt.QLineEdit{c.to, c.cc, c.bcc} {
		f.SetPlaceholderText("Addresses, separated by commas")
		c.completers = append(c.completers, newAddressCompleter(f, w.contacts.model))
	}
	form.AddRow3("To:", c.to.QWidget)
	form.AddRow3("Cc:", c.cc.QWidget)
	form.AddRow3("Bcc:", c.bcc.QWidget)
	form.AddRow3("Subject:", c.subject.QWidget)
	c.subject.OnTextChanged(func(text string) {
		if text == "" {
			text = "(no subject)"
		}
		c.SetWindowTitle("Write: " + text)
	})
	layout.AddLayout(form.QLayout)

	c.attachList = qt.NewQListWidget2()
	c.attachList.SetMaximumHeight(70)
	c.attachList.SetVisible(false)
	c.attachList.SetToolTip("Attachments; press Delete to remove the one selected")
	layout.AddWidget(c.attachList.QWidget)

	c.body = newBodyEditor(c.QObject, c.html)
	c.body.parts = c.image
	c.body.loadImages = a.LoadRemoteImages
	layout.AddWidget(c.body.web.QWidget)
	c.SetCentralWidget(central)

	c.makeButtonBar()
	c.makeMenus()
	composers[c] = true
	c.OnCloseEvent(func(super func(*qt.QCloseEvent), event *qt.QCloseEvent) {
		if !c.mayClose() {
			event.Ignore()
			return
		}
		delete(composers, c)
		// The next window is opened the size this one was left.
		if !c.IsMaximized() {
			w.config.ComposeWidth, w.config.ComposeHeight = c.Width(), c.Height()
			w.saveConfig()
		}
		super(event)
	})
	return c
}

// identity returns the sender an account writes as.
func identity(a account.Account) string {
	return message.FormatAddress(&mail.Address{Name: a.Name, Address: a.Email})
}

// makeButtonBar makes the bar of large buttons of the window.
func (c *composer) makeButtonBar() {
	bar := qt.NewQToolBar4("Buttons", c.QWidget)
	c.bar = bar
	bar.SetMovable(false)
	bar.SetToolButtonStyle(qt.ToolButtonTextUnderIcon)
	bar.SetIconSize(qt.NewQSize2(32, 32))
	add := func(text, shortcut string, ic *qt.QIcon, f func()) *qt.QAction {
		a := qt.NewQAction2(text)
		a.SetIcon(ic)
		if shortcut != "" {
			a.SetShortcut(qt.NewQKeySequence2(shortcut))
		}
		if f != nil {
			a.OnTriggered(f)
		}
		bar.QWidget.AddAction(a)
		return a
	}
	add("Send", "Ctrl+Return", icon(icons.Sent), c.send)
	add("Save Draft", "Ctrl+S", icon(icons.Save), func() { c.saveDraft(nil) })
	add("Attach", "", icon(icons.Attachment), c.attach)
	remove := qt.NewQAction2("Remove Attachment")
	remove.SetShortcut(qt.NewQKeySequence2("Del"))
	remove.SetShortcutContext(qt.WidgetShortcut)
	remove.OnTriggered(c.removeAttachment)
	c.attachList.QWidget.AddAction(remove)

	if c.html {
		bar.AddSeparator()
		// Each button does the editing command of its name to the text
		// selected, which turns the format on or off as it is there.
		format := func(text, shortcut string, ic *qt.QIcon, command string) *qt.QAction {
			a := add(text, shortcut, ic, func() { c.body.exec(command, "") })
			a.SetCheckable(true)
			return a
		}
		c.bold = format("Bold", "Ctrl+B", icon(icons.Bold), "bold")
		c.italic = format("Italic", "Ctrl+I", icon(icons.Italic), "italic")
		c.underline = format("Underline", "Ctrl+U", icon(icons.Underline), "underline")
		c.bullets = format("Bullets", "", icon(icons.Bullets), "insertUnorderedList")
		c.numbers = format("Numbers", "", icon(icons.Numbers), "insertOrderedList")
		add("Image", "", icon(icons.Image), c.insertImage)
		// The buttons show the format of the text at the cursor, as the
		// body says it is, whatever a click on one checked.
		c.body.onState = func(s editState) {
			c.bold.SetChecked(s.Bold)
			c.italic.SetChecked(s.Italic)
			c.underline.SetChecked(s.Underline)
			c.bullets.SetChecked(s.Bullets)
			c.numbers.SetChecked(s.Numbers)
		}
		c.formatting = []*qt.QAction{c.bold, c.italic, c.underline, c.bullets, c.numbers}
	}
	c.AddToolBar(qt.TopToolBarArea, bar)
}

// engine returns the engine of the account the message is from.
func (c *composer) engine() *engine.Engine {
	return c.w.engines[currentData(c.from)]
}

// draft calls got with the message as it has been written, once the body
// has said what it holds.
func (c *composer) draft(got func(*message.Draft, error)) {
	if c.engine() == nil {
		got(nil, errors.New("the account to send from has gone"))
		return
	}
	c.body.content(func(html, text string) {
		// The account may have gone meanwhile.
		e := c.engine()
		if e == nil {
			got(nil, errors.New("the account to send from has gone"))
			return
		}
		a := e.Account()
		d := &message.Draft{
			From:          &mail.Address{Name: a.Name, Address: a.Email},
			ReplyTo:       a.ReplyTo,
			To:            c.to.Text(),
			Cc:            c.cc.Text(),
			Bcc:           c.bcc.Text(),
			Subject:       c.subject.Text(),
			Text:          text,
			HTML:          html,
			InReplyTo:     c.inReplyTo,
			References:    c.references,
			Attachments:   c.attachments,
			ReturnReceipt: c.receipt.IsChecked(),
		}
		for _, img := range c.images {
			if strings.Contains(d.HTML, "cid:"+img.ContentID) {
				d.Images = append(d.Images, img)
			}
		}
		got(d, nil)
	})
}

// send sends the message. The window is not used from when it is asked
// for what is written, so that nothing changes that is not sent.
func (c *composer) send() {
	if c.sending {
		return
	}
	c.sending = true
	c.SetEnabled(false)
	stop := func(err error) {
		c.sending = false
		c.SetEnabled(true)
		if err != nil {
			c.warn("Send", err)
		}
	}
	c.draft(func(d *message.Draft, err error) {
		if err != nil {
			stop(err)
			return
		}
		rcpts, err := d.Recipients()
		if err != nil {
			stop(err)
			return
		}
		if len(rcpts) == 0 {
			stop(errors.New("the message has no recipients"))
			return
		}
		if strings.TrimSpace(d.Subject) == "" && !confirm(c.QWidget, "Send", "The message has no subject. Send it anyway?") {
			stop(nil)
			return
		}
		raw, err := d.Build(false)
		if err != nil {
			stop(err)
			return
		}
		c.sendBuilt(d, raw, rcpts)
	})
}

// sendBuilt sends the message as it has been built, to the recipients.
func (c *composer) sendBuilt(d *message.Draft, raw []byte, rcpts []string) {
	// Recipients has read every list of addresses already.
	to, _ := message.ParseAddresses(d.To)
	e := c.engine()
	c.StatusBar().ShowMessage("Sending…")
	e.Send(raw, rcpts, c.dsn.IsChecked(), func(err error) {
		c.sending = false
		c.SetEnabled(true)
		c.StatusBar().ClearMessage()
		if errors.Is(err, send.ErrUnreachable) {
			qt.QMessageBox_Information(c.QWidget, "Send", err.Error())
		} else if err != nil {
			c.warn("Send", err)
			return
		}
		c.sent = true
		if c.repliedUID != 0 {
			e.SetFlag(c.repliedFolder, []uint32{c.repliedUID}, `\Answered`, true, c.w.showError)
		}
		// Whoever a reply went to is put in the address book, unless
		// they are there already.
		if c.inReplyTo != "" {
			c.w.showError(c.w.addContacts(to))
		}
		c.Close()
	})
}

// saveDraft puts the message in the drafts folder, and then calls done if it
// is not nil.
func (c *composer) saveDraft(done func()) {
	if c.savingDraft {
		return
	}
	c.savingDraft = true
	c.StatusBar().ShowMessage("Saving the draft…")
	failed := func(err error) {
		c.savingDraft = false
		c.StatusBar().ClearMessage()
		c.warn("Save Draft", err)
	}
	c.draft(func(d *message.Draft, err error) {
		if err != nil {
			failed(err)
			return
		}
		raw, err := d.Build(true)
		if err != nil {
			failed(err)
			return
		}
		c.saveBuilt(raw, done)
	})
}

// saveBuilt puts the message, as it has been built, in the drafts folder,
// and then calls done if it is not nil.
func (c *composer) saveBuilt(raw []byte, done func()) {
	c.engine().SaveDraft(raw, func(saved engine.Saved, err error) {
		c.savingDraft = false
		c.StatusBar().ClearMessage()
		if err != nil {
			c.warn("Save Draft", err)
			return
		}
		c.body.modified = false
		c.saved = append(c.saved, saved)
		c.StatusBar().ShowMessage2("The draft was saved to "+saved.Folder+".", 5000)
		if done != nil {
			done()
		}
	})
}

// mayClose asks the user what to do with a message that has not been sent,
// moves the drafts of it that are no longer needed to the trash, and reports
// if the window can close.
func (c *composer) mayClose() bool {
	if c.sending || c.savingDraft {
		return false
	}
	keepLatest := false
	if !c.sent && c.body.modified {
		answer := qt.QMessageBox_Question5(c.QWidget, "Close",
			"The message has not been sent. Save it as a draft?",
			qt.QMessageBox__Save|qt.QMessageBox__Discard|qt.QMessageBox__Cancel)
		switch answer {
		case qt.QMessageBox__Save:
			c.saveDraft(func() { c.Close() })
			return false
		case qt.QMessageBox__Cancel:
			return false
		}
	} else if !c.sent {
		// The latest draft is what the message is, unless it was sent.
		keepLatest = true
	}

	obsolete := c.saved
	if keepLatest && len(obsolete) > 0 {
		obsolete = obsolete[:len(obsolete)-1]
	}
	var deletable []engine.Saved
	for _, s := range obsolete {
		if s.UID != 0 {
			deletable = append(deletable, s)
		}
	}
	// Drafts that a later one, or the message sent, replaces go to the
	// trash, as other clients do; nothing is lost, so the user is not asked.
	if e := c.engine(); e != nil && len(deletable) > 0 {
		e.TrashSaved(deletable, c.w.showError)
	}
	return true
}

// attach asks for files to attach.
func (c *composer) attach() {
	home, _ := os.UserHomeDir()
	paths := qt.QFileDialog_GetOpenFileNames3(c.QWidget, "Attach Files", home)
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			c.warn("Attach", err)
			continue
		}
		if info.Size() > maxAttachment && !confirm(c.QWidget, "Attach",
			fmt.Sprintf("%s is %s, which many servers will refuse. Attach it anyway?", filepath.Base(path), size(int(info.Size())))) {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			c.warn("Attach", err)
			continue
		}
		c.addAttachment(message.Attachment{
			Filename:    filepath.Base(path),
			ContentType: mime.TypeByExtension(filepath.Ext(path)),
			Data:        data,
		})
	}
}

// addAttachment attaches a file.
func (c *composer) addAttachment(a message.Attachment) {
	c.attachments = append(c.attachments, a)
	c.attachList.AddItem(fmt.Sprintf("%s (%s)", a.Filename, size(len(a.Data))))
	c.attachList.SetVisible(true)
	c.body.modified = true
}

// removeAttachment removes the attachment that is selected.
func (c *composer) removeAttachment() {
	row := c.attachList.CurrentRow()
	if row < 0 || row >= len(c.attachments) {
		return
	}
	c.attachments = append(c.attachments[:row], c.attachments[row+1:]...)
	c.attachList.TakeItem(row).Delete()
	c.attachList.SetVisible(len(c.attachments) > 0)
	c.body.modified = true
}

func (c *composer) warn(title string, err error) {
	warning(c.QWidget, title, err.Error())
}

// composeEngine returns the engine of the account to write from: the one
// that is selected, or the first.
func (w *Window) composeEngine() *engine.Engine {
	e := w.currentEngine()
	if e == nil {
		qt.QMessageBox_Information(w.QWidget, "Write", "There is no account to write from.")
	}
	return e
}

// composeNew opens a window to write a new message to the addresses.
func (w *Window) composeNew(to, subject string) {
	e := w.composeEngine()
	if e == nil {
		return
	}
	c := w.newComposer(e)
	c.to.SetText(to)
	c.subject.SetText(subject)
	c.setBody(e.Account(), "", "")
	c.Show()
	if to == "" {
		c.to.SetFocus()
	} else {
		c.body.web.SetFocus()
	}
}

// replyTo opens a window to reply to the message that is shown, or to
// everyone it was sent to.
func (w *Window) replyTo(all bool) {
	m := w.view.msg
	e := w.engines[w.account]
	if m == nil || e == nil {
		return
	}
	a := e.Account()
	c := w.newComposer(e)
	to, cc := m.ReplyRecipients(all, a.Email, a.ReplyTo)
	c.to.SetText(to)
	c.cc.SetText(cc)
	c.subject.SetText(message.ReplySubject(m.Subject))
	c.inReplyTo, c.references = m.ReplyReferences()
	c.repliedFolder, c.repliedUID = w.view.folder, w.view.uid
	c.addImages(m)
	c.addWebImages(m)
	c.setBody(a, m.Attribution()+"\n"+message.QuoteText(m.Body()), m.QuoteHTML())
	c.Show()
	c.body.web.SetFocus()
}

// forwardMessage opens a window to forward the message that is shown, with
// its attachments.
func (w *Window) forwardMessage() {
	m := w.view.msg
	e := w.engines[w.account]
	if m == nil || e == nil {
		return
	}
	c := w.newComposer(e)
	c.subject.SetText(message.ForwardSubject(m.Subject))
	header := m.ForwardHeader()
	quoteHTML := "<p>" + strings.ReplaceAll(html.EscapeString(header), "\n", "<br>") + "</p>"
	if m.HTML != "" {
		quoteHTML += m.HTML
	} else {
		quoteHTML += message.TextToHTML(m.Body())
	}
	c.addImages(m)
	c.addWebImages(m)
	c.setBody(e.Account(), header+"\n"+m.Body(), quoteHTML)
	for _, a := range m.Files() {
		c.addAttachment(a)
	}
	c.body.modified = false
	c.Show()
	c.to.SetFocus()
}

// composeDraft opens a draft to be finished, which is replaced when it is
// saved or sent, if the user agrees.
func (w *Window) composeDraft(e *engine.Engine, m *message.Message, folder string, uid uint32) {
	c := w.newComposer(e)
	c.to.SetText(message.FormatAddresses(m.To))
	c.cc.SetText(message.FormatAddresses(m.Cc))
	if bcc := m.Bcc; len(bcc) > 0 {
		c.bcc.SetText(message.FormatAddresses(bcc))
	}
	c.subject.SetText(m.Subject)
	c.receipt.SetChecked(len(m.ReceiptTo) > 0)
	if len(m.References) > 0 {
		c.references = m.References
		c.inReplyTo = m.References[len(m.References)-1]
	}
	if c.html && m.HTML != "" {
		c.addImages(m)
		c.addWebImages(m)
		c.body.setHTML(m.HTML)
	} else {
		c.body.setText(m.Body(), 0)
	}
	for _, a := range m.Files() {
		c.addAttachment(a)
	}
	c.saved = append(c.saved, engine.Saved{Folder: folder, UID: uid, UIDValidity: w.uidValidity(e, folder)})
	c.body.modified = false
	c.Show()
}

// uidValidity returns the UID validity of the folder that is listed.
func (w *Window) uidValidity(e *engine.Engine, folder string) uint32 {
	if w.folder == folder && w.engines[w.account] == e {
		return w.list.uidValidity
	}
	return 0
}

// closeComposers closes the windows of messages being written, asking the
// user about those that have not been sent, and reports if they all closed.
func (w *Window) closeComposers() bool {
	for c := range composers {
		c.Raise()
		if !c.Close() {
			return false
		}
	}
	return true
}
