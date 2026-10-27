package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tenjin/tenjin-cli/internal/testproject"
)

const flavorBuild = "app/build.gradle.kts"

// multiStore is a signed-in harness whose fake Tenjin knows each store's
// key as its own app's only, and the multi-store project.
func multiStore(t *testing.T, files testproject.Files) (*harness, string) {
	t.Helper()
	h := newHarness(t)
	h.fake.keyApps[testproject.PlayKey] = "com.example.sample|android"
	h.fake.keyApps[testproject.AmazonKey] = "com.example.sample|amazon"
	return h, testproject.Write(t, files)
}

func TestProjectDetectListsEachStoresApp(t *testing.T) {
	h, dir := multiStore(t, testproject.AndroidMultiStore())
	_, stdout, _ := h.run("project", "detect", dir)
	lacks(t, stdout, "Apps in Tenjin", "android:amazon", "Amazon Appstore", "__TENJIN_ANDROID_SDK_KEY_AMAZON__")

	_, stdout, _ = h.run("project", "detect", dir, "--json")
	var got struct {
		Targets []struct {
			Builds []struct {
				ID             string `json:"id"`
				TenjinPlatform string `json:"tenjin_platform"`
				Placeholder    string `json:"sdk_key_placeholder"`
			}
		}
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil || len(got.Targets) != 1 || len(got.Targets[0].Builds) != 2 {
		t.Fatalf("%v\n%s", err, stdout)
	}
	if b := got.Targets[0].Builds[1]; b.ID != "android:amazon" || b.TenjinPlatform != "amazon" || b.Placeholder != "__TENJIN_ANDROID_SDK_KEY_AMAZON__" {
		t.Errorf("amazon build = %+v", b)
	}
}

func TestDoctorChecksEachStoresKeyAgainstItsOwnApp(t *testing.T) {
	h, dir := multiStore(t, testproject.AndroidMultiStore())
	code, stdout, _ := h.run("doctor", dir)
	if code != 0 {
		t.Fatalf("exit = %d\n%s", code, stdout)
	}
	lacks(t, stdout,
		"Tenjin accepts the SDK key …PLAY for com.example.sample (the main build, Google Play)",
		"Tenjin accepts the SDK key …AMZN for com.example.sample (the amazon build, Amazon Appstore)",
	)
	if strings.Contains(stdout, testproject.PlayKey) || strings.Contains(stdout, testproject.AmazonKey) {
		t.Error("a whole key was printed")
	}
}

// The mistake this is all for: the Google Play key copied into the Amazon
// build, whose events Tenjin then refuses.
func TestDoctorCatchesOneStoresKeyInAnothersBuild(t *testing.T) {
	base := testproject.AndroidMultiStore()
	h, dir := multiStore(t, base.With(testproject.Files{
		flavorBuild: strings.Replace(base[flavorBuild], testproject.AmazonKey, testproject.PlayKey, 1),
	}))
	code, stdout, _ := h.run("doctor", dir, "--json")
	if code != 9 {
		t.Fatalf("exit = %d, want 9", code)
	}
	var report struct {
		Findings []struct{ ID, Severity, Build, Title string }
	}
	_ = json.Unmarshal([]byte(stdout), &report)
	found := false
	for _, f := range report.Findings {
		if f.ID == "key.rejected" && f.Build == "android:amazon" && f.Severity == "error" &&
			strings.Contains(f.Title, "No SDK key in the project is accepted for com.example.sample (the amazon build, Amazon Appstore)") {
			found = true
		}
		if f.ID == "key.valid" && f.Build == "android:amazon" {
			t.Errorf("the Google Play key passed for the Amazon build: %+v", f)
		}
	}
	if !found {
		t.Errorf("findings = %+v", report.Findings)
	}
}

func TestDoctorCanBeLimitedToBuilds(t *testing.T) {
	h, dir := multiStore(t, testproject.AndroidMultiStore())
	_, stdout, _ := h.run("doctor", dir, "--build", "android:amazon")
	if strings.Contains(stdout, "…PLAY") || !strings.Contains(stdout, "…AMZN") {
		t.Errorf("stdout:\n%s", stdout)
	}
	code, _, stderr := h.run("doctor", dir, "--build", "android:galaxy")
	if code != 2 || !strings.Contains(stderr, "android, android:amazon") {
		t.Errorf("exit = %d, stderr = %q", code, stderr)
	}
}

// placeholderStores is the multi-store project as a coding agent leaves it:
// a placeholder in each flavor.
func placeholderStores() testproject.Files {
	base := testproject.AndroidMultiStore()
	build := strings.Replace(base[flavorBuild], testproject.PlayKey, "__TENJIN_ANDROID_SDK_KEY__", 1)
	build = strings.Replace(build, testproject.AmazonKey, "__TENJIN_ANDROID_SDK_KEY_AMAZON__", 1)
	return base.With(testproject.Files{flavorBuild: build})
}

func TestSetupMakesAnAppAndWritesAKeyPerStore(t *testing.T) {
	h, dir := multiStore(t, placeholderStores())
	h.signIn()
	code, stdout, stderr := h.run("setup", dir, "--yes",
		"--sdk-key", "android="+testproject.PlayKey, "--sdk-key", "android:amazon="+testproject.AmazonKey)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q\n%s", code, stderr, stdout)
	}
	lacks(t, stdout,
		"This project ships builds that are separate apps in Tenjin",
		"this project needs 2 apps",
		`Android, amazon (Amazon Appstore) com.example.sample: created "`,
		"Wrote the SDK key of Android, play (Google Play) at "+flavorBuild,
		"Wrote the SDK key of Android, amazon (Amazon Appstore) at "+flavorBuild,
		"Nothing here would stop events reaching Tenjin",
	)
	platforms := map[string]bool{}
	for _, a := range h.fake.apps {
		platforms[a.BundleID+"|"+a.Platform+"|"+a.Name] = true
	}
	if !platforms["com.example.sample|android|"+filepath.Base(dir)+" (Android, Google Play)"] || !platforms["com.example.sample|amazon|"+filepath.Base(dir)+" (Android, Amazon Appstore)"] {
		t.Errorf("apps = %+v", h.fake.apps)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, flavorBuild))
	if !strings.Contains(string(raw), testproject.PlayKey) || !strings.Contains(string(raw), testproject.AmazonKey) {
		t.Errorf("build file:\n%s", raw)
	}
	if strings.Contains(stdout, testproject.AmazonKey) {
		t.Error("a whole key was printed")
	}
}

