package engine

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/nomad-software/apemail/internal/remote"
	"github.com/nomad-software/apemail/internal/store"
)

// listFolders asks for the folders to be listed, which synchronises none.
const listFolders = "\x00list"

// syncWorker synchronises folders as it is asked to.
func (e *Engine) syncWorker() {
	defer e.wg.Done()
	var conn *remote.Conn
	defer func() {
		if conn != nil {
			e.disconnect(conn)
		}
	}()
	watching := false

	for {
		var folder string
		select {
		case <-e.stop:
			return
		case folder = <-e.syncs:
		}
		// Requests that have piled up are done together, once each.
		wanted := map[string]bool{folder: true}
	drain:
		for {
			select {
			case f := <-e.syncs:
				wanted[f] = true
			default:
				break drain
			}
		}

		if conn == nil {
			e.status("Connecting to %s…", e.acct.Incoming.Host)
			c, err := e.connect(false, nil)
			if err != nil {
				e.status("")
				e.failed(err)
				continue
			}
			conn = c
		}
		err := e.sync(conn, wanted)
		e.status("")
		if err != nil {
			e.failed(err)
			// The connection may be what failed, so another is made
			// next time.
			e.disconnect(conn)
			conn = nil
			continue
		}
		if !watching && !e.stopped() {
			watching = true
			e.wg.Add(1)
			go e.watcher()
		}
	}
}

