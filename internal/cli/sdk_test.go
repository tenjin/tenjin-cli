package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tenjin/tenjin-cli/internal/testproject"
)

func TestSDKGuideByName(t *testing.T) {
	h := newHarness(t)
	code, stdout, stderr := h.run("sdk", "guide", "android")
	if code != 0 || stderr != "" {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	lacks(t, stdout,
		"tenjin/sdk-llm-guides at ",
		"Where this guide says `<TENJIN_SDK_VERSION>`, use **2.0.0**",
		"write the build's placeholder",
		"Tenjin Android SDK",
	)
	if code, _, stderr := h.run("sdk", "guide", "cordova"); code != 2 || !strings.Contains(stderr, "android, ios, flutter") {
		t.Errorf("exit = %d, stderr = %q", code, stderr)
	}
}

// With no platform named, the project's own guide.
func TestSDKGuideForTheProject(t *testing.T) {
	h := newHarness(t)
	for _, c := range []struct {
		files testproject.Files
		want  string
	}{
		{testproject.Flutter(), "Tenjin Flutter"},
		{testproject.IOSNative(), "Tenjin iOS"},
		{testproject.Unity(), "Unity"},
	} {
		dir := testproject.Write(t, c.files)
		code, stdout, stderr := h.run("sdk", "guide", "--path", dir, "--json")
		var g struct {
			Name, Package, Revision, Content string
			SDKVersion                       string `json:"sdk_version"`
		}
		if err := json.Unmarshal([]byte(stdout), &g); err != nil || code != 0 {
			t.Fatalf("exit = %d, err = %v, stderr = %q", code, err, stderr)
		}
		if !strings.Contains(g.Content, c.want) || g.SDKVersion == "" || g.Revision == "" {
			t.Errorf("%s: guide = %s %s %s, %d bytes", c.want, g.Name, g.Package, g.SDKVersion, len(g.Content))
		}
	}
}

func TestSDKGuideNeedsAPlatformWhereThereAreTwo(t *testing.T) {
	h := newHarness(t)
	both := testproject.AndroidNative()
	for path, content := range testproject.IOSNative() {
		both["ios/"+path] = content
	}
	dir := testproject.Write(t, both)
	code, _, stderr := h.run("sdk", "guide", "--path", dir)
	if code != 2 || !strings.Contains(stderr, "tenjin sdk guide android, or tenjin sdk guide ios") {
		t.Errorf("exit = %d, stderr = %q", code, stderr)
	}
}
