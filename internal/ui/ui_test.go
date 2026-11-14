package ui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/nomad-software/qute"
	"github.com/nomad-software/qute/qtcore"
	"github.com/nomad-software/qute/qtgui"
	"github.com/nomad-software/qute/qtwidgets"
	"github.com/nomad-software/qute/qutetest"

	"github.com/nomad-software/apemail/internal/account"
	"github.com/nomad-software/apemail/internal/crypt"
	"github.com/nomad-software/apemail/internal/crypt/crypttest"
	"github.com/nomad-software/apemail/internal/message"
	"github.com/nomad-software/apemail/internal/remote/remotetest"
	"github.com/nomad-software/apemail/internal/send/sendtest"
	"github.com/nomad-software/apemail/internal/settings"
	"github.com/nomad-software/apemail/internal/store"
)

// TestMain runs the Qt event loop on the main thread and the tests on another
// goroutine. Tests must touch widgets on the UI's thread, through
// qutetest.Run, or qute.PostSync where there is no test to fail.
func TestMain(m *testing.M) {
	os.Setenv("QT_QPA_PLATFORM", "offscreen")
	PrepareWeb()
	// A warning is kept, to be told if a test then waits in vain, rather
	// than shown in a dialog no one closes.
	warning = func(_ qtwidgets.QWidgetLike, title, text string) {
		warned.Lock()
		defer warned.Unlock()
		warned.list = append(warned.list, title+": "+text)
	}
	// So is what the program tells the user, and a question about saving
	// what is written, which a test that expects one answers with
	// answerSave: it is not saved, and the window stays.
	information = warning
	askSave = func(_ qtwidgets.QWidgetLike, title, text string) qtwidgets.QMessageBoxStandardButtons {
		warning(nil, title, text)
		return qtwidgets.QMessageBoxCancel
	}
	// Tests close windows, which does not end the event loop here.
	qutetest.TestMain(m)
}

// clickRow makes the row of the list the current one and the only one
// selected, as clicking it does.
func clickRow(l *messageList, row int) {
	l.SetCurrentIndex(l.model.Index(row, 0))
}

// addRow adds the row of the list to those selected, as Ctrl+click does.
func addRow(l *messageList, row int) {
	l.SelectionModel().Select(l.model.Index(row, 0), qtcore.QItemSelectionModelSelect|qtcore.QItemSelectionModelRows)
}

// warned are the warnings the program has given, which a wait that times out
// tells, as they are likely why.
var warned struct {
	sync.Mutex
	list []string
}

// warnings returns what the program has warned of, for a test that fails.
func warnings() string {
	warned.Lock()
	defer warned.Unlock()
	if len(warned.list) == 0 {
		return ""
	}
	return "; the program warned: " + strings.Join(warned.list, "; ")
}

// forgetWarnings forgets the warnings given before, as at the start of a
// test, whose own are told if it fails.
func forgetWarnings() {
	warned.Lock()
	defer warned.Unlock()
	warned.list = nil
}

// answer has confirm say yes or no until the test ends, without a dialog
// being shown, and returns the count of the questions it is asked.
func answer(t *testing.T, yes bool) *int {
	asked := 0
	old := confirm
	confirm = func(_ qtwidgets.QWidgetLike, _, _ string) bool {
		asked++
		return yes
	}
	t.Cleanup(func() { confirm = old })
	return &asked
}

// answerSave has askSave answer with the button until the test ends, without
// a dialog being shown, and returns the count of the questions it is asked.
func answerSave(t *testing.T, button qtwidgets.QMessageBoxStandardButtons) *int {
	asked := 0
	old := askSave
	askSave = func(_ qtwidgets.QWidgetLike, _, _ string) qtwidgets.QMessageBoxStandardButtons {
		asked++
		return button
	}
	t.Cleanup(func() { askSave = old })
	return &asked
}

// wait waits for the condition, which is checked on the UI's thread, to
// hold, failing the test if it does not within a few seconds.
func wait(t *testing.T, what string, cond func() bool) {
	t.Helper()
	eventually(t, what, func() bool { return onUI(cond) })
}

