package project_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/tenjin/tenjin-cli/internal/project"
	"github.com/tenjin/tenjin-cli/internal/testproject"
)

func detect(t *testing.T, files testproject.Files) *project.Project {
	t.Helper()
	p, err := project.Detect(testproject.Write(t, files))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func bundleID(p *project.Project, platform project.Platform) string {
	if t := p.Target(platform); t != nil {
		return t.BundleID
	}
	return ""
}

func hasNote(p *project.Project, fragment string) bool {
	for _, n := range p.Notes {
		if strings.Contains(n, fragment) {
			return true
		}
	}
	return false
}

func TestAndroidNative(t *testing.T) {
	p := detect(t, testproject.AndroidNative())
	if p.Framework != project.Native || !p.Supported {
		t.Fatalf("framework = %q, supported = %v", p.Framework, p.Supported)
	}
	target := p.Target(project.Android)
	if target == nil || target.BundleID != "com.example.sample" {
		t.Fatalf("android target = %+v", target)
	}
	// The root build file names the plugin too; the module that sets an
	// application ID is the app.
	if target.Android.BuildFile != "app/build.gradle.kts" {
		t.Errorf("build file = %q", target.Android.BuildFile)
	}
	if target.Android.Manifest != "app/src/main/AndroidManifest.xml" {
		t.Errorf("manifest = %q", target.Android.Manifest)
	}
	if len(p.Tenjin) != 1 || p.Tenjin[0].Package != "com.tenjin:android-sdk" || p.Tenjin[0].Version != "2.0.0" {
		t.Errorf("tenjin = %+v", p.Tenjin)
	}
	if p.Target(project.IOS) != nil {
		t.Error("unexpected iOS target")
	}
}

func TestAndroidGroovyWithSuffixAndFlavors(t *testing.T) {
	p := detect(t, testproject.Files{
		"app/build.gradle": `apply plugin: 'com.android.application'
android {
    defaultConfig {
        applicationId "com.example.groovy"
    }
    buildTypes {
        debug { applicationIdSuffix ".debug" }
        release { minifyEnabled true }
    }
    productFlavors {
        free {}
    }
}
dependencies {
    // implementation 'com.tenjin:android-sdk:1.0.0'
}
`,
	})
	target := p.Target(project.Android)
	if target == nil || target.BundleID != "com.example.groovy" {
		t.Fatalf("android target = %+v", target)
	}
	if !target.Android.Minify || !target.Android.Flavors {
		t.Errorf("minify = %v, flavors = %v", target.Android.Minify, target.Android.Flavors)
	}
	if !hasNote(p, "com.example.groovy.debug") {
		t.Errorf("no note about the debug suffix: %q", p.Notes)
	}
	if len(p.Tenjin) != 0 {
		t.Errorf("a commented-out dependency was counted: %+v", p.Tenjin)
	}
}

func TestIOSNative(t *testing.T) {
	p := detect(t, testproject.IOSNative())
	target := p.Target(project.IOS)
	if p.Framework != project.Native || target == nil {
		t.Fatalf("framework = %q, target = %+v", p.Framework, target)
	}
	// The test bundle is not the app.
	if target.BundleID != "com.example.sample" || len(target.OtherBundleIDs) != 0 {
		t.Errorf("bundle id = %q, others = %q", target.BundleID, target.OtherBundleIDs)
	}
	if target.IOS.InfoPlist != "Sample/Info.plist" || target.IOS.Podfile != "Podfile" {
		t.Errorf("ios info = %+v", target.IOS)
	}
	if len(p.Tenjin) != 1 || p.Tenjin[0].Version != "1.20.0" {
		t.Errorf("tenjin = %+v", p.Tenjin)
	}
}

// An SDK's repository holds its framework's project beside a demo app's;
// only the app has a bundle ID for Tenjin, whatever the names' order.
func TestIOSProjectWithAnAppTargetWins(t *testing.T) {
	files := testproject.Files{}
	for path, content := range testproject.IOSNative() {
		files["Sample/"+path] = content
	}
	app := files["Sample/Sample.xcodeproj/project.pbxproj"]
	files["Framework/Framework.xcodeproj/project.pbxproj"] = strings.ReplaceAll(strings.ReplaceAll(app, "com.apple.product-type.application", "com.apple.product-type.framework"), "com.example.sample", "com.example.library")
	p := detect(t, files)
	target := p.Target(project.IOS)
	if target == nil || target.IOS.Xcodeproj != "Sample/Sample.xcodeproj" || target.BundleID != "com.example.sample" {
		t.Fatalf("target = %+v", target)
	}
	if !strings.Contains(strings.Join(p.Notes, "\n"), "Found 2 Xcode projects; using Sample/Sample.xcodeproj.") {
		t.Errorf("notes = %q", p.Notes)
	}
}

func TestNativeBothPlatformsSideBySide(t *testing.T) {
	files := testproject.Files{}
	for path, content := range testproject.AndroidNative() {
		files["android-app/"+path] = content
	}
	for path, content := range testproject.IOSNative() {
		files["ios-app/"+path] = content
	}
	p := detect(t, files)
	if p.Framework != project.Native || len(p.Targets) != 2 || len(p.Tenjin) != 2 {
		t.Fatalf("framework = %q, targets = %d, tenjin = %d", p.Framework, len(p.Targets), len(p.Tenjin))
	}
}

func TestFlutter(t *testing.T) {
	p := detect(t, testproject.Flutter())
	if p.Framework != project.Flutter {
		t.Fatalf("framework = %q", p.Framework)
	}
	if bundleID(p, project.Android) != "com.example.sample" || bundleID(p, project.IOS) != "com.example.sample" {
		t.Errorf("targets = %+v", p.Targets)
	}
	if len(p.Tenjin) != 1 || p.Tenjin[0].Package != "tenjin_plugin" || p.Tenjin[0].Version != "1.5.0" {
		t.Errorf("tenjin = %+v", p.Tenjin)
	}
}

// A cross-platform project holds android/ and ios/ folders that look native.
// The root marker must win.
func TestFrameworkPrecedence(t *testing.T) {
	cases := []struct {
		name  string
		files testproject.Files
		want  project.Framework
	}{
		{"react native", testproject.Files{
			"package.json":             `{"dependencies": {"react-native": "0.79.2", "react-native-tenjin": "^1.7.0"}}`,
			"android/app/build.gradle": "apply plugin: \"com.android.application\"\nandroid { defaultConfig { applicationId \"com.example.rn\" } }\n",
		}, project.ReactNative},
		{"capacitor", testproject.Files{
			"package.json":             `{"dependencies": {"@capacitor/core": "^8.0.0"}}`,
			"capacitor.config.ts":      `const config = { appId: 'com.example.cap', appName: 'Cap' };`,
			"android/app/build.gradle": "apply plugin: 'com.android.application'\nandroid { defaultConfig { applicationId \"com.example.cap\" } }\n",
		}, project.Capacitor},
		{"unity", testproject.Files{
			"ProjectSettings/ProjectVersion.txt":    "m_EditorVersion: 2022.3.62f3\n",
			"ProjectSettings/ProjectSettings.asset": "PlayerSettings:\n  applicationIdentifier:\n    Android: com.example.unity\n    iPhone: com.example.unity.ios\n  buildNumber:\n    Standalone: 0\n",
			"Assets/Scenes/Main.unity":              "",
			"Packages/manifest.json":                `{"dependencies": {"com.tenjin.sdk": "https://github.com/tenjin/tenjin-unity-sdk.git#1.22.1"}}`,
		}, project.Unity},
		{"unreal", testproject.Files{
			"Game.uproject":            `{"FileVersion": 3}`,
			"Config/DefaultEngine.ini": "[/Script/IOSRuntimeSettings.IOSRuntimeSettings]\nBundleIdentifier=com.example.unreal\n\n[/Script/AndroidRuntimeSettings.AndroidRuntimeSettings]\nPackageName=com.example.unreal\n",
			"Source/Game/Game.cpp":     "",
		}, project.Unreal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := detect(t, tc.files)
			if p.Framework != tc.want || !p.Supported {
				t.Errorf("framework = %q (supported %v), want %q", p.Framework, p.Supported, tc.want)
			}
			if len(p.Targets) == 0 {
				t.Error("no targets found")
			}
		})
	}
}

