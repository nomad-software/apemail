package engine

import (
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"sync"
	"time"

	"github.com/nomad-software/apemail/internal/store"
)

// index returns the index of the folder, reading it from the local copy the
// first time. It must be called with e.mu held, and off the UI's thread.
func (e *Engine) index(folder string) *store.Index {
	if ix, ok := e.indexes[folder]; ok {
		return ix
	}
	ix := e.readIndex(folder)
	e.indexes[folder] = ix
	return ix
}

// readIndex reads the index of the folder from the local copy, or returns an
// empty one if it cannot.
func (e *Engine) readIndex(folder string) *store.Index {
	ix, err := e.store.Index(folder)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		e.failed(fmt.Errorf("reading the index of %s: %w", folder, err))
	}
	return ix
}

// preload reads the index of the folder from the local copy, if it has not
// been, without holding up the engine while it does: that of a large folder
// takes a while to read.
func (e *Engine) preload(folder string) {
	e.mu.Lock()
	_, ok := e.indexes[folder]
	e.mu.Unlock()
	if ok {
		return
	}
	ix := e.readIndex(folder)
	e.mu.Lock()
	// A sync may have read it, or made it anew, meanwhile.
	if _, ok := e.indexes[folder]; !ok {
		e.indexes[folder] = ix
	}
	e.mu.Unlock()
}

// snapshot returns a copy of the index of the folder.
func snapshot(ix *store.Index) *store.Index {
	c := *ix
	c.Messages = slices.Clone(ix.Messages)
	return &c
}

// changed has the index of the folder saved, and tells the listener of it.
// It must be called with e.mu held.
func (e *Engine) changed(ix *store.Index) {
	e.dirty[ix.Folder] = true
	select {
	case e.save <- struct{}{}:
	default:
		// The saver has been told already.
	}
	s := snapshot(ix)
	e.post(func() { e.listener.FolderChanged(e.acct.ID, s) })
}

// saveDelay is how long an index that has changed waits to be saved, so
// that a run of changes, as a few messages marked read one after another,
// is saved once: the index of a large folder takes a while to write.
const saveDelay = 2 * time.Second

// saver saves the indexes that have changed, a while after they do, and
// those that are left when the engine is closed.
func (e *Engine) saver() {
	defer e.wg.Done()
	for {
		select {
		case <-e.stop:
			e.saveIndexes()
			return
		case <-e.save:
		}
		select {
		case <-e.stop:
		case <-time.After(saveDelay):
		}
		e.saveIndexes()
	}
}

// saveIndexes saves the indexes that have changed. Each is saved from a copy,
// so that the engine is not held up while it is compressed, encrypted and
// written, and in a goroutine of its own, so that several are at once.
func (e *Engine) saveIndexes() {
	e.saveMu.Lock()
	defer e.saveMu.Unlock()
	e.mu.Lock()
	var changed []*store.Index
	for folder := range e.dirty {
		// A folder that has gone from the server has no index to save.
		if ix, ok := e.indexes[folder]; ok {
			changed = append(changed, snapshot(ix))
		}
	}
	clear(e.dirty)
	e.mu.Unlock()

	var wg sync.WaitGroup
	for _, ix := range changed {
		wg.Go(func() {
			if err := e.store.SaveIndex(ix); err != nil {
				// Shown even when the engine is closing, as it is
				// the last chance.
				e.post(func() {
					e.listener.Failed(e.acct.ID, fmt.Errorf("saving the index of %s: %w", ix.Folder, err))
				})
			}
		})
	}
	wg.Wait()
}
