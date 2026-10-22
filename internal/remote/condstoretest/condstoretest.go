// Package condstoretest runs an IMAP server for tests that has CONDSTORE
// (RFC 7162), which go-imap's in-memory server (remotetest) has not: it
// counts the changes to each message, answers EXAMINE and SELECT with the
// count for the folder (HIGHESTMODSEQ), and FETCH ... (CHANGEDSINCE n) with
// just the messages changed since. It has one folder, the inbox, and speaks
// just enough IMAP for the client to log in, list it, synchronise it, fetch
// messages and set flags. Tests change what it holds as another client
// would, and see the commands that were sent.
package condstoretest

import (
	"bufio"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nomad-software/apemail/internal/account"
)

const (
	Username = "user"
	Password = "secret"
)

// message is a message in the inbox.
type message struct {
	uid     uint32
	subject string
	flags   []string
	// modSeq is the count of changes to the folder when it last changed.
	modSeq uint64
}

// Server is an IMAP server running for a test.
type Server struct {
	host string
	port int

	mu          sync.Mutex
	uidValidity uint32
	uidNext     uint32
	modSeq      uint64
	noModSeq    bool
	messages    []*message
	commands    []string
}

// Start starts a server with an empty inbox, which is stopped when the test
// ends.
func Start(t *testing.T) *Server {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{uidValidity: 1, uidNext: 1, modSeq: 1}
	addr := l.Addr().(*net.TCPAddr)
	s.host, s.port = addr.IP.String(), addr.Port

	var wg sync.WaitGroup
	var conns sync.Map
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			conns.Store(conn, true)
			wg.Go(func() {
				defer conns.Delete(conn)
				s.serve(conn)
			})
		}
	}()
	t.Cleanup(func() {
		l.Close()
		conns.Range(func(c, _ any) bool {
			c.(net.Conn).Close()
			return true
		})
		wg.Wait()
	})
	return s
}

// Account returns the server as the incoming server of an account.
func (s *Server) Account() account.Server {
	return account.Server{
		Host:     s.host,
		Port:     s.port,
		Username: Username,
		Password: Password,
		Security: account.SecurityNone,
		Auth:     account.AuthPassword,
	}
}

// Append puts a message with the subject and flags in the inbox, as mail
// arriving, and returns its UID.
func (s *Server) Append(subject string, flags ...string) uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.modSeq++
	m := &message{uid: s.uidNext, subject: subject, flags: flags, modSeq: s.modSeq}
	s.uidNext++
	s.messages = append(s.messages, m)
	return m.uid
}

// SetFlags gives the message the flags, as another client would.
func (s *Server) SetFlags(uid uint32, flags ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m := s.find(uid); m != nil {
		s.modSeq++
		m.flags, m.modSeq = flags, s.modSeq
	}
}

// Flags returns the flags of the message.
func (s *Server) Flags(uid uint32) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m := s.find(uid); m != nil {
		return slices.Clone(m.flags)
	}
	return nil
}

// Expunge removes the message, as another client would. As CONDSTORE
// allows, the count of changes stays as it was.
func (s *Server) Expunge(uid uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = slices.DeleteFunc(s.messages, func(m *message) bool { return m.uid == uid })
}

// Renumber gives the folder a new UIDVALIDITY and its messages new UIDs, as
// a server does when it has to.
func (s *Server) Renumber() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.uidValidity++
	for _, m := range s.messages {
		m.uid = s.uidNext
		s.uidNext++
	}
}

// ForgetChanges starts the count of changes again, lower than it was, as a
// server restored from a backup might, without numbering the messages anew.
func (s *Server) ForgetChanges() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.modSeq = 1
	for _, m := range s.messages {
		m.modSeq = 1
	}
}

// NoModSeq has the server say it cannot count the changes to the folder
// (NOMODSEQ), as some do for some folders, or, given false, count them
// again.
func (s *Server) NoModSeq(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.noModSeq = on
}

// Commands returns the commands clients have sent, without their tags, and
// then forgets them.
func (s *Server) Commands() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	commands := s.commands
	s.commands = nil
	return commands
}

// find returns the message with the UID, or nil. It must be called with mu
// held.
func (s *Server) find(uid uint32) *message {
	for _, m := range s.messages {
		if m.uid == uid {
			return m
		}
	}
	return nil
}

