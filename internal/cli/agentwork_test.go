package cli

import (
	"testing"

	"github.com/tenjin/tenjin-cli/internal/verify"
)

// An agent installs nothing, so a plugin left to install into a native
// project is the developer's, like the keys: it is never the agent's work.
func TestAgentWorkLeavesTheDevelopersStepsOut(t *testing.T) {
	report := &verify.Report{Findings: []verify.Finding{
		{ID: "sdk.dependency", Severity: verify.Error},
		{ID: "sdk.native_sync", Severity: verify.Error},
		{ID: "sdk.key.unfilled", Severity: verify.Error},
		{ID: "key.rejected", Severity: verify.Error},
		{ID: "ios.debug_logs", Severity: verify.Info, Fix: "add it"},
		{ID: "ios.debug_logs", Severity: verify.Pass},
	}}
	var got []string
	for _, f := range agentWork(report) {
		got = append(got, f.ID)
	}
	if len(got) != 2 || got[0] != "sdk.dependency" || got[1] != "ios.debug_logs" {
		t.Errorf("agent work = %q", got)
	}
}
