package message_test

import (
	"testing"

	"github.com/nomad-software/apemail/internal/message"
)

// TestHiddenLeftOut checks that elements whose style hides them are left
// out with what is in them, as a newsletter's preview text, the rows it
// shows only on a phone and its tracking pixel, and that what is around
// them is kept.
func TestHiddenLeftOut(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"preview text",
			`<div style="display:none;font-size:1px">Preview <b>text</b></div><p>Hello</p>`,
			`<p>Hello</p>`},
		{"important",
			`<p>A</p><span style="color: red; DISPLAY : None !important">B</span><p>C</p>`,
			`<p>A</p><p>C</p>`},
		{"tracking pixel",
			`<p>Hello</p><img alt="" src="http://example.com/t.gif" style="display: none; width: 1px;"><img src=x style=display:none />`,
			`<p>Hello</p>`},
		{"row for a phone",
			`<table><tr><td>Fridge</td></tr><!--[if !mso]><!--><tr class=m style=display:none><td colspan=3>Fridge</td></tr><!--<![endif]--><tr><td>Milk</td></tr></table>`,
			`<table><tr><td>Fridge</td></tr><!--[if !mso]><!--><!--<![endif]--><tr><td>Milk</td></tr></table>`},
		{"table within",
			`<table><tr style="display:none"><td><table><tr><td>A</td></tr><tr><td>B</td></tr></table></td></tr><tr><td>C</td></tr></table>`,
			`<table><tr><td>C</td></tr></table>`},
		{"row not ended",
			`<table><tr style="display:none"><td>A<td>B<tr><td>C</table><p>D</p>`,
			`<table><tr><td>C</table><p>D</p>`},
		{"last row not ended",
			`<table><tr><td>A<tr style="display:none"><td>B</table><p>C</p>`,
			`<table><tr><td>A</table><p>C</p>`},
		{"paragraph not ended",
			`<div><p style="display:none">A<p>B</div><p style="display:none">C<table><tr><td>D</td></tr></table>`,
			`<div><p>B</div><table><tr><td>D</td></tr></table>`},
		{"terms not ended",
			`<dl><dt style="display:none">A<dd>B<dt>C<dd style="display:none">D<dt>E</dl>`,
			`<dl><dd>B<dt>C<dt>E</dl>`},
		{"options not ended",
			`<select><option style="display:none">A<option>B</select>`,
			`<select><option>B</select>`},
		{"next hidden too",
			`<ul><li style="display:none">A<li style="display:none">B<li>C</ul>`,
			`<ul><li>C</ul>`},
		{"same within",
			`<div style="display:none"><div>A</div><div>B</div></div><div>C</div>`,
			`<div>C</div>`},
		{"shown",
			`<div style="display:block">A</div><p style="color:red">B</p><!-- <div style="display:none"> --><p>C</p>`,
			`<div style="display:block">A</div><p style="color:red">B</p><!-- <div style="display:none"> --><p>C</p>`},
	} {
		if got := parseHTML(t, c.in); got != c.want {
			t.Errorf("%s: got\n%s\nwant\n%s", c.name, got, c.want)
		}
	}
}

// TestSizesInPixels checks that the sizes of images, tables and cells are
// made numbers Qt reads, taken from the style where the attributes have
// none, and that those it reads already, and other elements, are left as
// they were.
func TestSizesInPixels(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"auto height",
			`<img alt="" src="https://example.com/a.png" style="height:auto;width:100%;" width="600" height="auto">`,
			`<img alt="" src="https://example.com/a.png" style="height:auto;width:100%;" width="600">`},
		{"pixels",
			`<img alt=Tesco height=35px src=https://example.com/logo.png width=120.4px>`,
			`<img alt="Tesco" height="35" src="https://example.com/logo.png" width="120">`},
		{"from the style",
			`<img src="a.png" style="width: 32PX; height:32px; display:block">`,
			`<img src="a.png" style="width: 32PX; height:32px; display:block" width="32" height="32">`},
		{"percentage of an image",
			`<img src="a.png" width="100%" style="width:50%">`,
			`<img src="a.png" style="width:50%">`},
		{"table",
			`<table style=border:none;width:600px width=600px><tr><td style=width:44px;height:32px width=44px>A</td><td style="width:100%">B</td></tr></table>`,
			`<table style="border:none;width:600px" width="600"><tr><td style="width:44px;height:32px" width="44">A</td><td style="width:100%" width="100%">B</td></tr></table>`},
		{"kept",
			`<table width="100%" style="width:600px"><tr><td width="28">A</td><th>B</th></tr></table><img src="a.png" width="1" height="1"><div style="width:600px">C</div>`,
			`<table width="100%" style="width:600px"><tr><td width="28">A</td><th>B</th></tr></table><img src="a.png" width="1" height="1"><div style="width:600px">C</div>`},
		{"nonsense",
			`<td width="wide" style="width:auto">A</td><img src="a.png" height="-3" width="NaN">`,
			`<td style="width:auto">A</td><img src="a.png">`},
	} {
		if got := parseHTML(t, c.in); got != c.want {
			t.Errorf("%s: got\n%s\nwant\n%s", c.name, got, c.want)
		}
	}
}