// serve speaks IMAP to a client until it leaves.
func (s *Server) serve(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	reply := func(format string, args ...any) {
		fmt.Fprintf(w, format+"\r\n", args...)
	}
	reply("* OK [CAPABILITY IMAP4rev1 CONDSTORE UIDPLUS] condstoretest ready")
	w.Flush()
	writable := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		tag, command, _ := strings.Cut(strings.TrimRight(line, "\r\n"), " ")
		s.mu.Lock()
		s.commands = append(s.commands, command)
		s.mu.Unlock()
		name, args, _ := strings.Cut(command, " ")
		name = strings.ToUpper(name)
		if name == "UID" {
			sub, rest, _ := strings.Cut(args, " ")
			name, args = "UID "+strings.ToUpper(sub), rest
		}

		switch name {
		case "CAPABILITY":
			reply("* CAPABILITY IMAP4rev1 CONDSTORE UIDPLUS")
			reply("%s OK CAPABILITY completed", tag)
		case "LOGIN":
			if strings.Contains(args, Username) && strings.Contains(args, Password) {
				reply("%s OK [CAPABILITY IMAP4rev1 CONDSTORE UIDPLUS] logged in", tag)
			} else {
				reply("%s NO [AUTHENTICATIONFAILED] wrong password", tag)
			}
		case "NOOP":
			reply("%s OK NOOP completed", tag)
		case "LIST":
			reply(`* LIST (\HasNoChildren) "/" INBOX`)
			reply("%s OK LIST completed", tag)
		case "SELECT", "EXAMINE":
			if !strings.HasPrefix(strings.ToUpper(strings.Trim(args, `"`)), "INBOX") {
				reply("%s NO [NONEXISTENT] there is only the inbox", tag)
				break
			}
			writable = name == "SELECT"
			s.selected(reply)
			if writable {
				reply("%s OK [READ-WRITE] SELECT completed", tag)
			} else {
				reply("%s OK [READ-ONLY] EXAMINE completed", tag)
			}
		case "FETCH", "UID FETCH":
			s.fetch(reply, name == "UID FETCH", args)
			reply("%s OK FETCH completed", tag)
		case "UID SEARCH":
			s.mu.Lock()
			var uids []string
			for _, m := range s.messages {
				uids = append(uids, strconv.FormatUint(uint64(m.uid), 10))
			}
			s.mu.Unlock()
			reply("* SEARCH %s", strings.Join(uids, " "))
			reply("%s OK SEARCH completed", tag)
		case "UID STORE":
			if !writable {
				reply("%s NO the folder is read-only", tag)
				break
			}
			s.store(args)
			reply("%s OK STORE completed", tag)
		case "LOGOUT":
			reply("* BYE condstoretest leaving")
			reply("%s OK LOGOUT completed", tag)
			w.Flush()
			return
		default:
			reply("%s BAD condstoretest does not know %s", tag, name)
		}
		w.Flush()
	}
}

// selected says what the inbox holds, as SELECT and EXAMINE do.
func (s *Server) selected(reply func(string, ...any)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	reply(`* FLAGS (\Answered \Flagged \Deleted \Seen \Draft)`)
	reply(`* OK [PERMANENTFLAGS (\Answered \Flagged \Deleted \Seen \Draft \*)] flags`)
	reply("* %d EXISTS", len(s.messages))
	reply("* 0 RECENT")
	reply("* OK [UIDVALIDITY %d] UIDs", s.uidValidity)
	reply("* OK [UIDNEXT %d] next", s.uidNext)
	if s.noModSeq {
		reply("* OK [NOMODSEQ] no counting here")
	} else {
		reply("* OK [HIGHESTMODSEQ %d] changes", s.modSeq)
	}
}

