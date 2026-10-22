// Package account describes the mail accounts and keeps each in an encrypted
// JSON file of its own. It has no dependency on the UI.
package account

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nomad-software/apemail/internal/crypt"
	"github.com/nomad-software/apemail/internal/jsonfile"
)

// Security is how the connection to a server is protected.
type Security string

const (
	// SecurityNone sends everything, passwords included, as it is.
	SecurityNone Security = "none"
	// SecurityStartTLS starts without TLS and asks the server for it.
	SecurityStartTLS Security = "starttls"
	// SecurityTLS uses TLS from the start.
	SecurityTLS Security = "tls"
)

// Securities are the kinds of security, in the order they are offered.
var Securities = []Security{SecurityNone, SecurityStartTLS, SecurityTLS}

// String returns the name the security is shown by.
func (s Security) String() string {
	switch s {
	case SecurityNone:
		return "None"
	case SecurityStartTLS:
		return "STARTTLS"
	case SecurityTLS:
		return "SSL/TLS"
	}
	return string(s)
}

// Auth is how the client proves to a server who it is.
type Auth string

const (
	// AuthPassword sends the password as it is, which the connection
	// should protect: SASL PLAIN, or LOGIN if the server lacks it.
	AuthPassword Auth = "password"
	// AuthEncryptedPassword proves the password is known without sending
	// it: SASL CRAM-MD5.
	AuthEncryptedPassword Auth = "encrypted"
	// AuthTLSCert uses a client certificate, given in the TLS handshake:
	// SASL EXTERNAL.
	AuthTLSCert Auth = "tlscert"
)

// Auths are the kinds of authentication, in the order they are offered.
// Kerberos and NTLM are yet to be supported.
var Auths = []Auth{AuthPassword, AuthEncryptedPassword, AuthTLSCert}

// String returns the name the authentication is shown by.
func (a Auth) String() string {
	switch a {
	case AuthPassword:
		return "Normal password"
	case AuthEncryptedPassword:
		return "Encrypted password"
	case AuthTLSCert:
		return "TLS certificate"
	}
	return string(a)
}

// Server is how to reach and log in to a server.
type Server struct {
	Host     string   `json:"host"`
	Port     int      `json:"port"`
	Username string   `json:"username"`
	Password string   `json:"password,omitempty"`
	Security Security `json:"security"`
	Auth     Auth     `json:"auth"`
	// ClientCert and ClientKey are the paths of the PEM files of the
	// client certificate and its key, for AuthTLSCert.
	ClientCert string `json:"clientCert,omitempty"`
	ClientKey  string `json:"clientKey,omitempty"`
}

// Address returns the host and port of the server, as net.Dial takes them.
func (s Server) Address() string {
	return fmt.Sprintf("%s:%d", s.Host, s.Port)
}

// Folders are the folders that operations use. One that is empty is found
// from the special use the server marks a folder with, or its name.
type Folders struct {
	Sent    string `json:"sent,omitempty"`
	Drafts  string `json:"drafts,omitempty"`
	Trash   string `json:"trash,omitempty"`
	Archive string `json:"archive,omitempty"`
}

// Account is a mailbox on an IMAP server and the SMTP server mail from it is
// sent through.
type Account struct {
	// ID names the account's file and the folder its mail is kept in.
	ID string `json:"id"`

	// Name is the name of the user, as mail from them shows it.
	Name      string `json:"name"`
	Email     string `json:"email"`
	ReplyTo   string `json:"replyTo,omitempty"`
	Signature string `json:"signature,omitempty"`
	// SignatureFile is the path of a file of text or HTML to sign messages
	// with instead of Signature, read each time a message is written. It
	// can start with ~ for the home folder.
	SignatureFile string `json:"signatureFile,omitempty"`

	// Incoming is the IMAP server.
	Incoming Server `json:"incoming"`
	// OutgoingName describes the SMTP server, and Outgoing is how to reach
	// it.
	OutgoingName string `json:"outgoingName,omitempty"`
	Outgoing     Server `json:"outgoing"`

	Folders Folders `json:"folders,omitzero"`

	// ComposeHTML has messages written as HTML rather than plain text.
	ComposeHTML bool `json:"composeHtml,omitempty"`
	// QuoteBelow has a reply written below the message it quotes, rather
	// than above it.
	QuoteBelow bool `json:"quoteBelow,omitempty"`

	// CheckOnStart has the inbox checked for new mail when the client
	// starts.
	CheckOnStart bool `json:"checkOnStart"`

	// ArchiveBy is how archived mail is divided into folders within the
	// archive folder, as other clients do: by year (the default), by
	// month, or not at all.
	ArchiveBy ArchiveBy `json:"archiveBy,omitempty"`
}

