package verify_test

import (
	"context"
	"strings"
	"testing"

	"github.com/tenjin/tenjin-cli/internal/devicelog"
	"github.com/tenjin/tenjin-cli/internal/project"
	"github.com/tenjin/tenjin-cli/internal/testproject"
	"github.com/tenjin/tenjin-cli/internal/track"
	"github.com/tenjin/tenjin-cli/internal/verify"
)

// godotApps ties each of the fixture's keys to its own app, and refuses it
// for any other, as Tenjin does.
var godotApps = keyApps{
	testproject.Key:    "com.example.sample|android",
	testproject.IOSKey: "com.example.sample|ios",
}

// keyApps ties an SDK key to one app, "bundle|platform"; Tenjin refuses it
// for every other app.
type keyApps map[string]string

func (k keyApps) CheckKey(_ context.Context, key, bundleID, platform string) (track.KeyCheck, error) {
	if k[key] == bundleID+"|"+platform {
		return track.KeyCheck{Status: track.KeyValid}, nil
	}
	return track.KeyCheck{Status: track.KeyRejected, Detail: "the key is not this app's"}, nil
}

func TestGodotCorrectIntegrationPasses(t *testing.T) {
	r := run(t, testproject.Godot(), verify.Options{Keys: godotApps})
	if r.Summary.Errors != 0 || r.Summary.Warnings != 0 {
		t.Fatalf("summary = %+v, findings = %+v", r.Summary, r.Findings)
	}
	expect(t, r, "sdk.dependency", "", verify.Pass)
	expect(t, r, "godot.plugin", "", verify.Pass)
	expect(t, r, "sdk.init", "", verify.Pass)
	expect(t, r, "godot.gradle_build", project.Android, verify.Pass)
	expect(t, r, "ios.plist.skan_endpoint", project.IOS, verify.Pass)
	expect(t, r, "ios.att_request", project.IOS, verify.Pass)
	// Each platform's key is read from the constant OS.get_name() chooses
	// for it, and checked against that platform's app only.
	for platform, line := range map[project.Platform]int{project.Android: 3, project.IOS: 4} {
		f := expect(t, r, "key.valid", platform, verify.Pass)
		if f.File != testproject.GodotMain || f.Line != line {
			t.Errorf("%s key found at %s:%d", platform, f.File, f.Line)
		}
	}
	if f := find(r, "godot.shared_key", ""); f != nil {
		t.Errorf("shared key reported: %+v", f)
	}
	if f := find(r, "godot.resume", ""); f != nil {
		t.Errorf("resume reported: %+v", f)
	}
	if f := find(r, "godot.store_type", project.Android); f != nil {
		t.Errorf("store type reported: %+v", f)
	}
}

func TestGodotAddonNotInstalled(t *testing.T) {
	files := testproject.Godot().Without("addons/tenjin/plugin.cfg", "addons/tenjin/tenjin.gd")
	f := expect(t, run(t, files, verify.Options{}), "sdk.dependency", "", verify.Error)
	if !strings.Contains(f.Fix, "tenjin-godot-sdk-1.0.0.zip") || !strings.Contains(f.Fix, "Plugins") {
		t.Errorf("fix = %q", f.Fix)
	}
}

func TestGodotPluginNotEnabled(t *testing.T) {
	base := testproject.Godot()
	settings := strings.Replace(base["project.godot"], `enabled=PackedStringArray("res://addons/tenjin/plugin.cfg")`, `enabled=PackedStringArray()`, 1)
	f := expect(t, run(t, base.With(testproject.Files{"project.godot": settings}), verify.Options{}), "godot.plugin", "", verify.Error)
	if f.File != "project.godot" || !strings.Contains(f.Fix, "Project Settings > Plugins") {
		t.Errorf("finding = %+v", f)
	}
}

func TestGodotAddonCodeIsNotTheAppsCode(t *testing.T) {
	// The addon's script has initialize() and connect_sdk() too. With the
	// game's own script gone, that is not an integration.
	r := run(t, testproject.Godot().Without(testproject.GodotMain), verify.Options{})
	expect(t, r, "sdk.init", "", verify.Error)
}

func TestGodotCommentedCallsDoNotCount(t *testing.T) {
	main := "extends Node\n\nfunc _ready() -> void:\n\t# Tenjin.initialize(\"KEY\")\n\t# Tenjin.connect_sdk()\n\tpass\n"
	r := run(t, testproject.Godot().With(testproject.Files{testproject.GodotMain: main}), verify.Options{})
	f := expect(t, r, "sdk.init", "", verify.Error)
	if !strings.Contains(f.Fix, "Tenjin.initialize") {
		t.Errorf("fix = %q", f.Fix)
	}
}

