package ui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-message/mail"
	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/mainthread"

	"github.com/nomad-software/apemail/internal/account"
	"github.com/nomad-software/apemail/internal/message"
	"github.com/nomad-software/apemail/internal/remote/remotetest"
)

// imageServer is a web server of red images, which counts what it is asked
// for, by path.
type imageServer struct {
	*httptest.Server
	mu    sync.Mutex
	asked map[string]int
	// hold, if it is not nil, is waited on before an image is served.
	hold chan struct{}
}

// redImage returns a red PNG image, 60 by 40 pixels.
func redImage(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 60, 40))
	for y := range 40 {
		for x := range 60 {
			img.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// newImageServer starts a web server of red images, 60 by 40 pixels, at
// any path that ends in .png, which is stopped when the test ends.
func newImageServer(t *testing.T) *imageServer {
	red := redImage(t)
	s := &imageServer{asked: map[string]int{}}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.asked[r.URL.Path]++
		hold := s.hold
		s.mu.Unlock()
		if hold != nil {
			select {
			case <-hold:
			case <-r.Context().Done():
				return
			}
		}
		if !strings.HasSuffix(r.URL.Path, ".png") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(red)
	}))
	t.Cleanup(s.Close)
	return s
}

// count returns how many times the path was asked for, or every path if it
// is empty.
func (s *imageServer) count(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if path != "" {
		return s.asked[path]
	}
	n := 0
	for _, c := range s.asked {
		n += c
	}
	return n
}

// imageMessage returns a message in HTML that shows the images of the
// server at the paths, 60 by 40 pixels, and links a stylesheet from it too.
func imageMessage(s *imageServer, paths ...string) *message.Message {
	var b strings.Builder
	b.WriteString(`<html><head><link rel="stylesheet" href="` + s.URL + `/style.css"></head><body><p>Hello</p>`)
	for _, p := range paths {
		b.WriteString(`<img src="` + s.URL + p + `" width="60" height="40">`)
	}
	b.WriteString(`</body></html>`)
	return &message.Message{Subject: "Pictures", HTML: b.String()}
}

// buttonShown waits for the button that loads images to be shown.
func buttonShown(t *testing.T, v *messageView) {
	t.Helper()
	wait(t, "the button to load images", func() bool { return v.loadImages != nil && v.loadImages.IsVisibleTo(v.QWidget) })
}

// TestLoadRemoteImages checks that the images a message links to, those of
// its backgrounds too, are not loaded until the button above it is pressed,
// and are then shown, at the size the message gives them, and that nothing
// but images is loaded.
func TestLoadRemoteImages(t *testing.T) {
	s := newImageServer(t)
	m := imageMessage(s, "/a.png", "/b.png")
	m.HTML = strings.Replace(m.HTML, "<p>Hello</p>", `<div style="background: url(`+s.URL+`/background.png); width: 60px; height: 40px"></div>`, 1)
	v := newShownView(t, 600, 400)
	showWhole(t, v, m)
	buttonShown(t, v)
	mainthread.Wait(func() {
		if !v.loadImages.IsEnabled() {
			t.Error("the button is not enabled")
		}
		if n := redPixels(v); n != 0 {
			t.Errorf("%d red pixels are shown before the images are loaded", n)
		}
	})
	if n := s.count(""); n != 0 {
		t.Fatalf("%d things were fetched before the button was pressed", n)
	}

	mainthread.Wait(func() { v.loadImages.Click() })
	// The three are drawn whole, 60 by 40.
	wait(t, "the images", func() bool { return redPixels(v) >= 3*60*40 })
	mainthread.Wait(func() {
		if v.loadImages.IsVisibleTo(v.QWidget) {
			t.Error("the button is still shown")
		}
	})
	if s.count("/a.png") != 1 || s.count("/b.png") != 1 || s.count("/background.png") != 1 {
		t.Errorf("asked for %v", s.asked)
	}
	// The stylesheet, which is not an image, is not fetched.
	if s.count("/style.css") != 0 {
		t.Errorf("asked for %v", s.asked)
	}
}

// TestRemoteImagesNotLoaded checks that the button stays to try again an
// image that cannot be loaded, saying how many could not be.
func TestRemoteImagesNotLoaded(t *testing.T) {
	s := newImageServer(t)
	v := newShownView(t, 600, 400)
	showWhole(t, v, imageMessage(s, "/a.png", "/missing.gif"))
	buttonShown(t, v)
	mainthread.Wait(func() { v.loadImages.Click() })
	wait(t, "the images", func() bool { return v.stopImages == nil && v.loadImages.IsEnabled() })
	mainthread.Wait(func() {
		if len(v.images) != 1 || !v.loadImages.IsVisibleTo(v.QWidget) || !strings.Contains(v.loadImages.ToolTip(), "1 of 2") {
			t.Errorf("loaded %d, the button says %q", len(v.images), v.loadImages.ToolTip())
		}
		v.loadImages.Click()
	})
	wait(t, "the images again", func() bool { return s.count("/missing.gif") == 2 })
}

// TestNoRemoteImagesButton checks that a message with no images on the
// internet has no button to load them.
func TestNoRemoteImagesButton(t *testing.T) {
	v := newShownView(t, 600, 400)
	for _, m := range []*message.Message{
		{Text: "https://example.com/a.png"},
		{HTML: `<p>Hello <img src="cid:a"> <img src="data:image/png;base64,AA=="> <img src="file:///etc/a.png"></p>`},
	} {
		showWhole(t, v, m)
		time.Sleep(200 * time.Millisecond)
		mainthread.Wait(func() {
			if v.loadImages != nil && v.loadImages.IsVisibleTo(v.QWidget) {
				t.Errorf("%+v has a button to load images", m)
			}
		})
	}
}

