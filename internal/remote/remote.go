// Package remote talks to the IMAP server of an account.
//
// It is careful with the mailbox, which is the user's and not ours. Folders
// are opened read-only (EXAMINE) unless something is to be changed in them,
// messages are downloaded without being marked as read (BODY.PEEK), and
// nothing that changes a mailbox is done unless the connection was opened
// with changes allowed. Messages are only ever expunged by UID, so that
// those another client has marked as deleted are left alone, and a server
// that cannot expunge by UID is not asked to.
//
// It has no dependency on the UI.
package remote

import (
	"bytes"
	"crypto/hmac"
	"crypto/md5"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-message/charset"
	"github.com/emersion/go-message/mail"
	"github.com/emersion/go-sasl"

	"github.com/nomad-software/apemail/internal/account"
	"github.com/nomad-software/apemail/internal/homedir"
	"github.com/nomad-software/apemail/internal/store"
)

// ErrReadOnly is the error of an operation that would change a mailbox on
// a connection that is not allowed to.
var ErrReadOnly = errors.New("this connection only reads the mailbox, and cannot change it")

// dialTimeout limits how long a server is given to answer a connection.
const dialTimeout = 30 * time.Second

// fetchChunk is how many messages are asked for at once.
const fetchChunk = 250

// Conn is a connection to an IMAP server, logged in. It is not safe to use
// from more than one goroutine at once.
type Conn struct {
	c *imapclient.Client
	// writable says the mailbox can be changed.
	writable bool
	// selected is the folder that is open, and readWrite whether it was
	// opened to be changed.
	selected  string
	readWrite bool
	// uidValidity is that of the folder that is open.
	uidValidity uint32
}

// Options are what a connection is made with, apart from the server.
type Options struct {
	// Writable allows the mailbox to be changed.
	Writable bool
	// Unilateral handles what the server says of its own accord, as while
	// idling.
	Unilateral *imapclient.UnilateralDataHandler
}

// wordDecoder decodes the encoded words of headers in any character set.
var wordDecoder = &mime.WordDecoder{CharsetReader: charset.Reader}

// TLSConfig returns the configuration of TLS for the server, with the client
// certificate if it authenticates with one.
func TLSConfig(s account.Server) (*tls.Config, error) {
	config := &tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12}
	if s.Auth == account.AuthTLSCert {
		if s.ClientCert == "" || s.ClientKey == "" {
			return nil, errors.New("a client certificate and its key are needed")
		}
		cert, err := tls.LoadX509KeyPair(homedir.Expand(s.ClientCert), homedir.Expand(s.ClientKey))
		if err != nil {
			return nil, fmt.Errorf("the client certificate cannot be used: %w", err)
		}
		config.Certificates = []tls.Certificate{cert}
	}
	return config, nil
}

// Dial connects to the IMAP server and logs in.
func Dial(s account.Server, opts Options) (*Conn, error) {
	config, err := TLSConfig(s)
	if err != nil {
		return nil, err
	}
	options := &imapclient.Options{
		TLSConfig:             config,
		WordDecoder:           wordDecoder,
		Dialer:                &net.Dialer{Timeout: dialTimeout},
		UnilateralDataHandler: opts.Unilateral,
	}

	var c *imapclient.Client
	switch s.Security {
	case account.SecurityTLS:
		c, err = imapclient.DialTLS(s.Address(), options)
	case account.SecurityStartTLS:
		c, err = imapclient.DialStartTLS(s.Address(), options)
	case account.SecurityNone:
		c, err = imapclient.DialInsecure(s.Address(), options)
	default:
		return nil, fmt.Errorf("%q is not a kind of security", s.Security)
	}
	if err != nil {
		return nil, fmt.Errorf("connecting to %s: %w", s.Address(), err)
	}
	if err := c.WaitGreeting(); err != nil {
		c.Close()
		return nil, fmt.Errorf("connecting to %s: %w", s.Address(), err)
	}

	if err := login(c, s); err != nil {
		c.Close()
		return nil, err
	}
	return &Conn{c: c, writable: opts.Writable}, nil
}

