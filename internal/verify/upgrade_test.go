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

// A range with an upper bound, or several requirements, is read whole: one
// that stops below the newest release needs an edit.
func TestUpgradeReadsBoundedRanges(t *testing.T) {
	flutter := testproject.Flutter()
	pub := func(entry string) testproject.Files {
		return flutter.With(testproject.Files{"pubspec.yaml": strings.Replace(flutter["pubspec.yaml"], "  tenjin_plugin: ^1.5.0\n", entry, 1)})
	}
	live := map[string]string{"tenjin_plugin": "1.6.0"}
	for _, c := range []struct {
		entry, declared string
		edit            bool
	}{
		{"  tenjin_plugin: \">=1.4.0 <1.6.0\"\n", ">=1.4.0 <1.6.0", true},
		{"  tenjin_plugin: '>=1.4.0 <2.0.0' # bounded\n", ">=1.4.0 <2.0.0", false},
		{"  tenjin_plugin: \">=1.4.0\"\n", ">=1.4.0", false},
		{"  tenjin_plugin:\n    version: \">=1.4.0 <1.6.0\"\n", ">=1.4.0 <1.6.0", true},
		{"  tenjin_plugin: any\n", "any", false},
	} {
		t.Run(c.entry, func(t *testing.T) {
			u := only(t, plan(t, pub(c.entry), live))
			if u.Declared != c.declared || u.Edit != c.edit || u.Current != "1.5.0" {
				t.Errorf("upgrade = %+v", u)
			}
		})
	}

	base := testproject.IOSNative()
	locked := base.With(testproject.Files{"Podfile.lock": strings.ReplaceAll(base["Podfile.lock"], "1.20.0", "1.19.1")})
	for _, c := range []struct {
		pod, declared string
		edit          bool
	}{
		{"pod 'TenjinSDK', '>= 1.12', '< 1.20'", ">= 1.12, < 1.20", true},
		{"pod 'TenjinSDK', '>= 1.12', '< 2.0'", ">= 1.12, < 2.0", false},
		{"pod 'TenjinSDK', '~> 1.19', '!= 1.20.0'", "~> 1.19, != 1.20.0", true},
	} {
		t.Run(c.pod, func(t *testing.T) {
			u := only(t, plan(t, locked.With(testproject.Files{"Podfile": strings.Replace(base["Podfile"], "pod 'TenjinSDK'", c.pod, 1)}), nil))
			if u.Declared != c.declared || u.Edit != c.edit {
				t.Errorf("upgrade = %+v", u)
			}
		})
	}

	rn := func(declared string) testproject.Files {
		return testproject.Files{
			"package.json": `{"dependencies": {"react-native": "0.79.2", "react-native-tenjin": "` + declared + `"}}`,
			"node_modules/react-native-tenjin/package.json": `{"version": "1.7.0"}`,
			"App.tsx": "import Tenjin from 'react-native-tenjin';\nTenjin.initialize('" + testproject.Key + "');\nTenjin.connect();\n",
		}
	}
	for declared, edit := range map[string]bool{">=1.7.0 <1.8.0": true, "1.7.x": true, "1.x": false, "^1.0.0 || ^2.0.0": false, "1.6.0 - 1.7.9": true} {
		if u := only(t, plan(t, rn(declared), map[string]string{"react-native-tenjin": "1.8.0"})); u.Edit != edit || u.Declared != declared {
			t.Errorf("%s: %+v", declared, u)
		}
	}
}

// pbxSPM is the iOS sample with Tenjin added as a Swift package under this
// requirement, in the form Xcode writes it.
func pbxSPM(requirement string) testproject.Files {
	base := testproject.IOSNative().Without("Podfile", "Podfile.lock")
	const pbx = "Sample.xcodeproj/project.pbxproj"
	ref := "\t\tAAAA /* XCRemoteSwiftPackageReference \"tenjin-ios-spm\" */ = {\n\t\t\tisa = XCRemoteSwiftPackageReference;\n\t\t\trepositoryURL = \"https://github.com/tenjin/tenjin-ios-spm\";\n\t\t\trequirement = {\n" + requirement + "\t\t\t};\n\t\t};\n"
	return base.With(testproject.Files{
		pbx: strings.Replace(base[pbx], "\tobjects = {\n", "\tobjects = {\n"+ref, 1),
		"Sample.xcodeproj/project.xcworkspace/xcshareddata/swiftpm/Package.resolved": `{"pins": [{"identity": "tenjin-ios-spm", "location": "https://github.com/tenjin/tenjin-ios-spm", "state": {"revision": "0123abcd", "version": "1.19.1"}}], "version": 2}`,
	})
}

