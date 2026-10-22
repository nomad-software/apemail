package ui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/mainthread"

	"github.com/nomad-software/apemail/internal/account"
	"github.com/nomad-software/apemail/internal/crypt"
	"github.com/nomad-software/apemail/internal/crypt/crypttest"
	"github.com/nomad-software/apemail/internal/remote/remotetest"
	"github.com/nomad-software/apemail/internal/send/sendtest"
	"github.com/nomad-software/apemail/internal/settings"
)

// TestMain runs the Qt event loop on the main thread and the tests on another
// goroutine. Tests must touch widgets through mainthread.Wait.
func TestMain(m *testing.M) {
	os.Setenv("QT_QPA_PLATFORM", "offscreen")
	qt.NewQApplication(os.Args[:1])
	// Tests close windows, which must not end the event loop.
	qt.QGuiApplication_SetQuitOnLastWindowClosed(false)

	code := 0
	go func() {
		code = m.Run()
		mainthread.Start(qt.QCoreApplication_Quit)
	}()
	qt.QApplication_Exec()
	os.Exit(code)
}

// answer has confirm say yes or no until the test ends, without a dialog
// being shown, and returns the count of the questions it is asked.
func answer(t *testing.T, yes bool) *int {
	asked := 0
	old := confirm
	confirm = func(_ *qt.QWidget, _, _ string) bool {
		asked++
		return yes
	}
	t.Cleanup(func() { confirm = old })
	return &asked
}

// wait waits for the condition, which is checked on the UI's thread, to
// hold, failing the test if it does not within a few seconds.
func wait(t *testing.T, what string, cond func() bool) {
	t.Helper()
	eventually(t, what, func() bool { return mainthread.Wait2(cond) })
}

// eventually waits for the condition, which is checked on the test's own
// goroutine, as what is on the server is, to hold, failing the test if it
// does not within a few seconds.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

const htmlMessage = "From: Grace Hopper <grace@example.com>\r\n" +
	"To: user@example.com\r\n" +
	"Subject: Compiler notes\r\n" +
	"Date: Tue, 03 Jan 2006 09:30:00 +0000\r\n" +
	"MIME-Version: 1.0\r\n" +
	"Content-Type: text/html; charset=utf-8\r\n" +
	"\r\n" +
	"<p>Hello,</p><p>The notes are <b>attached</b>. <img src=\"https://tracker.example.com/pixel.gif\"></p>\r\n"

// newTestWindow starts a window with an account on the server.
func newTestWindow(t *testing.T, s *remotetest.Server, change ...func(*account.Account)) *Window {
	t.Helper()
	dir := t.TempDir()
	public, private := crypttest.Pair(t, t.TempDir(), "id", "")
	keys, err := crypt.Open(public, private, nil)
	if err != nil {
		t.Fatal(err)
	}
	files := Files{Settings: filepath.Join(dir, "settings.json"), Accounts: filepath.Join(dir, "accounts")}
	config := settings.Settings{Width: 1100, Height: 700, PublicKey: public, PrivateKey: private}
	if err := config.Save(files.Settings); err != nil {
		t.Fatal(err)
	}
	a := account.New()
	a.Name, a.Email = "Ada Lovelace", "ada@example.com"
	a.Incoming = s.Account()
	for _, f := range change {
		f(&a)
	}
	if err := account.Save(files.Accounts, a, keys); err != nil {
		t.Fatal(err)
	}

	var w *Window
	mainthread.Wait(func() {
		w = NewWindow(files)
		w.Start()
	})
	t.Cleanup(func() {
		mainthread.Wait(func() { w.Close() })
	})
	return w
}

