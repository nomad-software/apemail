package message_test

import (
	"strings"
	"testing"

	"github.com/nomad-software/apemail/internal/message"
)

// parseHTML returns the HTML of a message that is the HTML given.
func parseHTML(t *testing.T, body string) string {
	t.Helper()
	m, err := message.Parse([]byte("From: a@example.com\r\nContent-Type: text/html; charset=utf-8\r\n\r\n" + body))
	if err != nil {
		t.Fatal(err)
	}
	return m.HTML
}

// nested returns tables nested n deep, with the text in the innermost cell.
func nested(n int, text string) string {
	return strings.Repeat(`<table width="100%"><tbody><tr><td align="center">`, n) + text +
		strings.Repeat("</td></tr></tbody></table>", n)
}

// TestTablesKept checks that tables no deeper than Qt lays out quickly are
// left as they are.
func TestTablesKept(t *testing.T) {
	in := nested(4, "Hello") + `<table><tr><th>A</th><td>B</td></tr></table>`
	if got := parseHTML(t, in); got != in {
		t.Errorf("the tables were changed:\n%s", got)
	}
}

// TestDeepTablesFlattened checks that tables nested deeper than four are
// made blocks, their rows and cells too, so that Qt does not take minutes
// to lay them out, and that what is in them and the tables around them are
// kept.
func TestDeepTablesFlattened(t *testing.T) {
	got := parseHTML(t, nested(13, `<p style="color: red">Hello</p>`)+"<p>After</p>")
	want := nested(4, strings.Repeat("<div><div><div>", 9)+`<p style="color: red">Hello</p>`+strings.Repeat("</div></div></div>", 9)) + "<p>After</p>"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

// TestFlattenedTablesSideBySide checks that the depth is of tables within
// tables, not of how many there are: tables beside each other, each four
// deep, are all kept.
func TestFlattenedTablesSideBySide(t *testing.T) {
	in := `<table><tr><td>` + strings.Repeat(nested(3, "x"), 5) + `</td></tr></table>`
	if got := parseHTML(t, in); got != in {
		t.Errorf("the tables were changed:\n%s", got)
	}
}

// TestFlattenedTablesNotInComments checks that tables in comments, as those
// newsletters write for Outlook (<!--[if mso]><table>), in a style or in
// text are not counted or changed, and that an end of a table with no
// start does not let the next tables go deeper.
func TestFlattenedTablesNotInComments(t *testing.T) {
	in := strings.Repeat(`<!--[if mso]><table><tr><td><![endif]-->`, 8) +
		`<style>td::before { content: "<table>" }</style>&lt;table&gt;</table></table>` +
		nested(4, "Hello")
	if got := parseHTML(t, in); got != in {
		t.Errorf("the HTML was changed:\n%s", got)
	}
}
