package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tenjin/tenjin-cli/internal/keyfill"
	"github.com/tenjin/tenjin-cli/internal/project"
	"github.com/tenjin/tenjin-cli/internal/testproject"
)

const (
	wrongKey = "WRONGKEY1234567890ABCDEFGHIJWRNG"
	goodKey  = "GOODKEY01234567890ABCDEFGHIJGOOD"
)

func lacks(t *testing.T, stdout string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(stdout, want) {
			t.Errorf("output lacks %q", want)
		}
	}
	if t.Failed() {
		t.Logf("output:\n%s", stdout)
	}
}

// Tenjin's API does not hand out SDK keys, so setup says exactly where to
// copy one from, and checks what is pasted before anything is built on it.
func TestSetupWalksThroughGettingTheKey(t *testing.T) {
	// A project that declares the SDK and does not call it yet.
	h := newHarness(t)
	h.fake.addApp("Sample", "com.example.sample", "android")
	h.signIn()
	base := testproject.AndroidNative()
	const activity = "app/src/main/java/com/example/sample/MainActivity.kt"
	dir := testproject.Write(t, base.With(testproject.Files{
		activity: "package com.example.sample\n\nimport android.app.Activity\n\nclass MainActivity : Activity()\n",
	}))
	h.fake.keyAnswers[wrongKey] = "unauthorized"

	// A wrong key, then the right one, then stop at the final check.
	code, stdout, _ := h.typing("", wrongKey, " '"+goodKey+"' ", "q").run("setup", dir)
	lacks(t, stdout,
		"Step 4 of 7 · SDK keys",
		"Android SDK key, for com.example.sample:",
		"1. Open "+h.fake.srv.URL+"/dashboard/apps/00000000-0000-4000-8000-000000000001/sdk",
		`2. In the "SDK Keys" card, copy the key with the button beside it.`,
		`3. If the card lists no key, choose "Generate SDK Key".`,
		"✗   Android, key …WRNG: Tenjin does not accept this key for com.example.sample. It may be the key of another app",
		"Copy it again from the page above, or press Enter to skip.",
		// Quotes and spaces that came along with the paste are dropped.
		"✓   Android, key …GOOD: Tenjin accepts this key for com.example.sample",

		// With no agent installed, the next step says what to add and where.
		"To integrate it yourself:",
		"Android (the app starts in app/src/main/java/com/example/sample/MainActivity.kt)",
		`1. In the launcher Activity's onResume(), call TenjinSDK.getInstance(this, "<SDK_KEY>").connect().`,
		"Where a step says <SDK_KEY>, write __TENJIN_ANDROID_SDK_KEY__ for Android exactly.",
		"tenjin sdk guide android",

		// The key is not in the source yet; the pasted one is checked, and
		// the report says which key that was.
		"Tenjin accepts the SDK key …GOOD (from the key you pasted) for com.example.sample",
	)
	// The dependency is already declared, so the guide does not ask for it.
	if strings.Contains(stdout, "Add implementation(\"com.tenjin:android-sdk") {
		t.Errorf("the guide repeats a step that is already done:\n%s", stdout)
	}
	if code != 9 {
		t.Errorf("exit = %d, want 9 while the SDK is not called", code)
	}
	if strings.Contains(stdout, goodKey) || strings.Contains(stdout, wrongKey) {
		t.Error("a whole SDK key was printed")
	}
}

func TestSetupKeyCanBeSkipped(t *testing.T) {
	h, dir := placeholderAndroid(t)
	_, stdout, _ := h.typing("", "", "q").run("setup", dir)
	// The placeholder finding says what to do; "not checked" beside it would
	// say nothing more.
	lacks(t, stdout, "Skipped. The placeholder stays where the key belongs", "The SDK key is still a placeholder")
	if strings.Contains(stdout, "The SDK key was not checked") {
		t.Errorf("the placeholder is also reported as an unchecked key:\n%s", stdout)
	}
	if len(h.fake.keysAsked) != 0 {
		t.Errorf("Tenjin was asked about %q with no key given", h.fake.keysAsked)
	}
}

