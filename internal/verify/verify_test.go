package verify_test

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/tenjin/tenjin-cli/internal/project"
	"github.com/tenjin/tenjin-cli/internal/testproject"
	"github.com/tenjin/tenjin-cli/internal/track"
	"github.com/tenjin/tenjin-cli/internal/verify"
)

// fakeKeys answers key checks from a table keyed by "key|bundle|platform",
// and records what it was asked.
type fakeKeys struct {
	answers map[string]track.KeyStatus
	err     error
	asked   []string
}

func (f *fakeKeys) CheckKey(_ context.Context, key, bundleID, platform string) (track.KeyCheck, error) {
	f.asked = append(f.asked, key+"|"+bundleID+"|"+platform)
	if f.err != nil {
		return track.KeyCheck{}, f.err
	}
	status, ok := f.answers[key+"|"+bundleID+"|"+platform]
	if !ok {
		status = track.KeyRejected
	}
	return track.KeyCheck{Status: status, Detail: "test"}, nil
}

func run(t *testing.T, files testproject.Files, opts verify.Options) *verify.Report {
	t.Helper()
	p, err := project.Detect(testproject.Write(t, files))
	if err != nil {
		t.Fatal(err)
	}
	return verify.Run(context.Background(), p, opts)
}

func find(r *verify.Report, id string, platform project.Platform) *verify.Finding {
	for i := range r.Findings {
		if r.Findings[i].ID == id && r.Findings[i].Platform == platform {
			return &r.Findings[i]
		}
	}
	return nil
}

func expect(t *testing.T, r *verify.Report, id string, platform project.Platform, want verify.Severity) *verify.Finding {
	t.Helper()
	f := find(r, id, platform)
	if f == nil {
		t.Fatalf("no finding %q for platform %q; got %s", id, platform, ids(r))
	}
	if f.Severity != want {
		t.Fatalf("finding %q is %q, want %q (%s)", id, f.Severity, want, f.Title)
	}
	return f
}

func ids(r *verify.Report) string {
	var out []string
	for _, f := range r.Findings {
		out = append(out, string(f.Severity)+":"+f.ID)
	}
	return strings.Join(out, " ")
}

// Windows line endings change nothing the checks find.
func TestWindowsLineEndings(t *testing.T) {
	for name, files := range map[string]testproject.Files{
		"android": testproject.AndroidNative(), "stores": testproject.AndroidMultiStore(),
		"ios": testproject.IOSNative(), "flutter": testproject.Flutter(), "unity": testproject.Unity(),
	} {
		t.Run(name, func(t *testing.T) {
			crlf := testproject.Files{}
			for path, content := range files {
				crlf[path] = strings.ReplaceAll(content, "\n", "\r\n")
			}
			want, got := ids(run(t, files, verify.Options{})), ids(run(t, crlf, verify.Options{}))
			if got != want {
				t.Errorf("with CRLF: %s\nwith LF:   %s", got, want)
			}
		})
	}
}

func TestCorrectIntegrationsPassClean(t *testing.T) {
	cases := map[string]testproject.Files{
		"android": testproject.AndroidNative(),
		"ios":     testproject.IOSNative(),
		"flutter": testproject.Flutter(),
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			r := run(t, files, verify.Options{})
			if r.Summary.Errors != 0 || r.Summary.Warnings != 0 {
				t.Errorf("errors = %d, warnings = %d: %s", r.Summary.Errors, r.Summary.Warnings, ids(r))
			}
			if r.Summary.Passed == 0 {
				t.Error("nothing was checked")
			}
		})
	}
}

func TestAndroidFaults(t *testing.T) {
	base := testproject.AndroidNative()
	const (
		build    = "app/build.gradle.kts"
		manifest = "app/src/main/AndroidManifest.xml"
		activity = "app/src/main/java/com/example/sample/MainActivity.kt"
	)
	cases := []struct {
		name     string
		files    testproject.Files
		id       string
		severity verify.Severity
	}{
		{"sdk not a dependency",
			base.With(testproject.Files{build: strings.Replace(base[build], `implementation("com.tenjin:android-sdk:2.0.0")`, "", 1)}),
			"sdk.dependency", verify.Error},
		{"never initialised",
			base.With(testproject.Files{activity: "package com.example.sample\nclass MainActivity\n"}),
			"sdk.init", verify.Error},
		{"connect never called",
			base.With(testproject.Files{activity: strings.Replace(base[activity], "tenjin.connect()", "", 1)}),
			"sdk.connect", verify.Error},
		{"init only in a comment",
			base.With(testproject.Files{activity: "// TenjinSDK.getInstance(this, \"X\").connect()\n/* TenjinSDK.getInstance(this, \"Y\")\n   .connect() */\n"}),
			"sdk.init", verify.Error},
		{"placeholder key",
			base.With(testproject.Files{activity: strings.Replace(base[activity], testproject.Key, "YOUR_SDK_KEY", 1)}),
			"sdk.key.placeholder", verify.Error},
		{"no internet permission",
			base.With(testproject.Files{manifest: strings.Replace(base[manifest], `<uses-permission android:name="android.permission.INTERNET" />`, "", 1)}),
			"android.permission.internet", verify.Error},
		{"ad id not declared, nor brought by a library",
			base.With(testproject.Files{
				manifest: strings.Replace(base[manifest], `<uses-permission android:name="com.google.android.gms.permission.AD_ID" />`, "", 1),
				build:    strings.Replace(base[build], `implementation("com.google.android.gms:play-services-ads-identifier:18.1.0")`, "", 1),
			}),
			"android.permission.ad_id", verify.Warning},
		{"read phone state declared",
			base.With(testproject.Files{manifest: strings.Replace(base[manifest], "<application", `<uses-permission android:name="android.permission.READ_PHONE_STATE" />`+"\n<application", 1)}),
			"android.permission.read_phone_state", verify.Warning},
		{"no ads identifier library",
			base.With(testproject.Files{build: strings.Replace(base[build], `implementation("com.google.android.gms:play-services-ads-identifier:18.1.0")`, "", 1)}),
			"android.play_services.ads_identifier", verify.Error},
		{"no appset library",
			base.With(testproject.Files{build: strings.Replace(base[build], `implementation("com.google.android.gms:play-services-appset:16.1.0")`, "", 1)}),
			"android.play_services.appset", verify.Warning},
		{"shrinking without keep rules",
			// Before 2.0.0 the SDK has no keep rules of its own.
			base.With(testproject.Files{build: strings.Replace(strings.Replace(base[build], "isMinifyEnabled = false", "isMinifyEnabled = true", 1), "android-sdk:2.0.0", "android-sdk:1.24.0", 1)}),
			"android.proguard", verify.Warning},
		{"connect in the Application class",
			base.With(testproject.Files{activity: strings.Replace(base[activity], "class MainActivity : Activity()", "class App : Application()", 1)}),
			"android.connect.location", verify.Warning},
		// What is commented out is not there.
		{"ads identifier library commented out",
			base.With(testproject.Files{build: strings.Replace(base[build], `implementation("com.google.android.gms:play-services-ads-identifier:18.1.0")`, `// implementation("com.google.android.gms:play-services-ads-identifier:18.1.0")`, 1)}),
			"android.play_services.ads_identifier", verify.Error},
		{"ads identifier library commented out in the version catalog",
			base.With(testproject.Files{
				build:                       strings.Replace(base[build], `implementation("com.google.android.gms:play-services-ads-identifier:18.1.0")`, "implementation(libs.ads.identifier)", 1),
				"gradle/libs.versions.toml": "[libraries]\n# ads-identifier = { module = \"com.google.android.gms:play-services-ads-identifier\", version = \"18.1.0\" }\n",
			}),
			"android.play_services.ads_identifier", verify.Error},
		{"internet only from a commented-out Mobile Ads",
			base.With(testproject.Files{
				manifest: strings.Replace(base[manifest], `<uses-permission android:name="android.permission.INTERNET" />`, "", 1),
				build:    strings.Replace(base[build], "dependencies {", "dependencies {\n    /* implementation(\"com.google.android.gms:play-services-ads:23.0.0\") */", 1),
			}),
			"android.permission.internet", verify.Error},
		{"keep rules commented out",
			base.With(testproject.Files{
				build:                    strings.Replace(strings.Replace(base[build], "isMinifyEnabled = false", "isMinifyEnabled = true", 1), "android-sdk:2.0.0", "android-sdk:1.24.0", 1),
				"app/proguard-rules.pro": "# -keep class com.tenjin.** { *; }\n",
			}),
			"android.proguard", verify.Warning},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := run(t, tc.files, verify.Options{})
			f := expect(t, r, tc.id, project.Android, tc.severity)
			if f.Fix == "" {
				t.Errorf("%q has no fix", tc.id)
			}
		})
	}
}