// TestSourceHTMLKept checks that the HTML of a message is kept as it is,
// for a web browser, beside that made fit for Qt.
func TestSourceHTMLKept(t *testing.T) {
	body := `<div style="display:none">Preview</div><img src="a.png" width="600px" height="auto">` + nested(6, "Hello")
	m, err := message.Parse([]byte("From: a@example.com\r\nContent-Type: text/html; charset=utf-8\r\n\r\n" + body))
	if err != nil {
		t.Fatal(err)
	}
	if m.SourceHTML != body {
		t.Errorf("the HTML as the message has it is\n%s\nwant\n%s", m.SourceHTML, body)
	}
	if m.HTML == body {
		t.Error("the HTML shown was not made fit for Qt")
	}
}

// TestHiddenByStylesheet checks that what the stylesheet of a message hides
// or shows on a computer's screen, by the classes of an element, is left
// out or shown: an important rule over the element's style, its style over
// a rule that is not, and rules for a phone, for print, in comments or of
// selectors for another context ignored.
func TestHiddenByStylesheet(t *testing.T) {
	for _, c := range []struct{ name, style, in, want string }{
		{"shown on a computer, not in Outlook",
			`@media only screen and (min-width:505px){ .ee-show-on-desktop{display:block!important} .ee_columns.ee-hide-on-desktop{display:none!important} }` +
				`#MessageViewBody .ee-show-on-desktop{display:none!important} [owa] .ee-hide-on-desktop{display:block!important}` +
				`@media screen and (max-width:504px){ body .ee-show-on-desktop{display:none!important} .ee-hide-on-desktop{display:block!important} }`,
			`<table class="row ee-show-on-desktop" style="display: none;min-width: 100%"><tr><td>Computer</td></tr></table>` +
				`<table class="row ee_columns ee-hide-on-desktop" style="min-width:100%"><tr><td>Phone</td></tr></table><p>After</p>`,
			`<table class="row ee-show-on-desktop" style="min-width: 100%"><tr><td>Computer</td></tr></table><p>After</p>`},
		{"shown on a phone",
			`@media only screen and (max-width: 630px) { .shown-on-mobile { display: table-row !important; } }`,
			`<table><tr><td>Fridge</td></tr><tr class=shown-on-mobile style=display:none><td>Fridge</td></tr></table>`,
			`<table><tr><td>Fridge</td></tr></table>`},
		{"preview text",
			`.mcnPreviewText{ display:none !important; }`,
			`<span class="mcnPreviewText">Preview</span><p>Hello</p>`,
			`<p>Hello</p>`},
		{"style over a rule",
			`.a{display:none} .b{display:block}`,
			`<div class="a" style="display:block">A</div><div class="b" style="display:none">B</div><div class="a">C</div><p>D</p>`,
			`<div class="a" style="display:block">A</div><p>D</p>`},
		{"tag",
			`table.x, td.missing { display: none }`,
			`<div class="x">A</div><table class="x"><tr><td>B</td></tr></table>`,
			`<div class="x">A</div>`},
		{"stray end of a block",
			`} .g{display:none} }`,
			`<p class="g">G</p><p>H</p>`,
			`<p>H</p>`},
		{"comments and print",
			`/* .c{display:none} */ <!-- .e{display:none} --> @media print { .d{display:none} } @import url(x.css); .f{display:none}`,
			`<p class="c">C</p><p class="d">D</p><p class="e">E</p><p class="f">F</p>`,
			`<p class="c">C</p><p class="d">D</p>`},
	} {
		got := parseHTML(t, "<style>"+c.style+"</style>"+c.in)
		if want := "<style>" + c.style + "</style>" + c.want; got != want {
			t.Errorf("%s: got\n%s\nwant\n%s", c.name, got, want)
		}
	}
}

// TestSizesEmptyOrImportant checks that a size that is empty, which Qt reads
// as 0, is left out, that one marked important in the style is read, and
// that one so large it is nonsense is left out.
func TestSizesEmptyOrImportant(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"empty", `<img src="a.png" width="" height="">`, `<img src="a.png">`},
		{"important", `<img src="a.png" style="width: 30px !important">`, `<img src="a.png" style="width: 30px !important" width="30">`},
		{"auto", `<img src="a.png" width="16" height="16" style="height: auto !important">`, `<img src="a.png" width="16" style="height: auto !important">`},
		{"nonsense", `<table width="1e300"><tr><td width="99999999999">A</td></tr></table>`, `<table><tr><td>A</td></tr></table>`},
	} {
		if got := parseHTML(t, c.in); got != c.want {
			t.Errorf("%s: got\n%s\nwant\n%s", c.name, got, c.want)
		}
	}
}