func TestUnityReadsBothIdentifiersAndThePackageTag(t *testing.T) {
	p := detect(t, testproject.Files{
		"ProjectSettings/ProjectVersion.txt":    "m_EditorVersion: 2022.3.62f3\n",
		"ProjectSettings/ProjectSettings.asset": "PlayerSettings:\n  applicationIdentifier:\n    Android: com.example.unity\n    iPhone: com.example.unity.ios\n  buildNumber:\n    Standalone: 0\n",
		"Assets/Scenes/Main.unity":              "",
		"Packages/manifest.json":                `{"dependencies": {"com.tenjin.sdk": "https://github.com/tenjin/tenjin-unity-sdk.git#1.22.1"}}`,
	})
	if bundleID(p, project.Android) != "com.example.unity" || bundleID(p, project.IOS) != "com.example.unity.ios" {
		t.Errorf("targets = %+v", p.Targets)
	}
	if len(p.Tenjin) != 1 || p.Tenjin[0].Version != "1.22.1" {
		t.Errorf("tenjin = %+v", p.Tenjin)
	}
}

func TestExpoWithoutNativeFolders(t *testing.T) {
	p := detect(t, testproject.Files{
		"package.json": `{"dependencies": {"react-native": "0.79.2", "expo": "~53.0.0"}}`,
		"app.json":     `{"expo": {"ios": {"bundleIdentifier": "com.example.expo"}, "android": {"package": "com.example.expo"}}}`,
	})
	if p.Framework != project.ReactNative || bundleID(p, project.IOS) != "com.example.expo" {
		t.Fatalf("framework = %q, targets = %+v", p.Framework, p.Targets)
	}
	if !hasNote(p, "expo prebuild") {
		t.Errorf("no prebuild note: %q", p.Notes)
	}
}

