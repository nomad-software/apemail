package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"
	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/mainthread"

	"github.com/nomad-software/apemail/internal/account"
	"github.com/nomad-software/apemail/internal/remote/remotetest"
	"github.com/nomad-software/apemail/internal/settings"
)

func TestLayoutIsKept(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Sent")
	w := newTestWindow(t, s)
	wait(t, "the account", func() bool { return len(w.engines) == 1 })

	mainthread.Wait(func() {
		w.Resize(900, 640)
		w.split.SetSizes([]int{310, 590})
		w.right.SetSizes([]int{220, 330})
		w.list.setColumnWidths(180, 130)
		w.Close()
	})
	config := settings.Load(w.files.Settings)
	if config.Width != 900 || config.Height != 640 || config.SidebarWidth != 310 || config.ListHeight != 220 ||
		config.CorrespondentWidth != 180 || config.DateWidth != 130 {
		t.Fatalf("saved = %+v", config)
	}

	// A new window starts as the last one was left.
	var again *Window
	mainthread.Wait(func() {
		again = NewWindow(w.files)
		again.Start()
	})
	t.Cleanup(func() { mainthread.Wait(func() { again.Close() }) })
	wait(t, "the layout", func() bool {
		return len(again.split.Sizes()) == 2 && again.split.Sizes()[0] == 310 && again.right.Sizes()[0] == 220
	})
	mainthread.Wait(func() {
		if again.Width() != 900 || again.Height() != 640 {
			t.Errorf("size = %dx%d", again.Width(), again.Height())
		}
	})

	// The columns have the widths saved, in every folder: the sent folder,
	// which shows who messages are to, as much as the inbox.
	for _, folder := range []string{"INBOX", "Sent"} {
		wait(t, folder, func() bool { return len(again.engines) == 1 && again.account != "" })
		mainthread.Wait(func() { again.folders.selectFolder(again.account, folder) })
		wait(t, folder+" listed", func() bool { return again.folder == folder })
		mainthread.Wait(func() {
			if correspondent, date := again.list.columnWidths(); correspondent != 180 || date != 130 {
				t.Errorf("%s columns = %d, %d", folder, correspondent, date)
			}
		})
	}
}

func TestMoveAccounts(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	s.Append(t, "INBOX", "Kept")
	w := newTestWindow(t, s)
	wait(t, "the inbox", func() bool { return len(w.list.rows) == 1 })
	from := mainthread.Wait2(w.accountsDir)

	to := filepath.Join(t.TempDir(), "elsewhere")
	mainthread.Wait(func() {
		changed := w.config
		changed.DataDir = to
		w.applySettings(changed, nil, from, to, true)
	})
	wait(t, "the account to start again", func() bool { return len(w.engines) == 1 && mainthread.Wait2(w.accountsDir) == to })

	// The account and its mail are in the new folder, and not the old.
	ids, _ := filepath.Glob(filepath.Join(to, "*.json"))
	if len(ids) != 1 {
		t.Errorf("accounts in the new folder = %v", ids)
	}
	if _, err := os.Stat(from); !os.IsNotExist(err) {
		t.Errorf("the old folder is still there: %v", err)
	}
	if config := settings.Load(w.files.Settings); config.DataDir != to {
		t.Errorf("saved folder = %q", config.DataDir)
	}
	accounts, err := account.List(to, w.keys)
	if err != nil || len(accounts) != 1 {
		t.Errorf("accounts = %+v, %v", accounts, err)
	}
	// The inbox is shown again, from the moved copy.
	wait(t, "the inbox", func() bool { return len(w.list.rows) == 1 })
}

// TestSettingsFit checks that the settings dialog is tall enough for its
// notes, which wrap onto more lines in a larger font.
func TestSettingsFit(t *testing.T) {
	mainthread.Wait(func() {
		old := qt.QApplication_Font()
		big := qt.NewQFont5(old)
		big.SetPointSize(16)
		qt.QApplication_SetFont(big)
		defer qt.QApplication_SetFont(old)

		parent := qt.NewQWidget2()
		defer parent.DeleteLater()
		dialogs := map[string]*qt.QDialog{
			"settings": newSettingsDialog(parent, settings.Settings{}, "/home/user/.local/share/apemail/accounts", func(settings.Settings) {}),
			"account":  newAccountDialog(parent, account.New(), true, nil, func(account.Account) {}),
		}
		for name, d := range dialogs {
			d.Layout().Activate()
			if need := d.Layout().HeightForWidth(d.Width()); d.Height() < need {
				t.Errorf("%s: the dialog is %d high, and needs %d at a width of %d", name, d.Height(), need, d.Width())
			}
			// Each is wide enough not to be cramped.
			if want := map[string]int{"settings": settingsDialogWidth, "account": accountDialogWidth}[name]; d.Width() < want {
				t.Errorf("%s: the dialog is %d wide, not %d", name, d.Width(), want)
			}
			if need := d.MinimumSizeHint(); d.Height() < need.Height() || d.Width() < need.Width() {
				t.Errorf("%s: the dialog is %dx%d, and needs %dx%d", name, d.Width(), d.Height(), need.Width(), need.Height())
			}
			d.Reject()
		}
	})
}

