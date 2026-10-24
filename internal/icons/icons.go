// Package icons holds the program's own icons, drawn for it as SVG in one
// style and built into the binary: outlines on a grid of 24, 1.5 wide with
// round ends and corners, in currentColor, which is the colour of the text
// they are drawn beside. It has no dependency on Qt.
package icons

import (
	"embed"
	"fmt"
)

// The names of the icons.
const (
	GetMail     = "get-mail"
	Write       = "write"
	Reply       = "reply"
	ReplyAll    = "reply-all"
	Forward     = "forward"
	Archive     = "archive"
	Trash       = "trash"
	AddressBook = "address-book"
	AddContact  = "add-contact"
	Attachment  = "attachment"
	Account     = "account"
	Inbox       = "inbox"
	Folder      = "folder"
	Sent        = "sent"
	Drafts      = "drafts"
	Save        = "save"
	Bold        = "bold"
	Italic      = "italic"
	Underline   = "underline"
	Bullets     = "bullets"
	Numbers     = "numbers"
	Image       = "image"
	Browser     = "browser"
)

// Names are every icon there is.
var Names = []string{
	GetMail, Write, Reply, ReplyAll, Forward, Archive, Trash, AddressBook, AddContact, Attachment,
	Account, Inbox, Folder, Sent, Drafts,
	Save, Bold, Italic, Underline, Bullets, Numbers, Image, Browser,
}

//go:embed svg/*.svg
var files embed.FS

// SVG returns the SVG of the icon with the name.
func SVG(name string) ([]byte, error) {
	data, err := files.ReadFile("svg/" + name + ".svg")
	if err != nil {
		return nil, fmt.Errorf("there is no icon %q", name)
	}
	return data, nil
}
