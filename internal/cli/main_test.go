package cli

import (
	"os"
	"strings"
	"testing"
)

// TestMain runs the tests in an environment of their own: what the shell of
// whoever runs them sets, such as NO_COLOR or TENJIN_NO_TELEMETRY, must not
// change what a test sees, here or in the programs it starts. A test sets
// the variables it is about. The opt-in tests' own settings stay.
func TestMain(m *testing.M) {
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if readByTheCLI(name) {
			_ = os.Unsetenv(name)
		}
	}
	os.Exit(m.Run())
}

func readByTheCLI(name string) bool {
	switch {
	case strings.HasPrefix(name, "TENJIN_REALAGENT_"), strings.HasPrefix(name, "TENJIN_LIVE_"):
		return false
	case strings.HasPrefix(name, "TENJIN_"):
		return true
	}
	switch name {
	case "NO_COLOR", "CLICOLOR_FORCE", "COLORTERM", "DO_NOT_TRACK", "SSH_CONNECTION", "SSH_TTY", "CODEX_SANDBOX_NETWORK_DISABLED":
		return true
	}
	return false
}
