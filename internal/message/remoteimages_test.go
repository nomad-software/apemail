package message_test

import (
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
		if got := message.SmallRemoteImages(c.in); got != c.want {
			t.Errorf("%s\ngot  %s\nwant %s", c.in, got, c.want)
		}
	}
}
