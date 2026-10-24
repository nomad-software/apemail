package ui

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/mappu/miqt/qt6/mainthread"

	"github.com/nomad-software/apemail/internal/message"
)

// TestViewInBrowser checks that an HTML message has a button beside the one
// that loads its images which opens it in the web browser, served as the
// message has it rather than as it was made fit for Qt, with the images it
// carries; that a message in plain text has none; and that the message is
// served no more once the view is destroyed.
func TestViewInBrowser(t *testing.T) {
	var opened []string
	open := openURL
	openURL = func(link string) { opened = append(opened, link) }
	t.Cleanup(func() { openURL = open })

	html := `<div style="display:none">Preview</div><p>Hello <img src="cid:logo@example.com"> <img src="https://example.com/a.png"></p>`
	raw := "From: a@example.com\r\nSubject: Hello\r\nContent-Type: multipart/related; boundary=b\r\n\r\n" +
		"--b\r\nContent-Type: text/html; charset=utf-8\r\n\r\n" + html + "\r\n" +
		"--b\r\nContent-Type: image/png\r\nContent-ID: <logo@example.com>\r\nContent-Disposition: inline\r\n\r\nPNG\r\n--b--\r\n"
	m, err := message.Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	var v *messageView
	deleted := false
	t.Cleanup(func() {
		if !deleted {
			mainthread.Wait(func() { v.DeleteLater() })
		}
	})
	mainthread.Wait(func() {
		v = newMessageView()
		v.Resize(800, 600)
		v.Show()
		v.show(m, "INBOX", 1)
		if v.inBrowser == nil || !v.inBrowser.IsVisibleTo(v.QWidget) || v.loadImages == nil {
			t.Fatal("there is no button to view the message in the browser")
		}
		images, browser := v.loadImages.Geometry(), v.inBrowser.Geometry()
		if images.Right() > browser.Left() || images.Top() != browser.Top() {
			t.Errorf("the button is at %v, and the one that loads images at %v", browser, images)
		}
		v.inBrowser.Click()
	})
	if len(opened) != 1 {
		t.Fatalf("%d pages were opened", len(opened))
	}

	resp, err := http.Get(opened[0])
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `<div style="display:none">Preview</div>`) || !strings.Contains(string(body), `src="cid/logo@example.com"`) {
		t.Errorf("the browser is given\n%s", body)
	}
	resp, err = http.Get(opened[0] + "cid/logo@example.com")
	if err != nil {
		t.Fatal(err)
	}
	image, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(image) != "PNG" {
		t.Errorf("the image the message carries is %q", image)
	}

	mainthread.Wait(func() {
		v.show(&message.Message{Text: "Hello"}, "INBOX", 2)
		if v.inBrowser != nil || v.loadImages != nil {
			t.Error("a message in plain text has the buttons of the HTML message shown before")
		}
	})

	// The message is served no more once the view is gone.
	mainthread.Wait(func() { v.Delete() })
	deleted = true
	if resp, err := http.Get(opened[0]); err == nil {
		resp.Body.Close()
		t.Error("the message is still served once the view is gone")
	}
}