// eventually waits for the condition, which is checked on the test's own
// goroutine, as what is on the server is, to hold, failing the test if it
// does not within a few seconds.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("waiting for %s%s", what, warnings())
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
	forgetWarnings()
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
	qutetest.Run(t, func() {
		w = NewWindow(files)
		w.Start()
	})
	t.Cleanup(func() {
		qutetest.Run(t, func() { w.Close() })
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
	// The message is drawn, and its images asked for, only where it is
	// seen.
	qute.PostSync(w.Show)
	wait(t, "the inbox", func() bool { return len(w.list.rows) == 3 && w.folder == "INBOX" })

	// The newest message is first.
	qutetest.Run(t, func() {
		clickRow(w.list, 0)
	})
	wait(t, "the message", func() bool { return w.view.msg != nil })
	if body := pageText(t, w.view); !strings.Contains(body, "attached") {
		t.Errorf("body = %q", body)
	}
	qutetest.Run(t, func() {
		if w.view.msg.Subject != "Compiler notes" {
			t.Errorf("shown = %q", w.view.msg.Subject)
		}
		if !w.reply.IsEnabled() || !w.del.IsEnabled() {
			t.Error("the actions are not enabled")
		}
	})
	// The image on the internet is not fetched, but kept to be if the
	// user asks.
	qutetest.Run(t, func() {
		if len(w.view.images) != 0 || !slices.Equal(w.view.wanted, []string{"https://tracker.example.com/pixel.gif"}) {
			t.Errorf("loaded %d images, and the page asked for %q", len(w.view.images), w.view.wanted)
		}
	})

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
	qutetest.Run(t, func() {
		clickRow(w.list, 0)
	})
	wait(t, "the message", func() bool { return w.view.msg != nil })
	// Showing it marked it as read.
	wait(t, "the message to be read", func() bool { return len(w.list.rows) == 1 && w.list.rows[0].Has(`\Seen`) })

	// Moving it to the trash loses nothing, so the user is not asked.
	asked := answer(t, false)
	qute.PostSync(w.deleteMessages)
	wait(t, "the message to go", func() bool { return len(w.list.rows) == 0 })
	if *asked != 0 {
		t.Errorf("asked %d times", *asked)
	}
	if msgs := s.Messages(t, "Trash"); len(msgs) != 1 || msgs[0].Subject != "Unwanted" {
		t.Errorf("trash = %+v", msgs)
	}

	// Deleting it from the trash deletes it for good, so the user is
	// asked, and nothing is done if they say no.
	qutetest.Run(t, func() { w.folders.selectFolder(w.account, "Trash") })
	wait(t, "the trash", func() bool { return w.folder == "Trash" && len(w.list.rows) == 1 })
	qutetest.Run(t, func() {
		clickRow(w.list, 0)
	})
	wait(t, "the selection", func() bool { return len(w.list.selectedUIDs()) == 1 })
	qute.PostSync(w.deleteMessages)
	if *asked != 1 {
		t.Errorf("asked %d times", *asked)
	}
	if msgs := s.Messages(t, "Trash"); len(msgs) != 1 {
		t.Errorf("trash after no = %+v", msgs)
	}

	asked = answer(t, true)
	qute.PostSync(w.deleteMessages)
	wait(t, "the message to go for good", func() bool { return len(w.list.rows) == 0 })
	if *asked != 1 {
		t.Errorf("asked %d times", *asked)
	}
	if msgs := s.Messages(t, "Trash"); len(msgs) != 0 {
		t.Errorf("trash = %+v", msgs)
	}
}

// TestDeleteForGoodFromFolderAsked checks that the messages the user agrees
// to delete for good are deleted from the folder they were asked about,
// though another is listed by the time they answer.
func TestDeleteForGoodFromFolderAsked(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Trash")
	s.Append(t, "INBOX", "Kept")
	s.Append(t, "Trash", "Unwanted")
	w := newTestWindow(t, s)
	wait(t, "the inbox", func() bool { return w.folder == "INBOX" && len(w.list.rows) == 1 })
	qutetest.Run(t, func() { w.folders.selectFolder(w.account, "Trash") })
	wait(t, "the trash", func() bool { return w.folder == "Trash" && len(w.list.rows) == 1 })
	qutetest.Run(t, func() { clickRow(w.list, 0) })
	wait(t, "the selection", func() bool { return len(w.list.selectedUIDs()) == 1 })

	old := confirm
	t.Cleanup(func() { confirm = old })
	confirm = func(_ qtwidgets.QWidgetLike, _, text string) bool {
		if !strings.Contains(text, "from Trash") {
			t.Errorf("asked %q", text)
		}
		w.folders.selectFolder(w.account, "INBOX")
		return true
	}
	qute.PostSync(w.deleteMessages)
	eventually(t, "the message to go for good", func() bool { return len(s.Messages(t, "Trash")) == 0 })
	if msgs := s.Messages(t, "INBOX"); len(msgs) != 1 || msgs[0].Subject != "Kept" {
		t.Errorf("the inbox = %+v", msgs)
	}
	if warned := warnings(); warned != "" {
		t.Error(warned)
	}
}

func TestArchiveMakesFolder(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	s.Append(t, "INBOX", "Keep this")
	w := newTestWindow(t, s)
	wait(t, "the inbox", func() bool { return len(w.list.rows) == 1 && w.folder == "INBOX" })
	qutetest.Run(t, func() {
		clickRow(w.list, 0)
	})
	wait(t, "the selection", func() bool { return len(w.list.selectedUIDs()) == 1 })

	qute.PostSync(w.archiveMessages)
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
	qutetest.Run(t, func() {
		clickRow(w.list, 0)
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
	qutetest.Run(t, func() { w.showMessage(w.view.uid) })
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

	qutetest.Run(t, func() { w.folders.selectFolder(w.account, "Sent") })
	wait(t, "the sent folder", func() bool { return w.folder == "Sent" })
	qutetest.Run(t, func() { w.view.clear() })
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
	qutetest.Run(t, func() { w.folders.selectFolder(w.account, "Trash") })
	wait(t, "the trash", func() bool { return w.folder == "Trash" && len(w.list.rows) == 1 })

	s.Append(t, "Trash", "newer")
	qutetest.Run(t, func() { w.getMail.Trigger() })
	wait(t, "the new message in the trash", func() bool { return len(w.list.rows) == 2 })
}

// TestInboxSelectedAfterFocus checks that the inbox is shown when the client
// starts though the side bar took the focus before the folders were listed,
// which makes the account, its only item then, the one selected.
func TestInboxSelectedAfterFocus(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Sent")
	s.Append(t, "INBOX", "Analytical engine")
	held, release := s.Hold(t, "LIST")
	w := newTestWindow(t, s)
	<-held
	qutetest.Run(t, func() {
		w.Show()
		w.ActivateWindow()
		w.folders.SetFocus()
		// Qt selects the first item of a tree that takes the focus
		// with none selected; where the platform gives no focus, it is
		// selected as Qt would.
		if w.account == "" {
			w.folders.SetCurrentItem(w.folders.accounts[w.order[0]])
		}
		if w.account == "" || w.folder != "" {
			t.Fatalf("selected %q %q before the folders were listed", w.account, w.folder)
		}
	})
	release()
	wait(t, "the inbox", func() bool { return w.folder == "INBOX" && len(w.list.rows) == 1 })

	// Only when the client starts: the account chosen later stays chosen
	// when its folders are listed again.
	qutetest.Run(t, func() {
		id := w.account
		w.folders.SetCurrentItem(w.folders.accounts[id])
		w.FoldersChanged(id, []store.Folder{{Name: "INBOX", Delim: "/"}, {Name: "Sent", Delim: "/"}})
		if w.folder != "" {
			t.Errorf("folder = %q, not the account chosen", w.folder)
		}
	})
}

// folderText returns what the tree shows of a folder of the account that is
// shown.
func folderText(w *Window, folder string) string {
	return onUI(func() string {
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
	qutetest.Run(t, func() { w.syncAll.Trigger() })
	select {
	case <-held:
	case <-time.After(5 * time.Second):
		t.Fatal("the trash was not reached")
	}
	qutetest.Run(t, func() {
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

	qutetest.Run(t, func() { w.updateCounts.Trigger() })
	wait(t, "the count", func() bool { return folderText(w, "Trash") == "Trash (1)" })

	qutetest.Run(t, func() {
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

// TestTextLines checks that a plain text message is shown with the lines
// it has, and no more, though mail ends them in CRLF.
func TestTextLines(t *testing.T) {
	m, err := message.Parse([]byte("From: a@example.com\r\nSubject: s\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" +
		"\r\nHello,\r\n\r\nThe body.\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	v := newShownView(t, 400, 300)
	showWhole(t, v, m)
	// As in a browser, a line break just inside <pre> is not shown.
	if got := pageText(t, v); got != "Hello,\n\nThe body." {
		t.Errorf("shown %q", got)
	}
}

// TestDeepTablesShownQuickly checks that a message which nests tables a
// dozen deep, as newsletters do to place things, is shown at once: Qt's own
// browser took a minute to lay one out, the program not answering
// meanwhile.
func TestDeepTablesShownQuickly(t *testing.T) {
	cell := `<table width="100%" style="min-width: 100%"><tbody><tr><td align="center" style="padding: 10px">`
	var b strings.Builder
	for range 3 {
		b.WriteString(strings.Repeat(cell, 16))
		b.WriteString("<p>Your booking details. " + strings.Repeat("Some words to lay out. ", 50) + "</p>")
		b.WriteString(strings.Repeat("</td></tr></tbody></table>", 16))
	}
	m, err := message.Parse([]byte("From: a@example.com\r\nSubject: s\r\nContent-Type: text/html; charset=utf-8\r\n\r\n" + b.String()))
	if err != nil {
		t.Fatal(err)
	}
	v := newShownView(t, 800, 600)
	start := time.Now()
	showWhole(t, v, m)
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("the message took %v to show", took)
	}
	if got := pageText(t, v); !strings.Contains(got, "Your booking details.") {
		t.Errorf("shown %q", got)
	}
}

// TestRestartKeepsFolderListed checks that the folder listed is opened again
// on the engine an account is restarted with when its settings change, so
// that the list keeps up with the server, as the old engine did.
func TestRestartKeepsFolderListed(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Work")
	s.Append(t, "Work", "first")
	w := newTestWindow(t, s)
	wait(t, "the folders", func() bool { return w.folders.folders[w.account]["Work"] != nil })
	qutetest.Run(t, func() { w.folders.selectFolder(w.account, "Work") })
	wait(t, "the folder", func() bool { return w.folder == "Work" && len(w.list.rows) == 1 })

	s.Append(t, "Work", "second")
	qutetest.Run(t, func() {
		// As the account's settings are saved.
		w.startAccount(w.engines[w.account].Account())
	})
	wait(t, "the new message", func() bool { return w.folder == "Work" && len(w.list.rows) == 2 })
}

// TestRemoveAccount checks that Remove Account in the Accounts menu removes
// the account only once the user agrees, and leaves the server alone.
func TestRemoveAccount(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Work")
	s.Append(t, "Work", "d")
	w := newTestWindow(t, s)
	wait(t, "the folders", func() bool { return w.folders.folders[w.account]["Work"] != nil })
	id := onUI(func() string { return w.account })

	questions := asked(t, false)
	qute.PostSync(w.removeAccount.Trigger)
	if len(*questions) != 1 || !strings.Contains((*questions)[0], "not touched") {
		t.Errorf("asked %q", *questions)
	}
	if onUI(func() bool { return w.engines[id] == nil }) {
		t.Fatal("the account was removed when the user said no")
	}

	asked(t, true)
	qute.PostSync(w.removeAccount.Trigger)
	if !onUI(func() bool { return w.engines[id] == nil && w.folders.accounts[id] == nil }) {
		t.Error("the account was not removed")
	}
	if got := onServer(t, s); !slices.Equal(got, []string{"INBOX", "Work"}) {
		t.Errorf("the server has %q", got)
	}
	if msgs := s.Messages(t, "Work"); len(msgs) != 1 {
		t.Errorf("Work = %+v", msgs)
	}
}

// onUI returns what f does, which it runs on the UI's thread.
func onUI[T any](f func() T) T {
	var v T
	qute.PostSync(func() { v = f() })
	return v
}

// rgb returns the colour as a pixel of an image has it, wholly opaque.
func rgb(c qtgui.QColor) uint {
	return 0xff000000 | uint(c.R)<<16 | uint(c.G)<<8 | uint(c.B)
}

// colourOf returns the colour of a pixel of an image, whatever its opacity.
func colourOf(pixel uint) qtgui.QColor {
	return qtgui.RGB(uint8(pixel>>16), uint8(pixel>>8), uint8(pixel))
}

// TestReceiptAskedOnceWhenChosenAgain checks that a message chosen, left and
// chosen again before it has been downloaded is shown once, and its return
// receipt asked for once: the first download is not shown, though the
// message is selected when it arrives.
func TestReceiptAskedOnceWhenChosenAgain(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Sent")
	s.AppendRaw(t, "INBOX", askingReceipt)
	s.Append(t, "INBOX", "other")
	w := newTestWindow(t, s)
	asked := answer(t, false)
	wait(t, "the messages", func() bool { return len(w.list.rows) == 2 })
	row := func(subject string) int {
		for i, r := range w.list.rows {
			if r.Subject == subject {
				return i
			}
		}
		t.Fatalf("no %s", subject)
		return 0
	}

	held, release := s.Hold(t, "BODY.PEEK[]")
	qutetest.Run(t, func() { clickRow(w.list, row("Did you get this?")) })
	select {
	case <-held:
	case <-time.After(5 * time.Second):
		t.Fatal("the message was not downloaded")
	}
	qutetest.Run(t, func() {
		clickRow(w.list, row("other"))
		clickRow(w.list, row("Did you get this?"))
	})
	release()
	wait(t, "the message", func() bool { return w.view.msg != nil && w.view.msg.Subject == "Did you get this?" })
	wait(t, "the receipt flag", func() bool { return hasFlag(t, s, "INBOX", "$MDNSent") })
	time.Sleep(200 * time.Millisecond)
	if *asked != 1 {
		t.Errorf("asked %d times", *asked)
	}
}
