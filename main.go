package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	qt "github.com/mappu/miqt/qt6"

	"github.com/nomad-software/apemail/internal/appicon"
	"github.com/nomad-software/apemail/internal/desktop"
	"github.com/nomad-software/apemail/internal/settings"
	"github.com/nomad-software/apemail/internal/ui"
)

func main() {
	var files ui.Files

	// The settings are kept where the system keeps the configuration of
	// programs, or in the current directory if it has no such place.
	files.Settings = "settings.json"
	if path, err := settings.Path(); err != nil {
		fmt.Fprintln(os.Stderr, "apemail: the settings are kept in the current directory:", err)
	} else if err := settings.Prepare(path); err != nil {
		fmt.Fprintln(os.Stderr, "apemail: the settings are kept in the current directory:", err)
	} else {
		files.Settings = path
	}

	// The accounts and their mail are kept where the system keeps the data
	// of programs.
	files.Accounts = "accounts"
	if dir, err := settings.DataDir(); err != nil {
		fmt.Fprintln(os.Stderr, "apemail: the accounts are kept in the current directory:", err)
	} else {
		files.Accounts = filepath.Join(dir, "accounts")
	}

	qt.NewQApplication(os.Args)
	qt.QCoreApplication_SetApplicationName("apemail")
	// Under Wayland this is the name a window goes by, which a desktop
	// matches to the desktop entry of that name for its icon and its place
	// in the task bar.
	qt.QGuiApplication_SetDesktopFileName(desktop.Name)

	// On Linux the desktop is given an entry for the program, which starts
	// the copy that is running now, wherever it is and whatever it is
	// called, so that it can be started from the desktop's own menus and
	// kept in a dock or sidebar.
	if runtime.GOOS == "linux" {
		if err := desktop.Install(appicon.PNG); err != nil {
			fmt.Fprintln(os.Stderr, "apemail: the desktop entry could not be written:", err)
		}
	}

	window := ui.NewWindow(files)
	window.Start()

	qt.QApplication_Exec()
}
