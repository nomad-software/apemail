// Package images fetches the images a message links to on the internet,
// when the user asks for them: images and nothing else. It has no
// dependency on the UI.
package images

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	// MaxImages is how many images of a message are fetched, so that one
	// cannot have thousands asked for.
	MaxImages = 100
	// maxSize is the most of an image that is read; one larger is left
	// out.
	maxSize = 10 << 20
	// parallel is how many images are fetched at once.
	parallel = 6
	// timeout is how long an image is waited for.
	timeout = 30 * time.Second
	// maxRedirects is how many redirects are followed.
	maxRedirects = 5
)

// client fetches the images. It keeps no cookies, sends no referrer, and
// follows redirects only to the web.
var client = &http.Client{
	Timeout: timeout,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return errors.New("too many redirects")
		}
		if !Web(req.URL.String()) {
			return errors.New("redirected away from the web")
		}
		// Go says where a redirect came from, which is not to be told.
		req.Header.Del("Referer")
		return nil
	},
}

// Web reports if the address is one on the web (http or https), which is
// all that is fetched.
func Web(address string) bool {
	u, err := url.Parse(strings.TrimSpace(address))
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Scheme, "http") || strings.EqualFold(u.Scheme, "https")
}

// Fetch fetches the images at the addresses, a few at once and no more than
// MaxImages, and returns those that are images, by address. What is not on
// the web, is not an image, is too large, or cannot be fetched is left out.
// It returns early, with what it has, if ctx is cancelled.
func Fetch(ctx context.Context, addresses []string) map[string][]byte {
	if len(addresses) > MaxImages {
		addresses = addresses[:MaxImages]
	}
	var (
		mu  sync.Mutex
		got = map[string][]byte{}
		wg  sync.WaitGroup
	)
	slots := make(chan struct{}, parallel)
	for _, address := range addresses {
		if !Web(address) {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-slots }()
			if data, err := fetch(ctx, address); err == nil {
				mu.Lock()
				got[address] = data
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return got
}

// fetch fetches the image at the address.
func fetch(ctx context.Context, address string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(address), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "image/*")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New(resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxSize {
		return nil, errors.New("the image is too large")
	}
	if !isImage(resp.Header.Get("Content-Type"), data) {
		return nil, errors.New("it is not an image")
	}
	return data, nil
}

// isImage reports if what was fetched is an image: by what the server says
// it is, or, if it says nothing useful, by what it looks like.
func isImage(contentType string, data []byte) bool {
	t, _, err := mime.ParseMediaType(contentType)
	if err == nil && strings.HasPrefix(t, "image/") {
		return true
	}
	if err == nil && t != "application/octet-stream" {
		return false
	}
	return strings.HasPrefix(http.DetectContentType(data), "image/")
}
