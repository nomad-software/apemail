package engine

import (
	"cmp"
	"errors"
	"fmt"
	"slices"

	"github.com/nomad-software/apemail/internal/remote"
	"github.com/nomad-software/apemail/internal/store"
)

// ErrStopped is what SyncAll ends with when StopSyncAll stops it.
var ErrStopped = errors.New("the synchronising of every folder was stopped")

// SyncAll synchronises every folder that holds messages, but downloads no
// message, so that every folder is up to date and opens at once: the inbox
// first, then the drafts and sent mail, then the rest, and the trash last,
// as it is often the largest and the least looked at. It runs on a
// connection of its own, so that checking a folder meanwhile is not held up
// behind it, and gives done nil when it has finished, ErrStopped if it was
// stopped, or what went wrong. It does nothing if it is running already.
func (e *Engine) SyncAll(done func(error)) {
	e.mu.Lock()
	if e.allStop != nil {
		e.mu.Unlock()
		return
	}
	stop := make(chan struct{})
	e.allStop = stop
	e.mu.Unlock()

	started := e.spawn(func() {
		err := e.syncAll(stop)
		e.mu.Lock()
		e.allStop = nil
		e.mu.Unlock()
		e.say(allStatus, "")
		select {
		case <-stop:
			err = ErrStopped
		default:
		}
		e.reply(done, err)
	})
	if !started {
		e.mu.Lock()
		e.allStop = nil
		e.mu.Unlock()
	}
}

// SyncingAll reports if SyncAll is running.
func (e *Engine) SyncingAll() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.allStop != nil
}

// StopSyncAll stops SyncAll, at once: its connection is dropped, which
// leaves the folder it was at as it was.
func (e *Engine) StopSyncAll() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.allStop == nil {
		return
	}
	select {
	case <-e.allStop:
	default:
		close(e.allStop)
	}
	if e.allConn != nil {
		e.allConn.Abort()
	}
}

// syncAll synchronises every folder, until stop is closed.
func (e *Engine) syncAll(stop chan struct{}) error {
	e.say(allStatus, "Connecting to synchronise every folder…")
	conn, err := e.connect(false, nil)
	if err != nil {
		return err
	}
	defer e.disconnect(conn)
	e.mu.Lock()
	select {
	case <-stop:
		e.mu.Unlock()
		return ErrStopped
	default:
	}
	e.allConn = conn
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.allConn = nil
		e.mu.Unlock()
	}()

	e.say(allStatus, "Listing the folders…")
	list, err := conn.Folders()
	if err != nil {
		return err
	}
	e.listed(list)
	order := e.syncOrder(list)
	for i, folder := range order {
		if e.stopped() {
			return nil
		}
		say := func(format string, args ...any) {
			e.say(allStatus, fmt.Sprintf("Every folder, %d of %d: ", i+1, len(order))+format, args...)
		}
		if err := e.syncFolder(conn, folder, say); err != nil {
			return err
		}
	}
	return nil
}

// syncOrder returns the names of the folders that hold messages, in the
// order SyncAll synchronises them.
func (e *Engine) syncOrder(list []store.Folder) []string {
	drafts, sent, trash := e.SpecialFolder(Drafts), e.SpecialFolder(Sent), e.SpecialFolder(Trash)
	rank := func(name string) int {
		switch {
		case remote.IsInbox(name):
			return 0
		case name == drafts || name == sent:
			return 1
		case name == trash:
			return 3
		}
		return 2
	}
	var names []string
	for _, f := range list {
		if f.Selectable() {
			names = append(names, f.Name)
		}
	}
	slices.SortStableFunc(names, func(a, b string) int { return cmp.Compare(rank(a), rank(b)) })
	return names
}

// UpdateCounts asks the server how many messages of every folder are unread
// (STATUS), on a connection of its own, which opens none of them, and gives
// the counts to the listener and done what went wrong, if anything did.
func (e *Engine) UpdateCounts(done func(error)) {
	e.spawn(func() { e.reply(done, e.updateCounts()) })
}

// updateCounts counts the unread messages of every folder.
func (e *Engine) updateCounts() error {
	e.say(countsStatus, "Counting the unread messages…")
	defer e.say(countsStatus, "")
	conn, err := e.connect(false, nil)
	if err != nil {
		return err
	}
	defer e.disconnect(conn)
	list, err := conn.Folders()
	if err != nil {
		return err
	}
	e.listed(list)
	var folders []string
	for _, f := range list {
		if f.Selectable() {
			folders = append(folders, f.Name)
		}
	}
	unread, err := conn.Unread(folders...)
	// What could be counted is shown, even if some could not.
	if len(unread) > 0 {
		e.post(func() { e.listener.UnreadCounts(e.acct.ID, unread) })
	}
	return err
}
