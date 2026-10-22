package cli

import (
	"encoding/json"
	"testing"

	"github.com/tenjin/tenjin-cli/internal/testproject"
)

// Reading the app's analytics reads every source file again, so doctor
// does it only for the features that use them.
func TestDoctorReadsTheAnalyticsOnlyForTheFeaturesThatUseThem(t *testing.T) {
	dir := testproject.Write(t, withAnalytics())
	h := newHarness(t)
	events := func(args ...string) int {
		t.Helper()
		code, stdout, stderr := h.run(append([]string{"doctor", dir, "--offline", "--json", "--agent", "none"}, args...)...)
		var out struct {
			Project struct {
				Analytics struct {
					Events  []json.RawMessage `json:"events"`
					UserIDs []json.RawMessage `json:"user_ids"`
				} `json:"analytics"`
			} `json:"project"`
		}
		if err := json.Unmarshal([]byte(stdout), &out); err != nil {
			t.Fatalf("exit = %d, stderr = %q: %v", code, stderr, err)
		}
		if out.Project.Analytics.Events == nil || out.Project.Analytics.UserIDs == nil {
			t.Errorf("analytics without its lists:\n%s", stdout)
		}
		return len(out.Project.Analytics.Events)
	}
	if n := events(); n != 0 {
		t.Errorf("doctor read %d events with no feature that uses them", n)
	}
	if n := events("--features", "opens,purchases"); n != 0 {
		t.Errorf("doctor read %d events with no feature that uses them", n)
	}
	if n := events("--features", "custom_events"); n != 2 {
		t.Errorf("doctor read %d events for custom events, want 2", n)
	}
	if n := events("--user-id", "user.id"); n != 2 {
		t.Errorf("doctor read %d events for the user ID, want 2", n)
	}
}
