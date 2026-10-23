package verify

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tenjin/tenjin-cli/internal/project"
)

// The Unity checks follow the Tenjin Unity SDK's README and its package:
// Editor/Dependencies.xml lists the Android libraries for the External
// Dependency Manager, Editor/BuildPostProcessor.cs writes the tracking
// prompt text on every iOS build, and Runtime/TenjinObject.cs is a
// component that initialises and connects from the scene.

var unitySpec = frameworkSpec{
	exts:        []string{".cs"},
	sourceDirs:  func(*project.Target) []string { return []string{"Assets"} },
	packages:    []string{"com.tenjin.sdk", "Tenjin ("},
	installFix:  `Add "com.tenjin.sdk": "https://github.com/tenjin/tenjin-unity-sdk.git#` + sdkVersionToken + `" to the dependencies in Packages/manifest.json, or add that address in Window > Package Manager > Add package from git URL.`,
	initName:    "Tenjin.getInstance(key)",
	connectName: "Connect()",
	initCall:    regexp.MustCompile(`Tenjin\.getInstance\w*\(`),
	keyLiteral:  regexp.MustCompile(`Tenjin\.getInstance\w*\(\s*"([^"]*)"`),
	// On a Tenjin instance only, after normalizeUnity.
	connectCall: regexp.MustCompile(`\bTenjin\.Connect\(`),
	normalize:   normalizeUnity,
	initFix:     `In the first scene's Start(), call Tenjin.getInstance("<SDK_KEY>"), choosing the key with #if UNITY_IOS and #elif UNITY_ANDROID; or drag the TenjinObject prefab into the first scene and set its SDK Key.`,
	connectFix:  "Call Connect() on the instance in Start(), and again in OnApplicationPause when pauseStatus is false.",
	attCall:     regexp.MustCompile(`RequestTrackingAuthorizationWithCompletionHandler|RequestAuthorizationTracking`),
	// On iOS only: the package's Android DebugLogs() does nothing. On an
	// instance, chained on getInstance, or on the component's instance.
	debugLogsCall: regexp.MustCompile(`(?:\bTenjin\.(?:getInstance\w*\((?:[^()]|\([^()]*\))*\)\s*\.)?|\bGetTenjinInstance\(\s*\)\s*\.)DebugLogs\(`),
	debugLogsName: "DebugLogs()",
	debugOnlyFix:  "Call it in development builds only: inside #if DEVELOPMENT_BUILD … #endif, or under if (Debug.isDebugBuild).",
	// The package's own scripts call getInstance and Connect themselves.
	skip: []string{"Assets/Tenjin/"},
}

// The component and its prefab, by the IDs the package ships them with.
// Scenes and prefabs refer to them by these, not by name.
const (
	tenjinObjectScript = "e3e00e20639f4f469350ac04120ef3b0"
	tenjinObjectPrefab = "d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9"
)

var (
	componentKey  = regexp.MustCompile(`(?m)^\s*sdkKey:[ \t]*(\S*)[ \t]*$`)
	overriddenKey = regexp.MustCompile(`propertyPath: sdkKey\s+value:[ \t]*(\S*)`)
	minifyRelease = regexp.MustCompile(`(?m)^\s*AndroidMinify(?:Release|Debug): 1\s*$`)
	// The component's Debug Logs box, ticked on it or as a prefab override.
	componentDebugLogs  = regexp.MustCompile(`(?m)^\s*debugLogs:[ \t]*1[ \t]*$`)
	overriddenDebugLogs = regexp.MustCompile(`(?m)propertyPath: debugLogs\s+value:[ \t]*1[ \t]*$`)
	unityStoreType      = regexp.MustCompile(`SetAppStoreType\(`)
)