// A key given for the wrong store's app is refused before it is written.
func TestSetupRefusesOneStoresKeyForAnother(t *testing.T) {
	h, dir := multiStore(t, placeholderStores())
	h.signIn()
	code, stdout, _ := h.run("setup", dir, "--yes",
		"--sdk-key", "android="+testproject.PlayKey, "--sdk-key", "android:amazon="+testproject.PlayKey)
	lacks(t, stdout, "✗ Android, amazon (Amazon Appstore), key …PLAY from --sdk-key: Tenjin does not accept this key for com.example.sample")
	raw, _ := os.ReadFile(filepath.Join(dir, flavorBuild))
	if code != 9 || !strings.Contains(string(raw), "__TENJIN_ANDROID_SDK_KEY_AMAZON__") {
		t.Errorf("exit = %d\n%s", code, raw)
	}
}

func TestSetupAsksWhichBuildsShip(t *testing.T) {
	h, dir := multiStore(t, placeholderStores())
	h.signIn()
	_, stdout, _ := h.typing("", "1", "y", "", "q").run("setup", dir, "--sdk-key", "android="+testproject.PlayKey)
	lacks(t, stdout, "Which of these do you ship?", "Setting up 1 build.")
	for _, a := range h.fake.apps {
		if a.Platform == "amazon" {
			t.Errorf("an app was created for a build that does not ship: %+v", a)
		}
	}
	code, _, stderr := h.run("setup", dir, "--build", "android:galaxy")
	if code != 2 || !strings.Contains(stderr, "no such build") {
		t.Errorf("exit = %d, stderr = %q", code, stderr)
	}
}

