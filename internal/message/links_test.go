package message_test

import (
	"testing"

	"github.com/nomad-software/apemail/internal/message"
)

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