// Without a terminal nothing can be pasted, so the same instructions end
// with how to pass the key in.
func TestSetupWithoutATerminalSaysHowToPassTheKey(t *testing.T) {
	h, dir := placeholderAndroid(t)
	_, stdout, _ := h.run("setup", dir)
	lacks(t, stdout, "1. Open "+h.fake.srv.URL, `"SDK Keys" card`, "Then pass it with --sdk-key android=<key>.")
	if strings.Contains(stdout, "Paste the") {
		t.Error("setup prompted without a terminal")
	}
}

func TestSetupNeedsNoKeyTheProjectAlreadyHas(t *testing.T) {
	h := newHarness(t)
	h.fake.addApp("Sample", "com.example.sample", "android")
	h.signIn()
	dir := testproject.Write(t, testproject.AndroidNative())
	_, stdout, _ := h.typing("", "this must not be read as a key").run("setup", dir)
	lacks(t, stdout, "✓ Android: the project already holds a key Tenjin accepts (app/src/main/java/com/example/sample/MainActivity.kt)")
	if strings.Contains(stdout, "Paste the") || strings.Contains(stdout, "SDK Keys") {
		t.Errorf("setup asked for a key the project already has:\n%s", stdout)
	}
}

// flutterWithoutKeys is a signed-in harness, an app per platform, and a
// Flutter project whose key is still a placeholder.
func flutterWithoutKeys(t *testing.T) (*harness, string) {
	t.Helper()
	h := newHarness(t)
	h.fake.addApp("Sample", "com.example.sample", "android")
	h.fake.addApp("Sample", "com.example.sample", "ios")
	h.signIn()
	base := testproject.Flutter()
	return h, testproject.Write(t, base.With(testproject.Files{"lib/main.dart": strings.Replace(base["lib/main.dart"], testproject.Key, "YOUR_SDK_KEY", 1)}))
}

// A Tenjin app is one platform, so a cross-platform project has two keys,
// and each is checked against its own app.
func TestSetupTakesAKeyPerPlatform(t *testing.T) {
	h, dir := flutterWithoutKeys(t)
	h.fake.keyAnswers[wrongKey] = "unauthorized"

	_, stdout, _ := h.run("setup", dir, "--sdk-key", "android="+goodKey, "--sdk-key", "ios="+wrongKey)
	lacks(t, stdout,
		"Each app has its own SDK key, so this project needs 2.",
		"✓ Android, key …GOOD from --sdk-key: Tenjin accepts this key for com.example.sample",
		"✗ iOS, key …WRNG from --sdk-key: Tenjin does not accept this key for com.example.sample",
	)
	// Offline, a key is taken as given, and said to be unchecked.
	h, dir = flutterWithoutKeys(t)
	_, stdout, _ = h.run("setup", dir, "--offline", "--sdk-key", goodKey)
	lacks(t, stdout, "! Android, key …GOOD from --sdk-key: not checked against Tenjin (--offline)", "! iOS, key …GOOD")
	if len(h.fake.keysAsked) != 0 {
		t.Errorf("--offline still asked Tenjin about %q", h.fake.keysAsked)
	}
}

