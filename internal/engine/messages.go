package engine

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/nomad-software/apemail/internal/account"
	"github.com/nomad-software/apemail/internal/remote"
	"github.com/nomad-software/apemail/internal/store"
)

// Message returns the message with the UID in the folder to got, on the UI's
// thread: from the local copy, or downloaded if it is not kept. It is not
// marked as read.
func (e *Engine) Message(folder string, uid uint32, got func(raw []byte, err error)) {
	e.spawn(func() {
		raw, err := e.store.Message(folder, uid)
		if err == nil {
			e.post(func() { got(raw, nil) })
			return
		}
		if !errors.Is(err, fs.ErrNotExist) {
			e.failed(fmt.Errorf("reading the message kept: %w", err))
		}
		e.do(func(c *remote.Conn) error {
			if c == nil {
				e.post(func() { got(nil, errNoConn) })
				return nil
			}
			e.mu.Lock()
			validity := e.index(folder).UIDValidity
			e.mu.Unlock()
			var raw []byte
			err := c.Fetch(folder, validity, []uint32{uid}, func(u uint32, r []byte) error {
				if u == uid {
					raw = r
				}
				return nil
			})
			if err == nil && raw == nil {
				err = errors.New("the message is no longer on the server")
			}
			if err == nil {
				e.keepMessage(folder, validity, uid, raw)
			}
			e.post(func() { got(raw, err) })
			return err
		})
	})
}

// keepMessage keeps a message that was downloaded from the folder when its
// UIDVALIDITY was validity, unless the folder has been numbered anew or has
// gone since, when its UID may be another message's. saveMu is held, as the
// local copy of a folder is thrown away with it held.
func (e *Engine) keepMessage(folder string, validity, uid uint32, raw []byte) {
	e.saveMu.Lock()
	defer e.saveMu.Unlock()
	e.mu.Lock()
	ix, ok := e.indexes[folder]
	same := ok && ix.UIDValidity == validity
	e.mu.Unlock()
	if !same {
		return
	}
	if err := e.store.SaveMessage(folder, uid, raw); err != nil {
		e.failed(fmt.Errorf("keeping the message: %w", err))
	}
}

// SetFlag adds the flag to the messages, or takes it away, on the server
// and then in the index.
func (e *Engine) SetFlag(folder string, uids []uint32, flag string, add bool, done func(error)) {
	e.do(func(c *remote.Conn) error {
		if c == nil {
			e.reply(done, errNoConn)
			return nil
		}
		e.mu.Lock()
		validity := e.index(folder).UIDValidity
		e.mu.Unlock()
		err := c.SetFlags(folder, validity, uids, add, flag)
		if err == nil {
			e.mu.Lock()
			ix := e.index(folder)
			for _, uid := range uids {
				i, ok := ix.Find(uid)
				if !ok {
					continue
				}
				flags := slices.DeleteFunc(slices.Clone(ix.Messages[i].Flags), func(f string) bool {
					return strings.EqualFold(f, flag)
				})
				if add {
					flags = append(flags, flag)
				}
				ix.Messages[i].Flags = flags
			}
			e.flagEdits[folder]++
			if e.edited[folder] == nil {
				e.edited[folder] = map[uint32]uint64{}
			}
			for _, uid := range uids {
				e.edited[folder][uid] = e.flagEdits[folder]
			}
			e.changed(ix)
			e.mu.Unlock()
		}
		e.reply(done, err)
		return err
	})
}

// removed takes messages that have left the folder out of its index and the
// local copy.
func (e *Engine) removed(folder string, uids []uint32) {
	e.mu.Lock()
	defer e.mu.Unlock()
	ix := e.index(folder)
	for _, uid := range uids {
		ix.Remove(uid)
		e.store.RemoveMessage(folder, uid)
	}
	e.changed(ix)
}

// Move moves the messages to another folder.
func (e *Engine) Move(folder string, uids []uint32, to string, done func(error)) {
	e.do(func(c *remote.Conn) error {
		if c == nil {
			e.reply(done, errNoConn)
			return nil
		}
		err := e.move(c, folder, uids, to)
		e.reply(done, err)
		return err
	})
}

// move moves the messages to another folder, with the connection of the op
// worker.
func (e *Engine) move(c *remote.Conn, folder string, uids []uint32, to string) error {
	e.mu.Lock()
	validity := e.index(folder).UIDValidity
	e.mu.Unlock()
	if err := c.Move(folder, validity, uids, to); err != nil {
		return err
	}
	e.removed(folder, uids)
	e.requestSync(to)
	return nil
}

// Archive moves the messages to the archive folder, which is made if there
// is none, divided into folders for the year or month each was sent in as
// the account says, as other clients do. Those folders are made too.
func (e *Engine) Archive(folder string, uids []uint32, done func(error)) {
	e.do(func(c *remote.Conn) error {
		if c == nil {
			e.reply(done, errNoConn)
			return nil
		}
		err := e.archive(c, folder, uids)
		e.reply(done, err)
		return err
	})
}

