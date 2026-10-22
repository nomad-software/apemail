// Package settings stores the settings of the client, which are kept between
// runs in a JSON file that is not encrypted, as it names the keys everything
// else is encrypted with. It has no dependency on the UI.
package settings

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"

	"github.com/nomad-software/apemail/internal/jsonfile"
)

const (
	defaultWidth         = 1280
	defaultHeight        = 800
	defaultComposeWidth  = 800
	defaultComposeHeight = 650
	minSize              = 200
)

// Settings is the state that is saved to disk.
type Settings struct {
	// Width and Height are the size of the window when it is not maximised.
	Width     int  `json:"width"`
	Height    int  `json:"height"`
	Maximized bool `json:"maximized"`

	// ComposeWidth and ComposeHeight are the size of the window a message
	// is written in, as the last one was left when it was not maximised.
	ComposeWidth  int `json:"composeWidth"`
	ComposeHeight int `json:"composeHeight"`

	// SidebarWidth and ListHeight are the sizes the folder tree and the
	// message list were dragged to. The default layout is used if one is
	// zero.
	SidebarWidth int `json:"sidebarWidth,omitempty"`
	ListHeight   int `json:"listHeight,omitempty"`

	// CorrespondentWidth and DateWidth are the widths the columns of the
	// message list of who a message is from (or to) and its date were
	// dragged to. Every folder shares them. The default is used if one is
	// zero.
	CorrespondentWidth int `json:"correspondentWidth,omitempty"`
	DateWidth          int `json:"dateWidth,omitempty"`

	// Expanded says which items of the folder tree are open and which
	// closed, by the ID of the account and then by FolderKey of the folder,
	// which is "" for the account itself. Those that are not listed are as
	// they start: accounts open, and folders closed.
	Expanded map[string]map[string]bool `json:"expanded,omitempty"`

	// PublicKey and PrivateKey are the paths of the files of a pair of SSH
	// keys, which the accounts and the mail kept of them are encrypted
	// with. Nothing is loaded or saved until there are keys.
	PublicKey  string `json:"publicKey,omitempty"`
	PrivateKey string `json:"privateKey,omitempty"`

	// DataDir is the folder the accounts and their mail are kept in. The
	// default place is used if it is empty. It can start with ~ for the
	// home folder.
	DataDir string `json:"dataDir,omitempty"`
}

// FolderKey returns what a folder is known by in Expanded: a hash of its
// name, as this file is not encrypted and names of folders can say what the
// mail in them is about.
func FolderKey(folder string) string {
	if folder == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(folder))
	return hex.EncodeToString(sum[:12])
}

// appName is the name of the folders the settings and data are kept in.
const appName = "apemail"

// fileName is the name of the settings file.
const fileName = "settings.json"

// Path returns where the settings file is kept: in a folder for the program
// in the place the system has for the configuration of programs, which is
// ~/.config on Linux (or $XDG_CONFIG_HOME).
func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, appName, fileName), nil
}

// DataDir returns the folder the accounts and their mail are kept in: a
// folder for the program in the place the system has for the data of
// programs, which is ~/.local/share on Linux (or $XDG_DATA_HOME).
func DataDir() (string, error) {
	switch runtime.GOOS {
	case "darwin", "ios", "windows", "plan9":
		// These keep data where they keep configuration.
		dir, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(dir, appName), nil
	}

	// The rest follow the XDG base directory specification, in which a
	// path that is not absolute is to be ignored.
	if dir := os.Getenv("XDG_DATA_HOME"); filepath.IsAbs(dir) {
		return filepath.Join(dir, appName), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", appName), nil
}

// Prepare makes the folder the settings file at path is kept in.
func Prepare(path string) error {
	return os.MkdirAll(filepath.Dir(path), 0o700)
}

// Load reads the settings from the JSON file at path. Defaults are used if
// the file is missing or unreadable, and for any value that is unusable.
func Load(path string) Settings {
	var s Settings
	if err := jsonfile.Load(path, &s); err != nil {
		s = Settings{}
	}
	if s.Width < minSize || s.Height < minSize {
		s.Width, s.Height = defaultWidth, defaultHeight
	}
	if s.ComposeWidth < minSize || s.ComposeHeight < minSize {
		s.ComposeWidth, s.ComposeHeight = defaultComposeWidth, defaultComposeHeight
	}
	s.SidebarWidth = max(s.SidebarWidth, 0)
	s.ListHeight = max(s.ListHeight, 0)
	s.CorrespondentWidth = max(s.CorrespondentWidth, 0)
	s.DateWidth = max(s.DateWidth, 0)
	if s.Expanded == nil {
		s.Expanded = map[string]map[string]bool{}
	}
	return s
}

// Save writes the settings to the JSON file at path.
func (s Settings) Save(path string) error {
	return jsonfile.Save(path, s)
}
