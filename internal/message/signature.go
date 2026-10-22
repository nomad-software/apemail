package message

import (
	"crypto/rand"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/nomad-software/apemail/internal/homedir"
)

// maxSignatureSize is the largest a signature file, or an image it shows,
// is read: a signature is a few lines, and a file much larger was chosen by
// mistake.
const maxSignatureSize = 1 << 20

// Signature is what an account signs its messages with: as text, for a
// message written as text, and as HTML, for one written as HTML, with the
// images the HTML shows, which go with the message as it does.
type Signature struct {
	Text   string
	HTML   string
	Images []Attachment
}

// LoadSignature returns the signature of an account: the text, or, if file
// is not empty, what the file holds, read now so that a change to it is
// seen in the next message. A file whose name ends in .html or .htm is
// HTML, of which only what is within its body is taken; any other is text.
// The images on this computer that HTML shows are read, to go with the
// message, rather than be sent as links to files no one else can see.
func LoadSignature(text, file string) (Signature, error) {
	if file == "" {
		text = strings.TrimRight(text, "\n")
		if text == "" {
			return Signature{}, nil
		}
		return Signature{Text: text, HTML: TextToHTML(text)}, nil
	}

	path := homedir.Expand(file)
	data, err := readLimited(path)
	if err != nil {
		return Signature{}, fmt.Errorf("reading the signature: %w", err)
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".html", ".htm":
		body, images, err := embedImages(bodyOf(string(data)), filepath.Dir(path))
		if err != nil {
			return Signature{}, err
		}
		return Signature{Text: HTMLToText(body), HTML: body, Images: images}, nil
	}
	text = strings.TrimRight(string(data), "\r\n")
	return Signature{Text: text, HTML: TextToHTML(text)}, nil
}

// readLimited reads a file of no more than maxSignatureSize.
func readLimited(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxSignatureSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxSignatureSize {
		return nil, fmt.Errorf("%s is larger than a signature can be (%d KB)", filepath.Base(path), maxSignatureSize>>10)
	}
	return data, nil
}

// bodyPattern finds what is within the body of an HTML document.
var bodyPattern = regexp.MustCompile(`(?is)<body[^>]*>(.*?)(?:</body>|$)`)

// bodyOf returns what is within the body of HTML, or all of it if it has no
// body, as a fragment of HTML that is put within the body of a message.
func bodyOf(s string) string {
	if m := bodyPattern.FindStringSubmatch(s); m != nil {
		s = m[1]
	}
	return strings.TrimSpace(s)
}

// imagePattern finds the source of each image of HTML: what comes before it,
// its quote and the source.
var imagePattern = regexp.MustCompile(`(?is)(<img\b[^>]*?\bsrc\s*=\s*)(["'])(.*?)(["'])`)

// embedImages has the images of the HTML that are files on this computer,
// named by a path relative to dir or by a file: URL, shown from parts that
// go with the message (cid:), and returns them. Images on the internet, and
// those already within the message, are left as they are.
func embedImages(body, dir string) (string, []Attachment, error) {
	var images []Attachment
	var errs []error
	body = imagePattern.ReplaceAllStringFunc(body, func(tag string) string {
		m := imagePattern.FindStringSubmatch(tag)
		src := html.UnescapeString(m[3])
		path, ok := localPath(src, dir)
		if !ok {
			return tag
		}
		data, err := readLimited(path)
		if err != nil {
			errs = append(errs, fmt.Errorf("reading an image of the signature: %w", err))
			return tag
		}
		t := mime.TypeByExtension(filepath.Ext(path))
		if !strings.HasPrefix(t, "image/") {
			errs = append(errs, fmt.Errorf("%s, which the signature shows, is not an image", filepath.Base(path)))
			return tag
		}
		a := Attachment{Filename: filepath.Base(path), ContentType: t, ContentID: NewContentID(), Inline: true, Data: data}
		images = append(images, a)
		return m[1] + m[2] + "cid:" + a.ContentID + m[4]
	})
	return body, images, errors.Join(errs...)
}

// localPath returns the path of the file an image's source names, if it is
// a file on this computer: a file: URL, or a path, relative to dir if it is
// not absolute.
func localPath(src, dir string) (string, bool) {
	u, err := url.Parse(src)
	if err != nil {
		return "", false
	}
	switch strings.ToLower(u.Scheme) {
	case "file":
		return u.Path, u.Path != ""
	case "":
		path := homedir.Expand(src)
		if p, err := url.PathUnescape(path); err == nil {
			path = p
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(dir, path)
		}
		return path, true
	}
	// http:, https:, cid:, data: and the like are not files here.
	return "", false
}

// NewContentID returns a Content-ID, by which the HTML of a message shows a
// part that goes with it (cid:), that no other part has.
func NewContentID() string {
	return rand.Text() + "@apemail"
}
