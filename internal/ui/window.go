// Package ui contains the Qt user interface.
package ui

import (
	"errors"
	"fmt"
	"html"
	"os"
	"runtime"
	"slices"
	"strings"

	"github.com/emersion/go-message/mail"
	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/mainthread"

	"github.com/nomad-software/apemail/internal/account"
	"github.com/nomad-software/apemail/internal/crypt"
	"github.com/nomad-software/apemail/internal/engine"
	"github.com/nomad-software/apemail/internal/icons"
	"github.com/nomad-software/apemail/internal/message"
	"github.com/nomad-software/apemail/internal/settings"
	"github.com/nomad-software/apemail/internal/store"
)

// Files are the places the program's data is kept in.
type Files struct {
	// Settings is the path of the JSON file of the settings.
	Settings string
	// Accounts is the folder the accounts and their mail are kept in.
	Accounts string
}

// statusPadding is the space between the left edge of the window and the
// status, so that it does not touch the edge.
const statusPadding = 8

// defaultSidebarWidth and defaultListHeight are the sizes of the folder tree
// and the message list until they are dragged.
const (
	defaultSidebarWidth = 260
	defaultListHeight   = 300
)

// Window is the main application window.
type Window struct {
	*qt.QMainWindow

	files  Files
	config settings.Settings
	keys   *crypt.Keys

	// engines are those of the accounts, by ID, and order is the order the
	// accounts are shown in.
	engines map[string]*engine.Engine
	order   []string

	buttons      *qt.QToolBar
	split, right *qt.QSplitter
	folders      *folderTree
	folderTitle  *qt.QLabel
	list         *messageList
	view         *messageView
	statusText   *qt.QLabel

	// account and folder are those whose messages are listed.
	account, folder string
	// statuses are what each account is doing, and doing what the window
	// itself is, as applying the settings.
	statuses map[string]string
	doing    string

	// contacts is the address book, and bookDialog its window if it is
	// open.
	contacts   contacts
	bookDialog *bookWindow

	// The actions of the menus and the button bar.
	getMail, write, reply, replyAll, forward, archive, del *qt.QAction
	markRead, markUnread, viewSource, addressBook          *qt.QAction
	editAccount, removeAccount, updateCounts, syncAll      *qt.QAction
}

