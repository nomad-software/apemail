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
