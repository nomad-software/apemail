package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/mappu/miqt/qt6/mainthread"

	"github.com/nomad-software/apemail/internal/account"
	"github.com/nomad-software/apemail/internal/crypt"
	"github.com/nomad-software/apemail/internal/crypt/crypttest"
	"github.com/nomad-software/apemail/internal/remote/remotetest"
	"github.com/nomad-software/apemail/internal/settings"
)

// screenshotEnv names the file TestScreenshot draws the window into. The
// test is skipped without it; make screenshot sets it to
// docs/screenshot.png.
const screenshotEnv = "APEMAIL_SCREENSHOT"

// sampleMessage is a message of the mailbox the screenshot shows.
type sampleMessage struct {
	folder, from, subject string
	ago                   time.Duration
	flags                 []imap.Flag
}

// sampleMail is the mailbox the screenshot shows, newest first.
var sampleMail = []sampleMessage{
	{"INBOX", "Mary Somerville <mary@somerville.example>", "Lecture at the Royal Institution on Thursday", 3 * time.Hour, nil},
	{"INBOX", "Augustus De Morgan <augustus@ucl.example>", "Re: Notes on the Analytical Engine", 26 * time.Hour, []imap.Flag{imap.FlagSeen}},
	{"INBOX", "Michael Faraday <faraday@ri.example>", "Visit to the laboratory", 49 * time.Hour, nil},
	{"INBOX", "Charles Dickens <charles@household-words.example>", "Dinner on Saturday?", 72 * time.Hour, []imap.Flag{imap.FlagSeen}},
	{"INBOX", "Royal Society <secretary@royalsociety.example>", "Your fellowship subscription", 5 * 24 * time.Hour, []imap.Flag{imap.FlagSeen, imap.FlagFlagged}},
	{"INBOX", "Sophia De Morgan <sophia@ucl.example>", "Photographs from the exhibition", 8 * 24 * time.Hour, []imap.Flag{imap.FlagSeen}},
	{"INBOX", "Charles Wheatstone <wheatstone@kings.example>", "Translation of Menabrea's paper", 12 * 24 * time.Hour, []imap.Flag{imap.FlagSeen}},
	{"Sent", "Ada Lovelace <ada@example.com>", "Re: Translation of Menabrea's paper", 11 * 24 * time.Hour, []imap.Flag{imap.FlagSeen}},
}

// featured is the message the screenshot shows open: the newest, with a
// list, a table and an attachment.
const featured = "From: Charles Babbage <charles@babbage.example>\r\n" +
	"To: Ada Lovelace <ada@example.com>\r\n" +
	"Subject: Difference Engine: revised drawings\r\n" +
	"MIME-Version: 1.0\r\n" +
	"Content-Type: multipart/mixed; boundary=part\r\n" +
	"\r\n" +
	"--part\r\n" +
	"Content-Type: text/html; charset=utf-8\r\n" +
	"\r\n" +
	"<p>Dear Ada,</p>" +
	"<p>The revised drawings of the Difference Engine are attached. Mr Clement has made the changes we discussed:</p>" +
	"<ul><li>the carriage of tens now takes a single turn of the crank,</li>" +
	"<li>the printing apparatus sets its figures in stereotype, and</li>" +
	"<li>the frame is cast in two pieces rather than four.</li></ul>" +
	"<table border=\"1\" cellpadding=\"4\" cellspacing=\"0\">" +
	"<tr><th>Part</th><th>Count</th><th>Cost</th></tr>" +
	"<tr><td>Figure wheels</td><td>4,000</td><td>£1,200</td></tr>" +
	"<tr><td>Axes</td><td>96</td><td>£340</td></tr>" +
	"<tr><td>Frame</td><td>2</td><td>£180</td></tr></table>" +
	"<p>I would value your thoughts before they go to the Treasury on Monday.</p>" +
	"<p>Yours ever,<br>Charles</p>\r\n" +
	"--part\r\n" +
	"Content-Type: application/pdf\r\n" +
	"Content-Disposition: attachment; filename=\"difference-engine-drawings.pdf\"\r\n" +
	"Content-Transfer-Encoding: base64\r\n" +
	"\r\n" +
	"JVBERi0xLjQKJcOkw7zDtsOfCjIgMCBvYmoKPDwvTGVuZ3RoIDMgMCBSPj4Kc3RyZWFtCg==\r\n" +
	"--part--\r\n"

