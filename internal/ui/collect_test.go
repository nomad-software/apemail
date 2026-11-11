package ui

import (
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/nomad-software/qute/qtwidgets"

	"github.com/nomad-software/apemail/internal/store"
)

// collecting runs the garbage collector all the while until the test ends,
// so that a Qt value Go made, were it freed while Qt still used it, would be
// freed at once, and crash, rather than once in a long while.
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
func drawn(w qtwidgets.QWidgetLike) bool {
	return !w.AsQWidget().Grab().IsNull()
}

// TestListWhileCollecting draws the message list over and over while the
// collector runs: every cell and heading it draws is a QVariant returned to
// Qt, which Qt copies once the model's data has returned.
func TestListWhileCollecting(t *testing.T) {
	ix := &store.Index{Folder: "INBOX", UIDValidity: 1}
	for i := range 40 {
		s := store.Summary{UID: uint32(i + 1), Subject: fmt.Sprintf("Message %d", i), From: "Alice <alice@example.com>", Date: time.Now()}
		// The paperclips are drawn too.
		s.Attachments = i%2 == 0
		switch i % 3 {
		case 0:
			s.Flags = []string{`\Seen`}
		case 1:
			s.Flags = []string{`\Flagged`}
		}
		ix.Put(s)
	}
	collecting(t)
	withList(t, func(l *messageList) {
		l.setIndex(ix, false)
		for range 150 {
			if !drawn(l) {
				t.Fatal("the list was not drawn")
			}
		}
	})
}
