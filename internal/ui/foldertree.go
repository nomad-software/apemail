package ui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/nomad-software/qute/qtcore"
	"github.com/nomad-software/qute/qtgui"
	"github.com/nomad-software/qute/qtwidgets"

	"github.com/nomad-software/apemail/internal/account"
	"github.com/nomad-software/apemail/internal/engine"
	"github.com/nomad-software/apemail/internal/icons"
	"github.com/nomad-software/apemail/internal/remote"
	"github.com/nomad-software/apemail/internal/settings"
	"github.com/nomad-software/apemail/internal/store"
)

// Roles of the data of the items of the tree.
const (
	roleAccount = int(qtcore.UserRole) + iota
	// roleFolder is the name of a folder that holds messages, and is empty
	// for an account or a folder that does not.
	roleFolder
	// roleName is the name of every folder, and is empty for an account.
	roleName
)

// folderTree is the sidebar, which lists each account and the folders of its
// mailbox below it.
type folderTree struct {
	qtwidgets.QTreeWidgetBase

	// accounts are the items of the accounts, by ID, and folders those of
	// their folders, by ID and then name.
	accounts map[string]*qtwidgets.QTreeWidgetItem
	folders  map[string]map[string]*qtwidgets.QTreeWidgetItem
	// names are the names the folders are shown by, without the number of
	// unread messages.
	names map[*qtwidgets.QTreeWidgetItem]string
	// unread are the numbers of unread messages of the folders, by ID and
	// then name, which are shown again when the tree is made again.
	unread map[string]map[string]int
	// expanded says which items are open and which closed, as the settings
	// keep it, so that they are as they were left when the tree is made
	// again, and when the program starts.
	expanded map[string]map[string]bool

	// selected is called with the account and folder that are selected.
	// The folder is empty if it is an account that is.
	selected func(id, folder string)
	// contextMenu is called for the menu of an account or folder, with
	// the name of the folder, which is empty for an account, and the
	// folder to open, which is empty too for one that only holds others.
	contextMenu func(id, name, folder string)
	// dropped is called with messages dropped on a folder of an account,
	// and reports if they are moved there.
	dropped func(d draggedMessages, id, folder string) bool
}

// newFolderTree makes the tree, which keeps which of its items are open in
// expanded.
func newFolderTree(expanded map[string]map[string]bool) *folderTree {
	t := &folderTree{
		accounts: map[string]*qtwidgets.QTreeWidgetItem{},
		folders:  map[string]map[string]*qtwidgets.QTreeWidgetItem{},
		names:    map[*qtwidgets.QTreeWidgetItem]string{},
		unread:   map[string]map[string]int{},
		expanded: expanded,
	}
	qtwidgets.NewQTreeWidgetWith(t, nil)
	t.SetHeaderHidden(true)
	t.SetColumnCount(1)
	t.SetMinimumWidth(150)
	t.OnCurrentItemChanged(func(current, _ *qtwidgets.QTreeWidgetItem) {
		if current == nil || t.selected == nil {
			return
		}
		t.selected(itemData(current, roleAccount), itemData(current, roleFolder))
	})
	// Messages from the message list are dropped on a folder to move them
	// there. A drop is always on a folder, never between two.
	t.SetDragDropMode(qtwidgets.QAbstractItemViewDropOnly)
	t.SetDragDropOverwriteMode(true)
	t.SetDropIndicatorShown(true)

	t.OnItemExpanded(func(item *qtwidgets.QTreeWidgetItem) { t.remember(item, true) })
	t.OnItemCollapsed(func(item *qtwidgets.QTreeWidgetItem) { t.remember(item, false) })

	t.SetContextMenuPolicy(qtcore.CustomContextMenu)
	t.OnCustomContextMenuRequested(func(pos qtcore.QPoint) {
		item := t.ItemAt(pos)
		if item == nil || t.contextMenu == nil {
			return
		}
		t.contextMenu(itemData(item, roleAccount), itemData(item, roleName), itemData(item, roleFolder))
	})
	return t
}

// What Qt asks of the tree while messages are dragged over it and dropped.

