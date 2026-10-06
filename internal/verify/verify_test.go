package verify_test

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/tenjin/tenjin-cli/internal/track"
	"github.com/tenjin/tenjin-cli/internal/project"
	"github.com/tenjin/tenjin-cli/internal/testproject"
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
			base.With(testproject.Files{build: strings.Replace(base[build], `implementation("com.tenjin:android-sdk:1.24.0")`, "", 1)}),
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
		{"ad id not declared",
			base.With(testproject.Files{manifest: strings.Replace(base[manifest], `<uses-permission android:name="com.google.android.gms.permission.AD_ID" />`, "", 1)}),
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
			base.With(testproject.Files{build: strings.Replace(base[build], "isMinifyEnabled = false", "isMinifyEnabled = true", 1)}),
			"android.proguard", verify.Warning},
		{"connect in the Application class",
			base.With(testproject.Files{activity: strings.Replace(base[activity], "class MainActivity : Activity()", "class App : Application()", 1)}),
			"android.connect.location", verify.Warning},
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
	expect(t, r, "framework.unchecked", "", verify.Info)
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
	old := base.With(testproject.Files{build: strings.Replace(base[build], "android-sdk:1.24.0", "android-sdk:1.16.5", 1)})
	f := expect(t, run(t, old, verify.Options{}), "sdk.version", project.Android, verify.Warning)
	if !strings.Contains(f.Title, "1.16.5") || !strings.Contains(f.Fix, "com.tenjin:android-sdk to 1.24.0") || f.File != build {
		t.Errorf("finding = %+v", f)
	}

	// Current, and newer than this CLI knows about: nothing to say.
	for _, version := range []string{"1.24.0", "1.30.2", "2.0.0"} {
		files := base.With(testproject.Files{build: strings.Replace(base[build], "android-sdk:1.24.0", "android-sdk:"+version, 1)})
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
