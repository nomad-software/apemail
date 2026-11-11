package ui

import (
	"fmt"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/nomad-software/qute/qtcore"
	"github.com/nomad-software/qute/qtgui"
	"github.com/nomad-software/qute/qutetest"

	"github.com/nomad-software/apemail/internal/account"
	"github.com/nomad-software/apemail/internal/icons"
	"github.com/nomad-software/apemail/internal/store"
)

// These benchmarks measure the work of the interface that goes through Qt,
// rather than the program's own (as sorting does), so that how the bindings
// to Qt fare is seen: Qt asking the model for what it draws, the list asked
// about each of its rows, items and widgets made, and icons drawn.

// BenchmarkPaintList100k measures the list of a folder of 100,000 messages
// being drawn, which has Qt ask the model for what each cell it shows holds.
func BenchmarkPaintList100k(b *testing.B) {
	ix := folderOf(100000)
	withList(b, func(l *messageList) {
		l.setIndex(ix, false)
		for b.Loop() {
			runtime.KeepAlive(l.Viewport().Grab())
		}
	})
}

// BenchmarkModelData measures the model asked, through Qt, what each cell of
// a thousand rows shows, in each role the view asks for.
func BenchmarkModelData(b *testing.B) {
	ix := folderOf(1000)
	roles := []qtcore.ItemDataRole{qtcore.DisplayRole, qtcore.DecorationRole, qtcore.FontRole, qtcore.ForegroundRole, qtcore.ToolTipRole}
	withList(b, func(l *messageList) {
		l.setIndex(ix, false)
		for b.Loop() {
			for row := range len(l.rows) {
				for col := range colCount {
					index := l.model.Index(row, col)
					for _, role := range roles {
						runtime.KeepAlive(l.model.Data(index, int(role)))
					}
				}
			}
		}
	})
}

// BenchmarkSelectAll100k measures every message of a folder of 100,000 being
// selected, and the list working out which are.
func BenchmarkSelectAll100k(b *testing.B) {
	ix := folderOf(100000)
	withList(b, func(l *messageList) {
		l.setIndex(ix, false)
		for b.Loop() {
			l.SelectAll()
			if n := len(l.selectedUIDs()); n != len(ix.Messages) {
				b.Fatalf("%d selected", n)
			}
			l.ClearSelection()
		}
	})
}

// BenchmarkFolderTree measures the side bar being made of an account with a
// thousand folders, ten within each of a hundred.
func BenchmarkFolderTree(b *testing.B) {
	a := account.New()
	a.Name, a.Email = "Ada Lovelace", "ada@example.com"
	folders := []store.Folder{{Name: "INBOX", Delim: "/"}}
	for i := range 100 {
		parent := fmt.Sprintf("Projects %02d", i)
		folders = append(folders, store.Folder{Name: parent, Delim: "/"})
		for j := range 9 {
			folders = append(folders, store.Folder{Name: fmt.Sprintf("%s/Part %d", parent, j), Delim: "/"})
		}
	}
	qutetest.Run(b, func() {
		t := newFolderTree(map[string]map[string]bool{})
		defer t.Delete()
		t.Resize(260, 600)
		for b.Loop() {
			t.setFolders(a, folders)
			runtime.KeepAlive(t.Grab())
		}
	})
}

// BenchmarkIcons measures every icon being drawn, as the button bar's are.
func BenchmarkIcons(b *testing.B) {
	qutetest.Run(b, func() {
		var all []*qtgui.QIcon
		for _, name := range icons.Names {
			all = append(all, icon(name))
		}
		for b.Loop() {
			for _, i := range all {
				runtime.KeepAlive(i.Pixmap(qtcore.QSize{Width: 32, Height: 32}))
			}
		}
		for _, i := range all {
			i.Delete()
		}
	})
}

// BenchmarkNewWindow measures the main window being made, with its menus,
// button bar, side bar, list and message view, and drawn.
func BenchmarkNewWindow(b *testing.B) {
	dir := b.TempDir()
	files := Files{Settings: filepath.Join(dir, "settings.json"), Accounts: filepath.Join(dir, "accounts")}
	qutetest.Run(b, func() {
		for b.Loop() {
			w := NewWindow(files)
			runtime.KeepAlive(w.Grab())
			w.Delete()
		}
	})
}
