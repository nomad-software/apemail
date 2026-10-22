package settings

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/nomad-software/apemail/internal/homedir"
)

// rename renames a folder. Tests change it, to move as between file
// systems.
var rename = os.Rename

// AccountsDir returns the folder the accounts and their mail are kept in:
// the one the settings name, or def if they name none.
func (s Settings) AccountsDir(def string) string {
	if s.DataDir == "" {
		return def
	}
	return filepath.Clean(homedir.Expand(s.DataDir))
}

// MoveDir moves the folder from, and all in it, to the folder to, which
// must not exist or must be empty, so that nothing there is overwritten or
// mixed in. Within a file system the folder is renamed; between file
// systems it is copied, and the original removed only once all of it has
// been. A from that does not exist leaves to made and empty.
func MoveDir(from, to string) error {
	from, err := filepath.Abs(homedir.Expand(from))
	if err != nil {
		return err
	}
	to, err = filepath.Abs(homedir.Expand(to))
	if err != nil {
		return err
	}
	if from == to {
		return nil
	}
	if within(to, from) || within(from, to) {
		return fmt.Errorf("%s and %s are within each other", from, to)
	}

	if entries, err := os.ReadDir(to); err == nil {
		if len(entries) > 0 {
			return fmt.Errorf("%s is not empty", to)
		}
		// An empty folder is replaced, which a rename needs.
		if err := os.Remove(to); err != nil {
			return err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	if _, err := os.Stat(from); errors.Is(err, fs.ErrNotExist) {
		return os.MkdirAll(to, 0o700)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		return err
	}

	err = rename(from, to)
	if err == nil || !errors.Is(err, syscall.EXDEV) {
		return err
	}
	if err := copyDir(from, to); err != nil {
		// What was copied is taken away, leaving the original as it was.
		return errors.Join(err, os.RemoveAll(to))
	}
	return os.RemoveAll(from)
}

// within reports if path is inside dir.
func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// copyDir copies the folder from, and all in it, to the folder to, keeping
// the permissions of each file and folder.
func copyDir(from, to string) error {
	return filepath.WalkDir(from, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		target := filepath.Join(to, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(target, info.Mode().Perm())
		case d.Type().IsRegular():
			return copyFile(path, target, info.Mode().Perm())
		}
		return fmt.Errorf("%s is neither a file nor a folder", path)
	})
}

// copyFile copies the file from to the file to, with the permissions.
func copyFile(from, to string, perm fs.FileMode) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