func TestDoctorTakesAKeyPerPlatform(t *testing.T) {
	asked := func(h *harness) string { return strings.Join(h.fake.keysAsked, ",") }

	h, dir := flutterWithoutKeys(t)
	h.run("doctor", dir, "--sdk-key", "android=ANDROIDKEY", "--sdk-key", "IOS=IOSKEY")
	if got := asked(h); got != "ANDROIDKEY,IOSKEY" {
		t.Errorf("flags: keys asked = %q", got)
	}

	// One key with no platform serves both, as an organization's key does.
	h, dir = flutterWithoutKeys(t)
	_, stdout, _ := h.run("doctor", dir, "--sdk-key", goodKey)
	if got := asked(h); got != goodKey+","+goodKey || strings.Count(stdout, "(from --sdk-key)") != 2 {
		t.Errorf("one key: keys asked = %q\n%s", got, stdout)
	}

	// The environment works the same way; a flag wins over it for its platform.
	h, dir = flutterWithoutKeys(t)
	h.vars["TENJIN_SDK_KEY"] = "ANYKEY"
	h.vars["TENJIN_SDK_KEY_IOS"] = "IOSENVKEY"
	_, stdout, _ = h.run("doctor", dir)
	if got := asked(h); got != "ANYKEY,IOSENVKEY" || !strings.Contains(stdout, "(from TENJIN_SDK_KEY_IOS)") {
		t.Errorf("environment: keys asked = %q\n%s", got, stdout)
	}
	h.fake.keysAsked = nil
	h.run("doctor", dir, "--sdk-key", "ios=IOSFLAGKEY")
	if got := asked(h); got != "ANYKEY,IOSFLAGKEY" {
		t.Errorf("flag over environment: keys asked = %q", got)
	}

	code, _, stderr := h.run("doctor", dir, "--sdk-key", "windows=KEY")
	if code != 2 || !strings.Contains(stderr, `unknown platform "windows"`) || !strings.Contains(stderr, "android=<key>") {
		t.Errorf("unknown platform: exit = %d, stderr = %q", code, stderr)
	}
}

// A key written in the project is what the app will send, so setup's last
// check is about that one, even when another was passed in.
func TestSetupChecksTheKeyInTheProjectOverTheOneGiven(t *testing.T) {
	h := newHarness(t)
	h.fake.addApp("Sample", "com.example.sample", "android")
	h.signIn()
	dir := testproject.Write(t, testproject.AndroidNative())
	_, stdout, _ := h.run("setup", dir, "--sdk-key", goodKey)
	if strings.Contains(stdout, "…GOOD") || !strings.Contains(stdout, "Tenjin accepts the SDK key …MNBV for com.example.sample") {
		t.Errorf("the final check is not about the project's own key:\n%s", stdout)
	}
}

// Seen on production: a key from another organization, which happened to
// have some of the same bundle IDs. Tenjin's answer is "no such app", which
// reads as if the app were missing. setup has just found the app, so it can
// say what is really wrong.
func TestSetupSaysWhenAKeyIsFromAnotherOrganization(t *testing.T) {
	h, dir := placeholderAndroid(t)
	h.fake.keyAnswers[wrongKey] = "no such app"
	_, stdout, _ := h.run("setup", dir, "--sdk-key", wrongKey)
	lacks(t, stdout,
		`Android com.example.sample: found "Sample"`,
		"✗ Android, key …WRNG from --sdk-key: This key is from a different organization than this app",
		// The report at the end names both causes, and the way out.
		"Either the build's bundle ID is not the app's, or the key comes from a different organization than the app.",
		"Fix: Use the SDK key from this app's own page in the dashboard",
	)
}

// agentPlaceholderAndroid is an Android project in which a coding agent left
// the key placeholder.
func agentPlaceholderAndroid(t *testing.T) (*harness, string) {
	t.Helper()
	h := newHarness(t)
	base := testproject.AndroidNative()
	return h, testproject.Write(t, base.With(testproject.Files{
		androidActivity: strings.Replace(base[androidActivity], testproject.Key, keyfill.Placeholder(project.Android), 1),
	}))
}

const androidActivity = "app/src/main/java/com/example/sample/MainActivity.kt"

