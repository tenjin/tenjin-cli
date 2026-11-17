package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tenjin/tenjin-cli/internal/testproject"
	"github.com/tenjin/tenjin-cli/internal/verify"
)

// unreachable is an address nothing answers at.
const unreachable = "http://127.0.0.1:1"

const sandboxSays = "Codex's sandbox is blocking the network: set network_access = true under [sandbox_workspace_write]"

// Run by Codex in a sandbox without network, a failed connection says what
// to change in Codex, not only that Tenjin could not be reached.
func TestAConnectionCodexsSandboxBlocksSaysSo(t *testing.T) {
	h := newHarness(t)
	h.env.endpoints.Docs, h.env.endpoints.Track = unreachable, unreachable
	h.vars["CODEX_SANDBOX_NETWORK_DISABLED"] = "1"

	code, _, stderr := h.run("docs", "search", "sdk")
	if code != 7 || !strings.Contains(stderr, "could not reach Tenjin's documentation: ") || !strings.Contains(stderr, sandboxSays) {
		t.Errorf("docs search: exit = %d, stderr = %q", code, stderr)
	}

	// doctor's key check says it in its finding.
	dir := testproject.Write(t, testproject.AndroidNative())
	_, stdout, _ := h.run("doctor", dir, "--json")
	var report verify.Report
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range report.Findings {
		found = found || f.ID == "key.unreachable" && strings.Contains(f.Detail, sandboxSays)
	}
	if !found {
		t.Errorf("no key.unreachable finding that says why:\n%s", stdout)
	}

	// The doctor Codex runs for setup is offline, and says nothing of it.
	if code, stdout, stderr := h.run("doctor", dir, "--offline", "--json"); code != 0 || strings.Contains(stdout+stderr, "Codex's sandbox") {
		t.Errorf("offline: exit = %d, stderr = %q", code, stderr)
	}
}

// Elsewhere, the same failure says nothing of Codex.
func TestAConnectionThatFailsElsewhereSaysNothingOfCodex(t *testing.T) {
	h := newHarness(t)
	h.env.endpoints.Docs = unreachable
	code, _, stderr := h.run("docs", "search", "sdk")
	if code != 7 || !strings.Contains(stderr, "could not reach Tenjin's documentation") || strings.Contains(stderr, "Codex") {
		t.Errorf("exit = %d, stderr = %q", code, stderr)
	}
}
