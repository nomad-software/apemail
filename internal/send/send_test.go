package send_test

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-smtp"

	"github.com/nomad-software/apemail/internal/account"
	"github.com/nomad-software/apemail/internal/send"
	"github.com/nomad-software/apemail/internal/send/sendtest"
)

const raw = "Subject: Hello\r\n\r\nHello\r\n"

// sent returns the one message the server was given.
func sent(t *testing.T, s *sendtest.Server) sendtest.Message {
	t.Helper()
	msgs := s.Messages()
	if len(msgs) != 1 {
		t.Fatalf("the server was given %d messages", len(msgs))
	}
	return msgs[0]
}

func TestSend(t *testing.T) {
	s := sendtest.Start(t, true)
	if err := send.Send(s.Account, "me@example.com", []string{"bob@example.com", "carol@example.com"}, []byte(raw), false); err != nil {
		t.Fatal(err)
	}
	m := sent(t, s)
	if m.From != "me@example.com" || !slices.Equal(m.To, []string{"bob@example.com", "carol@example.com"}) || string(m.Data) != raw {
		t.Errorf("sent %+v", m)
	}
	// Nothing is asked for that was not.
	if m.Return != "" || m.Notify[0] != nil || m.Notify[1] != nil {
		t.Errorf("return = %q, notify = %v", m.Return, m.Notify)
	}
}

func TestSendDSN(t *testing.T) {
	s := sendtest.Start(t, true)
	if err := send.Send(s.Account, "me@example.com", []string{"bob@example.com", "carol@example.com"}, []byte(raw), true); err != nil {
		t.Fatal(err)
	}
	m := sent(t, s)
	want := []smtp.DSNNotify{smtp.DSNNotifySuccess, smtp.DSNNotifyFailure}
	if m.Return != smtp.DSNReturnHeaders || len(m.Notify) != 2 || !slices.Equal(m.Notify[0], want) || !slices.Equal(m.Notify[1], want) {
		t.Errorf("return = %q, notify = %v", m.Return, m.Notify)
	}
}

// TestSendDSNUnsupported checks that a server without DSN is sent the
// message all the same, without being asked for notifications.
func TestSendDSNUnsupported(t *testing.T) {
	s := sendtest.Start(t, false)
	if err := send.Send(s.Account, "me@example.com", []string{"bob@example.com"}, []byte(raw), true); err != nil {
		t.Fatal(err)
	}
	if m := sent(t, s); string(m.Data) != raw || m.Return != "" || m.Notify[0] != nil {
		t.Errorf("sent %+v", m)
	}
}

func TestSendNoRecipients(t *testing.T) {
	s := sendtest.Start(t, true)
	if err := send.Send(s.Account, "me@example.com", nil, []byte(raw), false); err == nil {
		t.Error("mail without recipients was sent")
	}
	if msgs := s.Messages(); len(msgs) != 0 {
		t.Errorf("the server was given %+v", msgs)
	}
}

// TestUnreachable checks that a server that cannot be reached says so, so
// that the mail is kept to be sent later.
func TestUnreachable(t *testing.T) {
	s := sendtest.Start(t, true)
	server := s.Account
	server.Port = 1
	if err := send.Send(server, "me@example.com", []string{"bob@example.com"}, []byte(raw), false); !errors.Is(err, send.ErrUnreachable) {
		t.Errorf("err = %v", err)
	}
}

// TestSentThoughGoodbyeFails checks that mail the server has taken is said
// to be sent, though the server then goes without answering the goodbye:
// said to have failed, it would be sent again.
func TestSentThoughGoodbyeFails(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	taken := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(10 * time.Second))
		r := bufio.NewReader(conn)
		say := func(line string) { conn.Write([]byte(line + "\r\n")) }
		say("220 test")
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			switch cmd := strings.ToUpper(strings.Fields(line + " -")[0]); cmd {
			case "DATA":
				say("354 go on")
				var data strings.Builder
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					if line == ".\r\n" {
						break
					}
					data.WriteString(line)
				}
				say("250 taken")
				taken <- data.String()
			case "QUIT":
				// The server goes without a word.
				return
			default:
				say("250 ok")
			}
		}
	}()

	host, port, _ := strings.Cut(ln.Addr().String(), ":")
	server := account.Server{Host: host, Security: account.SecurityNone, Auth: account.AuthPassword}
	if _, err := fmt.Sscan(port, &server.Port); err != nil {
		t.Fatal(err)
	}
	if err := send.Send(server, "me@example.com", []string{"bob@example.com"}, []byte(raw), false); err != nil {
		t.Errorf("mail the server took was said to fail: %v", err)
	}
	select {
	case data := <-taken:
		if data != raw {
			t.Errorf("the server was given %q", data)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("waiting for the server to be given the message")
	}
}