func TestSetKeysWritesACheckedKeyInPlaceOfThePlaceholder(t *testing.T) {
	h, dir := agentPlaceholderAndroid(t)
	before, _ := os.ReadFile(filepath.Join(dir, androidActivity))

	code, stdout, stderr := h.run("project", "set-keys", dir, "--sdk-key", "android="+goodKey, "--json")
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	var result struct {
		Filled []struct {
			Platform, File string
			Line           int
		}
		Missing, Refused []string
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("%v\n%s", err, stdout)
	}
	if len(result.Filled) != 1 || result.Filled[0].File != androidActivity || result.Filled[0].Line == 0 {
		t.Errorf("result = %+v", result)
	}
	after, _ := os.ReadFile(filepath.Join(dir, androidActivity))
	if want := strings.Replace(string(before), keyfill.Placeholder(project.Android), goodKey, 1); string(after) != want {
		t.Errorf("the file changed beyond the placeholder:\n%s", after)
	}
	if strings.Contains(stdout+stderr, goodKey) {
		t.Error("the whole key was printed")
	}
}

func TestSetKeysWritesNoKeyTenjinRefuses(t *testing.T) {
	h, dir := agentPlaceholderAndroid(t)
	h.fake.keyAnswers[wrongKey] = "unauthorized"

	code, stdout, _ := h.run("project", "set-keys", dir, "--sdk-key", "android="+wrongKey)
	source, _ := os.ReadFile(filepath.Join(dir, androidActivity))
	if code != 9 || strings.Contains(string(source), wrongKey) || !strings.Contains(string(source), keyfill.Placeholder(project.Android)) {
		t.Errorf("exit = %d\n%s", code, stdout)
	}
}

func TestSetKeysWithoutAKeyOrATerminal(t *testing.T) {
	h, dir := agentPlaceholderAndroid(t)
	code, _, stderr := h.run("project", "set-keys", dir)
	// Not a usage error, and no invitation to pass the key, which a coding
	// agent reading this must never do.
	if code != 5 || strings.Contains(stderr, "--sdk-key") || !strings.Contains(stderr, "tenjin setup") {
		t.Errorf("exit = %d, stderr = %q", code, stderr)
	}
}

func TestSetKeysAsksAtATerminal(t *testing.T) {
	h, dir := agentPlaceholderAndroid(t)
	code, stdout, _ := h.typing(goodKey).run("project", "set-keys", dir)
	source, _ := os.ReadFile(filepath.Join(dir, androidActivity))
	if code != 0 || !strings.Contains(string(source), goodKey) {
		t.Errorf("exit = %d\n%s", code, stdout)
	}
	lacks(t, stdout, "Android SDK key, for com.example.sample:", `"SDK Keys" card`)
}

func TestSetKeysWithNoPlaceholderChangesNothing(t *testing.T) {
	h := newHarness(t)
	dir := testproject.Write(t, testproject.AndroidNative())
	code, stdout, _ := h.run("project", "set-keys", dir, "--sdk-key", "android="+goodKey)
	if code != 0 || len(h.fake.keysAsked) != 0 {
		t.Errorf("exit = %d, key checks = %q\n%s", code, h.fake.keysAsked, stdout)
	}
}

// An agent working from the skill leaves a placeholder; setup asks for the key
// and writes it, with no separate command to run.
func TestSetupFillsAPlaceholderAnAgentLeft(t *testing.T) {
	h, dir := agentPlaceholderAndroid(t)
	h.fake.addApp("Sample", "com.example.sample", "android")
	h.signIn()

	code, stdout, _ := h.typing("", goodKey, "s").run("setup", dir)
	source, _ := os.ReadFile(filepath.Join(dir, androidActivity))
	if code != 0 || !strings.Contains(string(source), goodKey) {
		t.Errorf("exit = %d\n%s", code, stdout)
	}
	lacks(t, stdout, "✓ Wrote the Android SDK key at "+androidActivity, "Nothing here would stop events reaching Tenjin")
	if strings.Contains(stdout, "set-keys") || strings.Contains(stdout, goodKey) {
		t.Errorf("setup sent the developer elsewhere, or printed the key:\n%s", stdout)
	}
}