// A Swift package's requirement is read from the Xcode project or the
// Package.swift; one that follows a branch or a commit is not planned.
func TestUpgradeWithSwiftPackageManager(t *testing.T) {
	for _, c := range []struct {
		requirement, declared string
		edit                  bool
	}{
		{"\t\t\t\tkind = upToNextMajorVersion;\n\t\t\t\tminimumVersion = 1.19.1;\n", "Up to Next Major Version from 1.19.1", false},
		{"\t\t\t\tkind = upToNextMinorVersion;\n\t\t\t\tminimumVersion = 1.19.0;\n", "Up to Next Minor Version from 1.19.0", true},
		{"\t\t\t\tkind = exactVersion;\n\t\t\t\tversion = 1.19.1;\n", "Exact Version 1.19.1", true},
		{"\t\t\t\tkind = versionRange;\n\t\t\t\tmaximumVersion = 1.20.0;\n\t\t\t\tminimumVersion = 1.18.0;\n", "Range 1.18.0..<1.20.0", true},
	} {
		t.Run(c.declared, func(t *testing.T) {
			u := only(t, plan(t, pbxSPM(c.requirement), nil))
			if u.Declared != c.declared || u.Edit != c.edit || u.Current != "1.19.1" || u.Target != "1.20.0" {
				t.Errorf("upgrade = %+v", u)
			}
		})
	}
	for _, requirement := range []string{"\t\t\t\tbranch = main;\n\t\t\t\tkind = branch;\n", "\t\t\t\tkind = revision;\n\t\t\t\trevision = 0123abcd;\n"} {
		p := plan(t, pbxSPM(requirement), nil)
		if len(p.Upgrades) != 0 || len(p.Unchecked) != 1 || !strings.Contains(p.Unchecked[0].Reason, "not a release") {
			t.Errorf("%q: plan = %+v", requirement, p)
		}
	}

	swift := func(dependency string) testproject.Files {
		return testproject.Files{
			"Package.swift":    "// swift-tools-version:5.9\nimport PackageDescription\nlet package = Package(name: \"AdsKit\", products: [.library(name: \"AdsKit\", targets: [\"AdsKit\"])], dependencies: [\n    .package(url: \"https://github.com/example/other\", exact: \"5.2.0\"),\n    " + dependency + "\n])\n",
			"Package.resolved": `{"pins": [{"identity": "tenjin-ios-spm", "state": {"version": "1.19.1"}}]}`,
		}
	}
	for dependency, edit := range map[string]bool{
		`.package(url: "https://github.com/tenjin/tenjin-ios-spm", from: "1.19.1")`:                      false,
		`.package(url: "https://github.com/tenjin/tenjin-ios-spm", .upToNextMinor(from: "1.19.0"))`:      true,
		`.package(url: "https://github.com/tenjin/tenjin-ios-spm", exact: "1.19.1")`:                     true,
		`.package(url: "https://github.com/tenjin/tenjin-ios-spm", "1.18.0"..<"2.0.0")`:                  false,
		`.package(name: "TenjinSDK", url: "https://github.com/tenjin/tenjin-ios-spm", .exact("1.19.1"))`: true,
	} {
		if u := only(t, plan(t, swift(dependency), nil)); u.Edit != edit {
			t.Errorf("%s: %+v", dependency, u)
		}
	}
	if p := plan(t, swift(`.package(url: "https://github.com/tenjin/tenjin-ios-spm", branch: "main")`), nil); len(p.Unchecked) != 1 || !strings.Contains(p.Unchecked[0].Reason, "the branch main") {
		t.Errorf("branch: %+v", p)
	}
}

// Forms of the SDK that no package manager upgrades are named, with how to
// upgrade them by hand.
func TestUpgradeOfFormsThePlanCannotChange(t *testing.T) {
	android := testproject.AndroidNative()
	const build = "app/build.gradle.kts"
	unity := testproject.Unity()
	flutter := testproject.Flutter()
	for _, c := range []struct {
		name  string
		files testproject.Files
		want  string
	}{
		{"aar", android.With(testproject.Files{build: strings.Replace(android[build], `implementation("com.tenjin:android-sdk:2.0.0")`, `implementation(files("libs/tenjin.aar"))`, 1), "app/libs/tenjin.aar": "aar"}), "replace app/libs/tenjin.aar"},
		{"unitypackage", unity.With(testproject.Files{"Packages/manifest.json": `{"dependencies": {}}`, "Assets/Tenjin/Scripts/Tenjin.cs": "// the SDK\n"}), "import it over Assets/Tenjin"},
		{"community", flutter.With(testproject.Files{"pubspec.yaml": strings.Replace(flutter["pubspec.yaml"], "tenjin_plugin: ^1.5.0", "tenjin_sdk: ^0.2.0", 1)}), "Replace it with tenjin_plugin 1.5.0"},
		{"unreal", testproject.Files{"Sample.uproject": "{}", "Source/Sample/Sample.cpp": "// code\n", "Plugins/TenjinSDK/TenjinSDK.uplugin": `{"VersionName": "1.1.0"}`}, "replace the plugin's folder, Plugins/TenjinSDK"},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := plan(t, c.files, nil)
			if len(p.Upgrades)+len(p.Current) != 0 || len(p.Unchecked) != 1 || !strings.Contains(p.Unchecked[0].Reason, c.want) {
				t.Errorf("plan = %+v", p)
			}
		})
	}
}