func TestAdIDRemovalIsAnError(t *testing.T) {
	base := testproject.AndroidNative()
	const manifest = "app/src/main/AndroidManifest.xml"
	r := run(t, base.With(testproject.Files{manifest: `<?xml version="1.0" encoding="utf-8"?>
<manifest xmlns:android="http://schemas.android.com/apk/res/android" xmlns:tools="http://schemas.android.com/tools">
    <uses-permission android:name="android.permission.INTERNET" />
    <uses-permission android:name="android.permission.ACCESS_NETWORK_STATE" />
    <uses-permission android:name="com.google.android.gms.permission.AD_ID" tools:node="remove" />
</manifest>
`}), verify.Options{})
	expect(t, r, "android.permission.ad_id", project.Android, verify.Error)
}

// The build drops a permission the app's manifest removes, whatever brings
// it: a library such as Google Mobile Ads, or a library module's manifest.
func TestNetworkPermissionsTheAppRemovesAreMissing(t *testing.T) {
	base := testproject.AndroidNative()
	const (
		manifest = "app/src/main/AndroidManifest.xml"
		build    = "app/build.gradle.kts"
	)
	removing := `<?xml version="1.0" encoding="utf-8"?>
<manifest xmlns:android="http://schemas.android.com/apk/res/android" xmlns:tools="http://schemas.android.com/tools">
    <uses-permission android:name="android.permission.INTERNET" tools:node="remove" />
    <uses-permission android:name="android.permission.ACCESS_NETWORK_STATE" tools:node="remove" />
    <uses-permission android:name="com.google.android.gms.permission.AD_ID" />
</manifest>
`
	files := base.With(testproject.Files{
		manifest:                           removing,
		build:                              strings.Replace(base[build], "dependencies {", "dependencies {\n    implementation(\"com.google.android.gms:play-services-ads:23.0.0\")\n    implementation(project(\":net\"))", 1),
		"net/build.gradle.kts":             "plugins { id(\"com.android.library\") }\n",
		"net/src/main/AndroidManifest.xml": testproject.AndroidManifest,
	})
	r := run(t, files, verify.Options{})
	for id, severity := range map[string]verify.Severity{
		"android.permission.internet":      verify.Error,
		"android.permission.network_state": verify.Warning,
	} {
		f := expect(t, r, id, project.Android, severity)
		if f.File != manifest || !strings.Contains(f.Fix, `tools:node="remove"`) {
			t.Errorf("%s = %+v", id, f)
		}
	}
}

func TestProguardRulesSatisfyTheShrinkingCheck(t *testing.T) {
	base := testproject.AndroidNative()
	const build = "app/build.gradle.kts"
	r := run(t, base.With(testproject.Files{
		build:                    strings.Replace(base[build], "isMinifyEnabled = false", "isMinifyEnabled = true", 1),
		"app/proguard-rules.pro": "-keep class com.tenjin.** { *; }\n",
	}), verify.Options{})
	expect(t, r, "android.proguard", project.Android, verify.Pass)
}

func TestIOSFaults(t *testing.T) {
	base := testproject.IOSNative()
	const (
		plist    = "Sample/Info.plist"
		delegate = "Sample/AppDelegate.swift"
	)
	cases := []struct {
		name     string
		files    testproject.Files
		id       string
		severity verify.Severity
	}{
		{"pod missing",
			base.With(testproject.Files{"Podfile": "target 'Sample' do\nend\n"}),
			"sdk.dependency", verify.Error},
		{"pod commented out",
			base.With(testproject.Files{"Podfile": "target 'Sample' do\n  # pod 'TenjinSDK'\nend\n"}),
			"sdk.dependency", verify.Error},
		{"no tracking text",
			base.With(testproject.Files{plist: strings.Replace(base[plist], "NSUserTrackingUsageDescription", "SomethingElse", 1)}),
			"ios.plist.att_description", verify.Warning},
		{"tracking text commented out",
			base.With(testproject.Files{plist: strings.Replace(strings.Replace(base[plist], "<key>NSUserTrackingUsageDescription</key>", "<!-- <key>NSUserTrackingUsageDescription</key>", 1), "people to the app.</string>", "people to the app.</string> -->", 1)}),
			"ios.plist.att_description", verify.Warning},
		{"no skan endpoint",
			base.With(testproject.Files{plist: strings.Replace(base[plist], "NSAdvertisingAttributionReportEndpoint", "SomethingElse", 1)}),
			"ios.plist.skan_endpoint", verify.Warning},
		{"skan endpoint points elsewhere",
			base.With(testproject.Files{plist: strings.Replace(base[plist], "https://tenjin-skan.com", "https://other-mmp.example", 1)}),
			"ios.plist.skan_endpoint", verify.Warning},
		{"no tracking request",
			base.With(testproject.Files{delegate: "import TenjinSDK\nfunc start() {\n  TenjinSDK.getInstance(\"" + testproject.Key + "\")\n  TenjinSDK.connect()\n}\n"}),
			"ios.att_request", verify.Warning},
		{"connect never called",
			base.With(testproject.Files{delegate: "import TenjinSDK\nfunc start() {\n  TenjinSDK.getInstance(\"" + testproject.Key + "\")\n}\n"}),
			"sdk.connect", verify.Error},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := run(t, tc.files, verify.Options{})
			expect(t, r, tc.id, project.IOS, tc.severity)
		})
	}
}

