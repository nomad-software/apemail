package store_test

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nomad-software/apemail/internal/crypt"
	"github.com/nomad-software/apemail/internal/crypt/crypttest"
	"github.com/nomad-software/apemail/internal/jsonfile"
	"github.com/nomad-software/apemail/internal/store"
)

func open(t testing.TB) (*store.Store, string) {
	t.Helper()
	s, dir, _ := openWithKeys(t)
	return s, dir
}

// openWithKeys opens a store, and returns the keys it is encrypted with.
func openWithKeys(t testing.TB) (*store.Store, string, *crypt.Keys) {
	t.Helper()
	public, private := crypttest.Pair(t, t.TempDir(), "id", "")
	keys, err := crypt.Open(public, private, nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	s, err := store.Open(dir, keys)
	if err != nil {
		t.Fatal(err)
	}
	return s, dir, keys
}

// kept says if the message is kept in the local copy.
func kept(st *store.Store, folder string, uid uint32) bool {
	_, err := st.Message(folder, uid)
	return err == nil
}

// plain fails the test if a file under dir holds the text unencrypted.
func plain(t *testing.T, dir, text string) {
	t.Helper()
	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, _ := os.ReadFile(path)
		if bytes.Contains(data, []byte(text)) {
			t.Errorf("%s holds %q", path, text)
		}
		return nil
	})
}

func TestNoKeys(t *testing.T) {
	if _, err := store.Open(t.TempDir(), nil); err == nil {
		t.Error("a store was opened without keys")
	}
}

func TestIndex(t *testing.T) {
	s, dir := open(t)
	ix, err := s.Index("INBOX")
	if err != nil || len(ix.Messages) != 0 {
		t.Fatalf("index = %+v, %v", ix, err)
	}
	ix.UIDValidity = 7
	ix.Put(store.Summary{UID: 5, Subject: "five"})
	ix.Put(store.Summary{UID: 2, Subject: "secret subject"})
	ix.Put(store.Summary{UID: 9, Subject: "nine"})
	ix.Put(store.Summary{UID: 5, Subject: "five again"})
	if len(ix.Messages) != 3 || ix.Messages[0].UID != 2 || ix.Messages[1].Subject != "five again" {
		t.Errorf("messages = %+v", ix.Messages)
	}
	if !ix.Remove(9) || ix.Remove(9) {
		t.Error("remove")
	}
	if err := s.SaveIndex(ix); err != nil {
		t.Fatal(err)
	}
	plain(t, dir, "secret subject")
	plain(t, dir, "INBOX")

	read, err := s.Index("INBOX")
	if err != nil || read.UIDValidity != 7 || len(read.Messages) != 2 || read.Messages[0].Subject != "secret subject" {
		t.Errorf("read = %+v, %v", read, err)
	}
}

func TestMessages(t *testing.T) {
	s, dir := open(t)
	if _, err := s.Message("INBOX", 1); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("err = %v", err)
	}
	raw := []byte("Subject: hello\r\n\r\nsecret body")
	if err := s.SaveMessage("INBOX", 1, raw); err != nil {
		t.Fatal(err)
	}
	plain(t, dir, "secret body")
	if !kept(s, "INBOX", 1) {
		t.Error("the message is not kept")
	}
	if got, err := s.Message("INBOX", 1); err != nil || !bytes.Equal(got, raw) {
		t.Errorf("message = %q, %v", got, err)
	}
	if err := s.RemoveMessage("INBOX", 1); err != nil || kept(s, "INBOX", 1) {
		t.Errorf("remove: %v", err)
	}
	if err := s.RemoveMessage("INBOX", 1); err != nil {
		t.Errorf("removing again: %v", err)
	}
}

func TestFolders(t *testing.T) {
	s, _ := open(t)
	folders := []store.Folder{{Name: "INBOX"}, {Name: "Old", Attrs: []string{`\Noselect`}}}
	if err := s.SaveFolders(folders); err != nil {
		t.Fatal(err)
	}
	s.SaveMessage("Old", 1, []byte("x"))
	s.SaveMessage("INBOX", 1, []byte("x"))

	// A folder that has gone takes its copy with it.
	if err := s.SaveFolders(folders[:1]); err != nil {
		t.Fatal(err)
	}
	if kept(s, "Old", 1) || !kept(s, "INBOX", 1) {
		t.Error("the wrong copies were kept")
	}
	read, err := s.Folders()
	if err != nil || len(read) != 1 || read[0].Name != "INBOX" {
		t.Errorf("folders = %+v, %v", read, err)
	}
	if folders[1].Selectable() || !folders[0].Selectable() || !folders[1].Has(`\NOSELECT`) {
		t.Error("attributes")
	}
}

