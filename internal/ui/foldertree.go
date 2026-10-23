package ui

import (
	"fmt"
	"slices"
	"strings"

	qt "github.com/mappu/miqt/qt6"

	"github.com/nomad-software/apemail/internal/account"
	"github.com/nomad-software/apemail/internal/engine"
	"github.com/nomad-software/apemail/internal/icons"
	"github.com/nomad-software/apemail/internal/remote"
	"github.com/nomad-software/apemail/internal/settings"
	"github.com/nomad-software/apemail/internal/store"
)

// Roles of the data of the items of the tree.
const (
	roleAccount = int(qt.UserRole) + iota
	// roleFolder is the name of a folder that holds messages, and is empty
	// for an account or a folder that does not.
	roleFolder
	// roleName is the name of every folder, and is empty for an account.
	roleName
)

// folderTree is the sidebar, which lists each account and the folders of its
// mailbox below it.
type folderTree struct {
	*qt.QTreeWidget

	// accounts are the items of the accounts, by ID, and folders those of
	// their folders, by ID and then name.
	accounts map[string]*qt.QTreeWidgetItem
	folders  map[string]map[string]*qt.QTreeWidgetItem
	// names are the names the folders are shown by, without the number of
	// unread messages.
	names map[*qt.QTreeWidgetItem]string
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
	// contextMenu is called for the menu of an account or folder.
	contextMenu func(id, folder string)
	// dropped is called with messages dropped on a folder of an account,
	// and reports if they are moved there.
	dropped func(d draggedMessages, id, folder string) bool
}

// newFolderTree makes the tree, which keeps which of its items are open in
// expanded.
func newFolderTree(expanded map[string]map[string]bool) *folderTree {
	t := &folderTree{
		QTreeWidget: qt.NewQTreeWidget2(),
		accounts:    map[string]*qt.QTreeWidgetItem{},
		folders:     map[string]map[string]*qt.QTreeWidgetItem{},
		names:       map[*qt.QTreeWidgetItem]string{},
		unread:      map[string]map[string]int{},
		expanded:    expanded,
	}
	t.SetHeaderHidden(true)
	t.SetColumnCount(1)
	t.SetMinimumWidth(150)
	t.OnCurrentItemChanged(func(current, _ *qt.QTreeWidgetItem) {
		if current == nil || t.selected == nil {
			return
		}
		t.selected(current.Data(0, roleAccount).ToString(), current.Data(0, roleFolder).ToString())
	})
	// Messages from the message list are dropped on a folder to move them
	// there. A drop is always on a folder, never between two.
	t.SetDragDropMode(qt.QAbstractItemView__DropOnly)
	t.SetDragDropOverwriteMode(true)
	t.SetDropIndicatorShown(true)
	t.OnMimeTypes(func(func() []string) []string { return []string{messagesMime} })
	t.OnSupportedDropActions(func(func() qt.DropAction) qt.DropAction { return qt.MoveAction })
	t.OnDragMoveEvent(func(super func(*qt.QDragMoveEvent), event *qt.QDragMoveEvent) {
		super(event)
		// A folder the messages cannot go to is shown as refusing them.
		d, ok := decodeDragged(event.MimeData())
		item := t.ItemAt(event.Position().ToPoint())
		if !ok || item == nil || canDrop(d, item.Data(0, roleAccount).ToString(), item.Data(0, roleFolder).ToString()) != nil {
			event.Ignore()
		}
	})
	t.OnDropMimeData(func(_ func(*qt.QTreeWidgetItem, int, *qt.QMimeData, qt.DropAction) bool, parent *qt.QTreeWidgetItem, _ int, data *qt.QMimeData, _ qt.DropAction) bool {
		d, ok := decodeDragged(data)
		if !ok || parent == nil || t.dropped == nil {
			return false
		}
		return t.dropped(d, parent.Data(0, roleAccount).ToString(), parent.Data(0, roleFolder).ToString())
	})

	t.OnItemExpanded(func(item *qt.QTreeWidgetItem) { t.remember(item, true) })
	t.OnItemCollapsed(func(item *qt.QTreeWidgetItem) { t.remember(item, false) })

	t.SetContextMenuPolicy(qt.CustomContextMenu)
	t.OnCustomContextMenuRequested(func(pos *qt.QPoint) {
		item := t.ItemAt(pos)
		if item == nil || t.contextMenu == nil {
			return
		}
		t.contextMenu(item.Data(0, roleAccount).ToString(), item.Data(0, roleFolder).ToString())
	})
	return t
}

