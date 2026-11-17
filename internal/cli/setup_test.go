package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tenjin/tenjin-cli/internal/keyfill"
	"github.com/tenjin/tenjin-cli/internal/project"
	"github.com/tenjin/tenjin-cli/internal/testproject"
)

// unreadAndroidWithPlaceholder is an Android project whose applicationId is
// a setting the CLI cannot resolve, so its bundle ID is not read and the
// platform has no builds, with the key placeholder a coding agent left.
func unreadAndroidWithPlaceholder() testproject.Files {
	base := testproject.AndroidNative()
	return base.With(testproject.Files{
		"app/build.gradle.kts": strings.Replace(base["app/build.gradle.kts"], `applicationId = "com.example.sample"`, "applicationId = appId", 1),
		androidActivity:        strings.Replace(base[androidActivity], testproject.Key, keyfill.Placeholder(project.Android), 1),
	})
}

// A platform whose bundle ID could not be read has one app all the same, and
// the key given for it goes where its placeholder is, as set-keys and doctor
// write it: unchecked, for want of a bundle ID to check it against, and said
// so. Step 4 settles it, and steps 5 and 6 find it written.
func TestSetupWritesTheKeyGivenForAPlatformWhoseBundleIDIsUnread(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	dir := testproject.Write(t, unreadAndroidWithPlaceholder())

	code, stdout, stderr := h.run("setup", dir, "--yes", "--agent", "none", "--sdk-key", "android="+goodKey)
	source, _ := os.ReadFile(filepath.Join(dir, androidActivity))
	if code != 0 || !strings.Contains(string(source), `"`+goodKey+`"`) {
		t.Errorf("exit = %d, stderr = %q\n%s", code, stderr, stdout)
	}
	lacks(t, stdout,
		"Android: the bundle ID could not be read, so its app was not looked up.",
		"! Android, key …GOOD from --sdk-key: not checked: the bundle ID could not be read",
		"✓ Wrote the Android SDK key at "+androidActivity,
	)
	if strings.Contains(stdout, "still the placeholder") || strings.Contains(stdout, goodKey) {
		t.Errorf("the placeholder was left, or the key printed:\n%s", stdout)
	}
}

// unreal is an Unreal Engine project for Android, without the Tenjin plugin.
// This version detects it and checks nothing in it.
func unreal() testproject.Files {
	return testproject.Files{
		"Game.uproject":            `{"FileVersion": 3}`,
		"Source/Game/Game.cpp":     "// code\n",
		"Config/DefaultEngine.ini": "[/Script/AndroidRuntimeSettings.AndroidRuntimeSettings]\nPackageName=com.example.sample\n",
	}
}

// A report with no errors is not a finished integration: a framework this
// version cannot check has none either way. setup says what it cannot check
// and where the guide is, and claims nothing.
func TestSetupSaysWhenItCannotCheckTheFramework(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	dir := testproject.Write(t, unreal())

	code, stdout, stderr := h.run("setup", dir, "--offline", "--yes", "--agent", "none")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q\n%s", code, stderr, stdout)
	}
	lacks(t, stdout,
		"Tenjin SDK  not found",
		"The Tenjin SDK was not found in this project, and this version cannot check Unreal Engine projects beyond that.",
		"The full guide:\n  https://github.com/tenjin/tenjin-unreal-sdk",
		"Nothing here was checked: this version has no checks for Unreal Engine projects.",
	)
	for _, claim := range []string{"already set up", "Nothing here would stop events reaching Tenjin"} {
		if strings.Contains(stdout, claim) {
			t.Errorf("setup claims %q of a project it cannot check:\n%s", claim, stdout)
		}
	}

	// With the plugin in the project, that is said, and the guide still is.
	dir = testproject.Write(t, unreal().With(testproject.Files{"Plugins/TenjinSDK/TenjinSDK.uplugin": `{"VersionName": "1.1.0"}`}))
	code, stdout, _ = h.run("setup", dir, "--offline", "--yes", "--agent", "none")
	lacks(t, stdout,
		"The Tenjin SDK is in this project (Plugins/TenjinSDK/TenjinSDK.uplugin), and this version cannot check Unreal Engine projects beyond that: follow the guide to make sure it is set up.",
		"The full guide:\n  https://github.com/tenjin/tenjin-unreal-sdk",
	)
	if code != 0 || strings.Contains(stdout, "already set up") {
		t.Errorf("exit = %d\n%s", code, stdout)
	}
}