// fetch answers FETCH or UID FETCH: the set of messages, the items asked for,
// and CHANGEDSINCE, which leaves out those that have not changed since.
func (s *Server) fetch(reply func(string, ...any), byUID bool, args string) {
	set, items, _ := strings.Cut(args, " ")
	upper := strings.ToUpper(items)
	var since uint64
	if _, after, ok := strings.Cut(upper, "CHANGEDSINCE "); ok {
		since, _ = strconv.ParseUint(strings.TrimRight(after, ") "), 10, 64)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	last := uint32(len(s.messages))
	if byUID && len(s.messages) > 0 {
		last = s.messages[len(s.messages)-1].uid
	}
	for i, m := range s.messages {
		seq := uint32(i + 1)
		n := seq
		if byUID {
			n = m.uid
		}
		if !inSet(set, n, last) || (since != 0 && m.modSeq <= since) {
			continue
		}
		parts := []string{fmt.Sprintf("UID %d", m.uid)}
		if strings.Contains(upper, "FLAGS") {
			parts = append(parts, "FLAGS ("+strings.Join(m.flags, " ")+")")
		}
		if since != 0 || strings.Contains(upper, "MODSEQ") {
			parts = append(parts, fmt.Sprintf("MODSEQ (%d)", m.modSeq))
		}
		if strings.Contains(upper, "RFC822.SIZE") {
			parts = append(parts, fmt.Sprintf("RFC822.SIZE %d", len(raw(m))))
		}
		if strings.Contains(upper, "INTERNALDATE") {
			parts = append(parts, `INTERNALDATE "`+date.Format("02-Jan-2006 15:04:05 -0700")+`"`)
		}
		if strings.Contains(upper, "ENVELOPE") {
			parts = append(parts, fmt.Sprintf(`ENVELOPE (%q %q (("Alice" NIL "alice" "example.com")) NIL NIL (("" NIL "user" "example.com")) NIL NIL NIL "<%d@example.com>")`,
				date.Format(time.RFC1123Z), m.subject, m.uid))
		}
		if strings.Contains(upper, "BODY.PEEK[]") {
			body := raw(m)
			parts = append(parts, fmt.Sprintf("BODY[] {%d}\r\n%s", len(body), body))
		}
		reply("* %d FETCH (%s)", seq, strings.Join(parts, " "))
	}
}

// date is when every message was sent.
var date = time.Date(2016, 3, 1, 9, 30, 0, 0, time.UTC)

// raw returns the message as it is written.
func raw(m *message) string {
	return fmt.Sprintf("From: Alice <alice@example.com>\r\nTo: user@example.com\r\nSubject: %s\r\nMessage-Id: <%d@example.com>\r\nDate: %s\r\n\r\nThe body of %s.\r\n",
		m.subject, m.uid, date.Format(time.RFC1123Z), m.subject)
}

// store answers UID STORE: +FLAGS, -FLAGS or FLAGS, silent or not, which
// counts a change to each message whose flags it changes.
func (s *Server) store(args string) {
	set, rest, _ := strings.Cut(args, " ")
	op, list, _ := strings.Cut(rest, " ")
	flags := strings.Fields(strings.Trim(list, "()"))
	s.mu.Lock()
	defer s.mu.Unlock()
	last := uint32(0)
	if len(s.messages) > 0 {
		last = s.messages[len(s.messages)-1].uid
	}
	for _, m := range s.messages {
		if !inSet(set, m.uid, last) {
			continue
		}
		next := slices.Clone(m.flags)
		switch strings.TrimSuffix(strings.ToUpper(op), ".SILENT") {
		case "+FLAGS":
			for _, f := range flags {
				if !slices.ContainsFunc(next, func(g string) bool { return strings.EqualFold(f, g) }) {
					next = append(next, f)
				}
			}
		case "-FLAGS":
			next = slices.DeleteFunc(next, func(g string) bool {
				return slices.ContainsFunc(flags, func(f string) bool { return strings.EqualFold(f, g) })
			})
		case "FLAGS":
			next = flags
		}
		if !slices.Equal(next, m.flags) {
			s.modSeq++
			m.flags, m.modSeq = next, s.modSeq
		}
	}
}

// inSet says if n is in the IMAP set, as 1:3,5,7:*, in which * is last.
func inSet(set string, n, last uint32) bool {
	num := func(s string) uint32 {
		if s == "*" {
			return last
		}
		v, _ := strconv.ParseUint(s, 10, 32)
		return uint32(v)
	}
	for _, r := range strings.Split(set, ",") {
		from, to, isRange := strings.Cut(r, ":")
		lo, hi := num(from), num(from)
		if isRange {
			hi = num(to)
		}
		if lo > hi {
			lo, hi = hi, lo
		}
		if n >= lo && n <= hi {
			return true
		}
	}
	return false
}
