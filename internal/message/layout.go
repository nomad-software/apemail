package message

import (
	"math"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// withoutHidden returns the HTML without the elements whose style hides
// them (display: none), and what is in them. Qt shows them: a newsletter's
// preview text, the rows it shows only on a phone (a stylesheet for small
// screens shows them in place of others), and tracking pixels.
func withoutHidden(s string) string {
	z := html.NewTokenizer(strings.NewReader(s))
	var b strings.Builder
	b.Grow(len(s))
	// skipping is the element that is hidden while one is, and open the
	// elements opened within it that have not ended.
	var skipping atom.Atom
	var open []atom.Atom
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			// The end of the HTML, as it is read from a string.
			return b.String()
		}
		// Token unescapes the attributes where they are read, so the tag
		// as it was is kept first.
		raw := string(z.Raw())
		t := z.Token()
		if skipping != 0 {
			if !hiddenEnds(skipping, &open, tt, t.DataAtom) {
				continue
			}
			ended := skipping
			skipping, open = 0, nil
			if tt == html.EndTagToken && t.DataAtom == ended {
				continue
			}
			// The element ended as the one after it, or the one it is
			// in, starts or ends, which is kept.
		}
		if (tt == html.StartTagToken || tt == html.SelfClosingTagToken) && hidden(t) {
			if tt == html.StartTagToken && !void(t.DataAtom) {
				skipping = t.DataAtom
			}
			continue
		}
		b.WriteString(raw)
	}
}

// hiddenEnds reports if the hidden element, with the elements open within
// it, ends at the tag: its own end, the end of the element it is in, or
// the start of the next of its kind, as a row ends at the next row. It
// keeps open up to date with the tag if it does not.
func hiddenEnds(hidden atom.Atom, open *[]atom.Atom, tt html.TokenType, a atom.Atom) bool {
	switch tt {
	case html.StartTagToken:
		if endedBy(hidden, a) && !slices.ContainsFunc(*open, ownsRows) {
			return true
		}
		if !void(a) {
			*open = append(*open, a)
		}
	case html.EndTagToken:
		// The innermost of its kind ends, and any within it whose end
		// tag was left out.
		for i := len(*open) - 1; i >= 0; i-- {
			if (*open)[i] == a {
				*open = (*open)[:i]
				return false
			}
		}
		// The end of the hidden element itself, or of the one it is in.
		return true
	}
	return false
}

// endedBy reports if an element whose end tag can be left out ends where
// the element starts, as a row does where the next row starts.
func endedBy(e, next atom.Atom) bool {
	switch e {
	case atom.Tr:
		return next == atom.Tr || next == atom.Tbody || next == atom.Thead || next == atom.Tfoot
	case atom.Td, atom.Th:
		return next == atom.Td || next == atom.Th || next == atom.Tr || next == atom.Tbody || next == atom.Thead || next == atom.Tfoot
	case atom.Li:
		return next == atom.Li
	case atom.Dt, atom.Dd:
		return next == atom.Dt || next == atom.Dd
	case atom.Option:
		return next == atom.Option
	case atom.P:
		return next == atom.P || next == atom.Div || next == atom.Table || next == atom.Ul || next == atom.Ol
	}
	return false
}

// ownsRows reports if the element holds rows, items or options of its own,
// so that one starting within it does not end an element outside it.
func ownsRows(a atom.Atom) bool {
	return a == atom.Table || a == atom.Ul || a == atom.Ol || a == atom.Dl || a == atom.Select
}

// void reports if the element has no contents and no end tag.
func void(a atom.Atom) bool {
	switch a {
	case atom.Area, atom.Base, atom.Br, atom.Col, atom.Embed, atom.Hr, atom.Img, atom.Input,
		atom.Link, atom.Meta, atom.Source, atom.Track, atom.Wbr:
		return true
	}
	return false
}

// hidden reports if the style of the element hides it.
func hidden(t html.Token) bool {
	v, ok := styleValue(t, "display")
	return ok && strings.HasPrefix(v, "none")
}

// styleValue returns the value of a property of the style of an element,
// in lower case.
func styleValue(t html.Token, property string) (string, bool) {
	for _, a := range t.Attr {
		if a.Key != "style" {
			continue
		}
		// The last of a property is the one that counts.
		var value string
		var found bool
		for decl := range strings.SplitSeq(a.Val, ";") {
			k, v, ok := strings.Cut(decl, ":")
			if ok && strings.EqualFold(strings.TrimSpace(k), property) {
				value, found = strings.ToLower(strings.TrimSpace(v)), true
			}
		}
		return value, found
	}
	return "", false
}

// pixelSizes returns the HTML with the sizes of its images, tables and
// cells as Qt reads them: a number of pixels (or, for a table or cell, a
// percentage), taken from the style if the attribute has none. Qt reads
// any other attribute, as width="600px" or height="auto", as 0, which hides
// an image or squeezes a table into a narrow column, and leaves a size in
// the style alone.
func pixelSizes(s string) string {
	z := html.NewTokenizer(strings.NewReader(s))
	var b strings.Builder
	b.Grow(len(s))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return b.String()
		}
		raw := string(z.Raw())
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			b.WriteString(raw)
			continue
		}
		t := z.Token()
		var keys []string
		switch t.DataAtom {
		case atom.Img:
			keys = []string{"width", "height"}
		case atom.Table, atom.Td, atom.Th:
			keys = []string{"width"}
		default:
			b.WriteString(raw)
			continue
		}
		percent := t.DataAtom != atom.Img
		changed := false
		for _, key := range keys {
			i := slices.IndexFunc(t.Attr, func(a html.Attribute) bool { return a.Key == key })
			size, ok := "", false
			if i >= 0 {
				size, ok = qtSize(t.Attr[i].Val, percent)
				if size == t.Attr[i].Val {
					continue
				}
			}
			if !ok {
				if v, styled := styleValue(t, key); styled {
					size, ok = qtSize(v, percent)
				}
			}
			switch {
			case ok && i >= 0:
				t.Attr[i].Val = size
			case ok:
				t.Attr = append(t.Attr, html.Attribute{Key: key, Val: size})
			case i >= 0:
				t.Attr = slices.Delete(t.Attr, i, i+1)
			default:
				continue
			}
			changed = true
		}
		if changed {
			b.WriteString(t.String())
		} else {
			b.WriteString(raw)
		}
	}
}

// qtSize returns a size as Qt reads it, a whole number of pixels or a
// percentage (if percent allows one), and whether the size is one of those.
func qtSize(v string, percent bool) (string, bool) {
	v = strings.ToLower(strings.TrimSpace(v))
	unit := ""
	switch {
	case strings.HasSuffix(v, "%"):
		if !percent {
			return "", false
		}
		v, unit = strings.TrimSuffix(v, "%"), "%"
	case strings.HasSuffix(v, "px"):
		v = strings.TrimSuffix(v, "px")
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil || !(f >= 0) || math.IsInf(f, 0) {
		return "", false
	}
	return strconv.Itoa(int(math.Round(f))) + unit, true
}