func (r *runner) checkUnity() {
	spec := unitySpec
	r.checkDependency(spec, "")
	r.checkUnityPackageName()
	src := r.scanSource(spec, spec.sourceDirs(nil))
	resolveKeyConstants(&src)
	usesComponent := r.findTenjinObject(&src)
	r.checkCalls(spec, src, "")
	r.checkUnityResume(src, usesComponent)

	if t := r.p.Target(project.Android); t != nil {
		r.checkUnityAndroidLibraries()
		r.checkUnityStoreType(src, usesComponent)
		r.checkUnityProguard()
		r.checkKey(t, src)
		r.checkDevice(t, src)
	}
	if t := r.p.Target(project.IOS); t != nil {
		r.checkDevice(t, src)
		r.add(Finding{
			ID: "unity.att_description", Severity: Info, Platform: project.IOS,
			Title:  "The Tenjin package writes the tracking prompt text on every iOS build",
			Detail: "It sets NSUserTrackingUsageDescription to a generic sentence, replacing any text of your own. Apple reviews this text with the app.",
			Fix:    "To use your own wording, set it in a [PostProcessBuild] script whose callbackOrder is above 1, so that it runs after Tenjin's.",
		})
		r.checkUnitySKAN()
		r.checkUnityDebugLogs(spec, src)
		if usesComponent {
			r.pass("ios.att_request", project.IOS, "The TenjinObject component asks for tracking permission before it connects", "", 0)
		} else {
			r.checkATTRequest(spec, src)
		}
		r.checkKey(t, src)
	}
}

// checkUnityPackageName catches the package listed under a name Unity will
// not resolve, a mistake the published LLM guide makes.
func (r *runner) checkUnityPackageName() {
	var manifest struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	if json.Unmarshal([]byte(r.readRel("Packages/manifest.json")), &manifest) != nil {
		return
	}
	if _, wrong := manifest.Dependencies["com.tenjin.unity-sdk"]; wrong {
		r.add(Finding{
			ID: "unity.package_name", Severity: Error,
			Title:  "Packages/manifest.json lists the Tenjin package as com.tenjin.unity-sdk",
			Detail: "The package is named com.tenjin.sdk, and Unity rejects a dependency whose name does not match the package's own.",
			File:   "Packages/manifest.json",
			Fix:    `Rename the key to "com.tenjin.sdk".`,
		})
	}
}

var (
	keyArgument = regexp.MustCompile(`Tenjin\.getInstance\w*\(\s*([A-Za-z_]\w*)\s*[,)]`)
	stringConst = `(?m)(?:const|static readonly|readonly)\s+string\s+%s\s*=\s*"([^"]*)"`
)

// resolveKeyConstants finds keys passed by name, as Unity code usually
// does: Tenjin.getInstance(TENJIN_API_KEY), with the constant declared in the
// same file. Only a constant's string value is taken; anything computed is
// left alone.
func resolveKeyConstants(src *source) {
	seen := map[string]bool{}
	for _, c := range src.inits {
		for _, m := range keyArgument.FindAllStringSubmatch(c.Text, -1) {
			decl := regexp.MustCompile(fmt.Sprintf(stringConst, regexp.QuoteMeta(m[1])))
			loc := decl.FindStringSubmatchIndex(c.Text)
			if loc == nil || seen[c.File+m[1]] {
				continue
			}
			seen[c.File+m[1]] = true
			src.keys = append(src.keys, keyLiteral{Value: c.Text[loc[2]:loc[3]], File: c.File, Line: lineAt(c.Text, loc[0])})
		}
	}
}

// findTenjinObject looks for the no-code component in the project's scenes
// and prefabs. Where it is used, it stands for the initialise and connect
// calls, and its SDK Key field for a key in the code.
func (r *runner) findTenjinObject(src *source) bool {
	found := false
	for _, rel := range project.SourceFiles(r.p.Root, "Assets", ".unity", ".prefab") {
		if unitySpec.skips(rel) {
			continue
		}
		info, err := os.Stat(filepath.Join(r.p.Root, filepath.FromSlash(rel)))
		if err != nil || info.Size() > maxSourceFileSize {
			continue
		}
		text := r.readRel(rel)
		at := strings.Index(text, tenjinObjectScript)
		if at < 0 {
			at = strings.Index(text, tenjinObjectPrefab)
		}
		if at < 0 {
			continue
		}
		found = true
		c := call{File: rel, Line: lineAt(text, at), Text: text}
		src.inits = append(src.inits, c)
		src.connects = append(src.connects, c)
		for _, re := range []*regexp.Regexp{overriddenKey, componentKey} {
			for _, m := range re.FindAllStringSubmatchIndex(text, -1) {
				src.keys = append(src.keys, keyLiteral{Value: text[m[2]:m[3]], File: rel, Line: lineAt(text, m[0])})
			}
		}
		src.componentDebugLogs = append(src.componentDebugLogs, componentDebugTicks(rel, text)...)
	}
	return found
}

