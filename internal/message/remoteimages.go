package message

import (
	"slices"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// placeholderSize is the width and height, in pixels, of the icon Qt shows
// in place of an image it does not have.
const placeholderSize = 16

// SmallRemoteImages returns the HTML with its images on the internet that
// are not loaded no bigger than the icon shown in their place: all but
// those whose address loaded reports, which can be nil. Qt draws that icon
// at the size the image asks for, and a newsletter's are as wide as the
// message. Sizes smaller than the icon are kept, so that tracking pixels
// and spacers stay a pixel wide. It is for showing a message only: what is
// sent keeps the sizes.
func SmallRemoteImages(s string, loaded func(src string) bool) string {
	z := html.NewTokenizer(strings.NewReader(s))
	var b strings.Builder
	b.Grow(len(s))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			// The end of the HTML, as it is read from a string.
			return b.String()
		}
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			b.Write(z.Raw())
			continue
		}
		// Token unescapes the attributes where they are read, so the tag
		// as it was is kept first.
		raw := string(z.Raw())
		t := z.Token()
		if t.DataAtom != atom.Img || carried(t) || (loaded != nil && loaded(src(t))) {
			b.WriteString(raw)
			continue
		}
		var attrs []html.Attribute
		for _, a := range t.Attr {
			switch a.Key {
			case "width", "height":
				// A size that is not in pixels is left out, and the
				// icon's own taken.
				n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(a.Val), "px"))
				if err == nil {
					attrs = append(attrs, html.Attribute{Key: a.Key, Val: strconv.Itoa(min(n, placeholderSize))})
				}
			case "style":
				// A style can size the image too.
			default:
				attrs = append(attrs, a)
			}
		}
		t.Attr = attrs
		b.WriteString(t.String())
	}
}

// RemoteImages returns the addresses of the images of the HTML that the
// message does not carry itself, each once, in the order they are first
// shown.
func RemoteImages(s string) []string {
	z := html.NewTokenizer(strings.NewReader(s))
	var srcs []string
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return srcs
		}
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			continue
		}
		t := z.Token()
		if t.DataAtom == atom.Img && !carried(t) && src(t) != "" && !slices.Contains(srcs, src(t)) {
			srcs = append(srcs, src(t))
		}
	}
}

// src returns the address of an image.
func src(t html.Token) string {
	for _, a := range t.Attr {
		if a.Key == "src" {
			return strings.TrimSpace(a.Val)
		}
	}
	return ""
}

// carried reports if an image is one the message carries itself (cid: or
// data:), which is shown.
func carried(t html.Token) bool {
	s := strings.ToLower(src(t))
	return strings.HasPrefix(s, "cid:") || strings.HasPrefix(s, "data:")
}