// NewWindow makes the main window.
func NewWindow(files Files) *Window {
	w := &Window{
		QMainWindow: qt.NewQMainWindow2(),
		files:       files,
		config:      settings.Load(files.Settings),
		engines:     map[string]*engine.Engine{},
		statuses:    map[string]string{},
	}
	rememberSystemStyle()
	if w.config.Theme != "" {
		applyTheme(w.config.Theme)
	}
	w.SetWindowTitle("ApeMail")
	w.SetWindowIcon(appIcon())
	// Dialogs and the compose window take their icon from the application.
	qt.QGuiApplication_SetWindowIcon(appIcon())
	w.Resize(w.config.Width, w.config.Height)
	w.contacts.model = qt.NewQStringListModel3(w.QObject)

	w.makeActions()
	w.makeMenus()
	w.makeButtonBar()

	w.folders = newFolderTree(w.config.Expanded)
	w.list = newMessageList()
	w.list.setColumnWidths(w.config.CorrespondentWidth, w.config.DateWidth)
	w.view = newMessageView()

	// Above the list is the name of the folder and how many messages it
	// has.
	listPane := qt.NewQWidget2()
	listLayout := qt.NewQVBoxLayout(listPane)
	listLayout.SetContentsMargins(0, 0, 0, 0)
	listLayout.SetSpacing(0)
	w.folderTitle = qt.NewQLabel2()
	w.folderTitle.SetContentsMargins(titlePadding, titlePadding, titlePadding, titlePadding)
	w.folderTitle.SetTextFormat(qt.RichText)
	listLayout.AddWidget(w.folderTitle.QWidget)
	listLayout.AddWidget(w.list.QWidget)
	w.list.changed = w.showFolderTitle
	w.showFolderTitle()

	w.right = qt.NewQSplitter3(qt.Vertical)
	w.right.AddWidget(listPane)
	w.right.AddWidget(w.view.QWidget)
	w.right.SetStretchFactor(1, 1)
	w.right.SetChildrenCollapsible(false)

	w.split = qt.NewQSplitter3(qt.Horizontal)
	w.split.AddWidget(w.folders.QWidget)
	w.split.AddWidget(w.right.QWidget)
	w.split.SetStretchFactor(1, 1)
	w.split.SetChildrenCollapsible(false)
	w.SetCentralWidget(w.split.QWidget)

	// What the accounts are doing is on the left of the status bar, taking
	// the width there is, and the grip to resize the window by is on the
	// right. A message that is shown for a while, as an error, takes the
	// place of the status until it goes.
	w.statusText = qt.NewQLabel2()
	w.statusText.SetAlignment(qt.AlignLeft | qt.AlignVCenter)
	w.statusText.SetContentsMargins(statusPadding, 0, 0, 0)
	w.StatusBar().AddWidget2(w.statusText.QWidget, 1)
	w.StatusBar().SetSizeGripEnabled(true)

	w.folders.selected = w.selectFolder
	w.folders.contextMenu = w.folderMenu
	w.folders.dropped = w.dropMessages
	w.list.selected = w.selectMessages
	w.list.opened = w.openMessage
	w.list.contextMenu = w.messageMenu
	w.view.compose = w.composeNew
	w.view.inBook = w.inAddressBook
	w.view.loadAlways = func() bool {
		e := w.engines[w.account]
		return e != nil && e.Account().LoadRemoteImages
	}
	w.view.addContacts = func(addrs []*mail.Address) { w.showError(w.addContacts(addrs)) }

	w.OnCloseEvent(func(super func(event *qt.QCloseEvent), event *qt.QCloseEvent) {
		if !w.closeComposers() {
			event.Ignore()
			return
		}
		w.saveSettings()
		for _, e := range w.engines {
			e.Close()
		}
		w.closeAddressBook()
		super(event)
	})

	w.updateActions()
	return w
}

// Start lays the window out, which it can only be once it is shown, and opens
// the keys and accounts.
func (w *Window) Start() {
	if w.config.Maximized {
		w.ShowMaximized()
	} else {
		w.Show()
	}
	mainthread.Start(func() {
		w.restoreLayout()
		w.openKeys()
	})
}

// Activate brings the window to the front, as it is when the program is
// started again while it runs. token is the desktop's leave to take the focus
// under Wayland, which is refused without it; Qt reads it from the
// environment when it asks for the focus, and then takes it out.
func (w *Window) Activate(token string) {
	if token != "" {
		os.Setenv("XDG_ACTIVATION_TOKEN", token)
	}
	w.SetWindowState(w.WindowState() &^ qt.WindowMinimized)
	w.Show()
	w.Raise()
	w.ActivateWindow()
}

// restoreLayout gives the folder tree and message list their saved sizes.
func (w *Window) restoreLayout() {
	width := w.config.SidebarWidth
	if width <= 0 {
		width = defaultSidebarWidth
	}
	if sizes := w.split.Sizes(); len(sizes) == 2 && sizes[0]+sizes[1] > width {
		w.split.SetSizes([]int{width, sizes[0] + sizes[1] - width})
	}
	height := w.config.ListHeight
	if height <= 0 {
		height = defaultListHeight
	}
	if sizes := w.right.Sizes(); len(sizes) == 2 && sizes[0]+sizes[1] > height {
		w.right.SetSizes([]int{height, sizes[0] + sizes[1] - height})
	}
}

// saveSettings writes the settings, with the size and layout of the window.
func (w *Window) saveSettings() {
	w.config.Maximized = w.IsMaximized()
	if !w.config.Maximized {
		w.config.Width, w.config.Height = w.Width(), w.Height()
	}
	if sizes := w.split.Sizes(); len(sizes) == 2 && sizes[0] > 0 {
		w.config.SidebarWidth = sizes[0]
	}
	if sizes := w.right.Sizes(); len(sizes) == 2 && sizes[0] > 0 {
		w.config.ListHeight = sizes[0]
	}
	w.config.CorrespondentWidth, w.config.DateWidth = w.list.columnWidths()
	w.saveConfig()
}