// A cross-platform project with one platform's folder covers that one, and
// says how the other is added, with what in the project names it.
func TestAPlatformNotAddedYetIsNoted(t *testing.T) {
	ios := testproject.Files{
		"ios/App/App.xcodeproj/project.pbxproj": testproject.PBXProj(testproject.PBXTarget{Name: "App", ProductType: "com.apple.product-type.application", BundleID: "com.example.cap", InfoPlist: "App/Info.plist"}),
		"ios/App/App/Info.plist":                testproject.InfoPlist,
	}
	capacitor := ios.With(testproject.Files{
		"package.json":        `{"dependencies": {"@capacitor/core": "8.5.3", "@capacitor/ios": "8.5.3"}}`,
		"capacitor.config.ts": "const config = {\n  appId: 'com.example.cap',\n  ios: { contentInset: 'never' },\n  android: { backgroundColor: '#000000' },\n};\n",
	})
	android := testproject.Flutter().Without("ios/Runner.xcodeproj/project.pbxproj", "ios/Runner/Info.plist")
	cases := []struct {
		name  string
		files testproject.Files
		want  string // "" for no note with both platforms, "-" for none with one
	}{
		{"capacitor configures it", capacitor,
			"Only iOS is covered: there is no android/ folder yet, although capacitor.config.ts configures Android. To add Android, run `npm install @capacitor/android@8.5.3` and `npx cap add android`, then `tenjin setup` again."},
		{"capacitor depends on it, with yarn", capacitor.With(testproject.Files{
			"package.json": `{"dependencies": {"@capacitor/core": "^8.0.0", "@capacitor/android": "^8.0.0"}}`,
			"yarn.lock":    "",
		}), "although @capacitor/android is a dependency. To add Android, run `npx cap add android`, then"},
		{"capacitor installs a range, quoted", capacitor.With(testproject.Files{
			"package.json":   `{"dependencies": {"@capacitor/core": "^8.0.0"}}`,
			"pnpm-lock.yaml": "",
		}), "run `pnpm add \"@capacitor/android@^8.0.0\"` and `npx cap add android`"},
		{"capacitor in a workspace", capacitor.With(testproject.Files{
			"package.json": `{"dependencies": {"@capacitor/core": "workspace:*"}}`,
		}), "run `npm install @capacitor/android` (at the version @capacitor/core has, workspace:*) and `npx cap add android`, then"},
		{"capacitor names nothing", capacitor.With(testproject.Files{
			"capacitor.config.ts": "const config = { appId: 'com.example.cap' };\n",
			"package.json":        `{"dependencies": {"@capacitor/core": "8.5.3"}}`,
		}), "Only iOS is covered: there is no android/ folder. If the app ships on Android too, run `npm install @capacitor/android@8.5.3` and `npx cap add android`, then `tenjin setup` again."},
		{"capacitor keeps android elsewhere", capacitor.With(testproject.Files{
			"capacitor.config.ts": "const config = {\n  appId: 'com.example.cap',\n  android: { path: 'native/android' },\n};\n",
		}), "-"},
		{"capacitor with both", capacitor.With(testproject.Files{
			"android/app/build.gradle": "apply plugin: 'com.android.application'\nandroid { defaultConfig { applicationId \"com.example.cap\" } }\n",
		}), ""},
		{"expo configures it", ios.With(testproject.Files{
			"package.json": `{"dependencies": {"react-native": "0.79.2", "expo": "~53.0.0"}}`,
			"app.json":     `{"expo": {"ios": {"bundleIdentifier": "com.example.cap"}, "android": {"package": "com.example.cap"}}}`,
		}), "although app.json gives Android a bundle ID. To add Android, run `npx expo prebuild --platform android`, then"},
		{"flutter without ios", android,
			"Only Android is covered: there is no ios/ folder. If the app ships on iOS too, run `flutter create --platforms=ios .`, then `tenjin setup` again."},
		{"flutter with both", testproject.Flutter(), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := detect(t, tc.files)
			if tc.want == "-" {
				// One platform is found, and where the other is cannot be told.
				if len(p.Targets) != 1 || hasNote(p, "is covered") {
					t.Errorf("targets = %+v, notes = %q", p.Targets, p.Notes)
				}
				return
			}
			if tc.want == "" {
				if len(p.Targets) != 2 || hasNote(p, "is covered") {
					t.Errorf("targets = %+v, notes = %q", p.Targets, p.Notes)
				}
				return
			}
			if len(p.Targets) != 1 {
				t.Fatalf("targets = %+v", p.Targets)
			}
			if !hasNote(p, tc.want) {
				t.Errorf("notes = %q\nwant one with %q", p.Notes, tc.want)
			}
		})
	}
}

func TestCordovaIsRecognisedAndUnsupported(t *testing.T) {
	p := detect(t, testproject.Files{
		"config.xml": `<?xml version='1.0' encoding='utf-8'?><widget id="com.example.cordova" version="1.0.0"></widget>`,
	})
	if p.Framework != project.Cordova || p.Supported {
		t.Errorf("framework = %q, supported = %v", p.Framework, p.Supported)
	}
}

func TestUnknownDirectorySuggestsSubprojects(t *testing.T) {
	files := testproject.Files{"README.md": "monorepo\n"}
	for path, content := range testproject.Flutter() {
		files["mobile/"+path] = content
	}
	p := detect(t, files)
	if p.Framework != project.Unknown {
		t.Fatalf("framework = %q", p.Framework)
	}
	if !hasNote(p, "mobile (flutter)") {
		t.Errorf("notes = %q", p.Notes)
	}
}

func TestDependenciesAndBuildOutputAreNotScanned(t *testing.T) {
	files := testproject.AndroidNative()
	files["node_modules/some-lib/android/build.gradle"] = "apply plugin: 'com.android.application'\nandroid { defaultConfig { applicationId \"com.vendor.lib\" } }\n"
	files["app/build/intermediates/build.gradle"] = "apply plugin: 'com.android.application'\nandroid { defaultConfig { applicationId \"com.build.output\" } }\n"
	p := detect(t, files)
	if got := bundleID(p, project.Android); got != "com.example.sample" {
		t.Errorf("bundle id = %q", got)
	}
	if hasNote(p, "application modules") {
		t.Errorf("vendored or generated modules were counted: %q", p.Notes)
	}
}

func TestDetectFailsOnAMissingDirectory(t *testing.T) {
	if _, err := project.Detect("/definitely/not/here"); err == nil {
		t.Error("expected an error")
	}
}