func TestPending(t *testing.T) {
	s, dir := open(t)
	a, err := s.AddPending(store.Pending{Kind: store.PendingSend, To: []string{"a@example.com"}, Raw: []byte("secret mail")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddPending(store.Pending{Kind: store.PendingAppend, Folder: "Drafts", Raw: []byte("draft")}); err != nil {
		t.Fatal(err)
	}
	plain(t, dir, "a@example.com")
	pending, err := s.Pending()
	if err != nil || len(pending) != 2 || pending[0].ID != a.ID || string(pending[0].Raw) != "secret mail" {
		t.Fatalf("pending = %+v, %v", pending, err)
	}
	if err := s.RemovePending(a.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RemovePending("../settings"); err == nil || !strings.Contains(err.Error(), "names no") {
		t.Errorf("err = %v", err)
	}
	if pending, _ := s.Pending(); len(pending) != 1 {
		t.Errorf("pending = %+v", pending)
	}
}

// indexFile returns the path of the one index under dir.
func indexFile(t *testing.T, dir string) string {
	t.Helper()
	matches, _ := filepath.Glob(filepath.Join(dir, "*", "index.json"))
	if len(matches) != 1 {
		t.Fatalf("index files %v", matches)
	}
	return matches[0]
}

// TestOldIndex checks that an index written before indexes were compressed
// is still read.
func TestOldIndex(t *testing.T) {
	s, dir, keys := openWithKeys(t)
	ix := &store.Index{Folder: "INBOX", UIDValidity: 3, Messages: []store.Summary{{UID: 1, Subject: "kept"}}}
	if err := s.SaveIndex(ix); err != nil {
		t.Fatal(err)
	}
	if err := jsonfile.SaveWith(indexFile(t, dir), ix, keys); err != nil {
		t.Fatal(err)
	}
	read, err := s.Index("INBOX")
	if err != nil || read.UIDValidity != 3 || len(read.Messages) != 1 || read.Messages[0].Subject != "kept" {
		t.Errorf("read = %+v, %v", read, err)
	}
}

// bigIndex returns the index of a folder of n messages, much as a real one
// would be.
func bigIndex(n int) *store.Index {
	ix := &store.Index{Folder: "INBOX", UIDValidity: 1, HighestModSeq: 123456}
	d := time.Date(2016, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 1; i <= n; i++ {
		ix.Messages = append(ix.Messages, store.Summary{
			UID:       uint32(i),
			MessageID: fmt.Sprintf("%d.%x@mail.example.com", i, i*7919),
			Subject:   fmt.Sprintf("Re: About the quarterly report number %d", i%977),
			From:      fmt.Sprintf(`"Someone %d" <someone%d@example.com>`, i%331, i%331),
			To:        `"Ada Lovelace" <ada@example.com>`,
			Date:      d.Add(time.Duration(i) * time.Hour),
			Flags:     []string{`\Seen`},
			Size:      int64(1000 + i%50000),
		})
	}
	return ix
}

// TestLargeIndex checks that the index of a folder of 100,000 messages is
// kept in a few megabytes, and read back whole.
func TestLargeIndex(t *testing.T) {
	s, dir := open(t)
	ix := bigIndex(100000)
	if err := s.SaveIndex(ix); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(indexFile(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	// Uncompressed it was 44 MB.
	if info.Size() > 8<<20 {
		t.Errorf("the index is %d bytes", info.Size())
	}
	read, err := s.Index("INBOX")
	if err != nil || len(read.Messages) != 100000 || read.HighestModSeq != 123456 || !reflect.DeepEqual(read.Messages[99999], ix.Messages[99999]) {
		t.Errorf("read %d messages, %v", len(read.Messages), err)
	}
}

func BenchmarkSaveIndex100k(b *testing.B) {
	s, _ := open(b)
	ix := bigIndex(100000)
	for b.Loop() {
		if err := s.SaveIndex(ix); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLoadIndex100k(b *testing.B) {
	s, _ := open(b)
	if err := s.SaveIndex(bigIndex(100000)); err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		if _, err := s.Index("INBOX"); err != nil {
			b.Fatal(err)
		}
	}
}