// saveConfig writes the settings as they are.
func (w *Window) saveConfig() {
	if err := w.config.Save(w.files.Settings); err != nil {
		fmt.Fprintln(os.Stderr, "apemail: the settings could not be saved:", err)
	}
}

// makeActions makes the actions of the menus and the button bar. A letter,
// number or other character key is never a shortcut on its own, as it is too
// easily pressed by mistake; it needs a modifier. Function keys and special
// keys, as F5 and Delete, can be used alone.
func (w *Window) makeActions() {
	action := func(text, shortcut string, icon *qt.QIcon, triggered func()) *qt.QAction {
		a := qt.NewQAction2(text)
		if icon != nil {
			a.SetIcon(icon)
		}
		if shortcut != "" {
			a.SetShortcut(qt.NewQKeySequence2(shortcut))
		}
		a.OnTriggered(triggered)
		return a
	}
	w.getMail = action("Get Mail", "F5", icon(icons.GetMail), w.refresh)
	w.write = action("Write", "Ctrl+N", icon(icons.Write), func() { w.composeNew("", "") })
	w.reply = action("Reply", "Ctrl+R", icon(icons.Reply), func() { w.replyTo(false) })
	w.replyAll = action("Reply All", "Ctrl+Shift+R", icon(icons.ReplyAll), func() { w.replyTo(true) })
	w.forward = action("Forward", "Ctrl+L", icon(icons.Forward), w.forwardMessage)
	w.archive = action("Archive", "Ctrl+Shift+A", icon(icons.Archive), w.archiveMessages)
	w.del = action("Delete", "Del", icon(icons.Trash), w.deleteMessages)
	w.markRead = action("Mark as Read", "", nil, func() { w.markSeen(true) })
	w.markUnread = action("Mark as Unread", "", nil, func() { w.markSeen(false) })
	w.viewSource = action("View &Source", "Ctrl+U", nil, w.showMessageSource)
	w.addressBook = action("Address Book", "Ctrl+Shift+B", icon(icons.AddressBook), w.showAddressBook)
	w.addressBook.SetEnabled(false)
	w.editAccount = action("Account &Settings…", "", nil, w.editCurrentAccount)
	w.removeAccount = action("&Remove Account…", "", nil, w.removeCurrentAccount)
	w.updateCounts = action("&Update Unread Counts", "", nil, func() { w.updateUnreadCounts(w.currentEngine()) })
	w.syncAll = action(syncAllText, "", nil, func() { w.syncAllFolders(w.currentEngine()) })
}

// makeMenus makes the menu bar.
func (w *Window) makeMenus() {
	bar := w.MenuBar()

	file := bar.AddMenuWithTitle("&File")
	file.AddAction(w.write)
	file.AddAction(w.getMail)
	file.AddAction(w.addressBook)
	file.AddSeparator()
	settingsAction := file.AddActionWithText("&Settings…")
	settingsAction.SetMenuRole(qt.QAction__PreferencesRole)
	settingsAction.SetShortcut(qt.NewQKeySequence2("Ctrl+,"))
	settingsAction.OnTriggered(w.editSettings)
	file.AddSeparator()
	quit := file.AddActionWithText("&Quit")
	quit.SetShortcut(qt.NewQKeySequence2("Ctrl+Q"))
	quit.SetMenuRole(qt.QAction__QuitRole)
	quit.OnTriggered(func() { w.Close() })

	msg := bar.AddMenuWithTitle("&Message")
	msg.AddAction(w.reply)
	msg.AddAction(w.replyAll)
	msg.AddAction(w.forward)
	msg.AddSeparator()
	msg.AddAction(w.markRead)
	msg.AddAction(w.markUnread)
	msg.AddSeparator()
	msg.AddAction(w.archive)
	msg.AddAction(w.del)
	msg.AddSeparator()
	msg.AddAction(w.viewSource)

	accounts := bar.AddMenuWithTitle("&Accounts")
	add := accounts.AddActionWithText("&New Account…")
	add.OnTriggered(func() { w.editAccountDialog(account.New(), true) })
	accounts.AddAction(w.editAccount)
	accounts.AddSeparator()
	accounts.AddAction(w.updateCounts)
	accounts.AddAction(w.syncAll)
	accounts.AddSeparator()
	accounts.AddAction(w.removeAccount)

	help := bar.AddMenuWithTitle("&Help")
	about := help.AddActionWithText("&About")
	about.SetMenuRole(qt.QAction__AboutRole)
	about.SetShortcut(qt.NewQKeySequence2("Ctrl+I"))
	about.OnTriggered(func() { showAbout(w.QWidget) })
}