func TestWindow(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove, imap.CapIdle), "Archive", "Drafts", "Sent", "Trash", "Projects", "Projects/Engine")
	s.Append(t, "INBOX", "Analytical engine", imap.FlagSeen)
	s.Append(t, "INBOX", "Bernoulli numbers")
	c := s.Client(t)
	cmd := c.Append("INBOX", int64(len(htmlMessage)), nil)
	cmd.Write([]byte(htmlMessage))
	cmd.Close()
	if _, err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	before := s.Messages(t, "INBOX")

	w := newTestWindow(t, s)
	wait(t, "the inbox", func() bool { return len(w.list.rows) == 3 && w.folder == "INBOX" })

	// The newest message is first.
	mainthread.Wait(func() {
		w.list.SetCurrentIndex(w.list.model.Index(0, 0, qt.NewQModelIndex()))
	})
	wait(t, "the message", func() bool { return w.view.msg != nil })
	mainthread.Wait(func() {
		if w.view.msg.Subject != "Compiler notes" {
			t.Errorf("shown = %q", w.view.msg.Subject)
		}
		if !strings.Contains(w.view.browser.ToPlainText(), "attached") {
			t.Errorf("body = %q", w.view.browser.ToPlainText())
		}
		if !w.reply.IsEnabled() || !w.del.IsEnabled() {
			t.Error("the actions are not enabled")
		}
	})
	// The image on the internet is not fetched.
	if got := mainthread.Wait2(func() bool {
		return w.view.resource(int(qt.QTextDocument__ImageResource), "https://tracker.example.com/pixel.gif").IsValid()
	}); got {
		t.Error("a remote image was loaded")
	}

	// Showing it marks it as read.
	wait(t, "the message to be read", func() bool { return w.list.rows[0].Has(`\Seen`) })

	if after := s.Messages(t, "INBOX"); len(after) != len(before) {
		t.Fatalf("the inbox changed from %+v to %+v", before, after)
	}
}

func TestDeleteMovesToTrash(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Trash")
	s.Append(t, "INBOX", "Unwanted")
	w := newTestWindow(t, s)
	wait(t, "the inbox", func() bool { return len(w.list.rows) == 1 && w.folder == "INBOX" })
	mainthread.Wait(func() {
		w.list.SetCurrentIndex(w.list.model.Index(0, 0, qt.NewQModelIndex()))
	})
	wait(t, "the message", func() bool { return w.view.msg != nil })
	// Showing it marked it as read.
	wait(t, "the message to be read", func() bool { return len(w.list.rows) == 1 && w.list.rows[0].Has(`\Seen`) })

	// Moving it to the trash loses nothing, so the user is not asked.
	asked := answer(t, false)
	mainthread.Wait(w.deleteMessages)
	wait(t, "the message to go", func() bool { return len(w.list.rows) == 0 })
	if *asked != 0 {
		t.Errorf("asked %d times", *asked)
	}
	if msgs := s.Messages(t, "Trash"); len(msgs) != 1 || msgs[0].Subject != "Unwanted" {
		t.Errorf("trash = %+v", msgs)
	}

	// Deleting it from the trash deletes it for good, so the user is
	// asked, and nothing is done if they say no.
	mainthread.Wait(func() { w.folders.selectFolder(w.account, "Trash") })
	wait(t, "the trash", func() bool { return w.folder == "Trash" && len(w.list.rows) == 1 })
	mainthread.Wait(func() {
		w.list.SetCurrentIndex(w.list.model.Index(0, 0, qt.NewQModelIndex()))
	})
	wait(t, "the selection", func() bool { return len(w.list.selectedUIDs()) == 1 })
	mainthread.Wait(w.deleteMessages)
	if *asked != 1 {
		t.Errorf("asked %d times", *asked)
	}
	if msgs := s.Messages(t, "Trash"); len(msgs) != 1 {
		t.Errorf("trash after no = %+v", msgs)
	}

	asked = answer(t, true)
	mainthread.Wait(w.deleteMessages)
	wait(t, "the message to go for good", func() bool { return len(w.list.rows) == 0 })
	if *asked != 1 {
		t.Errorf("asked %d times", *asked)
	}
	if msgs := s.Messages(t, "Trash"); len(msgs) != 0 {
		t.Errorf("trash = %+v", msgs)
	}
}

func TestArchiveMakesFolder(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	s.Append(t, "INBOX", "Keep this")
	w := newTestWindow(t, s)
	wait(t, "the inbox", func() bool { return len(w.list.rows) == 1 && w.folder == "INBOX" })
	mainthread.Wait(func() {
		w.list.SetCurrentIndex(w.list.model.Index(0, 0, qt.NewQModelIndex()))
	})
	wait(t, "the selection", func() bool { return len(w.list.selectedUIDs()) == 1 })

	mainthread.Wait(w.archiveMessages)
	wait(t, "the message to go", func() bool { return len(w.list.rows) == 0 })
	// It goes in the folder for the year it was sent in, within the
	// archive, as other clients do.
	if msgs := s.Messages(t, "Archive/2006"); len(msgs) != 1 || msgs[0].Subject != "Keep this" {
		t.Errorf("archive = %+v", msgs)
	}
	// The new folders are in the tree, the year within the archive.
	wait(t, "the folders in the tree", func() bool {
		year, ok := w.folders.folders[w.account]["Archive/2006"]
		return ok && year.Parent() != nil && year.Parent().Text(0) == "Archive"
	})
}