func TestObjectiveCCallsAreRecognised(t *testing.T) {
	base := testproject.IOSNative()
	r := run(t, base.Without("Sample/AppDelegate.swift").With(testproject.Files{
		"Sample/AppDelegate.m": `#import "TenjinSDK.h"
- (void)applicationDidBecomeActive:(UIApplication *)application {
    [TenjinSDK initialize:@"` + testproject.Key + `"];
    [TenjinSDK requestTrackingAuthorizationWithCompletionHandler:^(NSUInteger status) {
        [TenjinSDK connect];
    }];
}
`,
	}), verify.Options{})
	if r.Summary.Errors != 0 || r.Summary.Warnings != 0 {
		t.Errorf("errors = %d, warnings = %d: %s", r.Summary.Errors, r.Summary.Warnings, ids(r))
	}
}

func TestFlutterDeprecatedInitialiser(t *testing.T) {
	base := testproject.Flutter()
	r := run(t, base.With(testproject.Files{
		"lib/main.dart": strings.Replace(base["lib/main.dart"], "initialize(sdkKey:", "init(apiKey:", 1),
	}), verify.Options{})
	expect(t, r, "sdk.init.deprecated", "", verify.Warning)
	expect(t, r, "sdk.init", "", verify.Pass)
}

// Flutter's plugin ships the Play Services libraries, so the app does not
// have to declare them.
func TestFlutterDoesNotAskForPlayServices(t *testing.T) {
	r := run(t, testproject.Flutter(), verify.Options{})
	if f := find(r, "android.play_services.ads_identifier", project.Android); f != nil {
		t.Errorf("unexpected finding: %+v", f)
	}
}

func TestUncheckedFrameworkSaysSo(t *testing.T) {
	r := run(t, testproject.Files{
		"Game.uproject":            `{"FileVersion": 3}`,
		"Config/DefaultEngine.ini": "[/Script/AndroidRuntimeSettings.AndroidRuntimeSettings]\nPackageName=com.example.unreal\n",
		"Source/Game/Game.cpp":     "",
	}, verify.Options{})
	if f := expect(t, r, "framework.unchecked", "", verify.Info); !strings.Contains(f.Title, "Unreal Engine projects") {
		t.Errorf("title = %q", f.Title)
	}
	if r.Summary.Errors != 0 {
		t.Errorf("errors = %d", r.Summary.Errors)
	}
}

func TestKeyCheck(t *testing.T) {
	const pairing = testproject.Key + "|com.example.sample|android"
	cases := []struct {
		name     string
		status   track.KeyStatus
		id       string
		severity verify.Severity
	}{
		{"accepted", track.KeyValid, "key.valid", verify.Pass},
		{"no app for this bundle", track.KeyNoApp, "key.no_app", verify.Error},
		{"rejected", track.KeyRejected, "key.rejected", verify.Error},
		{"disabled", track.KeyDisabled, "key.disabled", verify.Error},
		{"unexpected answer", track.KeyUnclear, "key.unclear", verify.Warning},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			keys := &fakeKeys{answers: map[string]track.KeyStatus{pairing: tc.status}}
			r := run(t, testproject.AndroidNative(), verify.Options{Keys: keys})
			f := expect(t, r, tc.id, project.Android, tc.severity)
			if strings.Contains(f.Title+f.Detail+f.Fix, testproject.Key) {
				t.Errorf("the finding prints the whole SDK key: %+v", f)
			}
		})
	}
}

func TestKeyCheckUsesTheOverride(t *testing.T) {
	keys := &fakeKeys{answers: map[string]track.KeyStatus{"OVERRIDE|com.example.sample|android": track.KeyValid}}
	r := run(t, testproject.AndroidNative(), verify.Options{Keys: keys, Given: verify.GivenKeys{All: verify.GivenKey{Value: "OVERRIDE"}}})
	expect(t, r, "key.valid", project.Android, verify.Pass)
	if len(keys.asked) != 1 {
		t.Errorf("asked = %q", keys.asked)
	}
}

// A cross-platform project with one key per platform is fine when each
// platform is covered by one of them.
func TestKeyCheckWithOneKeyPerPlatform(t *testing.T) {
	const iosKey = "IOSKEY7890ASDFGH5678ZXCVBN9012MNBV"
	base := testproject.Flutter()
	files := base.With(testproject.Files{
		"lib/main.dart": strings.Replace(base["lib/main.dart"],
			"TenjinSDK.instance.initialize(sdkKey: '"+testproject.Key+"');",
			"if (Platform.isIOS) {\n    TenjinSDK.instance.initialize(sdkKey: '"+iosKey+"');\n  } else {\n    TenjinSDK.instance.initialize(sdkKey: '"+testproject.Key+"');\n  }", 1),
	})
	keys := &fakeKeys{answers: map[string]track.KeyStatus{
		testproject.Key + "|com.example.sample|android": track.KeyValid,
		iosKey + "|com.example.sample|ios":              track.KeyValid,
	}}
	r := run(t, files, verify.Options{Keys: keys})
	expect(t, r, "key.valid", project.Android, verify.Pass)
	expect(t, r, "key.valid", project.IOS, verify.Pass)
}

// One key in a cross-platform project fails on the platform it was not made
// for, and the finding names that platform.
func TestKeyCheckWithOneKeyForBothPlatforms(t *testing.T) {
	keys := &fakeKeys{answers: map[string]track.KeyStatus{
		testproject.Key + "|com.example.sample|android": track.KeyValid,
	}}
	r := run(t, testproject.Flutter(), verify.Options{Keys: keys})
	expect(t, r, "key.valid", project.Android, verify.Pass)
	expect(t, r, "key.rejected", project.IOS, verify.Error)
}

func TestKeyCheckSkipsPlaceholdersAndMissingKeys(t *testing.T) {
	base := testproject.AndroidNative()
	const activity = "app/src/main/java/com/example/sample/MainActivity.kt"
	keys := &fakeKeys{}
	r := run(t, base.With(testproject.Files{
		activity: strings.Replace(base[activity], `"`+testproject.Key+`"`, "BuildConfig.TENJIN_KEY", 1),
	}), verify.Options{Keys: keys})
	expect(t, r, "key.unchecked", project.Android, verify.Info)
	if len(keys.asked) != 0 {
		t.Errorf("asked Tenjin about %q", keys.asked)
	}
}

