package ui

import (
	"fmt"
	"runtime"
	"testing"
	"time"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/mainthread"

	"github.com/nomad-software/apemail/internal/message"
	"github.com/nomad-software/apemail/internal/store"
)

// collecting runs the garbage collector all the while until the test ends,
// so that a Qt value miqt frees itself which is still in use is freed while
// it is, and crashes, rather than once in a long while.
func collecting(t *testing.T) {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				runtime.GC()
			}
		}
	}()
	t.Cleanup(func() {
		close(stop)
		<-done
	})
}

// drawn draws the widget, and reports if it was.
func drawn(w *qt.QWidget) bool {
	pixmap := w.Grab()
	defer runtime.KeepAlive(pixmap)
	return !pixmap.IsNull()
}

// TestListWhileCollecting draws the message list over and over while the
// collector runs: every cell and heading it draws is a QVariant returned to
// Qt, which Qt copies once the model's data has returned.
func TestListWhileCollecting(t *testing.T) {
	ix := &store.Index{Folder: "INBOX", UIDValidity: 1}
	for i := range 40 {
		s := store.Summary{UID: uint32(i + 1), Subject: fmt.Sprintf("Message %d", i), From: "Alice <alice@example.com>", Date: time.Now()}
		switch i % 3 {
		case 0:
			s.Flags = []string{`\Seen`}
		case 1:
			s.Flags = []string{`\Flagged`}
		}
		ix.Put(s)
	}
	collecting(t)
	withList(func(l *messageList) {
		l.setIndex(ix, false)
		for range 150 {
			if !drawn(l.QWidget) {
				t.Fatal("the list was not drawn")
			}
		}
	})
}

// TestImagesWhileCollecting shows an HTML message with an image in it over
// and over while the collector runs: the image is a QVariant returned to Qt
// by the browser's loadResource.
func TestImagesWhileCollecting(t *testing.T) {
	m := &message.Message{
		HTML:        `<p>A picture:</p><img src="cid:pic">`,
		Attachments: []message.Attachment{{Filename: "pic.png", ContentType: "image/png", ContentID: "pic", Inline: true, Data: pngImage(t, 40)}},
	}
	collecting(t)
	mainthread.Wait(func() {
		v := newMessageView()
		defer v.DeleteLater()
		v.Resize(500, 400)
		for range 150 {
			v.show(m, "INBOX", 1)
			if !drawn(v.browser.QWidget) {
				t.Fatal("the message was not drawn")
			}
		}
	})
}