// TestFormatDate checks that the list shows the full date and time of every
// message, today's as much as older ones.
func TestFormatDate(t *testing.T) {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 9, 5, 0, 0, time.Local)
	for when, want := range map[time.Time]string{
		today: today.Format("02/01/2006") + " 09:05",
		time.Date(2024, 3, 7, 18, 30, 0, 0, time.Local): "07/03/2024 18:30",
		{}: "",
	} {
		if got := formatDate(when); got != want {
			t.Errorf("formatDate(%v) = %q, want %q", when, got, want)
		}
	}
}

// askingReceipt is a message that asks for a return receipt.
const askingReceipt = "From: Alice <alice@example.com>\r\n" +
	"To: Ada Lovelace <ada@example.com>\r\n" +
	"Subject: Did you get this?\r\n" +
	"Message-Id: <asking@example.com>\r\n" +
	"Date: Mon, 02 Jan 2006 15:04:05 +0000\r\n" +
	"Disposition-Notification-To: Alice <receipts@example.com>\r\n" +
	"Content-Type: text/plain\r\n" +
	"\r\n" +
	"Tell me when you have read it.\r\n"

// showFirst shows the first message of the folder that is listed.
func showFirst(t *testing.T, w *Window) {
	t.Helper()
	wait(t, "the messages", func() bool { return len(w.list.rows) > 0 })
	mainthread.Wait(func() {
		w.list.SetCurrentIndex(w.list.model.Index(0, 0, qt.NewQModelIndex()))
	})
	wait(t, "the message", func() bool { return w.view.msg != nil })
}

// hasFlag says if the first message of a folder on the server has a flag.
func hasFlag(t *testing.T, s *remotetest.Server, folder string, flag imap.Flag) bool {
	msgs := s.Messages(t, folder)
	return len(msgs) > 0 && slices.Contains(msgs[0].Flags, flag)
}

func TestReturnReceiptSent(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Sent")
	s.AppendRaw(t, "INBOX", askingReceipt)
	smtp := sendtest.Start(t, true)
	w := newTestWindow(t, s, func(a *account.Account) { a.Outgoing = smtp.Account })
	asked := answer(t, true)
	showFirst(t, w)

	eventually(t, "the receipt", func() bool { return len(smtp.Messages()) == 1 })
	receipt := smtp.Messages()[0]
	if !slices.Equal(receipt.To, []string{"receipts@example.com"}) || receipt.From != "ada@example.com" ||
		!strings.Contains(string(receipt.Data), "Subject: Return Receipt (displayed) - Did you get this?") ||
		!strings.Contains(string(receipt.Data), "Original-Message-ID: <asking@example.com>") {
		t.Errorf("receipt %+v\n%s", receipt, receipt.Data)
	}
	eventually(t, "the message marked", func() bool { return hasFlag(t, s, "INBOX", "$MDNSent") })
	// The receipt is not kept as mail the user sent.
	if msgs := s.Messages(t, "Sent"); len(msgs) != 0 {
		t.Errorf("sent folder %+v", msgs)
	}

	// Shown again, once the mark is known, the message asks nothing.
	wait(t, "the mark in the list", func() bool {
		sum, ok := w.list.summary(w.view.uid)
		return ok && sum.Has(mdnSent)
	})
	mainthread.Wait(func() { w.showMessage(w.view.uid) })
	wait(t, "the message again", func() bool { return w.view.msg != nil })
	if *asked != 1 || len(smtp.Messages()) != 1 {
		t.Errorf("asked %d times, sent %d receipts", *asked, len(smtp.Messages()))
	}
}

// TestReturnReceiptRefused checks that a receipt the user refuses is not
// sent, and that they are not asked again.
func TestReturnReceiptRefused(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	s.AppendRaw(t, "INBOX", askingReceipt)
	smtp := sendtest.Start(t, true)
	w := newTestWindow(t, s, func(a *account.Account) { a.Outgoing = smtp.Account })
	asked := answer(t, false)
	showFirst(t, w)

	eventually(t, "the message marked", func() bool { return hasFlag(t, s, "INBOX", "$MDNSent") })
	if *asked != 1 || len(smtp.Messages()) != 0 {
		t.Errorf("asked %d times, sent %d receipts", *asked, len(smtp.Messages()))
	}
}

