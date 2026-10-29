package message

import (
	"html"
	"regexp"
	"strings"
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

// href returns the address a link to what is written as an address goes
// to: web addresses as they are, but with a scheme if they begin www., and
// mail to the others.
func href(link string) string {
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
