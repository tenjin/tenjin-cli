package verify_test

import (
	"context"
	"strings"
	"testing"

	"github.com/tenjin/tenjin-cli/internal/project"
	"github.com/tenjin/tenjin-cli/internal/testproject"
	"github.com/tenjin/tenjin-cli/internal/track"
	"github.com/tenjin/tenjin-cli/internal/verify"
)

const unityScript = "Assets/Scripts/TenjinBootstrap.cs"

func TestUnityCorrectIntegrationPasses(t *testing.T) {
	r := run(t, testproject.Unity(), verify.Options{Keys: acceptAll{}})
	if r.Summary.Errors != 0 || r.Summary.Warnings != 0 {
		t.Fatalf("summary = %+v, findings = %+v", r.Summary, r.Findings)
	}
	expect(t, r, "sdk.init", "", verify.Pass)
	expect(t, r, "unity.android_libraries", project.Android, verify.Pass)
	expect(t, r, "ios.plist.skan_endpoint", project.IOS, verify.Pass)
	expect(t, r, "ios.att_request", project.IOS, verify.Pass)
	// The key is read from the constant it is declared in, and checked for
	// each platform's app.
	for _, platform := range []project.Platform{project.Android, project.IOS} {
		f := expect(t, r, "key.valid", platform, verify.Pass)
		if f.File != unityScript || f.Line != 5 {
			t.Errorf("%s key found at %s:%d", platform, f.File, f.Line)
		}
	}
	// The package writes the tracking text on every iOS build: worth
	// knowing, not a fault.
	expect(t, r, "unity.att_description", project.IOS, verify.Info)
}

// acceptAll accepts every key.
type acceptAll struct{}

func (acceptAll) CheckKey(_ context.Context, _, _, _ string) (track.KeyCheck, error) {
	return track.KeyCheck{Status: track.KeyValid}, nil
}

func TestUnityWithoutTheDependencyManager(t *testing.T) {
	base := testproject.Unity()
	manifest := strings.Replace(base["Packages/manifest.json"], `"com.google.external-dependency-manager": "1.2.186",`, "", 1)
	f := expect(t, run(t, base.With(testproject.Files{"Packages/manifest.json": manifest}), verify.Options{}), "unity.android_libraries", project.Android, verify.Error)
	if !strings.Contains(f.Fix, "Force Resolve") {
		t.Errorf("fix = %q", f.Fix)
	}

	// The dependency manager as an Assets folder, as its .unitypackage
	// installs it, counts; so does a Gradle template that adds the SDK.
	for name, extra := range map[string]testproject.Files{
		"folder":   {"Assets/ExternalDependencyManager/Editor/version.txt": "1.2"},
		"template": {"Assets/Plugins/Android/mainTemplate.gradle": "dependencies {\n    implementation 'com.tenjin:android-sdk:1.24.0'\n}\n"},
	} {
		files := base.With(testproject.Files{"Packages/manifest.json": manifest}).With(extra)
		expect(t, run(t, files, verify.Options{}), "unity.android_libraries", project.Android, verify.Pass)
		_ = name
	}
	// A dependency commented out of the template adds nothing.
	files := base.With(testproject.Files{"Packages/manifest.json": manifest, "Assets/Plugins/Android/mainTemplate.gradle": "dependencies {\n    // implementation 'com.tenjin:android-sdk:1.24.0'\n}\n"})
	expect(t, run(t, files, verify.Options{}), "unity.android_libraries", project.Android, verify.Error)
}

func TestUnityPackageListedUnderTheWrongName(t *testing.T) {
	base := testproject.Unity()
	manifest := strings.Replace(base["Packages/manifest.json"], `"com.tenjin.sdk"`, `"com.tenjin.unity-sdk"`, 1)
	r := run(t, base.With(testproject.Files{"Packages/manifest.json": manifest}), verify.Options{})
	expect(t, r, "unity.package_name", "", verify.Error)
	expect(t, r, "sdk.dependency", "", verify.Error)
}

