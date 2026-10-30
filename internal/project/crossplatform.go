package project

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	flutterSDK    = regexp.MustCompile(`(?m)^\s+sdk:\s*flutter\s*$`)
	pubspecTenjin = regexp.MustCompile(`(?m)^\s+tenjin_plugin\s*:`)
	// tenjin_sdk is a community plugin (github.com/thize/flutter_tenjin_sdk),
	// not Tenjin's.
	pubspecCommunityTenjin = regexp.MustCompile(`(?m)^\s+tenjin_sdk\s*:\s*\^?([0-9][\w.+-]*)?`)
	pubLockTenjin          = regexp.MustCompile(`(?s)\n  tenjin_plugin:\n.*?\n    version: "([^"]+)"`)
	capacitorAppID         = regexp.MustCompile(`["']?appId["']?\s*:\s*["']([^"']+)["']`)
	cordovaWidget          = regexp.MustCompile(`<widget[^>]*\sid="([^"]+)"`)
	unityIdentifier        = regexp.MustCompile(`(?m)^  applicationIdentifier:\n((?:    .+\n)+)`)
	unityIDLine            = regexp.MustCompile(`(?m)^    (\w+): (.+)$`)
	unityGitTag            = regexp.MustCompile(`#v?([0-9][0-9A-Za-z.\-]*)$`)
	iniBundleID            = regexp.MustCompile(`(?m)^BundleIdentifier=(.+)$`)
	iniPackageName         = regexp.MustCompile(`(?m)^PackageName=(.+)$`)
	upluginVersion         = regexp.MustCompile(`"VersionName"\s*:\s*"([^"]+)"`)
	// capacitorPlatformBlock is a platform's own options in capacitor.config.
	capacitorPlatformBlock = regexp.MustCompile(`(?m)^\s*["']?(android|ios)["']?\s*:`)
	// plainVersion is a version a shell passes on as it is.
	plainVersion = regexp.MustCompile(`^[0-9][0-9A-Za-z.+-]*$`)
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
			// Some apps add the native SDK straight to the native project
			// instead of through the framework's package.
			d.androidIntegration(t.Android)
		}
	}
	if d.isDir("ios") {
		if t, ok := d.iosTarget("ios"); ok {
			d.p.Targets = append(d.p.Targets, t)
			d.iosIntegration(t.IOS)
		}
	}
}

// mobilePlatform is a platform a cross-platform framework adds a native
// project for, in a folder of that name.
type mobilePlatform struct {
	platform     Platform
	folder, name string
}

var mobilePlatforms = []mobilePlatform{{Android, "android", "Android"}, {IOS, "ios", "iOS"}}

