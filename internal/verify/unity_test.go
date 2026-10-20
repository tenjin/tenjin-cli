package verify_test

import (
	"context"
	"strings"
	"testing"

	"github.com/tenjin/tenjin-cli/internal/track"
	"github.com/tenjin/tenjin-cli/internal/project"
	"github.com/tenjin/tenjin-cli/internal/testproject"
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
