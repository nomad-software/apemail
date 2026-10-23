package icons_test

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nomad-software/apemail/internal/icons"
)

// TestStyle checks that every icon is drawn in the one style: an outline on
// a grid of 24, 1.5 wide with round ends and corners, in the colour of the
// text, with nothing filled but in that colour.
func TestStyle(t *testing.T) {
	for _, name := range icons.Names {
		data, err := icons.SVG(name)
		if err != nil {
			t.Fatal(err)
		}
		d := xml.NewDecoder(strings.NewReader(string(data)))
		root := true
		for {
			tok, err := d.Token()
			if err != nil {
				break
			}
			el, ok := tok.(xml.StartElement)
			if !ok {
				continue
			}
			attrs := map[string]string{}
			for _, a := range el.Attr {
				attrs[a.Name.Local] = a.Value
			}
			if root {
				root = false
				want := map[string]string{"viewBox": "0 0 24 24", "fill": "none", "stroke": "currentColor",
					"stroke-width": "1.5", "stroke-linecap": "round", "stroke-linejoin": "round"}
				for k, v := range want {
					if attrs[k] != v {
						t.Errorf("%s: %s = %q, want %q", name, k, attrs[k], v)
					}
				}
				continue
			}
			if !slices.Contains([]string{"path", "rect", "circle"}, el.Name.Local) {
				t.Errorf("%s: a %s", name, el.Name.Local)
			}
			for _, k := range []string{"stroke", "stroke-width", "stroke-linecap", "stroke-linejoin", "style", "transform"} {
				if _, ok := attrs[k]; ok {
					t.Errorf("%s: a %s has its own %s", name, el.Name.Local, k)
				}
			}
			if f, ok := attrs["fill"]; ok && f != "currentColor" {
				t.Errorf("%s: filled with %q", name, f)
			}
		}
	}
}

// TestNames checks that every icon is named, and every name has an icon.
func TestNames(t *testing.T) {
	entries, err := os.ReadDir("svg")
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, e := range entries {
		files = append(files, strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())))
	}
	slices.Sort(files)
	names := slices.Clone(icons.Names)
	slices.Sort(names)
	if !slices.Equal(files, names) {
		t.Errorf("files %q, names %q", files, names)
	}
	if _, err := icons.SVG("nothing"); err == nil {
		t.Error("an icon that is not there was found")
	}
}