func TestKeyCheckSurvivesUnreachableTracking(t *testing.T) {
	keys := &fakeKeys{err: errors.New("dial tcp: no route to host")}
	r := run(t, testproject.AndroidNative(), verify.Options{Keys: keys})
	expect(t, r, "key.unreachable", project.Android, verify.Warning)
	if r.Summary.Errors != 0 {
		t.Errorf("a network failure was counted as an integration error: %s", ids(r))
	}
}

// has reports whether the report holds a finding with this ID, on any
// platform.
func has(r *verify.Report, id string) bool {
	for _, f := range r.Findings {
		if f.ID == id {
			return true
		}
	}
	return false
}

func TestOutdatedSDKVersion(t *testing.T) {
	const build = "app/build.gradle.kts"
	base := testproject.AndroidNative()
	old := base.With(testproject.Files{build: strings.Replace(base[build], "android-sdk:2.0.0", "android-sdk:1.16.5", 1)})
	f := expect(t, run(t, old, verify.Options{}), "sdk.version", project.Android, verify.Warning)
	if !strings.Contains(f.Title, "1.16.5") || !strings.Contains(f.Fix, "com.tenjin:android-sdk to 2.0.0") || f.File != build {
		t.Errorf("finding = %+v", f)
	}

	// Current, and newer than this CLI knows about: nothing to say.
	for _, version := range []string{"2.0.0", "2.1.3", "3.0.0"} {
		files := base.With(testproject.Files{build: strings.Replace(base[build], "android-sdk:2.0.0", "android-sdk:"+version, 1)})
		if has(run(t, files, verify.Options{}), "sdk.version") {
			t.Errorf("version %s was reported as outdated", version)
		}
	}
}

// A range in package.json says what may be installed, not what is. Only a
// range that cannot reach the current release is certainly outdated.
func TestOutdatedSDKVersionWithRanges(t *testing.T) {
	cases := []struct {
		declared string
		outdated bool
	}{
		{"^1.7.0", false},
		{"^1.2.0", false}, // may already resolve to 1.7.0
		{"~1.6.0", true},  // stays on 1.6.x
		{"1.6.0", true},
		{"^0.9.0", true},
		{"1.7.0-beta.1", true}, // a prerelease of the current release
		{"^1.7.0-beta.1", false},
		{"file:..", false},
		{"latest", false},
	}
	for _, tc := range cases {
		t.Run(tc.declared, func(t *testing.T) {
			r := run(t, testproject.Files{
				"package.json": `{"dependencies": {"react-native": "0.79.2", "react-native-tenjin": "` + tc.declared + `"}}`,
				"App.tsx":      "import Tenjin from 'react-native-tenjin';\nTenjin.initialize('" + testproject.Key + "');\nTenjin.connect();\n",
			}, verify.Options{})
			if got := has(r, "sdk.version"); got != tc.outdated {
				t.Errorf("reported as outdated = %v, want %v", got, tc.outdated)
			}
		})
	}
}

// A fix must be something that can be pasted: a version, not "<latest>".
func TestFixesNameAVersion(t *testing.T) {
	const build = "app/build.gradle.kts"
	base := testproject.AndroidNative()
	bare := base.With(testproject.Files{build: "plugins { id(\"com.android.application\") }\nandroid { defaultConfig { applicationId = \"com.example.sample\" } }\n"})
	r := run(t, bare, verify.Options{})
	for _, id := range []string{"sdk.dependency", "android.play_services.ads_identifier", "android.play_services.appset"} {
		f := find(r, id, project.Android)
		if f == nil || strings.Contains(f.Fix, "<latest>") || !regexp.MustCompile(`:\d+\.\d+\.\d+"\)`).MatchString(f.Fix) {
			t.Errorf("%s: fix = %+v", id, f)
		}
	}
}

// A coding agent leaves a placeholder where the key belongs; it is named as
// such, once per platform, and not mistaken for a key Tenjin refused.
func TestAnUnfilledPlaceholderIsNamed(t *testing.T) {
	base := testproject.AndroidNative()
	const activity = "app/src/main/java/com/example/sample/MainActivity.kt"
	keys := &fakeKeys{}
	r := run(t, base.With(testproject.Files{
		activity: strings.Replace(base[activity], testproject.Key, "__TENJIN_ANDROID_SDK_KEY__", 1),
	}), verify.Options{Keys: keys})

	var unfilled []verify.Finding
	for _, f := range r.Findings {
		if f.ID == "sdk.key.unfilled" {
			unfilled = append(unfilled, f)
		}
		if strings.HasPrefix(f.ID, "sdk.key.") && f.ID != "sdk.key.unfilled" && f.Severity != verify.Info {
			t.Errorf("the placeholder was also reported as %s: %s", f.ID, f.Title)
		}
	}
	if len(unfilled) != 1 || unfilled[0].Severity != verify.Error || unfilled[0].File != activity || unfilled[0].Line == 0 ||
		!strings.Contains(unfilled[0].Fix, "Run tenjin setup") {
		t.Fatalf("unfilled = %+v", unfilled)
	}
	if len(keys.asked) != 0 {
		t.Errorf("the placeholder was sent to Tenjin as a key: %q", keys.asked)
	}
}

// An iOS launch can only be checked from the device log when debug builds
// turn the SDK's logs on, and release builds must not.
func TestIOSDebugLogs(t *testing.T) {
	base := testproject.IOSNative()
	const delegate = "Sample/AppDelegate.swift"
	guarded := "        #if DEBUG\n        TenjinSDK.debugLogs()\n        #endif\n"
	for _, c := range []struct {
		name, replace string
		severity      verify.Severity
	}{
		{"debug only", guarded, verify.Pass},
		{"absent", "", verify.Info},
		{"in release builds too", "        TenjinSDK.debugLogs()\n", verify.Warning},
		{"only outside debug", "        #if !DEBUG\n        TenjinSDK.debugLogs()\n        #endif\n", verify.Warning},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := run(t, base.With(testproject.Files{delegate: strings.Replace(base[delegate], guarded, c.replace, 1)}), verify.Options{})
			f := find(r, "ios.debug_logs", project.IOS)
			if f == nil || f.Severity != c.severity {
				t.Fatalf("ios.debug_logs = %+v", f)
			}
			if c.severity == verify.Info && !strings.Contains(f.Fix, "#if DEBUG") {
				t.Errorf("fix = %q", f.Fix)
			}
		})
	}
}

