package homedir

import (
	"path/filepath"
	"testing"
)

func TestExpand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for path, want := range map[string]string{
		"~":           home,
		"~/.ssh/id":   filepath.Join(home, ".ssh", "id"),
		"~user/a":     "~user/a",
		"/etc/a~":     "/etc/a~",
		"relative/~a": "relative/~a",
	} {
		if got := Expand(path); got != want {
			t.Errorf("Expand(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestDataHome(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/data/home")
	if got, err := DataHome(); err != nil || got != "/data/home" {
		t.Errorf("with XDG_DATA_HOME: %q, %v", got, err)
	}
	// A relative one is ignored, as the specification says.
	t.Setenv("XDG_DATA_HOME", "relative")
	t.Setenv("HOME", "/home/me")
	if got, err := DataHome(); err != nil || got != "/home/me/.local/share" {
		t.Errorf("without XDG_DATA_HOME: %q, %v", got, err)
	}
}
