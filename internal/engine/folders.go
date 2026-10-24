package engine

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/nomad-software/apemail/internal/remote"
	"github.com/nomad-software/apemail/internal/store"
)

// CreateFolder makes a folder with the name on the server: within the
// folder parent, or, if parent is empty, among the user's own folders
// (NAMESPACE, as INBOX. on some servers).
func (e *Engine) CreateFolder(parent, name string, done func(error)) {
	name = strings.TrimSpace(name)
	if name == "" {
		e.reply(done, errors.New("the folder has no name"))
		return
	}
	e.do(func(c *remote.Conn) error {
		if c == nil {
			e.reply(done, errNoConn)
			return nil
		}
		err := e.createFolder(c, parent, name)
		e.reply(done, err)
		return err
	})
}

// createFolder makes the folder, with the connection of the op worker.
func (e *Engine) createFolder(c *remote.Conn, parent, name string) error {
	delim := e.delim(parent)
	if delim != "" && strings.Contains(name, delim) {
		return fmt.Errorf("a folder's name cannot have %q in it, which the server separates folders with", delim)
	}
	full := c.PersonalPrefix() + name
	if parent != "" {
		if delim == "" {
			return errors.New("the server does not keep folders within folders")
		}
		full = parent + delim + name
	}
	if e.hasFolder(full) {
		return fmt.Errorf("there is a folder %s already", full)
	}
	if err := c.Create(full, ""); err != nil {
		return err
	}
	e.addFolder(store.Folder{Name: full, Delim: delim})
	return nil
}

// FolderContents gives got the folder and those within it, with how many
// messages each holds, as the server lists and counts them now, deepest
// first and the folder itself last: what DeleteFolder deletes, for the user
// to be shown first. It is an error if the folder cannot be deleted.
func (e *Engine) FolderContents(folder string, got func([]remote.FolderStatus, error)) {
	e.do(func(c *remote.Conn) error {
		if c == nil {
			e.post(func() { got(nil, errNoConn) })
			return nil
		}
		contents, err := e.folderContents(c, folder)
		e.post(func() { got(contents, err) })
		return err
	})
}

// folderContents lists and counts the folder and those within it, with the
// connection of the op worker.
func (e *Engine) folderContents(c *remote.Conn, folder string) ([]remote.FolderStatus, error) {
	if err := e.Deletable(folder); err != nil {
		return nil, err
	}
	list, err := c.Folders()
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(list, func(f store.Folder) bool { return f.Name == folder })
	if i < 0 {
		return nil, fmt.Errorf("there is no folder %s on the server", folder)
	}
	delim := list[i].Delim
	var folders []store.Folder
	for _, f := range list {
		if f.Name == folder || (delim != "" && strings.HasPrefix(f.Name, folder+delim)) {
			folders = append(folders, f)
		}
	}
	// The deepest are deleted first, so that none is deleted before the
	// folders within it.
	slices.SortStableFunc(folders, func(a, b store.Folder) int {
		return strings.Count(b.Name, delim) - strings.Count(a.Name, delim)
	})
	for _, f := range folders {
		if err := e.Deletable(f.Name); err != nil {
			return nil, err
		}
	}
	return c.Status(folders)
}

// Deletable says why the folder cannot be deleted, if it cannot: the inbox,
// and the folders the account uses, are kept.
func (e *Engine) Deletable(folder string) error {
	if remote.IsInbox(folder) {
		return errors.New("the inbox cannot be deleted")
	}
	for _, use := range []Special{Sent, Drafts, Trash, Archive} {
		if e.SpecialFolder(use) == folder {
			return fmt.Errorf("%s is the account's %s folder, which is not deleted", folder, strings.ToLower(use.Name()))
		}
	}
	return nil
}

// DeleteFolder deletes the folders FolderContents gave, and every message in
// them, from the server for good, deepest first. The UI asks the user first,
// showing what they hold. Nothing is deleted if they have changed since, as
// a message arriving or a folder made within them, as the user would not
// have been asked about it. A folder being synchronised is not deleted until
// the sync is done.
func (e *Engine) DeleteFolder(contents []remote.FolderStatus, done func(error)) {
	if len(contents) == 0 {
		e.reply(done, errors.New("no folder was given"))
		return
	}
	e.do(func(c *remote.Conn) error {
		if c == nil {
			e.reply(done, errNoConn)
			return nil
		}
		err := e.deleteFolder(c, contents)
		e.reply(done, err)
		return err
	})
}

// deleteFolder deletes the folders, with the connection of the op worker.
func (e *Engine) deleteFolder(c *remote.Conn, contents []remote.FolderStatus) error {
	folder := contents[len(contents)-1].Name
	// No folder is synchronised while it is deleted, so that no sync
	// keeps what it read of one after it was thrown away.
	var held []*sync.Mutex
	defer func() {
		for _, l := range held {
			l.Unlock()
		}
	}()
	for _, s := range contents {
		l := e.folderSync(s.Name)
		if !l.TryLock() {
			return fmt.Errorf("%s is being synchronised; delete it once that is done", s.Name)
		}
		held = append(held, l)
	}

	now, err := e.folderContents(c, folder)
	if err != nil {
		return err
	}
	if !slices.Equal(now, contents) {
		return fmt.Errorf("%s has changed since you were asked, so nothing was deleted", folder)
	}
	for _, s := range contents {
		if err := c.DeleteFolder(s.Name); err != nil {
			return err
		}
		e.forgetFolder(s.Name)
	}
	return nil
}

// forgetFolder takes a folder that has been deleted out of the list, and
// throws away its local copy and what is known of it, as listed does for a
// folder that is gone. saveMu is held, so that its index is not saved after
// it was thrown away.
func (e *Engine) forgetFolder(name string) {
	e.saveMu.Lock()
	defer e.saveMu.Unlock()
	e.mu.Lock()
	e.folders = slices.DeleteFunc(e.folders, func(f store.Folder) bool { return f.Name == name })
	delete(e.indexes, name)
	delete(e.dirty, name)
	delete(e.edited, name)
	delete(e.flagEdits, name)
	list := slices.Clone(e.folders)
	e.post(func() { e.listener.FoldersChanged(e.acct.ID, list) })
	e.mu.Unlock()
	// The local copy of a folder that is not listed is thrown away.
	if err := e.store.SaveFolders(list); err != nil {
		e.failed(fmt.Errorf("saving the folders: %w", err))
	}
}
