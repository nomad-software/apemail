package ui

import (
	"fmt"
	"mime"
	"os"
	"path/filepath"

	qt "github.com/mappu/miqt/qt6"

	"github.com/nomad-software/apemail/internal/message"
)

// maxImageWidth is the widest an image put in a message is shown, in it and
// to whoever reads it, so that a photograph does not run off the page.
const maxImageWidth = 600

// imageFilter is the filter of the file dialog that picks an image to put
// in a message.
const imageFilter = "Images (*.png *.jpg *.jpeg *.gif *.bmp *.webp)"

// setList makes the paragraphs of the selection a list of a style, or, if on
// is false, takes them out of the list they are in.
func (c *composer) setList(style qt.QTextListFormat__Style, on bool) {
	cursor := c.body.TextCursor()
	list := cursor.CurrentList()
	switch {
	case on && list != nil:
		// A list of the other style becomes one of this.
		f := list.Format()
		f.SetStyle(style)
		list.SetFormat(f)
	case on:
		cursor.CreateListWithStyle(style)
	case list != nil:
		doc := c.body.Document()
		for b := doc.FindBlock(cursor.SelectionStart()); b.IsValid() && b.Position() <= cursor.SelectionEnd(); b = b.Next() {
			if l := b.TextList(); l != nil {
				l.Remove(b)
				// The indent the list gave the paragraph goes with it.
				pos := qt.NewQTextCursor4(b)
				f := pos.BlockFormat()
				f.SetIndent(0)
				pos.SetBlockFormat(f)
			}
		}
	}
	c.showList()
}

// showList checks the list button of the style of the list at the cursor.
func (c *composer) showList() {
	style := qt.QTextListFormat__Style(0)
	if list := c.body.TextCursor().CurrentList(); list != nil {
		style = list.Format().Style()
	}
	c.bullets.SetChecked(style == qt.QTextListFormat__ListDisc)
	c.numbers.SetChecked(style == qt.QTextListFormat__ListDecimal)
}

// insertImage asks for an image and puts it in the message where the cursor
// is.
func (c *composer) insertImage() {
	home, _ := os.UserHomeDir()
	path := qt.QFileDialog_GetOpenFileName4(c.QWidget, "Insert Image", home, imageFilter)
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		c.warn("Insert Image", err)
		return
	}
	if err := c.putImage(filepath.Base(path), data); err != nil {
		c.warn("Insert Image", err)
	}
}

// putImage puts an image in the message where the cursor is, no wider than
// maxImageWidth, to go with it as a part the HTML shows as cid:ID.
func (c *composer) putImage(name string, data []byte) error {
	img := qt.NewQImage()
	defer img.Delete()
	if !img.LoadFromDataWithData(data) {
		return fmt.Errorf("%s is not an image that can be shown", name)
	}
	a := message.Attachment{
		Filename:    name,
		ContentType: mime.TypeByExtension(filepath.Ext(name)),
		ContentID:   message.NewContentID(),
		Inline:      true,
		Data:        data,
	}
	c.addImage(a)
	f := qt.NewQTextImageFormat()
	defer f.Delete()
	f.SetName("cid:" + a.ContentID)
	if img.Width() > maxImageWidth {
		f.SetWidth(maxImageWidth)
	}
	c.body.TextCursor().InsertImageWithFormat(f)
	return nil
}

// addImage makes an image something the HTML can show as cid:ID.
func (c *composer) addImage(a message.Attachment) {
	c.images = append(c.images, a)
	url := qt.NewQUrl3("cid:" + a.ContentID)
	defer url.Delete()
	v := qt.NewQVariant12(a.Data)
	defer v.Delete()
	c.body.Document().AddResource(int(qt.QTextDocument__ImageResource), url, v)
}

// addImages makes the images a message shows something the HTML of this one
// can show too, as it does when the message is quoted or continued.
func (c *composer) addImages(m *message.Message) {
	if !c.html {
		return
	}
	for _, a := range m.Images() {
		c.addImage(a)
	}
}
