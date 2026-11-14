package ui

import (
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/nomad-software/qute/qtcore"
	"github.com/nomad-software/qute/qtgui"
	"github.com/nomad-software/qute/qtwidgets"
	"github.com/nomad-software/qute/qutetest"

	"github.com/nomad-software/apemail/internal/remote/remotetest"
)

// TestButtonBarInWindow checks that the bars of buttons of the main window
// and the compose window are within the window, across the top of what it
// shows, rather than in the toolbar area that some styles draw as one with
// the title bar, and that a shortcut of a button only on the bar still works.
func TestButtonBarInWindow(t *testing.T) {
	s := remotetest.Start(t, remotetest.Caps(imap.CapUIDPlus, imap.CapMove))
	w := newTestWindow(t, s)
	wait(t, "the account", func() bool { return len(w.engines) == 1 })
	c := openComposer(t, w)

	qutetest.Run(t, func() {
		for name, win := range map[string]struct {
			window *qtwidgets.QMainWindow
			bar    *qtwidgets.QToolBar
		}{"main": {w.QMainWindow, w.buttons}, "compose": {c.QMainWindow, c.bar}} {
			central := win.window.CentralWidget()
			if win.bar.ParentWidget().QObject.Ptr() != central.QObject.Ptr() {
				t.Errorf("%s: the bar is not within the window's central widget", name)
			}
			if win.window.ToolBarArea(win.bar) != qtcore.NoToolBarArea {
				t.Errorf("%s: the bar is in the toolbar area", name)
			}
			if g := win.bar.Geometry(); g.X != 0 || g.Y != 0 || g.Width != central.Width() {
				t.Errorf("%s: the bar is at %+v in a widget %d wide", name, g, central.Width())
			}
		}

		// The shortcut of an action only on the bar, as Send and Save
		// Draft are, works from a field of the window.
		c.Show()
		c.ActivateWindow()
		probe := qtgui.NewQActionWithText("Probe", c.bar)
		probe.SetShortcut(qtgui.NewQKeySequenceWithKey("Ctrl+Alt+P"))
		fired := 0
		probe.OnTriggered(func(bool) { fired++ })
		c.bar.AddAction(probe)
		c.subject.SetFocus()
		qutetest.Key(c.subject, qtcore.Key_P, qtcore.ControlModifier|qtcore.AltModifier)
		if fired != 1 {
			t.Errorf("the shortcut fired %d times", fired)
		}
	})
}
