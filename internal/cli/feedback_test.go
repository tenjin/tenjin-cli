package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tenjin/tenjin-cli/internal/testproject"
	"github.com/tenjin/tenjin-cli/internal/verify"
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

// doctor's pointer to the upgrade names the project it checked, ready to
// paste into a shell.
func TestDoctorsUpgradeHintNamesTheProject(t *testing.T) {
	written := oldAndroid(t)
	dir := filepath.Join(filepath.Dir(written), "My App")
	if err := os.Rename(written, dir); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t)
	_, stdout, _ := h.run("doctor", dir, "--offline")
	lacks(t, stdout, "with your coding agent or by hand: tenjin sdk upgrade "+shellArg(dir, runtime.GOOS)+"\n")
	if !strings.Contains(shellArg(dir, runtime.GOOS), `My App`) || shellArg(dir, runtime.GOOS) == dir {
		t.Errorf("the path is not quoted: %s", shellArg(dir, runtime.GOOS))
	}

	// In the project itself, the command needs no path.
	t.Chdir(dir)
	_, stdout, _ = h.run("doctor", "--offline")
	lacks(t, stdout, "with your coding agent or by hand: tenjin sdk upgrade\n")

	for _, c := range []struct{ arg, goos, want string }{
		{"app", "linux", "app"},
		{"../my-app/v1.2", "darwin", "../my-app/v1.2"},
		{"My App", "darwin", "'My App'"},
		{"Bob's $HOME", "linux", `'Bob'\''s $HOME'`},
		{"-app", "linux", "./-app"},
		{`C:\Users\me\app`, "windows", `C:\Users\me\app`},
		{`C:\Users\me\My App`, "windows", `"C:\Users\me\My App"`},
	} {
		if got := shellArg(c.arg, c.goos); got != c.want {
			t.Errorf("shellArg(%q, %s) = %s, want %s", c.arg, c.goos, got, c.want)
		}
	}
}

// A key written in the code that Tenjin refuses stays what the app sends:
// step 4 says where it is when it finds the right one, and a skip does not
// talk of a placeholder.
func TestStepFourSaysWhereARefusedKeyIs(t *testing.T) {
	const wrong = "WRONGKEYWRONGKEYWRONGKEY1234"
	const activity = "app/src/main/java/com/example/sample/MainActivity.kt"
	base := testproject.AndroidNative()
	files := base.With(testproject.Files{activity: strings.Replace(base[activity], testproject.Key, wrong, 1)})

	h := newHarness(t)
	app := h.fake.addApp("Sample", "com.example.sample", "android")
	h.signIn()
	h.fake.keyAnswers[wrong] = "unauthorized"
	h.fake.serveKeys(app.ID, testproject.Key)
	_, stdout, _ := h.typing("", "q").run("setup", testproject.Write(t, files), "--agent", "none")
	if !strings.Contains(stdout, "The project holds another key for this app, at "+activity+":") || !strings.Contains(stdout, "replace it there with this one (…"+testproject.Key[len(testproject.Key)-4:]+")") {
		t.Errorf("fetched:\n%s", stdout)
	}

	h = newHarness(t)
	h.fake.addApp("Sample", "com.example.sample", "android")
	h.signIn()
	h.fake.keyAnswers[wrong] = "unauthorized"
	_, stdout, _ = h.typing("", "", "q").run("setup", testproject.Write(t, files), "--agent", "none")
	if !strings.Contains(stdout, "Skipped. The key written at "+activity+":") || strings.Contains(stdout, "The placeholder stays") {
		t.Errorf("skipped:\n%s", stdout)
	}
}

// A key step 4 found after Tenjin refused the one passed in is the one the
// checks use.
func TestAFoundKeyWinsOverARefusedGivenOne(t *testing.T) {
	const wrong = "WRONGKEYWRONGKEYWRONGKEY1234"
	given := verify.GivenKeys{ByBuild: map[string]verify.GivenKey{"android": {Value: wrong, Source: "--sdk-key"}}}
	s := &setup{given: given, keys: map[string]verify.GivenKey{"android": {Value: testproject.Key, Source: "the key you pasted"}}}
	if got := s.givenForChecks().ByBuild["android"].Value; got != testproject.Key {
		t.Errorf("checks use %q", maskKey(got))
	}
}