func TestAndroidFlavorsReportEveryApplicationID(t *testing.T) {
	// The flavors come first in the file, so the default ID is not simply
	// the first one that appears.
	p := detect(t, testproject.Files{
		"settings.gradle.kts": `include(":app")`,
		"app/build.gradle.kts": `plugins { id("com.android.application") }
android {
    flavorDimensions += "store"
    productFlavors {
        create("play") { dimension = "store" }
        create("amazon") {
            dimension = "store"
            applicationId = "com.example.shop.amazon"
        }
        create("beta") { applicationIdSuffix = ".beta" }
    }
    defaultConfig {
        applicationId = "com.example.shop"
    }
    buildTypes {
        debug { applicationIdSuffix = ".debug" }
    }
}
`,
	})
	target := p.Target(project.Android)
	if target == nil || target.BundleID != "com.example.shop" || !target.Android.Flavors {
		t.Fatalf("target = %+v", target)
	}
	got := strings.Join(target.OtherBundleIDs, " ")
	if got != "com.example.shop.amazon com.example.shop.beta com.example.shop.debug" {
		t.Errorf("other bundle IDs = %q", got)
	}
	if !hasNote(p, "com.example.shop.amazon") {
		t.Errorf("notes = %q", p.Notes)
	}
}

func TestAndroidBuildShape(t *testing.T) {
	base := testproject.AndroidNative()
	p := detect(t, base.With(testproject.Files{
		"gradle/libs.versions.toml": "[libraries]\ntenjin = { group = \"com.tenjin\", name = \"android-sdk\", version = \"1.24.0\" }\n",
	}))
	a := p.Target(project.Android).Android
	if a.DSL != "kotlin" || a.VersionCatalog != "gradle/libs.versions.toml" {
		t.Errorf("dsl = %q, catalog = %q", a.DSL, a.VersionCatalog)
	}
	// The manifest names the activity relative to the namespace.
	if a.LauncherActivity != "com.example.sample.MainActivity" || a.LauncherSource != "app/src/main/java/com/example/sample/MainActivity.kt" {
		t.Errorf("launcher = %q in %q", a.LauncherActivity, a.LauncherSource)
	}

	// An alias holds the launcher filter for an activity that lives in a
	// library, so there is no source file to point at.
	const manifest = "app/src/main/AndroidManifest.xml"
	p = detect(t, base.With(testproject.Files{
		manifest: `<manifest xmlns:android="http://schemas.android.com/apk/res/android">
    <application>
        <activity android:name="com.vendor.engine.GameActivity" />
        <activity-alias android:name=".Launcher" android:targetActivity="com.vendor.engine.GameActivity" android:exported="true">
            <intent-filter>
                <action android:name="android.intent.action.MAIN" />
                <category android:name="android.intent.category.LAUNCHER" />
            </intent-filter>
        </activity-alias>
    </application>
</manifest>`,
	}))
	a = p.Target(project.Android).Android
	if a.LauncherActivity != "com.vendor.engine.GameActivity" || a.LauncherSource != "" {
		t.Errorf("launcher = %q in %q", a.LauncherActivity, a.LauncherSource)
	}
}

// The catalog of a cross-platform app sits in android/, beside its settings
// file, and the SDK can be declared only there.
func TestVersionCatalogOfAWrappedAndroidProject(t *testing.T) {
	base := testproject.Flutter()
	p := detect(t, base.With(testproject.Files{
		"android/settings.gradle.kts":       `include(":app")`,
		"android/gradle/libs.versions.toml": "[libraries]\nads = { module = \"com.google.android.gms:play-services-ads:24.0.0\" }\n",
	}))
	if got := p.Target(project.Android).Android.VersionCatalog; got != "android/gradle/libs.versions.toml" {
		t.Errorf("catalog = %q", got)
	}
	if len(p.AdSDKs) != 1 || p.AdSDKs[0].Name != "Google AdMob" || p.AdSDKs[0].Source != "android/gradle/libs.versions.toml" {
		t.Errorf("ad SDKs = %+v", p.AdSDKs)
	}
}

// A SwiftUI app created by a recent Xcode has no Info.plist file and no app
// delegate.
func TestSwiftUIAppWithAGeneratedInfoPlist(t *testing.T) {
	p := detect(t, testproject.Files{
		"Notes.xcodeproj/project.pbxproj": `// !$*UTF8*$!
{
	objects = {
		AAAA /* XCRemoteSwiftPackageReference "tenjin-ios-spm" */ = {
			isa = XCRemoteSwiftPackageReference;
			repositoryURL = "https://github.com/tenjin/tenjin-ios-spm";
		};
		BBBB /* Debug */ = {
			isa = XCBuildConfiguration;
			buildSettings = {
				GENERATE_INFOPLIST_FILE = YES;
				INFOPLIST_KEY_NSUserTrackingUsageDescription = "To measure our ads.";
				PRODUCT_BUNDLE_IDENTIFIER = com.example.notes;
			};
		};
	};
}
`,
		"Notes.xcodeproj/project.xcworkspace/xcshareddata/swiftpm/Package.resolved": `{"pins": [{"identity": "tenjin-ios-spm", "state": {"version": "1.19.1"}}]}`,
		"Notes/NotesApp.swift": `import SwiftUI

// @main struct Old: App {}
@main
struct NotesApp: App {
    var body: some Scene { WindowGroup { Text("Notes") } }
}
`,
	})
	target := p.Target(project.IOS)
	if p.Framework != project.Native || target == nil || target.BundleID != "com.example.notes" {
		t.Fatalf("framework = %q, target = %+v", p.Framework, target)
	}
	ios := target.IOS
	if !ios.GeneratedInfoPlist || ios.InfoPlist != "" || !ios.SwiftPackages || ios.Podfile != "" {
		t.Errorf("ios = %+v", ios)
	}
	if ios.Lifecycle != "swiftui" || ios.EntryPoint != "Notes/NotesApp.swift" {
		t.Errorf("lifecycle = %q, entry point = %q", ios.Lifecycle, ios.EntryPoint)
	}
	if len(p.Tenjin) != 1 || p.Tenjin[0].Package != "TenjinSDK (Swift Package)" || p.Tenjin[0].Version != "1.19.1" {
		t.Errorf("tenjin = %+v", p.Tenjin)
	}
	if p.TrackingPrompt != nil {
		t.Errorf("tracking prompt = %+v, but the app has none", p.TrackingPrompt)
	}
}