// login authenticates with the server as the account says to.
func login(c *imapclient.Client, s account.Server) error {
	caps := c.Caps()
	if caps.Has(imap.CapLoginDisabled) && s.Auth != account.AuthTLSCert {
		return errors.New("the server does not allow logging in over this connection; it may need SSL/TLS or STARTTLS")
	}

	var err error
	switch s.Auth {
	case account.AuthPassword:
		if caps.Has(imap.Cap("AUTH=PLAIN")) {
			err = c.Authenticate(sasl.NewPlainClient("", s.Username, s.Password))
		} else {
			err = c.Login(s.Username, s.Password).Wait()
		}
	case account.AuthEncryptedPassword:
		if !caps.Has(imap.Cap("AUTH=CRAM-MD5")) {
			return errors.New("the server does not support encrypted passwords (CRAM-MD5)")
		}
		err = c.Authenticate(NewCramMD5Client(s.Username, s.Password))
	case account.AuthTLSCert:
		if !caps.Has(imap.Cap("AUTH=EXTERNAL")) {
			return errors.New("the server does not support logging in with a certificate (EXTERNAL)")
		}
		err = c.Authenticate(sasl.NewExternalClient(""))
	default:
		return fmt.Errorf("%q is not a kind of authentication that is supported", s.Auth)
	}
	if err != nil {
		return fmt.Errorf("logging in: %w", err)
	}
	return nil
}

// Close logs out and closes the connection.
func (c *Conn) Close() error {
	c.c.Logout().Wait()
	return c.c.Close()
}

// Abort closes the connection without logging out, which unblocks what is
// waiting on it.
func (c *Conn) Abort() {
	c.c.Close()
}

// Closed is closed once the connection is.
func (c *Conn) Closed() <-chan struct{} {
	return c.c.Closed()
}

// Folders lists the folders of the mailbox, in the order of their names with
// the inbox first.
func (c *Conn) Folders() ([]store.Folder, error) {
	var options *imap.ListOptions
	if c.c.Caps().Has(imap.CapSpecialUse) {
		options = &imap.ListOptions{ReturnSpecialUse: true}
	}
	list, err := c.c.List("", "*", options).Collect()
	if err != nil {
		return nil, fmt.Errorf("listing the folders: %w", err)
	}
	folders := make([]store.Folder, 0, len(list))
	for _, l := range list {
		f := store.Folder{Name: l.Mailbox}
		if l.Delim != 0 {
			f.Delim = string(l.Delim)
		}
		for _, a := range l.Attrs {
			f.Attrs = append(f.Attrs, string(a))
		}
		folders = append(folders, f)
	}
	SortFolders(folders)
	return folders, nil
}