// makeButtonBar makes the bar of large buttons under the menus.
func (w *Window) makeButtonBar() {
	bar := qt.NewQToolBar4("Buttons", w.QWidget)
	w.buttons = bar
	bar.SetMovable(false)
	bar.SetToolButtonStyle(qt.ToolButtonTextUnderIcon)
	bar.SetIconSize(qt.NewQSize2(32, 32))
	for _, a := range []*qt.QAction{w.getMail, w.write, nil, w.reply, w.replyAll, w.forward, nil, w.archive, nil, w.addressBook, nil, w.del} {
		if a == nil {
			bar.AddSeparator()
			continue
		}
		bar.QWidget.AddAction(a)
	}
	w.AddToolBar(qt.TopToolBarArea, bar)
}

// updateActions enables the actions that can be used on what is selected.
func (w *Window) updateActions() {
	uids := w.list.selectedUIDs()
	one := len(uids) == 1 && w.view.msg != nil
	some := len(uids) > 0
	w.reply.SetEnabled(one)
	w.replyAll.SetEnabled(one)
	w.forward.SetEnabled(one)
	w.viewSource.SetEnabled(one)
	w.archive.SetEnabled(some)
	w.del.SetEnabled(some)
	w.markRead.SetEnabled(some)
	w.markUnread.SetEnabled(some)
	e := w.currentEngine()
	has := e != nil
	w.editAccount.SetEnabled(has)
	w.removeAccount.SetEnabled(has)
	w.updateCounts.SetEnabled(has)
	w.syncAll.SetEnabled(has)
	w.syncAll.SetText(syncAllLabel(e))
	w.write.SetEnabled(len(w.engines) > 0)
	w.getMail.SetEnabled(len(w.engines) > 0)
}

// showStatus shows what the accounts are doing.
func (w *Window) showStatus() {
	var parts []string
	if w.doing != "" {
		parts = append(parts, w.doing)
	}
	for _, id := range w.order {
		if text := w.statuses[id]; text != "" {
			if len(w.order) > 1 {
				text = w.engines[id].Account().Title() + ": " + text
			}
			parts = append(parts, text)
		}
	}
	w.statusText.SetText(strings.Join(parts, "   "))
}

// showError shows what went wrong in the status bar for a while.
func (w *Window) showError(err error) {
	if err == nil {
		return
	}
	fmt.Fprintln(os.Stderr, "apemail:", err)
	w.StatusBar().ShowMessage2(err.Error(), 15000)
}

// warn shows what went wrong in a dialog.
func (w *Window) warn(title string, err error) {
	warning(w.QWidget, title, err.Error())
}

// warning shows what went wrong in a dialog. Tests replace it, so that what
// went wrong is told, rather than left in a dialog no one closes while the
// test waits for what did not happen.
var warning = func(parent *qt.QWidget, title, text string) {
	qt.QMessageBox_Warning(parent, title, text)
}

// confirm asks the user a question, and reports if they said yes. Tests
// replace it rather than let the dialog open.
var confirm = func(parent *qt.QWidget, title, text string) bool {
	return qt.QMessageBox_Question5(parent, title, text, qt.QMessageBox__Yes|qt.QMessageBox__No) == qt.QMessageBox__Yes
}

