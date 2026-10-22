package message_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomad-software/apemail/internal/message"
)

// write writes a file in dir and returns its path.
func write(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTextSignature(t *testing.T) {
	sig, err := message.LoadSignature("Ada <ada@example.com>\nAnalyst\n\n", "")
	if err != nil {
		t.Fatal(err)
	}
	if sig.Text != "Ada <ada@example.com>\nAnalyst" || len(sig.Images) != 0 {
		t.Errorf("signature = %+v", sig)
	}
	// As HTML it is escaped, line for line.
	if !strings.Contains(sig.HTML, "Ada &lt;ada@example.com&gt;<br>Analyst") {
		t.Errorf("html = %q", sig.HTML)
	}

	if sig, err := message.LoadSignature("\n", ""); err != nil || sig.Text != "" || sig.HTML != "" {
		t.Errorf("no signature = %+v, %v", sig, err)
	}
}

func TestTextFileSignature(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	write(t, home, "sig.txt", "From the file\n")

	// The file is used rather than the text, named from the home folder.
	sig, err := message.LoadSignature("Not this", "~/sig.txt")
	if err != nil {
		t.Fatal(err)
	}
	if sig.Text != "From the file" || !strings.Contains(sig.HTML, "From the file") {
		t.Errorf("signature = %+v", sig)
	}
}

func TestHTMLFileSignature(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "images/logo.png", "PNG logo")
	elsewhere := write(t, t.TempDir(), "badge.gif", "GIF badge")
	path := write(t, dir, "sig.HTML", `<!DOCTYPE html><html><head><style>p { color: red }</style></head>
<body class="x"><p><b>Ada</b> Lovelace</p>
<img src="images/logo.png" alt="logo"> <img alt='badge' src='file://`+elsewhere+`'>
<img src="https://example.com/remote.png"> <img src="data:image/png;base64,AAAA"> <img src="cid:already@there">
</body></html>`)

	sig, err := message.LoadSignature("", path)
	if err != nil {
		t.Fatal(err)
	}
	// Only what is within the body is taken.
	if strings.Contains(sig.HTML, "<head") || strings.Contains(sig.HTML, "color: red") || !strings.HasPrefix(sig.HTML, "<p><b>Ada</b> Lovelace</p>") {
		t.Errorf("html = %q", sig.HTML)
	}
	if !strings.Contains(sig.Text, "Ada Lovelace") || strings.Contains(sig.Text, "<") {
		t.Errorf("text = %q", sig.Text)
	}

	// The images on this computer go with the message.
	if len(sig.Images) != 2 {
		t.Fatalf("images = %+v", sig.Images)
	}
	logo, badge := sig.Images[0], sig.Images[1]
	if logo.Filename != "logo.png" || logo.ContentType != "image/png" || string(logo.Data) != "PNG logo" || !logo.Inline {
		t.Errorf("logo = %+v", logo)
	}
	if badge.Filename != "badge.gif" || string(badge.Data) != "GIF badge" || badge.ContentID == logo.ContentID {
		t.Errorf("badge = %+v", badge)
	}
	for _, want := range []string{`src="cid:` + logo.ContentID + `"`, `src='cid:` + badge.ContentID + `'`} {
		if !strings.Contains(sig.HTML, want) {
			t.Errorf("no %s in %q", want, sig.HTML)
		}
	}
	// The others are left as they are.
	for _, want := range []string{"https://example.com/remote.png", "data:image/png;base64,AAAA", "cid:already@there"} {
		if !strings.Contains(sig.HTML, want) {
			t.Errorf("no %s in %q", want, sig.HTML)
		}
	}
}

// TestHTMLFragmentSignature checks that HTML without a body is taken whole.
func TestHTMLFragmentSignature(t *testing.T) {
	path := write(t, t.TempDir(), "sig.htm", "<i>Ada</i>\n")
	if sig, err := message.LoadSignature("", path); err != nil || sig.HTML != "<i>Ada</i>" || sig.Text != "Ada" {
		t.Errorf("signature = %+v, %v", sig, err)
	}
}

func TestSignatureErrors(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"missing file":  filepath.Join(dir, "missing.txt"),
		"too large":     write(t, dir, "large.txt", strings.Repeat("x", 2<<20)),
		"missing image": write(t, dir, "a.html", `<img src="nowhere.png">`),
		"not an image":  write(t, dir, "b.html", `<img src="notes.txt">`),
	}
	write(t, dir, "notes.txt", "not a picture")
	for name, path := range cases {
		if _, err := message.LoadSignature("", path); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
