package verify_test

import (
	"strings"
	"testing"

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
			`Replace "YOUR_SDK_KEY" with __TENJIN_ANDROID_SDK_KEY__, then`},
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
