package remote_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/nomad-software/apemail/internal/account"
	"github.com/nomad-software/apemail/internal/remote"
	"github.com/nomad-software/apemail/internal/remote/remotetest"
)

// wait is how long a test waits for the server or a connection.
const wait = 10 * time.Second

// TestAppend checks that a message is put in a folder as it is written, with
// its flags, that the server's UID for it is returned, and that the messages
// there already are left alone.
func TestAppend(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus), "Drafts")
	s.Append(t, "Drafts", "first")
	before := s.Messages(t, "Drafts")
	c := dial(t, s, true)

	raw := "Subject: second\r\n\r\nThe body.\r\n"
	uid, uidValidity, err := c.Append("Drafts", []byte(raw), []string{`\Seen`, `\Draft`})
	if err != nil {
		t.Fatal(err)
	}
	msgs := s.Messages(t, "Drafts")
	if len(msgs) != 2 || msgs[0].UID != before[0].UID || string(msgs[0].Raw) != string(before[0].Raw) || len(msgs[0].Flags) != 0 {
		t.Fatalf("drafts = %+v", msgs)
	}
	m := msgs[1]
	if string(m.Raw) != raw || !slices.Contains(m.Flags, imap.FlagSeen) || !slices.Contains(m.Flags, imap.FlagDraft) {
		t.Errorf("appended %q with %v", m.Raw, m.Flags)
	}
	if uid != m.UID || uidValidity == 0 {
		t.Errorf("uid = %d (validity %d), and the message has %d", uid, uidValidity, m.UID)
	}

	// A folder that is not there is not made by the way: the message is
	// refused.
	if _, _, err := c.Append("Missing", []byte(raw), nil); err == nil {
		t.Error("a message was put in a folder that is not there")
	}
	if folders, err := c.Folders(); err != nil || len(folders) != 2 {
		t.Errorf("folders = %+v, err = %v", folders, err)
	}

	// The server for tests keeps its folders at the top, and says so or
	// not at all.
	if prefix := c.PersonalPrefix(); prefix != "" {
		t.Errorf("prefix = %q", prefix)
	}
}

// TestCheck checks that a connection the server still answers is found to be
// working, by a command which changes nothing, and that one the server has
// dropped is found to have gone.
func TestCheck(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps())
	s.Append(t, "INBOX", "first")
	c := dial(t, s, true)
	s.Commands()
	if err := c.Check(); err != nil {
		t.Fatal(err)
	}
	unchanged(t, s.Commands())
	select {
	case <-c.Closed():
		t.Fatal("the connection is said to be closed")
	default:
	}

	s.Restart(t)
	select {
	case <-c.Closed():
	case <-time.After(wait):
		t.Fatal("waiting for the connection to be found closed")
	}
	if err := c.Check(); err == nil {
		t.Error("a connection the server dropped was found to be working")
	}
	if msgs := s.Messages(t, "INBOX"); len(msgs) != 1 {
		t.Errorf("inbox = %+v", msgs)
	}
}

// TestLoginDisabled checks that a server which forbids the LOGIN command
// (LOGINDISABLED) is logged in to if it offers another way that takes a
// password (PLAIN, which the server of the tests always offers), rather than
// refused.
func TestLoginDisabled(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapLoginDisabled, imap.Cap("AUTH=PLAIN")))
	c, err := remote.Dial(s.Account(), remote.Options{})
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
}

// recv returns what arrives on the channel, failing the test if nothing does
// in time.
func recv[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(wait):
		t.Fatal("waiting for the server")
		panic("unreachable")
	}
}

// TestNoAnswer checks that a server which stops answering, as one whose
// network has gone without saying so, is not waited on for ever: Check finds
// the connection gone, and Close closes it without the logout answered.
func TestNoAnswer(t *testing.T) {
	t.Cleanup(remote.SetWaits(100 * time.Millisecond))
	s := remotetest.Start(t, remotetest.Caps())

	c := dial(t, s, true)
	held, _ := s.Hold(t, "NOOP")
	start := time.Now()
	if err := c.Check(); err == nil {
		t.Error("a server that did not answer was found to be working")
	}
	recv(t, held)
	if time.Since(start) > wait {
		t.Errorf("Check waited %v", time.Since(start))
	}
	select {
	case <-c.Closed():
	case <-time.After(wait):
		t.Error("the connection was left open")
	}

	c = dial(t, s, true)
	held, _ = s.Hold(t, "LOGOUT")
	closed := make(chan struct{})
	go func() {
		c.Close()
		close(closed)
	}()
	recv(t, held)
	select {
	case <-closed:
	case <-time.After(wait):
		t.Fatal("Close waited for a logout the server did not answer")
	}
}

