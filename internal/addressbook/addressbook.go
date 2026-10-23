// Package addressbook keeps the names and addresses of the people mail is
// written to, in an encrypted JSON file. It has no dependency on the UI.
package addressbook

import (
	"errors"
	"fmt"
	"net/mail"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nomad-software/apemail/internal/crypt"
	"github.com/nomad-software/apemail/internal/jsonfile"
)

// Contact is a person in the address book.
type Contact struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email"`
}

// Book is the address book: each address once, in the order they are shown,
// by name and then by address.
type Book struct {
	Contacts []Contact `json:"contacts"`
}

// Path returns the path of the address book among the accounts in dir. It
// is in a folder of its own, as a file of its own beside them would be taken
// for an account, so that it is moved and encrypted again with them.
func Path(dir string) string {
	return filepath.Join(dir, "addressbook", "contacts.json")
}

// Load reads the address book at path with keys. A missing file is an empty
// book.
func Load(path string, keys *crypt.Keys) (*Book, error) {
	if keys == nil {
		return nil, errors.New("the address book is not read without keys to decrypt it with")
	}
	b := &Book{}
	if err := jsonfile.LoadWith(path, b, keys); err != nil {
		return nil, err
	}
	return b, nil
}

// Save writes the address book to path, encrypted with keys.
func (b *Book) Save(path string, keys *crypt.Keys) error {
	if keys == nil {
		return errors.New("the address book is not saved without keys to encrypt it with")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return jsonfile.SaveWith(path, b, keys)
}

// Clone returns a copy of the book, which can be saved while the book is
// changed.
func (b *Book) Clone() *Book {
	return &Book{Contacts: slices.Clone(b.Contacts)}
}

// normalize returns a contact as it is kept: trimmed, and with an address
// that is one.
func normalize(c Contact) (Contact, error) {
	c.Name = strings.TrimSpace(c.Name)
	c.Email = strings.TrimSpace(c.Email)
	if c.Email == "" {
		return c, errors.New("the contact has no email address")
	}
	a, err := mail.ParseAddress(c.Email)
	if err != nil || a.Name != "" || a.Address != c.Email {
		return c, fmt.Errorf("%q is not an email address", c.Email)
	}
	return c, nil
}

// find returns where the address is in the book, or -1. Addresses are the
// same whatever their case, as servers treat them.
func (b *Book) find(email string) int {
	email = strings.TrimSpace(email)
	return slices.IndexFunc(b.Contacts, func(c Contact) bool { return strings.EqualFold(c.Email, email) })
}

// Has reports if the address is in the book.
func (b *Book) Has(email string) bool {
	return b.find(email) >= 0
}

// Add puts a contact in the book, unless its address is there already, in
// which case it is only given the name if it had none. It reports if the
// book changed.
func (b *Book) Add(c Contact) (bool, error) {
	c, err := normalize(c)
	if err != nil {
		return false, err
	}
	if i := b.find(c.Email); i >= 0 {
		if b.Contacts[i].Name != "" || c.Name == "" {
			return false, nil
		}
		b.Contacts[i].Name = c.Name
	} else {
		b.Contacts = append(b.Contacts, c)
	}
	b.sort()
	return true, nil
}

// Change replaces the contact with the address old by c. It is an error if
// c has the address of another contact.
func (b *Book) Change(old string, c Contact) error {
	c, err := normalize(c)
	if err != nil {
		return err
	}
	i := b.find(old)
	if i < 0 {
		return fmt.Errorf("%s is not in the address book", old)
	}
	if j := b.find(c.Email); j >= 0 && j != i {
		return fmt.Errorf("%s is in the address book already", c.Email)
	}
	b.Contacts[i] = c
	b.sort()
	return nil
}

// Remove takes the contact with the address out of the book, and reports if
// it was there.
func (b *Book) Remove(email string) bool {
	i := b.find(email)
	if i < 0 {
		return false
	}
	b.Contacts = slices.Delete(b.Contacts, i, i+1)
	return true
}

// sort puts the contacts in the order they are shown: by name, those
// without one by their address, whatever the case.
func (b *Book) sort() {
	key := func(c Contact) string {
		if c.Name != "" {
			return strings.ToLower(c.Name)
		}
		return strings.ToLower(c.Email)
	}
	slices.SortStableFunc(b.Contacts, func(x, y Contact) int {
		if n := strings.Compare(key(x), key(y)); n != 0 {
			return n
		}
		return strings.Compare(strings.ToLower(x.Email), strings.ToLower(y.Email))
	})
}
