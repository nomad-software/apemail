package ui

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"

	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/mainthread"

	"github.com/nomad-software/apemail/internal/account"
	"github.com/nomad-software/apemail/internal/crypt"
	"github.com/nomad-software/apemail/internal/engine"
	"github.com/nomad-software/apemail/internal/jsonfile"
	"github.com/nomad-software/apemail/internal/settings"
	"github.com/nomad-software/apemail/internal/store"
)

// needKeys says what is wrong when the settings have no keys.
const needKeys = "ApeMail needs a pair of SSH keys to encrypt your accounts and mail with. " +
	"Until they are chosen in File > Settings, nothing is loaded or saved."

// askHidden asks for a passphrase or password in a dialog which does not
// show what is typed. It is an error if the user cancels.
func askHidden(parent *qt.QWidget, title, label string) (string, error) {
	ok := false
	value := qt.QInputDialog_GetText4(parent, title, label, qt.QLineEdit__Password, "", &ok)
	if !ok {
		return "", errors.New("none was given")
	}
	return value, nil
}

// openKeysOf opens the keys the paths name, asking for the passphrase of the
// private key if it has one.
func openKeysOf(parent *qt.QWidget, public, private string) (*crypt.Keys, error) {
	return crypt.Open(public, private, func() ([]byte, error) {
		passphrase, err := askHidden(parent, "Private Key", "Passphrase of "+private)
		if err != nil {
			return nil, errors.New("no passphrase was given")
		}
		return []byte(passphrase), nil
	})
}

// openKeys opens the keys the settings name and starts the accounts, or
// asks for keys if there are none.
func (w *Window) openKeys() {
	if w.config.PublicKey == "" || w.config.PrivateKey == "" {
		qt.QMessageBox_Information(w.QWidget, "Keys Needed", needKeys)
		w.editSettings()
		return
	}
	keys, err := openKeysOf(w.QWidget, w.config.PublicKey, w.config.PrivateKey)
	if err != nil {
		w.warn("Keys", fmt.Errorf("the keys of the settings cannot be used: %w\n\n%s", err, needKeys))
		return
	}
	w.keys = keys
	w.loadAddressBook()
	w.loadAccounts()
}

// loadAccounts reads the accounts and starts the work of each. A new
// account is asked for if there are none.
func (w *Window) loadAccounts() {
	accounts, err := account.List(w.accountsDir(), w.keys)
	if err != nil {
		w.warn("Accounts", fmt.Errorf("some accounts could not be read: %w", err))
	}
	for _, a := range accounts {
		w.startAccount(a)
	}
	w.updateActions()
	if len(accounts) == 0 && err == nil {
		w.editAccountDialog(account.New(), true)
	}
}

// startAccount starts the work of an account, replacing any there was.
func (w *Window) startAccount(a account.Account) {
	if old := w.engines[a.ID]; old != nil {
		old.Close()
	}
	st, err := store.Open(account.CacheDir(w.accountsDir(), a.ID), w.keys)
	if err != nil {
		w.showError(err)
		return
	}
	e := engine.New(a, st, w, mainthread.Start)
	w.engines[a.ID] = e
	if !slices.Contains(w.order, a.ID) {
		w.order = append(w.order, a.ID)
	}
	w.folders.setAccount(a)
	e.Start()
}

// editCurrentAccount edits the account that is selected.
func (w *Window) editCurrentAccount() {
	if e := w.currentEngine(); e != nil {
		w.editAccountDialog(e.Account(), false)
	}
}

// editAccountDialog shows the settings of an account, and saves and restarts
// it if they are changed.
func (w *Window) editAccountDialog(a account.Account, isNew bool) {
	if w.keys == nil {
		qt.QMessageBox_Information(w.QWidget, "Keys Needed", needKeys)
		return
	}
	var folders []string
	if e := w.engines[a.ID]; e != nil {
		for _, f := range e.Folders() {
			if f.Selectable() {
				folders = append(folders, f.Name)
			}
		}
	}
	newAccountDialog(w.QWidget, a, isNew, folders, func(changed account.Account) {
		if err := account.Save(w.accountsDir(), changed, w.keys); err != nil {
			w.warn("Account", fmt.Errorf("the account could not be saved: %w", err))
			return
		}
		w.startAccount(changed)
		w.updateActions()
	})
}