// remember keeps whether the item is open.
func (t *folderTree) remember(item *qt.QTreeWidgetItem, open bool) {
	id := item.Data(0, roleAccount).ToString()
	if t.expanded[id] == nil {
		t.expanded[id] = map[string]bool{}
	}
	t.expanded[id][settings.FolderKey(item.Data(0, roleName).ToString())] = open
}

// restore opens or closes the item as it was left, or as it starts if it
// never was: an account open, and a folder closed.
func (t *folderTree) restore(item *qt.QTreeWidgetItem) {
	id, name := item.Data(0, roleAccount).ToString(), item.Data(0, roleName).ToString()
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
		item = qt.NewQTreeWidgetItem()
		item.SetData(0, roleAccount, qt.NewQVariant11(a.ID))
		item.SetData(0, roleFolder, qt.NewQVariant11(""))
		item.SetData(0, roleName, qt.NewQVariant11(""))
		item.SetFlags(qt.ItemIsSelectable | qt.ItemIsEnabled)
		item.SetIcon(0, icon(icons.Account))
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
	t.accounts = map[string]*qt.QTreeWidgetItem{}
	t.folders = map[string]map[string]*qt.QTreeWidgetItem{}
	t.names = map[*qt.QTreeWidgetItem]string{}
	t.unread = map[string]map[string]int{}
}

// forgetChildren forgets the items below an item, which are being removed.
func (t *folderTree) forgetChildren(item *qt.QTreeWidgetItem) {
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
	if cur := t.CurrentItem(); cur != nil && cur.Data(0, roleAccount).ToString() == a.ID {
		selectedFolder = cur.Data(0, roleFolder).ToString()
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
	items := map[string]*qt.QTreeWidgetItem{}
	t.folders[a.ID] = items

	uses := folderUses(folders)
	ordered := orderFolders(folders, uses)

	// Every folder is made first, and then each is put below the folder
	// its name is within, if that is listed, so that a folder comes out
	// below its parent whatever order they are listed in.
	for _, f := range ordered {
		item := qt.NewQTreeWidgetItem()
		item.SetData(0, roleAccount, qt.NewQVariant11(a.ID))
		item.SetData(0, roleName, qt.NewQVariant11(f.Name))
		if f.Selectable() {
			item.SetData(0, roleFolder, qt.NewQVariant11(f.Name))
			item.SetFlags(qt.ItemIsSelectable | qt.ItemIsEnabled | qt.ItemIsDropEnabled)
		} else {
			item.SetData(0, roleFolder, qt.NewQVariant11(""))
			item.SetFlags(qt.ItemIsSelectable | qt.ItemIsEnabled)
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
		if remote.IsInbox(f.Name) {
			name = "Inbox"
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
			t.selected(cur.Data(0, roleAccount).ToString(), cur.Data(0, roleFolder).ToString())
		} else {
			t.selected("", "")
		}
	}
}

// folderUses returns the special use of each folder that has one, by name.
func folderUses(folders []store.Folder) map[string]engine.Special {
	uses := map[string]engine.Special{}
	for _, use := range []engine.Special{engine.Drafts, engine.Sent, engine.Archive, engine.Trash} {
		if name := engine.FindSpecial(folders, use); name != "" {
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
			return 1 + slices.Index([]engine.Special{engine.Drafts, engine.Sent, engine.Archive, engine.Trash}, use)
		}
		return 5
	}
	ordered := slices.Clone(folders)
	slices.SortStableFunc(ordered, func(a, b store.Folder) int { return rank(a) - rank(b) })
	return ordered
}

// folderIcon returns the icon of a folder, by its use.
func folderIcon(f store.Folder, uses map[string]engine.Special) *qt.QIcon {
	if remote.IsInbox(f.Name) {
		return icon(icons.Inbox)
	}
	use, ok := uses[f.Name]
	switch {
	case !ok:
		return icon(icons.Folder)
	case use == engine.Sent:
		return icon(icons.Sent)
	case use == engine.Drafts:
		return icon(icons.Drafts)
	case use == engine.Trash:
		return icon(icons.Trash)
	}
	return icon(icons.Archive)
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
func showUnread(item *qt.QTreeWidgetItem, name string, unread int) {
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
