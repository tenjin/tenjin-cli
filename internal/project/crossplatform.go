package project

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	flutterSDK      = regexp.MustCompile(`(?m)^\s+sdk:\s*flutter\s*$`)
	pubspecTenjin   = regexp.MustCompile(`(?m)^\s+tenjin_plugin\s*:`)
	pubLockTenjin   = regexp.MustCompile(`(?s)\n  tenjin_plugin:\n.*?\n    version: "([^"]+)"`)
	capacitorAppID  = regexp.MustCompile(`["']?appId["']?\s*:\s*["']([^"']+)["']`)
	cordovaWidget   = regexp.MustCompile(`<widget[^>]*\sid="([^"]+)"`)
	unityIdentifier = regexp.MustCompile(`(?m)^  applicationIdentifier:\n((?:    .+\n)+)`)
	unityIDLine     = regexp.MustCompile(`(?m)^    (\w+): (.+)$`)
	unityGitTag     = regexp.MustCompile(`#v?([0-9][0-9A-Za-z.\-]*)$`)
	iniBundleID     = regexp.MustCompile(`(?m)^BundleIdentifier=(.+)$`)
	iniPackageName  = regexp.MustCompile(`(?m)^PackageName=(.+)$`)
	upluginVersion  = regexp.MustCompile(`"VersionName"\s*:\s*"([^"]+)"`)
)