func (t *folderTree) MimeTypes() []string { return []string{messagesMime} }

func (t *folderTree) SupportedDropActions() qtcore.DropActions { return qtcore.MoveAction }

func (t *folderTree) DragMoveEvent(event *qtgui.QDragMoveEvent) {
	t.Super().DragMoveEvent(event)
	// A folder the messages cannot go to is shown as refusing them.
	d, ok := decodeDragged(event.MimeData())
	item := t.ItemAt(event.Position().ToPoint())
	if !ok || item == nil || canDrop(d, itemData(item, roleAccount), itemData(item, roleFolder)) != nil {
		event.QEvent.Ignore()
	}
}

func (t *folderTree) DropMimeData(parent *qtwidgets.QTreeWidgetItem, _ int, data *qtcore.QMimeData, _ qtcore.DropActions) bool {
	d, ok := decodeDragged(data)
	if !ok || parent == nil || t.dropped == nil {
		return false
	}
	return t.dropped(d, itemData(parent, roleAccount), itemData(parent, roleFolder))
}

// itemData returns the text of the item's data in the role.
func itemData(item *qtwidgets.QTreeWidgetItem, role int) string {
	return item.Data(0, role).ToString()
}

// remember keeps whether the item is open.
func (t *folderTree) remember(item *qtwidgets.QTreeWidgetItem, open bool) {
	id := itemData(item, roleAccount)
	if t.expanded[id] == nil {
		t.expanded[id] = map[string]bool{}
	}
	t.expanded[id][settings.FolderKey(itemData(item, roleName))] = open
}

// restore opens or closes the item as it was left, or as it starts if it
// never was: an account open, and a folder closed.
func (t *folderTree) restore(item *qtwidgets.QTreeWidgetItem) {
	id, name := itemData(item, roleAccount), itemData(item, roleName)
	open, ok := t.expanded[id][settings.FolderKey(name)]
	if !ok {
		open = name == ""
	}
	item.SetExpanded(open)
}

// setAccount adds the account, or changes what it is shown as.
func (t *folderTree) setAccount(a account.Account) {
	item, ok := t.accounts[a.ID]
	if !ok {
		item = qtwidgets.NewQTreeWidgetItem()
		item.SetData(0, roleAccount, qtcore.NewQVariantWithString(a.ID))
		item.SetData(0, roleFolder, qtcore.NewQVariantWithString(""))
		item.SetData(0, roleName, qtcore.NewQVariantWithString(""))
		item.SetFlags(qtcore.ItemIsSelectable | qtcore.ItemIsEnabled)
		item.SetIcon(0, sidebarIcon(icons.Account))
		font := item.Font(0)
		font.SetBold(true)
		item.SetFont(0, font)
		t.AddTopLevelItem(item)
		t.restore(item)
		t.accounts[a.ID] = item
	}
	item.SetText(0, a.Title())
}

// removeAccount takes the account out.
func (t *folderTree) removeAccount(id string) {
	item, ok := t.accounts[id]
	if !ok {
		return
	}
	t.forgetChildren(item)
	delete(t.accounts, id)
	delete(t.folders, id)
	delete(t.unread, id)
	delete(t.expanded, id)
	t.TakeTopLevelItem(t.IndexOfTopLevelItem(item))
}

// clear takes every account out.
func (t *folderTree) clear() {
	t.Clear()
	t.accounts = map[string]*qtwidgets.QTreeWidgetItem{}
	t.folders = map[string]map[string]*qtwidgets.QTreeWidgetItem{}
	t.names = map[*qtwidgets.QTreeWidgetItem]string{}
	t.unread = map[string]map[string]int{}
}

// forgetChildren forgets the items below an item, which are being removed.
func (t *folderTree) forgetChildren(item *qtwidgets.QTreeWidgetItem) {
	for i := range item.ChildCount() {
		child := item.Child(i)
		t.forgetChildren(child)
		delete(t.names, child)
	}
}