// TestAbort checks that a connection is closed at once, with what waits on
// it let go, and that nothing was changed in doing so.
func TestAbort(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps())
	c := dial(t, s, true)
	s.Commands()
	stop := make(chan struct{})
	watched := make(chan error, 1)
	go func() { watched <- c.Watch("INBOX", stop) }()

	c.Abort()
	select {
	case <-watched:
	case <-time.After(wait):
		t.Fatal("waiting for the watch of a closed connection to end")
	}
	select {
	case <-c.Closed():
	case <-time.After(wait):
		t.Fatal("waiting for the connection to be closed")
	}
	unchanged(t, s.Commands())
}

// TestWatch checks that a connection waiting in the inbox is told of mail as
// it arrives, without the folder being opened to be written to, and that it
// stops when asked.
func TestWatch(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps())
	s.Append(t, "INBOX", "first")
	counts := make(chan uint32, 8)
	c, err := remote.Dial(s.Account(), remote.Options{Unilateral: &imapclient.UnilateralDataHandler{
		Mailbox: func(data *imapclient.UnilateralDataMailbox) {
			if data.NumMessages != nil {
				counts <- *data.NumMessages
			}
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if !c.CanIdle() {
		t.Fatal("the server is said not to tell of new mail")
	}
	s.Commands()

	stop := make(chan struct{})
	watched := make(chan error, 1)
	// The watch has started once the server has been asked to idle.
	idling, release := s.Hold(t, "IDLE")
	go func() { watched <- c.Watch("INBOX", stop) }()
	select {
	case <-idling:
	case <-time.After(wait):
		t.Fatal("waiting for the watch to start")
	}
	release()

	s.Append(t, "INBOX", "second")
	for arrived := false; !arrived; {
		select {
		case n := <-counts:
			arrived = n == 2
		case err := <-watched:
			t.Fatalf("the watch ended: %v", err)
		case <-time.After(wait):
			t.Fatal("waiting to be told of new mail")
		}
	}

	close(stop)
	select {
	case err := <-watched:
		if err != nil {
			t.Errorf("the watch ended with %v", err)
		}
	case <-time.After(wait):
		t.Fatal("waiting for the watch to stop")
	}
	// The one APPEND is the test's own, of the mail that arrived.
	commands := s.Commands()
	if i := slices.Index(commands, "APPEND"); i >= 0 {
		commands = slices.Delete(commands, i, i+1)
	}
	unchanged(t, commands)
	if !slices.Contains(commands, "EXAMINE") {
		t.Errorf("the inbox was not opened read-only: %v", commands)
	}
	if msgs := s.Messages(t, "INBOX"); len(msgs) != 2 || len(msgs[0].Flags) != 0 || len(msgs[1].Flags) != 0 {
		t.Errorf("inbox = %+v", msgs)
	}

	// A folder that is not there is not waited in.
	if err := c.Watch("Missing", stop); err == nil {
		t.Error("a folder that is not there was watched")
	}
}

// TestDialRefuses checks that no connection is made for an account that
// cannot log in as it says to, and that the wrong password is said to be.
func TestDialRefuses(t *testing.T) {
	tests := map[string]func(*account.Server){
		"the wrong password":    func(a *account.Server) { a.Password = "wrong" },
		"no client certificate": func(a *account.Server) { a.Auth = account.AuthTLSCert },
		"a client certificate that is not there": func(a *account.Server) {
			a.Auth, a.ClientCert, a.ClientKey = account.AuthTLSCert, "/nowhere/cert.pem", "/nowhere/key.pem"
		},
		"an unknown kind of authentication": func(a *account.Server) { a.Auth = "kerberos" },
		"an unknown kind of security":       func(a *account.Server) { a.Security = "other" },
		"a server that is not there":        func(a *account.Server) { a.Port = 1 },
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			s := remotetest.Start(t, remotetest.Caps())
			server := s.Account()
			change(&server)
			c, err := remote.Dial(server, remote.Options{Writable: true})
			if err == nil {
				c.Close()
				t.Fatal("a connection was made")
			}
			if strings.TrimSpace(err.Error()) == "" {
				t.Error("the error says nothing")
			}
		})
	}
}
