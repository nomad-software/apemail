// Package remotetest runs an IMAP server in memory for tests.
package remotetest

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"

	"github.com/nomad-software/apemail/internal/account"
)

const (
	Username = "user"
	Password = "secret"
)

// Server is an IMAP server in memory, with an account whose user is
// Username, and which has an inbox and the folders it was made with.
type Server struct {
	Addr   string
	Host   string
	Port   int
	server *imapserver.Server
	// options are those the server was started with, with its mailbox,
	// which Restart starts it again with.
	options *imapserver.Options

	mu       sync.Mutex
	commands []string
	hold     *hold
}

// hold is a line of traffic the server stops at, until it is released.
type hold struct {
	text     string
	held     chan struct{}
	released chan struct{}
}

// Caps are the capabilities of a server: IMAP4rev1 and those named.
func Caps(extra ...imap.Cap) imap.CapSet {
	caps := imap.CapSet{imap.CapIMAP4rev1: {}}
	for _, c := range extra {
		caps[c] = struct{}{}
	}
	return caps
}

// Start starts a server with the capabilities and folders, which is stopped
// when the test ends.
func Start(t *testing.T, caps imap.CapSet, folders ...string) *Server {
	t.Helper()
	mem := imapmemserver.New()
	user := imapmemserver.NewUser(Username, Password)
	if err := user.Create("INBOX", nil); err != nil {
		t.Fatal(err)
	}
	for _, f := range folders {
		if err := user.Create(f, nil); err != nil {
			t.Fatal(err)
		}
	}
	mem.AddUser(user)

	s := &Server{}
	s.options = &imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), nil, nil
		},
		Caps:         caps,
		InsecureAuth: true,
		DebugWriter:  (*commandLog)(s),
		// Clients that a test drops on purpose are not worth reporting.
		Logger: log.New(io.Discard, "", 0),
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s.serve(ln)
	t.Cleanup(func() { s.running().Close() })

	s.Addr = ln.Addr().String()
	host, port, _ := net.SplitHostPort(s.Addr)
	s.Host = host
	s.Port, _ = strconv.Atoi(port)
	return s
}

// serve has a server answer clients on the listener.
func (s *Server) serve(ln net.Listener) {
	server := imapserver.New(s.options)
	s.mu.Lock()
	s.server = server
	s.mu.Unlock()
	go server.Serve(ln)
}

// running returns the server that is answering clients.
func (s *Server) running() *imapserver.Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.server
}

// Restart drops every client, as a server that is restarted does, and
// answers again at the same address with the same mailbox.
func (s *Server) Restart(t *testing.T) {
	t.Helper()
	s.running().Close()
	ln, err := net.Listen("tcp", s.Addr)
	if err != nil {
		t.Fatal(err)
	}
	s.serve(ln)
}

// commandLog records the commands clients send, from the traffic of the
// server.
type commandLog Server

func (l *commandLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if h := l.hold; h != nil && bytes.Contains(p, []byte(h.text)) {
		// Only the connection the line is on waits, and not with the
		// lock, so that the others go on.
		l.hold = nil
		close(h.held)
		l.mu.Unlock()
		<-h.released
		l.mu.Lock()
	}
	for _, line := range bytes.Split(p, []byte("\r\n")) {
		// A command is a tag and then its name.
		fields := bytes.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := string(bytes.ToUpper(fields[1]))
		if name == "UID" && len(fields) > 2 {
			name += " " + string(bytes.ToUpper(fields[2]))
		}
		if commandNames[name] {
			l.commands = append(l.commands, name)
		}
	}
	return len(p), nil
}

// commandNames are the commands that are recorded.
var commandNames = map[string]bool{}

func init() {
	for _, name := range []string{"SELECT", "EXAMINE", "STORE", "EXPUNGE", "MOVE", "COPY", "APPEND", "FETCH", "IDLE", "STATUS"} {
		commandNames[name] = true
		commandNames["UID "+name] = true
	}
}