// removeCurrentAccount removes the account that is selected from the client,
// once the user agrees. Its mailbox on the server is not touched.
func (w *Window) removeCurrentAccount() {
	e := w.currentEngine()
	if e == nil {
		return
	}
	a := e.Account()
	if !confirm(w.QWidget, "Remove Account",
		fmt.Sprintf("Remove %s from ApeMail?\n\nIts settings and the copy of its mail kept on this computer are deleted, "+
			"along with any mail waiting in its outbox. The mailbox on the server is not touched.", a.Title())) {
		return
	}
	e.Close()
	delete(w.engines, a.ID)
	delete(w.statuses, a.ID)
	for i, id := range w.order {
		if id == a.ID {
			w.order = append(w.order[:i], w.order[i+1:]...)
			break
		}
	}
	w.folders.removeAccount(a.ID)
	if w.account == a.ID {
		w.account, w.folder = "", ""
		w.list.clear()
		w.view.clear()
	}
	if err := account.Remove(w.accountsDir(), a.ID); err != nil {
		w.warn("Remove Account", err)
	}
	w.updateActions()
	w.showStatus()
}

// accountsDir returns the folder the accounts and their mail are kept in.
func (w *Window) accountsDir() string {
	return w.config.AccountsDir(w.files.Accounts)
}

// editSettings shows the settings, and puts what is changed into effect.
func (w *Window) editSettings() {
	newSettingsDialog(w.QWidget, w.config, w.files.Accounts, func(changed settings.Settings) {
		w.applyInterface(changed)
		keysChanged := changed.PublicKey != w.config.PublicKey || changed.PrivateKey != w.config.PrivateKey || w.keys == nil
		from, to := w.accountsDir(), changed.AccountsDir(w.files.Accounts)
		dirChanged := filepath.Clean(from) != filepath.Clean(to)
		if !keysChanged && !dirChanged {
			return
		}

		var keys *crypt.Keys
		if keysChanged {
			k, err := openKeysOf(w.QWidget, changed.PublicKey, changed.PrivateKey)
			if err != nil {
				w.warn("Keys", err)
				return
			}
			keys = k
		}
		if dirChanged && !confirm(w.QWidget, "Move Accounts and Mail",
			fmt.Sprintf("Move your accounts and the copy of your mail from\n%s\nto\n%s?", from, to)) {
			return
		}
		w.applySettings(changed, keys, from, to, dirChanged)
	})
}

// applyInterface puts the fonts and the theme of the settings into effect,
// in the windows that are open as in those opened later.
func (w *Window) applyInterface(changed settings.Settings) {
	fonts := changed.CodeFont != w.config.CodeFont || changed.CodeFontSize != w.config.CodeFontSize
	theme := changed.Theme != w.config.Theme
	if !fonts && !theme {
		return
	}
	w.config.CodeFont, w.config.CodeFontSize, w.config.Theme = changed.CodeFont, changed.CodeFontSize, changed.Theme
	w.saveConfig()
	if fonts {
		w.setSourceFont()
	}
	if theme {
		applyTheme(w.config.Theme)
	}
}

// applySettings puts new keys and a new folder for the accounts into effect.
// The accounts are stopped while the files are moved and encrypted with the
// new keys, which is done away from the UI's thread, and started again
// after. keys is nil if they have not changed.
func (w *Window) applySettings(changed settings.Settings, keys *crypt.Keys, from, to string, move bool) {
	old := w.keys
	w.stopAccounts()
	w.closeAddressBook()
	w.Status("settings", "Applying the settings…")
	go func() {
		var failed []error
		moved := !move
		if move {
			if err := settings.MoveDir(from, to); err != nil {
				failed = append(failed, fmt.Errorf("the accounts were not moved to %s: %w", to, err))
			} else {
				moved = true
			}
		}
		dir := from
		if moved {
			dir = to
		}
		if keys != nil && old != nil {
			if err := convertDir(dir, old, keys); err != nil {
				failed = append(failed, fmt.Errorf("some files could not be encrypted with the new keys: %w", err))
			}
		}
		mainthread.Start(func() {
			w.Status("settings", "")
			if keys != nil {
				w.keys = keys
				w.config.PublicKey, w.config.PrivateKey = changed.PublicKey, changed.PrivateKey
			}
			if moved {
				w.config.DataDir = changed.DataDir
			}
			w.saveSettings()
			if err := errors.Join(failed...); err != nil {
				w.warn("Settings", err)
			}
			if w.keys != nil {
				w.loadAddressBook()
				w.loadAccounts()
			}
		})
	}()
}

// stopAccounts stops the work of every account and clears what is shown of
// them.
func (w *Window) stopAccounts() {
	for _, e := range w.engines {
		e.Close()
	}
	w.engines = map[string]*engine.Engine{}
	w.order = nil
	w.account, w.folder = "", ""
	w.folders.clear()
	w.list.clear()
	w.view.clear()
	w.updateActions()
}

// convertDir has every file in dir encrypted with other keys: each is
// decrypted with the keys there were and encrypted with those given.
func convertDir(dir string, from, to *crypt.Keys) error {
	var failed []error
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil || d.IsDir() {
			return err
		}
		if err := jsonfile.Convert(path, from, to); err != nil {
			failed = append(failed, fmt.Errorf("%s: %w", path, err))
		}
		return nil
	})
	return errors.Join(append(failed, err)...)
}
