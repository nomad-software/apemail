// Package sendtest is an SMTP server for tests, which records what it is
// sent rather than sending it on.
package sendtest

import (
	"io"
	"log"
	"net"
	"strconv"
	"sync"
	"testing"

	"github.com/emersion/go-smtp"

	"github.com/nomad-software/apemail/internal/account"
)

// Message is a message the server was given.
type Message struct {
	From string
	// Return is the RET= of a request for delivery status notifications.
	Return smtp.DSNReturn
	To     []string
	// Notify is the NOTIFY= of each recipient, nil where there was none.
	Notify [][]smtp.DSNNotify
	Data   []byte
}

// Server is an SMTP server running for a test.
type Server struct {
	// Account is the outgoing server of an account that sends through it.
	Account account.Server

	mu       sync.Mutex
	messages []Message
}

// Start starts a server, which supports DSN (RFC 3461) if dsn is true, until
// the test ends.
func Start(t *testing.T, dsn bool) *Server {
	t.Helper()
	s := &Server{}
	srv := smtp.NewServer(smtp.BackendFunc(func(*smtp.Conn) (smtp.Session, error) {
		return &session{server: s}, nil
	}))
	srv.Domain = "localhost"
	srv.EnableDSN = dsn
	// A client hanging up is not worth reporting.
	srv.ErrorLog = log.New(io.Discard, "", 0)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	host, port, _ := net.SplitHostPort(l.Addr().String())
	n, _ := strconv.Atoi(port)
	s.Account = account.Server{Host: host, Port: n, Security: account.SecurityNone, Auth: account.AuthPassword}
	return s
}

// Messages returns the messages the server was given.
func (s *Server) Messages() []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Message(nil), s.messages...)
}

// session records a message as a client sends it.
type session struct {
	server *Server
	msg    Message
}

func (s *session) Reset()        { s.msg = Message{} }
func (s *session) Logout() error { return nil }

func (s *session) Mail(from string, opts *smtp.MailOptions) error {
	s.msg.From = from
	if opts != nil {
		s.msg.Return = opts.Return
	}
	return nil
}

func (s *session) Rcpt(to string, opts *smtp.RcptOptions) error {
	s.msg.To = append(s.msg.To, to)
	var notify []smtp.DSNNotify
	if opts != nil {
		notify = opts.Notify
	}
	s.msg.Notify = append(s.msg.Notify, notify)
	return nil
}

func (s *session) Data(r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.msg.Data = data
	s.server.mu.Lock()
	defer s.server.mu.Unlock()
	s.server.messages = append(s.server.messages, s.msg)
	return nil
}