// The agent's task names each store's placeholder, and where it goes.
func TestTheAgentIsToldEachStoresPlaceholder(t *testing.T) {
	base := testproject.AndroidMultiStore()
	build := strings.Replace(base[flavorBuild], `            buildConfigField("String", "TENJIN_SDK_KEY", "\"`+testproject.PlayKey+`\"")`+"\n", "", 1)
	build = strings.Replace(build, `            buildConfigField("String", "TENJIN_SDK_KEY", "\"`+testproject.AmazonKey+`\"")`+"\n", "", 1)
	// The INTERNET permission is missing too, so there is work for the agent.
	const manifest = "app/src/main/AndroidManifest.xml"
	withoutInternet := strings.Replace(base[manifest], `    <uses-permission android:name="android.permission.INTERNET" />`+"\n", "", 1)
	h, dir := multiStore(t, base.With(testproject.Files{flavorBuild: build, manifest: withoutInternet}))
	h.signIn()
	agent := h.installAgent()
	h.run("setup", dir, "--yes", "--agent", "claude")
	prompt := agent.read("prompt")
	for _, want := range []string{
		"with 2 builds that are separate apps in Tenjin",
		"variants amazon, bundle ID com.example.sample, Amazon Appstore: write the string __TENJIN_ANDROID_SDK_KEY_AMAZON__ exactly",
		"variants play, bundle ID com.example.sample, Google Play: write the string __TENJIN_ANDROID_SDK_KEY__ exactly",
		"buildConfigField",
		"two builds never share a placeholder",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the task lacks %q:\n%s", want, prompt)
		}
	}
}

func TestSetKeysPerStoreFromTheEnvironment(t *testing.T) {
	h, dir := multiStore(t, placeholderStores())
	h.vars["TENJIN_SDK_KEY_ANDROID"] = testproject.PlayKey
	h.vars["TENJIN_SDK_KEY_ANDROID_AMAZON"] = testproject.AmazonKey
	code, stdout, stderr := h.run("project", "set-keys", dir)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q\n%s", code, stderr, stdout)
	}
	lacks(t, stdout, "Wrote the SDK key of Android, amazon (Amazon Appstore)")
	code, _, stderr = h.run("project", "set-keys", dir, "--sdk-key", "android:galaxy=X")
	if code != 2 || !strings.Contains(stderr, `no build "android:galaxy"`) {
		t.Errorf("exit = %d, stderr = %q", code, stderr)
	}
}

// At a terminal each build's key is asked for in turn, with its own app's
// page, and checked against that app.
func TestSetupAsksForEachStoresKey(t *testing.T) {
	h, dir := multiStore(t, placeholderStores())
	h.signIn()
	// All builds; the Amazon key pasted for Google Play first, then the
	// right ones.
	code, stdout, _ := h.typing("", "", testproject.AmazonKey, testproject.PlayKey, testproject.AmazonKey).run("setup", dir, "--yes")
	if code != 0 {
		t.Fatalf("exit = %d\n%s", code, stdout)
	}
	lacks(t, stdout,
		"Android, play (Google Play) SDK key, for com.example.sample:",
		"Android, amazon (Amazon Appstore) SDK key, for com.example.sample:",
		"✗   Android, play (Google Play), key …AMZN: Tenjin does not accept this key for com.example.sample",
		"✓   Android, amazon (Amazon Appstore), key …AMZN: Tenjin accepts this key for com.example.sample",
		"Nothing here would stop events reaching Tenjin",
	)
}

// withBilling is the Android sample with Google Play Billing, so purchases
// are suggested.
func withBilling() testproject.Files {
	base := testproject.AndroidNative()
	return base.With(testproject.Files{
		"app/build.gradle.kts": strings.Replace(base["app/build.gradle.kts"], "dependencies {", "dependencies {\n    implementation(\"com.android.billingclient:billing-ktx:7.1.1\")", 1),
	})
}