// componentDebugTicks finds the TenjinObject components in a scene or
// prefab whose Debug Logs box is ticked: on the component, in the YAML
// document of its script, or as an override of the prefab's.
func componentDebugTicks(rel, text string) []call {
	var ticks []call
	start := 0
	for start < len(text) {
		end := strings.Index(text[start+1:], "\n--- ")
		if end < 0 {
			end = len(text)
		} else {
			end += start + 1
		}
		doc := text[start:end]
		var re *regexp.Regexp
		switch {
		case strings.Contains(doc, tenjinObjectScript):
			re = componentDebugLogs
		case strings.Contains(doc, tenjinObjectPrefab):
			re = overriddenDebugLogs
		}
		if re != nil {
			for _, loc := range re.FindAllStringIndex(doc, -1) {
				at := start + loc[0]
				if text[at] == '\n' {
					at++
				}
				ticks = append(ticks, call{File: rel, Line: lineAt(text, at), At: at, Text: text})
			}
		}
		start = end
	}
	return ticks
}

// checkUnityDebugLogs reports the SDK's logs turned on in release builds:
// by DebugLogs() in code, or by the TenjinObject component, whose box
// applies to every build. They show on iOS only.
func (r *runner) checkUnityDebugLogs(spec frameworkSpec, src source) {
	if len(src.componentDebugLogs) > 0 {
		c := src.componentDebugLogs[0]
		r.add(Finding{
			ID: "ios.debug_logs", Severity: Warning, Platform: project.IOS,
			Title:  "The TenjinObject component has Debug Logs ticked, so release builds log too",
			Detail: "The box applies to every build. It logs every request and response body, device identifiers included, on the devices of the app's users.",
			File:   c.File, Line: c.Line,
			Fix: "Untick Debug Logs on the TenjinObject in " + c.File + " before building a release, or leave it off and call DebugLogs() in code inside #if DEVELOPMENT_BUILD … #endif.",
		})
		return
	}
	r.checkDebugLogs(spec, src)
}

// checkUnityResume reports when nothing connects again after the app comes
// back from the background, which the README asks for.
func (r *runner) checkUnityResume(src source, usesComponent bool) {
	for _, c := range src.connects {
		if strings.Contains(c.Text, "OnApplicationPause") && strings.HasSuffix(c.File, ".cs") {
			return
		}
	}
	if len(src.connects) == 0 {
		return
	}
	detail := "Tenjin counts a session each time Connect() runs. Without a call when the app is resumed, sessions after the first are not sent."
	if usesComponent {
		detail = "The TenjinObject component connects once, when its scene loads. " + detail
	}
	r.add(Finding{
		ID: "unity.resume", Severity: Info,
		Title:  "Connect() is not called again when the app returns from the background",
		Detail: detail,
		Fix:    "In a MonoBehaviour, call Connect() on the instance in OnApplicationPause when pauseStatus is false.",
	})
}

// checkUnityAndroidLibraries checks that something adds Tenjin's Android
// libraries to the build. The package only lists them, for the External
// Dependency Manager to resolve.
func (r *runner) checkUnityAndroidLibraries() {
	var manifest struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	_ = json.Unmarshal([]byte(r.readRel("Packages/manifest.json")), &manifest)
	_, edmPackage := manifest.Dependencies["com.google.external-dependency-manager"]
	switch {
	case edmPackage:
		r.pass("unity.android_libraries", project.Android, "The External Dependency Manager adds Tenjin's Android libraries to the build", "Packages/manifest.json", 0)
		return
	case r.isDir("Assets/ExternalDependencyManager"), r.isDir("Assets/PlayServicesResolver"):
		r.pass("unity.android_libraries", project.Android, "The External Dependency Manager adds Tenjin's Android libraries to the build", "Assets/ExternalDependencyManager", 0)
		return
	case strings.Contains(r.readCode("Assets/Plugins/Android/mainTemplate.gradle"), "com.tenjin:android-sdk"):
		r.pass("unity.android_libraries", project.Android, "The Gradle template adds Tenjin's Android SDK to the build", "Assets/Plugins/Android/mainTemplate.gradle", 0)
		return
	}
	r.add(Finding{
		ID: "unity.android_libraries", Severity: Error, Platform: project.Android,
		Title:  "Nothing adds Tenjin's Android libraries to the build",
		Detail: "The Tenjin package lists the Android SDK and the Play Services libraries it needs for the External Dependency Manager, which this project does not have. Without them the Android build has no Tenjin SDK and no advertising ID.",
		Fix:    "Install the External Dependency Manager for Unity (EDM4U, github.com/googlesamples/unity-jar-resolver), then run Assets > External Dependency Manager > Android Resolver > Force Resolve.",
	})
}