func TestGodotWithoutAGradleBuild(t *testing.T) {
	base := testproject.Godot()
	presets := strings.Replace(base["export_presets.cfg"], "gradle_build/use_gradle_build=true", "gradle_build/use_gradle_build=false", 1)
	f := expect(t, run(t, base.With(testproject.Files{"export_presets.cfg": presets}), verify.Options{}), "godot.gradle_build", project.Android, verify.Error)
	if f.File != "export_presets.cfg" || f.Line != 1 || !strings.Contains(f.Title, "Android") || !strings.Contains(f.Fix, "Use Gradle Build") {
		t.Errorf("finding = %+v", f)
	}
}

func TestGodotWithoutTheSKANEndpoint(t *testing.T) {
	base := testproject.Godot()
	presets := strings.Replace(base["export_presets.cfg"], `application/additional_plist_content="<key>NSAdvertisingAttributionReportEndpoint</key><string>https://tenjin-skan.com</string>"`, `application/additional_plist_content=""`, 1)
	f := expect(t, run(t, base.With(testproject.Files{"export_presets.cfg": presets}), verify.Options{}), "ios.plist.skan_endpoint", project.IOS, verify.Warning)
	if !strings.Contains(f.Fix, "Additional Plist Content") {
		t.Errorf("fix = %q", f.Fix)
	}
}

func TestGodotWithoutResumeStoreOrTrackingPrompt(t *testing.T) {
	main := "extends Node\n\nfunc _ready() -> void:\n\tTenjin.initialize(\"" + testproject.Key + "\")\n\tTenjin.connect_sdk()\n"
	r := run(t, testproject.Godot().With(testproject.Files{testproject.GodotMain: main}), verify.Options{Keys: godotApps})
	expect(t, r, "sdk.init", "", verify.Pass)
	expect(t, r, "godot.resume", "", verify.Info)
	expect(t, r, "godot.store_type", project.Android, verify.Info)
	expect(t, r, "ios.att_request", project.IOS, verify.Warning)
	expect(t, r, "key.valid", project.Android, verify.Pass)
}

func TestGodotFeaturesAreReadFromGDScript(t *testing.T) {
	base := testproject.Godot()
	main := strings.Replace(base[testproject.GodotMain], "\tTenjin.set_app_store", "\tTenjin.set_customer_user_id(\"player-1\")\n\tTenjin.event_with_name(\"level_up\")\n\t# Tenjin.transaction(\"gems\", \"USD\", 1, 0.99)\n\tTenjin.set_app_store", 1)
	r := run(t, base.With(testproject.Files{testproject.GodotMain: main}), verify.Options{Features: []string{project.FeatureUserID, project.FeatureCustomEvents, project.FeaturePurchases}})
	expect(t, r, "feature."+project.FeatureUserID, "", verify.Pass)
	expect(t, r, "feature."+project.FeatureCustomEvents, "", verify.Pass)
	// A call in a comment sends nothing.
	f := expect(t, r, "feature."+project.FeaturePurchases, "", verify.Warning)
	if !strings.Contains(f.Fix, "tenjin-godot-sdk") {
		t.Errorf("fix = %q", f.Fix)
	}
}

func TestGodotUpgradeIsByHand(t *testing.T) {
	files := testproject.Godot()
	files["addons/tenjin/plugin.cfg"] = strings.Replace(files["addons/tenjin/plugin.cfg"], "1.0.0", "0.9.0", 1)
	p, err := project.Detect(testproject.Write(t, files))
	if err != nil {
		t.Fatal(err)
	}
	plan := verify.Upgrades(p, nil)
	if len(plan.Upgrades) != 0 || len(plan.Unchecked) != 1 || !strings.Contains(plan.Unchecked[0].Reason, "replace addons/tenjin") {
		t.Errorf("plan = %+v", plan)
	}
	expect(t, verify.Run(t.Context(), p, verify.Options{}), "sdk.version", "", verify.Warning)
}

