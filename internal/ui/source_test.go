package ui

import (
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"
	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/mainthread"

	"github.com/nomad-software/apemail/internal/remote/remotetest"
)

// TestViewSource checks that Ctrl+U shows the message that is shown as the
// server has it, as text in a window of its own, which Escape closes.
func TestViewSource(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	s.AppendRaw(t, "INBOX", htmlMessage)
	w := newTestWindow(t, s)
	wait(t, "the inbox", func() bool { return len(w.list.rows) == 1 })

	mainthread.Wait(func() {
		if w.viewSource.IsEnabled() {
			t.Error("view source is enabled with no message shown")
		}
		if got := w.viewSource.Shortcut().ToString(); got != "Ctrl+U" {
			t.Errorf("shortcut = %q", got)
		}
	})
	showFirst(t, w)
	mainthread.Wait(func() {
		if !w.viewSource.IsEnabled() {
			t.Fatal("view source is not enabled with a message shown")
		}
		w.viewSource.Trigger()
	})
	wait(t, "the source", func() bool { return len(sources) == 1 })

	mainthread.Wait(func() {
		var src *sourceWindow
		for src = range sources {
		}
		text := src.ToPlainText()
		// The message is shown as it is, markup and all, not rendered.
		want := strings.ReplaceAll(htmlMessage, "\r\n", "\n")
		if strings.TrimSpace(text) != strings.TrimSpace(want) {
			t.Errorf("source = %q, want %q", text, want)
		}
		if !src.IsReadOnly() || !strings.HasPrefix(src.WindowTitle(), "Source of ") || !src.IsWindow() {
			t.Errorf("read only %v, title %q, window %v", src.IsReadOnly(), src.WindowTitle(), src.IsWindow())
		}
		// It belongs to the main window, which closes it.
		if src.ParentWidget() == nil || src.ParentWidget().UnsafePointer() != w.QWidget.UnsafePointer() {
			t.Error("the source does not belong to the main window")
		}
		var escape *qt.QAction
		for _, a := range src.QWidget.Actions() {
			if a.Shortcut().ToString() == "Esc" {
				escape = a
			}
		}
		if escape == nil {
			t.Fatal("no action has Escape")
		}
		escape.Trigger()
	})
	wait(t, "the source closed", func() bool { return len(sources) == 0 })
}

// TestSourceNotUTF8 checks that a message in another charset is shown,
// with what is not UTF-8 replaced rather than lost.
func TestSourceNotUTF8(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	w := newTestWindow(t, s)
	wait(t, "the account", func() bool { return len(w.engines) == 1 })
	mainthread.Wait(func() {
		src := w.showSource("Grüße", []byte("Subject: Gr\xfc\xdfe\r\n\r\nBody\r\n"))
		defer src.Close()
		if got := src.ToPlainText(); got != "Subject: Gr\uFFFDe\n\nBody\n" {
			t.Errorf("source = %q", got)
		}
	})
}
