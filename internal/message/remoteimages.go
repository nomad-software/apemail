package message

import (
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// placeholderSize is the width and height, in pixels, of the icon Qt shows
// in place of an image it does not have.
const placeholderSize = 16

// SmallRemoteImages returns the HTML with its images on the internet, which
// are never loaded, no bigger than the icon shown in their place. Qt draws
// that icon at the size the image asks for, and a newsletter's are as wide
// as the message. Sizes smaller than the icon are kept, so that tracking
// pixels and spacers stay a pixel wide. It is for showing a message only:
// what is sent keeps the sizes.
func SmallRemoteImages(s string) string {
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
		if t.DataAtom != atom.Img || carried(t) {
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

// carried reports if an image is one the message carries itself (cid: or
// data:), which is shown.
func carried(t html.Token) bool {
	for _, a := range t.Attr {
		if a.Key == "src" {
			src := strings.ToLower(strings.TrimSpace(a.Val))
			return strings.HasPrefix(src, "cid:") || strings.HasPrefix(src, "data:")
		}
	}
	return false
}
