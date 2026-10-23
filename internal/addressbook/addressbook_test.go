package addressbook_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/nomad-software/apemail/internal/addressbook"
	"github.com/nomad-software/apemail/internal/crypt"
	"github.com/nomad-software/apemail/internal/crypt/crypttest"
)

func keys(t *testing.T) *crypt.Keys {
	t.Helper()
	public, private := crypttest.Pair(t, t.TempDir(), "id", "")
	k, err := crypt.Open(public, private, nil)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestSaveAndLoad(t *testing.T) {
	k := keys(t)
	path := addressbook.Path(t.TempDir())

	// A book not yet saved is empty.
	b, err := addressbook.Load(path, k)
	if err != nil || len(b.Contacts) != 0 {
		t.Fatalf("book = %+v, %v", b, err)
	}
	if _, err := b.Add(addressbook.Contact{Name: "Grace Hopper", Email: "grace@example.com"}); err != nil {
		t.Fatal(err)
	}
	if err := b.Save(path, k); err != nil {
		t.Fatal(err)
	}

	// Nothing is written in plain text, and only the user can read it.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("grace")) || bytes.Contains(data, []byte("Hopper")) {
		t.Error("the address book was saved unencrypted")
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", info.Mode())
	}
	if info, _ := os.Stat(filepath.Dir(path)); info.Mode().Perm() != 0o700 {
		t.Errorf("folder mode = %v", info.Mode())
	}

	got, err := addressbook.Load(path, k)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Contacts) != 1 || got.Contacts[0] != (addressbook.Contact{Name: "Grace Hopper", Email: "grace@example.com"}) {
		t.Errorf("loaded %+v", got.Contacts)
	}

	// Other keys cannot read it, and none are not used.
	if _, err := addressbook.Load(path, keys(t)); err == nil {
		t.Error("other keys read the address book")
	}
	if _, err := addressbook.Load(path, nil); err == nil {
		t.Error("the address book was read without keys")
	}
	if err := got.Save(path, nil); err == nil {
		t.Error("the address book was saved without keys")
	}
}

func TestNoDuplicates(t *testing.T) {
	b := &addressbook.Book{}
	add := func(name, email string, want bool) {
		t.Helper()
		changed, err := b.Add(addressbook.Contact{Name: name, Email: email})
		if err != nil {
			t.Fatal(err)
		}
		if changed != want {
			t.Errorf("adding %s <%s> changed the book: %v", name, email, changed)
		}
	}
	add("", "alan@example.com", true)
	add("Alan", "alan@example.com", true) // A name is given to one without.
	add("Someone Else", "ALAN@example.com", false)
	add("", " alan@example.com ", false)
	add("Grace", "grace@example.com", true)
	add("Grace", "grace@example.com", false)

	want := []addressbook.Contact{{Name: "Alan", Email: "alan@example.com"}, {Name: "Grace", Email: "grace@example.com"}}
	if len(b.Contacts) != len(want) {
		t.Fatalf("contacts = %+v", b.Contacts)
	}
	for i := range want {
		if b.Contacts[i] != want[i] {
			t.Errorf("contact %d = %+v, want %+v", i, b.Contacts[i], want[i])
		}
	}
	if !b.Has("Grace@Example.com") || b.Has("nobody@example.com") {
		t.Error("Has is wrong")
	}
}

func TestInvalid(t *testing.T) {
	b := &addressbook.Book{}
	for _, email := range []string{"", "   ", "not an address", "Grace <grace@example.com>", "a@b, c@d"} {
		if _, err := b.Add(addressbook.Contact{Name: "X", Email: email}); err == nil {
			t.Errorf("%q was added", email)
		}
	}
	if len(b.Contacts) != 0 {
		t.Errorf("contacts = %+v", b.Contacts)
	}
}

func TestChangeAndRemove(t *testing.T) {
	b := &addressbook.Book{}
	for _, c := range []addressbook.Contact{{Name: "Zed", Email: "zed@example.com"}, {Name: "Amy", Email: "amy@example.com"}} {
		if _, err := b.Add(c); err != nil {
			t.Fatal(err)
		}
	}
	// They are kept in order of name.
	if b.Contacts[0].Name != "Amy" {
		t.Errorf("contacts = %+v", b.Contacts)
	}

	// An address cannot be changed to that of another contact.
	if err := b.Change("zed@example.com", addressbook.Contact{Name: "Zed", Email: "AMY@example.com"}); err == nil {
		t.Error("two contacts were given one address")
	}
	if err := b.Change("nobody@example.com", addressbook.Contact{Email: "x@example.com"}); err == nil {
		t.Error("a contact that is not there was changed")
	}
	if err := b.Change("zed@example.com", addressbook.Contact{Name: "Zed", Email: "bad"}); err == nil {
		t.Error("an address was changed to one that is not")
	}
	// Its own address, in another case, is fine.
	if err := b.Change("zed@example.com", addressbook.Contact{Name: "Adam", Email: "Zed@example.com"}); err != nil {
		t.Fatal(err)
	}
	if b.Contacts[0] != (addressbook.Contact{Name: "Adam", Email: "Zed@example.com"}) || len(b.Contacts) != 2 {
		t.Errorf("contacts = %+v", b.Contacts)
	}

	if b.Remove("nobody@example.com") {
		t.Error("a contact that is not there was removed")
	}
	if !b.Remove("zed@EXAMPLE.com") || len(b.Contacts) != 1 || b.Contacts[0].Email != "amy@example.com" {
		t.Errorf("contacts = %+v", b.Contacts)
	}

	// A copy is not changed with the book.
	c := b.Clone()
	b.Remove("amy@example.com")
	if len(c.Contacts) != 1 {
		t.Errorf("copy = %+v", c.Contacts)
	}
}