// A call's build is read from what its #if means, not from the names in it,
// and a condition that cannot be read is said to be unread, never passed.
func TestIOSDebugLogsConditions(t *testing.T) {
	base := testproject.IOSNative()
	const delegate = "Sample/AppDelegate.swift"
	guarded := "        #if DEBUG\n        TenjinSDK.debugLogs()\n        #endif\n"
	objc := func(directive, alternative string) string {
		body := "#import <TenjinSDK/TenjinSDK.h>\n\nvoid SampleLogs(void) {\n" + directive + "\n    NSLog(@\"first\");\n"
		if alternative != "" {
			body += alternative + "\n"
		}
		return body + "    [TenjinSDK debugLogs];\n#endif\n}\n"
	}
	for _, c := range []struct {
		name, swift, objc string
		severity          verify.Severity
	}{
		{"Objective-C, not debug", "", objc("#ifndef DEBUG", ""), verify.Warning},
		{"Objective-C, NDEBUG", "", objc("#ifdef NDEBUG", ""), verify.Warning},
		{"Objective-C, a symbol that only starts with DEBUG", "", objc("#if DEBUG_MENU_DISABLED", ""), verify.Warning},
		{"Objective-C, debug", "", objc("#ifdef DEBUG", ""), verify.Pass},
		{"Objective-C, debug and not QA", "", objc("#if defined(DEBUG) && !defined(QA)", ""), verify.Pass},
		{"Objective-C, the else of not debug", "", objc("#if !defined(DEBUG)", "#else"), verify.Pass},
		{"Objective-C, the elif after debug", "", objc("#ifdef DEBUG", "#elif defined(STAGING)"), verify.Warning},
		{"Objective-C, a comparison", "", objc("#if DEBUG == 0", ""), verify.Info},
		// Xcode does not define NDEBUG in release builds by default.
		{"Objective-C, not NDEBUG", "", objc("#ifndef NDEBUG", ""), verify.Info},
		{"Swift, debug and not QA", "        #if DEBUG && !QA\n        TenjinSDK.debugLogs()\n        #endif\n", "", verify.Pass},
		{"Swift, the else of not debug", "        #if !DEBUG\n        print(\"release\")\n        #else\n        TenjinSDK.debugLogs()\n        #endif\n", "", verify.Pass},
		{"Swift, debug on iOS", "        #if os(iOS)\n        #if DEBUG\n        TenjinSDK.debugLogs()\n        #endif\n        #endif\n", "", verify.Pass},
		{"Swift, debug or a platform", "        #if DEBUG || os(iOS)\n        TenjinSDK.debugLogs()\n        #endif\n", "", verify.Warning},
		{"Swift, in a string", "        print(\"TenjinSDK.debugLogs() is off\")\n", "", verify.Info},
	} {
		t.Run(c.name, func(t *testing.T) {
			files := base.With(testproject.Files{delegate: strings.Replace(base[delegate], guarded, c.swift, 1)})
			if c.objc != "" {
				files = files.With(testproject.Files{"Sample/SampleLogs.m": c.objc})
			}
			f := find(run(t, files, verify.Options{}), "ios.debug_logs", project.IOS)
			if f == nil || f.Severity != c.severity {
				t.Fatalf("ios.debug_logs = %+v", f)
			}
			switch {
			case c.objc != "" && (f.File != "Sample/SampleLogs.m" || f.Line != strings.Count(c.objc, "\n")-2):
				t.Errorf("found at %s:%d", f.File, f.Line)
			case c.objc != "" && c.severity == verify.Info && (!strings.Contains(f.Title, "could not be told") || !strings.Contains(f.Fix, "#ifdef DEBUG")):
				t.Errorf("finding = %+v", f)
			case c.objc == "" && c.severity == verify.Info && !strings.Contains(f.Title, "do not turn on"):
				t.Errorf("finding = %+v", f)
			}
		})
	}
}

// From 2.0.0 the Android SDK's AAR carries its keep rules, so shrinking needs
// none in the app.
func TestTheAndroidSDKBringsItsOwnKeepRules(t *testing.T) {
	base := testproject.AndroidNative()
	const build = "app/build.gradle.kts"
	r := run(t, base.With(testproject.Files{build: strings.Replace(base[build], "isMinifyEnabled = false", "isMinifyEnabled = true", 1)}), verify.Options{})
	f := expect(t, r, "android.proguard", project.Android, verify.Pass)
	if !strings.Contains(f.Title, "own keep rules") {
		t.Errorf("title = %q", f.Title)
	}
}

// A newer release from the registries is named only within the major version
// the checks were built for.
func TestNewerReleasesCountWithinTheKnownMajorVersion(t *testing.T) {
	base := testproject.AndroidNative()
	const build = "app/build.gradle.kts"
	missing := base.With(testproject.Files{build: strings.Replace(base[build], `implementation("com.tenjin:android-sdk:2.0.0")`, "", 1)})
	for _, c := range []struct{ live, want string }{
		{"2.0.3", "2.0.3"},
		{"3.0.0", "2.0.0"},
		{"1.24.0", "2.0.0"},
		{"2.1.0-rc.1", "2.0.0"},
	} {
		r := run(t, missing, verify.Options{Latest: map[string]string{"com.tenjin:android-sdk": c.live}})
		f := expect(t, r, "sdk.dependency", project.Android, verify.Error)
		if !strings.Contains(f.Fix, "com.tenjin:android-sdk:"+c.want+`")`) {
			t.Errorf("live %s: fix = %q", c.live, f.Fix)
		}
	}
	r := run(t, base, verify.Options{Latest: map[string]string{"com.tenjin:android-sdk": "2.0.3"}})
	if f := expect(t, r, "sdk.version", project.Android, verify.Warning); !strings.Contains(f.Fix, "to 2.0.3") {
		t.Errorf("fix = %q", f.Fix)
	}
}

// "image/*" in a string is not the start of a comment that would hide the
// SDK calls after it.
func TestAGlobInAStringHidesNoCode(t *testing.T) {
	base := testproject.AndroidNative()
	const activity = "app/src/main/java/com/example/sample/MainActivity.kt"
	source := strings.Replace(base[activity], "class MainActivity", "private val pickerType = \"image/*\"\n\nclass MainActivity", 1) + "\n/** Opens the picker. */\n"
	r := run(t, base.With(testproject.Files{activity: source}), verify.Options{})
	expect(t, r, "sdk.init", project.Android, verify.Pass)
	expect(t, r, "sdk.connect", project.Android, verify.Pass)
}

// What Google Mobile Ads and play-services-ads-identifier bring is counted:
// the build merges their manifests and dependencies into the app's.
func TestLibrariesBringPermissionsAndPlayServices(t *testing.T) {
	base := testproject.AndroidNative()
	const (
		build    = "app/build.gradle.kts"
		manifest = "app/src/main/AndroidManifest.xml"
	)
	deps := strings.Replace(base[build], `implementation("com.google.android.gms:play-services-ads-identifier:18.1.0")`, `implementation("com.google.android.gms:play-services-ads:23.4.0")`, 1)
	deps = strings.Replace(deps, `implementation("com.google.android.gms:play-services-appset:16.1.0")`, "", 1)
	bare := base[manifest]
	for _, perm := range []string{"android.permission.INTERNET", "android.permission.ACCESS_NETWORK_STATE", "com.google.android.gms.permission.AD_ID"} {
		bare = strings.Replace(bare, `<uses-permission android:name="`+perm+`" />`, "", 1)
	}
	r := run(t, base.With(testproject.Files{build: deps, manifest: bare}), verify.Options{})
	for _, id := range []string{"android.permission.internet", "android.permission.network_state", "android.permission.ad_id", "android.play_services.ads_identifier", "android.play_services.appset"} {
		if f := expect(t, r, id, project.Android, verify.Pass); !strings.Contains(f.Title, "comes with") {
			t.Errorf("%s: %q", id, f.Title)
		}
	}
}

