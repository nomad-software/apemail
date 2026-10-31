// Package packaging only has tests: what is in this folder is not Go.
package packaging

import (
	"os"
	"strings"
	"testing"
)

// TestNoCaseInSubstitution checks that macos.sh has no case within a $(...) of
// several lines. The /bin/sh of macOS is bash 3.2, which takes the ) of a case
// pattern for the end of the substitution and only fails when it gets there,
// so it cannot be seen on any other system: such code goes in a function, and
// the substitution calls that.
func TestNoCaseInSubstitution(t *testing.T) {
	script, err := os.ReadFile("macos.sh")
	if err != nil {
		t.Fatal(err)
	}
	inside := false
	for i, line := range strings.Split(string(script), "\n") {
		code := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(code, "#"):
		case inside:
			if strings.HasPrefix(code, "case ") {
				t.Errorf("macos.sh:%d: a case within a substitution, which bash 3.2 cannot parse: %s", i+1, code)
			}
			// A line that closes more than it opens ends it; one such as
			// place="$(inFrameworks "$name")" only has one of its own.
			inside = strings.Count(code, ")") <= strings.Count(code, "$(")
		default:
			inside = strings.Count(code, "$(") > strings.Count(code, ")")
		}
	}
}
