package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomad-software/qute/qtcore"
	"github.com/nomad-software/qute/qtgui"
	"github.com/nomad-software/qute/qtwidgets"
	"github.com/nomad-software/qute/qutetest"

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
	qutetest.Run(t, func() {
		v := newMessageView()
		defer v.DeleteLater()
		// They ask for no more width than one does.
		v.setAttachments(files[:1])
		one := v.MinimumSizeHint().Width
		v.setAttachments(files)
		if w := v.MinimumSizeHint().Width; w > one+200 {
			t.Errorf("the attachments ask for a width of %d, and one for %d", w, one)
		}
		v.Resize(600, 400)
		v.Show()
		qtcore.QCoreApplicationProcessEvents()
		if w := v.Width(); w != 600 {
			t.Errorf("the pane is %d wide", w)
		}

		// The rows are within the attachments, which are as wide as the
		// pane less its scroll bar.
		inside := v.attachments.Rect()
		rows := map[int]bool{}
		for i := range v.attachBox.Count() {
			g := v.attachBox.ItemAt(i).Geometry()
			if !inside.Contains(g.TopLeft()) || !inside.Contains(qtcore.QPoint{X: g.Right(), Y: g.Bottom()}) {
				t.Errorf("item %d is at %d,%d %dx%d, outside the %dx%d of the attachments",
					i, g.X, g.Y, g.Width, g.Height, inside.Width, inside.Height)
			}
			rows[g.Y] = true
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
		qtcore.QCoreApplicationProcessEvents()
		if got, want := v.attachScroll.Height(), v.attachBox.heightOfRows(1); got != want {
			t.Errorf("one attachment is %d high, not %d", got, want)
		}
	})
}

// attachmentButtons returns the buttons of the attachments shown.
func attachmentButtons(v *messageView) []*qtwidgets.QWidget {
	var buttons []*qtwidgets.QWidget
	for i := range v.attachBox.Count() {
		buttons = append(buttons, v.attachBox.ItemAt(i).Widget())
	}
	return buttons
}

// lightness returns the mean lightness of what the widget draws.
func lightness(w qtwidgets.QWidgetLike) int {
	img := w.AsQWidget().Grab().ToImage()
	total := 0
	for y := range img.Height() {
		for x := range img.Width() {
			total += lightnessOf(img.PixelColor(x, y))
		}
	}
	return total / (img.Width() * img.Height())
}

// moveMouse moves the mouse to the point of the window, as the desktop does,
// so that the widget under it is told that it is.
func moveMouse(window qtwidgets.QWidgetLike, to qtcore.QPoint) {
	w := window.AsQWidget()
	at := to.ToF()
	e := qtgui.NewQMouseEvent(qtcore.QEventMouseMove, at, w.MapToGlobal(to).ToF(), qtcore.NoButton, qtcore.NoButton, qtcore.NoModifier)
	qtcore.QCoreApplicationSendEvent(w.WindowHandle(), e)
}

// TestAttachmentButtons checks that the button of an attachment has no tool
// tip, darkens while the mouse is over it, in a light theme, and that Save
// All is shown only for more than one.
func TestAttachmentButtons(t *testing.T) {
	files := []message.Attachment{{Filename: "a.pdf", Data: []byte("a")}, {Filename: "b.pdf", Data: []byte("b")}}
	qutetest.Run(t, func() {
		applyTheme("light")
		defer applyTheme("")
		v := newMessageView()
		defer v.DeleteLater()
		v.Resize(600, 400)
		v.Show()
		v.setAttachments(files)
		qtcore.QCoreApplicationProcessEvents()
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
		moveMouse(v, b.MapTo(v, qtcore.QPoint{X: b.Width() / 2, Y: b.Height() / 2}))
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
	chooseFolder = func(_ qtwidgets.QWidgetLike, _, _ string) string { return chosen }
	t.Cleanup(func() { chooseFolder = old })
	files := []message.Attachment{
		{Filename: "a.pdf", Data: []byte("first")},
		{Filename: "a.pdf", Data: []byte("second")},
		{Filename: "../../notes.txt", Data: []byte("notes")},
		{Filename: "", Data: []byte("nameless")},
	}
	qutetest.Run(t, func() {
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
	qutetest.Run(t, func() {
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
		label := qtwidgets.QLabelCast(name.Widget())
		text := qtwidgets.QLabelCast(value.Layout().ItemAt(0).Widget())
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
	qutetest.Run(t, func() {
		v := newMessageView()
		defer v.DeleteLater()
		v.show(with, "INBOX", 1, 1)
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
		v.show(without, "INBOX", 1, 2)
		for name := range headerRows(v) {
			if strings.Contains(name, "Attachments") {
				t.Errorf("a message without attachments has the row %q", name)
			}
		}
	})
}

// TestAttachmentsPaneHeight checks that the pane of attachments is only as
// tall as its rows, however tall the view, and that the body has the rest.
func TestAttachmentsPaneHeight(t *testing.T) {
	m, err := message.Parse([]byte("From: a@example.com\r\nSubject: s\r\nContent-Type: multipart/mixed; boundary=b\r\n\r\n" +
		"--b\r\nContent-Type: text/plain\r\n\r\nHello.\r\n" +
		"--b\r\nContent-Type: application/pdf\r\nContent-Disposition: attachment; filename=a.pdf\r\n\r\n%PDF\r\n" +
		"--b--\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	qutetest.Run(t, func() {
		v := newMessageView()
		defer v.DeleteLater()
		v.show(m, "INBOX", 1, 1)
		policy := v.attachPane.SizePolicy()
		if got := policy.VerticalPolicy(); got != qtwidgets.QSizePolicyFixed {
			t.Errorf("the pane's height can be %v", got)
		}
		for _, height := range []int{400, 800, 300} {
			v.Resize(800, height)
			v.Show()
			qtcore.QCoreApplicationProcessEvents()
			hint := v.attachPane.SizeHint()
			if got, want := v.attachPane.Height(), hint.Height; got != want {
				t.Errorf("in a view %d tall the pane is %d, not %d", height, got, want)
			}
			if got, want := v.web.Height(), height-v.headers.Height()-v.attachPane.Height(); got != want {
				t.Errorf("in a view %d tall the body is %d, not %d", height, got, want)
			}
		}
		v.Hide()
	})
}

// TestSaveAttachment checks that clicking an attachment saves it where the
// user chooses, offering its own name, and saves nothing if they choose
// nowhere.
func TestSaveAttachment(t *testing.T) {
	dir := t.TempDir()
	chosen, offered := "", ""
	old := chooseFile
	chooseFile = func(_ qtwidgets.QWidgetLike, _, start string) string {
		offered = start
		return chosen
	}
	t.Cleanup(func() { chooseFile = old })
	qutetest.Run(t, func() {
		v := newMessageView()
		defer v.DeleteLater()
		v.setAttachments([]message.Attachment{{Filename: "../report.pdf", Data: []byte("report")}})
		button := qtwidgets.QPushButtonCast(attachmentButtons(v)[0])

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