// FoldersChanged shows the folders of an account.
func (w *Window) FoldersChanged(id string, folders []store.Folder) {
	e := w.engines[id]
	if e == nil {
		return
	}
	w.folders.setFolders(e.Account(), folders)
	if w.account == "" && len(w.order) > 0 && id == w.order[0] {
		// The inbox of the first account is shown when the client
		// starts.
		for _, f := range folders {
			if strings.EqualFold(f.Name, "INBOX") {
				w.folders.selectFolder(id, f.Name)
				break
			}
		}
	}
}

// titlePadding is the room around the title of the folder listed, in
// pixels.
const titlePadding = 6

// showFolderTitle shows the path of the folder listed, and how many
// messages it has once they are listed. Nothing is shown where no folder is.
func (w *Window) showFolderTitle() {
	if w.folder == "" {
		w.folderTitle.SetVisible(false)
		return
	}
	text := "<span style=\"font-size: large; font-weight: bold\">" + html.EscapeString(w.folders.folderPath(w.account, w.folder)) + "</span>"
	if n, ok := w.list.listed(); ok {
		text += "&nbsp;&nbsp;" + messageCount(n)
	}
	w.folderTitle.SetText(text)
	w.folderTitle.SetVisible(true)
}

// messageCount says how many messages there are, as "1 message" or "3
// messages".
func messageCount(n int) string {
	if n == 1 {
		return "1 message"
	}
	return fmt.Sprintf("%d messages", n)
}

// FolderChanged lists the messages of the folder, if it is the one shown.
func (w *Window) FolderChanged(id string, ix *store.Index) {
	w.folders.setUnread(id, ix.Folder, unreadIn(ix))
	if id != w.account || ix.Folder != w.folder {
		return
	}
	w.list.setIndex(ix, w.sentFolder(id, ix.Folder))
	w.updateActions()
}

// syncAllText and stopSyncAllText are what the item that synchronises every
// folder of an account says, when it would start and when it would stop.
const (
	syncAllText     = "&Synchronise All Folders"
	stopSyncAllText = "&Stop Synchronising All Folders"
)

// syncAllLabel returns what the item that synchronises every folder of the
// account says.
func syncAllLabel(e *engine.Engine) string {
	if e != nil && e.SyncingAll() {
		return stopSyncAllText
	}
	return syncAllText
}

// updateUnreadCounts has the server count the unread messages of every
// folder of the account, which opens none of them.
func (w *Window) updateUnreadCounts(e *engine.Engine) {
	if e != nil {
		e.UpdateCounts(w.showError)
	}
}

// syncAllFolders synchronises every folder of the account, though not the
// messages themselves, or stops it if it is being.
func (w *Window) syncAllFolders(e *engine.Engine) {
	if e == nil {
		return
	}
	if e.SyncingAll() {
		e.StopSyncAll()
		return
	}
	title := e.Account().Title()
	e.SyncAll(func(err error) {
		switch {
		case errors.Is(err, engine.ErrStopped):
			w.StatusBar().ShowMessage2("Stopped synchronising the folders of "+title+".", 5000)
		case err != nil:
			w.showError(err)
		default:
			w.StatusBar().ShowMessage2("Every folder of "+title+" is synchronised.", 5000)
		}
		w.updateActions()
	})
	w.updateActions()
}

// unreadIn returns how many messages of the index are unread.
func unreadIn(ix *store.Index) int {
	unread := 0
	for _, m := range ix.Messages {
		if !m.Has(`\Seen`) {
			unread++
		}
	}
	return unread
}

// UnreadCounts shows how many messages of each of the folders are unread.
func (w *Window) UnreadCounts(id string, unread map[string]int) {
	for folder, n := range unread {
		w.folders.setUnread(id, folder, n)
	}
}

// sentFolder reports if the folder holds mail from the user, whose list
// shows who it went to rather than who it is from.
func (w *Window) sentFolder(id, folder string) bool {
	e := w.engines[id]
	if e == nil {
		return false
	}
	return folder == e.SpecialFolder(engine.Sent) || folder == e.SpecialFolder(engine.Drafts)
}

// Status shows what an account is doing.
func (w *Window) Status(id, text string) {
	// What an engine closed since says, as the accounts are stopped while
	// the settings are applied, is no longer so.
	if w.engines[id] == nil {
		return
	}
	w.statuses[id] = text
	w.showStatus()
}

