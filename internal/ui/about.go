package ui

import (
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/nomad-software/qute/qtcore"
	"github.com/nomad-software/qute/qtwidgets"
)

// aboutWidth is the narrowest the about box is, so that the notices of the
// licences are set out in lines of a comfortable length.
const aboutWidth = 640

// aboutIconSize is the size of the program's icon in the about box.
const aboutIconSize = 96

// appVersion returns the version of the program, which Go records from git
// when it is built, as plain major.minor.patch.
func appVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return plainVersion(info.Main.Version)
	}
	return "dev"
}

// plainVersion returns the release a version recorded by Go is, or follows.
// Between tags Go records a version made from the commit, whose patch is one
// more than the tag before it, as v1.2.4-0.20261027184954-00c19a9187bb is
// after v1.2.3.
func plainVersion(version string) string {
	version, _, _ = strings.Cut(version, "+")
	version, pre, _ := strings.Cut(version, "-")
	if !strings.HasPrefix(pre, "0.") {
		return version
	}
	i := strings.LastIndex(version, ".") + 1
	patch, err := strconv.Atoi(version[i:])
	if err != nil || patch == 0 {
		return version
	}
	return version[:i] + strconv.Itoa(patch-1)
}

// aboutText returns what the about box says of the program.
func aboutText() string {
	return "<p>A simple IMAP email client. Mail lives on the server; what is kept on this computer " +
		"is a copy, encrypted with your SSH keys.</p>" +
		"<p>Copyright © 2026 Gary Willoughby. All rights reserved.</p>"
}

// newAboutTitle returns the name of the program with its version to the
// right of it, smaller and half transparent, on one baseline, and the label
// of the version.
func newAboutTitle() (*qtwidgets.QHBoxLayout, *qtwidgets.QLabel) {
	name := qtwidgets.NewQLabelWithText("ApeMail", nil)
	font := name.Font()
	font.SetBold(true)
	font.SetPointSizeF(font.PointSizeF() * 1.5)
	name.SetFont(font)

	version := qtwidgets.NewQLabelWithText(appVersion(), nil)
	version.SetTextFormat(qtcore.PlainText)
	small := version.Font()
	small.SetPointSizeF(small.PointSizeF() * 0.8)
	version.SetFont(small)
	nameMetrics, versionMetrics := name.FontMetrics(), version.FontMetrics()
	version.SetContentsMargins(qtcore.QMargins{Bottom: nameMetrics.Descent() - versionMetrics.Descent()})
	faded := qtwidgets.NewQGraphicsOpacityEffect(nil)
	faded.SetOpacity(0.5)
	version.SetGraphicsEffect(faded)

	title := qtwidgets.NewQHBoxLayout(nil)
	title.AddWidgetFull(name, 0, qtcore.AlignBottom)
	title.AddWidgetFull(version, 0, qtcore.AlignBottom)
	title.AddStretch()
	return title, version
}

// licenceText returns the notices of the licences of what the program is
// built with, which carry the notice the LGPL asks of programs that use Qt.
func licenceText() string {
	return "<p><b>Third party licences</b></p>" +
		"<p>This program uses Qt version " + qtVersion() + " (Qt Widgets, Qt SVG and Qt WebEngine).</p>" +
		"<p>Qt is a C++ toolkit for cross-platform application development, used here under the " +
		"terms of the <a href=\"https://www.gnu.org/licenses/lgpl-3.0.html\">GNU Lesser General " +
		"Public License version 3</a> (LGPLv3). Qt is dynamically linked, so it can be replaced " +
		"with a different or modified build of the library.</p>" +
		"<p>Qt and its source code are available from <a href=\"https://www.qt.io\">www.qt.io</a>. " +
		"See <a href=\"https://www.qt.io/licensing\">www.qt.io/licensing</a> for an overview of Qt " +
		"licensing.</p>" +
		"<p>Copyright (C) The Qt Company Ltd. and other contributors. Qt and the Qt logo are " +
		"trademarks of The Qt Company Ltd.</p>" +
		"<p>Qt WebEngine, which shows messages, is used under the same licence, and contains " +
		"<a href=\"https://www.chromium.org\">Chromium</a>, under its own BSD licence and those of the " +
		"third party code within it, listed at " +
		"<a href=\"https://doc.qt.io/qt-6/qtwebengine-licensing.html\">doc.qt.io/qt-6/qtwebengine-licensing.html</a>.</p>" +
		"<p>The Go bindings for Qt are provided by <a href=\"https://github.com/nomad-software/qute\">qute</a>, " +
		"used under the GNU General Public License version 3.</p>" +
		"<p>It is also built with the Go libraries " +
		"<a href=\"https://github.com/emersion/go-imap\">go-imap</a> (MIT) for IMAP, " +
		"<a href=\"https://github.com/emersion/go-smtp\">go-smtp</a> (MIT) for SMTP, " +
		"<a href=\"https://github.com/emersion/go-sasl\">go-sasl</a> (MIT) for logging in, " +
		"<a href=\"https://github.com/emersion/go-message\">go-message</a> (MIT) for reading and writing mail, " +
		"<a href=\"https://age-encryption.org\">age</a> (BSD) for encryption, with " +
		"<a href=\"https://github.com/FiloSottile/edwards25519\">edwards25519</a> (BSD) and " +
		"<a href=\"https://github.com/FiloSottile/hpke\">hpke</a> (BSD), " +
		"<a href=\"https://pkg.go.dev/golang.org/x/crypto\">x/crypto</a> (BSD), " +
		"<a href=\"https://pkg.go.dev/golang.org/x/sys\">x/sys</a> (BSD) and " +
		"<a href=\"https://pkg.go.dev/golang.org/x/text\">x/text</a> (BSD).</p>"
}

