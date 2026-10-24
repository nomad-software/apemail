package message_test

import (
	"strings"
	"testing"

	"github.com/nomad-software/apemail/internal/message"
)

// TestLinkedText checks that addresses written in the text of an HTML
// message are made links, and nothing else is changed.
func TestLinkedText(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"web", `<p>See https://example.com/a for more.</p>`,
			`<p>See <a href="https://example.com/a">https://example.com/a</a> for more.</p>`},
		{"www", `<p>Go to www.example.com/b, today</p>`,
			`<p>Go to <a href="http://www.example.com/b">www.example.com/b</a>, today</p>`},
		{"mail", `<td>Write to help@example.com.</td>`,
			`<td>Write to <a href="mailto:help@example.com">help@example.com</a>.</td>`},
		// Escaped text stays escaped, and the brackets around an address
		// are not taken into it.
		{"escaped", `<p>a &amp; b &lt;https://example.com/c?x=1&amp;y=2&gt;</p>`,
			`<p>a &amp; b &lt;<a href="https://example.com/c?x=1&amp;y=2">https://example.com/c?x=1&amp;y=2</a>&gt;</p>`},
		{"already a link", `<a href="https://example.com/d">https://example.com/d</a>`,
			`<a href="https://example.com/d">https://example.com/d</a>`},
		{"within a link", `<a href="https://example.com/e"><b>www.example.com</b></a> www.example.com`,
			`<a href="https://example.com/e"><b>www.example.com</b></a> <a href="http://www.example.com">www.example.com</a>`},
		{"style", `<style>a { background: url(https://example.com/f.png) }</style>`,
			`<style>a { background: url(https://example.com/f.png) }</style>`},
		{"script", `<script>var u = "https://example.com/g";</script>`,
			`<script>var u = "https://example.com/g";</script>`},
		{"title", `<title>https://example.com/h</title>`, `<title>https://example.com/h</title>`},
		{"attribute", `<img src="https://example.com/i.png" alt="https://example.com/i">`,
			`<img src="https://example.com/i.png" alt="https://example.com/i">`},
		// The address of a link is made one that can be followed.
		{"spaced link", "<a href=\"  https://example.com/j\n\">j</a>", `<a href="https://example.com/j">j</a>`},
		{"link without a scheme", `<a href="www.example.com/k">k</a>`, `<a href="http://www.example.com/k">k</a>`},
		{"none", `<p>Nothing to link &amp; no address.</p>`, `<p>Nothing to link &amp; no address.</p>`},
	} {
		if got := parseHTML(t, c.in); !strings.Contains(got, c.want) {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
	}
}

// TestLinked checks that the addresses written in text are made links to
// where they go.
func TestLinked(t *testing.T) {
	for in, want := range map[string]string{
		"See https://example.com/a.":  `See <a href="https://example.com/a">https://example.com/a</a>.`,
		"(www.example.com/b)":         `(<a href="http://www.example.com/b">www.example.com/b</a>)`,
		"WWW.example.com":             `<a href="http://WWW.example.com">WWW.example.com</a>`,
		"to someone@example.com":      `to <a href="mailto:someone@example.com">someone@example.com</a>`,
		"a < b & no address":          `a &lt; b &amp; no address`,
		"https://example.com/c?x&y=1": `<a href="https://example.com/c?x&amp;y=1">https://example.com/c?x&amp;y=1</a>`,
	} {
		if got := message.Linked(in); got != want {
			t.Errorf("Linked(%q) = %q, want %q", in, got, want)
		}
	}
}