func TestUnityPackageCodeIsNotTheAppsCode(t *testing.T) {
	// A .unitypackage install puts the package's scripts under
	// Assets/Tenjin, and they call getInstance and Connect themselves. With
	// the app's own script gone, that is not an integration.
	files := testproject.Unity().Without(unityScript).With(testproject.Files{
		"Assets/Tenjin/Scripts/TenjinObject.cs": "public class TenjinObject : MonoBehaviour\n{\n    public string sdkKey;\n    void Awake()\n    {\n        BaseTenjin instance = Tenjin.getInstance(sdkKey);\n        instance.Connect();\n    }\n}\n",
	})
	r := run(t, files, verify.Options{})
	expect(t, r, "sdk.dependency", "", verify.Pass)
	expect(t, r, "sdk.init", "", verify.Error)
	expect(t, r, "sdk.connect", "", verify.Error)
}

// The no-code path: the TenjinObject prefab dropped into a scene, its SDK
// Key set in the Inspector, which Unity saves as a prefab override.
func TestUnityTenjinObjectInAScene(t *testing.T) {
	scene := func(key string) string {
		return `%YAML 1.1
--- !u!1001 &1234
PrefabInstance:
  m_Modification:
    m_Modifications:
    - target: {fileID: 1, guid: d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9, type: 3}
      propertyPath: sdkKey
      value: ` + key + `
      objectReference: {fileID: 0}
  m_SourcePrefab: {fileID: 100100000, guid: d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9, type: 3}
`
	}
	files := testproject.Unity().Without(unityScript).With(testproject.Files{"Assets/Scenes/Main.unity": scene(testproject.Key)})
	r := run(t, files, verify.Options{Keys: acceptAll{}})
	if f := expect(t, r, "sdk.init", "", verify.Pass); f.File != "Assets/Scenes/Main.unity" {
		t.Errorf("init found in %q", f.File)
	}
	expect(t, r, "key.valid", project.Android, verify.Pass)
	expect(t, r, "ios.att_request", project.IOS, verify.Pass)
	// The component connects once; resumed sessions are not sent.
	if f := expect(t, r, "unity.resume", "", verify.Info); !strings.Contains(f.Detail, "TenjinObject") {
		t.Errorf("detail = %q", f.Detail)
	}
	if has(r, "unity.store_type") {
		t.Error("the component sets the store itself, yet the store type was reported")
	}

	// A component with no key set is a placeholder.
	files = testproject.Unity().Without(unityScript).With(testproject.Files{"Assets/Scenes/Main.unity": scene("")})
	expect(t, run(t, files, verify.Options{}), "sdk.key.placeholder", "", verify.Error)

	// A placeholder an agent left there is one the CLI fills, and until then
	// Tenjin refuses the app's events.
	files = testproject.Unity().Without(unityScript).With(testproject.Files{"Assets/Scenes/Main.unity": scene("__TENJIN_ANDROID_SDK_KEY__")})
	if f := expect(t, run(t, files, verify.Options{}), "sdk.key.unfilled", project.Android, verify.Error); f.File != "Assets/Scenes/Main.unity" || f.Line != 8 {
		t.Errorf("finding = %+v", f)
	}
}

func TestUnityShrinkingNeedsKeepRules(t *testing.T) {
	base := testproject.Unity()
	settings := strings.Replace(base["ProjectSettings/ProjectSettings.asset"], "AndroidMinifyRelease: 0", "AndroidMinifyRelease: 1", 1)
	shrunk := base.With(testproject.Files{"ProjectSettings/ProjectSettings.asset": settings})
	f := expect(t, run(t, shrunk, verify.Options{}), "android.proguard", project.Android, verify.Warning)
	if !strings.Contains(f.Fix, "Assets/Plugins/Android/proguard-user.txt") || !strings.Contains(f.Fix, "-keep class com.tenjin.** { *; }") {
		t.Errorf("fix = %q", f.Fix)
	}
	kept := shrunk.With(testproject.Files{"Assets/Plugins/Android/proguard-user.txt": "-keep class com.tenjin.** { *; }\n"})
	expect(t, run(t, kept, verify.Options{}), "android.proguard", project.Android, verify.Pass)
}