// TestLoadRemoteImagesAlways checks that the images of a message are loaded
// when it is shown if the account says to, and only then, and that the
// button to load them is not shown while they load, and afterwards only if
// some could not be, to try them again.
func TestLoadRemoteImagesAlways(t *testing.T) {
	for _, c := range []struct {
		always bool
		paths  []string
		button bool
	}{
		{true, []string{"/a.png"}, false},
		{true, []string{"/a.png", "/gone.txt"}, true},
		{false, []string{"/a.png"}, true},
	} {
		images := newImageServer(t)
		hold := make(chan struct{})
		images.mu.Lock()
		images.hold = hold
		images.mu.Unlock()
		s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
		s.AppendRaw(t, "INBOX", "From: a@example.com\r\nSubject: Pictures\r\nContent-Type: text/html; charset=utf-8\r\n\r\n"+
			imageMessage(images, c.paths...).HTML+"\r\n", imap.FlagSeen)
		w := newTestWindow(t, s, func(a *account.Account) { a.LoadRemoteImages = c.always })
		// The message asks for its images only where it is seen.
		mainthread.Wait(w.Show)
		wait(t, "the inbox", func() bool { return w.folder != "" && w.list.model.RowCount(noParent) == 1 })
		mainthread.Wait(func() { clickRow(w.list, 0) })
		wait(t, "the message", func() bool { return w.view.msg != nil })
		if c.always {
			wait(t, "the images to be loaded", func() bool { return w.view.stopImages != nil })
			mainthread.Wait(func() {
				if w.view.loadImages.IsVisibleTo(w.view.QWidget) {
					t.Errorf("%v: the button is shown while the images load", c.paths)
				}
			})
			close(hold)
			wait(t, "the images", func() bool { return w.view.stopImages == nil && len(w.view.images) == 1 })
			mainthread.Wait(func() {
				if shown := w.view.loadImages.IsVisibleTo(w.view.QWidget); shown != c.button {
					t.Errorf("%v: the button is shown %v once they have loaded, want %v", c.paths, shown, c.button)
				}
			})
			continue
		}
		close(hold)
		buttonShown(t, w.view)
		mainthread.Wait(func() {
			if w.view.stopImages != nil || !w.view.loadImages.IsEnabled() {
				t.Error("the images are being loaded")
			}
		})
		if n := images.count(""); n != 0 {
			t.Errorf("%d things were fetched", n)
		}
	}
}

// findCheckBox returns the check box within the widget with the text.
func findCheckBox(t *testing.T, within *qt.QWidget, text string) *qt.QCheckBox {
	t.Helper()
	for _, w := range qt.QApplication_AllWidgets() {
		if w.Inherits("QCheckBox") && within.IsAncestorOf(w) {
			if b := qt.UnsafeNewQCheckBox(w.UnsafePointer()); b.Text() == text {
				return b
			}
		}
	}
	t.Fatalf("there is no %q check box", text)
	return nil
}

// TestRemoteImagesSetting checks that the account dialog shows the setting
// to load remote images always, and saves it as it is left.
func TestRemoteImagesSetting(t *testing.T) {
	for _, before := range []bool{false, true} {
		a := account.New()
		a.Name, a.Email = "Ada", "ada@example.com"
		a.Incoming = account.Server{Host: "imap.example.com", Port: 993, Security: account.SecurityTLS, Auth: account.AuthPassword, Username: "u", Password: "p"}
		a.Outgoing = account.Server{Host: "smtp.example.com", Port: 465, Security: account.SecurityTLS, Auth: account.AuthPassword, Username: "u", Password: "p"}
		a.LoadRemoteImages = before
		var saved *account.Account
		mainthread.Wait(func() {
			parent := qt.NewQWidget2()
			defer parent.Delete()
			d := newAccountDialog(parent, a, false, nil, func(changed account.Account) { saved = &changed })
			box := findCheckBox(t, d.QWidget, "Load images from the internet when a message is shown")
			if box.IsChecked() != before {
				t.Errorf("the setting is shown as %v, not %v", box.IsChecked(), before)
			}
			box.SetChecked(!before)
			findButton(t, d.QWidget, "OK").Click()
		})
		if saved == nil || saved.LoadRemoteImages != !before {
			t.Errorf("saved %+v", saved)
		}
	}
}

// TestLoadImagesButtonPlace checks that the button to load images is at the
// bottom right of the headers.
func TestLoadImagesButtonPlace(t *testing.T) {
	s := newImageServer(t)
	v := newShownView(t, 800, 300)
	mainthread.Wait(func() {
		m := imageMessage(s, "/a.png")
		m.From = []*mail.Address{{Name: "Ada", Address: "ada@example.com"}}
		m.To = []*mail.Address{{Address: "grace@example.com"}}
		m.Date = time.Date(2026, 10, 8, 8, 15, 0, 0, time.UTC)
		v.show(m, "INBOX", 1)
	})
	buttonShown(t, v)
	mainthread.Wait(func() {
		headers, button := v.headers.Geometry(), v.loadImages.Geometry()
		defer runtime.KeepAlive(headers)
		defer runtime.KeepAlive(button)
		// Within the margins of the headers, 8 pixels across and 6 down.
		if gap := headers.Height() - (button.Y() + button.Height()); gap < 0 || gap > 8 {
			t.Errorf("the button is %d pixels above the bottom of the headers", gap)
		}
		if gap := headers.Width() - (button.X() + button.Width()); gap < 0 || gap > 10 {
			t.Errorf("the button is %d pixels from the right of the headers", gap)
		}
		if button.Y() < headers.Height()/2 {
			t.Errorf("the button is at %d of %d", button.Y(), headers.Height())
		}
	})
}
