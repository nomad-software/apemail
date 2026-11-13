// Package homedir finds the folders of the user's own: their home folder,
// for paths as a user writes them in the settings, where a path can start
// with ~ for it, and the place the system keeps the data of programs in.
package homedir

import (
	"os"
	"path/filepath"
	"strings"
)

// Expand returns a path with the ~ it starts with, if it does, replaced by
// the home folder of the user. A ~ followed by anything but a separator, as
// in ~user, is left as it is.
func Expand(path string) string {
	rest, ok := strings.CutPrefix(path, "~")
	if !ok || (rest != "" && !os.IsPathSeparator(rest[0])) {
		return path
	}
	dir, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(dir, rest)
}

// DataHome returns the folder the system keeps the data of the user's
// programs in: $XDG_DATA_HOME, or ~/.local/share without it. By the XDG base
// directory specification, a path that is not absolute is ignored.
func DataHome() (string, error) {
	if dir := os.Getenv("XDG_DATA_HOME"); filepath.IsAbs(dir) {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share"), nil
}
