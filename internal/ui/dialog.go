package ui

import (
	qt "github.com/mappu/miqt/qt6"
	"runtime"
)

// fitDialog makes the dialog the width, or wider if it needs to be, and as
// tall as what is in it needs at that width, so that notes which wrap onto
// more lines in a larger font are not cut off.
func fitDialog(d *qt.QDialog, width int) {
	layout := d.Layout()
	layout.Activate()
	least, hint := layout.MinimumSize(), d.SizeHint()
	width = max(width, least.Width())
	height := max(layout.HeightForWidth(width), hint.Height(), least.Height())
	runtime.KeepAlive(least)
	runtime.KeepAlive(hint)
	d.Resize(width, height)
}

// openDialog shows a dialog which keeps the window from being used until it
// is answered, without waiting for it, and deletes it once it is.
func openDialog(d *qt.QDialog) {
	d.OnFinished(func(int) { d.DeleteLater() })
	d.Open()
}
