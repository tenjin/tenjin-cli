package verify_test

import (
	"strings"
	"testing"

	"github.com/tenjin/tenjin-cli/internal/project"
	"github.com/tenjin/tenjin-cli/internal/testproject"
	"github.com/tenjin/tenjin-cli/internal/verify"
)

func plan(t *testing.T, files testproject.Files, live map[string]string) verify.UpgradePlan {
	t.Helper()
	p, err := project.Detect(testproject.Write(t, files))
	if err != nil {
		t.Fatal(err)
	}
	return verify.Upgrades(p, live)
}

func only(t *testing.T, plan verify.UpgradePlan) verify.Upgrade {
	t.Helper()
	if len(plan.Upgrades) != 1 {
		t.Fatalf("plan = %+v", plan)
	}
	return plan.Upgrades[0]
}

// A pinned Gradle version is changed in its file. Android 2 is a new major
// version whose changes can reach the app's code.
func TestUpgradeAcrossAMajorVersion(t *testing.T) {
	base := testproject.AndroidNative()
	const build = "app/build.gradle.kts"
	old := base.With(testproject.Files{build: strings.Replace(base[build], "android-sdk:2.0.0", "android-sdk:1.24.0", 1)})
	u := only(t, plan(t, old, nil))
	if u.Package != "com.tenjin:android-sdk" || u.Platform != project.Android || u.File != build || u.Current != "1.24.0" || u.Declared != "1.24.0" || u.Target != "2.0.0" {
		t.Errorf("upgrade = %+v", u)
	}
	if !u.Major || !u.Code || !u.Edit || !u.Agent() || !strings.Contains(u.Changes, "TenjinConsts") || !strings.Contains(u.Then, "Gradle") ||
		u.Releases != "https://github.com/tenjin/tenjin-android-sdk/releases" {
		t.Errorf("upgrade = %+v", u)
	}

	// A newer release of the major version the CLI knows is taken; one of a
	// major version it does not know is not.
	if u := only(t, plan(t, old, map[string]string{"com.tenjin:android-sdk": "2.0.1"})); u.Target != "2.0.1" {
		t.Errorf("target = %q", u.Target)
	}
	if u := only(t, plan(t, old, map[string]string{"com.tenjin:android-sdk": "3.0.0"})); u.Target != "2.0.0" {
		t.Errorf("target = %q", u.Target)
	}

	// At the newest release there is nothing to do.
	current := plan(t, base, nil)
	if len(current.Upgrades) != 0 || len(current.Current) != 1 || current.Current[0].Current != "2.0.0" {
		t.Errorf("plan = %+v", current)
	}
}

// CocoaPods installs from the lock file: a Podfile that allows the new
// release needs only pod update.
func TestUpgradeWithCocoaPods(t *testing.T) {
	base := testproject.IOSNative()
	locked := base.With(testproject.Files{"Podfile.lock": strings.ReplaceAll(base["Podfile.lock"], "1.20.0", "1.19.1")})
	for _, c := range []struct {
		pod  string
		edit bool
	}{
		{"pod 'TenjinSDK'", false},
		{"pod 'TenjinSDK', '~> 1.19'", false},
		{"pod 'TenjinSDK', '>= 1.0'", false},
		{"pod 'TenjinSDK', '~> 1.19.1'", true},
		{"pod 'TenjinSDK', '1.19.1'", true},
	} {
		t.Run(c.pod, func(t *testing.T) {
			files := locked.With(testproject.Files{"Podfile": strings.Replace(base["Podfile"], "pod 'TenjinSDK'", c.pod, 1)})
			u := only(t, plan(t, files, nil))
			if u.Current != "1.19.1" || u.Target != "1.20.0" || u.Major || u.Edit != c.edit || u.Agent() != c.edit || u.Then != "Run pod update TenjinSDK." {
				t.Errorf("upgrade = %+v", u)
			}
		})
	}
}

