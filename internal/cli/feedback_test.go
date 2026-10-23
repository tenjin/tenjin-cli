package cli

import (
	"strings"
	"testing"

	"github.com/tenjin/tenjin-cli/internal/testproject"
)

// The sign-in says who, and names the environment only when it is not
// Tenjin's own; the short-lived token under it is not the person's concern.
func TestTheSignInSaysWhoAndNoMore(t *testing.T) {
	if got := environmentNote("production"); got != "" {
		t.Errorf("production: %q", got)
	}
	if got := environmentNote("test"); got != " (test)" {
		t.Errorf("test: %q", got)
	}

	h := newHarness(t)
	h.signIn()
	_, stdout, _ := h.run("auth", "status")
	if stdout != "Signed in to Acme Games (test).\nThe session renews itself as you use the Tenjin CLI, until 'tenjin logout'.\n" {
		t.Errorf("auth status:\n%s", stdout)
	}
	_, stdout, _ = h.typing("", "q").run("setup", testproject.Write(t, testproject.AndroidNative()))
	signIn := stdout[strings.Index(stdout, "Step 2 of 7"):]
	signIn = signIn[:strings.Index(signIn, "Step 3 of 7")]
	if !strings.Contains(signIn, "Signed in to Acme Games (test).\n") || strings.Contains(signIn, "token") || strings.Contains(signIn, "renews") {
		t.Errorf("setup talks about the token:\n%s", stdout)
	}
}

// A new Tenjin app is named after the project, not the folder it is in.
func TestANewAppIsNamedAfterTheProject(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	dir := testproject.Write(t, testproject.AndroidNative().With(testproject.Files{"settings.gradle.kts": "rootProject.name = \"Timer\"\ninclude(\":app\")\n"}))
	_, stdout, _ := h.typing("", "n").run("setup", dir)
	if !strings.Contains(stdout, `Create "Timer (Android)"?`) {
		t.Errorf("stdout:\n%s", stdout)
	}
}

// between is the text from one marker to the next, or to the end.
func between(text, from, to string) string {
	i := strings.Index(text, from)
	if i < 0 {
		return ""
	}
	text = text[i:]
	if j := strings.Index(text, to); j > 0 {
		return text[:j]
	}
	return text
}

// What is still open names its platform where the project has two, so the
// same finding for each does not read as one line said twice; a key Tenjin
// refuses in the source is said to be the developer's to replace.
func TestWhatIsOpenSaysWhichPlatform(t *testing.T) {
	h := newHarness(t)
	h.fake.addApp("Sample", "com.example.sample", "android")
	h.fake.addApp("Sample", "com.example.sample", "ios")
	h.signIn()
	h.fake.keyAnswers[testproject.Key] = "unauthorized"
	dir := testproject.Write(t, testproject.Flutter())

	code, stdout, _ := h.typing("", "", "", "", "q").run("setup", dir, "--agent", "none")
	if code != 9 {
		t.Errorf("exit = %d", code)
	}
	step5 := between(stdout, "Step 5 of 7", "Step 6 of 7")
	lacks(t, step5,
		"✗ Android: Tenjin does not accept the SDK key …MNBV for com.example.sample  lib/main.dart:7\n",
		"✗ iOS: Tenjin does not accept the SDK key …MNBV for com.example.sample  lib/main.dart:7\n",
		"Tenjin refuses the SDK key written in lib/main.dart:7, and setup writes keys only over its own placeholders: replace it there",
	)
	if strings.Contains(step5, "which the next step settles") {
		t.Errorf("step 5 says the next step settles a key it does not write:\n%s", step5)
	}
	lacks(t, between(stdout, "Fix the items marked", "Still open"),
		"✗ Android: Tenjin does not accept the SDK key", "✗ iOS: Tenjin does not accept the SDK key")

	// One platform reads as it did.
	h = newHarness(t)
	h.fake.addApp("Sample", "com.example.sample", "android")
	h.signIn()
	h.fake.keyAnswers[testproject.Key] = "unauthorized"
	_, stdout, _ = h.typing("", "", "", "q").run("setup", testproject.Write(t, testproject.AndroidNative()), "--agent", "none")
	lacks(t, between(stdout, "Step 5 of 7", "Step 6 of 7"), "  ✗ Tenjin does not accept the SDK key …MNBV for com.example.sample  app/src/main/java/com/example/sample/MainActivity.kt:")
}

// With several builds, a line is labelled with its build unless its title
// names it already.
func TestWhatIsOpenSaysWhichBuild(t *testing.T) {
	base := testproject.AndroidMultiStore()
	h, dir := multiStore(t, base.With(testproject.Files{
		flavorBuild: strings.Replace(base[flavorBuild], testproject.AmazonKey, "__TENJIN_ANDROID_SDK_KEY_AMAZON__", 1),
	}))
	h.fake.addApp("Sample", "com.example.sample", "android")
	h.fake.addApp("Sample", "com.example.sample", "amazon")
	h.signIn()
	h.fake.keyAnswers[testproject.PlayKey] = "not logged"

	_, stdout, _ := h.typing("", "", "", "", "", "", "q").run("setup", dir, "--agent", "none")
	step5 := between(stdout, "Step 5 of 7", "Step 6 of 7")
	lacks(t, step5,
		"✗ Android, play (Google Play): The SDK key …PLAY is disabled  app/build.gradle.kts:",
		"✗ The SDK key of the amazon build, Amazon Appstore is still the placeholder __TENJIN_ANDROID_SDK_KEY_AMAZON__",
		"Tenjin refuses the SDK key written in app/build.gradle.kts:",
		"The next step settles the other SDK keys.",
	)
	lacks(t, between(stdout, "Fix the items marked", "Still open"), "✗ Android, play (Google Play): The SDK key …PLAY is disabled")
}