// Calls count on the SDK's receiver under whatever name the project gives
// it, and not on an unrelated object's method of the same name.
func TestReceiversUnderOtherNames(t *testing.T) {
	android := testproject.AndroidNative()
	const activity = "app/src/main/java/com/example/sample/MainActivity.kt"
	chained := strings.Replace(android[activity], "tenjin.connect()", "", 1)
	chained = strings.Replace(chained, `val tenjin = TenjinSDK.getInstance(this, "`+testproject.Key+`")`, `TenjinSDK.getInstance(this, "`+testproject.Key+`")
            .connect()`, 1)
	cases := []struct {
		name  string
		files testproject.Files
		id    string
		want  verify.Severity
	}{
		{"chained on getInstance", android.With(testproject.Files{activity: chained}), "sdk.connect", verify.Pass},
		{"another object's connect", android.With(testproject.Files{activity: strings.Replace(android[activity], "tenjin.connect()", "mediaBrowser.connect()", 1)}), "sdk.connect", verify.Error},
		{"from a function that returns the SDK", android.With(testproject.Files{activity: `package com.example.sample

import android.app.Activity
import android.content.Context
import com.tenjin.android.TenjinSDK

class MainActivity : Activity() {
    override fun onResume() {
        super.onResume()
        val sdk = sdk(this)
        sdk.connect()
    }

    private fun sdk(context: Context): TenjinSDK =
        TenjinSDK.getInstance(context, "` + testproject.Key + `")
}
`}), "sdk.connect", verify.Pass},
		{"only in a unit test", android.With(testproject.Files{
			activity: strings.Replace(android[activity], "tenjin.connect()", "", 1),
			"app/src/test/java/com/example/sample/TenjinTest.kt": "import com.tenjin.android.TenjinSDK\nfun t() { TenjinSDK.getInstance(null, \"K\").connect() }\n",
		}), "sdk.connect", verify.Error},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			expect(t, run(t, c.files, verify.Options{}), c.id, project.Android, c.want)
		})
	}
	// The chained form still has its key read.
	keys := &fakeKeys{answers: map[string]track.KeyStatus{testproject.Key + "|com.example.sample|android": track.KeyValid}}
	if r := run(t, android.With(testproject.Files{activity: chained}), verify.Options{Keys: keys}); find(r, "key.valid", project.Android) == nil {
		t.Errorf("the key of a chained call was not checked: %s", ids(r))
	}

	flutter := testproject.Flutter()
	const main = "lib/main.dart"
	aliased := strings.NewReplacer("TenjinSDK.instance.initialize(", "tenjin.initialize(", "TenjinSDK.instance.connect(", "tenjin.connect(", "TenjinSDK.instance.requestTrackingAuthorization(", "tenjin.requestTrackingAuthorization(").Replace(flutter[main])
	aliased = strings.Replace(aliased, "  tenjin.initialize(", "  final tenjin = TenjinSDK.instance;\n  tenjin.initialize(", 1)
	r := run(t, flutter.With(testproject.Files{main: aliased}), verify.Options{})
	// A wrapper's calls are in its shared code, on no platform of their own.
	expect(t, r, "sdk.init", "", verify.Pass)
	expect(t, r, "sdk.connect", "", verify.Pass)
}

// A call written in a string is text the app prints, not a call; the key
// in a string is still read.
func TestCallsInsideStringsAreNotCalls(t *testing.T) {
	android := testproject.AndroidNative()
	const activity = "app/src/main/java/com/example/sample/MainActivity.kt"
	ios := testproject.IOSNative()
	const delegate = "Sample/AppDelegate.swift"
	flutter := testproject.Flutter()
	unity := testproject.Unity()
	// The Flutter project's Android folder gives the JavaScript wrappers an
	// app to check their key for.
	androidFolder := testproject.Files{
		"android/app/build.gradle.kts":             flutter["android/app/build.gradle.kts"],
		"android/app/src/main/AndroidManifest.xml": flutter["android/app/src/main/AndroidManifest.xml"],
	}
	java := "package com.example.sample;\nimport android.app.Activity;\nimport com.tenjin.android.TenjinSDK;\npublic class MainActivity extends Activity {\n  @Override protected void onResume() {\n    super.onResume();\n    TenjinSDK tenjin = TenjinSDK.getInstance(this, \"" + testproject.Key + "\");\n    System.out.println(\"tenjin.connect() has not been called yet\");\n  }\n}\n"
	objc := "#import <TenjinSDK/TenjinSDK.h>\n@implementation AppDelegate\n- (void)applicationDidBecomeActive:(UIApplication *)application {\n  [TenjinSDK initialize:@\"" + testproject.Key + "\"];\n  NSLog(@\"[TenjinSDK connect] comes later\");\n}\n@end\n"
	cases := []struct {
		name     string
		files    testproject.Files
		platform project.Platform
	}{
		{"Kotlin", android.With(testproject.Files{activity: strings.Replace(android[activity], "tenjin.connect()", `println("tenjin.connect() has not been called yet")`, 1)}), project.Android},
		{"Java", android.Without(activity).With(testproject.Files{"app/src/main/java/com/example/sample/MainActivity.java": java}), project.Android},
		{"Swift", ios.With(testproject.Files{delegate: strings.Replace(ios[delegate], "TenjinSDK.connect()", `print("TenjinSDK.connect() is next")`, 1)}), project.IOS},
		{"Objective-C", ios.With(testproject.Files{delegate: "", "Sample/AppDelegate.m": objc}), project.IOS},
		{"Dart", flutter.With(testproject.Files{"lib/main.dart": strings.Replace(flutter["lib/main.dart"], "TenjinSDK.instance.connect();", `print('TenjinSDK.instance.connect() is next');`, 1)}), ""},
		{"React Native", androidFolder.With(testproject.Files{
			"package.json": `{"dependencies": {"react-native": "0.79.2", "react-native-tenjin": "1.7.0"}}`,
			"App.tsx":      "import Tenjin from 'react-native-tenjin';\nTenjin.initialize('" + testproject.Key + "');\nconsole.log(`Tenjin.connect() is next`);\n",
		}), ""},
		{"Capacitor", androidFolder.With(testproject.Files{
			"package.json":          `{"dependencies": {"@capacitor/core": "6.0.0", "ionic-capacitor-tenjin": "2.2.0"}}`,
			"capacitor.config.json": `{"appId": "com.example.sample"}`,
			"src/app.ts":            "import { Tenjin } from 'ionic-capacitor-tenjin';\nawait Tenjin.initialize({ sdkKey: '" + testproject.Key + "' });\nconsole.log(\"Tenjin.connect() is next\");\n",
		}), ""},
		{"C#", unity.With(testproject.Files{unityScript: strings.ReplaceAll(unity[unityScript], "instance.Connect();", `Debug.Log("instance.Connect() is next");`)}), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			keys := &fakeKeys{}
			r := run(t, c.files, verify.Options{Keys: keys})
			expect(t, r, "sdk.init", c.platform, verify.Pass)
			expect(t, r, "sdk.connect", c.platform, verify.Error)
			if len(keys.asked) == 0 || !strings.HasPrefix(keys.asked[0], testproject.Key+"|") {
				t.Errorf("the key was not read: asked %v", keys.asked)
			}
		})
	}
	// An initializer named in a string initializes nothing.
	files := android.With(testproject.Files{activity: strings.Replace(android[activity], `val tenjin = TenjinSDK.getInstance(this, "`+testproject.Key+`")`, `val tenjin = Log.d("x", "TenjinSDK.getInstance(this, key)")`, 1)})
	expect(t, run(t, files, verify.Options{}), "sdk.init", project.Android, verify.Error)
}

