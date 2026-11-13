package render

import (
	"os"
	"testing"
)

// TestMain runs the tests without the colour settings of whoever runs them:
// NO_COLOR, CLICOLOR_FORCE or TERM in a developer's shell would otherwise
// change what the styling tests see. A test sets the ones it is about.
func TestMain(m *testing.M) {
	for _, name := range []string{"NO_COLOR", "CLICOLOR_FORCE", "COLORTERM", "TERM"} {
		_ = os.Unsetenv(name)
	}
	os.Exit(m.Run())
}
