package message

import (
	"regexp"
	"slices"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// displayRule is a rule of a message's stylesheet that shows or hides the
// elements with all its classes, and its tag if it names one, on a screen
// as wide as a computer's.
type displayRule struct {
	tag       string
	classes   []string
	value     string
	important bool
}

// simpleSelector is a selector of an element by its classes, and perhaps its
// tag, alone: table.wide, or .a.b. Others, as body .a, [owa] .a or
// #MessageViewBody .a, are for a context that is not known here.
var simpleSelector = regexp.MustCompile(`^([a-zA-Z][a-zA-Z0-9]*)?((?:\.[a-zA-Z_][a-zA-Z0-9_-]*)+)$`)

// displayRules returns the rules of the stylesheets of the HTML that show or
// hide elements by their classes on a computer's screen: newsletters hide
// with them what they show on a phone, and show what they hide for one
// (display: none in its style), as Outlook reads no stylesheet. The rules
// for a phone, within @media for a screen no wider than so much, and for
// print, are left out, as are those of selectors that are not simple.
func displayRules(s string) []displayRule {
	z := html.NewTokenizer(strings.NewReader(s))
	var rules []displayRule
	inStyle := false
	for {
		switch z.Next() {
		case html.ErrorToken:
			return rules
		case html.StartTagToken:
			name, _ := z.TagName()
			inStyle = atom.Lookup(name) == atom.Style
		case html.EndTagToken:
			inStyle = false
		case html.TextToken:
			if inStyle {
				rules = append(rules, cssDisplayRules(string(z.Text()))...)
			}
		}
	}
}

// cssComment is a comment in CSS, or the marks that hide a stylesheet from
// a reader of HTML so old it shows them (<!-- and -->).
var cssComment = regexp.MustCompile(`(?s)/\*.*?\*/|<!--|-->`)

// cssBlock is a block of a stylesheet that is open where it is read: what
// came before it (the query of an @media, or the selectors of a rule), where
// it starts, and whether it holds blocks of its own.
type cssBlock struct {
	prelude string
	start   int
	nested  bool
}

// cssDisplayRules returns the rules of a stylesheet displayRules does.
func cssDisplayRules(css string) []displayRule {
	css = cssComment.ReplaceAllString(css, "")
	var rules []displayRule
	var open []cssBlock
	from := 0
	for i := 0; i < len(css); i++ {
		switch css[i] {
		case '{':
			if len(open) > 0 {
				open[len(open)-1].nested = true
			}
			open = append(open, cssBlock{prelude: strings.TrimSpace(css[from:i]), start: i + 1})
			from = i + 1
		case '}':
			if len(open) == 0 {
				from = i + 1
				continue
			}
			b := open[len(open)-1]
			open = open[:len(open)-1]
			from = i + 1
			if b.nested || !forComputers(open) {
				continue
			}
			rules = append(rules, ruleDisplay(b.prelude, css[b.start:i])...)
		case ';':
			// An @import or @charset before a rule is not its selector.
			if len(open) == 0 {
				from = i + 1
			}
		}
	}
	return rules
}

// forComputers reports if the rules within the blocks are for a computer's
// screen: none of them is an @media for print, or for a screen no wider
// than so much.
func forComputers(open []cssBlock) bool {
	for _, b := range open {
		q := strings.ToLower(b.prelude)
		if strings.Contains(q, "max-width") || strings.Contains(q, "max-device-width") || strings.Contains(q, "print") {
			return false
		}
	}
	return true
}

// ruleDisplay returns what a rule with the selectors and declarations does
// to display, for each of its selectors that is simple, or nothing if it
// sets no display.
func ruleDisplay(selectors, declarations string) []displayRule {
	var value string
	var important, found bool
	for decl := range strings.SplitSeq(declarations, ";") {
		k, v, ok := strings.Cut(decl, ":")
		if ok && strings.EqualFold(strings.TrimSpace(k), "display") {
			value, important = cssValue(v)
			found = true
		}
	}
	if !found {
		return nil
	}
	var rules []displayRule
	for selector := range strings.SplitSeq(selectors, ",") {
		m := simpleSelector.FindStringSubmatch(strings.TrimSpace(selector))
		if m == nil {
			continue
		}
		rules = append(rules, displayRule{
			tag:       strings.ToLower(m[1]),
			classes:   strings.Split(m[2][1:], "."),
			value:     value,
			important: important,
		})
	}
	return rules
}

// display returns what the display of the element is on a computer's
// screen, by its style and the rules, and whether it is set at all. An
// important rule wins over the style, which wins over a rule that is not,
// and the last rule over those before it.
func display(t html.Token, rules []displayRule) (string, bool) {
	var classes []string
	for _, a := range t.Attr {
		if a.Key == "class" {
			classes = strings.Fields(a.Val)
		}
	}
	var normal, important string
	var hasNormal, hasImportant bool
	for _, r := range rules {
		if r.tag != "" && r.tag != t.Data || !containsAll(classes, r.classes) {
			continue
		}
		if r.important {
			important, hasImportant = r.value, true
		} else {
			normal, hasNormal = r.value, true
		}
	}
	if hasImportant {
		return important, true
	}
	if v, ok := styleValue(t, "display"); ok {
		return v, true
	}
	return normal, hasNormal
}

// containsAll reports if the classes of an element include all of those.
func containsAll(classes, those []string) bool {
	for _, c := range those {
		if !slices.Contains(classes, c) {
			return false
		}
	}
	return true
}
