package ui

import (
	"github.com/nomad-software/qute"
	"github.com/nomad-software/qute/qtcore"
	"github.com/nomad-software/qute/qtwidgets"
)

// fitDialog makes the dialog the width, or wider if it needs to be, and as
// tall as what is in it needs at that width, so that notes which wrap onto
// more lines in a larger font are not cut off.
func fitDialog(dialog qtwidgets.QDialogLike, width int) {
	d := dialog.AsQDialog()
	layout := d.Layout()
	layout.Activate()
	least, hint := layout.MinimumSize(), d.SizeHint()
	width = max(width, least.Width)
	height := max(layout.AsQLayoutItem().HeightForWidth(width), hint.Height, least.Height)
	d.Resize(width, height)
}

// openDialog shows a dialog which keeps the window from being used until it
// is answered, without waiting for it, and deletes it once it is.
func openDialog(dialog qtwidgets.QDialogLike) {
	d := dialog.AsQDialog()
	d.OnFinished(func(int) { d.DeleteLater() })
	d.Open()
}

// inBackground runs work away from the UI's thread, as what reaches a server
// does, and then done on the UI's thread, unless w has been destroyed by
// then: a dialog answered meanwhile has been deleted, with its widgets,
// which done would use.
func inBackground(w qtwidgets.QWidgetLike, work, done func()) {
	gone := false
	w.AsQWidget().OnDestroyed(func(*qtcore.QObject) { gone = true })
	go func() {
		work()
		qute.Post(func() {
			if !gone {
				done()
			}
		})
	}()
}

// currentData returns the text of the data of the item chosen in the box.
func currentData(box *qtwidgets.QComboBox) string {
	return box.CurrentData().ToString()
}
