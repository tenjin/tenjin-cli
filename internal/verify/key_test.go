package verify_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tenjin/tenjin-cli/internal/collector"
	"github.com/tenjin/tenjin-cli/internal/project"
	"github.com/tenjin/tenjin-cli/internal/testproject"
	"github.com/tenjin/tenjin-cli/internal/verify"
)

// The fix for a placeholder names one that exists: each build's own, which
// is what setup and set-keys fill.
func TestThePlaceholderFixNamesTheBuildsPlaceholders(t *testing.T) {
	const activity = "app/src/main/java/com/example/sample/MainActivity.kt"
	android := testproject.AndroidNative()
	multi := testproject.AndroidMultiStore()
	flutter := testproject.Flutter()
	cases := []struct {
		name     string
		files    testproject.Files
		platform project.Platform
		want     string
	}{
		{"one build", android.With(testproject.Files{activity: strings.Replace(android[activity], testproject.Key, "YOUR_SDK_KEY", 1)}), project.Android,
			`Replace "YOUR_SDK_KEY" with __TENJIN_ANDROID_SDK_KEY__. Then`},
		{"flavors", multi.With(testproject.Files{activity: strings.Replace(multi[activity], "BuildConfig.TENJIN_SDK_KEY", `"YOUR_SDK_KEY"`, 1)}), project.Android,
			"with the placeholder of the build it is for (__TENJIN_ANDROID_SDK_KEY__ for the main build, Google Play, __TENJIN_ANDROID_SDK_KEY_AMAZON__ for the amazon build, Amazon Appstore)"},
		{"both platforms in one codebase", flutter.With(testproject.Files{"lib/main.dart": strings.Replace(flutter["lib/main.dart"], testproject.Key, "YOUR_SDK_KEY", 1)}), "",
			"with the placeholder of the build it is for (__TENJIN_ANDROID_SDK_KEY__ for Android, __TENJIN_IOS_SDK_KEY__ for iOS)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := expect(t, run(t, tc.files, verify.Options{}), "sdk.key.placeholder", tc.platform, verify.Error)
			if !strings.Contains(f.Fix, tc.want) || strings.Contains(f.Fix, "__TENJIN__") {
				t.Errorf("fix = %q", f.Fix)
			}
		})
	}
}

// A placeholder that holds most of a key is shown masked, like a key.
func TestAPlaceholderHoldingAKeyIsMasked(t *testing.T) {
	const activity = "app/src/main/java/com/example/sample/MainActivity.kt"
	base := testproject.AndroidNative()
	r := run(t, base.With(testproject.Files{activity: strings.Replace(base[activity], testproject.Key, "your_key_"+testproject.Key, 1)}), verify.Options{})
	f := expect(t, r, "sdk.key.placeholder", project.Android, verify.Error)
	if strings.Contains(f.Title+f.Detail+f.Fix, testproject.Key) || !strings.Contains(f.Detail, `"…MNBV"`) {
		t.Errorf("finding = %+v", f)
	}
}

// A key with a stray space is a key, not a placeholder: it is reported
// masked, and not sent to Tenjin as it stands.
func TestAKeyWithSpacesIsItsOwnFinding(t *testing.T) {
	const activity = "app/src/main/java/com/example/sample/MainActivity.kt"
	base := testproject.AndroidNative()
	keys := &fakeKeys{}
	r := run(t, base.With(testproject.Files{activity: strings.Replace(base[activity], testproject.Key, " "+testproject.Key, 1)}), verify.Options{Keys: keys})
	f := expect(t, r, "sdk.key.whitespace", project.Android, verify.Error)
	if f.File != activity || f.Line != 9 || !strings.Contains(f.Title, "…MNBV") || strings.Contains(f.Title+f.Detail+f.Fix, testproject.Key) {
		t.Errorf("finding = %+v", f)
	}
	if has(r, "sdk.key.placeholder") || has(r, "key.unchecked") || len(keys.asked) != 0 {
		t.Errorf("findings: %s; asked: %q", ids(r), keys.asked)
	}
}