// A dependency on a folder or a Git repository has no version to move.
func TestUpgradeOfLocalAndGitDependencies(t *testing.T) {
	rn := func(declared string) testproject.Files {
		return testproject.Files{
			"package.json": `{"dependencies": {"react-native": "0.79.2", "react-native-tenjin": "` + declared + `"}}`,
			"App.tsx":      "import Tenjin from 'react-native-tenjin';\nTenjin.initialize('" + testproject.Key + "');\nTenjin.connect();\n",
		}
	}
	for _, declared := range []string{"file:../tenjin", "link:../tenjin", "workspace:^1.7.0", "git+https://github.com/tenjin/tenjin-react-native-sdk.git#v1.7.0", "github:tenjin/tenjin-react-native-sdk", "tenjin/tenjin-react-native-sdk"} {
		if p := plan(t, rn(declared), nil); len(p.Upgrades) != 0 || len(p.Unchecked) != 1 || !strings.Contains(p.Unchecked[0].Reason, "not from a release on npm") {
			t.Errorf("%s: %+v", declared, p)
		}
	}
	flutter := testproject.Flutter()
	for _, entry := range []string{"  tenjin_plugin:\n    path: ../flutter-sdk\n", "  tenjin_plugin:\n    git:\n      url: https://github.com/tenjin/flutter-sdk\n"} {
		files := flutter.With(testproject.Files{"pubspec.yaml": strings.Replace(flutter["pubspec.yaml"], "  tenjin_plugin: ^1.5.0\n", entry, 1)})
		if p := plan(t, files, nil); len(p.Upgrades) != 0 || len(p.Unchecked) != 1 || !strings.Contains(p.Unchecked[0].Reason, "not from a release on pub.dev") {
			t.Errorf("%q: %+v", entry, p)
		}
	}
	ios := testproject.IOSNative()
	if p := plan(t, ios.With(testproject.Files{"Podfile": strings.Replace(ios["Podfile"], "pod 'TenjinSDK'", "pod 'TenjinSDK', :git => 'https://github.com/tenjin/tenjin-ios-sdk.git', :tag => '1.19.1'", 1)}), nil); len(p.Unchecked) != 1 || !strings.Contains(p.Unchecked[0].Reason, "Git repository") {
		t.Errorf("pod from git: %+v", p)
	}
}

// Without a lock file, the declared version stands for the one in use.
func TestUpgradeWithoutALockFile(t *testing.T) {
	ios := testproject.IOSNative()
	u := only(t, plan(t, ios.Without("Podfile.lock").With(testproject.Files{"Podfile": strings.Replace(ios["Podfile"], "pod 'TenjinSDK'", "pod 'TenjinSDK', '1.19.1'", 1)}), nil))
	if u.Current != "1.19.1" || !u.Edit {
		t.Errorf("pods: %+v", u)
	}
	u = only(t, plan(t, testproject.Flutter().Without("pubspec.lock"), map[string]string{"tenjin_plugin": "1.6.0"}))
	if u.Current != "1.5.0" || u.Edit {
		t.Errorf("pub: %+v", u)
	}
	// With neither a version nor a lock, it says what would tell.
	p := plan(t, ios.Without("Podfile.lock"), nil)
	if len(p.Unchecked) != 1 || !strings.Contains(p.Unchecked[0].Reason, "Podfile.lock (pod install)") {
		t.Errorf("plan = %+v", p)
	}
}