// npm and pub ranges: a caret range allows the next minor release, and the
// package manager updates within it.
func TestUpgradeWithinARange(t *testing.T) {
	rn := func(declared string) testproject.Files {
		return testproject.Files{
			"package.json": `{"dependencies": {"react-native": "0.79.2", "react-native-tenjin": "` + declared + `"}}`,
			"node_modules/react-native-tenjin/package.json": `{"name": "react-native-tenjin", "version": "1.7.0"}`,
			"App.tsx": "import Tenjin from 'react-native-tenjin';\nTenjin.initialize('" + testproject.Key + "');\nTenjin.connect();\n",
		}
	}
	live := map[string]string{"react-native-tenjin": "1.8.0"}
	u := only(t, plan(t, rn("^1.7.0"), live))
	if u.Current != "1.7.0" || u.Declared != "^1.7.0" || u.Edit || u.Then != "Run npm update react-native-tenjin, then pod install in ios/." {
		t.Errorf("caret: %+v", u)
	}
	if u := only(t, plan(t, rn("~1.7.0"), live)); !u.Edit || u.Then != "Run npm install, then pod install in ios/." {
		t.Errorf("tilde: %+v", u)
	}
	files := rn("1.7.0").With(testproject.Files{"yarn.lock": "# yarn\n"})
	if u := only(t, plan(t, files, live)); !u.Edit || u.Then != "Run yarn install, then pod install in ios/." {
		t.Errorf("yarn: %+v", u)
	}
	// Installed at the newest release already: nothing to do, whatever the
	// range says.
	files = rn("^1.7.0").With(testproject.Files{"node_modules/react-native-tenjin/package.json": `{"version": "1.8.0"}`})
	if p := plan(t, files, live); len(p.Upgrades) != 0 || len(p.Current) != 1 {
		t.Errorf("installed: %+v", p)
	}

	flutter := testproject.Flutter()
	u = only(t, plan(t, flutter, map[string]string{"tenjin_plugin": "1.6.0"}))
	if u.Current != "1.5.0" || u.Declared != "^1.5.0" || u.Edit || u.Then != "Run flutter pub upgrade tenjin_plugin." {
		t.Errorf("flutter: %+v", u)
	}
	pinned := flutter.With(testproject.Files{"pubspec.yaml": strings.Replace(flutter["pubspec.yaml"], "tenjin_plugin: ^1.5.0", "tenjin_plugin: 1.5.0", 1)})
	if u := only(t, plan(t, pinned, map[string]string{"tenjin_plugin": "1.6.0"})); !u.Edit || u.Declared != "1.5.0" {
		t.Errorf("flutter pinned: %+v", u)
	}
}

// Ionic's version 2 changed nothing in the plugin's API: the declaration
// changes, the app's code does not.
func TestUpgradeAMajorVersionWithoutCodeChanges(t *testing.T) {
	files := testproject.Files{
		"package.json":          `{"dependencies": {"@capacitor/core": "6.0.0", "ionic-capacitor-tenjin": "1.2.0"}}`,
		"capacitor.config.json": `{"appId": "com.example.sample", "webDir": "www"}`,
		"src/main.ts":           "import { Tenjin } from 'ionic-capacitor-tenjin';\nawait Tenjin.initialize({ sdkKey: '" + testproject.Key + "' });\nawait Tenjin.connect();\n",
	}
	u := only(t, plan(t, files, nil))
	if !u.Major || u.Code || !u.Edit || !strings.Contains(u.Changes, "Capacitor 8") || u.Then != "Run npm install, then npx cap sync." {
		t.Errorf("upgrade = %+v", u)
	}
}

func TestUpgradeInUnity(t *testing.T) {
	u := only(t, plan(t, testproject.Unity(), map[string]string{"com.tenjin.sdk": "1.23.1"}))
	if u.Package != "com.tenjin.sdk" || u.File != "Packages/manifest.json" || u.Current != "1.22.1" || u.Target != "1.23.1" || !u.Edit || !strings.Contains(u.Then, "Unity editor") {
		t.Errorf("upgrade = %+v", u)
	}
}

// Only what the project declares itself can be upgraded in it.
func TestUpgradeOfAnSDKAnotherPackageBrings(t *testing.T) {
	files := testproject.Flutter().With(testproject.Files{
		"pubspec.yaml":                    strings.Replace(testproject.Flutter()["pubspec.yaml"], "  tenjin_plugin: ^1.5.0\n", "  my_tenjin:\n    path: packages/my_tenjin\n", 1),
		"packages/my_tenjin/pubspec.yaml": "name: my_tenjin\ndependencies:\n  tenjin_plugin: ^1.5.0\n",
	})
	p := plan(t, files, nil)
	if len(p.Upgrades) != 0 || len(p.Unchecked) != 1 || !strings.Contains(p.Unchecked[0].Reason, "comes with my_tenjin") {
		t.Errorf("plan = %+v", p)
	}
}

// Doctor's warning about an older SDK says what a new major version changes.
func TestTheVersionWarningNamesAMajorVersionsChanges(t *testing.T) {
	base := testproject.AndroidNative()
	const build = "app/build.gradle.kts"
	r := run(t, base.With(testproject.Files{build: strings.Replace(base[build], "android-sdk:2.0.0", "android-sdk:1.24.0", 1)}), verify.Options{})
	f := expect(t, r, "sdk.version", project.Android, verify.Warning)
	if !strings.HasPrefix(f.Detail, "2.0.0 is a new major version.") || !strings.Contains(f.Detail, "TenjinConsts") || !strings.Contains(f.Fix, "change the app's code") {
		t.Errorf("finding = %+v", f)
	}
}
