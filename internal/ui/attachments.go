package ui

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/nomad-software/qute/qtcore"
	"github.com/nomad-software/qute/qtgui"
	"github.com/nomad-software/qute/qtwidgets"

	"github.com/nomad-software/apemail/internal/icons"
	"github.com/nomad-software/apemail/internal/message"
)

// makeAttachments makes the pane of a message's attachments, which holds a
// button for each, wrapped onto rows, and one that saves them all.
func (v *messageView) makeAttachments() *qtwidgets.QWidget {
	v.attachPane = qtwidgets.NewQWidget(nil)
	// It is only as tall as its rows, so that the height the view has to
	// spare goes to the body.
	v.attachPane.SetSizePolicy(qtwidgets.NewQSizePolicyWithPolicy(qtwidgets.QSizePolicyPreferred, qtwidgets.QSizePolicyFixed))
	v.attachments = qtwidgets.NewQWidget(nil)
	v.attachments.SetStyleSheet(attachmentStyle)
	v.attachBox = newFlowLayout(v.attachments)
	// The buttons' own padding lines their icons up with the headers.
	v.attachBox.SetContentsMargins(qtcore.QMargins{Left: 2, Top: 2, Right: 8, Bottom: 4})
	v.attachScroll = &resizingScroll{resized: v.fitAttachments}
	qtwidgets.NewQScrollAreaWith(v.attachScroll, v.attachPane)
	v.attachScroll.SetFrameShape(qtwidgets.QFrameNoFrame)
	v.attachScroll.SetHorizontalScrollBarPolicy(qtcore.ScrollBarAlwaysOff)
	v.attachScroll.SetWidgetResizable(true)
	// It is as tall as fitAttachments makes it: a scroll area grows to
	// take what height it can, and would take half the message's.
	v.attachScroll.SetSizePolicy(qtwidgets.NewQSizePolicyWithPolicy(qtwidgets.QSizePolicyExpanding, qtwidgets.QSizePolicyFixed))
	v.attachScroll.SetWidget(v.attachments)
	v.saveAll = qtwidgets.NewQPushButtonWithText("Save All…", nil)
	v.saveAll.OnClicked(func(bool) { v.saveFiles() })
	attachRow := qtwidgets.NewQHBoxLayout(v.attachPane)
	attachRow.SetContentsMargins(qtcore.QMargins{Top: 2, Right: 8})
	attachRow.SetSpacing(0)
	attachRow.AddWidgetFull(v.attachScroll, 1, 0)
	// The button stays in sight at the top while the attachments scroll.
	attachRow.AddWidgetFull(v.saveAll, 0, qtcore.AlignTop)
	return v.attachPane
}

// setAttachments shows a button for each attachment, which saves it, on as
// many rows as they need.
func (v *messageView) setAttachments(files []message.Attachment) {
	clearLayout(v.attachBox)
	v.files = files
	v.attachPane.SetVisible(len(files) > 0)
	// One attachment is saved by clicking it.
	v.saveAll.SetVisible(len(files) > 1)
	if len(files) == 0 {
		return
	}
	defer v.fitAttachments()
	// Each button copies the icon and the cursor.
	paperclip := icon(icons.Attachment)
	hand := qtgui.NewQCursorWithCursorShape(qtcore.PointingHandCursor)
	for _, a := range files {
		b := qtwidgets.NewQPushButtonWithText(fmt.Sprintf("%s (%s)", a.Filename, size(len(a.Data))), nil)
		b.SetIcon(paperclip)
		b.SetCursor(hand)
		b.OnClicked(func(bool) { v.save(a) })
		v.attachBox.addWidget(b)
	}
}

// resizingScroll is a scroll area which says when its size has changed.
type resizingScroll struct {
	qtwidgets.QScrollAreaBase
	resized func()
}

func (s *resizingScroll) ResizeEvent(e *qtgui.QResizeEvent) {
	s.Super().ResizeEvent(e)
	s.resized()
}

// attachmentStyle is that of the buttons of the attachments: flat, and shaded
// while the mouse is over it, so that it looks like it can be clicked. The
// shade is grey and half clear, which darkens a light theme and lightens a
// dark one.
const attachmentStyle = `QPushButton { border: none; border-radius: 4px; padding: 3px 6px; }
QPushButton:hover { background: rgba(128, 128, 128, 0.22); }
QPushButton:pressed { background: rgba(128, 128, 128, 0.4); }`

// attachRows is how many rows of attachments are shown before they scroll.
const attachRows = 3

// fitAttachments makes the attachments as tall as their rows, at most
// attachRows of them, in the width they have.
func (v *messageView) fitAttachments() {
	height := v.attachBox.HeightForWidth(v.attachScroll.Width())
	v.attachScroll.SetFixedHeight(min(height, v.attachBox.heightOfRows(attachRows)))
}

// save asks where to save an attachment and saves it there.
func (v *messageView) save(a message.Attachment) {
	path := chooseFile(v, "Save Attachment", filepath.Join(downloads(), fileName(a)))
	if path == "" {
		return
	}
	if err := os.WriteFile(path, a.Data, 0o600); err != nil {
		warning(v, "Save Attachment", err.Error())
	}
}

// saveFiles asks for a folder and saves every attachment shown there. A file
// already there is never written over: the attachment is given a name of its
// own, as name (2).pdf.
func (v *messageView) saveFiles() {
	dir := chooseFolder(v, "Save All Attachments", downloads())
	if dir == "" {
		return
	}
	var failed []string
	for _, a := range v.files {
		if err := saveNew(dir, fileName(a), a.Data); err != nil {
			failed = append(failed, err.Error())
		}
	}
	if len(failed) > 0 {
		warning(v, "Save All Attachments", "Some attachments could not be saved:\n\n"+strings.Join(failed, "\n"))
	}
}

// chooseFile asks where to save a file, starting at start, and returns it,
// or "" if nowhere was chosen; the dialog asks before a file is written
// over. chooseFolder asks for a folder. Tests replace them rather than let
// the dialogs open.
var (
	chooseFile = func(parent qtwidgets.QWidgetLike, title, start string) string {
		path, _ := qtwidgets.QFileDialogGetSaveFileNameFull(parent, title, start, "", "", 0)
		return path
	}
	chooseFolder = func(parent qtwidgets.QWidgetLike, title, start string) string {
		return qtwidgets.QFileDialogGetExistingDirectoryFull(parent, title, start, qtwidgets.QFileDialogShowDirsOnly)
	}
)

// downloads is the folder attachments are saved in unless another is chosen.
func downloads() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Downloads")
}

// fileName is the name an attachment is saved by: the last part of the name
// it has, so that it cannot be saved outside the folder chosen.
func fileName(a message.Attachment) string {
	name := filepath.Base(a.Filename)
	if name == "." || name == "/" || name == "" || name == ".." {
		return "attachment"
	}
	return name
}

// saveNew saves data as a new file with the name in dir, or, if one of that
// name is there, with the first of name (2), name (3)... that is not.
func saveNew(dir, name string, data []byte) error {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for n := 1; ; n++ {
		path := filepath.Join(dir, name)
		if n > 1 {
			path = filepath.Join(dir, fmt.Sprintf("%s (%d)%s", base, n, ext))
		}
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return err
		}
		_, err = f.Write(data)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			// What was written of it is no use.
			os.Remove(path)
		}
		return err
	}
}
