package message

import (
	"html"
	"regexp"
	"strings"

	nethtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// links finds web addresses, those beginning www. as well as those with
// their scheme, and mail addresses in text. A web address does not end in
// the punctuation that follows one in a sentence.
var links = regexp.MustCompile(`(?i)\b((?:https?://|www\.)[^\s<>"]+[^\s<>".,;:!?)\]'])|\b([a-z0-9._%+-]+@[a-z0-9.-]+\.[a-z]{2,})\b`)

// Linked returns text as HTML, escaped, with the web and mail addresses in
// it made links.
func Linked(text string) string {
	var b strings.Builder
	last := 0
	for _, m := range links.FindAllStringIndex(text, -1) {
		b.WriteString(html.EscapeString(text[last:m[0]]))
		link := text[m[0]:m[1]]
		b.WriteString(`<a href="`)
		b.WriteString(html.EscapeString(href(link)))
		b.WriteString(`">`)
		b.WriteString(html.EscapeString(link))
		b.WriteString(`</a>`)
		last = m[1]
	}
	b.WriteString(html.EscapeString(text[last:]))
	return b.String()
}

// href returns the address a link to what is written as a link goes to:
// web addresses as they are, but with a scheme if they begin www., and mail
// to the others. The spaces around it, which a link written by hand often
// has, are taken off: Qt cannot follow a link with them.
func href(link string) string {
	link = strings.TrimSpace(link)
	lower := strings.ToLower(link)
	switch {
	case strings.Contains(lower, "://"), strings.HasPrefix(lower, "mailto:"):
		return link
	case strings.HasPrefix(lower, "www."):
		return "http://" + link
	case strings.Contains(link, "@") && !strings.Contains(link, "/"):
		return "mailto:" + link
	}
	return link
}

// followable makes the address of the link one Qt can follow, and reports
// if it changed it.
func followable(t *nethtml.Token) bool {
	changed := false
	for i, a := range t.Attr {
		if a.Namespace == "" && strings.EqualFold(a.Key, "href") {
			if h := href(a.Val); h != a.Val {
				t.Attr[i].Val = h
				changed = true
			}
		}
	}
	return changed
}

// linkedText returns the HTML with the web and mail addresses written in its
// text, but not already links, made links, as Qt shows them only as text,
// and the addresses of its links made ones Qt can follow (href). Text within
// a link, a style, a script or the title is left as it is.
func linkedText(s string) string {
	z := nethtml.NewTokenizer(strings.NewReader(s))
	var b strings.Builder
	b.Grow(len(s))
	// within counts the elements open whose text is left as it is.
	within := 0
	for {
		tt := z.Next()
		if tt == nethtml.ErrorToken {
			// The end of the HTML, as it is read from a string.
			return b.String()
		}
		// Copied, as reading the text unescapes it where it lies.
		raw := string(z.Raw())
		switch tt {
		case nethtml.StartTagToken, nethtml.EndTagToken:
			t := z.Token()
			switch t.DataAtom {
			case atom.A, atom.Style, atom.Script, atom.Title, atom.Textarea:
				if tt == nethtml.EndTagToken {
					within = max(within-1, 0)
					break
				}
				within++
				if t.DataAtom == atom.A && followable(&t) {
					b.WriteString(t.String())
					continue
				}
			}
		case nethtml.TextToken:
			if within == 0 {
				if text := string(z.Text()); links.MatchString(text) {
					b.WriteString(Linked(text))
					continue
				}
			}
		}
		b.WriteString(raw)
	}
}