// aboutDialog is the about box: the program's icon beside what it is, and
// the notices of the licences beneath in a frame of the style's own, in the
// colours of the theme.
type aboutDialog struct {
	*qtwidgets.QDialog
	// notices is the frame around the notices of the licences.
	notices *qtwidgets.QFrame
	// version is the label of the version of the program.
	version *qtwidgets.QLabel
}

func newAboutDialog(parent qtwidgets.QWidgetLike) *aboutDialog {
	d := &aboutDialog{QDialog: qtwidgets.NewQDialog(parent)}
	d.SetWindowTitle("About ApeMail")
	d.SetMinimumWidth(aboutWidth)

	ic := qtwidgets.NewQLabel(nil)
	pixmap := appIcon().PixmapExtent(aboutIconSize)
	ic.SetPixmap(pixmap)
	ic.SetAlignment(qtcore.AlignTop)

	title, version := newAboutTitle()
	d.version = version

	about := qtwidgets.NewQVBoxLayout(nil)
	about.SetSpacing(12)
	about.AddLayout(title)
	about.AddWidget(newRichLabel(aboutText()))
	about.AddStretch()

	top := qtwidgets.NewQHBoxLayout(nil)
	top.SetSpacing(20)
	top.AddWidget(ic)
	top.AddLayoutFull(about, 1)

	// The notices are smaller than the rest, as on a label of the style.
	licences := newRichLabel(licenceText())
	font := licences.Font()
	font.SetPointSizeF(font.PointSizeF() * 0.9)
	licences.SetFont(font)

	d.notices = qtwidgets.NewQFrame(nil)
	d.notices.SetFrameShape(qtwidgets.QFrameStyledPanel)
	d.notices.SetFrameShadow(qtwidgets.QFrameSunken)
	frameLayout := qtwidgets.NewQVBoxLayout(d.notices)
	frameLayout.SetContentsMargins(qtcore.QMargins{Left: 12, Top: 8, Right: 12, Bottom: 8})
	frameLayout.AddWidget(licences)

	buttons := qtwidgets.NewQDialogButtonBoxWithStandardButton(qtwidgets.QDialogButtonBoxOk, nil)
	buttons.OnAccepted(d.Accept)

	layout := qtwidgets.NewQVBoxLayout(d)
	layout.SetContentsMargins(qtcore.QMargins{Left: 20, Top: 20, Right: 20, Bottom: 16})
	layout.SetSpacing(16)
	layout.AddLayout(top)
	layout.AddWidget(d.notices)
	layout.AddWidget(buttons)

	fitDialog(d, aboutWidth)
	return d
}

// newRichLabel returns a label of rich text which wraps, and whose links
// open in the browser.
func newRichLabel(text string) *qtwidgets.QLabel {
	label := qtwidgets.NewQLabelWithText(text, nil)
	label.SetTextFormat(qtcore.RichText)
	label.SetWordWrap(true)
	label.SetOpenExternalLinks(true)
	label.SetTextInteractionFlags(qtcore.TextBrowserInteraction)
	return label
}

// showAbout shows the about box until it is closed.
func showAbout(parent qtwidgets.QWidgetLike) {
	d := newAboutDialog(parent)
	// A dialog is not destroyed by being closed.
	defer d.DeleteLater()
	d.Exec()
}

// qtVersion returns the version of Qt the program runs with.
func qtVersion() string {
	v := qtcore.QLibraryInfoVersion()
	return v.ToString()
}