// notePlatformToAdd says, when a cross-platform project has the native
// project of one platform and no folder for the other, that only the one is
// covered and how the other is added. add gives the commands that add a
// platform, and what in the project already names it, or "".
func (d *detector) notePlatformToAdd(add func(mobilePlatform) (commands []string, why string)) {
	if len(d.p.Targets) == 0 {
		return
	}
	for i, m := range mobilePlatforms {
		if d.p.Target(m.platform) != nil || d.exists(m.folder) {
			continue
		}
		covered := mobilePlatforms[1-i].name
		commands, why := add(m)
		run := "`" + strings.Join(commands, "` and `") + "`"
		if why != "" {
			d.note("Only %s is covered: there is no %s/ folder yet, although %s. To add %s, run %s, then `tenjin setup` again.", covered, m.folder, why, m.name, run)
		} else {
			d.note("Only %s is covered: there is no %s/ folder. If the app ships on %s too, run %s, then `tenjin setup` again.", covered, m.folder, m.name, run)
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
	d.notePlatformToAdd(func(m mobilePlatform) ([]string, string) {
		return []string{"flutter create --platforms=" + m.folder + " ."}, ""
	})
	if pubspecTenjin.MatchString(pubspec) {
		i := Integration{Package: "tenjin_plugin", Source: "pubspec.yaml"}
		if m := pubLockTenjin.FindStringSubmatch(d.read("pubspec.lock")); m != nil {
			i.Version = m[1]
		}
		d.found(i)
	} else if m := pubspecCommunityTenjin.FindStringSubmatch(pubspec); m != nil {
		d.found(Integration{Package: CommunityFlutterPackage, Version: m[1], Source: "pubspec.yaml"})
	} else {
		d.flutterThroughPackage(pubspec)
	}
	return true
}

// pubspecPathDependency is a dependency taken from a folder:
//
//	my_ads:
//	  path: ../my_ads
var pubspecPathDependency = regexp.MustCompile(`(?m)^[ \t]+([\w]+):[ \t]*\n[ \t]+path:[ \t]*["']?([^"'\n]+?)["']?[ \t]*$`)

// flutterThroughPackage records the SDK when the app gets tenjin_plugin
// through another package: a local one, whose code is read for the calls,
// or a published one, which pubspec.lock shows.
func (d *detector) flutterThroughPackage(pubspec string) {
	version, transitive := "", false
	if m := pubLockTransitive.FindStringSubmatch(d.read("pubspec.lock")); m != nil {
		version, transitive = m[1], true
	} else if m := pubLockTenjin.FindStringSubmatch(d.read("pubspec.lock")); m != nil {
		version = m[1]
	}
	for _, m := range pubspecPathDependency.FindAllStringSubmatch(pubspec, -1) {
		dir := path.Clean(m[2])
		if pubspecTenjin.MatchString(d.read(path.Join(dir, "pubspec.yaml"))) {
			d.found(Integration{Package: "tenjin_plugin (through " + m[1] + ")", Version: version, Source: path.Join(dir, "pubspec.yaml"), Through: m[1], Local: true})
			d.p.LibraryCode = append(d.p.LibraryCode, path.Join(dir, "lib"))
			return
		}
	}
	// Only a transitive entry: a direct one the pubspec no longer has is a
	// lock out of date.
	if transitive {
		d.found(Integration{Package: "tenjin_plugin (through another package)", Version: version, Source: "pubspec.lock", Through: "another package"})
	}
}

// pubLockTransitive is tenjin_plugin in pubspec.lock as another package's
// dependency.
var pubLockTransitive = regexp.MustCompile(`(?m)^  tenjin_plugin:\n    dependency: transitive\n(?: {4,}.*\n)*? {4}version: "([^"]+)"`)

// CommunityFlutterPackage is how a project's dependency on the community
// tenjin_sdk plugin is recorded.
const CommunityFlutterPackage = "tenjin_sdk (community plugin)"

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
		d.p.Expo = true
		configured := d.expoTargets()
		if len(d.p.Targets) == 0 {
			d.p.Targets = configured
			d.note("This is an Expo project without native folders. The Tenjin SDK is a native module: run `npx expo prebuild` or use a development build before integrating.")
		} else {
			d.note("This is an Expo project. The Tenjin package ships no config plugin, so its native settings must be kept in the prebuilt android/ and ios/ folders.")
			d.notePlatformToAdd(func(m mobilePlatform) ([]string, string) {
				why := ""
				for _, t := range configured {
					if t.Platform == m.platform {
						why = t.Source + " gives " + m.name + " a bundle ID"
					}
				}
				return []string{"npx expo prebuild --platform " + m.folder}, why
			})
		}
	}
	if v, ok := pkg.dependency("react-native-tenjin"); ok {
		d.found(Integration{Package: "react-native-tenjin", Version: v, Source: "package.json"})
	} else {
		d.reactNativeThroughPackage(pkg)
	}
	return true
}

var (
	expoAndroidPackage   = regexp.MustCompile(`\bpackage\s*:\s*["']([\w.]+)["']`)
	expoBundleIdentifier = regexp.MustCompile(`\bbundleIdentifier\s*:\s*["']([\w.-]+)["']`)
)