func TestAbout(t *testing.T) {
	mainthread.Wait(func() {
		d := newAboutDialog(nil)
		defer d.DeleteLater()
		licences := licenceText()
		for _, want := range []string{qt.QLibraryInfo_Version().ToString(), "LGPLv3", "Qt WebEngine", "Chromium", "qtwebengine-licensing", "miqt", "go-imap", "go-smtp", "go-sasl", "go-message", "age", "x/crypto", "x/text"} {
			if !strings.Contains(licences, want) {
				t.Errorf("the licences do not mention %s", want)
			}
		}
		d.Layout().Activate()
		if need := d.Layout().HeightForWidth(d.Width()); d.Height() < need {
			t.Errorf("the about box is %d high, and needs %d", d.Height(), need)
		}
	})
}

func TestFolderTreeIsKept(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove), "Projects", "Projects/Engine", "Secret", "Secret/Plans")
	w := newTestWindow(t, s)
	item := func(w *Window, folder string) *qt.QTreeWidgetItem { return w.folders.folders[w.account][folder] }
	wait(t, "the folders", func() bool { return item(w, "Projects") != nil && item(w, "Secret") != nil })

	var before *qt.QTreeWidgetItem
	mainthread.Wait(func() {
		// Folders start closed, and accounts open.
		if item(w, "Projects").IsExpanded() || !w.folders.accounts[w.account].IsExpanded() {
			t.Error("the tree does not start as it should")
		}
		before = item(w, "Projects")
		before.SetExpanded(true)
		// Listing the folders again makes the tree again.
		w.engines[w.account].Refresh("")
	})
	wait(t, "the tree to be made again", func() bool { return item(w, "Projects") != nil && item(w, "Projects") != before })
	if !mainthread.Wait2(func() bool { return item(w, "Projects").IsExpanded() }) {
		t.Error("Projects was closed when the folders were listed again")
	}

	mainthread.Wait(func() { w.Close() })
	data, err := os.ReadFile(w.files.Settings)
	if err != nil {
		t.Fatal(err)
	}
	// The settings are not encrypted, so they do not name folders.
	if strings.Contains(string(data), "Projects") || strings.Contains(string(data), "Secret") {
		t.Errorf("the settings name folders: %s", data)
	}

	// A new window opens the tree as it was left.
	var again *Window
	mainthread.Wait(func() {
		again = NewWindow(w.files)
		again.Start()
	})
	t.Cleanup(func() { mainthread.Wait(func() { again.Close() }) })
	wait(t, "the folders", func() bool {
		return again.account != "" && item(again, "Projects") != nil && item(again, "Secret") != nil
	})
	mainthread.Wait(func() {
		if !item(again, "Projects").IsExpanded() {
			t.Error("Projects was not opened again")
		}
		if item(again, "Secret").IsExpanded() {
			t.Error("Secret was opened")
		}
	})
}

func TestStatusBar(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	w := newTestWindow(t, s)
	wait(t, "the account", func() bool { return len(w.engines) == 1 })
	mainthread.Wait(func() {
		bar := w.StatusBar()
		if !bar.IsSizeGripEnabled() {
			t.Error("the status bar has no grip to resize the window by")
		}
		// The status starts at the left, and takes the width of the bar.
		w.Status(w.account, "Checking Inbox…")
		if x := w.statusText.Geometry().X(); x > 20 {
			t.Errorf("the status starts %d from the left", x)
		}
		if w.statusText.Width() < bar.Width()/2 {
			t.Errorf("the status is %d wide, of %d", w.statusText.Width(), bar.Width())
		}
		if w.statusText.Alignment()&qt.AlignLeft == 0 {
			t.Error("the status is not left aligned")
		}
		// The text is not tight against the edge of the window.
		if left := w.statusText.Geometry().X() + w.statusText.ContentsMargins().Left(); left < statusPadding {
			t.Errorf("the status text is %d from the edge", left)
		}
	})
}

