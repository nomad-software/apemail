// Package engine keeps the local copy of an account in step with its
// mailbox, and does what the user asks of the mailbox, away from the UI.
//
// Each account has three goroutines, each with its own connection to the
// server: one synchronises the folders (sync.go), one does what the user
// asks (opens messages, moves them, sends mail: messages.go, outgoing.go),
// so that it is not held up by a long synchronisation, and one idles in the
// inbox so that new mail is seen as it arrives. Another saves the indexes
// that have changed (index.go). Synchronising every folder, and counting
// their unread messages, each have a connection of their own while they
// last (syncall.go). Decrypting and encrypting the local copy is done by
// them too, so the UI only ever handles what has already been read.
//
// What the engine has to tell the UI is passed to post, which runs it on the
// UI's thread. It has no dependency on the UI itself.
package engine

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/nomad-software/apemail/internal/account"
	"github.com/nomad-software/apemail/internal/remote"
	"github.com/nomad-software/apemail/internal/store"
)

// Listener is told what changes. Its methods are called on the UI's thread.
type Listener interface {
	// FoldersChanged gives the folders of the account, whenever they are
	// listed.
	FoldersChanged(accountID string, folders []store.Folder)
	// FolderChanged gives the index of a folder whenever it changes. It is
	// a copy, which the listener can keep.
	FolderChanged(accountID string, ix *store.Index)
	// Status says what the account is doing, or is empty when it is idle.
	Status(accountID, text string)
	// Failed says what went wrong in the background.
	Failed(accountID string, err error)
	// UnreadCounts gives the number of unread messages of folders, by
	// name, as the server counted them.
	UnreadCounts(accountID string, unread map[string]int)
}

// Engine is the work of an account.
type Engine struct {
	acct     account.Account
	store    *store.Store
	listener Listener
	post     func(func())

	mu      sync.Mutex
	folders []store.Folder
	indexes map[string]*store.Index
	// dirty are the folders whose indexes have changed since they were
	// saved, which saver saves, and save tells it one has.
	dirty map[string]bool
	save  chan struct{}
	// saveMu is held while indexes are saved, and while the local copy of
	// a folder is thrown away, so that an index is never saved into a
	// folder after it was thrown away. It is taken before mu.
	saveMu sync.Mutex
	// flagEdits counts the flags the user has set in each folder, and
	// edited is, for each message set, the count when it was, so that a
	// sync that read the flags of a message from the server before they
	// were set does not undo them.
	flagEdits map[string]uint64
	edited    map[string]map[uint32]uint64
	// conns are the connections that are open, which closing the engine
	// aborts.
	conns map[*remote.Conn]bool
	// folderSyncs are held by a sync of each folder, so that no folder is
	// synchronised on two connections at once, which could have the one
	// that read the server first save what it read last.
	folderSyncs map[string]*sync.Mutex
	// allStop is closed to stop SyncAll, and is nil when it is not
	// running; allConn is its connection, once it has one.
	allStop chan struct{}
	allConn *remote.Conn

	// statusMu guards statuses, what each part of the engine is doing,
	// which together are the status of the account.
	statusMu sync.Mutex
	statuses map[statusPart]string

	syncs chan string
	ops   chan func(*remote.Conn) error
	stop  chan struct{}
	wg    sync.WaitGroup
	once  sync.Once
}

// New starts the work of the account, whose local copy is st. Nothing is
// asked of the server until something is asked of the engine.
func New(acct account.Account, st *store.Store, listener Listener, post func(func())) *Engine {
	e := &Engine{
		acct:        acct,
		store:       st,
		listener:    listener,
		post:        post,
		indexes:     map[string]*store.Index{},
		dirty:       map[string]bool{},
		save:        make(chan struct{}, 1),
		flagEdits:   map[string]uint64{},
		edited:      map[string]map[uint32]uint64{},
		conns:       map[*remote.Conn]bool{},
		folderSyncs: map[string]*sync.Mutex{},
		statuses:    map[statusPart]string{},
		syncs:       make(chan string, 64),
		ops:         make(chan func(*remote.Conn) error, 1024),
		stop:        make(chan struct{}),
	}
	e.wg.Add(3)
	go e.syncWorker()
	go e.opWorker()
	go e.saver()
	return e
}

// Account returns the account the engine works for.
func (e *Engine) Account() account.Account {
	return e.acct
}

// Close stops the work of the account and closes its connections. Work that
// has been asked for and not started is not done.
func (e *Engine) Close() {
	e.once.Do(func() { close(e.stop) })
	// What is waiting on the network is let go, rather than waited for.
	e.mu.Lock()
	for c := range e.conns {
		c.Abort()
	}
	e.mu.Unlock()
	e.wg.Wait()
}

// stopped reports if the engine has been closed.
func (e *Engine) stopped() bool {
	select {
	case <-e.stop:
		return true
	default:
		return false
	}
}

// statusPart is a part of the engine that can be doing something while
// another is: what each is doing is shown.
type statusPart int

const (
	// mainStatus is that of the syncs and operations the user asks for.
	mainStatus statusPart = iota
	countsStatus
	allStatus
)

// status tells the listener what the account is doing.
func (e *Engine) status(format string, args ...any) {
	e.say(mainStatus, format, args...)
}