// ArchiveBy is how archived mail is divided into folders.
type ArchiveBy string

const (
	// ArchiveYearly puts mail in a folder for the year it was sent in,
	// within the archive, as Archive/2026.
	ArchiveYearly ArchiveBy = ""
	// ArchiveMonthly puts mail in a folder for the month it was sent in,
	// within one for the year, as Archive/2026/2026-03.
	ArchiveMonthly ArchiveBy = "monthly"
	// ArchiveSingle puts all mail in the archive folder itself.
	ArchiveSingle ArchiveBy = "single"
)

// ArchiveBys are the ways of dividing archived mail, in the order they are
// offered.
var ArchiveBys = []ArchiveBy{ArchiveSingle, ArchiveYearly, ArchiveMonthly}

// String returns the name the way of dividing archived mail is shown by.
func (a ArchiveBy) String() string {
	switch a {
	case ArchiveSingle:
		return "A single folder"
	case ArchiveMonthly:
		return "A folder for each month"
	}
	return "A folder for each year"
}

// New returns an account with the defaults of one that is yet to be filled
// in.
func New() Account {
	return Account{
		ID: newID(),
		Incoming: Server{
			Port:     993,
			Security: SecurityTLS,
			Auth:     AuthPassword,
		},
		Outgoing: Server{
			Port:     465,
			Security: SecurityTLS,
			Auth:     AuthPassword,
		},
		CheckOnStart: true,
	}
}

// Title returns what the account is shown as.
func (a Account) Title() string {
	if a.Email != "" {
		return a.Email
	}
	if a.Name != "" {
		return a.Name
	}
	return "New account"
}

// Check returns what must be filled in before the account can be used.
func (a Account) Check() error {
	var missing []string
	if strings.TrimSpace(a.Email) == "" {
		missing = append(missing, "the email address")
	}
	if strings.TrimSpace(a.Incoming.Host) == "" {
		missing = append(missing, "the incoming server")
	}
	if a.Incoming.Port <= 0 || a.Incoming.Port > 65535 {
		missing = append(missing, "the port of the incoming server")
	}
	if a.Outgoing.Host != "" && (a.Outgoing.Port <= 0 || a.Outgoing.Port > 65535) {
		missing = append(missing, "the port of the outgoing server")
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("the account needs %s", strings.Join(missing, ", "))
}

// newID returns a name for an account that no other will have.
func newID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// Path returns the path of the file of the account with the id in dir.
func Path(dir, id string) string {
	return filepath.Join(dir, id+".json")
}

// CacheDir returns the folder the mail of the account with the id is kept in.
func CacheDir(dir, id string) string {
	return filepath.Join(dir, id)
}

// validID reports if id can name a file, so that one that has been tampered
// with cannot reach outside dir.
func validID(id string) bool {
	if id == "" {
		return false
	}
	for _, r := range id {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// List reads every account in dir with keys, in the order of their titles.
// An account that cannot be read is left out, and why is among the errors.
func List(dir string, keys *crypt.Keys) ([]Account, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var accounts []Account
	var failed []error
	for _, entry := range entries {
		id, ok := strings.CutSuffix(entry.Name(), ".json")
		if !ok || entry.IsDir() || !validID(id) {
			continue
		}
		var a Account
		if err := jsonfile.LoadWith(Path(dir, id), &a, keys); err != nil {
			failed = append(failed, fmt.Errorf("%s: %w", entry.Name(), err))
			continue
		}
		// The file names the account, whatever it says inside.
		a.ID = id
		accounts = append(accounts, a)
	}
	slices.SortStableFunc(accounts, func(a, b Account) int {
		return strings.Compare(strings.ToLower(a.Title()), strings.ToLower(b.Title()))
	})
	return accounts, errors.Join(failed...)
}

// Save writes the account to its file in dir, encrypted with keys.
func Save(dir string, a Account, keys *crypt.Keys) error {
	if keys == nil {
		return errors.New("accounts are not saved without keys to encrypt them with")
	}
	if !validID(a.ID) {
		return fmt.Errorf("%q cannot name an account", a.ID)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return jsonfile.SaveWith(Path(dir, a.ID), a, keys)
}

// Remove deletes the file of the account with the id in dir, and the copy of
// its mail kept here. The mailbox on the server is not touched.
func Remove(dir, id string) error {
	if !validID(id) {
		return fmt.Errorf("%q cannot name an account", id)
	}
	err := os.Remove(Path(dir, id))
	if errors.Is(err, fs.ErrNotExist) {
		err = nil
	}
	return errors.Join(err, os.RemoveAll(CacheDir(dir, id)))
}