// TestReturnReceiptNotSent checks that a receipt that could not be sent
// leaves the message unmarked, so that the user is asked again.
func TestReturnReceiptNotSent(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	s.AppendRaw(t, "INBOX", askingReceipt)
	// The account has no outgoing server.
	w := newTestWindow(t, s)
	answer(t, true)
	showFirst(t, w)

	wait(t, "the error", func() bool {
		return strings.Contains(w.StatusBar().CurrentMessage(), "the return receipt could not be sent")
	})
	// It is read, but not marked as answered for a receipt.
	eventually(t, "the message read", func() bool { return hasFlag(t, s, "INBOX", imap.FlagSeen) })
	if hasFlag(t, s, "INBOX", "$MDNSent") {
		t.Error("the message was marked though no receipt was sent")
	}
}

// TestReturnReceiptNotAsked checks that the user is asked nothing of a
// message that asks for no receipt, nor of one of their own in the sent
// folder.
func TestReturnReceiptNotAsked(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Sent")
	s.Append(t, "INBOX", "No receipt")
	s.AppendRaw(t, "Sent", askingReceipt)
	w := newTestWindow(t, s)
	asked := answer(t, true)
	showFirst(t, w)

	mainthread.Wait(func() { w.folders.selectFolder(w.account, "Sent") })
	wait(t, "the sent folder", func() bool { return w.folder == "Sent" })
	mainthread.Wait(func() { w.view.clear() })
	showFirst(t, w)
	if *asked != 0 {
		t.Errorf("asked %d times", *asked)
	}
	if hasFlag(t, s, "Sent", "$MDNSent") {
		t.Error("the user's own mail was marked")
	}
}

// TestGetMailSelected checks that Get Mail checks the folder that is
// selected.
func TestGetMailSelected(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Trash")
	s.Append(t, "Trash", "old")
	w := newTestWindow(t, s)
	wait(t, "the trash listed", func() bool { return w.folders.folders[w.account]["Trash"] != nil })
	mainthread.Wait(func() { w.folders.selectFolder(w.account, "Trash") })
	wait(t, "the trash", func() bool { return w.folder == "Trash" && len(w.list.rows) == 1 })

	s.Append(t, "Trash", "newer")
	mainthread.Wait(func() { w.getMail.Trigger() })
	wait(t, "the new message in the trash", func() bool { return len(w.list.rows) == 2 })
}

// folderText returns what the tree shows of a folder of the account that is
// shown.
func folderText(w *Window, folder string) string {
	return mainthread.Wait2(func() string {
		item := w.folders.folders[w.account][folder]
		if item == nil {
			return ""
		}
		return item.Text(0)
	})
}

// TestSyncAllFromMenu checks that the Accounts menu synchronises every
// folder and, while it does, stops it instead.
func TestSyncAllFromMenu(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Trash")
	s.Append(t, "Trash", "old")
	w := newTestWindow(t, s)
	wait(t, "the folders", func() bool { return w.account != "" && w.folders.folders[w.account]["Trash"] != nil })

	// The inbox is empty, so the first headers asked for are the trash's.
	held, release := s.Hold(t, "ENVELOPE")
	mainthread.Wait(func() { w.syncAll.Trigger() })
	select {
	case <-held:
	case <-time.After(5 * time.Second):
		t.Fatal("the trash was not reached")
	}
	mainthread.Wait(func() {
		if got := w.syncAll.Text(); got != stopSyncAllText {
			t.Errorf("while synchronising the item says %q", got)
		}
		w.syncAll.Trigger()
	})
	wait(t, "the sync stopped", func() bool {
		return strings.Contains(w.StatusBar().CurrentMessage(), "Stopped synchronising") && w.syncAll.Text() == syncAllText
	})
	release()
}

// TestUpdateCountsFromMenu checks that the unread messages of a folder that
// has not been opened are counted, and that the count stays when the
// folders are listed again and the tree is made again.
func TestUpdateCountsFromMenu(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Trash")
	s.Append(t, "Trash", "unread")
	s.Append(t, "Trash", "read", imap.FlagSeen)
	w := newTestWindow(t, s)
	wait(t, "the folders", func() bool { return w.account != "" && w.folders.folders[w.account]["Trash"] != nil })
	if got := folderText(w, "Trash"); got != "Trash" {
		t.Fatalf("before counting the trash shows %q", got)
	}

	mainthread.Wait(func() { w.updateCounts.Trigger() })
	wait(t, "the count", func() bool { return folderText(w, "Trash") == "Trash (1)" })

	mainthread.Wait(func() {
		before := w.folders.folders[w.account]["Trash"]
		w.FoldersChanged(w.account, w.engines[w.account].Folders())
		if w.folders.folders[w.account]["Trash"] == before {
			t.Fatal("the tree was not made again")
		}
	})
	if got := folderText(w, "Trash"); got != "Trash (1)" {
		t.Errorf("once the tree was made again the trash shows %q", got)
	}
}
