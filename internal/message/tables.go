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
// shows its contents one below the other, with its colours. Those deep are
// nearly always one cell wide, holding what is in it in place, so the
// message looks much the same.
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
		t := z.Token()
		end := tt == html.EndTagToken
		switch t.DataAtom {
		case atom.Table:
			if !end {
				depth++
			}
			if depth > maxTableDepth {
				raw = block(t, end)
			}
			if end && depth > 0 {
				depth--
			}
		case atom.Tr, atom.Td, atom.Th, atom.Caption:
			if depth > maxTableDepth {
				raw = block(t, end)
			}
		case atom.Tbody, atom.Thead, atom.Tfoot, atom.Colgroup, atom.Col:
			if depth > maxTableDepth {
				raw = ""
			}
		}
		b.WriteString(raw)
	}
}

// block returns the tag of a <div> that starts or ends in place of the
// tag of a table, row or cell, with its colours, so that a newsletter's
// white text on a dark table still shows: its background (from its style,
// or bgcolor) and the colour of its text. Nothing else of its style is
// kept, as it places the table and its cells (alignment, sizes, padding,
// floats), which in a <div> would apply to all within it instead.
func block(t html.Token, end bool) string {
	if end {
		return "</div>"
	}
	var style []string
	if bg := background(t); bg != "" {
		style = append(style, "background-color:"+bg)
	}
	if c, ok := styleValue(t, "color"); ok && c != "" {
		style = append(style, "color:"+c)
	}
	if len(style) == 0 {
		return "<div>"
	}
	div := html.Token{Type: html.StartTagToken, Data: "div", Attr: []html.Attribute{{Key: "style", Val: strings.Join(style, ";")}}}
	return div.String()
}

// background returns the colour of the background of a table, row or cell,
// if it has one: background-color in its style, or background if that is
// only a colour, or else bgcolor.
func background(t html.Token) string {
	if c, ok := styleValue(t, "background-color"); ok && c != "" {
		return c
	}
	if c, ok := styleValue(t, "background"); ok && c != "" && !strings.ContainsAny(c, " (") {
		return c
	}
	for _, a := range t.Attr {
		if a.Key == "bgcolor" {
			return strings.TrimSpace(a.Val)
		}
	}
	return ""
}
