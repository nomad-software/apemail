package message_test

import "testing"

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