// A key exported in the shell for another project is not a mistake in this
// one: a variable for a platform the project does not build for is left
// unused, said only with --verbose. A flag for that platform is a mistake,
// and the error names the flag, not a variable.
func TestAnEnvironmentKeyForAPlatformTheProjectLacksIsNotUsed(t *testing.T) {
	h := newHarness(t)
	dir := testproject.Write(t, testproject.AndroidNative())
	h.vars["TENJIN_SDK_KEY_IOS"] = "IOSKEYOFANOTHERPROJECT0123456789"
	for _, args := range [][]string{{"doctor", dir, "--offline"}, {"setup", dir}, {"project", "set-keys", dir}} {
		code, _, stderr := h.run(args...)
		if code == 2 || strings.Contains(stderr, "does not build for iOS") {
			t.Errorf("%v: exit = %d, stderr = %q", args, code, stderr)
		}
	}
	_, _, stderr := h.run("doctor", dir, "--offline", "--verbose")
	if !strings.Contains(stderr, "TENJIN_SDK_KEY_IOS is not used: this project does not build for iOS") {
		t.Errorf("--verbose does not say the variable was passed over:\n%s", stderr)
	}
	if len(h.fake.keysAsked) != 0 {
		t.Errorf("Tenjin was asked about %q", h.fake.keysAsked)
	}

	code, _, stderr := h.run("doctor", dir, "--sdk-key", "ios=IOSKEYOFANOTHERPROJECT0123456789")
	if code != 2 || !strings.Contains(stderr, "--sdk-key ios=…: this project does not build for iOS") {
		t.Errorf("flag: exit = %d, stderr = %q", code, stderr)
	}
	if strings.Contains(stderr, "TENJIN_SDK_KEY") {
		t.Errorf("the error about a flag names a variable:\n%s", stderr)
	}
}

// A key exported with the newline or the quotes that came along when it was
// copied is cleaned as one passed with the flag is: what is written in the
// source is the key alone.
func TestEnvironmentKeysAreCleanedLikeTheFlag(t *testing.T) {
	for _, variable := range []string{"TENJIN_SDK_KEY", "TENJIN_SDK_KEY_ANDROID"} {
		t.Run(variable, func(t *testing.T) {
			h, dir := agentPlaceholderAndroid(t)
			h.vars[variable] = " \"" + goodKey + "\"\n"
			code, stdout, stderr := h.run("project", "set-keys", dir, "--offline")
			source, _ := os.ReadFile(filepath.Join(dir, androidActivity))
			if code != 0 || !strings.Contains(string(source), `"`+goodKey+`"`) {
				t.Errorf("exit = %d, stderr = %q\n%s\n%s", code, stderr, stdout, source)
			}
		})
	}
	// The variable of a build other than the main one is read later, once
	// the project's builds are known; it is cleaned the same way.
	h, dir := multiStore(t, placeholderStores())
	h.vars["TENJIN_SDK_KEY_ANDROID"] = testproject.PlayKey
	h.vars["TENJIN_SDK_KEY_ANDROID_AMAZON"] = testproject.AmazonKey + "\n"
	code, stdout, stderr := h.run("project", "set-keys", dir, "--offline")
	source, _ := os.ReadFile(filepath.Join(dir, flavorBuild))
	if code != 0 || !strings.Contains(string(source), `"\"`+testproject.AmazonKey+`\""`) {
		t.Errorf("exit = %d, stderr = %q\n%s\n%s", code, stderr, stdout, source)
	}
}

// Something that cannot be a key is not sent to Tenjin to check.
func TestPastedTextThatIsNotAKey(t *testing.T) {
	h, dir := placeholderAndroid(t)
	_, stdout, _ := h.typing("", "q", "", "q").run("setup", dir)
	if !strings.Contains(stdout, "That is not an SDK key") || len(h.fake.keysAsked) != 0 {
		t.Errorf("keys asked = %q\n%s", h.fake.keysAsked, stdout)
	}
}
