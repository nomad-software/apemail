package ui

import (
	"github.com/nomad-software/qute/qtgui"

	"github.com/nomad-software/apemail/internal/appicon"
)

var appIconCache *qtgui.QIcon

// appIcon returns the icon of the program.
func appIcon() *qtgui.QIcon {
	if appIconCache == nil {
		pixmap := qtgui.NewQPixmap()
		pixmap.LoadFromData(appicon.PNG)
		appIconCache = qtgui.NewQIconWithPixmap(pixmap)
	}
	return appIconCache
}
