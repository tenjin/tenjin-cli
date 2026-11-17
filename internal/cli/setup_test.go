package cli

import (
	"strings"
	"testing"

	"github.com/tenjin/tenjin-cli/internal/testproject"
)

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