func TestUnitySKANEndpointNeedsAScript(t *testing.T) {
	files := testproject.Unity().Without("Assets/Editor/TenjinPostProcess.cs").With(testproject.Files{
		"Assets/MaxSdk/Scripts/MaxSdk.cs": "public class MaxSdk {}\n",
	})
	f := expect(t, run(t, files, verify.Options{}), "ios.plist.skan_endpoint", project.IOS, verify.Warning)
	if !strings.Contains(f.Detail, "AppLovin MAX writes its own value") || !strings.Contains(f.Fix, "[PostProcessBuild]") {
		t.Errorf("finding = %+v", f)
	}
}

func TestUnityResumeAndStoreType(t *testing.T) {
	base := testproject.Unity()
	script := base[unityScript]
	script = strings.Replace(script, "OnApplicationPause", "OnSomethingElse", 1)
	script = strings.Replace(script, "        instance.SetAppStoreType(AppStoreType.googleplay);\n", "", 1)
	r := run(t, base.With(testproject.Files{unityScript: script}), verify.Options{})
	expect(t, r, "unity.resume", "", verify.Info)
	expect(t, r, "unity.store_type", project.Android, verify.Info)
}

// Connect counts on a Tenjin instance, under whatever name the script gives
// it, and not on an IAP store's.
func TestUnityConnectIsOnATenjinInstance(t *testing.T) {
	base := testproject.Unity()
	script := func(body string) testproject.Files {
		return base.With(testproject.Files{unityScript: "using UnityEngine;\n\npublic class Boot : MonoBehaviour\n{\n    private BaseTenjin tenjin;\n\n    async void Start()\n    {\n" + body + "    }\n}\n"})
	}
	get := `        tenjin = Tenjin.getInstance("` + testproject.Key + `");` + "\n"
	cases := map[string]struct {
		body string
		want verify.Severity
	}{
		"another library's Connect": {get + "        await UnityIAPServices.DefaultStore().Connect();\n", verify.Error},
		"a field":                   {get + "        tenjin.Connect();\n", verify.Pass},
		"a local":                   {`        var t = Tenjin.getInstance("` + testproject.Key + `");` + "\n        t.Connect();\n", verify.Pass},
		"chained":                   {`        Tenjin.getInstance("` + testproject.Key + `").Connect();` + "\n", verify.Pass},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			expect(t, run(t, script(tc.body), verify.Options{}), "sdk.connect", "", tc.want)
		})
	}
}

func TestUnityOutdatedPackage(t *testing.T) {
	base := testproject.Unity()
	manifest := strings.Replace(base["Packages/manifest.json"], "#1.22.1", "#1.16.5", 1)
	f := expect(t, run(t, base.With(testproject.Files{"Packages/manifest.json": manifest}), verify.Options{}), "sdk.version", "", verify.Warning)
	if !strings.Contains(f.Fix, "com.tenjin.sdk to 1.22.1") {
		t.Errorf("fix = %q", f.Fix)
	}
}