// forgetGone keeps the folders as listed, which throws away the local copies
// of those that have gone from the server, and their indexes. The indexes
// are not saved meanwhile, so that none is saved into a folder after it was
// thrown away.
func (e *Engine) forgetGone(list []store.Folder) {
	e.saveMu.Lock()
	defer e.saveMu.Unlock()
	if err := e.store.SaveFolders(list); err != nil {
		e.failed(fmt.Errorf("saving the folders: %w", err))
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for name := range e.indexes {
		if !slices.ContainsFunc(list, func(f store.Folder) bool { return f.Name == name }) {
			delete(e.indexes, name)
			delete(e.dirty, name)
		}
	}
}

// sync lists the folders, if that is wanted, and synchronises the folders
// that are wanted.
func (e *Engine) sync(conn *remote.Conn, wanted map[string]bool) error {
	if wanted[listFolders] {
		delete(wanted, listFolders)
		e.status("Listing the folders…")
		list, err := conn.Folders()
		if err != nil {
			return err
		}
		e.forgetGone(list)
		e.setFolders(list)
	}

	known := e.Folders()
	for name := range wanted {
		if e.stopped() {
			return nil
		}
		// A folder that is asked for before the folders are listed is
		// synchronised all the same.
		if i := slices.IndexFunc(known, func(f store.Folder) bool { return f.Name == name }); i >= 0 && !known[i].Selectable() {
			continue
		}
		if err := e.syncFolder(conn, name, e.status); err != nil {
			return err
		}
	}
	return nil
}

// folderSync returns the lock a sync of the folder holds.
func (e *Engine) folderSync(folder string) *sync.Mutex {
	e.mu.Lock()
	defer e.mu.Unlock()
	l, ok := e.folderSyncs[folder]
	if !ok {
		l = &sync.Mutex{}
		e.folderSyncs[folder] = l
	}
	return l
}

// syncFolder brings the index of the folder up to date, saying what it is
// doing with say. A folder that is being synchronised already, on another
// connection, is left to that sync, which shows what it finds, rather than
// waited for: SyncAll can take minutes over a large folder, and checking
// another meanwhile is not to wait behind it.
func (e *Engine) syncFolder(conn *remote.Conn, folder string, say func(format string, args ...any)) error {
	lock := e.folderSync(folder)
	if !lock.TryLock() {
		return nil
	}
	defer lock.Unlock()
	say("Checking %s…", folder)
	e.mu.Lock()
	known := snapshot(e.index(folder))
	edits := e.flagEdits[folder]
	e.mu.Unlock()

	// While a large folder is first fetched, what has arrived is shown
	// every second or so.
	last := time.Now()
	var arrived []store.Summary
	progress := func(s []store.Summary) {
		arrived = append(arrived, s...)
		say("Fetching the headers of %s… %d", folder, len(arrived))
		if time.Since(last) < time.Second {
			return
		}
		last = time.Now()
		e.mu.Lock()
		ix := e.index(folder)
		if ix.UIDValidity == known.UIDValidity {
			for _, m := range s {
				e.put(ix, m, edits)
			}
			s := snapshot(ix)
			e.post(func() { e.listener.FolderChanged(e.acct.ID, s) })
		}
		e.mu.Unlock()
	}

	u, err := conn.Sync(known, progress)
	if err != nil {
		return err
	}

	// The index is not saved while the folder may be thrown away.
	e.saveMu.Lock()
	defer e.saveMu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	ix := e.index(folder)
	// Only a sync that changes something has the index saved and shown
	// again, as most find nothing new, and a large folder takes a while.
	changed := ix.HighestModSeq != u.HighestModSeq
	if u.Reset || ix.UIDValidity != u.UIDValidity {
		if err := e.store.ResetFolder(folder); err != nil {
			e.failed(err)
		}
		ix = &store.Index{Folder: folder, UIDValidity: u.UIDValidity}
		e.indexes[folder] = ix
		changed = true
	}
	ix.HighestModSeq = u.HighestModSeq
	for _, uid := range u.Removed {
		if ix.Remove(uid) {
			changed = true
		}
		e.store.RemoveMessage(folder, uid)
	}
	for uid, flags := range u.Flags {
		if i, ok := ix.Find(uid); ok && !e.editedSince(folder, uid, edits) && !slices.Equal(ix.Messages[i].Flags, flags) {
			ix.Messages[i].Flags = flags
			changed = true
		}
	}
	for _, s := range u.Added {
		e.put(ix, s, edits)
		changed = true
	}
	// What was set before this sync began, it has read from the server.
	for uid, n := range e.edited[folder] {
		if n <= edits {
			delete(e.edited[folder], uid)
		}
	}
	if changed {
		e.changed(ix)
	}
	return nil
}

// editedSince says if the user has set a flag of the message in the folder
// since the count of the edits of its flags was edits. It must be called
// with mu held.
func (e *Engine) editedSince(folder string, uid uint32, edits uint64) bool {
	return e.edited[folder][uid] > edits
}

// put puts the summary of a message a sync read in the index, keeping the
// flags it has if the user has set one since the count of the edits of the
// folder's flags was edits. It must be called with mu held.
func (e *Engine) put(ix *store.Index, s store.Summary, edits uint64) {
	if i, ok := ix.Find(s.UID); ok && e.editedSince(ix.Folder, s.UID, edits) {
		s.Flags = ix.Messages[i].Flags
	}
	ix.Put(s)
}

// watcher idles in the inbox, and has it synchronised when something in it
// changes. A server that cannot idle is not watched: the user checks for
// mail.
func (e *Engine) watcher() {
	defer e.wg.Done()
	inbox := ""
	for _, f := range e.Folders() {
		if remote.IsInbox(f.Name) {
			inbox = f.Name
		}
	}
	if inbox == "" {
		return
	}

	backoff := 10 * time.Second
	for !e.stopped() {
		signal := make(chan struct{}, 1)
		notify := func() {
			select {
			case signal <- struct{}{}:
			default:
			}
		}
		conn, err := e.connect(false, &imapclient.UnilateralDataHandler{
			Expunge: func(uint32) { notify() },
			Mailbox: func(data *imapclient.UnilateralDataMailbox) {
				if data.NumMessages != nil {
					notify()
				}
			},
			Fetch: func(msg *imapclient.FetchMessageData) {
				msg.Collect()
				notify()
			},
		})
		if err == nil && !conn.CanIdle() {
			e.disconnect(conn)
			return
		}
		if err == nil {
			err = e.watch(conn, inbox, signal)
			e.disconnect(conn)
		}
		if e.stopped() {
			return
		}
		// A connection that fails is made again, after a while that
		// grows each time it fails.
		select {
		case <-e.stop:
			return
		case <-time.After(backoff):
		}
		if err != nil {
			backoff = min(backoff*2, 5*time.Minute)
		} else {
			backoff = 10 * time.Second
		}
	}
}

// watch idles in the inbox until the engine stops or the connection fails,
// having the inbox synchronised whenever the server says it has changed.
func (e *Engine) watch(conn *remote.Conn, inbox string, signal chan struct{}) error {
	for {
		wake := make(chan struct{})
		done := make(chan struct{})
		changed := false
		go func() {
			defer close(wake)
			select {
			case <-signal:
				changed = true
			case <-e.stop:
			case <-done:
			}
		}()
		err := conn.Watch(inbox, wake)
		close(done)
		<-wake
		if err != nil {
			return err
		}
		select {
		case <-e.stop:
			return nil
		case <-conn.Closed():
			return errors.New("the connection was closed")
		default:
		}
		if changed {
			e.requestSync(inbox)
		}
	}
}
