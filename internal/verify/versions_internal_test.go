package verify

import "testing"

// A declared version may carry a space after the operator, as a CocoaPods
// requirement does ("= 1.19.1"): what is stripped is trimmed too.
func TestOlderThanTrimsWhatItStrips(t *testing.T) {
	for _, tc := range []struct {
		declared string
		older    bool
	}{
		{"= 1.19.1", true},
		{"=1.19.1", true},
		{" v1.19.1 ", true},
		{"~ 1.19.0", true},
		{"^ 1.0.0", false},
		{"= 1.20.0", false},
	} {
		if got := olderThan(tc.declared, "1.20.0"); got != tc.older {
			t.Errorf("olderThan(%q, 1.20.0) = %v, want %v", tc.declared, got, tc.older)
		}
	}
}