// setFolders shows the folders of the account, nested as their names say,
// keeping the one that is selected.
func (t *folderTree) setFolders(a account.Account, folders []store.Folder) {
	t.setAccount(a)
	parent := t.accounts[a.ID]

	selectedFolder := ""
	if cur := t.CurrentItem(); cur != nil && itemData(cur, roleAccount) == a.ID {
		selectedFolder = itemData(cur, roleFolder)
	}
	// The folder selected stays so, without being opened again, as the
	// tree selects another while it is made again: that would empty the
	// list, and lose the message shown, each time the folders are listed.
	announce := t.selected
	t.selected = nil

	t.forgetChildren(parent)
	for _, child := range parent.TakeChildren() {
		child.Delete()
	}
	items := map[string]*qtwidgets.QTreeWidgetItem{}
	t.folders[a.ID] = items

	uses := folderUses(a.Folders, folders)
	ordered := orderFolders(folders, uses)

	// Every folder is made first, and then each is put below the folder
	// its name is within, if that is listed, so that a folder comes out
	// below its parent whatever order they are listed in.
	for _, f := range ordered {
		item := qtwidgets.NewQTreeWidgetItem()
		item.SetData(0, roleAccount, qtcore.NewQVariantWithString(a.ID))
		item.SetData(0, roleName, qtcore.NewQVariantWithString(f.Name))
		if f.Selectable() {
			item.SetData(0, roleFolder, qtcore.NewQVariantWithString(f.Name))
			item.SetFlags(qtcore.ItemIsSelectable | qtcore.ItemIsEnabled | qtcore.ItemIsDropEnabled)
		} else {
			item.SetData(0, roleFolder, qtcore.NewQVariantWithString(""))
			item.SetFlags(qtcore.ItemIsSelectable | qtcore.ItemIsEnabled)
		}
		item.SetToolTip(0, f.Name)
		item.SetIcon(0, folderIcon(f, uses))
		items[f.Name] = item
	}
	for _, f := range ordered {
		item, under, name := items[f.Name], parent, f.Name
		if f.Delim != "" {
			if i := strings.LastIndex(f.Name, f.Delim); i > 0 {
				if p, ok := items[f.Name[:i]]; ok {
					under, name = p, f.Name[i+len(f.Delim):]
				}
			}
		}
		under.AddChild(item)
		// A folder for a use is shown by the use, whatever the server
		// calls it, so that every account has the same: Archives is
		// shown as Archive.
		if remote.IsInbox(f.Name) {
			name = "Inbox"
		} else if use, ok := uses[f.Name]; ok {
			name = use.Name()
		}
		t.names[item] = name
		item.SetText(0, name)
		if n, ok := t.unread[a.ID][f.Name]; ok {
			showUnread(item, name, n)
		}
	}
	// The counts of folders that are gone are forgotten.
	for folder := range t.unread[a.ID] {
		if _, ok := items[folder]; !ok {
			delete(t.unread[a.ID], folder)
		}
	}
	// Each folder is opened or closed as it was left, once all are in
	// place, and what is kept of folders that are gone is forgotten.
	keys := map[string]bool{"": true}
	for _, f := range ordered {
		t.restore(items[f.Name])
		keys[settings.FolderKey(f.Name)] = true
	}
	for key := range t.expanded[a.ID] {
		if !keys[key] {
			delete(t.expanded[a.ID], key)
		}
	}
	item, kept := items[selectedFolder]
	if kept {
		t.SetCurrentItem(item)
	}
	t.selected = announce
	// A folder that has gone is no longer what is listed.
	if selectedFolder != "" && !kept && t.selected != nil {
		if cur := t.CurrentItem(); cur != nil {
			t.selected(itemData(cur, roleAccount), itemData(cur, roleFolder))
		} else {
			t.selected("", "")
		}
	}
}

// sidebarUses are the uses a folder can have, in the order the side bar has
// the folders for them, after the inbox, as other clients do.
var sidebarUses = []engine.Special{engine.Drafts, engine.Sent, engine.Archive, engine.Trash}

// folderUses returns the special use of each folder that has one, by name:
// the folders the account's settings choose, or else those found as the
// engine finds them.
func folderUses(chosen account.Folders, folders []store.Folder) map[string]engine.Special {
	uses := map[string]engine.Special{}
	for _, use := range sidebarUses {
		if name := engine.SpecialIn(chosen, folders, use); name != "" {
			uses[name] = use
		}
	}
	return uses
}

