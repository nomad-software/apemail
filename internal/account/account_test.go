package account_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomad-software/apemail/internal/account"
	"github.com/nomad-software/apemail/internal/crypt"
	"github.com/nomad-software/apemail/internal/crypt/crypttest"
)

func TestSaveAndList(t *testing.T) {
	public, private := crypttest.Pair(t, t.TempDir(), "id", "")
	keys, err := crypt.Open(public, private, nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()

	a := account.New()
	a.Email = "zed@example.com"
	a.Incoming.Host = "imap.example.com"
	a.Incoming.Password = "hunter2"
	b := account.New()
	b.Email = "amy@example.com"
	for _, x := range []account.Account{a, b} {
		if err := account.Save(dir, x, keys); err != nil {
			t.Fatal(err)
		}
	}
	data, _ := os.ReadFile(account.Path(dir, a.ID))
	if bytes.Contains(data, []byte("hunter2")) {
		t.Error("the password was saved unencrypted")
	}

	list, err := account.List(dir, keys)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Email != "amy@example.com" || list[1].Incoming.Password != "hunter2" {
		t.Errorf("list = %+v", list)
	}

	if err := account.Save(dir, a, nil); err == nil {
		t.Error("an account was saved without keys")
	}

	os.MkdirAll(account.CacheDir(dir, a.ID), 0o700)
	if err := account.Remove(dir, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(account.CacheDir(dir, a.ID)); !os.IsNotExist(err) {
		t.Error("the copy of the mail was kept")
	}
	if err := account.Remove(dir, "../"+filepath.Base(dir)); err == nil {
		t.Error("a path outside the folder was removed")
	}
}

func TestCheck(t *testing.T) {
	a := account.New()
	if a.Check() == nil {
		t.Error("an empty account passed")
	}
	a.Email, a.Incoming.Host = "me@example.com", "imap.example.com"
	if err := a.Check(); err != nil {
		t.Error(err)
	}
}

// TestNames checks what the kinds of security, of authentication and of
// archiving are shown by, and that one the program does not know (from a
// file written by a later version) is shown as it is written.
func TestNames(t *testing.T) {
	names := map[string]string{
		account.SecurityNone.String():          "None",
		account.SecurityStartTLS.String():      "STARTTLS",
		account.SecurityTLS.String():           "SSL/TLS",
		account.Security("other").String():     "other",
		account.AuthPassword.String():          "Normal password",
		account.AuthEncryptedPassword.String(): "Encrypted password",
		account.AuthTLSCert.String():           "TLS certificate",
		account.Auth("other").String():         "other",
		account.ArchiveSingle.String():         "A single folder",
		account.ArchiveYearly.String():         "A folder for each year",
		account.ArchiveMonthly.String():        "A folder for each month",
	}
	for got, want := range names {
		if got != want {
			t.Errorf("name = %q, want %q", got, want)
		}
	}
	// An IPv6 address is put in brackets, as net.Dial takes it.
	if got := (account.Server{Host: "::1", Port: 993}).Address(); got != "[::1]:993" {
		t.Errorf("IPv6 address = %q", got)
	}
	if got := (account.Server{Host: "imap.example.com", Port: 993}).Address(); got != "imap.example.com:993" {
		t.Errorf("address = %q", got)
	}
}

func TestTitle(t *testing.T) {
	a := account.New()
	if got := a.Title(); got != "New account" {
		t.Errorf("title of a new account = %q", got)
	}
	a.Name = "Amy"
	if got := a.Title(); got != "Amy" {
		t.Errorf("title with a name = %q", got)
	}
	a.Email = "amy@example.com"
	if got := a.Title(); got != "amy@example.com" {
		t.Errorf("title with an address = %q", got)
	}
}

// TestSaveRefuses checks that an account is never saved without keys, which
// would leave its passwords readable, nor by a name that reaches outside the
// folder of the accounts, and that removing one refuses such a name too.
func TestSaveRefuses(t *testing.T) {
	public, private := crypttest.Pair(t, t.TempDir(), "id", "")
	keys, err := crypt.Open(public, private, nil)
	if err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	dir := filepath.Join(parent, "accounts")
	outside := filepath.Join(parent, "outside.json")
	if err := os.WriteFile(outside, []byte("kept"), 0o600); err != nil {
		t.Fatal(err)
	}

	a := account.New()
	a.Incoming.Password = "hunter2"
	if err := account.Save(dir, a, nil); err == nil {
		t.Error("an account was saved without keys")
	}
	for _, id := range []string{"", "../outside", "a/b", "a.b"} {
		a.ID = id
		if err := account.Save(dir, a, keys); err == nil {
			t.Errorf("an account was saved as %q", id)
		}
		if err := account.Remove(dir, id); err == nil {
			t.Errorf("an account was removed as %q", id)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("%d files were written", len(entries))
	}
	if data, _ := os.ReadFile(outside); string(data) != "kept" {
		t.Errorf("the file outside = %q", data)
	}
}

// TestListLeavesOutUnreadable checks that an account which cannot be read
// (one encrypted with other keys) is left out and said to be, and that the
// rest are still listed. A missing folder has no accounts and is no error.
func TestListLeavesOutUnreadable(t *testing.T) {
	open := func(name string) *crypt.Keys {
		public, private := crypttest.Pair(t, t.TempDir(), name, "")
		keys, err := crypt.Open(public, private, nil)
		if err != nil {
			t.Fatal(err)
		}
		return keys
	}
	keys, others := open("id"), open("other")
	dir := t.TempDir()

	if list, err := account.List(filepath.Join(dir, "missing"), keys); err != nil || len(list) != 0 {
		t.Errorf("a missing folder: %d accounts, err = %v", len(list), err)
	}

	mine, theirs := account.New(), account.New()
	mine.Email = "mine@example.com"
	if err := account.Save(dir, mine, keys); err != nil {
		t.Fatal(err)
	}
	if err := account.Save(dir, theirs, others); err != nil {
		t.Fatal(err)
	}
	// Neither of these is an account: one is not named as one, and the
	// other is a folder.
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "folder.json"), 0o700); err != nil {
		t.Fatal(err)
	}

	list, err := account.List(dir, keys)
	if len(list) != 1 || list[0].ID != mine.ID {
		t.Errorf("listed %+v", list)
	}
	if err == nil || !strings.Contains(err.Error(), theirs.ID) {
		t.Errorf("err = %v, which does not name the account that cannot be read", err)
	}
}
