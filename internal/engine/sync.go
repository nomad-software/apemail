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

		reused := conn != nil
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
		if err != nil && reused && isConnErr(err) && !e.stopped() {
			// A connection kept from before can have been dropped
			// meanwhile, as when the server restarts, or a router
			// forgets it overnight: the sync is tried once more on
			// another. What was done before it failed is done again,
			// as nothing a sync does changes the mailbox.
			e.disconnect(conn)
			conn = nil
			c, cerr := e.connect(false, nil)
			if cerr != nil {
				err = cerr
			} else {
				conn = c
				err = e.sync(conn, wanted)
			}
		}
		e.status("")
		if err != nil {
			e.failed(err)
			// The connection may be what failed, so another is made
			// next time.
			if conn != nil {
				e.disconnect(conn)
				conn = nil
			}
			continue
		}
		// The inbox is watched once it is listed: a sync of another
		// folder, kept from before without it, comes first.
		if _, ok := e.listedInbox(); ok && !watching {
			watching = e.spawn(e.watcher)
		}
	}
}

// listed keeps the folders as the server listed them, and tells the
// listener of them. That throws away the local copies of those that have
// gone, and what is known of them. The indexes are not saved meanwhile, so
// that none is saved into a folder after it was thrown away, and the
// folders are kept and saved as one, as addFolder does, so that the list
// saved is the one kept.
func (e *Engine) listed(list []store.Folder) {
	e.saveMu.Lock()
	defer e.saveMu.Unlock()
	if err := e.store.SaveFolders(list); err != nil {
		e.failed(fmt.Errorf("saving the folders: %w", err))
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.folders = slices.Clone(list)
	gone := func(name string) bool {
		return !slices.ContainsFunc(list, func(f store.Folder) bool { return f.Name == name })
	}
	for name := range e.indexes {
		if gone(name) {
			delete(e.indexes, name)
			delete(e.dirty, name)
		}
	}
	for name := range e.edits {
		if gone(name) {
			e.forgetEdits(name)
		}
	}
	e.post(func() { e.listener.FoldersChanged(e.acct.ID, list) })
}

// sync lists the folders, if that is wanted, and synchronises the folders
// that are wanted.
func (e *Engine) sync(conn *remote.Conn, wanted map[string]bool) error {
	if wanted[listFolders] {
		e.status("Listing the folders…")
		list, err := conn.Folders()
		if err != nil {
			return err
		}
		e.listed(list)
	}

	known := e.Folders()
	for name := range wanted {
		if e.stopped() {
			return nil
		}
		if name == listFolders {
			continue
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
	e.preload(folder)
	e.mu.Lock()
	known := snapshot(e.index(folder))
	edits := e.edits[folder]
	e.mu.Unlock()

	// While a large folder is first fetched, what has arrived is shown
	// every second or so.
	last := time.Now()
	// unshown are those that have arrived since what had was shown.
	arrived := 0
	var unshown []store.Summary
	progress := func(uidValidity uint32, s []store.Summary) {
		arrived += len(s)
		unshown = append(unshown, s...)
		say("Fetching the headers of %s… %d", folder, arrived)
		if time.Since(last) < time.Second {
			return
		}
		last = time.Now()
		e.saveMu.Lock()
		defer e.saveMu.Unlock()
		e.mu.Lock()
		defer e.mu.Unlock()
		ix := e.index(folder)
		// What is shown is numbered as the server numbers it now, so
		// that a message can be opened or deleted before the sync ends:
		// a folder first fetched has no numbering yet, and one numbered
		// anew has its local copy thrown away first, as at the end, so
		// that no message kept is taken for another of the same UID.
		if ix.UIDValidity != uidValidity {
			if err := e.store.ResetFolder(folder); err != nil {
				e.failed(err)
			}
			ix = &store.Index{Folder: folder, UIDValidity: uidValidity}
			e.indexes[folder] = ix
		}
		for _, m := range unshown {
			e.put(ix, m, edits)
		}
		unshown = nil
		snap := snapshot(ix)
		e.post(func() { e.listener.FolderChanged(e.acct.ID, snap) })
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
	// A folder numbered anew has its local copy thrown away, unless that
	// was done while what arrived was shown (progress), when messages
	// opened since are kept.
	if ix.UIDValidity != u.UIDValidity {
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
	// Every summary now says if its message has attachments: those added
	// by this sync, and those known, which an index made before they did
	// has had looked up. Those that could not be are taken to have none,
	// and not looked up again: what kept them from being found would
	// again.
	for uid, has := range u.Attachments {
		if i, ok := ix.Find(uid); ok && ix.Messages[i].Attachments != has {
			ix.Messages[i].Attachments = has
			changed = true
		}
	}
	if !ix.Attachments {
		ix.Attachments = true
		changed = true
	}
	e.failed(u.AttachmentsErr)
	// What was done before this sync began, it has read from the server.
	for _, done := range []map[uint32]uint64{e.edited[folder], e.taken[folder]} {
		for uid, n := range done {
			if n <= edits {
				delete(done, uid)
			}
		}
	}
	if changed {
		e.changed(ix)
	}
	return nil
}

// editedSince says if the user has set a flag of the message in the folder
// since the count of the edits of the folder was edits. It must be called
// with mu held.
func (e *Engine) editedSince(folder string, uid uint32, edits uint64) bool {
	return e.edited[folder][uid] > edits
}

// edit counts a change the user has made to the messages of the folder,
// and keeps the count in kept (edited or taken) for each message changed.
// It must be called with mu held.
func (e *Engine) edit(folder string, uids []uint32, kept map[string]map[uint32]uint64) {
	e.edits[folder]++
	if kept[folder] == nil {
		kept[folder] = map[uint32]uint64{}
	}
	for _, uid := range uids {
		kept[folder][uid] = e.edits[folder]
	}
}

// forgetEdits forgets the changes counted in the folder, which has gone. It
// must be called with mu held.
func (e *Engine) forgetEdits(folder string) {
	delete(e.edits, folder)
	delete(e.edited, folder)
	delete(e.taken, folder)
}

// put puts the summary of a message a sync read in the index, keeping the
// flags it has if the user has set one since the count of the edits of the
// folder was edits, and leaving it out if the user has taken it away since.
// It must be called with mu held.
func (e *Engine) put(ix *store.Index, s store.Summary, edits uint64) {
	if e.taken[ix.Folder][s.UID] > edits {
		return
	}
	if i, ok := ix.Find(s.UID); ok && e.editedSince(ix.Folder, s.UID, edits) {
		s.Flags = ix.Messages[i].Flags
	}
	ix.Put(s)
}

// watchRetry is how long the watcher waits to connect again after its
// connection ends, which doubles each time it cannot, up to watchRetryMost.
var watchRetry, watchRetryMost = 10 * time.Second, 5 * time.Minute

// watcher idles in the inbox, and has it synchronised when something in it
// changes. A server that cannot idle is not watched: the user checks for
// mail. When it connects again, after its connection ended, the inbox is
// synchronised, as mail may have arrived while it was not watching.
func (e *Engine) watcher() {
	inbox, ok := e.listedInbox()
	if !ok {
		return
	}

	backoff := watchRetry
	again := false
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
			if again {
				e.requestSync(inbox)
			}
			again = true
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
			backoff = min(backoff*2, watchRetryMost)
		} else {
			backoff = watchRetry
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