// appendRaw puts a message in a folder of the server, sent ago, with the
// flags.
func appendRaw(t *testing.T, s *remotetest.Server, folder, raw string, ago time.Duration, flags []imap.Flag) {
	t.Helper()
	date := time.Now().Add(-ago)
	raw = "Date: " + date.Format(time.RFC1123Z) + "\r\n" + raw
	c := s.Client(t)
	cmd := c.Append(folder, int64(len(raw)), &imap.AppendOptions{Flags: flags, Time: date})
	cmd.Write([]byte(raw))
	if err := cmd.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	c.Logout().Wait()
}

// TestScreenshot draws the window, with a sample mailbox and a message open,
// into the PNG file screenshotEnv names, for the README.
func TestScreenshot(t *testing.T) {
	path := os.Getenv(screenshotEnv)
	if path == "" {
		t.Skip(screenshotEnv + " is not set")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}

	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove, imap.CapIdle),
		"Archive", "Archive/2025", "Archive/2026", "Drafts", "Sent", "Trash", "Projects", "Projects/Analytical Engine", "Projects/Bernoulli Numbers")
	for i, m := range sampleMail {
		raw := fmt.Sprintf("From: %s\r\nTo: Ada Lovelace <ada@example.com>\r\nSubject: %s\r\nMessage-Id: <%d@example.com>\r\n"+
			"Content-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n", m.from, m.subject, i, m.subject)
		appendRaw(t, s, m.folder, raw, m.ago, m.flags)
	}
	appendRaw(t, s, "INBOX", featured, 40*time.Minute, nil)

	dir := t.TempDir()
	public, private := crypttest.Pair(t, t.TempDir(), "id", "")
	keys, err := crypt.Open(public, private, nil)
	if err != nil {
		t.Fatal(err)
	}
	a := account.New()
	a.Name, a.Email = "Ada Lovelace", "ada@example.com"
	a.Incoming = s.Account()
	files := Files{Settings: filepath.Join(dir, "settings.json"), Accounts: filepath.Join(dir, "accounts")}
	if err := account.Save(files.Accounts, a, keys); err != nil {
		t.Fatal(err)
	}
	config := settings.Settings{
		Width: 1280, Height: 800, SidebarWidth: 250, ListHeight: 250,
		PublicKey: public, PrivateKey: private,
		// The folders within the archive and the projects are shown.
		Expanded: map[string]map[string]bool{a.ID: {
			settings.FolderKey("Archive"):  true,
			settings.FolderKey("Projects"): true,
		}},
	}
	if err := config.Save(files.Settings); err != nil {
		t.Fatal(err)
	}

	var w *Window
	mainthread.Wait(func() {
		w = NewWindow(files)
		w.Start()
	})
	t.Cleanup(func() { mainthread.Wait(func() { w.Close() }) })
	wait(t, "the inbox", func() bool { return w.folder == "INBOX" && len(w.list.rows) == len(sampleMail) })

	mainthread.Wait(func() {
		clickRow(w.list, 0)
	})
	wait(t, "the message", func() bool { return w.view.msg != nil && strings.Contains(w.view.msg.Subject, "Difference Engine") })
	wait(t, "the message to be read", func() bool { return w.list.rows[0].Has(`\Seen`) })

	saved := mainthread.Wait2(func() bool { return w.Grab().Save(path) })
	if !saved {
		t.Fatalf("the screenshot could not be saved to %s", path)
	}
}
