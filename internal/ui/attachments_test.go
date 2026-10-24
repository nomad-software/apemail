package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/mainthread"

	"github.com/nomad-software/apemail/internal/message"
)

// TestAttachmentsWrap checks that the attachments of a message with many of
// them are shown on as many rows as they need, within the width of the
// message pane, rather than widening it.
func TestAttachmentsWrap(t *testing.T) {
	var files []message.Attachment
	for i := range 40 {
		files = append(files, message.Attachment{Filename: fmt.Sprintf("attachment number %d.pdf", i), Data: []byte("x")})
	}
	// One name far wider than the pane is cut down to it.
	files = append(files, message.Attachment{Filename: strings.Repeat("long", 200) + ".pdf"})
	mainthread.Wait(func() {
		v := newMessageView()
		defer v.DeleteLater()
		// They ask for no more width than one does.
		v.setAttachments(files[:1])
		one := v.MinimumSizeHint().Width()
		v.setAttachments(files)
		if w := v.MinimumSizeHint().Width(); w > one+200 {
			t.Errorf("the attachments ask for a width of %d, and one for %d", w, one)
		}
		v.Resize(600, 400)
		v.Show()
		qt.QCoreApplication_ProcessEvents()
		if w := v.Width(); w != 600 {
			t.Errorf("the pane is %d wide", w)
		}

		// The rows are within the attachments, which are as wide as the
		// pane less its scroll bar.
		inside := v.attachments.Rect()
		defer runtime.KeepAlive(inside)
		rows := map[int]bool{}
		for i := range v.attachBox.Count() {
			g := v.attachBox.ItemAt(i).Geometry()
			if !inside.Contains(g) {
				t.Errorf("item %d is at %d,%d %dx%d, outside the %dx%d of the attachments",
					i, g.X(), g.Y(), g.Width(), g.Height(), inside.Width(), inside.Height())
			}
			rows[g.Y()] = true
			runtime.KeepAlive(g)
		}
		if len(rows) < 5 {
			t.Errorf("the attachments are on %d rows", len(rows))
		}
		// They scroll once they take more than attachRows rows.
		if got, want := v.attachScroll.Height(), v.attachBox.heightOfRows(attachRows); got != want {
			t.Errorf("the attachments are %d high, not %d", got, want)
		}

		// One takes a row, which is no taller than it needs.
		v.setAttachments(files[:1])
		qt.QCoreApplication_ProcessEvents()
		if got, want := v.attachScroll.Height(), v.attachBox.heightOfRows(1); got != want {
			t.Errorf("one attachment is %d high, not %d", got, want)
		}
	})
}

// attachmentButtons returns the buttons of the attachments shown.
func attachmentButtons(v *messageView) []*qt.QWidget {
	var buttons []*qt.QWidget
	for i := range v.attachBox.Count() {
		buttons = append(buttons, v.attachBox.ItemAt(i).Widget())
	}
	return buttons
}

// lightness returns the mean lightness of what the widget draws.
func lightness(w *qt.QWidget) int {
	pixmap := w.Grab()
	defer runtime.KeepAlive(pixmap)
	img := pixmap.ToImage()
	defer runtime.KeepAlive(img)
	total := 0
	for y := range img.Height() {
		for x := range img.Width() {
			c := img.PixelColor(x, y)
			total += c.Lightness()
			runtime.KeepAlive(c)
		}
	}
	return total / (img.Width() * img.Height())
}

// moveMouse moves the mouse to the point of the window, as the desktop does,
// so that the widget under it is told that it is.
func moveMouse(window *qt.QWidget, to *qt.QPointF) {
	defer runtime.KeepAlive(to)
	global := window.MapToGlobal(to)
	defer runtime.KeepAlive(global)
	e := qt.NewQMouseEvent2(qt.QEvent__MouseMove, to, global, qt.NoButton, qt.NoButton, qt.NoModifier)
	defer e.Delete()
	qt.QCoreApplication_SendEvent(window.WindowHandle().QObject, e.QEvent)
}

// TestAttachmentButtons checks that the button of an attachment has no tool
// tip, darkens while the mouse is over it, in a light theme, and that Save
// All is shown only for more than one.
func TestAttachmentButtons(t *testing.T) {
	files := []message.Attachment{{Filename: "a.pdf", Data: []byte("a")}, {Filename: "b.pdf", Data: []byte("b")}}
	mainthread.Wait(func() {
		applyTheme("light")
		defer applyTheme("")
		v := newMessageView()
		defer v.DeleteLater()
		v.Resize(600, 400)
		v.Show()
		v.setAttachments(files)
		qt.QCoreApplication_ProcessEvents()
		// The buttons are all there is: no label counts them.
		buttons := attachmentButtons(v)
		if len(buttons) != 2 {
			t.Fatalf("%d widgets", len(buttons))
		}
		for _, b := range buttons {
			if tip := b.ToolTip(); tip != "" {
				t.Errorf("tool tip %q", tip)
			}
		}
		b := buttons[0]
		before := lightness(b)
		middle := qt.NewQPointF3(float64(b.Width()/2), float64(b.Height()/2))
		moveMouse(v.QWidget, b.MapTo(v.QWidget, middle))
		middle.Delete()
		hovered := lightness(b)
		if hovered >= before {
			t.Errorf("hovered, the button is %d light, and %d before", hovered, before)
		}
		if !v.saveAll.IsVisible() {
			t.Error("Save All is not shown for two")
		}
		v.setAttachments(files[:1])
		if v.saveAll.IsVisible() {
			t.Error("Save All is shown for one")
		}
	})
}