// React Native's default export can take any name.
func TestReactNativeDefaultImportUnderAnyName(t *testing.T) {
	r := run(t, testproject.Files{
		"package.json": `{"dependencies": {"react-native": "0.79.2", "react-native-tenjin": "1.7.0"}}`,
		"App.tsx":      "import TenjinSDK from 'react-native-tenjin';\nTenjinSDK.initialize('" + testproject.Key + "');\nTenjinSDK.connect();\n",
	}, verify.Options{})
	expect(t, r, "sdk.init", "", verify.Pass)
	expect(t, r, "sdk.connect", "", verify.Pass)

	// A name that does not say Tenjin at all.
	r = run(t, testproject.Files{
		"package.json": `{"dependencies": {"react-native": "0.79.2", "react-native-tenjin": "1.7.0"}}`,
		"App.tsx":      "import sdk from 'react-native-tenjin';\nsdk.initialize('" + testproject.Key + "');\nsdk.connect();\n",
	}, verify.Options{})
	expect(t, r, "sdk.init", "", verify.Pass)
	expect(t, r, "sdk.connect", "", verify.Pass)
}

// A connect that only debug builds make does nothing for the app people
// install.
func TestAConnectInDebugBuildsOnlyIsAnError(t *testing.T) {
	ios := testproject.IOSNative()
	const delegate = "Sample/AppDelegate.swift"
	f := expect(t, run(t, ios.With(testproject.Files{delegate: strings.Replace(ios[delegate], "TenjinSDK.connect()", "#if DEBUG\n            TenjinSDK.connect()\n            #endif", 1)}), verify.Options{}), "sdk.connect", project.IOS, verify.Error)
	if !strings.Contains(f.Title, "only in debug builds") {
		t.Errorf("title = %q", f.Title)
	}
}

// A tracking request in a file of its own, which never names Tenjin, counts.
func TestATrackingRequestInItsOwnFile(t *testing.T) {
	ios := testproject.IOSNative()
	const delegate = "Sample/AppDelegate.swift"
	r := run(t, ios.With(testproject.Files{
		delegate:                          strings.Replace(ios[delegate], "ATTrackingManager.requestTrackingAuthorization", "TrackingPermission.request", 1),
		"Sample/TrackingPermission.swift": "import AppTrackingTransparency\nenum TrackingPermission {\n  static func request(completionHandler: @escaping (ATTrackingManager.AuthorizationStatus) -> Void) {\n    ATTrackingManager.requestTrackingAuthorization(completionHandler: completionHandler)\n  }\n}\n",
	}), verify.Options{})
	if f := expect(t, r, "ios.att_request", project.IOS, verify.Pass); f.File != "Sample/TrackingPermission.swift" {
		t.Errorf("found at %s", f.File)
	}
}

// sharedInstanceWithToken: still initialises and connects, and is
// deprecated.
func TestSharedInstanceWithTokenIsDeprecated(t *testing.T) {
	ios := testproject.IOSNative()
	const delegate = "Sample/AppDelegate.swift"
	objc := "#import <TenjinSDK/TenjinSDK.h>\n@implementation AppDelegate\n- (void)applicationDidBecomeActive:(UIApplication *)application {\n  [TenjinSDK sharedInstanceWithToken:@\"" + testproject.Key + "\"];\n}\n@end\n"
	r := run(t, ios.With(testproject.Files{delegate: "", "Sample/AppDelegate.m": objc}), verify.Options{})
	expect(t, r, "sdk.init", project.IOS, verify.Pass)
	expect(t, r, "sdk.connect", project.IOS, verify.Pass)
	expect(t, r, "sdk.init.deprecated", project.IOS, verify.Warning)
}

// An Expo project whose native folders are generated at build time, and
// whose IDs are in app.config.ts, says what was not checked.
func TestAnExpoProjectWithoutNativeFolders(t *testing.T) {
	files := testproject.Files{
		"package.json":  `{"dependencies": {"expo": "52.0.0", "react-native": "0.76.0", "react-native-tenjin": "1.7.0"}}`,
		"app.config.ts": "export default { expo: { name: 'Sample', ios: { bundleIdentifier: 'com.example.sample' }, android: { package: 'com.example.sample' } } };\n",
		"App.tsx":       "import Tenjin from 'react-native-tenjin';\nTenjin.initialize('" + testproject.Key + "');\nTenjin.connect();\n",
	}
	r := run(t, files, verify.Options{})
	expect(t, r, "native.unchecked", "", verify.Info)
	if len(r.Project.Targets) != 2 {
		t.Errorf("targets = %+v", r.Project.Targets)
	}
}

// Flutter shrinks release builds unless told not to, so its keep rules are
// checked even when the build file says nothing about shrinking.
func TestFlutterReleaseBuildsAreShrunk(t *testing.T) {
	flutter := testproject.Flutter()
	without := flutter.With(testproject.Files{"android/app/proguard-rules.pro": ""})
	expect(t, run(t, without, verify.Options{}), "android.proguard", project.Android, verify.Warning)

	const build = "android/app/build.gradle.kts"
	off := without.With(testproject.Files{build: strings.Replace(flutter[build], "android {", "android {\n    buildTypes { release { isMinifyEnabled = false } }", 1)})
	if f := find(run(t, off, verify.Options{}), "android.proguard", project.Android); f != nil {
		t.Errorf("checked with shrinking off: %+v", f)
	}
}

