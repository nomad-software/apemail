package message_test

import (
	"slices"
	"testing"

	"github.com/nomad-software/apemail/internal/message"
)

// TestSmallRemoteImages checks that images on the internet ask for no more
// room than the icon shown in their place, that smaller ones keep their
// size, and that images the message carries and everything else are left
// as they are.
func TestSmallRemoteImages(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{`<img src="https://x/hero.png" style="width: 100%; display: block" alt="Hero" width="620">`,
			`<img src="https://x/hero.png" alt="Hero" width="16">`},
		{`<IMG SRC="http://x/logo.png" WIDTH="235px" HEIGHT="120"/>`,
			`<img src="http://x/logo.png" width="16" height="16"/>`},
		// Tracking pixels and spacers stay as small as they were.
		{`<img src="https://x/o.gif" height="1" width="1"><img src="https://x/s.gif" width="1" height="10">`,
			`<img src="https://x/o.gif" height="1" width="1"><img src="https://x/s.gif" width="1" height="10">`},
		// A size that is not in pixels is left out.
		{`<img src="https://x/a.png" width="54.4%">`, `<img src="https://x/a.png">`},
		// One with no address cannot be loaded either.
		{`<img width="300">`, `<img width="16">`},
		// What is in the address is written back as it was.
		{`<img src="https://x/a.png?a=1&amp;b=&quot;2&quot;" width="300">`,
			`<img src="https://x/a.png?a=1&amp;b=&#34;2&#34;" width="16">`},
		{`<img src="cid:pic@x" width="620" style="width: 620px"><img src="DATA:image/png;base64,AA==" width="620">`,
			`<img src="cid:pic@x" width="620" style="width: 620px"><img src="DATA:image/png;base64,AA==" width="620">`},
		{`<p class="a" style="width: 620px">Fish &amp; chips &nbsp;<!-- <img src="https://x/a.png" width="620"> --></p><style>img { width: 620px }</style>`,
			`<p class="a" style="width: 620px">Fish &amp; chips &nbsp;<!-- <img src="https://x/a.png" width="620"> --></p><style>img { width: 620px }</style>`},
	} {
		if got := message.SmallRemoteImages(c.in, nil); got != c.want {
			t.Errorf("%s\ngot  %s\nwant %s", c.in, got, c.want)
		}
	}
}

// TestSmallRemoteImagesLoaded checks that images that have been loaded keep
// the size the message gives them, and the others are made small.
func TestSmallRemoteImagesLoaded(t *testing.T) {
	in := `<img src="https://x/a.png" width="620"><img src=" https://x/b.png " width="620">`
	got := message.SmallRemoteImages(in, func(src string) bool { return src == "https://x/b.png" })
	if want := `<img src="https://x/a.png" width="16"><img src=" https://x/b.png " width="620">`; got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestRemoteImages(t *testing.T) {
	in := `<img src="https://x/a.png?a=1&amp;b=2"><IMG SRC="cid:pic"><img src="data:image/png;base64,AA==">` +
		`<!-- <img src="https://x/hidden.png"> --><img src=" http://x/b.gif "><img src="https://x/a.png?a=1&amp;b=2"><img alt="none">` +
		`<p style="background: url(https://x/c.png)">x</p>`
	got := message.RemoteImages(in)
	if want := []string{"https://x/a.png?a=1&b=2", "http://x/b.gif"}; !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}