// The Godot SDK cannot turn on the iOS SDK's request logs, so the advice
// to call TenjinSDK.debugLogs() is no use to a Godot app.
func TestGodotIOSLaunchWithoutRequestLogs(t *testing.T) {
	ios := func(text string) *verify.Report {
		log, err := devicelog.Parse(strings.NewReader(text))
		if err != nil {
			t.Fatal(err)
		}
		return run(t, testproject.Godot(), verify.Options{Device: &verify.DeviceInput{Log: log, Platform: project.IOS}})
	}
	for id, f := range map[string]*verify.Finding{
		"device.no_debug_logs": expect(t, ios("[Tenjin] - LOG: Sending request - https://track.tenjin.com/v0/event\n[Tenjin] - LOG: Got http response 200\n"), "device.no_debug_logs", project.IOS, verify.Info),
		"device.no_sdk_output": expect(t, ios("nothing from Tenjin\n"), "device.no_sdk_output", project.IOS, verify.Warning),
		"device.connect":       expect(t, ios("[Tenjin] - LOG: Sending request - https://track.tenjin.com/v0/event\n[Tenjin] - LOG: Got http response 401\n"), "device.connect", project.IOS, verify.Error),
	} {
		if strings.Contains(f.Fix, "debugLogs") {
			t.Errorf("%s fix = %q", id, f.Fix)
		}
	}
}

// One constant for both platforms, as in the Godot SDK's sample: Tenjin
// accepts it for one app only, and the refusal says why.
func TestGodotOneKeyForBothPlatforms(t *testing.T) {
	base := testproject.Godot()
	main := strings.Replace(base[testproject.GodotMain], "const ANDROID_SDK_KEY := \"", "const SDK_KEY := \"", 1)
	main = strings.Replace(main, "const IOS_SDK_KEY := \""+testproject.IOSKey+"\"\n", "", 1)
	main = strings.Replace(main, "Tenjin.initialize(IOS_SDK_KEY if OS.get_name() == \"iOS\" else ANDROID_SDK_KEY)", "Tenjin.initialize(SDK_KEY)", 1)
	files := base.With(testproject.Files{testproject.GodotMain: main})

	r := run(t, files, verify.Options{Keys: godotApps})
	expect(t, r, "key.valid", project.Android, verify.Pass)
	f := expect(t, r, "key.rejected", project.IOS, verify.Error)
	if !strings.Contains(f.Detail, "SDK_KEY") || !strings.Contains(f.Detail, "both Android and iOS") || !strings.Contains(f.Fix, `OS.get_name() == "iOS"`) {
		t.Errorf("refusal = %+v", f)
	}
	if f := find(r, "godot.shared_key", ""); f != nil {
		t.Errorf("said twice: %+v", f)
	}

	// Offline, nothing is refused, and it is still said.
	f = expect(t, run(t, files, verify.Options{}), "godot.shared_key", "", verify.Info)
	if !strings.Contains(f.Title, "SDK_KEY") || f.File != testproject.GodotMain || f.Line != 3 || !strings.Contains(f.Fix, "IOS_SDK_KEY") {
		t.Errorf("finding = %+v", f)
	}
}

// The key chosen by platform may go through a variable first, with the
// platforms the other way round.
func TestGodotKeyChosenByPlatformThroughAVariable(t *testing.T) {
	base := testproject.Godot()
	main := strings.Replace(base[testproject.GodotMain],
		"\tTenjin.initialize(IOS_SDK_KEY if OS.get_name() == \"iOS\" else ANDROID_SDK_KEY)",
		"\tvar key: String = ANDROID_SDK_KEY if OS.get_name() != \"iOS\" else IOS_SDK_KEY\n\tTenjin.initialize(key)", 1)
	r := run(t, base.With(testproject.Files{testproject.GodotMain: main}), verify.Options{Keys: godotApps})
	expect(t, r, "key.valid", project.Android, verify.Pass)
	expect(t, r, "key.valid", project.IOS, verify.Pass)
}

// Godot writes text with line breaks over several lines; the endpoint is
// found across them.
func TestGodotSKANEndpointOverSeveralLines(t *testing.T) {
	base := testproject.Godot()
	presets := strings.Replace(base["export_presets.cfg"],
		`<key>NSAdvertisingAttributionReportEndpoint</key><string>https://tenjin-skan.com</string>"`,
		"<key>NSAdvertisingAttributionReportEndpoint</key>\n<string>https://tenjin-skan.com</string>\"", 1)
	f := expect(t, run(t, base.With(testproject.Files{"export_presets.cfg": presets}), verify.Options{}), "ios.plist.skan_endpoint", project.IOS, verify.Pass)
	if f.Line != 21 {
		t.Errorf("line = %d", f.Line)
	}
}
