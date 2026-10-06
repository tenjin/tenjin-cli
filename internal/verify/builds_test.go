package verify_test

import (
	"strings"
	"testing"

	"github.com/tenjin/tenjin-cli/internal/track"
	"github.com/tenjin/tenjin-cli/internal/devicelog"
	"github.com/tenjin/tenjin-cli/internal/project"
	"github.com/tenjin/tenjin-cli/internal/testproject"
	"github.com/tenjin/tenjin-cli/internal/verify"
)

// Only keys on lines that name Tenjin are taken from build configuration,
// so another service's secret is never sent to Tenjin.
func TestOnlyTenjinKeysAreSentToTenjin(t *testing.T) {
	base := testproject.AndroidMultiStore()
	const build = "app/build.gradle.kts"
	const otherSecret = "OTHERVENDORSECRET1234567890ABCDEF"
	keys := &fakeKeys{answers: map[string]track.KeyStatus{
		testproject.PlayKey + "|com.example.sample|android":  track.KeyValid,
		testproject.AmazonKey + "|com.example.sample|amazon": track.KeyValid,
	}}
	run(t, base.With(testproject.Files{
		build: strings.Replace(base[build], "    buildFeatures {", `    buildFeatures {
        // An analytics vendor's key, nothing to do with this SDK:
        resValue("string", "vendor_key", "`+otherSecret+`")`, 1),
	}), verify.Options{Keys: keys})
	for _, asked := range keys.asked {
		if strings.HasPrefix(asked, otherSecret) {
			t.Fatalf("another service's secret was sent to Tenjin: %q", keys.asked)
		}
	}
	if len(keys.asked) == 0 {
		t.Fatal("no key was checked")
	}
}

func TestAPlaceholderForNoBuild(t *testing.T) {
	base := testproject.AndroidMultiStore()
	const build = "app/build.gradle.kts"
	r := run(t, base.With(testproject.Files{
		build: strings.Replace(base[build], testproject.AmazonKey, "__TENJIN_ANDROID_SDK_KEY_GALAXY__", 1),
	}), verify.Options{})
	f := expect(t, r, "sdk.key.unfilled", project.Android, verify.Error)
	if !strings.Contains(f.Title, "__TENJIN_ANDROID_SDK_KEY_GALAXY__ names no build of this project") {
		t.Errorf("title = %q", f.Title)
	}
	r = run(t, base.With(testproject.Files{
		build: strings.Replace(base[build], testproject.AmazonKey, "__TENJIN_ANDROID_SDK_KEY_AMAZON__", 1),
	}), verify.Options{})
	f = expect(t, r, "sdk.key.unfilled", project.Android, verify.Error)
	if f.Build != "android:amazon" || !strings.Contains(f.Title, "the amazon build, Amazon Appstore") || !strings.Contains(f.Fix, "--sdk-key android:amazon=<key>") {
		t.Errorf("finding = %+v", f)
	}
}

// An install from the Amazon build is filed under the Amazon app, and the
// lookup asks about that app; the flavor's bundle ID is a build that ships.
func TestAnAmazonInstallIsLookedUpOnTheAmazonApp(t *testing.T) {
	log, _ := devicelog.Parse(strings.NewReader(`10-02 15:07:34.120 D/HttpConnection(1): Tenjin request URL: https://track.tenjin.com/v0/event
10-02 15:07:34.121 D/HttpConnection(1): Tenjin::connect params: {bundle_id=com.example.sample, platform=android, source_app_store=amazon, analytics_installation_id=I1, advertising_id=A1, sdk_version=1.24.0}
10-02 15:07:34.300 D/HttpConnection(1): Tenjin::connect response: {"code":200,"success":true}
`))
	lookup := &fakeLookup{known: map[string]track.Attribution{"I1": {Known: true, CampaignName: "Organic"}}}
	r := run(t, testproject.AndroidMultiStore(), verify.Options{Device: &verify.DeviceInput{Log: log, Lookup: lookup, Platform: project.Android}})
	expect(t, r, "device.processed", project.Android, verify.Pass)
	if len(lookup.asked) == 0 || !strings.HasSuffix(lookup.asked[0], "|com.example.sample|amazon|I1") {
		t.Errorf("asked = %q", lookup.asked)
	}
	if f := find(r, "device.bundle_id", project.Android); f != nil {
		t.Errorf("a shipped build was reported as the wrong app: %+v", f)
	}
}

func TestStoreDeclarations(t *testing.T) {
	base := testproject.AndroidMultiStore()
	const amazonManifest = "app/src/amazon/AndroidManifest.xml"

	// A flavor named for Amazon that declares nothing goes to Google Play.
	r := run(t, base.Without(amazonManifest), verify.Options{})
	f := expect(t, r, "android.store", project.Android, verify.Warning)
	if !strings.Contains(f.Title, "The amazon build declares no app store") || !strings.Contains(f.Fix, `android:value="amazon"`) || !strings.Contains(f.Fix, amazonManifest) {
		t.Errorf("finding = %+v", f)
	}

	// A value Tenjin does not know.
	r = run(t, base.With(testproject.Files{amazonManifest: testproject.StoreManifest("amazn")}), verify.Options{})
	f = expect(t, r, "android.store", project.Android, verify.Warning)
	if !strings.Contains(f.Title, `"amazn"`) || f.File != amazonManifest {
		t.Errorf("finding = %+v", f)
	}

	// Declared correctly: nothing to say.
	r = run(t, base, verify.Options{})
	if f := find(r, "android.store", project.Android); f != nil {
		t.Errorf("finding = %+v", f)
	}
}

// A store set in code cannot be read, so the name of a flavor is no reason
// to warn; the finding says what tenjin cannot see.
func TestAStoreSetInCode(t *testing.T) {
	base := testproject.AndroidMultiStore()
	const activity = "app/src/main/java/com/example/sample/MainActivity.kt"
	r := run(t, base.Without("app/src/amazon/AndroidManifest.xml").With(testproject.Files{
		activity: strings.Replace(base[activity], "tenjin.connect()", "tenjin.setAppStore(TenjinSDK.AppStoreType.amazon)\n        tenjin.connect()", 1),
	}), verify.Options{})
	if f := find(r, "android.store", project.Android); f != nil {
		t.Errorf("warned from a flavor's name: %+v", f)
	}
	f := expect(t, r, "android.store.in_code", project.Android, verify.Info)
	if f.File != activity {
		t.Errorf("finding = %+v", f)
	}
}