// orderFolders puts the inbox and the special folders first, in the order
// other clients have them, and the rest after in the order they are listed
// in, which is that of their names.
func orderFolders(folders []store.Folder, uses map[string]engine.Special) []store.Folder {
	rank := func(f store.Folder) int {
		if remote.IsInbox(f.Name) {
			return 0
		}
		if use, ok := uses[f.Name]; ok {
			return 1 + slices.Index(sidebarUses, use)
		}
		return 1 + len(sidebarUses)
	}
	ordered := slices.Clone(folders)
	slices.SortStableFunc(ordered, func(a, b store.Folder) int { return rank(a) - rank(b) })
	return ordered
}

// sidebarTints are the colours of the icons of the side bar, so that each
// kind of folder is told at a glance, as in other clients.
var sidebarTints = map[string]tint{
	icons.Account: {light: "#1a73e8", dark: "#6ea8fe"},
	icons.Inbox:   {light: "#1a73e8", dark: "#6ea8fe"},
	icons.Drafts:  {light: "#8e44ad", dark: "#c084fc"},
	icons.Sent:    {light: "#188038", dark: "#5cc77d"},
	icons.Archive: {light: "#a0622d", dark: "#d9a066"},
	icons.Folder:  {light: "#c99400", dark: "#f2c94c"},
	icons.Trash:   {light: "#6b6b6b", dark: "#a8a8a8"},
}

// sidebarIcon returns the icon with the name as the side bar shows it, in
// its colour if it has one.
func sidebarIcon(name string) *qtgui.QIcon {
	if t, ok := sidebarTints[name]; ok {
		return tintedIcon(name, t)
	}
	return icon(name)
}

// folderIcon returns the icon of a folder, by its use.
func folderIcon(f store.Folder, uses map[string]engine.Special) *qtgui.QIcon {
	if remote.IsInbox(f.Name) {
		return sidebarIcon(icons.Inbox)
	}
	use, ok := uses[f.Name]
	switch {
	case !ok:
		return sidebarIcon(icons.Folder)
	case use == engine.Sent:
		return sidebarIcon(icons.Sent)
	case use == engine.Drafts:
		return sidebarIcon(icons.Drafts)
	case use == engine.Trash:
		return sidebarIcon(icons.Trash)
	}
	return sidebarIcon(icons.Archive)
}

// folderPath returns the folder as the tree shows it, without counts of
// unread messages: the name it shows the folder by (Inbox, the name of its
// use, as Archive, or the last part of its name), after those of the
// folders it is within, each followed by /, as Archive/2026.
func (t *folderTree) folderPath(id, folder string) string {
	item, ok := t.folders[id][folder]
	if !ok {
		return folder
	}
	path := t.names[item]
	// The account, above them all, has no name.
	for parent := item.Parent(); parent != nil; parent = parent.Parent() {
		name, ok := t.names[parent]
		if !ok {
			break
		}
		path = name + "/" + path
	}
	return path
}

// setUnread shows how many messages of the folder are unread, and keeps it
// to be shown again when the tree is made again.
func (t *folderTree) setUnread(id, folder string, unread int) {
	item, ok := t.folders[id][folder]
	if !ok {
		return
	}
	if t.unread[id] == nil {
		t.unread[id] = map[string]int{}
	}
	t.unread[id][folder] = unread
	showUnread(item, t.names[item], unread)
}

// showUnread shows the item of a folder, of the name, with how many of its
// messages are unread: in bold and counted, if any is.
func showUnread(item *qtwidgets.QTreeWidgetItem, name string, unread int) {
	text := name
	if unread > 0 {
		text = fmt.Sprintf("%s (%d)", name, unread)
	}
	item.SetText(0, text)
	font := item.Font(0)
	font.SetBold(unread > 0)
	item.SetFont(0, font)
}

// selectFolder selects the folder of the account.
func (t *folderTree) selectFolder(id, folder string) {
	if item, ok := t.folders[id][folder]; ok {
		t.SetCurrentItem(item)
	}
}