// dragBorder drags the right border of a column of the header by dx pixels,
// with the mouse.
func dragBorder(h *qt.QHeaderView, col, dx int) {
	vp := h.Viewport()
	x := h.SectionViewportPosition(col) + h.SectionSize(col) - 1
	y := float64(h.Height() / 2)
	mouse := func(typ qt.QEvent__Type, x int, button, buttons qt.MouseButton) {
		p := qt.NewQPointF3(float64(x), y)
		e := qt.NewQMouseEvent2(typ, p, vp.MapToGlobal(p), button, buttons, qt.NoModifier)
		defer e.Delete()
		qt.QCoreApplication_SendEvent(vp.QObject, e.QEvent)
	}
	mouse(qt.QEvent__MouseMove, x, qt.NoButton, qt.NoButton)
	mouse(qt.QEvent__MouseButtonPress, x, qt.LeftButton, qt.LeftButton)
	mouse(qt.QEvent__MouseMove, x+dx, qt.NoButton, qt.LeftButton)
	mouse(qt.QEvent__MouseButtonRelease, x+dx, qt.LeftButton, qt.NoButton)
}

// TestColumnBorders checks that dragging the border between two columns of
// the message list trades width between them, so that the date can be made
// wider by the border on its left, that the columns always fill the list,
// and that the widths dragged to are kept.
func TestColumnBorders(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	w := newTestWindow(t, s)
	wait(t, "the account", func() bool { return len(w.engines) == 1 })

	mainthread.Wait(func() {
		h := w.list.header
		widths := func() [3]int {
			return [3]int{h.SectionSize(colSubject), h.SectionSize(colCorrespondent), h.SectionSize(colDate)}
		}
		// The paperclips keep their width, and the others fill the rest.
		fills := func(when string) {
			if got := h.SectionSize(colAttachment); got != attachmentWidth {
				t.Errorf("%s: the paperclips are %d wide", when, got)
			}
			if got := widths(); attachmentWidth+got[0]+got[1]+got[2] != h.Width() {
				t.Errorf("%s: columns %v do not fill %d", when, got, h.Width())
			}
		}
		fills("at first")
		start := widths()

		// The border between From and Date, dragged left, widens the date.
		dragBorder(h, colCorrespondent, -60)
		if got := widths(); got != [3]int{start[0], start[1] - 60, start[2] + 60} {
			t.Errorf("from|date: %v, from %v", got, start)
		}
		fills("from|date")

		// The border between Subject and From trades between them.
		before := widths()
		dragBorder(h, colSubject, 50)
		if got := widths(); got != [3]int{before[0] + 50, before[1] - 50, before[2]} {
			t.Errorf("subject|from: %v, from %v", got, before)
		}
		fills("subject|from")

		// A column beside the border gives no more than leaves it at the
		// narrowest.
		before = widths()
		dragBorder(h, colCorrespondent, 1000)
		if got := widths(); got[1] != before[1]+before[2]-minColumnWidth || got[2] != minColumnWidth {
			t.Errorf("too far: %v, from %v", got, before)
		}
		fills("too far")
		dragBorder(h, colCorrespondent, -150)

		// Nor is a column dragged narrower than that.
		before = widths()
		dragBorder(h, colCorrespondent, -(before[1] - 10))
		if got := widths(); got[1] != minColumnWidth || got[2] != before[1]+before[2]-minColumnWidth {
			t.Errorf("too narrow: %v, from %v", got, before)
		}
		fills("too narrow")
		dragBorder(h, colCorrespondent, 150)
	})

	// The subject takes what the window gains or loses.
	var before [3]int
	mainthread.Wait(func() {
		h := w.list.header
		before = [3]int{h.Width(), h.SectionSize(colCorrespondent), h.SectionSize(colDate)}
		w.Resize(w.Width()+120, w.Height())
	})
	wait(t, "the list wider", func() bool {
		return mainthread.Wait2(func() int { return w.list.header.Width() }) != before[0]
	})
	var dragged [2]int
	mainthread.Wait(func() {
		h := w.list.header
		if got := attachmentWidth + h.SectionSize(colSubject) + h.SectionSize(colCorrespondent) + h.SectionSize(colDate); got != h.Width() {
			t.Errorf("after resizing, columns %d do not fill %d", got, h.Width())
		}
		dragged[0], dragged[1] = w.list.columnWidths()
		if dragged != [2]int{before[1], before[2]} {
			t.Errorf("after resizing, from and date %v, were %v", dragged, before[1:])
		}
		w.Close()
	})
	if config := settings.Load(w.files.Settings); config.CorrespondentWidth != dragged[0] || config.DateWidth != dragged[1] {
		t.Errorf("saved %d, %d; dragged to %d, %d", config.CorrespondentWidth, config.DateWidth, dragged[0], dragged[1])
	}
}