// A Swift package pinned to a branch has no version of its own in
// Package.resolved; another package's is not taken for it.
func TestASwiftPackageOnABranchHasNoVersion(t *testing.T) {
	pbx := testproject.PBXProj(testproject.PBXTarget{Name: "Sample", ProductType: "com.apple.product-type.application", BundleID: "com.example.sample", InfoPlist: "Sample/Info.plist"})
	pbx = strings.Replace(pbx, "\tobjects = {\n", "\tobjects = {\n\t\tAAAA /* XCRemoteSwiftPackageReference \"tenjin-ios-spm\" */ = {\n\t\t\tisa = XCRemoteSwiftPackageReference;\n\t\t\trepositoryURL = \"https://github.com/tenjin/tenjin-ios-spm\";\n\t\t};\n", 1)
	for name, resolved := range map[string]string{
		"version 2": `{"pins": [{"identity": "tenjin-ios-spm", "kind": "remoteSourceControl", "location": "https://github.com/tenjin/tenjin-ios-spm", "state": {"branch": "main", "revision": "0123abcd"}}, {"identity": "other", "location": "https://github.com/example/other", "state": {"revision": "4567ef", "version": "5.2.0"}}], "version": 2}`,
		"version 1": `{"object": {"pins": [{"package": "TenjinSDK", "repositoryURL": "https://github.com/tenjin/tenjin-ios-spm", "state": {"branch": "main", "revision": "0123abcd", "version": null}}, {"package": "Other", "repositoryURL": "https://github.com/example/other", "state": {"branch": null, "revision": "4567ef", "version": "5.2.0"}}]}, "version": 1}`,
	} {
		t.Run(name, func(t *testing.T) {
			p := detect(t, testproject.Files{
				"Sample.xcodeproj/project.pbxproj":                                           pbx,
				"Sample.xcodeproj/project.xcworkspace/xcshareddata/swiftpm/Package.resolved": resolved,
				"Sample/Info.plist": testproject.InfoPlist,
			})
			if len(p.Tenjin) != 1 || p.Tenjin[0].Version != "" {
				t.Errorf("tenjin = %+v", p.Tenjin)
			}
		})
	}
}

func TestIOSEntryPointAndTrackingPrompt(t *testing.T) {
	p := detect(t, testproject.IOSNative())
	ios := p.Target(project.IOS).IOS
	if ios.Lifecycle != "app-delegate" || ios.EntryPoint != "Sample/AppDelegate.swift" {
		t.Errorf("lifecycle = %q, entry point = %q", ios.Lifecycle, ios.EntryPoint)
	}
	if p.TrackingPrompt == nil || p.TrackingPrompt.File != "Sample/AppDelegate.swift" || p.TrackingPrompt.Line != 12 {
		t.Errorf("tracking prompt = %+v", p.TrackingPrompt)
	}

	// A prompt that is commented out is not a prompt, and comments above the
	// real one do not shift its line.
	base := testproject.IOSNative()
	p = detect(t, base.With(testproject.Files{
		"Sample/AppDelegate.swift": "/*\n ATTrackingManager.requestTrackingAuthorization { _ in }\n*/\n\n// requestTrackingAuthorization\nfunc ask() {\n    ATTrackingManager.requestTrackingAuthorization { _ in }\n}\n",
	}))
	if p.TrackingPrompt == nil || p.TrackingPrompt.Line != 7 {
		t.Errorf("tracking prompt = %+v", p.TrackingPrompt)
	}
}

func TestIOSSDKLinkedByHand(t *testing.T) {
	p := detect(t, testproject.Files{
		"Demo.xcodeproj/project.pbxproj": `{
	objects = {
		AAAA /* libTenjinSDKUniversal.a in Frameworks */ = {isa = PBXBuildFile; fileRef = BBBB; };
		CCCC = { buildSettings = { PRODUCT_BUNDLE_IDENTIFIER = "com.example.demo"; }; };
	};
}`,
	})
	if len(p.Tenjin) != 1 || !strings.Contains(p.Tenjin[0].Package, "libTenjinSDKUniversal.a") {
		t.Errorf("tenjin = %+v", p.Tenjin)
	}
}

func TestTrackingPromptFromAPackage(t *testing.T) {
	p := detect(t, testproject.Files{
		"package.json":                             `{"dependencies": {"react-native": "0.79.2", "react-native-tracking-transparency": "^0.1.2"}}`,
		"yarn.lock":                                "",
		"ios/App.xcodeproj/project.pbxproj":        `PRODUCT_BUNDLE_IDENTIFIER = com.example.rn;`,
		"node_modules/lib/index.js":                "requestTrackingAuthorization()",
		"android/app/build.gradle":                 "apply plugin: \"com.android.application\"\nandroid { defaultConfig { applicationId \"com.example.rn\" } }\n",
		"android/app/src/main/AndroidManifest.xml": testproject.AndroidManifest,
	})
	if p.TrackingPrompt == nil || p.TrackingPrompt.Package != "react-native-tracking-transparency" || p.TrackingPrompt.File != "" {
		t.Errorf("tracking prompt = %+v", p.TrackingPrompt)
	}
	if p.PackageManager != "yarn" {
		t.Errorf("package manager = %q", p.PackageManager)
	}
}

