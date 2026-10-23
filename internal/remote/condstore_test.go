package remote_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/nomad-software/apemail/internal/remote"
	"github.com/nomad-software/apemail/internal/remote/condstoretest"
	"github.com/nomad-software/apemail/internal/store"
)

// dialCondStore connects to the CONDSTORE server, and closes the connection
// when the test ends.
func dialCondStore(t *testing.T, s *condstoretest.Server) *remote.Conn {
	t.Helper()
	c, err := remote.Dial(s.Account(), remote.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// apply brings the index up to date with what a sync found, as the engine
// does.
func apply(ix *store.Index, u *remote.Update) {
	if u.Reset {
		ix.Messages = nil
	}
	ix.UIDValidity, ix.HighestModSeq = u.UIDValidity, u.HighestModSeq
	for _, uid := range u.Removed {
		ix.Remove(uid)
	}
	for uid, flags := range u.Flags {
		if i, ok := ix.Find(uid); ok {
			ix.Messages[i].Flags = flags
		}
	}
	for _, s := range u.Added {
		ix.Put(s)
	}
}

// sync synchronises the index, and returns what was found and the commands
// sent to find it.
func sync(t *testing.T, c *remote.Conn, s *condstoretest.Server, ix *store.Index) (*remote.Update, []string) {
	t.Helper()
	s.Commands()
	u, err := c.Sync(ix, nil)
	if err != nil {
		t.Fatal(err)
	}
	apply(ix, u)
	return u, s.Commands()
}

// sent says if a command beginning with the text was sent.
func sent(commands []string, text string) bool {
	return slices.ContainsFunc(commands, func(c string) bool { return strings.HasPrefix(c, text) })
}

// fullFetch says if the flags of every message were asked for.
func fullFetch(commands []string) bool {
	return slices.ContainsFunc(commands, func(c string) bool {
		return strings.HasPrefix(c, "UID FETCH 1:*") && !strings.Contains(c, "CHANGEDSINCE")
	})
}

// uids returns the UIDs of the messages of the index.
func uids(ix *store.Index) []uint32 {
	var list []uint32
	for _, m := range ix.Messages {
		list = append(list, m.UID)
	}
	return list
}

// TestCondStore checks that a server with CONDSTORE is asked only for what
// has changed since the last sync, and nothing at all if nothing has.
func TestCondStore(t *testing.T) {
	s := condstoretest.Start(t)
	one := s.Append("one")
	two := s.Append("two")
	three := s.Append("three", `\Seen`)
	c := dialCondStore(t, s)

	// The first sync asks for everything, and keeps the count of changes.
	ix := &store.Index{Folder: "INBOX"}
	u, commands := sync(t, c, s, ix)
	if !u.Reset || len(u.Added) != 3 || u.HighestModSeq == 0 || !fullFetch(commands) {
		t.Fatalf("first sync: %+v, %v", u, commands)
	}
	if !sent(commands, "EXAMINE INBOX (CONDSTORE)") {
		t.Errorf("the count of changes was not asked for: %v", commands)
	}

	// Nothing has changed: nothing is fetched.
	u, commands = sync(t, c, s, ix)
	if len(u.Added)+len(u.Removed)+len(u.Flags) != 0 || sent(commands, "FETCH") || sent(commands, "UID FETCH") || sent(commands, "UID SEARCH") {
		t.Errorf("unchanged: %+v, %v", u, commands)
	}

	// A flag set elsewhere is all that is fetched.
	s.SetFlags(two, `\Seen`, `\Flagged`)
	u, commands = sync(t, c, s, ix)
	if len(u.Flags) != 1 || !slices.Equal(u.Flags[two], []string{`\Seen`, `\Flagged`}) || len(u.Added)+len(u.Removed) != 0 {
		t.Errorf("flag changed: %+v", u)
	}
	if fullFetch(commands) || !sent(commands, "UID FETCH 1:* (UID FLAGS) (CHANGEDSINCE") || sent(commands, "UID SEARCH") {
		t.Errorf("flag changed: %v", commands)
	}

	// New mail is found among the changes, and only it is fetched whole.
	four := s.Append("four")
	u, commands = sync(t, c, s, ix)
	if len(u.Added) != 1 || u.Added[0].UID != four || u.Added[0].Subject != "four" || len(u.Removed) != 0 || fullFetch(commands) {
		t.Errorf("new mail: %+v, %v", u, commands)
	}

	// A message removed elsewhere, which need not count as a change, is
	// found from the UIDs that are left.
	s.Expunge(one)
	u, commands = sync(t, c, s, ix)
	if !slices.Equal(u.Removed, []uint32{one}) || fullFetch(commands) || !sent(commands, "UID SEARCH") {
		t.Errorf("expunged: %+v, %v", u, commands)
	}

	// Removed and new at once.
	s.Expunge(three)
	five := s.Append("five")
	u, _ = sync(t, c, s, ix)
	if !slices.Equal(u.Removed, []uint32{three}) || len(u.Added) != 1 || u.Added[0].UID != five {
		t.Errorf("expunged and new: %+v", u)
	}
	if got := uids(ix); !slices.Equal(got, []uint32{two, four, five}) {
		t.Errorf("index = %v", got)
	}
	if i, _ := ix.Find(two); !slices.Equal(ix.Messages[i].Flags, []string{`\Seen`, `\Flagged`}) {
		t.Errorf("flags of two = %v", ix.Messages[i].Flags)
	}
}

// TestCondStoreRenumbered checks that a folder numbered anew is synchronised
// whole, whatever the count of changes says.
func TestCondStoreRenumbered(t *testing.T) {
	s := condstoretest.Start(t)
	s.Append("one")
	c := dialCondStore(t, s)
	ix := &store.Index{Folder: "INBOX"}
	sync(t, c, s, ix)

	s.Renumber()
	u, commands := sync(t, c, s, ix)
	if !u.Reset || len(u.Added) != 1 || !fullFetch(commands) {
		t.Errorf("renumbered: %+v, %v", u, commands)
	}
}

// TestNoModSeq checks that a folder whose changes the server does not count
// is synchronised as on a server without CONDSTORE.
func TestNoModSeq(t *testing.T) {
	s := condstoretest.Start(t)
	one := s.Append("one")
	c := dialCondStore(t, s)
	ix := &store.Index{Folder: "INBOX"}
	sync(t, c, s, ix)

	s.NoModSeq(true)
	s.SetFlags(one, `\Seen`)
	u, commands := sync(t, c, s, ix)
	if u.HighestModSeq != 0 || !slices.Equal(u.Flags[one], []string{`\Seen`}) || !fullFetch(commands) {
		t.Errorf("no modseq: %+v, %v", u, commands)
	}
	// Once it counts them again, a sync starts again from everything.
	s.NoModSeq(false)
	u, commands = sync(t, c, s, ix)
	if u.HighestModSeq == 0 || !fullFetch(commands) {
		t.Errorf("counted again: %+v, %v", u, commands)
	}
}

// TestModSeqBackwards checks that a count of changes lower than the one the
// index was made at, which cannot say what has changed since, has the folder
// synchronised whole, so that nothing is missed.
func TestModSeqBackwards(t *testing.T) {
	s := condstoretest.Start(t)
	one := s.Append("one")
	s.Append("two")
	s.Append("three")
	c := dialCondStore(t, s)
	ix := &store.Index{Folder: "INBOX"}
	sync(t, c, s, ix)

	s.ForgetChanges()
	s.SetFlags(one, `\Seen`)
	u, commands := sync(t, c, s, ix)
	if !slices.Equal(u.Flags[one], []string{`\Seen`}) || !fullFetch(commands) {
		t.Errorf("backwards: %+v, %v", u, commands)
	}
}