// checkUnityStoreType reports an Android build that does not say which
// store it is for. The TenjinObject component sets Google Play itself.
func (r *runner) checkUnityStoreType(src source, usesComponent bool) {
	if usesComponent || strings.Contains(r.readCode("Assets/Plugins/Android/AndroidManifest.xml"), "TENJIN_APP_STORE") {
		return
	}
	for _, c := range append(src.inits, src.connects...) {
		if unityStoreType.MatchString(c.Text) {
			return
		}
	}
	r.add(Finding{
		ID: "unity.store_type", Severity: Info, Platform: project.Android,
		Title:  "The Android app store is not set",
		Detail: "It is unspecified unless set. Tenjin's README asks for googleplay on Google Play builds; Amazon and other stores need their own value.",
		Fix:    "Call instance.SetAppStoreType(AppStoreType.googleplay) before Connect(), with amazon or other for builds for those stores.",
	})
}

// checkUnityProguard needs keep rules where Unity shrinks the Android code.
func (r *runner) checkUnityProguard() {
	if !minifyRelease.MatchString(r.readRel("ProjectSettings/ProjectSettings.asset")) {
		return
	}
	const rules = "Assets/Plugins/Android/proguard-user.txt"
	if strings.Contains(r.readCode(rules), "com.tenjin") {
		r.pass("android.proguard", project.Android, "ProGuard keep rules for Tenjin are present", rules, 0)
		return
	}
	r.add(Finding{
		ID: "android.proguard", Severity: Warning, Platform: project.Android,
		Title:  "Code shrinking is on and there are no keep rules for Tenjin",
		Detail: "The SDK reaches Play Services by reflection. Without keep rules, release builds can lose the advertising ID or the install referrer, or crash.",
		File:   "ProjectSettings/ProjectSettings.asset",
		Fix:    "Turn on Custom Proguard File in Player Settings > Publishing Settings, and add these lines to " + rules + ":\n" + proguardRules,
	})
}

// checkUnitySKAN looks for an editor script that points SKAdNetwork
// postbacks at Tenjin. The Tenjin package does not, so without one the key
// has to be added by hand to every exported Xcode project.
func (r *runner) checkUnitySKAN() {
	setter := ""
	_ = filepath.WalkDir(filepath.Join(r.p.Root, "Assets"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || setter != "" {
			return nil
		}
		if d.IsDir() {
			if d.Name() == "Library" || strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".cs") {
			return nil
		}
		raw, err := os.ReadFile(p)
		if code := blankComments(string(raw)); err == nil && strings.Contains(code, plistSKAN) && strings.Contains(code, "tenjin-skan.com") {
			rel, _ := filepath.Rel(r.p.Root, p)
			setter = filepath.ToSlash(rel)
		}
		return nil
	})
	if setter != "" {
		r.pass("ios.plist.skan_endpoint", project.IOS, "A build script points SKAdNetwork postbacks at Tenjin", setter, 0)
		return
	}
	detail := "The Tenjin Unity package does not set " + plistSKAN + ", so it is missing from each exported Xcode project unless added by hand. Without it Apple does not send SKAdNetwork postback copies to Tenjin."
	if r.p.AdSDKs != nil {
		for _, sdk := range r.p.AdSDKs {
			if sdk.Name == "AppLovin MAX" {
				detail += " AppLovin MAX writes its own value there during the build, so a script that sets Tenjin's must run after AppLovin's."
				break
			}
		}
	}
	r.add(Finding{
		ID: "ios.plist.skan_endpoint", Severity: Warning, Platform: project.IOS,
		Title:  plistSKAN + " is not set to Tenjin",
		Detail: detail,
		Fix:    `Add an editor script with a [PostProcessBuild] method that, for iOS builds, opens the exported Info.plist with PlistDocument and calls plist.root.SetString("` + plistSKAN + `", "` + tenjinSKANURL + `").`,
	})
}

// isDir reports whether a path relative to the project root is a folder.
func (r *runner) isDir(rel string) bool {
	info, err := os.Stat(filepath.Join(r.p.Root, filepath.FromSlash(path.Clean(rel))))
	return err == nil && info.IsDir()
}