// say tells the listener what a part of the engine is doing, or that it is
// done if format is empty, with what the other parts are doing.
func (e *Engine) say(part statusPart, format string, args ...any) {
	e.statusMu.Lock()
	e.statuses[part] = fmt.Sprintf(format, args...)
	var parts []string
	for p := mainStatus; p <= allStatus; p++ {
		if text := e.statuses[p]; text != "" {
			parts = append(parts, text)
		}
	}
	e.statusMu.Unlock()
	text := strings.Join(parts, " · ")
	e.post(func() { e.listener.Status(e.acct.ID, text) })
}

// failed tells the listener what went wrong.
func (e *Engine) failed(err error) {
	if err == nil || e.stopped() {
		return
	}
	e.post(func() { e.listener.Failed(e.acct.ID, err) })
}

// Start shows the folders kept from before, and then, if the account says
// to, checks the inbox for new mail, which starts watching it.
func (e *Engine) Start() {
	go func() {
		folders, err := e.store.Folders()
		if err != nil {
			e.failed(fmt.Errorf("reading the folders kept: %w", err))
		}
		if len(folders) > 0 {
			e.setFolders(folders)
		}
		if e.acct.CheckOnStart {
			e.Refresh("")
		}
	}()
}

// Refresh checks the folder for new mail, or the inbox if folder is empty,
// and does what was put off for want of a connection. The folders are
// listed again, so that those made or removed elsewhere are seen, but no
// other folder is synchronised: a folder is synchronised when it is opened,
// so that a large one costs nothing until it is looked at.
func (e *Engine) Refresh(folder string) {
	if folder == "" {
		folder = e.inbox()
	}
	e.requestSync(listFolders)
	e.requestSync(folder)
	e.do(func(c *remote.Conn) error { return e.flushPending(c) })
}

// inbox returns the name of the inbox as the server lists it, or INBOX, as
// every IMAP mailbox has, if the folders have not been listed.
func (e *Engine) inbox() string {
	for _, f := range e.Folders() {
		if remote.IsInbox(f.Name) {
			return f.Name
		}
	}
	return "INBOX"
}

// requestSync asks the sync worker to synchronise a folder, or to list the
// folders.
func (e *Engine) requestSync(folder string) {
	if e.stopped() {
		return
	}
	select {
	case e.syncs <- folder:
	default:
		// The queue is full of syncs already, which will do.
	}
}

// Folders returns the folders as they were last listed.
func (e *Engine) Folders() []store.Folder {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.folders)
}

// setFolders keeps the folders and tells the listener of them.
func (e *Engine) setFolders(folders []store.Folder) {
	e.mu.Lock()
	e.folders = slices.Clone(folders)
	e.mu.Unlock()
	e.post(func() { e.listener.FoldersChanged(e.acct.ID, folders) })
}

// Open reads the index of the folder, which is given to the listener, and
// then synchronises the folder.
func (e *Engine) Open(folder string) {
	go func() {
		e.preload(folder)
		e.mu.Lock()
		s := snapshot(e.index(folder))
		e.mu.Unlock()
		e.post(func() { e.listener.FolderChanged(e.acct.ID, s) })
		e.requestSync(folder)
	}()
}

// connect makes a connection to the IMAP server, which closing the engine
// aborts. It must be given back with disconnect. Only the connection that
// does what the user asks is writable: synchronising and idling only read.
func (e *Engine) connect(writable bool, unilateral *imapclient.UnilateralDataHandler) (*remote.Conn, error) {
	c, err := remote.Dial(e.acct.Incoming, remote.Options{
		Writable:   writable,
		Unilateral: unilateral,
	})
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.stopped() {
		c.Abort()
		return nil, errors.New("the account has been closed")
	}
	e.conns[c] = true
	return c, nil
}

// disconnect closes a connection made by connect.
func (e *Engine) disconnect(c *remote.Conn) {
	e.mu.Lock()
	delete(e.conns, c)
	e.mu.Unlock()
	if e.stopped() {
		c.Abort()
	} else {
		c.Close()
	}
}

// do asks the op worker to do something with its connection.
func (e *Engine) do(op func(*remote.Conn) error) {
	if e.stopped() {
		return
	}
	select {
	case e.ops <- op:
	case <-e.stop:
	}
}

// opWorker does what the user asks of the mailbox, in the order it is asked.
func (e *Engine) opWorker() {
	defer e.wg.Done()
	var conn *remote.Conn
	defer func() {
		if conn != nil {
			e.disconnect(conn)
		}
	}()
	for {
		var op func(*remote.Conn) error
		select {
		case <-e.stop:
			return
		case op = <-e.ops:
		}
		if conn == nil {
			c, err := e.connect(true, nil)
			if err != nil {
				// The op is given no connection, and says why it
				// could not be done.
				op(nil)
				continue
			}
			conn = c
		}
		if err := op(conn); err != nil && isConnErr(err) {
			e.disconnect(conn)
			conn = nil
		}
	}
}

// errNoConn is given to an op which there is no connection for.
var errNoConn = errors.New("the server could not be reached")

// isConnErr reports if the error is one of the connection, after which
// another is made.
func isConnErr(err error) bool {
	if errors.Is(err, remote.ErrReadOnly) {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "connection") || strings.Contains(msg, "EOF") || strings.Contains(msg, "timeout") || strings.Contains(msg, "broken pipe")
}

// reply runs done on the UI's thread with the error, if done is not nil.
func (e *Engine) reply(done func(error), err error) {
	if done != nil {
		e.post(func() { done(err) })
	}
}

const (
	Sent Special = iota
	Drafts
	Trash
	Archive
)
