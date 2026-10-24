package message

import (
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// maxTableDepth is how many tables deep HTML is shown as tables. Qt lays a
// table out again for each pass over the table it is in, so the time it
// takes doubles with each table deeper. Mail written by a newsletter's
// editor nests them a dozen deep, one inside the next, to place things
// (a table for each box), and took Qt a minute to show.
const maxTableDepth = 4

// flatTables returns the HTML with the tables nested more than
// maxTableDepth deep made blocks: each table, row and cell a <div>, which
// shows its contents one below the other. Those deep are nearly always one
// cell wide, holding what is in it in place, so the message looks much the
// same.
func flatTables(s string) string {
	z := html.NewTokenizer(strings.NewReader(s))
	var b strings.Builder
	b.Grow(len(s))
	depth := 0
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			// The end of the HTML, as it is read from a string.
			return b.String()
		}
		raw := string(z.Raw())
		if tt != html.StartTagToken && tt != html.EndTagToken {
			b.WriteString(raw)
			continue
		}
		name, _ := z.TagName()
		end := tt == html.EndTagToken
		switch atom.Lookup(name) {
		case atom.Table:
			if !end {
				depth++
			}
			if depth > maxTableDepth {
				raw = block(end)
			}
			if end && depth > 0 {
				depth--
			}
		case atom.Tr, atom.Td, atom.Th, atom.Caption:
			if depth > maxTableDepth {
				raw = block(end)
			}
		case atom.Tbody, atom.Thead, atom.Tfoot, atom.Colgroup, atom.Col:
			if depth > maxTableDepth {
				raw = ""
			}
		}
		b.WriteString(raw)
	}
}

// block returns the tag of a <div> that starts or ends.
func block(end bool) string {
	if end {
		return "</div>"
	}
	return "<div>"
}
