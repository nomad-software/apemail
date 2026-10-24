// Package preview shows an HTML message in the web browser: it serves the
// message, and the images it carries (cid:), from a web server of its own
// on this computer, for a while, at an address no one can guess. Nothing is
// written to disk, as a file would hold the message unencrypted. It has no
// dependency on the UI.
package preview

import (
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// lifetime is how long a message is served, which is long enough for the
// browser to open it and load its images, and for it to be loaded again.
// Tests make it shorter.
var lifetime = 10 * time.Minute

// policy keeps the browser from running the message's scripts or plugins,
// sending its forms or framing other pages, as the message is mail rather
// than a site that is trusted.
const policy = "script-src 'none'; object-src 'none'; frame-src 'none'; base-uri 'none'; form-action 'none'"

// Part is a part of the message its HTML shows as cid:ID.
type Part struct {
	ContentType string
	Data        []byte
}

// Page is a message being served.
type Page struct {
	// URL is where the browser finds the message.
	URL    string
	server *http.Server
	timer  *time.Timer
}

// Serve serves the HTML of a message, with the parts it shows by their
// content ID (cid:ID), for a while (lifetime), and returns where.
func Serve(htmlText string, parts map[string]Part) (*Page, error) {
	token := make([]byte, 16)
	if _, err := rand.Read(token); err != nil {
		return nil, err
	}
	base := "/" + hex.EncodeToString(token) + "/"
	page := []byte(partsServed(htmlText))
	// Content IDs are matched whatever their case, as when they are shown
	// in the message.
	byID := make(map[string]Part, len(parts))
	for id, p := range parts {
		byID[strings.ToLower(id)] = p
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+base, func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		// The browser keeps nothing of it, and tells the sites the
		// message loads images from nothing of where they are shown.
		h.Set("Cache-Control", "no-store")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Content-Type-Options", "nosniff")
		if r.URL.Path == base {
			h.Set("Content-Security-Policy", policy)
			h.Set("Content-Type", "text/html; charset=utf-8")
			w.Write(page)
			return
		}
		id, ok := strings.CutPrefix(r.URL.Path, base+"cid/")
		p, found := byID[strings.ToLower(id)]
		if !ok || !found {
			http.NotFound(w, r)
			return
		}
		h.Set("Content-Type", p.ContentType)
		w.Write(p.Data)
	})
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	p := &Page{
		URL:    "http://" + l.Addr().String() + base,
		server: &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second},
	}
	// Serve returns when the server is closed, and closes the listener.
	go p.server.Serve(l)
	// The server is closed by the timer itself rather than by Close, which
	// stops the timer and may be called before it is made.
	p.timer = time.AfterFunc(lifetime, func() { p.server.Close() })
	return p, nil
}

// Close stops serving the message.
func (p *Page) Close() {
	p.timer.Stop()
	p.server.Close()
}

// partsServed returns the HTML with the parts it shows as cid:ID at the
// address they are served at, beside it, as a browser knows nothing of
// cid:.
func partsServed(s string) string {
	z := html.NewTokenizer(strings.NewReader(s))
	var b strings.Builder
	b.Grow(len(s))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			// The end of the HTML, as it is read from a string.
			return b.String()
		}
		// Token unescapes the attributes where they are read, so the tag
		// as it was is kept first.
		raw := string(z.Raw())
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			b.WriteString(raw)
			continue
		}
		t := z.Token()
		changed := false
		for i, a := range t.Attr {
			if a.Key != "src" && a.Key != "background" {
				continue
			}
			v := strings.TrimSpace(a.Val)
			if len(v) < len("cid:") || !strings.EqualFold(v[:len("cid:")], "cid:") {
				continue
			}
			id, err := url.PathUnescape(v[len("cid:"):])
			if err != nil {
				id = v[len("cid:"):]
			}
			t.Attr[i].Val = "cid/" + url.PathEscape(id)
			changed = true
		}
		if changed {
			b.WriteString(t.String())
		} else {
			b.WriteString(raw)
		}
	}
}
