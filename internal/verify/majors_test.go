package verify

import "testing"

// A new major version is a CLI change: whoever moves the table to one says
// what it changes for an app's code.
func TestEveryMajorVersionSaysWhatItChanges(t *testing.T) {
	for _, sdk := range latestSDK {
		v, ok := parseVersion(sdk.version)
		if !ok {
			t.Fatalf("%s: %q is not a version", sdk.packagePrefix, sdk.version)
		}
		for m := 2; m <= v[0]; m++ {
			if c, ok := majorChanges[sdk.packagePrefix][m]; !ok || c.text == "" {
				t.Errorf("%s %d.x: no entry in majorChanges", sdk.packagePrefix, m)
			}
		}
		if releaseNotes[sdk.packagePrefix] == "" {
			t.Errorf("%s: no release notes address", sdk.packagePrefix)
		}
	}
}
