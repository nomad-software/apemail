package engine

import "time"

// SetWatchRetry has the watcher connect again after d, rather than after
// seconds, until the test ends, when it is put back.
func SetWatchRetry(d time.Duration) (restore func()) {
	old, oldMost := watchRetry, watchRetryMost
	watchRetry, watchRetryMost = d, 4*d
	return func() { watchRetry, watchRetryMost = old, oldMost }
}
