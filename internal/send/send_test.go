package send_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/emersion/go-smtp"

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