// archive moves the messages to the archive, with the connection of the op
// worker.
func (e *Engine) archive(c *remote.Conn, folder string, uids []uint32) error {
	archive, err := e.ensureFolder(c, Archive)
	if err != nil {
		return err
	}
	if e.InArchive(folder) {
		return fmt.Errorf("the messages are already in %s", folder)
	}

	// Each message goes in the folder for when it was sent. The delimiter
	// is found before the lock is taken, as finding it takes the lock too.
	delim := e.delim(archive)
	byFolder := map[string][]uint32{}
	e.mu.Lock()
	ix := e.index(folder)
	for _, uid := range uids {
		date := time.Now()
		if i, ok := ix.Find(uid); ok && !ix.Messages[i].Date.IsZero() {
			date = ix.Messages[i].Date
		}
		to := ArchiveFolder(archive, delim, e.acct.ArchiveBy, date)
		byFolder[to] = append(byFolder[to], uid)
	}
	e.mu.Unlock()

	targets := slices.Sorted(maps.Keys(byFolder))
	var failed []error
	for _, to := range targets {
		if err := e.ensureSubfolder(c, archive, to); err != nil {
			failed = append(failed, err)
			continue
		}
		if err := e.move(c, folder, byFolder[to], to); err != nil {
			failed = append(failed, err)
		}
	}
	return errors.Join(failed...)
}

// ArchiveFolder returns the folder within the archive that mail sent at the
// date goes in, as the account divides it: archive/2026, or
// archive/2026/2026-03, with the delimiter of the server. A server without
// folders within folders has everything in the archive itself.
func ArchiveFolder(archive, delim string, by account.ArchiveBy, date time.Time) string {
	if delim == "" || by == account.ArchiveSingle {
		return archive
	}
	date = date.Local()
	year := archive + delim + date.Format("2006")
	if by == account.ArchiveMonthly {
		return year + delim + date.Format("2006-01")
	}
	return year
}

// InArchive reports if the folder is the archive, or one within it.
func (e *Engine) InArchive(folder string) bool {
	archive := e.SpecialFolder(Archive)
	if archive == "" {
		return false
	}
	delim := e.delim(archive)
	return folder == archive || (delim != "" && strings.HasPrefix(folder, archive+delim))
}

// delim returns the delimiter of the folder, or of the mailbox if the
// folder is not listed.
func (e *Engine) delim(folder string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, f := range e.folders {
		if f.Name == folder && f.Delim != "" {
			return f.Delim
		}
	}
	for _, f := range e.folders {
		if f.Delim != "" {
			return f.Delim
		}
	}
	return ""
}

// ensureSubfolder makes the folder within the archive, and those between, if
// they are not there.
func (e *Engine) ensureSubfolder(c *remote.Conn, archive, folder string) error {
	if folder == archive {
		return nil
	}
	delim := e.delim(archive)
	// The folders between are made first, as not every server makes them.
	parts := strings.Split(strings.TrimPrefix(folder, archive+delim), delim)
	name := archive
	for _, part := range parts {
		name += delim + part
		if e.hasFolder(name) {
			continue
		}
		if err := c.Create(name, ""); err != nil {
			return err
		}
		e.addFolder(store.Folder{Name: name, Delim: delim})
	}
	return nil
}

// hasFolder reports if the folder is listed.
func (e *Engine) hasFolder(name string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.ContainsFunc(e.folders, func(f store.Folder) bool { return f.Name == name })
}

// addFolder lists a folder that has been made, and shows it at once, rather
// than when the folders are next listed. saveMu is held, as listed holds it,
// so that the folders saved last are those kept last.
func (e *Engine) addFolder(f store.Folder) {
	e.saveMu.Lock()
	defer e.saveMu.Unlock()
	e.mu.Lock()
	if !slices.ContainsFunc(e.folders, func(g store.Folder) bool { return g.Name == f.Name }) {
		e.folders = append(e.folders, f)
		remote.SortFolders(e.folders)
	}
	list := slices.Clone(e.folders)
	e.post(func() { e.listener.FoldersChanged(e.acct.ID, list) })
	e.mu.Unlock()
	if err := e.store.SaveFolders(list); err != nil {
		e.failed(fmt.Errorf("saving the folders: %w", err))
	}
}

// Trash moves the messages to the trash folder, which is made if there is
// none. That is what deleting them does outside the trash; mail already in
// the trash is deleted for good with Delete.
func (e *Engine) Trash(folder string, uids []uint32, done func(error)) {
	e.do(func(c *remote.Conn) error {
		if c == nil {
			e.reply(done, errNoConn)
			return nil
		}
		trash, err := e.ensureFolder(c, Trash)
		if err == nil && trash == folder {
			err = fmt.Errorf("the messages are already in %s", trash)
		}
		if err == nil {
			err = e.move(c, folder, uids, trash)
		}
		e.reply(done, err)
		return err
	})
}

// Delete deletes messages in the trash folder for good, which is what
// deleting them does there. Mail anywhere else is moved to the trash with
// Trash, and is not deleted for good. The UI asks the user first.
func (e *Engine) Delete(folder string, uids []uint32, done func(error)) {
	if trash := e.SpecialFolder(Trash); trash == "" || folder != trash {
		e.reply(done, errors.New("only mail in the trash is deleted for good; elsewhere it is moved to the trash"))
		return
	}
	e.do(func(c *remote.Conn) error {
		if c == nil {
			e.reply(done, errNoConn)
			return nil
		}
		e.mu.Lock()
		validity := e.index(folder).UIDValidity
		e.mu.Unlock()
		err := c.Delete(folder, validity, uids)
		if err == nil {
			e.removed(folder, uids)
		}
		e.reply(done, err)
		return err
	})
}
