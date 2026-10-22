// Package store keeps the local copy of the mail of an account, encrypted.
// It is a cache of the mailbox on the server, which is where mail lives:
// anything in it can be thrown away and downloaded again.
//
// The copy of an account is a folder with a file listing its mail folders,
// and a folder for each mail folder. That has an index of the headers of its
// messages, which is all the message list needs, and a file for each message
// that has been downloaded, which is the message as the server sent it. The
// folders are named by a hash of the name of the mail folder, so that the
// names are not seen without the keys.
//
// It has no dependency on the UI.
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/nomad-software/apemail/internal/crypt"
	"github.com/nomad-software/apemail/internal/jsonfile"
)

// Folder is a mail folder of the mailbox.
type Folder struct {
	// Name is the full name of the folder on the server.
	Name string `json:"name"`
	// Delim separates the parts of the name of a folder within another, or
	// is empty if the server does not nest folders.
	Delim string `json:"delim,omitempty"`
	// Attrs are the attributes the server gives the folder, such as
	// \Noselect, or \Sent for the folder sent mail is kept in.
	Attrs []string `json:"attrs,omitempty"`
}

// Has reports if the folder has the attribute, which is matched without
// regard to case, as IMAP does.
func (f Folder) Has(attr string) bool {
	return slices.ContainsFunc(f.Attrs, func(a string) bool { return equalFold(a, attr) })
}

// Selectable reports if the folder can hold messages.
func (f Folder) Selectable() bool {
	return !f.Has(`\Noselect`) && !f.Has(`\NonExistent`)
}

// Summary is what the message list shows of a message, which is fetched
// from the server without the message itself.
type Summary struct {
	UID       uint32    `json:"uid"`
	MessageID string    `json:"messageId,omitempty"`
	Subject   string    `json:"subject,omitempty"`
	From      string    `json:"from,omitempty"`
	To        string    `json:"to,omitempty"`
	Date      time.Time `json:"date"`
	Flags     []string  `json:"flags,omitempty"`
	Size      int64     `json:"size,omitempty"`
}

// Has reports if the message has the flag, which is matched without regard
// to case, as IMAP does.
func (s Summary) Has(flag string) bool {
	return slices.ContainsFunc(s.Flags, func(f string) bool { return equalFold(f, flag) })
}

// Index is the list of the messages of a folder.
type Index struct {
	Folder string `json:"folder"`
	// UIDValidity changes if the server numbers the messages of the folder
	// anew, after which the UIDs that are kept mean nothing.
	UIDValidity uint32 `json:"uidValidity"`
	// HighestModSeq is the server's count of the changes to the folder
	// (CONDSTORE, RFC 7162) when the index was brought up to date, so that
	// the next sync asks only for what has changed since. It is zero for a
	// server without it.
	HighestModSeq uint64 `json:"highestModSeq,omitempty"`
	// Messages are in the order of their UIDs.
	Messages []Summary `json:"messages"`
}

// Find returns the position of the message with the UID, and whether it is
// there.
func (ix *Index) Find(uid uint32) (int, bool) {
	return slices.BinarySearchFunc(ix.Messages, uid, func(s Summary, uid uint32) int {
		switch {
		case s.UID < uid:
			return -1
		case s.UID > uid:
			return 1
		}
		return 0
	})
}

// Put adds the message, or replaces the one with the same UID.
func (ix *Index) Put(s Summary) {
	i, found := ix.Find(s.UID)
	if found {
		ix.Messages[i] = s
		return
	}
	ix.Messages = slices.Insert(ix.Messages, i, s)
}

// Remove takes the message with the UID out, reporting if it was there.
func (ix *Index) Remove(uid uint32) bool {
	i, found := ix.Find(uid)
	if found {
		ix.Messages = slices.Delete(ix.Messages, i, i+1)
	}
	return found
}

// Store is the local copy of the mail of an account.
type Store struct {
	dir  string
	keys *crypt.Keys
}

// Open returns the copy kept in dir, which is encrypted with keys. Nothing is
// read or written without keys.
func Open(dir string, keys *crypt.Keys) (*Store, error) {
	if keys == nil {
		return nil, errors.New("mail is not kept without keys to encrypt it with")
	}
	return &Store{dir: dir, keys: keys}, nil
}

// folderDir returns the folder the copy of the mail folder is kept in.
func (s *Store) folderDir(folder string) string {
	sum := sha256.Sum256([]byte(folder))
	return filepath.Join(s.dir, "f-"+hex.EncodeToString(sum[:12]))
}

// Folders returns the mail folders as they were last listed, which is none if
// they never were.
func (s *Store) Folders() ([]Folder, error) {
	var folders []Folder
	err := jsonfile.LoadWith(filepath.Join(s.dir, "folders.json"), &folders, s.keys)
	return folders, err
}

// SaveFolders keeps the list of the mail folders, and throws away the copies
// of those that are no longer on it.
func (s *Store) SaveFolders(folders []Folder) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	if err := jsonfile.SaveWith(filepath.Join(s.dir, "folders.json"), folders, s.keys); err != nil {
		return err
	}

	keep := map[string]bool{}
	for _, f := range folders {
		keep[filepath.Base(s.folderDir(f.Name))] = true
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return err
	}
	var failed []error
	for _, e := range entries {
		if e.IsDir() && len(e.Name()) > 2 && e.Name()[:2] == "f-" && !keep[e.Name()] {
			failed = append(failed, os.RemoveAll(filepath.Join(s.dir, e.Name())))
		}
	}
	return errors.Join(failed...)
}

