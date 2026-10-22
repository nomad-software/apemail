package account_test

import (
	"bytes"
	"os"
	"path/filepath"
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
