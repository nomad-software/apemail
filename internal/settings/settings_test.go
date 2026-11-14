package settings

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestLoadAndSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Width != defaultWidth || s.Height != defaultHeight ||
		s.ComposeWidth != defaultComposeWidth || s.ComposeHeight != defaultComposeHeight {
		t.Errorf("defaults = %+v", s)
	}
	s.PublicKey, s.DataDir, s.SidebarWidth = "~/.ssh/id.pub", "~/mail", -5
	s.CorrespondentWidth, s.DateWidth = 240, -1
	s.ComposeWidth, s.ComposeHeight = 700, 500
	s.CodeFont, s.CodeFontSize = "DejaVu Sans Mono", 11
	s.Theme = ThemeDark
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	read, _ := Load(path)
	if read.PublicKey != "~/.ssh/id.pub" || read.DataDir != "~/mail" || read.SidebarWidth != 0 ||
		read.CorrespondentWidth != 240 || read.DateWidth != 0 ||
		read.ComposeWidth != 700 || read.ComposeHeight != 500 ||
		read.CodeFont != "DejaVu Sans Mono" || read.CodeFontSize != 11 || read.Theme != ThemeDark {
		t.Errorf("read = %+v", read)
	}

	// A size too small to use gives the default.
	read.ComposeWidth, read.CodeFontSize, read.Theme = 10, -3, "purple"
	if err := read.Save(path); err != nil {
		t.Fatal(err)
	}
	if s, _ := Load(path); s.ComposeWidth != defaultComposeWidth || s.ComposeHeight != defaultComposeHeight || s.CodeFontSize != 0 || s.Theme != "" {
		t.Errorf("too small = %+v", s)
	}

	// A file that cannot be read gives the defaults, and is kept beside
	// it, so that saving the settings does not lose the keys it names.
	broken := `{"publicKey": "~/.ssh/id.pub",}`
	os.WriteFile(path, []byte(broken), 0o600)
	s, err = Load(path)
	if s.PublicKey != "" || s.Width != defaultWidth {
		t.Errorf("broken = %+v", s)
	}
	if err == nil || !strings.Contains(err.Error(), path+".bad") {
		t.Errorf("err = %v", err)
	}
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	if kept, err := os.ReadFile(path + ".bad"); err != nil || string(kept) != broken {
		t.Errorf("kept %q, %v", kept, err)
	}
}

func TestAccountsDir(t *testing.T) {
	home, _ := os.UserHomeDir()
	if got := (Settings{}).AccountsDir("/default"); got != "/default" {
		t.Errorf("default = %q", got)
	}
	if got := (Settings{DataDir: "~/mail/"}).AccountsDir("/default"); got != filepath.Join(home, "mail") {
		t.Errorf("home = %q", got)
	}
}

// tree writes files in dir, by their paths relative to it.
func tree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// has fails the test unless the files are in dir.
func has(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil || string(data) != body {
			t.Errorf("%s = %q, %v", path, data, err)
		}
		if info, err := os.Stat(path); err == nil && info.Mode().Perm() != 0o600 {
			t.Errorf("%s has permissions %v", path, info.Mode().Perm())
		}
	}
}

var sample = map[string]string{
	"abc.json":           "account",
	"abc/folders.json":   "folders",
	"abc/f-1/index.json": "index",
	"abc/f-1/7.eml":      "message",
}

func TestMoveDir(t *testing.T) {
	from := filepath.Join(t.TempDir(), "accounts")
	tree(t, from, sample)
	to := filepath.Join(t.TempDir(), "new", "place")
	if err := MoveDir(from, to); err != nil {
		t.Fatal(err)
	}
	has(t, to, sample)
	if _, err := os.Stat(from); !os.IsNotExist(err) {
		t.Errorf("the old folder is still there: %v", err)
	}
}

func TestMoveDirBetweenFileSystems(t *testing.T) {
	old := rename
	rename = func(string, string) error { return &os.LinkError{Op: "rename", Err: syscall.EXDEV} }
	t.Cleanup(func() { rename = old })

	from := filepath.Join(t.TempDir(), "accounts")
	tree(t, from, sample)
	to := t.TempDir() // empty, which is allowed
	if err := MoveDir(from, to); err != nil {
		t.Fatal(err)
	}
	has(t, to, sample)
	if _, err := os.Stat(from); !os.IsNotExist(err) {
		t.Errorf("the old folder is still there: %v", err)
	}
}

func TestMoveDirRefuses(t *testing.T) {
	from := filepath.Join(t.TempDir(), "accounts")
	tree(t, from, sample)

	// Into a folder that has something in it, which would be mixed in.
	full := t.TempDir()
	tree(t, full, map[string]string{"other.txt": "mine"})
	if err := MoveDir(from, full); err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Errorf("err = %v", err)
	}
	has(t, full, map[string]string{"other.txt": "mine"})

	// Into itself.
	if err := MoveDir(from, filepath.Join(from, "abc", "deeper")); err == nil {
		t.Error("the folder was moved into itself")
	}
	has(t, from, sample)
}

func TestMoveDirMissing(t *testing.T) {
	to := filepath.Join(t.TempDir(), "new")
	if err := MoveDir(filepath.Join(t.TempDir(), "none"), to); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(to); err != nil || !info.IsDir() {
		t.Errorf("the new folder was not made: %v", err)
	}
}

// TestFolderKey checks that a folder is known by a hash, the same each time
// and another for another folder, which does not give away its name in the
// settings file, which is not encrypted.
func TestFolderKey(t *testing.T) {
	key := FolderKey("Clients/Acme")
	if key == "" || key != FolderKey("Clients/Acme") || key == FolderKey("Clients/Other") {
		t.Errorf("key = %q, and of another folder %q", key, FolderKey("Clients/Other"))
	}
	if strings.Contains(key, "Acme") || strings.Contains(key, "Clients") {
		t.Errorf("key = %q, which names the folder", key)
	}
	if got := FolderKey(""); got != "" {
		t.Errorf("key of no folder = %q", got)
	}
}

// TestPlaces checks where the settings and the data are kept: where the
// desktop says (XDG), or the usual places in the home folder if it does not,
// or says a path that is not absolute, which is to be ignored.
func TestPlaces(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")

	if got, err := Path(); err != nil || got != filepath.Join(home, ".config", "apemail", "settings.json") {
		t.Errorf("settings = %q, err = %v", got, err)
	}
	if got, err := DataDir(); err != nil || got != filepath.Join(home, ".local", "share", "apemail") {
		t.Errorf("data = %q, err = %v", got, err)
	}

	t.Setenv("XDG_DATA_HOME", "relative/data")
	if got, err := DataDir(); err != nil || got != filepath.Join(home, ".local", "share", "apemail") {
		t.Errorf("data with a relative XDG_DATA_HOME = %q, err = %v", got, err)
	}

	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(xdg, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(xdg, "data"))
	path, err := Path()
	if err != nil || path != filepath.Join(xdg, "config", "apemail", "settings.json") {
		t.Errorf("settings = %q, err = %v", path, err)
	}
	if got, err := DataDir(); err != nil || got != filepath.Join(xdg, "data", "apemail") {
		t.Errorf("data = %q, err = %v", got, err)
	}

	// The folder of the settings is made for the user alone, and the
	// settings can then be saved in it.
	if err := Prepare(path); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Dir(path)); err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("mode = %v, err = %v", info.Mode().Perm(), err)
	}
	s, _ := Load(path)
	if err := s.Save(path); err != nil {
		t.Error(err)
	}
}
