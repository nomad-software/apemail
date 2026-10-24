package message

import (
	"math"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// withoutHidden returns the HTML without the elements that are hidden on a
// computer's screen (display: none), by their style or the stylesheet's
// rules for their classes (displayRules), and what is in them. Qt shows
// them: a newsletter's preview text, what it shows only on a phone, and
// tracking pixels. What the stylesheet shows though the element's style
// hides it, as a newsletter does what it shows on a computer but not in
// Outlook, has display taken out of its style, as Qt would hide it.
func withoutHidden(s string) string {
	rules := displayRules(s)
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
		if tt == html.StartTagToken || tt == html.SelfClosingTagToken {
			v, set := display(t, rules)
			switch {
			case set && strings.HasPrefix(v, "none"):
				if tt == html.StartTagToken && !void(t.DataAtom) {
					skipping = t.DataAtom
				}
				continue
			case hiddenByStyle(t):
				raw = withoutDisplay(t).String()
			}
		}
		b.WriteString(raw)
	}
}

// hiddenByStyle reports if the style of the element hides it.
func hiddenByStyle(t html.Token) bool {
	v, ok := styleValue(t, "display")
	return ok && strings.HasPrefix(v, "none")
}

// withoutDisplay returns the element with display taken out of its style.
func withoutDisplay(t html.Token) html.Token {
	attrs := slices.Clone(t.Attr)
	for i, a := range attrs {
		if a.Key != "style" {
			continue
		}
		var kept []string
		for decl := range strings.SplitSeq(a.Val, ";") {
			k, _, _ := strings.Cut(decl, ":")
			if !strings.EqualFold(strings.TrimSpace(k), "display") && strings.TrimSpace(decl) != "" {
				kept = append(kept, strings.TrimSpace(decl))
			}
		}
		attrs[i].Val = strings.Join(kept, ";")
	}
	t.Attr = attrs
	return t
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

// styleValue returns the value of a property of the style of an element,
// in lower case and without !important.
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
				value, _ = cssValue(v)
				found = true
			}
		}
		return value, found
	}
	return "", false
}

// cssValue returns the value of a declaration of CSS, in lower case and
// without !important, and whether it was important.
func cssValue(v string) (string, bool) {
	v = strings.ToLower(strings.TrimSpace(v))
	if before, ok := strings.CutSuffix(v, "!important"); ok {
		return strings.TrimSpace(before), true
	}
	return v, false
}

// pixelSizes returns the HTML with the sizes of its images, tables and
// cells as Qt reads them: a number of pixels (or, for a table or cell, a
// percentage), taken from the style if the attribute has none. Qt reads
// any other attribute, as width="600px", height="auto" or one that is
// empty, as 0, which hides an image or squeezes a table into a narrow
// column, and leaves a size in the style alone. The attribute is kept
// where both have a size, though a browser takes the style's, as a style
// that makes a table or image as wide as the page (width: 100%) comes with
// a max-width Qt does not read either, which the attribute is nearer to.
// The size of an image the style makes auto is left out, so that Qt keeps
// its shape as a browser does.
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
			styled, hasStyle := styleValue(t, key)
			size, ok := "", false
			switch {
			case t.DataAtom == atom.Img && hasStyle && styled == "auto":
			case i >= 0:
				size, ok = qtSize(t.Attr[i].Val, percent)
				if ok && size == t.Attr[i].Val {
					continue
				}
			}
			if !ok && hasStyle && styled != "auto" {
				size, ok = qtSize(styled, percent)
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

// maxSize is the largest size, in pixels, that is kept: a larger one is
// nonsense, and would not fit in a number Qt reads.
const maxSize = 100000

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
	if err != nil || !(f >= 0) || f > maxSize {
		return "", false
	}
	return strconv.Itoa(int(math.Round(f))) + unit, true
}
