package ui

import (
	"encoding/json"
	"errors"

	qt "github.com/mappu/miqt/qt6"
)

// messagesMime is the type of the data of messages dragged from the message
// list, which only this program reads.
const messagesMime = "application/x-apemail-messages"

// draggedMessages are messages dragged from the message list: where they
// are, and their UIDs.
type draggedMessages struct {
	Account string   `json:"account"`
	Folder  string   `json:"folder"`
	UIDs    []uint32 `json:"uids"`
}

// encode returns the messages as the data of a drag.
func (d draggedMessages) encode() *qt.QMimeData {
	data, _ := json.Marshal(d)
	m := qt.NewQMimeData()
	m.SetData(messagesMime, data)
	return m
}

// decodeDragged returns the messages a drag carries, if it carries any.
func decodeDragged(m *qt.QMimeData) (draggedMessages, bool) {
	var d draggedMessages
	if m == nil || !m.HasFormat(messagesMime) {
		return d, false
	}
	if err := json.Unmarshal(m.Data(messagesMime), &d); err != nil || len(d.UIDs) == 0 {
		return d, false
	}
	return d, true
}

// canDrop reports if the messages can be dropped on the folder of the
// account, which is empty for an account itself or a folder that cannot
// hold messages. Messages are only moved within their account.
func canDrop(d draggedMessages, id, folder string) error {
	switch {
	case folder == "":
		return errors.New("messages can only be dropped on a folder that holds messages")
	case id != d.Account:
		return errors.New("messages can only be moved between the folders of the same account")
	case folder == d.Folder:
		return errors.New("the messages are already in " + folder)
	}
	return nil
}

// dropMessages moves messages dropped on a folder to it, on the server, and
// reports if they are being moved.
func (w *Window) dropMessages(d draggedMessages, id, folder string) bool {
	if canDrop(d, id, folder) != nil {
		return false
	}
	e := w.engines[id]
	if e == nil {
		return false
	}
	e.Move(d.Folder, d.UIDs, folder, func(err error) {
		if err != nil {
			w.warn("Move", err)
		}
	})
	return true
}
