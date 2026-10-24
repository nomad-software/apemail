package engine

import (
	"testing"

	"github.com/nomad-software/apemail/internal/account"
	"github.com/nomad-software/apemail/internal/crypt"
	"github.com/nomad-software/apemail/internal/crypt/crypttest"
	"github.com/nomad-software/apemail/internal/store"
)

// quiet is a listener that is told nothing of interest.
type quiet struct{}

func (quiet) FoldersChanged(string, []store.Folder) {}
func (quiet) FolderChanged(string, *store.Index)    {}
func (quiet) Status(string, string)                 {}
func (quiet) Failed(string, error)                  {}
func (quiet) UnreadCounts(string, map[string]int)   {}

// newQuiet returns an engine with an empty local copy that asks nothing of
// any server, and the copy.
func newQuiet(t *testing.T) (*Engine, *store.Store) {
	t.Helper()
	public, private := crypttest.Pair(t, t.TempDir(), "id", "")
	keys, err := crypt.Open(public, private, nil)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(t.TempDir(), keys)
	if err != nil {
		t.Fatal(err)
	}
	acct := account.New()
	acct.CheckOnStart = false
	e := New(acct, st, quiet{}, func(f func()) { f() })
	t.Cleanup(e.Close)
	return e, st
}

// TestKeepMessage checks that a message downloaded is kept only if its
// folder is still numbered as it was when it was downloaded: once the
// server has numbered it anew, or it has gone, the UID may be another
// message's, which would be shown in its place.
func TestKeepMessage(t *testing.T) {
	e, st := newQuiet(t)
	e.mu.Lock()
	e.indexes["INBOX"] = &store.Index{Folder: "INBOX", UIDValidity: 7}
	e.mu.Unlock()

	e.keepMessage("INBOX", 7, 1, []byte("as it was"))
	if raw, err := st.Message("INBOX", 1); err != nil || string(raw) != "as it was" {
		t.Errorf("kept %q, %v", raw, err)
	}
	e.keepMessage("INBOX", 6, 2, []byte("numbered before"))
	if _, err := st.Message("INBOX", 2); err == nil {
		t.Error("a message of the folder as it was numbered before was kept")
	}
	e.keepMessage("Gone", 7, 3, []byte("of a folder gone"))
	if _, err := st.Message("Gone", 3); err == nil {
		t.Error("a message of a folder that has gone was kept")
	}
}

// TestStartKeepsNewerFolders checks that the folders kept from before are
// not shown over those the server has listed meanwhile.
func TestStartKeepsNewerFolders(t *testing.T) {
	e, st := newQuiet(t)
	if err := st.SaveFolders([]store.Folder{{Name: "INBOX"}, {Name: "Old"}}); err != nil {
		t.Fatal(err)
	}
	listed := []store.Folder{{Name: "INBOX"}, {Name: "New"}}
	e.mu.Lock()
	e.folders = listed
	e.mu.Unlock()
	e.Start()
	// Closing waits for what Start began.
	e.Close()
	if got := e.Folders(); len(got) != 2 || got[1].Name != "New" {
		t.Errorf("folders = %+v", got)
	}

	// With none listed, those kept are shown.
	e, st = newQuiet(t)
	kept := []store.Folder{{Name: "INBOX", Delim: "/"}}
	if err := st.SaveFolders(kept); err != nil {
		t.Fatal(err)
	}
	e.Start()
	e.Close()
	if got := e.Folders(); len(got) != 1 || got[0].Name != "INBOX" {
		t.Errorf("folders = %+v", got)
	}
}

// TestSpawnAfterClose checks that nothing is started once the engine is
// closed, so that Close has waited for everything there is.
func TestSpawnAfterClose(t *testing.T) {
	e, _ := newQuiet(t)
	ran := make(chan struct{}, 2)
	if !e.spawn(func() { ran <- struct{}{} }) {
		t.Error("nothing was started before closing")
	}
	e.Close()
	if len(ran) != 1 {
		t.Error("Close returned before what was started had run")
	}
	if e.spawn(func() { ran <- struct{}{} }) {
		t.Error("something was started after closing")
	}
	if len(ran) != 1 {
		t.Error("it ran")
	}
}