// A prerelease comes before its release; one ahead of the newest release
// the CLI knows is not moved back.
func TestUpgradeFromAPrereleaseAndAhead(t *testing.T) {
	files := testproject.Files{
		"package.json": `{"dependencies": {"react-native": "0.79.2", "react-native-tenjin": "1.7.0-beta.1"}}`,
		"App.tsx":      "import Tenjin from 'react-native-tenjin';\nTenjin.initialize('" + testproject.Key + "');\nTenjin.connect();\n",
	}
	u := only(t, plan(t, files, nil))
	if u.Current != "1.7.0-beta.1" || u.Target != "1.7.0" || u.Major || !u.Edit {
		t.Errorf("prerelease: %+v", u)
	}
	android := testproject.AndroidNative()
	const build = "app/build.gradle.kts"
	p := plan(t, android.With(testproject.Files{build: strings.Replace(android[build], "android-sdk:2.0.0", "android-sdk:2.1.0", 1)}), nil)
	if len(p.Current) != 1 || !p.Current[0].Ahead() || p.Current[0].Current != "2.1.0" {
		t.Errorf("ahead: %+v", p)
	}
	if p := plan(t, android, nil); p.Current[0].Ahead() {
		t.Errorf("at the newest: %+v", p)
	}
}

// npm: the version in use is the copy Node loads, hoisted to a workspace's
// root, or the one the lock file names; Yarn 2 and later update with up.
func TestUpgradeInAnNPMWorkspace(t *testing.T) {
	app := testproject.Files{
		"package.json": `{"dependencies": {"react-native": "0.79.2", "react-native-tenjin": "^1.6.0"}}`,
		"App.tsx":      "import Tenjin from 'react-native-tenjin';\nTenjin.initialize('" + testproject.Key + "');\nTenjin.connect();\n",
	}
	live := map[string]string{"react-native-tenjin": "1.8.0"}
	workspace := func(extra testproject.Files) string {
		files := testproject.Files{"package.json": `{"private": true, "workspaces": ["apps/*"]}`}
		for name, content := range app {
			files["apps/mobile/"+name] = content
		}
		return testproject.Write(t, files.With(extra))
	}
	planIn := func(dir string) verify.Upgrade {
		t.Helper()
		p, err := project.Detect(dir)
		if err != nil {
			t.Fatal(err)
		}
		return only(t, verify.Upgrades(p, live))
	}
	root := workspace(testproject.Files{"node_modules/react-native-tenjin/package.json": `{"version": "1.7.0"}`, "package-lock.json": "{}"})
	if u := planIn(root + "/apps/mobile"); u.Current != "1.7.0" || u.Then != "Run npm update react-native-tenjin, then pod install in ios/." {
		t.Errorf("hoisted: %+v", u)
	}
	root = workspace(testproject.Files{"package-lock.json": `{"packages": {"node_modules/react-native-tenjin": {"version": "1.7.2"}}}`})
	if u := planIn(root + "/apps/mobile"); u.Current != "1.7.2" {
		t.Errorf("package-lock: %+v", u)
	}
	root = workspace(testproject.Files{"yarn.lock": "\"react-native-tenjin@npm:^1.6.0\":\n  version: 1.7.1\n  resolution: \"react-native-tenjin@npm:1.7.1\"\n", ".yarnrc.yml": "nodeLinker: node-modules\n"})
	if u := planIn(root + "/apps/mobile"); u.Current != "1.7.1" || u.Then != "Run yarn up -R react-native-tenjin, then pod install in ios/." {
		t.Errorf("yarn berry: %+v", u)
	}
	root = workspace(testproject.Files{"yarn.lock": "react-native-tenjin@^1.6.0:\n  version \"1.7.3\"\n"})
	if u := planIn(root + "/apps/mobile"); u.Current != "1.7.3" || u.Then != "Run yarn upgrade react-native-tenjin, then pod install in ios/." {
		t.Errorf("yarn 1: %+v", u)
	}
	dir := testproject.Write(t, app.With(testproject.Files{"package.json": `{"packageManager": "yarn@4.5.0", "dependencies": {"react-native": "0.79.2", "react-native-tenjin": "~1.6.0"}}`}))
	if u := planIn(dir); u.Then != "Run yarn install, then pod install in ios/." {
		t.Errorf("packageManager: %+v", u)
	}
}

// A React Native library's range is planned as written: ^1.7.0 already
// allows 1.8.0. Doctor still reads it as before.
func TestUpgradeOfAReactNativeLibrary(t *testing.T) {
	files := testproject.Files{"package.json": `{"name": "rn-ads-kit", "peerDependencies": {"react-native": "*"}, "dependencies": {"react-native-tenjin": "^1.7.0"}}`}
	u := only(t, plan(t, files, map[string]string{"react-native-tenjin": "1.8.0"}))
	if u.Declared != "^1.7.0" || u.Edit || u.Current != "1.7.0" {
		t.Errorf("upgrade = %+v", u)
	}
}
