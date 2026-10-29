package ui

import (
	"runtime"

	qt "github.com/mappu/miqt/qt6"
)

// A Qt value miqt returns by value, as a QVariant, a QFont or a QSize,
// is freed by a finalizer once Go no longer uses it, and miqt does not keep
// it alive through its own calls: it can be freed in the middle of the last
// call made on it, or of one it is given to. Such a value is kept, with
// runtime.KeepAlive, until it is no longer used (mappu/miqt#365 is the same
// fault in the lists miqt returns). These read what is wanted of the values
// read most.

// itemData returns the text of the item's data in the role.
func itemData(item *qt.QTreeWidgetItem, role int) string {
	v := item.Data(0, role)
	defer runtime.KeepAlive(v)
	return v.ToString()
}

// currentData returns the text of the data of the item chosen in the box.
func currentData(box *qt.QComboBox) string {
	v := box.CurrentData()
	defer runtime.KeepAlive(v)
	return v.ToString()
}

// returned keeps the QVariant an override of a Qt function, as a model's
// data, last returned to Qt, until it is next called, and then deletes it.
// miqt hands Qt the value returned and Qt copies it only once the override
// has returned: one made with a constructor would never be freed, and one
// miqt frees itself could be freed before Qt had copied it. By the next call
// Qt has.
type returned struct {
	last *qt.QVariant
}

// give returns v, which a constructor made, for the override to return,
// deleting the one it returned before.
func (r *returned) give(v *qt.QVariant) *qt.QVariant {
	if r.last != nil {
		r.last.Delete()
	}
	r.last = v
	return v
}

// giveCopy returns a copy of v, which miqt frees itself, for the override to
// return.
func (r *returned) giveCopy(v *qt.QVariant) *qt.QVariant {
	c := qt.NewQVariant3(v)
	runtime.KeepAlive(v)
	return r.give(c)
}