// setDoing shows what the window itself is doing, or that it is done if text
// is empty.
func (w *Window) setDoing(text string) {
	w.doing = text
	w.showStatus()
}

// Failed shows what went wrong in an account.
func (w *Window) Failed(id string, err error) {
	if e := w.engines[id]; e != nil && len(w.order) > 1 {
		err = fmt.Errorf("%s: %w", e.Account().Title(), err)
	}
	w.showError(err)
}

// currentEngine returns the engine of the account that is selected.
func (w *Window) currentEngine() *engine.Engine {
	if e := w.engines[w.account]; e != nil {
		return e
	}
	if len(w.order) > 0 {
		return w.engines[w.order[0]]
	}
	return nil
}

// selectFolder lists the messages of a folder.
func (w *Window) selectFolder(id, folder string) {
	if id == w.account && folder == w.folder {
		return
	}
	w.account, w.folder = id, folder
	w.list.clear()
	w.list.account, w.list.folder = id, folder
	w.view.clear()
	w.updateActions()
	if e := w.engines[id]; e != nil && folder != "" {
		e.Open(folder)
	}
}

// refresh checks the folder that is selected for new mail, or the inbox of
// the account if the account itself is.
func (w *Window) refresh() {
	if e := w.currentEngine(); e != nil {
		e.Refresh(w.folder)
	}
}

// selectMessages shows the message that is selected, if only one is.
func (w *Window) selectMessages() {
	uids := w.list.selectedUIDs()
	if len(uids) != 1 {
		w.view.clear()
		w.updateActions()
		return
	}
	w.showMessage(uids[0])
	w.updateActions()
}

// showMessage shows the message with the UID in the folder that is listed,
// marks it as read, and asks to send the return receipt it asks for.
func (w *Window) showMessage(uid uint32) {
	e := w.engines[w.account]
	if e == nil {
		return
	}
	id, folder := w.account, w.folder
	w.view.loading()
	e.Message(folder, uid, func(raw []byte, err error) {
		// What arrives after another message was chosen is not shown.
		if id != w.account || folder != w.folder || !slices.Equal(w.list.selectedUIDs(), []uint32{uid}) {
			return
		}
		if err != nil {
			w.view.failed(err)
			w.updateActions()
			return
		}
		// The message is read away from the UI's thread, as a large
		// one takes a while.
		go func() {
			m, err := message.Parse(raw)
			mainthread.Start(func() {
				if id != w.account || folder != w.folder || !slices.Equal(w.list.selectedUIDs(), []uint32{uid}) {
					return
				}
				if err != nil {
					w.view.failed(err)
				} else {
					w.view.show(m, folder, uid)
					w.markShownRead(uid)
					w.askReceipt(m, uid)
				}
				w.updateActions()
			})
		}()
	})
}

// markShownRead marks the message that is shown as read.
func (w *Window) markShownRead(uid uint32) {
	s, ok := w.list.summary(uid)
	if !ok || s.Has(`\Seen`) {
		return
	}
	if e := w.engines[w.account]; e != nil {
		e.SetFlag(w.folder, []uint32{uid}, `\Seen`, true, w.showError)
	}
}

// openMessage opens a message in the drafts folder to be finished.
func (w *Window) openMessage(uid uint32) {
	e := w.engines[w.account]
	if e == nil || w.folder != e.SpecialFolder(engine.Drafts) || w.view.msg == nil || w.view.uid != uid {
		return
	}
	w.composeDraft(e, w.view.msg, w.folder, uid)
}

// markSeen marks the messages that are selected as read or unread.
func (w *Window) markSeen(read bool) {
	e := w.engines[w.account]
	uids := w.list.selectedUIDs()
	if e == nil || len(uids) == 0 {
		return
	}
	e.SetFlag(w.folder, uids, `\Seen`, read, func(err error) {
		if err != nil {
			w.warn("Marking Messages", err)
		}
	})
}

