package images_test

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nomad-software/apemail/internal/images"
)

// pngData returns a small PNG.
func pngData(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 3))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// gifData returns a small GIF.
func gifData(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := gif.Encode(&b, image.NewPaletted(image.Rect(0, 0, 2, 2), []color.Color{color.Black}), nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestFetch(t *testing.T) {
	pngImage, gifImage := pngData(t), gifData(t)
	var served atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/a.png", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Referer") != "" || r.Header.Get("Cookie") != "" {
			t.Errorf("sent %v", r.Header)
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(pngImage)
	})
	// One with no type is known by what it looks like.
	mux.HandleFunc("/b", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write(gifImage)
	})
	mux.HandleFunc("/svg", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`))
	})
	// What is not an image is left out, whatever it looks like.
	mux.HandleFunc("/script.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		w.Write(gifImage)
	})
	mux.HandleFunc("/page", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html><script>alert(1)</script></html>"))
	})
	mux.HandleFunc("/untyped-text", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write([]byte("alert(1)"))
	})
	mux.HandleFunc("/large.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(bytes.Repeat([]byte{0}, 10<<20+1))
	})
	mux.HandleFunc("/moved", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/a.png", http.StatusFound)
	})
	mux.HandleFunc("/to-file", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "file:///etc/passwd", http.StatusFound)
	})
	mux.HandleFunc("/loop", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/loop", http.StatusFound)
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served.Add(1)
		mux.ServeHTTP(w, r)
	}))
	defer server.Close()

	at := func(path string) string { return server.URL + path }
	got := images.Fetch(context.Background(), []string{
		at("/a.png"), " " + at("/b") + " ", at("/svg"), at("/moved"),
		at("/script.js"), at("/page"), at("/untyped-text"), at("/large.png"), at("/missing.png"), at("/to-file"), at("/loop"),
		"file:///etc/passwd", "cid:x", "ftp://example.com/a.png", "/relative.png", "",
	})
	for _, want := range []string{at("/a.png"), " " + at("/b") + " ", at("/svg"), at("/moved")} {
		if _, ok := got[want]; !ok {
			t.Errorf("%s was not fetched", want)
		}
	}
	if len(got) != 4 {
		var keys []string
		for k := range got {
			keys = append(keys, k)
		}
		t.Errorf("fetched %q", keys)
	}
	if !bytes.Equal(got[at("/a.png")], pngImage) || !bytes.Equal(got[at("/moved")], pngImage) {
		t.Error("the image is not as it was served")
	}
}

func TestWeb(t *testing.T) {
	for address, want := range map[string]bool{
		"https://example.com/a.png":  true,
		" HTTP://example.com/a.png ": true,
		"file:///etc/passwd":         false,
		"cid:abc":                    false,
		"data:image/png;base64,AA==": false,
		"javascript:alert(1)":        false,
		"/a.png":                     false,
		"https:///a.png":             false,
		"":                           false,
	} {
		if got := images.Web(address); got != want {
			t.Errorf("Web(%q) = %v", address, got)
		}
	}
}

// TestFetchLimits checks that no more than MaxImages are asked for, and
// that no more than a few are asked for at once.
func TestFetchLimits(t *testing.T) {
	data := pngData(t)
	var served, at, most atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served.Add(1)
		n := at.Add(1)
		defer at.Add(-1)
		for {
			m := most.Load()
			if n <= m || most.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		w.Header().Set("Content-Type", "image/png")
		w.Write(data)
	}))
	defer server.Close()

	var addresses []string
	for i := range images.MaxImages + 20 {
		addresses = append(addresses, fmt.Sprintf("%s/%d.png", server.URL, i))
	}
	got := images.Fetch(context.Background(), addresses)
	if len(got) != images.MaxImages || served.Load() != images.MaxImages {
		t.Errorf("fetched %d, asked for %d", len(got), served.Load())
	}
	if m := most.Load(); m > 6 {
		t.Errorf("%d were asked for at once", m)
	}
}

// TestFetchStopped checks that fetching stops when it is cancelled, with
// what it has.
func TestFetchStopped(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	defer close(release)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan map[string][]byte)
	go func() { done <- images.Fetch(ctx, []string{server.URL + "/a.png", server.URL + "/b.png"}) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case got := <-done:
		if len(got) != 0 {
			t.Errorf("fetched %d", len(got))
		}
	case <-time.After(10 * time.Second):
		t.Fatal("fetching did not stop")
	}
}

func TestNothingToFetch(t *testing.T) {
	if got := images.Fetch(context.Background(), nil); len(got) != 0 {
		t.Errorf("fetched %v", got)
	}
	if got := images.Fetch(context.Background(), []string{"cid:a", strings.Repeat("x", 10)}); len(got) != 0 {
		t.Errorf("fetched %v", got)
	}
}