// expoTargets reads the bundle IDs app.json, or app.config, gives. They are
// the project's targets when there are no native folders.
func (d *detector) expoTargets() []Target {
	var targets []Target
	var app struct {
		Expo struct {
			IOS     struct{ BundleIdentifier string } `json:"ios"`
			Android struct{ Package string }          `json:"android"`
		} `json:"expo"`
	}
	b, err := os.ReadFile(d.path("app.json"))
	if err != nil || json.Unmarshal(b, &app) != nil {
		// app.config.ts or .js is code; its IDs are read where they are
		// written as plain strings.
		for _, name := range []string{"app.config.ts", "app.config.js"} {
			config := stripComments(d.read(name))
			if config == "" {
				continue
			}
			if m := expoAndroidPackage.FindStringSubmatch(config); m != nil {
				targets = append(targets, Target{Platform: Android, BundleID: m[1], Source: name})
			}
			if m := expoBundleIdentifier.FindStringSubmatch(config); m != nil {
				targets = append(targets, Target{Platform: IOS, BundleID: m[1], Source: name})
			}
			return targets
		}
		return nil
	}
	if id := app.Expo.Android.Package; id != "" {
		targets = append(targets, Target{Platform: Android, BundleID: id, Source: "app.json"})
	}
	if id := app.Expo.IOS.BundleIdentifier; id != "" {
		targets = append(targets, Target{Platform: IOS, BundleID: id, Source: "app.json"})
	}
	return targets
}

// reactNativeThroughPackage records the SDK when the app gets
// react-native-tenjin through another package: a local one (file:, link:,
// portal:), whose code is read, or a published one, which the lockfile
// shows.
func (d *detector) reactNativeThroughPackage(pkg packageJSON) {
	for name, spec := range pkg.Dependencies {
		for _, scheme := range []string{"file:", "link:", "portal:"} {
			if !strings.HasPrefix(spec, scheme) {
				continue
			}
			dir := path.Clean(strings.TrimPrefix(spec, scheme))
			var local packageJSON
			if json.Unmarshal([]byte(d.read(path.Join(dir, "package.json"))), &local) != nil {
				continue
			}
			if v, ok := local.dependency("react-native-tenjin"); ok {
				d.found(Integration{Package: "react-native-tenjin (through " + name + ")", Version: v, Source: path.Join(dir, "package.json"), Through: name, Local: true})
				d.p.LibraryCode = append(d.p.LibraryCode, dir)
				return
			}
		}
	}
	for _, lock := range []string{"package-lock.json", "yarn.lock", "pnpm-lock.yaml"} {
		if strings.Contains(d.read(lock), "react-native-tenjin") {
			d.found(Integration{Package: "react-native-tenjin (through another package)", Source: lock, Through: "another package"})
			return
		}
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
	configured := stripComments(d.read(config))
	d.notePlatformToAdd(func(m mobilePlatform) ([]string, string) {
		add := "npx cap add " + m.folder
		platformPkg := "@capacitor/" + m.folder
		if _, ok := pkg.dependency(platformPkg); ok {
			return []string{add}, platformPkg + " is a dependency"
		}
		why := ""
		for _, block := range capacitorPlatformBlock.FindAllStringSubmatch(configured, -1) {
			if block[1] == m.folder {
				why = config + " configures " + m.name
			}
		}
		// The platform's package is released with the core one, at the same
		// version.
		version, ok := pkg.dependency("@capacitor/core")
		if !ok {
			return []string{add}, why
		}
		install := map[string]string{"yarn": "yarn add", "pnpm": "pnpm add", "bun": "bun add"}[d.packageManager()]
		if install == "" {
			install = "npm install"
		}
		spec := platformPkg + "@" + version
		if !plainVersion.MatchString(version) {
			spec = "'" + spec + "'"
		}
		return []string{install + " " + spec, add}, why
	})
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