// SortFolders puts the inbox first and the rest in the order of their names.
func SortFolders(folders []store.Folder) {
	slices.SortStableFunc(folders, func(a, b store.Folder) int {
		ai, bi := IsInbox(a.Name), IsInbox(b.Name)
		switch {
		case ai && !bi:
			return -1
		case bi && !ai:
			return 1
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
}

// IsInbox reports if the name is that of the inbox, which IMAP matches
// without regard to case.
func IsInbox(name string) bool {
	return strings.EqualFold(name, "INBOX")
}

// examine opens the folder to be read from, which changes nothing in it.
func (c *Conn) examine(folder string) (*imap.SelectData, error) {
	if c.selected == folder && !c.readWrite {
		return nil, nil
	}
	// CONDSTORE has the server say how many changes the folder has had,
	// so that a sync can ask for those since the last.
	options := &imap.SelectOptions{ReadOnly: true, CondStore: c.c.Caps().Has(imap.CapCondStore)}
	data, err := c.c.Select(folder, options).Wait()
	if err != nil {
		c.selected = ""
		return nil, fmt.Errorf("opening %s: %w", folder, err)
	}
	c.selected, c.readWrite, c.uidValidity = folder, false, data.UIDValidity
	return data, nil
}

// Unread returns how many messages of each of the folders are unread
// (STATUS), which is asked without opening them, and so changes nothing.
// Every folder is asked about before any answer is waited for, so that many
// cost little more than one.
func (c *Conn) Unread(folders ...string) (map[string]int, error) {
	commands := make([]*imapclient.StatusCommand, len(folders))
	for i, f := range folders {
		commands[i] = c.c.Status(f, &imap.StatusOptions{NumUnseen: true})
	}
	unread := map[string]int{}
	var errs []error
	for i, cmd := range commands {
		data, err := cmd.Wait()
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("counting the unread messages of %s: %w", folders[i], err))
		case data.NumUnseen == nil:
			errs = append(errs, fmt.Errorf("the server did not say how many messages of %s are unread", folders[i]))
		default:
			unread[folders[i]] = int(*data.NumUnseen)
		}
	}
	return unread, errors.Join(errs...)
}

// open opens the folder to be changed, which is not allowed unless the
// connection is writable.
func (c *Conn) open(folder string) error {
	if !c.writable {
		return ErrReadOnly
	}
	if c.selected == folder && c.readWrite {
		return nil
	}
	data, err := c.c.Select(folder, nil).Wait()
	if err != nil {
		c.selected = ""
		return fmt.Errorf("opening %s: %w", folder, err)
	}
	c.selected, c.readWrite, c.uidValidity = folder, true, data.UIDValidity
	return nil
}

// Update is what has changed in a folder since its index was made.
type Update struct {
	// Reset says the server has numbered the messages anew, so that the
	// index and the messages kept are to be thrown away. Added then has
	// every message.
	Reset       bool
	UIDValidity uint32
	// Added are the summaries of messages that are new.
	Added []store.Summary
	// Flags are the flags of the messages that were already known, by UID.
	Flags map[uint32][]string
	// Removed are the UIDs of messages that are no longer in the folder.
	Removed []uint32
	// HighestModSeq is the server's count of the changes to the folder
	// (CONDSTORE) when it was synchronised, or zero for a server without
	// it, from which the next sync asks for what has changed.
	HighestModSeq uint64
}

// Sync finds what has changed in the folder since the index was made. It
// opens the folder read-only, and so changes nothing. progress, if it is not
// nil, is given the summaries of new messages as they arrive.
//
// A server with CONDSTORE (RFC 7162) is asked only for the messages that
// have changed since the index was made, and nothing at all if none has, so
// that a large folder costs little to keep up to date. Any other is asked
// for the flags of every message.
func (c *Conn) Sync(ix *store.Index, progress func([]store.Summary)) (*Update, error) {
	// The folder is opened anew, so that what it holds is up to date.
	c.selected = ""
	data, err := c.examine(ix.Folder)
	if err != nil {
		return nil, err
	}

	u := &Update{UIDValidity: data.UIDValidity, Flags: map[uint32][]string{}, HighestModSeq: data.HighestModSeq}
	known := map[uint32]bool{}
	if ix.UIDValidity != data.UIDValidity {
		u.Reset = true
	} else {
		for _, s := range ix.Messages {
			known[s.UID] = true
		}
	}

	if data.NumMessages == 0 {
		u.Removed = goneFrom(known, nil)
		return u, nil
	}

	var added []imap.UID
	found := false
	// A count of changes lower than the index's, as of a server restored
	// from a backup, cannot say what has changed since.
	if !u.Reset && ix.HighestModSeq != 0 && data.HighestModSeq >= ix.HighestModSeq {
		added, found, err = c.changedSince(ix.HighestModSeq, data, known, u)
		if err != nil {
			return nil, fmt.Errorf("listing the changes to %s: %w", ix.Folder, err)
		}
	}
	if !found {
		clear(u.Flags)
		u.Removed = nil
		added, err = c.everything(known, u)
		if err != nil {
			return nil, fmt.Errorf("listing the messages of %s: %w", ix.Folder, err)
		}
	}

	// The newest are fetched first, as they are the ones looked for.
	slices.Sort(added)
	slices.Reverse(added)
	for chunk := range slices.Chunk(added, fetchChunk) {
		summaries, err := c.summaries(imap.UIDSetNum(chunk...))
		if err != nil {
			return nil, err
		}
		u.Added = append(u.Added, summaries...)
		if progress != nil {
			progress(summaries)
		}
	}
	return u, nil
}

// everything finds what has changed in the folder that is open from the UIDs
// and flags of every message: the flags of those known, those that have gone
// and, returned, those that are new.
func (c *Conn) everything(known map[uint32]bool, u *Update) ([]imap.UID, error) {
	all := imap.SeqSet{}
	all.AddRange(1, 0)
	msgs, err := c.c.Fetch(all, &imap.FetchOptions{UID: true, Flags: true}).Collect()
	if err != nil {
		return nil, err
	}
	var added []imap.UID
	present := map[uint32]bool{}
	for _, m := range msgs {
		uid := uint32(m.UID)
		if uid == 0 {
			continue
		}
		present[uid] = true
		if known[uid] {
			u.Flags[uid] = flagStrings(m.Flags)
		} else {
			added = append(added, m.UID)
		}
	}
	u.Removed = goneFrom(known, present)
	return added, nil
}

// changedSince finds what has changed in the folder that is open since its
// count of changes was modSeq: the flags of the messages that have changed
// and, returned, those that are new, which are the only ones the server
// sends. A message that has gone need not change the count, so the number
// of messages says if any has, and only then are the UIDs of those left
// asked for. It reports false if the server's answer does not add up, and
// every message is to be asked for instead.
func (c *Conn) changedSince(modSeq uint64, data *imap.SelectData, known map[uint32]bool, u *Update) ([]imap.UID, bool, error) {
	count := int(data.NumMessages)
	// A new message changes the count, so the same count of changes and of
	// messages means nothing has.
	if data.HighestModSeq == modSeq && count == len(known) {
		return nil, true, nil
	}

	all := imap.SeqSet{}
	all.AddRange(1, 0)
	msgs, err := c.c.Fetch(all, &imap.FetchOptions{UID: true, Flags: true, ChangedSince: modSeq}).Collect()
	if err != nil {
		return nil, false, err
	}
	var added []imap.UID
	for _, m := range msgs {
		uid := uint32(m.UID)
		switch {
		case uid == 0:
		case known[uid]:
			u.Flags[uid] = flagStrings(m.Flags)
		default:
			added = append(added, m.UID)
		}
	}

	switch {
	case count == len(known)+len(added):
		// None has gone.
	case count > len(known)+len(added):
		// More are there than were known or are new: the server has
		// not said what changed.
		return nil, false, nil
	default:
		search, err := c.c.UIDSearch(&imap.SearchCriteria{}, nil).Wait()
		if err != nil {
			return nil, false, err
		}
		uids, ok := search.All.(imap.UIDSet)
		left, done := uids.Nums()
		if !ok || !done {
			return nil, false, nil
		}
		present := map[uint32]bool{}
		for _, uid := range left {
			present[uint32(uid)] = true
		}
		u.Removed = goneFrom(known, present)
	}
	return added, true, nil
}

// goneFrom returns, in order, the UIDs that are known but not present.
func goneFrom(known, present map[uint32]bool) []uint32 {
	var gone []uint32
	for uid := range known {
		if !present[uid] {
			gone = append(gone, uid)
		}
	}
	slices.Sort(gone)
	return gone
}

// summaries fetches the summaries of the messages with the UIDs in the folder
// that is open.
func (c *Conn) summaries(uids imap.UIDSet) ([]store.Summary, error) {
	msgs, err := c.c.Fetch(uids, &imap.FetchOptions{
		UID:          true,
		Flags:        true,
		Envelope:     true,
		RFC822Size:   true,
		InternalDate: true,
	}).Collect()
	if err != nil {
		return nil, fmt.Errorf("fetching the headers of %s: %w", c.selected, err)
	}
	summaries := make([]store.Summary, 0, len(msgs))
	for _, m := range msgs {
		if m.UID == 0 {
			continue
		}
		s := store.Summary{
			UID:   uint32(m.UID),
			Flags: flagStrings(m.Flags),
			Size:  m.RFC822Size,
			Date:  m.InternalDate,
		}
		if e := m.Envelope; e != nil {
			s.Subject = e.Subject
			s.MessageID = e.MessageID
			s.From = FormatAddresses(e.From)
			s.To = FormatAddresses(e.To)
			if !e.Date.IsZero() {
				s.Date = e.Date
			}
		}
		summaries = append(summaries, s)
	}
	return summaries, nil
}

// FormatAddresses returns the addresses as they are written in a header.
func FormatAddresses(addrs []imap.Address) string {
	var parts []string
	for _, a := range addrs {
		if a.IsGroupStart() || a.IsGroupEnd() {
			continue
		}
		parts = append(parts, (&mail.Address{Name: a.Name, Address: a.Addr()}).String())
	}
	return strings.Join(parts, ", ")
}

// flagStrings returns the flags as strings.
func flagStrings(flags []imap.Flag) []string {
	s := make([]string, len(flags))
	for i, f := range flags {
		s[i] = string(f)
	}
	return s
}

// Fetch downloads the messages with the UIDs in the folder, without marking
// them as read, and gives each to got as it arrives. uidValidity is that of
// the index the UIDs are from; if the server has numbered the messages anew
// since, nothing is fetched, as the UIDs could be of other messages.
func (c *Conn) Fetch(folder string, uidValidity uint32, uids []uint32, got func(uid uint32, raw []byte) error) error {
	if _, err := c.examine(folder); err != nil {
		return err
	}
	if c.uidValidity != uidValidity {
		return fmt.Errorf("the messages of %s have been numbered anew; it needs to be synchronised", folder)
	}
	section := &imap.FetchItemBodySection{Peek: true}
	for chunk := range slices.Chunk(uids, 50) {
		set := imap.UIDSet{}
		for _, uid := range chunk {
			set.AddNum(imap.UID(uid))
		}
		cmd := c.c.Fetch(set, &imap.FetchOptions{UID: true, BodySection: []*imap.FetchItemBodySection{section}})
		for {
			msg := cmd.Next()
			if msg == nil {
				break
			}
			buf, err := msg.Collect()
			if err != nil {
				cmd.Close()
				return fmt.Errorf("downloading from %s: %w", folder, err)
			}
			raw := buf.FindBodySection(section)
			if buf.UID == 0 || raw == nil {
				continue
			}
			if err := got(uint32(buf.UID), raw); err != nil {
				cmd.Close()
				return err
			}
		}
		if err := cmd.Close(); err != nil {
			return fmt.Errorf("downloading from %s: %w", folder, err)
		}
	}
	return nil
}

// uidSet returns the UIDs as a set.
func uidSet(uids []uint32) imap.UIDSet {
	set := imap.UIDSet{}
	for _, uid := range uids {
		set.AddNum(imap.UID(uid))
	}
	return set
}

// checkUIDs opens the folder to be changed and checks that the UIDs, from an
// index with uidValidity, still mean the messages they did.
func (c *Conn) checkUIDs(folder string, uidValidity uint32, uids []uint32) error {
	if len(uids) == 0 {
		return errors.New("no messages were given")
	}
	if err := c.open(folder); err != nil {
		return err
	}
	if c.uidValidity != uidValidity {
		return fmt.Errorf("the messages of %s have been numbered anew; it needs to be synchronised", folder)
	}
	return nil
}

// SetFlags adds the flags to the messages, or takes them away if add is
// false. The flags can be \Seen, \Flagged or \Answered; \Deleted is only
// set by Delete, which expunges what it marks.
func (c *Conn) SetFlags(folder string, uidValidity uint32, uids []uint32, add bool, flags ...string) error {
	for _, f := range flags {
		if strings.EqualFold(f, string(imap.FlagDeleted)) {
			return errors.New(`\Deleted is only set when deleting`)
		}
	}
	if err := c.checkUIDs(folder, uidValidity, uids); err != nil {
		return err
	}
	op := imap.StoreFlagsAdd
	if !add {
		op = imap.StoreFlagsDel
	}
	store := &imap.StoreFlags{Op: op, Silent: true}
	for _, f := range flags {
		store.Flags = append(store.Flags, imap.Flag(f))
	}
	if err := c.c.Store(uidSet(uids), store, nil).Close(); err != nil {
		return fmt.Errorf("setting flags in %s: %w", folder, err)
	}
	return nil
}

// canExpungeByUID reports if the server can expunge particular messages,
// rather than every message marked as deleted.
func (c *Conn) canExpungeByUID() bool {
	return c.c.Caps().Has(imap.CapUIDPlus)
}

// Move moves the messages to another folder. A server without MOVE is asked
// to copy them and then expunge the originals by UID; one that cannot do
// that either is not asked to move them at all, as it would expunge every
// message marked as deleted in the folder, not just these.
func (c *Conn) Move(folder string, uidValidity uint32, uids []uint32, to string) error {
	if folder == to {
		return errors.New("the messages are already in " + to)
	}
	if err := c.checkUIDs(folder, uidValidity, uids); err != nil {
		return err
	}
	if !c.c.Caps().Has(imap.CapMove) && !c.canExpungeByUID() {
		return errors.New("the server can neither move messages (MOVE) nor expunge particular ones (UIDPLUS), so it is not asked to")
	}
	if _, err := c.c.Move(uidSet(uids), to).Wait(); err != nil {
		return fmt.Errorf("moving messages from %s to %s: %w", folder, to, err)
	}
	return nil
}

// Create makes the folder on the server and subscribes to it. A folder that
// is already there is not an error. use is the special use (RFC 6154) the
// folder is for, as \Archive, if it is for one, which a server that supports
// CREATE-SPECIAL-USE marks it with, so that other clients know it too.
func (c *Conn) Create(name, use string) error {
	if !c.writable {
		return ErrReadOnly
	}
	var options *imap.CreateOptions
	if use != "" && c.c.Caps().Has(imap.CapCreateSpecialUse) {
		options = &imap.CreateOptions{SpecialUse: []imap.MailboxAttr{imap.MailboxAttr(use)}}
	}
	if err := c.c.Create(name, options).Wait(); err != nil {
		var imapErr *imap.Error
		if !errors.As(err, &imapErr) || imapErr.Code != imap.ResponseCodeAlreadyExists {
			return fmt.Errorf("making the folder %s: %w", name, err)
		}
	}
	// A client that only shows the folders subscribed to shows it too.
	c.c.Subscribe(name).Wait()
	return nil
}

// PersonalPrefix returns what the names of the user's own folders start
// with, such as "INBOX." on servers which keep every folder within the
// inbox, or "" on those that do not or cannot say.
func (c *Conn) PersonalPrefix() string {
	if !c.c.Caps().Has(imap.CapNamespace) {
		return ""
	}
	data, err := c.c.Namespace().Wait()
	if err != nil || len(data.Personal) == 0 {
		return ""
	}
	return data.Personal[0].Prefix
}

// Delete deletes the messages from the folder for good: they are marked as
// deleted and then expunged by UID. A server that cannot expunge by UID is
// not asked to, as it would expunge every message marked as deleted in the
// folder, not just these.
func (c *Conn) Delete(folder string, uidValidity uint32, uids []uint32) error {
	if err := c.checkUIDs(folder, uidValidity, uids); err != nil {
		return err
	}
	if !c.canExpungeByUID() {
		return errors.New("the server cannot expunge particular messages (UIDPLUS), so it is not asked to delete any")
	}
	set := uidSet(uids)
	store := &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagDeleted}}
	if err := c.c.Store(set, store, nil).Close(); err != nil {
		return fmt.Errorf("marking messages in %s as deleted: %w", folder, err)
	}
	if err := c.c.UIDExpunge(set).Close(); err != nil {
		return fmt.Errorf("expunging messages from %s: %w", folder, err)
	}
	return nil
}

