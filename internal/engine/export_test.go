package engine

import (
	"runtime/debug"
	"testing"
	"time"
)

// SetWatchRetry has the watcher connect again after d, rather than after
// seconds, until the test ends, when it is put back.
func SetWatchRetry(d time.Duration) (restore func()) {
	old, oldMost := watchRetry, watchRetryMost
	watchRetry, watchRetryMost = d, 4*d
	return func() { watchRetry, watchRetryMost = old, oldMost }
}

// Validity returns the UIDVALIDITY of the index of the folder, which a test
// gives with the UIDs it read from that index, as the UI does.
func (e *Engine) Validity(folder string) uint32 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.index(folder).UIDValidity
}

// SetOpIdle has the connection of the op worker tried once it has been
// unused for d, rather than a minute, until the test ends, when it is put
// back.
func SetOpIdle(d time.Duration) (restore func()) {
	old := opIdle
	opIdle = d
	return func() { opIdle = old }
}

// CheckIndexReads fails the test if an index is read from the local copy
// with the engine's lock held, until the test ends.
func CheckIndexReads(t *testing.T) {
	old := readLocked
	readLocked = func() { t.Errorf("an index was read with the engine's lock held:\n%s", debug.Stack()) }
	t.Cleanup(func() { readLocked = old })
}