// archiveMessages moves the messages that are selected to the archive,
// which is made on the server if there is none.
func (w *Window) archiveMessages() {
	e := w.engines[w.account]
	uids := w.list.selectedUIDs()
	if e == nil || len(uids) == 0 || e.InArchive(w.folder) {
		return
	}
	w.list.selectNextLater()
	e.Archive(w.folder, uids, func(err error) {
		if err != nil {
			w.list.next = 0
			w.warn("Archive", err)
		}
	})
}

// deleteMessages moves the messages that are selected to the trash, which
// is made on the server if there is none, without asking, as nothing is
// lost. Mail that is already in the trash is deleted for good, which the
// user is asked to confirm, as it cannot be undone.
func (w *Window) deleteMessages() {
	e := w.engines[w.account]
	uids := w.list.selectedUIDs()
	if e == nil || len(uids) == 0 {
		return
	}
	done := func(err error) {
		if err != nil {
			w.list.next = 0
			w.warn("Delete", err)
		}
	}
	// The message after those deleted is selected once they have gone,
	// so that it can be deleted too with another press.
	if w.folder != e.SpecialFolder(engine.Trash) {
		w.list.selectNextLater()
		e.Trash(w.folder, uids, done)
		return
	}
	what := "this message"
	if len(uids) > 1 {
		what = fmt.Sprintf("these %d messages", len(uids))
	}
	if confirm(w.QWidget, "Delete for Good",
		fmt.Sprintf("Delete %s from %s for good?\n\nThey will be removed from the server, and this cannot be undone.", what, w.folder)) {
		w.list.selectNextLater()
		e.Delete(w.folder, uids, done)
	}
}

// folderMenu shows the menu of an account or a folder in the side bar.
func (w *Window) folderMenu(id, name, folder string) {
	if menu := w.sidebarMenu(id, name, folder); menu != nil {
		popUp(menu)
	}
}

// sidebarMenu returns the menu of the account, if name is empty, or of the
// folder with the name, which is opened as folder (empty for one that only
// holds others). It is nil if the account has gone.
func (w *Window) sidebarMenu(id, name, folder string) *qt.QMenu {
	e := w.engines[id]
	if e == nil {
		return nil
	}
	menu := qt.NewQMenu2()
	// Get Mail checks the folder, or the inbox of the account.
	if name == "" {
		menu.AddActionWithText("Get Mail for " + e.Account().Title()).OnTriggered(func() { e.Refresh("") })
	} else if folder != "" {
		menu.AddActionWithText("Get Mail in " + folder).OnTriggered(func() { e.Refresh(folder) })
	}
	menu.AddActionWithText("Update Unread Counts").OnTriggered(func() { w.updateUnreadCounts(e) })
	menu.AddActionWithText(syncAllLabel(e)).OnTriggered(func() { w.syncAllFolders(e) })
	menu.AddSeparator()
	if name == "" {
		menu.AddActionWithText("New Folder…").OnTriggered(func() { w.newFolderDialog(e) })
	} else {
		del := menu.AddActionWithText("Delete Folder…")
		del.OnTriggered(func() { w.deleteFolder(e, name) })
		// The inbox, and the folders the account uses, are kept.
		if err := e.Deletable(name); err != nil {
			del.SetEnabled(false)
		}
	}
	menu.AddSeparator()
	menu.AddActionWithText("Account Settings…").OnTriggered(func() { w.editAccountDialog(e.Account(), false) })
	return menu
}

// messageMenu shows the menu of the messages that are selected.
func (w *Window) messageMenu() {
	menu := qt.NewQMenu2()
	for _, a := range []*qt.QAction{w.reply, w.replyAll, w.forward, nil, w.markRead, w.markUnread, nil, w.archive, w.del} {
		if a == nil {
			menu.AddSeparator()
		} else {
			menu.QWidget.AddAction(a)
		}
	}
	popUp(menu)
}

// popUp shows the menu where the mouse is, and deletes it once it is closed.
func popUp(menu *qt.QMenu) {
	at := qt.QCursor_Pos()
	menu.Exec3(at, nil)
	runtime.KeepAlive(at)
	menu.DeleteLater()
}