func TestSetupAsksWhichFeatures(t *testing.T) {
	h := newHarness(t)
	h.fake.addApp("Sample", "com.example.sample", "android")
	h.signIn()
	dir := testproject.Write(t, withBilling())

	// Enter: the suggested ones, said back, and the purchases are then
	// expected.
	code, stdout, _ := h.typing("", "q").run("setup", dir)
	lacks(t, stdout,
		"Tenjin features for this integration",
		"1) App opens and attribution  installs and opens, by campaign (always included)",
		"2) In-app purchases           the app sells items: revenue per campaign",
		"suggested: Google Play Billing in app/build.gradle.kts",
		"Keep app opens: running setup again adds the rest.",
		"Press Enter when the",
		"Integrating: App opens and attribution, In-app purchases.",
		"In-app purchases: chosen for the integration, but not sent to Tenjin",
	)
	if code != 0 {
		t.Errorf("exit = %d, want 0: a missing feature is a warning", code)
	}

	// A number removes a feature that was included; Enter then confirms.
	_, stdout, _ = h.typing("2", "", "q").run("setup", dir)
	lacks(t, stdout, "Integrating: App opens and attribution.", "In-app purchases: could be sent to Tenjin")

	// none, then a number: only that one.
	_, stdout, _ = h.typing("none", "3", "").run("setup", dir, "--agent", "none")
	lacks(t, stdout, "Integrating: App opens and attribution, Subscriptions.")

	// App opens cannot be removed; an answer that is not in the list is
	// explained.
	_, stdout, _ = h.typing("1", "maybe", "", "q").run("setup", dir)
	lacks(t, stdout, "App opens and attribution are always included.", `"maybe" is not in the list.`)
}

func TestFeaturesFlag(t *testing.T) {
	h := newHarness(t)
	h.fake.addApp("Sample", "com.example.sample", "android")
	h.signIn()
	dir := testproject.Write(t, withBilling())
	code, _, stderr := h.run("setup", dir, "--features", "iap")
	if code != 2 || !strings.Contains(stderr, "purchases, subscriptions") {
		t.Errorf("exit = %d, stderr = %q", code, stderr)
	}
	// The choice made with the flag is said back, as one made at the prompt.
	_, stdout, _ := h.run("setup", dir, "--features", "purchases")
	lacks(t, stdout, "Integrating: App opens and attribution, In-app purchases (--features).")
	_, stdout, _ = h.run("setup", dir, "--features", "none")
	lacks(t, stdout, "Integrating: App opens and attribution (--features).")

	_, stdout, _ = h.run("doctor", dir, "--features", "purchases")
	lacks(t, stdout, "In-app purchases: chosen for the integration, but not sent to Tenjin")
	_, stdout, _ = h.run("doctor", dir, "--features", "none")
	lacks(t, stdout, "In-app purchases: could be sent to Tenjin")
	_, stdout, _ = h.run("project", "detect", dir)
	lacks(t, stdout, "Suggested features", "Google Play Billing in app/build.gradle.kts")
}

// The agent is told which features to integrate, and where the project
// already does what each measures.
func TestTheAgentIsToldTheChosenFeatures(t *testing.T) {
	h := newHarness(t)
	h.fake.addApp("Sample", "com.example.sample", "android")
	h.signIn()
	base := withBilling()
	const manifest = "app/src/main/AndroidManifest.xml"
	dir := testproject.Write(t, base.With(testproject.Files{
		manifest: strings.Replace(base[manifest], `    <uses-permission android:name="android.permission.INTERNET" />`+"\n", "", 1),
	}))
	agent := h.installAgent()
	h.run("setup", dir, "--agent", "claude", "--features", "purchases,custom_events")
	prompt := agent.read("prompt")
	for _, want := range []string{
		"Features to integrate, as the developer chose them:",
		"- App opens and attribution",
		"- In-app purchases: the project has Google Play Billing in app/build.gradle.kts; send it to Tenjin where that code runs",
		"- Custom events",
		"Integrate these and no others.",
		"tenjin sdk guide android",
		"In-app purchases: chosen for the integration, but not sent to Tenjin",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the task lacks %q:\n%s", want, prompt)
		}
	}
}