// Append puts the message in the folder with the flags, returning its UID
// and the UID validity of the folder if the server says what they are.
func (c *Conn) Append(folder string, raw []byte, flags []string) (uid, uidValidity uint32, err error) {
	if !c.writable {
		return 0, 0, ErrReadOnly
	}
	options := &imap.AppendOptions{Time: time.Now()}
	for _, f := range flags {
		options.Flags = append(options.Flags, imap.Flag(f))
	}
	cmd := c.c.Append(folder, int64(len(raw)), options)
	if _, err := io.Copy(cmd, bytes.NewReader(raw)); err != nil {
		cmd.Close()
		return 0, 0, fmt.Errorf("saving to %s: %w", folder, err)
	}
	if err := cmd.Close(); err != nil {
		return 0, 0, fmt.Errorf("saving to %s: %w", folder, err)
	}
	data, err := cmd.Wait()
	if err != nil {
		return 0, 0, fmt.Errorf("saving to %s: %w", folder, err)
	}
	return uint32(data.UID), data.UIDValidity, nil
}

// CanIdle reports if the server can tell the client of new mail as it
// arrives.
func (c *Conn) CanIdle() bool {
	return c.c.Caps().Has(imap.CapIdle)
}

// Watch opens the folder read-only and idles in it, until stop is closed or
// the connection fails. The Unilateral handler of the connection is told of
// what changes.
func (c *Conn) Watch(folder string, stop <-chan struct{}) error {
	c.selected = ""
	if _, err := c.examine(folder); err != nil {
		return err
	}
	idle, err := c.c.Idle()
	if err != nil {
		return fmt.Errorf("waiting for mail in %s: %w", folder, err)
	}
	select {
	case <-stop:
	case <-c.c.Closed():
	}
	if err := idle.Close(); err != nil {
		return err
	}
	return idle.Wait()
}

// NewCramMD5Client returns a SASL client for CRAM-MD5, which proves that the
// password is known without sending it (RFC 2195).
func NewCramMD5Client(username, password string) sasl.Client {
	return &cramMD5{username: username, password: password}
}

type cramMD5 struct {
	username, password string
}

func (a *cramMD5) Start() (string, []byte, error) {
	return "CRAM-MD5", nil, nil
}

func (a *cramMD5) Next(challenge []byte) ([]byte, error) {
	mac := hmac.New(md5.New, []byte(a.password))
	mac.Write(challenge)
	return []byte(a.username + " " + hex.EncodeToString(mac.Sum(nil))), nil
}
