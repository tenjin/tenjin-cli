package cli

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tenjin/tenjin-cli/internal/sdkrelease"
	"github.com/tenjin/tenjin-cli/internal/testproject"
)

// doctor names the newest release the registries know; --offline keeps to
// the one the CLI was built with, and asks no registry.
func TestDoctorNamesTheNewestRelease(t *testing.T) {
	base := testproject.AndroidNative()
	const build = "app/build.gradle.kts"
	dir := testproject.Write(t, base.With(testproject.Files{build: strings.Replace(base[build], "android-sdk:2.0.0", "android-sdk:1.24.0", 1)}))
	h := newHarness(t)
	asked := 0
	h.env.sdkReleases = &sdkrelease.Lookup{
		State: filepath.Join(t.TempDir(), "sdk-releases.json"),
		Sources: map[string]sdkrelease.Source{"com.tenjin:android-sdk": func(context.Context, *http.Client) (string, error) {
			asked++
			return "2.0.3", nil
		}},
	}
	h.vars["TENJIN_SDK_KEY"] = testproject.Key

	_, stdout, _ := h.run("doctor", dir)
	if !strings.Contains(stdout, "the current release is 2.0.3") {
		t.Errorf("stdout:\n%s", stdout)
	}
	_, stdout, _ = h.run("doctor", dir, "--offline")
	if !strings.Contains(stdout, "the current release is 2.0.0") || asked != 1 {
		t.Errorf("asked %d times; stdout:\n%s", asked, stdout)
	}
}
