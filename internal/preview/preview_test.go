package preview_test

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/nomad-software/apemail/internal/preview"
)

// get fetches the address, and returns the response and its body.
func get(t *testing.T, address string) (*http.Response, string) {
	t.Helper()
	resp, err := http.Get(address)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, string(body)
}

// TestServe checks that the message is served on this computer alone, at
// an address no one can guess, with the parts it shows as cid: beside it
// whatever the case of their IDs, and nothing else; that the browser is
// told to run none of its scripts, keep none of it and send no referrer;
// and that it is served no more once closed.
func TestServe(t *testing.T) {
	html := `<p>Hello <img alt="logo" src="cid:Logo@example.com"> <img src="https://example.com/a.png"></p>` +
		`<table background="CID:back%20ground"><tr><td>x</td></tr></table><a href="cid:not-an-image">link</a>`
	page, err := preview.Serve(html, map[string]preview.Part{
		"logo@example.com": {ContentType: "image/png", Data: []byte("PNG")},
		"back ground":      {ContentType: "image/gif", Data: []byte("GIF")},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer page.Close()

	u, err := url.Parse(page.URL)
	if err != nil {
		t.Fatal(err)
	}
	if u.Hostname() != "127.0.0.1" || len(u.Path) < 32 || !strings.HasSuffix(u.Path, "/") {
		t.Errorf("the message is served at %s", page.URL)
	}

	resp, body := get(t, page.URL)
	want := `<p>Hello <img alt="logo" src="cid/Logo@example.com"> <img src="https://example.com/a.png"></p>` +
		`<table background="cid/back%20ground"><tr><td>x</td></tr></table><a href="cid:not-an-image">link</a>`
	if resp.StatusCode != http.StatusOK || body != want {
		t.Errorf("got %s\n%s\nwant\n%s", resp.Status, body, want)
	}
	for header, want := range map[string]string{
		"Content-Type":    "text/html; charset=utf-8",
		"Cache-Control":   "no-store",
		"Referrer-Policy": "no-referrer",
	} {
		if got := resp.Header.Get(header); got != want {
			t.Errorf("%s is %q, want %q", header, got, want)
		}
	}
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'none'") {
		t.Errorf("the message's scripts are not stopped: %q", csp)
	}

	for path, want := range map[string]string{"cid/Logo@example.com": "PNG", "cid/back%20ground": "GIF"} {
		resp, body := get(t, page.URL+path)
		if resp.StatusCode != http.StatusOK || body != want || !strings.HasPrefix(resp.Header.Get("Content-Type"), "image/") {
			t.Errorf("%s: got %s %q (%s)", path, resp.Status, body, resp.Header.Get("Content-Type"))
		}
	}
	for _, address := range []string{
		page.URL + "cid/missing",
		page.URL + "other",
		"http://" + u.Host + "/",
		"http://" + u.Host + "/0123456789abcdef0123456789abcdef/",
	} {
		if resp, _ := get(t, address); resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: got %s", address, resp.Status)
		}
	}

	page.Close()
	if resp, err := http.Get(page.URL); err == nil {
		resp.Body.Close()
		t.Error("the message is still served once closed")
	}
}

// TestServeAddresses checks that each message is served at an address of
// its own.
func TestServeAddresses(t *testing.T) {
	a, err := preview.Serve("<p>A</p>", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := preview.Serve("<p>B</p>", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if a.URL == b.URL {
		t.Fatalf("both are served at %s", a.URL)
	}
	if _, body := get(t, a.URL); body != "<p>A</p>" {
		t.Errorf("got %q", body)
	}
	if _, body := get(t, b.URL); body != "<p>B</p>" {
		t.Errorf("got %q", body)
	}
}
