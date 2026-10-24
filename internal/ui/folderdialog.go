package ui

import (
	"fmt"
	"slices"
	"strings"

	qt "github.com/mappu/miqt/qt6"

	"github.com/nomad-software/apemail/internal/engine"
	"github.com/nomad-software/apemail/internal/remote"
)

// topLevel is the choice of the new folder dialog that puts a folder among
// the user's own, within no other.
const topLevel = "(Top level)"

// folderDialog asks for the name of a new folder, and what folder it goes
// within.
type folderDialog struct {
	*qt.QDialog
	name   *qt.QLineEdit
	within *qt.QComboBox
	ok     func()
}

// newFolderDialog asks for a folder to make on the server of the account,
// and makes it when OK is pressed. What goes wrong is warned of once the
// server answers.
func (w *Window) newFolderDialog(e *engine.Engine) *folderDialog {
	d := &folderDialog{QDialog: qt.NewQDialog(w.QWidget)}
	d.SetWindowTitle("New Folder")
	layout := qt.NewQVBoxLayout(d.QWidget)
	form := qt.NewQFormLayout2()
	d.name = qt.NewQLineEdit2()
	d.within = qt.NewQComboBox2()
	d.within.AddItem3(topLevel, qt.NewQVariant11(""))
	for _, f := range e.Folders() {
		d.within.AddItem3(f.Name, qt.NewQVariant11(f.Name))
	}
	form.AddRow3("Name:", d.name.QWidget)
	form.AddRow3("Within:", d.within.QWidget)
	layout.AddLayout(form.QLayout)

	buttons := qt.NewQDialogButtonBox5(qt.QDialogButtonBox__Ok|qt.QDialogButtonBox__Cancel, qt.Horizontal)
	layout.AddWidget(buttons.QWidget)
	buttons.OnRejected(d.Reject)
	d.ok = func() {
		if strings.TrimSpace(d.name.Text()) == "" {
			warning(d.QWidget, "New Folder", "The folder has no name.")
			return
		}
		e.CreateFolder(currentData(d.within), d.name.Text(), func(err error) {
			if err != nil {
				w.warn("New Folder", err)
			}
		})
		d.Accept()
	}
	buttons.OnAccepted(d.ok)
	fitDialog(d.QDialog, 400)
	openDialog(d.QDialog)
	return d
}

// deleteFolder deletes the folder of the account from the server, with the
// folders within it and all their mail, once the user has been shown what
// it holds and agrees, as it cannot be undone.
func (w *Window) deleteFolder(e *engine.Engine, folder string) {
	e.FolderContents(folder, func(contents []remote.FolderStatus, err error) {
		if err != nil {
			w.warn("Delete Folder", err)
			return
		}
		if !confirm(w.QWidget, "Delete Folder", deleteFolderText(contents)) {
			return
		}
		e.DeleteFolder(contents, func(err error) {
			if err != nil {
				w.warn("Delete Folder", err)
			}
		})
	})
}

// maxListed is how many of the folders within one that is deleted are
// named in the question.
const maxListed = 10

// deleteFolderText asks if the folders FolderContents gave, the folder last
// and those within it before, are to be deleted, saying how many messages
// each holds and how many there are in all.
func deleteFolderText(contents []remote.FolderStatus) string {
	folder := contents[len(contents)-1]
	within := contents[:len(contents)-1]
	var b strings.Builder
	fmt.Fprintf(&b, "Delete the folder %s from the server for good?\n\n", folder.Name)
	holds := "It holds " + messages(int(folder.Messages))
	if !folder.Selectable {
		holds = "It holds no mail of its own"
	}
	if len(within) == 0 {
		if folder.Messages == 0 {
			b.WriteString("It is empty.")
		} else {
			b.WriteString(holds + ", which will be deleted with it.")
		}
		b.WriteString(" This cannot be undone.")
		return b.String()
	}

	total := int(folder.Messages)
	if len(within) == 1 {
		b.WriteString(holds + ", and within it is a folder:\n")
	} else {
		fmt.Fprintf(&b, "%s, and within it are %d folders:\n", holds, len(within))
	}
	// They are named in the order of their names, as the side bar has
	// them, rather than the order they are deleted in, the deepest first.
	byName := slices.SortedFunc(slices.Values(within), func(x, y remote.FolderStatus) int {
		return strings.Compare(strings.ToLower(x.Name), strings.ToLower(y.Name))
	})
	for i, f := range byName {
		total += int(f.Messages)
		if i < maxListed {
			fmt.Fprintf(&b, "    %s: %s\n", f.Name, messages(int(f.Messages)))
		}
	}
	if len(within) > maxListed {
		fmt.Fprintf(&b, "    and %d more\n", len(within)-maxListed)
	}
	fmt.Fprintf(&b, "\n%s in all will be deleted, with the %d folders. This cannot be undone.", messages(total), len(contents))
	return b.String()
}

// messages says how many messages there are.
func messages(n int) string {
	return fmt.Sprintf("%d message%s", n, plural(n))
}