// TestLogin checks that mail is sent logged in with the account's password,
// by PLAIN, or by LOGIN where that is all the server has, and that with the
// wrong password nothing is sent, nor said to be kept for later, as trying
// again would fare no better.
func TestLogin(t *testing.T) {
	for _, mech := range []string{"PLAIN", "LOGIN"} {
		t.Run(mech, func(t *testing.T) {
			s := sendtest.Start(t, true)
			s.Login("me", "hunter2", mech)
			server := s.Account
			server.Username, server.Password = "me", "wrong"
			err := send.Send(server, "me@example.com", []string{"bob@example.com"}, []byte(raw), false)
			if err == nil || errors.Is(err, send.ErrUnreachable) {
				t.Errorf("with the wrong password: err = %v", err)
			}
			if err := send.Test(server); err == nil {
				t.Error("the connection was said to work with the wrong password")
			}
			if msgs := s.Messages(); len(msgs) != 0 {
				t.Fatalf("the server was given %+v", msgs)
			}

			server.Password = "hunter2"
			if err := send.Test(server); err != nil {
				t.Errorf("testing the connection: %v", err)
			}
			if msgs := s.Messages(); len(msgs) != 0 {
				t.Fatalf("testing the connection sent %+v", msgs)
			}
			if err := send.Send(server, "me@example.com", []string{"bob@example.com"}, []byte(raw), false); err != nil {
				t.Fatal(err)
			}
			if m := sent(t, s); m.From != "me@example.com" || string(m.Data) != raw {
				t.Errorf("sent %+v", m)
			}
		})
	}
}

// TestNotLoggedIn checks that an account with no user name does not log in,
// as to a relay which trusts where it is reached from, and that a server
// which wants a login then takes nothing.
func TestNotLoggedIn(t *testing.T) {
	s := sendtest.Start(t, true)
	if err := send.Test(s.Account); err != nil {
		t.Errorf("testing the connection: %v", err)
	}
	s.Login("me", "hunter2", "PLAIN")
	err := send.Send(s.Account, "me@example.com", []string{"bob@example.com"}, []byte(raw), false)
	if err == nil || errors.Is(err, send.ErrUnreachable) {
		t.Errorf("err = %v", err)
	}
	if msgs := s.Messages(); len(msgs) != 0 {
		t.Errorf("the server was given %+v", msgs)
	}
}

// TestCannotLogIn checks that mail is not sent where the account cannot log
// in as it says to: the server has no such way, or the account is not filled
// in. None of these is the server being out of reach, so the mail is not
// kept to be tried again.
func TestCannotLogIn(t *testing.T) {
	tests := map[string]func(*account.Server){
		"no way of logging in with a password": func(a *account.Server) { a.Username, a.Password = "me", "hunter2" },
		"no encrypted passwords": func(a *account.Server) {
			a.Auth, a.Username, a.Password = account.AuthEncryptedPassword, "me", "hunter2"
		},
		"no client certificate": func(a *account.Server) { a.Auth = account.AuthTLSCert },
		"a client certificate that is not there": func(a *account.Server) {
			a.Auth, a.ClientCert, a.ClientKey = account.AuthTLSCert, "/nowhere/cert.pem", "/nowhere/key.pem"
		},
		"an unknown kind of authentication": func(a *account.Server) { a.Auth = "kerberos" },
		"an unknown kind of security":       func(a *account.Server) { a.Security = "other" },
		"no server":                         func(a *account.Server) { a.Host = "" },
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			s := sendtest.Start(t, true)
			server := s.Account
			change(&server)
			err := send.Send(server, "me@example.com", []string{"bob@example.com"}, []byte(raw), false)
			if err == nil || errors.Is(err, send.ErrUnreachable) {
				t.Errorf("sending: err = %v", err)
			}
			if err := send.Test(server); err == nil {
				t.Error("the connection was said to work")
			}
			if msgs := s.Messages(); len(msgs) != 0 {
				t.Errorf("the server was given %+v", msgs)
			}
		})
	}
}

// TestAddressesNotASCII checks that mail to an address that is not ASCII is
// not sent through a server that cannot take one (no SMTPUTF8), rather than
// the address being sent as it is.
func TestAddressesNotASCII(t *testing.T) {
	s := sendtest.Start(t, true)
	if err := send.Send(s.Account, "me@example.com", []string{"jürgen@example.com"}, []byte(raw), false); err == nil {
		t.Error("mail to an address that is not ASCII was sent")
	}
	if msgs := s.Messages(); len(msgs) != 0 {
		t.Errorf("the server was given %+v", msgs)
	}
}