// DebugLogs() turns on the iOS SDK's request logs; release builds must not.
// Unity marks development builds with DEVELOPMENT_BUILD and Debug.isDebugBuild.
func TestUnityDebugLogs(t *testing.T) {
	base := testproject.Unity()
	const before = "        instance.SetAppStoreType(AppStoreType.googleplay);\n"
	for _, c := range []struct {
		name, add string
		severity  verify.Severity
	}{
		{"in release builds too", "        instance.DebugLogs();\n", verify.Warning},
		{"chained on getInstance", "        Tenjin.getInstance(TenjinKey).DebugLogs();\n", verify.Warning},
		{"development builds only", "#if DEVELOPMENT_BUILD\n        instance.DebugLogs();\n#endif\n", verify.Pass},
		{"in the editor only", "#if UNITY_EDITOR\n        instance.DebugLogs();\n#endif\n", verify.Pass},
		{"debug or development", "#if DEBUG || DEVELOPMENT_BUILD\n        instance.DebugLogs();\n#endif\n", verify.Pass},
		{"not in development builds", "#if !DEVELOPMENT_BUILD\n        instance.DebugLogs();\n#endif\n", verify.Warning},
		{"after an elif", "#if UNITY_EDITOR\n        Debug.Log(\"editor\");\n#elif UNITY_IOS\n        instance.DebugLogs();\n#endif\n", verify.Warning},
		{"on a debug build, one line", "        if (Debug.isDebugBuild) instance.DebugLogs();\n", verify.Pass},
		{"on a debug build, in a block", "        if (Debug.isDebugBuild)\n        {\n            instance.DebugLogs();\n        }\n", verify.Pass},
		{"on a release build", "        if (!Debug.isDebugBuild) instance.DebugLogs();\n", verify.Warning},
	} {
		t.Run(c.name, func(t *testing.T) {
			script := strings.Replace(base[unityScript], before, before+c.add, 1)
			r := run(t, base.With(testproject.Files{unityScript: script}), verify.Options{})
			f := expect(t, r, "ios.debug_logs", project.IOS, c.severity)
			if f.File != unityScript || f.Line == 0 {
				t.Errorf("found at %s:%d", f.File, f.Line)
			}
			if c.severity == verify.Warning && (!strings.Contains(f.Title, "DebugLogs()") || !strings.Contains(f.Fix, "DEVELOPMENT_BUILD")) {
				t.Errorf("finding = %+v", f)
			}
		})
	}
	// Without the call there is nothing to say: Unity's launch check does
	// not depend on it the way a native iOS app's does.
	if r := run(t, base, verify.Options{}); has(r, "ios.debug_logs") {
		t.Error("ios.debug_logs reported for a project that never calls DebugLogs()")
	}
}

// The TenjinObject component's Debug Logs box turns the logs on in every
// build, release builds included.
func TestUnityTenjinObjectDebugLogs(t *testing.T) {
	component := func(debugLogs string) string {
		return `%YAML 1.1
--- !u!1 &100
GameObject:
  m_Name: Tenjin
--- !u!114 &200
MonoBehaviour:
  m_GameObject: {fileID: 100}
  m_Script: {fileID: 11500000, guid: e3e00e20639f4f469350ac04120ef3b0, type: 3}
  sdkKey: ` + testproject.Key + `
  debugLogs: ` + debugLogs + `
  optIn: 1
--- !u!114 &300
MonoBehaviour:
  m_Script: {fileID: 11500000, guid: 0123456789abcdef0123456789abcdef, type: 3}
  debugLogs: 1
`
	}
	override := `%YAML 1.1
--- !u!1001 &1234
PrefabInstance:
  m_Modification:
    m_Modifications:
    - target: {fileID: 1, guid: d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9, type: 3}
      propertyPath: sdkKey
      value: ` + testproject.Key + `
      objectReference: {fileID: 0}
    - target: {fileID: 1, guid: d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9, type: 3}
      propertyPath: debugLogs
      value: 1
      objectReference: {fileID: 0}
  m_SourcePrefab: {fileID: 100100000, guid: d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9, type: 3}
`
	const scene = "Assets/Scenes/Main.unity"
	for name, c := range map[string]struct {
		scene string
		line  int
	}{
		"ticked on the component": {component("1"), 10},
		"ticked as an override":   {override, 11},
	} {
		t.Run(name, func(t *testing.T) {
			files := testproject.Unity().Without(unityScript).With(testproject.Files{scene: c.scene})
			f := expect(t, run(t, files, verify.Options{}), "ios.debug_logs", project.IOS, verify.Warning)
			if f.File != scene || f.Line != c.line || !strings.Contains(f.Title, "Debug Logs") || !strings.Contains(f.Fix, "Untick") {
				t.Errorf("finding = %+v", f)
			}
		})
	}
	// Unticked, and another script's field of the same name, say nothing.
	files := testproject.Unity().Without(unityScript).With(testproject.Files{scene: component("0")})
	if r := run(t, files, verify.Options{}); has(r, "ios.debug_logs") {
		t.Errorf("reported: %+v", find(r, "ios.debug_logs", project.IOS))
	}
}
