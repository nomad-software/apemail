package remote_test

import (
	"slices"
	"testing"

	"github.com/nomad-software/apemail/internal/remote/condstoretest"
	"github.com/nomad-software/apemail/internal/remote/remotetest"
	"github.com/nomad-software/apemail/internal/store"
)

// attached is a message, by subject, and whether it has files attached.
type attached struct {
	subject, raw string
	has          bool
}

// withAttachments are messages with files attached, and without.
var withAttachments = []attached{
	{"plain", "Subject: plain\r\nContent-Type: text/plain\r\n\r\nHello.\r\n", false},
	{"alternative", "Subject: alternative\r\nContent-Type: multipart/alternative; boundary=b\r\n\r\n" +
		"--b\r\nContent-Type: text/plain\r\n\r\nHello.\r\n" +
		"--b\r\nContent-Type: text/html\r\n\r\n<p>Hello.</p>\r\n--b--\r\n", false},
	{"pdf", "Subject: pdf\r\nContent-Type: multipart/mixed; boundary=b\r\n\r\n" +
		"--b\r\nContent-Type: text/plain\r\n\r\nHello.\r\n" +
		"--b\r\nContent-Type: application/pdf; name=a.pdf\r\n\r\n%PDF\r\n--b--\r\n", true},
	{"text file", "Subject: text file\r\nContent-Type: multipart/mixed; boundary=b\r\n\r\n" +
		"--b\r\nContent-Type: text/plain\r\n\r\nHello.\r\n" +
		"--b\r\nContent-Type: text/plain\r\nContent-Disposition: attachment; filename=notes.txt\r\n\r\nNotes.\r\n--b--\r\n", true},
	// An image the HTML shows is not an attachment.
	{"related", "Subject: related\r\nContent-Type: multipart/related; boundary=b\r\n\r\n" +
		"--b\r\nContent-Type: text/html\r\n\r\n<img src=\"cid:i\">\r\n" +
		"--b\r\nContent-Type: image/png\r\nContent-ID: <i>\r\nContent-Disposition: inline\r\n\r\nPNG\r\n--b--\r\n", false},
}

// TestAttachments checks that a sync says which messages have files
// attached, and that one of an index made before summaries said looks them
// up, once, for the messages it knows.
func TestAttachments(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps())
	for _, m := range withAttachments {
		s.AppendRaw(t, "INBOX", "From: a@example.com\r\n"+m.raw)
	}
	c := dial(t, s, false)
	check := func(when string, has func(store.Summary) bool, summaries []store.Summary) {
		t.Helper()
		if len(summaries) != len(withAttachments) {
			t.Fatalf("%s: %d messages", when, len(summaries))
		}
		for _, sm := range summaries {
			i := slices.IndexFunc(withAttachments, func(m attached) bool { return m.subject == sm.Subject })
			if i < 0 || has(sm) != withAttachments[i].has {
				t.Errorf("%s: %q has attachments: %v", when, sm.Subject, has(sm))
			}
		}
	}

	s.Commands()
	u, err := c.Sync(&store.Index{Folder: "INBOX"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	check("new", func(s store.Summary) bool { return u.Attachments[s.UID] }, u.Added)
	// The structures are asked for apart from the headers.
	if n := count(s.Commands(), "UID FETCH"); n != 3 || u.AttachmentsErr != nil {
		t.Errorf("new: %d fetches, %v", n, u.AttachmentsErr)
	}

	// An index made before summaries said has them looked up.
	old := &store.Index{Folder: "INBOX", UIDValidity: u.UIDValidity}
	for _, m := range u.Added {
		m.Attachments = false
		old.Put(m)
	}
	s.Commands()
	u, err = c.Sync(old, nil)
	if err != nil {
		t.Fatal(err)
	}
	check("old", func(s store.Summary) bool { return u.Attachments[s.UID] }, old.Messages)
	if len(u.Attachments) != len(withAttachments) {
		t.Errorf("looked up %v", u.Attachments)
	}
	commands := s.Commands()
	unchanged(t, commands)
	if n := count(commands, "UID FETCH"); n != 2 {
		t.Errorf("an old index: %d fetches, in %v", n, commands)
	}

	// Once they are known they are not looked up again.
	old.Attachments = true
	u, err = c.Sync(old, nil)
	if err != nil {
		t.Fatal(err)
	}
	if u.Attachments != nil {
		t.Errorf("looked up again: %v", u.Attachments)
	}
	if n := count(s.Commands(), "UID FETCH"); n != 1 {
		t.Errorf("an index up to date: %d fetches", n)
	}
}

// count returns how many times the command was sent.
func count(commands []string, command string) int {
	n := 0
	for _, c := range commands {
		if c == command {
			n++
		}
	}
	return n
}

// TestUnreadableStructure checks that a structure the client cannot read,
// which ends the connection, keeps no new mail from being seen: the
// summaries are fetched before the structures, and stand.
func TestUnreadableStructure(t *testing.T) {
	s := condstoretest.Start(t)
	s.Append("one")
	s.Append("two")
	s.BadStructures(true)
	c := dialCondStore(t, s)
	u, err := c.Sync(&store.Index{Folder: "INBOX"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(u.Added) != 2 || u.AttachmentsErr == nil {
		t.Errorf("added %d, attachments %v, %v", len(u.Added), u.Attachments, u.AttachmentsErr)
	}
}