// Hold makes the server stop the next time a client sends it a line with
// the text, before it reads the line, until release is called; held is
// closed when it has stopped. Only that client's connection waits. It lets a
// test do something while a client is part way through what it is doing.
// The server is released when the test ends, if it was not before.
func (s *Server) Hold(t *testing.T, text string) (held <-chan struct{}, release func()) {
	h := &hold{text: text, held: make(chan struct{}), released: make(chan struct{})}
	s.mu.Lock()
	s.hold = h
	s.mu.Unlock()
	var once sync.Once
	release = func() { once.Do(func() { close(h.released) }) }
	t.Cleanup(release)
	return h.held, release
}

// Commands returns the names of the commands clients have sent, as SELECT or
// UID STORE, and then forgets them.
func (s *Server) Commands() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	commands := s.commands
	s.commands = nil
	return commands
}

// Account returns the server as the incoming server of an account.
func (s *Server) Account() account.Server {
	return account.Server{
		Host:     s.Host,
		Port:     s.Port,
		Username: Username,
		Password: Password,
		Security: account.SecurityNone,
		Auth:     account.AuthPassword,
	}
}

// Client returns a client of its own, logged in, for a test to set up and
// check the mailbox with.
func (s *Server) Client(t *testing.T) *imapclient.Client {
	t.Helper()
	c, err := imapclient.DialInsecure(s.Addr, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Login(Username, Password).Wait(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// Append puts a message with the subject in the folder, with the flags.
func (s *Server) Append(t *testing.T, folder, subject string, flags ...imap.Flag) {
	t.Helper()
	s.AppendDated(t, folder, subject, time.Date(2006, 1, 2, 15, 4, 5, 0, time.UTC), flags...)
}

// AppendDated puts a message with the subject, sent at the date, in the
// folder, with the flags.
func (s *Server) AppendDated(t *testing.T, folder, subject string, date time.Time, flags ...imap.Flag) {
	t.Helper()
	raw := fmt.Sprintf("From: Alice <alice@example.com>\r\nTo: user@example.com\r\nSubject: %s\r\nMessage-Id: <%d.%d@example.com>\r\nDate: %s\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nThe body of %s.\r\n",
		subject, len(subject), date.Unix(), date.Format(time.RFC1123Z), subject)
	s.AppendRaw(t, folder, raw, flags...)
}

// AppendRaw puts a message, as it is written, in the folder, with the flags.
func (s *Server) AppendRaw(t *testing.T, folder, raw string, flags ...imap.Flag) {
	t.Helper()
	c := s.Client(t)
	cmd := c.Append(folder, int64(len(raw)), &imap.AppendOptions{Flags: flags})
	cmd.Write([]byte(raw))
	if err := cmd.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	c.Logout().Wait()
}

// Message is what a test sees of a message in a folder.
type Message struct {
	UID     uint32
	Subject string
	Flags   []imap.Flag
	// Raw is the message as it is kept.
	Raw []byte
}

// Messages returns the messages in the folder, looked at read-only.
func (s *Server) Messages(t *testing.T, folder string) []Message {
	t.Helper()
	c := s.Client(t)
	data, err := c.Select(folder, &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		t.Fatal(err)
	}
	if data.NumMessages == 0 {
		return nil
	}
	all := imap.SeqSet{}
	all.AddRange(1, 0)
	whole := &imap.FetchItemBodySection{Peek: true}
	options := &imap.FetchOptions{UID: true, Flags: true, Envelope: true, BodySection: []*imap.FetchItemBodySection{whole}}
	msgs, err := c.Fetch(all, options).Collect()
	if err != nil {
		t.Fatal(err)
	}
	var out []Message
	for _, m := range msgs {
		out = append(out, Message{UID: uint32(m.UID), Subject: m.Envelope.Subject, Flags: m.Flags, Raw: m.FindBodySection(whole)})
	}
	c.Logout().Wait()
	return out
}
