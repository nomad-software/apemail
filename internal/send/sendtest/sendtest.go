// Package sendtest is an SMTP server for tests, which records what it is
// sent rather than sending it on.
package sendtest

import (
	"errors"
	"io"
	"log"
	"net"
	"strconv"
	"sync"
	"testing"

	"github.com/emersion/go-sasl"
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
	// username and password are who Login has the server take mail from,
	// and mechs the ways of logging in it offers.
	username, password string
	mechs              []string
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
	// The tests do not use TLS, without which a server does not otherwise
	// let a client log in.
	srv.AllowInsecureAuth = true
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

// Login has the server take mail only from a client that has logged in with
// the user name and password, by one of the mechanisms, which it offers:
// PLAIN and LOGIN are those it has. Until it is called the server offers no
// way of logging in, and takes mail from anyone.
func (s *Server) Login(username, password string, mechs ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.username, s.password, s.mechs = username, password, mechs
}

// session records a message as a client sends it.
type session struct {
	server *Server
	msg    Message
	// loggedIn is whether the client has logged in.
	loggedIn bool
}

// AuthMechanisms returns the ways of logging in the server offers.
func (s *session) AuthMechanisms() []string {
	s.server.mu.Lock()
	defer s.server.mu.Unlock()
	return s.server.mechs
}

// Auth returns what checks a client logging in by the mechanism.
func (s *session) Auth(mech string) (sasl.Server, error) {
	check := func(username, password string) error {
		s.server.mu.Lock()
		defer s.server.mu.Unlock()
		if username != s.server.username || password != s.server.password {
			return errors.New("the user name or password is wrong")
		}
		s.loggedIn = true
		return nil
	}
	switch mech {
	case sasl.Plain:
		return sasl.NewPlainServer(func(_, username, password string) error {
			return check(username, password)
		}), nil
	case sasl.Login:
		return &loginServer{check: check}, nil
	}
	return nil, smtp.ErrAuthUnknownMechanism
}

// loginServer is the server's side of LOGIN, which the library has only the
// client's of: it asks for the user name and then for the password.
type loginServer struct {
	check    func(username, password string) error
	username *string
}

func (l *loginServer) Next(response []byte) (challenge []byte, done bool, err error) {
	switch {
	case response == nil:
		return []byte("Username:"), false, nil
	case l.username == nil:
		username := string(response)
		l.username = &username
		return []byte("Password:"), false, nil
	}
	return nil, true, l.check(*l.username, string(response))
}

func (s *session) Reset()        { s.msg = Message{} }
func (s *session) Logout() error { return nil }

func (s *session) Mail(from string, opts *smtp.MailOptions) error {
	s.server.mu.Lock()
	refused := s.server.username != "" && !s.loggedIn
	s.server.mu.Unlock()
	if refused {
		return smtp.ErrAuthRequired
	}
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