func TestPackageManager(t *testing.T) {
	cases := []struct {
		name  string
		files testproject.Files
		want  string
	}{
		{"declared in package.json", testproject.Files{"package.json": `{"packageManager": "pnpm@9.1.0", "dependencies": {"react-native": "0.79.2"}}`, "yarn.lock": ""}, "pnpm"},
		{"npm lockfile", testproject.Files{"package.json": `{"dependencies": {"react-native": "0.79.2"}}`, "package-lock.json": "{}"}, "npm"},
		{"bun lockfile", testproject.Files{"package.json": `{"dependencies": {"@capacitor/core": "^8.0.0"}}`, "capacitor.config.json": `{"appId": "com.example.cap"}`, "bun.lock": ""}, "bun"},
		{"no lockfile", testproject.Files{"package.json": `{"dependencies": {"react-native": "0.79.2"}}`}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := detect(t, tc.files).PackageManager; got != tc.want {
				t.Errorf("package manager = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAdSDKs(t *testing.T) {
	names := func(p *project.Project) string {
		var out []string
		for _, sdk := range p.AdSDKs {
			out = append(out, sdk.Name+"@"+sdk.Source)
		}
		return strings.Join(out, " | ")
	}

	t.Run("android", func(t *testing.T) {
		base := testproject.AndroidNative()
		const build = "app/build.gradle.kts"
		p := detect(t, base.With(testproject.Files{
			build: strings.Replace(base[build], "dependencies {", `dependencies {
    implementation("com.applovin:applovin-sdk:13.0.0")
    // implementation("com.ironsource.sdk:mediationsdk:8.0.0")`, 1),
		}))
		// play-services-ads-identifier is not AdMob, and a commented-out
		// dependency is not a dependency.
		if got := names(p); got != "AppLovin MAX@app/build.gradle.kts" {
			t.Errorf("ad SDKs = %q", got)
		}
	})

	// YAML and TOML comment with #: what is commented out there is not a
	// dependency either, and neither suggests a feature.
	t.Run("flutter and a version catalog", func(t *testing.T) {
		base := testproject.Flutter()
		p := detect(t, base.With(testproject.Files{
			"pubspec.yaml":                      base["pubspec.yaml"] + "  # google_mobile_ads: ^5.0.0\n  # purchases_flutter: ^8.0.0\n",
			"android/settings.gradle.kts":       "include(\":app\")\n",
			"android/gradle/libs.versions.toml": "[libraries]\n# applovin = { module = \"com.applovin:applovin-sdk\", version = \"13.0.0\" }\nbilling = { module = \"com.android.billingclient:billing\", version = \"7.0.0\" } # not RevenueCat: com.revenuecat.purchases\n",
		}))
		if got := names(p); got != "" {
			t.Errorf("ad SDKs = %q", got)
		}
		var evidence []string
		for _, f := range p.Features {
			for _, e := range f.Evidence {
				evidence = append(evidence, e.What)
			}
		}
		if got := strings.Join(evidence, ", "); got != "Google Play Billing" {
			t.Errorf("evidence = %q", got)
		}
	})

	t.Run("ios pods", func(t *testing.T) {
		base := testproject.IOSNative()
		p := detect(t, base.With(testproject.Files{
			"Podfile": base["Podfile"] + "pod 'Google-Mobile-Ads-SDK'\n# pod 'AppLovinSDK'\n",
		}))
		if got := names(p); got != "Google AdMob@Podfile" {
			t.Errorf("ad SDKs = %q", got)
		}
	})

	t.Run("unity with applovin", func(t *testing.T) {
		p := detect(t, testproject.Files{
			"ProjectSettings/ProjectVersion.txt":    "m_EditorVersion: 2022.3.62f3\n",
			"ProjectSettings/ProjectSettings.asset": "PlayerSettings:\n  applicationIdentifier:\n    Android: com.example.unity\n    iPhone: com.example.unity\n  buildNumber:\n    Standalone: 0\n",
			"Assets/MaxSdk/Version.md":              "",
			"Assets/Scripts/Boot.cs":                "class Boot { void Start() { ATTrackingStatusBinding.RequestAuthorizationTracking(); } }\n",
			"Packages/manifest.json":                `{"dependencies": {"com.unity.services.levelplay": "8.4.0"}}`,
		})
		if got := names(p); got != "AppLovin MAX@Assets/MaxSdk | ironSource LevelPlay@Packages/manifest.json" {
			t.Errorf("ad SDKs = %q", got)
		}
		if !hasNote(p, "overwrites NSAdvertisingAttributionReportEndpoint") {
			t.Errorf("notes = %q", p.Notes)
		}
		if p.TrackingPrompt == nil || p.TrackingPrompt.File != "Assets/Scripts/Boot.cs" {
			t.Errorf("tracking prompt = %+v", p.TrackingPrompt)
		}
	})
}

// What Unity writes on each build is output. It must not be checked, or
// taken for a native app.
func TestExportedUnityBuild(t *testing.T) {
	t.Run("android", func(t *testing.T) {
		p := detect(t, testproject.Files{
			"settings.gradle":              "include ':launcher', ':unityLibrary'\n",
			"launcher/build.gradle":        "apply plugin: 'com.android.application'\nandroid { defaultConfig { applicationId 'com.example.game' } }\n",
			"unityLibrary/build.gradle":    "apply plugin: 'com.android.library'\n",
			"unityLibrary/libs/tenjin.aar": "",
		})
		if p.Framework != project.UnityExport || p.Supported {
			t.Fatalf("framework = %q, supported = %v", p.Framework, p.Supported)
		}
		if bundleID(p, project.Android) != "com.example.game" || !hasNote(p, "exported from Unity") {
			t.Errorf("targets = %+v, notes = %q", p.Targets, p.Notes)
		}
	})
	t.Run("ios", func(t *testing.T) {
		p := detect(t, testproject.Files{
			"Unity-iPhone.xcodeproj/project.pbxproj": `PRODUCT_BUNDLE_IDENTIFIER = com.example.game;`,
			"Classes/UnityAppController.mm":          "@implementation UnityAppController\n@end\n",
		})
		if p.Framework != project.UnityExport || p.Supported || bundleID(p, project.IOS) != "com.example.game" {
			t.Errorf("framework = %q, supported = %v, targets = %+v", p.Framework, p.Supported, p.Targets)
		}
		// What matters most comes first: the error shows the notes in order.
		if len(p.Notes) == 0 || !strings.Contains(p.Notes[0], "exported from Unity") {
			t.Errorf("notes = %q", p.Notes)
		}
	})
}

func TestKotlinMultiplatform(t *testing.T) {
	root := `plugins {
    alias(libs.plugins.androidApplication) apply false
    alias(libs.plugins.kotlinMultiplatform) apply false
}
`
	// Compose Multiplatform: the Android application is itself a
	// multiplatform module, with its manifest under src/androidMain.
	p := detect(t, testproject.Files{
		"settings.gradle.kts": `include(":composeApp")`,
		"build.gradle.kts":    root,
		"composeApp/build.gradle.kts": `plugins {
    alias(libs.plugins.kotlinMultiplatform)
    alias(libs.plugins.androidApplication)
}
android { defaultConfig { applicationId = "com.example.kmp" } }
`,
		"iosApp/iosApp.xcodeproj/project.pbxproj": `PRODUCT_BUNDLE_IDENTIFIER = com.example.kmp;`,
	})
	if p.Framework != project.KotlinMultiplatform || p.Supported || !hasNote(p, "no Kotlin Multiplatform SDK") {
		t.Errorf("framework = %q, supported = %v, notes = %q", p.Framework, p.Supported, p.Notes)
	}

	// A native Android app that only shares logic through a multiplatform
	// library is still a native app.
	files := testproject.AndroidNative().With(testproject.Files{
		"build.gradle.kts":        root,
		"shared/build.gradle.kts": "plugins {\n    kotlin(\"multiplatform\")\n    id(\"com.android.library\")\n}\n",
	})
	p = detect(t, files)
	if p.Framework != project.Native || !p.Supported || !hasNote(p, "Kotlin Multiplatform is used in shared") {
		t.Errorf("framework = %q, supported = %v, notes = %q", p.Framework, p.Supported, p.Notes)
	}
}

func TestDotNetIsRecognisedAndUnsupported(t *testing.T) {
	p := detect(t, testproject.Files{
		"Shop/Shop.csproj": `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <TargetFrameworks>net9.0-android;net9.0-ios</TargetFrameworks>
    <UseMaui>true</UseMaui>
  </PropertyGroup>
</Project>`,
		"Shop/Platforms/Android/AndroidManifest.xml": testproject.AndroidManifest,
	})
	if p.Framework != project.DotNet || p.Supported || !hasNote(p, "Shop/Shop.csproj") {
		t.Errorf("framework = %q, supported = %v, notes = %q", p.Framework, p.Supported, p.Notes)
	}
	// A server-side .NET project is not a mobile app.
	p = detect(t, testproject.Files{"Api/Api.csproj": `<Project Sdk="Microsoft.NET.Sdk.Web"><PropertyGroup><TargetFramework>net9.0</TargetFramework></PropertyGroup></Project>`})
	if p.Framework != project.Unknown {
		t.Errorf("framework = %q", p.Framework)
	}
}

func TestMonorepoSuggestsProjectsTwoLevelsDown(t *testing.T) {
	files := testproject.Files{"README.md": "monorepo\n", "services/api/main.go": "package main\n"}
	for path, content := range testproject.Flutter() {
		files["apps/mobile/"+path] = content
	}
	for path, content := range testproject.AndroidNative() {
		files["apps/tv/"+path] = content
	}
	p := detect(t, files)
	if p.Framework != project.Unknown || !hasNote(p, "apps/mobile (flutter), apps/tv (native)") {
		t.Errorf("framework = %q, notes = %q", p.Framework, p.Notes)
	}

	// A Gradle build whose app module sits deeper is named by its root, not
	// by the folder that happens to hold the module.
	p = detect(t, testproject.Files{
		"tv/settings.gradle.kts":          `include(":modules:app")`,
		"tv/modules/app/build.gradle.kts": "plugins { id(\"com.android.application\") }\nandroid { defaultConfig { applicationId = \"com.example.tv\" } }\n",
	})
	if !hasNote(p, "Found: tv (native).") {
		t.Errorf("notes = %q", p.Notes)
	}
}

// A project saved with Windows line endings, as Unity projects on Windows
// often are, is read as the same project.
func TestWindowsLineEndings(t *testing.T) {
	for name, files := range map[string]testproject.Files{
		"android": testproject.AndroidNative(), "stores": testproject.AndroidMultiStore(),
		"ios": testproject.IOSNative(), "flutter": testproject.Flutter(), "unity": testproject.Unity(),
	} {
		t.Run(name, func(t *testing.T) {
			want, got := describe(detect(t, files)), describe(detect(t, withCRLF(files)))
			if got != want {
				t.Errorf("with CRLF:\n%s\nwith LF:\n%s", got, want)
			}
		})
	}
}

func withCRLF(files testproject.Files) testproject.Files {
	out := testproject.Files{}
	for name, content := range files {
		out[name] = strings.ReplaceAll(content, "\n", "\r\n")
	}
	return out
}

func describe(p *project.Project) string {
	var b strings.Builder
	b.WriteString(string(p.Framework))
	for _, t := range p.Targets {
		for _, build := range t.Builds {
			b.WriteString(" " + build.ID + "=" + build.BundleID + "/" + build.TenjinPlatform)
		}
	}
	for _, d := range p.Tenjin {
		b.WriteString(" " + d.Package + "@" + d.Version)
	}
	return b.String()
}

// The Tenjin Unity package's own scripts mention the tracking prompt, and
// a test can ask for it: neither is the app asking.
func TestTheTrackingPromptIsTheAppsOwn(t *testing.T) {
	base := testproject.Unity()
	const script = "Assets/Scripts/TenjinBootstrap.cs"
	files := base.With(testproject.Files{
		script:                                   regexp.MustCompile(`(?s)#if UNITY_IOS.*?#else\n(.*?)#endif\n`).ReplaceAllString(base[script], "$1"),
		"Assets/Tenjin/Scripts/AndroidTenjin.cs": "public class AndroidTenjin : BaseTenjin {\n    public override void RequestTrackingAuthorizationWithCompletionHandler(Action<int> callback) {}\n}\n",
		"Assets/Tests/PromptTest.cs":             "public class PromptTest { void Run() { Tenjin.getInstance(\"k\").RequestTrackingAuthorizationWithCompletionHandler(null); } }\n",
	})
	if strings.Contains(files[script], "RequestTracking") {
		t.Fatal("the fixture still asks for tracking permission")
	}
	if p := detect(t, files); p.TrackingPrompt != nil {
		t.Errorf("tracking prompt = %+v, but the app has none", p.TrackingPrompt)
	}
}

// A module that sets no applicationId ships under its namespace, which the
// Android Gradle plugin takes as the default, in either DSL.
func TestTheNamespaceIsTheDefaultApplicationID(t *testing.T) {
	base := testproject.AndroidNative()
	const build = "app/build.gradle.kts"
	if !strings.Contains(base[build], `applicationId = "com.example.sample"`) {
		t.Fatal("the fixture sets no applicationId")
	}
	p := detect(t, base.With(testproject.Files{build: strings.Replace(base[build], `        applicationId = "com.example.sample"`+"\n", "", 1)}))
	target := p.Target(project.Android)
	if target == nil || target.BundleID != "com.example.sample" || target.Source != build {
		t.Fatalf("android target = %+v", target)
	}
	if got := buildList(t, p, project.Android); got != "android com.example.sample android  __TENJIN_ANDROID_SDK_KEY__" {
		t.Errorf("builds:\n%s", got)
	}
	if hasNote(p, "Could not read") {
		t.Errorf("notes = %q", p.Notes)
	}

	p = detect(t, testproject.Files{
		"settings.gradle":                  "include ':app'\n",
		"app/build.gradle":                 "apply plugin: 'com.android.application'\nandroid {\n    namespace 'com.example.groovy'\n    defaultConfig {\n        minSdk 24\n    }\n}\n",
		"app/src/main/AndroidManifest.xml": testproject.AndroidManifest,
	})
	if got := bundleID(p, project.Android); got != "com.example.groovy" {
		t.Errorf("bundle ID = %q, notes = %q", got, p.Notes)
	}

	// An applicationId still wins over the namespace.
	p = detect(t, base.With(testproject.Files{build: strings.Replace(base[build], `applicationId = "com.example.sample"`, `applicationId = "com.example.shipped"`, 1)}))
	if got := bundleID(p, project.Android); got != "com.example.shipped" {
		t.Errorf("bundle ID = %q", got)
	}
}

// Shrinking is on in every spelling Gradle takes: Groovy's minifyEnabled
// true, the assignment minifyEnabled = true, and Kotlin's isMinifyEnabled.
func TestMinifyInEverySpelling(t *testing.T) {
	base := testproject.AndroidNative()
	const build = "app/build.gradle.kts"
	if !strings.Contains(base[build], "isMinifyEnabled = false") {
		t.Fatal("the fixture does not turn shrinking off")
	}
	for _, spelling := range []string{"minifyEnabled true", "minifyEnabled = true", "isMinifyEnabled = true"} {
		p := detect(t, base.With(testproject.Files{build: strings.Replace(base[build], "isMinifyEnabled = false", spelling, 1)}))
		if !p.Target(project.Android).Android.Minify {
			t.Errorf("%q: minify is off", spelling)
		}
	}
	for _, spelling := range []string{"minifyEnabled false", "minifyEnabled = false", "isMinifyEnabled = false", "minifyEnabled = trueish"} {
		p := detect(t, base.With(testproject.Files{build: strings.Replace(base[build], "isMinifyEnabled = false", spelling, 1)}))
		if p.Target(project.Android).Android.Minify {
			t.Errorf("%q: minify is on", spelling)
		}
	}
}