// A placeholder in a symbolic link is one the CLI does not write through:
// it is reported, with why, and what to do instead.
func TestAPlaceholderInASymlinkIsReported(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need privileges on Windows")
	}
	const activity = "app/src/main/java/com/example/sample/MainActivity.kt"
	base := testproject.AndroidNative()
	dir := testproject.Write(t, base.Without(activity))
	outside := filepath.Join(t.TempDir(), "MainActivity.kt")
	if err := os.WriteFile(outside, []byte(strings.Replace(base[activity], testproject.Key, "__TENJIN_ANDROID_SDK_KEY__", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, filepath.FromSlash(activity))
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	p, err := project.Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := verify.Run(context.Background(), p, verify.Options{})
	f := expect(t, r, "sdk.key.unfilled", project.Android, verify.Error)
	if f.File != activity || !strings.Contains(f.Detail, "symbolic link") || !strings.Contains(f.Fix, activity+":9 yourself") {
		t.Errorf("finding = %+v", f)
	}
}

// findBuild returns a build's finding with this ID.
func findBuild(r *verify.Report, id, build string) *verify.Finding {
	for i := range r.Findings {
		if r.Findings[i].ID == id && r.Findings[i].Build == build {
			return &r.Findings[i]
		}
	}
	return nil
}

// Each build is checked against the keys that go into it: its flavor's
// block, then what every build shares. A key elsewhere cannot pass for a
// build, and a key in a comment is no key at all.
func TestEachBuildIsCheckedAgainstItsOwnKey(t *testing.T) {
	base := testproject.AndroidMultiStore()
	const build = "app/build.gradle.kts"
	const wrongKey = "WRONGKEY34567890ABCDEFGHIJKLWRNG"
	valid := func() *fakeKeys {
		return &fakeKeys{answers: map[string]collector.KeyStatus{
			testproject.PlayKey + "|com.example.sample|android":  collector.KeyValid,
			testproject.AmazonKey + "|com.example.sample|amazon": collector.KeyValid,
		}}
	}
	swap := strings.NewReplacer(testproject.PlayKey, testproject.AmazonKey, testproject.AmazonKey, testproject.PlayKey)
	playLine := `            buildConfigField("String", "TENJIN_SDK_KEY", "\"` + testproject.PlayKey + `\"")` + "\n"
	cases := []struct {
		name         string
		files        testproject.Files
		main, amazon string // the finding expected for each build
	}{
		{"own keys", base, "key.valid", "key.valid"},
		{"swapped", base.With(testproject.Files{build: swap.Replace(base[build])}), "key.rejected", "key.rejected"},
		{"the right key only in a comment", base.With(testproject.Files{
			build: strings.Replace(base[build], testproject.AmazonKey, wrongKey, 1) + "// old Tenjin amazon key \"" + testproject.AmazonKey + "\"\n",
		}), "key.valid", "key.rejected"},
		{"a shared key and one build's own", base.With(testproject.Files{
			build: strings.Replace(strings.Replace(base[build], playLine, "", 1),
				"minSdk = 24", "minSdk = 24\n"+playLine, 1),
		}), "key.valid", "key.valid"},
		{"keys that cannot be tied to a build", base.With(testproject.Files{
			build:               strings.NewReplacer(testproject.PlayKey, "${tenjinPlayKey}", testproject.AmazonKey, "${tenjinAmazonKey}").Replace(base[build]),
			"gradle.properties": "# Tenjin keys\ntenjinPlayKey=" + testproject.PlayKey + " # tenjin\ntenjinAmazonKey=" + testproject.AmazonKey + " # tenjin\n",
		}), "key.unverified", "key.unverified"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := run(t, tc.files, verify.Options{Keys: valid()})
			for build, want := range map[string]string{"android": tc.main, "android:amazon": tc.amazon} {
				f := findBuild(r, want, build)
				if f == nil {
					t.Errorf("%s: no %s; got %s", build, want, ids(r))
					continue
				}
				if want == "key.unverified" && (f.Severity != verify.Info || !strings.Contains(f.Detail, "gradle.properties")) {
					t.Errorf("%s: %+v", build, f)
				}
				if strings.Contains(f.Title+f.Detail+f.Fix, testproject.PlayKey) || strings.Contains(f.Title+f.Detail+f.Fix, testproject.AmazonKey) {
					t.Errorf("%s: a whole key is printed: %+v", build, f)
				}
			}
		})
	}
}

// When the project's own key cannot be read, a given key that Tenjin
// accepts says nothing about what the app sends, so it does not pass; where
// the project holds the build's placeholder, the given key is what the CLI
// writes there, and it does.
func TestAGivenKeyPassesOnlyWhereItIsWhatTheAppSends(t *testing.T) {
	base := testproject.AndroidNative()
	const activity = "app/src/main/java/com/example/sample/MainActivity.kt"
	const build = "app/build.gradle.kts"
	const given = "GIVENKEY34567890ABCDEFGHIJKLGIVN"
	fromBuildConfig := base.With(testproject.Files{activity: strings.Replace(base[activity], `"`+testproject.Key+`"`, "BuildConfig.TENJIN_SDK_KEY", 1)})
	opts := func() verify.Options {
		return verify.Options{
			Keys:  &fakeKeys{answers: map[string]collector.KeyStatus{given + "|com.example.sample|android": collector.KeyValid}},
			Given: verify.GivenKeys{All: verify.GivenKey{Value: given, Source: "the key you pasted"}, PreferSource: true},
		}
	}

	f := expect(t, run(t, fromBuildConfig, opts()), "key.unverified", project.Android, verify.Info)
	if !strings.Contains(f.Title, "Tenjin accepts the SDK key …GIVN (from the key you pasted) for com.example.sample, but whether the app sends it could not be read") ||
		!strings.Contains(f.Detail, "not written as a string literal") || strings.Contains(f.Title+f.Detail+f.Fix, given) {
		t.Errorf("finding = %+v", f)
	}

	withPlaceholder := fromBuildConfig.With(testproject.Files{build: strings.Replace(fromBuildConfig[build], "minSdk = 24",
		"minSdk = 24\n        buildConfigField(\"String\", \"TENJIN_SDK_KEY\", \"\\\"__TENJIN_ANDROID_SDK_KEY__\\\"\")", 1)})
	expect(t, run(t, withPlaceholder, opts()), "key.valid", project.Android, verify.Pass)

	// With no call to the SDK yet, there is no other key the app could send.
	noCall := base.With(testproject.Files{activity: "package com.example.sample\n\nclass MainActivity\n"})
	expect(t, run(t, noCall, opts()), "key.valid", project.Android, verify.Pass)
}