// packageJSON is the part of package.json detection needs.
type packageJSON struct {
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

func (p packageJSON) dependency(name string) (string, bool) {
	if v, ok := p.Dependencies[name]; ok {
		return v, true
	}
	v, ok := p.DevDependencies[name]
	return v, ok
}

func (d *detector) packageJSON() (packageJSON, bool) {
	var pkg packageJSON
	b, err := os.ReadFile(d.path("package.json"))
	if err != nil || json.Unmarshal(b, &pkg) != nil {
		return pkg, false
	}
	return pkg, true
}

// nativeTargets adds the android/ and ios/ subprojects that cross-platform
// frameworks generate.
func (d *detector) nativeTargets() {
	if d.isDir("android") {
		if t, ok := d.androidTarget("android"); ok {
			d.p.Targets = append(d.p.Targets, t)
		}
	}
	if d.isDir("ios") {
		if t, ok := d.iosTarget("ios"); ok {
			d.p.Targets = append(d.p.Targets, t)
		}
	}
}

func (d *detector) flutter() bool {
	pubspec := d.read("pubspec.yaml")
	if !flutterSDK.MatchString(pubspec) {
		return false
	}
	d.p.Framework = Flutter
	d.p.Supported = true
	d.nativeTargets()
	if pubspecTenjin.MatchString(pubspec) {
		i := Integration{Package: "tenjin_plugin", Source: "pubspec.yaml"}
		if m := pubLockTenjin.FindStringSubmatch(d.read("pubspec.lock")); m != nil {
			i.Version = m[1]
		}
		d.found(i)
	}
	return true
}

func (d *detector) reactNative() bool {
	pkg, ok := d.packageJSON()
	if !ok {
		return false
	}
	if _, ok := pkg.dependency("react-native"); !ok {
		return false
	}
	d.p.Framework = ReactNative
	d.p.Supported = true
	d.nativeTargets()

	if _, expo := pkg.dependency("expo"); expo {
		if len(d.p.Targets) == 0 {
			d.expoTargets()
			d.note("This is an Expo project without native folders. The Tenjin SDK is a native module: run `npx expo prebuild` or use a development build before integrating.")
		} else {
			d.note("This is an Expo project. The Tenjin package ships no config plugin, so its native settings must be kept in the prebuilt android/ and ios/ folders.")
		}
	}
	if v, ok := pkg.dependency("react-native-tenjin"); ok {
		d.found(Integration{Package: "react-native-tenjin", Version: v, Source: "package.json"})
	}
	return true
}

// expoTargets reads bundle IDs from app.json when there are no native folders.
func (d *detector) expoTargets() {
	var app struct {
		Expo struct {
			IOS     struct{ BundleIdentifier string } `json:"ios"`
			Android struct{ Package string }          `json:"android"`
		} `json:"expo"`
	}
	b, err := os.ReadFile(d.path("app.json"))
	if err != nil || json.Unmarshal(b, &app) != nil {
		return
	}
	if id := app.Expo.Android.Package; id != "" {
		d.p.Targets = append(d.p.Targets, Target{Platform: Android, BundleID: id, Source: "app.json"})
	}
	if id := app.Expo.IOS.BundleIdentifier; id != "" {
		d.p.Targets = append(d.p.Targets, Target{Platform: IOS, BundleID: id, Source: "app.json"})
	}
}

func (d *detector) capacitor() bool {
	config := ""
	for _, name := range []string{"capacitor.config.ts", "capacitor.config.js", "capacitor.config.json"} {
		if d.exists(name) {
			config = name
			break
		}
	}
	pkg, hasPkg := d.packageJSON()
	_, hasCore := pkg.dependency("@capacitor/core")
	if config == "" && !hasCore {
		return false
	}
	d.p.Framework = Capacitor
	d.p.Supported = true
	d.nativeTargets()

	if config == "" && len(d.p.Targets) == 0 {
		d.note("@capacitor/core is a dependency, but there is no capacitor.config file and no android/ or ios/ folder. This may be a plugin or a library, not an app.")
	}
	if m := capacitorAppID.FindStringSubmatch(d.read(config)); m != nil {
		appID := m[1]
		if len(d.p.Targets) == 0 {
			d.note("No android/ or ios/ folder yet. Run `npx cap add android` or `npx cap add ios`; both will use the app ID %s from %s.", appID, config)
		}
		for _, t := range d.p.Targets {
			if t.BundleID != "" && t.BundleID != appID {
				d.note("The %s bundle ID (%s) differs from appId in %s (%s). Tenjin sees the native one.", t.Platform, t.BundleID, config, appID)
			}
		}
	}
	if hasPkg {
		if v, ok := pkg.dependency("ionic-capacitor-tenjin"); ok {
			d.found(Integration{Package: "ionic-capacitor-tenjin", Version: v, Source: "package.json"})
		}
	}
	return true
}

func (d *detector) cordova() bool {
	m := cordovaWidget.FindStringSubmatch(d.read("config.xml"))
	if m == nil {
		return false
	}
	d.p.Framework = Cordova
	d.p.Supported = false
	d.note("This is a Cordova project (%s). Tenjin has no Cordova plugin; the supported path for hybrid apps is Capacitor.", m[1])
	return true
}

func (d *detector) unity() bool {
	if !d.exists("ProjectSettings/ProjectVersion.txt") || !d.isDir("Assets") {
		return false
	}
	d.p.Framework = Unity
	d.p.Supported = true

	settings := d.read("ProjectSettings/ProjectSettings.asset")
	if block := unityIdentifier.FindStringSubmatch(settings); block != nil {
		ids := map[string]string{}
		for _, m := range unityIDLine.FindAllStringSubmatch(block[1], -1) {
			ids[m[1]] = strings.TrimSpace(m[2])
		}
		if id := ids["Android"]; id != "" {
			d.p.Targets = append(d.p.Targets, Target{Platform: Android, BundleID: id, Source: "ProjectSettings/ProjectSettings.asset"})
		}
		if id := ids["iPhone"]; id != "" {
			d.p.Targets = append(d.p.Targets, Target{Platform: IOS, BundleID: id, Source: "ProjectSettings/ProjectSettings.asset"})
		}
	}
	if len(d.p.Targets) == 0 {
		d.note("Could not read the application identifiers from ProjectSettings/ProjectSettings.asset. It may be saved in binary form; set Asset Serialization to Force Text in Unity.")
	}

	var manifest struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	if b, err := os.ReadFile(d.path("Packages/manifest.json")); err == nil && json.Unmarshal(b, &manifest) == nil {
		if ref, ok := manifest.Dependencies["com.tenjin.sdk"]; ok {
			i := Integration{Package: "com.tenjin.sdk", Source: "Packages/manifest.json"}
			if m := unityGitTag.FindStringSubmatch(ref); m != nil {
				i.Version = m[1]
			}
			d.found(i)
		}
		if _, ok := manifest.Dependencies["com.tenjin.unity-sdk"]; ok {
			d.note("Packages/manifest.json uses the key com.tenjin.unity-sdk. The package is named com.tenjin.sdk, and Unity rejects a mismatched key.")
		}
	}
	if len(d.p.Tenjin) == 0 && d.isDir("Assets/Tenjin") {
		d.found(Integration{Package: "Tenjin (.unitypackage)", Source: "Assets/Tenjin"})
	}
	return true
}

func (d *detector) unreal() bool {
	matches, _ := filepath.Glob(d.path("*.uproject"))
	if len(matches) == 0 {
		return false
	}
	d.p.Framework = Unreal
	d.p.Supported = true

	ini := strings.ReplaceAll(d.read("Config/DefaultEngine.ini"), "\r\n", "\n")
	if m := iniPackageName.FindStringSubmatch(ini); m != nil {
		d.p.Targets = append(d.p.Targets, Target{Platform: Android, BundleID: strings.TrimSpace(m[1]), Source: "Config/DefaultEngine.ini"})
	}
	if m := iniBundleID.FindStringSubmatch(ini); m != nil {
		d.p.Targets = append(d.p.Targets, Target{Platform: IOS, BundleID: strings.TrimSpace(m[1]), Source: "Config/DefaultEngine.ini"})
	}
	if !d.isDir("Source") {
		d.note("This is a Blueprint-only project. The Tenjin plugin needs a C++ project: add a C++ class in the editor to convert it.")
	}
	if uplugin := "Plugins/TenjinSDK/TenjinSDK.uplugin"; d.exists(uplugin) {
		i := Integration{Package: "TenjinSDK (Unreal plugin)", Source: uplugin}
		if m := upluginVersion.FindStringSubmatch(d.read(uplugin)); m != nil {
			i.Version = m[1]
		}
		d.found(i)
	}
	return true
}
