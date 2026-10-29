package ui

import (
	"fmt"
	"html"
	"maps"
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
	tag := `<img src="cid:` + html.EscapeString(a.ContentID) + `"`
	if img.Width() > maxImageWidth {
		tag += fmt.Sprintf(` width="%d"`, maxImageWidth)
	}
	c.body.exec("insertHTML", tag+">")
	return nil
}

// addImage makes an image something the HTML can show as cid:ID.
func (c *composer) addImage(a message.Attachment) {
	c.images = append(c.images, a)
}

// image returns a picture the HTML shows as cid:ID.
func (c *composer) image(id string) (message.Attachment, bool) {
	for _, a := range c.images {
		if a.ContentID == id {
			return a, true
		}
	}
	return message.Attachment{}, false
}

// addWebImages gives the body the images on the web that the message view
// has loaded for the message, if it is the one shown, as it is when it is
// quoted or continued, so that they are shown without being fetched again.
func (c *composer) addWebImages(m *message.Message) {
	if v := c.w.view; v.msg == m {
		maps.Copy(c.body.webImages, v.images)
	}
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