// A build left out with --build keeps its placeholder: setup neither asks
// for its key nor writes one, and doctor does not count it.
func TestABuildLeftOutKeepsItsPlaceholder(t *testing.T) {
	h, dir := multiStore(t, placeholderStores())
	h.signIn()
	h.fake.addApp("Sample (Amazon)", "com.example.sample", "amazon")

	code, stdout, stderr := h.run("setup", dir, "--yes", "--build", "android:amazon", "--sdk-key", "android:amazon="+testproject.AmazonKey)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q\n%s", code, stderr, stdout)
	}
	build, _ := os.ReadFile(filepath.Join(dir, flavorBuild))
	if !strings.Contains(string(build), "__TENJIN_ANDROID_SDK_KEY__") || strings.Contains(string(build), "__TENJIN_ANDROID_SDK_KEY_AMAZON__") {
		t.Errorf("build file:\n%s", build)
	}
	if code, stdout, _ := h.run("doctor", dir, "--build", "android:amazon"); code != 0 || strings.Contains(stdout, "still the placeholder") {
		t.Errorf("exit = %d:\n%s", code, stdout)
	}
}

// A placeholder in one build does not make doctor's agent overwrite the
// working key of another.
func TestDoctorsAgentKeepsTheOtherStoresKey(t *testing.T) {
	base := testproject.AndroidMultiStore()
	build := strings.Replace(base[flavorBuild], testproject.AmazonKey, "__TENJIN_ANDROID_SDK_KEY_AMAZON__", 1)
	const manifest = "app/src/main/AndroidManifest.xml"
	withoutInternet := strings.Replace(base[manifest], `    <uses-permission android:name="android.permission.INTERNET" />`+"\n", "", 1)
	h, dir := multiStore(t, base.With(testproject.Files{flavorBuild: build, manifest: withoutInternet}))
	agent := h.installAgent()
	h.run("doctor", dir, "--offline", "--agent", "claude")
	prompt := agent.read("prompt")
	if !strings.Contains(prompt, "variants play, bundle ID com.example.sample, Google Play: already in the project: leave it as it is") {
		t.Errorf("the agent is not told to keep the Play key:\n%s", prompt)
	}
}

// A module whose only flavor has an application ID of its own still has one
// build, read as a project without flavors is: the agent's placeholder is
// filled and a key for android counts for it.
func TestSetupFillsTheKeyOfAModulesOnlyFlavor(t *testing.T) {
	base := testproject.AndroidNative()
	const build = "app/build.gradle.kts"
	const activity = "app/src/main/java/com/example/sample/MainActivity.kt"
	gradle := strings.Replace(base[build], "    buildTypes {", `    flavorDimensions += "tier"
    productFlavors {
        create("prod") {
            dimension = "tier"
            applicationId = "com.example.sample.prod"
        }
    }
    buildTypes {`, 1)
	dir := testproject.Write(t, base.With(testproject.Files{
		build:    gradle,
		activity: strings.Replace(base[activity], testproject.Key, "__TENJIN_ANDROID_SDK_KEY__", 1),
	}))
	h := newHarness(t)
	h.signIn()
	h.fake.addApp("Sample", "com.example.sample.prod", "android")

	code, stdout, stderr := h.run("setup", dir, "--yes", "--agent", "none", "--features", "none", "--sdk-key", "android="+testproject.Key)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q\n%s", code, stderr, stdout)
	}
	if strings.Contains(stdout, "android:prod") || strings.Contains(stdout, "Android, prod") {
		t.Errorf("the only build reads as one of several:\n%s", stdout)
	}
	if raw, _ := os.ReadFile(filepath.Join(dir, activity)); !strings.Contains(string(raw), testproject.Key) {
		t.Errorf("the placeholder was not filled:\n%s", raw)
	}
	// What is shown is the bundle ID the build ships with.
	if _, stdout, _ := h.run("doctor", dir, "--offline"); !strings.Contains(stdout, "Android · com.example.sample.prod") {
		t.Errorf("doctor:\n%s", stdout)
	}
	if _, stdout, _ := h.run("project", "detect", dir); !strings.Contains(stdout, "com.example.sample.prod") {
		t.Errorf("project detect:\n%s", stdout)
	}
}