// TestSaveAllAttachments checks that Save All saves every attachment in the
// folder chosen, never over a file there or one another saved, nor outside
// it, and saves nothing if no folder is chosen.
func TestSaveAllAttachments(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.pdf"), []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	chosen := ""
	old := chooseFolder
	chooseFolder = func(_ *qt.QWidget, _, _ string) string { return chosen }
	t.Cleanup(func() { chooseFolder = old })
	files := []message.Attachment{
		{Filename: "a.pdf", Data: []byte("first")},
		{Filename: "a.pdf", Data: []byte("second")},
		{Filename: "../../notes.txt", Data: []byte("notes")},
		{Filename: "", Data: []byte("nameless")},
	}
	mainthread.Wait(func() {
		v := newMessageView()
		defer v.DeleteLater()
		v.setAttachments(files)

		// None chosen: nothing is saved.
		v.saveAll.Click()
		if entries, _ := os.ReadDir(dir); len(entries) != 1 {
			t.Errorf("with no folder chosen, %d files", len(entries))
		}

		chosen = dir
		v.saveAll.Click()
	})
	want := map[string]string{
		"a.pdf":      "mine",
		"a (2).pdf":  "first",
		"a (3).pdf":  "second",
		"notes.txt":  "notes",
		"attachment": "nameless",
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
		data, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		if string(data) != want[e.Name()] {
			t.Errorf("%s holds %q", e.Name(), data)
		}
		if info, _ := e.Info(); info.Mode().Perm() != 0o600 {
			t.Errorf("%s is %v", e.Name(), info.Mode().Perm())
		}
	}
	if len(names) != len(want) {
		t.Errorf("saved %v", names)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(filepath.Dir(dir)), "notes.txt")); err == nil {
		t.Error("an attachment was saved outside the folder")
	}

	// A folder that cannot be written to is warned of.
	forgetWarnings()
	defer forgetWarnings()
	chosen = filepath.Join(dir, "missing")
	mainthread.Wait(func() {
		v := newMessageView()
		defer v.DeleteLater()
		v.setAttachments(files)
		v.saveAll.Click()
	})
	if got := warnings(); !strings.Contains(got, "Save All Attachments") {
		t.Errorf("warnings = %q", got)
	}
}

// headerRows returns the rows of the headers shown, by name.
func headerRows(v *messageView) map[string]string {
	rows := map[string]string{}
	grid := v.headerGrid
	for row := 1; row < grid.RowCount(); row++ {
		name, value := grid.ItemAtPosition(row, 0), grid.ItemAtPosition(row, 1)
		if name == nil || name.Widget() == nil || value == nil || value.Layout() == nil {
			continue
		}
		label := qt.UnsafeNewQLabel(name.Widget().UnsafePointer())
		text := qt.UnsafeNewQLabel(value.Layout().ItemAt(0).Widget().UnsafePointer())
		rows[label.Text()] = text.Text()
	}
	return rows
}

// TestAttachmentsRow checks that the headers of a message with attachments
// say how many it has, and those of one without have no such row.
func TestAttachmentsRow(t *testing.T) {
	with, err := message.Parse([]byte("From: a@example.com\r\nSubject: s\r\nContent-Type: multipart/mixed; boundary=b\r\n\r\n" +
		"--b\r\nContent-Type: text/plain\r\n\r\nHello.\r\n" +
		strings.Repeat("--b\r\nContent-Type: application/pdf\r\nContent-Disposition: attachment; filename=a.pdf\r\n\r\n%PDF\r\n", 5) +
		"--b--\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	without, err := message.Parse([]byte("From: a@example.com\r\nSubject: s\r\n\r\nHello.\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	mainthread.Wait(func() {
		v := newMessageView()
		defer v.DeleteLater()
		v.show(with, "INBOX", 1)
		rows := headerRows(v)
		var count string
		for name, value := range rows {
			if strings.Contains(name, "Attachments") {
				count = value
			}
		}
		if count != "5" {
			t.Errorf("headers %v", rows)
		}
		v.show(without, "INBOX", 2)
		for name := range headerRows(v) {
			if strings.Contains(name, "Attachments") {
				t.Errorf("a message without attachments has the row %q", name)
			}
		}
	})
}

// TestSaveAttachment checks that clicking an attachment saves it where the
// user chooses, offering its own name, and saves nothing if they choose
// nowhere.
func TestSaveAttachment(t *testing.T) {
	dir := t.TempDir()
	chosen, offered := "", ""
	old := chooseFile
	chooseFile = func(_ *qt.QWidget, _, start string) string {
		offered = start
		return chosen
	}
	t.Cleanup(func() { chooseFile = old })
	mainthread.Wait(func() {
		v := newMessageView()
		defer v.DeleteLater()
		v.setAttachments([]message.Attachment{{Filename: "../report.pdf", Data: []byte("report")}})
		button := qt.UnsafeNewQPushButton(attachmentButtons(v)[0].UnsafePointer())

		button.Click()
		if entries, _ := os.ReadDir(dir); len(entries) != 0 || filepath.Base(offered) != "report.pdf" {
			t.Errorf("with nowhere chosen, %d files; offered %q", len(entries), offered)
		}

		chosen = filepath.Join(dir, "saved.pdf")
		button.Click()
	})
	if data, err := os.ReadFile(filepath.Join(dir, "saved.pdf")); err != nil || string(data) != "report" {
		t.Errorf("saved %q, %v", data, err)
	}
}