// Tenjin used from a library module the app depends on is the app's
// integration: the build merges the module's dependencies, code and
// manifest into the app.
func TestTenjinInALibraryModule(t *testing.T) {
	base := testproject.AndroidNative()
	const (
		build    = "app/build.gradle.kts"
		activity = "app/src/main/java/com/example/sample/MainActivity.kt"
	)
	files := testproject.Files{}
	for name, content := range base {
		files[name] = content
	}
	files[build] = strings.Replace(base[build], `implementation("com.tenjin:android-sdk:2.0.0")`, `implementation(project(":ads"))`, 1)
	files["ads/build.gradle.kts"] = "plugins { id(\"com.android.library\") }\ndependencies {\n    implementation(\"com.tenjin:android-sdk:2.0.0\")\n}\n"
	files["ads/src/main/java/com/example/ads/Ads.kt"] = base[activity]
	files[activity] = "package com.example.sample\nclass MainActivity : android.app.Activity()\n"
	r := run(t, files, verify.Options{})
	for _, id := range []string{"sdk.dependency", "sdk.init", "sdk.connect"} {
		if f := expect(t, r, id, project.Android, verify.Pass); !strings.HasPrefix(f.File, "ads/") {
			t.Errorf("%s at %s", id, f.File)
		}
	}
}

// Checks that came from public projects using the SDK.
func TestShapesFromRealProjects(t *testing.T) {
	android := testproject.AndroidNative()
	const activity = "app/src/main/java/com/example/sample/MainActivity.kt"

	// A Capacitor app with the native Android SDK added straight to android/.
	capacitor := testproject.Files{
		"package.json":          `{"dependencies": {"@capacitor/core": "6.0.0", "@capacitor/android": "6.0.0"}}`,
		"capacitor.config.json": `{"appId": "com.example.sample"}`,
	}
	for name, content := range android {
		capacitor["android/"+name] = content
	}
	r := run(t, capacitor, verify.Options{})
	expect(t, r, "sdk.native_in_wrapper", "", verify.Info)
	expect(t, r, "sdk.dependency", project.Android, verify.Pass)
	expect(t, r, "sdk.connect", project.Android, verify.Pass)

	// The community tenjin_sdk Flutter plugin is named as such.
	flutter := testproject.Flutter()
	community := flutter.With(testproject.Files{"pubspec.yaml": strings.Replace(flutter["pubspec.yaml"], "tenjin_plugin:", "tenjin_sdk:", 1)})
	if f := expect(t, run(t, community, verify.Options{}), "sdk.dependency", "", verify.Warning); !strings.Contains(f.Title, "community plugin") {
		t.Errorf("title = %q", f.Title)
	}

	// Kotlin's by lazy keeps the receiver too.
	lazy := strings.Replace(android[activity], `val tenjin = TenjinSDK.getInstance(this, "`+testproject.Key+`")`, `val tenjin by lazy { TenjinSDK.getInstance(this, "`+testproject.Key+`") }`, 1)
	expect(t, run(t, android.With(testproject.Files{activity: lazy}), verify.Options{}), "sdk.connect", project.Android, verify.Pass)
}

// A Flutter, React Native or Capacitor app can only turn the iOS SDK's logs
// on in its native code, and that must stay out of release builds too.
func TestDebugLogsInAWrappersNativeCode(t *testing.T) {
	const delegate = "ios/Runner/AppDelegate.swift"
	app := func(body string) string {
		return "import Flutter\nimport TenjinSDK\n\n@main\nclass AppDelegate: FlutterAppDelegate {\n    override func application() -> Bool {\n" + body + "        return true\n    }\n}\n"
	}
	for _, c := range []struct {
		name, body string
		severity   verify.Severity
		line       int
	}{
		{"in release builds too", "        TenjinSDK.debugLogs()\n", verify.Warning, 7},
		{"debug only", "        #if DEBUG\n        TenjinSDK.debugLogs()\n        #endif\n", verify.Pass, 8},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := run(t, testproject.Flutter().With(testproject.Files{delegate: app(c.body)}), verify.Options{})
			if f := find(r, "ios.debug_logs", project.IOS); f == nil || f.Severity != c.severity || f.File != delegate || f.Line != c.line {
				t.Fatalf("ios.debug_logs = %+v", f)
			}
		})
	}
	// The Dart code cannot turn them on, so their absence is not a finding.
	if r := run(t, testproject.Flutter().With(testproject.Files{delegate: app("")}), verify.Options{}); find(r, "ios.debug_logs", project.IOS) != nil {
		t.Errorf("ios.debug_logs = %+v", find(r, "ios.debug_logs", project.IOS))
	}
}

// An SDK taken from a folder has no version to update to: the fix says how
// to upgrade it, as sdk upgrade does.
func TestAnOldSDKFromAFolderSaysHowToUpgradeIt(t *testing.T) {
	base := testproject.Flutter()
	files := base.With(testproject.Files{
		"pubspec.yaml": strings.Replace(base["pubspec.yaml"], "  tenjin_plugin: ^1.5.0\n", "  tenjin_plugin:\n    path: ../tenjin_plugin\n", 1),
		"pubspec.lock": strings.Replace(strings.Replace(base["pubspec.lock"], `version: "1.5.0"`, `version: "1.4.0"`, 1), "source: hosted", "source: path", 1),
	})
	f := expect(t, run(t, files, verify.Options{}), "sdk.version", "", verify.Warning)
	if strings.Contains(f.Fix, "Update tenjin_plugin to") || !strings.Contains(f.Fix, "path:") {
		t.Errorf("fix = %q", f.Fix)
	}
}

// With a build per platform, a key given without its build would be checked
// against every app: the fix names the build.
func TestAnUncheckedKeyNamesItsBuild(t *testing.T) {
	base := testproject.Flutter()
	files := base.With(testproject.Files{"lib/main.dart": strings.Replace(base["lib/main.dart"], "'"+testproject.Key+"'", "Env.tenjinKey", 1)})
	r := run(t, files, verify.Options{Keys: &fakeKeys{answers: map[string]track.KeyStatus{}}})
	for _, platform := range []project.Platform{project.Android, project.IOS} {
		f := expect(t, r, "key.unchecked", platform, verify.Info)
		if want := "--sdk-key " + string(platform) + "=<key>"; !strings.Contains(f.Fix, want) {
			t.Errorf("%s: fix = %q, want %q", platform, f.Fix, want)
		}
	}
}

// An empty key is said to be empty.
func TestAnEmptyKeyIsSaidToBe(t *testing.T) {
	base := testproject.Flutter()
	files := base.With(testproject.Files{"lib/main.dart": strings.Replace(base["lib/main.dart"], "'"+testproject.Key+"'", "''", 1)})
	r := run(t, files, verify.Options{})
	var found bool
	for _, f := range r.Findings {
		if f.ID == "sdk.key.placeholder" {
			found = true
			if f.Title != "The SDK key is empty" || strings.Contains(f.Fix, `Replace ""`) {
				t.Errorf("finding = %+v", f)
			}
		}
	}
	if !found {
		t.Fatalf("no finding for the empty key: %+v", r.Findings)
	}
}
