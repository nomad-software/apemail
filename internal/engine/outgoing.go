package engine

import (
	"errors"
	"fmt"

	"github.com/nomad-software/apemail/internal/remote"
	"github.com/nomad-software/apemail/internal/send"
	"github.com/nomad-software/apemail/internal/store"
)

// Saved is where a message was put in a folder, so that it can be found
// again.
type Saved struct {
	Folder      string
	UID         uint32
	UIDValidity uint32
}

// draftFlags are the flags of a draft that is saved.
var draftFlags = []string{`\Draft`, `\Seen`}

// SaveDraft puts the draft in the drafts folder, which is made if there is
// none. If there is no connection it is kept to be put there later, and
// saved is given no UID.
func (e *Engine) SaveDraft(raw []byte, done func(saved Saved, err error)) {
	e.do(func(c *remote.Conn) error {
		if c == nil {
			_, err := e.store.AddPending(store.Pending{Kind: store.PendingAppend, Use: useNames[Drafts], Flags: draftFlags, Raw: raw})
			if err == nil {
				err = errors.New("the server could not be reached, so the draft will be saved when it can be")
			}
			e.post(func() { done(Saved{}, err) })
			return nil
		}
		folder, err := e.ensureFolder(c, Drafts)
		var uid, validity uint32
		if err == nil {
			uid, validity, err = c.Append(folder, raw, draftFlags)
		}
		if err == nil {
			e.requestSync(folder)
		}
		e.post(func() { done(Saved{Folder: folder, UID: uid, UIDValidity: validity}, err) })
		return err
	})
}

// TrashSaved moves messages that were saved, as drafts that have been
// replaced, to the trash, which is made if there is none. The UI asks the
// user first. Those whose folder has been numbered anew since are left
// alone.
func (e *Engine) TrashSaved(saved []Saved, done func(error)) {
	e.do(func(c *remote.Conn) error {
		if c == nil {
			e.reply(done, errNoConn)
			return nil
		}
		trash, err := e.ensureFolder(c, Trash)
		if err != nil {
			e.reply(done, err)
			return err
		}
		byFolder := map[Saved][]uint32{}
		for _, s := range saved {
			if s.UID == 0 || s.Folder == trash {
				continue
			}
			key := Saved{Folder: s.Folder, UIDValidity: s.UIDValidity}
			byFolder[key] = append(byFolder[key], s.UID)
		}
		var failed []error
		for key, uids := range byFolder {
			if err := c.Move(key.Folder, key.UIDValidity, uids, trash); err != nil {
				failed = append(failed, err)
				continue
			}
			e.removed(key.Folder, uids)
		}
		if len(byFolder) > 0 {
			e.requestSync(trash)
		}
		err = errors.Join(failed...)
		e.reply(done, err)
		return err
	})
}

// Send sends the message to the recipients, asking for delivery status
// notifications if dsn is true, and puts a copy in the sent folder, which is
// made if there is none. If the outgoing server cannot be reached it is kept
// to be sent later, which the error says.
func (e *Engine) Send(raw []byte, rcpts []string, dsn bool, done func(error)) {
	e.send(raw, rcpts, dsn, true, done)
}

// SendReceipt sends a return receipt to the recipients, as Send does but
// without keeping a copy in the sent folder, as other clients do: it is a
// notice, not mail the user wrote.
func (e *Engine) SendReceipt(raw []byte, rcpts []string, done func(error)) {
	e.send(raw, rcpts, false, false, done)
}

// send sends the message, keeping a copy in the sent folder if keep is true.
func (e *Engine) send(raw []byte, rcpts []string, dsn, keep bool, done func(error)) {
	e.spawn(func() {
		err := send.Send(e.acct.Outgoing, e.acct.Email, rcpts, raw, dsn)
		if errors.Is(err, send.ErrUnreachable) {
			p := store.Pending{Kind: store.PendingSend, From: e.acct.Email, To: rcpts, DSN: dsn, Raw: raw}
			if keep {
				p.Use = useNames[Sent]
			}
			if _, perr := e.store.AddPending(p); perr != nil {
				e.reply(done, errors.Join(err, perr))
				return
			}
			e.reply(done, fmt.Errorf("%w; the message will be sent when it can be", err))
			return
		}
		if err != nil {
			e.reply(done, err)
			return
		}
		e.reply(done, nil)
		if keep {
			e.keepSent(raw)
		}
	})
}

// sentFlags are the flags of the copy of mail that was sent.
var sentFlags = []string{`\Seen`}

// keepSent puts a copy of mail that was sent in the sent folder, or keeps it
// to be put there when there is a connection.
func (e *Engine) keepSent(raw []byte) {
	e.do(func(c *remote.Conn) error {
		if c == nil {
			_, err := e.store.AddPending(store.Pending{Kind: store.PendingAppend, Use: useNames[Sent], Flags: sentFlags, Raw: raw})
			e.failed(err)
			return nil
		}
		folder, err := e.ensureFolder(c, Sent)
		if err == nil {
			_, _, err = c.Append(folder, raw, sentFlags)
		}
		if err != nil {
			e.failed(fmt.Errorf("the sent message was not put in the sent folder: %w", err))
			return err
		}
		e.requestSync(folder)
		return nil
	})
}

// pendingFolder returns the folder pending work is for: the one it names,
// or the one with its use, which is made if there is none.
func (e *Engine) pendingFolder(c *remote.Conn, p store.Pending) (string, error) {
	if p.Folder != "" || p.Use == "" {
		return p.Folder, nil
	}
	for use, name := range useNames {
		if name == p.Use {
			return e.ensureFolder(c, use)
		}
	}
	return "", fmt.Errorf("%q is not the use of a folder", p.Use)
}

// flushPending does what was put off for want of a connection.
func (e *Engine) flushPending(c *remote.Conn) error {
	pending, err := e.store.Pending()
	if err != nil {
		e.failed(fmt.Errorf("reading the outbox: %w", err))
	}
	if len(pending) == 0 || c == nil {
		return nil
	}
	for _, p := range pending {
		switch p.Kind {
		case store.PendingSend:
			e.status("Sending mail from the outbox…")
			if err := send.Send(e.acct.Outgoing, p.From, p.To, p.Raw, p.DSN); err != nil {
				e.status("")
				if !errors.Is(err, send.ErrUnreachable) {
					e.failed(fmt.Errorf("mail in the outbox could not be sent: %w", err))
				}
				continue
			}
			e.status("")
			if err := e.store.RemovePending(p.ID); err != nil {
				e.failed(err)
			}
			folder, err := e.pendingFolder(c, p)
			if err == nil && folder != "" {
				_, _, err = c.Append(folder, p.Raw, sentFlags)
			}
			if err != nil {
				e.failed(fmt.Errorf("the sent message was not put in the sent folder: %w", err))
			} else if folder != "" {
				e.requestSync(folder)
			}
		case store.PendingAppend:
			folder, err := e.pendingFolder(c, p)
			if err == nil {
				_, _, err = c.Append(folder, p.Raw, p.Flags)
			}
			if err != nil {
				e.failed(fmt.Errorf("a message could not be put in its folder: %w", err))
				if isConnErr(err) {
					return err
				}
				continue
			}
			if err := e.store.RemovePending(p.ID); err != nil {
				e.failed(err)
			}
			e.requestSync(folder)
		}
	}
	return nil
}