// Index returns the index of the folder, which is empty if there is none.
func (s *Store) Index(folder string) (*Index, error) {
	ix := &Index{Folder: folder}
	err := jsonfile.LoadWith(filepath.Join(s.folderDir(folder), "index.json"), ix, s.keys)
	if err != nil {
		return &Index{Folder: folder}, err
	}
	// A hash that two names share would mix up their folders, which the
	// name kept inside tells apart.
	if ix.Folder != folder {
		return &Index{Folder: folder}, nil
	}
	return ix, nil
}

// SaveIndex keeps the index of its folder, compressed, as that of a large
// folder is large and is written often.
func (s *Store) SaveIndex(ix *Index) error {
	dir := s.folderDir(ix.Folder)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return jsonfile.SaveCompressed(filepath.Join(dir, "index.json"), ix, s.keys)
}

// ResetFolder throws away the copy of the folder, as when the server numbers
// its messages anew.
func (s *Store) ResetFolder(folder string) error {
	return os.RemoveAll(s.folderDir(folder))
}

// messagePath returns the path of the file of a message.
func (s *Store) messagePath(folder string, uid uint32) string {
	return filepath.Join(s.folderDir(folder), strconv.FormatUint(uint64(uid), 10)+".eml")
}

// Message returns the message with the UID in the folder, as the server sent
// it. It is an error satisfying errors.Is(err, fs.ErrNotExist) if it has not
// been downloaded.
func (s *Store) Message(folder string, uid uint32) ([]byte, error) {
	return jsonfile.LoadRaw(s.messagePath(folder, uid), s.keys)
}

// SaveMessage keeps the message with the UID in the folder.
func (s *Store) SaveMessage(folder string, uid uint32, raw []byte) error {
	dir := s.folderDir(folder)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return jsonfile.SaveRaw(s.messagePath(folder, uid), raw, s.keys)
}

// RemoveMessage throws away the copy of the message, if there is one.
func (s *Store) RemoveMessage(folder string, uid uint32) error {
	err := os.Remove(s.messagePath(folder, uid))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// Pending is something to be done on the servers which could not be when it
// was asked for, as there was no connection.
type Pending struct {
	// ID names its file.
	ID string `json:"id"`
	// Kind is PendingSend or PendingAppend.
	Kind string `json:"kind"`
	// Folder is where an append is to, or, for mail to send, where a copy
	// of it goes. Use names the use of the folder instead, as "sent" or
	// "drafts", when it is found, or made, once there is a connection.
	Folder string `json:"folder,omitempty"`
	Use    string `json:"use,omitempty"`
	// Flags are those of a message that is appended.
	Flags []string `json:"flags,omitempty"`
	// From and To are the addresses of the envelope of mail to send.
	From string   `json:"from,omitempty"`
	To   []string `json:"to,omitempty"`
	// DSN asks the server for delivery status notifications of mail to
	// send.
	DSN bool `json:"dsn,omitempty"`
	// Raw is the message.
	Raw []byte `json:"raw"`
	// Created is when it was asked for.
	Created time.Time `json:"created"`
}

// The kinds of pending work.
const (
	// PendingSend is mail to send.
	PendingSend = "send"
	// PendingAppend is a message to put in a folder, as a draft.
	PendingAppend = "append"
)

// outboxDir is the folder pending work is kept in.
func (s *Store) outboxDir() string {
	return filepath.Join(s.dir, "outbox")
}

// AddPending keeps work to do later, giving it its ID.
func (s *Store) AddPending(p Pending) (Pending, error) {
	if err := os.MkdirAll(s.outboxDir(), 0o700); err != nil {
		return p, err
	}
	if p.Created.IsZero() {
		p.Created = time.Now()
	}
	p.ID = fmt.Sprintf("%d", p.Created.UnixNano())
	return p, jsonfile.SaveWith(filepath.Join(s.outboxDir(), p.ID+".json"), p, s.keys)
}

// Pending returns the work there is to do, oldest first.
func (s *Store) Pending() ([]Pending, error) {
	entries, err := os.ReadDir(s.outboxDir())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var pending []Pending
	var failed []error
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		var p Pending
		if err := jsonfile.LoadWith(filepath.Join(s.outboxDir(), e.Name()), &p, s.keys); err != nil {
			failed = append(failed, err)
			continue
		}
		pending = append(pending, p)
	}
	slices.SortFunc(pending, func(a, b Pending) int { return a.Created.Compare(b.Created) })
	return pending, errors.Join(failed...)
}

// RemovePending throws away work that has been done.
func (s *Store) RemovePending(id string) error {
	if _, err := strconv.ParseUint(id, 10, 64); err != nil {
		return fmt.Errorf("%q names no pending work", id)
	}
	err := os.Remove(filepath.Join(s.outboxDir(), id+".json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// equalFold compares ASCII strings without regard to case.
func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range len(a) {
		x, y := a[i], b[i]
		if 'A' <= x && x <= 'Z' {
			x += 'a' - 'A'
		}
		if 'A' <= y && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}
