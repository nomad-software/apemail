package remote

import "time"

// SetWaits has a server waited on for d for an answer to a logout or Check,
// rather than seconds, until the test ends, when it is put back.
func SetWaits(d time.Duration) (restore func()) {
	oldLogout, oldCheck := logoutWait, checkWait
	logoutWait, checkWait = d, d
	return func() { logoutWait, checkWait = oldLogout, oldCheck }
}
