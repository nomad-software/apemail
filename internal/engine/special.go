package engine

import (
	"strings"

	"github.com/nomad-software/apemail/internal/account"
	"github.com/nomad-software/apemail/internal/remote"
	"github.com/nomad-software/apemail/internal/store"
)

// Special is a use a folder can have.
type Special int

// specialUses are the attribute RFC 6154 gives each use, which a server
// marks the folder for it with, and the usual names of the folder, which it
// is found by on a server that marks none: the names other clients and
// servers give them, as Outlook, Gmail and Dovecot. The first name is the one
// the folder is made with if the mailbox has none.
var specialUses = map[Special]struct {
	attr  string
	names []string
}{
	Sent:    {`\Sent`, []string{"Sent", "Sent Items", "Sent Mail", "Sent Messages"}},
	Drafts:  {`\Drafts`, []string{"Drafts", "Draft"}},
	Trash:   {`\Trash`, []string{"Trash", "Deleted Items", "Deleted Messages", "Bin", "Deleted"}},
	Archive: {`\Archive`, []string{"Archive", "Archives"}},
}

// useNames name the uses, as pending work records them.
var useNames = map[Special]string{Sent: "sent", Drafts: "drafts", Trash: "trash", Archive: "archive"}

// Name returns the name of the use, which the folder for it is made with
// if the mailbox has none and shown by in the side bar whatever the server
// calls it: Sent, Drafts, Trash or Archive.
func (use Special) Name() string {
	return specialUses[use].names[0]
}

// FolderFor returns the folder with the use, or, if there is none, the name
// it will be made with when it is first needed.
func (e *Engine) FolderFor(use Special) string {
	if f := e.SpecialFolder(use); f != "" {
		return f
	}
	return use.Name()
}

// ensureFolder returns the folder with the use, making it on the server if
// it is not there: the folder the account names, or the one found by its
// use, or one with the preset name in the user's own namespace. It is run by
// the op worker, with its connection.
func (e *Engine) ensureFolder(c *remote.Conn, use Special) (string, error) {
	name := e.SpecialFolder(use)
	if name != "" && e.hasFolder(name) {
		return name, nil
	}
	if name == "" {
		name = c.PersonalPrefix() + use.Name()
	}
	if err := c.Create(name, specialUses[use].attr); err != nil {
		return "", err
	}

	e.addFolder(store.Folder{Name: name, Delim: e.delim(name), Attrs: []string{specialUses[use].attr}})
	return name, nil
}

// SpecialFolder returns the folder of the account with the use, as
// SpecialIn does.
func (e *Engine) SpecialFolder(use Special) string {
	return SpecialIn(e.acct.Folders, e.Folders(), use)
}

// SpecialIn returns the folder with the use: the one the account's settings
// name, or else the one among the folders FindSpecial finds. It is empty if
// there is none.
func SpecialIn(chosen account.Folders, folders []store.Folder, use Special) string {
	var named string
	switch use {
	case Sent:
		named = chosen.Sent
	case Drafts:
		named = chosen.Drafts
	case Trash:
		named = chosen.Trash
	case Archive:
		named = chosen.Archive
	}
	if named != "" {
		return named
	}
	return FindSpecial(folders, use)
}

// FindSpecial returns the folder with the use among the folders: the one
// marked with it, or one with a usual name for it, which can be within
// another, as INBOX.Sent is. It is empty if there is none.
func FindSpecial(folders []store.Folder, use Special) string {
	u := specialUses[use]
	for _, f := range folders {
		if f.Has(u.attr) {
			return f.Name
		}
	}
	for _, name := range u.names {
		for _, f := range folders {
			leaf := f.Name
			if f.Delim != "" {
				parts := strings.Split(f.Name, f.Delim)
				leaf = parts[len(parts)-1]
			}
			if strings.EqualFold(leaf, name) && f.Selectable() {
				return f.Name
			}
		}
	}
	return ""
}
